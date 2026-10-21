// Command cmgo-hook runs one Claude Code hook fire: `cmgo-hook <event>`. It
// is a separate binary from cmgo so a hook process does not load the MCP
// server, executor and fetcher, whose start-up cost every hook fire would
// otherwise pay (hooks spec §1.1).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/lang315/context-mode-go/internal/buildinfo"
	"github.com/lang315/context-mode-go/internal/hookcmd"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version string

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cmgo-hook <event>|version")
		os.Exit(2)
	}
	// No hook event is named version; cmgo doctor runs this to compare builds.
	if os.Args[1] == "version" {
		fmt.Println("cmgo-hook " + buildinfo.Version(version))
		return
	}
	os.Exit(hookcmd.Run(os.Args[1], hookcmd.IO{
		Getenv: os.Getenv, Getwd: os.Getwd, Stdin: os.Stdin, Stdout: os.Stdout, Now: time.Now,
	}))
}
