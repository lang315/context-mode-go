package store

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// holdWriteLock opens a second connection on path and keeps BEGIN IMMEDIATE
// until the test ends.
func holdWriteLock(t *testing.T, path string) {
	t.Helper()
	abs, _ := filepath.Abs(path)
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: abs}).String()+"?_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.ExecContext(context.Background(), "ROLLBACK")
		conn.Close()
		db.Close()
	})
}

// A hook opens the store on every fire. Claude Code waits for the process to
// exit, so an up-to-date DB must not queue behind another session's write.
func TestOpenUpToDateTakesNoWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	holdWriteLock(t, path)

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		s, err := Open(path)
		if err == nil {
			s.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Open on an up-to-date DB failed: %v", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("Open took %v while another connection held the write lock", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Open still waiting for the write lock after 3s")
	}
}

// The WAL switch of a fresh file needs an exclusive lock. With a short
// busy timeout OpenWith must give up quickly instead of retrying for 30 s.
func TestOpenWithShortBusyTimeoutFailsFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	holdWriteLock(t, path)

	start := time.Now()
	s, err := OpenWith(path, Options{BusyTimeout: 300 * time.Millisecond})
	if err == nil {
		s.Close()
		t.Fatal("OpenWith succeeded while the file was write-locked")
	}
	if !isBusy(err) && !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("want a busy error, got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("OpenWith took %v with a 300ms busy timeout", d)
	}
}

func TestOpenWithBusyTimeoutAppliesToStatements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenWith(path, Options{BusyTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	holdWriteLock(t, path)

	start := time.Now()
	_, err = s.InsertEvents("s1", "p", "PostToolUse", []Event{{Type: "file_read", Category: "file", Priority: 1, Data: "/a"}})
	if err == nil || !isBusy(err) {
		t.Fatalf("want a busy error, got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("InsertEvents waited %v with a 300ms busy timeout", d)
	}
}
