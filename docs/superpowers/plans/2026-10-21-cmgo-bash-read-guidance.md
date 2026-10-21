# cmgo Bash read guidance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline, TDD, one commit per task). Steps use checkbox (`- [ ]`) syntax.

**Goal:** A PreToolUse context hint for Bash commands that `cat` / `nl` / `tac` / `bat` / `head` / `tail` files whose statted size reaches 16 KiB.

**Architecture:** A pure-stdlib tokenizer and estimator in `internal/routing/readguard.go`, called from `routeBash` before the length nudge; `Deps` gains `Cwd` and `Home`, filled by `hookcmd.preToolUse`. Guidance only: no `Redirect`, no event.

**Tech Stack:** Go standard library only in `cmgo-hook`'s dependency set; Python 3 for the bench.

**Spec:** `docs/superpowers/specs/2026-10-21-cmgo-bash-read-guidance-design.md` (rev 2). Read §1 before Task 1.

## Global Constraints

- `cmgo-hook` links no new dependency: `go list -deps ./cmd/cmgo-hook` gains no package (no MCP SDK, `net/http`, executor, fetch, mcpserver).
- Hook p95 ≤ 10 ms in `bench/hook-latency.py`, including the two new cases.
- Output is `Decision{Action: "context"}` with no `Redirect`; `routing.Events` returns nothing for it.
- Defaults: threshold 16384 bytes, 64 bytes per line, at most 8 stats per command, commands over 4096 bytes skipped.
- Tests never touch the real home: temp `HOME`, `CMGO_DIR`, project dirs.
- Commits in English, trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`; PR body carries evidence; no session URLs anywhere.
- Import grouping: stdlib, blank line, module imports.

## Review Focus

- `cat` of a file with a space or quote in its name (`cat "my file.go"`): statted correctly, not split.
- A relative `cat` after `cd` into a directory that does not exist: no hint, no panic.
- `cat` of a symlink to a large file: counted; of a symlink loop or a FIFO: not regular, 0.
- A 4 KiB command made of many `cat` words and operators: bounded work, at most 8 stats.
- `CMGO_BASH_READ_MIN_BYTES` set to junk, a negative number, `0`: junk and negative keep 16384, `0` is off.
- Fall-through: a command the guard ignores gets exactly today's decision (nudge, generic once guidance).

## File Structure

- Create `internal/routing/readguard.go`: tokenizer, estimator, `bashReadGuidance`.
- Create `internal/routing/readguard_test.go`: table tests (Task 1) and route tests (Task 2).
- Modify `internal/routing/route.go`: `Deps.Cwd`, `Deps.Home`, call in `routeBash`.
- Modify `internal/routing/text.go`: two guidance templates.
- Modify `internal/hookcmd/hookcmd.go`: fill `Cwd`, `Home`; test in `hookcmd_test.go`.
- Modify `testdata/golden/HOOKS-KNOWN-DIFFS.md`, `CLAUDE.md`, `bench/hook-latency.py`.
- Create `internal/routing/replay_test.go`: replay helper over a fixture (Task 4). Create `internal/routing/replay_audit_test.go` (`//go:build audit`): the replay of the real audit, which reads the real home.

---

### Task 1: tokenizer and estimator

**Files:** Create `internal/routing/readguard.go`, `internal/routing/readguard_test.go`.

**Interfaces:**
- Produces: `func estimateRead(command string, cwd, home string) (total int64, largest string)` returns the summed estimate in bytes and the typed name of the largest statted file ("" when none). It returns `0, ""` for every skipped form (spec §1.1).
- Produces: constants `readGuardMaxCommand = 4096`, `readGuardMaxStats = 8`, `readGuardBytesPerLine = 64`.

- [ ] **Step 1: Write the failing table test.** `TestEstimateRead` builds a temp dir `proj` with files `a.txt` (20000 bytes), `b.txt` (3000), `my file.txt` (20000), `sub/c.txt` (30000), a symlink `link` to `a.txt`, a directory `dir`, and calls `estimateRead(cmd, proj, home)` where `home` is another temp dir holding `h.txt` (18000). Rows (command, want total):

