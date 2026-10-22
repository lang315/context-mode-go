package executor

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	if st, err := os.Stat("/opt/homebrew/bin/python3.14"); err == nil {
		dup := false
		for _, o := range out {
			dup = dup || os.SameFile(st, mustStat(t, o))
		}
		if !dup {
			out = append(out, "/opt/homebrew/bin/python3.14")
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
from pathlib import Path
for mode in ("r", "rb"):
    a = snap(lambda: _io.open(Path(p), mode))
    b = snap(lambda: open(Path(p), mode))
    n += 1
    if a != b:
        bad += 1
        print("DIFF path", mode, a, b)
print("OK" if bad == 0 else "BAD", n)`
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.ExitCode != 0 || !strings.HasPrefix(res.Stdout, "OK 74") {
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
	"latin-1 cookie":            "# -*- coding: latin-1 -*-\nprint('caf\xe9', len('\xe9'))",
	"utf-8 no cookie":           "print('caf\u00e9', len('\u00e9'))",
	"multiprocessing":           "import multiprocessing as mp\ndef g(x): return x * 2\nif __name__ == '__main__':\n    with mp.get_context('spawn').Pool(1) as p:\n        print(p.map(g, [1, 2]))",
	"atexit reads":              "import atexit\np = __file__\natexit.register(lambda: print(len(open(p).read()) > 0))\nprint('body')",
	"os._exit":                  "import os\nprint('x', flush=True)\nos._exit(7)",
	"custom excepthook":         "import sys\nsys.excepthook = lambda t, v, tb: print('hook', t.__name__)\nraise KeyError('k')",
	"missing file":              "open('nope.txt')",
	"missing pathlib":           "from pathlib import Path\nPath('nope.txt').read_text()",
	"socket timeout":            "import socket\na, b = socket.socketpair()\na.settimeout(0.05)\na.recv(1)",
	"raise from wrapper":        "try:\n    open('nope.txt')\nexcept OSError as e:\n    raise RuntimeError('x') from e",
	"path name and msg":         "from pathlib import Path\nprint(repr(open(Path('/etc/hosts')).name))\ntry:\n    open(Path('nope.txt'))\nexcept OSError as e:\n    print(e)",
	"syntax warning then error": "x = '\\d'\ny = (\n",
	"syntax warning ok":         "x = '\\d'\nprint(x)",
	"nonblocking fifo":          "import os, tempfile\np = os.path.join(tempfile.mkdtemp(), 'f')\nos.mkfifo(p)\nw = os.open(p, os.O_RDWR | os.O_NONBLOCK)\nf = open(p, 'rb', opener=lambda q, fl: os.open(q, fl | os.O_NONBLOCK))\nprint(f.read())",
	"exec string frame":         "exec('x = 1\\n1/0')",
	"dataclass post_init":       "from dataclasses import dataclass\n@dataclass\nclass A:\n    x: int\n    def __post_init__(self):\n        raise ValueError('p')\nA(1)",
	"namedtuple args":           "from collections import namedtuple\nP = namedtuple('P', 'a b')\nP(1)",
	"exception group":           "err = None\ntry:\n    open('nope.txt')\nexcept OSError as e:\n    err = e\nraise ExceptionGroup('g', [err])",
	"exceptions attr int":       "class E(Exception):\n    exceptions = 3\nraise E()",
	"exceptions attr list":      "class E(Exception):\n    exceptions = ['a', 'b']\nraise E()",
	"exec context frame":        "exec('try:\\n    1/0\\nexcept Exception:\\n    raise KeyError(1)')",
	"nul byte":                  "print(1)\x00",
	"traceback in frame":        "import traceback\ndef f():\n    traceback.print_exc()\ntry:\n    1/0\nexcept ZeroDivisionError:\n    f()",
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
	data := writeData(t, 3000)
	os.WriteFile(script, []byte("print(len(open("+jsonQuote(data)+").read()))"), 0o644)
	argv := []string{"-c", `exec(compile(` + jsonQuote(broken) + `, "<cm>", "exec"), {"__name__": "cm"})`, counts, script}
	for _, py := range pythons(t) {
		var out, errb bytes.Buffer
		cmd := exec.CommandContext(context.Background(), py, argv...)
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil || strings.TrimSpace(out.String()) != "3000" || errb.Len() != 0 {
			t.Fatalf("%s: err=%v out=%q err=%q", py, err, out.String(), errb.String())
		}
		if readPyCounts(counts) != 0 {
			t.Errorf("%s: counts file written after setup failure", py)
		}
	}
}

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

// Errors and warnings render source files through linecache/tokenize; those reads
// are not data the code read, and a failed request received nothing.
func TestPyErrorsCountNothing(t *testing.T) {
	for name, code := range map[string]string{
		"refused urlopen":   "import urllib.request\nurllib.request.urlopen('http://127.0.0.1:9/')",
		"json error":        "import json\njson.loads('{')",
		"missing pathlib":   "from pathlib import Path\nPath('/nonexistent/x').read_text()",
		"missing open":      "open('/nonexistent/x')",
		"thread excepthook": "import threading, json\nt = threading.Thread(target=lambda: json.loads('{'))\nt.start()\nt.join()\nraise SystemExit(1)",
		"reset excepthook":  "import sys, json\nsys.excepthook = sys.__excepthook__\njson.loads('{')",
		"direct excepthook": "import sys, json\ntry:\n    json.loads('{')\nexcept Exception:\n    sys.__excepthook__(*sys.exc_info())\nraise SystemExit(1)",
	} {
		for _, py := range pythons(t) {
			t.Run(name+"/"+py, func(t *testing.T) {
				res := runPy(t, pyExec(t, py), code)
				if res.ExitCode != 1 || res.SandboxBytes != 0 {
					t.Errorf("exit %d SandboxBytes %d, want 1 and 0\n%s", res.ExitCode, res.SandboxBytes, res.Stderr)
				}
			})
		}
	}
}

// MSG_PEEK reads nothing: only the bytes recv consumes count.
func TestPyPeekNotCounted(t *testing.T) {
	code := "import socket\na, b = socket.socketpair()\nb.send(b'x' * 100)\na.recv(100, socket.MSG_PEEK)\nbuf = bytearray(100)\na.recv_into(buf, 100, socket.MSG_PEEK)\nprint(len(a.recv(100)))"
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "100" || res.SandboxBytes != 100 {
			t.Errorf("%s: %+v", py, res)
		}
	}
}

// A forked child that exits normally must not write the counts file.
func TestPyForkedChildDoesNotFlush(t *testing.T) {
	file := writeData(t, 5000)
	code := "import os\nrpid = os.fork()\nif rpid == 0:\n    open(" + jsonQuote(file) + ").read()\n    raise SystemExit(0)\nos.waitpid(rpid, 0)\nos._exit(0)"
	for _, py := range pythons(t) {
		res := runPy(t, pyExec(t, py), code)
		if res.ExitCode != 0 || res.SandboxBytes != 0 {
			t.Errorf("%s: %+v", py, res)
		}
	}
}

// EncodingWarning (3.10+) points at the user's line, not the bootstrap.
func TestPyEncodingWarningLocation(t *testing.T) {
	t.Setenv("PYTHONWARNDEFAULTENCODING", "1")
	code := "print(len(open('/etc/hosts').read()) > 0)"
	for _, py := range pythons(t) {
		off := pyExec(t, py)
		off.getenv = func(k string) string {
			if k == "CMGO_PY_COUNTERS" {
				return "0"
			}
			return os.Getenv(k)
		}
		a, b := runPy(t, pyExec(t, py), code), runPy(t, off, code)
		if a.Stdout != b.Stdout || masked(a.Stderr) != masked(b.Stderr) {
			t.Errorf("%s: on %q off %q", py, a.Stderr, b.Stderr)
		}
	}
}

// Source files read as binary (what traceback and warning renderers do) are not
// data; any other binary read is still counted.
func TestPyBinarySourceReadsNotCounted(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mod.py")
	txt := filepath.Join(dir, "mod.txt")
	body := []byte(strings.Repeat("x", 4096))
	os.WriteFile(src, body, 0o644)
	os.WriteFile(txt, body, 0o644)
	for _, py := range pythons(t) {
		e := pyExec(t, py)
		if res := runPy(t, e, "open("+jsonQuote(src)+", 'rb').read()"); res.SandboxBytes != 0 {
			t.Errorf("%s .py rb: SandboxBytes = %d, want 0", py, res.SandboxBytes)
		}
		if res := runPy(t, e, "open("+jsonQuote(txt)+", 'rb').read()"); res.SandboxBytes != 4096 {
			t.Errorf("%s .txt rb: SandboxBytes = %d, want 4096", py, res.SandboxBytes)
		}
	}
}
