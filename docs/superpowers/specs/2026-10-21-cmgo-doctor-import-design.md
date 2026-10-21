# cmgo doctor, upgrade and TS import: design (sub-project 5 of 5)

Date: 2026-10-21. Status: written without an owner review round. The owner asked for sub-projects 2-5 to be specified, planned and built without further questions, following the reviewer's recommendations.

This builds on the four earlier specs. The TS reference is `a5e6dd31`:
- `S:` means `src/server.ts`.
- `CLI:` means `src/cli.ts`.

Behavior notes are in the R4 research (§B doctor/upgrade, §D on-disk data).

## 0. Goals and success criteria
- **Doctor:** `ctx_doctor` (MCP tool) and `cmgo doctor` (CLI) report whether cmgo is healthy on this machine, in the TS `[OK]/[WARN]/[FAIL]` plain-text format.
- **Upgrade:** `ctx_upgrade` and `cmgo upgrade` rebuild cmgo from source and refresh the Claude Code registration.
- **Import:** `cmgo import` copies the TS context-mode data still inside cmgo's retention windows into `cmgo.db`, once.

Criteria:
1. **Doctor:** every check in §1 has a test for its OK, WARN and FAIL outcomes, using a temp HOME and an injected executor. `cmgo doctor` exits 1 exactly when a FAIL line is printed.
2. **Upgrade:** it runs no command when `--dry-run` is given. When it does run, the command sequence equals §2, checked with an injected runner.
3. **Import:**
   - a fixture with TS session DBs and content DBs imports into the expected rows;
   - the TS files are byte-identical before and after;
   - a second run without `--force` changes nothing;
   - `ctx_search` finds an imported chunk, and the timeline search finds an imported event.
4. **No regressions:** all earlier criteria still hold (tests, idle writes, hook latency).

## 1. Doctor
Lines, in order:

```
cmgo doctor

[OK] Version: cmgo <version> (hook binary <version>)          | [WARN] ... hook binary <v2> differs — fix: cmgo upgrade
[OK] Storage: <CMGO_DIR> (writable)                           | [FAIL] Storage: <dir> — <error>
[OK] Database: <path> schema v<N>, FTS5 PASS                  | [FAIL] Database: <error>
[OK] Executor: PASS (shell)                                   | [FAIL] Executor: FAIL — exit <code>[ (<stderr ≤200>)]
[OK] Runtimes: <n>/12 — <lang, lang, ...>
[OK] Hooks: 7/7 events registered in <settings path>          | [WARN] Hooks: missing <Event, ...> — fix: cmgo install  | [FAIL] Hooks: could not read <path> — <error>
[OK] Hook binary: <path>                                      | [FAIL] Hook binary: not found at <path> — fix: cmgo install
[OK] MCP server: registered (<command> serve)                 | [WARN] MCP server: not registered — fix: cmgo install | [WARN] MCP server: registered at <old path> — fix: cmgo install
[OK] Statusline: cmgo                                          | [WARN] Statusline: another command is set (<cmd>)    | [OK] Statusline: not set
[OK] TS plugin: not enabled                                   | [WARN] TS plugin: context-mode@... is enabled — both inject routing text; fix: cmgo install --disable-ts-plugin
```

Check details:
- **Version:** from `-ldflags -X main.version`, else `runtime/debug.ReadBuildInfo` main module version, else `dev`. The hook binary version is read by running `<hook binary> version` with a 2 s timeout, so `cmgo-hook` gains a `version` argument.
- **Database:** opens the store normally (applying migrations), reads `user_version`, and runs the TS FTS5 probe (`CREATE VIRTUAL TABLE ... USING fts5`, insert, `MATCH`) on an in-memory DB.
- **Executor:** runs shell `echo ok` with a 5 s timeout and expects exit 0 and stdout `ok`. This is the TS "Server test", in the shell instead of JS.
- **Hooks:** counts which of the 7 events from `install.HookEvents` have a cmgo command in `settings.json`. "7/7" is the number of events cmgo registers after sub-projects 3 and 4.

`ctx_doctor` returns the same lines, from inside `cmgo serve`. `cmgo doctor` prints them and exits 1 on any `[FAIL]`.

## 2. Upgrade
`cmgo upgrade [--source <dir>] [--dry-run]`:
1. **Source:**
   - `--source`, else `$CMGO_SOURCE`, else the module (`github.com/lang315/context-mode-go`).
   - A source dir must be a git checkout of this module: its `go.mod` names it.
2. **With a source dir:**
   - `git -C <dir> pull --ff-only`;
   - then `go install ./cmd/cmgo ./cmd/cmgo-hook`, with the dir as working directory.
3. **Without one:** `go install github.com/lang315/context-mode-go/cmd/cmgo@latest github.com/lang315/context-mode-go/cmd/cmgo-hook@latest`, with `GOPRIVATE=github.com/lang315/*` added to the environment, because the repo is private. `GIT_TERMINAL_PROMPT=0` is also set, so a missing credential fails fast instead of waiting for input that `ctx_upgrade` can never give.
4. **Then:** run `<bin dir>/cmgo install` from the new binary. The bin dir comes from `go env GOBIN GOPATH`, so values set with `go env -w` count: GOBIN, else the first GOPATH entry + `/bin`. This refreshes the hooks, the MCP registration (the path may have changed) and the statusline.
5. **Output:** print one line per step as `[x] <step>` or `[ ] <step> — <error>`, and stop at the first failure. Exit 1 on failure.

