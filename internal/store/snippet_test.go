package store

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDistinctiveTermsGolden(t *testing.T) {
	s := openTest(t)
	md := strings.Join([]string{
		"# A\nalpha_beta gamma delta shared_term common",
		"# B\nalpha_beta epsilon delta shared_term common",
		"# C\nzeta eta theta longidentifiername common",
		"# D\nzeta iota kappa longidentifiername common",
		"# E\nlambda iota kappa common",
	}, "\n")
	res, err := s.Index("p", "terms", ChunkMarkdown(md, ChunkMaxBytes))
	if err != nil || res.TotalChunks != 5 {
		t.Fatalf("index: %+v %v", res, err)
	}
	// Expected values from TS getDistinctiveTerms(sourceId) and (sourceId, 3).
	got, err := s.DistinctiveTerms(res.SourceID, 40)
	want := []string{"alpha_beta", "shared_term", "longidentifiername", "delta", "kappa", "zeta", "iota"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("DistinctiveTerms = %q, %v; want %q", got, err, want)
	}
	got, _ = s.DistinctiveTerms(res.SourceID, 3)
	if !reflect.DeepEqual(got, want[:3]) {
		t.Fatalf("DistinctiveTerms(3) = %q", got)
	}
	small, _ := s.Index("p", "small", []Chunk{{Title: "a", Content: "alpha"}, {Title: "b", Content: "alpha"}})
	if got, err := s.DistinctiveTerms(small.SourceID, 40); got != nil || err != nil {
		t.Fatalf("small source = %q %v", got, err)
	}
	if got, err := s.DistinctiveTerms(9999, 40); got != nil || err != nil {
		t.Fatalf("missing source = %q %v", got, err)
	}
}

func TestExtractSnippetGolden(t *testing.T) {
	filler := func(n int) string { return strings.Repeat("lorem ipsum ", (n+11)/12)[:n] }
	long := filler(1000) + "TARGET word here " + filler(1000) + "target again " + filler(500)
	hl := strings.Replace(long, "TARGET", "\x02TARGET\x03", 1)
	emoji := strings.Repeat("😀日本", 300) + " needle " + strings.Repeat("😀", 400)

	// Expected values from TS extractSnippet (server.ts) run under node:
	// UTF-16 length of the result and sha256(UTF-8)[:12].
	cases := []struct {
		name, content, query string
		max                  int
		hl                   string
		len16                int
		sum                  string
	}{
		{"short", "short content", "x", 1500, "", 13, "0e17930348d7"},
		{"highlight", long, "target", 1500, hl, 602, "3b4400a58604"},
		{"indexOf", long, "target ab", 1500, "", 1206, "466fa1d8e131"},
		{"nomatch", long, "zzz", 1500, "", 1502, "26478ec57fb3"},
		{"tight", long, "target", 400, "", 402, "441bca426745"},
		{"emoji", emoji, "needle", 500, "", 502, "92e02af35564"},
		{"edge", strings.Repeat("abcdefghij", 100), "abc", 50, "", 50, "cf833eb479a8"},
	}
	for _, c := range cases {
		out := ExtractSnippet(c.content, c.query, c.max, c.hl)
		h := sha256.Sum256([]byte(out))
		n := len(utf16.Encode([]rune(out)))
		if n != c.len16 || hex.EncodeToString(h[:])[:12] != c.sum {
			t.Errorf("%s: len16=%d sum=%x, want %d %s\n%.120q", c.name, n, h[:6], c.len16, c.sum, out)
		}
	}
	if out := ExtractSnippet(long, "zzz", 1500, ""); !strings.HasSuffix(out, "\n…") {
		t.Fatalf("no-match suffix: %q", out[len(out)-10:])
	}
}
