package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/tsimport"
)

// runImport is `cmgo import [--from <dir>] [--dry-run] [--force]` (spec §3).
// A usage error exits 2, any other error 1.
func runImport(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("cmgo import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "TS data root (default: $CONTEXT_MODE_DIR, $CLAUDE_CONFIG_DIR/context-mode, ~/.claude/context-mode)")
	dryRun := fs.Bool("dry-run", false, "print the counts and write nothing to cmgo.db")
	force := fs.Bool("force", false, "import again although cmgo.db records an import")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "cmgo import: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	cfg, err := config.Load(getenv, os.Getwd)
	if err != nil {
		fmt.Fprintf(stderr, "cmgo import: %v\n", err)
		return 1
	}
	root := *from
	if root == "" {
		if root = tsimport.DefaultRoot(getenv, cfg.Home, cfg.ClaudeDir); root == "" {
			fmt.Fprintln(stderr, "cmgo import: no TS context-mode data found in $CONTEXT_MODE_DIR, $CLAUDE_CONFIG_DIR/context-mode or ~/.claude/context-mode; use --from")
			return 1
		}
	}
	start := time.Now()
	r, err := tsimport.Run(tsimport.Options{Root: root, DataDir: cfg.DataDir, DBPath: cfg.DBPath,
		DryRun: *dryRun, Force: *force, Now: start, SourceRetention: sourceRetention})
	if err != nil {
		fmt.Fprintf(stderr, "cmgo import: %v\n", err)
		return 1
	}
	title := "cmgo import from " + root
	if *dryRun {
		title += " (dry run)"
	}
	fmt.Fprintln(stdout, title)
	fmt.Fprintf(stdout, "  sessions: %d imported, %s\n", r.Sessions,
		skipped(reason{r.InactiveSessions, "inactive"}, reason{r.NoProjectSessions, "no project"}, reason{r.UnreadableSessionDBs, "unreadable"}))
	fmt.Fprintf(stdout, "  events:   %d imported, %d duplicates\n", r.Events, r.DupEvents)
	fmt.Fprintf(stdout, "  sources:  %d imported (%d chunks), %s\n", r.Sources, r.Chunks,
		skipped(reason{r.ExpiredSources, "expired"}, reason{r.NoProjectSources, "no project"}, reason{r.ExistingSources, "exists"}, reason{r.UnreadableContentDBs, "unreadable"}))
	fmt.Fprintf(stdout, "done in %d ms\n", time.Since(start).Milliseconds())
	if r.UnreadableSessionDBs+r.UnreadableContentDBs > 0 {
		fmt.Fprintln(stdout, "some TS databases could not be read; close the Claude Code sessions that still run the TS plugin and run the import again with --force")
	}
	return 0
}

// reason is one skip count of the report.
type reason struct {
	n    int
	what string
}

// skipped formats "<total> skipped (<n> <reason>, ...)", leaving out zero
// counts and the parentheses when nothing was skipped.
func skipped(reasons ...reason) string {
	total := 0
	var parts []string
	for _, r := range reasons {
		if r.n > 0 {
			total += r.n
			parts = append(parts, fmt.Sprintf("%d %s", r.n, r.what))
		}
	}
	if total == 0 {
		return "0 skipped"
	}
	return fmt.Sprintf("%d skipped (%s)", total, strings.Join(parts, ", "))
}
