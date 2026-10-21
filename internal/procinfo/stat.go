package procinfo

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
)

// parseStat reads the parent pid from a Linux /proc/<pid>/stat line:
// "pid (comm) state ppid ...". comm may contain spaces and parentheses, so it
// ends at the last ')'. It lives in an untagged file so darwin runs its tests.
func parseStat(s string) (ppid int, err error) {
	end := strings.LastIndexByte(s, ')')
	if strings.IndexByte(s, '(') < 0 || end < 0 {
		return 0, errors.New("procinfo: malformed stat")
	}
	f := strings.Fields(s[end+1:]) // state, ppid, ...
	if len(f) < 2 {
		return 0, errors.New("procinfo: short stat")
	}
	return strconv.Atoi(f[1])
}

// parseStatStart reads field 22 of a Linux /proc/<pid>/stat line: when the
// process started, in clock ticks after boot.
func parseStatStart(s string) (int64, error) {
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return 0, errors.New("procinfo: malformed stat")
	}
	f := strings.Fields(s[end+1:]) // field 3 (state) onwards
	if len(f) < 20 {
		return 0, errors.New("procinfo: short stat")
	}
	return strconv.ParseInt(f[19], 10, 64)
}

// parseBtime reads the boot time (Unix seconds) from Linux /proc/stat.
func parseBtime(s string) (int64, error) {
	for _, line := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
	}
	return 0, errors.New("procinfo: no btime in /proc/stat")
}

// parseCmdline returns argv[0] and argv[1] from a Linux /proc/<pid>/cmdline,
// whose arguments are NUL-terminated.
func parseCmdline(b []byte) (arg0, arg1 string) {
	f := strings.SplitN(string(b), "\x00", 3)
	arg0 = f[0]
	if len(f) > 1 {
		arg1 = f[1]
	}
	return arg0, arg1
}

// parseProcArgs reads a darwin kern.procargs2 buffer: an int32 argc, the exec
// path, NUL padding, then argc NUL-terminated arguments and the environment.
// It returns the exec path, argv[0] and argv[1], "" for what is missing.
func parseProcArgs(b []byte) (exe, arg0, arg1 string) {
	if len(b) < 4 {
		return "", "", ""
	}
	argc := int(int32(binary.NativeEndian.Uint32(b)))
	rest := b[4:]
	cut := func() string {
		i := 0
		for i < len(rest) && rest[i] != 0 {
			i++
		}
		s := string(rest[:i])
		rest = rest[min(i+1, len(rest)):]
		return s
	}
	exe = cut()
	for len(rest) > 0 && rest[0] == 0 { // padding up to the first argument
		rest = rest[1:]
	}
	if argc >= 1 && len(rest) > 0 {
		arg0 = cut()
	}
	if argc >= 2 && len(rest) > 0 {
		arg1 = cut()
	}
	return exe, arg0, arg1
}
