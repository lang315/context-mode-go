package install

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fake records what Install and Uninstall print and run.
type fake struct {
	stdout, stderr bytes.Buffer
	claude         bool     // LookPath("claude") succeeds
	runs           []string // each Run call, name and args joined by spaces
}

// newOpts returns options over a fresh temp dir whose settings dir does not exist yet.
func newOpts(t *testing.T) (Options, *fake) {
	t.Helper()
	dir := t.TempDir()
	f := &fake{}
	return Options{
		SettingsPath:   filepath.Join(dir, "claude", "settings.json"),
		ClaudeJSONPath: filepath.Join(dir, ".claude.json"),
		Binary:         bin,
		Stdout:         &f.stdout,
		Stderr:         &f.stderr,
		LookPath: func(name string) (string, error) {
			if f.claude && name == "claude" {
				return "/usr/local/bin/claude", nil
			}
			return "", exec.ErrNotFound
		},
		Run: func(name string, args ...string) error {
			f.runs = append(f.runs, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	}, f
}

func writeSettings(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// freshHooks is the file Install writes when it starts from {}.
func freshHooks(t *testing.T, binary string) string {
	t.Helper()
	root := newObject()
	if err := addHooks(root, binary); err != nil {
		t.Fatal(err)
	}
	return string(encodeJSON(root))
}

// commands returns, per matcher entry of event, the inner command strings.
func commands(t *testing.T, content, event string) [][]string {
	t.Helper()
	hooks, _ := mustParse(t, content).get("hooks")
	ev, _ := hooks.(*object).get(event)
	var out [][]string
	for _, e := range ev.([]any) {
		inner, _ := e.(*object).get("hooks")
		var cmds []string
		for _, h := range inner.([]any) {
			c, _ := h.(*object).get("command")
			cmds = append(cmds, c.(string))
		}
		out = append(out, cmds)
	}
	return out
}

const userSettings = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "user.sh"
          },
          {
            "type": "command",
            "command": "\"/old/cmgo\" hook pretooluse"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "say done",
            "timeout": 5
          }
        ]
      }
    ]
  },
  "theme": "dark"
}
`

func TestInstallMissingFile(t *testing.T) {
	o, f := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, bin); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	fi, _ := os.Stat(o.SettingsPath)
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if exists(o.SettingsPath + ".cmgo-bak") {
		t.Fatal("backup of a missing file")
	}
	if !strings.Contains(f.stdout.String(), "wrote "+o.SettingsPath) {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestInstallKeepsUserHooks(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, userSettings, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	own := func(arg string) []string { return []string{hookCommand(bin, arg)} }
	if c := commands(t, got, "PreToolUse"); !slices.EqualFunc(c, [][]string{{"user.sh"}, own("pretooluse")}, slices.Equal) {
		t.Fatalf("PreToolUse %q", c)
	}
	if c := commands(t, got, "Stop"); !slices.EqualFunc(c, [][]string{{"say done"}, own("stop")}, slices.Equal) {
		t.Fatalf("Stop %q", c)
	}
	if c := commands(t, got, "PostToolUse"); !slices.EqualFunc(c, [][]string{own("posttooluse")}, slices.Equal) {
		t.Fatalf("PostToolUse %q", c)
	}
	root := mustParse(t, got)
	if strings.Join(root.keys, ",") != "model,hooks,theme" {
		t.Fatalf("keys %v", root.keys)
	}
	if !strings.Contains(got, `"timeout": 5`) || strings.Contains(got, "/old/") {
		t.Fatalf("got\n%s", got)
	}
	if readFile(t, o.SettingsPath+".cmgo-bak") != userSettings {
		t.Fatal("backup is not the original")
	}
}

func TestInstallReplacesOldPath(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, freshHooks(t, "/old place/cmgo"), 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, o.SettingsPath), freshHooks(t, bin); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestInstallIdempotent(t *testing.T) {
	for name, orig := range map[string]string{"missing": "", "user hooks": userSettings} {
		t.Run(name, func(t *testing.T) {
			o, f := newOpts(t)
			if orig != "" {
				writeSettings(t, o.SettingsPath, orig, 0o644)
			}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			first := readFile(t, o.SettingsPath)
			f.stdout.Reset()
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if second := readFile(t, o.SettingsPath); second != first {
				t.Fatalf("second install changed the file:\n%s\nfirst:\n%s", second, first)
			}
			if !strings.Contains(f.stdout.String(), "is up to date") {
				t.Fatalf("stdout %q", f.stdout.String())
			}
			bak := o.SettingsPath + ".cmgo-bak"
			if orig == "" && exists(bak) {
				t.Fatal("backup created")
			}
			if orig != "" && readFile(t, bak) != orig {
				t.Fatal("backup overwritten")
			}
		})
	}
}

func TestInstallUnparseable(t *testing.T) {
	for _, bad := range []string{`{"a": 1,`, `[]`, `{"hooks": []}`, `{"hooks": {"Stop": {}}}`} {
		o, f := newOpts(t)
		writeSettings(t, o.SettingsPath, bad, 0o644)
		err := Install(o)
		if err == nil || !strings.Contains(err.Error(), o.SettingsPath) {
			t.Fatalf("%s: err %v", bad, err)
		}
		if readFile(t, o.SettingsPath) != bad || exists(o.SettingsPath+".cmgo-bak") || len(f.runs) != 0 {
			t.Fatalf("%s: wrote or ran something", bad)
		}
		if err := Uninstall(o); bad == `{"a": 1,` && err == nil {
			t.Fatal("uninstall parsed a broken file")
		}
	}
}

func TestInstallBlankFile(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, " \n", 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if readFile(t, o.SettingsPath) != freshHooks(t, bin) {
		t.Fatal("blank file not treated as {}")
	}
}

func TestInstallBadBinary(t *testing.T) {
	for _, b := range []string{"cmgo", `/a"b/cmgo`} {
		o, _ := newOpts(t)
		o.Binary = b
		if err := Install(o); err == nil || exists(o.SettingsPath) {
			t.Fatalf("%s: err %v", b, err)
		}
	}
}

