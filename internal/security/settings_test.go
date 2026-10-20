package security

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile creates path (and its parents) with content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTestPolicy returns a policy over fresh project and home dirs whose
// warnings go to the returned buffer.
func newTestPolicy(t *testing.T) (p *Policy, project, home string, warn *bytes.Buffer) {
	t.Helper()
	project, home = t.TempDir(), t.TempDir()
	warn = &bytes.Buffer{}
	p = NewPolicy(project, home)
	p.warn = warn
	return p, project, home, warn
}

func localSettings(project string) string {
	return filepath.Join(project, ".claude", "settings.local.json")
}
func sharedSettings(project string) string {
	return filepath.Join(project, ".claude", "settings.json")
}
func homeSettings(home string) string { return filepath.Join(home, ".claude", "settings.json") }

func TestGlobToRegex(t *testing.T) {
	// Cases from tests/security.test.ts:49-114.
	cases := []struct {
		glob, cmd  string
		fold, want bool
	}{
		{"ls *", "ls -la", false, true},
		{"ls *", "lsof -i", false, false},
		{"ls*", "lsof -i", false, true},
		{"ls*", "ls -la", false, true},
		{"git *", `git commit -m "msg"`, false, true},
		{"* commit *", `git commit -m "msg"`, false, true},
		{"tree:*", "tree", false, true},
		{"tree:*", "tree -a", false, true},
		{"tree:*", "treemap", false, false},
		{"sudo *", "sudo apt install", false, true},
		{"sudo *", "sudoedit", false, false},
		{"rm -rf /*", "rm -rf /etc", false, true},
		{"chmod -R 777 *", "chmod -R 777 /tmp", false, true},
		{"dir *", "DIR /W", true, true},
		{"dir *", "DIR /W", false, false},
		{"echo (x)", "echo (x)", false, true},
		{"a.b", "axb", false, false},
	}
	for _, c := range cases {
		if got := globToRegex(c.glob, c.fold).MatchString(c.cmd); got != c.want {
			t.Errorf("globToRegex(%q, %v).Match(%q) = %v, want %v", c.glob, c.fold, c.cmd, got, c.want)
		}
	}
}

func TestFileGlobToRegex(t *testing.T) {
	// Cases from tests/security.test.ts:522-546.
	cases := []struct {
		glob, path string
		want       bool
	}{
		{".env", ".env", true},
		{".env", "src/.env", false},
		{"**/.env", "deep/nested/.env", true},
		{"**/.env", ".env", true},
		{"**/*credentials*", "secrets/credentials.json", true},
		{"**/*credentials*", "readme.md", false},
		{"/home/u/.ssh/**", "/home/u/.ssh/id_rsa", true},
		{"src/*.go", "src/a/b.go", false},
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "file/.txt", false},
	}
	for _, c := range cases {
		if got := fileGlobToRegex(c.glob, false).MatchString(c.path); got != c.want {
			t.Errorf("fileGlobToRegex(%q).Match(%q) = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
}

func TestRulesOrderAndFiltering(t *testing.T) {
	p, project, home, warn := newTestPolicy(t)
	writeFile(t, localSettings(project), `{"permissions":{"deny":["Bash(local *)", 42, "Grep(**)", "bare"],"allow":["Bash(echo:*)"]}}`)
	writeFile(t, sharedSettings(project), `{"permissions":{"deny":["Read(.env)"],"allow":["Read(/var/log/**)"]}}`)
	writeFile(t, homeSettings(home), `{"permissions":{"deny":["Bash(sudo *)","Read(**/*.pem)"]}}`)

	rs := p.rules()
	if len(rs) != 3 {
		t.Fatalf("got %d rule sets, want 3", len(rs))
	}
	if len(rs[0].bashDeny) != 1 || rs[0].bashDeny[0].pattern != "Bash(local *)" || len(rs[0].readAllow) != 0 {
		t.Errorf("local rules = %+v", rs[0])
	}
	if len(rs[1].readDeny) != 1 || rs[1].readDeny[0].glob != ".env" || rs[1].readAllow[0].glob != "/var/log/**" {
		t.Errorf("shared rules = %+v", rs[1])
	}
	if len(rs[2].bashDeny) != 1 || rs[2].bashDeny[0].pattern != "Bash(sudo *)" || rs[2].readDeny[0].glob != "**/*.pem" {
		t.Errorf("home rules = %+v", rs[2])
	}
	if warn.Len() != 0 {
		t.Errorf("unexpected warning: %q", warn.String())
	}
}

func TestRulesMissingFilesAreSilent(t *testing.T) {
	p, _, _, warn := newTestPolicy(t)
	if rs := p.rules(); len(rs) != 0 {
		t.Fatalf("got %d rule sets, want 0", len(rs))
	}
	if warn.Len() != 0 {
		t.Errorf("unexpected warning: %q", warn.String())
	}
}

func TestRulesMalformedJSONSkipped(t *testing.T) {
	p, project, home, warn := newTestPolicy(t)
	writeFile(t, localSettings(project), `{"permissions":{"deny":["Bash(sudo *)"`)
	writeFile(t, homeSettings(home), `{"permissions":{"deny":["Bash(rm *)"]}}`)

	for range 2 {
		rs := p.rules()
		if len(rs) != 1 || rs[0].bashDeny[0].pattern != "Bash(rm *)" {
			t.Fatalf("rules = %+v, want only the home file", rs)
		}
	}
	if n := strings.Count(warn.String(), "\n"); n != 1 || !strings.Contains(warn.String(), localSettings(project)) {
		t.Errorf("want one warning naming the bad file, got %q", warn.String())
	}
}

func TestRulesCacheInvalidation(t *testing.T) {
	p, _, home, _ := newTestPolicy(t)
	path := homeSettings(home)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeFile(t, path, `{"permissions":{"deny":["Bash(aaaa *)"]}}`)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if got := p.rules()[0].bashDeny[0].pattern; got != "Bash(aaaa *)" {
		t.Fatalf("first read = %q", got)
	}

	// Same size and mtime: the cached rules are served without re-reading.
	writeFile(t, path, `{"permissions":{"deny":["Bash(bbbb *)"]}}`)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if got := p.rules()[0].bashDeny[0].pattern; got != "Bash(aaaa *)" {
		t.Fatalf("cache hit = %q, want stale Bash(aaaa *)", got)
	}

	// New mtime: re-parsed.
	newer := old.Add(time.Minute)
	if err := os.Chtimes(path, newer, newer); err != nil {
		t.Fatal(err)
	}
	if got := p.rules()[0].bashDeny[0].pattern; got != "Bash(bbbb *)" {
		t.Fatalf("after mtime change = %q, want Bash(bbbb *)", got)
	}

	// Deleted: dropped.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if rs := p.rules(); len(rs) != 0 {
		t.Fatalf("after delete got %d rule sets", len(rs))
	}
}
