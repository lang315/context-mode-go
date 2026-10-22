# Python Sandbox Bytes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ctx_execute` Python runs report the bytes the code read from files and sockets in `Result.SandboxBytes`, so `bytes_kept` is no longer 0 for them.

**Architecture:** The executor runs Python as `python3 -c <bootstrap> <countsfile> <script>`. The bootstrap (embedded `pycounters.py`) patches `open`/`io.open` and, lazily, `socket`/`ssl`, execs the unchanged `script.py` in `__main__`, and writes the count to a file at exit. The executor reads that file; stdout/stderr are never parsed.

**Tech Stack:** Go 1.x (stdlib `embed`), Python 3.9+ bootstrap, existing `internal/executor` tests.

**Spec:** `docs/superpowers/specs/2026-10-22-cmgo-python-sandbox-bytes-design.md` (rev 2.1, owner approved)

## Global Constraints

- Python 3.9+ floor; no 3.10+ syntax or API in `pycounters.py`; the file is ASCII only.
- Tests never touch the real home: `t.TempDir()`, `httptest`, scratch `CMGO_DIR`. No `go install`, no `cmgo install`.
- `ctx_execute_file` (FilePath set), background runs and all non-Python languages are not instrumented.
- Switch `CMGO_PY_COUNTERS=0` (read through `e.getenv`) runs Python exactly as before.
- Only `internal/executor`, docs, `testdata/golden/KNOWN-DIFFS.md` and `CLAUDE.md` change; `mcpserver/execute.go` already adds `SandboxBytes`.
- Imports: stdlib first, blank line, module imports. `gofmt -l .` prints nothing.
- Commits in English, end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. No session URLs. Do not push before the PR step.
- Reads of `script.py` itself are not counted; the bootstrap's first statement execs the plain interpreter below Python 3.9.
- Counts are a lower bound; SIGKILL/timeout and `os._exit` lose them (same as JS).

## Review Focus

- A script that reads only the first bytes of a 1 MiB file must count about one buffer, not 1 MiB (Task 2).
- Write-mode `open`, `open(fd)` and `buffering=0` must behave as the original `open` and count 0 (Task 2).
- A script that calls `os._exit(0)` or replaces `sys.excepthook` must still exit with its own code and output; counts may be 0 (Task 4).
- A `sitecustomize` found through the user's `PYTHONPATH` must still run (Task 4).
- Two Python runs at the same time must each report their own count (Task 1).

## File Structure

- Create `internal/executor/pycounters.py`: the bootstrap, embedded.
- Create `internal/executor/pycounters.go`: `pyArgv`, `readPyCounts`, `pyBootstrap` embed, `pyCountersOn`.
- Modify `internal/executor/run.go`: use the bootstrap argv for Python, read the counts file; update `Result.SandboxBytes` comment.
- Create `internal/executor/pycounters_test.go`: helpers and all tests.
- Create `internal/executor/pycounters_bench_test.go`: overhead benchmark (switch off vs on).
- Modify `docs/superpowers/specs/2026-10-20-cmgo-core-design.md` §2.2, `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md` §1.3, `testdata/golden/KNOWN-DIFFS.md`, `CLAUDE.md`.

---

### Task 1: Bootstrap core, counts file, trust and switch

**Files:**
- Create: `internal/executor/pycounters.py`, `internal/executor/pycounters.go`, `internal/executor/pycounters_test.go`
- Modify: `internal/executor/run.go` (Result comment, `Run`)

**Interfaces:**
- Produces: `func pyArgv(python, countsFile, script string) []string`; `func readPyCounts(path string) int64` (missing or malformed = 0); `var pyBootstrap string` (embedded); test helpers `pythons(t) []string`, `pyExec(t, python string) *Executor`, `runPy(t, e, code string) Result`.
- Consumes: `Executor.lookPath`, `Executor.getenv`, `jsonQuote`, `newTestExec`, `run` (existing).

- [ ] **Step 1: Write the failing tests** in `internal/executor/pycounters_test.go`

