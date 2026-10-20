package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// S:3418-3445, minus the "inline index tool" WHEN NOT bullet (cmgo has no ctx_index).
const fetchDescription = "Fetches URL content, converts HTML to markdown (JSON is chunked by key paths, plain text indexed directly), persists it in a searchable knowledge base, and returns a small preview window per source. The raw page bytes never enter your conversation — they live in storage and you retrieve any section on-demand via ctx_search.\n\n" +
	"Caching: every fetch is cached on disk and reused for repeat calls within the TTL window. The default TTL is 24 hours; override per-call with the `ttl` parameter (milliseconds, `ttl: 0` bypasses cache like `force: true`). Stored content older than 14 days is cleaned up on startup.\n\n" +
	"WHEN:\n" +
	"  - You need web content (docs, changelogs, API references, spec pages) and the raw page bytes should NOT enter your conversation\n" +
	"  - Multi-URL research (library evaluation, migration scans, doc comparisons): pass the `requests` array and a `concurrency` value 2-8 for parallel I/O\n" +
	"  - You want repeat lookups against the same URL to be cheap (TTL cache hits return only a hint, no re-fetch)\n" +
	"  - You want a long-lived cache window (override `ttl` upward for stable specs) or a guaranteed-fresh fetch (`ttl: 0` or `force: true`)\n\n" +
	"WHEN NOT:\n" +
	"  - The page is SPA-rendered (JavaScript-required to materialize content) — this is a plain HTTP fetch, no headless browser\n\n" +
	"RETURNS:\n" +
	"  Per-source preview windows extracted around indexable headings plus indexing metadata (chunk counts, source labels, cache state). Raw content is NOT echoed back — retrieve any section on-demand via ctx_search(source: \"<label>\"). Concurrency parallelizes the fetch phase up to your chosen value (capped by the host's logical CPU count); the FTS5 write phase always runs serially because SQLite is a single-writer store. Net latency = max(fetch latency across the pool) + sum(per-source index write time). Cache hits skip both phases and return a small freshness hint instead of re-fetching. Use 4-8 for stable I/O-bound batches; lower the value when the target host enforces a per-IP rate limit you cannot raise.\n\n" +
	"EXAMPLE: ctx_fetch_and_index(\n" +
	"  requests: [{url: \"https://react.dev/...\", source: \"react\"}, {url: \"https://vuejs.org/...\", source: \"vue\"}],\n" +
	"  concurrency: 5\n" +
	")"

// S:3446-3498 (zod schema), with coerced fields widened to accept strings.
const fetchSchema = `{
  "type": "object",
  "properties": {
    "url": {"type": "string", "description": "Single URL to fetch and index (legacy single-shape)"},
    "source": {"type": "string", "description": "Label for the indexed content when using single ` + "`url`" + ` (e.g., 'React useEffect docs', 'Supabase Auth API'). For batch, put source in each requests entry."},
    "requests": {
      "type": ["array", "string"],
      "items": {
        "type": "object",
        "properties": {
          "url": {"type": "string", "description": "URL to fetch"},
          "source": {"type": "string", "description": "Label for this URL's indexed content"}
        },
        "required": ["url"]
      },
      "minItems": 1,
      "description": "Batch shape: array of {url, source?} entries. Use with concurrency>1 for parallel fetch. Each request indexed under its own source label. Output preserves input order."
    },
    "concurrency": {"type": ["integer", "string"], "minimum": 1, "maximum": 8, "default": 1, "description": "Max URLs to fetch in parallel (1-8, default: 1). Use 4-8 for I/O-bound multi-URL batches (library docs, changelogs, pricing pages). Capped by os.cpus().length on small machines (response notes when capped). Indexing is always serial regardless — only fetches race."},
    "force": {"type": ["boolean", "string"], "description": "Skip cache and re-fetch even if content was recently indexed"},
    "ttl": {"type": ["integer", "string"], "minimum": 0, "description": "Override the cache freshness window for this call, in milliseconds. ` + "`ttl: 0`" + ` bypasses the cache like ` + "`force: true`" + `; omit to use the default 24h TTL."}
  }
}`

type fetchRequest struct {
	url, source string
	hasSource   bool
}

// fetchJob is the parallel-safe half of one URL (fetchOneUrl, S:3288-3370):
// either a final outcome (cached, error) or a body still to index.
type fetchJob struct {
	out  fetchOutcome
	body fetch.Result
}

func registerFetchAndIndex(s *mcp.Server, d *Deps) {
	no := false
	yes := true
	s.AddTool(&mcp.Tool{
		Name:        "ctx_fetch_and_index",
		Title:       "Fetch & Index URL(s)",
		Description: fetchDescription,
		InputSchema: json.RawMessage(fetchSchema),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, OpenWorldHint: &yes},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleFetchAndIndex(ctx, d, req.Params.Arguments), nil
	})
}

