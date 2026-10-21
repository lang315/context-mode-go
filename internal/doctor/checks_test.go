package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
)

// fakeExec answers Run with a fixed result and records the request.
type fakeExec struct {
	res   executor.Result
	err   error
	langs []executor.Language
	got   executor.Request
}

func (f *fakeExec) Run(_ context.Context, r executor.Request) (executor.Result, error) {
	f.got = r
	return f.res, f.err
}

func (f *fakeExec) Available() []executor.Language { return f.langs }

func TestNewEnv(t *testing.T) {
	home := "/h"
	cases := []struct{ claudeDir, want string }{
		{"/h/.claude", "/h/.claude.json"},
		{"/cfg", "/cfg/.claude.json"},
	}
	for _, c := range cases {
		cfg := config.Config{DataDir: "/d", DBPath: "/d/cmgo.db", Home: home, ClaudeDir: c.claudeDir}
		e := NewEnv(cfg, "v1", nil)
		if e.ClaudeJSONPath != c.want || e.SettingsPath != c.claudeDir+"/settings.json" || e.DataDir != "/d" || e.DBPath != "/d/cmgo.db" || e.Version != "v1" {
			t.Errorf("%s: %+v", c.claudeDir, e)
		}
		if !filepath.IsAbs(e.Binary) {
			t.Errorf("Binary %q is not absolute", e.Binary)
		}
	}
}

func TestCheckStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cmgo")
	if got, want := checkStorage(dir), "[OK] Storage: "+dir+" (writable)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("probe file left behind: %v", left)
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o644)
	under := filepath.Join(file, "cmgo")
	if got := checkStorage(under); !strings.HasPrefix(got, "[FAIL] Storage: "+under+" — ") {
		t.Errorf("got %q", got)
	}
}

func TestCheckDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmgo.db")
	got := checkDatabase(path)
	if !strings.HasPrefix(got, "[OK] Database: "+path+" schema v") || !strings.HasSuffix(got, ", FTS5 PASS") {
		t.Errorf("got %q", got)
	}
	bad := filepath.Join(t.TempDir(), "cmgo.db")
	os.WriteFile(bad, []byte(strings.Repeat("not a database ", 100)), 0o644)
	if got := checkDatabase(bad); !strings.HasPrefix(got, "[FAIL] Database: ") {
		t.Errorf("got %q", got)
	}
}

func TestCheckExecutor(t *testing.T) {
	cases := []struct {
		name string
		f    *fakeExec
		want string
	}{
		{"pass", &fakeExec{res: executor.Result{Stdout: "ok\n"}}, "[OK] Executor: PASS (shell)"},
		{"exit code", &fakeExec{res: executor.Result{ExitCode: 127, Stderr: "  sh: echo: not found\n"}}, "[FAIL] Executor: FAIL — exit 127 (sh: echo: not found)"},
		{"wrong output", &fakeExec{res: executor.Result{Stdout: "ko"}}, "[FAIL] Executor: FAIL — exit 0"},
		{"long stderr", &fakeExec{res: executor.Result{ExitCode: 1, Stderr: strings.Repeat("é", 300)}}, "[FAIL] Executor: FAIL — exit 1 (" + strings.Repeat("é", 200) + ")"},
		{"cannot run", &fakeExec{err: errors.New("no shell")}, "[FAIL] Executor: FAIL — no shell"},
	}
	for _, c := range cases {
		if got := checkExecutor(context.Background(), c.f, 5*time.Second); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if r := c.f.got; r.Language != executor.Shell || r.Code != "echo ok" || r.Timeout != 5*time.Second {
			t.Errorf("%s: request %+v", c.name, r)
		}
	}
}

// The real executor runs the probe through the shell.
func TestCheckExecutorReal(t *testing.T) {
	ex := executor.New(t.TempDir())
	defer ex.Shutdown()
	if got := checkExecutor(context.Background(), ex, 5*time.Second); got != "[OK] Executor: PASS (shell)" {
		t.Fatalf("got %q", got)
	}
}

func TestCheckRuntimes(t *testing.T) {
	f := &fakeExec{langs: []executor.Language{executor.JavaScript, executor.Shell, executor.Go}}
	if got, want := checkRuntimes(f), "[OK] Runtimes: 3/12 — javascript, shell, go"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
