package store

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestEventDataHash(t *testing.T) {
	// sha256("hello") = 2cf24dba5fb0a30e...
	if got := EventDataHash("hello"); got != "2CF24DBA5FB0A30E" {
		t.Fatalf("EventDataHash = %q", got)
	}
}

func metaRow(t *testing.T, s *Store, sid string) (project, started, last string, count int) {
	t.Helper()
	err := s.db.QueryRow("SELECT project, started_at, last_event_at, event_count FROM session_meta WHERE session_id = ?", sid).
		Scan(&project, &started, &last, &count)
	if err != nil {
		t.Fatal(err)
	}
	return
}

var sqliteTimeRe = regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$`)

func TestInsertEventsStoresRowsAndMeta(t *testing.T) {
	s := openTest(t)
	n, err := s.InsertEvents("s1", "/p", "PostToolUse", []Event{
		{Type: "file_read", Category: "file", Priority: 1, Data: "/p/a.go", BytesAvoided: 10},
		{Type: "git", Category: "git", Priority: 2, Data: "status"},
	})
	if err != nil || n != 2 {
		t.Fatalf("InsertEvents = %d, %v", n, err)
	}
	evs, err := s.SessionEvents("s1")
	if err != nil || len(evs) != 2 {
		t.Fatalf("SessionEvents = %d rows, %v", len(evs), err)
	}
	e := evs[0]
	if e.SessionID != "s1" || e.Project != "/p" || e.Type != "file_read" || e.Category != "file" || e.Priority != 1 ||
		e.Data != "/p/a.go" || e.DataHash != EventDataHash("/p/a.go") || e.SourceHook != "PostToolUse" ||
		e.BytesAvoided != 10 || !sqliteTimeRe.MatchString(e.CreatedAt) || evs[1].ID <= e.ID || evs[1].Type != "git" {
		t.Fatalf("rows = %+v", evs)
	}
	project, started, last, count := metaRow(t, s, "s1")
	if project != "/p" || !sqliteTimeRe.MatchString(started) || last < started || count != 2 {
		t.Fatalf("meta = %q %q %q %d", project, started, last, count)
	}

	// An empty batch inserts nothing but still registers a new session.
	if n, err := s.InsertEvents("s2", "/q", "SessionStart", nil); err != nil || n != 0 {
		t.Fatalf("empty InsertEvents = %d, %v", n, err)
	}
	if project, _, _, count := metaRow(t, s, "s2"); project != "/q" || count != 0 {
		t.Fatalf("s2 meta = %q %d", project, count)
	}
	if evs, _ := s.SessionEvents("s2"); len(evs) != 0 {
		t.Fatalf("s2 rows = %+v", evs)
	}
}

// The cut lands on a rune start and the hash is of the stored bytes.
func TestInsertEventsCutsData(t *testing.T) {
	s := openTest(t)
	long := strings.Repeat("a", MaxEventData-1) + "é" + "tail" // é spans bytes 32767-32768
	if _, err := s.InsertEvents("s", "/p", "PostToolUse", []Event{{Type: "t", Category: "c", Priority: 3, Data: long}}); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.SessionEvents("s")
	want := strings.Repeat("a", MaxEventData-1)
	if len(evs) != 1 || evs[0].Data != want || evs[0].DataHash != EventDataHash(want) {
		t.Fatalf("stored %d bytes, hash %q", len(evs[0].Data), evs[0].DataHash)
	}
}

// Two hook processes writing to one session at once: no busy error, no lost row.
func TestInsertEventsConcurrentStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	const stores, calls = 2, 100
	var wg sync.WaitGroup
	errs := make(chan error, stores*calls)
	handles := make([]*Store, stores)
	for i := range handles {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		handles[i] = s
	}
	start := make(chan struct{})
	for i, s := range handles {
		for j := 0; j < calls; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ev := Event{Type: "t", Category: "c", Priority: 2, Data: fmt.Sprintf("%d-%d", i, j)}
				if _, err := s.InsertEvents("s", "/p", "PostToolUse", []Event{ev}); err != nil {
					errs <- err
				}
			}()
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent InsertEvents: %v", err)
	}
	evs, _ := handles[0].SessionEvents("s")
	if _, _, _, count := metaRow(t, handles[0], "s"); len(evs) != stores*calls || count != stores*calls {
		t.Fatalf("rows=%d event_count=%d, want %d", len(evs), count, stores*calls)
	}
}
