package store

import (
	"database/sql"
	"errors"
)

// ImportedSession is the session_meta of a session copied from TS
// context-mode (import spec §3.3). Times are 'YYYY-MM-DD HH:MM:SS' UTC.
type ImportedSession struct {
	ID, Project            string
	StartedAt, LastEventAt string
	CompactCount           int
}

// ImportedEvent is one TS session_events row, copied verbatim.
type ImportedEvent struct {
	Type, Category   string
	Priority         int
	Data, SourceHook string
	BytesAvoided     int64
	CreatedAt        string
}

// ImportEvents writes the batches in order in one transaction, bypassing the
// dedup window and the per-session cap, but cutting data to MaxEventData and
// recomputing data_hash. A batch is the rows of one source DB. A row whose
// (session_id, type, data_hash, created_at) already exists from an earlier
// batch or an earlier import is a duplicate and is skipped, so a second import
// of the same data inserts nothing; repeats inside one batch are kept, as TS
// stored them. session_meta is merged: the earliest start, the latest
// activity, event_count plus the rows inserted, the larger compact_count; an
// existing row keeps its project and cc_pid.
func (s *Store) ImportEvents(sess ImportedSession, batches ...[]ImportedEvent) (inserted, dupes int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	for _, rows := range batches {
		hashes := make([]string, len(rows))
		for i, r := range rows {
			hashes[i] = EventDataHash(cutData(r.Data))
		}
		seen, err := existingEvents(tx, sess.ID) // read before the batch writes, so its own repeats count as new
		if err != nil {
			return 0, 0, err
		}
		for i, r := range rows {
			if _, dup := seen[eventKey{r.Type, hashes[i], r.CreatedAt}]; dup {
				dupes++
				continue
			}
			if _, err := tx.Exec(`INSERT INTO session_events(session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				sess.ID, sess.Project, r.Type, r.Category, r.Priority, cutData(r.Data), hashes[i], r.SourceHook, r.BytesAvoided, r.CreatedAt); err != nil {
				return 0, 0, err
			}
			inserted++
		}
	}
	if _, err := tx.Exec(`INSERT INTO session_meta(session_id, project, started_at, last_event_at, event_count, compact_count)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
  started_at = min(started_at, excluded.started_at),
  last_event_at = max(last_event_at, excluded.last_event_at),
  event_count = event_count + excluded.event_count,
  compact_count = max(compact_count, excluded.compact_count)`,
		sess.ID, sess.Project, sess.StartedAt, sess.LastEventAt, inserted, sess.CompactCount); err != nil {
		return 0, 0, err
	}
	return inserted, dupes, tx.Commit()
}

// eventKey is what makes an imported event a duplicate, within one session.
type eventKey struct{ typ, hash, createdAt string }

// existingEvents returns the (type, data_hash, created_at) of every event of the session.
func existingEvents(tx *sql.Tx, sessionID string) (map[eventKey]struct{}, error) {
	rows, err := tx.Query("SELECT type, data_hash, created_at FROM session_events WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[eventKey]struct{}{}
	for rows.Next() {
		var k eventKey
		if err := rows.Scan(&k.typ, &k.hash, &k.createdAt); err != nil {
			return nil, err
		}
		seen[k] = struct{}{}
	}
	return seen, rows.Err()
}

// ImportSource writes a TS source with its chunks as stored (no re-chunking),
// keeping label and indexedAt, the same way Index does. An existing
// (project, label) source wins: nothing is written and false is returned.
func (s *Store) ImportSource(project, label, indexedAt string, chunks []Chunk) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM sources WHERE project = ? AND label = ?", project, label).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	_, added, err := insertSource(tx, project, label, indexedAt, chunks, true)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if added > 0 { // new words can change fuzzy answers, as in Index
		s.mu.Lock()
		s.fuzzyKeys, s.fuzzyVals = nil, map[string]string{}
		s.mu.Unlock()
	}
	return true, nil
}

// Meta returns the meta value of key and whether it exists.
func (s *Store) Meta(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetMeta sets the meta value of key.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec("INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}
