package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// alive reports whether any process matches pgrep's args (e.g. "-f", "sleep 7731").
func alive(args ...string) bool {
	out, _ := exec.Command("pgrep", args...).Output()
	return strings.TrimSpace(string(out)) != ""
}

func newTestExec(t *testing.T) *Executor {
	t.Helper()
	return New(t.TempDir())
}

func run(t *testing.T, e *Executor, r Request) Result {
	t.Helper()
	res, err := e.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestRunHelloEachLanguage(t *testing.T) {
	progs := map[Language]string{
		JavaScript: `const v = await Promise.resolve("hello"); console.log(v)`,
		TypeScript: `const v: string = await Promise.resolve("hello"); console.log(v)`,
		Python:     `print("hello")`,
		Shell:      `echo hello`,
		Ruby:       `puts "hello"`,
		Go:         `fmt.Println("hello")`,
		Rust:       `fn main() { println!("hello"); }`,
		PHP:        `echo "hello\n";`,
		Perl:       `print "hello\n";`,
		R:          `cat("hello\n")`,
		Elixir:     `IO.puts "hello"`,
		CSharp:     `Console.WriteLine("hello");`,
	}
	for _, lang := range Languages {
		t.Run(string(lang), func(t *testing.T) {
			e := newTestExec(t)
			if e.runtime(lang) == "" {
				t.Skipf("%s runtime not installed", lang)
			}
			res := run(t, e, Request{Language: lang, Code: progs[lang], Timeout: 60 * time.Second})
			if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "hello" {
				t.Fatalf("got %+v", res)
			}
		})
	}
}

func TestRunExitCodeAndStreams(t *testing.T) {
	res := run(t, newTestExec(t), Request{Language: Shell, Code: "echo out; echo err >&2; exit 3"})
	if res.Stdout != "out\n" || res.Stderr != "err\n" || res.ExitCode != 3 || res.TimedOut {
		t.Fatalf("got %+v", res)
	}
}

func TestRunCwd(t *testing.T) {
	e := newTestExec(t)
	want, _ := filepath.EvalSymlinks(e.projectDir)
	res := run(t, e, Request{Language: Shell, Code: "pwd -P"})
	if strings.TrimSpace(res.Stdout) != want {
		t.Errorf("default cwd = %q; want %q", res.Stdout, want)
	}
	other := t.TempDir()
	want, _ = filepath.EvalSymlinks(other)
	res = run(t, e, Request{Language: Python, Code: "import os; print(os.path.realpath(os.getcwd()))", Cwd: other})
	if strings.TrimSpace(res.Stdout) != want {
		t.Errorf("cwd override = %q; want %q", res.Stdout, want)
	}
}

func TestRunTempDirRemoved(t *testing.T) {
	res := run(t, newTestExec(t), Request{Language: Shell, Code: `echo "$TMPDIR"; ls "$TMPDIR"`})
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	dir := lines[0]
	if !strings.HasPrefix(filepath.Base(dir), ".cmgo-") || len(lines) < 2 || lines[1] != "script.sh" {
		t.Fatalf("unexpected TMPDIR listing %q", res.Stdout)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp dir %s still exists (err=%v)", dir, err)
	}
}

func TestRunTimeoutKillsGroup(t *testing.T) {
	start := time.Now()
	res := run(t, newTestExec(t), Request{
		Language: Shell,
		Code:     "echo $$; sleep 7731 & sleep 7731",
		Timeout:  300 * time.Millisecond,
	})
	if !res.TimedOut || res.ExitCode != 1 || res.Backgrounded {
		t.Fatalf("got %+v", res)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Run took %v after timeout", d)
	}
	pgid := strings.TrimSpace(res.Stdout) // partial output: the shell's pid = its pgid
	time.Sleep(100 * time.Millisecond)
	if pgid == "" || alive("-g", pgid) || alive("-f", "sleep 7731") {
		t.Fatalf("processes left in pgid %q", pgid)
	}
}

func TestRunCallerCancelKillsGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := newTestExec(t).Run(ctx, Request{Language: Shell, Code: "sleep 7734 & sleep 7734"})
	if err != nil || res.ExitCode != 1 {
		t.Fatalf("got %+v, %v", res, err)
	}
	time.Sleep(100 * time.Millisecond)
	if alive("-f", "sleep 7734") {
		t.Fatal("sleep 7734 survived ctx cancel")
	}
}