| Command | Want |
|---|---|
| `cat a.txt` | 20000 |
| `cat a.txt b.txt` | 23000 |
| `cat -n a.txt` | 20000 |
| `nl -ba a.txt` | 20000 |
| `cat "my file.txt"` | 20000 |
| `cat my\ file.txt` | 20000 |
| `cat link` | 20000 |
| `cat dir` | 0 |
| `cat missing.txt` | 0 |
| `cat` | 0 |
| `cd sub && cat c.txt` | 30000 |
| `cd sub; cat c.txt` | 30000 |
| `cd .. && cat a.txt` | 0 (parent has no a.txt) |
| `cd "$X" && cat a.txt` | 0 (base unknown) |
| `cd - && cat a.txt` | 0 |
| `cat ~/h.txt` | 18000 |
| `cat $F` | 0 |
| `cat *.txt` | 0 |
| `cat a.txt \| head -5` | 320 |
| `cat a.txt \| head -n 1000` | 20000 (min with size) |
| `cat a.txt \| wc -l` | 0 |
| `cat a.txt \| grep -c x` | 0 |
| `cat a.txt \| grep x` | 20000 |
| `cat a.txt > out.txt` | 0 |
| `cat a.txt >/dev/null` | 0 |
| `cat a.txt 2>/dev/null` | 20000 |
| `cat a.txt 2>&1` | 20000 |
| `cat > a.txt <<EOF` | 0 |
| `cat $(echo a.txt)` | 0 |
| `echo hi; cat a.txt` | 20000 |
| `cat a.txt; cat a.txt` | 40000 |
| `head -n 100 a.txt` | 6400 |
| `head -100 a.txt` | 6400 |
| `head -c 500 a.txt` | 500 |
| `head a.txt` | 640 |
| `tail -n 1000 a.txt` | 20000 |
| `tail -n +5 a.txt` | 20000 |
| `tail -f a.txt` | 0 |
| `sed -n '1,500p' a.txt` | 0 |
| `git diff` | 0 |
| `cat a.txt b.txt a.txt b.txt a.txt b.txt a.txt b.txt a.txt` (9 operands) | first 8 operands counted: `a.txt`×4 + `b.txt`×4 = 92000 |
| a command of 5000 bytes starting `cat a.txt` | 0 |

Also `TestEstimateReadLargest`: `cat a.txt sub/c.txt` returns largest `sub/c.txt`.

- [ ] **Step 2: Run to verify it fails.** `go test ./internal/routing -run 'TestEstimateRead' -count=1` → FAIL to build: `undefined: estimateRead`.
- [ ] **Step 3: Implement** `readguard.go`: a `tokenize(command) ([]chain, bool)` pass (spec §1.1) and `estimateRead` that walks chains tracking the cd base, sums class A estimates, applies pipeline limiters, counts stats (cap 8).
- [ ] **Step 4: Run** `go test ./internal/routing -count=1` → PASS; `go vet ./internal/routing`; `gofmt -l .` empty.
- [ ] **Step 5: Commit** `feat(routing): estimate how much a Bash command reads from files`.

### Task 2: routing integration, texts, switch

**Files:** Modify `internal/routing/route.go`, `internal/routing/text.go`, `internal/routing/readguard.go`; test in `internal/routing/readguard_test.go`.

**Interfaces:**
- Consumes: `estimateRead` (Task 1), `guidanceOnce(runDir, sid, typ) bool`, `jsParseInt`, `hookio.JSPrefix`, `named`.
- Produces: `Deps.Cwd`, `Deps.Home string`; `func bashReadGuidance(command string, d Deps) (string, bool)`; `func readGuardMin(getenv func(string) string) int64` (0 = off); templates `bashReadLong`, `bashReadShort` with `%s` slots filled by `fmt.Sprintf` after `named`.

