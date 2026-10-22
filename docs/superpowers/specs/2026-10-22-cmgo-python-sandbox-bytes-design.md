# cmgo: count Python sandbox reads in ctx_execute `bytes_kept`

Date: 2026-10-22. Status: draft for owner approval. No code until approved.
Extends: core spec §2.2 (intentional differences), stats spec §1.3 (raw bytes).

## 0. Context and goals

`ctx_execute` raw bytes = stdout + stderr + `Result.SandboxBytes`. Only the JS/TS
wrapper fills `SandboxBytes` (`__CM_FS__`/`__CM_NET__` on stderr, stripped by
`takeCounters` in `internal/executor/script.go`). For every other language the
code can read a 5 MB JSON and print 3 lines, and stats show `kept 0`.

Real case: session 15c05b48 ran a Python `ctx_execute` that fetched
githubstatus.com JSON with `urllib` and printed a few lines; `tool_stats`
shows kept 0, returned 422. The saving was real but not measured.

### Understanding (stated by the owner via "context" / my assumptions)

- Said: Python first, shell and the rest only if feasible and honest; "not
  measured" beats a guess; `ctx_execute_file` and `ctx_batch_execute` keep
  their raw definitions unless there is a reason; counters trusted only from
  the instrumented wrapper; startup overhead measured.
- Assumed: the metric means "bytes the sandbox code read from files or the
  network that did not enter the conversation", the same meaning as the TS
  counters. A lower bound is acceptable; an over-count is not (the replay work
  for Bash guidance used the same rule: 0 over-estimates).
- Assumed: macOS only, python3 from PATH (here 3.12.1), one user.

### Success criteria

1. A Python `ctx_execute` that reads a file with `open`, `pathlib`, or fetches
   with `urllib`/`http.client`/`requests` reports those bytes in `SandboxBytes`.
2. Output is identical to an unwrapped run: stdout, stderr, exit code,
   traceback line numbers, `__name__`, `__file__`, `sys.argv`, `sys.path[0]`.
3. Printing `__CM_FS__:99` (or any text) cannot change a count.
4. Added startup cost per Python run: median <= +3 ms on `print(1)`; no
   `socket`/`ssl` import unless the user code imports them (§5).
5. Unmeasured languages are documented as unmeasured, not guessed.

### Out of scope

`ctx_execute_file`, `ctx_batch_execute`, background runs, children of the
user's code (`subprocess`), shell, and compiled languages (§3).

## 1. Decisions (answers to the six questions)

### Q1. Python mechanism: a `-c` bootstrap, not sitecustomize, not a code prefix

| Option | Verdict | Why |
|---|---|---|
| Code prefix like `jsCounters` | rejected | Shifts traceback lines; breaks `from __future__` (must be first) and a leading encoding line/docstring |
| `sitecustomize` via `PYTHONPATH` | rejected | Hides the user's or venv's own `sitecustomize` unless chained; `PYTHONPATH` is inherited by every child Python, which would then print counters into captured output; ignores `python -I` |
| Bootstrap run with `python3 -c BOOT <countsfile> <script>` | chosen | `script.py` is still written verbatim, so lines and file name are unchanged; no env var, no extra file, no inheritance |

Bootstrap behavior (constant in `internal/executor/pycounters.go`, about 60
lines of Python):

1. Read `script.py` with the original `open` (before any patch).
2. Restore the ordinary script environment: `sys.argv = [script]`,
   `sys.path[0] = dirname(script)` (with `-c` it would be `''`), run in the
   `__main__` module namespace with `__file__ = script`, `__spec__ = None`.
   `multiprocessing` spawn then re-imports the same `script.py`.
3. Install the hooks (below), `atexit.register(write_counts)` first, so it runs
   last (LIFO) and sees reads done by the user's own atexit handlers.
4. `exec(compile(src, script, "exec", dont_inherit=True), ns)`.
5. `sys.excepthook` wrapper drops the bootstrap frame (`tb.tb_next`) so an
   uncaught error or `SyntaxError` prints exactly what a plain script prints.
   `SystemExit` passes through unchanged, so exit codes match.

The user's `PYTHONSTARTUP`, `PYTHONHOME` etc. stay in `deniedEnv` as today.

