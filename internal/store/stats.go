package store

import (
	"database/sql"
	"errors"
	"time"
)

// ToolStat is one tool's summed tool_stats counters.
type ToolStat struct {
	Tool                            string
	Calls, BytesReturned, BytesKept int64
	Unmeasured                      int64 // ctx_execute calls whose kept bytes could not be counted
}

// AddToolStat records one finished tool call of a cmgo serve process: calls+1
// and the call's bytes are added, updated_at becomes now. The row's window is
// the id of the session most recently linked to ccPID in project (empty when
// there is none, or ccPID is 0), so after a /clear or /resume the calls go to
// that session's rows (stats spec §1.2). The first call of a (serverID, tool,
// window) inserts the row with startedAt, the server start. One statement:
// the window is read in the same transaction as the write. The project's
// stats_totals row, the running lifetime sums the statusline reads, is
// updated in that transaction too.
func (s *Store) AddToolStat(serverID string, ccPID int, project, startedAt, tool string, returned, kept int64) error {
	return s.AddToolStatExec(serverID, ccPID, project, startedAt, tool, returned, kept, 0)
}

// AddToolStatExec is AddToolStat for a call that may not be measurable:
// unmeasured (0 or 1) is added to the row's unmeasured_calls.
func (s *Store) AddToolStatExec(serverID string, ccPID int, project, startedAt, tool string, returned, kept, unmeasured int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO tool_stats(server_id, cc_pid, project, tool, calls, bytes_returned, bytes_kept, started_at, window, updated_at, unmeasured_calls)
VALUES(?1, ?2, ?3, ?4, 1, ?5, ?6, ?7, CASE WHEN ?2 = 0 THEN '' ELSE COALESCE((`+newestSession+`), '') END, datetime('now'), ?8)
ON CONFLICT(server_id, tool, window) DO UPDATE SET calls = calls + 1,
  bytes_returned = bytes_returned + excluded.bytes_returned,
  bytes_kept = bytes_kept + excluded.bytes_kept,
  unmeasured_calls = unmeasured_calls + excluded.unmeasured_calls,
  updated_at = excluded.updated_at`,
		serverID, ccPID, project, tool, returned, kept, startedAt, unmeasured); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO stats_totals(project, calls, bytes_returned, bytes_kept, first_at) VALUES(?1, 1, ?2, ?3, ?4)
ON CONFLICT(project) DO UPDATE SET calls = calls + 1, bytes_returned = bytes_returned + ?2, bytes_kept = bytes_kept + ?3`,
		project, returned, kept, startedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// newestSession selects the id of the session most recently linked to Claude
// Code pid ?2 in project ?3: the window a server write belongs to. rowid
// breaks a tie of linked_at (milliseconds) in favour of the newer row.
const newestSession = `SELECT session_id FROM session_meta WHERE cc_pid = ?2 AND project = ?3
ORDER BY linked_at DESC, rowid DESC LIMIT 1`

// linkedNow is the linked_at value of a link made now. Both link paths use it:
// newestSession orders sessions by it, so they must write the same format.
const linkedNow = `strftime('%Y-%m-%d %H:%M:%f', 'now')`

// SetSessionCC records the Claude Code pid that owns a session and when it was
// linked (stats spec §1.2). Every SessionStart source calls it, so /clear and
// /resume re-link in the same process. A session not seen yet gets its
// session_meta row, as InsertEvents' upsert would create it.
func (s *Store) SetSessionCC(sessionID, project string, ccPID int) error {
	_, err := s.db.Exec(`INSERT INTO session_meta(session_id, project, started_at, last_event_at, cc_pid, linked_at)
VALUES(?1, ?2, datetime('now'), datetime('now'), ?3, `+linkedNow+`)
ON CONFLICT(session_id) DO UPDATE SET cc_pid = excluded.cc_pid, linked_at = excluded.linked_at`, sessionID, project, ccPID)
	return err
}

// InsertEventsAndLink is InsertEvents that also links a session no cmgo
// SessionStart has seen, such as one started before cmgo was installed, in
// the same transaction (stats spec §1.2). findClaude returns the Claude Code
// pid and when that process started; it runs only while the session's
// linked_at is empty, so once per session, also when it finds none (cc_pid
// stays 0, unknown). A linked session is left alone: its linked_at picks the
// window.
func (s *Store) InsertEventsAndLink(sessionID, project, sourceHook string, evs []Event, findClaude func() (int, time.Time)) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	inserted, linkedAt, err := insertEvents(tx, sessionID, project, sourceHook, evs)
	if err != nil {
		return 0, err
	}
	if linkedAt == "" {
		cc, start := findClaude()
		if err := linkLate(tx, sessionID, project, cc, start); err != nil {
			return 0, err
		}
	}
	return inserted, tx.Commit()
}

