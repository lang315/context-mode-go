package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeCmdDryRun(t *testing.T) {
	t.Setenv("CMGO_SOURCE", "")
	t.Setenv("GOBIN", "/gobin")
	var out, errb bytes.Buffer
	if code := upgradeCmd([]string{"--dry-run"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "dry run: GOPRIVATE=") || !strings.HasSuffix(out.String(), "dry run: /gobin/cmgo install\n") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestUpgradeCmdBadSource(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := upgradeCmd([]string{"--source", dir, "--dry-run"}, &out, &errb); code != 1 || !strings.HasPrefix(out.String(), "[ ] source "+dir) {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

func TestUpgradeCmdUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := upgradeCmd([]string{"--nope"}, &out, &errb); code != 2 {
		t.Fatalf("bad flag: exit %d", code)
	}
	if code := upgradeCmd([]string{"extra"}, &out, &errb); code != 2 {
		t.Fatalf("extra arg: exit %d", code)
	}
}

// runCommand runs in dir with the extra env and streams output.
func TestRunCommand(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	var out bytes.Buffer
	if err := runCommand(&out)(dir, []string{"CMGO_TEST_X=1"}, "sh", "-c", `echo "$CMGO_TEST_X $(pwd)"; echo err >&2`); err != nil {
		t.Fatal(err)
	}
	if out.String() != "1 "+dir+"\nerr\n" {
		t.Fatalf("got %q", out.String())
	}
	if err := runCommand(&out)("", nil, "sh", "-c", "exit 3"); err == nil {
		t.Fatal("exit 3: no error")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
