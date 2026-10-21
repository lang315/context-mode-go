package store

import (
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
