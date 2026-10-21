# cmgo doctor, upgrade and TS import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish cmgo with:
- `cmgo doctor` / `ctx_doctor`: a health report in the TS `[OK]/[WARN]/[FAIL]` format;
- `cmgo upgrade` / `ctx_upgrade`: rebuild from source, then refresh the Claude Code registration;
- `cmgo import`: a one-time copy of the TS context-mode data that is still inside cmgo's retention windows.

**Architecture:**
- **`internal/tsimport` (new):** copies each TS SQLite file (with `-wal` and `-shm`) into a temp dir and reads the copy. Writes go through new store import functions that bypass dedup and cap but reuse `Index`'s insert path.
- **`internal/doctor` (new) and `internal/upgrade` (new):** keep every external effect injectable.
- **`internal/buildinfo` (new):** resolves the version for both binaries.
- **MCP server:** gains `ctx_doctor` and `ctx_upgrade`, 9 tools in all. The routing block now lists every TS `<ctx_commands>` entry.

**Tech Stack:**
- Go 1.27.2, existing dependencies only.
- Python 3 for the existing benches.

**Spec:** `docs/superpowers/specs/2026-10-21-cmgo-doctor-import-design.md` (sub-project 5 of 5). It builds on the four earlier specs.

**How the code in this plan was produced:**
- Every code block was built and tested in a draft module on top of the finished sub-project 4 branch, then copied in here verbatim.
- The assembled plan was replayed task by task, Task 1 through 17, into a fresh worktree of `sp4-stats`. After each task, `go vet` and that task's package tests ran. Appendix B has the result.
- Each code block is the complete file named on the line above it. When a later task shows a file again, replace the whole file.

## Global Constraints

- **Repo and branch:** the repo is `~/GolandProjects/github.com/lang315/context-mode-go`. Work on branch `sp5-doctor-import`, stacked on `sp4-stats`. `go.mod` is unchanged.
- **Platforms:** macOS and Linux only. Verified on macOS arm64.
- **Tests:**
  - Use `t.TempDir()`.
  - Never write to the real `~/.claude`, `~/.claude.json`, `~/.claude/context-mode` or the TS repo.
  - Never run a real `go install`, `git pull` or `claude` from a test. The upgrade runner is injected.
- **Import never touches the TS originals:** it opens only copies under `$CMGO_DIR/run/import-*`, and a test compares sha256 of every source file before and after.
- **Import respects retention:** sessions within `store.SessionRetention` (7 days), sources within the 14-day source retention.
- **Doctor exit code:** `cmgo doctor` exits 1 exactly when it prints a `[FAIL]` line.
- **Upgrade output:** `cmgo upgrade` stops at the first failed step, prints `[x]` / `[ ]` lines, and runs nothing with `--dry-run`.
- **Binary boundary:** `cmd/cmgo-hook` still must not link the MCP SDK, `net/http`, HTML conversion, executor, fetch or mcpserver.
- **Process markers:** never put a literal process marker in a shell command that also runs `pkill` or `pgrep`.
- **Commits:** Conventional Commits, in English, one per task.

## Review Focus

These are the five conditions most likely to hurt someone using these commands that the spec implies but does not list. Each has a test in the task that owns the code.

1. **The TS data is imported while TS DBs still hold uncheckpointed WAL frames.** The frames must be imported, and the TS files must be byte-identical afterwards. Tests: `TestReadSessionsIncludesWALFrames` (Task 2) and `TestRunImportsFixture` (sha256 before and after, Task 3).
2. **`cmgo import` runs twice, or with `--dry-run` first.** The second run must change nothing without `--force`, and a dry run must write nothing to `cmgo.db`. Tests: `TestRunSecondRunNeedsForce` and `TestRunDryRunWritesNothing` (Task 3).
3. **Old TS schema generations:** a session DB without `project_dir`/`bytes_*`, or a 4-column FTS content DB. These must import with defaults, not fail. Tests: `TestReadSessionsOldSchemaDefaults` and `TestReadSourcesBothFTSSchemas` (Task 2).
4. **`cmgo upgrade` fails halfway**, for example `go install` fails or `git` is missing. It must stop, report which step failed, and not run `cmgo install` from a half-built binary. Tests: `TestUpgradeStopsAtFirstFailure` and `TestUpgradeBadSourceAndMissingTools` (Task 12).
5. **Doctor runs on a machine where cmgo is half installed:** some hooks missing, a stale MCP path, the TS plugin still enabled. Each must be a WARN with a fix hint, and only real breakage may be a FAIL. Tests: `TestCheckHooks`, `TestCheckMCP` and `TestCheckTSPlugin` (Task 10), and `TestReportFailures` (Task 11).

## File Structure

| Path | Responsibility |
|---|---|
| `internal/store/import.go` (+ `index.go` refactor) | `ImportEvents`, `ImportSource`, `Meta` / `SetMeta` |
| `internal/tsimport` | Copy-then-read of TS session and content DBs, mapping and retention, the `ts_import` guard |
| `internal/buildinfo` | Version resolution for both binaries |
| `internal/doctor` | Checks and the report |
| `internal/upgrade` | Upgrade steps with an injected runner |
| `internal/mcpserver/doctor.go`, `upgrade.go` | `ctx_doctor`, `ctx_upgrade` |
| `internal/routing/text.go` | Full `<ctx_commands>` block |
| `cmd/cmgo` | `doctor`, `upgrade`, `import` commands |

## Task order

| Task | Content |
|---|---|
| 1 | Store import functions and meta access |
| 2 | TS fixtures, copies and readers for both schema generations |
| 3 | Run: retention, project mapping, guard, dry run |
| 4 | cmgo import command |
| 5 | Version resolution and `cmgo-hook version` |
| 6 | Store schema version and FTS5 probe |
| 7 | Executor runtime list |
| 8 | Install status read-back |
| 9 | Doctor environment and local checks |
| 10 | Doctor settings, hook binary and MCP checks |
| 11 | Doctor report |
| 12 | Upgrade steps |
| 13 | `cmgo doctor` and `cmgo upgrade` commands |
| 14 | `ctx_doctor` and `ctx_upgrade` MCP tools |
| 15 | Routing block gains `ctx doctor` and `ctx upgrade` |
| 16 | Wire `doctor` and `upgrade` into cmgo |
| 17 | Wire `import` into cmgo |

Suggested review checkpoints, after:
- Task 4 (import);
- Task 11 (doctor building blocks);
- Task 17 (commands, tools, routing and wiring).

---

### Task 1: Store import functions and meta access

**Files:**
- Create: `internal/store/import.go`
- Modify: `internal/store/index.go`
- Test: `internal/store/import_test.go`

**Interfaces:**
- Consumes: `cutData`, `EventDataHash`, `vocabWords`, `Chunk`, `Store.SourceByLabel`, `Store.SearchWithFallback`, `Store.InsertEvents`, `Store.SessionEvents` (existing, package store)
- Produces: `type ImportedSession struct { ID, Project string; StartedAt, LastEventAt string; CompactCount int }`; `type ImportedEvent struct { Type, Category string; Priority int; Data, SourceHook string; BytesAvoided int64; CreatedAt string }`; `func (s *Store) ImportEvents(sess ImportedSession, rows []ImportedEvent) (inserted, dupes int, err error)`; `func (s *Store) ImportSource(project, label, indexedAt string, chunks []Chunk) (bool, error)`; `func (s *Store) Meta(key string) (string, bool, error)`; `func (s *Store) SetMeta(key, value string) error`; unexported `func insertSource(tx *sql.Tx, project, label, indexedAt string, chunks []Chunk) (int64, int64, error)` (`Index` keeps its signature)

- [ ] **Step 1: Write the failing test**

`internal/store/import_test.go`:

```go
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

	// The second copy of row in one batch is a duplicate (same session, type, hash, created_at).
	ins, dupes, err := s.ImportEvents(sess, []ImportedEvent{row, row})
	if err != nil || ins != 1 || dupes != 1 {
		t.Fatalf("first import = %d, %d, %v; want 1, 1, nil", ins, dupes, err)
	}
	// A re-run (import --force) inserts nothing and leaves event_count alone.
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestImport|TestMeta' -count=1 -v`
Expected: FAIL with "internal/store/import_test.go:12:12: undefined: ImportedEvent"

- [ ] **Step 3: Write minimal implementation**

`internal/store/index.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

// IndexResult reports one Index call.
type IndexResult struct {
	SourceID                int64
	Label                   string
	TotalChunks, CodeChunks int
	Sections                []Section
}

// Section is one chunk in the "## Indexed Sections" list.
type Section struct {
	Title string
	Bytes int
}

// Source is one row of sources.
type Source struct {
	ID                         int64
	Label                      string
	ChunkCount, CodeChunkCount int
	IndexedAt                  time.Time
}

// optimizeEvery: ContentStore.OPTIMIZE_EVERY (ST:463).
const optimizeEvery = 50

var vocabSplit = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`[^\p{L}\p{N}_-]+`) })

// vocabWords: lowercase, split on [^\p{L}\p{N}_-]+, keep words of at least 3
// chars that are not stopwords; unique, in first-seen order (ST:1675-1681).
func vocabWords(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range vocabSplit().Split(strings.ToLower(text), -1) {
		if u16len(w) >= 3 && !stopwords[w] && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// Index replaces the (project, label) source with chunks in one transaction
// and adds their words to the vocabulary. Port of #insertChunks
// (ST:1085-1143). Vocabulary comes from the chunk contents because the raw
// text is not passed in.
func (s *Store) Index(project, label string, chunks []Chunk) (IndexResult, error) {
	res := IndexResult{Label: label, TotalChunks: len(chunks)}
	for _, c := range chunks {
		if c.HasCode {
			res.CodeChunks++
		}
		res.Sections = append(res.Sections, Section{Title: c.Title, Bytes: len(c.Content)})
	}

	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM chunks WHERE source_id IN (SELECT id FROM sources WHERE project = ? AND label = ?)",
		"DELETE FROM chunks_trigram WHERE source_id IN (SELECT id FROM sources WHERE project = ? AND label = ?)",
		"DELETE FROM sources WHERE project = ? AND label = ?",
	} {
		if _, err := tx.Exec(q, project, label); err != nil {
			return res, err
		}
	}
	id, added, err := insertSource(tx, project, label, "", chunks)
	if err != nil {
		return res, err
	}
	res.SourceID = id
	if err := tx.Commit(); err != nil {
		return res, err
	}

	s.mu.Lock()
	if added > 0 { // new words can change fuzzy answers (ST:1694)
		s.fuzzyKeys, s.fuzzyVals = nil, map[string]string{}
	}
	s.indexCalls++
	optimize := s.indexCalls%optimizeEvery == 0
	s.mu.Unlock()
	if optimize { // best effort, like #optimizeFTS (ST:1661)
		s.db.Exec("INSERT INTO chunks(chunks) VALUES('optimize')")
		s.db.Exec("INSERT INTO chunks_trigram(chunks_trigram) VALUES('optimize')")
	}
	return res, nil
}

// insertSource writes one sources row, its chunks in both FTS tables and
// their words, inside tx. indexedAt "" means datetime('now'). It returns the
// source id and the number of new vocabulary words.
func insertSource(tx *sql.Tx, project, label, indexedAt string, chunks []Chunk) (int64, int64, error) {
	code := 0
	contents := make([]string, len(chunks))
	for i, c := range chunks {
		if c.HasCode {
			code++
		}
		contents[i] = c.Content
	}
	r, err := tx.Exec("INSERT INTO sources (project, label, chunk_count, code_chunk_count, indexed_at) VALUES (?, ?, ?, ?, COALESCE(NULLIF(?, ''), datetime('now')))",
		project, label, len(chunks), code, indexedAt)
	if err != nil {
		return 0, 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	for _, table := range []string{"chunks", "chunks_trigram"} {
		stmt, err := tx.Prepare("INSERT INTO " + table + " (title, content, source_id, content_type) VALUES (?, ?, ?, ?)")
		if err != nil {
			return 0, 0, err
		}
		for _, c := range chunks {
			ct := Prose
			if c.HasCode {
				ct = Code
			}
			if _, err := stmt.Exec(c.Title, c.Content, id, string(ct)); err != nil {
				stmt.Close()
				return 0, 0, err
			}
		}
		stmt.Close()
	}
	var added int64
	if words := vocabWords(strings.Join(contents, "\n")); len(words) > 0 {
		stmt, err := tx.Prepare("INSERT OR IGNORE INTO vocabulary (word) VALUES (?)")
		if err != nil {
			return 0, 0, err
		}
		for _, w := range words {
			r, err := stmt.Exec(w)
			if err != nil {
				stmt.Close()
				return 0, 0, err
			}
			n, _ := r.RowsAffected()
			added += n
		}
		stmt.Close()
	}
	return id, added, nil
}

// SourceByLabel returns the (project, label) source, or nil if missing.
func (s *Store) SourceByLabel(project, label string) (*Source, error) {
	var src Source
	var at string
	err := s.db.QueryRow("SELECT id, label, chunk_count, code_chunk_count, indexed_at FROM sources WHERE project = ? AND label = ?",
		project, label).Scan(&src.ID, &src.Label, &src.ChunkCount, &src.CodeChunkCount, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	src.IndexedAt, err = time.Parse(sqliteTime, at)
	return &src, err
}

// ChunkCount is the number of chunks indexed for project.
func (s *Store) ChunkCount(project string) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COALESCE(SUM(chunk_count), 0) FROM sources WHERE project = ?", project).Scan(&n)
	return n, err
}
```

`internal/store/import.go`:

```go
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

// ImportEvents writes rows in order in one transaction, bypassing the dedup
// window and the per-session cap, but cutting data to MaxEventData and
// recomputing data_hash. A row whose (session_id, type, data_hash,
// created_at) already exists is a duplicate and is skipped, so a second
// import of the same data inserts nothing. session_meta is merged: the
// earliest start, the latest activity, event_count plus the rows inserted,
// the larger compact_count; an existing row keeps its project and cc_pid.
func (s *Store) ImportEvents(sess ImportedSession, rows []ImportedEvent) (inserted, dupes int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	for _, r := range rows {
		data := cutData(r.Data)
		hash := EventDataHash(data)
		var n int
		if err := tx.QueryRow("SELECT count(*) FROM session_events WHERE session_id = ? AND type = ? AND data_hash = ? AND created_at = ?",
			sess.ID, r.Type, hash, r.CreatedAt).Scan(&n); err != nil {
			return 0, 0, err
		}
		if n > 0 {
			dupes++
			continue
		}
		if _, err := tx.Exec(`INSERT INTO session_events(session_id, project, type, category, priority, data, data_hash, source_hook, bytes_avoided, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sess.ID, sess.Project, r.Type, r.Category, r.Priority, data, hash, r.SourceHook, r.BytesAvoided, r.CreatedAt); err != nil {
			return 0, 0, err
		}
		inserted++
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
	_, added, err := insertSource(tx, project, label, indexedAt, chunks)
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestImport|TestMeta' -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/import.go internal/store/import_test.go internal/store/index.go
git commit -m "feat(store): import TS events and sources without dedup window or re-chunking"
```

### Task 2: TS fixtures, copies and readers for both schema generations

**Files:**
- Create: `internal/tsimport/read.go`
- Test: `internal/tsimport/fixture_test.go`
- Test: `internal/tsimport/read_test.go`

**Interfaces:**
- Consumes: `store.ImportedEvent`, `store.Chunk`, `store.Code` (Task 1); `driver.Open`, `fts5.Register` (github.com/ncruces/go-sqlite3)
- Produces: unexported `func copyDB(src, dir string) (string, error)`; `type tsCopy struct`; `func openCopy(path string) (*tsCopy, error)`; `func (c *tsCopy) Close() error`; `func (c *tsCopy) sessions() ([]tsSession, error)`; `func (c *tsCopy) sources() ([]tsSource, error)`; `type tsSession struct { id, projectDir, startedAt, lastEventAt string; compact int; events []store.ImportedEvent }`; `type tsSource struct { label, indexedAt string; chunks []store.Chunk }`; `func minTime(a, b string) string`; `func mostCommon(vals []string) string`; test helpers `tsDB`, `mustExec`, `addEvent`, `addMeta`, `addSource`, `checkpoint` and the DDL constants `sessionDDL`, `sessionDDLOld`, `contentDDL`, `contentDDLOld`

- [ ] **Step 1: Write the failing test**

`internal/tsimport/fixture_test.go`:

```go
package tsimport

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// sessionDDL is the TS SessionDB schema (src/session/db.ts:827-903,
// with the usage_cursor migration applied).
const sessionDDL = `
CREATE TABLE IF NOT EXISTS session_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 2,
  data TEXT NOT NULL,
  project_dir TEXT NOT NULL DEFAULT '',
  attribution_source TEXT NOT NULL DEFAULT 'unknown',
  attribution_confidence REAL NOT NULL DEFAULT 0,
  bytes_avoided INTEGER NOT NULL DEFAULT 0,
  bytes_returned INTEGER NOT NULL DEFAULT 0,
  source_hook TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  data_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_session_events_session ON session_events(session_id);
CREATE INDEX IF NOT EXISTS idx_session_events_type ON session_events(session_id, type);
CREATE INDEX IF NOT EXISTS idx_session_events_priority ON session_events(session_id, priority);
CREATE TABLE IF NOT EXISTS session_meta (
  session_id TEXT PRIMARY KEY,
  project_dir TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT (datetime('now')),
  last_event_at TEXT,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0,
  usage_cursor TEXT
);
CREATE TABLE IF NOT EXISTS session_resume (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL UNIQUE,
  snapshot TEXT NOT NULL,
  event_count INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  consumed INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS tool_calls (
  session_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  bytes_returned INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (session_id, tool)
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);`

// sessionDDLOld is the v1.0.0 SessionDB schema (packages/session/src/db.ts
// at 2ffccbee^): no project_dir, attribution_* or bytes_* columns.
const sessionDDLOld = `
CREATE TABLE IF NOT EXISTS session_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 2,
  data TEXT NOT NULL,
  source_hook TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  data_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_session_events_session ON session_events(session_id);
CREATE TABLE IF NOT EXISTS session_meta (
  session_id TEXT PRIMARY KEY,
  project_dir TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT (datetime('now')),
  last_event_at TEXT,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0
);`

// contentDDL is the TS ContentStore schema (src/store.ts:516-558).
const contentDDL = `
CREATE TABLE IF NOT EXISTS sources (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL DEFAULT 0,
  code_chunk_count INTEGER NOT NULL DEFAULT 0,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  file_path TEXT,
  content_hash TEXT
);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  source_category UNINDEXED, session_id UNINDEXED, event_id UNINDEXED, timestamp UNINDEXED,
  tokenize='porter unicode61'
);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  source_category UNINDEXED, session_id UNINDEXED, event_id UNINDEXED, timestamp UNINDEXED,
  tokenize='trigram'
);
CREATE TABLE IF NOT EXISTS vocabulary (word TEXT PRIMARY KEY);
CREATE INDEX IF NOT EXISTS idx_sources_label ON sources(label);`

// contentDDLOld has the 4-column FTS tables that TS before source_category
// wrote (src/store.ts before the 8-column migration at :560-600).
const contentDDLOld = `
CREATE TABLE IF NOT EXISTS sources (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL DEFAULT 0,
  code_chunk_count INTEGER NOT NULL DEFAULT 0,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  file_path TEXT,
  content_hash TEXT
);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize='porter unicode61'
);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize='trigram'
);
CREATE TABLE IF NOT EXISTS vocabulary (word TEXT PRIMARY KEY);`

