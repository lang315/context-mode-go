# cmgo Bash compaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `cmgo-hook posttooluse` replaces a large main-session Bash result (5 KB to 30,000 bytes, not a reading command) with a short head/tail/error-lines text via `updatedToolOutput`, after indexing the original into FTS as `bash:<tool_use_id>`.

**Architecture:** A new pure package `internal/compact` builds the replacement text and parses the switch. `internal/routing` exports `ReadsFiles` on the existing `readguard.go` tokenizer. `internal/store` gains `IndexBashOutput` (source without vocabulary plus a `bash-compact` stat row, one transaction) and a 48-hour retention for `bash:` sources. `internal/hookcmd.postToolUse` runs the compaction before the existing event capture. If any step fails, the hook prints nothing.

**Tech Stack:** Go 1.x, ncruces go-sqlite3 (FTS5), Python 3 for bench and audit scripts.

**Spec:** `docs/superpowers/specs/2026-10-27-cmgo-bash-compact-design.md`

## Global Constraints

- Branch `bash-compact`, stacked on `cleanup-kept-cap` (PR #17). The PR targets `cleanup-kept-cap` until #17 merges.
- `cmgo-hook` must not import `mcpserver`, `executor` or `fetch`. `cmd/cmgo-hook`'s deps test enforces this.
- Thresholds:
  - Default 5120 (`compact.DefaultMinBytes`).
  - Upper bound 30000 (`compact.MaxBytes`).
  - Head and tail 1024 bytes each.
  - At most 20 matching lines, each at most 200 bytes.
  - Match pattern: case-insensitive `error|fail|panic|warn`.
- Switch `CMGO_BASH_COMPACT_MIN_BYTES`:
  - unset or junk: 5120;
  - `0`: off;
  - 1-30000: that value.
- Full-output marker in a command: `# cmgo:full`.
- Reading commands, matched on the first command of any chain:
  - `sed`, `cat`, `head`, `tail`, `nl`, `tac`, `bat`, `less`;
  - `git diff` and `git show`, also after `git -C <dir>` or `git --no-pager`.
- Source label `bash:<tool_use_id>`; chunks `ChunkPlainText(output, 20)`; no vocabulary words.
- Stats row: `server_id 'hook'`, `cc_pid 0`, `window = session_id`, `tool 'bash-compact'`.
  - `bytes_returned` = length of the compact text.
  - `bytes_kept = min(len(output) - len(text), 30000)`.
- `bash:` sources are deleted 48 hours after `indexed_at`. Other sources keep 14 days.
- Output JSON: `{"hookSpecificOutput":{"hookEventName":"PostToolUse","updatedToolOutput":{"stdout":<text>,"stderr":"","interrupted":<from response>,"isImage":false}}}` plus a newline.
- Imports: stdlib first, a blank line, then module imports. `gofmt -l .` prints nothing.
- Commits in English, ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Never run `cmgo install`, `go install` or `cmgo import`. Never point a binary at the real `~/.claude/cmgo`. Scratch dirs go under the session scratchpad.

## Review Focus

1. A result with `interrupted: true` (a timed-out command) is still compacted, and `interrupted` stays `true` in the replacement. Pinned in Task 4.
2. An output with no newline at all (minified JSON, a progress bar) is cut on UTF-8 boundaries and stays valid UTF-8. Pinned in Task 2.
3. The first hook fire on a machine with no `cmgo.db` yet: the compaction creates and migrates the DB and still compacts. Pinned in Task 4: the test env starts with no DB.
4. A `tool_response` without `stdout`, from an older Claude Code shape or a background run, is never compacted. Pinned in Task 4.
5. A low threshold (`CMGO_BASH_COMPACT_MIN_BYTES=1`) on a short output, where the compact text would be longer than the original, is not compacted. Pinned in Task 4.

---

### Task 1: `routing.ReadsFiles`

**Files:**
- Create: `internal/routing/readsfiles.go`
- Test: `internal/routing/readsfiles_test.go`

**Interfaces:**
- Consumes: `tokenize(cmd string) ([]rgChain, bool)`, `rgChain []rgStage`, `rgStage{words []rgWord}`, `rgWord{s string}` from `internal/routing/readguard.go`.
- Produces: `func ReadsFiles(command string) bool`.

- [ ] **Step 1: Write the failing test**

```go
package routing

import "testing"

func TestReadsFiles(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"sed -n 1,9p f.go", true},
		{"cd d && cat f", true},
		{"FOO=1 head -50 f", true},
		{"git -C d diff", true},
		{"git --no-pager show HEAD", true},
		{"cat f | grep x", true},
		{"ls && cat f", true},
		{"/usr/bin/tail -n 5 log", true},
		{"go test ./...", false},
		{"git status", false},
		{"git log --oneline", false},
		{"grep -rn x .", false},
		{"go test ./... | tail -5", false}, // only the first stage of a chain counts
		{"cat <<EOF\nx\nEOF", false},      // the tokenizer skips heredocs
		{"echo $(cat f)", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ReadsFiles(c.cmd); got != c.want {
			t.Errorf("ReadsFiles(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/routing -run TestReadsFiles -count=1`
Expected: FAIL to compile with `undefined: ReadsFiles`.

- [ ] **Step 3: Write minimal implementation**

`internal/routing/readsfiles.go`:

```go
package routing

import (
	"path/filepath"
	"strings"
)

// readCommands start a command whose output the model usually needs
// verbatim, to Edit or review it (bash-compact spec §1 item 5).
var readCommands = map[string]bool{"sed": true, "cat": true, "head": true, "tail": true, "nl": true, "tac": true, "bat": true, "less": true}

// ReadsFiles reports whether any chain of command starts with a reading
// command: sed, cat, head, tail, nl, tac, bat, less, git diff or git show.
// Leading VAR=value words are skipped, and so are git's -C <dir> and
// --no-pager. A form the tokenizer does not read (heredoc, $(, backtick,
// subshell) is not a reading command.
func ReadsFiles(command string) bool {
	chains, ok := tokenize(command)
	if !ok {
		return false
	}
	for _, ch := range chains {
		if len(ch) == 0 {
			continue
		}
		w := ch[0].words
		for len(w) > 0 && strings.Index(w[0].s, "=") > 0 {
			w = w[1:]
		}
		if len(w) == 0 {
			continue
		}
		name := filepath.Base(w[0].s)
		if readCommands[name] {
			return true
		}
		if name == "git" && gitReads(w[1:]) {
			return true
		}
	}
	return false
}

// gitReads reports whether git's arguments name the diff or show subcommand.
func gitReads(args []rgWord) bool {
	for len(args) > 0 {
		switch {
		case args[0].s == "--no-pager":
			args = args[1:]
		case args[0].s == "-C" && len(args) > 1:
			args = args[2:]
		default:
			return args[0].s == "diff" || args[0].s == "show"
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/routing -count=1`
Expected: `ok`. If the `cat <<EOF` or `$(` cases fail, check that `tokenize` still returns `false` for them (`readguard.go:75`). Do not change `tokenize`.

- [ ] **Step 5: Commit**

```bash
git add internal/routing/readsfiles.go internal/routing/readsfiles_test.go
git commit -m "feat(routing): ReadsFiles tells reading Bash commands apart

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `internal/compact`

**Files:**
- Create: `internal/compact/compact.go`
- Test: `internal/compact/compact_test.go`

**Interfaces:**
- Produces:
  - `const DefaultMinBytes = 5120`, `const MaxBytes = 30000`, `const FullMarker = "# cmgo:full"` (all untyped);
  - `func MinBytes(getenv func(string) string) int`;
  - `func Output(stdout, stderr string) string`;
  - `func Text(output, label string) string`.

- [ ] **Step 1: Write the failing test**

```go
package compact

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// numbered is n lines of test-runner output; extra replaces line i.
func numbered(n int, extra map[int]string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if s, ok := extra[i]; ok {
			b.WriteString(s + "\n")
			continue
		}
		fmt.Fprintf(&b, "line %04d ok github.com/x/pkg%d 0.01s\n", i, i)
	}
	return b.String()
}

