package fetch

// Golden parity test for HTML → Markdown (spec §5.2). html.json holds the
// Turndown output from tools/golden-gen/gen.mjs. Turndown and
// html-to-markdown v2 differ on some markup; each difference has an ID in
// testdata/golden/KNOWN-DIFFS.md and a normalizer below that is applied to
// both outputs of exactly the cases listed for it. Anything else must match.

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	reTitle         = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	reListMarker    = regexp.MustCompile(`(?m)^[ \t]*([*]|\d+\.)[ \t]+`)
	reBlankLine     = regexp.MustCompile(`(?m)^[ \t]+\n`)
	reDelimRow      = regexp.MustCompile(`(?m)^\|(?: ?-{3,} ?\|)+$`)
	reDelimCell     = regexp.MustCompile(` ?-{3,} ?`)
	reHTMLTable     = regexp.MustCompile(`(?s)<table>.*?</table>`)
	reEmptyHeadTbl  = regexp.MustCompile(`(?m)^\|(?:  \|)+\n\|(?:---\|)+(?:\n\|.*\|)+`)
	entityUnescaper = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")
)

// htmlNormalizers: KNOWN-DIFFS.md ID → normalizer(html, markdown).
var htmlNormalizers = map[string]func(html, md string) string{
	// Turndown keeps the <head><title> text as a first line; html-to-markdown drops <head>.
	"html-title": func(html, md string) string {
		if m := reTitle.FindStringSubmatch(html); m != nil {
			t := strings.TrimSpace(m[1])
			md = strings.TrimLeft(md, " \t")
			if strings.HasPrefix(md, t) {
				md = strings.TrimLeft(strings.TrimPrefix(md, t), " \t\n")
			}
		}
		return md
	},
	// Turndown gfm writes ~x~, html-to-markdown writes ~~x~~.
	"html-strike": func(_, md string) string { return strings.ReplaceAll(md, "~~", "~") },
	// Turndown pads list markers to 4 columns ("*   x", "1.  x") and indents
	// nested lists by 4; html-to-markdown uses "* x", 2-space nesting and a
	// whitespace-only line before a nested list.
	"html-list": func(_, md string) string {
		return reListMarker.ReplaceAllString(reBlankLine.ReplaceAllString(md, ""), "$1 ")
	},
	// Turndown decodes entities and escapes * and ]; html-to-markdown keeps
	// &lt; &gt; &amp; and escapes only [.
	"html-escape": func(_, md string) string {
		md = entityUnescaper.Replace(md)
		return strings.NewReplacer(`\*`, `*`, `\]`, `]`).Replace(md)
	},
	// Table delimiter row: Turndown "| --- |", html-to-markdown "|---|".
	"html-table-delim": func(_, md string) string {
		return reDelimRow.ReplaceAllStringFunc(md, func(row string) string { return reDelimCell.ReplaceAllString(row, "---") })
	},
	// A table without <th>: Turndown keeps the raw HTML, html-to-markdown
	// emits a table with an empty header row. Run after html-table-delim.
	"html-table-headless": func(_, md string) string {
		return reEmptyHeadTbl.ReplaceAllString(reHTMLTable.ReplaceAllString(md, "<TABLE>"), "<TABLE>")
	},
}

// htmlKnown: case ID → KNOWN-DIFFS.md IDs, applied in this order.
var htmlKnown = map[string][]string{
	"html:html/strip.html": {"html-title", "html-strike", "html-list", "html-escape"},
	"html:html/table.html": {"html-title", "html-list", "html-table-delim", "html-table-headless"},
}

func TestGoldenHTMLToMarkdown(t *testing.T) {
	b, err := os.ReadFile("../../testdata/golden/html.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		TSCommit string
		Cases    []struct{ Path, Markdown string }
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if g.TSCommit != "a5e6dd31" || len(g.Cases) == 0 {
		t.Fatalf("html.json: commit %q, %d cases", g.TSCommit, len(g.Cases))
	}
	for _, c := range g.Cases {
		id := "html:" + c.Path
		html, err := os.ReadFile("../../testdata/corpus/" + c.Path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := HTMLToMarkdown(string(html))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		known := htmlKnown[id]
		if slices.Contains(known, "html-table-headless") { // the normalizer hides the cells; check them raw
			for _, row := range []string{"| alpha | 1 |", "| beta | 2 |"} {
				if !strings.Contains(got, row) {
					t.Errorf("%s: header-less table row %q missing:\n%s", id, row, got)
				}
			}
		}
		normalize := func(skip string) (string, string) {
			g, w := got, c.Markdown
			for _, k := range known {
				if k != skip {
					g, w = htmlNormalizers[k](string(html), g), htmlNormalizers[k](string(html), w)
				}
			}
			return g, w
		}
		if g, w := normalize(""); g != w {
			t.Errorf("%s (normalized by %v):\n--- got\n%s\n--- want\n%s", id, known, g, w)
			continue
		}
		for _, k := range known { // every listed diff must still be needed
			if g, w := normalize(k); g == w {
				t.Errorf("%s: %s no longer differs; drop it from KNOWN-DIFFS.md and htmlKnown", id, k)
			}
		}
	}
}