// tsDB creates a TS database at path (and its directory) with ddl, in WAL mode with automatic
// checkpoints off, so rows written after a checkpoint stay in the -wal file.
// The connection stays open until the test ends, like a running TS process.
func tsDB(t *testing.T, path, ddl string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	dsn := (&url.URL{Scheme: "file", Path: path}).String() +
		"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)"
	db, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	mustExec(t, db, ddl)
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// addEvent inserts a current-schema session_events row.
func addEvent(t *testing.T, db *sql.DB, sid, typ, data, projectDir, createdAt string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO session_events(session_id, type, category, priority, data, project_dir, bytes_avoided, source_hook, created_at, data_hash)
VALUES(?, ?, 'file', 1, ?, ?, 7, 'PostToolUse', ?, 'X')`, sid, typ, data, projectDir, createdAt)
}

// addMeta inserts a session_meta row.
func addMeta(t *testing.T, db *sql.DB, sid, projectDir, startedAt, lastEventAt string, compact int) {
	t.Helper()
	mustExec(t, db, "INSERT INTO session_meta(session_id, project_dir, started_at, last_event_at, compact_count) VALUES(?, ?, ?, ?, ?)",
		sid, projectDir, startedAt, lastEventAt, compact)
}

// addSource inserts a source and its chunks (title, content, content_type
// triples) into a content DB of either schema.
func addSource(t *testing.T, db *sql.DB, label, indexedAt string, chunks ...[3]string) {
	t.Helper()
	r, err := db.Exec("INSERT INTO sources(label, chunk_count, indexed_at) VALUES(?, ?, ?)", label, len(chunks), indexedAt)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	for _, c := range chunks {
		for _, table := range []string{"chunks", "chunks_trigram"} {
			mustExec(t, db, "INSERT INTO "+table+"(title, content, source_id, content_type) VALUES(?, ?, ?, ?)", c[0], c[1], id, c[2])
		}
	}
}

// checkpoint moves every WAL frame into the main file and empties the WAL.
func checkpoint(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, "PRAGMA wal_checkpoint(TRUNCATE)")
}
```

`internal/tsimport/read_test.go`:

```go
package tsimport

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// copyAndOpen copies src (with -wal/-shm) into a fresh dir and opens the copy.
func copyAndOpen(t *testing.T, src string) *tsCopy {
	t.Helper()
	path, err := copyDB(src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := openCopy(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestReadSessionsCurrentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "0123456789abcdef.db")
	db := tsDB(t, path, sessionDDL)
	addMeta(t, db, "s1", "/Users/me/Proj", "2026-10-20 09:00:00", "2026-10-20 10:00:00", 2)
	addEvent(t, db, "s1", "file_read", "/a.go", "/Users/me/Proj", "2026-10-20 09:30:00")
	addEvent(t, db, "s2", "file_read", "/b.go", "/b", "2026-10-19 08:00:00")
	addEvent(t, db, "s2", "file_edit", "/b.go", "/c", "2026-10-19 08:00:01")
	addEvent(t, db, "s1", "file_edit", "/a.go", "", "2026-10-20 11:00:00") // newer than meta.last_event_at
	addEvent(t, db, "s2", "cwd", "/b", "/b", "2026-10-19 08:00:02")
	mustExec(t, db, "INSERT INTO session_meta(session_id, project_dir) VALUES('s3', '/no-events')")

	got, err := copyAndOpen(t, path).sessions()
	if err != nil {
		t.Fatal(err)
	}
	ev := func(typ, data, at string) store.ImportedEvent {
		return store.ImportedEvent{Type: typ, Category: "file", Priority: 1, Data: data, SourceHook: "PostToolUse", BytesAvoided: 7, CreatedAt: at}
	}
	want := []tsSession{
		{id: "s1", projectDir: "/Users/me/Proj", startedAt: "2026-10-20 09:00:00", lastEventAt: "2026-10-20 11:00:00", compact: 2,
			events: []store.ImportedEvent{ev("file_read", "/a.go", "2026-10-20 09:30:00"), ev("file_edit", "/a.go", "2026-10-20 11:00:00")}},
		{id: "s2", projectDir: "/b", startedAt: "2026-10-19 08:00:00", lastEventAt: "2026-10-19 08:00:02",
			events: []store.ImportedEvent{ev("file_read", "/b.go", "2026-10-19 08:00:00"), ev("file_edit", "/b.go", "2026-10-19 08:00:01"), ev("cwd", "/b", "2026-10-19 08:00:02")}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadSessionsOldSchemaDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db := tsDB(t, path, sessionDDLOld)
	addMeta(t, db, "s1", "/p", "2026-10-20 09:00:00", "", 0)
	mustExec(t, db, "INSERT INTO session_events(session_id, type, category, data, source_hook, created_at) VALUES('s1', 'cwd', 'cwd', '/p', 'PostToolUse', '2026-10-20 09:01:00')")

	got, err := copyAndOpen(t, path).sessions()
	if err != nil {
		t.Fatal(err)
	}
	want := []tsSession{{id: "s1", projectDir: "/p", startedAt: "2026-10-20 09:00:00", lastEventAt: "2026-10-20 09:01:00",
		events: []store.ImportedEvent{{Type: "cwd", Category: "cwd", Priority: 2, Data: "/p", SourceHook: "PostToolUse", CreatedAt: "2026-10-20 09:01:00"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions = %+v, want %+v", got, want)
	}
}

func TestReadSessionsIncludesWALFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	db := tsDB(t, path, sessionDDL)
	addEvent(t, db, "s1", "cwd", "/p", "/p", "2026-10-20 09:00:00")
	checkpoint(t, db)
	addEvent(t, db, "s1", "file_read", "/in-wal.go", "/p", "2026-10-20 09:00:01")
	if fi, err := os.Stat(path + "-wal"); err != nil || fi.Size() == 0 {
		t.Fatalf("want uncheckpointed frames in -wal: %v, %v", fi, err)
	}

	got, err := copyAndOpen(t, path).sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].events) != 2 || got[0].events[1].Data != "/in-wal.go" {
		t.Fatalf("sessions = %+v, want the WAL row too", got)
	}
}

func TestReadSourcesBothFTSSchemas(t *testing.T) {
	for name, ddl := range map[string]string{"8-column": contentDDL, "4-column": contentDDLOld} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "content.db")
			db := tsDB(t, path, ddl)
			addSource(t, db, "docs::https://x", "2026-10-15 08:00:00",
				[3]string{"Intro", "pooling basics", "prose"}, [3]string{"Dial", "func Dial() {}", "code"})
			addSource(t, db, "empty", "2026-10-16 08:00:00")

			got, err := copyAndOpen(t, path).sources()
			if err != nil {
				t.Fatal(err)
			}
			want := []tsSource{
				{label: "docs::https://x", indexedAt: "2026-10-15 08:00:00",
					chunks: []store.Chunk{{Title: "Intro", Content: "pooling basics"}, {Title: "Dial", Content: "func Dial() {}", HasCode: true}}},
				{label: "empty", indexedAt: "2026-10-16 08:00:00"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("sources = %+v, want %+v", got, want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tsimport/ -count=1 -v`
Expected: FAIL with "internal/tsimport/read_test.go:13:45: undefined: tsCopy"

- [ ] **Step 3: Write minimal implementation**

`internal/tsimport/read.go`:

```go
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
	events                 []store.ImportedEvent // in id order
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tsimport/ -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tsimport/read.go internal/tsimport/fixture_test.go internal/tsimport/read_test.go
git commit -m "feat(tsimport): read copied TS session and content DBs, WAL frames included"
```

### Task 3: Run: retention, project mapping, guard, dry run

**Files:**
- Create: `internal/tsimport/import.go`
- Test: `internal/tsimport/import_test.go`

**Interfaces:**
- Consumes: `copyDB`, `openCopy`, `tsSession`, `tsSource`, `minTime`, `mostCommon` (Task 2); `store.Open`, `store.SessionRetention`, `Store.ImportEvents`, `Store.ImportSource`, `Store.Meta`, `Store.SetMeta` (Task 1); `config.NormalizeProject`
- Produces: `type Options struct { Root, DataDir, DBPath string; DryRun, Force bool; Now time.Time; SourceRetention time.Duration }`; `type Report struct { Root string; Sessions, InactiveSessions, NoProjectSessions, Events, DupEvents, Sources, Chunks, ExpiredSources, NoProjectSources, ExistingSources int }`; `var ErrAlreadyImported error`; `func Run(o Options) (Report, error)`; `func DefaultRoot(getenv func(string) string, home, claudeDir string) string`

- [ ] **Step 1: Write the failing test**

`internal/tsimport/import_test.go`:

```go
package tsimport

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/store"
)

var testNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// ago formats testNow-d as a TS datetime('now') value.
func ago(d time.Duration) string { return testNow.Add(-d).Format("2006-01-02 15:04:05") }

const day = 24 * time.Hour

// buildRoot writes a TS data root and returns it. Its writers stay open
// until the test ends, so the -wal and -shm files exist during the import.
//
//	sessions/aaaa….db          current schema: s1 (active, 3 events, the last one only in the WAL),
//	                           s-old (inactive), s-noproj (no project dir anywhere)
//	sessions/aaaa…__1234abcd.db old schema: s1 again, one duplicate event and one new
//	sessions/bbbb….db          old schema: s-b, inactive, project /Proj/B
//	content/aaaa….db           8-column FTS: "fresh" (1 day), "stale" (15 days)
//	content/bbbb….db           4-column FTS: "bdocs" (2 days): its project has only inactive sessions
//	content/cccc….db           "orphan": no session DB starts with cccc…
func buildRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	a, b, c := "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb", "cccccccccccccccc"

	db := tsDB(t, filepath.Join(root, "sessions", a+".db"), sessionDDL)
	addMeta(t, db, "s1", "/Proj/A", ago(2*time.Hour), ago(90*time.Minute), 1)
	addEvent(t, db, "s1", "file_read", "/Proj/A/main.go", "/Proj/A", ago(2*time.Hour))
	addEvent(t, db, "s1", "decision", "use the quokkaline parser", "/Proj/A", ago(110*time.Minute))
	addMeta(t, db, "s-old", "/Proj/A", ago(9*day), ago(8*day), 0)
	addEvent(t, db, "s-old", "file_read", "/Proj/A/old.go", "/Proj/A", ago(8*day))
	addEvent(t, db, "s-noproj", "cwd", "/", "", ago(time.Hour))
	checkpoint(t, db)
	addEvent(t, db, "s1", "file_edit", "/Proj/A/wal.go", "/Proj/A", ago(time.Hour))

	old := tsDB(t, filepath.Join(root, "sessions", a+"__1234abcd.db"), sessionDDLOld)
	addMeta(t, old, "s1", "/Proj/A", ago(3*time.Hour), ago(30*time.Minute), 2)
	for _, e := range [][3]string{
		{"file_read", "/Proj/A/main.go", ago(2 * time.Hour)}, // same as in a.db
		{"git", "commit", ago(30 * time.Minute)},
	} {
		mustExec(t, old, "INSERT INTO session_events(session_id, type, category, data, source_hook, created_at) VALUES('s1', ?, 'file', ?, 'PostToolUse', ?)", e[0], e[1], e[2])
	}

	bs := tsDB(t, filepath.Join(root, "sessions", b+".db"), sessionDDLOld)
	addMeta(t, bs, "s-b", "/Proj/B", ago(10*day), ago(10*day), 0)
	mustExec(t, bs, "INSERT INTO session_events(session_id, type, category, data, source_hook, created_at) VALUES('s-b', 'cwd', 'cwd', '/Proj/B', 'PostToolUse', ?)", ago(10*day))

	ca := tsDB(t, filepath.Join(root, "content", a+".db"), contentDDL)
	addSource(t, ca, "fresh", ago(day), [3]string{"Zephyrine", "the zephyrine config loader", "prose"}, [3]string{"Code", "func Load() {}", "code"})
	addSource(t, ca, "stale", ago(15*day), [3]string{"Old", "old text", "prose"})
	checkpoint(t, ca)
	cb := tsDB(t, filepath.Join(root, "content", b+".db"), contentDDLOld)
	addSource(t, cb, "bdocs", ago(2*day), [3]string{"B", "bravo docs", "prose"})
	cc := tsDB(t, filepath.Join(root, "content", c+".db"), contentDDL)
	addSource(t, cc, "orphan", ago(day), [3]string{"O", "orphan text", "prose"})
	return root
}

// hashTree returns the sha256 of every file under root.
func hashTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = sha256.Sum256(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func options(t *testing.T, root string) Options {
	data := t.TempDir()
	return Options{Root: root, DataDir: data, DBPath: filepath.Join(data, "cmgo.db"), Now: testNow, SourceRetention: 14 * day}
}

// wantReport is the report of the first import of buildRoot.
var wantReport = Report{
	Sessions: 1, InactiveSessions: 2, NoProjectSessions: 1,
	Events: 4, DupEvents: 1,
	Sources: 2, Chunks: 3, ExpiredSources: 1, NoProjectSources: 1,
}

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRunImportsFixture(t *testing.T) {
	root := buildRoot(t)
	if fi, err := os.Stat(filepath.Join(root, "sessions", "aaaaaaaaaaaaaaaa.db-wal")); err != nil || fi.Size() == 0 {
		t.Fatalf("fixture wants WAL frames: %v %v", fi, err)
	}
	before := hashTree(t, root)
	o := options(t, root)
	got, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want := wantReport
	want.Root = root
	if got != want {
		t.Fatalf("report = %+v\nwant     %+v", got, want)
	}
	if after := hashTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("TS files changed")
	}
	if left, _ := filepath.Glob(filepath.Join(o.DataDir, "run", "import-*")); len(left) != 0 {
		t.Fatalf("temp dirs left: %v", left)
	}

	st := openStore(t, o.DBPath)
	projA, projB := config.NormalizeProject("/Proj/A"), config.NormalizeProject("/Proj/B")
	evs, err := st.SessionEvents("s1")
	if err != nil {
		t.Fatal(err)
	}
	var datas []string
	for _, e := range evs {
		if e.Project != projA {
			t.Fatalf("event project = %q, want %q", e.Project, projA)
		}
		datas = append(datas, e.Data)
	}
	if want := []string{"/Proj/A/main.go", "use the quokkaline parser", "/Proj/A/wal.go", "commit"}; !reflect.DeepEqual(datas, want) {
		t.Fatalf("s1 events = %q, want %q", datas, want)
	}
	for _, sid := range []string{"s-old", "s-noproj", "s-b"} {
		if evs, _ := st.SessionEvents(sid); len(evs) != 0 {
			t.Fatalf("%s imported: %+v", sid, evs)
		}
	}
	for _, c := range []struct{ project, label string }{{projA, "fresh"}, {projB, "bdocs"}} {
		if src, err := st.SourceByLabel(c.project, c.label); err != nil || src == nil {
			t.Fatalf("source %s/%s = %v, %v", c.project, c.label, src, err)
		}
	}
	if src, _ := st.SourceByLabel(projA, "stale"); src != nil {
		t.Fatal("expired source imported")
	}
	mark, ok, err := st.Meta("ts_import")
	if want := testNow.Format(time.RFC3339) + " 1 2"; err != nil || !ok || mark != want {
		t.Fatalf("ts_import = %q, %v, %v; want %q", mark, ok, err, want)
	}

	// ctx_search and the timeline search find the imported data.
	res, err := st.SearchWithFallback("zephyrine", store.SearchOpts{Project: projA, Limit: 5})
	if err != nil || len(res) != 1 || res[0].Source != "fresh" {
		t.Fatalf("search = %+v, %v", res, err)
	}
	hits, err := st.SessionEventsForSearch(projA, "quokkaline", "", 5)
	if err != nil || len(hits) != 1 || hits[0].SessionID != "s1" {
		t.Fatalf("timeline = %+v, %v", hits, err)
	}
}

func TestRunSecondRunNeedsForce(t *testing.T) {
	root := buildRoot(t)
	o := options(t, root)
	if _, err := Run(o); err != nil {
		t.Fatal(err)
	}
	st := openStore(t, o.DBPath)
	mark, _, _ := st.Meta("ts_import")
	st.Close()
	sum := func() [32]byte { // cmgo.db after its WAL is checkpointed by the last close
		b, err := os.ReadFile(o.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(b)
	}
	before := sum()

	if _, err := Run(o); !errors.Is(err, ErrAlreadyImported) {
		t.Fatalf("second Run err = %v, want ErrAlreadyImported", err)
	}
	if sum() != before {
		t.Fatal("cmgo.db changed without --force")
	}

	o.Force = true
	o.Now = testNow.Add(time.Minute)
	got, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want := Report{Root: root, Sessions: 1, InactiveSessions: 2, NoProjectSessions: 1, DupEvents: 5,
		ExpiredSources: 1, NoProjectSources: 1, ExistingSources: 2}
	if got != want {
		t.Fatalf("forced report = %+v\nwant           %+v", got, want)
	}
	st = openStore(t, o.DBPath)
	if m, _, _ := st.Meta("ts_import"); m == mark || m != o.Now.Format(time.RFC3339)+" 1 0" {
		t.Fatalf("ts_import after --force = %q (was %q)", m, mark)
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	root := buildRoot(t)
	o := options(t, root)
	o.DryRun = true
	got, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want := wantReport
	want.Root = root
	if got != want {
		t.Fatalf("dry-run report = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(o.DBPath); !os.IsNotExist(err) {
		t.Fatalf("dry run created cmgo.db: %v", err)
	}

	// With an existing cmgo.db, the counts see its rows and the file is untouched.
	st := openStore(t, o.DBPath)
	if _, err := st.Index(config.NormalizeProject("/Proj/A"), "fresh", []store.Chunk{{Title: "Mine", Content: "cmgo text"}}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	before := hashTree(t, o.DataDir)
	got, err = Run(o)
	if err != nil {
		t.Fatal(err)
	}
	want.Sources, want.Chunks, want.ExistingSources = 1, 1, 1
	if got != want {
		t.Fatalf("dry-run report = %+v, want %+v", got, want)
	}
	if after := hashTree(t, o.DataDir); !reflect.DeepEqual(before, after) {
		t.Fatal("dry run changed CMGO_DIR")
	}
}

func TestRunMissingRoot(t *testing.T) {
	o := options(t, filepath.Join(t.TempDir(), "nope"))
	if _, err := Run(o); err == nil {
		t.Fatal("Run on a missing root succeeded")
	}
	if _, err := os.Stat(o.DBPath); !os.IsNotExist(err) {
		t.Fatalf("cmgo.db created for a missing root: %v", err)
	}
}

func TestDefaultRoot(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, "cc")
	cm := filepath.Join(home, "cm")
	env := map[string]string{"CONTEXT_MODE_DIR": cm}
	getenv := func(k string) string { return env[k] }
	mk := func(dir string) {
		if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		create, want string
	}{
		{"", ""},
		{filepath.Join(home, ".claude", "context-mode"), filepath.Join(home, ".claude", "context-mode")},
		{filepath.Join(claude, "context-mode"), filepath.Join(claude, "context-mode")},
		{cm, cm},
	}
	for i, s := range steps {
		if s.create != "" {
			mk(s.create)
		}
		if got := DefaultRoot(getenv, home, claude); got != s.want {
			t.Fatalf("step %d: DefaultRoot = %q, want %q", i, got, s.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tsimport/ -count=1 -v`
Expected: FAIL with "internal/tsimport/import_test.go:90:41: undefined: Options"

- [ ] **Step 3: Write minimal implementation**

`internal/tsimport/import.go`:

```go
package tsimport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/store"
)

// markKey is the meta key that records a finished import (spec §3.2).
const markKey = "ts_import"

// sqliteTime is the layout of TS datetime('now') values.
const sqliteTime = "2006-01-02 15:04:05"

// ErrAlreadyImported is returned when cmgo.db records an import and Force
// is not set.
var ErrAlreadyImported = errors.New("TS data already imported")

// Options configures Run.
type Options struct {
	Root            string // TS data root, holding sessions/ and content/
	DataDir         string // $CMGO_DIR: the copies go to DataDir/run/import-*
	DBPath          string // cmgo.db
	DryRun, Force   bool
	Now             time.Time
	SourceRetention time.Duration // sources older than this are not imported
}

// Report counts what Run imported and skipped. In a dry run it counts what
// a real run would do now.
type Report struct {
	Root                                          string
	Sessions, InactiveSessions, NoProjectSessions int
	Events, DupEvents                             int
	Sources, Chunks                               int
	ExpiredSources, NoProjectSources              int
	ExistingSources                               int // a cmgo source with the same (project, label) won
}

// DefaultRoot returns the first TS data root that exists, probing
// $CONTEXT_MODE_DIR, <claudeDir>/context-mode and ~/.claude/context-mode
// (R4 §D.1), or "" when none does.
func DefaultRoot(getenv func(string) string, home, claudeDir string) string {
	for _, root := range []string{
		getenv("CONTEXT_MODE_DIR"),
		filepath.Join(claudeDir, "context-mode"),
		filepath.Join(home, ".claude", "context-mode"),
	} {
		if root != "" && hasData(root) {
			return root
		}
	}
	return ""
}

// hasData reports whether root has a sessions/ or content/ directory.
func hasData(root string) bool {
	for _, dir := range []string{"sessions", "content"} {
		if fi, err := os.Stat(filepath.Join(root, dir)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// Run imports the TS data under o.Root into o.DBPath. Every TS file is
// copied into a temp dir first and only the copy is opened; the temp dir is
// removed on return. A dry run imports into a copy of cmgo.db, so its counts
// are exact and cmgo.db is not touched.
func Run(o Options) (Report, error) {
	r := Report{Root: o.Root}
	if !hasData(o.Root) {
		return r, fmt.Errorf("no sessions/ or content/ directory in %s", o.Root)
	}
	run := filepath.Join(o.DataDir, "run")
	if err := os.MkdirAll(run, 0o755); err != nil {
		return r, err
	}
	tmp, err := os.MkdirTemp(run, "import-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(tmp)

	st, err := openTarget(o, tmp)
	if err != nil {
		return r, err
	}
	defer st.Close()
	if mark, ok, err := st.Meta(markKey); err != nil {
		return r, err
	} else if ok && !o.Force {
		return r, fmt.Errorf("%w (%s); run with --force to import again", ErrAlreadyImported, mark)
	}

	sessions, projects, err := readSessionDBs(filepath.Join(o.Root, "sessions"), filepath.Join(tmp, "sessions"))
	if err != nil {
		return r, err
	}
	cutoff := o.Now.Add(-store.SessionRetention).UTC().Format(sqliteTime)
	for _, s := range sessions {
		switch {
		case s.lastEventAt < cutoff:
			r.InactiveSessions++
			continue
		case s.projectDir == "":
			r.NoProjectSessions++
			continue
		}
		ins, dupes, err := st.ImportEvents(store.ImportedSession{ID: s.id, Project: s.projectDir,
			StartedAt: s.startedAt, LastEventAt: s.lastEventAt, CompactCount: s.compact}, s.events)
		if err != nil {
			return r, fmt.Errorf("session %s: %w", s.id, err)
		}
		r.Sessions++
		r.Events += ins
		r.DupEvents += dupes
	}

	if err := importContent(st, o, filepath.Join(tmp, "content"), projects, &r); err != nil {
		return r, err
	}
	if o.DryRun {
		return r, nil
	}
	return r, st.SetMeta(markKey, fmt.Sprintf("%s %d %d", o.Now.UTC().Format(time.RFC3339), r.Sessions, r.Sources))
}

// openTarget opens cmgo.db, or for a dry run a copy of it (a fresh DB when
// cmgo.db does not exist yet) inside tmp.
func openTarget(o Options, tmp string) (*store.Store, error) {
	if !o.DryRun {
		return store.Open(o.DBPath)
	}
	dir := filepath.Join(tmp, "cmgo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, filepath.Base(o.DBPath))
	if _, err := os.Stat(o.DBPath); err == nil {
		if path, err = copyDB(o.DBPath, dir); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
}

// readSessionDBs reads every <dir>/*.db through a copy in tmp. Sessions
// found in several DBs (worktrees) are merged into one, keeping DB name
// order and id order within a DB; their projects are normalized. projects
// maps each file's hash prefix (the name before "__" or ".db") to the most
// common project of all its sessions, active or not, for the content DBs;
// the unsuffixed <h16>.db sorts first and wins.
func readSessionDBs(dir, tmp string) ([]tsSession, map[string]string, error) {
	files, err := copies(dir, tmp)
	if err != nil {
		return nil, nil, err
	}
	var out []tsSession
	index := map[string]int{}
	projects := map[string]string{}
	for _, f := range files {
		c, err := openCopy(f.copy)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.src, err)
		}
		sessions, err := c.sessions()
		c.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.src, err)
		}
		var fileProjects []string
		for _, s := range sessions {
			if s.projectDir != "" {
				s.projectDir = config.NormalizeProject(s.projectDir)
				fileProjects = append(fileProjects, s.projectDir)
			}
			i, ok := index[s.id]
			if !ok {
				index[s.id] = len(out)
				out = append(out, s)
				continue
			}
			m := &out[i]
			m.events = append(m.events, s.events...)
			m.startedAt = minTime(m.startedAt, s.startedAt)
			m.lastEventAt = max(m.lastEventAt, s.lastEventAt)
			m.compact = max(m.compact, s.compact)
			if m.projectDir == "" {
				m.projectDir = s.projectDir
			}
		}
		prefix, _, _ := strings.Cut(strings.TrimSuffix(filepath.Base(f.src), ".db"), "__")
		if p := mostCommon(fileProjects); p != "" && projects[prefix] == "" {
			projects[prefix] = p
		}
	}
	return out, projects, nil
}

// importContent imports the fresh sources of every content DB whose
// <h16>.db name has a project in projects.
func importContent(st *store.Store, o Options, tmp string, projects map[string]string, r *Report) error {
	files, err := copies(filepath.Join(o.Root, "content"), tmp)
	if err != nil {
		return err
	}
	cutoff := o.Now.Add(-o.SourceRetention).UTC().Format(sqliteTime)
	for _, f := range files {
		c, err := openCopy(f.copy)
		if err != nil {
			return fmt.Errorf("%s: %w", f.src, err)
		}
		sources, err := c.sources()
		c.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", f.src, err)
		}
		project := projects[strings.TrimSuffix(filepath.Base(f.src), ".db")]
		for _, s := range sources {
			switch {
			case s.indexedAt < cutoff:
				r.ExpiredSources++
				continue
			case project == "":
				r.NoProjectSources++
				continue
			}
			ok, err := st.ImportSource(project, s.label, s.indexedAt, s.chunks)
			if err != nil {
				return fmt.Errorf("%s: source %q: %w", f.src, s.label, err)
			}
			if !ok {
				r.ExistingSources++
				continue
			}
			r.Sources++
			r.Chunks += len(s.chunks)
		}
	}
	return nil
}

// copied is a TS DB and its copy.
type copied struct{ src, copy string }

// copies copies every <dir>/*.db (with -wal/-shm) into tmp, in name order.
// A missing dir has no DBs.
func copies(dir, tmp string) ([]copied, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.db"))
	if err != nil || len(files) == 0 {
		return nil, err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	out := make([]copied, 0, len(files))
	for _, f := range files { // Glob sorts by name
		c, err := copyDB(f, tmp)
		if err != nil {
			return nil, err
		}
		out = append(out, copied{src: f, copy: c})
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tsimport/ -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tsimport/import.go internal/tsimport/import_test.go
git commit -m "feat(tsimport): import TS data inside the retention windows once, with dry run"
```

### Task 4: cmgo import command

**Files:**
- Create: `cmd/cmgo/import.go`
- Test: `cmd/cmgo/import_test.go`

**Interfaces:**
- Consumes: `tsimport.Run`, `tsimport.Options`, `tsimport.Report`, `tsimport.DefaultRoot` (Task 3); `config.Load`; `sourceRetention` (cmd/cmgo/serve.go)
- Produces: `func runImport(args []string, stdout, stderr io.Writer, getenv func(string) string) int`; unexported `type reason struct { n int; what string }`, `func skipped(reasons ...reason) string`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/import_test.go`:

```go
package main

import (
	"bytes"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// tsRoot writes a minimal TS data root: one active session with two events,
// one session without a project, and a content DB with a fresh and an
// expired source. Only the columns the importer reads are created; the
// full TS schemas are covered in internal/tsimport.
func tsRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	at := func(d time.Duration) string { return time.Now().UTC().Add(-d).Format("2006-01-02 15:04:05") }
	open := func(path, ddl string) *sql.DB {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := driver.Open((&url.URL{Scheme: "file", Path: path}).String(), fts5.Register)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
		return db
	}
	exec := func(db *sql.DB, q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s := open(filepath.Join(root, "sessions", "0123456789abcdef.db"), `
CREATE TABLE session_events (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, type TEXT NOT NULL,
  category TEXT NOT NULL, data TEXT NOT NULL, project_dir TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);`)
	exec(s, "INSERT INTO session_events(session_id, type, category, data, project_dir, created_at) VALUES('s1', 'cwd', 'cwd', '/w', '/w', ?), ('s1', 'git', 'git', 'push', '', ?), ('s2', 'cwd', 'cwd', '/', '', ?)",
		at(time.Hour), at(time.Minute), at(time.Minute))
	c := open(filepath.Join(root, "content", "0123456789abcdef.db"), `
CREATE TABLE sources (id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT NOT NULL, indexed_at TEXT NOT NULL);
CREATE VIRTUAL TABLE chunks USING fts5(title, content, source_id UNINDEXED, content_type UNINDEXED);`)
	exec(c, "INSERT INTO sources(label, indexed_at) VALUES('fresh', ?), ('stale', ?)", at(time.Hour), at(20*24*time.Hour))
	exec(c, "INSERT INTO chunks(title, content, source_id, content_type) VALUES('T', 'text', 1, 'prose'), ('U', 'more', 1, 'prose'), ('V', 'old', 2, 'prose')")
	return root
}

func importEnv(t *testing.T, extra map[string]string) (getenv func(string) string, dataDir string) {
	home := t.TempDir()
	dataDir = filepath.Join(home, "cmgo")
	env := map[string]string{"HOME": home, "CMGO_DIR": dataDir, "CMGO_PROJECT_DIR": home}
	for k, v := range extra {
		env[k] = v
	}
	return func(k string) string { return env[k] }, dataDir
}

func runImportCmd(getenv func(string) string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := runImport(args, &out, &errb, getenv)
	return code, out.String(), errb.String()
}

var doneLine = regexp.MustCompile(`\ndone in \d+ ms\n$`)

func TestImportCommandOutputAndGuard(t *testing.T) {
	root := tsRoot(t)
	getenv, dataDir := importEnv(t, nil)
	code, out, errOut := runImportCmd(getenv, "--from", root)
	want := "cmgo import from " + root + "\n" +
		"  sessions: 1 imported, 1 skipped (1 no project)\n" +
		"  events:   2 imported, 0 duplicates\n" +
		"  sources:  1 imported (2 chunks), 1 skipped (1 expired)\n"
	if code != 0 || !strings.HasPrefix(out, want) || !doneLine.MatchString(out) || errOut != "" {
		t.Fatalf("first run = %d\n%s\nstderr %q\nwant prefix\n%s", code, out, errOut, want)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cmgo.db")); err != nil {
		t.Fatal(err)
	}

	code, out, errOut = runImportCmd(getenv, "--from", root)
	if code != 1 || out != "" || !strings.Contains(errOut, "already imported") || !strings.Contains(errOut, "--force") {
		t.Fatalf("second run = %d %q %q", code, out, errOut)
	}

	code, out, _ = runImportCmd(getenv, "--from", root, "--force")
	want = "cmgo import from " + root + "\n" +
		"  sessions: 1 imported, 1 skipped (1 no project)\n" +
		"  events:   0 imported, 2 duplicates\n" +
		"  sources:  0 imported (0 chunks), 2 skipped (1 expired, 1 exists)\n"
	if code != 0 || !strings.HasPrefix(out, want) {
		t.Fatalf("forced run = %d\n%s\nwant prefix\n%s", code, out, want)
	}
}

func TestImportCommandDryRunAndDefaultRoot(t *testing.T) {
	root := tsRoot(t)
	getenv, dataDir := importEnv(t, map[string]string{"CONTEXT_MODE_DIR": root})
	code, out, errOut := runImportCmd(getenv, "--dry-run")
	if code != 0 || !strings.HasPrefix(out, "cmgo import from "+root+" (dry run)\n  sessions: 1 imported,") || errOut != "" {
		t.Fatalf("dry run = %d\n%s\nstderr %q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cmgo.db")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote cmgo.db: %v", err)
	}
}

func TestImportCommandErrors(t *testing.T) {
	getenv, _ := importEnv(t, nil)
	if code, _, errOut := runImportCmd(getenv); code != 1 || !strings.Contains(errOut, "no TS context-mode data found") {
		t.Fatalf("no root = %d %q", code, errOut)
	}
	if code, _, _ := runImportCmd(getenv, "--bogus"); code != 2 {
		t.Fatalf("bad flag exit = %d, want 2", code)
	}
	if code, _, _ := runImportCmd(getenv, "extra"); code != 2 {
		t.Fatalf("extra argument exit = %d, want 2", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run TestImportCommand -count=1 -v`
Expected: FAIL with "cmd/cmgo/import_test.go:70:10: undefined: runImport"

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo/import.go`:

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/tsimport"
)

// runImport is `cmgo import [--from <dir>] [--dry-run] [--force]` (spec §3).
// A usage error exits 2, any other error 1.
func runImport(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("cmgo import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "TS data root (default: $CONTEXT_MODE_DIR, $CLAUDE_CONFIG_DIR/context-mode, ~/.claude/context-mode)")
	dryRun := fs.Bool("dry-run", false, "print the counts and write nothing to cmgo.db")
	force := fs.Bool("force", false, "import again although cmgo.db records an import")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cmgo import: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	cfg, err := config.Load(getenv, os.Getwd)
	if err != nil {
		fmt.Fprintf(stderr, "cmgo import: %v\n", err)
		return 1
	}
	root := *from
	if root == "" {
		if root = tsimport.DefaultRoot(getenv, cfg.Home, cfg.ClaudeDir); root == "" {
			fmt.Fprintln(stderr, "cmgo import: no TS context-mode data found in $CONTEXT_MODE_DIR, $CLAUDE_CONFIG_DIR/context-mode or ~/.claude/context-mode; use --from")
			return 1
		}
	}
	start := time.Now()
	r, err := tsimport.Run(tsimport.Options{Root: root, DataDir: cfg.DataDir, DBPath: cfg.DBPath,
		DryRun: *dryRun, Force: *force, Now: start, SourceRetention: sourceRetention})
	if err != nil {
		fmt.Fprintf(stderr, "cmgo import: %v\n", err)
		return 1
	}
	title := "cmgo import from " + root
	if *dryRun {
		title += " (dry run)"
	}
	fmt.Fprintln(stdout, title)
	fmt.Fprintf(stdout, "  sessions: %d imported, %s\n", r.Sessions,
		skipped(reason{r.InactiveSessions, "inactive"}, reason{r.NoProjectSessions, "no project"}))
	fmt.Fprintf(stdout, "  events:   %d imported, %d duplicates\n", r.Events, r.DupEvents)
	fmt.Fprintf(stdout, "  sources:  %d imported (%d chunks), %s\n", r.Sources, r.Chunks,
		skipped(reason{r.ExpiredSources, "expired"}, reason{r.NoProjectSources, "no project"}, reason{r.ExistingSources, "exists"}))
	fmt.Fprintf(stdout, "done in %d ms\n", time.Since(start).Milliseconds())
	return 0
}

// reason is one skip count of the report.
type reason struct {
	n    int
	what string
}

// skipped formats "<total> skipped (<n> <reason>, ...)", leaving out zero
// counts and the parentheses when nothing was skipped.
func skipped(reasons ...reason) string {
	total := 0
	var parts []string
	for _, r := range reasons {
		if r.n > 0 {
			total += r.n
			parts = append(parts, fmt.Sprintf("%d %s", r.n, r.what))
		}
	}
	if total == 0 {
		return "0 skipped"
	}
	return fmt.Sprintf("%d skipped (%s)", total, strings.Join(parts, ", "))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cmgo/ -run TestImportCommand -count=1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/import.go cmd/cmgo/import_test.go
git commit -m "feat(cmgo): add cmgo import for TS context-mode data"
```

---

### Task 5: Version resolution and `cmgo-hook version`

Before the change, `cmgo-hook version` exits 0 and prints nothing: `TestVersionArg` fails with `main_test.go:48: version: "", <nil>` once the buildinfo package compiles. `cmd/cmgo/main.go` keeps its existing `TestVersion` (`cmgo dev`), because under `go test` the main module has no version.

**Files:**
- Create: `internal/buildinfo/buildinfo.go`
- Test: `internal/buildinfo/buildinfo_test.go`
- Modify: `cmd/cmgo-hook/main.go`
- Test: `cmd/cmgo-hook/main_test.go`
- Modify: `cmd/cmgo/main.go`

**Interfaces:**
- Consumes: none (stdlib `runtime/debug`)
- Produces: `func buildinfo.Version(ldflag string) string`; unexported `func resolve(ldflag string, info func() (*debug.BuildInfo, bool)) string`; `cmgo-hook version` prints `cmgo-hook <version>`; `cmgo`'s `var version string` resolved in `init`

- [ ] **Step 1: Write the failing test**

`internal/buildinfo/buildinfo_test.go`:

```go
package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/lang315/context-mode-go", Version: v}}, true
		}
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	cases := []struct {
		name, ldflag string
		info         func() (*debug.BuildInfo, bool)
		want         string
	}{
		{"ldflags win", "v1.2.3", info("v0.9.0"), "v1.2.3"},
		{"module version", "", info("v0.9.0"), "v0.9.0"},
		{"local build", "", info("(devel)"), "dev"},
		{"empty module version", "", info(""), "dev"},
		{"no build info", "", none, "dev"},
	}
	for _, c := range cases {
		if got := resolve(c.ldflag, c.info); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Under go test the main module has no version, so Version falls back to dev.
func TestVersionUnderTest(t *testing.T) {
	if got := Version(""); got != "dev" {
		t.Fatalf("got %q", got)
	}
	if got := Version("v2"); got != "v2" {
		t.Fatalf("got %q", got)
	}
}
```

`cmd/cmgo-hook/main_test.go`:

```go
package main

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// cmgo-hook exists to start fast (hooks spec §1.1): it must not link the
// MCP server or anything only the server needs.
func TestNoServerDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	forbidden := []string{
		"github.com/modelcontextprotocol/go-sdk",
		"net/http",
		"github.com/JohannesKaufmann/html-to-markdown",
		"github.com/lang315/context-mode-go/internal/executor",
		"github.com/lang315/context-mode-go/internal/fetch",
		"github.com/lang315/context-mode-go/internal/mcpserver",
	}
	deps := strings.Fields(string(out))
	if !slices.Contains(deps, "github.com/lang315/context-mode-go/internal/hookcmd") {
		t.Fatalf("cmgo-hook does not link internal/hookcmd; deps: %v", deps)
	}
	for _, dep := range deps {
		for _, f := range forbidden {
			if dep == f || strings.HasPrefix(dep, f+"/") {
				t.Errorf("cmgo-hook links %s", dep)
			}
		}
	}
}

// `cmgo-hook version` prints the -X main.version value; doctor compares it
// with cmgo's own version.
func TestVersionArg(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cmgo-hook")
	if out, err := exec.Command("go", "build", "-ldflags", "-X main.version=v9.9.9", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil || string(out) != "cmgo-hook v9.9.9\n" {
		t.Fatalf("version: %q, %v", out, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/buildinfo/ ./cmd/cmgo-hook/ ./cmd/cmgo/ -run 'TestResolve|TestVersionUnderTest|TestVersionArg|TestVersion$' -v`
Expected: FAIL with "internal/buildinfo/buildinfo_test.go:27:13: undefined: resolve"

- [ ] **Step 3: Write minimal implementation**

`internal/buildinfo/buildinfo.go`:

```go
// Package buildinfo resolves the version both binaries print. It imports
// only runtime/debug, so cmgo-hook can use it without growing its start-up.
package buildinfo

import "runtime/debug"

// Version is ldflag when the build set one (-ldflags "-X main.version=..."),
// else the main module version a `go install module@version` records, else
// "dev". A source-tree build records "(devel)", which also means dev.
func Version(ldflag string) string { return resolve(ldflag, debug.ReadBuildInfo) }

func resolve(ldflag string, info func() (*debug.BuildInfo, bool)) string {
	if ldflag != "" {
		return ldflag
	}
	if bi, ok := info(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
```

`cmd/cmgo-hook/main.go`:

```go
// Command cmgo-hook runs one Claude Code hook fire: `cmgo-hook <event>`. It
// is a separate binary from cmgo so a hook process does not load the MCP
// server, executor and fetcher, whose start-up cost every hook fire would
// otherwise pay (hooks spec §1.1).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/buildinfo"
	"github.com/lang315/context-mode-go/internal/hookcmd"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version string

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cmgo-hook <event>|version")
		os.Exit(2)
	}
	// No hook event is named version; cmgo doctor runs this to compare builds.
	if os.Args[1] == "version" {
		fmt.Println("cmgo-hook " + buildinfo.Version(version))
		return
	}
	os.Exit(hookcmd.Run(os.Args[1], hookcmd.IO{
		Getenv: os.Getenv, Getwd: os.Getwd, Stdin: os.Stdin, Stdout: os.Stdout, Now: time.Now,
	}))
}
```

`cmd/cmgo/main.go`:

```go
// Command cmgo is a Go rewrite of context-mode for Claude Code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/buildinfo"
	"github.com/lang315/context-mode-go/internal/hookcmd"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
// Without it, init resolves it from the build info (spec §1).
var version string

func init() { version = buildinfo.Version(version) }

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run dispatches a subcommand. stdin feeds `hook`; serve keeps os.Stdin
// because the MCP transport needs a ReadCloser.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cmgo <serve|hook|install|uninstall|version>")
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "cmgo "+version)
		return 0
	case "serve":
		return serve(context.Background(), serveIO{
			getenv: os.Getenv, getwd: os.Getwd, in: os.Stdin, out: os.Stdout, stderr: stderr,
		})
	case "hook": // the same code as cmgo-hook, for manual runs; install registers cmgo-hook
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cmgo hook <event>")
			return 2
		}
		return hookcmd.Run(args[1], hookcmd.IO{Getenv: os.Getenv, Getwd: os.Getwd, Stdin: stdin, Stdout: stdout, Now: time.Now})
	case "install", "uninstall":
		return installCmd(args[0], args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cmgo: unknown command %q\n", args[0])
		return 2
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/buildinfo/ ./cmd/cmgo-hook/ ./cmd/cmgo/ -run 'TestResolve|TestVersionUnderTest|TestVersionArg|TestVersion$' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/buildinfo/buildinfo.go internal/buildinfo/buildinfo_test.go cmd/cmgo-hook/main.go cmd/cmgo-hook/main_test.go cmd/cmgo/main.go
git commit -m "feat(buildinfo): resolve the version from ldflags or build info; add cmgo-hook version"
```

### Task 6: Store schema version and FTS5 probe

**Files:**
- Create: `internal/store/health.go`
- Test: `internal/store/health_test.go`

**Interfaces:**
- Consumes: `store.Open`, the unexported `migrations` slice, `driver.Open`, `fts5.Register`
- Produces: `func (s *Store) SchemaVersion() (int, error)`; `func ProbeFTS5() error`

- [ ] **Step 1: Write the failing test**

`internal/store/health_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func TestSchemaVersion(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := s.SchemaVersion()
	if err != nil || v != len(migrations) {
		t.Fatalf("SchemaVersion = %d, %v; want %d", v, err, len(migrations))
	}
}

func TestProbeFTS5(t *testing.T) {
	if err := ProbeFTS5(); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestSchemaVersion|TestProbeFTS5' -v`
Expected: FAIL with "internal/store/health_test.go:14:14: s.SchemaVersion undefined (type *Store has no field or method SchemaVersion)"

- [ ] **Step 3: Write minimal implementation**

`internal/store/health.go`:

```go
package store

import (
	"errors"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// SchemaVersion returns PRAGMA user_version: how many migrations the file has.
func (s *Store) SchemaVersion() (int, error) {
	var v int
	err := s.db.QueryRow("PRAGMA user_version").Scan(&v)
	return v, err
}

// ProbeFTS5 is the TS doctor FTS5 test (S:4213-4229) on an in-memory DB:
// create an fts5 table, insert a row and find it with MATCH.
func ProbeFTS5() error {
	db, err := driver.Open(":memory:", fts5.Register)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("CREATE VIRTUAL TABLE fts_test USING fts5(content)"); err != nil {
		return err
	}
	if _, err := db.Exec("INSERT INTO fts_test(content) VALUES ('hello world')"); err != nil {
		return err
	}
	var got string
	if err := db.QueryRow("SELECT content FROM fts_test WHERE fts_test MATCH 'hello'").Scan(&got); err != nil {
		return err
	}
	if got != "hello world" {
		return errors.New("unexpected result")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestSchemaVersion|TestProbeFTS5' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/health.go internal/store/health_test.go
git commit -m "feat(store): expose the schema version and the TS FTS5 probe for cmgo doctor"
```

### Task 7: Executor runtime list

**Files:**
- Create: `internal/executor/available.go`
- Test: `internal/executor/available_test.go`

**Interfaces:**
- Consumes: unexported `(*Executor).runtime(Language) string`, `Languages`, test helper `fakeExec` (runtime_test.go)
- Produces: `func (e *Executor) Available() []Language`

- [ ] **Step 1: Write the failing test**

`internal/executor/available_test.go`:

```go
package executor

import (
	"reflect"
	"testing"
)

func TestAvailable(t *testing.T) {
	// Shell falls back to sh, so it is always available.
	e := fakeExec(nil, "node", "python3", "go")
	want := []Language{JavaScript, Python, Shell, Go}
	if got := e.Available(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Available = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/executor/ -run TestAvailable -v`
Expected: FAIL with "internal/executor/available_test.go:12:14: e.Available undefined (type *Executor has no field or method Available)"

- [ ] **Step 3: Write minimal implementation**

`internal/executor/available.go`:

```go
package executor

// Available lists the languages whose runtime is found, in schema order. It
// detects every runtime that has not been used yet (cmgo doctor's Runtimes).
func (e *Executor) Available() []Language {
	var out []Language
	for _, l := range Languages {
		if e.runtime(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/executor/ -run TestAvailable -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/executor/available.go internal/executor/available_test.go
git commit -m "feat(executor): list the languages whose runtime is found"
```

### Task 8: Install status read-back

**Files:**
- Create: `internal/install/status.go`
- Test: `internal/install/status_test.go`

**Interfaces:**
- Consumes: unexported `loadSettings`, `object`, `ownArg`, `shellWord`, `isOwnStatusLine`, `mcpState`, `HookEvents`; test helpers `newOpts`, `writeSettings`, `writeClaudeJSON`, const `bin`
- Produces: `type Status struct { Events []string; HookBinary string; StatusLine string; OwnStatusLine bool; TSPlugins []string }`; `func Inspect(settingsPath string) (Status, error)`; `func MCPServer(claudeJSONPath string) (command string, args []string, ok bool)`

- [ ] **Step 1: Write the failing test**

`internal/install/status_test.go`:

```go
package install

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestInspectAfterInstall(t *testing.T) {
	o, _ := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	st, err := Inspect(o.SettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, he := range HookEvents {
		all = append(all, he.Event)
	}
	want := Status{Events: all, HookBinary: "/opt/cmgo/bin/cmgo-hook", StatusLine: "/opt/cmgo/bin/cmgo-hook statusline", OwnStatusLine: true}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("Inspect = %+v\nwant %+v", st, want)
	}
}

func TestInspectPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	// PreToolUse uses the pre-cmgo-hook form; Stop holds a cmgo command for
	// another event, which does not count; SessionStart's is a user command.
	writeSettings(t, path, `{
  "hooks": {
    "PreToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "'/old dir/cmgo' hook pretooluse"}]}],
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "/x/cmgo-hook pretooluse"}]}],
    "SessionStart": [{"matcher": "", "hooks": [{"type": "command", "command": "echo hi"}, {"type": "command", "command": "/x/cmgo-hook sessionstart"}]}]
  },
  "statusLine": {"type": "command", "command": "ccline"},
  "enabledPlugins": {"context-mode@context-mode": true, "context-mode@other": false, "x@y": true}
}`, 0o644)
	st, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Status{Events: []string{"PreToolUse", "SessionStart"}, HookBinary: "/old dir/cmgo", StatusLine: "ccline", TSPlugins: []string{"context-mode@context-mode"}}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("Inspect = %+v\nwant %+v", st, want)
	}
}

func TestInspectMissingAndBad(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "none.json")
	if st, err := Inspect(missing); err != nil || !reflect.DeepEqual(st, Status{}) {
		t.Fatalf("missing file: %+v, %v", st, err)
	}
	bad := filepath.Join(dir, "bad.json")
	writeSettings(t, bad, `{"hooks":`, 0o644)
	if _, err := Inspect(bad); err == nil {
		t.Fatal("unparseable file: no error")
	}
}

func TestMCPServer(t *testing.T) {
	o, _ := newOpts(t)
	if _, _, ok := MCPServer(o.ClaudeJSONPath); ok {
		t.Fatal("no file: registered")
	}
	writeClaudeJSON(t, o, `{"mcpServers":{"cmgo":{"type":"stdio","command":"/a/cmgo","args":["serve"]}}}`)
	cmd, args, ok := MCPServer(o.ClaudeJSONPath)
	if !ok || cmd != "/a/cmgo" || !reflect.DeepEqual(args, []string{"serve"}) {
		t.Fatalf("got %q %v %v", cmd, args, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -run 'TestInspect|TestMCPServer' -v`
Expected: FAIL with "internal/install/status_test.go:14:13: undefined: Inspect"

- [ ] **Step 3: Write minimal implementation**

`internal/install/status.go`:

```go
package install

import "strings"

// Status is the cmgo state Install writes to settings.json, read back with
// Install's own command detection (cmgo doctor).
type Status struct {
	Events        []string // HookEvents events holding a cmgo command for their own arg, in HookEvents order
	HookBinary    string   // the binary of the first such command; "" when none
	StatusLine    string   // statusLine.command; "" when not set
	OwnStatusLine bool     // StatusLine runs cmgo-hook statusline
	TSPlugins     []string // enabled context-mode@ plugin keys
}

// Inspect reads settingsPath. A missing or blank file is an empty Status; a
// file that does not parse is an error.
func Inspect(settingsPath string) (Status, error) {
	s, err := loadSettings(settingsPath)
	if err != nil {
		return Status{}, err
	}
	var st Status
	hv, _ := s.root.get("hooks")
	hooks, _ := hv.(*object)
	for _, he := range HookEvents {
		if bin, ok := ownBinary(hooks, he.Event, he.Arg); ok {
			st.Events = append(st.Events, he.Event)
			if st.HookBinary == "" {
				st.HookBinary = bin
			}
		}
	}
	if v, ok := s.root.get("statusLine"); ok {
		if sl, ok := v.(*object); ok {
			st.StatusLine, _ = sl.vals["command"].(string)
			st.OwnStatusLine = isOwnStatusLine(sl)
		}
	}
	pv, _ := s.root.get("enabledPlugins")
	if p, ok := pv.(*object); ok {
		for _, k := range p.keys {
			if strings.HasPrefix(k, "context-mode@") && p.vals[k] == true {
				st.TSPlugins = append(st.TSPlugins, k)
			}
		}
	}
	return st, nil
}

// ownBinary finds a cmgo command for arg under hooks[event] and returns its
// binary. Values of an unexpected shape are skipped, as stripHooks does.
func ownBinary(hooks *object, event, arg string) (string, bool) {
	if hooks == nil {
		return "", false
	}
	entries, _ := hooks.vals[event].([]any)
	for _, e := range entries {
		eo, ok := e.(*object)
		if !ok {
			continue
		}
		inner, _ := eo.vals["hooks"].([]any)
		for _, h := range inner {
			ho, ok := h.(*object)
			if !ok {
				continue
			}
			if c, ok := ho.vals["command"].(string); ok && ownArg(c) == arg {
				bin, _, _ := shellWord(c)
				return bin, true
			}
		}
	}
	return "", false
}

// MCPServer returns the command and args of the user-scope cmgo MCP server
// in claudeJSONPath; ok is false when it is not registered.
func MCPServer(claudeJSONPath string) (command string, args []string, ok bool) {
	e, ok := mcpState(claudeJSONPath)
	return e.Command, e.Args, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/install/ -run 'TestInspect|TestMCPServer' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/install/status.go internal/install/status_test.go
git commit -m "feat(install): read back the cmgo hooks, statusline, plugins and MCP entry"
```

### Task 9: Doctor environment and local checks

**Files:**
- Create: `internal/doctor/env.go`
- Create: `internal/doctor/checks.go`
- Test: `internal/doctor/checks_test.go`

**Interfaces:**
- Consumes: `config.Config`, `executor.Request`, `executor.Result`, `executor.Languages`, `executor.New`, `store.Open`, `(*store.Store).SchemaVersion`, `store.ProbeFTS5`, `hookio.JSPrefix`
- Produces: `type Executor interface { Run(ctx context.Context, r executor.Request) (executor.Result, error); Available() []executor.Language }`; `type Env struct { Version, Binary, DataDir, DBPath, SettingsPath, ClaudeJSONPath string; Exec Executor; HookVersion func(ctx context.Context, path string) (string, error); HookTimeout, ExecTimeout time.Duration }`; `func NewEnv(cfg config.Config, version string, ex Executor) Env`; unexported `checkStorage(dir string) string`, `checkDatabase(path string) string`, `checkExecutor(ctx context.Context, ex Executor, timeout time.Duration) string`, `checkRuntimes(ex Executor) string`

- [ ] **Step 1: Write the failing test**

`internal/doctor/checks_test.go`:

```go
package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
)

// fakeExec answers Run with a fixed result and records the request.
type fakeExec struct {
	res   executor.Result
	err   error
	langs []executor.Language
	got   executor.Request
}

func (f *fakeExec) Run(_ context.Context, r executor.Request) (executor.Result, error) {
	f.got = r
	return f.res, f.err
}

func (f *fakeExec) Available() []executor.Language { return f.langs }

func TestNewEnv(t *testing.T) {
	home := "/h"
	cases := []struct{ claudeDir, want string }{
		{"/h/.claude", "/h/.claude.json"},
		{"/cfg", "/cfg/.claude.json"},
	}
	for _, c := range cases {
		cfg := config.Config{DataDir: "/d", DBPath: "/d/cmgo.db", Home: home, ClaudeDir: c.claudeDir}
		e := NewEnv(cfg, "v1", nil)
		if e.ClaudeJSONPath != c.want || e.SettingsPath != c.claudeDir+"/settings.json" || e.DataDir != "/d" || e.DBPath != "/d/cmgo.db" || e.Version != "v1" {
			t.Errorf("%s: %+v", c.claudeDir, e)
		}
		if !filepath.IsAbs(e.Binary) {
			t.Errorf("Binary %q is not absolute", e.Binary)
		}
	}
}

func TestCheckStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cmgo")
	if got, want := checkStorage(dir), "[OK] Storage: "+dir+" (writable)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("probe file left behind: %v", left)
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o644)
	under := filepath.Join(file, "cmgo")
	if got := checkStorage(under); !strings.HasPrefix(got, "[FAIL] Storage: "+under+" — ") {
		t.Errorf("got %q", got)
	}
}

func TestCheckDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	got := checkDatabase(path)
	if !strings.HasPrefix(got, "[OK] Database: "+path+" schema v") || !strings.HasSuffix(got, ", FTS5 PASS") {
		t.Errorf("got %q", got)
	}
	bad := filepath.Join(t.TempDir(), "cmgo.db")
	os.WriteFile(bad, []byte(strings.Repeat("not a database ", 100)), 0o644)
	if got := checkDatabase(bad); !strings.HasPrefix(got, "[FAIL] Database: ") {
		t.Errorf("got %q", got)
	}
}

func TestCheckExecutor(t *testing.T) {
	cases := []struct {
		name string
		f    *fakeExec
		want string
	}{
		{"pass", &fakeExec{res: executor.Result{Stdout: "ok\n"}}, "[OK] Executor: PASS (shell)"},
		{"exit code", &fakeExec{res: executor.Result{ExitCode: 127, Stderr: "  sh: echo: not found\n"}}, "[FAIL] Executor: FAIL — exit 127 (sh: echo: not found)"},
		{"wrong output", &fakeExec{res: executor.Result{Stdout: "ko"}}, "[FAIL] Executor: FAIL — exit 0"},
		{"long stderr", &fakeExec{res: executor.Result{ExitCode: 1, Stderr: strings.Repeat("é", 300)}}, "[FAIL] Executor: FAIL — exit 1 (" + strings.Repeat("é", 200) + ")"},
		{"cannot run", &fakeExec{err: errors.New("no shell")}, "[FAIL] Executor: FAIL — no shell"},
	}
	for _, c := range cases {
		if got := checkExecutor(context.Background(), c.f, 5*time.Second); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if r := c.f.got; r.Language != executor.Shell || r.Code != "echo ok" || r.Timeout != 5*time.Second {
			t.Errorf("%s: request %+v", c.name, r)
		}
	}
}

// The real executor runs the probe through the shell.
func TestCheckExecutorReal(t *testing.T) {
	ex := executor.New(t.TempDir())
	defer ex.Shutdown()
	if got := checkExecutor(context.Background(), ex, 5*time.Second); got != "[OK] Executor: PASS (shell)" {
		t.Fatalf("got %q", got)
	}
}

func TestCheckRuntimes(t *testing.T) {
	f := &fakeExec{langs: []executor.Language{executor.JavaScript, executor.Shell, executor.Go}}
	if got, want := checkRuntimes(f), "[OK] Runtimes: 3/12 — javascript, shell, go"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/doctor/ -v`
Expected: FAIL with "internal/doctor/checks_test.go:39:8: undefined: NewEnv"

- [ ] **Step 3: Write minimal implementation**

`internal/doctor/env.go`:

```go
// Package doctor runs the cmgo health checks behind `cmgo doctor` and
// ctx_doctor and renders them as the TS [OK]/[WARN]/[FAIL] plain-text lines
// (S:4139-4267, spec §1).
package doctor

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
)

// Executor is what the Executor and Runtimes checks use; *executor.Executor
// has both methods.
type Executor interface {
	Run(ctx context.Context, r executor.Request) (executor.Result, error)
	Available() []executor.Language
}

// Env holds every path and effect the checks touch, so tests point them at
// temp dirs, fake scripts and fakes.
type Env struct {
	Version        string // cmgo's own version
	Binary         string // this cmgo, symlinks resolved, as cmgo install registers it
	DataDir        string
	DBPath         string
	SettingsPath   string
	ClaudeJSONPath string
	Exec           Executor
	// HookVersion returns the version `<path> version` prints; nil runs it.
	HookVersion func(ctx context.Context, path string) (string, error)
	HookTimeout time.Duration // 0 = 2 s (spec §1)
	ExecTimeout time.Duration // 0 = 5 s, the TS server test timeout
}

// NewEnv is the Env of this process: paths from cfg, Binary from
// os.Executable.
func NewEnv(cfg config.Config, version string, ex Executor) Env {
	bin, err := os.Executable()
	if err == nil {
		bin, _ = filepath.EvalSymlinks(bin)
	}
	// shortcut: cmgo install keys on whether CLAUDE_CONFIG_DIR is set, this on
	// whether it is the default dir; they differ only for an explicit
	// CLAUDE_CONFIG_DIR=~/.claude. Upgrade if that case ever matters.
	claudeJSON := filepath.Join(cfg.ClaudeDir, ".claude.json")
	if cfg.ClaudeDir == filepath.Join(cfg.Home, ".claude") {
		claudeJSON = filepath.Join(cfg.Home, ".claude.json")
	}
	return Env{
		Version:        version,
		Binary:         bin,
		DataDir:        cfg.DataDir,
		DBPath:         cfg.DBPath,
		SettingsPath:   filepath.Join(cfg.ClaudeDir, "settings.json"),
		ClaudeJSONPath: claudeJSON,
		Exec:           ex,
	}
}
```

`internal/doctor/checks.go`:

```go
package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

// checkStorage creates the data dir if needed and writes and removes a probe
// file in it.
func checkStorage(dir string) string {
	err := os.MkdirAll(dir, 0o755)
	if err == nil {
		var f *os.File
		if f, err = os.CreateTemp(dir, ".doctor-*"); err == nil {
			_, err = f.WriteString("ok")
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			os.Remove(f.Name())
		}
	}
	if err != nil {
		return fmt.Sprintf("[FAIL] Storage: %s — %v", dir, err)
	}
	return fmt.Sprintf("[OK] Storage: %s (writable)", dir)
}

// checkDatabase opens the store as serve does (migrations included), reads
// its schema version and runs the TS FTS5 probe.
func checkDatabase(path string) string {
	st, err := store.Open(path)
	if err != nil {
		return "[FAIL] Database: " + err.Error()
	}
	defer st.Close()
	v, err := st.SchemaVersion()
	if err == nil {
		err = store.ProbeFTS5()
	}
	if err != nil {
		return "[FAIL] Database: " + err.Error()
	}
	return fmt.Sprintf("[OK] Database: %s schema v%d, FTS5 PASS", path, v)
}

// checkExecutor is the TS server test (S:4196-4211) in the shell: echo ok
// must exit 0 and print ok.
func checkExecutor(ctx context.Context, ex Executor, timeout time.Duration) string {
	res, err := ex.Run(ctx, executor.Request{Language: executor.Shell, Code: "echo ok", Timeout: timeout})
	if err != nil {
		return "[FAIL] Executor: FAIL — " + err.Error()
	}
	if res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "ok" {
		return "[OK] Executor: PASS (shell)"
	}
	detail := ""
	if s := strings.TrimSpace(res.Stderr); s != "" {
		detail = " (" + hookio.JSPrefix(s, 200) + ")"
	}
	return fmt.Sprintf("[FAIL] Executor: FAIL — exit %d%s", res.ExitCode, detail)
}

// checkRuntimes lists the languages ctx_execute can run here.
func checkRuntimes(ex Executor) string {
	var names []string
	for _, l := range ex.Available() {
		names = append(names, string(l))
	}
	return fmt.Sprintf("[OK] Runtimes: %d/%d — %s", len(names), len(executor.Languages), strings.Join(names, ", "))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/doctor/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/doctor/env.go internal/doctor/checks.go internal/doctor/checks_test.go
git commit -m "feat(doctor): storage, database, executor and runtime checks"
```

### Task 10: Doctor settings, hook binary and MCP checks

**Files:**
- Create: `internal/doctor/settings.go`
- Test: `internal/doctor/settings_test.go`

**Interfaces:**
- Consumes: `type install.Status` and `func install.MCPServer(claudeJSONPath string) (command string, args []string, ok bool)` (Task 8); `var install.HookEvents []struct{ Event, Arg, Matcher string }`; `type Env` (Task 9)
- Produces: unexported `checkVersion(ctx context.Context, e Env, hookBin string) string`, `runVersion(ctx context.Context, path string) (string, error)`, `checkHooks(path string, st install.Status, err error) string`, `hookBinaryPath(st install.Status, binary string) string`, `checkHookBinary(path string) string`, `checkMCP(claudeJSON, binary string) string`, `checkStatusLine(st install.Status) string`, `checkTSPlugin(st install.Status) string`

- [ ] **Step 1: Write the failing test**

`internal/doctor/settings_test.go`:

```go
package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/install"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckHooks(t *testing.T) {
	all := []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "SessionStart", "Stop", "PreCompact"}
	p := "/c/settings.json"
	cases := []struct {
		name string
		st   install.Status
		err  error
		want string
	}{
		{"all", install.Status{Events: all}, nil, "[OK] Hooks: 7/7 events registered in /c/settings.json"},
		{"some missing", install.Status{Events: all[:4]}, nil, "[WARN] Hooks: missing SessionStart, Stop, PreCompact — fix: cmgo install"},
		{"none", install.Status{}, nil, "[WARN] Hooks: missing " + strings.Join(all, ", ") + " — fix: cmgo install"},
		{"unreadable", install.Status{}, errors.New("parse /c/settings.json: bad"), "[FAIL] Hooks: could not read /c/settings.json — parse /c/settings.json: bad"},
	}
	for _, c := range cases {
		if got := checkHooks(p, c.st, c.err); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHookBinaryPath(t *testing.T) {
	if got := hookBinaryPath(install.Status{HookBinary: "/s/cmgo-hook"}, "/b/cmgo"); got != "/s/cmgo-hook" {
		t.Errorf("from settings: %q", got)
	}
	if got := hookBinaryPath(install.Status{}, "/b/cmgo"); got != "/b/cmgo-hook" {
		t.Errorf("next to cmgo: %q", got)
	}
}

func TestCheckHookBinary(t *testing.T) {
	dir := t.TempDir()
	p := script(t, dir, "cmgo-hook", "exit 0")
	if got := checkHookBinary(p); got != "[OK] Hook binary: "+p {
		t.Errorf("got %q", got)
	}
	missing := filepath.Join(dir, "none")
	if got := checkHookBinary(missing); got != "[FAIL] Hook binary: not found at "+missing+" — fix: cmgo install" {
		t.Errorf("got %q", got)
	}
	if got := checkHookBinary(dir); got != "[FAIL] Hook binary: not found at "+dir+" — fix: cmgo install" {
		t.Errorf("dir: got %q", got)
	}
}

func TestCheckVersion(t *testing.T) {
	dir := t.TempDir()
	same := script(t, dir, "same", `[ "$1" = version ] && echo "cmgo-hook v1.0.0"`)
	other := script(t, dir, "other", `echo "cmgo-hook v0.9.0"`)
	silent := script(t, dir, "silent", "exit 0")
	slow := script(t, dir, "slow", "exec sleep 10")
	// A fresh script's first run can take seconds on a loaded macOS box, so
	// only the timeout case is short, and only it is timed.
	cases := []struct {
		name, path string
		timeout    time.Duration
		want       string
	}{
		{"same", same, time.Minute, "[OK] Version: cmgo v1.0.0 (hook binary v1.0.0)"},
		{"differs", other, time.Minute, "[WARN] Version: cmgo v1.0.0 (hook binary v0.9.0 differs) — fix: cmgo upgrade"},
		{"old hook binary", silent, time.Minute, "[WARN] Version: cmgo v1.0.0 (hook binary version unknown: no output) — fix: cmgo upgrade"},
		{"timeout", slow, 300 * time.Millisecond, "[WARN] Version: cmgo v1.0.0 (hook binary version unknown: timed out after 300ms) — fix: cmgo upgrade"},
	}
	for _, c := range cases {
		e := Env{Version: "v1.0.0", HookTimeout: c.timeout}
		start := time.Now()
		if got := checkVersion(context.Background(), e, c.path); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if d := time.Since(start); c.timeout < time.Second && d > 3*time.Second {
			t.Errorf("%s: took %v", c.name, d)
		}
	}
	missing := filepath.Join(dir, "none")
	got := checkVersion(context.Background(), Env{Version: "v1"}, missing)
	if !strings.HasPrefix(got, "[WARN] Version: cmgo v1 (hook binary version unknown: ") {
		t.Errorf("missing: got %q", got)
	}
	// The injected runner replaces the process.
	e := Env{Version: "v1", HookVersion: func(_ context.Context, p string) (string, error) { return "v1", nil }}
	if got := checkVersion(context.Background(), e, missing); got != "[OK] Version: cmgo v1 (hook binary v1)" {
		t.Errorf("injected: got %q", got)
	}
}

func TestCheckMCP(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude.json")
	if got := checkMCP(p, "/b/cmgo"); got != "[WARN] MCP server: not registered — fix: cmgo install" {
		t.Errorf("no file: %q", got)
	}
	os.WriteFile(p, []byte(`{"mcpServers":{"cmgo":{"type":"stdio","command":"/b/cmgo","args":["serve"]}}}`), 0o600)
	if got := checkMCP(p, "/b/cmgo"); got != "[OK] MCP server: registered (/b/cmgo serve)" {
		t.Errorf("registered: %q", got)
	}
	if got := checkMCP(p, "/new/cmgo"); got != "[WARN] MCP server: registered at /b/cmgo — fix: cmgo install" {
		t.Errorf("old path: %q", got)
	}
}

func TestCheckStatusLine(t *testing.T) {
	cases := []struct {
		st   install.Status
		want string
	}{
		{install.Status{}, "[OK] Statusline: not set"},
		{install.Status{StatusLine: "/b/cmgo-hook statusline", OwnStatusLine: true}, "[OK] Statusline: cmgo"},
		{install.Status{StatusLine: "ccline"}, "[WARN] Statusline: another command is set (ccline)"},
	}
	for _, c := range cases {
		if got := checkStatusLine(c.st); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestCheckTSPlugin(t *testing.T) {
	if got := checkTSPlugin(install.Status{}); got != "[OK] TS plugin: not enabled" {
		t.Errorf("got %q", got)
	}
	got := checkTSPlugin(install.Status{TSPlugins: []string{"context-mode@context-mode", "context-mode@fork"}})
	want := "[WARN] TS plugin: context-mode@context-mode is enabled — both inject routing text; fix: cmgo install --disable-ts-plugin\n" +
		"[WARN] TS plugin: context-mode@fork is enabled — both inject routing text; fix: cmgo install --disable-ts-plugin"
	if got != want {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/doctor/ -v`
Expected: FAIL with "internal/doctor/settings_test.go:40:13: undefined: checkHooks"

- [ ] **Step 3: Write minimal implementation**

`internal/doctor/settings.go`:

```go
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/install"
)

// checkVersion compares cmgo's version with what the hook binary prints.
func checkVersion(ctx context.Context, e Env, hookBin string) string {
	timeout := e.HookTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	run := e.HookVersion
	if run == nil {
		run = runVersion
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hv, err := run(ctx, hookBin)
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("timed out after %v", timeout)
	}
	switch {
	case err != nil:
		return fmt.Sprintf("[WARN] Version: cmgo %s (hook binary version unknown: %v) — fix: cmgo upgrade", e.Version, err)
	case hv != e.Version:
		return fmt.Sprintf("[WARN] Version: cmgo %s (hook binary %s differs) — fix: cmgo upgrade", e.Version, hv)
	}
	return fmt.Sprintf("[OK] Version: cmgo %s (hook binary %s)", e.Version, hv)
}

// runVersion runs `<path> version` and returns the last word it prints:
// cmgo-hook prints "cmgo-hook <v>", an old cmgo hook command's cmgo "cmgo <v>".
func runVersion(ctx context.Context, path string) (string, error) {
	c := exec.CommandContext(ctx, path, "version")
	c.WaitDelay = 100 * time.Millisecond // a killed script's child must not hold stdout open
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		// A cmgo-hook from before the version argument exits 0 silently.
		return "", errors.New("no output")
	}
	return f[len(f)-1], nil
}

// checkHooks reports which HookEvents have a cmgo command in settings.json.
func checkHooks(path string, st install.Status, err error) string {
	if err != nil {
		return fmt.Sprintf("[FAIL] Hooks: could not read %s — %v", path, err)
	}
	var missing []string
	for _, he := range install.HookEvents {
		if !slices.Contains(st.Events, he.Event) {
			missing = append(missing, he.Event)
		}
	}
	if len(missing) > 0 {
		return "[WARN] Hooks: missing " + strings.Join(missing, ", ") + " — fix: cmgo install"
	}
	n := len(install.HookEvents)
	return fmt.Sprintf("[OK] Hooks: %d/%d events registered in %s", n, n, path)
}

// hookBinaryPath is the binary Claude Code runs for cmgo's hooks, else
// cmgo-hook next to cmgo, where cmgo install looks for it.
func hookBinaryPath(st install.Status, binary string) string {
	if st.HookBinary != "" {
		return st.HookBinary
	}
	return filepath.Join(filepath.Dir(binary), "cmgo-hook")
}

func checkHookBinary(path string) string {
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return "[FAIL] Hook binary: not found at " + path + " — fix: cmgo install"
	}
	return "[OK] Hook binary: " + path
}

// checkMCP checks that the cmgo MCP server runs this binary.
func checkMCP(claudeJSON, binary string) string {
	cmd, args, ok := install.MCPServer(claudeJSON)
	switch {
	case !ok:
		return "[WARN] MCP server: not registered — fix: cmgo install"
	case cmd == binary && slices.Equal(args, []string{"serve"}):
		return "[OK] MCP server: registered (" + cmd + " serve)"
	}
	return "[WARN] MCP server: registered at " + cmd + " — fix: cmgo install"
}

func checkStatusLine(st install.Status) string {
	switch {
	case st.OwnStatusLine:
		return "[OK] Statusline: cmgo"
	case st.StatusLine == "":
		return "[OK] Statusline: not set"
	}
	return "[WARN] Statusline: another command is set (" + st.StatusLine + ")"
}

// checkTSPlugin warns once per enabled TS context-mode plugin.
func checkTSPlugin(st install.Status) string {
	if len(st.TSPlugins) == 0 {
		return "[OK] TS plugin: not enabled"
	}
	var lines []string
	for _, k := range st.TSPlugins {
		lines = append(lines, "[WARN] TS plugin: "+k+" is enabled — both inject routing text; fix: cmgo install --disable-ts-plugin")
	}
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/doctor/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/doctor/settings.go internal/doctor/settings_test.go
git commit -m "feat(doctor): version, hooks, hook binary, MCP, statusline and TS plugin checks"
```

### Task 11: Doctor report

**Files:**
- Create: `internal/doctor/report.go`
- Test: `internal/doctor/report_test.go`

**Interfaces:**
- Consumes: `func install.Inspect(settingsPath string) (install.Status, error)`; `func install.Install(o install.Options) error` (test); `checkStorage`, `checkDatabase`, `checkExecutor`, `checkRuntimes` (Task 9) and `checkVersion`, `checkHooks`, `hookBinaryPath`, `checkHookBinary`, `checkMCP`, `checkStatusLine`, `checkTSPlugin` (Task 10) with the signatures listed there
- Produces: `func Report(ctx context.Context, e Env) (text string, failed bool)`

- [ ] **Step 1: Write the failing test**

`internal/doctor/report_test.go`:

```go
package doctor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/install"
)

// healthyEnv installs cmgo into a temp Claude config dir with a fake
// cmgo-hook script, so every check passes.
func healthyEnv(t *testing.T) Env {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "cmgo")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	script(t, filepath.Dir(bin), "cmgo-hook", `echo "cmgo-hook v1.2.3"`)
	e := Env{
		Version:        "v1.2.3",
		Binary:         bin,
		DataDir:        filepath.Join(root, "data"),
		DBPath:         filepath.Join(root, "data", "cmgo.db"),
		SettingsPath:   filepath.Join(root, "claude", "settings.json"),
		ClaudeJSONPath: filepath.Join(root, ".claude.json"),
		Exec:           &fakeExec{res: executor.Result{Stdout: "ok\n"}, langs: []executor.Language{executor.Shell}},
		HookTimeout:    time.Minute, // a fresh script can be slow to start under load
	}
	err := install.Install(install.Options{
		SettingsPath: e.SettingsPath, ClaudeJSONPath: e.ClaudeJSONPath, Binary: bin,
		Stdout: io.Discard, Stderr: io.Discard,
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(e.ClaudeJSONPath, []byte(`{"mcpServers":{"cmgo":{"command":"`+bin+`","args":["serve"]}}}`), 0o600)
	return e
}

func TestReportHealthy(t *testing.T) {
	e := healthyEnv(t)
	text, failed := Report(context.Background(), e)
	if failed {
		t.Errorf("failed on a healthy install:\n%s", text)
	}
	hook := filepath.Join(filepath.Dir(e.Binary), "cmgo-hook")
	want := strings.Join([]string{
		"cmgo doctor",
		"",
		"[OK] Version: cmgo v1.2.3 (hook binary v1.2.3)",
		"[OK] Storage: " + e.DataDir + " (writable)",
		"[OK] Database: " + e.DBPath + " schema v@, FTS5 PASS",
		"[OK] Executor: PASS (shell)",
		"[OK] Runtimes: 1/12 — shell",
		"[OK] Hooks: 7/7 events registered in " + e.SettingsPath,
		"[OK] Hook binary: " + hook,
		"[OK] MCP server: registered (" + e.Binary + " serve)",
		"[OK] Statusline: cmgo",
		"[OK] TS plugin: not enabled",
	}, "\n")
	if got := regexp.MustCompile(`schema v\d+`).ReplaceAllString(text, "schema v@"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReportFailures(t *testing.T) {
	e := healthyEnv(t)
	os.Remove(filepath.Join(filepath.Dir(e.Binary), "cmgo-hook"))
	text, failed := Report(context.Background(), e)
	if !failed || !strings.Contains(text, "\n[FAIL] Hook binary: not found at ") {
		t.Errorf("missing hook binary: failed=%v\n%s", failed, text)
	}

	// An unreadable settings file fails Hooks and drops the lines read from it.
	e = healthyEnv(t)
	os.WriteFile(e.SettingsPath, []byte("{"), 0o644)
	text, failed = Report(context.Background(), e)
	if !failed || !strings.Contains(text, "\n[FAIL] Hooks: could not read "+e.SettingsPath+" — ") || strings.Contains(text, "Statusline") || strings.Contains(text, "TS plugin") {
		t.Errorf("unreadable settings: failed=%v\n%s", failed, text)
	}

	// WARN lines alone do not fail.
	e = healthyEnv(t)
	os.Remove(e.ClaudeJSONPath)
	text, failed = Report(context.Background(), e)
	if failed || !strings.Contains(text, "\n[WARN] MCP server: not registered") {
		t.Errorf("warn only: failed=%v\n%s", failed, text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/doctor/ -run TestReport -v`
Expected: FAIL with "internal/doctor/report_test.go:49:18: undefined: Report"

- [ ] **Step 3: Write minimal implementation**

`internal/doctor/report.go`:

```go
package doctor

import (
	"context"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/install"
)

// Report runs every check in spec §1 order and returns the text, a header
// then one line per check, and whether any line is a [FAIL].
func Report(ctx context.Context, e Env) (text string, failed bool) {
	st, serr := install.Inspect(e.SettingsPath)
	hookBin := hookBinaryPath(st, e.Binary)
	execTimeout := e.ExecTimeout
	if execTimeout <= 0 {
		execTimeout = 5 * time.Second
	}
	lines := []string{
		"cmgo doctor",
		"",
		checkVersion(ctx, e, hookBin),
		checkStorage(e.DataDir),
		checkDatabase(e.DBPath),
		checkExecutor(ctx, e.Exec, execTimeout),
		checkRuntimes(e.Exec),
		checkHooks(e.SettingsPath, st, serr),
		checkHookBinary(hookBin),
		checkMCP(e.ClaudeJSONPath, e.Binary),
	}
	// Without a readable settings file there is no statusline or plugin to report.
	if serr == nil {
		lines = append(lines, checkStatusLine(st), checkTSPlugin(st))
	}
	text = strings.Join(lines, "\n")
	return text, strings.Contains(text, "\n[FAIL] ")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/doctor/ -run TestReport -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/doctor/report.go internal/doctor/report_test.go
git commit -m "feat(doctor): render the spec §1 report and flag FAIL lines"
```

### Task 12: Upgrade steps

**Files:**
- Create: `internal/upgrade/upgrade.go`
- Test: `internal/upgrade/upgrade_test.go`

**Interfaces:**
- Consumes: none (stdlib)
- Produces: `const Module = "github.com/lang315/context-mode-go"`; `type Runner func(dir string, env []string, name string, args ...string) error`; `type Options struct { Source string; DryRun bool; Getenv func(string) string; LookPath func(string) (string, error); Run Runner; Stdout io.Writer }`; `func Upgrade(o Options) error`

- [ ] **Step 1: Write the failing test**

`internal/upgrade/upgrade_test.go`:

```go
package upgrade

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rec records every command and fails the one whose name and args start with failOn.
type rec struct {
	runs   []string
	failOn string
}

func (r *rec) run(dir string, env []string, name string, args ...string) error {
	line := strings.Join(append([]string{name}, args...), " ")
	r.runs = append(r.runs, "dir="+dir+" env="+strings.Join(env, ",")+" "+line)
	if r.failOn != "" && strings.HasPrefix(line, r.failOn) {
		return errors.New("exit status 1")
	}
	return nil
}

// opts returns Options with env vars from vars, go and git on PATH, and a recorder.
func opts(vars map[string]string) (Options, *rec, *bytes.Buffer) {
	r := &rec{}
	var out bytes.Buffer
	return Options{
		Getenv:   func(k string) string { return vars[k] },
		LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
		Run:      r.run,
		Stdout:   &out,
	}, r, &out
}

// sourceDir makes a checkout whose go.mod names module.
func sourceDir(t *testing.T, module string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// cmgo\nmodule "+module+"\n\ngo 1.27.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUpgradeFromSource(t *testing.T) {
	dir := sourceDir(t, Module)
	for _, viaEnv := range []bool{false, true} {
		vars := map[string]string{"GOBIN": "/gobin", "GOPATH": "/gopath"}
		o, r, out := opts(vars)
		if viaEnv {
			vars["CMGO_SOURCE"] = dir
		} else {
			o.Source = dir
			vars["CMGO_SOURCE"] = "/ignored"
		}
		if err := Upgrade(o); err != nil {
			t.Fatalf("viaEnv=%v: %v\n%s", viaEnv, err, out)
		}
		wantRuns := []string{
			"dir= env= git -C " + dir + " pull --ff-only",
			"dir=" + dir + " env= go install ./cmd/cmgo ./cmd/cmgo-hook",
			"dir= env= /gobin/cmgo install",
		}
		if !slices.Equal(r.runs, wantRuns) {
			t.Errorf("viaEnv=%v: runs\n%s\nwant\n%s", viaEnv, strings.Join(r.runs, "\n"), strings.Join(wantRuns, "\n"))
		}
		wantOut := "[x] source " + dir + " is " + Module + "\n" +
			"[x] git -C " + dir + " pull --ff-only\n" +
			"[x] go install ./cmd/cmgo ./cmd/cmgo-hook (in " + dir + ")\n" +
			"[x] /gobin/cmgo install\n"
		if out.String() != wantOut {
			t.Errorf("viaEnv=%v: output\n%s\nwant\n%s", viaEnv, out, wantOut)
		}
	}
}

func TestUpgradeFromModule(t *testing.T) {
	cases := []struct {
		name      string
		vars      map[string]string
		goprivate string
		gobin     string
	}{
		{"GOPATH bin", map[string]string{"GOPATH": "/gp1:/gp2"}, "github.com/lang315/*", "/gp1/bin"},
		{"HOME go bin, GOPRIVATE kept", map[string]string{"HOME": "/home/u", "GOPRIVATE": "example.com/x"}, "example.com/x,github.com/lang315/*", "/home/u/go/bin"},
	}
	for _, c := range cases {
		o, r, out := opts(c.vars)
		if err := Upgrade(o); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		wantRuns := []string{
			"dir= env=GOPRIVATE=" + c.goprivate + " go install " + Module + "/cmd/cmgo@latest " + Module + "/cmd/cmgo-hook@latest",
			"dir= env= " + c.gobin + "/cmgo install",
		}
		if !slices.Equal(r.runs, wantRuns) {
			t.Errorf("%s: runs\n%s", c.name, strings.Join(r.runs, "\n"))
		}
		wantOut := "[x] GOPRIVATE=" + c.goprivate + " go install " + Module + "/cmd/cmgo@latest " + Module + "/cmd/cmgo-hook@latest\n" +
			"[x] " + c.gobin + "/cmgo install\n"
		if out.String() != wantOut {
			t.Errorf("%s: output\n%s", c.name, out)
		}
	}
}

func TestUpgradeDryRun(t *testing.T) {
	o, r, out := opts(map[string]string{"GOBIN": "/gobin"})
	o.DryRun = true
	if err := Upgrade(o); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 0 {
		t.Errorf("dry run ran %v", r.runs)
	}
	want := "dry run: GOPRIVATE=github.com/lang315/* go install " + Module + "/cmd/cmgo@latest " + Module + "/cmd/cmgo-hook@latest\n" +
		"dry run: /gobin/cmgo install\n"
	if out.String() != want {
		t.Errorf("output\n%s", out)
	}
}

func TestUpgradeStopsAtFirstFailure(t *testing.T) {
	dir := sourceDir(t, Module)
	o, r, out := opts(map[string]string{"GOBIN": "/gobin"})
	o.Source = dir
	r.failOn = "go install"
	if err := Upgrade(o); err == nil {
		t.Fatal("no error")
	}
	if len(r.runs) != 2 {
		t.Errorf("runs after the failure: %v", r.runs)
	}
	if !strings.HasSuffix(out.String(), "[ ] go install ./cmd/cmgo ./cmd/cmgo-hook (in "+dir+") — exit status 1\n") {
		t.Errorf("output\n%s", out)
	}
}

func TestUpgradeBadSourceAndMissingTools(t *testing.T) {
	other := sourceDir(t, "example.com/other")
	o, r, out := opts(map[string]string{"GOBIN": "/gobin"})
	o.Source = other
	if err := Upgrade(o); err == nil || len(r.runs) != 0 {
		t.Fatalf("other module: err %v, runs %v", err, r.runs)
	}
	if want := "[ ] source " + other + " is " + Module + " — go.mod names example.com/other\n"; out.String() != want {
		t.Errorf("other module output %q", out)
	}

	o, r, out = opts(map[string]string{"GOBIN": "/gobin"})
	o.Source = sourceDir(t, Module)
	o.LookPath = func(n string) (string, error) {
		if n == "git" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + n, nil
	}
	if err := Upgrade(o); err == nil || len(r.runs) != 0 {
		t.Fatalf("no git: err %v, runs %v", err, r.runs)
	}
	if !strings.HasSuffix(out.String(), " pull --ff-only — git not found on PATH; install git\n") {
		t.Errorf("no git output %q", out)
	}

	o, r, out = opts(map[string]string{"GOBIN": "/gobin"})
	o.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if err := Upgrade(o); err == nil || len(r.runs) != 0 {
		t.Fatalf("no go: err %v, runs %v", err, r.runs)
	}
	if !strings.HasSuffix(out.String(), "@latest — go not found on PATH; install Go from https://go.dev/dl\n") {
		t.Errorf("no go output %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/upgrade/ -v`
Expected: FAIL with "internal/upgrade/upgrade_test.go:30:36: undefined: Options"

- [ ] **Step 3: Write minimal implementation**

`internal/upgrade/upgrade.go`:

```go
// Package upgrade rebuilds cmgo from source and re-runs cmgo install
// (spec §2). There are no release binaries for the private repo, so the Go
// toolchain builds it.
package upgrade

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Module is cmgo's module path.
const Module = "github.com/lang315/context-mode-go"

// Runner runs name with args in dir ("" = the current dir), with env entries
// added to the process environment.
type Runner func(dir string, env []string, name string, args ...string) error

// Options are Upgrade's inputs; tests inject Getenv, LookPath and Run.
type Options struct {
	Source   string // --source; "" falls back to $CMGO_SOURCE, then the module
	DryRun   bool
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Run      Runner
	Stdout   io.Writer
}

// step is one checklist line and the work behind it.
type step struct {
	label string
	run   func() error
}

// Upgrade prints one [x] line per step done and stops at the first failure,
// printing it as [ ] with the error, which it returns. A dry run prints the
// steps and runs nothing.
func Upgrade(o Options) error {
	steps, err := plan(o)
	if err != nil {
		return err
	}
	for _, s := range steps {
		if o.DryRun {
			fmt.Fprintln(o.Stdout, "dry run: "+s.label)
			continue
		}
		if err := s.run(); err != nil {
			fmt.Fprintf(o.Stdout, "[ ] %s — %v\n", s.label, err)
			return err
		}
		fmt.Fprintln(o.Stdout, "[x] "+s.label)
	}
	return nil
}

// plan builds the spec §2 steps. Checking a source dir is a read, not a
// command, so it runs even in a dry run.
func plan(o Options) ([]step, error) {
	var steps []step
	src := o.Source
	if src == "" {
		src = o.Getenv("CMGO_SOURCE")
	}
	if src != "" {
		dir, err := filepath.Abs(src)
		if err == nil {
			err = checkSource(dir)
		}
		label := "source " + dir + " is " + Module
		if err != nil {
			fmt.Fprintf(o.Stdout, "[ ] %s — %v\n", label, err)
			return nil, err
		}
		fmt.Fprintln(o.Stdout, "[x] "+label)
		steps = append(steps,
			o.command("git -C "+dir+" pull --ff-only", "", nil, "git", "-C", dir, "pull", "--ff-only"),
			o.command("go install ./cmd/cmgo ./cmd/cmgo-hook (in "+dir+")", dir, nil, "go", "install", "./cmd/cmgo", "./cmd/cmgo-hook"),
		)
	} else {
		// The repo is private; keep any GOPRIVATE the user already has.
		private := "github.com/lang315/*"
		if cur := o.Getenv("GOPRIVATE"); cur != "" {
			private = cur + "," + private
		}
		args := []string{"install", Module + "/cmd/cmgo@latest", Module + "/cmd/cmgo-hook@latest"}
		env := []string{"GOPRIVATE=" + private}
		steps = append(steps, o.command(env[0]+" go "+strings.Join(args, " "), "", env, "go", args...))
	}
	cmgo := filepath.Join(o.goBin(), "cmgo")
	// The new binary refreshes hooks, MCP registration and statusline.
	return append(steps, o.command(cmgo+" install", "", nil, cmgo, "install")), nil
}

// command is a step that runs name, failing with a hint when go or git is
// not on PATH.
func (o Options) command(label, dir string, env []string, name string, args ...string) step {
	return step{label, func() error {
		if name == "go" || name == "git" {
			if _, err := o.LookPath(name); err != nil {
				hint := "install git"
				if name == "go" {
					hint = "install Go from https://go.dev/dl"
				}
				return fmt.Errorf("%s not found on PATH; %s", name, hint)
			}
		}
		return o.Run(dir, env, name, args...)
	}}
}

// goBin is where go install puts binaries: $GOBIN, else the first $GOPATH
// entry's bin, else ~/go/bin.
// shortcut: a GOBIN set only with `go env -w` is not seen; run `go env GOBIN`
// if that is ever needed.
func (o Options) goBin() string {
	if b := o.Getenv("GOBIN"); b != "" {
		return b
	}
	if gp := filepath.SplitList(o.Getenv("GOPATH")); len(gp) > 0 && gp[0] != "" {
		return filepath.Join(gp[0], "bin")
	}
	return filepath.Join(o.Getenv("HOME"), "go", "bin")
}

// checkSource requires dir/go.mod to name cmgo's module.
func checkSource(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "module" {
			if m := strings.Trim(f[1], `"`); m != Module {
				return fmt.Errorf("go.mod names %s", m)
			}
			return nil
		}
	}
	return errors.New("go.mod has no module line")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/upgrade/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/upgrade/upgrade.go internal/upgrade/upgrade_test.go
git commit -m "feat(upgrade): rebuild cmgo from a source dir or the module, then rerun cmgo install"
```

### Task 13: `cmgo doctor` and `cmgo upgrade` commands

**Files:**
- Create: `cmd/cmgo/doctor.go`
- Test: `cmd/cmgo/doctor_test.go`
- Create: `cmd/cmgo/upgrade.go`
- Test: `cmd/cmgo/upgrade_test.go`

**Interfaces:**
- Consumes: `func config.Load(getenv func(string) string, getwd func() (string, error)) (config.Config, error)`; `func executor.New(projectDir string) *executor.Executor`; `func doctor.NewEnv(cfg config.Config, version string, ex doctor.Executor) doctor.Env`; `func doctor.Report(ctx context.Context, e doctor.Env) (string, bool)`; `func upgrade.Upgrade(o upgrade.Options) error`; `type upgrade.Runner func(dir string, env []string, name string, args ...string) error`; `func install.Install(o install.Options) error` (test); package var `version string` (Task 5)
- Produces: unexported `func doctorCmd(args []string, stdout, stderr io.Writer) int`; `func upgradeCmd(args []string, stdout, stderr io.Writer) int`; `func runCommand(out io.Writer) upgrade.Runner`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/doctor_test.go`:

```go
package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/install"
)

// doctorHome points HOME, CMGO_DIR and the project at a temp dir.
func doctorHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CMGO_DIR", filepath.Join(root, "data"))
	t.Setenv("CMGO_PROJECT_DIR", root)
	return root
}

func TestDoctorCmdFails(t *testing.T) {
	doctorHome(t)
	var out, errb bytes.Buffer
	// No settings file: the hook binary is looked for next to the test binary.
	code := doctorCmd(nil, &out, &errb)
	if code != 1 || !strings.HasPrefix(out.String(), "cmgo doctor\n\n[") || !strings.Contains(out.String(), "\n[FAIL] Hook binary: not found at ") {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "\n[WARN] Hooks: missing PreToolUse, ") || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestDoctorCmdPasses(t *testing.T) {
	root := doctorHome(t)
	hook := filepath.Join(root, "bin", "cmgo-hook")
	os.MkdirAll(filepath.Dir(hook), 0o755)
	os.WriteFile(hook, []byte("#!/bin/sh\necho cmgo-hook "+version+"\n"), 0o755)
	err := install.Install(install.Options{
		SettingsPath: filepath.Join(root, "home", ".claude", "settings.json"), ClaudeJSONPath: filepath.Join(root, "home", ".claude.json"),
		Binary: filepath.Join(root, "bin", "cmgo"), HookBinary: hook, Stdout: io.Discard, Stderr: io.Discard,
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
	})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := doctorCmd(nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	// The Version line is not checked: the fake script may miss the 2 s
	// limit under load, and a WARN does not change the exit code.
	if !strings.Contains(out.String(), "\n[OK] Hooks: 7/7 events registered in ") || !strings.Contains(out.String(), "\n[OK] Hook binary: "+hook+"\n") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestDoctorCmdUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := doctorCmd([]string{"extra"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}
```

`cmd/cmgo/upgrade_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeCmdDryRun(t *testing.T) {
	t.Setenv("CMGO_SOURCE", "")
	t.Setenv("GOBIN", "/gobin")
	var out, errb bytes.Buffer
	if code := upgradeCmd([]string{"--dry-run"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "dry run: GOPRIVATE=") || !strings.HasSuffix(out.String(), "dry run: /gobin/cmgo install\n") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestUpgradeCmdBadSource(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := upgradeCmd([]string{"--source", dir, "--dry-run"}, &out, &errb); code != 1 || !strings.HasPrefix(out.String(), "[ ] source "+dir) {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

func TestUpgradeCmdUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := upgradeCmd([]string{"--nope"}, &out, &errb); code != 2 {
		t.Fatalf("bad flag: exit %d", code)
	}
	if code := upgradeCmd([]string{"extra"}, &out, &errb); code != 2 {
		t.Fatalf("extra arg: exit %d", code)
	}
}

// runCommand runs in dir with the extra env and streams output.
func TestRunCommand(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	var out bytes.Buffer
	if err := runCommand(&out)(dir, []string{"CMGO_TEST_X=1"}, "sh", "-c", `echo "$CMGO_TEST_X $(pwd)"; echo err >&2`); err != nil {
		t.Fatal(err)
	}
	if out.String() != "1 "+dir+"\nerr\n" {
		t.Fatalf("got %q", out.String())
	}
	if err := runCommand(&out)("", nil, "sh", "-c", "exit 3"); err == nil {
		t.Fatal("exit 3: no error")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run 'TestDoctorCmd|TestUpgradeCmd|TestRunCommand' -v`
Expected: FAIL with "cmd/cmgo/doctor_test.go:30:10: undefined: doctorCmd"

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo/doctor.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/doctor"
	"github.com/lang315/context-mode-go/internal/executor"
)

// doctorCmd runs `cmgo doctor` (spec §1): it prints the report and exits 1
// when a line is [FAIL].
func doctorCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "cmgo doctor: unexpected argument %q\n", args[0])
		return 2
	}
	cfg, err := config.Load(os.Getenv, os.Getwd)
	if err != nil {
		fmt.Fprintf(stderr, "cmgo: config: %v\n", err)
		return 1
	}
	ex := executor.New(cfg.ProjectDir)
	defer ex.Shutdown()
	text, failed := doctor.Report(context.Background(), doctor.NewEnv(cfg, version, ex))
	fmt.Fprintln(stdout, text)
	if failed {
		return 1
	}
	return 0
}
```

`cmd/cmgo/upgrade.go`:

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/lang315/context-mode-go/internal/upgrade"
)

// upgradeCmd runs `cmgo upgrade [--source <dir>] [--dry-run]` (spec §2). A
// usage error exits 2, a failed step 1.
func upgradeCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cmgo upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "cmgo source checkout (default: $CMGO_SOURCE, else go install the module)")
	dryRun := fs.Bool("dry-run", false, "print the steps without running them")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cmgo upgrade: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	err := upgrade.Upgrade(upgrade.Options{
		Source:   *source,
		DryRun:   *dryRun,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Run:      runCommand(stderr),
		Stdout:   stdout,
	})
	if err != nil {
		return 1
	}
	return 0
}

// runCommand runs each step with its output streamed to out, so go and git
// errors show above the step's [ ] line.
func runCommand(out io.Writer) upgrade.Runner {
	return func(dir string, env []string, name string, args ...string) error {
		c := exec.Command(name, args...)
		c.Dir = dir
		if len(env) > 0 {
			c.Env = append(os.Environ(), env...)
		}
		c.Stdout, c.Stderr = out, out
		return c.Run()
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cmgo/ -run 'TestDoctorCmd|TestUpgradeCmd|TestRunCommand' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/doctor.go cmd/cmgo/doctor_test.go cmd/cmgo/upgrade.go cmd/cmgo/upgrade_test.go
git commit -m "feat(cmgo): doctor and upgrade commands"
```