**File reads.** Patch `builtins.open` and `io.open` (pathlib's `read_text` and
`codecs.open` go through them). For the common read-only text/binary calls
(path argument, mode `r`, `rt`, `rb`, no `opener`, default buffering) build the
same stack Python builds, `FileIO` subclass counting `readinto` -> `BufferedReader`
-> `TextIOWrapper` (same encoding/errors/newline handling, `mode` and `name`
set as `open` sets them). Every other call (write modes, file descriptors,
`buffering=0`, `opener`) is delegated to the original `open` and is not
counted. The count is "bytes the kernel returned to the process", so reading
one line of a large file counts one buffer (<= 8 KiB over), never the file size.
Imports use `_io.open_code`, so `import json` is not counted.

**Network.** Count bytes received on sockets, not bytes of HTTP bodies:
patch `socket.socket.recv`, `socket.socket.recv_into` and `ssl.SSLSocket.read`
(`SSLSocket.recv`/`recv_into` call `read`, so no double count). This covers
`urllib`, `http.client`, `requests`/urllib3, `httpx` (sync) and any other
blocking socket client with one patch set instead of per-library hooks.
Difference from TS: headers are included (about 0.3 to 1 KB per response) and
local sockets (a database client) count too. Not covered, so lower bound only:
asyncio SSL (MemoryBIO), `mmap`, C extensions that read files themselves
(`sqlite3`, `pandas` C parser), `os.read`.

**Lazy network patching.** Importing `socket` costs +5.4 ms and `ssl` +12.3 ms
(median of 40 `python3 -c` starts here: 19.8 ms bare). The bootstrap therefore
does not import them. A `sys.meta_path` finder wraps the loader for `socket` and
`ssl` and applies the patch right after the module executes; if either is
already in `sys.modules` it is patched at once. A user who never touches the
network pays nothing.

**Failure rule.** Every wrapper catches its own errors and falls back to the
original call; a counting failure must never change the user's program.

### Q5 (before Q2 because it shapes the channel). Trust: a file, not stderr text

Python counts go to `<tmpdir>/.cmgo-counts` (path passed as `argv[1]` of the
bootstrap), written at exit with the original `open`. The executor parses that
file only when it chose the instrumented argv for this run (a boolean returned
by the argv builder, not a string prefix test). stdout and stderr are never
parsed for Python, so nothing needs stripping and a printed `__CM_FS__:99` is
plain output. Tests (§4):

- Python prints `__CM_FS__:99\n__CM_NET__:99` to stdout and stderr: `SandboxBytes`
  is 0 and both streams come back byte for byte.
- Existing `TestRunCountsOnlyInstrumentedRuns` is extended to every language.
- `ctx_execute_file` with Python is not instrumented: the counts file is absent
  and `SandboxBytes` is 0.

Observation, not a change: the JS/TS channel is still stderr text (TS parity),
so a JS user can print `__CM_FS__:99` and inflate it. The owner can ask for the
same file channel for JS later; it is a separate change because it is a
second difference from TS.

### Q2. Shell: not measured

Checked on macOS here: after `cat` of a 30 MB file, `RUSAGE_CHILDREN.ru_inblock`
is 0, so `wait4` gives no read count. `proc_pid_rusage` `ri_diskio_bytesread`
works only for a live process, and the shell's children (`cat`, `curl`) have
exited before the group is reaped; it also misses page-cache hits and all
pipe/network reads. `DYLD_INSERT_LIBRARIES` is denied by `deniedEnv` and
stripped by SIP for system binaries. Linux `/proc/<pid>/io` is not available
(macOS only project). Nothing honest exists, so shell stays "not measured".
For `curl ... | jq` the answer is `ctx_fetch_and_index`, which already counts.

### Q3. Other languages

| Language | Hook available | Decision |
|---|---|---|
| ruby | `ruby -r boot.rb script.rb` (no env, lines kept); patch `File.read`, `IO.read`, `Net::HTTP` | follow-up, only if language mix shows demand; not in this spec |
| perl, php | php `-d auto_prepend_file`, perl `-M`; but file reads are builtins/stream functions that cannot be wrapped without a stream-wrapper override | out of scope: not measured |
| go, rust, csharp | compiled, no runtime hook | out of scope: not measured |
| r, elixir | no cheap hook | out of scope: not measured |

The language mix of `ctx_execute` calls is not recorded per call in `tool_stats`
today, so there is no data to rank Ruby; I did not guess one.

