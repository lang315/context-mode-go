// Package procinfo finds the Claude Code process that owns this one, so the
// MCP server and the hooks can agree on a session without being told its id.
package procinfo

import "strings"

// Lookup returns a process's parent pid and executable name.
type Lookup func(pid int) (ppid int, name string, err error)

// maxHops bounds the walk; it also ends parent cycles.
const maxHops = 8

// ClaudePID walks from pid's parent up to 8 hops and returns the first
// ancestor whose name contains "claude" (case-insensitive), else 0.
// pid's own name is not checked.
func ClaudePID(pid int, look Lookup) int {
	next, _, err := look(pid)
	for hop := 0; hop < maxHops && err == nil && next > 0; hop++ {
		cur := next
		var name string
		if next, name, err = look(cur); err == nil && strings.Contains(strings.ToLower(name), "claude") {
			return cur
		}
	}
	return 0
}
