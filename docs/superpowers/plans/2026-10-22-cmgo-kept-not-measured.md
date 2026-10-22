# Kept "not measured" in ctx_stats Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (native, owner's choice) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ctx_stats` says how many `ctx_execute` calls could not be measured, instead of showing a bare `0 B kept`.

**Architecture:** The executor reports `Result.Measured`. `ctx_execute` records an `unmeasured_calls` count per `tool_stats` row (one new column, migration 4). The store sums it per tool and for the lifetime. `stats.Format` prints a suffix only when the count is above 0.

**Tech Stack:** Go, ncruces `go-sqlite3`, existing test harnesses (`newHarness`, `recordStats`, `statsFixture`, `newTestExec`, `run`).

**Spec:** `docs/superpowers/specs/2026-10-22-cmgo-kept-not-measured-design.md` (rev 2)

## Global Constraints

- Migration 4 adds only `unmeasured_calls INTEGER NOT NULL DEFAULT 0` to `tool_stats`; never edit an existing migration entry.
- Nothing in any output says "measured" or "unrecorded"; only `· N not measured` (session `ctx_execute` row) and `· N ctx_execute calls not measured` (lifetime line), each only when N > 0.
- Headline `Kept out of context`, `pct`, the statusline, and `stats_totals` do not change.
- `AddToolStat` keeps its signature (19 test callers).
- `cmgo-hook` must not link `executor`, `mcpserver` or `fetch`: `go list -deps ./cmd/cmgo-hook | grep -c internal/executor` prints 0.
- Tests never touch the real home. Commits in English with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. No session URLs. No push, no PR, no `go install`/`cmgo install` before the owner's go.

## Review Focus

- A JS-only session, and a DB with only pre-migration rows, print byte for byte as before (no suffix).
- A `ctx_execute` that fails argument parsing or hits a security denial adds 0 to `unmeasured_calls`.
- Python with `CMGO_PY_COUNTERS=0`, a shell run and any detached Background run (JS included, its counters were never written) are unmeasured; a Background run that finishes before its timeout is not detached, so an instrumented one is measured.
- Opening a v3 database upgrades to v4 and keeps every existing row.
- Several servers writing the same `(server_id, tool, window)` row add the counts (upsert), not overwrite.

---

### Task 1: `executor.Result.Measured`

**Files:**
- Modify: `internal/executor/run.go` (Result struct line ~27, `Run` lines ~92-97)
- Test: `internal/executor/measured_test.go` (create)

**Interfaces:**
- Produces: `Result.Measured bool`, true when the run used an instrumented path (JS/TS `wrapJS`, or the Python bootstrap argv) and was not detached (`!Backgrounded`): a detached run has not exited, so its counters were never written.

- [ ] **Step 1: Write the failing test**

```go
package executor

import (
	"os"
	"testing"
	"time"
)

// Measured says whether the run could report SandboxBytes: JS/TS and Python
// with counters on. Everything else (shell, other languages, Python with the
// switch off or in the background) has no counter, so its kept bytes are unknown.
func TestResultMeasured(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		off  bool // CMGO_PY_COUNTERS=0
		want bool
	}{
		{"javascript", Request{Language: JavaScript, Code: "console.log(1)"}, false, true},
		{"shell", Request{Language: Shell, Code: "echo 1"}, false, false},
		{"python", Request{Language: Python, Code: "print(1)"}, false, true},
		{"python counters off", Request{Language: Python, Code: "print(1)"}, true, false},
		{"python background", Request{Language: Python, Code: "print(1)", Background: true, Timeout: 300 * time.Millisecond}, false, false},
		// Detached: still running when Run returns, so no counter line was written.
		{"javascript background detached", Request{Language: JavaScript, Code: "console.log(1)", Background: true, Timeout: 300 * time.Millisecond}, false, false},
		// wrapJS keeps a background JS run alive with setInterval, so a run that
		// exits before its timeout is not reachable from JS. Shell exits, but is not instrumented.
		{"shell background finished", Request{Language: Shell, Code: "echo 1", Background: true, Timeout: 5 * time.Second}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestExec(t)
			if c.off {
				e.getenv = func(k string) string {
					if k == "CMGO_PY_COUNTERS" {
						return "0"
					}
					return os.Getenv(k)
				}
			}
			if c.req.Language == Python && c.req.Timeout == 0 {
				c.req.Timeout = 60 * time.Second
			}
			res := run(t, e, c.req)
			if res.Measured != c.want {
				t.Errorf("Measured = %v, want %v", res.Measured, c.want)
			}
		})
	}
}
```

