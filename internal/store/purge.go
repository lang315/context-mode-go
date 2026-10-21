package store

import "database/sql"

// PurgeSession deletes one session's session_events and session_meta rows
// and returns how many rows went. Port of the session scope of purgeSession
// (src/session/purge.ts:196-275); chunks carry no session id here. TS had one
// DB per project; here the DB is shared, so only project's rows can go.
func (s *Store) PurgeSession(sessionID, project string) (rows int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM session_events WHERE session_id = ? AND project = ?",
		"DELETE FROM session_meta WHERE session_id = ? AND project = ?",
	} {
		n, err := execCount(tx, q, sessionID, project)
		if err != nil {
			return 0, err
		}
		rows += n
	}
	return rows, tx.Commit()
}

// PurgeProject deletes every source of project with its porter and trigram
// chunks, the events and meta rows of the project's sessions, and the
// project's tool_stats rows and stats_totals row (stats spec §4). The shared
// vocabulary is kept. Returns the sources, sessions (meta rows) and tool_stats
// rows deleted.
// This replaces the TS project scope, which unlinked the per-project DB files
// (purge.ts:277-338); cmgo's DB is shared, so only this project's rows go.
func (s *Store) PurgeProject(project string) (sources, sessions, stats int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	ids := "SELECT id FROM sources WHERE project = ?"
	if _, err := tx.Exec("DELETE FROM chunks WHERE source_id IN ("+ids+")", project); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM chunks_trigram WHERE source_id IN ("+ids+")", project); err != nil {
		return 0, 0, 0, err
	}
	if sources, err = execCount(tx, "DELETE FROM sources WHERE project = ?", project); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM session_events WHERE project = ? OR session_id IN (SELECT session_id FROM session_meta WHERE project = ?)",
		project, project); err != nil {
		return 0, 0, 0, err
	}
	if sessions, err = execCount(tx, "DELETE FROM session_meta WHERE project = ?", project); err != nil {
		return 0, 0, 0, err
	}
	if stats, err = execCount(tx, "DELETE FROM tool_stats WHERE project = ?", project); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM stats_totals WHERE project = ?", project); err != nil {
		return 0, 0, 0, err
	}
	return sources, sessions, stats, tx.Commit()
}

// execCount runs a statement and returns its RowsAffected.
func execCount(tx *sql.Tx, query string, args ...any) (int64, error) {
	res, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