### Task 14: `ctx_doctor` and `ctx_upgrade` MCP tools

**Files:**
- Create: `internal/mcpserver/doctor.go`
- Create: `internal/mcpserver/upgrade.go`
- Modify: `internal/mcpserver/server.go`
- Test: `internal/mcpserver/doctor_test.go`
- Test: `internal/mcpserver/tools_list_test.go`
- Test: `cmd/cmgo/serve_test.go`

**Interfaces:**
- Consumes: `func doctor.NewEnv(cfg config.Config, version string, ex doctor.Executor) doctor.Env`; `func doctor.Report(ctx context.Context, e doctor.Env) (string, bool)`; `func TextResult(text string, isError bool) *mcp.CallToolResult`; `func (r *StatsRecorder) Record(d *Deps, tool string, raw int64, res *mcp.CallToolResult)`; test harness `func newHarness(t *testing.T, now func() time.Time) *harness`
- Produces: `Deps.Binary string` ("" = this executable); unexported `registerDoctor(s *mcp.Server, d *Deps, version string)`, `registerUpgrade(s *mcp.Server, d *Deps)`, `upgradeText(binary string) string`; tools `ctx_doctor`, `ctx_upgrade` (9 tools)

- [ ] **Step 1: Write the failing test**

`internal/mcpserver/doctor_test.go`:

