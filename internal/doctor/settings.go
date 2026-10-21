package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/install"
)

// checkVersion compares cmgo's version with what the hook binary prints.
func checkVersion(ctx context.Context, e Env, hookBin string) string {
	timeout := e.HookTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	run := e.HookVersion
	if run == nil {
		run = runVersion
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hv, err := run(ctx, hookBin)
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("timed out after %v", timeout)
	}
	switch {
	case err != nil:
		return fmt.Sprintf("[WARN] Version: cmgo %s (hook binary version unknown: %v) — fix: cmgo upgrade", e.Version, err)
	case hv != e.Version:
		return fmt.Sprintf("[WARN] Version: cmgo %s (hook binary %s differs) — fix: cmgo upgrade", e.Version, hv)
	}
	return fmt.Sprintf("[OK] Version: cmgo %s (hook binary %s)", e.Version, hv)
}

// runVersion runs `<path> version` and returns the last word it prints:
// cmgo-hook prints "cmgo-hook <v>", an old cmgo hook command's cmgo "cmgo <v>".
func runVersion(ctx context.Context, path string) (string, error) {
	c := exec.CommandContext(ctx, path, "version")
	c.WaitDelay = 100 * time.Millisecond // a killed script's child must not hold stdout open
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		// A cmgo-hook from before the version argument exits 0 silently.
		return "", errors.New("no output")
	}
	return f[len(f)-1], nil
}

// checkHooks reports which HookEvents have a cmgo command in settings.json.
func checkHooks(path string, st install.Status, err error) string {
	if err != nil {
		return fmt.Sprintf("[FAIL] Hooks: could not read %s — %v", path, err)
	}
	var missing []string
	for _, he := range install.HookEvents {
		if !slices.Contains(st.Events, he.Event) {
			missing = append(missing, he.Event)
		}
	}
	if len(missing) > 0 {
		return "[WARN] Hooks: missing " + strings.Join(missing, ", ") + " — fix: cmgo install"
	}
	n := len(install.HookEvents)
	return fmt.Sprintf("[OK] Hooks: %d/%d events registered in %s", n, n, path)
}

// hookBinaryPath is the binary Claude Code runs for cmgo's hooks, else
// cmgo-hook next to cmgo, where cmgo install looks for it.
func hookBinaryPath(st install.Status, binary string) string {
	if st.HookBinary != "" {
		return st.HookBinary
	}
	return filepath.Join(filepath.Dir(binary), "cmgo-hook")
}

func checkHookBinary(path string) string {
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return "[FAIL] Hook binary: not found at " + path + " — fix: cmgo install"
	}
	return "[OK] Hook binary: " + path
}

// checkMCP checks that the cmgo MCP server runs this binary.
func checkMCP(claudeJSON, binary string) string {
	cmd, args, ok := install.MCPServer(claudeJSON)
	switch {
	case !ok:
		return "[WARN] MCP server: not registered — fix: cmgo install"
	case cmd == binary && slices.Equal(args, []string{"serve"}):
		return "[OK] MCP server: registered (" + cmd + " serve)"
	}
	return "[WARN] MCP server: registered at " + cmd + " — fix: cmgo install"
}

// checkSkills reports cmgo's skills: how many are current, which are
// missing or outdated (cmgo install fixes both), and which are a user's or
// edited, which install leaves alone.
func checkSkills(r install.SkillReport, err error) string {
	if err != nil {
		return "[WARN] Skills: could not read " + r.Dir + " — " + err.Error()
	}
	total := len(r.Current) + len(r.Missing) + len(r.Outdated) + len(r.Foreign)
	line := fmt.Sprintf("Skills: %d/%d in %s", len(r.Current), total, r.Dir)
	if len(r.Current) == total {
		return "[OK] " + line
	}
	if len(r.Missing) > 0 {
		line += "; missing " + strings.Join(r.Missing, ", ")
	}
	if len(r.Outdated) > 0 {
		line += "; outdated " + strings.Join(r.Outdated, ", ")
	}
	if len(r.Foreign) > 0 {
		line += "; not cmgo's or edited (left as is): " + strings.Join(r.Foreign, ", ")
	}
	if len(r.Missing)+len(r.Outdated) > 0 {
		line += " — fix: cmgo install"
	}
	return "[WARN] " + line
}

func checkStatusLine(st install.Status) string {
	switch {
	case st.OwnStatusLine:
		return "[OK] Statusline: cmgo"
	case st.StatusLine == "":
		return "[OK] Statusline: not set"
	}
	return "[WARN] Statusline: another command is set (" + st.StatusLine + ")"
}

// checkTSPlugin warns once per enabled TS context-mode plugin.
func checkTSPlugin(st install.Status) string {
	if len(st.TSPlugins) == 0 {
		return "[OK] TS plugin: not enabled"
	}
	var lines []string
	for _, k := range st.TSPlugins {
		lines = append(lines, "[WARN] TS plugin: "+k+" is enabled — both inject routing text; fix: cmgo install --disable-ts-plugin")
	}
	return strings.Join(lines, "\n")
}
