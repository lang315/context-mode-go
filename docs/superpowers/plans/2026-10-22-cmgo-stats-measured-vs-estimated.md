# Lifetime and statusline measured only Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (native, owner's choice) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** the `ctx_stats` lifetime line and the statusline show measured sandbox bytes only; the session line `Redirected by hooks` excludes Read tips and says `(estimated)`.

**Architecture:** `lifetimeKept` in the store becomes the `stats_totals` sum (the one query both `ctx_stats` and the statusline use). The session redirect query ignores `read-redirected`. `stats.Format` adds the `(estimated)` text. No schema change.

**Tech Stack:** Go, ncruces `go-sqlite3`, existing test helpers (`statsFixture`, `openTest`, `insertTestEvents`, `newHarness`).

**Spec:** `docs/superpowers/specs/2026-10-22-cmgo-stats-measured-vs-estimated-design.md` (rev 2)

## Global Constraints

- No migration, no change to `routing` or the capture write path; `read-redirected` events keep being written.
- Lifetime kept (`ctx_stats` `All projects` line and statusline) = `SUM(stats_totals.bytes_kept)` and nothing else; no lifetime estimate appears anywhere.
- The session line is `  Redirected by hooks    <n> calls  ·  ~<kb> (estimated)`; `n` and bytes exclude `read-redirected`.
- `cmgo-hook` must not link `executor`, `mcpserver` or `fetch`: `go list -deps ./cmd/cmgo-hook | grep -c internal/executor` prints 0.
- Tests never touch the real home. Commits in English with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. No session URLs. No push, no PR, no `go install`/`cmgo install` before the owner's go.

## Review Focus

- A database whose redirect events are all Read tips shows `Redirected by hooks    0 calls  ·  ~0 B (estimated)` and a lifetime equal to `stats_totals`.
- The statusline lines for a session with and without kept bytes print exactly as before when no redirect events exist (the existing statusline tests stay unchanged).
- `Bash` and `WebFetch` redirects still count in the session line.
- A database at schema 3 or 4 opens read-only for the statusline and works (no new column is read).
- `ctx_purge` still leaves the lifetime consistent: it deletes the project's `tool_stats` and `stats_totals`, and the lifetime is now only `stats_totals`.

---

### Task 1: store: measured-only lifetime, Read tips out of the session redirect numbers

**Files:**
- Modify: `internal/store/stats.go` (`lifetimeKept` const, the session redirect query in `Stats`)
- Modify: `internal/store/stats_test.go` (fixture expectation at the `LifetimeKept:` line; `TestStatusLineQueriesDoNotScanGrowingTables`)
- Modify: `internal/store/readonly_test.go` (`TestSessionLine` expected lifetime; replace `TestLifetimeKeptUsesRedirectIndex`)
- Test: `internal/store/stats_test.go`, `internal/store/readonly_test.go`

**Interfaces:**
- Produces: `StatsSnapshot.LifetimeKept` and the third value of `SessionLine` are measured bytes only; `Redirects`/`RedirectBytes` exclude `read-redirected`. No signature changes.

- [ ] **Step 1: Write the failing tests** (append to `internal/store/stats_test.go`)

```go
// The lifetime figure is what the sandbox measured; hook redirect estimates are not part of it.
func TestLifetimeIgnoresRedirectEvents(t *testing.T) {
	s := statsFixture(t) // has bash, webfetch and read redirect events
	snap, err := s.Stats(StatsQuery{ServerID: "srvA", CCPID: 100, Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(900 + 90 + 4000 + 8); snap.LifetimeKept != want {
		t.Fatalf("LifetimeKept = %d, want %d (stats_totals only)", snap.LifetimeKept, want)
	}
}

// A Read tip is not a redirect: the Read still runs.
func TestSessionRedirectsIgnoreReadTips(t *testing.T) {
	s := openTest(t)
	if err := s.SetSessionCC("s1", "/p", 100); err != nil {
		t.Fatal(err)
	}
	insertTestEvents(t, s, "s1", "/p",
		Event{Type: "bash-redirected", Category: "redirect", Priority: 2, Data: "a", BytesAvoided: 8192},
		Event{Type: "read-redirected", Category: "redirect", Priority: 2, Data: "b", BytesAvoided: 50000})
	snap, err := s.Stats(StatsQuery{CCPID: 100, Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Redirects != 1 || snap.RedirectBytes != 8192 {
		t.Fatalf("Redirects = %d, RedirectBytes = %d; want 1 and 8192", snap.Redirects, snap.RedirectBytes)
	}
}
```

