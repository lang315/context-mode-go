# cmgo

A Go rewrite of the [context-mode](https://github.com/lang315/context-mode) Claude Code plugin. It keeps large tool output out of the model's context window: code runs in a sandbox, big output is indexed into SQLite FTS5, and the model gets a short answer or search results instead of raw bytes.

cmgo is a personal tool: one user, Claude Code only, macOS (Linux code paths exist but are untested). Tool names, parameters, thresholds and output text match the TypeScript plugin (commit `a5e6dd31`), so existing routing rules and model habits keep working. The tool prefix changes from `mcp__plugin_context-mode_context-mode__` to `mcp__cmgo__`.

## Why

| | TS plugin (node) | cmgo |
|---|---|---|
| Hook p50 / p95, `pretooluse` | 73.0 / 80.3 ms | 4.8 / 5.6 ms |
| Hook p50 / p95, `posttooluse` | 66.5 / 70.5 ms | 5.8 / 6.9 ms |
| Hook p50 / p95, `sessionstart` | 71.0 / 78.2 ms | 6.1 / 8.8 ms |
| MCP server RSS, per session | 56-59 MB | 17-19 MB |
| Disk writes while idle | — | 0 bytes in 60 s |

Measured on macOS arm64 at a load average of about 3 (`bench/hook-latency.py`, 30 runs per case for the comparison). Eight `cmgo serve` processes use 78 MB `phys_footprint` in total (`bench/footprint.py`). Two hooks fire on every tool call, so each call saves about 130 ms of hook time.

## Requirements

- Go 1.27.2 or later, with `~/go/bin` (or `$GOBIN`) on `PATH`
- Claude Code with the `claude` CLI on `PATH` (used to register the MCP server)
- No Node.js and no cgo: SQLite is [ncruces/go-sqlite3](https://github.com/ncruces/go-sqlite3)

## Install

```sh
git clone git@github.com:lang315/context-mode-go.git
cd context-mode-go
go install ./cmd/cmgo ./cmd/cmgo-hook

cmgo install --dry-run                # print the new settings.json, write nothing
cmgo install --disable-ts-plugin      # write it, register the MCP server, turn off the TS plugin
cmgo doctor                           # every line [OK]; exits 1 on any [FAIL]
```

`cmgo install`:

- adds its 7 hooks (PreToolUse, PostToolUse, PostToolUseFailure, UserPromptSubmit, SessionStart, Stop, PreCompact) to `~/.claude/settings.json`, keeping your own hooks in place and in order;
- saves the original file as `settings.json.cmgo-bak`;
- registers `cmgo serve` as a user MCP server with `claude mcp add --scope user`;
- sets `statusLine` only when none is set; otherwise it prints a notice and leaves yours alone;
- writes the skills below to `~/.claude/skills/` (or `$CLAUDE_CONFIG_DIR/skills/`). A skill of yours with the same name, or a cmgo skill you edited, is left alone.

Open a new Claude Code session, or run `/reload-plugins`, to pick up the MCP server.

### Bring over TS data (optional, once)

```sh
cmgo import --dry-run    # counts only
cmgo import
```

`cmgo import` copies the TS session events and indexed sources that are still inside cmgo's retention windows (7 days for sessions, 14 days for sources). It reads copies of the TS databases, never the originals. A second run is refused unless you pass `--force`.

### Uninstall

```sh
cmgo uninstall           # removes cmgo's hooks, MCP entry, unedited skills and (if it is cmgo's) statusLine
```

Re-enable the TS plugin in Claude Code if you want it back. The data in `~/.claude/cmgo/` is left in place.

## Commands

| Command | What it does |
|---|---|
| `cmgo serve` | MCP server over stdio, one process per Claude Code session (Claude Code starts it) |
| `cmgo install` / `uninstall` | Edit `settings.json` hooks and the user MCP entry; both take `--dry-run` |
| `cmgo doctor` | Check version, storage, DB/FTS5, executor, runtimes, hooks, MCP registration, skills, statusline, TS plugin |
| `cmgo upgrade` | `git pull` and `go install` from `--source` or `$CMGO_SOURCE` (else `go install …@latest`), then `cmgo install`; takes `--dry-run` |
| `cmgo import` | One-time import of TS data; `--from`, `--dry-run`, `--force` |
| `cmgo version` | Print the version |
| `cmgo-hook <event>` | What the hooks run; also `cmgo-hook statusline` and `cmgo-hook version` |

## Skills

| Skill | What it does |
|---|---|
| `/ctx-stats` | Shows `ctx_stats` output in full |
| `/ctx-doctor` | Runs `ctx_doctor` (or `cmgo doctor`) and repeats the fix hints |
| `/ctx-search` | Searches the knowledge base and session memory with `ctx_search` |
| `/ctx-upgrade` | Runs the `ctx_upgrade` command and shows the steps as a checklist |
| `/ctx-purge` | Asks for the scope, warns, then calls `ctx_purge` |

Typing `ctx stats`, `ctx doctor`, `ctx upgrade` or `ctx purge` in the chat also works, through the routing block that SessionStart adds.

Each installed `SKILL.md` ends with a `cmgo-skill` line that holds the sha256 of the text above it. `cmgo install` updates and `cmgo uninstall` removes only files whose hash still matches.

## MCP tools

| Tool | Purpose |
|---|---|
| `ctx_execute` | Run code (shell, JS, TS, Python, Go, Rust, Ruby, Perl, …) in a sandbox; only what it prints comes back |
| `ctx_execute_file` | Same, with a file's content passed to the code |
| `ctx_batch_execute` | Run several commands, index their output, answer search queries in one call |
| `ctx_fetch_and_index` | Fetch URLs (SSRF-guarded), convert HTML to Markdown, index it |
| `ctx_search` | BM25 + trigram search over indexed content; `sort: "timeline"` adds session memory and auto-memory |
| `ctx_stats` | Bytes kept out of context, per tool, for this session and lifetime |
| `ctx_doctor`, `ctx_upgrade`, `ctx_purge` | Diagnostics, upgrade instructions, delete a session's or project's data |

While `cmgo serve` is running, the PreToolUse hook blocks `curl`/`wget`, inline HTTP, build tools and `WebFetch`, and points the model at these tools instead. Large `Read`s and `Grep` get a one-time guidance note. Hooks fail open: a hook error never blocks Claude Code.

## Data

Everything lives in `$CMGO_DIR` (default `~/.claude/cmgo`):

- `cmgo.db`: one SQLite file for indexed content, session events, and tool stats
- `run/`: MCP-ready sentinels and guidance markers
- `hook-errors.log`: one line per hook error; absent when there are none

Session events are kept for 7 days after a session's last event and capped at 1000 per session. Indexed sources are kept for 14 days.

## Differences from the TS plugin

- Claude Code only; no other agent adapters.
- Fixes for TS behaviors that never worked on Claude Code: the "Last Request" section after compaction, newest-first timeline search, and auto-memory reading Claude Code's real memory directory.
- `ctx_stats` reports measured bytes (output produced minus output returned), not estimates. It drops the projections. For JS/TS `ctx_execute` runs, the bytes the code read through `fs` or the network count as produced, as in TS; reads in shell, Python and other languages are not counted.
- Not ported: the PreCompact XML snapshot, `ctx_insight`, token and cost reports from transcripts.

The full list, with reasons, is in the specs under `docs/superpowers/specs/` and in `testdata/golden/*KNOWN-DIFFS.md`.

## Development

See [CLAUDE.md](CLAUDE.md) for build and test commands, the architecture, parity tests against the TS goldens, and the rules for working in this repo. In short:

```sh
go vet ./... && go test ./... -count=1 && go test -race ./... -count=1
python3 -I bench/hook-latency.py bin/cmgo-hook
```
