// Package executor runs ctx_execute / ctx_execute_file code in a sandboxed subprocess.
package executor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Language is one of the 12 languages ctx_execute accepts.
type Language string

const (
	JavaScript Language = "javascript"
	TypeScript Language = "typescript"
	Python     Language = "python"
	Shell      Language = "shell"
	Ruby       Language = "ruby"
	Go         Language = "go"
	Rust       Language = "rust"
	PHP        Language = "php"
	Perl       Language = "perl"
	R          Language = "r"
	Elixir     Language = "elixir"
	CSharp     Language = "csharp"
)

// Languages is the ctx_execute language enum, in schema order.
var Languages = []Language{JavaScript, TypeScript, Python, Shell, Ruby, Go, Rust, PHP, Perl, R, Elixir, CSharp}

// OutputHardCap is the combined stdout+stderr limit; past it the process group is killed.
const OutputHardCap = 100 << 20

// Executor runs scripts. Runtimes are detected lazily per language and cached.
type Executor struct {
	projectDir string
	hardCap    int64                        // OutputHardCap; tests lower it
	lookPath   func(string) (string, error) // exec.LookPath; tests stub it
	getenv     func(string) string          // os.Getenv; tests stub it

	mu       sync.Mutex
	runtimes map[Language]string // "" = not available
	bg       map[int]struct{}    // pgids of backgrounded runs
}

// New returns an Executor whose default cwd is projectDir.
func New(projectDir string) *Executor {
	return &Executor{
		projectDir: projectDir,
		hardCap:    OutputHardCap,
		lookPath:   exec.LookPath,
		getenv:     os.Getenv,
		runtimes:   map[Language]string{},
		bg:         map[int]struct{}{},
	}
}

// first returns the resolved path of the first name found on PATH, or "".
func (e *Executor) first(names ...string) string {
	for _, n := range names {
		if p, err := e.lookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// detect resolves the runtime binary for lang. Port of runtime.ts:334-391 (POSIX branches only).
func (e *Executor) detect(lang Language) string {
	switch lang {
	case JavaScript:
		if b := e.bun(); b != "" {
			return b
		}
		return e.first("node")
	case TypeScript:
		if b := e.bun(); b != "" {
			return b
		}
		return e.first("tsx", "ts-node")
	case Python:
		return e.first("python3", "python")
	case Shell:
		// runtime.ts:342-357: honor $SHELL only when it exists and is an allowlisted shell.
		if s := e.getenv("SHELL"); s != "" {
			switch filepath.Base(s) {
			case "bash", "sh", "zsh", "dash":
				if _, err := os.Stat(s); err == nil {
					return s
				}
			}
		}
		if p := e.first("bash"); p != "" {
			return p
		}
		return "sh"
	case Ruby:
		return e.first("ruby")
	case Go:
		return e.first("go")
	case Rust:
		return e.first("rustc")
	case PHP:
		return e.first("php")
	case Perl:
		return e.first("perl")
	case R:
		return e.first("Rscript", "R")
	case Elixir:
		return e.first("elixir")
	case CSharp:
		return e.first("dotnet-script")
	}
	return ""
}

// bun finds bun on PATH, else at ~/.bun/bin/bun. Port of runtime.ts:129-152 (POSIX).
func (e *Executor) bun() string {
	if p := e.first("bun"); p != "" {
		return p
	}
	if home := e.getenv("HOME"); home != "" {
		p := filepath.Join(home, ".bun", "bin", "bun")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// runtime returns the cached runtime for lang, detecting it on first use.
func (e *Executor) runtime(lang Language) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.runtimes[lang]; ok {
		return p
	}
	p := e.detect(lang)
	e.runtimes[lang] = p
	return p
}

// missing holds the TS error text for each language whose runtime is absent (runtime.ts:650-763).
var missing = map[Language]string{
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

// command builds argv for running file. Port of runtime.ts:644-764 (POSIX).
// For rust it returns [rustc, file]; Run compiles and then runs the binary.
func (e *Executor) command(lang Language, file string) ([]string, error) {
	if _, ok := missing[lang]; !ok && lang != Shell {
		return nil, errors.New("Unsupported language: " + string(lang))
	}
	bin := e.runtime(lang)
	if bin == "" {
		return nil, errors.New(missing[lang])
	}
	switch lang {
	case JavaScript, TypeScript:
		if filepath.Base(bin) == "bun" {
			return []string{bin, "run", file}, nil
		}
	case Go:
		return []string{bin, "run", file}, nil
	}
	return []string{bin, file}, nil
}
