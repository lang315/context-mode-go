# cmgo stats and statusline: design (sub-project 4 of 5)

Date: 2026-10-21. Status: written without an owner review round. The owner asked for sub-projects 2-5 to be specified, planned and built without further questions, following the reviewer's recommendations.

This builds on the core, hooks and session-memory specs. The TS reference is `a5e6dd31`:
- `A:` means `src/session/analytics.ts`;
- `SL:` means `bin/statusline.mjs`;
- `S:` means `src/server.ts`.

## 0. Context and goals

### What TS does on Claude Code (R4 §A)
- **Counters:** the MCP server keeps in-memory counters per process (calls and bytes per tool, bytes indexed, bytes sandboxed, cache hits). They are written to a `stats-pid-<ppid>.json` sidecar that **nothing on Claude Code reads**.
- **Stats records:** the server attaches its `tool_calls` rows and `sandbox-execute` / `index-write` events to "the most recently started session of the project DB", which is often the wrong session.
- **`ctx_stats`:** prints a five-section narrative. Its headline numbers mix three definitions of "kept out":
  - event payload size;
  - `bytes_avoided` estimates;
  - runtime counters.

  It also includes marketing lines (team-size projections, "months of Cursor Pro").
- **Statusline:** it opens every session DB **writable** several times per second and may `ALTER TABLE` them.

### What cmgo does instead
- **Persistence:** one measured definition, persisted in `cmgo.db`, and attributed to the right session.
- **`ctx_stats`:** a short factual report.
- **Statusline:** a fast `cmgo-hook statusline` that opens the DB read-only.

### Success criteria
1. **"Kept out" is measured, never estimated:**
   - for `ctx_execute`, `ctx_execute_file` and `ctx_batch_execute`: bytes the tool produced or read minus bytes it returned;
   - for `ctx_fetch_and_index`: bytes fetched minus bytes returned;
   - for PreToolUse redirects: the TS `bytes_avoided` values (8192 / file size / 16384), shown separately as "redirected".

   Unit tests pin each definition.
2. **Stats go to the right session:** server statistics are attributed to the Claude Code session that owns the server process (§1.2), with two concurrent sessions in one project in the test.
3. **Statusline speed:** p95 ≤ 10 ms over 200 runs against a DB with 100 sessions and 50,000 events (`bench/hook-latency.py` gains the case). It never writes to the DB.
4. **No idle writes:** the idle-write criterion still holds. Counters are written only when a tool call finishes.

### Out of scope / dropped
- **Token usage and cost from transcripts:** Claude Code's own statusline input already carries `cost.total_cost_usd`.
- **The pricing catalog.**
- **TS-only extras:** the multi-adapter scan, `ctx_insight`, and the version-outdated banner (sub-project 5 handles versions).

## 1. Data

### 1.1 Schema (migration 3)
```sql
CREATE TABLE tool_stats (
  server_id TEXT NOT NULL,          -- "<pid>-<start unix nanos>" of the cmgo serve process
  cc_pid INTEGER NOT NULL,          -- Claude Code process that owns the server (§1.2), 0 if unknown
  project TEXT NOT NULL,
  tool TEXT NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  bytes_returned INTEGER NOT NULL DEFAULT 0,
  bytes_kept INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,         -- server start, UTC 'YYYY-MM-DD HH:MM:SS'
  window TEXT NOT NULL DEFAULT '',   -- started_at of the linked session at write time ('' when none)
  updated_at TEXT NOT NULL,
  PRIMARY KEY (server_id, tool, window)
);
CREATE INDEX tool_stats_cc ON tool_stats(cc_pid, updated_at);
CREATE INDEX tool_stats_project ON tool_stats(project);
ALTER TABLE session_meta ADD COLUMN cc_pid INTEGER NOT NULL DEFAULT 0;
```
Sub-project 3 adds no schema change, so this is migration 3.

### 1.2 Linking a server to a session
Hooks, the statusline and `cmgo serve` all run under the same Claude Code process. The MCP server is never told its session id, because Claude Code does not export `CLAUDE_SESSION_ID` to MCP servers (R4 §A.2).

- **Finding `cc_pid`:** walk the parent chain from the current process, up to 8 hops. `cc_pid` is the first ancestor whose executable name contains `claude` (case-insensitive).
  - On darwin the walk uses `sysctl kern.proc.pid.<pid>` (`golang.org/x/sys/unix.SysctlKinfoProc`, already in the module graph).
  - On Linux it reads `/proc/<pid>/stat`.
  - If no ancestor matches, `cc_pid` is 0.
- **SessionStart** (every source, `clear` included, because `/clear` starts a new session in the same Claude Code process) stores `cc_pid` in `session_meta` for its session id. It is the hook's first DB write, so server calls that follow are tagged with the new session.
- **`cmgo serve`** computes `cc_pid` once at start and writes it in every `tool_stats` row.
- **Each `tool_stats` write is tagged** with `window` = the `started_at` of the newest `session_meta` row with the server's `cc_pid` and project, read in the same transaction (`''` when there is none).
- **A session's server stats** are the rows with its `cc_pid`, project and `window` = its `started_at`. After `/clear`, the new session gets a new `started_at`, so its calls go to new rows, and the previous session's rows stay with it.

