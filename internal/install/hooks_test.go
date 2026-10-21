package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	bin     = "/opt/cmgo/bin/cmgo"
	hookBin = "/opt/cmgo/bin/cmgo-hook" // the default next to bin
)

func mustParse(t *testing.T, s string) *object {
	t.Helper()
	v, err := parseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*object)
}

func TestHookEvents(t *testing.T) {
	post := "Bash|Read|Write|Edit|NotebookEdit|Glob|Grep|TodoWrite|TaskCreate|TaskUpdate|EnterPlanMode|ExitPlanMode|Skill|Agent|AskUserQuestion|EnterWorktree"
	want := []struct{ Event, Arg, Matcher string }{
		{"PreToolUse", "pretooluse", "Bash|WebFetch|Read|Grep|Agent"},
		{"PostToolUse", "posttooluse", post},
		{"PostToolUseFailure", "posttoolusefailure", post},
		{"UserPromptSubmit", "userpromptsubmit", ""},
		{"SessionStart", "sessionstart", ""},
		{"Stop", "stop", ""},
		{"PreCompact", "precompact", ""},
	}
	if len(HookEvents) != len(want) {
		t.Fatalf("len %d", len(HookEvents))
	}
	for i := range want {
		if HookEvents[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, HookEvents[i], want[i])
		}
	}
}

func TestHookCommand(t *testing.T) {
	if got := hookCommand(hookBin, "stop"); got != `/opt/cmgo/bin/cmgo-hook stop` {
		t.Fatalf("got %s", got)
	}
}

func TestIsOwnCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`"/opt/cmgo/bin/cmgo-hook" pretooluse`, true},
		{`"/old place/cmgo-hook" stop`, true},
		{`/usr/local/bin/cmgo-hook sessionstart`, true},
		{`cmgo-hook posttoolusefailure`, true},
		{`"/x/cmgo-hook" hook stop`, false},
		{`"/x/cmgo-hook" stop extra`, false},
		{`"/x/cmgo-hook" unknown`, false},
		{`"/x/cmgo" stop`, false},
		{`"/opt/cmgo/bin/cmgo" hook pretooluse`, true}, // the old form, from before cmgo-hook
		{`"/old place/cmgo" hook stop`, true},
		{`/usr/local/bin/cmgo hook sessionstart`, true},
		{`cmgo hook posttoolusefailure`, true},
		{`"/x/cmgo"   hook   stop  `, true},
		{`"/x/cmgo" hook stop extra`, false},
		{`"/x/cmgo" hook unknown`, false},
		{`"/x/cmgo" serve`, false},
		{`"/x/cmgo-dev" hook stop`, false},
		{`/x/cmgo/run.sh hook stop`, false},
		{`"/x/cmgo hook stop`, false},
		{`node "/p/hooks/pretooluse.mjs"`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isOwnCommand(c.cmd); got != c.want {
			t.Errorf("%q: %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestAddHooksEmpty(t *testing.T) {
	root := newObject()
	if err := addHooks(root, hookBin); err != nil {
		t.Fatal(err)
	}
	got := string(encodeJSON(root))
	for _, he := range HookEvents {
		want := `"` + he.Event + `": [
      {
        "matcher": "` + he.Matcher + `",
        "hooks": [
          {
            "type": "command",
            "command": "/opt/cmgo/bin/cmgo-hook ` + he.Arg + `"
          }
        ]
      }
    ]`
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s entry in\n%s", he.Event, got)
		}
	}
	hooks, _ := root.get("hooks")
	if strings.Join(hooks.(*object).keys, ",") != "PreToolUse,PostToolUse,PostToolUseFailure,UserPromptSubmit,SessionStart,Stop,PreCompact" {
		t.Fatalf("order %v", hooks.(*object).keys)
	}
}

func TestAddHooksBadShape(t *testing.T) {
	for _, s := range []string{`{"hooks":[]}`, `{"hooks":{"Stop":{}}}`} {
		if err := addHooks(mustParse(t, s), hookBin); err == nil {
			t.Errorf("%s: no error", s)
		}
	}
}

func TestStripHooks(t *testing.T) {
	root := mustParse(t, `{"hooks":{
		"PreToolUse":[
			{"matcher":"Bash","hooks":[{"type":"command","command":"user.sh"},{"type":"command","command":"\"/old/cmgo\" hook pretooluse"}]},
			{"matcher":"Read","hooks":[{"type":"command","command":"/old/cmgo hook pretooluse"},{"type":"command","command":"\"/new/cmgo-hook\" pretooluse"}]}
		],
		"Notification":[{"matcher":"","hooks":[{"type":"command","command":"cmgo hook stop"}]}],
		"Stop":[],
		"Weird":[1,{"hooks":"x"},{"hooks":[2]}]
	},"other":true}`)
	if n := stripHooks(root); n != 4 {
		t.Fatalf("removed %d, want 4", n)
	}
	want := mustParse(t, `{"hooks":{
		"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"user.sh"}]}],
		"Stop":[],
		"Weird":[1,{"hooks":"x"},{"hooks":[2]}]
	},"other":true}`)
	if got, w := string(encodeJSON(root)), string(encodeJSON(want)); got != w {
		t.Fatalf("got\n%s\nwant\n%s", got, w)
	}
}

func TestStripHooksDropsEmptiedHooksKey(t *testing.T) {
	root := mustParse(t, `{"a":1,"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"cmgo hook stop"}]}]}}`)
	stripHooks(root)
	if got := string(encodeJSON(root)); got != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("got %s", got)
	}
	// Nothing of ours: an empty hooks object stays.
	root = mustParse(t, `{"hooks":{}}`)
	if n := stripHooks(root); n != 0 {
		t.Fatalf("removed %d", n)
	}
	if _, ok := root.get("hooks"); !ok {
		t.Fatal("hooks removed")
	}
}

// Claude Code runs the command through sh -c, so $, backtick and backslash in
// the install path must not expand.
func TestHookCommandIsShellSafe(t *testing.T) {
	for _, dir := range []string{"my$HOME dir", "it's here", "back`tick`", `back\slash`, `dq"uote`, "plain"} {
		t.Run(dir, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), dir, "cmgo-hook")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s|%s\\n' \"$0\" \"$1\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := hookCommand(p, "stop")
			out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
			if err != nil || string(out) != p+"|stop\n" {
				t.Fatalf("sh -c %s\n got %q (%v)\nwant %q", cmd, out, err, p+"|stop\n")
			}
			if !isOwnCommand(cmd) {
				t.Fatalf("isOwnCommand rejects its own command %s", cmd)
			}
		})
	}
}

func TestIsOwnCommandQuotingForms(t *testing.T) {
	for cmd, want := range map[string]bool{
		`'/opt/cmgo/bin/cmgo-hook' pretooluse`:  true,
		`'/old place/cmgo-hook' stop`:           true,
		`'/it'\''s/cmgo-hook' stop`:             true,
		`'/my$HOME dir/cmgo-hook' sessionstart`: true,
		`"/old/cmgo-hook" stop`:                 true, // old installs
		`/usr/local/bin/cmgo-hook stop`:         true,
		`'/x/cmgo-hook' unknown`:                false,
		`'/x/cmgo-hook' stop extra`:             false,
		`'/x/cmgo-dev' hook stop`:               false,
		`'/x/cmgo-hook stop`:                    false,
		`'/opt/cmgo/bin/cmgo' hook pretooluse`:  true,
	} {
		if got := isOwnCommand(cmd); got != want {
			t.Errorf("%q: %v, want %v", cmd, got, want)
		}
	}
}
