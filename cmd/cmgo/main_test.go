package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.String() != "cmgo dev\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"nope"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestRunHook(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CMGO_DIR", filepath.Join(dir, "data"))
	t.Setenv("CMGO_PROJECT_DIR", dir)
	var out, errb bytes.Buffer
	if code := run([]string{"hook", "stop"}, strings.NewReader(`{"session_id":"s1"}`), &out, &errb); code != 0 || out.String() != "{}\n" {
		t.Fatalf("stop: exit %d, out %q, stderr %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"hook", "nope"}, strings.NewReader("{}"), &out, &errb); code != 0 || out.Len() != 0 {
		t.Fatalf("unknown event: exit %d, out %q", code, out.String())
	}
	if code := run([]string{"hook"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("missing event: exit %d", code)
	}
}

// doctor and upgrade reach their commands: a usage error exits 2.
func TestRunDispatchesNewCommands(t *testing.T) {
	for _, args := range [][]string{{"doctor", "extra"}, {"upgrade", "--nope"}} {
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb); code != 2 || strings.Contains(errb.String(), "unknown command") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errb.String())
		}
	}
}
