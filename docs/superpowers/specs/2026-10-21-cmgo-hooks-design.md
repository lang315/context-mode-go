# cmgo hooks: design (sub-project 2 of 5)

Date: 2026-10-21. Status: written without an owner review round. The owner asked for sub-projects 2-5 to be specified, planned and built without further questions, following the reviewer's recommendations.

This document builds on the core spec, `docs/superpowers/specs/2026-10-20-cmgo-core-design.md`. The reference implementation is the TypeScript fork at commit `a5e6dd31`. In this document:
- `R:` means `hooks/core/routing.mjs`.
- `RB:` means `hooks/routing-block.mjs`.
- `X:` means `src/session/extract.ts`.
- `DB:` means `src/session/db.ts`.

All refs are at that commit.

## 0. Context and goals

### What this sub-project delivers
- The `cmgo hook <event>` subcommand, which Claude Code runs for every hook event. One short-lived process per event, like the TS hooks, but a static Go binary instead of `node` plus native-module checks.
- **PreToolUse routing and blocking:**
  - curl/wget, inline-HTTP and build-tool redirects;
  - the WebFetch redirect;
  - Read, Grep and Bash guidance;
  - routing text added to `Agent` prompts;
  - Bash deny and ask rules from Claude Code settings.
- **Session event capture into `cmgo.db`:**
  - PostToolUse;
  - PostToolUseFailure (new);
  - UserPromptSubmit;
  - Stop;
  - SessionStart (startup facts only).
- **SessionStart routing instructions:** the routing block, with cmgo tool names.
- **`cmgo install` and `cmgo uninstall`:** register the hooks in `~/.claude/settings.json` and the MCP server in Claude Code.

### Measured baseline (2026-10-21, macOS arm64)
- **TS hooks:** every TS hook fire starts `node`. `node -e ''` alone takes 20 ms wall time on this machine, before any hook code runs.
- **ensure-deps cost:** on darwin, the PreToolUse chain also copies and code-signs the SQLite native binary on every call (ensure-deps).
- **Go side:** a Go process that opens and migrates `cmgo.db` takes about 1 ms (measured: `store.Open` 0.64–0.88 ms, process total 0.86–1.1 ms).

### Success criteria for this sub-project
All measured on this machine.

1. **Latency:** hook wall time from process start to exit is p95 ≤ 10 ms over 200 runs for each case in `bench/hook-latency.py`:
   - `pretooluse` passthrough (`git status`);
   - `pretooluse` curl redirect;
   - `posttooluse` Read capture;
   - `userpromptsubmit`;
   - `sessionstart` startup.
2. **PreToolUse parity:** decisions and output JSON match the TS golden cases (§5.2), after the tool-name change in §1.3, apart from the documented differences in §2.3.
3. **Capture parity:** the kept extractors match the TS golden outputs (§5.2), apart from the fixes listed in §3.4.
4. **Install safety:**
   - `cmgo install` is idempotent: a second run changes no bytes.
   - It keeps every hook entry that is not cmgo's.
   - It refuses to write a settings file it cannot parse.
   - `cmgo uninstall` after `cmgo install` restores a settings file equal to the original as JSON.
5. **No idle writes:** the core criterion still holds. Disk writes from 8 idle `cmgo serve` processes are at most 0.1 MB/min (`bench/idle-writes.py`).

### Out of scope here
- Resume and compaction directives, PreCompact, timeline search over session events, `ctx_purge`: sub-project 3.
- Token usage from transcripts, the pricing catalog, `ctx_stats`, the statusline, server-side byte counters: sub-project 4.
- `ctx_doctor`, `ctx_upgrade`, the TS data import: sub-project 5.

## 1. Architecture

### 1.1 Binary and packages
`cmgo hook <event>` reads one JSON object from stdin and writes at most one JSON line to stdout. `<event>` is one of:
- `pretooluse`
- `posttooluse`
- `posttoolusefailure`
- `userpromptsubmit`
- `stop`
- `sessionstart`

An unknown event exits 0 with no output.

New packages:

| Package | Responsibility |
|---|---|
| `internal/hookio` | stdin read (BOM strip, size cap 16 MiB), session id and project resolution, compact JSON writer with HTML escaping off, an order-preserving JSON object type |
| `internal/routing` | PreToolUse decisions, guidance throttle, MCP-ready check, routing block text, Claude Code output format |
| `internal/capture` | event extractors for PostToolUse, PostToolUseFailure, UserPromptSubmit, Stop and SessionStart |
| `internal/install` | settings.json editing, MCP registration, uninstall |

