// Command cmgo is a Go rewrite of context-mode for Claude Code.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: cmgo <serve|version>")
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
	default:
		fmt.Fprintf(stderr, "cmgo: unknown command %q\n", args[0])
		return 2
	}
}
