package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Review focus: several Claude sessions starting at once open the same fresh
// DB. The WAL switch needs an exclusive lock and SQLite returns SQLITE_BUSY
// for it without consulting busy_timeout, so Open must retry.
func TestOpenConcurrentFreshDB(t *testing.T) {
	for round := 0; round < 5; round++ {
		path := filepath.Join(t.TempDir(), "cmgo.db")
		const n = 8
		var wg sync.WaitGroup
		errs := make(chan error, n)
		stores := make(chan *Store, n)
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s, err := Open(path)
				if err != nil {
					errs <- err
					return
				}
				stores <- s
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		close(stores)
		for err := range errs {
			t.Fatalf("round %d: concurrent Open failed: %v", round, err)
		}
		for s := range stores {
			var mode string
			if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
				t.Fatalf("journal_mode = %q, %v", mode, err)
			}
			s.Close()
		}
	}
}

// Review focus: a new session starts while another session holds a write
// transaction (indexing). Open must wait for the lock instead of failing.
func TestOpenWhileAnotherConnectionWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	tx, err := s1.db.Begin() // _txlock=immediate: takes the write lock now
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO meta(key, value) VALUES ('hold', '1')"); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(500 * time.Millisecond)
		tx.Commit()
		close(released)
	}()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open during another connection's write failed: %v", err)
	}
	defer s2.Close()
	<-released
}

// Review focus: the exact `cmgo serve` startup sequence (Open, then
// CleanupIfDue, which writes on a fresh DB) run by two sessions at once.
// Before the WAL switch moved out of the DSN this failed in round 0 with
// "invalid _pragma: database is locked".
func TestOpenThenCleanupConcurrently(t *testing.T) {
	for round := 0; round < 50; round++ {
		path := filepath.Join(t.TempDir(), "cmgo.db")
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := Open(path)
				if err != nil {
					errs <- err
					return
				}
				defer s.Close()
				if _, err := s.CleanupIfDue(time.Now(), 14*24*time.Hour, 24*time.Hour); err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}
