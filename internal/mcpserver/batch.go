package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/lang315/context-mode-go/internal/executor"
	"github.com/lang315/context-mode-go/internal/security"
	"github.com/lang315/context-mode-go/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	commandEchoMax   = 500       // S:1477
	batchSnippetMax  = 3000      // S:1398
	batchOutputCap   = 80 * 1024 // S:1375
	batchSearchLimit = 3         // S:1393
	batchTermsMax    = 40        // ST:1573 getDistinctiveTerms default
)

const batchDescription = "Run multiple commands in ONE call. Every command's output is auto-indexed into the knowledge base; if you also pass `queries`, the matching sections come back in the same round trip so a follow-up search call is not needed.\n" +
	"\n" +
	"Concurrency parallelizes the FETCH phase (run-the-commands). The DERIVATION phase — turning raw output into an answer — still belongs in code: add a processing command that consumes the indexed output and prints only the answer, so the raw bytes never enter your conversation (Think-in-Code, same principle as the sandbox tool).\n" +
	"\n" +
	"WHEN:\n" +
	"  - You have 3+ related commands you would otherwise run sequentially (multi-issue lookups, git log + git diff + git blame, multi-file reads, multi-region cloud queries)\n" +
	"  - You want to gather AND query in one round trip — pass `queries` so the matching sections come back inline\n" +
	"  - You want to parallelize I/O-bound work — pass `concurrency` 2-8 (network calls, gh CLI, cloud APIs, multi-repo git reads)\n" +
	"  - The combined output is large enough that piping it through ctx_search later would itself be expensive — let auto-index + inline queries do both in one shot\n" +
	"\n" +
	"WHEN NOT:\n" +
	"  - Single command with no follow-up query — run it in the sandbox tool directly\n" +
	"  - CPU-bound or stateful commands — keep concurrency at 1 (npm test, build, lint, port-binding servers, lock-file holders, anything that races on the same resource)\n" +
	"\n" +
	"RETURNS:\n" +
	"  Auto-indexed section list per command label, plus top matches per query (when `queries` is passed). Raw output is NOT echoed in full — only the matched windows. Concurrency>1 switches each command to its own per-command timeout (no shared budget); concurrency=1 preserves the legacy shared-budget cascading-skip-on-timeout path. Use 4-8 for I/O-bound batches; keep at 1 for CPU work or shared-state commands; lower the value when target hosts enforce per-IP rate limits.\n" +
	"\n" +
	"EXAMPLE: ctx_batch_execute(\n" +
	"  commands: [\n" +
	"    {label: \"issue 1\", command: \"gh issue view 1\"},\n" +
	"    {label: \"issue 2\", command: \"gh issue view 2\"},\n" +
	"    {label: \"summarize\", command: \"echo done\"}\n" +
	"  ],\n" +
	"  queries: [\"root cause\", \"proposed fix\"],\n" +
	"  concurrency: 2\n" +
	")"

// batchSchema mirrors the zod schema at S:3715-3776; coerced fields also accept strings.
const batchSchema = `{
  "type": "object",
  "properties": {
    "commands": {
      "type": ["array", "string"],
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "label": {"type": "string", "description": "Section header for this command's output (e.g., 'README', 'Package.json', 'Source Tree')"},
          "command": {"type": "string", "description": "Shell command to execute"}
        },
        "required": ["label", "command"]
      },
      "description": "Commands to execute as a batch. Output is labeled with the section header. Default order is sequential; pass concurrency>1 to run in parallel (output stays in input order)."
    },
    "queries": {
      "type": ["array", "string"],
      "minItems": 1,
      "items": {"type": "string"},
      "description": "Search queries to extract information from indexed output. Use 5-8 comprehensive queries. Each returns top 5 matching sections with full content. This is your ONLY chance — put ALL your questions here. No follow-up calls needed."
    },
    "timeout": {
      "type": ["number", "string"],
      "description": "Max execution time in ms. When omitted, no server-side timer fires — the MCP host's RPC timeout governs. With concurrency=1, the value (when set) is a shared budget across commands; with concurrency>1, it is applied per-command."
    },
    "concurrency": {
      "type": ["integer", "string"],
      "minimum": 1,
      "maximum": 8,
      "default": 1,
      "description": "Max commands to run in parallel (1-8, default: 1). Use 4-8 for I/O-bound batches (network, gh, curl, multi-repo git reads). Keep at 1 for CPU-bound (npm test, build, lint) or stateful commands (ports, locks). >1 switches to per-command timeouts (no shared budget) and individual ` + "`(timed out)`" + ` blocks instead of cascading skip."
    },
    "cwd": {"type": "string", "description": "Optional working directory for all shell commands in this batch."},
    "query_scope": {
      "type": "string",
      "enum": ["batch", "global"],
      "default": "batch",
      "description": "Scope for ` + "`queries`" + ` (default: ` + "`batch`" + `). ` + "`batch`" + ` searches ONLY the chunks produced by this batch's commands — useful when you want answers about the just-fetched output. ` + "`global`" + ` searches the entire persistent index (same scope as ctx_search) — useful when you want the batch commands to enrich context and the queries to also surface related prior knowledge in one round trip."
    }
  },
  "required": ["commands", "queries"]
}`

