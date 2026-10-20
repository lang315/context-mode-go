package mcpserver

import (
	"fmt"
	"strings"

	"github.com/lang315/context-mode-go/internal/store"
)

const (
	intentSearchThreshold = 5_000   // INTENT_SEARCH_THRESHOLD (S:1979)
	largeOutputThreshold  = 102_400 // LARGE_OUTPUT_THRESHOLD (S:1980)
)

// intentSearch indexes output as plain text under source, searches it for
// intent and returns section titles + first-line previews, not the content.
// Port of intentSearch (S:1982-2036).
func intentSearch(d *Deps, output, intent, source string) (string, error) {
	totalLines := strings.Count(output, "\n") + 1
	size := KB(len(output))
	indexed, err := d.Store.Index(d.Cfg.ProjectKey, source, store.ChunkPlainText(output, 20))
	if err != nil {
		return "", err
	}
	results, err := d.Store.SearchWithFallback(intent, store.SearchOpts{
		Project: d.Cfg.ProjectKey, Source: source, Limit: 5, Mode: "relevance",
	})
	if err != nil {
		return "", err
	}
	terms, err := d.Store.DistinctiveTerms(indexed.SourceID, 40)
	if err != nil {
		return "", err
	}

	lines := []string{fmt.Sprintf(`Indexed %d sections from "%s" into knowledge base.`, indexed.TotalChunks, source)}
	if len(results) == 0 {
		lines = append(lines, fmt.Sprintf(`No sections matched intent "%s" in %d-line output (%s).`, intent, totalLines, size))
	} else {
		lines = append(lines, fmt.Sprintf(`%d sections matched "%s" (%d lines, %s):`, len(results), intent, totalLines, size), "")
		for _, r := range results {
			first, _, _ := strings.Cut(r.Content, "\n")
			preview, _ := utf16Prefix(first, 120)
			lines = append(lines, "  - "+r.Title+": "+preview)
		}
	}
	if len(terms) > 0 {
		lines = append(lines, "", "Searchable terms: "+strings.Join(terms, ", "))
	}
	if len(results) == 0 {
		lines = append(lines, "", "Use ctx_search(queries: [...]) to explore the indexed content.")
	} else {
		lines = append(lines, "", "Use ctx_search(queries: [...]) to retrieve full content of any section.")
	}
	return strings.Join(lines, "\n"), nil
}

// indexStdout indexes large output with Markdown chunking and returns the
// pointer text. Port of indexStdout (S:1958-1973).
func indexStdout(d *Deps, output, source string) (string, error) {
	res, err := d.Store.Index(d.Cfg.ProjectKey, source, store.ChunkMarkdown(output, store.ChunkMaxBytes))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Indexed %d sections (%d with code) from: %s\nUse ctx_search(queries: [\"...\"]) to query this content. Use source: \"%s\" to scope results.",
		res.TotalChunks, res.CodeChunks, res.Label, res.Label), nil
}