// linkLate records cc as the session's Claude Code pid, and sets linked_at
// even when cc is 0, so the walk does not run again. The rows that process's
// servers wrote for the project before the link, with an empty window, then
// move to the session. Only servers started at or after ccStart are this
// process's, so the rows of an earlier process that had the same pid stay
// put; a zero ccStart (unknown) takes every row. No row of the session's
// window can exist yet: a server writes that window only once the session is
// linked to its pid.
func linkLate(tx *sql.Tx, sessionID, project string, cc int, ccStart time.Time) error {
	if _, err := tx.Exec(`UPDATE session_meta SET cc_pid = ?, linked_at = `+linkedNow+` WHERE session_id = ?`,
		cc, sessionID); err != nil {
		return err
	}
	if cc == 0 {
		return nil
	}
	// started_at has whole seconds, so ccStart is cut to the second as well.
	_, err := tx.Exec("UPDATE tool_stats SET window = ? WHERE cc_pid = ? AND project = ? AND window = '' AND started_at >= ?",
		sessionID, cc, project, ccStart.UTC().Format(sqliteTime))
	return err
}

// StatsQuery identifies the cmgo serve process asking for ctx_stats.
type StatsQuery struct {
	ServerID string
	CCPID    int // 0 when serve found no Claude Code ancestor
	Project  string
}

// StatsSnapshot is everything the ctx_stats report shows except the version
// and the clock (stats spec §1.2, §2).
type StatsSnapshot struct {
	SessionID     string
	SessionLinked bool
	SessionStart  string     // 'YYYY-MM-DD HH:MM:SS' UTC; "" when not linked
	Tools         []ToolStat // linked session's rows, else this server's rows; by tool name
	Raw           int64      // bytes processed: Kept + Returned over the Tools that have a raw size
	// Redirects and RedirectBytes are the linked session's "redirect" events:
	// their count and SUM(bytes_avoided).
	Redirects, RedirectBytes int64 // read-redirected tips are not redirects: the Read still runs
	Events, Compactions      int
	Categories               map[string]int // event count per category; nil when not linked
	ProjectSources           int
	ProjectContentBytes      int64 // UTF-8 bytes of the project's chunk contents
	ProjectSessions          int
	ProjectEvents            int
	LifetimeKept             int64 // every tool_stats bytes_kept (stats_totals): measured bytes only
	LifetimeUnmeasured       int64 // every tool_stats unmeasured_calls
	LifetimeSessions         int
	LifetimeSince            string // earliest tool_stats started_at; "" when there is none
}

// rawTools are the tools whose calls have a raw size (stats spec §1.3), plus bash-compact; the
// others (ctx_search, ctx_purge, ctx_stats) only return bytes.
var rawTools = map[string]bool{"ctx_execute": true, "ctx_execute_file": true, "ctx_batch_execute": true, "ctx_fetch_and_index": true, BashCompactTool: true}

// rawBytes is the bytes processed by tools: what they kept plus what they returned.
func rawBytes(tools []ToolStat) int64 {
	var n int64
	for _, t := range tools {
		if rawTools[t.Tool] {
			n += t.BytesKept + t.BytesReturned
		}
	}
	return n
}

// lifetimeKept is the all-projects kept-out total (stats spec §2 "Lifetime"):
// the stats_totals rows, one per project. Only measured sandbox bytes: the
// hook's redirect estimates in session_events are not part of it.
const lifetimeKept = `(SELECT COALESCE(SUM(bytes_kept), 0) FROM stats_totals)`

