// Command cmgo is a Go rewrite of context-mode for Claude Code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/buildinfo"
	"github.com/lang315/context-mode-go/internal/hookcmd"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
// Without it, init resolves it from the build info (spec §1).
var version string

func init() { version = buildinfo.Version(version) }

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run dispatches a subcommand. stdin feeds `hook`; serve keeps os.Stdin
// because the MCP transport needs a ReadCloser.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cmgo <serve|hook|install|uninstall|doctor|upgrade|version>")
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "cmgo "+version)
		return 0
	case "serve":
		return serve(context.Background(), serveIO{
			getenv: os.Getenv, getwd: os.Getwd, in: os.Stdin, out: os.Stdout, stderr: stderr,
		})
	case "hook": // the same code as cmgo-hook, for manual runs; install registers cmgo-hook
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cmgo hook <event>")
			return 2
		}
		return hookcmd.Run(args[1], hookcmd.IO{Getenv: os.Getenv, Getwd: os.Getwd, Stdin: stdin, Stdout: stdout, Now: time.Now})
	case "install", "uninstall":
		return installCmd(args[0], args[1:], stdout, stderr)
	case "doctor":
		return doctorCmd(args[1:], stdout, stderr)
	case "upgrade":
		return upgradeCmd(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cmgo: unknown command %q\n", args[0])
		return 2
	}
}