`internal/store` gains the session tables (§3.1) and `InsertEvents`. `internal/security` gains `EvaluateCommand` (§2.2). `cmd/cmgo` gains `hook`, `install` and `uninstall`, and `serve` writes the MCP-ready sentinel (§2.5).

The `hook` path must not build the MCP server, the executor or the fetcher. It opens the store only when it has an event to write.

### 1.2 Failure policy
Every hook fails open:
- **Error handling:** any error or panic is recovered, appended as one line to `$CMGO_DIR/hook-errors.log` (`<RFC3339> pid=<pid> <event>: <error>`), and the process exits 0 with no stdout.
- **Malformed input:** malformed or empty stdin produces no output.
- **Stop output:** Stop prints `{}` on success, as TS does (`hooks/stop.mjs`).
- **stdin timeout:** stdin has a 2 s idle timeout. This is a fallback; Claude Code closes stdin.

### 1.3 Tool names
Under `claude mcp add --scope user cmgo`, the tools are named `mcp__cmgo__ctx_execute` and so on. Every TS string that names a context-mode tool uses `mcp__plugin_context-mode_context-mode__<tool>`. cmgo uses `mcp__cmgo__<tool>` in the same places. The golden generator applies this one substitution to the TS output, so parity is still byte-exact (§5.2).

### 1.4 Session id and project
These rules are the same for every hook:
- **Session id:**
  1. the 36-character UUID at the end of `transcript_path` (`/([a-f0-9-]{36})\.jsonl$/`);
  2. else `session_id`;
  3. else `$CLAUDE_SESSION_ID`;
  4. else `pid-<parent pid>`.
- **Project dir:** `config.Load` with `getwd` replaced by `input.cwd`:
  1. `CMGO_PROJECT_DIR`;
  2. `CLAUDE_PROJECT_DIR`;
  3. `input.cwd`;
  4. the process cwd.

  The project key is `config.NormalizeProject`, the same key `sources.project` uses. TS used two different rules in different hooks (R2 §1.1); cmgo uses one.
- **File names:** a session id used in a file name is kept only if it matches `^[A-Za-z0-9._-]{1,128}$`. Otherwise its SHA-256 hex prefix (16 characters) is used instead.

## 2. PreToolUse

### 2.1 Kept exactly
These are the TS decision tree (R:670-1017) and the Claude Code formatter (`hooks/core/formatters.mjs:7-71`), with the tool-name change from §1.3:

1. **Bash, in this order:**
   - security deny or ask;
   - curl/wget;
   - inline HTTP;
   - build tool;
   - structurally bounded command;
   - opt-in byte threshold (`CMGO_BASH_NUDGE_MIN_COMMAND_BYTES`, same parsing as TS `CONTEXT_MODE_BASH_NUDGE_MIN_COMMAND_BYTES`);
   - once-per-session guidance.

   All regexes, message texts, segment splitting and the "all curl segments safe means stop" short cut are kept, including the known false negatives (curl with any `>` passes through with no nudge, R1 P5). The build-tool reason keeps its literal `\"`.

   Go `regexp` (RE2) has no lookahead and no backreferences. These TS patterns become hand-written matchers with the same results on the TS examples (R1 P1):
   - `SHELL_CONTROL_OPERATORS` (`<(?!<)`, `&(?!&)`);
   - the `mv`, `cp`, `rm`, `ln` and `ls` safe patterns (`(?!…)` on the first flag token);
   - `stripHeredocs` (`\1`);
   - the verbose test `/\s(-v|--verbose|--trace|-D\s+-)\b/`.

   JS `\s` and `trim()` cover Unicode spaces; a shared `jsSpace` class is used wherever TS uses `\s` (R1 P2).
2. **Bash redirects become denies:** a modify carrying `command` is emitted as `permissionDecision: "deny"`, and the reason is the text inside `echo "…"`, not unescaped.
3. **Read:**
   - When the file is a regular file over 50,000 bytes, Read guidance is shown on every call, and a `read-redirected` event is recorded (§2.4).
   - Otherwise, Read guidance is shown once per session.
4. **Grep:** guidance once per session.
5. **WebFetch:** deny with the TS reason. The URL is put into the text raw.
6. **Agent:**
   - The routing block is appended, with `includeCommands:false` and `toolSearchBootstrap:true`, to the first present field of `prompt`, `request`, `objective`, `question`, `query`, `task` (default `prompt`).
   - `subagent_type` `Bash` becomes `general-purpose`.
   - Output is `updatedInput` with no `permissionDecision`.
   - The key order of `tool_input` is kept, with replaced values left in place.
