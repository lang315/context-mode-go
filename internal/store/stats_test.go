package store

import (
	"reflect"
	"testing"
	"time"
)

func TestAddToolStatUpserts(t *testing.T) {
	s := openTest(t)
	add := func(server, tool string, returned, kept int64) {
		t.Helper()
		if err := s.AddToolStat(server, 42, "/p", "2026-10-21 10:00:00", tool, returned, kept); err != nil {
			t.Fatal(err)
		}
	}
	add("srv1", "ctx_execute", 100, 900)
	add("srv1", "ctx_execute", 50, 0)
	add("srv1", "ctx_search", 30, 0)
	add("srv2", "ctx_execute", 7, 3)

	var calls, returned, kept, ccPID int64
	var project, started, window, updated string
	row := func(window string) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT calls, bytes_returned, bytes_kept, cc_pid, project, started_at, updated_at
FROM tool_stats WHERE server_id = 'srv1' AND tool = 'ctx_execute' AND window = ?`, window).Scan(&calls, &returned, &kept, &ccPID, &project, &started, &updated); err != nil {
			t.Fatal(err)
		}
	}
	// No session of cc 42 yet: the window is ''.
	row("")
	if calls != 2 || returned != 150 || kept != 900 || ccPID != 42 || project != "/p" || started != "2026-10-21 10:00:00" || len(updated) != len(sqliteTime) {
		t.Fatalf("row = %d %d %d %d %q %q %q", calls, returned, kept, ccPID, project, started, updated)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats"); n != 3 {
		t.Fatalf("rows = %d, want 3", n)
	}

	// Sessions of cc 42: the newest one linked in /p is the window; /q's is ignored.
	for _, sess := range []struct{ id, project string }{{"s1", "/p"}, {"s2", "/p"}, {"s3", "/q"}} {
		if err := s.SetSessionCC(sess.id, sess.project, 42); err != nil {
			t.Fatal(err)
		}
	}
	add("srv1", "ctx_execute", 1, 2)
	row("s2")
	if calls != 1 || returned != 1 || kept != 2 {
		t.Fatalf("windowed row = %d %d %d", calls, returned, kept)
	}
	// cc_pid 0 never takes a window, even with sessions registered under 0.
	if err := s.SetSessionCC("s0", "/p", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStat("srv0", 0, "/p", "2026-10-21 10:00:00", "ctx_execute", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT window FROM tool_stats WHERE server_id = 'srv0'").Scan(&window); err != nil || window != "" {
		t.Fatalf("srv0 window = %q, %v", window, err)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats"); n != 5 {
		t.Fatalf("rows = %d, want 5", n)
	}
}

func TestSetSessionCC(t *testing.T) {
	s := openTest(t)
	// A session SessionStart has not registered yet gets its meta row.
	if err := s.SetSessionCC("s1", "/p", 4242); err != nil {
		t.Fatal(err)
	}
	insertTestEvents(t, s, "s1", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "x"})
	if err := s.SetSessionCC("s1", "/p", 777); err != nil {
		t.Fatal(err)
	}
	var cc, events int
	var project string
	if err := s.db.QueryRow("SELECT cc_pid, event_count, project FROM session_meta WHERE session_id = 's1'").Scan(&cc, &events, &project); err != nil {
		t.Fatal(err)
	}
	if cc != 777 || events != 1 || project != "/p" {
		t.Fatalf("meta = cc %d, events %d, project %q", cc, events, project)
	}
	// Sessions registered by InsertEvents alone keep cc_pid 0.
	insertTestEvents(t, s, "s2", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "y"})
	if n := tableCount(t, s, "SELECT cc_pid FROM session_meta WHERE session_id = 's2'"); n != 0 {
		t.Fatalf("s2 cc_pid = %d", n)
	}
}

// statsFixture is two Claude Code processes (cc 100 and 200) with one
// session each in /p, their servers' rows, and a foreign project /q.
// Each row's window is the session id it was attributed to at write time.
func statsFixture(t *testing.T) *Store {
	t.Helper()
	s := openTest(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	add := func(server string, cc int, project, tool string, returned, kept int64) {
		t.Helper()
		if err := s.AddToolStat(server, cc, project, "2026-10-21 08:00:00", tool, returned, kept); err != nil {
			t.Fatal(err)
		}
	}
	for _, sess := range []struct {
		id      string
		cc      int
		project string
	}{{"sA", 100, "/p"}, {"sB", 200, "/p"}, {"sQ", 300, "/q"}} {
		if err := s.SetSessionCC(sess.id, sess.project, sess.cc); err != nil {
			t.Fatal(err)
		}
		exec("UPDATE session_meta SET started_at = '2026-10-21 09:00:00' WHERE session_id = ?", sess.id) // the report's duration
	}
	add("srvA", 100, "/p", "ctx_execute", 100, 900)
	add("srvA", 100, "/p", "ctx_execute", 100, 0)
	add("srvB", 200, "/p", "ctx_execute", 10, 90)
	add("srvB", 200, "/p", "ctx_search", 5, 0)
	add("srvQ", 300, "/q", "ctx_fetch_and_index", 1, 4000)
	add("srvX", 0, "/p", "ctx_execute", 2, 8)
	insertTestEvents(t, s, "sA", "/p",
		Event{Type: "bash-redirected", Category: "redirect", Priority: 2, Data: "a", BytesAvoided: 8192},
		Event{Type: "webfetch-redirected", Category: "redirect", Priority: 2, Data: "b", BytesAvoided: 16384},
		Event{Type: "file_read", Category: "file", Priority: 1, Data: "c"})
	insertTestEvents(t, s, "sQ", "/q", Event{Type: "read-redirected", Category: "redirect", Priority: 2, Data: "d", BytesAvoided: 1000})
	exec("UPDATE session_meta SET compact_count = 2 WHERE session_id = 'sA'")
	for _, src := range []struct{ project, label, content string }{
		{"/p", "one", "héllo"}, {"/p", "two", "abc"}, {"/q", "three", "zzzzzzzzzz"},
	} {
		if _, err := s.Index(src.project, src.label, []Chunk{{Title: "T", Content: src.content}}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestStatsLinksSessionByCCPID(t *testing.T) {
	s := statsFixture(t)
	got, err := s.Stats(StatsQuery{ServerID: "srvA", CCPID: 100, Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	want := StatsSnapshot{
		SessionID: "sA", SessionLinked: true, SessionStart: "2026-10-21 09:00:00",
		Tools: []ToolStat{{Tool: "ctx_execute", Calls: 2, BytesReturned: 200, BytesKept: 900}}, Raw: 1100,
		Redirects: 2, RedirectBytes: 24576,
		Events: 3, Compactions: 2, Categories: map[string]int{"redirect": 2, "file": 1},
		ProjectSources: 2, ProjectContentBytes: 9, // "héllo" is 6 bytes, not 5 characters
		ProjectSessions: 2, ProjectEvents: 3,
		LifetimeKept: 900 + 90 + 4000 + 8 + 24576 + 1000, LifetimeSessions: 3, LifetimeSince: "2026-10-21 08:00:00",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stats =\n%+v\nwant\n%+v", got, want)
	}

	// The other Claude Code process in the same project sees only its own server.
	got, err = s.Stats(StatsQuery{ServerID: "srvB", CCPID: 200, Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	wantTools := []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 10, BytesKept: 90}, {Tool: "ctx_search", Calls: 1, BytesReturned: 5}}
	if got.SessionID != "sB" || !got.SessionLinked || !reflect.DeepEqual(got.Tools, wantTools) || got.Raw != 100 || got.Events != 0 || got.Redirects != 0 || got.Categories == nil {
		t.Fatalf("srvB: %+v", got)
	}
}

// /clear keeps the server process: its later calls go to the new session's
// window, and each session sees only its own calls.
func TestStatsClearWindow(t *testing.T) {
	s := statsFixture(t)
	if err := s.SetSessionCC("sA2", "/p", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE session_meta SET started_at = '2026-10-21 11:00:00' WHERE session_id = 'sA2'"); err != nil { // the report's duration
		t.Fatal(err)
	}
	q := StatsQuery{ServerID: "srvA", CCPID: 100, Project: "/p"}
	got, err := s.Stats(q)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "sA2" || got.SessionStart != "2026-10-21 11:00:00" || len(got.Tools) != 0 || got.Raw != 0 || got.Events != 0 {
		t.Fatalf("after /clear: %+v", got)
	}
	// The same server writes again, after the /clear.
	if err := s.AddToolStat("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 1, 2); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Stats(q)
	if want := []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 1, BytesKept: 2}}; !reflect.DeepEqual(got.Tools, want) || got.Raw != 3 {
		t.Fatalf("second session: %+v", got)
	}
	// The first session keeps its own calls, in its own window.
	first, err := s.toolStats("window = 'sA'")
	if want := []ToolStat{{Tool: "ctx_execute", Calls: 2, BytesReturned: 200, BytesKept: 900}}; err != nil || !reflect.DeepEqual(first, want) {
		t.Fatalf("first session: %+v %v", first, err)
	}
	// Unlinked, the server's own rows add up across windows.
	if got, _ := s.Stats(StatsQuery{ServerID: "srvA", CCPID: 0, Project: "/p"}); !reflect.DeepEqual(got.Tools, []ToolStat{{Tool: "ctx_execute", Calls: 3, BytesReturned: 201, BytesKept: 902}}) {
		t.Fatalf("unlinked: %+v", got.Tools)
	}
}

func TestStatsUnlinked(t *testing.T) {
	s := statsFixture(t)
	for _, q := range []StatsQuery{
		{ServerID: "srvX", CCPID: 0, Project: "/p"},   // pid unknown: never linked, even to old cc_pid 0 rows
		{ServerID: "srvX", CCPID: 100, Project: "/q"}, // cc_pid of a session in another project
		{ServerID: "srvX", CCPID: 999, Project: "/p"}, // no SessionStart for this pid
	} {
		if err := s.SetSessionCC("old", "/p", 0); err != nil {
			t.Fatal(err)
		}
		got, err := s.Stats(q)
		if err != nil {
			t.Fatal(err)
		}
		if got.SessionLinked || got.SessionID != "" || got.SessionStart != "" || got.Events != 0 ||
			!reflect.DeepEqual(got.Tools, []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 2, BytesKept: 8}}) {
			t.Errorf("%+v: %+v", q, got)
		}
	}
}

func TestStatsEmptyDB(t *testing.T) {
	s := openTest(t)
	got, err := s.Stats(StatsQuery{ServerID: "srv", CCPID: 1, Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionLinked || len(got.Tools) != 0 || got.LifetimeKept != 0 || got.LifetimeSince != "" || got.LifetimeSessions != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestSourceBytes(t *testing.T) {
	s := openTest(t)
	r, err := s.Index("/p", "x", []Chunk{{Title: "A", Content: "héllo"}, {Title: "B", Content: "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.SourceBytes(r.SourceID); err != nil || n != 9 {
		t.Fatalf("SourceBytes = %d, %v", n, err)
	}
	if n, err := s.SourceBytes(r.SourceID + 1); err != nil || n != 0 {
		t.Fatalf("missing source = %d, %v", n, err)
	}
}

// sessionTools is the tools a session's window holds.
func sessionTools(t *testing.T, s *Store, sid string) []ToolStat {
	t.Helper()
	tools, err := s.toolStats("window = ?", sid)
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func ms() { time.Sleep(3 * time.Millisecond) } // linked_at has millisecond resolution

// Two /clear in the same second give three sessions of one Claude Code
// process. Their server calls must not land in one window.
func TestStatsClearTwiceInOneSecond(t *testing.T) {
	s := openTest(t)
	call := func(returned int64) {
		t.Helper()
		if err := s.AddToolStat("srv", 7, "/p", "2026-10-21 08:00:00", "ctx_execute", returned, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i, sid := range []string{"c1", "c2", "c3"} {
		if err := s.SetSessionCC(sid, "/p", 7); err != nil {
			t.Fatal(err)
		}
		call(int64(10 * (i + 1)))
		ms()
	}
	for i, sid := range []string{"c1", "c2", "c3"} {
		want := []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: int64(10 * (i + 1))}}
		if got := sessionTools(t, s, sid); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", sid, got, want)
		}
	}
	got, err := s.Stats(StatsQuery{ServerID: "srv", CCPID: 7, Project: "/p"})
	if err != nil || got.SessionID != "c3" || !reflect.DeepEqual(got.Tools, sessionTools(t, s, "c3")) {
		t.Fatalf("Stats = %+v, %v", got, err)
	}
}

// /resume of an older session in the same Claude Code process re-links it:
// the calls after it belong to that session, not to the newer one.
func TestStatsResumeOlderSession(t *testing.T) {
	s := openTest(t)
	call := func() {
		t.Helper()
		if err := s.AddToolStat("srv", 7, "/p", "2026-10-21 08:00:00", "ctx_execute", 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	link := func(sid string) {
		t.Helper()
		if err := s.SetSessionCC(sid, "/p", 7); err != nil {
			t.Fatal(err)
		}
	}
	link("old")
	call()
	ms()
	link("new") // /clear
	call()
	ms()
	link("old") // /resume of the first session
	call()
	call()

	if got := sessionTools(t, s, "old"); len(got) != 1 || got[0].Calls != 3 {
		t.Errorf("old: %+v, want 3 calls", got)
	}
	if got := sessionTools(t, s, "new"); len(got) != 1 || got[0].Calls != 1 {
		t.Errorf("new: %+v, want 1 call", got)
	}
	got, err := s.Stats(StatsQuery{ServerID: "srv", CCPID: 7, Project: "/p"})
	if err != nil || got.SessionID != "old" || len(got.Tools) != 1 || got.Tools[0].Calls != 3 {
		t.Fatalf("Stats after /resume = %+v, %v", got, err)
	}
	// Re-linking changes link time only, never the session's start.
	var started string
	s.db.QueryRow("SELECT started_at FROM session_meta WHERE session_id = 'old'").Scan(&started)
	var linked string
	s.db.QueryRow("SELECT linked_at FROM session_meta WHERE session_id = 'old'").Scan(&linked)
	if len(linked) != len("2026-10-21 08:00:00.000") || linked <= started {
		t.Errorf("linked_at %q, started_at %q", linked, started)
	}
}

// Unlinked server calls have window ”. An empty session id (a statusline
// without a session) must not read them as a session's.
func TestSessionLineEmptyID(t *testing.T) {
	s := statsFixture(t) // srvX wrote with cc_pid 0, so window ''
	if kept, raw, _, err := s.SessionLine(""); err != nil || kept != 0 || raw != 0 {
		t.Fatalf("SessionLine(\"\") = %d %d %v, want 0 0", kept, raw, err)
	}
}