func registerBatch(s *mcp.Server, d *Deps) {
	t := true
	s.AddTool(&mcp.Tool{
		Name:        "ctx_batch_execute",
		Title:       "Batch Execute & Search",
		Description: batchDescription,
		InputSchema: json.RawMessage(batchSchema),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &t, OpenWorldHint: &t},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return batchExecute(ctx, d, req.Params.Arguments), nil
	})
}

// batchArgs is the validated ctx_batch_execute input.
type batchArgs struct {
	commands    []Command
	queries     []string
	timeoutMs   float64
	hasTimeout  bool
	concurrency int
	cwd         string
	scope       string
}

func parseBatchArgs(raw json.RawMessage) (batchArgs, error) {
	var b batchArgs
	a, err := ParseArgs(raw)
	if err != nil {
		return b, err
	}
	var ok bool
	if b.commands, ok, err = a.Commands("commands"); err != nil {
		return b, err
	} else if !ok || len(b.commands) == 0 {
		return b, errors.New("commands: expected a non-empty array")
	}
	if b.queries, ok, err = a.Strings("queries"); err != nil {
		return b, err
	} else if !ok || len(b.queries) == 0 {
		return b, errors.New("queries: expected a non-empty array")
	}
	if b.timeoutMs, b.hasTimeout, err = a.Number("timeout"); err != nil {
		return b, err
	}
	b.concurrency = 1
	if c, ok, err := a.Number("concurrency"); err != nil {
		return b, err
	} else if ok {
		if c != math.Trunc(c) || c < 1 || c > 8 {
			return b, errors.New("concurrency: expected an integer from 1 to 8")
		}
		b.concurrency = int(c)
	}
	if b.cwd, _, err = a.String("cwd"); err != nil {
		return b, err
	}
	b.scope = "batch"
	if sc, ok, err := a.String("query_scope"); err != nil {
		return b, err
	} else if ok {
		if sc != "batch" && sc != "global" {
			return b, errors.New(`query_scope: expected "batch" or "global"`)
		}
		b.scope = sc
	}
	return b, nil
}

// batchExecute is the ctx_batch_execute handler (S:3778-3892).
func batchExecute(ctx context.Context, d *Deps, raw json.RawMessage) *mcp.CallToolResult {
	b, err := parseBatchArgs(raw)
	if err != nil {
		return ArgError(err)
	}
	for _, c := range b.commands {
		if blocked, pattern := d.Policy.CheckCommand(c.Command); blocked {
			return TextResult(security.BlockedMessage(pattern), true)
		}
	}

	outputs, err := runBatchCommands(ctx, d.Exec, b)
	if err != nil {
		return ErrorResult("Batch execution error: %v", err)
	}
	stdout := strings.Join(outputs, "\n")
	totalLines := strings.Count(stdout, "\n") + 1

	labels := make([]string, len(b.commands))
	for i, c := range b.commands {
		labels[i] = c.Label
	}
	source := "batch:" + jsPrefix(strings.Join(labels, ","), 80)
	indexed, err := d.Store.Index(d.Cfg.ProjectKey, source, store.ChunkMarkdown(stdout, store.ChunkMaxBytes))
	if err != nil {
		return ErrorResult("Batch execution error: %v", err)
	}

	lines := []string{
		fmt.Sprintf("Executed %d commands (%d lines, %s). Indexed %d sections. Searched %d queries.",
			len(b.commands), totalLines, KB(len(stdout)), indexed.TotalChunks, len(b.queries)),
		"",
		"## Commands",
		"",
	}
	for _, c := range b.commands {
		lines = append(lines, fmt.Sprintf("- %s: `%s`", c.Label, truncateCommandForEcho(c.Command)))
	}
	lines = append(lines, "", "## Indexed Sections", "")
	for _, s := range indexed.Sections {
		lines = append(lines, fmt.Sprintf("- %s (%s)", s.Title, KB(s.Bytes)))
	}
	lines = append(lines, "")
	qr, err := formatBatchQueryResults(d.Store, d.Cfg.ProjectKey, b.queries, source, batchOutputCap, b.scope)
	if err != nil {
		return ErrorResult("Batch execution error: %v", err)
	}
	lines = append(lines, qr...)
	terms, err := d.Store.DistinctiveTerms(indexed.SourceID, batchTermsMax)
	if err != nil {
		return ErrorResult("Batch execution error: %v", err)
	}
	if len(terms) > 0 {
		lines = append(lines, "\nSearchable terms for follow-up: "+strings.Join(terms, ", "))
	} else {
		lines = append(lines, "")
	}
	// TS S:3810 also has a "Batch timed out after Nms. No output captured."
	// branch; it needs zero outputs, which commands.min(1) makes unreachable.
	return TextResult(strings.Join(lines, "\n"), false)
}