If `e.getenv` has a different shape or `JavaScript` is skipped when node is missing, mirror how `pycounters_test.go` stubs `getenv` and how `counters_test.go` skips; do not change production code to fit the test.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/executor -run TestResultMeasured -count=1`
Expected: FAIL to compile (`res.Measured undefined`).

- [ ] **Step 3: Implement**

In `Result`, after `SandboxBytes`:

```go
	Measured bool // the run was instrumented, so SandboxBytes is a real count (0 may be true)
```

In `Run`, set it where the two counters are read:

```go
	if countsFile != "" {
		res.SandboxBytes = readPyCounts(countsFile)
		res.Measured = !res.Backgrounded // a detached run has not exited: no counts yet
	}
	if strings.HasPrefix(code, jsCounters) { // only a wrapJS run reports counts
		res.Stderr, res.SandboxBytes = takeCounters(res.Stderr)
		res.Measured = !res.Backgrounded
	}
```

- [ ] **Step 4: Run to see it pass**

Run: `go test ./internal/executor -run TestResultMeasured -count=1 -v`
Expected: PASS, seven subtests. Then `go test ./internal/executor -count=1` → ok.

- [ ] **Step 5: Commit**

```bash
git add internal/executor/run.go internal/executor/measured_test.go
git commit -m "feat(executor): report whether a run was measured

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: store column, `AddToolStatExec`, snapshot fields

**Files:**
- Modify: `internal/store/store.go` (append migration 4 to `migrations`)
- Modify: `internal/store/stats.go` (`ToolStat`, `AddToolStat`, `toolStats`, `StatsSnapshot`, `Stats` query)
- Test: `internal/store/unmeasured_test.go` (create); `internal/store/health_test.go` already compares to `len(migrations)`

**Interfaces:**
- Produces: `ToolStat.Unmeasured int64`; `(*Store).AddToolStatExec(serverID string, ccPID int, project, startedAt, tool string, returned, kept, unmeasured int64) error`; `StatsSnapshot.LifetimeUnmeasured int64`.
- `AddToolStat(...)` unchanged, calls `AddToolStatExec(..., 0)`.

- [ ] **Step 1: Write the failing tests**

```go
package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestUnmeasuredCountsAddUp(t *testing.T) {
	s := statsFixture(t)
	q := StatsQuery{ServerID: "srvA", CCPID: 100, Project: "/p"}
	before, _ := s.Stats(q)
	if err := s.AddToolStatExec("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 5, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStatExec("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 5, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStat("srvA", 100, "/p", "2026-10-21 08:00:00", "ctx_execute", 5, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stats(q)
	if err != nil {
		t.Fatal(err)
	}
	var ex ToolStat
	for _, ts := range got.Tools {
		if ts.Tool == "ctx_execute" {
			ex = ts
		}
	}
	var was ToolStat
	for _, ts := range before.Tools {
		if ts.Tool == "ctx_execute" {
			was = ts
		}
	}
	if ex.Unmeasured != was.Unmeasured+2 || ex.Calls != was.Calls+3 {
		t.Fatalf("ctx_execute = %+v, before %+v; want Unmeasured +2, Calls +3", ex, was)
	}
	if got.LifetimeUnmeasured != before.LifetimeUnmeasured+2 {
		t.Fatalf("LifetimeUnmeasured = %d, before %d; want +2", got.LifetimeUnmeasured, before.LifetimeUnmeasured)
	}
}

// A database at schema version 3 opens at 4 with every row kept and counted 0.
func TestMigration4KeepsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddToolStat("srv", 1, "/p", "2026-10-21 08:00:00", "ctx_execute", 1, 2); err != nil {
		t.Fatal(err)
	}
	// Rewind to v3: drop the column and the version, as an old file would look.
	if _, err := s.db.Exec("ALTER TABLE tool_stats DROP COLUMN unmeasured_calls"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != 4 {
		t.Fatalf("SchemaVersion = %d, want 4", v)
	}
	got, err := s.toolStats("server_id = ?", "srv")
	want := []ToolStat{{Tool: "ctx_execute", Calls: 1, BytesReturned: 1, BytesKept: 2}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, %v; want %+v", got, err, want)
	}
}
```

