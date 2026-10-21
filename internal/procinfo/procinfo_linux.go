package procinfo

import (
	"os"
	"strconv"
)

// System returns the Linux process table lookup (/proc/<pid>/stat).
// The name is comm, cut to 15 bytes by the kernel.
func System() Lookup {
	return func(pid int) (int, string, error) {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return 0, "", err
		}
		return parseStat(string(b))
	}
}
