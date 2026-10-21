package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// ../../testdata/golden/hooks-capture.json holds TS outputs: the pure
// extractors of hooks/session-extract.bundle.mjs (TS repo at a5e6dd31), run
// by tools/golden-gen/gen-hooks.mjs on the research seed's 27 tool cases and
// 11 prompts, then on the cases added for this package. The expected cmgo
// output is the TS output minus the types in dropped, except for the cases in
// fixed. Both lists are documented in testdata/golden/HOOKS-KNOWN-DIFFS.md.

// dropped lists the event types spec §3.4 does not port. error_tool moved
// to PostToolUseFailure; data is the UserPromptSubmit extractData event.
var dropped = map[string]bool{
	"error_tool": true, "constraint_discovered": true, "bash_outcome": true, "file_read_metadata": true,
	"agent_finding": true, "external_ref": true, "mcp": true, "mcp_tool_call": true, "webfetch_metadata": true,
	"worktree_exit": true, "agent_usage": true, "error_resolved": true, "retry_detected": true, "data": true,
}

// fixed lists the cases a spec §3.4 fix changes, with the new expected
// events. The test fails if a fix stops making a difference.
var fixed = map[string][]store.Event{
	"git -C checkout (TS bug)": {ev("cwd", "cwd", 2, "/x"), ev("git", "git", 2, "branch")},
	"Read CLAUDE.md CC object response": {
		ev("rule", "rule", 1, "/p/CLAUDE.md"),
		ev("rule_content", "rule", 1, "# rules\n"),
		ev("file_read", "file", 1, "/p/CLAUDE.md"),
	},
}

type tsEvent struct {
	Type         string `json:"type"`
	Category     string `json:"category"`
	Data         string `json:"data"`
	Priority     int    `json:"priority"`
	BytesAvoided int64  `json:"bytes_avoided"`
}

type goldenFile struct {
	TSCommit  string `json:"tsCommit"`
	ToolCases []struct {
		Name   string          `json:"name"`
		Input  json.RawMessage `json:"input"`
		Output []tsEvent       `json:"output"`
	} `json:"toolCases"`
	UserCases []struct {
		Prompt string    `json:"prompt"`
		Events []tsEvent `json:"events"`
	} `json:"userCases"`
}

func kept(ts []tsEvent) []store.Event {
	var out []store.Event
	for _, e := range ts {
		if !dropped[e.Type] {
			out = append(out, store.Event{Type: e.Type, Category: e.Category, Priority: e.Priority, Data: e.Data, BytesAvoided: e.BytesAvoided})
		}
	}
	return out
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "hooks-capture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" {
		t.Fatalf("hooks-capture.json: commit %q", g.TSCommit)
	}
	return g
}

func TestGoldenToolCases(t *testing.T) {
	g := loadGolden(t)
	seen := map[string]bool{}
	for _, c := range g.ToolCases {
		in, err := hookio.ParseObject(c.Input)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		name, _ := in.String("tool_name")
		toolInput, _ := in.Object("tool_input")
		resp, _ := in.Get("tool_response") // nil when absent, as on the hook path
		got := PostToolUse(name, toolInput, resp, "/home/test")
		want := kept(c.Output)
		if fix, ok := fixed[c.Name]; ok {
			seen[c.Name] = true
			if reflect.DeepEqual(want, fix) {
				t.Errorf("%s: listed as fixed, but TS already gives %+v", c.Name, want)
			}
			want = fix
		}
		checkEvents(t, c.Name, got, want)
	}
	for name := range fixed {
		if !seen[name] {
			t.Errorf("fixed case %q is missing from hooks-capture.json", name)
		}
	}
}

func TestGoldenUserCases(t *testing.T) {
	for _, c := range loadGolden(t).UserCases {
		want := append([]store.Event{ev("user_prompt", "user-prompt", 1, c.Prompt)}, kept(c.Events)...)
		checkEvents(t, c.Prompt, UserPrompt(c.Prompt), want)
	}
}