If `statsFixture` seeds `ctx_execute` rows for `srvA`, the relative assertions hold; adjust the project/pid to what the fixture linked (`/p`, 100, `srvA` as in `stats_test.go`). `DROP COLUMN` needs SQLite 3.35+; ncruces ships newer.

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/store -run 'TestUnmeasured|TestMigration4' -count=1`
Expected: FAIL to compile (`AddToolStatExec`, `Unmeasured`, `LifetimeUnmeasured` undefined).

- [ ] **Step 3: Implement**

`store.go`: append a fourth entry to `migrations`:

```go
	// ctx_execute calls whose kept bytes cannot be measured (kept-not-measured spec §1).
	`ALTER TABLE tool_stats ADD COLUMN unmeasured_calls INTEGER NOT NULL DEFAULT 0;`,
```

`stats.go`:

- `ToolStat` gains `Unmeasured int64`.
- Rename the body of `AddToolStat` to `AddToolStatExec` with the extra `unmeasured int64` parameter; add `unmeasured_calls` to the INSERT column list (value `?8`), and `unmeasured_calls = unmeasured_calls + excluded.unmeasured_calls` to the DO UPDATE. `AddToolStat` becomes:

```go
func (s *Store) AddToolStat(serverID string, ccPID int, project, startedAt, tool string, returned, kept int64) error {
	return s.AddToolStatExec(serverID, ccPID, project, startedAt, tool, returned, kept, 0)
}
```

- `toolStats`: select `SUM(unmeasured_calls)` and scan into `t.Unmeasured`.
- `StatsSnapshot` gains `LifetimeUnmeasured int64 // every ctx_execute call recorded as not measured`; in `Stats` add a subselect `(SELECT COALESCE(SUM(unmeasured_calls), 0) FROM tool_stats)` to the big query and scan it into `&st.LifetimeUnmeasured`.

- [ ] **Step 4: Run to see it pass**

Run: `go test ./internal/store -count=1`
Expected: ok (existing `TestSchemaVersion`, `DeepEqual` tests still pass since the new field is 0).

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): unmeasured_calls column and AddToolStatExec

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: `stats.Format` suffixes

**Files:**
- Modify: `internal/stats/report.go` (`ToolRow`, `Report`, `Format`)
- Test: `internal/stats/report_test.go`

**Interfaces:**
- Consumes: nothing from Task 2 (pure renderer).
- Produces: `ToolRow.Unmeasured int64`, `Report.LifetimeUnmeasured int64`.

