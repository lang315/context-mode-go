package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

const shellEcho = "```shell\n%s\n```\n\n"

// writeProjectFile writes body to <project>/name.
func (h *harness) writeProjectFile(name, body string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.Project, name), []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// checkListedTool asserts tools/list carries name with the given title,
// description, hand-written schema and the TS #846 annotations.
func checkListedTool(t *testing.T, name, title, desc, schema string) {
	t.Helper()
	h := newHarness(t, nil)
	res, err := h.Session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal([]byte(schema), &want); err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != name {
			continue
		}
		if !reflect.DeepEqual(tool.InputSchema, want) {
			t.Errorf("%s schema = %v", name, tool.InputSchema)
		}
		if tool.Title != title || tool.Description != desc {
			t.Errorf("%s title/description mismatch: %q", name, tool.Title)
		}
		if a := tool.Annotations; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s annotations = %+v", name, a)
		}
		return
	}
	t.Fatalf("%s not listed", name)
}

func TestToolsListIncludesExecute(t *testing.T) {
	checkListedTool(t, "ctx_execute", "Run code in a sandbox (executes the supplied code)", executeDesc, executeSchema)
	if !strings.HasPrefix(executeDesc, "Run code in a sandboxed subprocess. Languages: javascript, typescript, python,") {
		t.Errorf("executeDesc starts %q", executeDesc[:60])
	}
}

func TestExecuteSmallOutputWithEcho(t *testing.T) {
	h := newHarness(t, nil)
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"echo hello","timeout":"2000"}`)
	if isErr || text != fmt.Sprintf(shellEcho, "echo hello")+"hello\n" {
		t.Errorf("got %v %q", isErr, text)
	}
	text, isErr = h.call("ctx_execute", `{"language":"shell","code":"true"}`)
	if isErr || text != fmt.Sprintf(shellEcho, "true")+"(no output)" {
		t.Errorf("empty stdout: got %v %q", isErr, text)
	}
}

func TestExecuteSoftFailShellExit1(t *testing.T) {
	h := newHarness(t, nil)
	code := "echo found; exit 1"
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"`+code+`"}`)
	if isErr || text != fmt.Sprintf(shellEcho, code)+"found\n" {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestExecuteNonZeroExit(t *testing.T) {
	h := newHarness(t, nil)
	code := "echo out; echo err >&2; exit 3"
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"`+code+`"}`)
	want := fmt.Sprintf(shellEcho, code) + "Exit code: 3\n\nstdout:\nout\n\n\nstderr:\nerr\n"
	if !isErr || text != want {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestExecuteTimeoutPartialOutput(t *testing.T) {
	h := newHarness(t, nil)
	code := "echo hi; sleep 10"
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"`+code+`","timeout":300}`)
	want := fmt.Sprintf(shellEcho, code) + "hi\n\n_(timed out after 300ms — partial output shown above)_"
	if isErr || text != want {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestExecuteTimeoutNoOutput(t *testing.T) {
	h := newHarness(t, nil)
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"sleep 10","timeout":300}`)
	want := fmt.Sprintf(shellEcho, "sleep 10") + "Execution timed out after 300ms\n\nstderr:\n"
	if !isErr || text != want {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestExecuteBackground(t *testing.T) {
	h := newHarness(t, nil)
	code := "echo started; sleep 30"
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"`+code+`","timeout":300,"background":"true"}`)
	want := fmt.Sprintf(shellEcho, code) + "started\n\n_(process backgrounded after 300ms — still running)_"
	if isErr || text != want {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestExecuteIntentSearch(t *testing.T) {
	h := newHarness(t, nil)
	out := intentFixture()
	h.writeProjectFile("build.log", out)
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"cat build.log","intent":"disk quota"}`)
	echo := fmt.Sprintf(shellEcho, "cat build.log")
	if isErr || !strings.HasPrefix(text, echo) {
		t.Fatalf("got %v %q", isErr, text)
	}
	lines := strings.Split(strings.TrimPrefix(text, echo), "\n")
	if !regexp.MustCompile(`^Indexed \d+ sections from "execute:shell" into knowledge base\.$`).MatchString(lines[0]) {
		t.Errorf("line 0 = %q", lines[0])
	}
	if !regexp.MustCompile(`^[1-5] sections matched "disk quota" \(201 lines, ` + regexp.QuoteMeta(KB(len(out))) + `\):$`).MatchString(lines[1]) {
		t.Errorf("line 1 = %q", lines[1])
	}
	if !strings.Contains(text, "\n\nSearchable terms: ") {
		t.Errorf("missing Searchable terms:\n%s", text)
	}
}

// bigMarkdown is > 100KB of Markdown with one rare word per section.
func bigMarkdown() string {
	var b strings.Builder
	for i := 0; b.Len() <= largeOutputThreshold+1000; i++ {
		fmt.Fprintf(&b, "## Section %d\n\n%s marker%dword\n\n", i, strings.Repeat("filler text for the section body. ", 30), i)
	}
	return b.String()
}

func TestExecuteLargeOutputIndexed(t *testing.T) {
	h := newHarness(t, nil)
	h.writeProjectFile("big.md", bigMarkdown())
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"cat big.md"}`)
	want := regexp.MustCompile(`^` + regexp.QuoteMeta(fmt.Sprintf(shellEcho, "cat big.md")) +
		`Indexed \d+ sections \(0 with code\) from: execute:shell\nUse ctx_search\(queries: \["\.\.\."\]\) to query this content\. Use source: "execute:shell" to scope results\.$`)
	if isErr || !want.MatchString(text) {
		t.Fatalf("got %v %q", isErr, text)
	}
	hits, err := h.Deps.Store.SearchWithFallback("marker7word", store.SearchOpts{
		Project: h.Deps.Cfg.ProjectKey, Source: "execute:shell", SourceExact: true, Limit: 3, Mode: "relevance",
	})
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Content, "marker7word") {
		t.Fatalf("store search: %v %+v", err, hits)
	}
}

