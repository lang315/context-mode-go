package capture

import (
	"reflect"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
	"github.com/lang315/context-mode-go/internal/store"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`echo a"b c"d`, []string{"echo", "ab cd"}},
		{"a\t 'b\\'c' \\ d", []string{"a", "b'c", " d"}},
		{"x\ny z", []string{"x\ny", "z"}}, // only space and tab separate
		{`unterminated "q r`, []string{"unterminated", "q r"}},
		{`\é`, []string{"é"}}, // a backslash keeps the whole next character
		{"  ", nil},
	}
	for _, c := range cases {
		if got := tokenize(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("tokenize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGitEvents(t *testing.T) {
	cwd := func(d string) store.Event { return ev("cwd", "cwd", 2, d) }
	git := func(op string) store.Event { return ev("git", "git", 2, op) }
	commit := func(m string) store.Event { return ev("git_commit", "git", 2, m) }
	cases := []struct {
		cmd, home string
		want      []store.Event
	}{
		// spec §3.4 fix: TS matched the subcommand against the op label and lost these.
		{"git -C /x checkout main", "/h", []store.Event{cwd("/x"), git("branch")}},
		{"git --no-pager checkout -b f", "/h", []store.Event{git("branch")}},
		{"git checkout main", "/h", []store.Event{git("branch")}},
		{"git -C /x merge dev", "/h", []store.Event{cwd("/x"), git("merge")}},
		{"git -C ~/repos/r status", "/h", []store.Event{cwd("/h/repos/r"), git("status")}},
		{"git -C ~ status", "/h", []store.Event{cwd("/h"), git("status")}},
		{"git -C ~/r status", "", []store.Event{cwd("~/r"), git("status")}}, // no home: TS expands to "~"
		{"git --directory=~other/foo status", "/h", []store.Event{cwd("~other/foo"), git("status")}},
		{`git -C "" status`, "/h", []store.Event{git("status")}},
		{"FOO=1 sudo git --no-pager log -n 3", "/h", []store.Event{git("log")}},
		{"/usr/bin/git stash pop", "/h", []store.Event{git("stash")}},
		{"cd /r && git status", "/h", []store.Event{git("status")}},
		{"git frob && git status", "/h", []store.Event{git("status")}},
		{"git status\ngit log", "/h", []store.Event{git("log")}}, // table order, not position
		{"cd /r && git merge", "/h", nil},
		{"git frob", "/h", nil},
		{"ls", "/h", nil},
		{`git commit -m "feat: x"`, "/h", []store.Event{commit("feat: x")}},
		{`git commit -am "stage+commit"`, "/h", []store.Event{commit("stage+commit")}},
		{"git commit --message=longform", "/h", []store.Event{commit("longform")}},
		{"git commit --message= -m later", "/h", []store.Event{commit("later")}}, // an empty --message= is skipped
		{"git commit --message 'two words'", "/h", []store.Event{commit("two words")}},
		{"git commit --amend --no-edit", "/h", []store.Event{git("commit")}},
		{`git commit -m ""`, "/h", []store.Event{git("commit")}},
		{`git commit -m "say \"hi\"" && git push`, "/h", []store.Event{commit(`say "hi"`)}},
		{"git -C /abs/repo commit -m x", "/h", []store.Event{cwd("/abs/repo"), commit("x")}},
	}
	for _, c := range cases {
		in := call{tool: "Bash", input: &hookio.Object{}, home: c.home}
		in.input.SetString("command", c.cmd)
		checkEvents(t, c.cmd, gitEvents(in), c.want)
	}
	if evs := gitEvents(call{tool: "Read", input: &hookio.Object{}}); evs != nil {
		t.Errorf("Read: %v", evs)
	}
}
