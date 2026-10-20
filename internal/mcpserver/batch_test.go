package mcpserver

import (
	"regexp"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// sizeRe stands in for a "(12.3KB)"-style size in a pinned output.
const sizeRe = `\d+\.\dKB`

// pinOutput turns want into an anchored regexp: literal text, except that
// every "<KB>" placeholder matches a size.
func pinOutput(t *testing.T, got, want string) {
	t.Helper()
	parts := strings.Split(want, "<KB>")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	re := regexp.MustCompile(`\A` + strings.Join(parts, sizeRe) + `\z`)
	if !re.MatchString(got) {
		t.Fatalf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBatchOutputStructure(t *testing.T) {
	h := newHarness(t, nil)
	out, isErr := h.call("ctx_batch_execute", `{
		"commands": [
			{"label": "alpha", "command": "echo   alphaword"},
			{"label": "beta", "command": "echo betaword; echo betaerr >&2"}
		],
		"queries": ["alphaword", "betaerr"]
	}`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	pinOutput(t, out, "Executed 2 commands (15 lines, <KB>). Indexed 2 sections. Searched 2 queries.\n"+
		"\n"+
		"## Commands\n"+
		"\n"+
		"- alpha: `echo alphaword`\n"+
		"- beta: `echo betaword; echo betaerr >&2`\n"+
		"\n"+
		"## Indexed Sections\n"+
		"\n"+
		"- alpha (<KB>)\n"+
		"- beta (<KB>)\n"+
		"\n"+
		"## alphaword\n"+
		"\n"+
		"### alpha\n"+
		"# alpha\n\n$ echo alphaword\n\nalphaword\n"+
		"\n"+
		"## betaerr\n"+
		"\n"+
		"### beta\n"+
		"# beta\n\n$ echo betaword; echo betaerr >&2\n\nbetaword\nbetaerr\n"+
		"\n"+
		"\n> **Tip:** Results are scoped to this batch only. To search across all indexed sources, use `ctx_search(queries: [...])` or call ctx_batch_execute with `query_scope: \"global\"`.\n")

	src, err := h.Deps.Store.SourceByLabel(h.Deps.Cfg.ProjectKey, "batch:alpha,beta")
	if err != nil || src == nil || src.ChunkCount != 2 {
		t.Fatalf("batch source not indexed under batch:alpha,beta: %+v %v", src, err)
	}
}

func TestBatchSearchableTermsLine(t *testing.T) {
	h := newHarness(t, nil)
	// DistinctiveTerms needs >= 3 chunks; "sharedterm" appears in 2 of 3.
	out, _ := h.call("ctx_batch_execute", `{
		"commands": [
			{"label": "one", "command": "echo sharedterm"},
			{"label": "two", "command": "echo sharedterm"},
			{"label": "three", "command": "echo other"}
		],
		"queries": ["sharedterm"]
	}`)
	if !regexp.MustCompile(`\n\nSearchable terms for follow-up: [^\n]*sharedterm[^\n]*\z`).MatchString(out) {
		t.Fatalf("missing terms line:\n%s", out)
	}
}

func TestBatchBareStringCommandsAndJSONStringQueries(t *testing.T) {
	h := newHarness(t, nil)
	out, isErr := h.call("ctx_batch_execute", `{"commands": ["echo barehello", "echo baretwo"], "queries": "[\"barehello\"]"}`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	for _, want := range []string{"- cmd_1: `echo barehello`\n- cmd_2: `echo baretwo`\n", "## barehello\n\n### cmd_1\n", "Searched 1 queries."} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// A whole JSON-string commands array and a bare-string query also work.
	out, isErr = h.call("ctx_batch_execute", `{"commands": "[{\"label\":\"j\",\"command\":\"echo jsonform\"}]", "queries": "jsonform"}`)
	if isErr || !strings.Contains(out, "## jsonform\n\n### j\n") {
		t.Fatalf("JSON-string commands failed (isErr=%v):\n%s", isErr, out)
	}
}

func TestBatchSerialSharedBudget(t *testing.T) {
	h := newHarness(t, nil)
	out, isErr := h.call("ctx_batch_execute", `{
		"commands": [
			{"label": "slow", "command": "echo started; sleep 5"},
			{"label": "later", "command": "echo never"}
		],
		"queries": ["started"], "timeout": 300
	}`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "- later (") || strings.Contains(out, "never\n") {
		t.Fatalf("later command should be listed but not run:\n%s", out)
	}
	sec := sectionContent(t, h, "batch:slow,later", "later")
	if sec != "# later\n\n(skipped — batch timeout exceeded)\n" && sec != "# later\n\n(skipped — batch timeout exceeded)" {
		t.Fatalf("later section = %q", sec)
	}

	// A spent budget skips every command before it runs (S:1580).
	out, _ = h.call("ctx_batch_execute", `{"commands": ["echo a", "echo b"], "queries": ["skipped"], "timeout": "0"}`)
	if strings.Count(out, "(skipped — batch timeout exceeded)") != 2 || !strings.Contains(out, "Executed 2 commands (8 lines,") {
		t.Fatalf("zero budget should skip both:\n%s", out)
	}
}

func TestBatchParallelPerCommandTimeout(t *testing.T) {
	h := newHarness(t, nil)
	out, isErr := h.call("ctx_batch_execute", `{
		"commands": [
			{"label": "slow", "command": "echo partialout; sleep 5"},
			{"label": "quick", "command": "echo quickout"},
			{"label": "fast", "command": "echo fastout"}
		],
		"queries": ["timed out"], "timeout": 300, "concurrency": 3
	}`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.Contains(out, "skipped") {
		t.Fatalf("parallel mode must not skip:\n%s", out)
	}
	if got := sectionContent(t, h, "batch:slow,quick,fast", "slow"); !strings.HasSuffix(got, "partialout\n\n(timed out after 300ms)") {
		t.Fatalf("slow section = %q", got)
	}
	if got := sectionContent(t, h, "batch:slow,quick,fast", "quick"); !strings.Contains(got, "quickout") {
		t.Fatalf("quick section = %q", got)
	}
}

// sectionContent returns the indexed chunk titled title in source label.
func sectionContent(t *testing.T, h *harness, label, title string) string {
	t.Helper()
	res, err := h.Deps.Store.SearchWithFallback(title, store.SearchOpts{Project: h.Deps.Cfg.ProjectKey, Source: label, SourceExact: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Title == title {
			return r.Content
		}
	}
	t.Fatalf("no chunk %q in %s (got %d results)", title, label, len(res))
	return ""
}

func TestBatchDenyPattern(t *testing.T) {
	h := newHarness(t, nil)
	h.writeSettings("Bash(sudo *)")
	out, isErr := h.call("ctx_batch_execute", `{"commands": ["echo fine", "echo ok && sudo ls"], "queries": ["x"]}`)
	if !isErr || out != "Command blocked by security policy: matches deny pattern Bash(sudo *)" {
		t.Fatalf("got isErr=%v %q", isErr, out)
	}
	if n, _ := h.Deps.Store.ChunkCount(h.Deps.Cfg.ProjectKey); n != 0 {
		t.Fatalf("nothing should have run or been indexed, chunks=%d", n)
	}
}

func TestBatchQueryScope(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.Deps.Store.Index(h.Deps.Cfg.ProjectKey, "notes", []store.Chunk{{Title: "Zebra notes", Content: "zebrafish regenerate fins"}}); err != nil {
		t.Fatal(err)
	}
	out, _ := h.call("ctx_batch_execute", `{"commands": ["echo unrelated"], "queries": ["zebrafish"]}`)
	if !strings.Contains(out, "## zebrafish\n\nNo matching sections found.\n\n\n> **Tip:**") {
		t.Fatalf("batch scope leaked other sources:\n%s", out)
	}
	out, _ = h.call("ctx_batch_execute", `{"commands": ["echo unrelated"], "queries": ["zebrafish"], "query_scope": "global"}`)
	want := "## zebrafish\n\n### Zebra notes\nzebrafish regenerate fins\n\n\n> **Scope:** Queries searched the entire persistent index (query_scope: \"global\").\n"
	if !strings.Contains(out, want) {
		t.Fatalf("global scope missing other source:\n%s", out)
	}
}

func TestBatchQueryOutputCap(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.Deps.Store.Index(h.Deps.Cfg.ProjectKey, "big", []store.Chunk{{Title: "Big", Content: strings.Repeat("capword filler ", 20)}}); err != nil {
		t.Fatal(err)
	}
	got, err := formatBatchQueryResults(h.Deps.Store, h.Deps.Cfg.ProjectKey, []string{"capword", "second"}, "big", 100, "batch")
	if err != nil {
		t.Fatal(err)
	}
	want := "## second\n(output cap reached — use ctx_search(queries: [\"second\"]) for details)\n"
	if len(got) != 7 || got[0] != "## capword" || got[2] != "### Big" || got[5] != want {
		t.Fatalf("got %q", got)
	}
}

func TestBatchArgErrors(t *testing.T) {
	h := newHarness(t, nil)
	for args, want := range map[string]string{
		`{"commands": ["echo a"], "queries": ["a"], "concurrency": 9}`:            "Invalid arguments: concurrency: expected an integer from 1 to 8",
		`{"commands": ["echo a"]}`:                                                "Invalid arguments: queries: expected a non-empty array",
		`{"commands": [], "queries": ["a"]}`:                                      "Invalid arguments: commands: expected a non-empty array",
		`{"commands": ["echo a"], "queries": ["a"], "query_scope": "everything"}`: `Invalid arguments: query_scope: expected "batch" or "global"`,
	} {
		if out, isErr := h.call("ctx_batch_execute", args); !isErr || out != want {
			t.Errorf("%s: got isErr=%v %q", args, isErr, out)
		}
	}
}

func TestTruncateCommandForEcho(t *testing.T) {
	if got := truncateCommandForEcho("  a \n\t b  "); got != "a b" {
		t.Fatalf("collapse: %q", got)
	}
	long := strings.Repeat("x", 501)
	if got := truncateCommandForEcho(long); got != strings.Repeat("x", 500)+"…" {
		t.Fatalf("cut: len %d", len(got))
	}
	if got := jsPrefix("a😀b", 2); got != "a" {
		t.Fatalf("jsPrefix must not split a surrogate pair: %q", got)
	}
	if jsLen("a😀") != 3 {
		t.Fatal("jsLen counts UTF-16 units")
	}
}