func TestExecuteLargeErrorOutputUsesIntentSearch(t *testing.T) {
	h := newHarness(t, nil)
	h.writeProjectFile("big.md", bigMarkdown())
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"cat big.md; exit 2"}`)
	if !isErr || !regexp.MustCompile(`\n\nIndexed \d+ sections from "execute:shell:error" into knowledge base\.\n`).MatchString(text) {
		t.Fatalf("got %v %q", isErr, text[:min(len(text), 300)])
	}
}

func TestExecuteDenyPolicy(t *testing.T) {
	h := newHarness(t, nil)
	h.writeSettings("Bash(sudo *)")
	text, isErr := h.call("ctx_execute", `{"language":"shell","code":"echo ok && sudo ls"}`)
	if !isErr || text != "Command blocked by security policy: matches deny pattern Bash(sudo *)" {
		t.Errorf("shell: got %v %q", isErr, text)
	}
	text, isErr = h.call("ctx_execute", `{"language":"python","code":"import os\nos.system(\"sudo reboot\")"}`)
	if !isErr || text != `Command blocked by security policy: embedded shell command "sudo reboot" matches deny pattern Bash(sudo *)` {
		t.Errorf("python: got %v %q", isErr, text)
	}
}

func TestExecuteArgErrors(t *testing.T) {
	h := newHarness(t, nil)
	for _, args := range []string{
		`{"language":"cobol","code":"x"}`,
		`{"code":"echo hi"}`,
		`{"language":"shell"}`,
		`{"language":"shell","code":"echo hi","background":"yes"}`,
		`{"language":"shell","code":"echo hi","timeout":"soon"}`,
	} {
		if text, isErr := h.call("ctx_execute", args); !isErr || !strings.HasPrefix(text, "Invalid arguments: ") {
			t.Errorf("%s: got %v %q", args, isErr, text)
		}
	}
}
