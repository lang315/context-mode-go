// Package security ports context-mode's server-side deny firewall
// (src/security.ts and its wiring in src/server.ts).
package security

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// caseInsensitive mirrors TS `process.platform === "darwin"` (win32 dropped).
// It applies to Bash deny, the project boundary, Read allow and Read deny.
var caseInsensitive = runtime.GOOS == "darwin"

// bashRule is one `Bash(glob)` entry; pattern is the full entry text.
type bashRule struct {
	pattern string
	re      *regexp.Regexp
}

// fileRule is one `Read(glob)` entry; glob is the text inside the parens.
type fileRule struct {
	glob string
	re   *regexp.Regexp
}

// fileRules holds the rules parsed from one settings file.
type fileRules struct {
	bashDeny  []bashRule
	bashAsk   []bashRule // read by EvaluateCommand only
	bashAllow []bashRule // read by EvaluateCommand only
	readDeny  []fileRule
	readAllow []fileRule
}

type cacheEntry struct {
	mtime time.Time
	size  int64
	rules *fileRules // nil when the file could not be read or parsed
}

// Policy evaluates commands and paths against Claude Code settings files.
type Policy struct {
	projectDir string
	claudeDir  string
	warn       io.Writer

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewPolicy returns a Policy reading settings under projectDir and
// home/.claude. An empty projectDir or home skips the files rooted there.
func NewPolicy(projectDir, home string) *Policy {
	claudeDir := ""
	if home != "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	return NewPolicyDir(projectDir, claudeDir)
}

// NewPolicyDir is NewPolicy with the Claude config dir given directly, for
// when $CLAUDE_CONFIG_DIR moves the global settings.json away from ~/.claude.
func NewPolicyDir(projectDir, claudeDir string) *Policy {
	return &Policy{projectDir: projectDir, claudeDir: claudeDir, warn: os.Stderr, cache: map[string]cacheEntry{}}
}

// settingsPaths ports readBashPolicies / readToolPermissionPatterns file order
// (security.ts:363-394, 426-490). Only the claude global is read, from the
// Claude config dir (cmgo follows CLAUDE_CONFIG_DIR, as install does): adapter
// globals are dropped (spec §4.5).
func (p *Policy) settingsPaths() []string {
	var paths []string
	if p.projectDir != "" {
		paths = append(paths,
			filepath.Join(p.projectDir, ".claude", "settings.local.json"),
			filepath.Join(p.projectDir, ".claude", "settings.json"))
	}
	if p.claudeDir != "" {
		paths = append(paths, filepath.Join(p.claudeDir, "settings.json"))
	}
	return paths
}

// rules returns the parsed rules of every readable settings file in
// precedence order. Each file costs one stat; it is re-parsed only when its
// mtime or size changes.
func (p *Policy) rules() []*fileRules {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*fileRules
	for _, path := range p.settingsPaths() {
		fi, err := os.Stat(path)
		if err != nil {
			delete(p.cache, path)
			continue // missing file: silently skipped, as in TS
		}
		e, ok := p.cache[path]
		if !ok || !e.mtime.Equal(fi.ModTime()) || e.size != fi.Size() {
			e = cacheEntry{mtime: fi.ModTime(), size: fi.Size()}
			r, err := parseSettingsFile(path)
			if err != nil {
				// Fail-open like TS; warn once per file version, not per call.
				fmt.Fprintf(p.warn, "context-mode: ignoring settings file %s: %v\n", path, err)
			} else {
				e.rules = r
			}
			p.cache[path] = e
		}
		if e.rules != nil {
			out = append(out, e.rules)
		}
	}
	return out
}

// toolPattern ports parseToolPattern (security.ts:37-44).
var toolPattern = regexp.MustCompile(`^(\w+)\((.+)\)$`)

// parseSettingsFile ports readSingleSettings + readToolPermissionPatterns
// extractGlobs (security.ts:321-351, 434-461): permissions.deny Bash()/Read(),
// permissions.allow Bash()/Read() and permissions.ask Bash() entries are kept.
func parseSettingsFile(path string) (*fileRules, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	root, _ := doc.(map[string]any)
	perms, _ := root["permissions"].(map[string]any)
	r := &fileRules{}
	for _, entry := range stringsOf(perms["deny"]) {
		m := toolPattern.FindStringSubmatch(entry)
		if m == nil {
			continue
		}
		switch m[1] {
		case "Bash":
			r.bashDeny = append(r.bashDeny, bashRule{pattern: entry, re: globToRegex(m[2], caseInsensitive)})
		case "Read":
			// Intentional difference from TS (server.ts:1222 folds only on win32): fold on darwin so .ENV cannot bypass Read(.env) on APFS.
			r.readDeny = append(r.readDeny, fileRule{glob: m[2], re: fileGlobToRegex(m[2], caseInsensitive)})
		}
	}
	for _, entry := range stringsOf(perms["allow"]) {
		m := toolPattern.FindStringSubmatch(entry)
		if m == nil {
			continue
		}
		switch m[1] {
		case "Bash":
			r.bashAllow = append(r.bashAllow, bashRule{pattern: entry, re: globToRegex(m[2], caseInsensitive)})
		case "Read":
			r.readAllow = append(r.readAllow, fileRule{glob: m[2], re: fileGlobToRegex(m[2], caseInsensitive)})
		}
	}
	for _, entry := range stringsOf(perms["ask"]) {
		if m := toolPattern.FindStringSubmatch(entry); m != nil && m[1] == "Bash" {
			r.bashAsk = append(r.bashAsk, bashRule{pattern: entry, re: globToRegex(m[2], caseInsensitive)})
		}
	}
	return r, nil
}

// stringsOf returns the string elements of a JSON array, skipping the rest.
func stringsOf(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// globToRegex ports security.ts:69-90. "cmd:args" becomes ^cmd(\s args)?$;
// otherwise * becomes .* and everything else is literal.
func globToRegex(glob string, fold bool) *regexp.Regexp {
	var expr string
	if cmd, args, ok := strings.Cut(glob, ":"); ok {
		expr = `^` + regexp.QuoteMeta(cmd) + `(\s` + convertGlobPart(args) + `)?$`
	} else {
		expr = `^` + convertGlobPart(glob) + `$`
	}
	if fold {
		expr = `(?i)` + expr
	}
	return regexp.MustCompile(expr)
}

// convertGlobPart ports security.ts:56-60.
func convertGlobPart(glob string) string {
	return strings.ReplaceAll(regexp.QuoteMeta(glob), `\*`, `.*`)
}

// fileGlobToRegex ports security.ts:101-135: ** spans directories, * and ?
// stay within one path segment.
func fileGlobToRegex(glob string, fold bool) *regexp.Regexp {
	var b strings.Builder
	for i := 0; i < len(glob); {
		switch {
		case glob[i] == '*' && i+1 < len(glob) && glob[i+1] == '*':
			if i+2 < len(glob) && glob[i+2] == '/' {
				b.WriteString(`(.*/)?`)
				i += 3
			} else {
				b.WriteString(`.*`)
				i += 2
			}
		case glob[i] == '*':
			b.WriteString(`[^/]*`)
			i++
		case glob[i] == '?':
			b.WriteString(`[^/]`)
			i++
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
			i++
		}
	}
	expr := `^` + b.String() + `$`
	if fold {
		expr = `(?i)` + expr
	}
	return regexp.MustCompile(expr)
}
