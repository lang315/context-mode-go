package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// purgeDescription is S:4457-4481 minus the ctx_stats, stats-file, events
// markdown and per-session FTS5 chunk wording (cmgo has one DB and chunks
// carry no session) and the stderr deprecation warning.
const purgeDescription = `DESTRUCTIVE: permanently delete indexed content. Cannot be undone. Requires confirm:true and exactly one scope.

WHEN:
  - User explicitly asks to clear a specific session ('purge this session', 'wipe this conversation')
  - User explicitly asks to reset the whole project ('reset everything', 'wipe the knowledge base')

WHEN NOT:
  - User says 'reset', 'clear', or 'wipe' without naming a scope -> ask which scope before calling
  - User wants to free memory or improve performance -> do not purge

SCOPES (pass exactly one):
  - Per-session: ctx_purge(confirm: true, sessionId: "<uuid>") deletes that session's events (auto-captured decisions, errors, plans, user prompts, rejected approaches, etc.); sibling sessions are preserved.
  - Per-project: ctx_purge(confirm: true, scope: "project") wipes the FTS5 knowledge base and every session event of this project.

CONTRACT:
  - confirm:true is required; confirm:false returns 'purge cancelled'.
  - sessionId and scope:'project' together return 'ambiguous - pick one'.
  - scope:'session' without sessionId throws (sessionId required).
  - Bare {confirm:true} is deprecated: maps to scope:'project'.

RETURNS:
  A summary of removed rows + the resolved scope.

EXAMPLE: ctx_purge(confirm: true, sessionId: "7c8a-1234-5678-9abc-def012345678")
EXAMPLE: ctx_purge(confirm: true, scope: "project")`

// purgeSchema mirrors the TS zod object (S:4485-4503) minus the stats wording.
// confirm is not required: spec §4 answers a missing confirm with the
// cancel message instead of a schema error.
const purgeSchema = `{
  "type": "object",
  "properties": {
    "confirm": {"type": ["boolean", "string"], "description": "MUST be true. Destructive operation; false returns 'purge cancelled'."},
    "sessionId": {"type": "string", "description": "UUID of a single session. Pairs with confirm:true to wipe only that session's events. Sibling sessions are preserved. MUST NOT be combined with scope:'project'."},
    "scope": {"type": "string", "enum": ["session", "project"], "description": "Explicit scope selector. 'session' REQUIRES sessionId. 'project' wipes the entire project (FTS5 + every session). Omit only for the deprecated bare-{confirm:true} back-compat path."}
  }
}`

// registerPurge registers ctx_purge. Port of S:4445-4611 over cmgo's single DB.
func registerPurge(s *mcp.Server, d *Deps) {
	s.AddTool(&mcp.Tool{
		Name:        "ctx_purge",
		Title:       "Purge Knowledge Base",
		Description: purgeDescription,
		InputSchema: json.RawMessage(purgeSchema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := purgeTool(d, req.Params.Arguments)
		d.Stats.Record(d, "ctx_purge", 0, res)
		return res, nil
	})
}

func purgeTool(d *Deps, raw json.RawMessage) *mcp.CallToolResult {
	a, err := ParseArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	confirm, _, err := a.Bool("confirm")
	if err != nil {
		return ArgError(err)
	}
	sessionID, _, err := a.String("sessionId")
	if err != nil {
		return ArgError(err)
	}
	scope, _, err := a.String("scope")
	if err != nil {
		return ArgError(err)
	}
	if scope != "" && scope != "session" && scope != "project" {
		return ArgError(errors.New(`scope: expected "session" or "project"`))
	}
	if sessionID != "" && scope == "project" {
		return TextResult("Ambiguous purge: sessionId implies scope:'session', cannot combine with scope:'project'. "+
			"Use scope:'project' WITHOUT sessionId for the legacy whole-project wipe.", true)
	}
	if !confirm {
		return TextResult("Purge cancelled. Pass confirm: true to proceed.", false)
	}
	if scope == "" && sessionID != "" {
		scope = "session"
	}
	switch scope {
	case "session":
		if sessionID == "" { // purge.ts:189 throws
			return TextResult("purgeSession: scope:'session' requires sessionId. Pass scope:'project' for the legacy whole-project wipe.", true)
		}
		rows, err := d.Store.PurgeSession(sessionID, d.Cfg.ProjectKey)
		if err != nil {
			return ErrorResult("Purge error: %v", err)
		}
		what := "no matching rows"
		if rows > 0 {
			what = "session events"
		}
		return TextResult("Purged session "+sessionID+": "+what+". Other sessions and project-wide stats preserved.", false)
	default: // "project", or bare {confirm:true}
		sources, sessions, stats, err := d.Store.PurgeProject(d.Cfg.ProjectKey)
		if err != nil {
			return ErrorResult("Purge error: %v", err)
		}
		if sources == 0 && sessions == 0 && stats == 0 {
			return TextResult("Nothing to purge for this project ("+d.Cfg.ProjectKey+").", false)
		}
		return TextResult("Purged: knowledge base (FTS5), session events, session stats. All session data for this project has been permanently deleted.", false)
	}
}
