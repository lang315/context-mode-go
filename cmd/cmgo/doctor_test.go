package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/doctor"
	"github.com/lang315/context-mode-go/internal/install"
)

// doctorHome points HOME, CMGO_DIR and the project at a temp dir.
func doctorHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CMGO_DIR", filepath.Join(root, "data"))
	t.Setenv("CMGO_PROJECT_DIR", root)
	return root
}

func TestDoctorCmdFails(t *testing.T) {
	doctorHome(t)
	var out, errb bytes.Buffer
	// No settings file: the hook binary is looked for next to the test binary.
	code := doctorCmd(nil, &out, &errb)
	if code != 1 || !strings.HasPrefix(out.String(), "cmgo doctor\n\n[") || !strings.Contains(out.String(), "\n[FAIL] Hook binary: not found at ") {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "\n[WARN] Hooks: missing PreToolUse, ") || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestDoctorCmdPasses(t *testing.T) {
	root := doctorHome(t)
	hook := filepath.Join(root, "bin", "cmgo-hook")
	os.MkdirAll(filepath.Dir(hook), 0o755)
	os.WriteFile(hook, []byte("#!/bin/sh\necho cmgo-hook "+version+"\n"), 0o755)
	err := install.Install(install.Options{
		SettingsPath: filepath.Join(root, "home", ".claude", "settings.json"), ClaudeJSONPath: filepath.Join(root, "home", ".claude.json"),
		Binary: filepath.Join(root, "bin", "cmgo"), HookBinary: hook, Stdout: io.Discard, Stderr: io.Discard,
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
	})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := doctorCmd(nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	// The Version line is not checked: the fake script may miss the 2 s
	// limit under load, and a WARN does not change the exit code.
	if !strings.Contains(out.String(), "\n[OK] Hooks: 7/7 events registered in ") || !strings.Contains(out.String(), "\n[OK] Hook binary: "+hook+"\n") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestDoctorCmdUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := doctorCmd([]string{"extra"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

// Doctor reads the .claude.json that install writes, for every way of setting
// CLAUDE_CONFIG_DIR, including an explicit default dir.
func TestDoctorReadsTheClaudeJSONInstallWrites(t *testing.T) {
	root := doctorHome(t)
	home := filepath.Join(root, "home")
	for _, dir := range []string{"", filepath.Join(home, ".claude"), "~/.claude", "~", "/cfg"} {
		t.Setenv("CLAUDE_CONFIG_DIR", dir)
		cfg, err := config.Load(os.Getenv, os.Getwd)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := doctor.NewEnv(cfg, "v", nil).ClaudeJSONPath, claudeJSONPath(home); got != want {
			t.Errorf("CLAUDE_CONFIG_DIR=%q: doctor reads %s, install writes %s", dir, got, want)
		}
	}
}
