package procinfo

import (
	"os"
	"strconv"
)

// System returns the Linux process table lookup: /proc/<pid>/stat for the
// parent, /proc/<pid>/exe and /proc/<pid>/cmdline for the paths. The paths
// are best effort: another user's process refuses them.
func System() Lookup {
	return func(pid int) (Info, error) {
		dir := "/proc/" + strconv.Itoa(pid) + "/"
		b, err := os.ReadFile(dir + "stat")
		if err != nil {
			return Info{}, err
		}
		ppid, err := parseStat(string(b))
		if err != nil {
			return Info{}, err
		}
		info := Info{PPID: ppid}
		info.Exe, _ = os.Readlink(dir + "exe")
		if b, err := os.ReadFile(dir + "cmdline"); err == nil {
			info.Arg0, info.Arg1 = parseCmdline(b)
		}
		return info, nil
	}
}
