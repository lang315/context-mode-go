package mcpserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lang315/context-mode-go/internal/stats"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// statsDescription is S:3947-3950 minus "estimated token usage": cmgo keeps
// no token counts (stats spec §0).
const statsDescription = "Returns context consumption statistics for the current session. " +
	"Shows bytes kept out of and returned to context, breakdown by tool, call counts, " +
	"session memory, and context savings ratio."

// registerStats registers ctx_stats (stats spec §2). version is cmgo's, for
// the report's last line.
func registerStats(s *mcp.Server, d *Deps, version string) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_stats",
		Title:       "Session Statistics",
		Description: statsDescription,
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: new(bool), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		d.Stats.Flush(statsFlushWait) // the report sees the calls made before it
		res := statsTool(d, version)
		d.Stats.Record(d, "ctx_stats", 0, res)
		return res, nil
	})
}

func statsTool(d *Deps, version string) *mcp.CallToolResult {
	q := store.StatsQuery{Project: d.Cfg.ProjectKey}
	if d.Stats != nil {
		q.ServerID, q.CCPID = d.Stats.ServerID, d.Stats.CCPID
	}
	snap, err := d.Store.Stats(q)
	if err != nil {
		return ErrorResult("Stats error: %v", err)
	}
	return TextResult(stats.Format(statsReport(snap, d.Now(), version)), false)
}

// statsReport maps the store snapshot onto the renderer's input.
func statsReport(s store.StatsSnapshot, now time.Time, version string) stats.Report {
	r := stats.Report{
		Version:             version,
		Now:                 now,
		SessionLinked:       s.SessionLinked,
		SessionStart:        sqliteUTC(s.SessionStart),
		Raw:                 s.Raw,
		Redirects:           s.Redirects,
		RedirectBytes:       s.RedirectBytes,
		Events:              s.Events,
		Compactions:         s.Compactions,
		ProjectSources:      s.ProjectSources,
		ProjectContentBytes: s.ProjectContentBytes,
		ProjectSessions:     s.ProjectSessions,
		ProjectEvents:       s.ProjectEvents,
		LifetimeKept:        s.LifetimeKept,
		LifetimeSessions:    s.LifetimeSessions,
		LifetimeSince:       sqliteUTC(s.LifetimeSince),
	}
	for _, t := range s.Tools {
		r.Tools = append(r.Tools, stats.ToolRow{Tool: t.Tool, Calls: t.Calls, Returned: t.BytesReturned, Kept: t.BytesKept})
	}
	for name, n := range s.Categories {
		r.Categories = append(r.Categories, stats.Category{Name: name, Count: n})
	}
	return r
}

// sqliteUTC parses a datetime('now') value; "" or a bad value gives the zero time.
func sqliteUTC(s string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04:05", s)
	return t
}
