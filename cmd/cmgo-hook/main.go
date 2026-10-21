// Command cmgo-hook runs one Claude Code hook fire: `cmgo-hook <event>`. It
// is a separate binary from cmgo so a hook process does not load the MCP
// server, executor and fetcher, whose start-up cost every hook fire would
// otherwise pay (hooks spec §1.1).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/hookcmd"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cmgo-hook <event>")
		os.Exit(2)
	}
	os.Exit(hookcmd.Run(os.Args[1], hookcmd.IO{
		Getenv: os.Getenv, Getwd: os.Getwd, Stdin: os.Stdin, Stdout: os.Stdout, Now: time.Now,
	}))
}
