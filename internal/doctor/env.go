// Package doctor runs the cmgo health checks behind `cmgo doctor` and
// ctx_doctor and renders them as the TS [OK]/[WARN]/[FAIL] plain-text lines
// (S:4139-4267, spec §1).
package doctor

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
)

// Executor is what the Executor and Runtimes checks use; *executor.Executor
// has both methods.
type Executor interface {
	Run(ctx context.Context, r executor.Request) (executor.Result, error)
	Available() []executor.Language
}

// Env holds every path and effect the checks touch, so tests point them at
// temp dirs, fake scripts and fakes.
type Env struct {
	Version        string // cmgo's own version
	Binary         string // this cmgo, symlinks resolved, as cmgo install registers it
	DataDir        string
	DBPath         string
	SettingsPath   string
	ClaudeJSONPath string
	Exec           Executor
	// HookVersion returns the version `<path> version` prints; nil runs it.
	HookVersion func(ctx context.Context, path string) (string, error)
	HookTimeout time.Duration // 0 = 2 s (spec §1)
	ExecTimeout time.Duration // 0 = 5 s, the TS server test timeout
}

// NewEnv is the Env of this process: paths from cfg, Binary from
// os.Executable.
func NewEnv(cfg config.Config, version string, ex Executor) Env {
	bin, err := os.Executable()
	if err == nil {
		bin, _ = filepath.EvalSymlinks(bin)
	}
	// shortcut: cmgo install keys on whether CLAUDE_CONFIG_DIR is set, this on
	// whether it is the default dir; they differ only for an explicit
	// CLAUDE_CONFIG_DIR=~/.claude. Upgrade if that case ever matters.
	claudeJSON := filepath.Join(cfg.ClaudeDir, ".claude.json")
	if cfg.ClaudeDir == filepath.Join(cfg.Home, ".claude") {
		claudeJSON = filepath.Join(cfg.Home, ".claude.json")
	}
	return Env{
		Version:        version,
		Binary:         bin,
		DataDir:        cfg.DataDir,
		DBPath:         cfg.DBPath,
		SettingsPath:   filepath.Join(cfg.ClaudeDir, "settings.json"),
		ClaudeJSONPath: claudeJSON,
		Exec:           ex,
	}
}
