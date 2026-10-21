package routing

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
)

func TestGuidanceOnce(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run") // created on first use
	if !guidanceOnce(run, "s1", "bash") {
		t.Fatal("first bash guidance not shown")
	}
	if guidanceOnce(run, "s1", "bash") {
		t.Error("second bash guidance shown")
	}
	if !guidanceOnce(run, "s1", "read") {
		t.Error("throttle is not per type")
	}
	if !guidanceOnce(run, "s2", "bash") {
		t.Error("throttle is not per session")
	}
	if fi, err := os.Stat(filepath.Join(run, "guidance-s1", "bash")); err != nil || fi.Size() != 0 {
		t.Errorf("marker: %v, %v; want an empty file", fi, err)
	}
}

func TestGuidanceOnceUnsafeSessionID(t *testing.T) {
	run := t.TempDir()
	if !guidanceOnce(run, "../escape", "grep") {
		t.Fatal("guidance not shown")
	}
	if _, err := os.Stat(filepath.Join(run, "guidance-"+hookio.FileID("../escape"), "grep")); err != nil {
		t.Error(err)
	}
}

func TestGuidanceOnceUnwritable(t *testing.T) {
	// TS: any error creating the marker means no guidance.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if guidanceOnce(filepath.Join(file, "run"), "s1", "bash") {
		t.Error("guidance shown although the marker cannot be created")
	}
}

func TestPruneGuidance(t *testing.T) {
	run := t.TempDir()
	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	mk := func(name string, dir bool, mtime time.Time) string {
		p := filepath.Join(run, name)
		var err error
		if dir {
			err = os.MkdirAll(filepath.Join(p, "bash"), 0o700)
		} else {
			err = os.WriteFile(p, nil, 0o600)
		}
		if err == nil {
			err = os.Chtimes(p, mtime, mtime)
		}
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	gone := mk("guidance-old", true, old)
	kept := []string{
		mk("guidance-new", true, now),
		mk("other-old", true, old),
		mk("guidance-file", false, old),
		mk("mcp-ready-1", false, old),
	}
	PruneGuidance(run, now, 7*24*time.Hour)
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("old guidance dir kept: %v", err)
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(p), err)
		}
	}
	PruneGuidance(filepath.Join(run, "missing"), now, time.Hour) // no panic
}
