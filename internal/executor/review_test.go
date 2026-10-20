package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Review focus: the shell script's first line is `export PATH='<parent PATH>'`.
// A single quote inside a PATH entry must not break that line.
func TestShellPathWithSingleQuote(t *testing.T) {
	odd := filepath.Join(t.TempDir(), "it's bin")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", odd+string(os.PathListSeparator)+os.Getenv("PATH"))
	res, err := New(t.TempDir()).Run(context.Background(), Request{Language: Shell, Code: `printf '%s' "$PATH"`})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.HasPrefix(res.Stdout, odd+":") {
		t.Fatalf("exit %d stdout %q stderr %q", res.ExitCode, res.Stdout, res.Stderr)
	}
}

// Review focus: project directories with spaces, quotes and non-ASCII names
// are the cwd for runs and the base for FILE_CONTENT paths.
func TestProjectDirWithSpacesQuotesUnicode(t *testing.T) {
	project := filepath.Join(t.TempDir(), `my "proj" it's dự án`)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(project)
	ex := New(project)
	res, err := ex.Run(context.Background(), Request{Language: Shell, Code: "pwd -P"})
	if err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != real {
		t.Fatalf("shell cwd: %q (want %q) err=%v stderr=%q", res.Stdout, real, err, res.Stderr)
	}
	file := filepath.Join(project, "a b's.txt")
	if err := os.WriteFile(file, []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = ex.Run(context.Background(), Request{Language: Shell, FilePath: file, Code: `printf '%s' "$FILE_CONTENT" | wc -l | tr -d ' '`})
	if err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "1" {
		t.Fatalf("shell FILE_CONTENT: %q err=%v stderr=%q", res.Stdout, err, res.Stderr)
	}
	if _, lookErr := exec.LookPath("python3"); lookErr == nil {
		res, err = ex.Run(context.Background(), Request{Language: Python, FilePath: file, Code: `print(len(FILE_CONTENT.splitlines()))`})
		if err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "2" {
			t.Fatalf("python FILE_CONTENT: %q err=%v stderr=%q", res.Stdout, err, res.Stderr)
		}
	}
}

// Review focus: binary output and one very long line come back intact in
// size; the MCP layer sanitizes UTF-8, the executor must not truncate.
func TestBinaryAndLongLineOutput(t *testing.T) {
	ex := New(t.TempDir())
	res, err := ex.Run(context.Background(), Request{Language: Shell, Code: "head -c 300000 /dev/urandom"})
	if err != nil || res.ExitCode != 0 || len(res.Stdout) != 300000 {
		t.Fatalf("binary: len %d exit %d err=%v", len(res.Stdout), res.ExitCode, err)
	}
	res, err = ex.Run(context.Background(), Request{Language: Shell, Code: "head -c 2000000 /dev/zero | tr '\\0' 'a'"})
	if err != nil || res.ExitCode != 0 || len(res.Stdout) != 2000000 || strings.Contains(res.Stdout, "\n") {
		t.Fatalf("long line: len %d exit %d err=%v", len(res.Stdout), res.ExitCode, err)
	}
}