```go
package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// pythons returns the python3 on PATH and /usr/bin/python3 when it is a different file.
func pythons(t *testing.T) []string {
	t.Helper()
	var out []string
	if p, err := exec.LookPath("python3"); err == nil {
		out = append(out, p)
	}
	if st, err := os.Stat("/usr/bin/python3"); err == nil {
		if len(out) == 0 || !os.SameFile(st, mustStat(t, out[0])) {
			out = append(out, "/usr/bin/python3")
		}
	}
	if len(out) == 0 {
		t.Skip("no python3")
	}
	return out
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// pyExec returns an Executor whose python3 is the given interpreter.
func pyExec(t *testing.T, python string) *Executor {
	t.Helper()
	e := newTestExec(t)
	e.lookPath = func(n string) (string, error) {
		if n == "python3" {
			return python, nil
		}
		return exec.LookPath(n)
	}
	return e
}

func runPy(t *testing.T, e *Executor, code string) Result {
	t.Helper()
	return run(t, e, Request{Language: Python, Code: code, Timeout: 60 * time.Second})
}

func writeData(t *testing.T, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(p, []byte(strings.Repeat("f", size)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A Python run counts the bytes read through open, open(..., "rb") and pathlib.
func TestPyCountsFileReads(t *testing.T) {
	file := writeData(t, 10240)
	q := jsonQuote(file)
	code := "from pathlib import Path\nprint(len(open(" + q + ").read()), len(open(" + q + ", 'rb').read()), len(Path(" + q + ").read_text()))"
	for _, py := range pythons(t) {
		t.Run(py, func(t *testing.T) {
			res := runPy(t, pyExec(t, py), code)
			if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "10240 10240 10240" {
				t.Fatalf("run: %+v", res)
			}
			if res.SandboxBytes != 3*10240 {
				t.Errorf("SandboxBytes = %d, want %d", res.SandboxBytes, 3*10240)
			}
		})
	}
}

// Text printed by the code is plain output: it neither counts nor is removed.
func TestPyIgnoresPrintedCounters(t *testing.T) {
	code := "import sys\nprint('__CM_FS__:99')\nprint('__CM_NET__:99', file=sys.stderr)"
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.SandboxBytes != 0 || res.Stdout != "__CM_FS__:99\n" || res.Stderr != "__CM_NET__:99\n" {
			t.Fatalf("%s: %+v", py, res)
		}
	}
}

// ctx_execute_file, the switch and non-Python languages report no Python counts.
func TestPyCountersOnlyInstrumentedRuns(t *testing.T) {
	file := writeData(t, 4096)
	code := "print(len(open(" + jsonQuote(file) + ").read()))"
	e := newTestExec(t)
	res := run(t, e, Request{Language: Python, Code: code, FilePath: file, Timeout: 30 * time.Second})
	if res.SandboxBytes != 0 {
		t.Errorf("execute_file: SandboxBytes = %d, want 0", res.SandboxBytes)
	}
	off := newTestExec(t)
	off.getenv = func(k string) string {
		if k == "CMGO_PY_COUNTERS" {
			return "0"
		}
		return os.Getenv(k)
	}
	res = runPy(t, off, code)
	if res.SandboxBytes != 0 || strings.TrimSpace(res.Stdout) != "4096" {
		t.Errorf("switch off: %+v", res)
	}
	res = run(t, e, Request{Language: Shell, Code: "echo __CM_FS__:99 >&2", Timeout: 10 * time.Second})
	if res.SandboxBytes != 0 {
		t.Errorf("shell: %+v", res)
	}
}

// Two runs at once each report their own count.
func TestPyCountsConcurrentRuns(t *testing.T) {
	e := newTestExec(t)
	sizes := []int{3000, 70000}
	got := make([]int64, len(sizes))
	var wg sync.WaitGroup
	for i, n := range sizes {
		file := writeData(t, n)
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = runPy(t, e, "print(len(open("+jsonQuote(file)+").read()))").SandboxBytes
		}()
	}
	wg.Wait()
	for i, n := range sizes {
		if got[i] != int64(n) {
			t.Errorf("run %d: SandboxBytes = %d, want %d", i, got[i], n)
		}
	}
}

func TestReadPyCounts(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]int64{"123": 123, "": 0, "x": 0, "-5": 0} {
		p := filepath.Join(dir, "c")
		os.WriteFile(p, []byte(body), 0o644)
		if got := readPyCounts(p); got != want {
			t.Errorf("readPyCounts(%q) = %d, want %d", body, got, want)
		}
	}
	if readPyCounts(filepath.Join(dir, "missing")) != 0 {
		t.Error("missing file must be 0")
	}
}

func TestPyBootstrapIsASCII(t *testing.T) {
	for i := 0; i < len(pyBootstrap); i++ {
		if pyBootstrap[i] > 0x7e || pyBootstrap[i] < 0x09 {
			t.Fatalf("non-ASCII byte %#x at %d", pyBootstrap[i], i)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/executor -run 'TestPy|TestReadPyCounts' -count=1`
Expected: FAIL to compile (`pyBootstrap`, `readPyCounts` undefined).

- [ ] **Step 3: Write the implementation**

`internal/executor/pycounters.py` (core part; the line `# CM:NET` is replaced in Task 3):

