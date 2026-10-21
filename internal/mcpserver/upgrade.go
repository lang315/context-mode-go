package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// upgradeDescription is S:4282-4285 with cmgo for context-mode.
const upgradeDescription = "Upgrade cmgo to the latest version. Returns a shell command to execute. " +
	"You MUST run the returned command using your shell tool (Bash, shell_execute, " +
	"run_in_terminal, etc.) and display the output as a checklist. " +
	"Tell the user to restart their session after upgrade."

// registerUpgrade registers ctx_upgrade. As in TS (S:4270-4427) it runs
// nothing: it hands the model `cmgo upgrade` to run in its shell.
func registerUpgrade(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_upgrade",
		Title:       "Upgrade Plugin",
		Description: upgradeDescription,
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(bool), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := TextResult(upgradeText(d.Binary), false)
		d.Stats.Record(d, "ctx_upgrade", 0, res)
		return res, nil
	})
}

// upgradeText is the TS reply (S:4401-4421) with the cmgo command and an
// example checklist of cmgo upgrade's own steps. binary "" means this
// executable.
func upgradeText(binary string) string {
	if binary == "" {
		if p, err := os.Executable(); err == nil {
			binary, _ = filepath.EvalSymlinks(p)
		}
	}
	return "## ctx-upgrade\n\n" +
		"Run this command using your shell execution tool:\n\n" +
		"```\n\"" + binary + "\" upgrade\n```\n\n" +
		"After the command completes, display results as a markdown checklist:\n" +
		"- `[x]` for success, `[ ]` for failure\n" +
		"- Example format:\n" +
		"  ```\n" +
		"  ## cmgo upgrade\n" +
		"  - [x] git -C ~/src/context-mode-go pull --ff-only\n" +
		"  - [x] go install ./cmd/cmgo ./cmd/cmgo-hook (in ~/src/context-mode-go)\n" +
		"  - [x] ~/go/bin/cmgo install\n" +
		"  ```\n" +
		"- Tell the user to restart their session to pick up the new version."
}
