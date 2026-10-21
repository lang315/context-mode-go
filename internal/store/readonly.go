package store

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// readOnlyBusy bounds a read-only wait. A WAL reader does not wait for
// writers; it only waits while another connection recovers the WAL index.
const readOnlyBusy = "250"

// OpenReadOnly opens an existing DB for the statusline (stats spec §3): the
// file URI has mode=ro, and unlike Open it creates no directory, sets no
// journal mode and runs no migration, so it never writes. It fails when the
// file is missing; callers check UserVersion before relying on a table.
func OpenReadOnly(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() + "?mode=ro&_pragma=busy_timeout(" + readOnlyBusy + ")"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, fuzzyVals: map[string]string{}}, nil
}

// UserVersion is the schema version: the number of migrations applied.
func (s *Store) UserVersion() (int, error) {
	var v int
	err := s.db.QueryRow("PRAGMA user_version").Scan(&v)
	return v, err
}

// SessionLine returns the statusline numbers (stats spec §3): the kept and
// raw bytes of the server rows linked to sessionID (its cc_pid and project,
// window = its started_at, as Stats links them), and the all-projects kept
// total. An unknown session, or one whose cc_pid is 0, has no server rows.
func (s *Store) SessionLine(sessionID string) (sessionKept, sessionRaw, lifetime int64, err error) {
	var cc int
	var project, started string
	err = s.db.QueryRow("SELECT cc_pid, project, started_at FROM session_meta WHERE session_id = ?", sessionID).Scan(&cc, &project, &started)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, err
	}
	if err == nil && cc != 0 {
		tools, err := s.toolStats("cc_pid = ? AND project = ? AND window = ?", cc, project, started)
		if err != nil {
			return 0, 0, 0, err
		}
		for _, t := range tools {
			sessionKept += t.BytesKept
		}
		sessionRaw = rawBytes(tools)
	}
	err = s.db.QueryRow("SELECT " + lifetimeKept).Scan(&lifetime)
	return sessionKept, sessionRaw, lifetime, err
}
