package store

import (
	"database/sql"
	"fmt"
)

// SessionEventsForSearch returns the rows of project whose data or category
// contains every whitespace-separated term of query (SQLite LIKE: ASCII
// case folding, % and _ literal). category must equal source when source is
// not "". Newest first (id DESC), at most limit rows. A query with no terms
// matches every row, as TS's LIKE '%%' did.
// TS searchEvents (db.ts:1387-1421) matched the whole query as one substring
// and kept the oldest rows; spec §3.2 splits terms and keeps the newest.
func (s *Store) SessionEventsForSearch(project, query, source string, limit int) ([]StoredEvent, error) {
	where := "project = ?"
	args := []any{project}
	for _, t := range jsFields(query) {
		where += ` AND (data LIKE ? ESCAPE '\' OR category LIKE ? ESCAPE '\')`
		args = append(args, likeParam(t), likeParam(t))
	}
	if source != "" {
		where += " AND category = ?"
		args = append(args, source)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT id, session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at
FROM session_events WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// scanEvents reads full session_events rows in query order and closes rows.
func scanEvents(rows *sql.Rows) ([]StoredEvent, error) {
	defer rows.Close()
	var out []StoredEvent
	for rows.Next() {
		var e StoredEvent
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Project, &e.Type, &e.Category, &e.Priority,
			&e.Data, &e.DataHash, &e.SourceHook, &e.BytesAvoided, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CompactSession records a compaction (spec §2.1) in one transaction: it
// counts the session's events (n) and its file-category events (files),
// inserts the compaction_summary event with InsertEvents' dedup and cap,
// and increments compact_count. A session with no events is left untouched,
// as TS precompact.mjs:47 skips it.
func (s *Store) CompactSession(sessionID, project string) (n, files int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow("SELECT count(*), count(CASE WHEN category = 'file' THEN 1 END) FROM session_events WHERE session_id = ?",
		sessionID).Scan(&n, &files); err != nil {
		return 0, 0, err
	}
	if n == 0 {
		return 0, 0, nil
	}
	ev := Event{Type: "compaction_summary", Category: "compaction", Priority: 1,
		Data: fmt.Sprintf("Session compacted. %d events, %d files touched.", n, files)}
	if _, err := insertEvents(tx, sessionID, project, "PreCompact", []Event{ev}); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("UPDATE session_meta SET compact_count = compact_count + 1 WHERE session_id = ?", sessionID); err != nil {
		return 0, 0, err
	}
	return n, files, tx.Commit()
}
