package security

import (
	"runtime"
	"slices"
	"testing"
)

func TestSplitChainedCommands(t *testing.T) {
	// Cases from tests/security.test.ts:137-202.
	cases := []struct {
		in   string
		want []string
	}{
		{"echo hello && sudo rm -rf /", []string{"echo hello", "sudo rm -rf /"}},
		{"test -f /tmp/x || sudo reboot", []string{"test -f /tmp/x", "sudo reboot"}},
		{"cd /tmp; sudo rm -rf /", []string{"cd /tmp", "sudo rm -rf /"}},
		{"cat /etc/passwd | sudo tee /tmp/out", []string{"cat /etc/passwd", "sudo tee /tmp/out"}},
		{"echo a && echo b; sudo rm -rf /", []string{"echo a", "echo b", "sudo rm -rf /"}},
		{`echo "hello && world"`, []string{`echo "hello && world"`}},
		{"echo 'test; value'", []string{"echo 'test; value'"}},
		{"git status", []string{"git status"}},
		{"git status\nsudo rm -rf /", []string{"git status", "sudo rm -rf /"}},
		{"git status\r\nsudo rm -rf /", []string{"git status", "sudo rm -rf /"}},
		{"echo hello & git status", []string{"echo hello", "git status"}},
		{`echo hello \& git status`, []string{`echo hello \& git status`}},
		{"git status $(sudo rm -rf / | cat) && echo done", []string{"git status $(sudo rm -rf / | cat)", "echo done"}},
		{"echo $(printf '(x)' | cat) && git status", []string{"echo $(printf '(x)' | cat)", "git status"}},
		{"echo `a | b` ; c", []string{"echo `a | b`", "c"}},
		{" ; && ", nil},
	}
	for _, c := range cases {
		if got := splitChainedCommands(c.in); !slices.Equal(got, c.want) {
			t.Errorf("splitChainedCommands(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractSubshellCommands(t *testing.T) {
	// Cases from tests/security.test.ts:204-243.
	cases := []struct {
		in   string
		want []string
	}{
		{"echo $(sudo rm -rf /)", []string{"sudo rm -rf /"}},
		{"git checkout $(echo $(uname))", []string{"uname", "echo $(uname)"}},
		{"echo `sudo rm -rf /`", []string{"sudo rm -rf /"}},
		{"echo '$(sudo rm -rf /)'", nil},
		{`echo "$(sudo rm -rf /)"`, []string{"sudo rm -rf /"}},
		{`echo \\$(sudo rm -rf /)`, []string{"sudo rm -rf /"}},
		{`echo \$(sudo rm -rf /)`, nil},
		{"echo $((1 + 2))", nil},
		{"echo $(( $(sudo rm -rf /) + 1 ))", []string{"sudo rm -rf /"}},
	}
	for _, c := range cases {
		if got := extractSubshellCommands(c.in); !slices.Equal(got, c.want) {
			t.Errorf("extractSubshellCommands(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCheckCommand(t *testing.T) {
	p, project, home, _ := newTestPolicy(t)
	writeFile(t, homeSettings(home), `{"permissions":{"deny":["Bash(sudo *)","Bash(rm -rf /*)"],"allow":["Bash(echo:*)","Bash(git:*)"]}}`)
	writeFile(t, sharedSettings(project), `{"permissions":{"deny":["Bash(tree:*)"]}}`)

	cases := []struct {
		cmd     string
		pattern string // "" = allowed
	}{
		{"sudo apt install", "Bash(sudo *)"},
		{"sudoedit /etc/hosts", ""},
		{"echo ok && sudo rm -rf /", "Bash(sudo *)"},
		{"cd /tmp; sudo apt install vim", "Bash(sudo *)"},
		{"cat file | rm -rf /etc", "Bash(rm -rf /*)"},
		{"false || sudo reboot", "Bash(sudo *)"},
		{"sleep 1 & sudo reboot", "Bash(sudo *)"},
		{"echo hi\nsudo reboot", "Bash(sudo *)"},
		{"echo hello && git status", ""},
		{`echo "x && sudo reboot"`, ""},
		{"echo 'x; sudo reboot'", ""},
		{"git status $(sudo rm -rf /)", "Bash(sudo *)"},
		{"git status $(sudo rm -rf / | cat)", "Bash(sudo *)"},
		{`git status \\$(sudo rm -rf /)`, "Bash(sudo *)"},
		{"echo $(echo $(echo `sudo reboot`))", "Bash(sudo *)"},
		{"echo `echo $(sudo reboot)`", "Bash(sudo *)"},
		{"echo '$(sudo reboot)'", ""},
		{"tree", "Bash(tree:*)"},
		{"tree -a", "Bash(tree:*)"},
		{"treemap", ""},
	}
	for _, c := range cases {
		blocked, pattern := p.CheckCommand(c.cmd)
		if blocked != (c.pattern != "") || pattern != c.pattern {
			t.Errorf("CheckCommand(%q) = %v, %q; want pattern %q", c.cmd, blocked, pattern, c.pattern)
		}
	}
}

func TestCheckCommandPrecedenceFirstFileWins(t *testing.T) {
	p, project, home, _ := newTestPolicy(t)
	writeFile(t, localSettings(project), `{"permissions":{"deny":["Bash(rm *)"]}}`)
	writeFile(t, homeSettings(home), `{"permissions":{"deny":["Bash(rm -rf *)"]}}`)
	if _, pattern := p.CheckCommand("rm -rf x"); pattern != "Bash(rm *)" {
		t.Errorf("pattern = %q, want the project-local rule", pattern)
	}
}

func TestCheckCommandDarwinCaseFolding(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("case folding applies on darwin only")
	}
	p, _, home, _ := newTestPolicy(t)
	writeFile(t, homeSettings(home), `{"permissions":{"deny":["Bash(sudo *)"]}}`)
	if blocked, _ := p.CheckCommand("SUDO rm -rf /"); !blocked {
		t.Error("SUDO not blocked on darwin")
	}
}

func TestBlockedMessage(t *testing.T) {
	want := "Command blocked by security policy: matches deny pattern Bash(sudo *)"
	if got := BlockedMessage("Bash(sudo *)"); got != want {
		t.Errorf("got %q", got)
	}
}
