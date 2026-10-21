package procinfo

import "golang.org/x/sys/unix"

// System returns the darwin process table lookup (sysctl kern.proc.pid).
// The name is p_comm, cut to 16 bytes by the kernel.
func System() Lookup {
	return func(pid int) (int, string, error) {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			return 0, "", err
		}
		return int(kp.Eproc.Ppid), unix.ByteSliceToString(kp.Proc.P_comm[:]), nil
	}
}
