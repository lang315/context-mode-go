package mcpserver

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/store"
)

const throttle1 = "\n\n> Throttle: call #1/8 in this window. 2 call(s) before soft cap. Prefer ctx_search(queries: [...]) array form for multi-query workloads — it counts as a single call."

// index stores chunks under label in the harness project.
func (h *harness) index(label string, chunks ...store.Chunk) {
	h.t.Helper()
	if _, err := h.Deps.Store.Index(h.Deps.Cfg.ProjectKey, label, chunks); err != nil {
		h.t.Fatal(err)
	}
}

// fakeClock returns a clock func and a setter for flood-guard tests.
func fakeClock() (func() time.Time, func(time.Duration)) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) }
}

func TestSearchEmptyKnowledgeBase(t *testing.T) {
	h := newHarness(t, nil)
	out, isErr := h.call("ctx_search", `{"queries": ["anything"]}`)
	if !isErr || out != emptyKBMessage || !strings.HasPrefix(out, "Knowledge base is empty — no content has been indexed yet.\n\n") {
		t.Fatalf("got isErr=%v %q", isErr, out)
	}
	// Timeline mode skips the guard (S:2599).
	out, isErr = h.call("ctx_search", `{"queries": ["anything"], "sort": "timeline"}`)
	if isErr || out != "## anything\nNo results found."+throttle1 {
		t.Fatalf("timeline on empty KB: isErr=%v %q", isErr, out)
	}
}

func TestSearchMissingQuery(t *testing.T) {
	h := newHarness(t, nil)
	h.index("notes", store.Chunk{Title: "Alpha", Content: "gizmo text"})
	for _, args := range []string{`{}`, `{"queries": []}`, `{"query": ""}`} {
		if out, isErr := h.call("ctx_search", args); !isErr || out != "Error: provide query or queries." {
			t.Fatalf("%s: got isErr=%v %q", args, isErr, out)
		}
	}
	// Error calls do not consume the flood budget: the next search is call #1.
	if out, _ := h.call("ctx_search", `{"queries": ["gizmo"]}`); !strings.HasSuffix(out, throttle1) {
		t.Fatalf("budget consumed by error calls:\n%s", out)
	}
}

