package doctor

import (
	"context"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/install"
)

// Report runs every check in spec §1 order and returns the text, a header
// then one line per check, and whether any line is a [FAIL].
func Report(ctx context.Context, e Env) (text string, failed bool) {
	st, serr := install.Inspect(e.SettingsPath)
	hookBin := hookBinaryPath(st, e.Binary)
	execTimeout := e.ExecTimeout
	if execTimeout <= 0 {
		execTimeout = 5 * time.Second
	}
	lines := []string{
		"cmgo doctor",
		"",
		checkVersion(ctx, e, hookBin),
		checkStorage(e.DataDir),
		checkDatabase(e.DBPath),
		checkExecutor(ctx, e.Exec, execTimeout),
		checkRuntimes(e.Exec),
		checkHooks(e.SettingsPath, st, serr),
		checkHookBinary(hookBin),
		checkMCP(e.ClaudeJSONPath, e.Binary),
		checkSkills(install.Skills(e.SettingsPath)),
	}
	// Without a readable settings file there is no statusline or plugin to report.
	if serr == nil {
		lines = append(lines, checkStatusLine(st), checkTSPlugin(st))
	}
	text = strings.Join(lines, "\n")
	return text, strings.Contains(text, "\n[FAIL] ")
}