````go
package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorTool(t *testing.T) {
	h := newHarness(t, nil)
	bin := filepath.Join(t.TempDir(), "cmgo")
	os.WriteFile(filepath.Join(filepath.Dir(bin), "cmgo-hook"), []byte("#!/bin/sh\necho cmgo-hook test\n"), 0o755)
	h.Deps.Binary = bin
	text, isErr := h.call("ctx_doctor", `{}`)
	// The Version line depends on the fake script starting within 2 s, so it
	// is only checked for its cmgo half.
	if isErr || !strings.HasPrefix(text, "cmgo doctor\n\n[") || !strings.Contains(text, "] Version: cmgo test (hook binary ") {
		t.Fatalf("isErr=%v\n%s", isErr, text)
	}
	if !strings.Contains(text, "\n[OK] Storage: "+h.Deps.Cfg.DataDir+" (writable)\n[OK] Database: "+h.Deps.Cfg.DBPath+" schema v") {
		t.Fatalf("isErr=%v\n%s", isErr, text)
	}
	// No settings.json in the temp home: hooks missing, statusline not set.
	for _, want := range []string{"\n[OK] Executor: PASS (shell)\n", "\n[WARN] Hooks: missing PreToolUse, ", "\n[OK] Hook binary: " + filepath.Join(filepath.Dir(bin), "cmgo-hook") + "\n", "\n[WARN] MCP server: not registered — fix: cmgo install\n", "\n[OK] Statusline: not set\n[OK] TS plugin: not enabled"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
}

func TestUpgradeTool(t *testing.T) {
	h := newHarness(t, nil)
	h.Deps.Binary = "/opt/cmgo/bin/cmgo"
	text, isErr := h.call("ctx_upgrade", `{}`)
	want := "## ctx-upgrade\n\nRun this command using your shell execution tool:\n\n```\n\"/opt/cmgo/bin/cmgo\" upgrade\n```\n\n" +
		"After the command completes, display results as a markdown checklist:\n- `[x]` for success, `[ ]` for failure\n- Example format:\n" +
		"  ```\n  ## cmgo upgrade\n  - [x] git -C ~/src/context-mode-go pull --ff-only\n  - [x] go install ./cmd/cmgo ./cmd/cmgo-hook (in ~/src/context-mode-go)\n  - [x] ~/go/bin/cmgo install\n  ```\n" +
		"- Tell the user to restart their session to pick up the new version."
	if isErr || text != want {
		t.Fatalf("isErr=%v\ngot:\n%s\nwant:\n%s", isErr, text, want)
	}
}
````

`internal/mcpserver/tools_list_test.go`:

```go
package mcpserver

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func TestServerRegistersNineTools(t *testing.T) {
	h := newHarness(t, nil)
	res, err := h.Session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"ctx_batch_execute", "ctx_doctor", "ctx_execute", "ctx_execute_file", "ctx_fetch_and_index", "ctx_purge", "ctx_search", "ctx_stats", "ctx_upgrade"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}
```

`cmd/cmgo/serve_test.go`:

```go
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/procinfo"
)

// rpcPeer drives serve() over in-process pipes with newline-delimited JSON-RPC.
type rpcPeer struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	done chan int
	errb *bytes.Buffer
}

func startServe(t *testing.T, env map[string]string) *rpcPeer {
	t.Helper()
	return startServeCtx(t, context.Background(), env)
}

func startServeCtx(t *testing.T, ctx context.Context, env map[string]string) *rpcPeer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errb := &bytes.Buffer{}
	p := &rpcPeer{t: t, in: inW, out: bufio.NewReader(outR), done: make(chan int, 1), errb: errb}
	go func() {
		p.done <- serve(ctx, serveIO{
			getenv: func(k string) string { return env[k] },
			getwd:  os.Getwd,
			in:     inR,
			out:    outW,
			stderr: errb,
		})
		outW.Close()
	}()
	return p
}

func (p *rpcPeer) send(msg map[string]any) {
	p.t.Helper()
	b, _ := json.Marshal(msg)
	written := make(chan error, 1)
	go func() { _, err := p.in.Write(append(b, '\n')); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			p.t.Fatal(err)
		}
	case code := <-p.done:
		p.t.Fatalf("server exited with %d before reading the request (stderr: %s)", code, p.errb.String())
	case <-time.After(10 * time.Second):
		p.t.Fatalf("server did not read the request within 10s (stderr: %s)", p.errb.String())
	}
}

func (p *rpcPeer) recv(id int) map[string]any {
	p.t.Helper()
	for {
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			p.t.Fatalf("read: %v (stderr: %s)", err, p.errb.String())
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			p.t.Fatalf("bad frame %q: %v", line, err)
		}
		if got, ok := m["id"].(float64); ok && int(got) == id {
			return m
		}
	}
}

func (p *rpcPeer) initialize() {
	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"},
	}})
	if r := p.recv(1); r["error"] != nil {
		p.t.Fatalf("initialize: %v", r["error"])
	}
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func testEnv(t *testing.T) map[string]string {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": filepath.Join(root, "home"), "CMGO_DIR": filepath.Join(root, "data"), "CMGO_PROJECT_DIR": project}
}

func TestServeListsNineToolsAndExitsOnEOF(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	r := p.recv(2)
	var names []string
	for _, tool := range r["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	want := "ctx_batch_execute,ctx_doctor,ctx_execute,ctx_execute_file,ctx_fetch_and_index,ctx_purge,ctx_search,ctx_stats,ctx_upgrade"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
	p.in.Close()
	select {
	case code := <-p.done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit after stdin EOF")
	}
	if _, err := os.Stat(filepath.Join(env["CMGO_DIR"], "cmgo.db")); err != nil {
		t.Fatalf("db not created: %v", err)
	}
}

// Spec §2.5: the MCP-ready sentinel lives exactly as long as serve.
func TestServeWritesSentinel(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize() // the store is open, so the sentinel is written
	sentinel := filepath.Join(env["CMGO_DIR"], "run", fmt.Sprintf("mcp-ready-%d", os.Getpid()))
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != fmt.Sprint(os.Getpid()) {
		t.Fatalf("sentinel %q: %v", b, err)
	}
	p.in.Close()
	if code := <-p.done; code != 0 {
		t.Fatalf("exit %d: %s", code, p.errb.String())
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("sentinel left after serve returned")
	}
}

// A sentinel that cannot be written costs the redirects, not the server.
func TestServeSentinelErrorIsAWarning(t *testing.T) {
	env := testEnv(t)
	os.MkdirAll(env["CMGO_DIR"], 0o755)
	os.WriteFile(filepath.Join(env["CMGO_DIR"], "run"), []byte("x"), 0o644) // run/ cannot be a dir
	p := startServe(t, env)
	p.initialize()
	p.in.Close()
	if code := <-p.done; code != 0 || !strings.Contains(p.errb.String(), "cmgo: mcp-ready sentinel skipped:") {
		t.Fatalf("exit %d, stderr %q", code, p.errb.String())
	}
}

func TestServeFailsOnUnusableDataDir(t *testing.T) {
	env := testEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	env["CMGO_DIR"] = filepath.Join(blocker, "sub") // parent is a file
	p := startServe(t, env)
	select {
	case code := <-p.done:
		if code != 1 || !strings.Contains(p.errb.String(), "cmgo: open") {
			t.Fatalf("exit %d stderr %q", code, p.errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve should fail fast")
	}
}

// pgrepCount counts processes whose command line contains pattern.
func pgrepCount(t *testing.T, pattern string) int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", pattern).Output()
	return len(strings.Fields(string(out)))
}

// Review focus: Claude Code closing a session while a foreground command is
// still running must not leave that command (or its children) behind.
func TestServeShutdownKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	// Built at runtime so no other command line (e.g. the shell running go test) contains it.
	marker := fmt.Sprintf("sleep %d", 4000+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker + " & " + marker + "; wait"},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 2 {
		t.Fatal("command never started")
	}
	p.in.Close()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit while a foreground command was running")
	}
	deadline = time.Now().Add(3 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after shutdown", n)
	}
}

// SIGINT/SIGTERM reach serve as a cancelled ctx: a running foreground command
// with no timeout must be killed and serve must return promptly.
func TestServeCancelKillsForegroundRun(t *testing.T) {
	env := testEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := startServeCtx(t, ctx, env)
	p.initialize()
	marker := fmt.Sprintf("sleep %d", 4001+os.Getpid()%5000)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": marker},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for pgrepCount(t, marker) < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pgrepCount(t, marker) < 1 {
		t.Fatal("command never started")
	}
	cancel()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		exec.Command("pkill", "-f", marker).Run()
		t.Fatal("serve did not return within 3s of cancel")
	}
	deadline = time.Now().Add(2 * time.Second)
	for pgrepCount(t, marker) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := pgrepCount(t, marker); n > 0 {
		exec.Command("pkill", "-f", marker).Run()
		t.Fatalf("%d process(es) left running after cancel", n)
	}
}

// Review focus: two sessions on the same project share one DB; concurrent
// indexing from both must not surface SQLITE_BUSY to the model.
func TestServeConcurrentSessionsSameDB(t *testing.T) {
	env := testEnv(t)
	a, b := startServe(t, env), startServe(t, env)
	a.initialize()
	b.initialize()
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for _, p := range []*rpcPeer{a, b} {
		wg.Add(1)
		go func(p *rpcPeer) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				id := 100 + i
				p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
					"name": "ctx_batch_execute", "arguments": map[string]any{
						"commands": []any{map[string]any{"label": fmt.Sprintf("seq%d", i), "command": "seq 1 2000"}},
						"queries":  []any{"1999"},
					},
				}})
				r := p.recv(id)
				res, _ := r["result"].(map[string]any)
				if r["error"] != nil || res == nil || res["isError"] == true {
					errs <- fmt.Sprintf("call %d: %v %v", i, r["error"], res)
				}
			}
		}(p)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	a.in.Close()
	b.in.Close()
	<-a.done
	<-b.done
}

// ctx_execute checks deny rules itself; the global ones live in
// $CLAUDE_CONFIG_DIR/settings.json when that is set.
func TestServeDeniesFromClaudeConfigDir(t *testing.T) {
	env := testEnv(t)
	cfg := filepath.Join(t.TempDir(), "claudecfg")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"permissions":{"deny":["Bash(echo cmgodenied*)"]}}`), 0o644)
	env["CLAUDE_CONFIG_DIR"] = cfg
	p := startServe(t, env)
	p.initialize()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "ctx_execute", "arguments": map[string]any{"language": "shell", "code": "echo cmgodenied"},
	}})
	res := p.recv(2)["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] != true || !strings.Contains(text, "deny") {
		t.Fatalf("not denied: %v", res)
	}
	p.in.Close()
	<-p.done
}

