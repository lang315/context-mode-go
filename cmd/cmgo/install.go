package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lang315/context-mode-go/internal/install"
)

// installCmd runs `cmgo install` or `cmgo uninstall` (spec §5). A usage
// error exits 2, any other error 1.
func installCmd(name string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cmgo "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var binary string
	var disableTS bool
	if name == "install" {
		fs.StringVar(&binary, "binary", "", "path to the cmgo binary (default: this executable)")
		fs.BoolVar(&disableTS, "disable-ts-plugin", false, "turn off an enabled context-mode TS plugin")
	}
	dryRun := fs.Bool("dry-run", false, "print the new settings file instead of writing it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cmgo %s: unexpected argument %q\n", name, fs.Arg(0))
		return 2
	}
	if err := runInstall(name, binary, disableTS, *dryRun, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "cmgo: %v\n", err)
		return 1
	}
	return 0
}

func runInstall(name, binary string, disableTS, dryRun bool, stdout, stderr io.Writer) error {
	home := os.Getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = h
	}
	o := install.Options{
		SettingsPath:    settingsPath(home),
		ClaudeJSONPath:  claudeJSONPath(home),
		DisableTSPlugin: disableTS,
		DryRun:          dryRun,
		Stdout:          stdout,
		Stderr:          stderr,
		LookPath:        exec.LookPath,
		Run: func(name string, args ...string) error {
			c := exec.Command(name, args...)
			c.Stdout, c.Stderr = stdout, stderr
			return c.Run()
		},
	}
	if name == "uninstall" {
		return install.Uninstall(o)
	}
	var err error
	if binary == "" {
		// The hooks must keep working through a symlink that later moves.
		if binary, err = os.Executable(); err == nil {
			binary, err = filepath.EvalSymlinks(binary)
		}
	} else {
		binary, err = filepath.Abs(binary)
	}
	if err != nil {
		return err
	}
	o.Binary = binary
	// Claude Code would run a missing hook binary on every event and fail.
	o.HookBinary = filepath.Join(filepath.Dir(binary), "cmgo-hook")
	if _, err := os.Stat(o.HookBinary); err != nil && !dryRun {
		return fmt.Errorf("hook binary missing: %w (build cmd/cmgo-hook next to cmgo)", err)
	}
	return install.Install(o)
}

// claudeConfigDir is $CLAUDE_CONFIG_DIR ("~" expanded), else ~/.claude.
func claudeConfigDir(home string) string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	switch {
	case dir == "":
		dir = filepath.Join(home, ".claude")
	case dir == "~" || strings.HasPrefix(dir, "~/"):
		dir = filepath.Join(home, dir[1:])
	}
	return dir
}

// settingsPath is settings.json in the Claude config dir.
func settingsPath(home string) string {
	return filepath.Join(claudeConfigDir(home), "settings.json")
}

// claudeJSONPath is where Claude Code keeps the user-scope MCP servers:
// .claude.json inside $CLAUDE_CONFIG_DIR when that is set, else ~/.claude.json.
func claudeJSONPath(home string) string {
	if os.Getenv("CLAUDE_CONFIG_DIR") == "" {
		return filepath.Join(home, ".claude.json")
	}
	return filepath.Join(claudeConfigDir(home), ".claude.json")
}
