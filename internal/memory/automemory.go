package memory

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// MemoryHit is one auto-memory match for ctx_search timeline mode.
type MemoryHit struct {
	Title, Content, Source string    // "[auto-memory] <label>", snippet, label
	Timestamp              time.Time // file mtime
}

// autoMemoryMaxBytes: files above this size are skipped (AM:139).
const autoMemoryMaxBytes = 1_000_000

// ProjectSlug is Claude Code's directory name for a project under
// <claude dir>/projects: the absolute dir with every "/" and "." replaced by "-".
func ProjectSlug(projectDir string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(projectDir)
}

// SearchAutoMemory ports searchAutoMemory (AM:57-188) for one query over
// <projectDir>/CLAUDE.md, <claudeDir>/CLAUDE.md and the *.md files of
// Claude Code's memory dir for the project, in that order, with at most one
// hit per file and at most limit hits. claudeDir is config.Config.ClaudeDir
// ($CLAUDE_CONFIG_DIR, else ~/.claude). TS read <configDir>/memory/<hash>/,
// a dir Claude Code does not write (spec §3.3).
func SearchAutoMemory(query string, limit int, projectDir, claudeDir string) []MemoryHit {
	type candidate struct{ path, label string }
	cands := []candidate{{filepath.Join(projectDir, "CLAUDE.md"), "project/CLAUDE.md"}}
	if claudeDir != projectDir { // AM:103, no duplicate label
		cands = append(cands, candidate{filepath.Join(claudeDir, "CLAUDE.md"), "user/CLAUDE.md"})
	}
	memDir := filepath.Join(claudeDir, "projects", ProjectSlug(projectDir), "memory")
	entries, _ := os.ReadDir(memDir) // sorted by name; a missing dir has no entries
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			cands = append(cands, candidate{filepath.Join(memDir, e.Name()), "memory/" + e.Name()})
		}
	}

	terms := autoMemoryTerms(query)
	var hits []MemoryHit
	for _, c := range cands {
		if len(hits) >= limit {
			break
		}
		b, mtime, ok := readMemoryFile(c.path)
		if !ok {
			continue
		}
		if snippet, ok := autoMemorySnippet(string(b), terms); ok {
			hits = append(hits, MemoryHit{Title: "[auto-memory] " + c.label, Content: snippet, Source: c.label, Timestamp: mtime})
		}
	}
	return hits
}

// readMemoryFile reads a regular file of at most autoMemoryMaxBytes. The path
// may be a symlink; what it points at must be a regular file, because opening
// a FIFO blocks and a device such as /dev/zero never ends. The read is capped
// too, in case the file grows after the stat.
func readMemoryFile(path string) (b []byte, mtime time.Time, ok bool) {
	info, err := os.Stat(path) // follows symlinks
	if err != nil || !info.Mode().IsRegular() || info.Size() > autoMemoryMaxBytes {
		return nil, time.Time{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() { // swapped since the stat
		return nil, time.Time{}, false
	}
	b, err = io.ReadAll(io.LimitReader(f, autoMemoryMaxBytes+1))
	if err != nil || len(b) > autoMemoryMaxBytes {
		return nil, time.Time{}, false
	}
	return b, info.ModTime(), true
}

// autoMemoryTerms is query.toLowerCase().split(/\s+/) keeping terms of at
// least 3 UTF-16 units (AM:150).
func autoMemoryTerms(query string) []string {
	var terms []string
	for _, t := range strings.FieldsFunc(strings.ToLower(query), autoMemorySpace) {
		if hookio.JSLen(t) >= 3 {
			terms = append(terms, t)
		}
	}
	return terms
}

// autoMemorySnippet reports whether any term occurs word-bounded
// (case-insensitive) in content and returns the AM:160-170 window: from 200
// units before the first substring hit of any term to 500 after it, snapped
// out to the surrounding blank lines, trimmed. Positions are UTF-16 units,
// as in JS.
func autoMemorySnippet(content string, terms []string) (string, bool) {
	// RE2's \b is ASCII-only, like JS \b without the u flag.
	if !slices.ContainsFunc(terms, func(t string) bool {
		return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(t) + `\b`).MatchString(content)
	}) {
		return "", false
	}
	u := utf16.Encode([]rune(content))
	lower := utf16.Encode([]rune(strings.ToLower(content)))
	idx := -1
	for _, t := range terms {
		if i := indexU16(lower, utf16.Encode([]rune(t)), 0); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	start, end := max(0, idx-200), min(len(u), idx+500)
	blank := []uint16{'\n', '\n'}
	if p := lastIndexU16(u, blank, start); p >= 0 {
		start = p + 2
	}
	if p := indexU16(u, blank, end); p >= 0 {
		end = p
	}
	if start >= end { // JS slice(start, end) is "" then
		return "", true
	}
	return strings.TrimFunc(string(utf16.Decode(u[start:end])), autoMemorySpace), true
}

// indexU16 is JS s.indexOf(sub, from) over UTF-16 units.
func indexU16(s, sub []uint16, from int) int {
	for i := max(from, 0); i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// lastIndexU16 is JS s.lastIndexOf(sub, from): the last match starting at or
// before from.
func lastIndexU16(s, sub []uint16, from int) int {
	for i := min(from, len(s)-len(sub)); i >= 0; i-- {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// autoMemorySpace is the JS `\s` / trim() whitespace set.
func autoMemorySpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
