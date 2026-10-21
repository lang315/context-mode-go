package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenSchemaAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var jm string
	var busy, fk, syncMode int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.db.QueryRow("PRAGMA journal_mode").Scan(&jm)
	s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.db.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	if v != 2 || jm != "wal" || busy != 30000 || fk != 1 || syncMode != 1 {
		t.Fatalf("user_version=%d journal=%s busy=%d fk=%d sync=%d", v, jm, busy, fk, syncMode)
	}
	for _, tbl := range []string{"meta", "sources", "chunks", "chunks_trigram", "vocabulary"} {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: n=%d err=%v", tbl, n, err)
		}
	}
	if _, err := s.db.Exec("INSERT INTO sources(project, label, chunk_count, code_chunk_count) VALUES('p','x',0,0)"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen: migrations are not re-run and data survives.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&n)
	if n != 1 {
		t.Fatalf("sources after reopen = %d", n)
	}
}

// A DB written by the core (user_version 1) gains the session tables on Open
// and keeps its sources.
func TestMigrateFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	all := migrations
	migrations = all[:1] // the core code path: only migration 1 exists
	old, err := Open(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec("INSERT INTO sources(project, label, chunk_count, code_chunk_count) VALUES('p','x',0,0)"); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, n int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&n)
	if v != 2 || n != 1 {
		t.Fatalf("user_version=%d sources=%d, want 2 and 1", v, n)
	}
	for _, name := range []string{"session_meta", "session_events", "session_events_session", "session_events_project", "session_events_evict"} {
		if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", name).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: n=%d err=%v", name, n, err)
		}
	}
}

func TestCleanupIfDue(t *testing.T) {
	s := openTest(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO sources(id, project, label, chunk_count, code_chunk_count, indexed_at) VALUES(1,'p','old',1,0,'2020-01-01 00:00:00')")
	exec("INSERT INTO sources(id, project, label, chunk_count, code_chunk_count) VALUES(2,'q','fresh',1,0)")
	for _, tbl := range []string{"chunks", "chunks_trigram"} {
		exec("INSERT INTO " + tbl + "(title, content, source_id, content_type) VALUES('t','old body',1,'prose')")
		exec("INSERT INTO " + tbl + "(title, content, source_id, content_type) VALUES('t','fresh body',2,'prose')")
	}

	now := time.Now()
	const day = 24 * time.Hour
	n, err := s.CleanupIfDue(now, 14*day, day)
	if err != nil || n != 1 {
		t.Fatalf("first cleanup n=%d err=%v", n, err)
	}
	var left, chunks, tri int
	s.db.QueryRow("SELECT count(*) FROM sources").Scan(&left)
	s.db.QueryRow("SELECT count(*) FROM chunks").Scan(&chunks)
	s.db.QueryRow("SELECT count(*) FROM chunks_trigram").Scan(&tri)
	if left != 1 || chunks != 1 || tri != 1 {
		t.Fatalf("after cleanup sources=%d chunks=%d trigram=%d", left, chunks, tri)
	}

	// Not due again within 24h, even if an old source appears.
	exec("UPDATE sources SET indexed_at = '2020-01-01 00:00:00' WHERE id = 2")
	if n, err := s.CleanupIfDue(now.Add(23*time.Hour), 14*day, day); err != nil || n != 0 {
		t.Fatalf("second cleanup n=%d err=%v", n, err)
	}
	// Due after 24h.
	if n, err := s.CleanupIfDue(now.Add(25*time.Hour), 14*day, day); err != nil || n != 1 {
		t.Fatalf("third cleanup n=%d err=%v", n, err)
	}
	var last string
	s.db.QueryRow("SELECT value FROM meta WHERE key='last_cleanup'").Scan(&last)
	if last != now.Add(25*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("last_cleanup = %q", last)
	}
}

// Sessions are swept by last activity, not start time: an old session that is
// still active stays.
func TestCleanupIfDueSessions(t *testing.T) {
	s := openTest(t)
	ev := []Event{{Type: "t", Category: "c", Priority: 1, Data: "x"}}
	for _, sid := range []string{"stale", "active", "fresh"} {
		if _, err := s.InsertEvents(sid, "/p", "PostToolUse", ev); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	ago := func(d time.Duration) string { return now.Add(-d).UTC().Format(sqliteTime) }
	const day = 24 * time.Hour
	s.db.Exec("UPDATE session_meta SET started_at = ?, last_event_at = ? WHERE session_id = 'stale'", ago(9*day), ago(8*day))
	s.db.Exec("UPDATE session_meta SET started_at = ?, last_event_at = ? WHERE session_id = 'active'", ago(30*day), ago(6*day))

	if _, err := s.CleanupIfDue(now, 14*day, day); err != nil {
		t.Fatal(err)
	}
	var metas, events int
	s.db.QueryRow("SELECT count(*) FROM session_meta WHERE session_id = 'stale'").Scan(&metas)
	s.db.QueryRow("SELECT count(*) FROM session_events WHERE session_id = 'stale'").Scan(&events)
	if metas != 0 || events != 0 {
		t.Fatalf("stale session left: meta=%d events=%d", metas, events)
	}
	s.db.QueryRow("SELECT count(*) FROM session_meta").Scan(&metas)
	s.db.QueryRow("SELECT count(*) FROM session_events").Scan(&events)
	if metas != 2 || events != 2 {
		t.Fatalf("kept meta=%d events=%d, want 2 and 2", metas, events)
	}
}

// A not-due CleanupIfDue must only read. Another Store holds the write lock;
// the not-due call must return at once, while a due call (control) waits.
func TestCleanupNotDueTakesNoWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	now := time.Now()
	const day = 24 * time.Hour
	if _, err := s.CleanupIfDue(now, 14*day, day); err != nil {
		t.Fatal(err)
	}
	lock, err := other.db.Begin() // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { _, err := s.CleanupIfDue(now.Add(time.Hour), 14*day, day); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		lock.Rollback()
		t.Fatal("not-due CleanupIfDue waited for the write lock")
	}

	// Control: a due call needs the lock, so it waits until the lock is released.
	go func() { _, err := s.CleanupIfDue(now.Add(25*time.Hour), 14*day, day); done <- err }()
	select {
	case <-done:
		lock.Rollback()
		t.Fatal("due CleanupIfDue did not wait for the write lock")
	case <-time.After(300 * time.Millisecond):
	}
	lock.Rollback()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
