package executor

import (
	"strings"
	"syscall"
)

// Shutdown sends SIGTERM to every backgrounded process group and SIGKILL to every
// run still in flight, then forgets them. Safe to call more than once.
// Port of executor.ts:268-276 (cleanupBackgrounded).
func (e *Executor) Shutdown() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for pgid := range e.fg {
		syscall.Kill(-pgid, syscall.SIGKILL)
	}
	for pgid := range e.bg {
		syscall.Kill(-pgid, syscall.SIGTERM) // already gone: ESRCH, ignored
	}
	clear(e.bg)
}

// IsSoftFail reports a non-zero exit that is not an error: shell, exit code 1, and
// non-blank stdout (grep with no match in one file of many). Port of exit-classify.ts:15-33.
func IsSoftFail(lang Language, exitCode int, stdout string) bool {
	return lang == Shell && exitCode == 1 && strings.TrimSpace(stdout) != ""
}