func TestTextHeadTailAndFooter(t *testing.T) {
	out := numbered(500, nil)
	got := Text(out, "bash:t1")
	head, _, ok := strings.Cut(got, "\n\n[cmgo:")
	if !ok || !strings.HasPrefix(got, "line 0000 ") || len(head) > 1024 {
		t.Fatalf("head (%d bytes):\n%s", len(head), got)
	}
	for _, want := range []string{
		fmt.Sprintf("[cmgo: %.1f KB, 500 lines; the middle is kept out of context.]\n...\n", float64(len(out))/1024),
		"line 0499 ok",
		`[cmgo: the full output is indexed as "bash:t1". Search it: ctx_search(queries: ["..."], source: "bash:t1"). Need it verbatim? Re-run the command with "# cmgo:full" at the end.]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line 0250 ") {
		t.Errorf("middle line shown:\n%s", got)
	}
	if !strings.HasSuffix(got, "at the end.]") {
		t.Errorf("does not end with the footer:\n%s", got)
	}
}

func TestTextMatches(t *testing.T) {
	out := numbered(500, map[int]string{3: "error in head", 250: "--- FAIL: TestX (0.00s)", 260: "panic: boom"})
	got := Text(out, "bash:t1")
	if !strings.Contains(got, " 2 lines match error|fail|panic|warn:]\n--- FAIL: TestX (0.00s)\npanic: boom\n...\n") {
		t.Errorf("matches:\n%s", got)
	}
	if n := strings.Count(got, "error in head"); n != 1 {
		t.Errorf("head match shown %d times, want 1 (head only)", n)
	}
	one := Text(numbered(500, map[int]string{250: "WARNING: x"}), "bash:t1")
	if !strings.Contains(one, " 1 line matches error|fail|panic|warn:]\nWARNING: x\n") {
		t.Errorf("one match:\n%s", one)
	}
}

func TestTextManyMatchesAndLongLine(t *testing.T) {
	extra := map[int]string{}
	for i := 200; i <= 220; i++ {
		extra[i] = fmt.Sprintf("warn %d", i)
	}
	got := Text(numbered(500, extra), "bash:t1")
	if !strings.Contains(got, " 21 lines match error|fail|panic|warn (first 20 shown):]\n") ||
		!strings.Contains(got, "warn 219\n") || strings.Contains(got, "warn 220") {
		t.Errorf("21 matches:\n%s", got)
	}
	long := Text(numbered(500, map[int]string{250: "error " + strings.Repeat("x", 500)}), "bash:t1")
	if !strings.Contains(long, "error "+strings.Repeat("x", 194)+"\n") || strings.Contains(long, strings.Repeat("x", 195)) {
		t.Errorf("long match not cut to 200 bytes:\n%s", long)
	}
}

func TestTextNoNewlineStaysUTF8(t *testing.T) {
	got := Text(strings.Repeat("é", 10000), "bash:t1") // 20,000 bytes, no newline
	if !utf8.ValidString(got) || len(got) > 4096 {
		t.Fatalf("valid %v, %d bytes", utf8.ValidString(got), len(got))
	}
}

func TestMinBytes(t *testing.T) {
	for v, want := range map[string]int{"": 5120, "0": 0, "junk": 5120, "-1": 5120, "1": 1, "30000": 30000, "30001": 5120, " 8192 ": 8192} {
		if got := MinBytes(func(string) string { return v }); got != want {
			t.Errorf("MinBytes(%q) = %d, want %d", v, got, want)
		}
	}
}

func TestOutput(t *testing.T) {
	for _, c := range [][3]string{{"a", "", "a"}, {"", "b", "b"}, {"a", "b", "a\nb"}, {"", "", ""}} {
		if got := Output(c[0], c[1]); got != c[2] {
			t.Errorf("Output(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/compact -count=1`
Expected: FAIL with `no non-test Go files` or `undefined: Text`.

- [ ] **Step 3: Write minimal implementation**

`internal/compact/compact.go`:

```go
// Package compact builds the text cmgo-hook puts in place of a large Bash
// result in the main session (bash-compact spec §1, §3). No I/O.
package compact

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMinBytes = 5120
	MaxBytes        = 30000 // Claude Code spills a larger Bash result to a file itself
	FullMarker      = "# cmgo:full"

	edgeBytes    = 1024
	maxMatches   = 20
	maxMatchLine = 200
)

var matchRE = regexp.MustCompile(`(?i)error|fail|panic|warn`)

// MinBytes reads CMGO_BASH_COMPACT_MIN_BYTES: an integer 1-30000 is the
// threshold, 0 turns compaction off (returned as 0), anything else, unset
// included, is DefaultMinBytes.
func MinBytes(getenv func(string) string) int {
	n, err := strconv.Atoi(strings.TrimSpace(getenv("CMGO_BASH_COMPACT_MIN_BYTES")))
	if err != nil || n < 0 || n > MaxBytes {
		return DefaultMinBytes
	}
	return n
}

// Output is what the model sees of a Bash result: stdout, then stderr after
// a newline when both are non-empty.
func Output(stdout, stderr string) string {
	if stdout == "" || stderr == "" {
		return stdout + stderr
	}
	return stdout + "\n" + stderr
}

// Text returns what the model sees instead of output: the first and last
// edgeBytes cut at line boundaries, up to maxMatches lines of the middle that
// match error|fail|panic|warn, and how to get the rest from label.
func Text(output, label string) string {
	head := cutHead(output, edgeBytes)
	tail := cutTail(output[len(head):], edgeBytes)
	middle := output[len(head) : len(output)-len(tail)]
	var matches []string
	for _, line := range strings.Split(middle, "\n") {
		if matchRE.MatchString(line) {
			matches = append(matches, line)
		}
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(head, "\n"))
	fmt.Fprintf(&b, "\n\n[cmgo: %.1f KB, %d lines; the middle is kept out of context.", float64(len(output))/1024, lineCount(output))
	switch n := len(matches); {
	case n == 0:
		b.WriteString("]\n")
	case n == 1:
		b.WriteString(" 1 line matches error|fail|panic|warn:]\n")
	case n <= maxMatches:
		fmt.Fprintf(&b, " %d lines match error|fail|panic|warn:]\n", n)
	default:
		fmt.Fprintf(&b, " %d lines match error|fail|panic|warn (first %d shown):]\n", n, maxMatches)
	}
	for _, m := range matches[:min(len(matches), maxMatches)] {
		b.WriteString(cutHead(m, maxMatchLine))
		b.WriteString("\n")
	}
	b.WriteString("...\n")
	b.WriteString(strings.TrimRight(tail, "\n"))
	fmt.Fprintf(&b, "\n\n[cmgo: the full output is indexed as %q. Search it: ctx_search(queries: [\"...\"], source: %q). Need it verbatim? Re-run the command with %q at the end.]",
		label, label, FullMarker)
	return b.String()
}

// cutHead returns at most the first n bytes of s, cut back to the last
// newline when there is one, else to a UTF-8 boundary.
func cutHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if i := strings.LastIndexByte(s[:n], '\n'); i >= 0 {
		return s[:i+1]
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// cutTail returns at most the last n bytes of s, cut forward to just after
// the first newline when there is one, else to a UTF-8 boundary.
func cutTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	if i := strings.IndexByte(s[start:], '\n'); i >= 0 && start+i+1 < len(s) {
		return s[start+i+1:]
	}
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// lineCount counts the lines of s; a trailing newline does not start one.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/compact -count=1 && gofmt -l internal/compact`
Expected: `ok`, and gofmt prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/compact
git commit -m "feat(compact): build the text that replaces a large Bash result

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: store, `IndexBashOutput` and 48-hour `bash:` retention

**Files:**
- Create: `internal/store/bashcompact.go`
- Modify:
  - `internal/store/index.go`: `insertSource` gains a `vocab bool` parameter; `Index` passes `true`.
  - `internal/store/import.go:112`: passes `true`.
  - `internal/store/store.go`: `CleanupIfDue` deletes `bash:` sources after `BashSourceRetention`.
- Test: `internal/store/bashcompact_test.go`

**Interfaces:**
- Consumes: `insertSource`, `Chunk`, `ChunkPlainText`, `sqliteTime`, `toolStats(where string, args ...any) ([]ToolStat, error)`, `ToolStat{Tool, Calls, BytesReturned, BytesKept, Unmeasured}`, `SearchWithFallback(query string, o SearchOpts)`.
- Produces:
  - `const BashCompactTool = "bash-compact"`;
  - `const BashSourceRetention = 48 * time.Hour`;
  - `func (s *Store) IndexBashOutput(project, label, sessionID string, chunks []Chunk, returned, kept int64) error`.

- [ ] **Step 1: Write the failing tests**

`internal/store/bashcompact_test.go`:

```go
package store

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIndexBashOutputSkipsVocabularyAndCountsTheCall(t *testing.T) {
	s := openTest(t)
	vocab := func() (n int) {
		s.db.QueryRow("SELECT count(*) FROM vocabulary").Scan(&n)
		return
	}
	before := vocab()
	chunks := ChunkPlainText("alpha\nzebramarker 42\nomega\n", 20)
	if err := s.IndexBashOutput("/p", "bash:t1", "s1", chunks, 2000, 15000); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexBashOutput("/p", "bash:t2", "s1", chunks, 1000, 25000); err != nil {
		t.Fatal(err)
	}
	if got := vocab(); got != before {
		t.Errorf("vocabulary %d -> %d, want unchanged", before, got)
	}
	hits, err := s.SearchWithFallback("zebramarker", SearchOpts{Project: "/p", Source: "bash:t1", SourceExact: true})
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Content, "zebramarker 42") {
		t.Fatalf("search = %+v, %v", hits, err)
	}
	rows, err := s.toolStats("window = ?", "s1")
	want := []ToolStat{{Tool: BashCompactTool, Calls: 2, BytesReturned: 3000, BytesKept: 40000}}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("tool_stats = %+v, %v; want %+v", rows, err, want)
	}
	var calls, ret, kept int64
	if err := s.db.QueryRow("SELECT calls, bytes_returned, bytes_kept FROM stats_totals WHERE project = '/p'").Scan(&calls, &ret, &kept); err != nil ||
		calls != 2 || ret != 3000 || kept != 40000 {
		t.Fatalf("stats_totals = %d %d %d, %v", calls, ret, kept, err)
	}
}

func TestCleanupDropsBashSourcesAfter48h(t *testing.T) {
	s := openTest(t)
	now := time.Now()
	chunks := ChunkPlainText("x\n", 20)
	for _, l := range []string{"bash:old", "bash:new"} {
		if err := s.IndexBashOutput("/p", l, "s1", chunks, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Index("/p", "doc:x", chunks); err != nil {
		t.Fatal(err)
	}
	for label, age := range map[string]time.Duration{"bash:old": 49 * time.Hour, "bash:new": 47 * time.Hour, "doc:x": 72 * time.Hour} {
		if _, err := s.db.Exec("UPDATE sources SET indexed_at = ? WHERE label = ?", now.Add(-age).UTC().Format(sqliteTime), label); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.CleanupIfDue(now, 14*24*time.Hour, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("CleanupIfDue = %d, %v; want 1", n, err)
	}
	var labels []string
	r, err := s.db.Query("SELECT label FROM sources ORDER BY label")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for r.Next() {
		var l string
		r.Scan(&l)
		labels = append(labels, l)
	}
	if want := []string{"bash:new", "doc:x"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("sources = %v, want %v", labels, want)
	}
	var orphans int
	s.db.QueryRow("SELECT count(*) FROM chunks WHERE source_id NOT IN (SELECT id FROM sources)").Scan(&orphans)
	if orphans != 0 {
		t.Errorf("%d chunks left without a source", orphans)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store -run 'TestIndexBashOutput|TestCleanupDropsBash' -count=1`
Expected: FAIL to compile with `undefined: BashCompactTool` / `s.IndexBashOutput undefined`.

- [ ] **Step 3: Implement**

In `internal/store/index.go`, change the signature and the vocabulary block of `insertSource`, and update its doc comment:

```go
// insertSource writes one sources row, its chunks in both FTS tables and,
// when vocab is true, their words, inside tx. indexedAt "" means
// datetime('now'). It returns the source id and the number of new
// vocabulary words.
func insertSource(tx *sql.Tx, project, label, indexedAt string, chunks []Chunk, vocab bool) (int64, int64, error) {
```

```go
	var added int64
	if !vocab {
		return id, 0, nil
	}
	if words := vocabWords(strings.Join(contents, "\n")); len(words) > 0 {
```

In `Index`: `id, added, err := insertSource(tx, project, label, "", chunks, true)`.
In `internal/store/import.go:112`: `_, added, err := insertSource(tx, project, label, indexedAt, chunks, true)`.

`internal/store/bashcompact.go`:

```go
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
```

In `internal/store/store.go` `CleanupIfDue`, replace the `cutoff`/`old`/three-DELETE block with:

```go
	cutoff := now.Add(-retention).UTC().Format(sqliteTime)
	bashCutoff := now.Add(-BashSourceRetention).UTC().Format(sqliteTime)
	// bash: sources (compacted Bash results) only serve their session.
	stale := "datetime(indexed_at) < datetime(?1) OR (label LIKE 'bash:%' AND datetime(indexed_at) < datetime(?2))"
	old := "SELECT id FROM sources WHERE " + stale
	if _, err := tx.Exec("DELETE FROM chunks WHERE source_id IN ("+old+")", cutoff, bashCutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("DELETE FROM chunks_trigram WHERE source_id IN ("+old+")", cutoff, bashCutoff); err != nil {
		return 0, err
	}
	res, err := tx.Exec("DELETE FROM sources WHERE "+stale, cutoff, bashCutoff)
	if err != nil {
		return 0, err
	}
```

Keep the rest of the function (RowsAffected, session cleanup, last_cleanup, commit) as it is. The existing local variable `stale` further down, which holds the session cutoff, now clashes with the new one. Rename the session one to `inactive` in both of its uses.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store ./internal/tsimport -count=1`
Expected: `ok` for both, including the existing cleanup and import tests.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): IndexBashOutput and 48-hour retention for bash: sources

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: hook, compact in `postToolUse`

**Files:**
- Modify: `internal/hookcmd/hookcmd.go`: `postToolUse`, new `compactBash`, imports `errors`, `strconv`, `github.com/lang315/context-mode-go/internal/compact`.
- Create: `internal/hookcmd/compact_test.go`
- Modify:
  - `testdata/golden/HOOKS-KNOWN-DIFFS.md`: one row.
  - `testdata/golden/KNOWN-DIFFS.md`: one entry.
  - `CLAUDE.md`: the switches line and the Hooks bullet.

**Interfaces:**
- Consumes: `routing.ReadsFiles` (Task 1), `routing.MCPReady(runDir string) bool`, `compact.MinBytes`/`Output`/`Text`/`MaxBytes`/`FullMarker` (Task 2), `(*store.Store).IndexBashOutput` and `store.ChunkPlainText` (Task 3), `h.openStore()`, `notNull`, `hookio.QuoteString(s string) json.RawMessage`, `(*hookio.Object).String/Bool/Object`.
- Produces: no exported API. Behavior per spec §1.

- [ ] **Step 1: Write the failing tests**

`internal/hookcmd/compact_test.go`:

```go
package hookcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/store"
)

// compactInput is a posttooluse payload for a Bash call; extra is spliced
// into the top-level object.
func compactInput(cmd, stdout string, interrupted bool, extra string) string {
	return `{"session_id":"s1","tool_use_id":"tu1","tool_name":"Bash","tool_input":{"command":` + string(hookio.QuoteString(cmd)) +
		`},"tool_response":{"stdout":` + string(hookio.QuoteString(stdout)) + `,"stderr":"","interrupted":` + fmt.Sprint(interrupted) +
		`,"isImage":false}` + extra + `}`
}

// bigStdout is about 19 KB of test output with a marker line and an error
// line in the middle.
func bigStdout() string {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		switch i {
		case 250:
			b.WriteString("zebra-marker-77 in the middle\n")
		case 260:
			b.WriteString("error: boom\n")
		default:
			fmt.Fprintf(&b, "line %04d ok github.com/x/pkg%d 0.01s\n", i, i)
		}
	}
	return b.String()
}

