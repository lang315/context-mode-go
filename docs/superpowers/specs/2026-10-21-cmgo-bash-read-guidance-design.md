# cmgo Bash read guidance: design

Status: draft for owner review. No production code is written until the owner approves this spec.

## 0. Goal, evidence, success criteria

**Goal.** When a Bash command is about to print a large file or a large dump into the context, add a short `additionalContext` (never a block) that points the model at `ctx_execute_file` / `ctx_batch_execute`, the way the Read guidance does.

**Evidence** (peer audit of transcripts since 2026-09-30; scripts and output in the `context-mode` scratchpad `audit/` dir):
- Bash, Read, Grep and WebFetch results over 5 KB went straight into context 5,947 times, 67.3 MB. Bash alone: 4,050 results, 37.9 MB, out of 41,187 Bash calls (9.8% of calls).
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

  So the classes this spec targets hold about 76% of the large-Bash bytes. The audit's `grep` bytes sit mostly in "other" and are out of scope (see §6).
- Typical large commands are *compounds*: `sed -n 40,120p f; sed -n 295,310p f` (many small ranges add up), `cat a b c`, `cd X && cat -n a b`. The estimate must therefore sum over the whole command, not look at one word.
- The audit is almost all TS-plugin era (30 of 32 sessions with ctx tools used the plugin, 1 used cmgo). It is a baseline for the problem, not for cmgo's effect.

**Success criteria** (measured as in §5):
1. On cmgo-era sessions, large (over 5 KB) Bash results of the targeted classes drop at least 30% per 100 Bash calls against the audit baseline.
2. At least 25% of fired guidances are followed within 3 actions by `ctx_execute_file`, `ctx_batch_execute` or `ctx_execute` (the audit measured 54% after a real redirect; a hint is weaker than a block).
3. In a manual sample of 30 fires, at most 20% are false positives (§4).
4. `bench/hook-latency.py`: p95 ≤ 10 ms for the new cases and no regression of the existing ones. `cmgo-hook` links no new dependency.

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
  - Other stages (`grep`, `awk`, `jq`, `cut`, `sed` without `-n`) are filters: not limiters, estimate unchanged.

### 1.2 Class A: file readers (exact stat)

| Command | Estimate |
|---|---|
| `cat`, `nl`, `tac`, `bat` `[opts] F...` | sum of `size(F)` |
| `head`, `tail` `[-n N \| -N \| -c N] F...` | `min(sum size, N × 64)`; N defaults to 10; `tail -f` / `-F` is skipped |
| `sed -n 'A,Bp' F...` (several `-e` or `;`-separated prints allowed) | `min(sum size, (B-A+1) × 64)` per print; `Np` counts as 1 line |
| `sed [-n] 'other script' F...`, `sed -n '/re/,/re/p'` | `sum size(F)` (unknown range); `sed -i` is skipped |

- **Files:** only non-option words that `os.Stat` (following symlinks) reports as regular files. At most **8 stats per command**; directories, missing files and unknown words count 0. A command with no file operand (stdin) counts 0.
- **64 bytes per line** is an upper-side average for source and Markdown. It is a named constant, tuned by the replay (§5.1).
- The command estimate is the sum over all chains. **It fires when the sum is at least the threshold.**

### 1.3 Class B: unbounded git dumps (no stat possible)

`git [global opts] diff|show|log ...` where global opts are `-C <dir>`, `-c k=v`, `--no-pager`, `-P`. It counts as a dump unless the arguments contain one of:
- `--stat`, `--shortstat`, `--numstat`, `--name-only`, `--name-status`, `--summary`, `--check`, `-s`, `--no-patch`, `--quiet`;
- a count: `-n N`, `-N`, `--max-count=N`, `-<digits>`;
- or a later pipe stage is a limiter (§1.1).

There is no size to check without running git, so class B is *heuristic*: it fires **once per session** (marker `bashgit`) and does not use the threshold.

### 1.4 Threshold and switches

- Default threshold **8,192 bytes** of estimated output. Below that the hint costs about as much context as the read would. The audit's size buckets: 5-10 KB 3,710 results, 10-20 KB 1,746, 20-50 KB 644, over 50 KB 20 (all tools); starting at 8 KB skips the cheapest part of the lowest bucket, and the replay (§5.1) shows whether to move it.
- `CMGO_BASH_READ_MIN_BYTES`: integer 1-100,000,000 sets the threshold; unset or invalid keeps 8192; `0` turns class A and B both off. This is the kill switch.
- The hint needs a live MCP server: it is skipped when `d.MCPReady()` is false (one extra stat, only once a candidate exists). Subagents are not excluded: they get the routing block through the Agent prompt (spec §2.1.6).

