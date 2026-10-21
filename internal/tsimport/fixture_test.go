package tsimport

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// sessionDDL is the TS SessionDB schema (src/session/db.ts:827-903,
// with the usage_cursor migration applied).
const sessionDDL = `
CREATE TABLE IF NOT EXISTS session_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 2,
  data TEXT NOT NULL,
  project_dir TEXT NOT NULL DEFAULT '',
  attribution_source TEXT NOT NULL DEFAULT 'unknown',
  attribution_confidence REAL NOT NULL DEFAULT 0,
  bytes_avoided INTEGER NOT NULL DEFAULT 0,
  bytes_returned INTEGER NOT NULL DEFAULT 0,
  source_hook TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  data_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_session_events_session ON session_events(session_id);
CREATE INDEX IF NOT EXISTS idx_session_events_type ON session_events(session_id, type);
CREATE INDEX IF NOT EXISTS idx_session_events_priority ON session_events(session_id, priority);
CREATE TABLE IF NOT EXISTS session_meta (
  session_id TEXT PRIMARY KEY,
  project_dir TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT (datetime('now')),
  last_event_at TEXT,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0,
  usage_cursor TEXT
);
CREATE TABLE IF NOT EXISTS session_resume (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL UNIQUE,
  snapshot TEXT NOT NULL,
  event_count INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  consumed INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS tool_calls (
  session_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  bytes_returned INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (session_id, tool)
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);`

// sessionDDLOld is the v1.0.0 SessionDB schema (packages/session/src/db.ts
// at 2ffccbee^): no project_dir, attribution_* or bytes_* columns.
const sessionDDLOld = `
CREATE TABLE IF NOT EXISTS session_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 2,
  data TEXT NOT NULL,
  source_hook TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  data_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_session_events_session ON session_events(session_id);
CREATE TABLE IF NOT EXISTS session_meta (
  session_id TEXT PRIMARY KEY,
  project_dir TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT (datetime('now')),
  last_event_at TEXT,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0
);`

// contentDDL is the TS ContentStore schema (src/store.ts:516-558).
const contentDDL = `
CREATE TABLE IF NOT EXISTS sources (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL DEFAULT 0,
  code_chunk_count INTEGER NOT NULL DEFAULT 0,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  file_path TEXT,
  content_hash TEXT
);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  source_category UNINDEXED, session_id UNINDEXED, event_id UNINDEXED, timestamp UNINDEXED,
  tokenize='porter unicode61'
);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  source_category UNINDEXED, session_id UNINDEXED, event_id UNINDEXED, timestamp UNINDEXED,
  tokenize='trigram'
);
CREATE TABLE IF NOT EXISTS vocabulary (word TEXT PRIMARY KEY);
CREATE INDEX IF NOT EXISTS idx_sources_label ON sources(label);`

// contentDDLOld has the 4-column FTS tables that TS before source_category
// wrote (src/store.ts before the 8-column migration at :560-600).
const contentDDLOld = `
CREATE TABLE IF NOT EXISTS sources (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL DEFAULT 0,
  code_chunk_count INTEGER NOT NULL DEFAULT 0,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  file_path TEXT,
  content_hash TEXT
);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize='porter unicode61'
);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize='trigram'
);
CREATE TABLE IF NOT EXISTS vocabulary (word TEXT PRIMARY KEY);`

// tsDB creates a TS database at path (and its directory) with ddl, in WAL mode with automatic
// checkpoints off, so rows written after a checkpoint stay in the -wal file.
// The connection stays open until the test ends, like a running TS process.
func tsDB(t *testing.T, path, ddl string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	dsn := (&url.URL{Scheme: "file", Path: path}).String() +
		"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	mustExec(t, db, ddl)
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// addEvent inserts a current-schema session_events row.
func addEvent(t *testing.T, db *sql.DB, sid, typ, data, projectDir, createdAt string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO session_events(session_id, type, category, priority, data, project_dir, bytes_avoided, source_hook, created_at, data_hash)
VALUES(?, ?, 'file', 1, ?, ?, 7, 'PostToolUse', ?, 'X')`, sid, typ, data, projectDir, createdAt)
}

// addMeta inserts a session_meta row.
func addMeta(t *testing.T, db *sql.DB, sid, projectDir, startedAt, lastEventAt string, compact int) {
	t.Helper()
	mustExec(t, db, "INSERT INTO session_meta(session_id, project_dir, started_at, last_event_at, compact_count) VALUES(?, ?, ?, ?, ?)",
		sid, projectDir, startedAt, lastEventAt, compact)
}

// addSource inserts a source and its chunks (title, content, content_type
// triples) into a content DB of either schema.
func addSource(t *testing.T, db *sql.DB, label, indexedAt string, chunks ...[3]string) {
	t.Helper()
	r, err := db.Exec("INSERT INTO sources(label, chunk_count, indexed_at) VALUES(?, ?, ?)", label, len(chunks), indexedAt)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	for _, c := range chunks {
		for _, table := range []string{"chunks", "chunks_trigram"} {
			mustExec(t, db, "INSERT INTO "+table+"(title, content, source_id, content_type) VALUES(?, ?, ?, ?)", c[0], c[1], id, c[2])
		}
	}
}

// checkpoint moves every WAL frame into the main file and empties the WAL.
func checkpoint(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, "PRAGMA wal_checkpoint(TRUNCATE)")
}
