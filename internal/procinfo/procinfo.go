// Package procinfo finds the Claude Code process that owns this one, so the
// MCP server and the hooks can agree on a session without being told its id.
package procinfo

import (
	"strings"
	"time"
)

// Info is what the walk needs to know about one process.
type Info struct {
	PPID       int
	Exe        string    // executable path; "" when unreadable
	Arg0, Arg1 string    // argv[0] and argv[1]; "" when absent or unreadable
	Start      time.Time // when the process started; zero when unknown
}

// Lookup returns a process's parent pid, the paths it was started with and
// its start time. It fails only when the process cannot be found; unreadable
// paths are "" and an unknown start time is zero.
type Lookup func(pid int) (Info, error)

// maxHops bounds the walk; it also ends parent cycles.
const maxHops = 8

// ClaudePID walks from pid's parent up to 8 hops and returns the first
// ancestor that looks like Claude Code, else 0. pid itself is not checked.
//
// The kernel's process name is no help: the native installer runs
// ~/.local/bin/claude, a symlink to .../claude/versions/<version>, and darwin
// then names the process "<version>". The executable path, argv[0] and argv[1]
// do say "claude" (argv[1] covers a node install running
// .../@anthropic-ai/claude-code/cli.js).
func ClaudePID(pid int, look Lookup) int {
	cc, _ := Claude(pid, look)
	return cc
}

// Claude is ClaudePID that also returns when the Claude Code process started
// (zero when unknown or none was found).
func Claude(pid int, look Lookup) (int, time.Time) {
	self, err := look(pid)
	next := self.PPID
	for hop := 0; hop < maxHops && err == nil && next > 0; hop++ {
		cur := next
		var info Info
		if info, err = look(cur); err == nil {
			if isClaude(info) {
				return cur, info.Start
			}
			next = info.PPID
		}
	}
	return 0, time.Time{}
}

// isClaude reports whether any of the paths has an element containing
// "claude", case-insensitive.
func isClaude(i Info) bool {
	for _, p := range [...]string{i.Exe, i.Arg0, i.Arg1} {
		for _, el := range strings.Split(p, "/") {
			if strings.Contains(strings.ToLower(el), "claude") {
				return true
			}
		}
	}
	return false
}
