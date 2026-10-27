# cmgo: compact large Bash output in the main session (design)

Date: 2026-10-27. Rev 1. Evidence: the 2026-10-27 effectiveness audit (`tools/audit/cmgo_audit.py`), a main-session transcript analysis since the install (2026-10-21 08:00Z, 60 sessions, 83 compactions), and a hook spike on Claude Code 2.1.311. Follows the cleanup in PR #17 (`cleanup-kept-cap`), on which this branch is stacked.

## 0. Problem and evidence

cmgo keeps bytes out of the context only when the model chooses a `ctx_*` tool. Six days after the install, raw intake did not drop (3.47 MB/day against 2.92 MB/day in the TS era; 28% of sessions use `ctx_*`, 29% before). Tips and hints are mostly ignored: the Bash read guidance was followed 0 times in 11 fires, and hard redirects are followed 57% of the time.

Main-session Bash results since the install: 15,869. Claude Code already spills a Bash result over about 30,000 characters to a file with a 2,000-character preview, so the gap is 5-30 KB. Bytes a compaction would save (original minus a 2 KB preview), by threshold and first command:

| threshold | saved | results | other commands | `sed` | `cat`/`head`/`tail` | `git diff`/`show` |
|---|---:|---:|---:|---:|---:|---:|
| 5 KB | 3.94 MB | 655 | 1.96 MB | 1.15 MB | 0.67 MB | 0.15 MB |
| 8 KB | 1.99 MB | 201 | 0.99 MB | 0.48 MB | 0.43 MB | 0.08 MB |
| 16 KB | 0.50 MB | 28 | 0.30 MB | 0.07 MB | 0.09 MB | 0.03 MB |

**Spike (Claude Code 2.1.311, `claude -p --settings`, scratch `CMGO_DIR`).** A PostToolUse hook that prints `hookSpecificOutput.updatedToolOutput`:

- replaces a Bash result (`{stdout, stderr, interrupted, isImage}`) and a Read result (`{type, file{...}}`) before the model sees it;
- does the same inside a subagent; the hook input then carries `agent_id` and `agent_type`;
- leaves no copy of the original: both the transcript `tool_result` and `toolUseResult` hold the replacement;
- breaks Read-then-Edit: the Edit lands on the real file, but the model re-reads the replacement and believes the file is unchanged.

So: compact Bash only, never Read, and store the original before replacing it.

**Owner decisions (2026-10-27):** threshold 5 KB, skip reading commands; main session only; index inside `cmgo-hook` (not a spill file, not head/tail only).

**Ceiling:** about 1.96 MB per 6 days in 60 sessions, about 33 KB (about 8k tokens) per session. This is modest; §5 says when it is not worth keeping.

## 1. Behavior

`cmgo-hook posttooluse` compacts when all hold:

1. `tool_name` is `Bash`;
2. the input has no `agent_id` (main session only);
3. the MCP server is up (`routing.MCPReady(runDir)`, the sentinel the PreToolUse routing already uses): without it the model cannot call `ctx_search`;
4. `len(stdout) + len(stderr)` of `tool_response` is above the threshold (§3) and at most 30,000 bytes;
5. the command is not a reading command: the first command of every chain, after `cd …&&` and env assignments, is not `sed`, `cat`, `head`, `tail`, `nl`, `tac`, `bat`, `less`, `git diff` or `git show` (`git -C <dir>` and `git --no-pager` count as `git`). A command the tokenizer skips (heredoc, `$(`, backtick, `(` outside quotes, over 4096 bytes) is treated as not reading;
6. the command does not contain `# cmgo:full`.

A failing command goes to PostToolUseFailure, which this design does not touch; Claude Code already cuts it to about 10,000 characters.

When it compacts, the hook prints:

```json
{"hookSpecificOutput":{"hookEventName":"PostToolUse","updatedToolOutput":{"stdout":"<compact text>","stderr":"","interrupted":<from the response>,"isImage":false}}}
```

The compact text (at most about 2.5 KB):

```
<the first 1024 bytes of the output, cut back to the last newline>

[cmgo: 18.3 KB, 412 lines; the middle is kept out of context. 7 lines match error|fail|panic|warn:]
<up to 20 matching lines from the middle, each cut to 200 bytes>
...
<the last 1024 bytes, cut forward to the first newline>

[cmgo: the full output is indexed as "bash:<tool_use_id>". Search it: ctx_search(queries: ["..."], source: "bash:<tool_use_id>"). Need it verbatim? Re-run the command with "# cmgo:full" at the end.]
```

- The output is stdout, then stderr after a newline when both are non-empty (what the model would have seen).
- Matching is case-insensitive on `error|fail|panic|warn`, only in the middle part (head and tail are shown anyway). When no line matches, the bracket line ends at "kept out of context." and no lines follow.
- Sizes use the `ctx_execute` formatting (`KB` with one decimal).
- Cuts never split a UTF-8 sequence.

When any condition fails, or any write in §2 fails, the hook prints nothing, as today, and the model sees the original output. A compaction never loses output.

## 2. Storage

In one transaction, before printing:

