package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installEnv points HOME at a temp dir and PATH at a dir holding only a fake
// claude that logs its arguments, so no test reaches the real ~/.claude.
func installEnv(t *testing.T) (home, claudeLog string) {
	t.Helper()
	home = t.TempDir()
	bin := t.TempDir()
	claudeLog = filepath.Join(bin, "claude.log")
	script := "#!/bin/sh\necho \"$@\" >> '" + claudeLog + "'\necho fake-claude-out\necho fake-claude-err >&2\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("PATH", bin)
	return home, claudeLog
}

// fakeBinaries makes a dir holding cmgo and cmgo-hook and returns cmgo's path.
func fakeBinaries(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range []string{"cmgo", "cmgo-hook"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "cmgo")
}

func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestInstallAndUninstall(t *testing.T) {
	home, claudeLog := installEnv(t)
	cmgo := fakeBinaries(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	code, out, errs := runCmd(t, "install", "--binary", cmgo)
	if code != 0 || !strings.Contains(out, "cmgo: wrote "+settings) || !strings.Contains(out, "fake-claude-out") || !strings.Contains(errs, "fake-claude-err") {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), `"command": "\"`+cmgo+`-hook\" pretooluse"`) {
		t.Fatalf("settings:\n%s", b)
	}
	// claude.json now says cmgo is registered, as claude mcp add would leave it.
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"cmgo":{}}}`), 0o644)
	if code, out, errs := runCmd(t, "uninstall"); code != 0 {
		t.Fatalf("uninstall: exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
	if b, _ := os.ReadFile(settings); strings.Contains(string(b), "cmgo") {
		t.Fatalf("settings after uninstall:\n%s", b)
	}
	log, _ := os.ReadFile(claudeLog)
	want := "mcp add --scope user cmgo -- " + cmgo + " serve\nmcp remove --scope user cmgo\n"
	if string(log) != want {
		t.Fatalf("claude calls %q, want %q", log, want)
	}
}

func TestInstallConfigDirAndNoClaude(t *testing.T) {
	home, _ := installEnv(t)
	cmgo := fakeBinaries(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "~/cfg")
	t.Setenv("PATH", t.TempDir()) // no claude on PATH: print the command
	code, out, errs := runCmd(t, "install", "--binary", cmgo)
	settings := filepath.Join(home, "cfg", "settings.json")
	if code != 0 || !strings.Contains(out, "cmgo: wrote "+settings) ||
		!strings.Contains(out, "run: claude mcp add --scope user cmgo -- "+cmgo+" serve\n") {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
}

// A dry run needs no cmgo-hook on disk: it only shows the file.
func TestInstallDefaultBinaryDryRun(t *testing.T) {
	home, claudeLog := installEnv(t)
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	code, out, errs := runCmd(t, "install", "--dry-run")
	if code != 0 || !strings.Contains(out, `\"`+filepath.Join(filepath.Dir(exe), "cmgo-hook")+`\" sessionstart`) {
		t.Fatalf("exit %d\nstdout %q\nstderr %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Fatal("dry run wrote the settings file")
	}
	if _, err := os.Stat(claudeLog); err == nil {
		t.Fatal("dry run ran claude")
	}
}

func TestInstallErrors(t *testing.T) {
	home, claudeLog := installEnv(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	alone := filepath.Join(t.TempDir(), "cmgo") // no cmgo-hook next to it
	if code, _, errs := runCmd(t, "install", "--binary", alone); code != 1 || !strings.HasPrefix(errs, "cmgo: hook binary missing: ") {
		t.Fatalf("no cmgo-hook: exit %d, stderr %q", code, errs)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Fatal("settings written without cmgo-hook")
	}
	if _, err := os.Stat(claudeLog); err == nil {
		t.Fatal("claude ran without cmgo-hook")
	}
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte("{not json"), 0o644)
	if code, _, errs := runCmd(t, "install", "--binary", fakeBinaries(t)); code != 1 || !strings.HasPrefix(errs, "cmgo: parse ") {
		t.Fatalf("unparseable: exit %d, stderr %q", code, errs)
	}
	if b, _ := os.ReadFile(settings); string(b) != "{not json" {
		t.Fatalf("settings changed: %q", b)
	}
	if code, _, _ := runCmd(t, "install", "--nope"); code != 2 {
		t.Fatalf("bad flag: exit %d", code)
	}
	if code, _, _ := runCmd(t, "uninstall", "--binary", "/x"); code != 2 {
		t.Fatalf("uninstall --binary: exit %d", code)
	}
	if code, _, _ := runCmd(t, "install", "extra"); code != 2 {
		t.Fatalf("extra argument: exit %d", code)
	}
}
