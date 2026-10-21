package procinfo

import (
	"os"
	"strconv"
	"sync"
	"time"
)

// userHZ is the unit of /proc/<pid>/stat times, fixed at 100 by the kernel ABI.
const userHZ = 100

// System returns the Linux process table lookup: /proc/<pid>/stat for the
// parent and the start time (with btime from /proc/stat), /proc/<pid>/exe and
// /proc/<pid>/cmdline for the paths. Both are best effort: another user's
// process refuses the paths, and the start time needs btime and field 22.
func System() Lookup {
	btime := sync.OnceValues(func() (int64, error) {
		b, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, err
		}
		return parseBtime(string(b))
	})
	return func(pid int) (Info, error) {
		dir := "/proc/" + strconv.Itoa(pid) + "/"
		b, err := os.ReadFile(dir + "stat")
		if err != nil {
			return Info{}, err
		}
		ppid, ticks, err := parseStat(string(b))
		if err != nil {
			return Info{}, err
		}
		info := Info{PPID: ppid}
		if bt, err := btime(); err == nil && ticks > 0 {
			info.Start = time.Unix(bt, 0).Add(time.Duration(ticks) * time.Second / userHZ)
		}
		info.Exe, _ = os.Readlink(dir + "exe")
		if b, err := os.ReadFile(dir + "cmdline"); err == nil {
			info.Arg0, info.Arg1 = parseCmdline(b)
		}
		return info, nil
	}
}