func TestRunOutputCap(t *testing.T) {
	e := newTestExec(t)
	e.hardCap = 1 << 20
	res := run(t, e, Request{Language: Shell, Code: "yes cmgocap7732 & yes cmgocap7732 >&2", Timeout: 30 * time.Second})
	if res.TimedOut {
		t.Fatal("cap did not kill before timeout")
	}
	if !strings.HasSuffix(res.Stderr, "\n[output capped at 1MB — process killed]") {
		t.Fatalf("missing marker; stderr tail %q", res.Stderr[max(0, len(res.Stderr)-80):])
	}
	marker := len("\n[output capped at 1MB — process killed]")
	if n := len(res.Stdout) + len(res.Stderr) - marker; n > 1<<20 {
		t.Fatalf("kept %d bytes > cap", n)
	}
	if res.ExitCode != 1 {
		t.Errorf("exit code = %d", res.ExitCode)
	}
	time.Sleep(100 * time.Millisecond)
	if alive("-f", "yes cmgocap7732") {
		t.Fatal("yes survived the cap")
	}
}

func TestRunMissingRuntime(t *testing.T) {
	e := newTestExec(t)
	e.lookPath = func(string) (string, error) { return "", errors.New("nope") }
	e.getenv = func(string) string { return "" }
	_, err := e.Run(context.Background(), Request{Language: Ruby, Code: "puts 1"})
	if err == nil || err.Error() != "Ruby not available. Install ruby." {
		t.Fatalf("err = %v", err)
	}
}

func TestRunSpawnFailureIsResult(t *testing.T) {
	e := newTestExec(t)
	e.runtimes[Python] = "/nonexistent/python3"
	res, err := e.Run(context.Background(), Request{Language: Python, Code: "print(1)"})
	if err != nil || res.ExitCode != 1 || !strings.Contains(res.Stderr, "/nonexistent/python3") {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestRunChildEnvFiltered(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash missing")
	}
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("NODE_OPTIONS", "--require /evil.js")
	t.Setenv("BASH_FUNC_cmgofn%%", "() { echo pwned; }")
	t.Setenv("CMGO_KEEP", "kept")
	res := run(t, newTestExec(t), Request{Language: Shell,
		Code: `type cmgofn >/dev/null 2>&1 && echo IMPORTED; echo "no=[$NODE_OPTIONS] keep=$CMGO_KEEP color=$NO_COLOR lang=$LANG"`})
	if strings.TrimSpace(res.Stdout) != "no=[] keep=kept color=1 lang=en_US.UTF-8" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
}

func TestRunFileContent(t *testing.T) {
	e := newTestExec(t)
	f := filepath.Join(e.projectDir, "it's data.txt")
	os.WriteFile(f, []byte("alpha beta"), 0o644)
	progs := map[Language]string{
		Shell:      `printf '%s|%s' "$FILE_CONTENT" "$file_path"`,
		Python:     `print(FILE_CONTENT + "|" + file_path, end="")`,
		JavaScript: `process.stdout.write(FILE_CONTENT + "|" + file_path)`,
		Go:         `fmt.Print(FILE_CONTENT + "|" + file_path)`,
	}
	for lang, code := range progs {
		t.Run(string(lang), func(t *testing.T) {
			if e.runtime(lang) == "" {
				t.Skipf("%s runtime not installed", lang)
			}
			res := run(t, e, Request{Language: lang, Code: code, FilePath: f, Timeout: 60 * time.Second})
			if res.Stdout != "alpha beta|"+f {
				t.Fatalf("got %+v", res)
			}
		})
	}
	// A relative FilePath resolves against the project dir.
	res := run(t, e, Request{Language: Shell, Code: `printf '%s' "$FILE_CONTENT"`, FilePath: "it's data.txt"})
	if res.Stdout != "alpha beta" {
		t.Fatalf("relative path: %+v", res)
	}
}

func TestRunRustCompileError(t *testing.T) {
	e := newTestExec(t)
	if e.runtime(Rust) == "" {
		t.Skip("rustc not installed")
	}
	res := run(t, e, Request{Language: Rust, Code: "fn main() { nope }"})
	if res.ExitCode != 1 || !strings.HasPrefix(res.Stderr, "Compilation failed:\n") || res.Stdout != "" {
		t.Fatalf("got %+v", res)
	}
}