7. **Gating:**
   - When `agent_id` or `agent_type` is present, or no cmgo MCP server is live (§2.5), the curl/wget, inline-HTTP, build-tool and WebFetch redirects pass through.
   - Guidance, Agent injection and security still apply.
8. **Output JSON:**
   - The output is compact, with keys in TS order and a trailing newline.
   - `<`, `>` and `&` are not escaped.
   - A passthrough writes nothing.

### 2.2 Security
`security.Policy` gains `EvaluateCommand(cmd) (decision string, pattern string)`, a port of `evaluateCommand` (`src/security.ts:512-563`):
- **Rules read:** the existing settings files and cache, now also reading `permissions.allow` and `permissions.ask` `Bash(...)` entries.
- **Deny pass:** for each chained or subshell element in order, then each policy file in order, the first matching deny wins and returns `"deny"` with the full rule.
- **Ask and allow pass:** then, per file in order:
  - any element matching an ask rule returns `"ask"` with that pattern;
  - else, if every element matches an allow rule, the result is `"allow"`.
- **Default:** `"ask"` with an empty pattern.

PreToolUse maps the result:
- deny → deny, with reason `Blocked by security policy: matches deny pattern <rule>`;
- ask with a pattern → `permissionDecision: "ask"`;
- anything else falls through. An allow rule does not stop the redirects, as in TS.

### 2.3 Intentional differences from TS

| TS | cmgo | Why |
|---|---|---|
| Matchers for `ctx_execute`, `ctx_execute_file` and `ctx_batch_execute`, each running a security check and pinning `cwd` | Not registered | `cmgo serve` already checks deny rules for these tools (core §4.5), and runs them in the project dir |
| External-MCP periodic guidance behind matcher `mcp__` | Dropped | Claude Code treats `mcp__` as an exact name, so the TS branch never fires (R1 P11). cmgo keeps that effective behavior |
| `CONTEXT_MODE_REQUIRE_SECURITY` fail-closed gate and the security-warning context | Dropped | The security engine is linked into the binary and cannot fail to load |
| A rejected marker for every `deny` **and** every `modify` | A `rejected` event for `deny` only | Agent injection and cwd pins were recorded as rejections (R1 P8) |
| Latency, rejected and redirect markers in tmp files, consumed by the next PostToolUse | PreToolUse writes the `rejected` and redirect events itself, in one store transaction | A denied tool never gets PostToolUse, so TS attributed the markers to an unrelated later call (R1 P9). The `tool_latency` event is dropped |
| MCP-ready sentinel in `/tmp`, refreshed every 30 s | `$CMGO_DIR/run/mcp-ready-<pid>`, written once at serve start and removed at exit. Liveness is `kill(pid, 0)` (EPERM counts as live). A dead pid's file is removed | No idle writes (criterion 5). `$CMGO_DIR` is derived the same way by `serve` and `hook`; a temp dir is not, because `$TMPDIR` can differ between the two processes (R1 P10) |
| Guidance markers under `os.tmpdir()/context-mode-guidance-s-<sid>` | `$CMGO_DIR/run/guidance-<sid>/<type>`, created with `O_EXCL` | Own namespace, so the TS plugin and cmgo can run side by side; same root as the sentinel |
| `ctx_execute(shell)` and `ctx_batch_execute` commands checked against Bash **ask** rules in PreToolUse | Not checked | Follows from not registering those matchers. Deny rules are still enforced by `cmgo serve`; ask rules are not |
| Self-heal of the plugin cache and settings | Dropped | Node plugin layout only |
| `CONTEXT_MODE_*` env names | `CMGO_*` with the same parsing | Own namespace |

### 2.4 Events written by PreToolUse
PreToolUse opens the store only when it writes one of these events:
- **`rejected`** (category `rejected-approach`, priority 2): data `<tool>: <reason>`, for a security deny or a redirect deny.
- **`bash-redirected`, `read-redirected`, `webfetch-redirected`** (category `redirect`, priority 2): data `<tool>: <summary>`, where summary is the first 200 UTF-16 units of the command, path or URL. `bytes_avoided` is 8192, the file size and 16384 respectively.

A redirect deny writes both events.

