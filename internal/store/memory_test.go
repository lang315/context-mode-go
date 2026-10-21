package store

import (
	"fmt"
	"strings"
	"testing"
)

func insertTestEvents(t *testing.T, s *Store, sid, project string, evs ...Event) {
	t.Helper()
	if _, err := s.InsertEvents(sid, project, "PostToolUse", evs); err != nil {
		t.Fatal(err)
	}
}

func eventData(evs []StoredEvent) string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Data)
	}
	return strings.Join(out, "|")
}

func TestSessionEventsForSearch(t *testing.T) {
	s := openTest(t)
	insertTestEvents(t, s, "s1", "/p",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "use Alpha cache with BETA flag"},
		Event{Type: "file_read", Category: "file", Priority: 1, Data: "alpha.go"},
		Event{Type: "error_tool", Category: "error", Priority: 2, Data: "rate 50% exceeded"},
	)
	insertTestEvents(t, s, "s2", "/p",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "alpha beta second session"},
		Event{Type: "git", Category: "git", Priority: 2, Data: "rate 500 commits"},
	)
	insertTestEvents(t, s, "s3", "/q",
		Event{Type: "decision", Category: "decision", Priority: 2, Data: "alpha beta other project"},
	)

	cases := []struct {
		query, source string
		limit         int
		want          string
	}{
		// Every term must occur (case-insensitive) in data or category; all sessions of the project; newest first.
		{"alpha beta", "", 10, "alpha beta second session|use Alpha cache with BETA flag"},
		{"ALPHA", "", 10, "alpha beta second session|alpha.go|use Alpha cache with BETA flag"},
		{"alpha  beta", "", 1, "alpha beta second session"},
		// A term may match the category instead of the data.
		{"deci alpha", "", 10, "alpha beta second session|use Alpha cache with BETA flag"},
		// source must equal the category exactly.
		{"alpha", "file", 10, "alpha.go"},
		{"alpha", "fil", 10, ""},
		// % and _ are literal.
		{"50%", "", 10, "rate 50% exceeded"},
		{"rate_", "", 10, ""},
		// No terms: every row of the project, newest first.
		{"   ", "", 2, "rate 500 commits|alpha beta second session"},
	}
	for _, c := range cases {
		evs, err := s.SessionEventsForSearch("/p", c.query, c.source, c.limit)
		if err != nil {
			t.Fatalf("%q/%q: %v", c.query, c.source, err)
		}
		if got := eventData(evs); got != c.want {
			t.Errorf("%q/%q limit %d = %q, want %q", c.query, c.source, c.limit, got, c.want)
		}
	}

	evs, err := s.SessionEventsForSearch("/p", "deci", "decision", 1)
	if err != nil || len(evs) != 1 {
		t.Fatalf("full row: %v %v", evs, err)
	}
	if e := evs[0]; e.SessionID != "s2" || e.Project != "/p" || e.Type != "decision" || e.Category != "decision" ||
		e.Priority != 2 || e.SourceHook != "PostToolUse" || !sqliteTimeRe.MatchString(e.CreatedAt) {
		t.Fatalf("row = %+v", e)
	}
}

func compactCount(t *testing.T, s *Store, sid string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT compact_count FROM session_meta WHERE session_id = ?", sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCompactSession(t *testing.T) {
	s := openTest(t)
	// No events: nothing is written, not even a session_meta row (TS precompact skips, no idle writes).
	if n, files, err := s.CompactSession("empty", "/p"); err != nil || n != 0 || files != 0 {
		t.Fatalf("empty = %d %d %v", n, files, err)
	}
	var metas int
	if err := s.db.QueryRow("SELECT count(*) FROM session_meta").Scan(&metas); err != nil || metas != 0 {
		t.Fatalf("meta rows = %d %v", metas, err)
	}

	insertTestEvents(t, s, "s1", "/p",
		Event{Type: "file_read", Category: "file", Priority: 1, Data: "a.go"},
		Event{Type: "file_write", Category: "file", Priority: 1, Data: "b.go"},
		Event{Type: "git", Category: "git", Priority: 2, Data: "status"},
	)
	n, files, err := s.CompactSession("s1", "/p")
	if err != nil || n != 3 || files != 2 {
		t.Fatalf("CompactSession = %d %d %v", n, files, err)
	}
	evs, _ := s.SessionEvents("s1")
	last := evs[len(evs)-1]
	if len(evs) != 4 || last.Type != "compaction_summary" || last.Category != "compaction" || last.Priority != 1 ||
		last.Data != "Session compacted. 3 events, 2 files touched." || last.SourceHook != "PreCompact" || last.Project != "/p" {
		t.Fatalf("rows = %+v", evs)
	}
	if _, _, _, count := metaRow(t, s, "s1"); count != 4 || compactCount(t, s, "s1") != 1 {
		t.Fatalf("meta event_count %d compact_count %d", count, compactCount(t, s, "s1"))
	}

	// The second compaction counts the first summary.
	if n, files, err := s.CompactSession("s1", "/p"); err != nil || n != 4 || files != 2 {
		t.Fatalf("second = %d %d %v", n, files, err)
	}
	evs, _ = s.SessionEvents("s1")
	if evs[len(evs)-1].Data != "Session compacted. 4 events, 2 files touched." || compactCount(t, s, "s1") != 2 {
		t.Fatalf("second rows = %+v", evs)
	}
}

func TestCompactSessionKeepsEventCap(t *testing.T) {
	s := openTest(t)
	evs := make([]Event, MaxEventsPerSession)
	for i := range evs {
		evs[i] = Event{Type: "note", Category: "file", Priority: 4, Data: fmt.Sprint("e", i)}
	}
	insertTestEvents(t, s, "s1", "/p", evs...)
	if n, files, err := s.CompactSession("s1", "/p"); err != nil || n != MaxEventsPerSession || files != MaxEventsPerSession {
		t.Fatalf("CompactSession = %d %d %v", n, files, err)
	}
	got, _ := s.SessionEvents("s1")
	if len(got) != MaxEventsPerSession || got[0].Data != "e1" || got[len(got)-1].Type != "compaction_summary" {
		t.Fatalf("%d rows, first %q last %q", len(got), got[0].Data, got[len(got)-1].Type)
	}
}