// handleFetchAndIndex is the tool handler, S:3499-3671.
func handleFetchAndIndex(ctx context.Context, d *Deps, raw json.RawMessage) *mcp.CallToolResult {
	a, err := ParseArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	batch, hasRequests, err := fetchRequests(a)
	if err != nil {
		return ArgError(err)
	}
	concurrency, err := fetchInt(a, "concurrency", 1, 1, 8)
	if err != nil {
		return ArgError(err)
	}
	ttl, err := fetchInt(a, "ttl", -1, 0, math.MaxInt64)
	if err != nil {
		return ArgError(err)
	}
	force, _, err := a.Bool("force")
	if err != nil {
		return ArgError(err)
	}
	if len(batch) == 0 {
		return TextResult("ctx_fetch_and_index requires either `url` (single) or `requests: [{url, source?}, ...]` (batch).", true)
	}

	single := !hasRequests && len(batch) == 1
	eff, capped := fetchPoolSize(int(concurrency), len(batch), !single && concurrency > 1)

	// Parallel fetch with eff workers; results land by index.
	jobs := make([]fetchJob, len(batch))
	sem := make(chan struct{}, eff)
	var wg sync.WaitGroup
	for i, r := range batch {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			jobs[i] = fetchOne(ctx, d, r, force, ttl)
		}()
	}
	wg.Wait()

	// Serial index drain in request order, S:3542-3570.
	outs := make([]fetchOutcome, len(jobs))
	for i, j := range jobs {
		if j.out.kind != "" {
			outs[i] = j.out
			continue
		}
		outs[i] = fetchIndex(d, batch[i], j.body)
	}

	var text string
	var isErr bool
	if single {
		text, isErr = fetchSingleText(outs[0])
	} else {
		text, isErr = fetchBatchText(outs, eff, capped)
	}
	return TextResult(text, isErr)
}

// fetchRequests normalizes {url, source} and requests[] (requests wins), S:3502-3506.
func fetchRequests(a Args) ([]fetchRequest, bool, error) {
	arr, hasRequests, err := a.Array("requests")
	if err != nil {
		return nil, false, err
	}
	if hasRequests {
		if len(arr) == 0 {
			return nil, true, errors.New("requests: expected at least 1 entry")
		}
		out := make([]fetchRequest, 0, len(arr))
		for i, e := range arr {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, true, fmt.Errorf("requests[%d]: expected object", i)
			}
			ra := Args(m)
			u, hasURL, err := ra.String("url")
			if err != nil || !hasURL {
				return nil, true, fmt.Errorf("requests[%d].url: expected string", i)
			}
			src, hasSrc, err := ra.String("source")
			if err != nil {
				return nil, true, fmt.Errorf("requests[%d].%v", i, err)
			}
			out = append(out, fetchRequest{url: u, source: src, hasSource: hasSrc})
		}
		return out, true, nil
	}
	u, _, err := a.String("url")
	if err != nil {
		return nil, false, err
	}
	src, hasSrc, err := a.String("source")
	if err != nil {
		return nil, false, err
	}
	if u == "" {
		return nil, false, nil
	}
	return []fetchRequest{{url: u, source: src, hasSource: hasSrc}}, false, nil
}

// fetchInt mirrors z.coerce.number().int().min(lo).max(hi).optional(); def when absent.
func fetchInt(a Args, key string, def, lo, hi int64) (int64, error) {
	f, ok, err := a.Number(key)
	if err != nil || !ok {
		return def, err
	}
	if f != math.Trunc(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%s: expected integer", key)
	}
	if f < float64(lo) || f > float64(hi) {
		return 0, fmt.Errorf("%s: must be between %d and %d", key, lo, hi)
	}
	return int64(f), nil
}

// fetchOne is the cache check plus fetch, S:3288-3370. It writes nothing.
// ttl < 0 means "not given".
func fetchOne(ctx context.Context, d *Deps, r fetchRequest, force bool, ttl int64) fetchJob {
	label := fetchCacheKey(r.source, r.hasSource, r.url)
	if !force && ttl != 0 {
		src, err := d.Store.SourceByLabel(d.Cfg.ProjectKey, label)
		if err == nil && src != nil {
			ttlMs := ttl
			if ttlMs < 0 {
				ttlMs = fetchTTLDefaultMs
			}
			if age := d.Now().Sub(src.IndexedAt).Milliseconds(); age < ttlMs {
				return fetchJob{out: fetchOutcome{kind: "cached", url: r.url, label: src.Label, chunks: src.ChunkCount, age: fetchAge(age), ttl: fetchTTL(ttlMs)}}
			}
		}
	}
	res, err := d.Fetch.Fetch(ctx, r.url)
	if err != nil {
		reason := "exit"
		if errors.Is(err, fetch.ErrEmpty) {
			reason = "empty"
		}
		return fetchJob{out: fetchOutcome{kind: "error", url: r.url, err: err.Error(), reason: reason}}
	}
	return fetchJob{body: res}
}

// fetchIndex is indexFetched, S:3378-3409: route by content kind, one store write.
func fetchIndex(d *Deps, r fetchRequest, res fetch.Result) fetchOutcome {
	var chunks []store.Chunk
	switch res.Kind {
	case fetch.KindJSON:
		var err error
		if chunks, err = store.ChunkJSON(res.Body, store.ChunkMaxBytes); err != nil || len(chunks) == 0 {
			chunks = store.ChunkPlainText(res.Body, 20)
		}
	case fetch.KindHTML:
		chunks = store.ChunkMarkdown(res.Body, store.ChunkMaxBytes)
	default:
		chunks = store.ChunkPlainText(res.Body, 20)
	}
	ir, err := d.Store.Index(d.Cfg.ProjectKey, fetchCacheKey(r.source, r.hasSource, r.url), chunks)
	if err != nil {
		return fetchOutcome{kind: "error", url: r.url, err: err.Error(), reason: "throw"}
	}
	return fetchOutcome{kind: "fetched", url: r.url, label: ir.Label, chunks: ir.TotalChunks, bytes: len(res.Body), preview: fetchPreview(res.Body)}
}
