package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectSlug(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/lang/x.y": "-Users-lang-x-y",
		"/Users/lang/GolandProjects/github.com/lang315/context-mode": "-Users-lang-GolandProjects-github-com-lang315-context-mode",
		"/tmp/My Project": "-tmp-My Project",
	} {
		if got := ProjectSlug(in); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// memoryFixture returns a project dir and home with the three candidate kinds.
func memoryFixture(t *testing.T) (project, home, memDir string) {
	root := t.TempDir()
	project = filepath.Join(root, "work", "my.app")
	home = filepath.Join(root, "home")
	memDir = filepath.Join(home, ".claude", "projects", ProjectSlug(project), "memory")
	writeFile(t, filepath.Join(project, "CLAUDE.md"), "Project rules: run the widget tests.")
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "User rules: prefer widget names.")
	writeFile(t, filepath.Join(memDir, "b.md"), "Widget note B.")
	writeFile(t, filepath.Join(memDir, "a.md"), "Widget note A.")
	writeFile(t, filepath.Join(memDir, "c.txt"), "widget ignored: not markdown")
	// Another project's memory dir is never read.
	writeFile(t, filepath.Join(home, ".claude", "projects", "-other", "memory", "x.md"), "widget elsewhere")
	return project, home, memDir
}

func hitLabels(hits []MemoryHit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Source)
	}
	return strings.Join(out, ",")
}

func TestSearchAutoMemoryCandidates(t *testing.T) {
	project, home, memDir := memoryFixture(t)
	hits := SearchAutoMemory("WIDGET", 10, project, home)
	if got := hitLabels(hits); got != "project/CLAUDE.md,user/CLAUDE.md,memory/a.md,memory/b.md" {
		t.Fatalf("labels = %s", got)
	}
	h := hits[2]
	if h.Title != "[auto-memory] memory/a.md" || h.Content != "Widget note A." {
		t.Fatalf("hit = %+v", h)
	}
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(memDir, "a.md"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if hits := SearchAutoMemory("widget", 10, project, home); !hits[2].Timestamp.Equal(mtime) {
		t.Fatalf("timestamp = %v", hits[2].Timestamp)
	}
	if got := hitLabels(SearchAutoMemory("widget", 2, project, home)); got != "project/CLAUDE.md,user/CLAUDE.md" {
		t.Fatalf("limit 2 = %s", got)
	}
	if hits := SearchAutoMemory("widget", 0, project, home); len(hits) != 0 {
		t.Fatalf("limit 0 = %v", hits)
	}
	// No project or user CLAUDE.md, no memory dir: nothing to search.
	if hits := SearchAutoMemory("widget", 5, filepath.Join(home, "none"), filepath.Join(home, "nohome")); len(hits) != 0 {
		t.Fatalf("empty = %v", hits)
	}
}

func TestSearchAutoMemoryMatching(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "p")
	home := filepath.Join(root, "h")
	md := filepath.Join(project, "CLAUDE.md")
	// Expected values from node running the AM:149-170 code on the same inputs.
	cases := []struct {
		name, content, query string
		want                 string // "" = no hit
	}{
		{"word boundary", "the cached value", "cache", ""},
		{"word boundary hit", "Cache, then serve", "cache", "Cache, then serve"},
		{"short terms ignored", "go is fun", "go", ""},
		{"any term matches", "only zeta here", "alpha zeta", "only zeta here"},
		{"regex metachars are literal", "use a+b now", "a+b", "use a+b now"},
		{"underscore is a word char", "my_cache_key", "cache", ""},
	}
	for _, c := range cases {
		writeFile(t, md, c.content)
		hits := SearchAutoMemory(c.query, 5, project, home)
		got := ""
		if len(hits) > 0 {
			got = hits[0].Content
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// Files over 1,000,000 bytes are skipped.
	writeFile(t, md, "widget "+strings.Repeat("x", 1_000_000))
	if hits := SearchAutoMemory("widget", 5, project, home); len(hits) != 0 {
		t.Fatal("large file was searched")
	}
}

func TestAutoMemorySnippet(t *testing.T) {
	// Expected values from node running the AM:149-170 code on the same inputs.
	pad := strings.Repeat("x", 250)
	ys := strings.Repeat("y", 600)
	emoji := strings.Repeat("😀", 150)
	cases := []struct {
		name, content, query, want string
	}{
		// start = idx-200 snaps forward to just after the last "\n\n" at or before it.
		{"snap start", "intro\n\n" + pad + " needle here\n\nmore text\n\ntail", "needle",
			pad + " needle here\n\nmore text\n\ntail"},
		// end = idx+500 snaps to the next "\n\n" at or after it.
		{"snap end", "needle start\n\n" + ys + "\n\nlast", "needle", "needle start\n\n" + ys},
		// idx is the first substring hit of any term, even inside a longer word.
		{"substring index", strings.Repeat("z", 300) + " cached then cache", "cache",
			strings.Repeat("z", 199) + " cached then cache"},
		// Windows count UTF-16 units: start 101 cuts an emoji in half (U+FFFD, as V8 writes it).
		{"utf16 window", emoji + " needle", "needle", "�" + strings.Repeat("😀", 99) + " needle"},
	}
	for _, c := range cases {
		got, ok := autoMemorySnippet(c.content, autoMemoryTerms(c.query))
		if !ok || got != c.want {
			t.Errorf("%s: ok=%v\ngot  %q\nwant %q", c.name, ok, got, c.want)
		}
	}
}