- [ ] **Step 1: Write the failing tests** (append to `report_test.go`; reuse the file's existing sample `Report` helper if it has one, else build a minimal one)

```go
func TestFormatNotMeasuredSuffixes(t *testing.T) {
	r := Report{Version: "x", Now: time.Unix(1000, 0), SessionLinked: true, SessionStart: time.Unix(0, 0),
		Tools: []ToolRow{{Tool: "ctx_execute", Calls: 20, Returned: 3000, Kept: 1200, Unmeasured: 14}}}
	got := Format(r)
	if !strings.Contains(got, "returned  ·  14 not measured\n") {
		t.Errorf("row suffix missing:\n%s", got)
	}
	r.Tools[0].Unmeasured = 0
	if strings.Contains(Format(r), "not measured") {
		t.Errorf("suffix printed with 0:\n%s", Format(r))
	}
	r.LifetimeUnmeasured = 31
	if got := Format(r); !strings.Contains(got, "kept out  ·  31 ctx_execute calls not measured  ·  0 sessions") {
		t.Errorf("lifetime suffix missing:\n%s", got)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/stats -run TestFormatNotMeasuredSuffixes -count=1`
Expected: FAIL to compile (`Unmeasured` undefined).

- [ ] **Step 3: Implement**

`ToolRow` gains `Unmeasured int64`; `Report` gains `LifetimeUnmeasured int64`. In `Format`, the tool-row loop becomes:

```go
	for _, t := range tools {
		fmt.Fprintf(&b, "  %-22s %4d calls  %9s kept  %9s returned", t.Tool, t.Calls, KB(t.Kept), KB(t.Returned))
		if t.Unmeasured > 0 {
			fmt.Fprintf(&b, "  ·  %d not measured", t.Unmeasured)
		}
		b.WriteString("\n")
	}
```

and the lifetime line:

```go
	fmt.Fprintf(&b, "\nAll projects  ·  %s kept out", KB(r.LifetimeKept))
	if r.LifetimeUnmeasured > 0 {
		fmt.Fprintf(&b, "  ·  %d ctx_execute calls not measured", r.LifetimeUnmeasured)
	}
	fmt.Fprintf(&b, "  ·  %d sessions", r.LifetimeSessions)
```

- [ ] **Step 4: Run to see it pass**

Run: `go test ./internal/stats -count=1`
Expected: ok (existing exact-text tests unchanged because 0 prints nothing).

- [ ] **Step 5: Commit**

```bash
git add internal/stats
git commit -m "feat(stats): show not-measured counts in the report

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: record and report through `mcpserver`

**Files:**
- Modify: `internal/mcpserver/stats.go` (`statJob`, worker call, `RecordExec`)
- Modify: `internal/mcpserver/execute.go` (handler: `unmeasured` flag, `RecordExec`)
- Modify: `internal/mcpserver/ctxstats.go` (`statsReport` mapping)
- Test: `internal/mcpserver/unmeasured_test.go` (create)

**Interfaces:**
- Consumes: `Result.Measured` (Task 1), `Store.AddToolStatExec`, `ToolStat.Unmeasured`, `StatsSnapshot.LifetimeUnmeasured` (Task 2), `ToolRow.Unmeasured`, `Report.LifetimeUnmeasured` (Task 3).
- Produces: `(*StatsRecorder).RecordExec(d *Deps, tool string, raw int64, res *mcp.CallToolResult, unmeasured bool)`.

- [ ] **Step 1: Write the failing tests**

```go
package mcpserver

import (
	"strings"
	"testing"
)

func TestExecuteRecordsUnmeasured(t *testing.T) {
	h := newHarness(t, nil)
	row := h.recordStats()
	h.call("ctx_execute", args("language", "shell", "code", "echo hi"))
	if got := row("ctx_execute"); got.Calls != 1 || got.Unmeasured != 1 {
		t.Fatalf("after shell: %+v", got)
	}
	h.call("ctx_execute", args("language", "javascript", "code", "console.log(1)"))
	if got := row("ctx_execute"); got.Calls != 2 || got.Unmeasured != 1 {
		t.Fatalf("after javascript: %+v", got)
	}
	// Never ran code: an argument error adds a call, not an unmeasured call.
	h.call("ctx_execute", `{"language":"shell"}`)
	if got := row("ctx_execute"); got.Calls != 3 || got.Unmeasured != 1 {
		t.Fatalf("after argument error: %+v", got)
	}
}

func TestStatsShowsNotMeasuredOnlyWhenCounted(t *testing.T) {
	h := newHarness(t, nil)
	h.recordStats()
	h.call("ctx_execute", args("language", "javascript", "code", "console.log(1)"))
	if got, _ := h.call("ctx_stats", `{}`); strings.Contains(got, "not measured") {
		t.Fatalf("JS-only report mentions not measured:\n%s", got)
	}
	h.call("ctx_execute", args("language", "shell", "code", "echo hi"))
	got, _ := h.call("ctx_stats", `{}`)
	if !strings.Contains(got, "returned  ·  1 not measured\n") || !strings.Contains(got, "kept out  ·  1 ctx_execute calls not measured  ·  ") {
		t.Fatalf("report:\n%s", got)
	}
}
```

Check the harness: if `ctx_stats` rows of the unlinked server print only after `Stats` has a server id, `recordStats` already sets `srv-test`. If JS needs node, follow `execute_test.go`'s skip pattern.

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/mcpserver -run 'TestExecuteRecordsUnmeasured|TestStatsShowsNotMeasured' -count=1`
Expected: FAIL (`Unmeasured` stays 0; no suffix).

- [ ] **Step 3: Implement**

`stats.go`: `statJob` gains `unmeasured int64`; the worker calls `j.st.AddToolStatExec(r.ServerID, r.CCPID, j.project, r.StartedAt, j.tool, j.returned, j.kept, j.unmeasured)`. Turn the body of `Record` into `RecordExec(d, tool, raw, res, unmeasured bool)` that sets `unmeasured: 1` when true, and make `Record` call `r.RecordExec(d, tool, raw, res, false)` (a nil receiver already returns early inside).

`execute.go` handler: replace the `raw` defer with

```go
		var raw int64
		var unmeasured bool
		defer func() { d.Stats.RecordExec(d, "ctx_execute", raw, res, unmeasured) }()
```

and after `r, err := d.Exec.Run(...)` succeeds: `unmeasured = !r.Measured`.

`ctxstats.go` `statsReport`: set `LifetimeUnmeasured: s.LifetimeUnmeasured` and `Unmeasured: t.Unmeasured` on each `stats.ToolRow` (only `ctx_execute` is ever non-zero).

- [ ] **Step 4: Run to see it pass**

Run: `go test ./internal/mcpserver -count=1`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver
git commit -m "feat(mcpserver): record and report unmeasured ctx_execute calls

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: docs, KNOWN-DIFFS, final verification

**Files:**
- Modify: `testdata/golden/KNOWN-DIFFS.md` (entry `stats:kept-not-measured`, after `executor:python-counters`)
- Modify: `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md` (§1.1 schema note, §1.3 `ctx_execute` row, §2 output sample)
- Test: `internal/mcpserver/unmeasured_test.go` (append)

- [ ] **Step 1: Write the failing test** (append)

```go
func TestKnownDiffsListsKeptNotMeasured(t *testing.T) {
	b, err := os.ReadFile("../../testdata/golden/KNOWN-DIFFS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "stats:kept-not-measured") {
		t.Error("KNOWN-DIFFS.md has no stats:kept-not-measured entry")
	}
}
```

Add `"os"` to the file's imports.

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/mcpserver -run TestKnownDiffsListsKeptNotMeasured -count=1`
Expected: FAIL ("has no stats:kept-not-measured entry").

- [ ] **Step 3: Write the docs**

KNOWN-DIFFS entry, same shape as `executor:python-counters`:

```markdown
- `stats:kept-not-measured`: the `ctx_stats` `ctx_execute` row ends with `· N not measured`,
  and the `All projects` line with `· N ctx_execute calls not measured`, when `ctx_execute`
  ran code cmgo cannot count (shell and the other languages; TS has no such field). No golden
  case runs such a call through stats, so none is skipped. Spec: `2026-10-22-cmgo-kept-not-measured-design.md`.
```

Stats spec: in §1.1 note migration 4 and `unmeasured_calls`; in §1.3 add a sentence under the table that `ctx_execute` calls with no counter are added to `unmeasured_calls`; in §2 add the two suffixes to the sample.

- [ ] **Step 4: Verify everything**

Run each, read the output:

```bash
gofmt -l .                                   # prints nothing
go vet ./...                                 # exit 0
go test ./... -count=1                       # 20 ok
go test -race ./... -count=1                 # 20 ok
go list -deps ./cmd/cmgo-hook | grep -c internal/executor   # 0
```

Then a live check against scratch dirs (not the real home):

```bash
SCR=<scratchpad>/live; mkdir -p $SCR
go build -o $SCR/cmgo ./cmd/cmgo
# drive $SCR/cmgo serve with HOME, CMGO_DIR, CLAUDE_CONFIG_DIR under $SCR: one shell
# and one javascript ctx_execute, then ctx_stats; save the report to $SCR/report.txt
```

Expected: the report shows `· 1 not measured` on the `ctx_execute` row and `· 1 ctx_execute calls not measured` on the lifetime line. Keep the output for the PR body.

- [ ] **Step 5: Commit**

```bash
git add testdata docs internal/mcpserver
git commit -m "docs: record the not-measured display in KNOWN-DIFFS and the stats spec

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

After Task 5: whole-branch review by one fresh reviewer on the most capable model (`review-package`), fix Critical/Important by TDD in one pass, then report to "context" and wait for the owner's go before push and PR.

## Self-review

- Spec §1 column, §2 recording (Task 1, 2, 4), §3 display (Task 3, 4), Q3 compat (Task 2 `TestMigration4KeepsRows`, Task 3 absent-suffix assertion), §4 KNOWN-DIFFS (Task 5), §5 tests (every bullet has a test above), hook deps (Task 5 step 4).
- Names consistent across tasks: `Measured`, `Unmeasured`, `LifetimeUnmeasured`, `AddToolStatExec`, `RecordExec`.
- Execution method: native, as in PR #12, since the five tasks share the same few interfaces and are small.
