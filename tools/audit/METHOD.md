# cmgo effectiveness baseline: method (written before any number is computed)

Date: 2026-10-22. Main = 5d3e402 (PRs #13, #14 merged), DB schema v4.
Everything is read-only: the DB is copied (db + wal, never the shm) into a temp dir and opened there; transcripts are only read. Nothing under `~/.claude` is written. Outputs and caches go to `~/.cache/cmgo-audit` (or `$CMGO_AUDIT_OUT`).
Every number in the report is printed by `cmgo_audit.py`. A number not printed by the script is not in the report.

## What "effective" has to mean here

cmgo exists to keep bytes out of the context window. Three things can show that, and each proves less than it sounds:

| Evidence | Source | Can prove | Cannot prove |
|---|---|---|---|
| Bytes kept out | `tool_stats` (`bytes_kept`) | the sandbox processed N bytes and returned fewer | that the model would otherwise have pulled N bytes into context (counterfactual) |
| Raw bytes still dumped | transcripts: Bash/Read/Grep/WebFetch results over 5 KB | upper bound on what could have been routed | that each was avoidable (editing needs Read; many are small-range reads) |
| Context size / compactions per session | transcripts: assistant `usage`, `compact_boundary` | what happened per session | cause: task type, project and session length dominate |

## Fixed definitions

**Sessions.** One main transcript `.jsonl` (not under `subagents/`) = one session. Subagent transcripts are counted apart and never in session shares. Test projects are excluded: project directory name contains `scratchpad`, `-e2e`, or starts with `-private-tmp`.

**Window.** Transcript lines with `timestamp >= 2026-09-30` (same as the earlier audit, for the cross-check). Eras: *pre-cmgo* = before 2026-10-21T08:00Z (first cmgo `tool_stats` row, first server start), *cmgo era* = after. The TS plugin (`mcp__plugin_context-mode_context-mode__*`) is still installed, so both families are tracked separately; "ctx user" means any `ctx_*` tool call from either.

**Large raw result.** A `tool_result` for a tool_use named Bash, Read, Grep or WebFetch whose text is over 5000 bytes (UTF-8), as in the earlier `verify.js`. No dedupe. Reported with and without Reads of `tool-results/` and `scratchpad` files (spilled outputs, which are re-reads of already-dumped data).

**Redirect and follow.** A redirect is a `hook_success` attachment from a hook command containing `cmgo-hook` or `context-mode` with a `PreToolUse` hookName whose stdout contains a deny decision or the text `redirected`, plus the earlier-audit pattern (`hook error` / `redirected` in a tool_result). Followed = one of the next 3 `tool_use`s of the same session is a `ctx_*` tool. Redirects are counted per hook family (TS plugin vs cmgo), by structure, never by grepping free text (specs and code discussions contain the same strings).

**Bash read-guidance fire (PR #11 spec §5.3).** A `hook_success` attachment, command contains `cmgo-hook`, hookName `PreToolUse:Bash`, stdout contains the class A tip (`This command prints about` or `KB of output (`). Follow rate M2 = share of fires with a `ctx_execute_file`/`ctx_batch_execute`/`ctx_execute` in the next 3 actions. Rollback rule needs 40 fires; below that the script says so and projects the date from the fire rate. M1 (class A results >= 16 KiB per 100 Bash calls against the baseline 0.55 per 100) is computed from Bash results with a cat/nl/tac/bat/head/tail first stage. M3 (precision) needs a manual sample and is listed, not computed.

**Kept, from the DB.** `tool_stats` rows are `(server_id, tool, window)` sums, so a row cannot be split by time or by language. A row is assigned to a period by `updated_at`:
- before 2026-10-21 (PR #10 JS/TS counters merged): kept excludes JS/TS reads, so it is not comparable;
- before 2026-10-22 05:36Z (12:36 local, PR #12 Python install): kept excludes Python reads;
- `unmeasured_calls` exists from 2026-10-22 06:30Z (13:30 local, migration 4): rows before that are "unknown", never "measured".
Test exclusions: projects matching the transcript test rule, and window `5f9ecd0a...` (context-mode-06's 2.79 MB test call; the row also holds that session's other calls, so the whole row is excluded and its size is printed).
Language is not stored in `tool_stats`; the language mix of `ctx_execute` calls comes from the `tool_use` inputs in transcripts (cmgo and TS plugin separately) and is a call count, not bytes.

**Net effect and comparison.** Per main session (non-test, cmgo era): peak context = max over assistant turns of `input + cache_creation + cache_read` tokens; turns; compactions = `compact_boundary` system lines. Groups: no ctx use / light (1-4 ctx calls) / heavy (5+). Compared inside the same project where both groups exist, and as per-100-turn rates. This is observational: task type, project, session length and the model's own habits differ between groups. No causal claim is made.

**Cost.**
- Hook latency: `durationMs` of `hook_success` attachments from `cmgo-hook` commands (what Claude Code measured, process start included), p50/p95/max per hook event, against the TS plugin's hooks over the same period.
- Python bootstrap overhead: PR #12 benchmark (+3.0 to +4.1 ms), quoted, not re-measured.
- Failures attributable to cmgo: `tool_result.is_error` for `mcp__cmgo__*`; `hook_non_blocking_error`, `hook_cancelled` and non-zero `exitCode` for `cmgo-hook` commands; `hook-errors.log` if present.

## Verdict rules (decided now, not after seeing the data)

- A question is "not enough data" when its post-fix sample is under 30 events (calls, fires or sessions) or the post-fix window is under 3 days; the script prints the projected date at the current rate for 30 events.
- "Effective" needs the measured quantity to move in the intended direction and an honest statement of what is not proven. Observational context comparisons can at most say "consistent with".
- The script re-runs with no arguments (`python3 -I tools/audit/cmgo_audit.py`) and writes `report.md` and `report.json` to the output directory; `--since` and `--out` override.

## Subagent funnel (added 2026-10-22)

For each non-test subagent transcript (`.../subagents/agent-*.jsonl`, type from its `.meta.json`): *block* = the first user message contains `context_window_protection` and a ctx tool name (the Agent hook's routing block); *listed* = a `ctx_*` tool name in a `deferred_tools_delta` attachment; *ToolSearch* = at least one ToolSearch call; *ctx called* = at least one `ctx_*` tool call. Reported for block+listed subagents per agent type, TS-plugin period vs cmgo period. A subagent's context is separate from the main window, so these bytes are subagent cost, not main-context pressure (spec `2026-10-22-cmgo-subagent-routing-design.md` §1.6).
