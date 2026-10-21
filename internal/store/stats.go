package store

// ToolStat is one tool's summed tool_stats counters.
type ToolStat struct {
	Tool                            string
	Calls, BytesReturned, BytesKept int64
}

// AddToolStat records one finished tool call of a cmgo serve process: calls+1
// and the call's bytes are added, updated_at becomes now. The row's window is
// the started_at of the newest session of ccPID in project (” when there is
// none, or ccPID is 0), so after a /clear the calls go to new rows (stats
// spec §1.2). The first call of a (serverID, tool, window) inserts the row
// with startedAt, the server start. One statement: the window is read in the
// same transaction as the write, and a call costs one write.
func (s *Store) AddToolStat(serverID string, ccPID int, project, startedAt, tool string, returned, kept int64) error {
	_, err := s.db.Exec(`INSERT INTO tool_stats(server_id, cc_pid, project, tool, calls, bytes_returned, bytes_kept, started_at, window, updated_at)
VALUES(?1, ?2, ?3, ?4, 1, ?5, ?6, ?7, CASE WHEN ?2 = 0 THEN '' ELSE COALESCE((`+newestSession+`), '') END, datetime('now'))
ON CONFLICT(server_id, tool, window) DO UPDATE SET calls = calls + 1,
  bytes_returned = bytes_returned + excluded.bytes_returned,
  bytes_kept = bytes_kept + excluded.bytes_kept,
  updated_at = excluded.updated_at`,
		serverID, ccPID, project, tool, returned, kept, startedAt)
	return err
}

// newestSession selects the started_at of the newest session of Claude Code
// pid ?2 in project ?3: the window a server write belongs to.
const newestSession = `SELECT started_at FROM session_meta WHERE cc_pid = ?2 AND project = ?3
ORDER BY started_at DESC, rowid DESC LIMIT 1`

// SetSessionCC records the Claude Code pid that owns a session (stats spec
// §1.2). A session not seen yet gets its session_meta row, as InsertEvents'
// upsert would create it.
func (s *Store) SetSessionCC(sessionID, project string, ccPID int) error {
	_, err := s.db.Exec(`INSERT INTO session_meta(session_id, project, started_at, last_event_at, cc_pid)
VALUES(?, ?, datetime('now'), datetime('now'), ?)
ON CONFLICT(session_id) DO UPDATE SET cc_pid = excluded.cc_pid`, sessionID, project, ccPID)
	return err
}
