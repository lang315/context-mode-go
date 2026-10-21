package doctor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/install"
)

// healthyEnv installs cmgo into a temp Claude config dir with a fake
// cmgo-hook script, so every check passes.
func healthyEnv(t *testing.T) Env {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "cmgo")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	script(t, filepath.Dir(bin), "cmgo-hook", `echo "cmgo-hook v1.2.3"`)
	e := Env{
		Version:        "v1.2.3",
		Binary:         bin,
		DataDir:        filepath.Join(root, "data"),
		DBPath:         filepath.Join(root, "data", "cmgo.db"),
		SettingsPath:   filepath.Join(root, "claude", "settings.json"),
		ClaudeJSONPath: filepath.Join(root, ".claude.json"),
		Exec:           &fakeExec{res: executor.Result{Stdout: "ok\n"}, langs: []executor.Language{executor.Shell}},
		HookTimeout:    time.Minute, // a fresh script can be slow to start under load
	}
	err := install.Install(install.Options{
		SettingsPath: e.SettingsPath, ClaudeJSONPath: e.ClaudeJSONPath, Binary: bin,
		Stdout: io.Discard, Stderr: io.Discard,
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(e.ClaudeJSONPath, []byte(`{"mcpServers":{"cmgo":{"command":"`+bin+`","args":["serve"]}}}`), 0o600)
	return e
}

func TestReportHealthy(t *testing.T) {
	e := healthyEnv(t)
	text, failed := Report(context.Background(), e)
	if failed {
		t.Errorf("failed on a healthy install:\n%s", text)
	}
	hook := filepath.Join(filepath.Dir(e.Binary), "cmgo-hook")
	want := strings.Join([]string{
		"cmgo doctor",
		"",
		"[OK] Version: cmgo v1.2.3 (hook binary v1.2.3)",
		"[OK] Storage: " + e.DataDir + " (writable)",
		"[OK] Database: " + e.DBPath + " schema v@, FTS5 PASS",
		"[OK] Executor: PASS (shell)",
		"[OK] Runtimes: 1/12 — shell",
		"[OK] Hooks: 7/7 events registered in " + e.SettingsPath,
		"[OK] Hook binary: " + hook,
		"[OK] MCP server: registered (" + e.Binary + " serve)",
		"[OK] Skills: 5/5 in " + filepath.Join(filepath.Dir(e.SettingsPath), "skills"),
		"[OK] Statusline: cmgo",
		"[OK] TS plugin: not enabled",
	}, "\n")
	if got := regexp.MustCompile(`schema v\d+`).ReplaceAllString(text, "schema v@"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReportFailures(t *testing.T) {
	e := healthyEnv(t)
	os.Remove(filepath.Join(filepath.Dir(e.Binary), "cmgo-hook"))
	text, failed := Report(context.Background(), e)
	if !failed || !strings.Contains(text, "\n[FAIL] Hook binary: not found at ") {
		t.Errorf("missing hook binary: failed=%v\n%s", failed, text)
	}

	// An unreadable settings file fails Hooks and drops the lines read from it.
	e = healthyEnv(t)
	os.WriteFile(e.SettingsPath, []byte("{"), 0o644)
	text, failed = Report(context.Background(), e)
	if !failed || !strings.Contains(text, "\n[FAIL] Hooks: could not read "+e.SettingsPath+" — ") || strings.Contains(text, "Statusline") || strings.Contains(text, "TS plugin") {
		t.Errorf("unreadable settings: failed=%v\n%s", failed, text)
	}

	// WARN lines alone do not fail.
	e = healthyEnv(t)
	os.Remove(e.ClaudeJSONPath)
	text, failed = Report(context.Background(), e)
	if failed || !strings.Contains(text, "\n[WARN] MCP server: not registered") {
		t.Errorf("warn only: failed=%v\n%s", failed, text)
	}
}