- `store.Index(project, "bash:<tool_use_id>", ChunkPlainText(output, 20))` (20 lines per chunk, as `ctx_execute`'s intent indexing) through a variant that does **not** add words to `vocabulary`. Bash output is full of hashes, paths and numbers, and `vocabulary` is never pruned (open follow-up from sub-project 1).
- One `tool_stats` row per session (primary key `(server_id, tool, window)`, added to on each compaction; `started_at` is the first compaction's time): `server_id = 'hook'`, `cc_pid = 0`, `window = session_id`, `tool = 'bash-compact'`, `bytes_returned = len(compact text)`, `bytes_kept = min(len(output) - len(compact text), 30000)`. `stats_totals` is updated the same way as for serve rows. These are measured bytes, so they count toward lifetime.

The session events (`capture.PostToolUse`) are written afterwards, in their own transaction, from the original response, exactly as today. A failed compaction still writes the events.

**Retention.** A `bash:` source is useful only during its session. `CleanupIfDue` also deletes `bash:%` sources (and their chunks) older than 48 hours; other sources keep 14 days. At about 0.65 MB of original output a day, with the trigram index, this is about 4-6 MB of steady-state DB.

**Busy DB.** The hook uses its usual busy timeout. On timeout it prints nothing and logs to `hook-errors.log`, as the event writes do.

## 3. Configuration

`CMGO_BASH_COMPACT_MIN_BYTES`: unset means 5120; `0` turns compaction off; an integer 1-30000 sets the threshold; anything else means 5120. The feature is **on by default** as an experiment with the decision rule in §5; `0` is the immediate off switch. `CLAUDE.md` lists the switch.

## 4. Code layout and parity

- `internal/compact` (new, pure functions, no I/O): `Text(output string, sourceLabel string) string` builds the compact text; the threshold parsing.
- `internal/routing`: export `ReadsFiles(command string) bool`, built on the `readguard.go` tokenizer. The Bash read guidance stays off (PR #17); its hint text is decided after §5 has data.
- `internal/store`: the no-vocabulary `Index` variant; a hook stat write (`tool_stats` + `stats_totals` for a session window); the 48-hour `bash:` cleanup.
- `internal/hookcmd`: `postToolUse` runs compaction first, then the events.
- `cmgo-hook` imports only `hookcmd` and its current dependencies plus `compact`; never `mcpserver`, `executor` or `fetch` (the deps test enforces it).
- `testdata/golden/HOOKS-KNOWN-DIFFS.md`: `posttooluse:bash-compact`. TS never replaces tool output. Posttooluse output is unchanged whenever cmgo does not compact, so the existing goldens still match.
- `testdata/golden/KNOWN-DIFFS.md`: `stats:bash-compact`, the `bash-compact` rows in `tool_stats` (no TS tool of that name).

## 5. Measurement and decision rule (pre-registered)

`tools/audit/cmgo_audit.py` gets a "Bash compaction" section, computed from `tool_stats` and the transcripts of main sessions:

- **M1 saved:** sum of `bytes_kept` of `bash-compact`, per session. Expected about 33 KB per session.
- **M2 recovery:** the share of compactions followed within 3 actions by a `ctx_search` with that source, a re-run of the same command, or the same command with `# cmgo:full`. A re-run puts the original back into the context, so it counts as a full loss.
- **M3 net:** M1 minus the bytes the recovering actions brought back (re-run results plus `ctx_search` results).

Judge after at least 200 compactions (about 2 days at the measured 110 a day):

- keep if M2 ≤ 30% and M3 ≥ 50% of M1;
- M2 between 30% and 50%: raise the default threshold to 8192 once, judge again after 200 more;
- remove (default `0`, then delete the code) if M2 > 50%, M3 < 0, or any compaction ever loses output.

## 6. Testing (TDD, red first)

- `compact`: head and tail cut at line boundaries and never inside a UTF-8 sequence; 0, 1, 20 and 21 matching lines; 200-byte cut of a long matching line; size and line count text; stdout + stderr joining; threshold parsing (unset, `0`, junk, 1, 30000, 30001).
- `routing`: reading-command table: `sed -n 1,9p f`, `cd d && cat f`, `FOO=1 head f`, `git -C d diff`, `git --no-pager show x`, `cat f | grep x`, `go test ./...` (not reading), `ls && cat f` (reading in a later chain counts), skipped forms (heredoc, `$(`).
- `store`: the no-vocabulary `Index` leaves `vocabulary` unchanged and is searchable; the hook stat write adds to `tool_stats` and `stats_totals`; `bash:` sources older than 48 hours are cleaned, a 47-hour one and a 15-day-old non-`bash:` source within 14 days are kept.
- `hookcmd` (end to end, in-process like the existing hook tests):
  - a 20 KB stdout gives the `updatedToolOutput` shape; `ctx_search`'s store query finds a line from the middle under `bash:<id>`; one `bash-compact` row;
  - prints nothing for: `agent_id` present; MCP down; 5120 bytes; 30001 bytes; a reading command; `# cmgo:full`; `CMGO_BASH_COMPACT_MIN_BYTES=0`; a non-Bash tool;
  - DB write-locked: prints nothing, logs to `hook-errors.log`, events still written once the lock is gone;
  - the existing posttooluse golden cases are unchanged.
- Bench: `bench/hook-latency.py` gains `posttooluse bash compact (20 KB)`, p95 ≤ 25 ms; no existing case regresses by 1 ms or more in p95 on the same machine.
- E2E before the PR: `claude -p --settings <hooks pointing at bin/cmgo-hook>` with a scratch `CMGO_DIR`, a command printing 20 KB with a marker line in the middle; the model must find the marker with `ctx_search`. The transcript shows the compact text.

## 7. Out of scope

- Read output (breaks Read-then-Edit, §0), subagents (owner decision; `ctx_search` is deferred there and only 14% of subagents run ToolSearch), PostToolUseFailure.
- Changing the routing block, the SessionStart text or the Agent block.
- Removing the Bash read guidance code (after §5).
