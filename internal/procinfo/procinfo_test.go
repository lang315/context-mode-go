package procinfo

import (
	"errors"
	"testing"
)

type proc struct {
	ppid int
	name string
}

// table turns a fake process table into a Lookup that counts its calls.
func table(procs map[int]proc, calls *int) Lookup {
	return func(pid int) (int, string, error) {
		*calls++
		p, ok := procs[pid]
		if !ok {
			return 0, "", errors.New("no such process")
		}
		return p.ppid, p.name, nil
	}
}

func TestClaudePID(t *testing.T) {
	cases := []struct {
		name  string
		procs map[int]proc
		pid   int
		want  int
	}{
		{"parent is claude", map[int]proc{100: {50, "cmgo"}, 50: {1, "claude"}, 1: {0, "launchd"}}, 100, 50},
		{"grandparent, mixed case", map[int]proc{100: {60, "cmgo-hook"}, 60: {50, "sh"}, 50: {1, "Claude Helper"}, 1: {0, "init"}}, 100, 50},
		{"own name is not checked", map[int]proc{100: {1, "claude"}, 1: {0, "launchd"}}, 100, 0},
		{"no claude ancestor stops at pid 0", map[int]proc{100: {60, "cmgo"}, 60: {1, "zsh"}, 1: {0, "launchd"}}, 100, 0},
		{"pid 1 may be claude (container init)", map[int]proc{100: {1, "cmgo"}, 1: {0, "claude"}}, 100, 1},
		{"own lookup fails", map[int]proc{}, 100, 0},
		{"ancestor lookup fails", map[int]proc{100: {60, "cmgo"}, 60: {40, "sh"}}, 100, 0},
		{"two-process cycle", map[int]proc{100: {60, "cmgo"}, 60: {70, "a"}, 70: {60, "b"}}, 100, 0},
		{"self-parent cycle", map[int]proc{100: {60, "cmgo"}, 60: {60, "a"}}, 100, 0},
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

// chain builds 100 -> 101 -> ... with the ancestor at hop n named claude.
func chain(n int) map[int]proc {
	procs := map[int]proc{}
	for i := 0; i < 20; i++ {
		name := "sh"
		if i == n {
			name = "claude"
		}
		procs[100+i] = proc{101 + i, name}
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
