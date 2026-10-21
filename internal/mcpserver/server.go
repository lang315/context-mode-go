// Package mcpserver registers cmgo's MCP tools and formats their output.
package mcpserver

import (
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps are the long-lived services every tool handler uses.
type Deps struct {
	Cfg    config.Config
	Store  *store.Store
	Exec   *executor.Executor
	Policy *security.Policy
	Fetch  *fetch.Fetcher
	Now    func() time.Time // injectable clock; defaults to time.Now
	Stats  *StatsRecorder   // nil records no tool_stats
	Binary string           // cmgo's path for ctx_doctor and ctx_upgrade; "" = this executable
}

// New builds the MCP server with all nine tools registered.
func New(d *Deps, version string) *mcp.Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "cmgo", Version: version}, nil)
	registerExecute(s, d)
	registerExecuteFile(s, d)
	registerBatch(s, d)
	registerSearch(s, d)
	registerFetchAndIndex(s, d)
	registerPurge(s, d)
	registerStats(s, d, version)
	registerDoctor(s, d, version)
	registerUpgrade(s, d)
	return s
}
