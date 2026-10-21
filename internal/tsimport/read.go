// Package tsimport copies the TS context-mode data still inside cmgo's
// retention windows into cmgo.db (import spec §3). TS files are copied
// first and only the copies are opened.
package tsimport

import (
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/lang315/context-mode-go/internal/store"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// copyDB copies src and its -wal and -shm files (when present) into dir,
// keeping the base name, and returns the path of the copy. The WAL holds
// rows TS wrote but has not checkpointed yet.
func copyDB(src, dir string) (string, error) {
	dst := filepath.Join(dir, filepath.Base(src))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFile(src+suffix, dst+suffix); err != nil {
			if suffix != "" && os.IsNotExist(err) {
				continue
			}
			return "", err
		}
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// tsCopy is an open copy of one TS database.
type tsCopy struct{ db *sql.DB }

// openCopy opens a copied TS database read-only.
func openCopy(path string) (*tsCopy, error) {
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?mode=ro"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &tsCopy{db: db}, nil
}

func (c *tsCopy) Close() error { return c.db.Close() }

// columns returns the column names of table; none when it does not exist.
// table_xinfo also lists generated columns, like the old data_hash.
func (c *tsCopy) columns(table string) (map[string]bool, error) {
	rows, err := c.db.Query("SELECT name FROM pragma_table_xinfo(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// pick selects column name, or the literal def when an older schema lacks it.
func pick(cols map[string]bool, name, def string) string {
	if cols[name] {
		return name
	}
	return def
}

// tsSession is one session of a TS session DB that has events.
type tsSession struct {
	id         string
	projectDir string // session_meta.project_dir, else the most common event project_dir; raw
	// startedAt and lastEventAt span session_meta and the events, so a
	// missing or stale meta row still gives the session's real activity.
	startedAt, lastEventAt string
	compact                int
	events                 []store.ImportedEvent   // in id order
	fromOtherDBs           [][]store.ImportedEvent // the session's events in later DBs (worktrees), one batch per DB
}

// sessions reads every session that has events, in first-event order.
// data_hash is not read: cmgo recomputes it.
func (c *tsCopy) sessions() ([]tsSession, error) {
	cols, err := c.columns("session_events")
	if err != nil || len(cols) == 0 {
		return nil, err
	}
	rows, err := c.db.Query("SELECT session_id, type, category, " + pick(cols, "priority", "2") + ", data, " +
		pick(cols, "source_hook", "''") + ", " + pick(cols, "bytes_avoided", "0") + ", created_at, " +
		pick(cols, "project_dir", "''") + " FROM session_events ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tsSession
	index := map[string]int{}
	dirs := map[string][]string{} // session id -> non-empty event project_dir values
	for rows.Next() {
		var sid, dir string
		var e store.ImportedEvent
		if err := rows.Scan(&sid, &e.Type, &e.Category, &e.Priority, &e.Data, &e.SourceHook, &e.BytesAvoided, &e.CreatedAt, &dir); err != nil {
			return nil, err
		}
		i, ok := index[sid]
		if !ok {
			i = len(out)
			index[sid] = i
			out = append(out, tsSession{id: sid, startedAt: e.CreatedAt, lastEventAt: e.CreatedAt})
		}
		s := &out[i]
		s.events = append(s.events, e)
		s.startedAt = minTime(s.startedAt, e.CreatedAt)
		s.lastEventAt = max(s.lastEventAt, e.CreatedAt)
		if dir != "" {
			dirs[sid] = append(dirs[sid], dir)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].projectDir = mostCommon(dirs[out[i].id])
	}
	return out, c.mergeMeta(out, index)
}

// mergeMeta applies session_meta rows to the sessions that have events.
func (c *tsCopy) mergeMeta(out []tsSession, index map[string]int) error {
	cols, err := c.columns("session_meta")
	if err != nil || len(cols) == 0 {
		return err
	}
	rows, err := c.db.Query("SELECT session_id, " + pick(cols, "project_dir", "''") + ", " + pick(cols, "started_at", "''") +
		", " + pick(cols, "last_event_at", "''") + ", " + pick(cols, "compact_count", "0") + " FROM session_meta")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var dir, started, last sql.NullString
		var compact int
		if err := rows.Scan(&sid, &dir, &started, &last, &compact); err != nil {
			return err
		}
		i, ok := index[sid]
		if !ok {
			continue
		}
		s := &out[i]
		if dir.String != "" {
			s.projectDir = dir.String
		}
		s.startedAt = minTime(s.startedAt, started.String)
		s.lastEventAt = max(s.lastEventAt, last.String)
		s.compact = compact
	}
	return rows.Err()
}

// minTime is the earlier of two SQLite time strings, ignoring an empty one.
func minTime(a, b string) string {
	if a == "" || (b != "" && b < a) {
		return b
	}
	return a
}

// mostCommon returns the most frequent value, the first seen on a tie.
func mostCommon(vals []string) string {
	counts := map[string]int{}
	best := ""
	for _, v := range vals {
		counts[v]++
		if counts[v] > counts[best] {
			best = v
		}
	}
	return best
}

// tsSource is one source of a TS content DB with its chunks.
type tsSource struct {
	label, indexedAt string
	chunks           []store.Chunk // in rowid order
}

// sources reads every source with its chunks. Only the columns both the
// 4-column and the 8-column FTS schemas have are read.
func (c *tsCopy) sources() ([]tsSource, error) {
	byID := map[int64][]store.Chunk{}
	rows, err := c.db.Query("SELECT title, content, source_id, content_type FROM chunks ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ch store.Chunk
		var id int64
		var ct string
		if err := rows.Scan(&ch.Title, &ch.Content, &id, &ct); err != nil {
			rows.Close()
			return nil, err
		}
		ch.HasCode = ct == string(store.Code)
		byID[id] = append(byID[id], ch)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = c.db.Query("SELECT id, label, indexed_at FROM sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tsSource
	for rows.Next() {
		var id int64
		var s tsSource
		if err := rows.Scan(&id, &s.label, &s.indexedAt); err != nil {
			return nil, err
		}
		s.chunks = byID[id]
		out = append(out, s)
	}
	return out, rows.Err()
}