### 1.5 Texts

Class A, first fire in a session (marker `bashread`), `$T` = the `mcp__cmgo__` prefix as in `text.go`:

```
<context_guidance>
  <tip>
    This command prints about 38 KB (src/db.ts) straight into your conversation. If you only need to analyze, summarize, search or extract from it, use $Tctx_execute_file(path, language, code) for one file, or $Tctx_batch_execute(commands, queries) for several files or commands: the bytes stay in the sandbox and only what your code prints enters your conversation. Keep Bash for output you must read in full; bound it with head, tail or a sed range.
  </tip>
</context_guidance>
```

Class A, later fires in the same session (the marker already exists; no new marker):

```
<context_guidance>
  <tip>
    About 38 KB of output (src/db.ts). To analyze it use $Tctx_execute_file or $Tctx_batch_execute, or bound it with head or a sed range.
  </tip>
</context_guidance>
```

Class B (once per session):

```
<context_guidance>
  <tip>
    git diff, show and log can print far more than you need. To analyze the output (not just glance at a short result), run it through $Tctx_execute(language: "shell", code: "git diff ... | ...") or $Tctx_batch_execute(commands, queries): only what you print enters your conversation. Or bound it: --stat, --name-only, -n N, a path.
  </tip>
</context_guidance>
```

The size is the command estimate in KB (rounded up); the name is the largest statted file, cut to 120 UTF-16 units with `hookio.JSPrefix`.

## 2. Code layout

- `internal/routing/readguard.go`: `bashReadGuidance(command string, d Deps) (text string, ok bool)`, the tokenizer and the estimator. Pure standard library; it is reached from `cmgo-hook` and adds no import to it.
- `internal/routing/route.go`: `Deps` gains `Cwd` and `Home`; `routeBash` calls it as in §1.
- `internal/routing/text.go`: the three texts (`named()` applies `$T`).
- `internal/hookcmd/hookcmd.go`: `preToolUse` fills `Cwd` (input `cwd`, else `os.Getwd()`) and `Home` (`h.cfg.Home`).
- `CLAUDE.md`: add `CMGO_BASH_READ_MIN_BYTES` to the config switches.
- `testdata/golden/HOOKS-KNOWN-DIFFS.md`: one row (§3).
- `bench/hook-latency.py`: two cases (§6).

## 3. Deliberate difference from TS

TS has no output-size guidance for Bash: it only has the generic once-per-session `bashGuidance` and the command-length nudge. Record in `HOOKS-KNOWN-DIFFS.md`:

| Case ID | File | Reason |
|---|---|---|
| `routing:bash-read-guidance` | (no TS case) | A Bash command that reads a file or a dump of at least `CMGO_BASH_READ_MIN_BYTES` (8 KiB) gets a context hint pointing at `ctx_execute_file` / `ctx_batch_execute`. TS only gives the generic Bash tip. |

The ID form `routing:<name>` is new: it marks an *extension* with no golden case to differ from. The header text of that file (the "Case ID forms" paragraph) gets one more form, and the guard that "a listed difference must exist" is a unit test in `internal/routing/route_test.go` (§4.1) instead of a golden case. The current `hooks-pretooluse.json` has no `cat` / `sed` / `head` / `tail` / `git diff|show|log` case on an existing file, so `TestGoldenPreToolUse` is unchanged; this is checked in the plan's first step.

## 4. False positives and negatives

| Case | Behavior | Why |
|---|---|---|
| `cat > f <<EOF`, `cat a > b` | no hint | heredoc skip; stdout redirected to a file |
| `sed -n '1,20p' huge.log` | no hint | bounded by the range (1,280 bytes) |
| `tail -f log`, `sed -i ... f` | no hint | streaming; write |
| `cat big.log \| head -5`, `\| wc -l`, `\| grep -c x` | no hint | limiter |
| `cat big.json \| jq '.a'` | hint | filter, not a limiter (false positive if `jq` shrinks it) |
| `cat small1 small2` totalling under 8 KiB | no hint | threshold |
| `cat $F`, `cat dir/*.go`, `for f in ...; do cat $f; done` | no hint (known false negative) | unknown words are never statted; loops are skipped |
| `cd /repo && cat -n src/db.ts` | hint | `cd` tracked |
| `cd "$DIR" && cat f` | no hint for relative `f` | base unknown |
| file read only to analyze, via a path under `tool-results/` or scratchpad | hint | exactly the audit's spilled-result reads |
| `git diff` of a one-line change | hint once per session | class B is heuristic; this is its cost |
| `cat file` right before an Edit | hint | Edit needs a prior *Read*, not `cat`; the hint costs about 600 bytes once and the full text is not repeated |