- [ ] **Step 1: Write the failing tests** (`TestRouteBashReadGuidance`): with `d := testDeps(t)`, `d.Cwd = proj`, `d.MCPReady = func() bool { return true }`, a 20000-byte `big.txt` and a 3000-byte `small.txt` in `proj`:
  1. `Route("Bash", bash("cat big.txt"), false, d)` → Action `context`, `Redirect == nil`, text contains `about 20 KB (big.txt)`, `Use Read`, `Edit needs a prior Read`, `ctx_execute_file`, and not `sed`.
  2. Second identical call → `context` with the short text (`About 20 KB`).
  3. `cat small.txt` → today's decision (`context`, `bashGuidance`, first use) — see fall-through below.
  4. `d.MCPReady = func() bool { return false }` → `cat big.txt` gets only the generic path.
  5. Env: `CMGO_BASH_READ_MIN_BYTES=0` → no read hint for `cat big.txt`; `=junk` and `=-5` → hint (default 16384); `=30000` → no hint; `=1000` → hint for `cat small.txt`.
  6. `d.Policy` deny still wins: `cat big.txt && sudo ls` → deny (policy runs first).
  7. Fall-through equality: for `echo hi`, `git status`, `curl https://x` (modify/deny), `npm test` (generic), `Route` output with the guard equals the output without (compare with the same Deps copy; `cmdline` forms that the guard skips).
  8. `CMGO_BASH_NUDGE_MIN_COMMAND_BYTES=100` with `cat big.txt`: still hinted (the guard runs before the nudge).
  9. `routing.Events("Bash", dec)` for the hint is empty.
- [ ] **Step 2: Run** `go test ./internal/routing -run TestRouteBashReadGuidance -count=1` → FAIL (`d.Cwd undefined` build error, then behavior).
- [ ] **Step 3: Implement** `Deps` fields, `readGuardMin`, `bashReadGuidance`, the templates and the `routeBash` call after `isStructurallyBounded` and before `nudgeMinBytes`.
- [ ] **Step 4: Run** `go test ./internal/routing -count=1 -v | grep -c PASS` and the whole package → PASS, including `TestGoldenPreToolUse` and `TestGoldenStrings` unchanged.
- [ ] **Step 5: Commit** `feat(routing): guide Bash reads of large files to ctx_execute_file`.

### Task 3: hook wiring

**Files:** Modify `internal/hookcmd/hookcmd.go`, test in `internal/hookcmd/hookcmd_test.go`.

**Interfaces:** Consumes `routing.Deps{Cwd, Home}`. Produces nothing new.

- [ ] **Step 1: Write the failing test** `TestPreToolUseBashReadGuidance`: temp `HOME`, `CMGO_DIR`, project dir with a 20000-byte `big.txt`; touch the MCP-ready sentinel the way existing PreToolUse tests do; stdin `{"tool_name":"Bash","tool_input":{"command":"cat big.txt"},"cwd":"<proj>","session_id":"s1"}`; run the hook with the process cwd elsewhere. Expect stdout `additionalContext` containing `about 20 KB (big.txt)` and no `session_events` row of type `*-redirected`.
- [ ] **Step 2: Run** `go test ./internal/hookcmd -run TestPreToolUseBashReadGuidance -count=1` → FAIL (no hint printed: `Cwd` not passed).
- [ ] **Step 3: Implement** in `preToolUse`: `Cwd` from `h.in.String("cwd")`, else `h.getwd()`-style `os.Getwd()`; `Home: h.cfg.Home`.
- [ ] **Step 4: Run** `go test ./internal/hookcmd ./internal/routing -count=1` → PASS.
- [ ] **Step 5: Commit** `feat(hookcmd): pass the hook cwd and home to Bash read guidance`.

### Task 4: replay helper, known-diffs row, docs

**Files:** Create `internal/routing/replay_test.go`, `internal/routing/replay_audit_test.go`; modify `testdata/golden/HOOKS-KNOWN-DIFFS.md`, `CLAUDE.md`.