// runBatchCommands runs every command as shell (S:1560-1641). Serial mode
// shares one timeout budget and skips what is left once it runs out; an
// executor error aborts the batch. Parallel mode gives each command the full
// timeout and turns an executor error into that command's section.
func runBatchCommands(ctx context.Context, ex *executor.Executor, b batchArgs) ([]string, error) {
	run := func(c Command, timeout time.Duration) (executor.Result, error) {
		return ex.Run(ctx, executor.Request{Language: executor.Shell, Code: c.Command, Timeout: timeout, Cwd: b.cwd})
	}
	budget := time.Duration(b.timeoutMs * float64(time.Millisecond))

	if b.concurrency <= 1 {
		outputs := make([]string, 0, len(b.commands))
		start := time.Now()
		for i, c := range b.commands {
			var perCmd time.Duration
			if b.hasTimeout {
				remaining := budget - time.Since(start)
				if remaining <= 0 {
					outputs = append(outputs, skippedSection(c.Label))
					continue
				}
				perCmd = remaining
			}
			res, err := run(c, perCmd)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, formatCommandOutput(c.Label, c.Command, combineExecOutput(res)))
			if res.TimedOut {
				for _, rest := range b.commands[i+1:] {
					outputs = append(outputs, skippedSection(rest.Label))
				}
				break
			}
		}
		return outputs, nil
	}

	var perCmd time.Duration
	timeoutText := "?"
	if b.hasTimeout {
		perCmd = budget
		timeoutText = strconv.FormatFloat(b.timeoutMs, 'f', -1, 64)
	}
	outputs := make([]string, len(b.commands))
	sem := make(chan struct{}, b.concurrency)
	var wg sync.WaitGroup
	for i, c := range b.commands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			res, err := run(c, perCmd)
			if err != nil {
				outputs[i] = fmt.Sprintf("# %s\n\n(executor error: %v)\n", c.Label, err)
				return
			}
			out := formatCommandOutput(c.Label, c.Command, combineExecOutput(res))
			if res.TimedOut {
				out = strings.TrimSuffix(out, "\n") + "\n(timed out after " + timeoutText + "ms)\n"
			}
			outputs[i] = out
		}()
	}
	wg.Wait()
	return outputs, nil
}

func skippedSection(label string) string {
	return "# " + label + "\n\n(skipped — batch timeout exceeded)\n"
}

// formatCommandOutput is S:1529-1543 without the __CM_FS__ byte counter.
func formatCommandOutput(label, command, raw string) string {
	if raw == "" {
		raw = "(no output)"
	}
	return "# " + label + "\n\n$ " + truncateCommandForEcho(command) + "\n\n" + raw + "\n"
}

// combineExecOutput is S:1545-1551.
func combineExecOutput(r executor.Result) string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	sep := "\n"
	if strings.HasSuffix(r.Stdout, "\n") {
		sep = ""
	}
	return r.Stdout + sep + r.Stderr
}

// truncateCommandForEcho is S:1479-1483: collapse whitespace, cap at 500.
func truncateCommandForEcho(command string) string {
	cleaned := strings.Join(strings.Fields(command), " ")
	if jsLen(cleaned) <= commandEchoMax {
		return cleaned
	}
	return jsPrefix(cleaned, commandEchoMax) + "…"
}

// formatBatchQueryResults is S:1371-1418. scope "batch" matches the batch
// source label exactly; "global" searches the whole project.
func formatBatchQueryResults(st *store.Store, project string, queries []string, source string, maxOutput int, scope string) ([]string, error) {
	var sections []string
	outputSize := 0
	opts := store.SearchOpts{Project: project, Limit: batchSearchLimit, Mode: "relevance", SourceExact: true}
	if scope != "global" {
		opts.Source = source
	}
	for _, q := range queries {
		if outputSize > maxOutput {
			sections = append(sections, fmt.Sprintf("## %s\n(output cap reached — use ctx_search(queries: [\"%s\"]) for details)\n", q, q))
			continue
		}
		results, err := st.SearchWithFallback(q, opts)
		if err != nil {
			return nil, err
		}
		sections = append(sections, "## "+q, "")
		if len(results) == 0 {
			sections = append(sections, "No matching sections found.", "")
			continue
		}
		for _, r := range results {
			snippet := store.ExtractSnippet(r.Content, q, batchSnippetMax, r.Highlighted)
			sections = append(sections, "### "+r.Title, snippet, "")
			outputSize += jsLen(snippet) + jsLen(r.Title)
		}
	}
	if scope == "global" {
		sections = append(sections, "\n> **Scope:** Queries searched the entire persistent index (query_scope: \"global\").")
	} else {
		sections = append(sections, "\n> **Tip:** Results are scoped to this batch only. To search across all indexed sources, use `ctx_search(queries: [...])` or call ctx_batch_execute with `query_scope: \"global\"`.")
	}
	return sections, nil
}

// jsLen is JS String.length (UTF-16 code units), used where TS sizes text.
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// jsPrefix is JS s.slice(0, n) in UTF-16 code units; a split surrogate pair
// is dropped rather than emitted as invalid UTF-8.
func jsPrefix(s string, n int) string {
	units := 0
	for i, r := range s {
		units += utf16.RuneLen(r)
		if units > n {
			return s[:i]
		}
	}
	return s
}
