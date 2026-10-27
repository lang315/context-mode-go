package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func execRow(t *testing.T, tools []ToolStat) ToolStat {
	t.Helper()
	for _, ts := range tools {
		if ts.Tool == "ctx_execute" {
			return ts
		}
	}
	return ToolStat{}
}

func TestUnmeasuredCountsAddUp(t *testing.T) {
	s := statsFixture(t)
	q := StatsQuery{ServerID: "srvA", CCPID: 100, Project: "/p"}
	before, err := s.Stats(q)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStatExec("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 5, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStatExec("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 5, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStat("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 5, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stats(q)
	if err != nil {
		t.Fatal(err)
	}
	was, now := execRow(t, before.Tools), execRow(t, got.Tools)
	if now.Unmeasured != was.Unmeasured+2 || now.Calls != was.Calls+3 {
		t.Fatalf("ctx_execute = %+v, before %+v; want Unmeasured +2, Calls +3", now, was)
	}
	if got.LifetimeUnmeasured != before.LifetimeUnmeasured+2 {
		t.Fatalf("LifetimeUnmeasured = %d, before %d; want +2", got.LifetimeUnmeasured, before.LifetimeUnmeasured)
	}
}

// A database at schema version 3 opens at the latest version with every row kept and counted 0.
func TestMigration4KeepsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStat("srv", 1, "/p", "2026-10-21 08:00:00", "ctx_execute", 1, 2); err != nil {
		t.Fatal(err)
	}
	// Rewind to v3, as an old file looks.
	if _, err := s.db.Exec("ALTER TABLE tool_stats DROP COLUMN unmeasured_calls"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != len(migrations) {
		t.Fatalf("SchemaVersion = %d, want %d", v, len(migrations))
	}
	got, err := s.toolStats("server_id = ?", "srv")
	want := []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 1, BytesKept: 2}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, %v; want %+v", got, err, want)
	}
}

// Migration 5 caps each row's kept bytes at 30000 per call (the per-call cap
// mcpserver applies since then) and takes the same amount off the project's
// lifetime total. A total with no rows left (purged) stays as it is.
func TestMigration5CapsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO tool_stats(server_id, cc_pid, project, tool, calls, bytes_returned, bytes_kept, started_at, window, updated_at) VALUES
		 ('a', 1, '/p', 'ctx_execute', 13, 18741, 750000000, '2026-10-22 05:39:40', '', '2026-10-24 03:58:57'),
		 ('b', 1, '/p', 'ctx_execute', 2, 10, 1000, '2026-10-22 05:39:40', '', '2026-10-24 03:58:57'),
		 ('c', 1, '/q', 'ctx_fetch_and_index', 1, 10, 50000, '2026-10-22 05:39:40', '', '2026-10-24 03:58:57')`,
		`INSERT INTO stats_totals(project, calls, bytes_returned, bytes_kept, first_at) VALUES
		 ('/p', 15, 18751, 750001000, '2026-10-22 05:39:40'),
		 ('/q', 1, 10, 50000, '2026-10-22 05:39:40'),
		 ('/r', 4, 10, 99999, '2026-10-22 05:39:40')`,
		"PRAGMA user_version = 4",
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != 5 {
		t.Fatalf("SchemaVersion = %d, want 5", v)
	}
	rows := map[string]int64{}
	for _, srv := range []string{"a", "b", "c"} {
		var kept int64
		if err := s.db.QueryRow("SELECT bytes_kept FROM tool_stats WHERE server_id = ?", srv).Scan(&kept); err != nil {
			t.Fatal(err)
		}
		rows[srv] = kept
	}
	if want := map[string]int64{"a": 390000, "b": 1000, "c": 30000}; !reflect.DeepEqual(rows, want) {
		t.Errorf("tool_stats kept = %v, want %v", rows, want)
	}
	totals := map[string]int64{}
	r, err := s.db.Query("SELECT project, bytes_kept FROM stats_totals")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for r.Next() {
		var p string
		var kept int64
		if err := r.Scan(&p, &kept); err != nil {
			t.Fatal(err)
		}
		totals[p] = kept
	}
	if want := map[string]int64{"/p": 391000, "/q": 30000, "/r": 99999}; !reflect.DeepEqual(totals, want) {
		t.Errorf("stats_totals kept = %v, want %v", totals, want)
	}
}