func TestNewStatsRecorder(t *testing.T) {
	procs := map[int]procinfo.Info{
		500: {PPID: 400, Exe: "/x/cmgo", Arg0: "cmgo"},
		400: {PPID: 300, Exe: "/usr/bin/node", Arg0: "node"},
		300: {PPID: 1, Exe: "/h/.local/share/claude/versions/2.1.304", Arg0: "claude"},
	}
	look := func(pid int) (procinfo.Info, error) {
		p, ok := procs[pid]
		if !ok {
			return procinfo.Info{}, fmt.Errorf("no pid %d", pid)
		}
		return p, nil
	}
	start := time.Date(2026, 10, 21, 9, 30, 5, 123456789, time.FixedZone("x", 7200))
	r := newStatsRecorder(500, start, look)
	want := fmt.Sprintf("500-%d", start.UnixNano())
	if r.ServerID != want || r.CCPID != 300 || r.StartedAt != "2026-10-21 07:30:05" {
		t.Fatalf("recorder = %+v, want server %s", r, want)
	}
	if r := newStatsRecorder(400, start, look); r.CCPID != 300 {
		t.Fatalf("from node: cc_pid %d", r.CCPID)
	}
	if r := newStatsRecorder(300, start, look); r.CCPID != 0 { // no claude ancestor above it
		t.Fatalf("from claude itself: cc_pid %d", r.CCPID)
	}
}

