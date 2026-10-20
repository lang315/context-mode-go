package executor

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeExec returns an Executor whose PATH contains exactly the given names (resolved to /bin/<name>).
func fakeExec(env map[string]string, onPath ...string) *Executor {
	e := New("/proj")
	have := map[string]bool{}
	for _, n := range onPath {
		have[n] = true
	}
	e.lookPath = func(n string) (string, error) {
		if have[n] {
			return "/fake/" + n, nil
		}
		return "", errors.New("not found")
	}
	e.getenv = func(k string) string { return env[k] }
	return e
}

func TestLanguagesEnum(t *testing.T) {
	want := []Language{"javascript", "typescript", "python", "shell", "ruby", "go", "rust", "php", "perl", "r", "elixir", "csharp"}
	if !reflect.DeepEqual(Languages, want) {
		t.Fatalf("Languages = %v", Languages)
	}
}

func TestCommandArgv(t *testing.T) {
	cases := []struct {
		name   string
		onPath []string
		lang   Language
		want   []string
	}{
		{"js bun", []string{"bun", "node"}, JavaScript, []string{"/fake/bun", "run", "f"}},
		{"js node", []string{"node"}, JavaScript, []string{"/fake/node", "f"}},
		{"ts bun", []string{"bun", "tsx"}, TypeScript, []string{"/fake/bun", "run", "f"}},
		{"ts tsx", []string{"tsx", "ts-node"}, TypeScript, []string{"/fake/tsx", "f"}},
		{"ts ts-node", []string{"ts-node"}, TypeScript, []string{"/fake/ts-node", "f"}},
		{"python3", []string{"python3", "python"}, Python, []string{"/fake/python3", "f"}},
		{"python", []string{"python"}, Python, []string{"/fake/python", "f"}},
		{"shell bash", []string{"bash"}, Shell, []string{"/fake/bash", "f"}},
		{"shell sh", nil, Shell, []string{"sh", "f"}},
		{"go", []string{"go"}, Go, []string{"/fake/go", "run", "f"}},
		{"rust", []string{"rustc"}, Rust, []string{"/fake/rustc", "f"}},
		{"r Rscript", []string{"Rscript", "R"}, R, []string{"/fake/Rscript", "f"}},
		{"r R", []string{"R"}, R, []string{"/fake/R", "f"}},
		{"csharp", []string{"dotnet-script"}, CSharp, []string{"/fake/dotnet-script", "f"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := fakeExec(nil, c.onPath...).command(c.lang, "f")
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("command = %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestShellHonorsAllowlistedSHELL(t *testing.T) {
	if _, err := os.Stat("/bin/zsh"); err != nil {
		t.Skip("/bin/zsh missing")
	}
	got, _ := fakeExec(map[string]string{"SHELL": "/bin/zsh"}, "bash").command(Shell, "f")
	if got[0] != "/bin/zsh" {
		t.Fatalf("SHELL=/bin/zsh ignored: %v", got)
	}
	py := filepath.Join(t.TempDir(), "python3")
	os.WriteFile(py, nil, 0o755)
	got, _ = fakeExec(map[string]string{"SHELL": py}, "bash").command(Shell, "f")
	if got[0] != "/fake/bash" {
		t.Fatalf("non-allowlisted SHELL used: %v", got)
	}
	got, _ = fakeExec(map[string]string{"SHELL": "/nonexistent/bash"}, "bash").command(Shell, "f")
	if got[0] != "/fake/bash" {
		t.Fatalf("nonexistent SHELL used: %v", got)
	}
}

func TestBunFallbackPath(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".bun", "bin"), 0o755)
	os.WriteFile(filepath.Join(home, ".bun", "bin", "bun"), nil, 0o755)
	got, _ := fakeExec(map[string]string{"HOME": home}, "node").command(JavaScript, "f")
	if got[0] != filepath.Join(home, ".bun", "bin", "bun") || got[1] != "run" {
		t.Fatalf("bun fallback not used: %v", got)
	}
}

func TestMissingRuntimeErrors(t *testing.T) {
	want := map[Language]string{
		JavaScript: "No JavaScript runtime available. Install Node.js or Bun on PATH (the host process is not itself a JS runtime).",
		TypeScript: "No TypeScript runtime available. Install one of: bun (recommended), tsx (npm i -g tsx), or ts-node.",
		Python:     "No Python runtime available. Install python3 or python.",
		Ruby:       "Ruby not available. Install ruby.",
		Go:         "Go not available. Install go.",
		Rust:       "Rust not available. Install rustc via https://rustup.rs",
		PHP:        "PHP not available. Install php.",
		Perl:       "Perl not available. Install perl.",
		R:          "R not available. Install R / Rscript.",
		Elixir:     "Elixir not available. Install elixir.",
		CSharp:     "C# not available. Install dotnet-script via `dotnet tool install -g dotnet-script`.",
	}
	e := fakeExec(nil)
	for lang, msg := range want {
		if _, err := e.command(lang, "f"); err == nil || err.Error() != msg {
			t.Errorf("%s: err = %v; want %q", lang, err, msg)
		}
	}
}

func TestRuntimeCached(t *testing.T) {
	e := fakeExec(nil, "python3")
	calls := 0
	look := e.lookPath
	e.lookPath = func(n string) (string, error) { calls++; return look(n) }
	e.command(Python, "a")
	e.command(Python, "b")
	if calls != 1 {
		t.Fatalf("lookPath called %d times; want 1", calls)
	}
}
