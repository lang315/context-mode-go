// Package config resolves cmgo's paths and environment switches.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config is the process-wide configuration, resolved once at startup.
type Config struct {
	DataDir     string // $CMGO_DIR, default ~/.claude/cmgo
	DBPath      string // DataDir/cmgo.db
	ProjectDir  string // absolute project dir, as given (used as cwd)
	ProjectKey  string // NormalizeProject(ProjectDir), stored in sources.project
	Home        string
	ClaudeDir   string // $CLAUDE_CONFIG_DIR ("~" expanded), default ~/.claude: where settings.json lives
	ClaudeJSON  string // where Claude Code keeps user-scope MCP servers, see ClaudeJSONPath
	FetchStrict bool   // CMGO_FETCH_STRICT=1 blocks private ranges
}

// Load reads the environment. getenv and getwd are injectable for tests.
func Load(getenv func(string) string, getwd func() (string, error)) (Config, error) {
	home := getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Config{}, err
		}
		home = h
	}
	claudeDir := strings.TrimSpace(getenv("CLAUDE_CONFIG_DIR"))
	switch {
	case claudeDir == "":
		claudeDir = filepath.Join(home, ".claude")
	case claudeDir == "~" || strings.HasPrefix(claudeDir, "~/"):
		claudeDir = filepath.Join(home, claudeDir[1:])
	}
	dataDir := getenv("CMGO_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(home, ".claude", "cmgo")
	}
	project := getenv("CMGO_PROJECT_DIR")
	if project == "" {
		project = getenv("CLAUDE_PROJECT_DIR")
	}
	if project == "" {
		wd, err := getwd()
		if err != nil {
			return Config{}, err
		}
		project = wd
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return Config{}, err
	}
	return Config{
		DataDir:     dataDir,
		DBPath:      filepath.Join(dataDir, "cmgo.db"),
		ProjectDir:  abs,
		ProjectKey:  NormalizeProject(abs),
		Home:        home,
		ClaudeDir:   claudeDir,
		ClaudeJSON:  ClaudeJSONPath(getenv, home, claudeDir),
		FetchStrict: getenv("CMGO_FETCH_STRICT") == "1",
	}, nil
}

// ClaudeJSONPath is where Claude Code keeps the user-scope MCP servers:
// .claude.json inside $CLAUDE_CONFIG_DIR when that is set (even to the default
// dir), else ~/.claude.json. cmgo install writes it and cmgo doctor reads it.
func ClaudeJSONPath(getenv func(string) string, home, claudeDir string) string {
	if strings.TrimSpace(getenv("CLAUDE_CONFIG_DIR")) == "" {
		return filepath.Join(home, ".claude.json")
	}
	return filepath.Join(claudeDir, ".claude.json")
}

// NormalizeProject cleans an absolute path; darwin paths are lowercased
// because the default APFS volume is case-insensitive (TS parity).
func NormalizeProject(abs string) string {
	p := filepath.Clean(abs)
	if runtime.GOOS == "darwin" {
		p = strings.ToLower(p)
	}
	return p
}