func TestUninstallRoundTrip(t *testing.T) {
	plainUser := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "user.sh"
          }
        ]
      }
    ],
    "Notification": []
  },
  "model": "opus"
}
`
	origs := map[string]string{
		"no hooks key": "{\n  \"model\": \"opus\",\n  \"n\": 1.50,\n  \"s\": \"<a> & b\"\n}\n",
		"user hooks":   plainUser,
	}
	for name, orig := range origs {
		t.Run(name, func(t *testing.T) {
			o, _ := newOpts(t)
			writeSettings(t, o.SettingsPath, orig, 0o644)
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if err := Uninstall(o); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, o.SettingsPath); got != orig {
				t.Fatalf("got\n%s\nwant\n%s", got, orig)
			}
		})
	}
}

func TestUninstallNothingToRemove(t *testing.T) {
	o, f := newOpts(t)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if exists(o.SettingsPath) {
		t.Fatal("uninstall created a settings file")
	}
	// Four-space indent: nothing of ours, so no reformatting either.
	orig := "{\n    \"hooks\": {}\n}\n"
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if readFile(t, o.SettingsPath) != orig || exists(o.SettingsPath+".cmgo-bak") {
		t.Fatal("file rewritten")
	}
	if !strings.Contains(f.stdout.String(), "no cmgo hooks in") {
		t.Fatalf("stdout %q", f.stdout.String())
	}
}

func TestTSPlugin(t *testing.T) {
	orig := `{"enabledPlugins": {"other@x": true, "context-mode@context-mode": true, "context-mode@old": false}}`
	o, f := newOpts(t)
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.stderr.String(), "warning: plugin context-mode@context-mode is enabled") || strings.Contains(f.stderr.String(), "@old") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
	if !strings.Contains(readFile(t, o.SettingsPath), `"context-mode@context-mode": true`) {
		t.Fatal("plugin changed without DisableTSPlugin")
	}

	o, f = newOpts(t)
	o.DisableTSPlugin = true
	writeSettings(t, o.SettingsPath, orig, 0o644)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, o.SettingsPath)
	if !strings.Contains(got, `"context-mode@context-mode": false`) || !strings.Contains(got, `"other@x": true`) {
		t.Fatalf("got\n%s", got)
	}
	if !strings.Contains(f.stderr.String(), "disabled plugin context-mode@context-mode") {
		t.Fatalf("stderr %q", f.stderr.String())
	}
}

func TestDryRun(t *testing.T) {
	for name, orig := range map[string]string{"missing": "", "user hooks": userSettings} {
		t.Run(name, func(t *testing.T) {
			o, f := newOpts(t)
			if orig != "" {
				writeSettings(t, o.SettingsPath, orig, 0o644)
			}
			o.DryRun = true
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if orig == "" && exists(filepath.Dir(o.SettingsPath)) {
				t.Fatal("dry run created the settings dir")
			}
			if orig != "" {
				if readFile(t, o.SettingsPath) != orig {
					t.Fatal("dry run wrote the file")
				}
				if entries, _ := os.ReadDir(filepath.Dir(o.SettingsPath)); len(entries) != 1 {
					t.Fatalf("dry run left files: %v", entries)
				}
			}
			if len(f.runs) != 0 {
				t.Fatalf("ran %v", f.runs)
			}
			printed := f.stdout.String()
			o.DryRun, o.Stdout = false, &bytes.Buffer{}
			if err := Install(o); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(printed, readFile(t, o.SettingsPath)) {
				t.Fatalf("dry run printed\n%s", printed)
			}
		})
	}
}

func TestModePreserved(t *testing.T) {
	o, _ := newOpts(t)
	writeSettings(t, o.SettingsPath, "{}", 0o600)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{o.SettingsPath, o.SettingsPath + ".cmgo-bak"} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", p, fi.Mode())
		}
	}
}

func TestSymlinkedSettings(t *testing.T) {
	// Dotfile managers link settings.json; the link must survive.
	o, _ := newOpts(t)
	real := filepath.Join(t.TempDir(), "settings.json")
	writeSettings(t, real, "{}", 0o644)
	if err := os.MkdirAll(filepath.Dir(o.SettingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, o.SettingsPath); err != nil {
		t.Fatal(err)
	}
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(o.SettingsPath); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	if readFile(t, real) != freshHooks(t, bin) || readFile(t, real+".cmgo-bak") != "{}" {
		t.Fatal("target not updated")
	}
}