### 1.3 Counting
`mcpserver` records each tool call after its result is built. One UPSERT per call adds 1 to `calls`, plus that call's `bytes_returned` (UTF-8 bytes of all result text) and `bytes_kept`.

| Tool | raw bytes |
|---|---|
| `ctx_execute` | stdout + stderr bytes of the run |
| `ctx_execute_file` | file size + stdout + stderr bytes |
| `ctx_batch_execute` | sum of every command's output bytes |
| `ctx_fetch_and_index` | response body bytes of every fetched URL (cache hits: the cached source's content bytes) |
| `ctx_search`, `ctx_purge`, `ctx_stats` | 0 |

`bytes_kept = max(0, raw - bytes_returned)`. A failed DB write is ignored: stats must never fail a tool.

## 2. `ctx_stats`
- **Input:** `{}`.
- **Output:** plain text. All sizes use the TS `kb()` helper (A:1961-1977), and durations use `formatDuration`.

```
cmgo stats

This session  ·  <duration>  ·  <calls> calls
  Kept out of context    <kb kept>   (<pct>% of <kb raw> processed)
  Returned to context    <kb returned>
  Redirected by hooks    <n> calls  ·  ~<kb bytes_avoided>
  <tool padEnd 22> <calls padStart 4> calls  <kb kept padStart 9> kept  <kb returned padStart 9> returned     (one line per tool, by calls desc)

Session memory  ·  <events> events  ·  <compactions> compactions
  <category padEnd 22> <count padStart 5>     (top 8 categories by count)

This project  ·  <sources> sources indexed (<kb content>)  ·  <sessions> sessions  ·  <events> events

All projects  ·  <kb kept> kept out  ·  <sessions> sessions  ·  since <YYYY-MM-DD>

cmgo <version>
```

Details:
- **"This session":** the caller's session is resolved as in §1.2, from `serve`'s own `cc_pid`. If no session is linked, the session lines are replaced by `This session  ·  no hook data yet (is cmgo-hook installed?)`. The server's own `tool_stats` rows are still shown.
- **`pct`:** `round(kept / raw * 100)`, or `0` when raw is 0.
- **Lifetime:** sums over all `tool_stats` rows, plus the redirected `bytes_avoided`. The start date is the earliest `started_at`.

## 3. Statusline
`cmgo-hook statusline` reads Claude Code's statusLine stdin JSON and prints one line with no trailing newline, then exits 0.

Data sources:
- **Session id:** `session_id` from stdin, else `CLAUDE_SESSION_ID`.
- **DB:** opened read-only (`mode=ro`, no migrations). If the DB is missing or older than migration 3, the line falls back.

Output, plain text with no ANSI, because Claude Code's stdout is a pipe:

| Case | Line |
|---|---|
| fallback (no DB, no session, error) | `cmgo  ●  ready` |
| session has no kept bytes yet | `cmgo  ●  <kb lifetime kept> kept out lifetime` |
| otherwise | `cmgo  ●  <kb session kept> this chat  ·  <kb lifetime kept> lifetime  ·  <pct>% kept out` |

`pct` is computed over the session.

## 4. Other changes
- **Routing block:** it gains the `ctx stats` command lines from TS `<ctx_commands>` (RB:62-77), rewording "context savings question" to keep the same trigger list.
- **`cmgo install`:** sets `statusLine` to `{"type":"command","command":"<shell-quoted hook binary> statusline"}` when it is absent or is cmgo's own, so a reinstall from a new path updates it. When another statusline is set, it prints a notice and leaves it. `uninstall` removes `statusLine` only when it is cmgo's.
- **`ctx_purge`, project scope:** also deletes the project's `tool_stats` rows. The message lists `session stats`.

## 5. Testing
- **Unit tests:**
  - the kept-bytes definitions per tool;
  - the UPSERT;
  - parent-chain walk with an injected process table (`func(pid int) (ppid int, name string, err error)`);
  - session linking (two sessions, two `cc_pid`s; `/clear` within one `cc_pid`);
  - `ctx_stats` text with an injected clock and version;
  - statusline cases;
  - read-only open, with a test that asserts the DB file's mtime and size do not change.
- **Golden:** none. The report is cmgo's own format, so tests pin exact strings.
- **Bench:** the statusline case.

## 6. Decisions taken by default
- **The TS narrative report is not ported.** Its headline mixes inconsistent definitions and includes projections; the owner's goal is a fast personal tool.
- **The `cc_pid` link is used instead of "latest session":** it is the only signal shared by the MCP server and the hooks without changing Claude Code.
- **Statusline in the small binary,** because Claude Code runs it often.