Add to `internal/store/readonly_test.go`, in place of `TestLifetimeKeptUsesRedirectIndex` (delete that function):

```go
// The lifetime total runs on every statusline call; it reads stats_totals (one row per
// project) and never session_events.
func TestLifetimeKeptReadsOnlyStatsTotals(t *testing.T) {
	s := openTest(t)
	rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT " + lifetimeKept)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	got := strings.Join(plan, "\n")
	if !strings.Contains(got, "stats_totals") || strings.Contains(got, "session_events") || strings.Contains(got, "tool_stats") {
		t.Fatalf("plan:\n%s", got)
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/store -run 'TestLifetimeIgnoresRedirectEvents|TestSessionRedirectsIgnoreReadTips|TestLifetimeKeptReadsOnlyStatsTotals' -count=1`
Expected: FAIL: LifetimeKept 30,... (includes redirects), `Redirects = 2`, plan mentions `session_events`.

- [ ] **Step 3: Implement**

`internal/store/stats.go`: replace the `lifetimeKept` constant with

```go
// lifetimeKept is the all-projects kept-out total (stats spec §2 "Lifetime"):
// the stats_totals rows, one per project. Only measured sandbox bytes; the
// redirect estimates in session_events are not part of it.
const lifetimeKept = `(SELECT COALESCE(SUM(bytes_kept), 0) FROM stats_totals)`
```

and in `Stats`, the session redirect query becomes

```go
		if err := s.db.QueryRow(`SELECT count(*), COALESCE(SUM(bytes_avoided), 0) FROM session_events
WHERE session_id = ? AND category = 'redirect' AND type <> 'read-redirected'`, st.SessionID).Scan(&st.Redirects, &st.RedirectBytes); err != nil {
```

Update the comments on `StatsSnapshot.Redirects` (`excluding read-redirected tips`) and `LifetimeKept` (`every tool_stats bytes_kept (stats_totals)`).

Update existing expectations:
- `stats_test.go` `LifetimeKept: 900 + 90 + 4000 + 8 + 24576 + 1000` → `LifetimeKept: 900 + 90 + 4000 + 8`.
- `readonly_test.go` `TestSessionLine`: all three `900 + 90 + 4000 + 8 + 24576 + 1000` → `900 + 90 + 4000 + 8`, and the comment `lifetime is every kept byte plus every redirect` → `lifetime is every kept byte (stats_totals)`.
- `TestStatusLineQueriesDoNotScanGrowingTables` (`stats_test.go`): replace the final loop with

```go
	if !strings.Contains(life, "stats_totals") || strings.Contains(life, "session_events") {
		t.Errorf("lifetime query plan should read only stats_totals:\n%s", life)
	}
```

and fix its comment (the statusline lifetime is a sum over stats_totals, one row per project).

- [ ] **Step 4: Run to see them pass**

Run: `go test ./internal/store -count=1`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): lifetime is measured bytes only, Read tips are not redirects

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `stats.Format` labels the session redirect line as estimated

**Files:**
- Modify: `internal/stats/report.go` (the `Redirected by hooks` line and the `Report` field comments)
- Test: `internal/stats/report_test.go` (the three `Redirected by hooks` expectations)

**Interfaces:**
- Consumes: nothing from Task 1 (pure renderer).

- [ ] **Step 1: Write the failing test change.** In `report_test.go` change the three expected lines (about lines 44, 82, 137):

```go
		"  Redirected by hooks    4 calls  ·  ~40.0 KB (estimated)",
		"  Redirected by hooks    0 calls  ·  ~0 B (estimated)",
		"  Redirected by hooks    0 calls  ·  ~0 B (estimated)",
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/stats -count=1`
Expected: FAIL in `TestFormatFull` and the two other full-report tests (missing ` (estimated)`).

- [ ] **Step 3: Implement.** In `Format`:

```go
		fmt.Fprintf(&b, "  Redirected by hooks    %d calls  ·  ~%s (estimated)\n", r.Redirects, KB(r.RedirectBytes))
```

Update the field comments: `Redirects // PreToolUse redirects this session (Read tips excluded)`, `LifetimeKept // all tool_stats kept (measured bytes only)`.

- [ ] **Step 4: Run to see it pass**

Run: `go test ./internal/stats -count=1`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/stats
git commit -m "feat(stats): label hook redirect bytes as estimated

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: end to end, KNOWN-DIFFS, docs, before/after, verification

**Files:**
- Modify: `internal/mcpserver/ctxstats_test.go` (expected report), `internal/mcpserver/unmeasured_test.go` (add the pinning test), `testdata/golden/KNOWN-DIFFS.md`, `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md` (§2 Lifetime bullet, §3 text)

