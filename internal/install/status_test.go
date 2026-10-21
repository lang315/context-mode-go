package install

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestInspectAfterInstall(t *testing.T) {
	o, _ := newOpts(t)
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	st, err := Inspect(o.SettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, he := range HookEvents {
		all = append(all, he.Event)
	}
	want := Status{Events: all, HookBinary: "/opt/cmgo/bin/cmgo-hook", StatusLine: "/opt/cmgo/bin/cmgo-hook statusline", OwnStatusLine: true}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("Inspect = %+v\nwant %+v", st, want)
	}
}

func TestInspectPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	// PreToolUse uses the pre-cmgo-hook form; Stop holds a cmgo command for
	// another event, which does not count; SessionStart's is a user command.
	writeSettings(t, path, `{
  "hooks": {
    "PreToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "'/old dir/cmgo' hook pretooluse"}]}],
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "/x/cmgo-hook pretooluse"}]}],
    "SessionStart": [{"matcher": "", "hooks": [{"type": "command", "command": "echo hi"}, {"type": "command", "command": "/x/cmgo-hook sessionstart"}]}]
  },
  "statusLine": {"type": "command", "command": "ccline"},
  "enabledPlugins": {"context-mode@context-mode": true, "context-mode@other": false, "x@y": true}
}`, 0o644)
	st, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Status{Events: []string{"PreToolUse", "SessionStart"}, HookBinary: "/old dir/cmgo", StatusLine: "ccline", TSPlugins: []string{"context-mode@context-mode"}}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("Inspect = %+v\nwant %+v", st, want)
	}
}

func TestInspectMissingAndBad(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "none.json")
	if st, err := Inspect(missing); err != nil || !reflect.DeepEqual(st, Status{}) {
		t.Fatalf("missing file: %+v, %v", st, err)
	}
	bad := filepath.Join(dir, "bad.json")
	writeSettings(t, bad, `{"hooks":`, 0o644)
	if _, err := Inspect(bad); err == nil {
		t.Fatal("unparseable file: no error")
	}
}

func TestMCPServer(t *testing.T) {
	o, _ := newOpts(t)
	if _, _, ok := MCPServer(o.ClaudeJSONPath); ok {
		t.Fatal("no file: registered")
	}
	writeClaudeJSON(t, o, `{"mcpServers":{"cmgo":{"type":"stdio","command":"/a/cmgo","args":["serve"]}}}`)
	cmd, args, ok := MCPServer(o.ClaudeJSONPath)
	if !ok || cmd != "/a/cmgo" || !reflect.DeepEqual(args, []string{"serve"}) {
		t.Fatalf("got %q %v %v", cmd, args, ok)
	}
}
