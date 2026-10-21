package store

import (
	"testing"
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

	// Sessions of cc 42: the newest one in /p is the window; /q's is ignored.
	for _, sess := range []struct{ id, project, started string }{
		{"s1", "/p", "2026-10-21 09:00:00"}, {"s2", "/p", "2026-10-21 09:30:00"}, {"s3", "/q", "2026-10-21 11:00:00"},
	} {
		if err := s.SetSessionCC(sess.id, sess.project, 42); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("UPDATE session_meta SET started_at = ? WHERE session_id = ?", sess.started, sess.id); err != nil {
			t.Fatal(err)
		}
	}
	add("srv1", "ctx_execute", 1, 2)
	row("2026-10-21 09:30:00")
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
