---
name: ctx-stats
description: |
  Show how much context cmgo kept out of the context window: this session,
  per tool, session memory, this project and lifetime. Read-only.
  Trigger: /ctx-stats, "ctx stats", or a question about context savings.
user-invocable: true
---

# cmgo stats

1. Call the `mcp__cmgo__ctx_stats` MCP tool (no parameters).
2. Copy the ENTIRE tool output into your reply as it is. Do not summarize, collapse or paraphrase it.
3. After the output, add ONE sentence on the key number, for example "cmgo kept **4.2 MB** (93%) out of context this session." If the session has no calls yet, say so.

`ctx_stats` only reads. To delete data, use `/ctx-purge`.
