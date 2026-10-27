// Package store is the SQLite FTS5 knowledge base: schema, chunkers, search
// and snippets. Port of context-mode src/store.ts (ST) with the differences
// listed in the cmgo core spec §2.2 and §3.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// ContentType is the chunk kind stored in the content_type column.
type ContentType string

const (
	Code  ContentType = "code"
	Prose ContentType = "prose"
)

// sqliteTime is the layout of datetime('now') values in sources.indexed_at.
const sqliteTime = "2006-01-02 15:04:05"

// Store owns one *sql.DB with a single connection kept open for the life of
// the process.
type Store struct {
	db *sql.DB

	mu         sync.Mutex
	indexCalls int               // Index calls in this process; FTS optimize every 50 (ST:1132)
	fuzzyKeys  []string          // LRU order of fuzzyVals, oldest first (ST:470)
	fuzzyVals  map[string]string // word -> correction; "" means no correction
}

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE sources (
  id INTEGER PRIMARY KEY,
  project TEXT NOT NULL,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL,
  code_chunk_count INTEGER NOT NULL,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (project, label)
);
CREATE VIRTUAL TABLE chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'porter unicode61');
CREATE VIRTUAL TABLE chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'trigram');
CREATE TABLE vocabulary (word TEXT PRIMARY KEY);`,
	// Session event capture (hooks spec §3.1).
	`CREATE TABLE session_meta (
  session_id TEXT PRIMARY KEY,
  project TEXT NOT NULL,
  started_at TEXT NOT NULL,         -- UTC 'YYYY-MM-DD HH:MM:SS', like sources.indexed_at
  last_event_at TEXT NOT NULL,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE session_events (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  project TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL,        -- 1 = most important ... 4 = least
  data TEXT NOT NULL,
  data_hash TEXT NOT NULL,          -- sha256(data) hex, first 16, upper case (DB:1145)
  source_hook TEXT NOT NULL,
  bytes_avoided INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL          -- UTC 'YYYY-MM-DD HH:MM:SS'
);
CREATE INDEX session_events_session ON session_events(session_id, id);
CREATE INDEX session_events_project ON session_events(project, id);
CREATE INDEX session_events_evict ON session_events(session_id, priority, id);`,
	// Server tool counters and the session link (stats spec §1.1).
	`CREATE TABLE tool_stats (
  server_id TEXT NOT NULL,          -- "<pid>-<start unix nanos>" of the cmgo serve process
  cc_pid INTEGER NOT NULL,          -- Claude Code process that owns the server (§1.2), 0 if unknown
  project TEXT NOT NULL,
  tool TEXT NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  bytes_returned INTEGER NOT NULL DEFAULT 0,
  bytes_kept INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,         -- server start, UTC 'YYYY-MM-DD HH:MM:SS'
  window TEXT NOT NULL DEFAULT '',   -- session id the call was attributed to at write time ('' when none)
  updated_at TEXT NOT NULL,
  PRIMARY KEY (server_id, tool, window)
);
CREATE INDEX tool_stats_window ON tool_stats(window);
CREATE INDEX tool_stats_project ON tool_stats(project);
CREATE TABLE stats_totals (         -- running lifetime sums, one row per project
  project TEXT PRIMARY KEY,
  calls INTEGER NOT NULL DEFAULT 0,
  bytes_returned INTEGER NOT NULL DEFAULT 0,
  bytes_kept INTEGER NOT NULL DEFAULT 0,
  first_at TEXT NOT NULL
);
ALTER TABLE session_meta ADD COLUMN cc_pid INTEGER NOT NULL DEFAULT 0;
ALTER TABLE session_meta ADD COLUMN linked_at TEXT NOT NULL DEFAULT '';  -- UTC with milliseconds, set by SetSessionCC
CREATE INDEX session_meta_cc ON session_meta(cc_pid, project, linked_at);
-- Not in the spec DDL: the statusline sums every redirect's bytes_avoided on
-- each run, and a scan of 50,000 events alone takes longer than its 10 ms budget.
CREATE INDEX session_events_redirect ON session_events(bytes_avoided) WHERE category = 'redirect';`,
	// ctx_execute calls whose kept bytes cannot be measured (kept-not-measured spec §1).
	`ALTER TABLE tool_stats ADD COLUMN unmeasured_calls INTEGER NOT NULL DEFAULT 0;`,
	// Cap kept at 30000 bytes per call (mcpserver.MaxKeptPerCall) on rows written
	// before the cap, and take the same amount off each project's lifetime total.
	`UPDATE stats_totals SET bytes_kept = max(0, bytes_kept - coalesce((
  SELECT sum(t.bytes_kept - t.calls * 30000) FROM tool_stats t
  WHERE t.project = stats_totals.project AND t.bytes_kept > t.calls * 30000), 0));
UPDATE tool_stats SET bytes_kept = calls * 30000 WHERE bytes_kept > calls * 30000;`,
}

// defaultBusyTimeout is how long a statement or the WAL switch waits for
// another connection's lock. The server keeps it; hooks use a shorter one.
const defaultBusyTimeout = 30 * time.Second

// Options tunes OpenWith. The zero value is what Open uses.
type Options struct {
	// BusyTimeout bounds every lock wait, the WAL switch included.
	// 0 means 30 s.
	BusyTimeout time.Duration
}

// Open creates the parent directory, opens the DB with WAL pragmas and
// applies pending migrations.
func Open(path string) (*Store, error) { return OpenWith(path, Options{}) }

// OpenWith is Open with options. Claude Code waits for a hook process to
// exit, so hooks pass a short BusyTimeout and drop their events on BUSY.
func OpenWith(path string, opts Options) (*Store, error) {
	busy := opts.BusyTimeout
	if busy <= 0 {
		busy = defaultBusyTimeout
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// journal_mode is not in the DSN: WAL persists in the file, and switching
	// to it needs an exclusive lock that SQLite refuses with SQLITE_BUSY
	// without consulting busy_timeout. Re-applying it on every connection made
	// a session fail to start while another session was writing.
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() +
		"?_txlock=immediate" +
		"&_pragma=busy_timeout(" + strconv.FormatInt(busy.Milliseconds(), 10) + ")" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	s := &Store{db: db, fuzzyVals: map[string]string{}}
	if err := s.ensureWAL(busy); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// ensureWAL switches the file to WAL once. Reading the mode takes no lock;
// the switch is retried with backoff for up to busy while another connection
// holds a lock.
func (s *Store) ensureWAL(busy time.Duration) error {
	deadline := time.Now().Add(busy)
	wait := 10 * time.Millisecond
	for {
		var mode string
		err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
		if err == nil && mode == "wal" {
			return nil
		}
		if err == nil {
			err = s.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode)
			if err == nil && mode == "wal" {
				return nil
			}
			if err == nil {
				err = fmt.Errorf("journal_mode is %q, want wal", mode)
			}
		}
		if !isBusy(err) || time.Now().After(deadline) {
			return fmt.Errorf("enable WAL: %w", err)
		}
		time.Sleep(wait)
		if wait < 250*time.Millisecond {
			wait *= 2
		}
	}
}

// isBusy reports SQLITE_BUSY / SQLITE_LOCKED, which the driver may wrap.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// migrate re-reads user_version inside an IMMEDIATE transaction so two
// processes opening a fresh file at once do not both run step 1.
func (s *Store) migrate() error {
	// A plain read takes no write lock: an up-to-date DB (every hook fire)
	// must not queue behind another session's write transaction.
	var cur int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&cur); err != nil {
		return err
	}
	if cur >= len(migrations) {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v >= len(migrations) {
		return nil
	}
	for i := v; i < len(migrations); i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the DB (SQLite checkpoints the WAL on last close).
func (s *Store) Close() error { return s.db.Close() }

// cleanupDue reports whether meta.last_cleanup is missing or older than every.
func cleanupDue(q interface {
	QueryRow(string, ...any) *sql.Row
}, now time.Time, every time.Duration) (bool, error) {
	var last string
	err := q.QueryRow("SELECT value FROM meta WHERE key = 'last_cleanup'").Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	t, err := time.Parse(time.RFC3339, last)
	return err != nil || now.Sub(t) >= every, nil
}

// CleanupIfDue deletes sources older than retention (all projects) when
// meta.last_cleanup is missing or older than every, and in the same
// transaction the sessions inactive for SessionRetention. Returns sources deleted.
// When not due it only reads: no write lock is taken.
// Port of cleanupStaleSources (ST:1641) gated per spec §3.1.
func (s *Store) CleanupIfDue(now time.Time, retention, every time.Duration) (int, error) {
	if due, err := cleanupDue(s.db, now, every); err != nil || !due {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Re-check under the write lock: another process may have just cleaned.
	if due, err := cleanupDue(tx, now, every); err != nil || !due {
		return 0, err
	}
	cutoff := now.Add(-retention).UTC().Format(sqliteTime)
	bashCutoff := now.Add(-BashSourceRetention).UTC().Format(sqliteTime)
	// bash: sources (compacted Bash results) only serve their session.
	stale := "datetime(indexed_at) < datetime(?1) OR (label LIKE 'bash:%' AND datetime(indexed_at) < datetime(?2))"
	old := "SELECT id FROM sources WHERE " + stale
	if _, err := tx.Exec("DELETE FROM chunks WHERE source_id IN ("+old+")", cutoff, bashCutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM chunks_trigram WHERE source_id IN ("+old+")", cutoff, bashCutoff); err != nil {
		return 0, err
	}
	res, err := tx.Exec("DELETE FROM sources WHERE "+stale, cutoff, bashCutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	// Sessions inactive for SessionRetention (DB:1696-1705, keyed on activity).
	inactive := now.Add(-SessionRetention).UTC().Format(sqliteTime)
	if _, err := tx.Exec("DELETE FROM session_events WHERE session_id IN (SELECT session_id FROM session_meta WHERE last_event_at < ?)", inactive); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM session_meta WHERE last_event_at < ?", inactive); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("INSERT INTO meta(key, value) VALUES('last_cleanup', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		now.UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