// compactEnv is a hook env with a live MCP sentinel and no DB yet.
func compactEnv(t *testing.T) map[string]string {
	t.Helper()
	env := hookEnv(t)
	remove, err := routing.WriteSentinel(filepath.Join(env["CMGO_DIR"], "run"), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { remove() })
	return env
}

type compactOut struct {
	HookSpecificOutput struct {
		HookEventName     string
		UpdatedToolOutput struct {
			Stdout, Stderr       string
			Interrupted, IsImage bool
		}
	}
}

func TestHookPostToolUseBashCompact(t *testing.T) {
	env := compactEnv(t)
	stdout := bigStdout()
	out := callHook(t, env, "posttooluse", compactInput("go test ./...", stdout, true, ""))
	var got compactOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	u := got.HookSpecificOutput.UpdatedToolOutput
	if got.HookSpecificOutput.HookEventName != "PostToolUse" || u.Stderr != "" || !u.Interrupted || u.IsImage {
		t.Fatalf("shape: %+v", got)
	}
	for _, want := range []string{"line 0000 ", "line 0499 ", "error: boom", `indexed as "bash:tu1"`} {
		if !strings.Contains(u.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, u.Stdout)
		}
	}
	if strings.Contains(u.Stdout, "zebra-marker-77") {
		t.Errorf("middle line shown:\n%s", u.Stdout)
	}

	st, err := store.Open(filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hits, err := st.SearchWithFallback("zebra", store.SearchOpts{Project: config.NormalizeProject(env["CLAUDE_PROJECT_DIR"]), Source: "bash:tu1", SourceExact: true})
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Content, "zebra-marker-77") {
		t.Fatalf("search = %+v, %v", hits, err)
	}
	var calls, ret, kept int64
	if err := metaDB(t, env).QueryRow("SELECT calls, bytes_returned, bytes_kept FROM tool_stats WHERE server_id = 'hook' AND tool = 'bash-compact' AND window = 's1'").
		Scan(&calls, &ret, &kept); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || ret != int64(len(u.Stdout)) || kept != min(int64(len(stdout)-len(u.Stdout)), 30000) {
		t.Errorf("tool_stats = %d %d %d", calls, ret, kept)
	}
	if s := hookErrors(env); s != "" {
		t.Errorf("errors logged: %s", s)
	}
}

