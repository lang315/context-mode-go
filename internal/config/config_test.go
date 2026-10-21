package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{"HOME": "/h"}), func() (string, error) { return "/work/Proj", nil })
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/h/.claude/cmgo" || c.DBPath != "/h/.claude/cmgo/cmgo.db" {
		t.Fatalf("data dir: %+v", c)
	}
	if c.ProjectDir != "/work/Proj" {
		t.Fatalf("project dir: %q", c.ProjectDir)
	}
	want := "/work/Proj"
	if runtime.GOOS == "darwin" {
		want = "/work/proj"
	}
	if c.ProjectKey != want {
		t.Fatalf("project key: %q want %q", c.ProjectKey, want)
	}
	if c.FetchStrict {
		t.Fatal("strict should default off")
	}
}

func TestLoadOverridesInPriorityOrder(t *testing.T) {
	c, err := Load(env(map[string]string{
		"HOME": "/h", "CMGO_DIR": "/d", "CMGO_PROJECT_DIR": "/a", "CLAUDE_PROJECT_DIR": "/b", "CMGO_FETCH_STRICT": "1",
	}), func() (string, error) { return "/c", nil })
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/d" || c.ProjectDir != "/a" || !c.FetchStrict {
		t.Fatalf("%+v", c)
	}
	c, _ = Load(env(map[string]string{"HOME": "/h", "CLAUDE_PROJECT_DIR": "/b"}), func() (string, error) { return "/c", nil })
	if c.ProjectDir != "/b" {
		t.Fatalf("CLAUDE_PROJECT_DIR should win over cwd: %q", c.ProjectDir)
	}
}

func TestLoadMakesRelativeProjectAbsolute(t *testing.T) {
	c, _ := Load(env(map[string]string{"HOME": "/h", "CMGO_PROJECT_DIR": "rel"}), func() (string, error) { return "/c", nil })
	if !filepath.IsAbs(c.ProjectDir) {
		t.Fatalf("not absolute: %q", c.ProjectDir)
	}
}

func TestLoadClaudeDir(t *testing.T) {
	for _, c := range []struct{ name, val, want string }{
		{"unset", "", "/h/.claude"},
		{"blank", "  \t", "/h/.claude"},
		{"absolute", "/cfg/claude", "/cfg/claude"},
		{"tilde", "~/cfg", "/h/cfg"},
		{"bare tilde", "~", "/h"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Load(env(map[string]string{"HOME": "/h", "CLAUDE_CONFIG_DIR": c.val}), func() (string, error) { return "/w", nil })
			if err != nil {
				t.Fatal(err)
			}
			if got.ClaudeDir != c.want {
				t.Fatalf("ClaudeDir = %q, want %q", got.ClaudeDir, c.want)
			}
		})
	}
}