func TestSearchOutputFormat(t *testing.T) {
	h := newHarness(t, nil)
	h.index("notes", store.Chunk{Title: "Alpha", Content: "gizmo text"})
	out, isErr := h.call("ctx_search", `{"queries": ["gizmo", "qqqqqqqq"]}`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	re := regexp.MustCompile(`\A## gizmo\n\n--- \[current-session \| \d{4}-\d\d-\d\d \d\d:\d\d \| notes\] ---\n### Alpha\n\ngizmo text` +
		`\n\n---\n\n## qqqqqqqq\nNo results found.` + regexp.QuoteMeta(throttle1) + `\z`)
	if !re.MatchString(out) {
		t.Fatalf("output:\n%s", out)
	}
	stamp := regexp.MustCompile(`\d{4}-\d\d-\d\d \d\d:\d\d`).FindString(out)
	if at, err := time.Parse("2006-01-02 15:04", stamp); err != nil || time.Since(at) > 5*time.Minute || time.Since(at) < -time.Minute {
		t.Fatalf("timestamp %q is not the UTC index time", stamp)
	}
}

// TS parity (golden t18/t19): the legacy `query` field is stripped by the TS
// zod schema, so it is rejected and does not count against the flood guard.
func TestSearchIgnoresLegacyQueryParam(t *testing.T) {
	h := newHarness(t, nil)
	h.index("notes", store.Chunk{Title: "Alpha", Content: "gizmo text"})
	out, isErr := h.call("ctx_search", `{"query": "gizmo"}`)
	if !isErr || out != "Error: provide query or queries." {
		t.Fatalf("legacy query should be rejected, got isError=%v:\n%s", isErr, out)
	}
	if out, _ := h.call("ctx_search", `{"queries": ["gizmo"]}`); !strings.Contains(out, "call #1/8") {
		t.Fatalf("rejected legacy call must not be counted:\n%s", out)
	}
}

func TestSearchLimitCapping(t *testing.T) {
	h := newHarness(t, nil)
	for i := range 5 {
		h.index(fmt.Sprintf("doc-%d", i), store.Chunk{Title: fmt.Sprintf("Doc %d", i), Content: fmt.Sprintf("widget number %d", i)})
	}
	out, _ := h.call("ctx_search", `{"queries": ["widget"], "limit": "5"}`)
	if n := strings.Count(out, "--- [current-session"); n != 2 {
		t.Fatalf("limit 5 should be capped to 2 results, got %d:\n%s", n, out)
	}
	out, _ = h.call("ctx_search", `{"queries": ["widget"], "limit": 1}`)
	if n := strings.Count(out, "--- [current-session"); n != 1 {
		t.Fatalf("limit 1: got %d results", n)
	}
	if out, isErr := h.call("ctx_search", `{"queries": ["widget"], "limit": "lots"}`); !isErr || !strings.HasPrefix(out, "Invalid arguments: limit:") {
		t.Fatalf("bad limit: isErr=%v %q", isErr, out)
	}
}

func TestSearchFloodGuard(t *testing.T) {
	now, advance := fakeClock()
	h := newHarness(t, now)
	for i := range 3 {
		h.index(fmt.Sprintf("doc-%d", i), store.Chunk{Title: fmt.Sprintf("Doc %d", i), Content: fmt.Sprintf("widget number %d", i)})
	}
	results := func(out string) int { return strings.Count(out, "--- [current-session") }

	for call := 1; call <= 2; call++ {
		out, _ := h.call("ctx_search", `{"queries": ["widget"]}`)
		want := fmt.Sprintf("\n\n> Throttle: call #%d/8 in this window. %d call(s) before soft cap.", call, 3-call)
		if results(out) != 2 || !strings.Contains(out, want) {
			t.Fatalf("call %d:\n%s", call, out)
		}
	}
	// Call 3 warns but still returns 2 per query; call 4 is soft-capped to 1.
	out, _ := h.call("ctx_search", `{"queries": ["widget"]}`)
	if results(out) != 2 || !strings.HasSuffix(out, "\n\n⚠ search call #3/8 in this window. Results limited to 2/query. 5 call(s) remaining before block. "+
		`Batch queries: ctx_search(queries: ["q1","q2","q3"]) or use ctx_batch_execute.`) {
		t.Fatalf("call 3:\n%s", out)
	}
	for call := 4; call <= 8; call++ {
		out, isErr := h.call("ctx_search", `{"queries": ["widget"]}`)
		want := fmt.Sprintf("⚠ search call #%d/8 in this window. Results limited to 1/query. %d call(s) remaining before block.", call, 8-call)
		if isErr || results(out) != 1 || !strings.Contains(out, want) {
			t.Fatalf("call %d (isErr=%v):\n%s", call, isErr, out)
		}
	}
	advance(12400 * time.Millisecond)
	out, isErr := h.call("ctx_search", `{"queries": ["widget"]}`)
	if !isErr || out != "BLOCKED: 9 search calls in 12s. You're flooding context. STOP making individual search calls. Use ctx_batch_execute(commands, queries) for your next research step." {
		t.Fatalf("call 9: isErr=%v %q", isErr, out)
	}
	// Still inside the window at exactly +60s from its start.
	advance(60*time.Second - 12400*time.Millisecond)
	if out, isErr := h.call("ctx_search", `{"queries": ["widget"]}`); !isErr || !strings.HasPrefix(out, "BLOCKED: 10 search calls in 60s.") {
		t.Fatalf("at +60s: %q", out)
	}
	advance(time.Second)
	out, isErr = h.call("ctx_search", `{"queries": ["widget"]}`)
	if isErr || results(out) != 2 || !strings.HasSuffix(out, throttle1) {
		t.Fatalf("after window reset (isErr=%v):\n%s", isErr, out)
	}
}

func TestSearchSourceAndContentTypeFilters(t *testing.T) {
	h := newHarness(t, nil)
	h.index("alpha-docs", store.Chunk{Title: "A prose", Content: "shared alpha words"}, store.Chunk{Title: "A code", Content: "```go\nshared()\n```", HasCode: true})
	h.index("beta-docs", store.Chunk{Title: "B prose", Content: "shared beta words"})

	out, _ := h.call("ctx_search", `{"queries": ["shared"], "source": "lpha"}`)
	if strings.Contains(out, "beta-docs") || strings.Count(out, "| alpha-docs] ---") != 2 {
		t.Fatalf("source LIKE filter:\n%s", out)
	}
	out, _ = h.call("ctx_search", `{"queries": ["shared"], "contentType": "code"}`)
	if strings.Count(out, "--- [") != 1 || !strings.Contains(out, "### A code\n") {
		t.Fatalf("contentType filter:\n%s", out)
	}
	if out, isErr := h.call("ctx_search", `{"queries": ["shared"], "contentType": "binary"}`); !isErr || out != `Invalid arguments: contentType: expected "code" or "prose"` {
		t.Fatalf("bad contentType: %q", out)
	}
}

func TestSearchTimelineSort(t *testing.T) {
	h := newHarness(t, nil)
	// Older source is the weaker match, so relevance and timeline disagree.
	h.index("older", store.Chunk{Title: "Old", Content: "timeword " + strings.Repeat("padding filler text ", 40)})
	time.Sleep(1100 * time.Millisecond) // indexed_at has one-second resolution
	h.index("newer", store.Chunk{Title: "timeword", Content: "timeword timeword"})

	order := func(out string) string {
		return strings.Join(regexp.MustCompile(`\| (older|newer)\] ---`).FindAllString(out, -1), ",")
	}
	if got := order(h.call2("ctx_search", `{"queries": ["timeword"]}`)); got != "| newer] ---,| older] ---" {
		t.Fatalf("relevance order: %s", got)
	}
	if got := order(h.call2("ctx_search", `{"queries": ["timeword"], "sort": "timeline"}`)); got != "| older] ---,| newer] ---" {
		t.Fatalf("timeline order: %s", got)
	}
}

// call2 is call without the error flag.
func (h *harness) call2(name, args string) string {
	out, _ := h.call(name, args)
	return out
}

func TestSearchOutputCap(t *testing.T) {
	h := newHarness(t, nil)
	for i := range 4 {
		h.index(fmt.Sprintf("big-%d", i), store.Chunk{Title: fmt.Sprintf("Big %d", i), Content: strings.Repeat("capterm lorem ipsum ", 100)})
	}
	qs := make([]string, 20)
	for i := range qs {
		qs[i] = `"capterm"`
	}
	out, isErr := h.call("ctx_search", `{"queries": [`+strings.Join(qs, ",")+`]}`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	sections := strings.Split(strings.Split(out, "\n\n> Throttle:")[0], "\n\n---\n\n")
	answered, capped := 0, 0
	for _, s := range sections {
		switch {
		case s == "## capterm\n(output cap reached)\n":
			capped++
		case strings.HasPrefix(s, "## capterm\n\n--- [current-session"):
			if capped > 0 {
				t.Fatal("a result section followed a capped one")
			}
			answered++
		default:
			t.Fatalf("unexpected section %q", s)
		}
	}
	// Each answered section is two ~1.5K snippets; the cap is checked before a query runs.
	if answered+capped != 20 || capped == 0 || answered < 12 {
		t.Fatalf("answered=%d capped=%d", answered, capped)
	}
}
