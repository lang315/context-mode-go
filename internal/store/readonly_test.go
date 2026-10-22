package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenReadOnlyMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if s, err := OpenReadOnly(filepath.Join(dir, "cmgo.db")); err == nil {
		s.Close()
		t.Fatal("OpenReadOnly of a missing file succeeded")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("OpenReadOnly created %s: %v", dir, err)
	}
}

// fileState is a file's size and mtime, or "absent".
func fileState(t *testing.T, path string) string {
	t.Helper()
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d bytes, %v", fi.Size(), fi.ModTime())
}

// The statusline reads while a hook or the server holds the write lock: a
// WAL reader must not wait, must see only committed rows, and must leave the
// DB and its -wal file as they were.
func TestOpenReadOnlyUnderWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.SetSessionCC("s1", "/p", 100); err != nil {
		t.Fatal(err)
	}
	if err := w.AddToolStat("srv", 100, "/p", "2000-01-01 00:00:00", "ctx_execute", 10, 90); err != nil {
		t.Fatal(err)
	}
	tx, err := w.db.Begin() // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE tool_stats SET bytes_kept = 5000"); err != nil {
		t.Fatal(err)
	}
	before := fileState(t, path) + " | " + fileState(t, path+"-wal")

	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := r.UserVersion(); err != nil || v != len(migrations) {
		t.Fatalf("UserVersion = %d, %v", v, err)
	}
	kept, raw, life, err := r.SessionLine("s1")
	if err != nil || kept != 90 || raw != 100 || life != 90 {
		t.Fatalf("SessionLine = %d %d %d %v", kept, raw, life, err)
	}
	if err := r.AddToolStat("srv", 100, "/p", "2000-01-01 00:00:00", "ctx_execute", 1, 1); err == nil {
		t.Fatal("write through a read-only store succeeded")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if after := fileState(t, path) + " | " + fileState(t, path+"-wal"); after != before {
		t.Fatalf("files changed:\nbefore %s\nafter  %s", before, after)
	}
}

// With no other connection, SQLite has removed -wal and -shm; a read-only
// open must still read the WAL-mode file.
func TestOpenReadOnlyClosedDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddToolStat("srv", 100, "/p", "2000-01-01 00:00:00", "ctx_execute", 10, 90); err != nil {
		t.Fatal(err)
	}
	w.Close()
	before := fileState(t, path)
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, _, life, err := r.SessionLine("unknown"); err != nil || life != 90 {
		t.Fatalf("SessionLine = %d, %v", life, err)
	}
	if after := fileState(t, path); after != before {
		t.Fatalf("db changed: %s -> %s", before, after)
	}
}

func TestSessionLine(t *testing.T) {
	s := statsFixture(t)
	cases := []struct {
		session         string
		kept, raw, life int64
	}{
		// sA: srvA's ctx_execute row; lifetime is every kept byte (stats_totals).
		{"sA", 900, 1100, 900 + 90 + 4000 + 8},
		// sB: ctx_search returns bytes but has no raw size.
		{"sB", 90, 100, 900 + 90 + 4000 + 8},
		{"missing", 0, 0, 900 + 90 + 4000 + 8},
	}
	for _, c := range cases {
		kept, raw, life, err := s.SessionLine(c.session)
		if err != nil || kept != c.kept || raw != c.raw || life != c.life {
			t.Errorf("%s: %d %d %d %v, want %d %d %d", c.session, kept, raw, life, err, c.kept, c.raw, c.life)
		}
	}
	// A session whose pid is unknown never takes the cc_pid 0 rows (srvX).
	if err := s.SetSessionCC("s0", "/p", 0); err != nil {
		t.Fatal(err)
	}
	if kept, raw, _, err := s.SessionLine("s0"); err != nil || kept != 0 || raw != 0 {
		t.Fatalf("s0: %d %d %v", kept, raw, err)
	}
	// /clear: the new session under cc 100 starts after srvA's last write.
	if err := s.SetSessionCC("sA2", "/p", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE session_meta SET started_at = '2026-10-21 11:00:00' WHERE session_id = 'sA2'"); err != nil {
		t.Fatal(err)
	}
	if kept, raw, _, err := s.SessionLine("sA2"); err != nil || kept != 0 || raw != 0 {
		t.Fatalf("sA2: %d %d %v", kept, raw, err)
	}
	// srvA, kept across the /clear, calls again: only sA2 sees the call.
	if err := s.AddToolStat("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 1, 2); err != nil {
		t.Fatal(err)
	}
	if kept, raw, _, err := s.SessionLine("sA2"); err != nil || kept != 2 || raw != 3 {
		t.Fatalf("sA2 after a call: %d %d %v", kept, raw, err)
	}
	if kept, raw, _, err := s.SessionLine("sA"); err != nil || kept != 900 || raw != 1100 {
		t.Fatalf("sA after /clear: %d %d %v", kept, raw, err)
	}
}

// The lifetime total runs on every statusline call; it reads stats_totals (one row per
// project) and never session_events.
func TestLifetimeKeptReadsOnlyStatsTotals(t *testing.T) {
	s := openTest(t)
	rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT " + lifetimeKept)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	got := strings.Join(plan, "\n")
	if !strings.Contains(got, "stats_totals") || strings.Contains(got, "session_events") || strings.Contains(got, "tool_stats") {
		t.Fatalf("plan:\n%s", got)
	}
}
