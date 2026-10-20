# cmgo core: design (sub-project 1 of 5)

Date: 2026-10-20. Status: draft for owner review.

`cmgo` is a Go rewrite of [context-mode](https://github.com/lang315/context-mode) for one user on Claude Code. The reference implementation is the TypeScript fork at commit `a5e6dd31` (`1.0.169-lang315.4`). In this document, `S:` means `src/server.ts`, `E:` means `src/executor.ts` and `ST:` means `src/store.ts` at that commit.

## 0. Context and goals

### What the owner asked for
- **Use:** a personal tool, for Claude Code only.
- **Main goal:** performance and resource use.
- **Features kept across all 5 sub-projects:**
  - the 5 core tools;
  - routing and command blocking;
  - session memory and compaction;
  - stats and statusline;
  - doctor and upgrade.
- **Data:** start with a fresh store, plus a one-time import of old data (sub-project 5). The Go and TS versions do not share data files.
- **Architecture:** approach A. One binary, one process per session, all processes on one shared SQLite DB.
- **Code location:** a new repo, `lang315/context-mode-go`.

### Sub-projects
Each sub-project has its own spec and plan.

1. **Core (this document):** binary, storage, the 5 tools, executor, fetch, security.
2. **Hooks:** routing, command blocking, session event capture.
3. **Session memory and compaction:** resume after compaction, timeline search over session events.
4. **Stats and statusline.**
5. **Doctor, upgrade, and the one-time import of TS data.**

### Measured baseline (2026-10-20, macOS arm64)

| Measure | TS (Node) | Go probe |
|---|---|---|
| Sum of `phys_footprint`, 8 MCP processes | 380.5 MB (47.6 per process) | 53.1 MB (6.6 per process; ncruces driver, after initialize plus 1 tool call) |
| Sum of RSS, 8 MCP processes | 512.5 MB | 118.0 MB |
| Process start to exit | 24.2 ms p50 (`node -e 0`) | 2.8 ms p50 |

RSS counts shared code pages once per process. That is why the success criteria below use `phys_footprint`, which does not.

Tool usage over all session DBs:

| Tool | Calls |
|---|---|
| `ctx_execute` | 1,896 |
| `ctx_batch_execute` | 1,134 |
| `ctx_execute_file` | 361 |
| `ctx_search` | 225 |
| `ctx_fetch_and_index` | 73 |
| `ctx_index` | 1 |

### Success criteria for this sub-project
All measured on this machine.

1. Sum of `phys_footprint` for 8 `cmgo serve` processes, each after `initialize` and one call to each tool, is at most 100 MB.
2. Disk writes from 8 idle `cmgo serve` processes over 60 s (`proc_pid_rusage` `ri_diskio_byteswritten`) are at most 0.1 MB/min.
3. The 5 tools match the TS output on the golden corpus (§5.2), apart from the documented differences in §2.2.
4. No Node.js is needed to run any tool except JavaScript and TypeScript code inside `ctx_execute`.

Hook latency (p95 ≤ 10 ms) is a sub-project 2 criterion and is not tested here.

### Out of scope here
- Hooks, session events, `sort: "timeline"` over session events, auto-memory search: sub-projects 2 and 3.
- `ctx_stats`, `stats-*.json`, byte accounting: sub-project 4.
- `ctx_doctor`, `ctx_upgrade`, import: sub-project 5.
- `ctx_index` (1 use in the data) and file-backed source refresh, which only `ctx_index` creates.
- Windows, and every platform other than Claude Code.

## 1. Architecture and components (approved)

### Binary
One binary, `cmgo`:
- `cmgo serve`: the MCP server over stdio.
- `cmgo version`.
- `cmgo hook <event>` is reserved for sub-project 2.

### Packages

| Package | Responsibility |
|---|---|
| `cmd/cmgo` | Entry point and subcommand dispatch |
| `internal/mcpserver` | Registers the 5 tools with `github.com/modelcontextprotocol/go-sdk` v1.8.0 and formats output text |
| `internal/executor` | Runtime detection, script files, env filtering, process-group spawn and kill, output cap, background runs |
| `internal/store` | SQLite through `github.com/ncruces/go-sqlite3` v0.35.6 (no cgo), with `fts5.Register`. Also schema, chunkers, search and snippets |
| `internal/fetch` | SSRF guard at dial time, redirects, HTML to Markdown with `github.com/JohannesKaufmann/html-to-markdown/v2` v2.5.2 (base, commonmark, strikethrough and table plugins), JSON pretty-printing |
| `internal/security` | `Bash(...)` and `Read(...)` deny rules from settings files, command splitting, extraction of shell commands embedded in other languages |
| `internal/config` | Paths and environment |

### Data and install
- **Data:** a single file, `$CMGO_DIR/cmgo.db`. `CMGO_DIR` defaults to `~/.claude/cmgo`.
- **Install:**
  - Build and install: `go install ./cmd/cmgo`.
  - Register: `claude mcp add --scope user cmgo -- ~/go/bin/cmgo serve`.
  - There is no plugin packaging. Tools appear as `mcp__cmgo__ctx_*`, so the TS plugin can run side by side while testing. The TS plugin is turned off once sub-project 2 ships.
- **Project dir:** `CMGO_PROJECT_DIR`, then `CLAUDE_PROJECT_DIR`, then `os.Getwd()`. On darwin the path is normalized to absolute and lowercased, as the TS hash input is.

### Why ncruces
- No cgo.
- With two writer processes it split inserts evenly (about 11k each).
- mattn split them 24k / 6k and needs the `sqlite_fts5` build tag.
- modernc starved the second writer.

Cost: about 6 MB more footprint across 8 processes than mattn.

## 2. Tool behavior

### 2.1 Kept exactly
Tool names, parameter names, defaults, thresholds and output text are kept, so existing `CLAUDE.md` routing rules and model habits still work. The flows below are the contract. File references point at the TS code to port.

**Thresholds and constants:**

| Constant | Value | Use |
|---|---|---|
| `INTENT_MIN_BYTES` | 5000 | `intent` search only applies when output is larger than this |
| `INDEX_MIN_BYTES` | 102400 | Output larger than this is indexed instead of returned |
| `OUTPUT_HARD_CAP` | 100 MB | stdout+stderr combined. Past it the tree is killed and the marker `\n[output capped at 100MB — process killed]` is added to stderr |
| `CODE_ECHO_MAX` | 2000 chars | Length of the code echo |
| `BATCH_SNIPPET_MAX` | 3000 | Snippet length in batch query results |
| `BATCH_OUTPUT_CAP` | 80 KB | Total size of batch query results |
| `SEARCH_SNIPPET_MAX` | 1500 | Snippet length in `ctx_search` |
| `SEARCH_OUTPUT_CAP` | 40 KB | Total size of `ctx_search` output |
| `CHUNK_MAX_BYTES` | 4096 | Largest chunk |
| `FETCH_TIMEOUT` | 30000 ms | Fetch timeout |
| `FETCH_BODY_CAP` | 50 MB | Largest fetched body |
| `FETCH_TTL_DEFAULT` | 24 h | Fetch cache freshness |
| `FETCH_MAX_REDIRECTS` | 5 | Redirect hops |
| `FETCH_PREVIEW` | 3072 chars (single URL), 384 (batch) | Preview length |
| `SOURCE_RETENTION` | 14 days | Source age before cleanup |
| `RRF_K` | 60 | Rank fusion constant |
| `BM25` | `bm25(chunks, 5.0, 1.0)` | Ranking weights |

**`ctx_execute`** (S:1647-1952)
- **Schema:** `language` (an enum of 12 languages), `code`, `timeout` (coerced number, optional), `background` (bool; also accepts the strings "true" and "false"), `cwd`, `intent`.
- **Default timeout:** none.
- **Order of work:**
  1. Deny check.
  2. Code echo prefix (S:1511-1527).
  3. Run the code.
  4. Classify the result (`exit-classify.ts:15-33`). Soft fail: language is shell AND exit code is 1 AND stdout is not blank. That case returns stdout and is not an error.
  5. Route the output:
     - `intent` is set and output is over `INTENT_MIN_BYTES`: intent search (S:1982-2036), which uses plain-text chunking.
     - Otherwise output over `INDEX_MIN_BYTES` is indexed with Markdown chunking. If it is an error, the fixed intent `"errors failures exceptions"` is used instead.
     - Otherwise: the echo plus the output.
- **Source label:** `execute:<lang>`, with `:error` added for real errors. Indexing again under the same label replaces the old content.
- **Timeout and background messages** (whitespace exactly as in S:1840-1952):
  - Backgrounded, with output: echo + output + `\n\n_(process backgrounded after Nms — still running)_`.
  - Timed out, partial output: echo + output + `\n\n_(timed out after Nms — partial output shown above)_`.
  - Timed out, no output: an error with `Execution timed out after Nms\n\nstderr:\n<stderr>`.
  - Exit code is not 0 and the run is not a soft fail: an error with `Exit code: N\n\nstdout:\n<stdout>\n\nstderr:\n<stderr>`.
  - Empty stdout on success is shown as `(no output)`.

**`ctx_execute_file`** (S:2042-2230)
- **Schema:** `path`, `language`, `code`, `timeout`, `intent`. There is no `cwd` or `background`.
- **Path check:** the resolved path and its realpath must be inside the project dir, unless a `Read(glob)` allow rule matches. `Read(glob)` deny rules are then checked against the raw, resolved and realpath forms.
- **Per-language prefixes** (E:747-784) define `FILE_CONTENT_PATH`, `file_path` and `FILE_CONTENT`. Rust and Elixir use the lowercase `file_content`. Go wraps the code in a full `package main`.
- **Echo:** starts with a `path=<path>\n` line.
- **Timeout text:** `Timed out processing <path> after Nms`.
- **Source label:** `file:<path>`.

**`ctx_batch_execute`** (S:3678-3893, S:1560-1641, S:1371-1418)
- **Schema:**
  - `commands`: `[{label, command}]`. Also accepts a JSON string; a bare string becomes `cmd_<i>`.
  - `queries`: string[], lifted from a JSON string or a bare string.
  - `timeout`, `concurrency` (1-8, default 1), `cwd`, `query_scope` (`batch` or `global`).
- **Running:** each command runs as shell, with a deny check per command.
- **Section format** (S:1529-1551):
  ```
  # <label>

  $ <cmd>

  <stdout>
  <stderr>
  ```
  `<cmd>` has whitespace collapsed and is cut to 500 chars with `…`. Exit codes are ignored.
- **Timeouts:**
  - `concurrency` = 1: the timeout is one shared budget. Commands left when it runs out get `(skipped — batch timeout exceeded)`.
  - `concurrency` > 1: a timeout per command, giving `(timed out after Nms)`. An executor failure gives `(executor error: msg)`.
- **Indexing:** the joined output is indexed with Markdown chunking under `batch:<labels joined by ",">`, cut to 80 chars.
- **Queries:** `searchWithFallback(q, 3, scope)`. `query_scope=batch` matches the source label exactly.
- **Output:** the header and sections exactly as in S (`Executed N commands …`, `## Commands`, `## Indexed Sections`, `## <query>` / `### <title>`, the tip line, `Searchable terms for follow-up:`).

**`ctx_search`** (S:2553-2806)
- **Schema:**
  - `queries`. No legacy `query`: TS reads it, but its zod schema has no such field, so the SDK strips it and TS answers `Error: provide query or queries.` (golden cases t18/t19).
  - `limit` (default 3; the effective value is `min(limit, 2)`, and 1 after the soft cap).
  - `source` (LIKE match), `contentType` (`code` or `prose`), `sort` (`relevance` or `timeline`).
- **Flood guard:** 60 s window. The soft cap applies when the count goes over 3. A count over 8 is blocked with `BLOCKED: N search calls in Xs…`.
- **Output:** each query section is `## q\n\n--- [origin | YYYY-MM-DD HH:MM | source] ---\n### title\n\n<snippet>`. Results are joined by `\n\n`, queries by `\n\n---\n\n`. The total stops at `SEARCH_OUTPUT_CAP`. The suffix is the throttle line or the warning.
- **Errors:**
  - An empty store in relevance mode returns the TS "Knowledge base is empty…" error.
  - No query returns `Error: provide query or queries.`.

**`ctx_fetch_and_index`** (S:2838-3672)
- **Schema:** `url`, `source`, `requests: [{url, source?}]` (takes priority), `concurrency` (1-8, capped at the CPU count), `force`, `ttl` (ms; `0` bypasses the cache).
- **Cache:** the key and the source label are `source::url` when `source` is given, otherwise `url`. A source is fresh if `now - indexed_at < ttl`. A cache hit reports `chunkCount × 1600` as its size.
- **Content types:**
  - `json` and `+json`: pretty-printed, then indexed with JSON chunking. If it cannot be parsed, it is treated as text.
  - `text/html` and `xhtml`: converted to Markdown with `script, style, nav, header, footer, noscript` removed (ATX headings, fenced code, GFM tables), then Markdown chunking.
  - Anything else: plain-text chunking.
- **Errors:** an HTTP status that is not OK gives `HTTP <status>`. Empty content is an error.
- **Output:** the single-URL and batch texts exactly as in S:3572-3613 and the batch header `fetched N c=E… ok=a cache=b err=c. …`.

### 2.2 Intentional differences from TS

| TS behavior | cmgo | Why |
|---|---|---|
| The JS/TS wrapper adds byte counters (`__CM_FS__`, `__CM_NET__`). Batch commands get a `NODE_OPTIONS` fs preload | The `async function __cm_main(){…}` wrapper (S:1756-1820) is ported without the counters. Batch commands get no preload | The counters only feed stats (sub-project 4). The preload costs a Node start in every batch command |
| Fetch runs in a Node subprocess, with DNS patched to re-check IPs | Fetch runs in-process with `net/http`. IPs are checked in `net.Dialer.Control` against the address actually dialed. `Transport.Proxy = nil` | No Node needed. Checking at dial time closes the DNS-rebinding gap between the lookup check and the connect |
| One content DB per project (`content/<sha256[:16]>.db`). DBs older than 14 days are deleted on every first open | One DB with a `project` column. Sources older than 14 days are deleted at most once per 24 h (time kept in `meta`) | One file instead of hundreds. Startup writes nothing on most days |
| `mmap_size` 256 MB | No mmap | mmap pages raise footprint and RSS. The OS page cache is enough |
| Timeline sort merges session events and auto-memory | Content store only, ordered by `indexed_at` | Session events arrive in sub-project 3 |
| Antigravity timeout, Windows paths, the `project` parameter, `CONTEXT_MODE_*` env vars | Dropped | Claude Code on macOS/Linux only |
| On `SQLITE_BUSY`, retry after 100/500/2000 ms | `busy_timeout=30000` for normal statements. The WAL switch runs once in `Open` with its own retry for up to 30 s | Switching to WAL needs an exclusive lock that SQLite refuses with `SQLITE_BUSY` without consulting `busy_timeout`. Re-applying it per connection made a new session fail to start while another session was writing |
| `Read()` deny rules are case-sensitive on darwin (S:1222 passes `platform === "win32"`) | Case-insensitive on darwin, like the `Bash()` rules | On case-insensitive APFS, a case variant of a denied path would otherwise be readable |

## 3. Storage and search

### 3.1 Schema
`PRAGMA user_version = 1`. Per-connection pragmas: `synchronous=NORMAL`, `busy_timeout=30000`, `foreign_keys=ON`. `journal_mode=WAL` persists in the file and is set once in `Open`: read the mode (no lock), and only if it is not `wal`, switch it, retrying on `SQLITE_BUSY` for up to 30 s. Each process keeps one connection open for its whole life, so nothing is reopened on a timer.

```sql
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE sources (
  id INTEGER PRIMARY KEY,
  project TEXT NOT NULL,
  label TEXT NOT NULL,
  chunk_count INTEGER NOT NULL,
  code_chunk_count INTEGER NOT NULL,
  indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (project, label)
);
CREATE VIRTUAL TABLE chunks USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'porter unicode61');
CREATE VIRTUAL TABLE chunks_trigram USING fts5(
  title, content, source_id UNINDEXED, content_type UNINDEXED,
  tokenize = 'trigram');
CREATE TABLE vocabulary (word TEXT PRIMARY KEY);
```

- **Re-index:** indexing a label that already exists deletes its chunks and inserts the new ones in one transaction (ST:1102-1123).
- **FTS optimize:** `INSERT INTO chunks(chunks) VALUES('optimize')` (and the same for `chunks_trigram`) every 50 index calls, counted per process.
- **Vocabulary:** words split on `[^\p{L}\p{N}_-]+`, at least 3 chars, not stopwords. Inserted with `INSERT OR IGNORE`.
- **Retention:** at `serve` start, read `meta.last_cleanup`. Delete sources older than 14 days only if that value is more than 24 h old.
- **Schema upgrades:** each later sub-project adds tables through a `user_version` step. Steps are numbered and applied in a transaction.

### 3.2 Chunking (ST:1699-2113)
Ported exactly. Golden tests cover all three chunkers.

- **Markdown:**
  - Split on H1-H4 and on horizontal rules (`^[-_*]{3,}\s*$`).
  - Title is the heading stack joined with ` > `, or `Untitled`.
  - Fenced code blocks are never split.
  - `hasCode` is set when a line starts with three backticks.
  - Chunks over `CHUNK_MAX_BYTES` are split on paragraphs, titled `title (n)`.
- **Plain text:**
  - 3-200 sections separated by blank lines, each under 5000 B: one chunk per section, titled by its first line (at most 80 chars).
  - Otherwise, at most 20 lines: one chunk titled `Output`.
  - Otherwise: windows of 20 lines with a 2-line overlap.
  - Plain-text chunks are always `prose`.
- **JSON:**
  - Key paths are joined with ` > `.
  - Arrays are batched under the byte cap and titled by an identity field (`id`, `name`, `title`, `path`, `slug`, `key`, `label`), or by `[i-j]`.

### 3.3 Search (ST:90-125, ST:1341-1442, S:1300-1367)
Ported exactly. The stopword list is copied from ST:51.

1. **Sanitize:**
   - Porter: strip `'"(){}[]*:^~`, drop AND/OR/NOT/NEAR, dedupe ignoring case, drop stopwords (keep them all if every word is a stopword), quote each term, join with ` OR `.
   - Trigram: the same, but only words of at least 3 chars.
2. **RRF:** run the porter and trigram queries, each with fetch limit `max(2*limit, 10)`. Score each hit `1/(RRF_K + rank + 1)`, keyed by `source::title`.
3. **Proximity rerank:**
   - Title boost: 0.6 for code or 0.3 for prose, times the hit ratio.
   - Span factor for 2 or more terms: `1/(1+minSpan/len)`.
   - Phrase boost: `0.5*min(1, adjacentPairs/4)`.
   - Ties keep the RRF order.
4. **Fuzzy fallback** when there are no results:
   - Correct each word of at least 3 chars (stopwords excepted) against `vocabulary` with Levenshtein distance.
   - Allowed distance: 1 for words up to 4 chars, 2 up to 12 chars, 3 above that.
   - Corrections are kept in an LRU cache of 256 entries.
   - RRF runs once more with the corrected words.
5. **Snippets:**
   - Matches come from `highlight(...,char(2),char(3))` markers, with fallback to `indexOf` on terms longer than 2 chars.
   - Windows of ±300 chars are merged and joined with `…`.
   - Content shorter than the max is returned whole.
6. **Scope:** every query filters `sources.project = <current project>`.
   - `ctx_search` `source` is `label LIKE %x% ESCAPE '\'`.
   - Batch scope is `label = ?`.

## 4. Executor, fetch, security

### 4.1 Runtimes
Detection runs lazily on first use per language, with `exec.LookPath`, and the result is cached for the process.

| Language | Command |
|---|---|
| javascript | `bun run f` if bun is found, else `node f` |
| typescript | `bun run f`, else `tsx f`, else `ts-node f` |
| python | `python3`, else `python` |
| shell | `$SHELL` if it is bash, sh, zsh or dash; else bash; else sh |
| ruby, php, perl, elixir, r | `ruby`, `php`, `perl`, `elixir`, `Rscript` (else `R`) |
| go | `go run f`. Code without `package ` is wrapped in `package main` with `fmt` |
| rust | `rustc f -o bin` (timeout `min(t, 60s)`), then `bin` |
| csharp | `dotnet-script f` |

A missing runtime returns the TS error text for that language.

### 4.2 Running a script
1. Create the script dir with `os.MkdirTemp(<OS temp dir>, ".cmgo-")`. The OS temp dir is found as in TS (E:99-111): `getconf DARWIN_USER_TEMP_DIR` on darwin, `/tmp` on Linux. A `TMPDIR` override that points into the project is therefore ignored.
2. Write the script as `script.<ext>`, using the extension map at E:23-36.
   - Shell scripts get mode 0700 and the first line `export PATH='<parent PATH>'`.
   - PHP gets `<?php` if it is missing.
3. cwd is `cwd`, else the project dir.
4. stdin is nil.
5. Spawn:
   ```go
   cmd := exec.CommandContext(ctx, bin, args...)
   cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
   cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
   cmd.WaitDelay = 2 * time.Second
   ```
   On timeout the result is `exitCode=1, timedOut=true`.
6. stdout and stderr each go to a writer that counts the combined bytes and cancels the run at `OUTPUT_HARD_CAP`.
7. **Background runs** (`background=true` and the timeout fires):
   - The process is not killed.
   - Readers switch to `io.Discard`.
   - The pgid is recorded.
   - The call returns `{exitCode 0, timedOut, backgrounded}`.
   - On `serve` shutdown (stdin EOF, SIGTERM or SIGINT), every recorded group gets `SIGTERM`.
8. The temp dir is removed after the run, unless the run was sent to the background.

### 4.3 Environment
Start from the parent env.

**Removed:**
- `BASH_ENV`, `ENV`, `PROMPT_COMMAND`, `PS4`, `SHELLOPTS`
- `NODE_OPTIONS`, `NODE_PATH`, `PYTHON*`, `RUBYOPT`, `PERL5*`, `ERL_*`
- `GOFLAGS`, `CGO_*`, `RUSTC*`, `PHPRC`, `R_*`, `DOTNET_*`, `CORECLR_*`
- `LD_PRELOAD`, `DYLD_INSERT_LIBRARIES`, `OPENSSL_*`, `CC`/`CXX`/`AR`
- `GIT_SSH*`, `GIT_CONFIG_*`
- every `BASH_FUNC_*` and `COMPlus_*` (case-insensitive)
- the remaining entries of the E:573-745 list, ported as a table

The list is the exact TS list. Patterns such as `PYTHON*` above are shorthand for its entries, not wildcards: a wildcard would also remove variables TS keeps, such as `PYTHONPATH`, `R_LIBS` and `DOTNET_CLI_HOME`.

**Forced:**
- `TMPDIR=<script dir>` (as TS), `HOME`
- `LANG=en_US.UTF-8`, `PYTHONDONTWRITEBYTECODE=1`, `PYTHONUNBUFFERED=1`, `PYTHONUTF8=1`, `NO_COLOR=1`
- `PATH` defaults to `/usr/local/bin:/usr/bin:/bin` when empty
- `SSL_CERT_FILE` is set to the first existing well-known cert path

### 4.4 Fetch
1. Only http and https URLs are accepted.
2. A dialer `Control` hook classifies the IP actually being dialed.
   - **Always blocked:** `169.254/16`, `0/8`, `≥224/4`, `::`, `fe80::/10`, `ff00::/8`.
   - **Private ranges** (`127/8`, `10/8`, `172.16/12`, `192.168/16`, `::1`, `fc00::/7`): blocked only when `CMGO_FETCH_STRICT=1`.
3. Redirects are followed by hand, up to 5 hops. Every hop goes through the same dialer.
4. The body is read through `io.LimitReader` at `FETCH_BODY_CAP+1`. Going over the cap is an error, and so is a `Content-Length` above the cap.
5. A transient DNS error gets the TS retry hint added.
6. Fetches run with a pool of `concurrency` workers. Indexing is serial, in request order.

### 4.5 Security
- **Settings files, in order:**
  1. `<project>/.claude/settings.local.json`
  2. `<project>/.claude/settings.json`
  3. `~/.claude/settings.json`

  Only `permissions.deny` entries shaped `Bash(glob)` or `Read(glob)` are used, plus `permissions.allow` `Read(glob)` for the project-boundary exception.
- **Matching:**
  - `cmd:args` becomes `^cmd(\s args)?$`. Otherwise `*` becomes `.*`.
  - Matching ignores case on darwin.
  - Commands are split on `&& || ; | & \n`, honoring quotes.
  - `$()` and backtick subshells are checked recursively.
- **Non-shell code:** shell commands embedded in the code are pulled out with the `security.ts:796-889` regexes (`os.system`, `subprocess.*`, `exec.Command`, `Command::new`, Ruby `system` and backticks, PHP `shell_exec`/`exec`/`system`/`passthru`/`proc_open`) and checked the same way.
- **Error text:** `Command blocked by security policy: matches deny pattern P`.
- **Settings that cannot be read or parsed** are skipped and a warning goes to stderr. This matches TS, which fails open. A matching rule always blocks.
- **Settings are cached** by file path and mtime, so each call costs one `stat` per file.

## 5. Testing and acceptance

### 5.1 Unit tests (`go test ./...`)
Table tests port the relevant TS cases.

- **store:** query sanitizing, the 3 chunkers, RRF, rerank, Levenshtein and fuzzy correction, snippets, retention gating.
- **executor:**
  - each language runs (the test is skipped when its runtime is missing);
  - a timeout kills the whole group: `sh -c "sleep 30 & sleep 30"` leaves 0 processes;
  - the output cap kills at 100 MB;
  - background runs survive the timeout and get `SIGTERM` on shutdown;
  - the env denylist is applied;
  - the soft-fail rule.
- **security:** deny matching on chains and subshells, embedded-command extraction, Read deny and the boundary check, darwin case folding.
- **fetch:** an `httptest` server, redirect limit, body cap, JSON/HTML/text routing, SSRF classification (strict mode blocks loopback), and a dial-time check that blocks a name whose DNS answer changes between lookups.

### 5.2 Golden parity tests
- **Generator:** `tools/golden-gen/gen.mjs` runs on Node against the TS fork at `$TS_REPO` (pinned to `a5e6dd31`) and writes `testdata/golden/*.json`. It gets its data two ways:
  - **Through exported modules:** `ContentStore` from `$TS_REPO/src/store.ts` (run with `tsx`) for chunks, ranked results and snippets.
  - **Through the real server:** for tool output formats and any function that is not exported, it drives `$TS_REPO/server.bundle.mjs` over stdio. That run uses `HOME`, `CONTEXT_MODE_DIR` and `CLAUDE_PROJECT_DIR` all pointed at a fresh temp dir, so nothing is written to the real `~/.claude`. `start.mjs` and `postinstall` are never run.
- The generated files are committed. The generator runs by hand only, never in `go test`.
- **Corpora** (`testdata/corpus/`): about 10 Markdown files, plain-text logs, 3 JSON files, and 2 HTML pages (one with a table).
- **Compared:**
  - chunk lists (title, content, `hasCode`);
  - sanitized queries;
  - the ranked `source::title` lists for 30 queries;
  - snippets;
  - the formatted outputs of `ctx_batch_execute` and `ctx_search` on fixed commands and queries, with timings and dates masked;
  - HTML to Markdown output.
- **HTML to Markdown:** Turndown and html-to-markdown v2 will differ on some markup. Each difference is either fixed or listed in `testdata/golden/KNOWN-DIFFS.md`. Criterion 3 allows only listed differences.

### 5.3 End-to-end
Two layers:
- **In-process, `cmd/cmgo/serve_test.go`:** runs the real `serve()` over pipes against a temp `CMGO_DIR`. Over JSON-RPC it runs `initialize` and `tools/list` (checking the 5 names), then closes stdin and checks that the server exits. It also checks two things: a foreground command still running at shutdown is killed, and two servers indexing into the same DB at once both succeed.
- **The built binary:** each tool's result text is asserted through the MCP client in `internal/mcpserver`. The built binary itself is driven by the §5.4 scripts, which spawn `cmgo serve` and call every tool once.

### 5.4 Acceptance scripts (`bench/`, run by hand on this machine)
- **`bench/footprint.py`:** starts 8 `cmgo serve` processes, calls each tool once, and prints the sum of `phys_footprint` and of RSS. Must meet criterion 1.
- **`bench/idle-writes.py`:** 8 idle servers for 60 s; sums `ri_diskio_byteswritten`. Must meet criterion 2.
- **`bench/no-node.sh`:** runs the e2e test with `node` and `bun` removed from `PATH`, skipping JavaScript and TypeScript cases. Must meet criterion 4.

The results of the three scripts go in the PR body for this sub-project.

## 6. Decisions taken by default
The owner asked for sections 2-5 to follow the author's recommendations without further questions. These choices were made on that basis and can be revisited in review:

- `ctx_search` keeps the TS effective limit of 2 and the flood guard, unchanged.
- Unreadable settings skip open (TS parity), with a stderr warning.
- Fetch keeps private ranges allowed by default (TS parity). Strict mode is opt-in.
- Byte-counter instrumentation and the batch `NODE_OPTIONS` preload are dropped until sub-project 4.
- The data dir is `~/.claude/cmgo`.
