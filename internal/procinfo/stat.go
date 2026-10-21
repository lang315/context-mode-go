package procinfo

import (
	"errors"
	"strconv"
	"strings"
)

// parseStat reads the parent pid and name from a Linux /proc/<pid>/stat line:
// "pid (comm) state ppid ...". comm may contain spaces and parentheses, so it
// ends at the last ')'. It lives in an untagged file so darwin runs its tests.
func parseStat(s string) (ppid int, name string, err error) {
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return 0, "", errors.New("procinfo: malformed stat")
	}
	f := strings.Fields(s[end+1:]) // state, ppid, ...
	if len(f) < 2 {
		return 0, "", errors.New("procinfo: short stat")
	}
	if ppid, err = strconv.Atoi(f[1]); err != nil {
		return 0, "", err
	}
	return ppid, s[open+1 : end], nil
}
