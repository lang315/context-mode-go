package routing

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// sentinelPrefix names the files cmgo serve writes in $CMGO_DIR/run.
const sentinelPrefix = "mcp-ready-"

// MCPReady ports isMCPReady (hooks/core/mcp-ready.mjs:73-109) over runDir
// (spec §2.5): true at the first sentinel whose pid is live (kill(pid, 0)
// succeeds or fails with EPERM). The file of a dead pid is removed. TS kept
// a dead pid's file for 90 s because its server refreshed it every 30 s;
// cmgo serve writes it once, so there is no grace period. A file whose text
// parseInt cannot read is skipped and left alone, as in TS (it may be
// mid-write).
func MCPReady(runDir string) bool {
	ents, err := os.ReadDir(runDir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), sentinelPrefix) {
			continue
		}
		p := filepath.Join(runDir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		pid, ok := jsParseInt(string(b))
		if !ok {
			continue
		}
		if pidLive(pid) {
			return true
		}
		_ = os.Remove(p) // best effort, as TS
	}
	return false
}

// pidLive reports kill(pid, 0) success or EPERM. A pid <= 0 counts as dead:
// TS would call kill(0, 0) or kill(-1, 0), which signal a process group and
// report a server that does not exist.
func pidLive(pid int64) bool {
	if pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	err := syscall.Kill(int(pid), 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// WriteSentinel creates runDir (0700) and runDir/mcp-ready-<pid> holding the
// pid. remove deletes the file; cmgo serve defers it.
func WriteSentinel(runDir string, pid int) (remove func(), err error) {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	p := filepath.Join(runDir, sentinelPrefix+strconv.Itoa(pid))
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(p) }, nil
}