```python
import sys
if sys.version_info < (3, 9) and sys.executable:
    import os
    os.execv(sys.executable, [sys.executable, sys.argv[2]])
def cm_main():
    counts, script = sys.argv[1], sys.argv[2]
    import os, io, builtins, atexit
    orig_open = builtins.open
    with orig_open(script, "rb") as f:
        src = f.read()
    n = [0]
    undo = []
    def install():
        class CFile(io.FileIO):
            def readinto(self, b):
                r = io.FileIO.readinto(self, b)
                if r:
                    n[0] += r
                return r
            def readall(self):
                d = io.FileIO.readall(self)
                n[0] += len(d)
                return d
        def cm_open(file, mode="r", buffering=-1, encoding=None, errors=None, newline=None, closefd=True, opener=None):
            if (mode in ("r", "rt", "rb") and buffering == -1 and isinstance(file, (str, bytes, os.PathLike))
                    and not (isinstance(file, str) and os.path.abspath(file) == script)
                    and (mode != "rb" or (encoding is None and errors is None and newline is None))):
                raw = CFile(file, "r", closefd, opener)
                try:
                    size = io.DEFAULT_BUFFER_SIZE
                    try:
                        bs = os.fstat(raw.fileno()).st_blksize
                        if bs > 1:
                            size = bs
                    except Exception:
                        pass
                    buf = io.BufferedReader(raw, size)
                    if mode == "rb":
                        return buf
                    txt = io.TextIOWrapper(buf, encoding, errors, newline, raw.isatty())
                    txt.mode = mode
                    return txt
                except BaseException:
                    raw.close()
                    raise
            return orig_open(file, mode, buffering, encoding, errors, newline, closefd, opener)
        builtins.open = io.open = cm_open
        undo.append(lambda: (setattr(builtins, "open", orig_open), setattr(io, "open", orig_open)))

        # CM:NET
        def flush():
            try:
                with orig_open(counts, "w") as f:
                    f.write(str(n[0]))
            except BaseException:
                pass
        atexit.register(flush)
    try:
        install()
    except BaseException:
        for u in reversed(undo):
            try:
                u()
            except BaseException:
                pass
    oh = sys.excepthook
    def hook(t, v, tb):
        while tb is not None and tb.tb_frame.f_code.co_filename in ("<string>", "<cm>"):
            tb = tb.tb_next
        oh(t, v.with_traceback(tb), tb)
    sys.excepthook = hook
    sys.argv = [script]
    if not getattr(sys.flags, "safe_path", False):
        sys.path[0] = os.path.dirname(script)
    ns = sys.modules["__main__"].__dict__
    ns["__file__"] = script
    ns["__cached__"] = None
    ns["__loader__"] = sys.modules["_frozen_importlib_external"].SourceFileLoader("__main__", script)
    try:
        code = compile(src, script, "exec", dont_inherit=True)
    except SyntaxError:
        if sys.executable:
            os.execv(sys.executable, [sys.executable, script])
        raise
    exec(code, ns)
cm_main()
```

`internal/executor/pycounters.go`:

```go
package executor

import (
	_ "embed"
	"os"
	"strconv"
	"strings"
)

// pyBootstrap is the Python source run with -c. It counts the bytes the user's
// script reads through open() and sockets and writes the sum to a file at exit.
//
//go:embed pycounters.py
var pyBootstrap string

// pyArgv runs script under the bootstrap. The bootstrap gets the counts file
// path and the script path as sys.argv[1] and [2]; its own source is exec'd in
// a private namespace so __main__ gains no name (spec §1 Q1).
func pyArgv(python, countsFile, script string) []string {
	boot := `exec(compile(` + jsonQuote(pyBootstrap) + `, "<cm>", "exec"), {"__name__": "cm"})`
	return []string{python, "-c", boot, countsFile, script}
}

// readPyCounts returns the count the bootstrap wrote; a missing or malformed
// file (killed run, os._exit, setup fallback) is 0.
func readPyCounts(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
```

`internal/executor/run.go`: change the `SandboxBytes` comment to `// bytes a ctx_execute JS/TS or Python run read through files or the network`; after `argv, err := e.command(...)` and `env := buildEnv(...)` insert

```go
	countsFile := ""
	if r.Language == Python && file == "" && !r.Background && e.getenv("CMGO_PY_COUNTERS") != "0" {
		countsFile = filepath.Join(dir, ".counts")
		argv = pyArgv(argv[0], countsFile, script)
	}
```

and after `keep = res.Backgrounded` add

```go
	if countsFile != "" {
		res.SandboxBytes = readPyCounts(countsFile)
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/executor -run 'TestPy|TestReadPyCounts' -count=1 -v 2>&1 | tail -20`
Expected: PASS for every test; `TestPyCountsFileReads` runs once per interpreter.