### 2.5 MCP-ready sentinel
- **Writer:** `cmgo serve` writes its pid to `$CMGO_DIR/run/mcp-ready-<pid>` after the store opens (creating `run/`), and removes the file when `serve` returns.
- **Reader:** `routing.MCPReady(dir)` scans `$CMGO_DIR/run` for `mcp-ready-*` files. It returns true at the first one whose pid is live, and removes the files of dead pids.

## 3. Session event capture

### 3.1 Schema (migration 2 of `cmgo.db`)
```sql
CREATE TABLE session_meta (
  session_id TEXT PRIMARY KEY,
  project TEXT NOT NULL,
  started_at TEXT NOT NULL,         -- UTC 'YYYY-MM-DD HH:MM:SS', like sources.indexed_at
  last_event_at TEXT NOT NULL,
  event_count INTEGER NOT NULL DEFAULT 0,
  compact_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE session_events (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  project TEXT NOT NULL,
  type TEXT NOT NULL,
  category TEXT NOT NULL,
  priority INTEGER NOT NULL,        -- 1 = most important ... 4 = least
  data TEXT NOT NULL,
  data_hash TEXT NOT NULL,          -- sha256(data) hex, first 16, upper case (DB:1145)
  source_hook TEXT NOT NULL,
  bytes_avoided INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL          -- UTC 'YYYY-MM-DD HH:MM:SS'
);
CREATE INDEX session_events_session ON session_events(session_id, id);
CREATE INDEX session_events_project ON session_events(project, id);
CREATE INDEX session_events_evict ON session_events(session_id, priority, id);
```
The per-event attribution columns are dropped (`attribution_source`, `attribution_confidence`, `bytes_returned`, `project_dir`); see §3.4.

### 3.2 Insert rules (`store.InsertEvents(sessionID, project, hook string, events []Event)`)
All events of one hook fire go in **one** IMMEDIATE transaction:
1. `INSERT OR IGNORE` into `session_meta` with `started_at = last_event_at = now`.
2. **Dedup:** skip an event if the same `(type, data_hash)` is among the last 5 rows of the session, counting rows inserted earlier in the same transaction (DB:962-968).
3. **Cap:** when the session has 1000 rows, delete one row first: `ORDER BY priority DESC, id ASC LIMIT 1`. This keeps the most important rows; TS deleted them first (R2 Port note 1).
4. Insert the event, with `data` cut to 32 KiB at a UTF-8 boundary.
5. Update `session_meta`: `last_event_at = now`, `event_count += inserted`.

`store.CleanupIfDue` also deletes the events and meta rows of sessions whose `last_event_at` is older than 7 days (the TS startup sweep, DB:1696-1705, keyed on activity instead of start time).

### 3.3 Extractors kept
Each extractor gets `tool_name`, `tool_input` (ordered JSON) and `tool_response` (raw JSON). The data formats are exactly as in X: for the cases in `r2-golden.json`, apart from §3.4.

| Hook | Extractor (X:) | type / category / priority |
|---|---|---|
| PostToolUse | file and rule (`:176-306`): Read, Edit, NotebookEdit, Write, Glob, Grep | `rule` + `rule_content` / `rule` / 1, `file_read`, `file_edit`, `file_write` / `file` / 1, `file_glob`, `file_search` / `file` / 3 |
| PostToolUse | cwd (`:312-327`), Bash | `cwd` / `cwd` / 2 |
| PostToolUse | git (`:373-440`), Bash | optional `cwd`, then `git_commit` or `git` / `git` / 2 |
| PostToolUse | env (`:810-847`), Bash | `env` / `env` / 2 |
| PostToolUse | task (`:653-668`) | `task`, `task_create`, `task_update` / `task` / 1 |
| PostToolUse | plan (`:714-803`) | `plan_enter`, `plan_exit`, `plan_approved` (1), `plan_rejected`, `plan_file_write` / `plan` / 2 |
| PostToolUse | skill (`:853-863`) | `skill` / `skill` / 2 |
| PostToolUse | subagent (`:900-915`) | `subagent_completed` (2) or `subagent_launched` (3) / `subagent` |
| PostToolUse | decision (`:1096-1152`), AskUserQuestion | `decision_question` / `decision` / 2 |
| PostToolUse | worktree (`:1243-1265`), EnterWorktree | `worktree` / `env` / 2 |
| PostToolUseFailure | new | `error_tool` / `error` / 2, data = the input `error` string; if `error` is absent or not a string, the compact JSON of `tool_response` (the payload field name is from the Claude Code docs, not observed) |
| UserPromptSubmit | prompt (`hooks/userpromptsubmit.mjs:36-71`) | `user_prompt` / `user-prompt` / 1 (raw prompt; skipped when empty or starting with `<task-notification>`, `<system-reminder>`, `<context_guidance>`, `<tool-result>`) |
| UserPromptSubmit | `extractUserEvents` (X:2758-2774) minus `extractData` | `plan_enter`, `decision`, `role`, `intent`, `goal`, `blocker`, `blocker_resolved` |
| Stop | `hooks/stop.mjs:32-43` | `turn_end` / `session` / 1, data `{"stop_hook_active":<bool>,"last_assistant_message":<first 2000 UTF-16 units or null>}` |
| SessionStart (`startup` only) | `hooks/sessionstart.mjs:341-373` | `rule` + `rule_content` / `rule` / 1 for each non-blank `~/.claude/CLAUDE.md`, `<project>/CLAUDE.md`, `<project>/.claude/CLAUDE.md`; then `session_start` / `session_start` / 1, data `{"source":"startup","project_dir":<dir>,"started_at":<unix s>}` |

