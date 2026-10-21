package store

import "testing"

func tableCount(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPurgeSession(t *testing.T) {
	s := openTest(t)
	insertTestEvents(t, s, "s1", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "x"}, Event{Type: "b", Category: "git", Priority: 2, Data: "y"})
	insertTestEvents(t, s, "s2", "/p", Event{Type: "a", Category: "file", Priority: 1, Data: "z"})
	if _, err := s.Index("/p", "notes", []Chunk{{Title: "T", Content: "kept content"}}); err != nil {
		t.Fatal(err)
	}

	// 2 events + 1 meta row.
	if rows, err := s.PurgeSession("s1", "/p"); err != nil || rows != 3 {
		t.Fatalf("PurgeSession = %d, %v", rows, err)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE session_id = 's1'") +
		tableCount(t, s, "SELECT count(*) FROM session_meta WHERE session_id = 's1'"); n != 0 {
		t.Fatalf("s1 rows left: %d", n)
	}
	if evs, _ := s.SessionEvents("s2"); len(evs) != 1 {
		t.Fatalf("s2 events = %d", len(evs))
	}
	if n, _ := s.ChunkCount("/p"); n != 1 {
		t.Fatalf("chunks = %d", n)
	}
	if rows, err := s.PurgeSession("s1", "/p"); err != nil || rows != 0 {
		t.Fatalf("second PurgeSession = %d, %v", rows, err)
	}
}

func TestPurgeProjectLeavesOtherProjects(t *testing.T) {
	s := openTest(t)
	for _, p := range []string{"/p", "/q"} {
		for _, label := range []string{"notes", "docs"} {
			if _, err := s.Index(p, label, []Chunk{{Title: "T", Content: "gizmo widget " + p}, {Title: "U", Content: "second chunk"}}); err != nil {
				t.Fatal(err)
			}
		}
		insertTestEvents(t, s, p+"-s1", p, Event{Type: "a", Category: "file", Priority: 1, Data: "x"})
		insertTestEvents(t, s, p+"-s2", p, Event{Type: "a", Category: "file", Priority: 1, Data: "y"})
		for _, tool := range []string{"ctx_execute", "ctx_search"} {
			if err := s.AddToolStat("srv"+p, 7, p, "2026-10-21 08:00:00", tool, 1, 2); err != nil {
				t.Fatal(err)
			}
		}
	}
	vocab := tableCount(t, s, "SELECT count(*) FROM vocabulary")

	sources, sessions, stats, err := s.PurgeProject("/p")
	if err != nil || sources != 2 || sessions != 2 || stats != 2 {
		t.Fatalf("PurgeProject = %d %d %d %v, want 2 2 2", sources, sessions, stats, err)
	}
	if n, _ := s.ChunkCount("/p"); n != 0 {
		t.Fatalf("/p chunks = %d", n)
	}
	if n, _ := s.ChunkCount("/q"); n != 4 {
		t.Fatalf("/q chunks = %d", n)
	}
	// Trigram rows go with their sources; /q keeps its 4.
	if n := tableCount(t, s, "SELECT count(*) FROM chunks_trigram"); n != 4 {
		t.Fatalf("trigram rows = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM sources WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p sources = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE project = '/p'") +
		tableCount(t, s, "SELECT count(*) FROM session_meta WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p session rows = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_events WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q events = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_meta WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q meta = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p tool_stats = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM tool_stats WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q tool_stats = %d", n)
	}
	// The running totals go with the rows they sum; /q keeps its own.
	if n := tableCount(t, s, "SELECT count(*) FROM stats_totals WHERE project = '/p'"); n != 0 {
		t.Fatalf("/p stats_totals = %d", n)
	}
	if n := tableCount(t, s, "SELECT calls FROM stats_totals WHERE project = '/q'"); n != 2 {
		t.Fatalf("/q stats_totals calls = %d", n)
	}
	if n := tableCount(t, s, "SELECT count(*) FROM vocabulary"); n != vocab {
		t.Fatalf("vocabulary %d -> %d", vocab, n)
	}
	if sources, sessions, stats, err := s.PurgeProject("/p"); err != nil || sources != 0 || sessions != 0 || stats != 0 {
		t.Fatalf("second PurgeProject = %d %d %d %v", sources, sessions, stats, err)
	}
}

// One DB serves every project, so a session id from another project must not
// be purged by a call made in this one (project /x/ab vs /x/a).
func TestPurgeSessionIgnoresOtherProjects(t *testing.T) {
	s := openTest(t)
	insertTestEvents(t, s, "shared", "/x/ab", Event{Type: "a", Category: "file", Priority: 1, Data: "x"})
	insertTestEvents(t, s, "mine", "/x/a", Event{Type: "a", Category: "file", Priority: 1, Data: "y"})

	if rows, err := s.PurgeSession("shared", "/x/a"); err != nil || rows != 0 {
		t.Fatalf("purge from /x/a of a /x/ab session = %d, %v, want 0", rows, err)
	}
	if evs, _ := s.SessionEvents("shared"); len(evs) != 1 {
		t.Fatalf("/x/ab events = %d, want 1", len(evs))
	}
	if n := tableCount(t, s, "SELECT count(*) FROM session_meta WHERE session_id = 'shared'"); n != 1 {
		t.Fatalf("/x/ab meta rows = %d, want 1", n)
	}
	if rows, err := s.PurgeSession("mine", "/x/a"); err != nil || rows != 2 {
		t.Fatalf("own session = %d, %v, want 2", rows, err)
	}
}
