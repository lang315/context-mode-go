package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/lang315/context-mode-go/internal/doctor"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// doctorDescription is S:4151-4154 with cmgo for context-mode.
const doctorDescription = "Diagnose the cmgo installation. Runs all checks server-side and " +
	"returns a plain-text status report with [OK]/[FAIL]/[WARN] prefixes " +
	"(renderer-safe across MCP clients). No CLI execution needed."

// registerDoctor registers ctx_doctor (spec §1): the `cmgo doctor` text,
// run inside this server.
func registerDoctor(s *mcp.Server, d *Deps, version string) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_doctor",
		Title:       "Run Diagnostics",
		Description: doctorDescription,
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: new(bool), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		e := doctor.NewEnv(d.Cfg, version, d.Exec)
		if d.Binary != "" {
			e.Binary = d.Binary
		}
		text, _ := doctor.Report(ctx, e)
		res := TextResult(text, false)
		d.Stats.Record(d, "ctx_doctor", 0, res)
		return res, nil
	})
}
