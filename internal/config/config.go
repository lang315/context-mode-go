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
	FetchStrict bool // CMGO_FETCH_STRICT=1 blocks private ranges
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
		FetchStrict: getenv("CMGO_FETCH_STRICT") == "1",
	}, nil
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