- [ ] **Step 5: Whole package and commit**

```bash
go vet ./internal/executor && gofmt -l internal/executor && go test ./internal/executor -count=1
git add internal/executor && git commit -m "feat(executor): count Python file reads through a -c bootstrap

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
Expected: vet and gofmt print nothing, tests ok.

---

### Task 2: `open()` equivalence and partial reads

**Files:**
- Modify: `internal/executor/pycounters_test.go`
- Modify (only if a case fails): `internal/executor/pycounters.py`

**Interfaces:** Consumes `pythons`, `pyExec`, `runPy` from Task 1. Produces nothing new.

- [ ] **Step 1: Write the failing tests** (append)

```go
// Patched open() and the original (_io.open) agree on content and attributes
// across read modes, encodings, error handlers and newline settings.
func TestPyOpenEquivalence(t *testing.T) {
	data := filepath.Join(t.TempDir(), "mixed.bin")
	os.WriteFile(data, []byte("line1\r\nline2\ncaf\xc3\xa9 ok\nlast"), 0o644)
	code := `import _io, io, itertools
p = ` + jsonQuote(data) + `
def snap(op):
    try:
        f = op()
    except Exception as e:
        return ("raise", type(e).__name__, str(e))
    with f:
        try:
            c = f.read()
        except Exception as e:
            return ("readraise", type(e).__name__)
        raw = getattr(f, "buffer", f)
        raw = getattr(raw, "raw", raw)
        f.seek(0)
        first = f.readline()
        return (c, first, f.tell(), f.mode, f.name, getattr(f, "encoding", None), getattr(f, "errors", None),
                getattr(f, "newlines", None), getattr(f, "line_buffering", None), type(f).__name__,
                isinstance(raw, io.FileIO), list(f) == list(f))
bad = 0
n = 0
for mode, enc, err, nl in itertools.product(("r", "rt", "rb"), (None, "utf-8", "latin-1"), (None, "replace"), (None, "", "\n", "\r\n")):
    a = snap(lambda: _io.open(p, mode, -1, enc, err, nl))
    b = snap(lambda: open(p, mode, -1, enc, err, nl))
    n += 1
    if a != b:
        bad += 1
        print("DIFF", mode, enc, err, repr(nl), a, b)
print("OK" if bad == 0 else "BAD", n)`
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.ExitCode != 0 || !strings.HasPrefix(res.Stdout, "OK 72") {
			t.Fatalf("%s: %+v", py, res)
		}
	}
}

// Reading the start of a large file counts about one buffer, not the file.
func TestPyPartialReadCountsBuffer(t *testing.T) {
	file := writeData(t, 1<<20)
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), "f = open("+jsonQuote(file)+"); f.read(100)")
		if res.SandboxBytes <= 0 || res.SandboxBytes > 64<<10 {
			t.Errorf("%s: SandboxBytes = %d, want 1..65536", py, res.SandboxBytes)
		}
	}
}

// Write mode, file descriptors and buffering=0 take the original path and count 0.
func TestPyUncountedOpens(t *testing.T) {
	file := writeData(t, 2048)
	out := filepath.Join(t.TempDir(), "out.txt")
	code := `import os
with open(` + jsonQuote(out) + `, "w") as f: f.write("x" * 500)
fd = os.open(` + jsonQuote(file) + `, os.O_RDONLY)
print(len(open(fd, "rb", closefd=True).read()))
print(len(open(` + jsonQuote(file) + `, "rb", buffering=0).read()))`
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.ExitCode != 0 || res.Stdout != "2048\n2048\n" || res.SandboxBytes != 0 {
			t.Errorf("%s: %+v", py, res)
		}
	}
}
```

- [ ] **Step 2: Run to verify** — `go test ./internal/executor -run 'TestPyOpen|TestPyPartial|TestPyUncounted' -count=1 -v 2>&1 | tail -30`
Expected: all PASS if the Task 1 `cm_open` is right. If `TestPyOpenEquivalence` prints `DIFF` lines, that is the failing run: fix `cm_open` until it prints `OK 72` on both interpreters (e.g. `txt.mode`, `line_buffering`, buffer size). A pass at first run is acceptable only after temporarily breaking `cm_open` (change `raw.isatty()` to `True`) and confirming `BAD`, then reverting; record that in the commit message.

- [ ] **Step 3: Commit**

```bash
gofmt -l internal/executor && go test ./internal/executor -count=1
git add internal/executor && git commit -m "test(executor): open() equivalence matrix and partial-read count

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Network bytes, lazy socket/ssl patch

