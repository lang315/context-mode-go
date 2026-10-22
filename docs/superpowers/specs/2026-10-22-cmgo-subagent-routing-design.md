# cmgo: routing large outputs in subagents (investigation and design)

Date: 2026-10-22. Rev 1. Inputs: the 2026-10-22 baseline audit (`cmgo_audit.py`, `subagent_probe.py`; 1,347 non-test subagent transcripts since 2026-09-30, 1,322 before the cmgo install and 25 after).

## 0. Question

The audit found 84% of raw tool results over 5 KB in subagents (52.3 of 62.1 MB non-test), and no `ctx_*` call by any subagent after the cmgo install. Why, and what is the smallest change that routes more of it, if any?

## 1. Findings (every number from the probe; TS-plugin period unless stated)

**1.1 The Agent hook injects the block into every subagent prompt.** `routeAgent` appends `RoutingBlock(false, true)` (no `<ctx_commands>`, with the ToolSearch bootstrap) to the prompt field. It is a port of TS `routing.mjs:892-916`. Seen in 1,301 of 1,322 TS-period prompts and 23 of 25 cmgo-period prompts (the 2 without it were not examined).

**1.2 The ctx tools are listed to most subagents.** Deferred-tool lists show `ctx_*` for general-purpose (944 of 948) and Explore (67 of 72). They are not listed for fixed-tool agent types (implementer, go-reviewer, evaluator, protocol-expert, claude-code-guide: 104 subagents). Those types hold **6% of the large-result bytes (3.4 of 60.1 MB)**. This part is outside cmgo's control; TS handled it with `mcpToolsAvailable` (#794) for hard redirects, and cmgo does the same (`canRedirect` is false for subagents).

**1.3 The tools are reachable and the model does not use them.** Subagents with the block and the tools listed: 1,197. Of those, 167 (14%) ran ToolSearch and 39 (3.3%) called a `ctx_*` tool. The 1,179 that did not hold 53.4 MB of large results. In the cmgo period: 23 such subagents, 0 ToolSearch, 0 `ctx_*`. Exposure is not the bottleneck; adoption is.

**1.4 Hooks do fire inside subagents, but mostly say nothing.** `cmgo-hook pretooluse` ran in a cmgo-period subagent (Read tip). Three mechanisms keep it quiet:
- hard redirects (curl, inline HTTP, build tools, WebFetch) are skipped when the input has `agent_id`/`agent_type` (TS parity);
- the generic Bash and Read tips use `guidanceOnce(runDir, sessionID, type)`, and the marker dirs under `~/.claude/cmgo/run/` are named by the main session id only (11 UUID dirs, none an agent id). A subagent shares its parent's marker, so after the parent has seen the tip, no subagent sees it. TS: only 80 of 1,322 subagents had any logged PreToolUse output from the TS hook;
- the thresholds are far above the bulk: the Read tip fires above 50,000 B (26 of 1,948 large subagent Reads); the Bash class A hint above 16 KiB.

**1.5 What the bytes are.** 5,247 large results, 60.7 MB: Bash 3,292 / 31.4 MB (first word: `cat` 8.4 MB, `cd ... &&` 6.6, `sed` 5.8, `git` 3.3, `grep` 1.9), Read 1,948 / 29.2 MB. By size: 5-10 KB 2,995 / 20.5 MB; 10-16 KB 1,231 / 15.2 MB; 16-50 KB 1,001 / 23.9 MB; over 50 KB 20 / 1.1 MB. 59% of the bytes are under 16 KB, below every existing threshold. general-purpose holds 73%; Explore 5%.

**1.6 Why it matters less than "84%" suggests.** A subagent's context is separate from the main session's: its raw bytes do not enter the main window, only its final report does. Subagent contexts are small (median peak 85k tokens, p90 276k; median 9 turns), **0 of 1,354 ever compacted**, and the median subagent holds 29 KB of large results (about 7k tokens, under 10% of its peak). The benefit of routing is subagent token cost and time, not context-window pressure.

## 2. Options

| | Change | Expected effect | Cost / risk |
|---|---|---|---|
| **O0** | No behavior change. Add the subagent funnel (listed, ToolSearch, `ctx_*`, raw bytes) to the audit script. | none; gives the number to decide on | none |
| **O1** | Subagent block v2: shorter, one rule up front ("before a command or Read that prints more than 5 KB, load the tool: `ToolSearch(select:...)`, then `ctx_execute_file`"), with the exact ToolSearch line. Env switch `CMGO_AGENT_BLOCK=legacy` restores the TS text. | targets the biggest loss (86% never run ToolSearch). Unknown size; TS-era baseline 3.3%. | text differs from TS (KNOWN-DIFFS); prompt size about equal |
| **O2** | Subagent tips by agent type or size: Read tip from 16 KiB instead of 50,000 B, and a per-subagent marker (`agent_id`) instead of the session marker. | reaches 60% of subagent Read bytes (17.4 of 29.2 MB; 640 of 1,948 results) with a tip; but tips were ignored 71% of the time in the main session | more hook output; a tip still needs the ToolSearch step first |
| **O3** | Deny or rewrite large subagent reads. | would force routing | breaks Edit workflows and fixed-tool agents; rejected |

## 3. Recommendation

**O0 now, O1 only if the owner wants to pursue subagent cost.**

- Value ceiling is low (§1.6): no main-context effect, no compaction, a few percent of a subagent's context per run.
- The cmgo-period sample is 25 subagents (2 on 10-22): too few to know whether the TS-era 3.3% holds. O0 produces that number at no risk.
- If O1 is wanted: pre-registered success is funnel-based, measured by the audit script on subagents spawned with the v2 block.
  - Metric: share of subagents (block present, tools listed) that call any `ctx_*`; baseline 3.3% (TS), 0 of 23 (cmgo).
  - Keep if at least 15% over at least 60 subagents; revert (switch to `legacy`, then remove) if under 8% over 60. In between: collect 60 more.
  - At the 6 to 124 subagents per day seen in the last week of the TS period (2 on 10-22), 60 subagents take about 1 to 10 days, longer if usage stays as low as today.
- Not proposed: O2 as a first step (needs the same ToolSearch step; tips already ignored most of the time), O3.

## 4. What cmgo cannot do

- Make `ctx_*` non-deferred or visible to fixed-tool agent types: Claude Code and the agent definitions decide that. Changing agent definitions, settings or CLAUDE.md is an owner decision and is not part of this spec.
- Shrink a built-in tool's output after it ran (PostToolUse cannot replace Bash/Read output).

## 5. Testing (O1 only)

- `routing`: `agentBlock` v2 text golden; `CMGO_AGENT_BLOCK=legacy` yields the byte-identical TS text; non-block fields of the Agent input unchanged (existing `routeAgent` tests).
- `cmgo-hook` deps unchanged.
- Audit: `subagent_probe` funnel table in the script, with the v2 block detected by a marker string.
- KNOWN-DIFFS entry `routing:agent-block-v2`.

## 6. Decisions taken by default

- No change to agent definitions, settings or CLAUDE.md.
- Subagent raw bytes are reported as cost, not as context pressure.
- Fixed-tool agent types are reported as out of scope (6% of bytes).