// serve records every tool call; ctx_stats reads the rows back.
func TestServeRecordsToolStats(t *testing.T) {
	env := testEnv(t)
	p := startServe(t, env)
	p.initialize()
	call := func(id int, name string, args map[string]any) string {
		p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		res := p.recv(id)["result"].(map[string]any)
		return res["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	call(2, "ctx_execute", map[string]any{"language": "shell", "code": "echo hi"})
	call(3, "ctx_execute", map[string]any{"language": "shell", "code": "echo again"})
	if text := call(4, "ctx_stats", map[string]any{}); !strings.Contains(text, "\n  ctx_execute               2 calls") || !strings.HasSuffix(text, "\n\ncmgo "+version) {
		t.Fatalf("ctx_stats:\n%s", text)
	}
	p.in.Close()
	<-p.done
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ ./cmd/cmgo/ -run 'TestDoctorTool|TestUpgradeTool|TestServerRegistersNineTools|TestServeListsNineToolsAndExitsOnEOF' -v`
Expected: FAIL with "internal/mcpserver/doctor_test.go:14:9: h.Deps.Binary undefined (type *Deps has no field or method Binary)"

- [ ] **Step 3: Write minimal implementation**

`internal/mcpserver/doctor.go`:

```go
package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/lang315/context-mode-go/internal/doctor"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// doctorDescription is S:4151-4154 with cmgo for context-mode.
const doctorDescription = "Diagnose the cmgo installation. Runs all checks server-side and " +
	"returns a plain-text status report with [OK]/[FAIL]/[WARN] prefixes " +
	"(renderer-safe across MCP clients). No CLI execution needed."

// registerDoctor registers ctx_doctor (spec §1): the `cmgo doctor` text,
// run inside this server.
func registerDoctor(s *mcp.Server, d *Deps, version string) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_doctor",
		Title:       "Run Diagnostics",
		Description: doctorDescription,
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: new(bool), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		e := doctor.NewEnv(d.Cfg, version, d.Exec)
		if d.Binary != "" {
			e.Binary = d.Binary
		}
		text, _ := doctor.Report(ctx, e)
		res := TextResult(text, false)
		d.Stats.Record(d, "ctx_doctor", 0, res)
		return res, nil
	})
}
```

`internal/mcpserver/upgrade.go`:

````go
package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// upgradeDescription is S:4282-4285 with cmgo for context-mode.
const upgradeDescription = "Upgrade cmgo to the latest version. Returns a shell command to execute. " +
	"You MUST run the returned command using your shell tool (Bash, shell_execute, " +
	"run_in_terminal, etc.) and display the output as a checklist. " +
	"Tell the user to restart their session after upgrade."

// registerUpgrade registers ctx_upgrade. As in TS (S:4270-4427) it runs
// nothing: it hands the model `cmgo upgrade` to run in its shell.
func registerUpgrade(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_upgrade",
		Title:       "Upgrade Plugin",
		Description: upgradeDescription,
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(bool), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := TextResult(upgradeText(d.Binary), false)
		d.Stats.Record(d, "ctx_upgrade", 0, res)
		return res, nil
	})
}

// upgradeText is the TS reply (S:4401-4421) with the cmgo command and an
// example checklist of cmgo upgrade's own steps. binary "" means this
// executable.
func upgradeText(binary string) string {
	if binary == "" {
		if p, err := os.Executable(); err == nil {
			binary, _ = filepath.EvalSymlinks(p)
		}
	}
	return "## ctx-upgrade\n\n" +
		"Run this command using your shell execution tool:\n\n" +
		"```\n\"" + binary + "\" upgrade\n```\n\n" +
		"After the command completes, display results as a markdown checklist:\n" +
		"- `[x]` for success, `[ ]` for failure\n" +
		"- Example format:\n" +
		"  ```\n" +
		"  ## cmgo upgrade\n" +
		"  - [x] git -C ~/src/context-mode-go pull --ff-only\n" +
		"  - [x] go install ./cmd/cmgo ./cmd/cmgo-hook (in ~/src/context-mode-go)\n" +
		"  - [x] ~/go/bin/cmgo install\n" +
		"  ```\n" +
		"- Tell the user to restart their session to pick up the new version."
}
````