- [ ] **Step 1: Write the failing tests.**
  - `TestReplayFixture`: a fixture list of `{cwd, command, recorded int}` over temp files; the helper `replay(rows, thresholds)` returns per-threshold counts of flagged calls, recorded bytes flagged, over-estimates (flagged with recorded under 5 KiB) and skipped (no operand file exists). Assert exact numbers on a 6-row fixture (two flagged at 16 KiB, one skipped, one limiter-bounded, one `sed`, one heredoc).
  - `TestKnownDiffsRowExists`: reads `testdata/golden/HOOKS-KNOWN-DIFFS.md`, requires the row ID `routing:bash-read-guidance`, and requires that `Route` for `cat big.txt` differs from `Route` with `CMGO_BASH_READ_MIN_BYTES=0` (the extension is visible).
- [ ] **Step 2: Run** → FAIL (`replay undefined`; row missing).
- [ ] **Step 3: Implement** `replay` in the test file (it reuses `estimateRead`); add the row and the new ID-form paragraph to `HOOKS-KNOWN-DIFFS.md`; add `CMGO_BASH_READ_MIN_BYTES` to the config list in `CLAUDE.md`.
- [ ] **Step 4: Run** `go test ./internal/routing -count=1` → PASS.
- [ ] **Step 5: Run the replay on the real audit data.** `TestReplayAudit` lives in `replay_audit_test.go` behind the `audit` build tag, because it stats real files under the real home and tests must not: `CMGO_AUDIT_JSON=<audit>/transcripts-raw.json go test -tags audit -run TestReplayAudit -v ./internal/routing`. Check that it compiles with `go vet -tags audit ./internal/routing`. Record the table in the PR body: per threshold flagged, share, over-estimates, skipped. If over-estimates at 16 KiB exceed 10, change `readGuardBytesPerLine` or the default before Task 5.
- [ ] **Step 6: Commit** `docs: record the Bash read guidance difference and config switch`.

### Task 5: bench, evidence, PR

**Files:** Modify `bench/hook-latency.py`.

- [ ] **Step 1:** Add the cases `pretooluse bash large cat` (`cat big.txt` over a 100 KB file written in the temp project) and `pretooluse bash compound` (`cd <project> && cat f1 ... f8`, 8 files of 4 KB each), both with `CMGO_PROJECT_DIR` as the bench already sets, `cwd` in the payload, and a check on the first run that stdout contains `<context_guidance>` (large cat) and `about 32 KB` (compound).
- [ ] **Step 2: Run** `go build -o bin/ ./cmd/cmgo-hook && python3 -I bench/hook-latency.py bin/cmgo-hook` at a quiet load average; expected PASS for all cases, p95 ≤ 10 ms. If a case is over the limit under load, rerun when the load average is under 3 and report both.
- [ ] **Step 3: Verify** `go vet ./...`, `gofmt -l .` (empty), `go test ./... -count=1`, `go test -race ./... -count=1`, `go list -deps ./cmd/cmgo-hook | grep -E 'modelcontextprotocol|net/http|internal/(executor|fetch|mcpserver)'` (empty).
- [ ] **Step 4: Commit** `test(bench): hook latency cases for Bash read guidance`.
- [ ] **Step 5: Push the branch and open the PR** against `main` with the evidence from Step 3 and Task 4 Step 5, the rollback rule from spec §5.3 and no session URL. Do not merge. Report the PR link to session `context`.

## Self-Review

- Spec coverage: §1.1 parsing → Task 1; §1.2-1.4 estimate, threshold, switch, gating → Tasks 1-2; §1.5 texts → Task 2; §2 layout → Tasks 2-3; §3 known-diffs row → Task 4; §5.1 replay → Task 4; §5.3 rollback → PR body (Task 5); §6.3 bench → Task 5. `sed`/`git` negatives are rows in the Task 1 table and the Task 2 fall-through test.
- Type consistency: `estimateRead(command, cwd, home)` is the only entry used by `bashReadGuidance` and by `replay`.
