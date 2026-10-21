// Package upgrade rebuilds cmgo from source and re-runs cmgo install
// (spec §2). There are no release binaries for the private repo, so the Go
// toolchain builds it.
package upgrade

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Module is cmgo's module path.
const Module = "github.com/lang315/context-mode-go"

// Runner runs name with args in dir ("" = the current dir), with env entries
// added to the process environment.
type Runner func(dir string, env []string, name string, args ...string) error

// Options are Upgrade's inputs; tests inject Getenv, LookPath and Run.
type Options struct {
	Source   string // --source; "" falls back to $CMGO_SOURCE, then the module
	DryRun   bool
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Run      Runner
	Stdout   io.Writer
}

// step is one checklist line and the work behind it.
type step struct {
	label string
	run   func() error
}

// Upgrade prints one [x] line per step done and stops at the first failure,
// printing it as [ ] with the error, which it returns. A dry run prints the
// steps and runs nothing.
func Upgrade(o Options) error {
	steps, err := plan(o)
	if err != nil {
		return err
	}
	for _, s := range steps {
		if o.DryRun {
			fmt.Fprintln(o.Stdout, "dry run: "+s.label)
			continue
		}
		if err := s.run(); err != nil {
			fmt.Fprintf(o.Stdout, "[ ] %s — %v\n", s.label, err)
			return err
		}
		fmt.Fprintln(o.Stdout, "[x] "+s.label)
	}
	return nil
}

// plan builds the spec §2 steps. Checking a source dir is a read, not a
// command, so it runs even in a dry run.
func plan(o Options) ([]step, error) {
	var steps []step
	src := o.Source
	if src == "" {
		src = o.Getenv("CMGO_SOURCE")
	}
	if src != "" {
		dir, err := filepath.Abs(src)
		if err == nil {
			err = checkSource(dir)
		}
		label := "source " + dir + " is " + Module
		if err != nil {
			fmt.Fprintf(o.Stdout, "[ ] %s — %v\n", label, err)
			return nil, err
		}
		fmt.Fprintln(o.Stdout, "[x] "+label)
		steps = append(steps,
			o.command("git -C "+dir+" pull --ff-only", "", nil, "git", "-C", dir, "pull", "--ff-only"),
			o.command("go install ./cmd/cmgo ./cmd/cmgo-hook (in "+dir+")", dir, nil, "go", "install", "./cmd/cmgo", "./cmd/cmgo-hook"),
		)
	} else {
		// The repo is private; keep any GOPRIVATE the user already has.
		private := "github.com/lang315/*"
		if cur := o.Getenv("GOPRIVATE"); cur != "" {
			private = cur + "," + private
		}
		args := []string{"install", Module + "/cmd/cmgo@latest", Module + "/cmd/cmgo-hook@latest"}
		env := []string{"GOPRIVATE=" + private}
		steps = append(steps, o.command(env[0]+" go "+strings.Join(args, " "), "", env, "go", args...))
	}
	cmgo := filepath.Join(o.goBin(), "cmgo")
	// The new binary refreshes hooks, MCP registration and statusline.
	return append(steps, o.command(cmgo+" install", "", nil, cmgo, "install")), nil
}

// command is a step that runs name, failing with a hint when go or git is
// not on PATH.
func (o Options) command(label, dir string, env []string, name string, args ...string) step {
	return step{label, func() error {
		if name == "go" || name == "git" {
			if _, err := o.LookPath(name); err != nil {
				hint := "install git"
				if name == "go" {
					hint = "install Go from https://go.dev/dl"
				}
				return fmt.Errorf("%s not found on PATH; %s", name, hint)
			}
		}
		return o.Run(dir, env, name, args...)
	}}
}

// goBin is where go install puts binaries: $GOBIN, else the first $GOPATH
// entry's bin, else ~/go/bin.
// shortcut: a GOBIN set only with `go env -w` is not seen; run `go env GOBIN`
// if that is ever needed.
func (o Options) goBin() string {
	if b := o.Getenv("GOBIN"); b != "" {
		return b
	}
	if gp := filepath.SplitList(o.Getenv("GOPATH")); len(gp) > 0 && gp[0] != "" {
		return filepath.Join(gp[0], "bin")
	}
	return filepath.Join(o.Getenv("HOME"), "go", "bin")
}

// checkSource requires dir/go.mod to name cmgo's module.
func checkSource(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "module" {
			if m := strings.Trim(f[1], `"`); m != Module {
				return fmt.Errorf("go.mod names %s", m)
			}
			return nil
		}
	}
	return errors.New("go.mod has no module line")
}