func TestHookPostToolUseBashCompactSkips(t *testing.T) {
	big := bigStdout()
	readInput := `{"session_id":"s1","tool_use_id":"tu1","tool_name":"Read","tool_input":{"file_path":"/p/a.go"},"tool_response":{"type":"text","file":{"filePath":"/p/a.go","content":` +
		string(hookio.QuoteString(big)) + `}}}`
	cases := []struct {
		name, stdin string
		env         map[string]string
		mcpDown     bool
	}{
		{name: "subagent", stdin: compactInput("go test ./...", big, false, `,"agent_id":"a1","agent_type":"general-purpose"`)},
		{name: "at threshold", stdin: compactInput("go test ./...", strings.Repeat("x\n", 2560), false, "")},
		{name: "over 30000", stdin: compactInput("go test ./...", strings.Repeat("y", 30001), false, "")},
		{name: "reading command", stdin: compactInput("cat big.log", big, false, "")},
		{name: "full marker", stdin: compactInput("go test ./... # cmgo:full", big, false, "")},
		{name: "switched off", stdin: compactInput("go test ./...", big, false, ""), env: map[string]string{"CMGO_BASH_COMPACT_MIN_BYTES": "0"}},
		{name: "text longer than output", stdin: compactInput("go test ./...", strings.Repeat("z", 100), false, ""), env: map[string]string{"CMGO_BASH_COMPACT_MIN_BYTES": "1"}},
		{name: "no stdout", stdin: `{"session_id":"s1","tool_use_id":"tu1","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"stderr":""}}`},
		{name: "not Bash", stdin: readInput},
		{name: "MCP down", stdin: compactInput("go test ./...", big, false, ""), mcpDown: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := hookEnv(t)
			if !c.mcpDown {
				env = compactEnv(t)
			}
			for k, v := range c.env {
				env[k] = v
			}
			if out := callHook(t, env, "posttooluse", c.stdin); strings.Contains(out, "updatedToolOutput") {
				t.Errorf("compacted: %q", out)
			}
			if s := hookErrors(env); s != "" {
				t.Errorf("errors logged: %s", s)
			}
		})
	}
}

