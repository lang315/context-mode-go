package routing

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// deadPID is above every pid_max (TS tests use the same value).
const deadPID = "2147483647"

func writeSentinelFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestMCPReadyMissingDir(t *testing.T) {
	if MCPReady(filepath.Join(t.TempDir(), "run")) {
		t.Error("ready without a run dir")
	}
}

func TestMCPReadyLivePID(t *testing.T) {
	dir := t.TempDir()
	p := writeSentinelFile(t, dir, "mcp-ready-x", strconv.Itoa(os.Getpid()))
	if !MCPReady(dir) {
		t.Error("live pid not ready")
	}
	if !exists(p) {
		t.Error("live sentinel removed")
	}
}

func TestMCPReadyEPERMIsLive(t *testing.T) {
	// pid 1 belongs to root: kill(1, 0) is EPERM for a normal user, nil for root.
	dir := t.TempDir()
	writeSentinelFile(t, dir, "mcp-ready-1", "1")
	if !MCPReady(dir) {
		t.Error("pid 1 not ready")
	}
}

func TestMCPReadyRemovesDead(t *testing.T) {
	dir := t.TempDir()
	dead := writeSentinelFile(t, dir, "mcp-ready-"+deadPID, deadPID)
	zero := writeSentinelFile(t, dir, "mcp-ready-0", "0") // kill(0, 0) would signal our own group
	if MCPReady(dir) {
		t.Error("dead pids reported ready")
	}
	if exists(dead) || exists(zero) {
		t.Error("dead sentinels kept")
	}
}

func TestMCPReadySkipsGarbage(t *testing.T) {
	// parseInt NaN: TS skips the file and leaves it (it may be mid-write).
	dir := t.TempDir()
	empty := writeSentinelFile(t, dir, "mcp-ready-empty", "")
	garbage := writeSentinelFile(t, dir, "mcp-ready-garbage", "abc")
	other := writeSentinelFile(t, dir, "other-"+deadPID, deadPID)
	if MCPReady(dir) {
		t.Error("garbage reported ready")
	}
	if !exists(empty) || !exists(garbage) || !exists(other) {
		t.Error("a skipped file was removed")
	}
	writeSentinelFile(t, dir, "mcp-ready-live", " "+strconv.Itoa(os.Getpid())+"\n") // parseInt skips spaces
	if !MCPReady(dir) {
		t.Error("padded live pid not ready")
	}
}

func TestWriteSentinel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cmgo", "run")
	remove, err := WriteSentinel(dir, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "mcp-ready-"+strconv.Itoa(os.Getpid()))
	if b, err := os.ReadFile(p); err != nil || string(b) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("sentinel = %q, %v", b, err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("run dir mode: %v, %v; want 0700", fi, err)
	}
	if !MCPReady(dir) {
		t.Error("not ready after WriteSentinel")
	}
	remove()
	if exists(p) || MCPReady(dir) {
		t.Error("sentinel still there after remove")
	}
}

func TestWriteSentinelError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSentinel(filepath.Join(file, "run"), 1); err == nil {
		t.Error("no error for a run dir under a file")
	}
}
