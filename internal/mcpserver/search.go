package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	searchSnippetMax = 1500      // S:2750
	searchOutputCap  = 40 * 1024 // S:2672
)

// searchDescription is S:2564-2585 minus the ctx_stats, CONTEXT_MODE_* env
// and file-staleness sentences, and minus session-memory claims until
// sub-project 3 adds session events (restore them then).
const searchDescription = "Search a unified knowledge base with a multi-strategy ranking pipeline. Two parallel matchers run on every query: a Porter-stemming matcher (\"caching\" finds \"cached\", \"caches\", \"cach\") and a trigram-substring matcher (\"useEff\" finds \"useEffect\"). Their ranked lists are merged via Reciprocal Rank Fusion, so a document that ranks well in both surfaces above one that wins only on a single strategy. Multi-term queries get an additional proximity-rerank pass that boosts passages where the query terms appear close together. Typos are corrected via Levenshtein distance and re-searched. Result snippets are window-extracted around the matched terms, not blindly truncated.\n" +
	"\n" +
	"The knowledge base is unified: queries reach indexed content you stored (ctx_fetch_and_index, ctx_batch_execute output, and large ctx_execute / ctx_execute_file output).\n" +
	"\n" +
	"WHEN:\n" +
	"  - You want to recall something that exists in storage (recently indexed content) instead of re-reading raw sources\n" +
	"  - You have multiple related questions about the same body of knowledge — batch every question into one call (the ranking pipeline runs per-query but the round-trip cost is paid once)\n" +
	"  - You want to scope the query to one labelled source (pass `source` — partial match is fine)\n" +
	"  - You want matches in the order they were indexed, oldest first (pass `sort: \"timeline\"`)\n" +
	"  - You want to filter ranked results by content shape (pass `contentType: \"code\"` to surface implementation snippets or `contentType: \"prose\"` to surface explanations)\n" +
	"\n" +
	"WHEN NOT:\n" +
	"  - The data you want to query has never been stored in the knowledge base — capture first (run a gather-and-index call), then come back here to query\n" +
	"  - You have one ad-hoc question against data that is not in the knowledge base — answer it inline by running code in the sandbox tool; one round-trip instead of capture-then-query\n" +
	"\n" +
	"RETURNS:\n" +
	"  Per-query ranked sections with window-extracted snippets. Use 2-4 specific technical terms per query. Each response carries a throttle counter (call #N/M in the rolling time window); results taper toward the soft cap and calls block after the hard cap.\n" +
	"\n" +
	"EXAMPLE: ctx_search(queries: [\"root cause\", \"proposed fix\", \"test coverage\"], source: \"issue-#683\")\n" +
	"EXAMPLE: ctx_search(queries: [\"useEffect cleanup pattern\"], source: \"react-docs\", contentType: \"code\", limit: 5)"

// searchSchema mirrors buildCtxSearchInputSchema (ctx-search-schema.ts:62-105)
// without the shared-DB `project` field.
const searchSchema = `{
  "type": "object",
  "properties": {
    "queries": {"type": ["array", "string"], "items": {"type": "string"}, "description": "Array of search queries. Batch ALL questions in one call."},
    "limit": {"type": ["number", "string"], "default": 3, "description": "Results per query (default: 3)"},
    "source": {"type": "string", "description": "Filter to a specific indexed source (partial match)."},
    "contentType": {"type": "string", "enum": ["code", "prose"], "description": "Filter results by content type: 'code' or 'prose'."},
    "sort": {"type": "string", "enum": ["relevance", "timeline"], "default": "relevance", "description": "Sort mode. 'relevance' (default): BM25 ranked. 'timeline': the same matches ordered oldest-indexed first."}
  }
}`

const emptyKBMessage = "Knowledge base is empty — no content has been indexed yet.\n\n" +
	"ctx_search is a follow-up tool that queries previously indexed content. " +
	"To gather and index content first, use:\n" +
	"  • ctx_batch_execute(commands, queries) — run commands, auto-index output, and search in one call\n" +
	"  • ctx_fetch_and_index(url) — fetch a URL, index it, then search with ctx_search\n" +
	"  • ctx_index(content, source) — manually index text content\n\n" +
	"After indexing, ctx_search becomes available for follow-up queries."

