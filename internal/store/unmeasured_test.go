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

// A database at schema version 3 opens at 4 with every row kept and counted 0.
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
	if v, _ := s.SchemaVersion(); v != 4 {
		t.Fatalf("SchemaVersion = %d, want 4", v)
	}
	got, err := s.toolStats("server_id = ?", "srv")
	want := []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 1, BytesKept: 2}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, %v; want %+v", got, err, want)
	}
}
