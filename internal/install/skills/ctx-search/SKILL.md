---
name: ctx-search
description: |
  Search cmgo's knowledge base and session memory with ctx_search.
  Trigger: /ctx-search, or "what did we decide about ...", "find in the index ...".
user-invocable: true
---

# cmgo search

1. Call the `mcp__cmgo__ctx_search` MCP tool. Put every related question in one `queries` array, each two to four specific technical terms.
2. Pass `sort: "timeline"` for session memory: what was decided, tried or asked before. It picks the newest matches and lists them in time order.
3. Pass `source` when the user names an indexed label (partial match), for example a URL label from `ctx_fetch_and_index`.
4. Answer from the returned snippets and name their sources. Do not reread the raw files or pages they came from.
5. If nothing matches, say so. To add content, use `ctx_fetch_and_index` for URLs or `ctx_batch_execute` for command output.

```json
{ "queries": ["token refresh", "auth middleware"], "limit": 5 }
{ "queries": ["decision caching"], "sort": "timeline" }
```