func registerSearch(s *mcp.Server, d *Deps) {
	guard := &floodGuard{} // one server per process, so this is process-wide
	s.AddTool(&mcp.Tool{
		Name:        "ctx_search",
		Title:       "Search Indexed Content",
		Description: searchDescription,
		InputSchema: json.RawMessage(searchSchema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: new(bool), OpenWorldHint: new(bool)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return searchTool(d, guard, req.Params.Arguments), nil
	})
}

type searchArgs struct {
	queries     []string
	limit       float64
	source      string
	contentType store.ContentType
	sort        string
}

func parseSearchArgs(raw json.RawMessage) (searchArgs, error) {
	sa := searchArgs{limit: 3, sort: "relevance"}
	a, err := ParseArgs(raw)
	if err != nil {
		return sa, err
	}
	if sa.queries, _, err = a.Strings("queries"); err != nil {
		return sa, err
	}
	// No legacy `query`: TS reads it (S:2621) but its zod schema has no such
	// field, so the SDK strips it and TS answers "provide query or queries".
	if n, ok, err := a.Number("limit"); err != nil {
		return sa, err
	} else if ok {
		sa.limit = n
	}
	if sa.source, _, err = a.String("source"); err != nil {
		return sa, err
	}
	if ct, ok, err := a.String("contentType"); err != nil {
		return sa, err
	} else if ok {
		if ct != string(store.Code) && ct != string(store.Prose) {
			return sa, errors.New(`contentType: expected "code" or "prose"`)
		}
		sa.contentType = store.ContentType(ct)
	}
	if so, ok, err := a.String("sort"); err != nil {
		return sa, err
	} else if ok {
		if so != "relevance" && so != "timeline" {
			return sa, errors.New(`sort: expected "relevance" or "timeline"`)
		}
		sa.sort = so
	}
	return sa, nil
}

// searchTool is the ctx_search handler (S:2592-2805) over the content store only.
func searchTool(d *Deps, guard *floodGuard, raw json.RawMessage) *mcp.CallToolResult {
	sa, err := parseSearchArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	project := d.Cfg.ProjectKey
	if sa.sort != "timeline" {
		n, err := d.Store.ChunkCount(project)
		if err != nil {
			return ErrorResult("Search error: %v", err)
		}
		if n == 0 {
			return TextResult(emptyKBMessage, true)
		}
	}
	if len(sa.queries) == 0 {
		return TextResult("Error: provide query or queries.", true)
	}

	now := d.Now()
	flood := guard.record(now)
	if flood.blocked {
		return ErrorResult("BLOCKED: %d search calls in %ds. "+
			"You're flooding context. STOP making individual search calls. "+
			"Use ctx_batch_execute(commands, queries) for your next research step.",
			flood.count, int(math.Round(now.Sub(flood.windowStart).Seconds())))
	}
	effectiveLimit := 1
	if !flood.softCapped {
		effectiveLimit = int(math.Min(sa.limit, 2)) // JS slice(0, 1.5) keeps 1
	}

	opts := store.SearchOpts{Project: project, Source: sa.source, ContentType: sa.contentType, Limit: effectiveLimit, Mode: sa.sort}
	totalSize := 0
	var sections []string
	for _, q := range sa.queries {
		if totalSize > searchOutputCap {
			sections = append(sections, "## "+q+"\n(output cap reached)\n")
			continue
		}
		var results []store.SearchResult
		if effectiveLimit > 0 { // TS slice(0, <=0) is empty; the store would default to 3
			if results, err = d.Store.SearchWithFallback(q, opts); err != nil {
				return ErrorResult("Search error: %v", err)
			}
		}
		if len(results) == 0 {
			sections = append(sections, "## "+q+"\nNo results found.")
			continue
		}
		parts := make([]string, len(results))
		for i, r := range results {
			ts := ""
			if !r.IndexedAt.IsZero() {
				ts = " | " + r.IndexedAt.UTC().Format("2006-01-02 15:04")
			}
			parts[i] = fmt.Sprintf("--- [current-session%s | %s] ---\n### %s\n\n%s",
				ts, r.Source, r.Title, store.ExtractSnippet(r.Content, q, searchSnippetMax, r.Highlighted))
		}
		formatted := strings.Join(parts, "\n\n")
		sections = append(sections, "## "+q+"\n\n"+formatted)
		totalSize += jsLen(formatted)
	}
	output := strings.Join(sections, "\n\n---\n\n")

	if flood.count >= searchSoftCapAt {
		output += fmt.Sprintf("\n\n⚠ search call #%d/%d in this window. "+
			"Results limited to %d/query. %d call(s) remaining before block. "+
			`Batch queries: ctx_search(queries: ["q1","q2","q3"]) or use ctx_batch_execute.`,
			flood.count, searchBlockAfter, effectiveLimit, max(0, searchBlockAfter-flood.count))
	} else {
		output += fmt.Sprintf("\n\n> Throttle: call #%d/%d in this window. "+
			"%d call(s) before soft cap. "+
			"Prefer ctx_search(queries: [...]) array form for multi-query workloads — it counts as a single call.",
			flood.count, searchBlockAfter, max(0, searchSoftCapAt-flood.count))
	}
	// TS S:2785 falls back to "No results found. Indexed sources: …" when the
	// output is blank; the throttle line above makes that unreachable.
	return TextResult(output, false)
}