- [ ] **Step 1: Write the failing tests**

In `ctxstats_test.go`, the end-to-end expectation changes: the session line gets `(estimated)` and the lifetime is measured only (962 + 5000 = 5962 B):

```
  Redirected by hooks    1 calls  ·  ~8.0 KB (estimated)
...
All projects  ·  5.8 KB kept out  ·  1 sessions  ·  since 2026-10-20
```

Append to `internal/mcpserver/unmeasured_test.go`:

```go
func TestKnownDiffsListsLifetimeSplit(t *testing.T) {
	b, err := os.ReadFile("../../testdata/golden/KNOWN-DIFFS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "stats:lifetime-split") {
		t.Error("KNOWN-DIFFS.md has no stats:lifetime-split entry")
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/mcpserver -run 'TestCtxStatsReport|TestKnownDiffsListsLifetimeSplit' -count=1`
Expected: the pinning test fails; `TestCtxStatsReport` passes once Tasks 1 and 2 are in, so only the pinning test fails.

- [ ] **Step 3: Write the docs**

KNOWN-DIFFS entry after `stats:kept-not-measured`:

```markdown
- `stats:lifetime-split`: the `All projects` line of `ctx_stats` and the statusline `lifetime`
  show measured sandbox bytes only (`stats_totals`); hook-estimated `bytes_avoided` is no longer
  added to them. The session line `Redirected by hooks` excludes Read tips (the Read still runs)
  and ends with `(estimated)`. No golden case reads these lines, so none is skipped. Spec:
  `2026-10-22-cmgo-stats-measured-vs-estimated-design.md`.
```

Stats spec: §2 Lifetime bullet becomes `sums over stats_totals only (measured bytes); hook-estimated redirect bytes are shown for the session, not here` and the sample line `Redirected by hooks    <n> calls  ·  ~<kb> bytes_avoided` gets `(estimated)` and "Read tips excluded". §3 table: lifetime = measured.

- [ ] **Step 4: Verify everything.** Run each, read the output:

```bash
gofmt -l .                                                   # prints nothing
go vet ./...                                                 # exit 0
go test ./... -count=1                                       # 20 ok
go test -race ./... -count=1                                 # 20 ok
go list -deps ./cmd/cmgo-hook | grep -c internal/executor    # 0
```

Statusline regression check (bench exists for hook latency): `go build -o <scratch>/bin/ ./cmd/cmgo ./cmd/cmgo-hook` then `python3 -I bench/hook-latency.py <scratch>/bin/cmgo-hook 100` and its last phase runs `cmgo-hook statusline` against a generated DB; its p95 line goes in the PR body (budget p95 <= 10 ms; noisy when the load average is high, say so).

- [ ] **Step 5: Before and after on a DB copy** (scratch HOME/CMGO_DIR/CLAUDE_CONFIG_DIR; never the real home)

Copy `~/.claude/cmgo/cmgo.db` and its `-wal` into `<scratch>/ba/cmgodir/`. Build `main` into `<scratch>/ba/before/cmgo` (`git stash`-free: `git worktree add <scratch>/ba/wt main` or `git archive main`) and this branch into `<scratch>/ba/after/cmgo`. For each binary run the same MCP session (`initialize`, `ctx_stats`) with `CMGO_DIR=<scratch>/ba/cmgodir` on a fresh copy of the DB, and `<binary> hook statusline` with `{"session_id":"x"}` on stdin. Expected: `All projects` 14.66 MB before, about 2.73 MB after; statusline lifetime the same change. Save both outputs for the PR body.

- [ ] **Step 6: Commit**

```bash
git add testdata docs internal/mcpserver
git commit -m "docs: record the lifetime split in KNOWN-DIFFS and the stats spec

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

After Task 3: one fresh whole-branch reviewer on the most capable model (`review-package`), fix Critical/Important by TDD in one pass, then report to "context" with the evidence and wait for the owner's go before push and PR.

## Self-review

- Spec §1-§3 map to Task 1 (store), Task 2 (format), Task 3 (docs); §4 to Task 1 plan tests; §5 compatibility to Task 3 step 5; §6 to Task 3; §7 tests all present; §8 nothing to build.
- Names consistent: `lifetimeKept`, `TestLifetimeKeptReadsOnlyStatsTotals`, `TestKnownDiffsListsLifetimeSplit`.
- Statusline tests (`hookcmd`, `cmd/cmgo`) need no change: their fixtures hold no redirect events (checked).
- Execution method: native; three small tasks over three packages.
