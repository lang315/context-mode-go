# cmgo Bash read guidance: design

Status: approved by the owner with five changes (rev 2, this file). Implementation follows `docs/superpowers/plans/2026-10-21-cmgo-bash-read-guidance.md`.

## 0. Goal, evidence, success criteria

**Goal.** When a Bash command is about to print a large file into the context, add a short `additionalContext` (never a block) that points the model at `ctx_execute_file` / `ctx_batch_execute`, the way the Read guidance does.

**Evidence** (peer audit of transcripts since 2026-09-30; scripts and output in the `context-mode` scratchpad `audit/` dir):
- Bash, Read, Grep and WebFetch results over 5 KB went straight into context 5,947 times, 67.3 MB. Bash alone: 4,050 results, 37.9 MB, out of 41,187 Bash calls.
- Bash results over 5 KB by leading word: `cd ... &&` 17.8 MB, `sed` 5.0 MB, `cat` 4.8 MB, `git` 3.2 MB, `grep` 1.7 MB.
- I re-classified the 4,050 large Bash commands after stripping `cd X &&` (commands are cut at 200 characters in the audit, so this is approximate):

| Class | Calls | Bytes | Share |
|---|---|---|---|
| `cat` / `nl` of files | 1,146 | 13.4 MB | 35.2% |
| `sed` (almost all `-n` ranges) | 1,456 | 11.8 MB | 31.0% |
| `git diff` / `show` / `log` | 353 | 3.6 MB | 9.5% |
| `head` / `tail` | 34 | 0.3 MB | 0.7% |
| same readers, but a later pipe stage limits the output | 194 | 1.9 MB | 4.9% |
| heredoc, `$(...)`, `for` loops | 172 | 1.4 MB | 3.8% |
| other (`grep`, `gh`, `python3`, ...) | 695 | 5.7 MB | 15.0% |

- The audit is almost all TS-plugin era (30 of 32 sessions with ctx tools used the plugin, 1 used cmgo). It is a baseline for the problem, not for cmgo's effect.
- **Bash output is capped.** The largest Bash result in the audit is 29.1 KB: Claude Code truncates Bash output at about 30,000 characters. No call can put more than that into the context, which matters for the threshold (§1.4).

**What v1 covers, and what it leaves out.** v1 is class A only: `cat` / `nl` / `tac` / `bat` and `head` / `tail` on files whose size can be statted. Left out, with the reason:
- `sed` ranges (31% of the bytes). They are mostly the model reading code to understand or edit it, which is legitimate, and a numeric range is already the bounded read the hint would ask for (§1.6 has the replay numbers).
- `git diff` / `show` / `log` (9.5%). No size can be checked without running git, it fires on one-line diffs, and the generic once-per-session `bashGuidance` already exists.
- `grep`, `find`, `awk`, `jq`, loops, globs, `$VAR` paths: see §7.

**Success criteria.** A hint changes only *later* calls: the output of the call that fired it still enters the context, and legitimate code reading stays. The targets are therefore modest, and §5.3 says what result means the feature is not worth keeping.
1. **Effect (M1).** Class A results of at least 16 KiB drop by at least 15% per 100 Bash calls against the audit baseline of 228 in 41,187 calls (0.55 per 100). The replay (§5.1) says class A at 16 KiB can address about 6% of the large-Bash bytes among replayable calls (11% if the skipped ones behave alike), so a larger target is not credible.
2. **Follow rate (M2).** At least 25% of fires are followed within 3 actions by `ctx_execute_file`, `ctx_batch_execute` or `ctx_execute` (the audit measured 54% after a real redirect; a hint is weaker than a block).
3. **Precision (M3).** In a manual sample of 30 fires, at most 30% are false positives (§4).
4. **Latency.** `bench/hook-latency.py`: p95 ≤ 10 ms for the new cases, no regression of the existing ones. `cmgo-hook` links no new dependency.

## 1. Behavior

