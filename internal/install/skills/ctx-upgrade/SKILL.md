---
name: ctx-upgrade
description: |
  Upgrade cmgo: pull and rebuild from source with go install, then refresh
  the hooks, the MCP registration and these skills with cmgo install.
  Trigger: /ctx-upgrade, "ctx upgrade", or "update cmgo".
user-invocable: true
---

# cmgo upgrade

1. Call the `mcp__cmgo__ctx_upgrade` MCP tool (no parameters). It returns a shell command; it runs nothing itself.
2. Run that command with your shell tool. It prints one line per step, `[x] <step>` or `[ ] <step> — <error>`, and stops at the first failure.
3. Show the steps as a checklist with the same `[x]` / `[ ]` marks and the version cmgo now reports.
4. Tell the user to start a new Claude Code session, or run `/mcp` and reconnect `cmgo`, so the MCP server runs the new binary. Hooks use the new binary at once.
5. If the MCP tool is unavailable, run `cmgo upgrade` with your shell tool.
