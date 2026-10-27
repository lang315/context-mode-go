package mcpserver

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lang315/context-mode-go/internal/store"
)

// holdDBLock keeps BEGIN IMMEDIATE on the harness DB from a second connection.
func (h *harness) holdDBLock() (release func()) {
	h.t.Helper()
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: h.Deps.Cfg.DBPath}).String()+"?_pragma=busy_timeout(100)")
	if err != nil {
		h.t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		h.t.Fatal(err)
	}
	released := false
	release = func() {
		if !released {
			released = true
			conn.ExecContext(context.Background(), "ROLLBACK")
			conn.Close()
			db.Close()
		}
	}
	h.t.Cleanup(release)
	return release
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// within fails the test when f takes longer than d; it returns after f does
// or after d, whichever is first.
func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s took longer than %v", what, d)
	}
}

// A tool call must never wait on the stats write: another session holding the
// DB's write lock would otherwise stall it for the 30 s busy timeout.
func TestStatsRecordNeverWaitsOnTheDB(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	release := h.holdDBLock()
	within(t, time.Second, "Record with the DB write-locked", func() {
		h.Deps.Stats.Record(h.Deps, "ctx_execute", 100, textResult("ten bytes!"))
	})
	release()
	if got := row("ctx_execute"); got.Calls != 1 || got.BytesReturned != 10 || got.BytesKept != 90 {
		t.Fatalf("after the lock is released: %+v, want the call written", got)
	}
}

// kept is capped per call at what one Bash result can carry: a ctx_execute
// that downloaded 750 MB kept at most that much out of the context, not 750 MB.
func TestStatsKeptCappedPerCall(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	ten := textResult("ten bytes!")
	cases := []struct {
		tool string
		raw  int64
		kept int64
	}{
		{"ctx_execute", 750_000_000, 30000},
		{"ctx_execute_file", 10 + 29999, 29999},
		{"ctx_batch_execute", 10 + 30000, 30000},
		{"ctx_fetch_and_index", 10 + 30001, 30000},
		{"ctx_search", 4, 0},
	}
	for _, c := range cases {
		h.Deps.Stats.RecordExec(h.Deps, c.tool, c.raw, ten, false)
	}
	for _, c := range cases {
		if got := row(c.tool); got.Calls != 1 || got.BytesReturned != 10 || got.BytesKept != c.kept {
			t.Errorf("%s raw %d: %+v, want kept %d", c.tool, c.raw, got, c.kept)
		}
	}
}

// A full queue drops the record instead of blocking, and the rest still lands.
func TestStatsFullQueueDrops(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	release := h.holdDBLock()
	const n = 2000
	within(t, 2*time.Second, "2000 Records with the DB write-locked", func() {
		for range n {
			h.Deps.Stats.Record(h.Deps, "ctx_execute", 0, textResult("x"))
		}
	})
	release()
	got := row("ctx_execute")
	if got.Calls == 0 || got.Calls >= n {
		t.Fatalf("calls = %d, want some written and the overflow dropped (of %d)", got.Calls, n)
	}
}

// Shutdown drains for at most the given time, then gives up.
func TestStatsCloseDrainsAndGivesUp(t *testing.T) {
	h := newHarness(t, nil)
	h.recordStats()
	h.Deps.Stats.Record(h.Deps, "ctx_execute", 0, textResult("abc"))
	within(t, 5*time.Second, "Close", func() { h.Deps.Stats.Close(time.Second) })
	snap, err := h.Deps.Store.Stats(store.StatsQuery{ServerID: "srv-test", Project: h.Deps.Cfg.ProjectKey})
	if err != nil || len(snap.Tools) != 1 || snap.Tools[0].Calls != 1 {
		t.Fatalf("after Close: %+v, %v", snap.Tools, err)
	}

	// A DB that stays locked: Close returns after about its timeout.
	h2 := newHarness(t, nil)
	h2.recordStats()
	h2.holdDBLock()
	h2.Deps.Stats.Record(h2.Deps, "ctx_execute", 0, textResult("abc"))
	start := time.Now()
	within(t, 5*time.Second, "Close with the DB write-locked", func() { h2.Deps.Stats.Close(300 * time.Millisecond) })
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Close took %v with a 300ms limit", d)
	}
	// Records after Close are ignored, not a panic.
	h2.Deps.Stats.Record(h2.Deps, "ctx_execute", 0, textResult("late"))
}

// Calls still queued when ctx_purge runs must not be written after it: a
// purged project would get stats rows back.
func TestPurgeFlushesQueuedStatsFirst(t *testing.T) {
	h := newHarness(t, nil)
	h.recordStats()
	for range 30 {
		h.call("ctx_execute", args("language", "shell", "code", "echo hi"))
	}
	if out, isErr := h.call("ctx_purge", `{"confirm": true}`); isErr || out != purgeProjectMsg {
		t.Fatalf("purge: isErr=%v %q", isErr, out)
	}
	h.Deps.Stats.Flush(5 * time.Second)
	snap, err := h.Deps.Store.Stats(store.StatsQuery{ServerID: "srv-test", Project: h.Deps.Cfg.ProjectKey})
	if err != nil || len(snap.Tools) != 0 {
		t.Fatalf("stats rows after the purge: %+v, %v; want none", snap.Tools, err)
	}
}
