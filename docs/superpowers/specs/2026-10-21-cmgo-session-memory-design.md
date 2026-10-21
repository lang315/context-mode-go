# cmgo session memory and compaction: design (sub-project 3 of 5)

Date: 2026-10-21. Status: written without an owner review round. The owner asked for sub-projects 2-5 to be specified, planned and built without further questions, following the reviewer's recommendations.

This document builds on the core spec (`2026-10-20-cmgo-core-design.md`) and the hooks spec (`2026-10-21-cmgo-hooks-design.md`). The reference implementation is the TypeScript fork at `a5e6dd31`. Refs:
- `SS:` means `hooks/sessionstart.mjs`.
- `SD:` means `hooks/session-directive.mjs`.
- `AI:` means `hooks/auto-injection.mjs`.
- `AM:` means `src/search/auto-memory.ts`.
- `U:` means `src/search/unified.ts`.
- `S:` means `src/server.ts`.

## 0. Context and goals

### What this sub-project delivers
- **After a compaction or a resume, the model gets back what the session was doing:**
  - the last request;
  - pending tasks;
  - decisions, files, errors, git operations, rules, subagents, skills and environment;
  - intent, role and plan state.

  This is injected by SessionStart from the session events captured in sub-project 2.
- **PreCompact** records the compaction.
- **`ctx_search` reaches session memory:**
  - session events, through `sort: "timeline"` and through `source: "session-events"`;
  - the project and user `CLAUDE.md`;
  - Claude Code's auto-memory files.
- **`ctx_purge`** wipes a session or a project.