Residual risks:
- A network or slow filesystem could make `os.Stat` slow. `routeRead` already stats one path without a timeout; this adds at most 8, and only for paths that the parser kept.
- If the model ignores the hint, each later fire costs about 200 bytes (short text). Over a session that is far below one avoided 8 KB read.

## 5. Measuring the effect

### 5.1 Before shipping: replay (deterministic)

Extend `audit/transcripts.js` to keep full commands (not cut at 200 characters) for Bash results over 5 KB. Run them through the real classifier with a fake stat that returns the recorded result size for the first file operand. Report per class and per threshold (4, 6, 8, 12 KiB): share of bytes flagged (coverage), calls flagged, and flagged calls whose recorded result was under 5 KB (over-estimates). This checks the parser and picks the threshold and the 64 B/line constant. From §0 the expected class A + B coverage is about 76% of 37.9 MB, minus the threshold effect.

### 5.2 After shipping: re-run the audit on cmgo-era sessions

At least 10 sessions or two weeks of cmgo use, whichever is later (today there is one cmgo session in the audit). The cmgo era is marked by the PreToolUse hook output containing `mcp__cmgo__`. Compare with §0's baseline:
- **M1:** large Bash results of classes A and B per 100 Bash calls, and their bytes per day (baseline: 4,050 / 41,187 calls; 37.9 MB over 22 days).
- **M2:** follow rate: a fired guidance followed within 3 actions by `ctx_execute_file`, `ctx_batch_execute` or `ctx_execute`. Fires are the Bash PreToolUse results containing the new tip text.
- **M3:** false-positive review: 30 random fires; a fire counts as false positive when the output was needed in full and short (for example the same file is Edited within 3 actions, or the model re-ran the command with `| head`).
- **M4:** `bench/hook-latency.py` output.

Decision rule: M2 under 10% after 30 fires means the text, not the trigger, is wrong: revise the text before touching thresholds. M3 over 20% means raise the threshold or drop class B.

## 6. Test plan

### 6.1 Unit (`internal/routing`, real temp files like `routeRead`'s tests)
- Table test of the parser and estimator: each row of §1.2 and §1.3; quoting with spaces; unknown words; the redirect forms; limiters; heredoc, `$(`, backtick and over-long commands skip; more than 8 operands; directory, missing and symlinked files.
- `cd` tracking: absolute, relative, `~`, `cd -`, `cd "$X"`, two successive `cd`; relative file resolved against `Deps.Cwd` when the input has `cwd`.
- Threshold: just below, equal and above; `CMGO_BASH_READ_MIN_BYTES` unset, invalid, `0`, a valid value.
- Once rules: first fire long text, second short text, class B only once; `MCPReady` false gives no decision from this path; policy deny and the curl / inline / build redirects still win; `isStructurallyBounded` commands (`git status`, `ls`) get nothing from this path.
- Fall-through: when nothing matches, the decision equals today's (nudge, generic once guidance).
- The `routing:bash-read-guidance` row exists and the extension is visible as a decision change (the "difference must not vanish" guard).

### 6.2 Integration
- `internal/hookcmd`: a `pretooluse` fire with `cwd` in the input and a relative `cat` of a 20 KB temp file prints the tip; no event is written (`routing.Events` returns none).
- `TestGoldenPreToolUse` and `TestGoldenStrings` stay green without changing any golden file.

### 6.3 Bench (`bench/hook-latency.py`, p95 ≤ 10 ms, same method as the other cases)
- `pretooluse bash large cat`: `cat big.txt` over a 100 KB file in the temp project.
- `pretooluse bash compound`: `cd <project> && sed -n '1,200p' a; sed -n '1,200p' b; cat c d` with 8 stat targets.

Expected cost: a tokenizer pass of a few µs and at most 8 stats (tens of µs warm). The bench confirms it; it is not measured yet.

## 7. Decisions taken by default

1. Threshold 8 KiB, 64 bytes per line; both tunable after §5.1.
2. Guidance only, no `Redirect`, no event, no stats entry: measurement comes from transcripts, not from the DB.
3. Long text once per session, short text afterwards; class B once per session.
4. Gated on `MCPReady`, not on `subagent`.
5. `CMGO_BASH_READ_MIN_BYTES=0` is the off switch; there is no per-class switch. If the owner wants class B dropped, it is one `if` and the §1.3 text.
6. Out of scope for v1: `grep -r`, `find`, `ls -R`, `awk`, `jq`, `python3 file`, loops, globs, `$VAR` paths. Each is a stat-free guess or needs real expansion; the replay's "other" row says whether it is worth a second round.

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