### Q4. `ctx_execute_file` and `ctx_batch_execute`: unchanged

`ctx_execute_file` raw is `file size + stdout + stderr`, which already counts
the read exactly. Wrapping it would double count. `ctx_batch_execute` stays
"sum of command outputs" (core spec §2.2 already records the missing preload).

### Q6. Overhead protocol

Measure before and after with the same harness, same machine, load average
below 5 (as the hook benches): 100 runs each of `print(1)` through
`Executor.Run`, report median and p95, plus the same for a script that imports
`json` and one that imports `urllib.request` (lazy patching applies at import).
Budget: median <= +3 ms for `print(1)`. If lazy patching is not enough, fall
back to patching only `open` and dropping the network patch; that is a
decision for the owner at that point, not made silently. Results go in the PR.

## 2. Code changes (for the plan)

- `internal/executor/pycounters.go` (new): `pyBootstrap` constant,
  `pyArgv(python, countsFile, script) []string`, `readPyCounts(path) int64`
  (missing or malformed file = 0, no error).
- `internal/executor/run.go`: for `Python` with `r.FilePath == ""` and not
  `Background`, use the bootstrap argv and read the counts file after `spawn`.
  Set `res.SandboxBytes`. `prepareCode` and `writeScript` stay as they are.
- `Result.SandboxBytes` doc comment: "JS/TS and Python".
- `internal/mcpserver/execute.go`: no change (already adds `SandboxBytes`).
- Docs: core spec §2.2 new row; stats spec §1.3 row text ("Reads in other
  languages are not counted" -> "Python: ...; others are not counted");
  `testdata/golden/KNOWN-DIFFS.md` By-design entry `executor:python-counters`
  with a test that fails if the entry is removed (same pattern as
  `TestKnownDiffsRowExists`).

## 3. Rollback and switch

`CMGO_PY_COUNTERS=0` runs Python exactly as today (no bootstrap). Default on.
Rule: if the fidelity corpus (§4) ever shows a stdout/stderr/exit difference in
a real session, the owner can flip the switch without a release, then we fix.
(Owner decision: keep the switch, or drop it as YAGNI. I recommend keeping it:
it is five lines and the change wraps every Python run.)

## 4. Testing

Tests never touch the real home; they use `t.TempDir()` and `httptest`.

1. Counts: file read via `open`, `Path.read_text`, `open(..., "rb")`; partial
   read of a large file counts at most one buffer; `urllib.request.urlopen`
   against `httptest.NewServer` and `NewTLSServer` (unverified context);
   `http.client`; a write-mode `open` counts 0.
2. No double count: TLS response counted once (`SSLSocket.read` path).
3. Trust: spoof lines on stdout and stderr; `ctx_execute_file`; shell.
4. Fidelity corpus, unwrapped vs wrapped, equal stdout, stderr, exit code:
   `SyntaxError`; nested uncaught exception (line numbers); `sys.exit(3)`;
   `from __future__ import annotations` as first statement; `__name__ ==
   "__main__"`; `__file__`, `sys.argv`, `sys.path[0]`; reading stdin;
   `multiprocessing` spawn of a top-level function; user `atexit` handler that
   reads a file; a user `sitecustomize` in the project dir still honored.
5. Lazy patch: a script that never imports `socket` leaves `socket` out of
   `sys.modules`; `import json` adds 0 to the count.
6. Timeout (SIGKILL): counts are lost, same as JS; test pins 0 and exit 1.
7. Live before/after in the PR: one Python call fetching a JSON over `urllib`,
   `ctx_stats` kept 0 before and the byte count after (scratch `CMGO_DIR`).

## 5. Known limits (stated in the docs, not hidden)

Counts are a lower bound. Lost on SIGKILL/timeout and `os._exit`. Child
processes (`subprocess`) are not instrumented. Network counts include headers
and local sockets. Background runs return 0.

## 6. Decisions I need from the owner

1. Bootstrap via `python3 -c` (recommended) vs sitecustomize.
2. Keep `CMGO_PY_COUNTERS=0` switch (recommended).
3. Accept socket-level network counting (headers included) instead of body-only.
4. Shell and compiled languages stay "not measured"; Ruby as a separate
   follow-up only when demand is shown.
5. JS/TS spoofing via stderr text: leave as is (TS parity) or give it the same
   file channel as a separate change.
