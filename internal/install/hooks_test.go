package install

import (
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
	if got := hookCommand(hookBin, "stop"); got != `"/opt/cmgo/bin/cmgo-hook" stop` {
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
            "command": "\"/opt/cmgo/bin/cmgo-hook\" ` + he.Arg + `"
          }
        ]
      }
    ]`
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s entry in\n%s", he.Event, got)
		}
	}
	hooks, _ := root.get("hooks")
	if strings.Join(hooks.(*object).keys, ",") != "PreToolUse,PostToolUse,PostToolUseFailure,UserPromptSubmit,SessionStart,Stop" {
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
