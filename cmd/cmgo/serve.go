package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/mcpserver"
	"github.com/lang315/context-mode-go/internal/procinfo"
	"github.com/lang315/context-mode-go/internal/routing"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	sourceRetention = 14 * 24 * time.Hour // spec §2.1 SOURCE_RETENTION
	cleanupEvery    = 24 * time.Hour      // spec §2.2: at most once per 24 h
)

// serveIO is what `cmgo serve` talks over; tests pass pipes.
type serveIO struct {
	getenv func(string) string
	getwd  func() (string, error)
	in     io.ReadCloser
	out    io.WriteCloser
	stderr io.Writer
}

// statsDrain is how long shutdown waits for queued stats records.
const statsDrain = time.Second

// newStatsRecorder identifies this server process in tool_stats (stats spec
// §1.1-§1.2): pid plus start time, so a reused pid is a new server, and the
// Claude Code ancestor that links it to the session hooks record.
func newStatsRecorder(pid int, start time.Time, look procinfo.Lookup) *mcpserver.StatsRecorder {
	return &mcpserver.StatsRecorder{
		ServerID:  fmt.Sprintf("%d-%d", pid, start.UnixNano()),
		CCPID:     procinfo.ClaudePID(pid, look),
		StartedAt: start.UTC().Format("2006-01-02 15:04:05"),
	}
}

// serve runs the MCP server until stdin closes or SIGINT/SIGTERM arrives,
// then terminates background process groups and closes the DB.
func serve(ctx context.Context, sio serveIO) int {
	cfg, err := config.Load(sio.getenv, sio.getwd)
	if err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: config: %v\n", err)
		return 1
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: open %s: %v\n", cfg.DBPath, err)
		return 1
	}
	defer st.Close()
	// Hooks redirect curl and WebFetch only while a server is live (spec §2.5).
	if remove, err := routing.WriteSentinel(filepath.Join(cfg.DataDir, "run"), os.Getpid()); err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: mcp-ready sentinel skipped: %v\n", err)
	} else {
		defer remove()
	}
	if _, err := st.CleanupIfDue(time.Now(), sourceRetention, cleanupEvery); err != nil {
		fmt.Fprintf(sio.stderr, "cmgo: cleanup skipped: %v\n", err) // never fatal
	}
	ex := executor.New(cfg.ProjectDir)
	defer ex.Shutdown()

	d := &mcpserver.Deps{
		Cfg:    cfg,
		Store:  st,
		Exec:   ex,
		Policy: security.NewPolicyDir(cfg.ProjectDir, cfg.ClaudeDir),
		Fetch:  fetch.New(cfg.FetchStrict),
		Stats:  newStatsRecorder(os.Getpid(), time.Now(), procinfo.System()),
	}
	defer d.Stats.Close(statsDrain) // write what is queued, for at most this long
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// A signal cancels ctx; kill running commands now instead of waiting for Run to drain.
	defer context.AfterFunc(ctx, ex.Shutdown)()
	// Lift the 16 MB frame cap: TS accepted any size of tool argument.
	t := &mcp.IOTransport{Reader: sio.in, Writer: sio.out, MaxLineLength: -1}
	if err := mcpserver.New(d, version).Run(ctx, t); err != nil && ctx.Err() == nil && err != io.EOF {
		fmt.Fprintf(sio.stderr, "cmgo: serve: %v\n", err)
		return 1
	}
	return 0
}