Two rules apply throughout:
- `go` and `git` are found on `PATH`. A missing one fails that step with a hint.
- Nothing is deleted.

`ctx_upgrade` keeps the TS contract (S:4270-4427): it returns the text "Run this command using your shell execution tool", with the command `"<cmgo path>" upgrade`, then the checklist instructions and the restart hint. It does not run anything itself.

## 3. Import (`cmgo import [--from <dir>] [--dry-run] [--force]`)

### 3.1 Sources
`--from` defaults to the TS data roots, probed in this order (R4 §D.1):
1. `$CONTEXT_MODE_DIR`;
2. `$CLAUDE_CONFIG_DIR/context-mode`;
3. `~/.claude/context-mode`.

Two dirs are read:
- **Session DBs:** `<root>/sessions/*.db`.
- **Content DBs:** `<root>/content/*.db`.

`sessions-archive/` is not read, because its sessions are older than cmgo's 7-day retention and the next cleanup would delete them.

### 3.2 Safety
- **Copy first:** each TS DB is copied with its `-wal` and `-shm` files into a temp dir under `$CMGO_DIR/run/import-*`, then opened there read-only. The originals are never opened.
- **Clean up:** the temp dir is removed afterwards.
- **One import per DB:** in `meta`, key `ts_import`, value `<RFC3339 time> <n sessions> <n sources>`. If the key exists, import stops with a message unless `--force` is given.
- **`--dry-run`:** prints the counts and writes nothing to `cmgo.db`.
- **Bad files:** an unreadable or corrupt TS DB is skipped and counted (reason `unreadable`); it never aborts the run.
- **Torn copies:** a copy that SQLite reports as malformed (a TS process wrote between copying the `.db` and the `-wal`) is copied again once. If that fails too, the file is skipped as `unreadable`, and the summary suggests closing Claude Code sessions that still run the TS plugin before importing.

### 3.3 Mapping
- **Project:**
  - from `session_meta.project_dir`, else the most common non-empty `session_events.project_dir` of the session;
  - then `config.NormalizeProject`.
  - Sessions with no project dir are skipped and counted.
- **Session events:**
  - **Which rows:** rows of sessions whose `session_meta.last_event_at`, or newest event, is within `store.SessionRetention`.
  - **Columns:** `type`, `category`, `priority`, `data`, `source_hook`, `bytes_avoided` and `created_at` are copied verbatim, through a store import function. It bypasses dedup and cap but cuts `data` to 32 KiB and recomputes `data_hash`.
  - **Order and duplicates:** rows go in `id` order. A row is skipped when a row with the same `(session_id, type, data_hash, created_at)` is already in `cmgo.db` from another source DB or an earlier run. Repeats inside one source DB are kept, as TS stored them.
  - **Missing columns:** found with `PRAGMA table_xinfo`; they take their defaults.
- **Session meta:** `started_at`, `last_event_at`, `compact_count`, `event_count` = imported rows, `cc_pid` = 0. An existing cmgo session with the same id is merged, not replaced.
- **Content:**
  - **Which content DBs:** a content DB `<h16>.db` belongs to the project whose session DB file name starts with `<h16>`. Content DBs with no matching session DB are skipped and counted.
  - **Which sources:** sources with `indexed_at` within the 14-day source retention.
  - **How they are written:** each source and its `chunks` rows (title, content, content_type) go through a store import function. It writes `sources` (keeping `label` and `indexed_at`), `chunks`, `chunks_trigram` and `vocabulary`, the same way `Index` does, without re-chunking.
  - **Conflicts:** an existing cmgo source with the same `(project, label)` wins and the TS one is skipped.
  - **The `session-events` label is not imported.** It is TS's events dump, and cmgo answers that source from the events table.
- **Not imported:** `session_resume`, `tool_calls` (cmgo keeps its own stats from this point on), stats JSON files, events markdown, and logs.

### 3.4 Output
```
cmgo import from <root>
  sessions: <imported> imported, <skipped> skipped (<reasons>)
  events:   <n> imported, <d> duplicates
  sources:  <n> imported (<chunks> chunks), <s> skipped (<reasons>)
done in <ms> ms
```

## 4. Other changes
- **Routing block:** it gains the `ctx doctor` and `ctx upgrade` command lines from TS `<ctx_commands>`. With sub-projects 3 and 4, the block now matches the TS `includeCommands: true` text exactly, apart from the tool names and the context-mode wording that is kept.
- **Version:** `cmgo version` and `cmgo-hook version` print the §1 version.

## 5. Testing
- **Doctor:** each check with temp HOME/CMGO_DIR, fake settings files, a fake hook binary script, and an injected executor.
- **Upgrade:** an injected runner records the commands; tests cover `--dry-run`, the source-dir and module paths, and stop-on-failure.
- **Import:**
  - The fixture is built by a Go test helper that creates TS-schema SQLite files with the exact TS DDL (R4 §D.3/D.4, including an old variant without `project_dir`/`bytes_*` columns and a 4-column FTS content DB).
  - It covers WAL contents (rows written but not checkpointed are imported), the retention filters, dedup across DBs, the conflict rules, `--force`, `--dry-run`, and byte-identical sources (sha256 before and after).

## 6. Decisions taken by default
- **Upgrade builds from source**, because there are no release binaries for a private personal repo.
- **Import respects the retention windows**, so the next cleanup does not delete what was just imported.
- **No TS stats are imported**, because the TS numbers use other definitions (stats spec §0).
