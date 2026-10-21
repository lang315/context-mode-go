package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/install"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckHooks(t *testing.T) {
	all := []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "SessionStart", "Stop", "PreCompact"}
	p := "/c/settings.json"
	cases := []struct {
		name string
		st   install.Status
		err  error
		want string
	}{
		{"all", install.Status{Events: all}, nil, "[OK] Hooks: 7/7 events registered in /c/settings.json"},
		{"some missing", install.Status{Events: all[:4]}, nil, "[WARN] Hooks: missing SessionStart, Stop, PreCompact — fix: cmgo install"},
		{"none", install.Status{}, nil, "[WARN] Hooks: missing " + strings.Join(all, ", ") + " — fix: cmgo install"},
		{"unreadable", install.Status{}, errors.New("parse /c/settings.json: bad"), "[FAIL] Hooks: could not read /c/settings.json — parse /c/settings.json: bad"},
	}
	for _, c := range cases {
		if got := checkHooks(p, c.st, c.err); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHookBinaryPath(t *testing.T) {
	if got := hookBinaryPath(install.Status{HookBinary: "/s/cmgo-hook"}, "/b/cmgo"); got != "/s/cmgo-hook" {
		t.Errorf("from settings: %q", got)
	}
	if got := hookBinaryPath(install.Status{}, "/b/cmgo"); got != "/b/cmgo-hook" {
		t.Errorf("next to cmgo: %q", got)
	}
}

func TestCheckHookBinary(t *testing.T) {
	dir := t.TempDir()
	p := script(t, dir, "cmgo-hook", "exit 0")
	if got := checkHookBinary(p); got != "[OK] Hook binary: "+p {
		t.Errorf("got %q", got)
	}
	missing := filepath.Join(dir, "none")
	if got := checkHookBinary(missing); got != "[FAIL] Hook binary: not found at "+missing+" — fix: cmgo install" {
		t.Errorf("got %q", got)
	}
	if got := checkHookBinary(dir); got != "[FAIL] Hook binary: not found at "+dir+" — fix: cmgo install" {
		t.Errorf("dir: got %q", got)
	}
}

func TestCheckVersion(t *testing.T) {
	dir := t.TempDir()
	same := script(t, dir, "same", `[ "$1" = version ] && echo "cmgo-hook v1.0.0"`)
	other := script(t, dir, "other", `echo "cmgo-hook v0.9.0"`)
	silent := script(t, dir, "silent", "exit 0")
	slow := script(t, dir, "slow", "exec sleep 10")
	// A fresh script's first run can take seconds on a loaded macOS box, so
	// only the timeout case is short, and only it is timed.
	cases := []struct {
		name, path string
		timeout    time.Duration
		want       string
	}{
		{"same", same, time.Minute, "[OK] Version: cmgo v1.0.0 (hook binary v1.0.0)"},
		{"differs", other, time.Minute, "[WARN] Version: cmgo v1.0.0 (hook binary v0.9.0 differs) — fix: cmgo upgrade"},
		{"old hook binary", silent, time.Minute, "[WARN] Version: cmgo v1.0.0 (hook binary version unknown: no output) — fix: cmgo upgrade"},
		{"timeout", slow, 300 * time.Millisecond, "[WARN] Version: cmgo v1.0.0 (hook binary version unknown: timed out after 300ms) — fix: cmgo upgrade"},
	}
	for _, c := range cases {
		e := Env{Version: "v1.0.0", HookTimeout: c.timeout}
		start := time.Now()
		if got := checkVersion(context.Background(), e, c.path); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if d := time.Since(start); c.timeout < time.Second && d > 3*time.Second {
			t.Errorf("%s: took %v", c.name, d)
		}
	}
	missing := filepath.Join(dir, "none")
	got := checkVersion(context.Background(), Env{Version: "v1"}, missing)
	if !strings.HasPrefix(got, "[WARN] Version: cmgo v1 (hook binary version unknown: ") {
		t.Errorf("missing: got %q", got)
	}
	// The injected runner replaces the process.
	e := Env{Version: "v1", HookVersion: func(_ context.Context, p string) (string, error) { return "v1", nil }}
	if got := checkVersion(context.Background(), e, missing); got != "[OK] Version: cmgo v1 (hook binary v1)" {
		t.Errorf("injected: got %q", got)
	}
}

func TestCheckMCP(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude.json")
	if got := checkMCP(p, "/b/cmgo"); got != "[WARN] MCP server: not registered — fix: cmgo install" {
		t.Errorf("no file: %q", got)
	}
	os.WriteFile(p, []byte(`{"mcpServers":{"cmgo":{"type":"stdio","command":"/b/cmgo","args":["serve"]}}}`), 0o600)
	if got := checkMCP(p, "/b/cmgo"); got != "[OK] MCP server: registered (/b/cmgo serve)" {
		t.Errorf("registered: %q", got)
	}
	if got := checkMCP(p, "/new/cmgo"); got != "[WARN] MCP server: registered at /b/cmgo — fix: cmgo install" {
		t.Errorf("old path: %q", got)
	}
}

func TestCheckStatusLine(t *testing.T) {
	cases := []struct {
		st   install.Status
		want string
	}{
		{install.Status{}, "[OK] Statusline: not set"},
		{install.Status{StatusLine: "/b/cmgo-hook statusline", OwnStatusLine: true}, "[OK] Statusline: cmgo"},
		{install.Status{StatusLine: "ccline"}, "[WARN] Statusline: another command is set (ccline)"},
	}
	for _, c := range cases {
		if got := checkStatusLine(c.st); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestCheckTSPlugin(t *testing.T) {
	if got := checkTSPlugin(install.Status{}); got != "[OK] TS plugin: not enabled" {
		t.Errorf("got %q", got)
	}
	got := checkTSPlugin(install.Status{TSPlugins: []string{"context-mode@context-mode", "context-mode@fork"}})
	want := "[WARN] TS plugin: context-mode@context-mode is enabled — both inject routing text; fix: cmgo install --disable-ts-plugin\n" +
		"[WARN] TS plugin: context-mode@fork is enabled — both inject routing text; fix: cmgo install --disable-ts-plugin"
	if got != want {
		t.Errorf("got %q", got)
	}
}

func TestCheckSkills(t *testing.T) {
	cases := []struct {
		r    install.SkillReport
		err  error
		want string
	}{
		{install.SkillReport{Dir: "/c/skills", Current: []string{"a", "b", "c", "d", "e"}}, nil, "[OK] Skills: 5/5 in /c/skills"},
		{install.SkillReport{Dir: "/c/skills", Current: []string{"a", "b"}, Missing: []string{"ctx-stats", "ctx-purge"}, Outdated: []string{"ctx-search"}}, nil,
			"[WARN] Skills: 2/5 in /c/skills; missing ctx-stats, ctx-purge; outdated ctx-search — fix: cmgo install"},
		{install.SkillReport{Dir: "/c/skills", Current: []string{"a", "b", "c", "d"}, Foreign: []string{"ctx-doctor"}}, nil,
			"[WARN] Skills: 4/5 in /c/skills; not cmgo's or edited (left as is): ctx-doctor"},
		{install.SkillReport{Dir: "/c/skills"}, errors.New("boom"), "[WARN] Skills: could not read /c/skills — boom"},
	}
	for _, c := range cases {
		if got := checkSkills(c.r, c.err); got != c.want {
			t.Errorf("got  %q\nwant %q", got, c.want)
		}
	}
}
