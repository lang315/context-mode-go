package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/lang315/context-mode-go/internal/upgrade"
)

// upgradeCmd runs `cmgo upgrade [--source <dir>] [--dry-run]` (spec §2). A
// usage error exits 2, a failed step 1.
func upgradeCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cmgo upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "cmgo source checkout (default: $CMGO_SOURCE, else go install the module)")
	dryRun := fs.Bool("dry-run", false, "print the steps without running them")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cmgo upgrade: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	err := upgrade.Upgrade(upgrade.Options{
		Source:   *source,
		DryRun:   *dryRun,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Run:      runCommand(stderr),
		Stdout:   stdout,
	})
	if err != nil {
		return 1
	}
	return 0
}

// runCommand runs each step with its output streamed to out, so go and git
// errors show above the step's [ ] line.
func runCommand(out io.Writer) upgrade.Runner {
	return func(dir string, env []string, name string, args ...string) error {
		c := exec.Command(name, args...)
		c.Dir = dir
		if len(env) > 0 {
			c.Env = append(os.Environ(), env...)
		}
		c.Stdout, c.Stderr = out, out
		return c.Run()
	}
}