`internal/mcpserver/server.go`:

```go
// Package mcpserver registers cmgo's MCP tools and formats their output.
package mcpserver

import (
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps are the long-lived services every tool handler uses.
type Deps struct {
	Cfg    config.Config
	Store  *store.Store
	Exec   *executor.Executor
	Policy *security.Policy
	Fetch  *fetch.Fetcher
	Now    func() time.Time // injectable clock; defaults to time.Now
	Stats  *StatsRecorder   // nil records no tool_stats
	Binary string           // cmgo's path for ctx_doctor and ctx_upgrade; "" = this executable
}

// New builds the MCP server with all nine tools registered.
func New(d *Deps, version string) *mcp.Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "cmgo", Version: version}, nil)
	registerExecute(s, d)
	registerExecuteFile(s, d)
	registerBatch(s, d)
	registerSearch(s, d)
	registerFetchAndIndex(s, d)
	registerPurge(s, d)
	registerStats(s, d, version)
	registerDoctor(s, d, version)
	registerUpgrade(s, d)
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpserver/ ./cmd/cmgo/ -run 'TestDoctorTool|TestUpgradeTool|TestServerRegistersNineTools|TestServeListsNineToolsAndExitsOnEOF' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/doctor.go internal/mcpserver/upgrade.go internal/mcpserver/server.go internal/mcpserver/doctor_test.go internal/mcpserver/tools_list_test.go cmd/cmgo/serve_test.go
git commit -m "feat(mcpserver): ctx_doctor and ctx_upgrade tools"
```

### Task 15: Routing block gains `ctx doctor` and `ctx upgrade`

**Files:**
- Modify: `internal/routing/text.go`
- Test: `internal/routing/text_test.go`
- Test: `internal/routing/golden_test.go`

**Interfaces:**
- Consumes: `testdata/golden/hooks-strings.json` routing block `(true, false)`
- Produces: `RoutingBlock(true, false)` equals the TS `includeCommands: true` text with `mcp__cmgo__` names: 4239 bytes, sha256 `52c54ed411336a4795911e32dee0ea8c103c0d41b793d942355d3eca57ca4e47`

- [ ] **Step 1: Write the failing test**

`internal/routing/text_test.go`:

```go
package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The expected texts are the TS output (hooks/routing-block.mjs and
// hooks/core/routing.mjs at a5e6dd31) with mcp__plugin_context-mode_context-mode__
// replaced by mcp__cmgo__, pinned by byte length and sha256. testdata/golden/
// hooks-strings.json carries the full texts (TestGoldenStrings).
type pinned struct {
	name string
	size int
	hash string
}

func checkPinned(t *testing.T, p pinned, got string) {
	t.Helper()
	sum := sha256.Sum256([]byte(got))
	if len(got) != p.size || hex.EncodeToString(sum[:]) != p.hash {
		t.Errorf("%s: %d bytes, sha256 %x; want %d bytes, %s\ngot:\n%s", p.name, len(got), sum, p.size, p.hash, got)
	}
}

func TestRoutingBlock(t *testing.T) {
	cases := []struct {
		commands, bootstrap bool
		want                pinned
	}{
		// The full TS text: every <ctx_commands> entry has a cmgo tool (spec §4 of sub-project 5).
		{true, false, pinned{"routing block with commands", 4239, "52c54ed411336a4795911e32dee0ea8c103c0d41b793d942355d3eca57ca4e47"}},
		{false, true, pinned{"agent routing suffix", 4105, "8c756c25026a96477fac96011f6869f83d1a4974640c5bff5373d76ba5de762c"}},
		{false, false, pinned{"SessionStart routing block", 3481, "6b7c33490c32133a9bfe5a11b240236f6732a0f700c581ad90f46d6868edd023"}},
	}
	for _, c := range cases {
		checkPinned(t, c.want, RoutingBlock(c.commands, c.bootstrap))
	}
	if strings.Contains(RoutingBlock(false, false), "mcp__plugin_") {
		t.Error("routing block still names the TS plugin tools")
	}
}

func TestGuidanceAndRedirectTexts(t *testing.T) {
	cases := []struct {
		got  string
		want pinned
	}{
		{bashGuidance, pinned{"guidance-bash", 597, "8e5b6e0792263eb68968bdf22bf1ad96db5ab44c58b3d0b686ef59144d885100"}},
		{readGuidance, pinned{"guidance-read", 377, "480cdbe6726c56c5e9d30043d59b710fd69760afa3bcd18c4bcc1b84d1f5f11c"}},
		{grepGuidance, pinned{"guidance-grep", 477, "3759bf1be31f94b6c5f49281c80144dc629201c1410ff6a1850ce98729713c88"}},
		{curlMessage, pinned{"deny-curl-wget", 467, "8445533370d36a76361daaa46de4c9046d37a4c49980a04d71bca1374a7207b9"}},
		{inlineMessage, pinned{"deny-inline-http", 342, "da11642dff800f1658d28902eddc419ada0b3f3e0912f3e96b4dfb02f4bdc356"}},
		{webFetchMessage("https://example.com/a"), pinned{"deny-webfetch-example", 563, "4a134a67a5f425ddb67b4c38b81e67d9a6939d3f16a1976f734756a8025c9ed9"}},
		{buildMessage("./gradlew build"), pinned{"deny-build-gradlew-example", 402, "f97ec9b71a06e587b3529da87527eb15345ea76f4c53cdaf03dfd48bb2412ef6"}},
	}
	for _, c := range cases {
		checkPinned(t, c.want, c.got)
	}
}

func TestBuildMessageEscapes(t *testing.T) {
	// routing.mjs:811: backslashes doubled, then quotes escaped.
	got := buildMessage(`mvn "test" C:\x`)
	if want := `code: \"mvn \"test\" C:\\x 2>&1 | tail -30\")`; !strings.Contains(got, want) {
		t.Errorf("buildMessage = %q, want it to contain %q", got, want)
	}
}
```

`internal/routing/golden_test.go`:

```go
package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/security"
)

// goldenCase is one case of ../../testdata/golden/hooks-pretooluse.json,
// written by tools/golden-gen/gen-hooks.mjs from TS routePreToolUse +
// formatDecision, each case in a fresh node process. output is the exact
// stdout of the TS hook, or null when it writes nothing. Tool names are
// already cmgo's; <BASE> stands for the generator's temp dir.
type goldenCase struct {
	I          int             `json:"i"`
	Tool       string          `json:"tool"`
	Input      json.RawMessage `json:"input"`
	ProjectDir string          `json:"projectDir"` // "<proj>" or ""
	SessionID  string          `json:"sessionId"`
	Subagent   bool            `json:"subagent"`
	Output     *string         `json:"output"`
}

// knownDiff is a case where cmgo differs from TS on purpose, one row of
// testdata/golden/HOOKS-KNOWN-DIFFS.md. want is the cmgo output ("" for none).
type knownDiff struct{ reason, want string }

func knownDiffs() map[int]knownDiff {
	const ctxMatcher = "spec §2.3: ctx_execute, ctx_execute_file and ctx_batch_execute are not PreToolUse matchers; cmgo serve checks deny rules itself"
	return map[int]knownDiff{
		25: {ctxMatcher, ""},
		26: {ctxMatcher, ""},
		28: {ctxMatcher, ""},
		29: {ctxMatcher, ""},
		30: {ctxMatcher, ""},
		31: {"spec §2.3: external-MCP guidance dropped (the TS mcp__ matcher never fires, R1 P11)", ""},
	}
}

func TestGoldenPreToolUse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "hooks-pretooluse.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	raw = []byte(strings.ReplaceAll(string(raw), "<BASE>", base))
	var g struct {
		TSCommit string       `json:"tsCommit"`
		Cases    []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" {
		t.Fatalf("hooks-pretooluse.json: commit %q", g.TSCommit)
	}
	cases := g.Cases

	// The state gen-hooks.mjs sets up: two files around the 50,000-byte
	// Read threshold, project rules, an empty home, a live MCP sentinel.
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proj := filepath.Join(base, "proj")
	write(filepath.Join(proj, ".claude", "settings.json"),
		`{"permissions":{"deny":["Bash(sudo *)","Read(**/.env)"],"ask":["Bash(git push:*)"],"allow":["Bash(curl:*)"]}}`)
	write(filepath.Join(base, "big.txt"), strings.Repeat("x", 50001))
	write(filepath.Join(base, "small.txt"), strings.Repeat("x", 50000))
	home := filepath.Join(base, "home")
	run := filepath.Join(base, "run")
	remove, err := WriteSentinel(run, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remove()

	diffs := knownDiffs()
	seen := map[int]bool{}
	for _, c := range cases { // in order: session s1's guidance markers carry over
		in, err := hookio.ParseObject(c.Input)
		if err != nil {
			t.Fatal(err)
		}
		pd := ""
		if c.ProjectDir == "<proj>" {
			pd = proj
		}
		d := Deps{
			ProjectDir: pd,
			SessionID:  c.SessionID,
			RunDir:     run,
			Policy:     security.NewPolicy(pd, home),
			Getenv:     func(string) string { return "" },
			MCPReady:   func() bool { return MCPReady(run) },
		}
		got := string(Format(Route(c.Tool, in, c.Subagent, d)))
		ts := ""
		if c.Output != nil {
			ts = *c.Output
		}
		if kd, ok := diffs[c.I]; ok {
			seen[c.I] = true
			if got == ts {
				t.Errorf("case %d (%s): listed difference is gone (%s)", c.I, c.Tool, kd.reason)
			} else if got != kd.want {
				t.Errorf("case %d (%s):\n got %q\nwant %q", c.I, c.Tool, got, kd.want)
			}
			continue
		}
		if got != ts {
			t.Errorf("case %d (%s %s):\n got %q\nwant %q", c.I, c.Tool, c.Input, got, ts)
		}
	}
	if len(cases) != 37 || len(seen) != len(diffs) {
		t.Errorf("%d cases, %d of %d known diffs seen", len(cases), len(seen), len(diffs))
	}
}

