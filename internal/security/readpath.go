package security

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// CheckReadPath ports the ctx_execute_file path checks (server.ts:2118-2123):
// first the project boundary with its Read() allow exception
// (checkProjectBoundary, server.ts:1184-1209), then the Read() deny rules
// (checkFilePathDenyPolicy, server.ts:1215-1241). The error text is the TS
// message. projectDir is expected to be absolute (config.Load: config.go:46);
// an empty projectDir disables the boundary, as TS does for an unknown root.
func (p *Policy) CheckReadPath(path string) error {
	rules := p.rules()
	cands := pathCandidates(path, p.projectDir)
	if !isPathInsideProject(path, p.projectDir, caseInsensitive) {
		if _, ok := matchFileRules(cands, rules, func(r *fileRules) []fileRule { return r.readAllow }); !ok {
			return fmt.Errorf(`File access blocked: "%s" resolves outside the project root `+
				`(%s). context-mode confines ctx_execute_file to the workspace so it `+
				`cannot be used to bypass the host's sandbox/permission controls (issue #852). `+
				`To intentionally process a file outside the project, add a host allow rule, `+
				`e.g. "permissions": { "allow": ["Read(%s)"] } in your settings.`, path, p.projectDir, path)
		}
	}
	if glob, ok := matchFileRules(cands, rules, func(r *fileRules) []fileRule { return r.readDeny }); ok {
		return errors.New("File access blocked by security policy: path matches Read deny pattern " + glob)
	}
	return nil
}

// resolveIn mirrors node's path.resolve(root, p) for an absolute root.
func resolveIn(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}

// pathCandidates ports the candidate set of evaluateFilePath
// (security.ts:628-638): the raw input, the path resolved against the project
// root, and its realpath when it exists. Backslash normalisation is dropped
// with Windows support.
func pathCandidates(path, root string) []string {
	cands := []string{path}
	if root == "" {
		return cands
	}
	lexical := resolveIn(root, path)
	cands = append(cands, lexical)
	if real, err := filepath.EvalSymlinks(lexical); err == nil {
		cands = append(cands, real)
	}
	return cands
}

// matchFileRules ports the matching loop of evaluateFilePath
// (security.ts:640-655): settings files in order, then rules, then candidates.
// It returns the first matching glob.
func matchFileRules(cands []string, rules []*fileRules, pick func(*fileRules) []fileRule) (string, bool) {
	for _, r := range rules {
		for _, fr := range pick(r) {
			for _, c := range cands {
				if fr.re.MatchString(c) {
					return fr.glob, true
				}
			}
		}
	}
	return "", false
}

// isPathInsideProject ports security.ts:686-730: the resolved path must be
// inside root, and when both exist their realpaths must be too (symlink escape).
func isPathInsideProject(path, root string, fold bool) bool {
	if root == "" {
		return true
	}
	root = filepath.Clean(root)
	lexical := resolveIn(root, path)
	if !within(root, lexical, fold) {
		return false
	}
	realRoot, err1 := filepath.EvalSymlinks(root)
	real, err2 := filepath.EvalSymlinks(lexical)
	if err1 == nil && err2 == nil && !within(realRoot, real, fold) {
		return false
	}
	return true
}

// within ports the inner helper of security.ts:696-713.
func within(root, cand string, fold bool) bool {
	if fold {
		root, cand = strings.ToLower(root), strings.ToLower(cand)
	}
	if root == cand {
		return true
	}
	rel, err := filepath.Rel(root, cand)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
