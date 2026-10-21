package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

// Event is one session event a hook records (hooks spec §3).
type Event struct {
	Type, Category string
	Priority       int // 1 most important .. 4 least
	Data           string
	BytesAvoided   int64
}

// StoredEvent is a session_events row.
type StoredEvent struct {
	ID                                 int64
	SessionID, Project, Type, Category string
	Priority                           int
	Data, DataHash, SourceHook         string
	BytesAvoided                       int64
	CreatedAt                          string // 'YYYY-MM-DD HH:MM:SS' UTC
}

const (
	MaxEventsPerSession = 1000               // DB:639
	EventDedupWindow    = 5                  // DB:642
	MaxEventData        = 32 << 10           // bytes, cut at a UTF-8 boundary
	SessionRetention    = 7 * 24 * time.Hour // DB:1696 cleanupOldSessions default
)

// EventDataHash ports the dedup hash of DB:1145: sha256 hex, first 16, upper case.
func EventDataHash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return strings.ToUpper(hex.EncodeToString(sum[:8]))
}

// cutData shortens data to MaxEventData bytes without splitting a rune.
func cutData(data string) string {
	if len(data) <= MaxEventData {
		return data
	}
	i := MaxEventData
	for i > 0 && !utf8.RuneStart(data[i]) {
		i--
	}
	return data[:i]
}

// InsertEvents implements spec §3.2 in one transaction and returns the number
// of rows inserted. An empty evs still runs the session_meta upsert, so
// SessionStart can register a session.
func (s *Store) InsertEvents(sessionID, project, sourceHook string, evs []Event) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_meta(session_id, project, started_at, last_event_at)
VALUES(?, ?, datetime('now'), datetime('now'))`, sessionID, project); err != nil {
		return 0, err
	}
	inserted := 0
	for _, ev := range evs {
		data := cutData(ev.Data)
		if _, err := tx.Exec(`INSERT INTO session_events(session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			sessionID, project, ev.Type, ev.Category, ev.Priority, data, EventDataHash(data), sourceHook, ev.BytesAvoided); err != nil {
			return 0, err
		}
		inserted++
	}
	if _, err := tx.Exec("UPDATE session_meta SET last_event_at = datetime('now'), event_count = event_count + ? WHERE session_id = ?",
		inserted, sessionID); err != nil {
		return 0, err
	}
	return inserted, tx.Commit()
}

// SessionEvents returns a session's rows ordered by id.
func (s *Store) SessionEvents(sessionID string) ([]StoredEvent, error) {
	rows, err := s.db.Query(`SELECT id, session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at
FROM session_events WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
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
