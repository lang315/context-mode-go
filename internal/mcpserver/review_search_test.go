package mcpserver

import (
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/store"
)

// Review focus: models paste raw text into ctx_search — FTS5 operators,
// unbalanced quotes, column filters, only stopwords. None may surface an SQL
// error; each query gets a normal section.
func TestSearchSurvivesFTSSyntax(t *testing.T) {
	h := newHarness(t, nil)
	h.index("notes", store.Chunk{Title: "Alpha", Content: "the gizmo handles NEAR misses and col:value pairs"})
	queries := []string{`"gizmo`, `gizmo*`, `NEAR(gizmo misses)`, `title:gizmo`, `the and of`, `^gizmo`, `gizmo AND OR NOT`, `(((`, `'; DROP TABLE sources; --`}
	for i, q := range queries {
		if i > 0 && i%3 == 0 {
			h = newHarness(t, nil) // stay under the flood-guard soft cap per harness
			h.index("notes", store.Chunk{Title: "Alpha", Content: "the gizmo handles NEAR misses and col:value pairs"})
		}
		out, isErr := h.call("ctx_search", `{"queries": [`+jsonString(q)+`]}`)
		if isErr || strings.Contains(strings.ToLower(out), "sql") || strings.Contains(out, "fts5") {
			t.Errorf("query %q: isError=%v\n%s", q, isErr, out)
		}
		if !strings.HasPrefix(out, "## "+q) {
			t.Errorf("query %q: missing section header\n%s", q, out)
		}
	}
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
