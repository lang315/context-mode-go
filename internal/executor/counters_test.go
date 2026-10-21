package executor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A JS/TS run counts the bytes its code read through fs.readFileSync,
// fs.readFile, fetch and http.get into SandboxBytes, and the counter lines
// do not reach Stderr.
func TestRunCountsJSReads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		w.Write([]byte(strings.Repeat("n", n)))
	}))
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(file, []byte(strings.Repeat("f", 10240)), 0o644); err != nil {
		t.Fatal(err)
	}
	code := `const fs = require("fs");
fs.readFileSync(` + strconv.Quote(file) + `, "utf8");
await new Promise((ok) => fs.readFile(` + strconv.Quote(file) + `, () => ok()));
await (await fetch("` + srv.URL + `/2000")).text();
require("http").get("` + srv.URL + `/3000", (res) => res.resume());
console.error("own stderr");
console.log("ok");`
	for _, lang := range []Language{JavaScript, TypeScript} {
		t.Run(string(lang), func(t *testing.T) {
			e := newTestExec(t)
			if e.runtime(lang) == "" {
				t.Skipf("%s runtime not installed", lang)
			}
			res := run(t, e, Request{Language: lang, Code: code, Timeout: 60 * time.Second})
			if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "ok" {
				t.Fatalf("run: %+v", res)
			}
			if want := int64(2*10240 + 2000 + 3000); res.SandboxBytes != want {
				t.Errorf("SandboxBytes = %d, want %d", res.SandboxBytes, want)
			}
			if res.Stderr != "own stderr" { // the TS replace also takes the newline before a counter line
				t.Errorf("Stderr = %q, want only the code's own line", res.Stderr)
			}
		})
	}
}

// takeCounters gives what the server.ts replace of \n?__CM_…:\d+\n? gives.
func TestTakeCounters(t *testing.T) {
	for _, c := range []struct {
		in, out string
		n       int64
	}{
		{"plain\n", "plain\n", 0},
		{"__CM_FS__:5\n", "", 5},
		{"err\n__CM_FS__:5\n__CM_NET__:7\n", "err", 12},
		{"err__CM_NET__:7", "err", 7},
		{"a\n__CM_FS__:5\nb\n", "ab\n", 5},
	} {
		if out, n := takeCounters(c.in); out != c.out || n != c.n {
			t.Errorf("takeCounters(%q) = %q, %d; want %q, %d", c.in, out, n, c.out, c.n)
		}
	}
}

// Only the instrumented ctx_execute JS/TS wrapper reports counts: the same
// line from another language, or from ctx_execute_file, is plain stderr.
func TestRunCountsOnlyInstrumentedRuns(t *testing.T) {
	e := newTestExec(t)
	res := run(t, e, Request{Language: Shell, Code: `echo __CM_FS__:99 >&2`, Timeout: 10 * time.Second})
	if res.SandboxBytes != 0 || res.Stderr != "__CM_FS__:99\n" {
		t.Fatalf("shell: %+v", res)
	}
	if e.runtime(JavaScript) == "" {
		t.Skip("no JS runtime")
	}
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = run(t, e, Request{Language: JavaScript, Code: `console.error("__CM_NET__:7")`, FilePath: file, Timeout: 30 * time.Second})
	if res.SandboxBytes != 0 || res.Stderr != "__CM_NET__:7\n" {
		t.Fatalf("execute_file: %+v", res)
	}
}
