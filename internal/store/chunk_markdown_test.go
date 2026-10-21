package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// golden is one chunk as produced by the TS ContentStore (store.index /
// indexPlainText / indexJSON + getChunksBySource), captured with tsx.
// sum = first 12 hex chars of sha256(content).
type golden struct {
	title string
	code  bool
	bytes int
	sum   string
}

func checkChunks(t *testing.T, name string, got []Chunk, want []golden) {
	t.Helper()
	if len(got) != len(want) {
		for _, c := range got {
			t.Logf("%s got %q code=%v bytes=%d", name, c.Title, c.HasCode, len(c.Content))
		}
		t.Fatalf("%s: %d chunks, want %d", name, len(got), len(want))
	}
	for i, w := range want {
		h := sha256.Sum256([]byte(got[i].Content))
		g := golden{got[i].Title, got[i].HasCode, len(got[i].Content), hex.EncodeToString(h[:])[:12]}
		if g != w {
			t.Errorf("%s[%d] = %+v, want %+v\ncontent: %.200q", name, i, g, w, got[i].Content)
		}
	}
}

func TestJSStringHelpers(t *testing.T) {
	if got := hookio.JSTrim("\u00a0\ufeff x \u3000\n"); got != "x" {
		t.Fatalf("jsTrim = %q", got)
	}
	if hookio.JSTrim("\u0085x") != "\u0085x" { // NEL is not JS whitespace
		t.Fatal("jsTrim trimmed U+0085")
	}
	if n := u16len("aé日😀"); n != 5 {
		t.Fatalf("u16len = %d", n)
	}
	if got := u16prefix("ab😀c", 3); got != "ab\ufffd" {
		t.Fatalf("u16prefix cut pair = %q", got)
	}
	if got := u16prefix("ab😀c", 4); got != "ab😀" {
		t.Fatalf("u16prefix = %q", got)
	}
}

func TestChunkMarkdownGolden(t *testing.T) {
	md1 := strings.Join([]string{
		"Intro line", "", "# Title", "Some text", "## Sub A", "```js", "# not a heading", "const x = 1;", "```",
		"---", "More after rule", "### Deep", "deep text", "## Sub B", "b text", "#### Four", "##### five is not heading",
		"***", "# Second", "````", "```", "inner", "````", "tail",
	}, "\n")
	checkChunks(t, "md1", ChunkMarkdown(md1, ChunkMaxBytes), []golden{
		{"Untitled", false, 10, "81261b7be621"},
		{"Title", false, 17, "5a3e3b50b56b"},
		{"Title > Sub A", true, 47, "f19c8ed79033"},
		{"Title > Sub A", false, 15, "427234d2217c"},
		{"Title > Sub A > Deep", false, 18, "21a4868f8a0d"},
		{"Title > Sub B", false, 15, "9d5ec916364a"},
		{"Title > Sub B > Four", false, 35, "5c1e63f90082"},
		{"Second", true, 33, "25c04b09d2ad"},
	})

	r := strings.Repeat
	md2 := "# Big\n\n" + r("a", 1800) + "\n\n" + r("b", 1800) + "\n\n" + r("c", 1800) +
		"\n\n```\ncode " + r("d", 100) + "\n```\n\n" + r("e", 3000)
	checkChunks(t, "md2", ChunkMarkdown(md2, ChunkMaxBytes), []golden{
		{"Big (1)", false, 3609, "dcff9c475036"},
		{"Big (2)", true, 1915, "97a4334fa9ea"},
		{"Big (3)", false, 3000, "138988658ac3"},
	})

	// CRLF headings and fences are not recognised by the TS regexps.
	md3 := "# Win\r\nbody line\r\n```js\r\ncode\r\n```\r\n## Sub\nx\n```\nunclosed fence\n# inside"
	checkChunks(t, "md3", ChunkMarkdown(md3, ChunkMaxBytes), []golden{
		{"Untitled", true, 34, "20f18dde6e07"},
		{"Sub", true, 36, "a4dfaa83a444"},
	})

	md4 := "no heading at all\n\n" + r("z", 5000)
	checkChunks(t, "md4", ChunkMarkdown(md4, ChunkMaxBytes), []golden{
		{"Untitled (1)", false, 17, "e35a5aa97721"},
		{"Untitled (2)", false, 5000, "bc9d580ae187"},
	})

	if got := ChunkMarkdown(" \n\n---\n", ChunkMaxBytes); len(got) != 0 {
		t.Fatalf("blank input gave %d chunks", len(got))
	}
}