// With the DB write-locked the hook prints nothing, so the model keeps the
// original output, and logs the error.
func TestHookPostToolUseBashCompactLockedDB(t *testing.T) {
	env := compactEnv(t)
	callHook(t, env, "posttooluse", `{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"/p/a.go"}}`) // creates the DB
	holdDBLock(t, filepath.Join(env["CMGO_DIR"], "cmgo.db"))
	if out := callHook(t, env, "posttooluse", compactInput("go test ./...", bigStdout(), false, "")); out != "" {
		t.Errorf("printed %q with the DB locked", out)
	}
	if hookErrors(env) == "" {
		t.Error("no error logged")
	}
}

func TestKnownDiffsListBashCompact(t *testing.T) {
	for file, id := range map[string]string{"HOOKS-KNOWN-DIFFS.md": "posttooluse:bash-compact", "KNOWN-DIFFS.md": "stats:bash-compact"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), id) {
			t.Errorf("%s has no %s entry", file, id)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hookcmd -run 'BashCompact' -count=1`
Expected: `TestHookPostToolUseBashCompact` fails at `json.Unmarshal` because the output is empty. `TestKnownDiffsListBashCompact` fails because the entries are missing. The skip cases pass already, which is expected, since nothing compacts yet.

- [ ] **Step 3: Implement**

In `internal/hookcmd/hookcmd.go`, replace `postToolUse`:

```go
// postToolUse compacts a large main-session Bash result (bash-compact spec
// §1), then records the kept extractors' events (spec §3.3) from the original
// response. A failed compaction prints nothing, so the model keeps the
// original; its error is returned with the events' error, to be logged.
func postToolUse(h *hookCtx) error {
	tool, _ := h.in.String("tool_name")
	input, _ := h.in.Object("tool_input")
	resp, _ := h.in.Get("tool_response") // nil when absent
	cerr := compactBash(h, tool, input)
	return errors.Join(h.insertEvents("PostToolUse", capture.PostToolUse(tool, input, resp, h.cfg.Home)), cerr)
}

// compactBash replaces a large main-session Bash result with compact.Text
// after indexing the original as "bash:<tool_use_id>" (bash-compact spec
// §1-§2). It prints nothing whenever a condition fails or the store write
// fails.
func compactBash(h *hookCtx, tool string, input *hookio.Object) error {
	if tool != "Bash" || input == nil || notNull(h.in, "agent_id") || notNull(h.in, "agent_type") {
		return nil
	}
	threshold := compact.MinBytes(h.getenv)
	resp, ok := h.in.Object("tool_response")
	if threshold == 0 || !ok {
		return nil
	}
	stdout, _ := resp.String("stdout")
	stderr, _ := resp.String("stderr")
	output := compact.Output(stdout, stderr)
	if len(output) <= threshold || len(output) > compact.MaxBytes {
		return nil
	}
	command, _ := input.String("command")
	id, _ := h.in.String("tool_use_id")
	if id == "" || strings.Contains(command, compact.FullMarker) || routing.ReadsFiles(command) || !routing.MCPReady(h.runDir) {
		return nil
	}
	label := "bash:" + id
	text := compact.Text(output, label)
	if len(text) >= len(output) {
		return nil
	}
	st, err := h.openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	kept := min(int64(len(output)-len(text)), compact.MaxBytes)
	if err := st.IndexBashOutput(h.cfg.ProjectKey, label, h.sid, store.ChunkPlainText(output, 20), int64(len(text)), kept); err != nil {
		return err
	}
	interrupted, _ := resp.Bool("interrupted")
	_, err = io.WriteString(h.out, `{"hookSpecificOutput":{"hookEventName":"PostToolUse","updatedToolOutput":{"stdout":`+
		string(hookio.QuoteString(text))+`,"stderr":"","interrupted":`+strconv.FormatBool(interrupted)+`,"isImage":false}}}`+"\n")
	return err
}
```

Add `"errors"` and `"strconv"` to the stdlib imports (keep `"io"` and `"strings"`; add them if missing), and `"github.com/lang315/context-mode-go/internal/compact"` to the module imports, sorted.

`testdata/golden/HOOKS-KNOWN-DIFFS.md`: add a row after `routing:bash-read-guidance`:

```
| `posttooluse:bash-compact` | (no TS case) | A main-session Bash result above `CMGO_BASH_COMPACT_MIN_BYTES` (default 5120) and at most 30000 bytes, not from a reading command (`sed`, `cat`, `head`, `tail`, `nl`, `tac`, `bat`, `less`, `git diff`, `git show`) and without `# cmgo:full`, is indexed as `bash:<tool_use_id>` and replaced through `updatedToolOutput` with its head, tail, error lines and a `ctx_search` hint, when the MCP server is up. TS never replaces tool output; posttooluse prints nothing otherwise, as before. Spec `2026-10-27-cmgo-bash-compact-design.md`. |
```

`testdata/golden/KNOWN-DIFFS.md`: add after the `stats:kept-cap` entry:

```
- `stats:bash-compact`: `tool_stats` has a `bash-compact` row per session (`server_id` `hook`,
  window = the session) for the Bash results `cmgo-hook` compacted, counted in `stats_totals` too,
  so `ctx_stats` lists `bash-compact` and lifetime includes it. TS has no such tool. No golden case
  records one, so none is skipped. Spec: `2026-10-27-cmgo-bash-compact-design.md`.
```

`CLAUDE.md`:
- In the switches sentence, after the `CMGO_BASH_READ_MIN_BYTES` item, add: `` `CMGO_BASH_COMPACT_MIN_BYTES` (posttooluse Bash compaction threshold, 1-30000, default 5120, `0` = off) ``.
- In the Hooks bullet, change `` `posttooluse`, `posttoolusefailure`, `userpromptsubmit` and `stop` capture session events `` to `` `posttooluse` compacts a large main-session Bash result (`internal/compact`, indexed as `bash:<tool_use_id>`); it, `posttoolusefailure`, `userpromptsubmit` and `stop` capture session events ``.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/hookcmd ./cmd/cmgo-hook ./internal/capture -count=1 && go vet ./internal/hookcmd && gofmt -l .`
Expected: `ok` for all three. The `cmd/cmgo-hook` deps test passes. gofmt prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/hookcmd testdata/golden/HOOKS-KNOWN-DIFFS.md testdata/golden/KNOWN-DIFFS.md CLAUDE.md
git commit -m "feat(hook): compact large main-session Bash output

posttooluse indexes a Bash result of 5 KB to 30,000 bytes as bash:<id>
and replaces it through updatedToolOutput with its head, tail, error lines
and a ctx_search hint. Reading commands, subagents, # cmgo:full and a down
MCP server are left alone; a failed write prints nothing.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: bench case

**Files:**
- Modify: `bench/hook-latency.py`

**Interfaces:**
- Consumes: `fire(binary, env, event, payload) -> (ms, stdout)`, `report(name, times)`, `p95`, `LIMIT_MS`, `project`, `runs`, and the `ok` accumulator in `main`.
- Produces: the case `posttooluse bash compact (20 KB)` with its own limit `COMPACT_LIMIT_MS = 25.0`.

- [ ] **Step 1: Change `report` to take a limit**

```python
def report(name, times, limit=LIMIT_MS):
    ok = p95(times) <= limit
    print(f"{name}: {stats(times)} {'PASS' if ok else 'FAIL'}")
    return ok
```

Add `COMPACT_LIMIT_MS = 25.0  # bash-compact spec §6: the case indexes 20 KB` after the `LIMIT_MS, WORKERS, ...` line.

- [ ] **Step 2: Add the case after the `for name, event, payload, check in cases:` loop and before the concurrent workers**

```python
        # Bash compaction (bash-compact spec §6): indexes about 20 KB per fire.
        compact_out = "".join(f"line {n:04d} ok github.com/x/pkg{n} 0.01s\n" for n in range(560))
        times = []
        for i in range(runs):
            ms, out = fire(binary, env, "posttooluse", {
                "tool_name": "Bash", "tool_use_id": f"bench-compact-{i}", "session_id": "bench-compact", "cwd": project,
                "tool_input": {"command": "go test ./..."},
                "tool_response": {"stdout": compact_out, "stderr": "", "interrupted": False, "isImage": False}})
            if i == 0 and b'"updatedToolOutput"' not in out:
                print("posttooluse bash compact (20 KB): not supported by this binary, skipped")
                break
            times.append(ms)
        if times:
            ok &= report("posttooluse bash compact (20 KB)", times, COMPACT_LIMIT_MS)
```

Also add a line to the module docstring: `posttooluse bash compact (20 KB) has its own limit, 25 ms (bash-compact spec §6).`

- [ ] **Step 3: Run the bench on the branch binary**

Run: `go build -o bin/ ./cmd/cmgo ./cmd/cmgo-hook && python3 -I bench/hook-latency.py bin/cmgo-hook 30`
Expected: the new line `posttooluse bash compact (20 KB): p50 … p95 … PASS`. If the 1-min load is above 3, results are noisy. Rerun when the load is lower before judging.

- [ ] **Step 4: Commit**

```bash
git add bench/hook-latency.py
git commit -m "bench: time the posttooluse Bash compaction case

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: audit script for M1-M3

**Files:**
- Create: `tools/audit/compact_audit.py`
- Modify: `tools/audit/README.md` (one usage line), and spec §5 (one sentence saying the measurement lives in `compact_audit.py`, not in `cmgo_audit.py`).

**Interfaces:**
- Consumes: `~/.claude/cmgo/cmgo.db` (copied to a temp dir, read-only on `~/.claude`) and `~/.claude/projects/*/*.jsonl` main-session transcripts.
- Produces: `python3 -I tools/audit/compact_audit.py [--since 2026-10-28] [--self-test]`, which prints M1, M2, M3 and the §5 verdict.

- [ ] **Step 1: Write the script with its self-test first**

```python
#!/usr/bin/env python3
"""Bash compaction effect (bash-compact spec §5). Read-only on ~/.claude.
    python3 -I compact_audit.py [--since 2026-10-28] [--self-test]
M1 saved = bash-compact kept bytes (DB). M2 recovery = share of compactions
followed within 3 tool calls by a ctx_search naming the source or a re-run of
the same command (with or without # cmgo:full). M3 net = M1 - bytes those
recovering calls returned.
"""
import argparse, glob, json, os, re, shutil, sqlite3, sys, tempfile

HOME = os.path.expanduser("~")
DB = os.path.join(HOME, ".claude", "cmgo", "cmgo.db")
ROOT = os.path.join(HOME, ".claude", "projects")
LABEL_RE = re.compile(r'\[cmgo: the full output is indexed as "(bash:[^"]+)"')
WINDOW = 3


def norm(cmd):
    return " ".join(cmd.replace("# cmgo:full", "").split())


def size(content):
    if isinstance(content, str):
        return len(content.encode())
    return sum(len(x.get("text", "").encode()) for x in content or [] if isinstance(x, dict))


def scan(lines, since=""):
    """Compactions and their recovery from one transcript's JSON lines;
    messages stamped before since are skipped."""
    calls, results, order = {}, {}, []
    for line in lines:
        try:
            m = json.loads(line)
        except ValueError:
            continue
        if m.get("timestamp", since) < since:
            continue
        content = (m.get("message") or {}).get("content")
        if not isinstance(content, list):
            continue
        for x in content:
            if x.get("type") == "tool_use":
                calls[x["id"]] = (x["name"], x.get("input") or {})
                order.append(x["id"])
            elif x.get("type") == "tool_result":
                results[x.get("tool_use_id")] = x.get("content")
    out = []
    for i, cid in enumerate(order):
        name, inp = calls[cid]
        res = results.get(cid)
        text = res if isinstance(res, str) else " ".join(y.get("text", "") for y in res or [] if isinstance(y, dict))
        mm = LABEL_RE.search(text or "") if name == "Bash" else None
        if not mm:
            continue
        label, back = mm.group(1), 0
        recovered = False
        for nid in order[i + 1:i + 1 + WINDOW]:
            n2, i2 = calls[nid]
            if (n2.endswith("ctx_search") and label in json.dumps(i2)) or (n2 == "Bash" and norm(i2.get("command", "")) == norm(inp.get("command", ""))):
                recovered = True
                back += size(results.get(nid))
        out.append((label, recovered, back))
    return out


def self_test():
    t = [
        {"message": {"content": [{"type": "tool_use", "id": "a", "name": "Bash", "input": {"command": "go test ./..."}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "a", "content": 'x [cmgo: the full output is indexed as "bash:a". y'}]}},
        {"message": {"content": [{"type": "tool_use", "id": "b", "name": "mcp__cmgo__ctx_search", "input": {"queries": ["q"], "source": "bash:a"}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "b", "content": "12345"}]}},
        {"message": {"content": [{"type": "tool_use", "id": "c", "name": "Bash", "input": {"command": "ls"}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "c", "content": '[cmgo: the full output is indexed as "bash:c".'}]}},
        {"message": {"content": [{"type": "tool_use", "id": "d", "name": "Bash", "input": {"command": "ls   # cmgo:full"}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "d", "content": "abc"}]}},
        {"message": {"content": [{"type": "tool_use", "id": "e", "name": "Bash", "input": {"command": "pwd"}}]}},
        {"message": {"content": [{"type": "tool_result", "tool_use_id": "e", "content": '[cmgo: the full output is indexed as "bash:e".'}]}},
    ]
    got = scan(json.dumps(x) for x in t)
    assert got == [("bash:a", True, 5), ("bash:c", True, 3), ("bash:e", False, 0)], got
    assert norm("ls   # cmgo:full") == "ls"
    print("self-test ok")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--since", default="2026-10-28")
    ap.add_argument("--self-test", action="store_true")
    a = ap.parse_args()
    if a.self_test:
        return self_test()
    tmp = tempfile.mkdtemp(prefix="cmgo-compact-audit-")
    try:
        db = os.path.join(tmp, "cmgo.db")
        shutil.copy(DB, db)
        if os.path.exists(DB + "-wal"):
            shutil.copy(DB + "-wal", db + "-wal")
        c = sqlite3.connect(db)
        calls, kept, sessions = c.execute(
            "SELECT coalesce(sum(calls),0), coalesce(sum(bytes_kept),0), count(DISTINCT window) FROM tool_stats WHERE tool = 'bash-compact' AND updated_at >= ?",
            (a.since,)).fetchone()
        c.close()
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    found = []
    for f in glob.glob(os.path.join(ROOT, "*", "*.jsonl")):
        if "scratchpad" in f or "mem-observer" in f:
            continue
        with open(f, errors="replace") as fh:
            found += scan(fh, a.since)
    n = len(found)
    rec = sum(1 for _, r, _ in found if r)
    back = sum(b for _, _, b in found)
    m2 = rec / n if n else 0.0
    m3 = kept - back
    print(f"compactions: DB {calls} calls in {sessions} sessions; transcripts {n}")
    print(f"M1 saved: {kept} bytes ({kept / max(1, sessions):.0f} per session)")
    print(f"M2 recovery: {rec} of {n} ({100 * m2:.0f}%), bringing back {back} bytes")
    print(f"M3 net: {m3} bytes ({100 * m3 / max(1, kept):.0f}% of M1)")
    if n < 200:
        print(f"verdict: not enough data ({n} of 200 compactions)")
    elif m2 > 0.5 or m3 < 0:
        print("verdict: remove (spec §5)")
    elif m2 > 0.3:
        print("verdict: raise the default threshold to 8192 once (spec §5)")
    elif m3 >= 0.5 * kept:
        print("verdict: keep (spec §5)")
    else:
        print("verdict: net below 50% of saved; raise the threshold to 8192 once (spec §5)")


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 2: Run the self-test**

Run: `python3 -I tools/audit/compact_audit.py --self-test`
Expected: `self-test ok`. On an assertion error, fix `scan`, not the expected values.

- [ ] **Step 3: Run it against the live data, read-only**

Run: `python3 -I tools/audit/compact_audit.py`
Expected: it prints the counts. With the feature not installed yet, the result is `compactions: DB 0 …` and `verdict: not enough data (0 of 200 compactions)`.

- [ ] **Step 4: Docs**

- `tools/audit/README.md`: add `python3 -I tools/audit/compact_audit.py [--since YYYY-MM-DD]: Bash compaction M1-M3 and the keep/raise/remove verdict (bash-compact spec §5).`
- Spec §5, first line: replace "`tools/audit/cmgo_audit.py` gets a "Bash compaction" section" with "`tools/audit/compact_audit.py` measures".

- [ ] **Step 5: Commit**

```bash
git add tools/audit/compact_audit.py tools/audit/README.md docs/superpowers/specs/2026-10-27-cmgo-bash-compact-design.md
git commit -m "tools/audit: measure Bash compaction recovery and net effect

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: E2E check, full verification, PR

**Files:**
- None in the repo. Scratch only: `$SCRATCH/e2e/` under the session scratchpad.

- [ ] **Step 1: Build and run the full checks**

```bash
go build -o bin/ ./cmd/cmgo ./cmd/cmgo-hook
gofmt -l .
go vet ./...
go test ./... -count=1
go test -race ./... -count=1 2>&1 | tail -6
```

Expected: gofmt prints nothing, vet exits 0, all 21 packages `ok` (20 plus `internal/compact`), and `-race` exits 0.

- [ ] **Step 2: E2E with `claude -p`, scratch data dir**

```bash
E=$SCRATCH/e2e; rm -rf $E; mkdir -p $E/work $E/cmgo
cat > $E/settings.json <<EOF
{"hooks": {"PostToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "$PWD/bin/cmgo-hook posttooluse"}]}]}}
EOF
cd $E/work && CMGO_DIR=$E/cmgo claude -p --model haiku --settings $E/settings.json \
  --allowedTools "Bash(python3:*)" mcp__cmgo__ctx_search --max-turns 6 \
  "Run: python3 -c \"[print(f'line {i:04d} ok github.com/x/pkg{i} 0.01s') if i != 300 else print('zebra-marker-77 is the answer') for i in range(600)]\". The output may be shortened. Find the line containing 'zebra' and reply with that whole line only." < /dev/null
```

Expected:
- The reply is `zebra-marker-77 is the answer`.
- In the session transcript under `~/.claude/projects/*e2e-work*`, the Bash `tool_result` contains `[cmgo: the full output is indexed as "bash:` and the next tool call is `ctx_search`.
- `$E/cmgo/hook-errors.log` does not exist.

The user MCP server `cmgo` (installed binary) inherits `CMGO_DIR`, writes its ready sentinel into `$E/cmgo/run` and searches the scratch DB.

- [ ] **Step 3: Bench at low load**

Wait until the 1-min load is under 3. Then run `python3 -I bench/hook-latency.py bin/cmgo-hook 100` and `python3 -I bench/hook-latency.py ~/go/bin/cmgo-hook 100` back to back.
Expected:
- The branch binary passes every case, including `posttooluse bash compact (20 KB)` with p95 ≤ 25 ms.
- No shared case is 1 ms or more slower in p95 than on the installed binary.

- [ ] **Step 4: Request the whole-branch review**

Use superpowers:requesting-code-review on `cleanup-kept-cap..HEAD`. Fix every Critical and Important finding, then re-run Step 1.

- [ ] **Step 5: Push and open the PR against `cleanup-kept-cap`**

The PR body carries:
- the spec and plan paths;
- the Step 1 outputs (gofmt, vet, go test, the `-race` tail);
- the E2E reply and transcript excerpt;
- the bench table for both binaries with the load at start and end;
- the review summary;
- a "Not done" list: not merged, not installed, and the §5 measurement starts after install.
