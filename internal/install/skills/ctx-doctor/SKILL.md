---
name: ctx-doctor
description: |
  Run cmgo diagnostics: version, storage, database and FTS5, executor,
  runtimes, hooks, hook binary, MCP registration, statusline, TS plugin.
  Trigger: /ctx-doctor, "ctx doctor", or "diagnose cmgo".
user-invocable: true
---

# cmgo doctor

1. Call the `mcp__cmgo__ctx_doctor` MCP tool (no parameters). It runs every check in the server and returns a plain-text report.
2. Show the report as it is. Its lines already start with `[OK]`, `[WARN]` or `[FAIL]`.
3. For each `[WARN]` or `[FAIL]` line, repeat the `fix:` hint it carries, if any.
4. If the MCP tool is unavailable (the server is not registered or not running), run `cmgo doctor` with your shell tool and show its output the same way.
