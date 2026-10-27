package store

import "time"

// BashCompactTool is the tool_stats name of a Bash result cmgo-hook
// compacted (bash-compact spec §2).
const BashCompactTool = "bash-compact"

// BashSourceRetention is how long a compacted Bash result stays searchable:
// it is useful only during its session (bash-compact spec §2).
const BashSourceRetention = 48 * time.Hour

// IndexBashOutput stores a compacted Bash result in one transaction: the
// (project, label) source without vocabulary words (Bash output is mostly
// hashes, paths and numbers, and vocabulary is never pruned), and one call of
// BashCompactTool for the session's window in tool_stats and in stats_totals.
// The caller caps kept (MaxKeptPerCall).
func (s *Store) IndexBashOutput(project, label, sessionID string, chunks []Chunk, returned, kept int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM chunks WHERE source_id IN (SELECT id FROM sources WHERE project = ? AND label = ?)",
		"DELETE FROM chunks_trigram WHERE source_id IN (SELECT id FROM sources WHERE project = ? AND label = ?)",
		"DELETE FROM sources WHERE project = ? AND label = ?",
	} {
		if _, err := tx.Exec(q, project, label); err != nil {
			return err
		}
	}
	if _, _, err := insertSource(tx, project, label, "", chunks, false); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO tool_stats(server_id, cc_pid, project, tool, calls, bytes_returned, bytes_kept, started_at, window, updated_at)
VALUES('hook', 0, ?1, ?2, 1, ?3, ?4, datetime('now'), ?5, datetime('now'))
ON CONFLICT(server_id, tool, window) DO UPDATE SET calls = calls + 1,
  bytes_returned = bytes_returned + excluded.bytes_returned,
  bytes_kept = bytes_kept + excluded.bytes_kept,
  updated_at = excluded.updated_at`,
		project, BashCompactTool, returned, kept, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO stats_totals(project, calls, bytes_returned, bytes_kept, first_at) VALUES(?1, 1, ?2, ?3, datetime('now'))
ON CONFLICT(project) DO UPDATE SET calls = calls + 1, bytes_returned = bytes_returned + ?2, bytes_kept = bytes_kept + ?3`,
		project, returned, kept); err != nil {
		return err
	}
	return tx.Commit()
}