Lengths that TS measures with `.length` or `.slice` count UTF-16 code units in Go too. Limits that TS measures with `[...s].length` count code points.

### 3.4 Capture differences from TS

| TS | cmgo | Why |
|---|---|---|
| Eviction `ORDER BY priority ASC` | `ORDER BY priority DESC, id ASC` | TS deleted user prompts, files and rules first (R2 Port note 1) |
| Per-event project attribution with confidence scores, and a stale bundle in production (R2 §5.3-5.4) | One project per hook fire (§1.4) | One rule everywhere. Only the snapshot and search read the project, and they need the session's project |
| `git -C <dir> checkout` produces no events | The parsed subcommand is matched against the git op table | Bug (R2 §2.3.1) |
| `error_tool` from a regex over successful Bash output | `error_tool` from PostToolUseFailure `error` | TS rows were mostly false positives (R2 §2.4) |
| `rule_content` from Read stores the stringified response object | Stores `tool_response.file.content` when the response is an object with that field, else the response text | The stored text is the file, not JSON |
| Dropped extractors: `constraint_discovered`, `bash_outcome`, `file_read_metadata`, `agent_finding`, `external_ref`, `mcp`, `mcp_tool_call`, `webfetch_metadata`, `worktree_exit`, Task `agent_usage`, `error_resolved`, `retry_detected`, `data` | Not ported | Noise, duplicates, or dead on Claude Code (R2 §9). No sub-project 3 consumer reads them |
| Platform forwarding, prompt features, error classifiers | Not ported | Forward-only, with no local effect |
| Transcript usage in Stop | Sub-project 4 | Stats |
| `bulkInsertEvents` adds 1 to `event_count` per batch | Adds the number of inserted rows | Bug |

### 3.5 Matchers
`cmgo install` registers these matchers:

| Event | Matcher |
|---|---|
| PreToolUse | `Bash\|WebFetch\|Read\|Grep\|Agent` |
| PostToolUse | `Bash\|Read\|Write\|Edit\|NotebookEdit\|Glob\|Grep\|TodoWrite\|TaskCreate\|TaskUpdate\|EnterPlanMode\|ExitPlanMode\|Skill\|Agent\|AskUserQuestion\|EnterWorktree` |
| PostToolUseFailure | same as PostToolUse |
| UserPromptSubmit | `""` |
| SessionStart | `""` |
| Stop | `""` |

## 4. SessionStart (this sub-project's part)
- **All sources** print `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":<routing block>}}`. The routing block is the TS `createRoutingBlock(t)` text (RB:16-80), with these changes:
  - cmgo tool names;
  - the `<ctx_commands>` section left out, because cmgo has no stats, doctor, upgrade or purge tools yet. Sub-projects 3-5 add their lines back.
- **`startup` also:**
  - deletes guidance marker directories older than 7 days;
  - records the rule and session_start events (§3.3).
- **`resume` and `compact`:** context is added by sub-project 3.

## 5. Install

### 5.1 `cmgo install [--binary <path>] [--disable-ts-plugin] [--dry-run]`
1. **Paths:**
   - The settings file is `$CLAUDE_CONFIG_DIR/settings.json`, else `~/.claude/settings.json`.
   - The binary path defaults to `os.Executable()`, with symlinks resolved.
2. **Read and parse:** read and parse the settings file with an order-preserving JSON decoder.
   - If the file is missing, start from `{}`.
   - If it does not parse, exit 1, print the parse error, and do not write.
