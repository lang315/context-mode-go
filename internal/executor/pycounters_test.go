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
