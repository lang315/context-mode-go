package procinfo

import (
	"time"

	"golang.org/x/sys/unix"
)

// System returns the darwin process table lookup: kern.proc.pid for the
// parent and the start time, kern.procargs2 for the executable path and the
// first two arguments. The paths are best effort: another user's process
// refuses procargs2.
func System() Lookup {
	return func(pid int) (Info, error) {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			return Info{}, err
		}
		st := kp.Proc.P_starttime
		info := Info{PPID: int(kp.Eproc.Ppid), Start: time.Unix(st.Sec, int64(st.Usec)*1000)}
		if b, err := unix.SysctlRaw("kern.procargs2", pid); err == nil {
			info.Exe, info.Arg0, info.Arg1 = parseProcArgs(b)
		}
		return info, nil
	}
}
