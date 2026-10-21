package store

import (
	"fmt"
	"strings"
	"testing"
)

func TestImportEventsKeepsColumnsAndCutsData(t *testing.T) {
	s := openTest(t)
	big := strings.Repeat("é", MaxEventData) // 2 bytes per rune: cut to MaxEventData bytes
	rows := []ImportedEvent{
		{Type: "file_read", Category: "file", Priority: 1, Data: "/a.go", SourceHook: "PostToolUse", BytesAvoided: 42, CreatedAt: "2026-10-20 10:00:00"},
		{Type: "user_prompt", Category: "prompt", Priority: 3, Data: big, SourceHook: "UserPromptSubmit", CreatedAt: "2026-10-20 10:00:05"},
	}
	sess := ImportedSession{ID: "s1", Project: "/p", StartedAt: "2026-10-20 09:59:00", LastEventAt: "2026-10-20 10:00:05", CompactCount: 2}
	ins, dupes, err := s.ImportEvents(sess, rows)
	if err != nil || ins != 2 || dupes != 0 {
		t.Fatalf("ImportEvents = %d, %d, %v; want 2, 0, nil", ins, dupes, err)
	}
	got, err := s.SessionEvents("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	e := got[0]
	if e.Project != "/p" || e.Type != "file_read" || e.Category != "file" || e.Priority != 1 || e.Data != "/a.go" ||
		e.SourceHook != "PostToolUse" || e.BytesAvoided != 42 || e.CreatedAt != "2026-10-20 10:00:00" || e.DataHash != EventDataHash("/a.go") {
		t.Fatalf("row 0 = %+v", e)
	}
	if len(got[1].Data) != MaxEventData || got[1].DataHash != EventDataHash(got[1].Data) {
		t.Fatalf("row 1 data len %d hash %s, want cut to %d with its own hash", len(got[1].Data), got[1].DataHash, MaxEventData)
	}
	var started, last string
	var count, compact, cc int
	if err := s.db.QueryRow("SELECT started_at, last_event_at, event_count, compact_count, cc_pid FROM session_meta WHERE session_id = 's1'").
		Scan(&started, &last, &count, &compact, &cc); err != nil {
		t.Fatal(err)
	}
	if started != sess.StartedAt || last != sess.LastEventAt || count != 2 || compact != 2 || cc != 0 {
		t.Fatalf("meta = %s %s %d %d %d", started, last, count, compact, cc)
	}
}

func TestImportEventsBypassesDedupWindowAndCap(t *testing.T) {
	s := openTest(t)
	n := MaxEventsPerSession + 5
	rows := make([]ImportedEvent, n)
	for i := range rows {
		// Same type and data: InsertEvents would keep one; only created_at differs.
		rows[i] = ImportedEvent{Type: "cwd", Category: "cwd", Priority: 2, Data: "/x", SourceHook: "PostToolUse",
			CreatedAt: fmt.Sprintf("2026-10-20 %02d:%02d:%02d", i/3600, i/60%60, i%60)}
	}
	sess := ImportedSession{ID: "s1", Project: "/p", StartedAt: rows[0].CreatedAt, LastEventAt: rows[n-1].CreatedAt}
	ins, dupes, err := s.ImportEvents(sess, rows)
	if err != nil || ins != n || dupes != 0 {
		t.Fatalf("ImportEvents = %d, %d, %v; want %d, 0, nil", ins, dupes, err, n)
	}
}

func TestImportEventsSkipsDuplicatesAndMergesMeta(t *testing.T) {
	s := openTest(t)
	if _, err := s.InsertEvents("s1", "/cmgo-project", "PostToolUse", []Event{{Type: "git", Category: "git", Priority: 2, Data: "status"}}); err != nil {
		t.Fatal(err)
	}
	_, _, cmgoLast, _ := metaRow(t, s, "s1")
	row := ImportedEvent{Type: "file_read", Category: "file", Priority: 1, Data: "/a.go", SourceHook: "PostToolUse", CreatedAt: "2020-01-01 00:00:00"}
	sess := ImportedSession{ID: "s1", Project: "/ts-project", StartedAt: "2020-01-01 00:00:00", LastEventAt: "2020-01-01 00:00:00", CompactCount: 3}

	ins, dupes, err := s.ImportEvents(sess, []ImportedEvent{row})
	if err != nil || ins != 1 || dupes != 0 {
		t.Fatalf("first import = %d, %d, %v; want 1, 0, nil", ins, dupes, err)
	}
	// A re-run (import --force) finds the row from the earlier run: it inserts nothing and leaves event_count alone.
	ins, dupes, err = s.ImportEvents(sess, []ImportedEvent{row})
	if err != nil || ins != 0 || dupes != 1 {
		t.Fatalf("second import = %d, %d, %v; want 0, 1, nil", ins, dupes, err)
	}
	project, started, last, count := metaRow(t, s, "s1")
	if project != "/cmgo-project" || started != "2020-01-01 00:00:00" || last != cmgoLast || count != 2 {
		t.Fatalf("merged meta = %q %q %q %d; want existing project, older start, newer last, 2 events", project, started, last, count)
	}
	var compact int
	if err := s.db.QueryRow("SELECT compact_count FROM session_meta WHERE session_id = 's1'").Scan(&compact); err != nil || compact != 3 {
		t.Fatalf("compact_count = %d, %v; want 3", compact, err)
	}
}

func TestImportSource(t *testing.T) {
	s := openTest(t)
	chunks := []Chunk{
		{Title: "Pooling", Content: "connection pooling with pgbouncer"},
		{Title: "Dial", Content: "func Dial() {}", HasCode: true},
	}
	ok, err := s.ImportSource("/p", "docs::https://x", "2026-10-15 08:00:00", chunks)
	if err != nil || !ok {
		t.Fatalf("ImportSource = %v, %v; want true, nil", ok, err)
	}
	src, err := s.SourceByLabel("/p", "docs::https://x")
	if err != nil || src == nil {
		t.Fatalf("SourceByLabel = %v, %v", src, err)
	}
	if src.ChunkCount != 2 || src.CodeChunkCount != 1 || src.IndexedAt.Format(sqliteTime) != "2026-10-15 08:00:00" {
		t.Fatalf("source = %+v", src)
	}
	res, err := s.SearchWithFallback("pgbouncer", SearchOpts{Project: "/p", Limit: 5})
	if err != nil || len(res) != 1 || res[0].Title != "Pooling" {
		t.Fatalf("search = %+v, %v", res, err)
	}
	var tri, vocab int
	s.db.QueryRow("SELECT count(*) FROM chunks_trigram WHERE source_id = ?", src.ID).Scan(&tri)
	s.db.QueryRow("SELECT count(*) FROM vocabulary WHERE word = 'pgbouncer'").Scan(&vocab)
	if tri != 2 || vocab != 1 {
		t.Fatalf("trigram rows %d, vocabulary hit %d; want 2, 1", tri, vocab)
	}

	// An existing (project, label) wins, whether imported or indexed by cmgo.
	ok, err = s.ImportSource("/p", "docs::https://x", "2026-10-16 08:00:00", chunks[:1])
	if err != nil || ok {
		t.Fatalf("second ImportSource = %v, %v; want false, nil", ok, err)
	}
	if _, err := s.Index("/p", "mine", chunks[:1]); err != nil {
		t.Fatal(err)
	}
	if ok, err = s.ImportSource("/p", "mine", "2026-10-16 08:00:00", chunks); err != nil || ok {
		t.Fatalf("ImportSource over Index = %v, %v; want false, nil", ok, err)
	}
	if n, _ := s.ChunkCount("/p"); n != 3 {
		t.Fatalf("ChunkCount = %d, want 3", n)
	}
}

func TestMeta(t *testing.T) {
	s := openTest(t)
	if v, ok, err := s.Meta("ts_import"); err != nil || ok || v != "" {
		t.Fatalf("missing Meta = %q, %v, %v", v, ok, err)
	}
	for _, want := range []string{"a", "b"} {
		if err := s.SetMeta("ts_import", want); err != nil {
			t.Fatal(err)
		}
		if v, ok, err := s.Meta("ts_import"); err != nil || !ok || v != want {
			t.Fatalf("Meta = %q, %v, %v; want %q", v, ok, err, want)
		}
	}
}

// A repeat inside one source DB is a real event and is kept; the same rows
// from a second source DB, or from an earlier import, are duplicates.
func TestImportEventsKeepsRepeatsWithinOneSource(t *testing.T) {
	s := openTest(t)
	row := ImportedEvent{Type: "file_read", Category: "file", Priority: 1, Data: "/a.go", SourceHook: "PostToolUse", CreatedAt: "2026-10-20 10:00:00"}
	sess := ImportedSession{ID: "s1", Project: "/p", StartedAt: row.CreatedAt, LastEventAt: row.CreatedAt}

	ins, dupes, err := s.ImportEvents(sess, []ImportedEvent{row, row})
	if err != nil || ins != 2 || dupes != 0 {
		t.Fatalf("one DB with two identical rows = %d, %d, %v; want 2, 0, nil", ins, dupes, err)
	}
	// The same rows from a second DB, and one new row.
	other := row
	other.Data = "/b.go"
	ins, dupes, err = s.ImportEvents(sess, []ImportedEvent{row, row}, []ImportedEvent{row, other})
	if err != nil || ins != 1 || dupes != 3 {
		t.Fatalf("second DB = %d, %d, %v; want 1, 3, nil", ins, dupes, err)
	}
	if got, _ := s.SessionEvents("s1"); len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
}