// TestGoldenStrings checks the texts in ../../testdata/golden/hooks-strings.json,
// rendered by gen-hooks.mjs from hooks/routing-block.mjs with the mcp__cmgo__
// namer, and the redirect texts taken from TS routePreToolUse decisions.
func TestGoldenStrings(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "hooks-strings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		TSCommit      string `json:"tsCommit"`
		RoutingBlocks []struct {
			Name                string `json:"name"`
			IncludeCommands     bool   `json:"includeCommands"`
			ToolSearchBootstrap bool   `json:"toolSearchBootstrap"`
			Text                string `json:"text"`
		} `json:"routingBlocks"`
		Texts map[string]string `json:"texts"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" || len(g.RoutingBlocks) != 3 {
		t.Fatalf("hooks-strings.json: commit %q, %d routing blocks", g.TSCommit, len(g.RoutingBlocks))
	}
	for _, b := range g.RoutingBlocks {
		if got := RoutingBlock(b.IncludeCommands, b.ToolSearchBootstrap); got != b.Text {
			t.Errorf("%s: RoutingBlock(%v, %v) differs\ngot:\n%s", b.Name, b.IncludeCommands, b.ToolSearchBootstrap, got)
		}
	}
	got := map[string]string{
		"guidance-bash":              bashGuidance,
		"guidance-read":              readGuidance,
		"guidance-grep":              grepGuidance,
		"deny-curl-wget":             curlMessage,
		"deny-inline-http":           inlineMessage,
		"deny-build-gradlew-example": buildMessage("./gradlew build"),
		"deny-webfetch-example":      webFetchMessage("https://example.com/a"),
	}
	if len(g.Texts) != len(got) {
		t.Errorf("hooks-strings.json has %d texts, want %d", len(g.Texts), len(got))
	}
	for name, want := range g.Texts {
		if got[name] != want {
			t.Errorf("%s:\ngot  %q\nwant %q", name, got[name], want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing/ -run 'TestRoutingBlock|TestGoldenStrings' -v`
Expected: FAIL with "golden_test.go:155: routing_block_claude: RoutingBlock(true, false) differs"

- [ ] **Step 3: Write minimal implementation**

`internal/routing/text.go`:

```go
package routing

import "strings"

// ToolPrefix is the name prefix of cmgo's MCP tools under
// `claude mcp add --scope user cmgo` (spec §1.3).
const ToolPrefix = "mcp__cmgo__"

// named stands in for the TS tool namer t(): every $T in a TS template
// becomes ToolPrefix. It is applied to constant text only, never to input.
func named(s string) string {
	return strings.ReplaceAll(s, "$T", ToolPrefix)
}

// The routing block of createRoutingBlock (routing-block.mjs:16-80), cut at
// its two ${…} slots. Each optional section carries the newlines its slot
// adds, so an empty slot still leaves a blank line, as in TS.
var (
	blockHead = named(`
<context_window_protection>
  <priority_instructions>
    Every byte a tool returns enters your conversation memory and costs reasoning capacity for the rest of the session. The context-mode tools let you do the work in a sandbox and surface only the derived answer — the raw bytes stay out. Think-in-Code: program the analysis, do not compute it by reading raw data into your conversation.
  </priority_instructions>
`)
	blockBootstrap = named(`
  <deferred_tool_bootstrap>
    The context-mode tools below may be DEFERRED in your harness — their schemas are not loaded yet, so calling them directly fails (e.g. "tool not found" / InputValidationError). Load them ONCE before your first ctx_* call:
    ToolSearch(query: "select:$Tctx_batch_execute,$Tctx_search,$Tctx_execute,$Tctx_execute_file,$Tctx_fetch_and_index")
    After that they are callable. If any ctx_* call fails as not-found, ToolSearch it and retry — do NOT fall back to Bash/Read just because the schema was not loaded yet.
  </deferred_tool_bootstrap>
`)
	blockMiddle = named(`
  <tool_selection_hierarchy>
    0. MEMORY: $Tctx_search(sort: "timeline")
       - On resume or compaction, query prior decisions, errors, plans, user prompts before asking the user — auto-captured session memory is searchable.
    1. GATHER: $Tctx_batch_execute(commands, queries)
       - Primary research tool. Runs commands in parallel, auto-indexes each output, and (when queries are passed) returns matching sections in the same round trip — no follow-up search call.
       - Each command: {label: "section header", command: "shell command"}; the label becomes the FTS5 chunk title — descriptive labels improve search.
    2. FOLLOW-UP: $Tctx_search(queries: ["q1", "q2", ...])
       - Multiple related questions about anything already indexed (your captures + session memory). Batch every question in one array; the ranking pipeline runs per-query and the round-trip cost is paid once.
    3. PROCESSING: $Tctx_execute(language, code) | $Tctx_execute_file(path, language, code)
       - Derive answers FROM data: filter, count, aggregate, parse, transform. Only what you console.log() enters your conversation; the raw bytes stay in the sandbox.
  </tool_selection_hierarchy>

  <when_not_to_use>
    - You intend to PROCESS the output (filter, count, parse, aggregate) → use $Tctx_batch_execute or $Tctx_execute. Bash stays correct when you intend to OBSERVE a short fixed output (git status on a clean tree, whoami, pwd) or when you are mutating state (git, mkdir, rm, mv, navigation).
    - You want to analyze, summarize, or extract from a file → use $Tctx_execute_file. Read stays correct when you intend to Edit the file (Edit needs the exact bytes in your conversation to match against).
    - WebFetch → use $Tctx_fetch_and_index; full network access, results indexed for $Tctx_search, raw page bytes never enter your conversation.
    - $Tctx_execute and $Tctx_execute_file for file writes → these run code in a subprocess and discard the sandbox FS; they are for analysis, processing, and computation only.
  </when_not_to_use>

  <file_writing_policy>
    File writes use the native Write or Edit tool — $Tctx_execute, $Tctx_execute_file, and Bash subprocesses do not persist edits to the host filesystem.
    Applies to all file types: code, configs, plans, specs, YAML, JSON, markdown.
  </file_writing_policy>

  <output_constraints>
    <artifact_policy>
      Write artifacts (code, configs, PRDs) to files. Return only: file path + 1-line description.
    </artifact_policy>
  </output_constraints>
  <session_continuity>
    Skills, roles, and decisions captured earlier in this session are a memory aid, not a standing order. Treat them as context that may help — the user's most recent message always takes precedence. If a captured directive conflicts with what the user now asks, follow the user; a past phrase does not bind you.
  </session_continuity>
`)
	// Every TS command, now that cmgo has a tool for each (sub-project 5
	// spec §4). The trigger lists and the context-mode wording are kept verbatim.
	blockCommands = named(`
  <ctx_commands>
    "ctx stats" | "ctx-stats" | "/ctx-stats" | context savings question
    → Call stats MCP tool, display full output verbatim.

    "ctx doctor" | "ctx-doctor" | "/ctx-doctor" | diagnose context-mode
    → Call doctor MCP tool, run returned shell command, display as checklist.

    "ctx upgrade" | "ctx-upgrade" | "/ctx-upgrade" | update context-mode
    → Call upgrade MCP tool, run returned shell command, display as checklist.

    "ctx purge" | "ctx-purge" | "/ctx-purge" | wipe/reset knowledge base
    → Call purge MCP tool with confirm: true. Warn: irreversible.

    After /clear or /compact: knowledge base preserved. Tell user: "context-mode knowledge base preserved. Use ` + "`" + `ctx purge` + "`" + ` to start fresh."
  </ctx_commands>
`)
	blockTail = named(`
</context_window_protection>`)
)

// RoutingBlock ports createRoutingBlock with the cmgo tool namer.
// includeCommands adds the <ctx_commands> section and toolSearchBootstrap
// the <deferred_tool_bootstrap> section.
func RoutingBlock(includeCommands, toolSearchBootstrap bool) string {
	s := blockHead
	if toolSearchBootstrap {
		s += blockBootstrap
	}
	s += blockMiddle
	if includeCommands {
		s += blockCommands
	}
	return s + blockTail
}

// The guidance texts (routing-block.mjs:82-92) and the redirect messages
// (routing.mjs:771, 802, 815, 879).
var (
	readGuidance  = named("<context_guidance>\n  <tip>\n    Reading to Edit the file? Read is correct — Edit needs the exact bytes in your conversation to match against.\n    Reading to analyze, summarize, or extract from the file? Use $Tctx_execute_file(path, language, code) — the bytes stay in the sandbox and only what your code prints enters your conversation.\n  </tip>\n</context_guidance>")
	grepGuidance  = named("<context_guidance>\n  <tip>\n    Grep results may be larger than you expect. When you intend to count, filter, or aggregate matches (not just spot-check one), run the search through $Tctx_execute(language: \"javascript\", code: \"...\") — the raw match list stays in the sandbox and only your derived answer enters your conversation. Use language: \"shell\" only when the code matches the host shell (PowerShell on Windows, POSIX shell on Unix).\n  </tip>\n</context_guidance>")
	bashGuidance  = named("<context_guidance>\n  <tip>\n    When you intend to PROCESS the output (filter, count, parse, aggregate), use $Tctx_batch_execute(commands, queries) for multiple commands or $Tctx_execute(language: \"javascript\", code: \"...\") for one — the raw output stays in the sandbox and only what you print enters your conversation. Shell stays the right surface when you intend to OBSERVE a short fixed output or when you are mutating state (git, mkdir, rm, mv, navigation); if you use $Tctx_execute(language: \"shell\"), write syntax for the host shell.\n  </tip>\n</context_guidance>")
	curlMessage   = named(`context-mode: curl/wget redirected. Call $Tctx_execute(language, code) to fetch the URL, derive your answer in code, and print only the result — the raw HTTP body stays in the sandbox instead of entering your conversation. Or call $Tctx_fetch_and_index(url, source) when you want to query the response later via $Tctx_search. Both have full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
	inlineMessage = named(`context-mode: Inline HTTP redirected. Call $Tctx_execute(language, code) to fetch, derive your answer in code, and console.log() only the result — the raw response body stays in the sandbox instead of entering your conversation. Full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
	buildHead     = named(`context-mode: Build tool redirected. Call $Tctx_execute(language: \"shell\", code: \"`)
	buildTail     = ` 2>&1 | tail -30\") to run the build and print only the tail — the verbose build log stays in the sandbox instead of entering your conversation. For more targeted output, replace \"tail -30\" with \"grep -E '(error|warning|FAIL|✗|×)'\" or similar, so only the lines that matter come back.`
	webFetchHead  = named(`context-mode: WebFetch redirected. Call $Tctx_fetch_and_index(url: "`)
	webFetchTail  = named(`", source: "...") to fetch + index the page, then $Tctx_search(queries: [...]) to query the indexed content — the raw page bytes stay in storage instead of entering your conversation. Or call $Tctx_execute(language, code) when you want to derive your answer in one round trip (parse, extract, count) without persisting the response. Both have full network access. Retry the same call on a transient DNS error (EAI_AGAIN, ETIMEDOUT, ENETUNREACH).`)
)

// buildMessage is the build-tool redirect text for command. The command is
// escaped as routing.mjs:811 does, and the template's \" stay literal: the
// Claude Code formatter never unescapes them.
func buildMessage(command string) string {
	safe := strings.ReplaceAll(command, `\`, `\\`)
	safe = strings.ReplaceAll(safe, `"`, `\"`)
	return buildHead + safe + buildTail
}

// webFetchMessage is the WebFetch deny text; url goes in raw (R1 P16).
func webFetchMessage(url string) string {
	return webFetchHead + url + webFetchTail
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing/ -run 'TestRoutingBlock|TestGoldenStrings' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/routing/text.go internal/routing/text_test.go internal/routing/golden_test.go
git commit -m "feat(routing): add the ctx doctor and ctx upgrade commands to the routing block"
```

### Task 16: Wire `doctor` and `upgrade` into cmgo

**Files:**
- Modify: `cmd/cmgo/main.go`
- Test: `cmd/cmgo/main_test.go`

**Interfaces:**
- Consumes: `func doctorCmd(args []string, stdout, stderr io.Writer) int`; `func upgradeCmd(args []string, stdout, stderr io.Writer) int` (Task 13)
- Produces: `cmgo doctor`, `cmgo upgrade [--source <dir>] [--dry-run]`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/main_test.go`:

```go
package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != "cmgo dev\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"nope"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestRunHook(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CMGO_DIR", filepath.Join(dir, "data"))
	t.Setenv("CMGO_PROJECT_DIR", dir)
	var out, errb bytes.Buffer
	if code := run([]string{"hook", "stop"}, strings.NewReader(`{"session_id":"s1"}`), &out, &errb); code != 0 || out.String() != "{}\n" {
		t.Fatalf("stop: exit %d, out %q, stderr %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"hook", "nope"}, strings.NewReader("{}"), &out, &errb); code != 0 || out.Len() != 0 {
		t.Fatalf("unknown event: exit %d, out %q", code, out.String())
	}
	if code := run([]string{"hook"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("missing event: exit %d", code)
	}
}

// doctor and upgrade reach their commands: a usage error exits 2.
func TestRunDispatchesNewCommands(t *testing.T) {
	for _, args := range [][]string{{"doctor", "extra"}, {"upgrade", "--nope"}} {
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb); code != 2 || strings.Contains(errb.String(), "unknown command") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errb.String())
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run 'TestRunDispatchesNewCommands|TestVersion|TestUnknownCommand' -v`
Expected: FAIL with "main_test.go:50: [doctor extra]: exit 2, stderr "cmgo: unknown command \"doctor\"\n""

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo/main.go`:

```go
// Command cmgo is a Go rewrite of context-mode for Claude Code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/buildinfo"
	"github.com/lang315/context-mode-go/internal/hookcmd"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
// Without it, init resolves it from the build info (spec §1).
var version string

func init() { version = buildinfo.Version(version) }

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run dispatches a subcommand. stdin feeds `hook`; serve keeps os.Stdin
// because the MCP transport needs a ReadCloser.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cmgo <serve|hook|install|uninstall|doctor|upgrade|version>")
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "cmgo "+version)
		return 0
	case "serve":
		return serve(context.Background(), serveIO{
			getenv: os.Getenv, getwd: os.Getwd, in: os.Stdin, out: os.Stdout, stderr: stderr,
		})
	case "hook": // the same code as cmgo-hook, for manual runs; install registers cmgo-hook
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cmgo hook <event>")
			return 2
		}
		return hookcmd.Run(args[1], hookcmd.IO{Getenv: os.Getenv, Getwd: os.Getwd, Stdin: stdin, Stdout: stdout, Now: time.Now})
	case "install", "uninstall":
		return installCmd(args[0], args[1:], stdout, stderr)
	case "doctor":
		return doctorCmd(args[1:], stdout, stderr)
	case "upgrade":
		return upgradeCmd(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cmgo: unknown command %q\n", args[0])
		return 2
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cmgo/ -run 'TestRunDispatchesNewCommands|TestVersion|TestUnknownCommand' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/main.go cmd/cmgo/main_test.go
git commit -m "feat(cmgo): dispatch doctor and upgrade"
```

### Task 17: Wire `import` into cmgo

**Files:**
- Modify: `cmd/cmgo/main.go`
- Test: `cmd/cmgo/main_test.go`

**Interfaces:**
- Consumes: `func runImport(args []string, stdout, stderr io.Writer, getenv func(string) string) int` from `cmd/cmgo/import.go` (the import fragment; do this task after it)
- Produces: `cmgo import [--from <dir>] [--dry-run] [--force]`

- [ ] **Step 1: Write the failing test**

`cmd/cmgo/main_test.go`:

```go
package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != "cmgo dev\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"nope"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestRunHook(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CMGO_DIR", filepath.Join(dir, "data"))
	t.Setenv("CMGO_PROJECT_DIR", dir)
	var out, errb bytes.Buffer
	if code := run([]string{"hook", "stop"}, strings.NewReader(`{"session_id":"s1"}`), &out, &errb); code != 0 || out.String() != "{}\n" {
		t.Fatalf("stop: exit %d, out %q, stderr %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"hook", "nope"}, strings.NewReader("{}"), &out, &errb); code != 0 || out.Len() != 0 {
		t.Fatalf("unknown event: exit %d, out %q", code, out.String())
	}
	if code := run([]string{"hook"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("missing event: exit %d", code)
	}
}

// doctor, upgrade and import reach their commands: a usage error exits 2.
func TestRunDispatchesNewCommands(t *testing.T) {
	for _, args := range [][]string{{"doctor", "extra"}, {"upgrade", "--nope"}, {"import", "--nope"}} {
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb); code != 2 || strings.Contains(errb.String(), "unknown command") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errb.String())
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cmgo/ -run 'TestRunDispatchesNewCommands' -v`
Expected: FAIL with "main_test.go:50: [import --nope]: exit 2, stderr "cmgo: unknown command \"import\"\n""

- [ ] **Step 3: Write minimal implementation**

`cmd/cmgo/main.go`:

```go
// Command cmgo is a Go rewrite of context-mode for Claude Code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/buildinfo"
	"github.com/lang315/context-mode-go/internal/hookcmd"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
// Without it, init resolves it from the build info (spec §1).
var version string

func init() { version = buildinfo.Version(version) }

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run dispatches a subcommand. stdin feeds `hook`; serve keeps os.Stdin
// because the MCP transport needs a ReadCloser.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cmgo <serve|hook|install|uninstall|doctor|upgrade|import|version>")
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "cmgo "+version)
		return 0
	case "serve":
		return serve(context.Background(), serveIO{
			getenv: os.Getenv, getwd: os.Getwd, in: os.Stdin, out: os.Stdout, stderr: stderr,
		})
	case "hook": // the same code as cmgo-hook, for manual runs; install registers cmgo-hook
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cmgo hook <event>")
			return 2
		}
		return hookcmd.Run(args[1], hookcmd.IO{Getenv: os.Getenv, Getwd: os.Getwd, Stdin: stdin, Stdout: stdout, Now: time.Now})
	case "install", "uninstall":
		return installCmd(args[0], args[1:], stdout, stderr)
	case "doctor":
		return doctorCmd(args[1:], stdout, stderr)
	case "upgrade":
		return upgradeCmd(args[1:], stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr, os.Getenv)
	default:
		fmt.Fprintf(stderr, "cmgo: unknown command %q\n", args[0])
		return 2
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/cmgo/ -run 'TestRunDispatchesNewCommands' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cmgo/main.go cmd/cmgo/main_test.go
git commit -m "feat(cmgo): dispatch import"
```

---

## Appendix A: Porting notes by package

These notes are the deliberate choices made while drafting. If a review disagrees with one, change the spec first, then the code.

### import (store and tsimport) (Tasks 1-4)

Ports nothing from TS as code: it reads the TS on-disk data and writes it into cmgo.db for `cmgo import` (spec §3, §0 criterion 3, §5). It reads the TS SessionDB schema (`src/session/db.ts:827-903`, including the old column set handled at `db.ts:700-713` and the pre-`project_dir` v1.0.0 DDL from `packages/session/src/db.ts` at `2ffccbee^`) and the TS ContentStore schema (`src/store.ts:516-605`, including the 4-column FTS tables replaced at `store.ts:560-600`). Roots follow R4 §D.1 (`db.ts:86-96`, `173-200`); file naming and the content-DB-to-session-DB mapping follow R4 §D.2 (`db.ts:310-315`, `363-396`, `449-479`). The retention windows are cmgo's: `store.SessionRetention` (7 days, TS `cleanupOldSessions` at `db.ts:1696`) and `sourceRetention` in `cmd/cmgo/serve.go` (14 days). New store functions write the rows: `ImportEvents` and `ImportSource` (sharing `Index`'s insert code through a new unexported `insertSource`), plus `Meta`/`SetMeta` for the `ts_import` guard.

**Parity notes (decided, flag in review if wrong):**
- Only copies are opened. Each `*.db` is copied with its `-wal` and `-shm` into `$CMGO_DIR/run/import-*/{sessions,content}` (two subdirs, because session and content DBs share `<h16>.db` names), then opened with `mode=ro`. The ncruces driver reads WAL frames from such a copy, also with a missing or zeroed `-shm` (checked while drafting), so no read-write fallback is needed. `immutable=1` is not used: it would ignore the WAL.
- `--dry-run` imports into a copy of cmgo.db (or a fresh DB) in the same temp dir, so its counts are exact (duplicates and existing sources included) and cmgo.db is never opened. The guard applies to a dry run too.
- Dedup lives in the store: `ImportEvents` skips a row whose `(session_id, type, data_hash, created_at)` already exists. That one check covers duplicates within a DB, across DBs (worktree DBs hold the same session) and against cmgo's own rows, so `--force` re-runs insert no events twice. `data_hash` is never read from TS (the old generated-column variant needs no special case).
- Sessions seen in several DBs are merged before import: events concatenated in DB-name order then `id` order, `started_at` = earliest, `last_event_at` = latest of `session_meta` and the events, `compact_count` = largest. Retention is checked on the merged `last_event_at`.
- `session_meta` merge into an existing cmgo session: earliest `started_at`, latest `last_event_at`, `event_count` + rows inserted, larger `compact_count`; the existing `project` and `cc_pid` are kept. A new row gets `cc_pid` 0. Imported rows take the TS session's project, even when the cmgo session row has another.
- A session that has `session_meta` but no events is not imported and not counted. A session with no project dir is skipped (spec), even when other sessions of its DB have one.
- The project of a session DB file (for content mapping) is the most common project of all its sessions, active or not, so the sources of a project whose sessions all expired still import. When several session DBs share an `<h16>` prefix, the unsuffixed `<h16>.db` wins (it sorts first).
- Retention compares the TS `datetime('now')` strings with a cutoff in the same format; TS writes no other format.
- Output (spec §3.4): skip reasons are `inactive`, `no project` (sessions) and `expired`, `no project`, `exists` (sources), as `<total> skipped (<n> <reason>, ...)`; with nothing skipped the line ends `0 skipped` without parentheses. A dry run adds ` (dry run)` to the first line. Sources of a content DB without a session DB count as `no project`.
- When `ts_import` exists and `--force` is not given, `cmgo import` prints `cmgo import: TS data already imported (<value>); run with --force to import again` to stderr and exits 1. The `ts_import` value is written after every real run, `--force` included.
- A TS DB that cannot be copied, opened or read fails the import with its path. Sessions imported before the failure stay (one transaction per session); `ts_import` is not written, so a re-run needs no `--force`.
- `cmd/cmgo/main.go` is not changed here: the `import` case that calls `runImport(args[1:], stdout, stderr, os.Getenv)` belongs to the doctor/upgrade fragment.

### doctor, upgrade, version, tools (Tasks 5-17)

Ports the TS `ctx_doctor` tool (`src/server.ts:4139-4267`: header, runtimes line, server test `:4196-4211`, FTS5 probe `:4213-4229`, hook validation, version line) as `internal/doctor`, behind both `cmgo doctor` and a new `ctx_doctor`, with the check list of spec §1 instead of the TS one. It replaces the TS `context-mode upgrade` CLI (`src/cli.ts:1234-1967`) with `internal/upgrade`, which rebuilds cmgo with the Go toolchain and re-runs `cmgo install` (spec §2), and ports `ctx_upgrade` (`src/server.ts:4270-4427`), which runs nothing and hands the model the command. It adds the version resolution both binaries print (spec §1, §4), a `version` argument to `cmgo-hook`, the `ctx doctor` and `ctx upgrade` entries of the routing block (`hooks/routing-block.mjs` `<ctx_commands>`, spec §4), and the `doctor`, `upgrade` and `import` subcommands of `cmgo`. Spec: §1, §2 and §4 of `docs/superpowers/specs/2026-10-21-cmgo-doctor-import-design.md`; TS behavior: `../research/r4-stats-doctor-install.md` §B.

**Parity notes (decided, flag in review if wrong):**
- Exports added to packages this fragment does not own, each in a new file: `store.(*Store).SchemaVersion` and `store.ProbeFTS5` (`internal/store/health.go`), `executor.(*Executor).Available` (`internal/executor/available.go`), and `install.Inspect`, `install.Status` and `install.MCPServer` (`internal/install/status.go`). Doctor uses `install`'s own parser and its own-command detection (`ownArg`, `shellWord`, `isOwnStatusLine`, `mcpState`), so the doctor and `cmgo install` cannot disagree about what counts as a cmgo hook.
- An event counts as registered only when it holds a cmgo command for its own arg (`hooks.Stop` holding `cmgo-hook pretooluse` does not count for Stop). Both the `cmgo-hook <arg>` form and the older `cmgo hook <arg>` form count.
- The hook binary that the Version and Hook binary lines check is the binary of the first cmgo hook command in settings.json, because that is what Claude Code runs. With no cmgo hook, it is `cmgo-hook` next to cmgo, where `cmgo install` looks for it. The version is the last word that `<hook binary> version` prints, so the old `cmgo hook` form (`cmgo <v>`) works too.
- The spec gives no Version line for a hook binary whose version cannot be read: it is `[WARN] Version: cmgo <v> (hook binary version unknown: <error>) — fix: cmgo upgrade`. Errors include a timeout (`timed out after 2s`) and `no output`: a `cmgo-hook` built before this fragment treats `version` as an unknown event and exits 0 silently. The spec's `[WARN] ...differs` line is rendered `[WARN] Version: cmgo <v> (hook binary <v2> differs) — fix: cmgo upgrade`.
- A settings.json that cannot be read or parsed gives `[FAIL] Hooks: could not read ...`. The Statusline and TS plugin lines are then left out, since there is nothing to report. A missing or blank settings file is `{}` (all hooks missing: WARN), as in `cmgo install`.
- `.claude.json` is `<home>/.claude.json` when the Claude dir is the default `~/.claude`, else `<ClaudeDir>/.claude.json`. This is install's rule, keyed on the resolved dir instead of on whether `CLAUDE_CONFIG_DIR` is set; it is marked as a `shortcut:` in `env.go`.
- The MCP server line is OK only when `mcpServers.cmgo` runs this binary with `serve`, matching install's re-register rule. Any other command is `registered at <command>`.
- Database FAIL is `[FAIL] Database: <error>`, as in the spec, with no path. The FTS5 probe is the TS one, on an in-memory DB opened with the same driver and fts5 extension as the store.
- Executor FAIL follows TS: the stderr detail is trimmed and cut to 200 UTF-16 units (`hookio.JSPrefix`). A run error is `FAIL — <error>`.
- Several enabled `context-mode@` plugins give one TS plugin WARN line each.
- No clock is injected: no check reads the time. The timeouts are `Env` fields (2 s and 5 s by default), so tests can lower them.
- `ctx_doctor` and `ctx_upgrade` descriptions are the TS text with "cmgo" for "context-mode"; titles and annotations are the TS ones. The `ctx_upgrade` text is the TS block with the command `"<cmgo path>" upgrade`. Its example checklist shows `cmgo upgrade`'s own steps, not the TS npm steps, which would be wrong for cmgo. `ctx_doctor` returns its text with `isError` false even with a FAIL line, as TS does.
- Upgrade: the go.mod check of a source dir is a file read, so it also runs under `--dry-run`. A dry run prints `dry run: <step>` lines and runs nothing; `[ ]` already means failure. GOBIN comes from `$GOBIN`, else the first `$GOPATH` entry plus `/bin`, else `~/go/bin`; a GOBIN set only with `go env -w` is not seen (marked `shortcut:`). `GOPRIVATE` is appended to an existing value, not replaced. The steps' go and git output is streamed to stderr, so a failure shows above its `[ ]` line.
- The routing block now equals the TS `includeCommands: true` text after the tool-name substitution, so the golden test compares it directly and `dropCommands` is deleted.
## Appendix B: Verification (2026-10-21, macOS arm64)

**Replay of this plan** (`replay_plan2.py`): each task's files were written into a fresh worktree of `sp4-stats`, then `go vet ./...` and that task's package tests were run. All 17 tasks were `ok`. Final step: `go test ./... && go test -race ./...`.

```
FINAL ok
internal/hookcmd	11.988s
ok  	github.com/lang315/context-mode-go/internal/hookio	1.953s
ok  	github.com/lang315/context-mode-go/internal/install	6.723s
ok  	github.com/lang315/context-mode-go/internal/mcpserver	7.576s
ok  	github.com/lang315/context-mode-go/internal/memory	2.069s
ok  	github.com/lang315/context-mode-go/internal/procinfo	1.729s
ok  	github.com/lang315/context-mode-go/internal/routing	1.524s
ok  	github.com/lang315/context-mode-go/internal/security	1.106s
ok  	github.com/lang315/context-mode-go/internal/stats	1.213s
ok  	github.com/lang315/context-mode-go/internal/store	4.211s
ok  	github.com/lang315/context-mode-go/internal/tsimport	1.357s
ok  	github.com/lang315/context-mode-go/internal/upgrade	1.281s
ok  	github.com/lang315/context-mode-go/cmd/cmgo	13.855s
ok  	github.com/lang315/context-mode-go/cmd/cmgo-hook	4.306s
ok  	github.com/lang315/context-mode-go/internal/buildinfo	2.377s
ok  	github.com/lang315/context-mode-go/internal/capture	2.912s
ok  	github.com/lang315/context-mode-go/internal/config	1.546s
ok  	github.com/lang315/context-mode-go/internal/doctor	8.664s
ok  	github.com/lang315/context-mode-go/internal/executor	15.814s
ok  	github.com/lang315/context-mode-go/internal/fetch	3.251s
ok  	github.com/lang315/context-mode-go/internal/hookcmd	15.051s
ok  	github.com/lang315/context-mode-go/internal/hookio	3.475s
ok  	github.com/lang315/context-mode-go/internal/install	7.584s
ok  	github.com/lang315/context-mode-go/internal/mcpserver	19.111s
ok  	github.com/lang315/context-mode-go/internal/memory	2.049s
ok  	github.com/lang315/context-mode-go/internal/procinfo	2.040s
ok  	github.com/lang315/context-mode-go/internal/routing	2.132s
ok  	github.com/lang315/context-mode-go/internal/security	1.942s
ok  	github.com/lang315/context-mode-go/internal/stats	1.882s
ok  	github.com/lang315/context-mode-go/internal/store	27.469s
ok  	github.com/lang315/context-mode-go/internal/tsimport	3.306s
ok  	github.com/lang315/context-mode-go/internal/upgrade	1.994s
```

`go test ./... -count=1 -v`: 499 `--- PASS`, 0 `--- FAIL`. After the replay, `diff -rq` of the replayed tree against the draft showed no differences.

**Benches and a doctor run on the draft binaries:**

```
load average (1 min) at start: 4.40
context /usr/bin/true: p50 1.90 ms p95 2.46 ms
context cmgo-hook, no args: p50 3.87 ms p95 4.38 ms
pretooluse passthrough: p50 4.29 ms p95 4.97 ms PASS
pretooluse curl redirect: p50 5.28 ms p95 5.92 ms PASS
posttooluse Read capture: p50 5.37 ms p95 6.37 ms PASS
userpromptsubmit: p50 5.38 ms p95 6.13 ms PASS
sessionstart startup: p50 5.65 ms p95 6.51 ms PASS
precompact (1000 events): p50 5.58 ms p95 6.95 ms PASS
sessionstart compact (1000 events): p50 7.04 ms p95 7.84 ms PASS
posttooluse x4 concurrent: p50 6.89 ms p95 9.00 ms PASS
statusline (100 sessions, 50000 events): p50 4.90 ms p95 5.44 ms PASS
load average (1 min) at end: 3.89
PASS
8 idle servers over 60s: 0 bytes written = 0.000 MB/min
PASS
```

`cmgo doctor` with a fresh temp HOME and CMGO_DIR (cmgo not installed there, so the two WARN lines are expected):

```
cmgo doctor

[OK] Version: cmgo v0.0.0-20261021060314-543ce874bcf8+dirty (hook binary v0.0.0-20261021060314-543ce874bcf8+dirty)
[OK] Storage: <tmp>/doc.y9VC/cmgo (writable)
[OK] Database: <tmp>/doc.y9VC/cmgo/cmgo.db schema v3, FTS5 PASS
[OK] Executor: PASS (shell)
[OK] Runtimes: 8/12 — javascript, typescript, python, shell, ruby, go, rust, perl
[WARN] Hooks: missing PreToolUse, PostToolUse, PostToolUseFailure, UserPromptSubmit, SessionStart, Stop, PreCompact — fix: cmgo install
[OK] Hook binary: <tmp>/sp5-bin/cmgo-hook
[WARN] MCP server: not registered — fix: cmgo install
[OK] Statusline: not set
[OK] TS plugin: not enabled
doctor exit 0
```

**What the replay did not exercise:**
- **The fail-first steps:** the replay did not run any task's Step 2. The drafting agents took each "Expected: FAIL" line from a real run while replaying their own tasks.
- **External commands:** no real `go install`, `git pull` or `claude` was run. The upgrade runner is injected in every test.
- **Real data:** import was never run against the real `~/.claude/context-mode` data. Only TS-schema fixtures were used.