### What TS actually does on Claude Code (R3 headline findings)
- **The PreCompact snapshot is never shown:** SessionStart(compact) marks it consumed and rebuilds context from live events instead (SS:191-259). The snapshot is shown only on a resume with no live events, and that path is blocked by the same consumed flag.
- **Dead sections:** the directive's "Last Request" and `<continue_from>` never appear, because they read category `prompt` and capture writes `user-prompt`.
- **Leg boundary:** the prior-leg split of subagent tasks (#780) never applies in the directive.
- **Timeline search:** it returns at most 2 results per query, and they are the **oldest** matches.
- **Auto-memory:** it searches `<configDir>/memory/<hash>/`, not Claude Code's real memory dir.
- **Events file:** every `*-events.md` file of every project is indexed into, and then deleted by, whichever MCP server touches the store first.

cmgo keeps the effective behavior (rebuild from live events) and fixes the dead and cross-project parts (§2.4).

### Success criteria for this sub-project
1. **Directive and auto-injection parity:** the session directive and auto-injection texts match the TS builders on the golden fixtures (§5.2), after the tool-name change, apart from the fixes in §2.4.
2. **Timeline search:** `ctx_search(sort: "timeline")` returns matching session events of the current project and auto-memory snippets next to indexed content, newest matches first-selected, then shown oldest to newest (§3.2).
3. **Purge:** `ctx_purge` removes exactly the rows of its scope and nothing else (§4).
4. **Latency:** the hook criteria still hold. `sessionstart` with `source: "compact"` on a session with 1000 events, and `precompact`, stay at p95 ≤ 10 ms (`bench/hook-latency.py` gains these cases).
5. **No idle writes:** the core idle-write criterion still holds.

### Out of scope
- The PreCompact XML snapshot (`buildResumeSnapshot`) and the `session_resume` table.
- Stats, statusline and token usage: sub-project 4.
- Doctor, upgrade and import: sub-project 5.

## 1. Architecture
No new binary. Changes by package:

| Package | Change |
|---|---|
| `internal/memory` (new) | Directive builder, auto-injection builder, auto-memory search. Pure functions over `[]store.StoredEvent` and files |
| `internal/store` | `SessionEventsForSearch`, `CompactSession` (event count, compaction event and `compact_count` in one transaction), `PurgeSession`, `PurgeProject` |
| `internal/hookcmd` | `precompact` event; SessionStart `compact` and `resume` add the directive and auto-injection |
| `internal/routing` | `RoutingBlock` gains the `ctx purge` lines (§2.5) |
| `internal/install` | registers `PreCompact` (matcher `""`) |
| `internal/mcpserver` | `ctx_search` timeline merge and the `session-events` source, restored session-memory sentences in its description, new `ctx_purge` tool |

## 2. Hooks

### 2.1 PreCompact (`cmgo-hook precompact`)
- **Store** (`store.CompactSession`), only when the session has events (as TS), in one transaction:
  - `session_meta.compact_count += 1`;
  - insert `{type:"compaction_summary", category:"compaction", priority:1, data:"Session compacted. <N> events, <F> files touched."}`, source hook `PreCompact`. `N` is the session's event count before the insert. `F` counts events of category `file`.
- **Output:** prints `{}` and a newline.

### 2.2 SessionStart, per source
`additionalContext` is the concatenation below. The routing block comes first, as in sub-project 2.

| source | added after the routing block | side effects |
|---|---|---|
| `startup` (or missing) | nothing | sub-project 2 rules and `session_start` events |
| `compact` | if the session has events: `directive("compact")`, then `"\n\n" + autoInjection` when that is non-empty | insert `resume_completed` (`session-resume`, priority 1, data `Session resumed from compact. Prior events loaded.`); insert `session_start` with source `compact` |
| `resume` | if the session has events: `directive("resume")` | insert `session_start` with source `resume` |
| `clear` and others | nothing | none |

The session's events are `store.SessionEvents(sessionID)`, ordered by `id`. TS ordered by `created_at` at 1 s resolution.

### 2.3 Events markdown: not ported
TS writes `writeSessionEventsFile` (SD:77-272), a Markdown dump of the session's events, and the MCP server later indexes it under label `session-events` (S:508-521). The directive then tells the model to call `ctx_search(queries, source: "session-events")`.

cmgo does not build or index that file. Instead, `ctx_search` with `source: "session-events"` reads the session events of the current project directly (§3.1).

Reasons:
- **Latency:** indexing the dump took 3–4 ms of a SessionStart(compact) hook fire. With it, the hook missed the 10 ms target on a 1000-event session (p95 15.5 ms measured while drafting).
- **Freshness:** the dump would be stale the moment the next event was captured.
- **No cross-project risk:** TS had one, because every project's dump was indexed by whichever server touched the store first.

### 2.4 Directive and auto-injection (`internal/memory`)
Both are ported from SD:275-500 and AI:30-102 with the same templates, section order, truncation (UTF-16 units) and the task-matching algorithm (R3 §2.1). The search tool name is `mcp__cmgo__ctx_search`.

Auto-injection leaves out TS's fallback to the last 3 decisions: with a role capped at 400 characters and 5 decisions capped at 100, the first block always fits the 500-token budget, so the fallback cannot trigger.

Fixes:

| TS | cmgo | Why |
|---|---|---|
| "Last Request" and `<continue_from>` read category `prompt`, never written on CC | They read the last `user-prompt` event | Dead code that was meant to work (R3 headline 4) |
| The prior-leg split receives `writeSessionEventsFile`'s return value, so it never applies | An event is prior-leg when its id is lower than the id of the session's last `session_start` event | R3 headline 5. Ids also avoid TS's string compare of second-resolution timestamps |
| "MCP Tools Used" and "Data References" sections | Left out | Their categories (`mcp`, `data`) are not captured (hooks spec §3.4) |
| `getSessionEvents` ordered by `created_at` | Ordered by `id` | Same-second ties were unordered |

### 2.5 Routing block
- `RoutingBlock(includeCommands, …)` gains the `ctx purge` command lines from the TS `<ctx_commands>` block (RB:62-77), and the closing "After /clear or /compact" line.
- The stats, doctor and upgrade lines arrive with sub-projects 4 and 5.
- SessionStart uses `includeCommands: true` from this sub-project on. The block lists only the commands cmgo has.

## 3. `ctx_search` and session memory

### 3.1 `source: "session-events"`
- **Behavior:** when `source` is exactly `session-events`, `ctx_search` answers in both sort modes from `store.SessionEventsForSearch(project, query, "", limit)`:
  - match rules as in §3.2;
  - newest first;
  - shown as `--- [prior-session | <YYYY-MM-DD HH:MM> | <category>] ---`, `### [<category>] <type>`, then the event data as the snippet (the existing snippet extractor).
- **Errors:** an empty result is `No results found.`, as for other sources. The empty-knowledge-base error does not apply to this source.
- **Coverage:** this is the source the directive's `<session_search>` block names, so its advice works without an indexed dump.

### 3.2 `sort: "timeline"`
For each query, the result list is built from three sources:
1. **Content store:** the existing timeline path (core §3.3), with project scope.
2. **Session events** of the current project, all sessions (`store.SessionEventsForSearch(project, query, source, limit)`):
   - a row matches when every whitespace-separated term of the query (case-insensitive, ASCII folding as SQLite `LIKE`) occurs in `data` or in `category`;
   - `source`, when given, must equal `category` exactly;
   - title `[<category>] <type>`, origin `prior-session`, timestamp `created_at`.
3. **Auto-memory** (§3.3), only when `source` is empty.

From each source, take the **newest** `effectiveLimit` matches. Merge them, keep the newest `effectiveLimit` overall, then show them oldest to newest. `effectiveLimit` is the existing `min(limit, 2)`, or 1 after the soft cap.

The output format is the TS one: `--- [<origin> | <YYYY-MM-DD HH:MM> | <source>] ---` header, `### <title>`, then the snippet (S:2739-2756).

Difference from TS: TS kept the **oldest** matches (R3 headline 7). That contradicts the routing block's "query prior decisions … on resume", which needs the latest ones.

### 3.3 Auto-memory
Candidate files, in order:
1. `<project>/CLAUDE.md` (label `project/CLAUDE.md`);
2. `<claude dir>/CLAUDE.md` (label `user/CLAUDE.md`);
3. every `*.md` in Claude Code's memory dir for the project, `<claude dir>/projects/<slug>/memory/` (label `memory/<file>`), sorted by name.

`<claude dir>` is `$CLAUDE_CONFIG_DIR` when set, else `<home>/.claude` (`config.Config.ClaudeDir`, added in sub-project 2). The user `CLAUDE.md` in item 2 is read from the same dir.

`<slug>` is the absolute project dir as given, with every `/` and `.` replaced by `-`. That is Claude Code's layout, for example `/Users/lang/x.y` gives `-Users-lang-x-y`.

Matching and snippets follow AM:57-188: terms of 3 or more characters, a word-bounded case-insensitive match, a ±200/500 character window snapped to blank lines, files over 1,000,000 bytes skipped, and at most one result per file. Title `[auto-memory] <label>`, origin `auto-memory`, timestamp = file mtime.

Difference from TS: TS searched `<configDir>/memory/<hash>/`, a directory Claude Code does not use (R3 headline 6).

### 3.4 Description
The `ctx_search` description gets back the TS session-memory sentences that the core spec removed (S:2564-2585), with tool names unchanged (`ctx_search`).

## 4. `ctx_purge`
Port of S:4445-4611 and `src/session/purge.ts:178-338`, reduced to cmgo's single DB:

| Arguments | Effect | Message |
|---|---|---|
| `confirm` missing or false | nothing | `Purge cancelled. Pass confirm: true to proceed.` |
| `confirm: true, sessionId: X` | delete X's `session_events` and `session_meta` rows | `Purged session X: session events. Other sessions and project-wide stats preserved.` (or `no matching rows`) |
| `confirm: true, scope: "project"` or `confirm: true` alone | delete every source and chunk (porter and trigram) of the current project, and the events and meta rows of every session of the project. The shared vocabulary table is kept | `Purged: knowledge base (FTS5), session events. All session data for this project has been permanently deleted.` |
| `sessionId` together with `scope: "project"` | nothing, error | the TS ambiguity message |

The schema and description are copied from TS, without the stats wording.

## 5. Testing
- **Unit tests:**
  - `memory` builders (directive, auto-injection) against goldens;
  - task matching, leg boundary, truncation;
  - auto-memory matching and slug;
  - store purge scope;
  - the timeline merge (newest selection, ascending display, source filter, project scope).
- **Golden:** `tools/golden-gen/gen-memory.mjs` imports `hooks/session-directive.mjs` (`buildSessionDirective`, `groupEvents`) and `hooks/auto-injection.mjs` with a temp HOME. It writes `testdata/golden/memory-*.json` for the fixtures of R3 §9, with the tool-name substitution. `testdata/golden/MEMORY-KNOWN-DIFFS.md` lists the §2.4 fixes.
- **End to end:**
  - `hookcmd` tests for `precompact` and for SessionStart `compact` and `resume`;
  - an `mcpserver` test that runs a timeline search after events are inserted, and a `ctx_purge` round trip.
- **Bench:** `bench/hook-latency.py` adds `precompact` and `sessionstart compact` with 1000 events; both p95 ≤ 10 ms.

## 6. Decisions taken by default
- No snapshot table: the TS snapshot does not reach the model on Claude Code. One fewer write per compaction.
- The events markdown is not built at all; `source: "session-events"` searches the events table directly (§2.3).
- Timeline returns the newest matches.
- Auto-memory reads Claude Code's real memory dir.