3. **Remove cmgo's old commands:** remove every inner hook command whose `command` is `"<abs path>" hook <event>` and whose path basename is `cmgo`. A matcher entry left with no commands is dropped, and an event array left empty is dropped. Other commands, including ones in the same matcher entry, are kept.
4. **Add the new entries:** add one matcher entry per event from §3.5, each with one command `"<binary>" hook <event>`.
5. **TS plugin:**
   - If `enabledPlugins` has a true key starting with `context-mode@`, print a warning that both plugins will inject routing text.
   - With `--disable-ts-plugin`, set that key to false.
6. **Write:**
   - If the bytes changed, copy the old file to `settings.json.cmgo-bak` once, then write atomically (temp file plus rename), with 2-space indent and a trailing newline.
   - `--dry-run` prints the new file instead.
7. **MCP registration:**
   - If `~/.claude.json` has no `mcpServers.cmgo`, run `claude mcp add --scope user cmgo -- <binary> serve` when `claude` is on `PATH`.
   - Otherwise print that command.

### 5.2 `cmgo uninstall [--dry-run]`
Removes cmgo's hook commands (step 3), writes the file the same way, and runs or prints `claude mcp remove --scope user cmgo`. It does not touch `enabledPlugins`.

## 6. Testing and acceptance

### 6.1 Unit tests
- **Routing:**
  - every regex and matcher on its own, with the TS verified examples (R1 §2.1.d);
  - the guidance throttle in a temp dir;
  - the MCP-ready check with live and dead pids;
  - Agent key-order preservation.
- **Security:** `EvaluateCommand` against the TS deny, ask and allow cases.
- **Store:**
  - migration from user_version 1 to 2 on a DB created by the core code;
  - dedup;
  - cap and eviction order;
  - the 32 KiB cut;
  - `event_count`;
  - cleanup.
- **Capture:** one test per extractor, plus the §3.4 fixes.
- **Install:** missing file, user hooks in the same matcher entry, idempotency, parse error, `--dry-run`, uninstall round trip, and a fake `claude` on `PATH`.

### 6.2 Golden parity tests
A Node script, `tools/golden-gen/gen-hooks.mjs`, runs by hand against `$TS_REPO` at `a5e6dd31`:
- It imports the TS modules directly: `hooks/core/routing.mjs`, `hooks/core/formatters.mjs`, `hooks/routing-block.mjs`, and `hooks/session-extract.bundle.mjs`.
- It points HOME, TMPDIR and the sentinel dir at a fresh temp dir, and never runs a hook script.
- It writes `testdata/golden/hooks-pretooluse.json`, `hooks-capture.json` and `hooks-strings.json`, replacing every `mcp__plugin_context-mode_context-mode__` with `mcp__cmgo__`.

The Go tests replay each case with the same state (guidance markers, MCP ready or not, subagent fields). `testdata/golden/HOOKS-KNOWN-DIFFS.md` lists every case whose expected value differs because of §2.3 or §3.4. The tests fail if a listed difference disappears.

### 6.3 End to end
- `cmd/cmgo` tests run `run([]string{"hook", ...})` in-process with a temp HOME and a temp `CMGO_DIR`, for each event.
- They also run the built binary once per event through `os/exec`, to check the stdout bytes and the exit code.
- One test starts a real `cmgo serve` process and a separate `cmgo hook pretooluse` process with the same `CMGO_DIR`, and checks that the curl redirect fires. With `serve` stopped, the same call passes through. This catches a sentinel the two processes resolve differently.

### 6.4 Acceptance scripts
- **`bench/hook-latency.py <binary>`:** runs each case from criterion 1 200 times against a temp `CMGO_DIR` (warm DB), and prints p50 and p95 per case. It then runs the `posttooluse` case from 4 concurrent workers (200 fires each) into the same DB. PASS when every p95 ≤ 10 ms, the concurrent case included.
- **`bench/idle-writes.py`:** unchanged; must still PASS.

The results go in the PR body.

## 7. Decisions taken by default
- **Delivered as the binary's own subcommand:** hooks are `cmgo hook <event>`, not a second binary. One binary to install, and the hook path skips server setup.
- **Data:** events live in `cmgo.db`, not in per-project files, so sub-project 3 can search across sessions of a project.
- **Text kept as is:** the `context-mode:` prefix stays in redirect messages, so TS parity holds byte for byte.
- **The installer never runs as part of tests or the build.** Running it on the owner's real `~/.claude` is left to the owner, after the PR is merged.
