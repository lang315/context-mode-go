package procinfo

import (
	"errors"
	"os"
	"testing"
)

type proc struct {
	ppid            int
	exe, arg0, arg1 string
}

// table turns a fake process table into a Lookup that counts its calls.
func table(procs map[int]proc, calls *int) Lookup {
	return func(pid int) (Info, error) {
		*calls++
		p, ok := procs[pid]
		if !ok {
			return Info{}, errors.New("no such process")
		}
		return Info{PPID: p.ppid, Exe: p.exe, Arg0: p.arg0, Arg1: p.arg1}, nil
	}
}

// Real process tables seen on darwin and Linux. The native installer runs
// ~/.local/bin/claude, a symlink to .../claude/versions/<version>, so the
// kernel's process name is the version and only the paths say "claude".
var (
	native  = proc{50, "/Users/me/.local/share/claude/versions/2.1.304", "claude", "--resume"}
	nodeCLI = proc{50, "/usr/local/bin/node", "node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}
	viaPath = proc{50, "/Users/me/.local/share/claude/versions/2.1.304", "/Users/me/.local/bin/claude", ""}
	zsh     = proc{1, "/bin/zsh", "-/bin/zsh", ""}
)

func TestClaudePID(t *testing.T) {
	cases := []struct {
		name  string
		procs map[int]proc
		pid   int
		want  int
	}{
		{"native installer: version-named exe under claude/versions", map[int]proc{100: {60, "/x/cmgo", "cmgo", ""}, 60: {50, "/bin/zsh", "zsh", "-c"}, 50: {1, native.exe, native.arg0, native.arg1}, 1: {0, "/sbin/launchd", "launchd", ""}}, 100, 50},
		{"native installer: only argv0 has claude", map[int]proc{100: {50, "/x/cmgo", "cmgo", ""}, 50: {1, "/opt/2.1.304", "/Users/me/.local/bin/claude", ""}, 1: {0, "/sbin/launchd", "launchd", ""}}, 100, 50},
		{"native installer: argv0 and exe", map[int]proc{100: {50, "/x/cmgo", "cmgo", ""}, 50: {1, viaPath.exe, viaPath.arg0, ""}, 1: {0, "/sbin/launchd", "launchd", ""}}, 100, 50},
		{"node install: argv1 is claude-code/cli.js", map[int]proc{100: {50, "/x/cmgo", "cmgo", ""}, 50: {1, nodeCLI.exe, nodeCLI.arg0, nodeCLI.arg1}, 1: {0, "/sbin/launchd", "launchd", ""}}, 100, 50},
		{"mixed case", map[int]proc{100: {60, "/x/cmgo-hook", "cmgo-hook", ""}, 60: {50, "/bin/sh", "sh", ""}, 50: {1, "/Applications/Claude.app/Contents/MacOS/Claude Helper", "Claude Helper", ""}, 1: {0, "/sbin/init", "init", ""}}, 100, 50},
		{"own paths are not checked", map[int]proc{100: {1, "/x/claude/cmgo", "/x/claude/cmgo", "claude"}, 1: {0, "/sbin/launchd", "launchd", ""}}, 100, 0},
		{"no claude ancestor stops at pid 0", map[int]proc{100: {60, "/x/cmgo", "cmgo", ""}, 60: zsh, 1: {0, "/sbin/launchd", "launchd", ""}}, 100, 0},
		{"unrelated arguments do not match", map[int]proc{100: {60, "/x/cmgo", "cmgo", ""}, 60: {1, "/usr/bin/vim", "vim", "notes.md"}, 1: {0, "/sbin/launchd", "launchd", ""}}, 100, 0},
		{"process names alone do not match", map[int]proc{100: {60, "/x/cmgo", "cmgo", ""}, 60: {1, "", "", ""}, 1: {0, "", "", ""}}, 100, 0},
		{"pid 1 may be claude (container init)", map[int]proc{100: {1, "/x/cmgo", "cmgo", ""}, 1: {0, "/usr/bin/claude", "claude", ""}}, 100, 1},
		{"own lookup fails", map[int]proc{}, 100, 0},
		{"ancestor lookup fails", map[int]proc{100: {60, "/x/cmgo", "cmgo", ""}, 60: {40, "/bin/sh", "sh", ""}}, 100, 0},
		{"two-process cycle", map[int]proc{100: {60, "/x/cmgo", "cmgo", ""}, 60: {70, "/a", "a", ""}, 70: {60, "/b", "b", ""}}, 100, 0},
		{"self-parent cycle", map[int]proc{100: {60, "/x/cmgo", "cmgo", ""}, 60: {60, "/a", "a", ""}}, 100, 0},
	}
	for _, c := range cases {
		calls := 0
		if got := ClaudePID(c.pid, table(c.procs, &calls)); got != c.want {
			t.Errorf("%s: ClaudePID = %d, want %d", c.name, got, c.want)
		}
		if calls > 9 {
			t.Errorf("%s: %d lookups, want at most 9", c.name, calls)
		}
	}
}

// chain builds 100 -> 101 -> ... with the ancestor at hop n running claude.
func chain(n int) map[int]proc {
	procs := map[int]proc{}
	for i := 0; i < 20; i++ {
		exe := "/bin/sh"
		if i == n {
			exe = "/Users/me/.local/share/claude/versions/2.1.304"
		}
		procs[100+i] = proc{101 + i, exe, "sh", ""}
	}
	return procs
}

func TestClaudePIDDepthCap(t *testing.T) {
	calls := 0
	if got := ClaudePID(100, table(chain(8), &calls)); got != 108 {
		t.Errorf("hop 8: ClaudePID = %d, want 108", got)
	}
	calls = 0
	if got := ClaudePID(100, table(chain(9), &calls)); got != 0 {
		t.Errorf("hop 9: ClaudePID = %d, want 0", got)
	}
	if calls != 9 {
		t.Errorf("hop 9: %d lookups, want 9 (own pid + 8 ancestors)", calls)
	}
}

// Claude Code sets CLAUDECODE=1 for the processes it starts. Under it the
// real process table must lead to Claude Code: with the native installer the
// kernel name is the version number, so a name match finds nothing.
func TestClaudePIDRealAncestor(t *testing.T) {
	if os.Getenv("CLAUDECODE") == "" {
		t.Skip("CLAUDECODE is not set: not running under Claude Code")
	}
	if got := ClaudePID(os.Getpid(), System()); got <= 0 {
		t.Fatalf("ClaudePID(self, System()) = %d under Claude Code (CLAUDECODE=%q), want the claude pid", got, os.Getenv("CLAUDECODE"))
	}
}
