package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/store"
)

// recordStats turns stats on for h, as serve does, and returns a reader of
// one tool's row for this server.
func (h *harness) recordStats() func(tool string) store.ToolStat {
	h.t.Helper()
	h.Deps.Stats = &StatsRecorder{ServerID: "srv-test", CCPID: 0, StartedAt: "2026-10-21 08:00:00"}
	return func(tool string) store.ToolStat {
		h.t.Helper()
		if !h.Deps.Stats.Flush(5 * time.Second) { // records are written by a background worker
			h.t.Fatal("stats queue did not drain")
		}
		snap, err := h.Deps.Store.Stats(store.StatsQuery{ServerID: "srv-test", Project: h.Deps.Cfg.ProjectKey})
		if err != nil {
			h.t.Fatal(err)
		}
		for _, ts := range snap.Tools {
			if ts.Tool == tool {
				return ts
			}
		}
		return store.ToolStat{Tool: tool}
	}
}

// args is a JSON object literal for h.call.
func args(kv ...any) string {
	m := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestStatsOffRecordsNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.call("ctx_execute", args("language", "shell", "code", "echo hi"))
	snap, err := h.Deps.Store.Stats(store.StatsQuery{Project: h.Deps.Cfg.ProjectKey})
	if err != nil || len(snap.Tools) != 0 || snap.LifetimeSince != "" {
		t.Fatalf("rows recorded with Stats nil: %+v %v", snap, err)
	}
}

// ctx_execute: raw is the run's stdout + stderr bytes (stats spec §1.3).
func TestStatsExecuteKept(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()

	small, _ := h.call("ctx_execute", args("language", "shell", "code", "printf abc; printf de >&2"))
	// 5 raw bytes, more returned (the code echo): nothing kept.
	if got, want := row("ctx_execute"), (store.ToolStat{Tool: "ctx_execute", Calls: 1, BytesReturned: int64(len(small))}); got != want {
		t.Fatalf("small run: %+v, want %+v", got, want)
	}

	// Over 5 KB with an intent: indexed, and only matching sections come back.
	big, _ := h.call("ctx_execute", args("language", "shell", "code", `head -c 100000 /dev/zero | tr '\0' a`, "intent", "letters"))
	got := row("ctx_execute")
	want := store.ToolStat{Tool: "ctx_execute", Calls: 2, BytesReturned: int64(len(small) + len(big)), BytesKept: int64(100000 - len(big))}
	if got != want || len(big) > 10000 {
		t.Fatalf("indexed run: %+v, want %+v (returned %d bytes)", got, want, len(big))
	}

	// An argument error is a call that processed nothing.
	bad, isErr := h.call("ctx_execute", args("language", "cobol", "code", "x"))
	want.Calls, want.BytesReturned = 3, want.BytesReturned+int64(len(bad))
	if got := row("ctx_execute"); !isErr || got != want {
		t.Fatalf("arg error: %+v, want %+v", got, want)
	}
}

// ctx_execute_file: raw is the file size + stdout + stderr bytes.
func TestStatsExecuteFileKept(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	h.writeProjectFile("data.txt", strings.Repeat("x", 3000))
	code := `printf %s "${#FILE_CONTENT}"`
	text, isErr := h.call("ctx_execute_file", args("path", "data.txt", "language", "shell", "code", code))
	stdout, ok := strings.CutPrefix(text, buildExecuteEcho("shell", code, "data.txt"))
	if isErr || !ok || stdout != "3000" {
		t.Fatalf("run: %v %q", isErr, text)
	}
	want := store.ToolStat{Tool: "ctx_execute_file", Calls: 1, BytesReturned: int64(len(text)), BytesKept: int64(3000 + len(stdout) - len(text))}
	if got := row("ctx_execute_file"); got != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
}

// ctx_batch_execute: raw is the sum of every command's stdout + stderr
// bytes, in serial and in parallel mode.
func TestStatsBatchKept(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		h := newHarness(t, nil)
		row := h.recordStats()
		text, isErr := h.call("ctx_batch_execute", args(
			"commands", []map[string]string{
				{"label": "letters", "command": `head -c 50000 /dev/zero | tr '\0' b`},
				{"label": "errs", "command": "printf xy >&2"},
			},
			"queries", []string{"letters"}, "concurrency", concurrency))
		want := store.ToolStat{Tool: "ctx_batch_execute", Calls: 1, BytesReturned: int64(len(text)), BytesKept: int64(50000 + 2 - len(text))}
		if got := row("ctx_batch_execute"); isErr || got != want || len(text) > 20000 {
			t.Fatalf("concurrency %d: %+v, want %+v (isErr %v, returned %d)", concurrency, got, want, isErr, len(text))
		}
	}
}

// ctx_fetch_and_index: raw is the fetched body bytes, or on a cache hit the
// cached source's content bytes.
func TestStatsFetchKept(t *testing.T) {
	body := strings.Repeat("zebrafish line of text\n", 2000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	h := newHarness(t, nil)
	row := h.recordStats()

	first, isErr := h.call("ctx_fetch_and_index", args("url", srv.URL+"/a", "source", "big"))
	raw := int64(len(strings.TrimSpace(body)))
	want := store.ToolStat{Tool: "ctx_fetch_and_index", Calls: 1, BytesReturned: int64(len(first)), BytesKept: raw - int64(len(first))}
	if got := row("ctx_fetch_and_index"); isErr || got != want {
		t.Fatalf("fetch: %+v, want %+v (isErr %v)", got, want, isErr)
	}

	cached, isErr := h.call("ctx_fetch_and_index", args("url", srv.URL+"/a", "source", "big"))
	src, err := h.Deps.Store.SourceByLabel(h.Deps.Cfg.ProjectKey, fetchCacheKey("big", true, srv.URL+"/a"))
	if err != nil || src == nil {
		t.Fatal(src, err)
	}
	content, err := h.Deps.Store.SourceBytes(src.ID)
	if err != nil || content < 40000 {
		t.Fatalf("SourceBytes = %d, %v", content, err)
	}
	want.Calls, want.BytesReturned = 2, want.BytesReturned+int64(len(cached))
	want.BytesKept += content - int64(len(cached))
	if got := row("ctx_fetch_and_index"); isErr || !strings.Contains(cached, "cached") || got != want {
		t.Fatalf("cache hit: %+v, want %+v\n%s", got, want, cached)
	}
}

// ctx_search has no raw size: it only returns bytes. ctx_purge is not recorded.
func TestStatsSearchAndPurgeReturnOnly(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	h.index("notes", store.Chunk{Title: "Alpha", Content: strings.Repeat("gizmo text ", 500)})
	found, _ := h.call("ctx_search", args("queries", []string{"gizmo"}))
	if got, want := row("ctx_search"), (store.ToolStat{Tool: "ctx_search", Calls: 1, BytesReturned: int64(len(found))}); got != want {
		t.Fatalf("search: %+v, want %+v", got, want)
	}
	// ctx_purge is not recorded: a purge must not leave a fresh row behind.
	h.call("ctx_purge", `{}`)
	if got, want := row("ctx_purge"), (store.ToolStat{Tool: "ctx_purge"}); got != want {
		t.Fatalf("purge: %+v, want no row", got)
	}
}
