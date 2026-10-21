package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// StatsRecorder writes this server's tool_stats rows (stats spec §1.3).
type StatsRecorder struct {
	ServerID  string // "<pid>-<start unix nanos>" of this cmgo serve process
	CCPID     int    // owning Claude Code process, 0 if unknown
	StartedAt string // server start, UTC 'YYYY-MM-DD HH:MM:SS'
}

// Record adds one finished call of tool: bytes_returned is the UTF-8 size of
// the result text, bytes_kept is max(0, raw - returned), where raw is what
// the tool produced or read. A nil recorder records nothing, and a failed
// write is dropped: stats never fail a tool.
func (r *StatsRecorder) Record(d *Deps, tool string, raw int64, res *mcp.CallToolResult) {
	if r == nil || res == nil {
		return
	}
	var returned int64
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			returned += int64(len(tc.Text))
		}
	}
	_ = d.Store.AddToolStat(r.ServerID, r.CCPID, d.Cfg.ProjectKey, r.StartedAt, tool, returned, max(0, raw-returned))
}