**Files:**
- Modify: `internal/executor/pycounters.py` (replace the `# CM:NET` line), `internal/executor/pycounters_test.go`

**Interfaces:** Consumes Task 1 helpers. Produces nothing new.

- [ ] **Step 1: Write the failing tests** (append; add imports `net/http`, `net/http/httptest`)

```go
func bodyServer(t *testing.T, tls bool, size int) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("n", size)))
	})
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	return srv
}

// urllib over plain HTTP counts the body plus the response headers.
func TestPyCountsHTTP(t *testing.T) {
	srv := bodyServer(t, false, 5000)
	code := "import urllib.request\nprint(len(urllib.request.urlopen(" + jsonQuote(srv.URL) + ").read()))"
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "5000" {
			t.Fatalf("%s: %+v", py, res)
		}
		if res.SandboxBytes < 5000 || res.SandboxBytes > 5000+1024 {
			t.Errorf("%s: SandboxBytes = %d, want 5000..6024", py, res.SandboxBytes)
		}
	}
}

// HTTPS goes through SSLSocket.read once: no double count with recv/recv_into.
func TestPyCountsHTTPSOnce(t *testing.T) {
	srv := bodyServer(t, true, 100000)
	code := "import ssl, urllib.request\nctx = ssl._create_unverified_context()\nprint(len(urllib.request.urlopen(" + jsonQuote(srv.URL) + ", context=ctx).read()))"
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "100000" {
			t.Fatalf("%s: %+v", py, res)
		}
		if res.SandboxBytes < 100000 || res.SandboxBytes > 100000+2048 {
			t.Errorf("%s: SandboxBytes = %d, want 100000..102048", py, res.SandboxBytes)
		}
	}
}

// socket and ssl are patched only when the code imports them, and imports are not counted.
func TestPyNetworkPatchIsLazy(t *testing.T) {
	probe := "import sys\nprint('socket' in sys.modules, 'ssl' in sys.modules)\nimport json\nprint(json.dumps(1))"
	for _, py := range pythons(t) {
		on := runPy(t, pyExec(t, py), probe)
		off := pyExec(t, py)
		off.getenv = func(k string) string {
			if k == "CMGO_PY_COUNTERS" {
				return "0"
			}
			return os.Getenv(k)
		}
		plain := runPy(t, off, probe)
		if on.Stdout != plain.Stdout || on.SandboxBytes != 0 {
			t.Errorf("%s: on=%+v plain=%+v", py, on, plain)
		}
	}
}
```