// Stats gathers the ctx_stats numbers. The caller's session is the one most
// recently linked to its Claude Code process in this project; its server rows
// are those whose window is its id, so a /clear starts from zero and a
// /resume continues the resumed session's count (stats spec §1.2). With no
// linked session the server's own rows, all windows, are shown.
func (s *Store) Stats(q StatsQuery) (StatsSnapshot, error) {
	var st StatsSnapshot
	// cc_pid 0 means unknown, and every session_meta row older than
	// migration 3 has it: never link on it.
	if q.CCPID != 0 {
		err := s.db.QueryRow(`SELECT session_id, started_at FROM session_meta WHERE cc_pid = ? AND project = ?
ORDER BY linked_at DESC, rowid DESC LIMIT 1`, q.CCPID, q.Project).Scan(&st.SessionID, &st.SessionStart)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return st, err
		}
		st.SessionLinked = err == nil
	}
	var err error
	if st.SessionLinked {
		st.Tools, err = s.toolStats("window = ?", st.SessionID)
	} else {
		st.Tools, err = s.toolStats("server_id = ?", q.ServerID)
	}
	if err != nil {
		return st, err
	}
	st.Raw = rawBytes(st.Tools)
	if st.SessionLinked {
		if err := s.db.QueryRow(`SELECT count(*), COALESCE(SUM(bytes_avoided), 0) FROM session_events
WHERE session_id = ? AND category = 'redirect' AND type <> 'read-redirected'`, st.SessionID).Scan(&st.Redirects, &st.RedirectBytes); err != nil {
			return st, err
		}
		if err := s.db.QueryRow("SELECT compact_count FROM session_meta WHERE session_id = ?", st.SessionID).Scan(&st.Compactions); err != nil {
			return st, err
		}
		if st.Categories, st.Events, err = s.categories(st.SessionID); err != nil {
			return st, err
		}
	}
	err = s.db.QueryRow(`SELECT (SELECT count(*) FROM sources WHERE project = ?1),
  (SELECT COALESCE(SUM(length(CAST(content AS BLOB))), 0) FROM chunks WHERE source_id IN (SELECT id FROM sources WHERE project = ?1)),
  (SELECT count(*) FROM session_meta WHERE project = ?1),
  (SELECT count(*) FROM session_events WHERE project = ?1),
  `+lifetimeKept+`,
  (SELECT count(*) FROM session_meta),
  (SELECT COALESCE(MIN(first_at), '') FROM stats_totals),
  (SELECT COALESCE(SUM(unmeasured_calls), 0) FROM tool_stats)`, q.Project).Scan(
		&st.ProjectSources, &st.ProjectContentBytes, &st.ProjectSessions, &st.ProjectEvents,
		&st.LifetimeKept, &st.LifetimeSessions, &st.LifetimeSince, &st.LifetimeUnmeasured)
	return st, err
}

// toolStats sums tool_stats rows matching where, one ToolStat per tool.
func (s *Store) toolStats(where string, args ...any) ([]ToolStat, error) {
	rows, err := s.db.Query("SELECT tool, SUM(calls), SUM(bytes_returned), SUM(bytes_kept), SUM(unmeasured_calls) FROM tool_stats WHERE "+where+
		" GROUP BY tool ORDER BY tool", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolStat
	for rows.Next() {
		var t ToolStat
		if err := rows.Scan(&t.Tool, &t.Calls, &t.BytesReturned, &t.BytesKept, &t.Unmeasured); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// categories counts a session's events per category, and in total.
func (s *Store) categories(sessionID string) (map[string]int, int, error) {
	rows, err := s.db.Query("SELECT category, count(*) FROM session_events WHERE session_id = ? GROUP BY category", sessionID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	cats, total := map[string]int{}, 0
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, 0, err
		}
		cats[name] = n
		total += n
	}
	return cats, total, rows.Err()
}

// SourceBytes is the UTF-8 size of a source's chunk contents: the raw bytes a
// ctx_fetch_and_index cache hit keeps out (stats spec §1.3).
// shortcut: source_id is UNINDEXED in FTS5, so this scans every chunk; add a
// bytes column to sources if cache hits on a large knowledge base get slow.
func (s *Store) SourceBytes(sourceID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow("SELECT COALESCE(SUM(length(CAST(content AS BLOB))), 0) FROM chunks WHERE source_id = ?", sourceID).Scan(&n)
	return n, err
}
