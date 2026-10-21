---
name: ctx-purge
description: |
  Permanently delete cmgo data: one session's events, or the whole project's
  knowledge base and session events. Destructive, no undo.
  Trigger: /ctx-purge, "ctx purge", or "wipe / reset the knowledge base".
user-invocable: true
---

# cmgo purge

There are two scopes:

- **Session** (`sessionId`): deletes that session's events only. Other sessions and the knowledge base stay.
- **Project** (`scope: "project"`): deletes every indexed source and chunk of the current project, and the events of every session of the project.

## Steps

1. Ask the user which scope they want. For a session, get its id.
2. Warn that the deletion cannot be undone. For the project scope, say that indexed content must be fetched or indexed again to search it.
3. Only after the user confirms, call the `mcp__cmgo__ctx_purge` MCP tool:
   - session: `{ "confirm": true, "sessionId": "<id>" }`
   - project: `{ "confirm": true, "scope": "project" }`
4. Report the tool's message, which lists what was deleted.

`/clear` and `/compact` delete nothing; `ctx_purge` is the only way to delete cmgo data.
