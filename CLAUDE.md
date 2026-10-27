# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`cmgo` is a Go rewrite of the TypeScript `context-mode` Claude Code plugin, for one user, on Claude Code only, on macOS. The main goals are low memory, low disk writes and fast hook start-up. The reference implementation is the TS fork at `/Users/lang/GolandProjects/github.com/lang315/context-mode`, commit `a5e6dd31`. Tool names, parameters, thresholds and output text match TS byte for byte, so existing routing rules and model habits keep working.

The design record is in `docs/superpowers/specs/` (one spec per sub-project: core, hooks, session memory, stats, doctor/import) and `docs/superpowers/plans/`. Code comments cite these as `spec §N` and cite TS code as `S:` (`src/server.ts`), `E:` (`src/executor.ts`), `ST:` (`src/store.ts`) with line numbers. Read the matching spec section before changing behavior.

## Commands

```sh
go build ./...
go test ./... -count=1                         # about 20 s, all 20 packages
go test -race ./... -count=1
go test ./internal/store -run TestGolden -count=1   # one package / one test
go vet ./...
gofmt -l .                                     # must print nothing

# Binaries (bin/ is gitignored)
go build -o bin/ ./cmd/cmgo ./cmd/cmgo-hook
go install ./cmd/...                           # replaces the binaries the live ~/.claude uses

# Acceptance benches (macOS only, use proc_pid_rusage)
python3 -I bench/footprint.py bin/cmgo         # 8 serve processes, phys_footprint <= 100 MB
python3 -I bench/idle-writes.py bin/cmgo [seconds]
python3 -I bench/no-node.py bin/cmgo
python3 -I bench/hook-latency.py bin/cmgo-hook [runs]   # p95 <= 10 ms; noisy when load average is high
```

Golden files in `testdata/golden/` are TS output. Regenerate them by hand only, never from `go test`:

```sh
TS_REPO=/Users/lang/GolandProjects/github.com/lang315/context-mode node tools/golden-gen/gen.mjs   # also gen-hooks.mjs, gen-memory.mjs
```

## Architecture

Two binaries share one SQLite file, `$CMGO_DIR/cmgo.db` (`CMGO_DIR` defaults to `~/.claude/cmgo`):

- **`cmgo`** (`cmd/cmgo`): subcommands `serve` (MCP server over stdio, one process per Claude session), `install`/`uninstall` (edits `settings.json` hooks and the user MCP entry in `~/.claude.json`), `doctor`, `upgrade`, `import` (one-time import of TS data), `version`, and `hook <event>` for manual runs.
- **`cmgo-hook`** (`cmd/cmgo-hook`): what `install` registers for every hook. It links only `internal/hookcmd` and its deps, never `mcpserver`, `executor` or `fetch`, so each hook fire starts fast. Keep it that way: do not import those packages from anything `hookcmd` reaches.

Data flow:

- **MCP tools** (`internal/mcpserver`): `ctx_execute`, `ctx_execute_file`, `ctx_batch_execute`, `ctx_search`, `ctx_fetch_and_index`, plus `ctx_stats`, `ctx_doctor`, `ctx_upgrade`, `ctx_purge`. Large output is chunked and indexed into FTS5 (`internal/store`) instead of returned. `internal/executor` runs code in process groups with an output cap. `internal/fetch` guards SSRF at dial time and converts HTML to Markdown. `internal/security` applies the `Bash(...)`/`Read(...)` deny rules from Claude settings files.
- **Hooks** (`internal/hookcmd`, `hookHandlers` map): `pretooluse` routes or blocks tools (`internal/routing`, `internal/security`). `posttooluse`, `posttoolusefailure`, `userpromptsubmit` and `stop` capture session events into `session_events` (`internal/capture`). `precompact` records a compaction in the store (`CompactSession`). `sessionstart` with source `compact` or `resume` builds the resume directive from the session's events (`internal/memory`); `compact` also adds the auto-memory injection. `statusline` prints the status line (`internal/stats`). A hook always exits 0. Errors go to `$CMGO_DIR/hook-errors.log`. On a DB busy timeout the hook drops its events instead of blocking Claude Code.
- **Store** (`internal/store`): ncruces `go-sqlite3` (no cgo) with FTS5. Tables: `sources`, `chunks` + `chunks_trigram` (FTS5), `vocabulary`, `session_meta`, `session_events`, `tool_stats`, `stats_totals`. Schema changes are appended to the `migrations` slice in `store.go`; each entry moves `user_version` up by one. Never edit an existing entry.
- **Config** (`internal/config`): project dir is `CMGO_PROJECT_DIR`, then `CLAUDE_PROJECT_DIR`, then cwd, normalized to absolute and lowercased (as the TS hash input is). `CLAUDE_CONFIG_DIR` overrides `~/.claude`. Functions take injected `getenv`/`getwd`/`Now` so tests never touch the real home. Other switches: `CMGO_FETCH_STRICT=1` (fetch blocks private IP ranges), `CMGO_BASH_NUDGE_MIN_COMMAND_BYTES` (routing nudge threshold, 1-100000, else off), `CMGO_BASH_READ_MIN_BYTES` (Bash read guidance threshold, 1-100000000; unset or anything else = off, `16384` = the old default), `CMGO_PY_COUNTERS` (Python read counters in `ctx_execute`, `0` = off), `CMGO_SOURCE` (source checkout for `upgrade`), `CONTEXT_MODE_DIR` (TS data dir for `import`).

Hook stdin JSON is parsed with `internal/hookio`, which keeps key order and JS string semantics (`JSLen`, `QuoteString`) so the output matches TS exactly. Do not switch it to `encoding/json` maps.

## Parity tests

Golden tests (`golden_test.go` in `store`, `mcpserver`, `fetch`, `routing`, `capture`, `memory`) compare Go output with the TS goldens. The only allowed differences are listed with IDs in `testdata/golden/KNOWN-DIFFS.md`, `HOOKS-KNOWN-DIFFS.md` and `MEMORY-KNOWN-DIFFS.md`. A test fails if a listed difference goes away, so update the list in the same change that fixes or adds a difference.

## Rules for this repo

- cmgo is installed live on this machine: `~/go/bin/cmgo` and `cmgo-hook` serve the real `~/.claude`. When you run a binary by hand, point `CMGO_DIR`, `HOME` and `CLAUDE_CONFIG_DIR` at a scratch dir. Run `cmgo install`, `cmgo import` or `go install` only when the owner asks.
- The TS repo is reference only. Never run `npm install`, `scripts/postinstall.mjs` or `start.mjs` there: they write the real `~/.claude`.
- In a shell command that also runs `pkill`/`pgrep`, build the process marker at runtime. A literal marker matches the shell itself.
- `python3 -I` still writes `bench/__pycache__`. It is excluded in `.git/info/exclude`; never commit it.
- Import grouping: stdlib first, then a blank line, then module imports (`github.com/lang315/context-mode-go/...` with third-party).
- Commits and PRs are in English. Every PR body carries evidence: `go vet`, the `go test` result, the `-race` tail, and bench output when performance-relevant code changed.