(Do not import `strconv`; add `net/http` and `net/http/httptest`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/executor -run 'TestPyCountsHTTP|TestPyNetworkPatch' -count=1 2>&1 | tail -20`
Expected: `TestPyCountsHTTP` and `TestPyCountsHTTPSOnce` FAIL with `SandboxBytes = 0`; `TestPyNetworkPatchIsLazy` passes already.

- [ ] **Step 3: Write the implementation** — in `pycounters.py` replace the line `        # CM:NET` with:

```python
        def count_socket(m):
            S = m.socket
            if getattr(S, "_cm", False):
                return
            o_recv, o_into = S.recv, S.recv_into
            def recv(self, *a, **k):
                r = o_recv(self, *a, **k)
                n[0] += len(r)
                return r
            def recv_into(self, *a, **k):
                r = o_into(self, *a, **k)
                n[0] += r
                return r
            S.recv, S.recv_into, S._cm = recv, recv_into, True
        def count_ssl(m):
            S = m.SSLSocket
            if getattr(S, "_cm", False):
                return
            o_read = S.read
            def read(self, *a, **k):
                r = o_read(self, *a, **k)
                n[0] += r if isinstance(r, int) else len(r)
                return r
            S.read, S._cm = read, True
        patchers = {"socket": count_socket, "ssl": count_ssl}
        class Finder:
            busy = set()
            def find_spec(self, name, path=None, target=None):
                if name not in patchers or name in self.busy:
                    return None
                self.busy.add(name)
                try:
                    spec = None
                    for f in sys.meta_path:
                        fs = getattr(f, "find_spec", None)
                        if f is self or fs is None:
                            continue
                        spec = fs(name, path, target)
                        if spec is not None:
                            break
                finally:
                    self.busy.discard(name)
                if spec is None or spec.loader is None or not hasattr(spec.loader, "exec_module"):
                    return spec
                ld = spec.loader
                oe = ld.exec_module
                def exec_module(module):
                    oe(module)
                    try:
                        patchers[name](module)
                    except Exception:
                        pass
                ld.exec_module = exec_module
                return spec
        fin = Finder()
        sys.meta_path.insert(0, fin)
        undo.append(lambda: fin in sys.meta_path and sys.meta_path.remove(fin))
        for name, p in patchers.items():
            if name in sys.modules:
                p(sys.modules[name])
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/executor -run 'TestPy' -count=1 -v 2>&1 | tail -30`
Expected: PASS, including the Task 1 and 2 tests.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/executor && go vet ./internal/executor && go test ./internal/executor -count=1
git add internal/executor && git commit -m "feat(executor): count Python socket reads, patched lazily

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Fidelity corpus and setup-failure fallback

**Files:**
- Modify: `internal/executor/pycounters_test.go`
- Modify (only if a case fails): `internal/executor/pycounters.py`

**Interfaces:** Consumes Task 1 helpers, `pyArgv`, `pyBootstrap`. Produces nothing new.

- [ ] **Step 1: Write the failing tests** (append; add imports `regexp`, `bytes`, `context`)

```go
var runDir = regexp.MustCompile(`/\.cmgo-[A-Za-z0-9]+/`)

// fidelityCases are run with the counters on and off; stdout, stderr (run dir
// masked) and the exit code must match byte for byte.
var fidelityCases = map[string]string{
	"syntax mid-file":  "x = 1\ny = (\nz = 2\n",
	"syntax at eof":    "x = (\n",
	"nested exception": "def f():\n    raise ValueError('boom')\ntry:\n    f()\nexcept ValueError as e:\n    raise RuntimeError('outer') from e\n",
	"sys.exit":         "import sys\nsys.exit(3)",
	"future import":    "from __future__ import annotations\ndef f(x: Undefined): return x\nprint(f(1))",
	"main identity": "import sys, os\nprint(sorted(globals()))\nprint(__name__, type(__loader__).__name__, __spec__, __cached__)\n" +
		"print(os.path.basename(__file__), len(sys.argv), os.path.basename(sys.argv[0]), sys.path[0] == os.path.dirname(__file__))",
	"latin-1 cookie":     "# -*- coding: latin-1 -*-\nprint('caf\xe9', len('\xe9'))",
	"utf-8 no cookie":    "print('caf\u00e9', len('\u00e9'))",
	"multiprocessing":    "import multiprocessing as mp\ndef g(x): return x * 2\nif __name__ == '__main__':\n    with mp.get_context('spawn').Pool(1) as p:\n        print(p.map(g, [1, 2]))",
	"atexit reads":       "import atexit\natexit.register(lambda: print(len(open(__file__).read()) > 0))\nprint('body')",
	"os._exit":           "import os\nprint('x', flush=True)\nos._exit(7)",
	"custom excepthook":  "import sys\nsys.excepthook = lambda t, v, tb: print('hook', t.__name__)\nraise KeyError('k')",
	"traceback in frame": "import traceback\ndef f():\n    traceback.print_exc()\ntry:\n    1/0\nexcept ZeroDivisionError:\n    f()",
}

func masked(s string) string { return runDir.ReplaceAllString(s, "/D/") }

func TestPyFidelity(t *testing.T) {
	for _, py := range pythons(t) {
		on := pyExec(t, py)
		off := pyExec(t, py)
		off.getenv = func(k string) string {
			if k == "CMGO_PY_COUNTERS" {
				return "0"
			}
			return os.Getenv(k)
		}
		for name, code := range fidelityCases {
			t.Run(py+"/"+name, func(t *testing.T) {
				a, b := runPy(t, on, code), runPy(t, off, code)
				if a.ExitCode != b.ExitCode || a.Stdout != b.Stdout || masked(a.Stderr) != masked(b.Stderr) {
					t.Errorf("counters on:  %d %q %q\ncounters off: %d %q %q", a.ExitCode, a.Stdout, masked(a.Stderr), b.ExitCode, b.Stdout, masked(b.Stderr))
				}
			})
		}
	}
}

// Reads of script.py itself (linecache on a traceback, __file__) are code, not data.
func TestPyIgnoresScriptSource(t *testing.T) {
	code := "print(len(open(__file__).read()) > 0)\nraise ValueError('x')"
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.ExitCode != 1 || res.SandboxBytes != 0 {
			t.Errorf("%s: %+v", py, res)
		}
	}
}

// With PYTHONSAFEPATH (3.11+) the bootstrap leaves sys.path alone, as a plain run does.
func TestPySafePathFidelity(t *testing.T) {
	t.Setenv("PYTHONSAFEPATH", "1")
	for _, py := range pythons(t) {
		if out, _ := exec.Command(py, "-c", "import sys;print(sys.version_info >= (3, 11))").Output(); strings.TrimSpace(string(out)) != "True" {
			t.Logf("%s has no safe_path, skipped", py)
			continue
		}
		code := "import sys\nprint(sys.path)"
		off := pyExec(t, py)
		off.getenv = func(k string) string {
			if k == "CMGO_PY_COUNTERS" {
				return "0"
			}
			return os.Getenv(k)
		}
		a, b := runPy(t, pyExec(t, py), code), runPy(t, off, code)
		if masked(a.Stdout) != masked(b.Stdout) || a.ExitCode != 0 {
			t.Errorf("%s: on %q off %q", py, a.Stdout, b.Stdout)
		}
	}
}

// Below the version floor the bootstrap execs the plain interpreter and writes no counts.
// (No Python below 3.9 is installed here, so the floor is raised in the test instead.)
func TestPyOldVersionRunsPlain(t *testing.T) {
	old := strings.Replace(pyBootstrap, "sys.version_info < (3, 9)", "sys.version_info < (99, 0)", 1)
	if old == pyBootstrap {
		t.Fatal("version guard not found in pycounters.py")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "script.py")
	counts := filepath.Join(dir, ".counts")
	os.WriteFile(script, []byte("print('plain')"), 0o644)
	for _, py := range pythons(t) {
		out, err := exec.Command(py, "-c", `exec(compile(`+jsonQuote(old)+`, "<cm>", "exec"), {"__name__": "cm"})`, counts, script).Output()
		if err != nil || string(out) != "plain\n" {
			t.Fatalf("%s: err=%v out=%q", py, err, out)
		}
		if _, err := os.Stat(counts); err == nil {
			t.Errorf("%s: counts file written", py)
		}
	}
}

// A sitecustomize on the user's PYTHONPATH still runs.
func TestPyKeepsUserSitecustomize(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sitecustomize.py"), []byte("print('site ran')\n"), 0o644)
	t.Setenv("PYTHONPATH", dir)
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), "print('main')")
		if res.Stdout != "site ran\nmain\n" {
			t.Errorf("%s: %+v", py, res)
		}
	}
}

// Any error while the hooks are installed runs the script unhooked with no counts.
func TestPySetupFailureRunsScript(t *testing.T) {
	broken := strings.Replace(pyBootstrap, "    def install():\n", "    def install():\n        raise RuntimeError('seam')\n", 1)
	if broken == pyBootstrap {
		t.Fatal("seam not found in pycounters.py: keep `    def install():` unchanged")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "script.py")
	counts := filepath.Join(dir, ".counts")
	os.WriteFile(script, []byte("print(len(open("+jsonQuote(script)+").read()))"), 0o644)
	argv := []string{"-c", `exec(compile(` + jsonQuote(broken) + `, "<cm>", "exec"), {"__name__": "cm"})`, counts, script}
	for _, py := range pythons(t) {
		var out, errb bytes.Buffer
		cmd := exec.CommandContext(context.Background(), py, argv...)
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil || strings.TrimSpace(out.String()) == "" || errb.Len() != 0 {
			t.Fatalf("%s: err=%v out=%q err=%q", py, err, out.String(), errb.String())
		}
		if readPyCounts(counts) != 0 {
			t.Errorf("%s: counts file written after setup failure", py)
		}
	}
}
```

- [ ] **Step 2: Run to verify** — `go test ./internal/executor -run 'TestPyFidelity|TestPyKeeps|TestPySetup' -count=1 2>&1 | tail -40`
Expected: should pass on the Task 1 bootstrap (it was prototyped against these cases on 3.9.6 and 3.12.1). Any FAIL is a real fidelity bug: fix `pycounters.py` (do not edit the case). Prove the corpus can fail: temporarily delete the `hook` excepthook trimming (`sys.excepthook = hook`), confirm `nested exception` FAILS, then restore.
The `atexit reads` case reads the script itself, so its count must be 0 (`TestPyIgnoresScriptSource`).

- [ ] **Step 3: Commit**

```bash
gofmt -l internal/executor && go test ./internal/executor -count=1
git add internal/executor && git commit -m "test(executor): Python fidelity corpus and setup-failure fallback

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Docs, known difference, switch documented

**Files:**
- Modify: `docs/superpowers/specs/2026-10-20-cmgo-core-design.md` (§2.2 table), `docs/superpowers/specs/2026-10-21-cmgo-stats-design.md` (§1.3 table), `testdata/golden/KNOWN-DIFFS.md`, `CLAUDE.md`
- Modify: `internal/executor/pycounters_test.go`

- [ ] **Step 1: Write the failing test** (append; add `os` already imported)

```go
// The Python counters are a listed difference from TS (KNOWN-DIFFS.md).
func TestKnownDiffsListsPythonCounters(t *testing.T) {
	b, err := os.ReadFile("../../testdata/golden/KNOWN-DIFFS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "executor:python-counters") {
		t.Error("KNOWN-DIFFS.md has no executor:python-counters entry")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/executor -run TestKnownDiffsListsPythonCounters -count=1`
Expected: FAIL `has no executor:python-counters entry`.

- [ ] **Step 3: Write the docs**
  - `testdata/golden/KNOWN-DIFFS.md`, under "By design: spec §2.2" add: `- executor:python-counters: ctx_execute counts the bytes a Python run reads through open()/pathlib and receives on sockets (TS counts JS/TS only). No golden case runs Python through stats, so none is skipped. Spec: 2026-10-22-cmgo-python-sandbox-bytes-design.md.`
  - Core spec §2.2: add a table row `| No byte counters for languages other than JS/TS | Python ctx_execute counts file reads and socket receives through a -c bootstrap (`CMGO_PY_COUNTERS=0` turns it off) | The kept bytes of Python runs were reported as 0 |`
  - Stats spec §1.3: change "Reads in other languages are not counted" to "Python runs also count bytes read through `open()`/`pathlib` and received on sockets (lower bound). Shell and other languages are not counted".
  - `CLAUDE.md` config line: add `` `CMGO_PY_COUNTERS` (Python read counters, `0` = off) `` next to `CMGO_BASH_READ_MIN_BYTES`.

- [ ] **Step 4: Run to verify it passes and commit**

```bash
go test ./internal/executor -count=1
git add -A docs testdata CLAUDE.md internal/executor && git commit -m "docs: record the Python counters difference and CMGO_PY_COUNTERS

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Overhead, live before/after, verification, PR

**Files:**
- Create: `internal/executor/pycounters_bench_test.go`

- [ ] **Step 1: Write the benchmark**

```go
package executor

import (
	"context"
	"os"
	"testing"
	"time"
)

// BenchmarkPyRun compares a Python run with the counters off and on.
func BenchmarkPyRun(b *testing.B) {
	progs := map[string]string{
		"print":  "print(1)",
		"json":   "import json\nprint(json.dumps(1))",
		"urllib": "import urllib.request\nprint(1)",
	}
	for name, code := range progs {
		for _, mode := range []string{"off", "on"} {
			b.Run(name+"/"+mode, func(b *testing.B) {
				e := New(b.TempDir())
				e.getenv = func(k string) string {
					if k == "CMGO_PY_COUNTERS" && mode == "off" {
						return "0"
					}
					return os.Getenv(k)
				}
				for i := 0; i < b.N; i++ {
					if _, err := e.Run(context.Background(), Request{Language: Python, Code: code, Timeout: 30 * time.Second}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
```
Run `gofmt -w` on the file.

- [ ] **Step 2: Overhead table** — wait for load average below 5 (`uptime`), then
`go test ./internal/executor -run '^$' -bench BenchmarkPyRun -benchtime 100x -count 5 | tee $SCRATCH/pybench.txt`
Build the median ns/op per case (off vs on) and the delta. Budget: `print` median delta <= 3 ms. If it is over, stop and report to "context"; do not drop the network patch silently (spec §Q6).

- [ ] **Step 3: Live before/after** with `bench/driver.py`'s `Server` against a scratch `CMGO_DIR`/`HOME`/`CLAUDE_CONFIG_DIR`: build `go build -o bin/ ./cmd/cmgo`, start a local `http.server` for a 20 KB JSON, call `ctx_execute` (python, `urllib.request.urlopen(url).read()`, print 3 lines) once with `CMGO_PY_COUNTERS=0` and once without, then call `ctx_stats` each time. Keep the two `ctx_stats` kept/returned lines for the PR (before: kept 0; after: kept about 20 KB).

- [ ] **Step 4: Full verification**

```bash
gofmt -l . ; go vet ./... ; echo vet=$?
go test ./... -count=1 ; echo test=$?
go test -race ./... -count=1 ; echo race=$?
go list -deps ./cmd/cmgo-hook | grep -c executor
```
Expected: gofmt empty, `vet=0`, `test=0`, `race=0`, last line `0` (the hook binary must not link the executor). Also run the executor tests on `/usr/bin/python3` (3.9.6): the `pythons` helper already includes it; state the version in the PR. If 3.9 were missing, say so.

- [ ] **Step 5: Final review and PR** — report to "context" for review as with PR #11. Open the PR only after the owner's go; body carries: spec path, `go vet`/`go test`/`-race` output with exit status, the overhead table, the live before/after, the 3.9.6 result, the known limits (§5 of the spec). `🤖 Generated with [Claude Code](https://claude.com/claude-code)` last line. No session URLs.
