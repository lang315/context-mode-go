package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/store"
)

// ctx_stats end to end: a session linked by cc_pid, a server of another
// Claude Code process in the same project, hook events, an indexed source.
func TestCtxStatsReport(t *testing.T) {
	var now time.Time
	h := newHarness(t, func() time.Time { return now })
	project := h.Deps.Cfg.ProjectKey
	h.Deps.Stats = &StatsRecorder{ServerID: "srv-e2e", CCPID: 4242, StartedAt: "2026-10-21 08:00:00"}
	if err := h.Deps.Store.SetSessionCC("sess-1", project, 4242); err != nil {
		t.Fatal(err)
	}
	// started_at is datetime('now'), cut to the second: the report is 2h
	// later give or take that second, which Duration prints as "2h".
	now = time.Now().Add(2 * time.Hour)
	h.events("sess-1", "",
		store.Event{Type: "bash-redirected", Category: "redirect", Priority: 2, Data: "Bash: curl x", BytesAvoided: 8192},
		store.Event{Type: "file_read", Category: "file", Priority: 1, Data: "a.go"})
	if err := h.Deps.Store.AddToolStat("srv-other", 999, project, "2026-10-20 07:00:00", "ctx_execute", 5, 5000); err != nil {
		t.Fatal(err)
	}
	h.index("notes", store.Chunk{Title: "N", Content: "héllo wörld"})
	h.writeProjectFile("data.txt", strings.Repeat("x", 1000))
	// raw 1000 + 2 stdout; returned is the 38-byte code echo plus "ok".
	if out, isErr := h.call("ctx_execute_file", args("path", "data.txt", "language", "shell", "code", "printf ok")); isErr || len(out) != 40 {
		t.Fatalf("execute_file: %v %d %q", isErr, len(out), out)
	}

	got, isErr := h.call("ctx_stats", `{}`)
	want := `cmgo stats

This session  ·  2h  ·  1 calls
  Kept out of context    962 B   (96% of 1002 B processed)
  Returned to context    40 B
  Redirected by hooks    1 calls  ·  ~8.0 KB
  ctx_execute_file          1 calls      962 B kept       40 B returned

Session memory  ·  2 events  ·  0 compactions
  file                       1
  redirect                   1

This project  ·  1 sources indexed (13 B)  ·  1 sessions  ·  2 events

All projects  ·  13.8 KB kept out  ·  1 sessions  ·  since 2026-10-20

cmgo test`
	if isErr || got != want {
		t.Fatalf("isErr=%v\ngot:\n%s\nwant:\n%s", isErr, got, want)
	}
	// The ctx_stats call itself is recorded, with no raw size.
	if again, _ := h.call("ctx_stats", `{}`); !strings.Contains(again, "  ctx_stats                 1 calls        0 B kept") ||
		!strings.Contains(again, "(96% of 1002 B processed)") {
		t.Fatalf("second report:\n%s", again)
	}
}

func TestCtxStatsUnlinked(t *testing.T) {
	h := newHarness(t, nil)
	h.Deps.Stats = &StatsRecorder{ServerID: "srv-solo", StartedAt: "2026-10-21 08:00:00"} // no Claude Code ancestor
	h.call("ctx_execute", args("language", "shell", "code", "printf abc"))
	got, isErr := h.call("ctx_stats", `{}`)
	if isErr || !strings.HasPrefix(got, "cmgo stats\n\nThis session  ·  no hook data yet (is cmgo-hook installed?)\n") ||
		!strings.Contains(got, "\n  ctx_execute               1 calls        0 B kept") || strings.Contains(got, "Session memory") {
		t.Fatalf("isErr=%v\n%s", isErr, got)
	}
}