Where it sits in `routeBash` (`internal/routing/route.go`): after the policy check and the curl / inline-HTTP / build-tool redirects (unchanged), after `isStructurallyBounded`, and *before* the `CMGO_BASH_NUDGE_MIN_COMMAND_BYTES` check. The nudge threshold is about command length; this guidance is about output size, so a short `cat big.log` must still be guided. When the new check finds nothing, the existing flow runs as today (nudge, then the generic once-per-session `bashGuidance`).

The result is `Decision{Action: "context", AdditionalContext: ...}` with **no** `Redirect`: nothing was avoided, so no `bytes_avoided` is recorded and no `*-redirected` event is written.

### 1.1 Parsing (bounded, no new dependency)

One pass over the command, cost O(len), using only `strings`, `os` and `path/filepath`.

- **Skip entirely** (fall through to today's flow) when the command is longer than 4096 bytes, or contains `<<` (heredoc), `$(`, a backtick, or `(` outside quotes. Those are scripts, not reads.
- **Split** at top level (outside single and double quotes, honoring `\`) into *chains* at `&&`, `||`, `;`, `&` and newline, and each chain into *stages* at `|`.
- **Words:** quotes are removed. A word with an unquoted `$`, `*`, `?`, `[`, `{` or `!` is *unknown*: it is never statted. `~` and `~/x` expand to `Deps.Home`.
- **Redirects:** `2>&1`, `>/dev/null`, `2>file` and the like are ignored. A redirect of stdout to anything else (`> f`, `>> f`, `1> f`) means the chain's output does not reach the context: its estimate is 0.
- **Working dir.** The base is `Deps.Cwd`: the hook input's `cwd` field, else `os.Getwd()` (what `routeRead` relies on today). A chain `cd P` changes it for the later chains: `P` absolute, `~`-relative or relative to the current base. `cd -`, `cd` with an unknown word, `pushd` and `popd` make the base *unknown*: later relative paths are not statted, absolute ones still are.
- **Pipelines:** the estimate comes from the first stage. Later stages can only lower it, through *limiters*:
  - `head` / `tail -n N` or `-N` (default 10 lines): at most `N × 64` bytes; `-c N`: `N` bytes;
  - `wc`, `grep -c|-q|-l|-L`: 0; `grep -m N`: `N × 64`.
  - Other stages (`grep`, `awk`, `jq`, `cut`, `sed`) are filters: not limiters, estimate unchanged.

### 1.2 The estimate (class A)

| Command | Estimate |
|---|---|
| `cat`, `nl`, `tac`, `bat` `[opts] F...` | sum of `size(F)` |
| `head`, `tail` `[-n N \| -N \| -c N] F...` | `min(sum size, N × 64)`; N defaults to 10; `tail -f` / `-F` is skipped |

- **Files:** only non-option words that `os.Stat` (following symlinks) reports as regular files. At most **8 stats per command**; directories, missing files and unknown words count 0. A command with no file operand (stdin) counts 0.
- **64 bytes per line** is an upper-side average for source and Markdown. It is a named constant, checked by the replay (§5.1).
- The command estimate is the sum over all chains. **It fires when the sum is at least the threshold.**

### 1.3 Why a stat estimate

The estimate uses the real file size, so it is exact for `cat` and an upper bound for `head` / `tail`. The replay (§5.1) found no fired call whose recorded result was under 5 KB.

### 1.4 Threshold and switches

- Default threshold **16,384 bytes** (16 KiB) of estimated output, about half of the Bash output cap of about 30,000 characters (§0). A call that prints more than half of what Bash can ever return is the one worth a hint.
- **Why stricter than Read.** `routeRead` gives guidance above `readRedirectMin = 50_000` bytes (`route.go:38`). That bar cannot be reused here, for three reasons:
  1. It is a *file size* bar; the cap makes a Bash output of 50 KB impossible. In the replay only 2 of 681 `cat` / `nl` calls reach an estimate of 50,000, against 132 at 16 KiB.
  2. Read's high bar protects the read-then-Edit flow. Edit needs a prior Read, not `cat`, so `cat` is never that prerequisite.
  3. A `cat` that prints half the cap is already one of the largest single payloads a Bash call can add.
  The resulting inconsistency is real and accepted: a Read of a 49 KB file gets no extra hint, a `cat` of a 17 KB file does. Lowering `readRedirectMin` would break TS parity (`routing.mjs:853`) and the golden files, so it is out of scope.
- `CMGO_BASH_READ_MIN_BYTES`: integer 1-100,000,000 sets the threshold; unset or invalid keeps 16384; `0` turns the feature off. This is the kill switch.
- The hint needs a live MCP server: it is skipped when `d.MCPReady()` is false (one extra stat, only once a candidate exists). Subagents are not excluded: they get the routing block through the Agent prompt (spec §2.1.6).

### 1.5 Text

First fire in a session (marker `bashread`), `$T` = the `mcp__cmgo__` prefix as in `text.go`:

```
<context_guidance>
  <tip>
    This command prints about 38 KB (src/db.ts) straight into your conversation. Reading to Edit the file? Use Read: Edit needs a prior Read, not cat. Reading to analyze, summarize, search or extract from it? Use $Tctx_execute_file(path, language, code) for one file, or $Tctx_batch_execute(commands, queries) for several files or commands: the bytes stay in the sandbox and only what your code prints enters your conversation. To read only part of it, print just that part with head or tail.
  </tip>
</context_guidance>
```

Later fires in the same session (the marker already exists; no new marker):

```
<context_guidance>
  <tip>
    About 38 KB of output (src/db.ts). To edit it use Read; to analyze it use $Tctx_execute_file or $Tctx_batch_execute; or print only the part you need.
  </tip>
</context_guidance>
```

The size is the command estimate in KB (rounded up); the name is the largest statted file, cut to 120 UTF-16 units with `hookio.JSPrefix`.

### 1.6 Why `sed` ranges are not in v1 (replay)

The prototype replay (§5.1) over the audit's `sed` calls (1,529 calls, 12.5 MB, without heredocs):
- Recorded results of at least 8 KiB: 559 calls, 52% of the class bytes. At least 16 KiB: 77 calls, 12% (1.5 MB, 4% of all large-Bash bytes). None reaches 30,000.
- So the sed bytes sit in 8-16 KiB results, mostly several small ranges added up (`sed -n 40,120p f; sed -n 295,310p f`). Firing on them at the class threshold would reach about 77 calls in three weeks, nearly all of them the model reading code in ranges it picked on purpose.
- A numeric range is the bounded read the hint would recommend. The old text told the model to "bound it with a sed range" when the command already was one.

Option "fire above a much higher bar (32 KB)" is also rejected: no sed result in the audit is over 30,000, so such a bar never fires. `sed` is therefore dropped from v1 and listed under revisit (§7); the post-ship audit (§5.2) keeps counting `sed` results so the question can be reopened with cmgo-era data.

## 2. Code layout

- `internal/routing/readguard.go`: `bashReadGuidance(command string, d Deps) (text string, ok bool)`, the tokenizer and the estimator. Pure standard library; it is reached from `cmgo-hook` and adds no import to it.
- `internal/routing/route.go`: `Deps` gains `Cwd` and `Home`; `routeBash` calls it as in §1.
- `internal/routing/text.go`: the two texts (`named()` applies `$T`).
- `internal/hookcmd/hookcmd.go`: `preToolUse` fills `Cwd` (input `cwd`, else `os.Getwd()`) and `Home` (`h.cfg.Home`).
- `CLAUDE.md`: add `CMGO_BASH_READ_MIN_BYTES` to the config switches.
- `testdata/golden/HOOKS-KNOWN-DIFFS.md`: one row (§3).
- `bench/hook-latency.py`: two cases (§6.3).

## 3. Deliberate difference from TS

TS has no output-size guidance for Bash: it only has the generic once-per-session `bashGuidance` and the command-length nudge. Record in `HOOKS-KNOWN-DIFFS.md`:

| Case ID | File | Reason |
|---|---|---|
| `routing:bash-read-guidance` | (no TS case) | A Bash command that reads files of at least `CMGO_BASH_READ_MIN_BYTES` (16 KiB) with `cat`, `nl`, `head` or `tail` gets a context hint pointing at `ctx_execute_file` / `ctx_batch_execute` (and at Read for editing). TS only gives the generic Bash tip. |

The ID form `routing:<name>` is new: it marks an *extension* with no golden case to differ from. The header text of that file (the "Case ID forms" paragraph) gets one more form, and the guard that "a listed difference must exist" is a unit test in `internal/routing/route_test.go` (§6.1) instead of a golden case. The current `hooks-pretooluse.json` has no `cat` / `head` / `tail` case on an existing file, so `TestGoldenPreToolUse` is unchanged; the plan's first step checks this.

## 4. False positives and negatives

| Case | Behavior | Why |
|---|---|---|
| `cat > f <<EOF`, `cat a > b` | no hint | heredoc skip; stdout redirected to a file |
| `sed -n '1,200p' f`, any `sed` | no hint | not in v1 (§1.6) |
| `tail -f log` | no hint | streaming |
| `cat big.log \| head -5`, `\| wc -l`, `\| grep -c x` | no hint | limiter |
| `cat big.json \| jq '.a'` | hint | filter, not a limiter (false positive if `jq` shrinks it) |
| `cat small1 small2` totalling under 16 KiB | no hint | threshold |
| `cat $F`, `cat dir/*.go`, `for f in ...; do cat $f; done` | no hint (known false negative) | unknown words are never statted; loops are skipped |
| `cd /repo && cat -n src/db.ts` | hint | `cd` tracked |
| `cd "$DIR" && cat f` | no hint for relative `f` | base unknown |
| a spilled tool result under `tool-results/` or the scratchpad | hint | the audit's spilled-result reads |
| `cat file` to understand code | hint (false positive when the model needs it in full) | main residual risk; the text offers Read for that; at 16 KiB and above such reads are a minority |
| `cat file` right before an Edit | hint, text says "use Read to Edit" | Edit needs a prior *Read*, not `cat` |

Residual risks:
- A network or slow filesystem could make `os.Stat` slow. `routeRead` already stats one path without a timeout; this adds at most 8, and only for paths the parser kept.
- If the model ignores the hint, each later fire costs about 200 bytes (short text). Over a session that is far below one avoided 16 KiB read.

## 5. Measuring the effect, and when to remove it

### 5.1 Before shipping: replay on real files

The audit stores each Bash command (cut at 200 characters), its result size and the session's project dir. The replay runs the commands through the **real classifier** (a Go test helper in the plan, not Python) and stats the **real files where they still exist**:
- Commands whose operand files no longer exist, are relative to an unknown cwd, or contain unknown words are **skipped**, and the report says how many (count and share of calls and of bytes). They are not guessed from the recorded result size: that would make the check circular for any command with several operands.
- Each replayed call is compared with its recorded result size: flagged calls whose recorded size is under 5 KB are over-estimates.
- The report is per threshold (8, 12, 16, 20, 30 KiB): calls flagged, recorded bytes flagged, share of large-Bash bytes, over-estimates, skipped.

Prototype run (Python, throwaway, same rules, 2026-10-21): 1,367 `cat` / `nl` calls without heredocs; **600 skipped** (44%: no operand file found); 86 more excluded by a pipe limiter; **681 replayed**.

| Threshold | Flagged calls | Recorded bytes flagged | Share of large-Bash bytes | Flagged with recorded < 5 KB |
|---|---|---|---|---|
| 8 KiB | 414 | 5.6 MB | 14.8% | 0 |
| 12 KiB | 256 | 3.9 MB | 10.4% | 0 |
| **16 KiB** | **132** | **2.3 MB** | **6.0%** | **0** |
| 20 KiB | 78 | 1.4 MB | 3.8% | 0 |
| 30 KiB | 12 | 0.2 MB | 0.5% | 0 |

The stat estimate has no false over-estimate at any threshold, so the choice of threshold is about intent (§4), not accuracy. The replay in the plan repeats this with the real classifier and confirms the 16 KiB default and the 64 bytes per line; if it shows over 10 over-estimates at 16 KiB, the constant moves before the implementation is merged.

### 5.2 After shipping: re-run the audit on cmgo-era sessions

At least 10 sessions or two weeks of cmgo use, whichever is later (today there is one cmgo session in the audit). The cmgo era is marked by the PreToolUse hook output containing `mcp__cmgo__`. Compare with §0's baseline:
- **M1:** class A results of at least 16 KiB per 100 Bash calls (baseline 0.55 per 100). `sed` results of at least 16 KiB are counted too, for the revisit.
- **M2:** follow rate: a fired guidance followed within 3 actions by `ctx_execute_file`, `ctx_batch_execute` or `ctx_execute`. Fires are the Bash PreToolUse results containing the new tip text.
- **M3:** false-positive review of 30 random fires; a fire is a false positive when the output was needed in full (the file is Edited within 3 actions, or the model repeats the command with `| head`, or reads the same file with Read).
- **M4:** `bench/hook-latency.py` output.
- **M5 (net context):** `follow rate × mean estimate` minus `fires × hint bytes`, so the hint is paid for.

### 5.3 Rollback rule

Judge after at least 40 fires:
- M2 under 10%: the text is wrong; change the text once, re-measure after 20 more fires.
- M3 over 30%: raise the default threshold to 32 KiB once, re-measure after 20 more fires.
- If after that step M2 is still under 10%, or M3 still over 30%, or M5 is negative, or M1 shows no drop of at least 5%: **remove the feature** (delete `readguard.go` and its call; keep the `Cwd` / `Home` fields only if something else uses them), and drop the HOOKS-KNOWN-DIFFS row.
- Any bench regression of 1 ms or more in p95 against the same machine's baseline: set the default off until fixed.
- Until then, `CMGO_BASH_READ_MIN_BYTES=0` is the immediate off switch.

## 6. Test plan

### 6.1 Unit (`internal/routing`, real temp files like `routeRead`'s tests)
- Table test of the parser and estimator: each row of §1.2; quoting with spaces; unknown words; the redirect forms; limiters; heredoc, `$(`, backtick and over-long commands skip; more than 8 operands; directory, missing and symlinked files; `sed` and `git` commands never fire.
- `cd` tracking: absolute, relative, `~`, `cd -`, `cd "$X"`, two successive `cd`; relative file resolved against `Deps.Cwd` when the input has `cwd`.
- Threshold: just below, equal and above; `CMGO_BASH_READ_MIN_BYTES` unset, invalid, `0`, a valid value.
- Once rules: first fire long text, second short text; `MCPReady` false gives no decision from this path; policy deny and the curl / inline / build redirects still win; `isStructurallyBounded` commands (`git status`, `ls`) get nothing from this path.
- Text: the long text contains the Read-for-Edit sentence and no sed advice.
- Fall-through: when nothing matches, the decision equals today's (nudge, generic once guidance).
- The `routing:bash-read-guidance` row exists and the extension is visible as a decision change (the "difference must not vanish" guard).

### 6.2 Integration
- `internal/hookcmd`: a `pretooluse` fire with `cwd` in the input and a relative `cat` of a 20 KB temp file prints the tip; no event is written (`routing.Events` returns none). Tests use temp `HOME`, `CMGO_DIR` and project dirs, never the real ones.
- `TestGoldenPreToolUse` and `TestGoldenStrings` stay green without changing any golden file.
- The replay helper (§5.1) is a test over a small fixture of commands and files, not over the real audit data.

### 6.3 Bench (`bench/hook-latency.py`, p95 ≤ 10 ms, same method as the other cases)
- `pretooluse bash large cat`: `cat big.txt` over a 100 KB file in the temp project.
- `pretooluse bash compound`: `cd <project> && cat a b c d e f g h` with 8 stat targets.

Expected cost: a tokenizer pass of a few µs and at most 8 stats (tens of µs warm). The bench confirms it; it is not measured yet.

## 7. Decisions taken by default

1. Threshold 16 KiB, 64 bytes per line; confirmed by the replay (§5.1).
2. Guidance only, no `Redirect`, no event, no stats entry: measurement comes from transcripts, not from the DB.
3. Long text once per session, short text afterwards.
4. Gated on `MCPReady`, not on `subagent`.
5. `CMGO_BASH_READ_MIN_BYTES=0` is the off switch; there is no per-class switch.
6. **Out of scope for v1, revisit after the post-ship audit:** `sed` ranges (§1.6), `git diff` / `show` / `log` (no size, once-per-session heuristic; the generic Bash tip covers it), `grep -r`, `find`, `ls -R`, `awk`, `jq`, `python3 file`, loops, globs, `$VAR` paths. Each is a size guess or needs real expansion.

## 8. Post-compact recall (investigation, no code)

**Question.** After `SessionStart` with `source=compact` (and on `resume`), would a `ctx_search` nudge help? The audit: after 149 compactions the model called `ctx_search` within its next 4 actions 3 times.

**What cmgo injects today** (`hookcmd.sessionResume`, `memory.Directive`, `memory.AutoInjection`):
- The routing block (every source).
- `<session_knowledge source="compact|continue">` with `<session_guide>`: Last Request (300 chars), Pending Tasks, Key Decisions, Files Modified, Unresolved Errors, Git, Project Rules, Subagent Tasks, Skills Used, Environment, Session Intent, User Role, Plan Mode.
- A `<session_search>` block that already says, with the exact call: `Use mcp__cmgo__ctx_search(queries: [...], source: "session-events") when you need specifics. Do NOT call ctx_index() — data is already indexed.`
- On compact only: `<continue_from>` when there is a last request, and `<session_state source="compaction">` (role, last 5 decisions, skills, intent) under a 500-token budget.
- The `session-events` source is answered from the events table by `ctx_search`, so the sentence is true.
So a ctx_search nudge exists at SessionStart already, with the tool name and the source. Resume gets the same directive without `<continue_from>` and without the auto-injection.

**What the model does instead** (audit, 149 compactions, my re-read of `transcripts-raw.json`):
- First action after a compact: Bash 83, Skill 26, ToolSearch 13, ListAgents 4, Read 3, AskUserQuestion 3.
- Of the 83 Bash first actions: `git` status / log / fetch / switch 36; other (`gh run view`, `gh pr view`, `ioreg`, `system_profiler`) 24; `cat` / `tail` of a plan, ledger or spilled result 10; `grep` 7; `ls` / `find` / `wc` 6.
- 84 of 149 compactions have a re-gathering action (Read, Grep, Glob, or a Bash read) in the next 4 actions; 5 have any `ctx_*` tool.
- These are live-state checks (branch, CI, PR state, the plan or ledger file as it is now). Session events record what happened, not what is true now, so `ctx_search` cannot replace them.
- 127 of the 149 compactions are in sessions that used the TS plugin; the TS directive has the same `<session_search>` text. The audit says little about cmgo.

**Finding.** 3 of 149 is not a failure metric: its numerator needs "the model lacked a specific that the events hold", and that is never counted. What is counted points the other way: the first actions verify live state, and the directive already carries both the summary and the exact `ctx_search` call. An extra nudge would be repeated text at the moment the context is smallest and most expensive to fill (post-compact average 43,295 tokens).

**Recommendation: do nothing now.** No text change.

**Revisit if** cmgo-era data (at least 20 compactions) shows that within 4 actions after a compact the model re-reads a file listed under *Files Modified*, or re-runs a command whose result is in the events (for example `git log` for commits made this session), and does not call `ctx_search`, in more than 30% of compactions. Then the candidate change is one sentence appended inside `<session_search>`, no new block:

```
Before re-reading a file or re-running a command to recover what you did earlier, query mcp__cmgo__ctx_search(queries: [...], source: "session-events") first.
```

A cheap way to get that data without code: the same audit scripts, restricted to the cmgo era, with two extra counters (Read of a Files Modified path; Bash `git log|diff` within 4 actions of a compact).
