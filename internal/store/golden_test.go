package store

// Golden parity tests (spec §5.2). Expected values come from the TS fork via
// tools/golden-gen/gen.mjs; differences allowed by design are listed with
// their case IDs in testdata/golden/KNOWN-DIFFS.md and in goldenKnown below.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const goldenDir = "../../testdata/golden"
const goldenCorpus = "../../testdata/corpus"

// goldenKnown maps a KNOWN-DIFFS.md case ID to its reason. A listed case is
// skipped; every other case must match TS exactly.
var goldenKnown = map[string]string{}

func loadGolden(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !strings.Contains(string(b), `"tsCommit": "a5e6dd31"`) {
		t.Fatalf("%s: not generated from TS commit a5e6dd31", name)
	}
}

// mustCases fails on an empty golden list, which would pass vacuously.
func mustCases(t *testing.T, name string, n int) {
	t.Helper()
	if n == 0 {
		t.Fatalf("%s: no golden cases", name)
	}
}

func skipKnown(t *testing.T, id string) bool {
	t.Helper()
	if why, ok := goldenKnown[id]; ok {
		t.Logf("%s: known diff (%s)", id, why)
		return true
	}
	return false
}

type goldenFile struct {
	Path, Kind string
	Chunks     []Chunk
	Terms      []string
}

// chunkCorpusFile runs the chunker the TS store would use for this kind.
func chunkCorpusFile(t *testing.T, path, kind string) []Chunk {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenCorpus, path))
	if err != nil {
		t.Fatal(err)
	}
	switch kind {
	case "markdown":
		return ChunkMarkdown(string(b), ChunkMaxBytes)
	case "plain":
		return ChunkPlainText(string(b), 20)
	case "json":
		c, err := ChunkJSON(string(b), ChunkMaxBytes)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return c
	}
	t.Fatalf("%s: unknown kind %q", path, kind)
	return nil
}

// goldenStore indexes the corpus in the generator's order, labels = paths.
func goldenStore(t *testing.T, files []goldenFile) (*Store, map[string]int64) {
	t.Helper()
	s := openTest(t)
	ids := map[string]int64{}
	for _, f := range files {
		r, err := s.Index("p", f.Path, chunkCorpusFile(t, f.Path, f.Kind))
		if err != nil {
			t.Fatal(err)
		}
		ids[f.Path] = r.SourceID
	}
	return s, ids
}

func TestGoldenChunks(t *testing.T) {
	var g struct{ Files []goldenFile }
	loadGolden(t, "chunks.json", &g)
	mustCases(t, "chunks.json", len(g.Files))
	for _, f := range g.Files {
		id := "chunks:" + f.Path
		if skipKnown(t, id) {
			continue
		}
		got := chunkCorpusFile(t, f.Path, f.Kind)
		if len(got) != len(f.Chunks) {
			t.Errorf("%s: %d chunks, want %d", id, len(got), len(f.Chunks))
			continue
		}
		for i, w := range f.Chunks {
			if got[i] != w {
				t.Errorf("%s[%d]:\n got %+v\nwant %+v", id, i, got[i], w)
			}
		}
	}
}

func TestGoldenDistinctiveTerms(t *testing.T) {
	var g struct{ Files []goldenFile }
	loadGolden(t, "chunks.json", &g)
	s, ids := goldenStore(t, g.Files)
	for _, f := range g.Files {
		id := "terms:" + f.Path
		if skipKnown(t, id) {
			continue
		}
		got, err := s.DistinctiveTerms(ids[f.Path], 40)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, f.Terms) { // nil and [] both mean none
			t.Errorf("%s:\n got %q\nwant %q", id, got, f.Terms)
		}
	}
}

func TestGoldenSanitize(t *testing.T) {
	var g struct {
		Cases []struct{ In, Porter, Trigram string }
	}
	loadGolden(t, "sanitize.json", &g)
	mustCases(t, "sanitize.json", len(g.Cases))
	for _, c := range g.Cases {
		id := "sanitize:" + c.In
		if skipKnown(t, id) {
			continue
		}
		if got := SanitizeQuery(c.In); got != c.Porter {
			t.Errorf("%s porter = %q, want %q", id, got, c.Porter)
		}
		if got := SanitizeTrigramQuery(c.In); got != c.Trigram {
			t.Errorf("%s trigram = %q, want %q", id, got, c.Trigram)
		}
	}
}

type goldenSearchCase struct {
	ID, Query, Source, ContentType, Mode string
	Limit                                int
	Results                              []struct{ Key, ContentType, Highlighted string }
}

func TestGoldenSearch(t *testing.T) {
	var g struct {
		Files []goldenFile
		Cases []goldenSearchCase
	}
	loadGolden(t, "search.json", &g)
	mustCases(t, "search.json", len(g.Cases))
	s, _ := goldenStore(t, g.Files)
	for _, c := range g.Cases {
		id := "search:" + c.ID
		if skipKnown(t, id) {
			continue
		}
		got, err := s.SearchWithFallback(c.Query, SearchOpts{
			Project: "p", Source: c.Source, SourceExact: c.Mode == "exact",
			ContentType: ContentType(c.ContentType), Limit: c.Limit, Mode: "relevance",
		})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		var gotKeys, wantKeys []string
		for _, r := range got {
			gotKeys = append(gotKeys, r.Source+"::"+r.Title)
		}
		for _, w := range c.Results {
			wantKeys = append(wantKeys, w.Key)
		}
		if !slices.Equal(gotKeys, wantKeys) {
			t.Errorf("%s %q:\n got %q\nwant %q", id, c.Query, gotKeys, wantKeys)
			continue
		}
		for i, w := range c.Results {
			hl := got[i].Highlighted
			if hl == "" { // Go drops marker-free highlights; TS keeps the plain content
				hl = got[i].Content
			}
			if string(got[i].ContentType) != w.ContentType || hl != w.Highlighted {
				t.Errorf("%s[%d] %s: contentType %s want %s; highlighted equal=%v",
					id, i, w.Key, got[i].ContentType, w.ContentType, hl == w.Highlighted)
			}
		}
	}
}

func TestGoldenSnippets(t *testing.T) {
	var g struct {
		Cases []struct {
			ID, Query, Content, Highlighted string
			Snippets                        map[string]string
		}
	}
	loadGolden(t, "snippets.json", &g)
	mustCases(t, "snippets.json", len(g.Cases))
	for _, c := range g.Cases {
		for maxStr, want := range c.Snippets {
			id := "snippet:" + c.ID + "@" + maxStr
			if skipKnown(t, id) {
				continue
			}
			max, err := strconv.Atoi(maxStr)
			if err != nil {
				t.Fatal(err)
			}
			if got := ExtractSnippet(c.Content, c.Query, max, c.Highlighted); got != want {
				t.Errorf("%s %q:\n got %q\nwant %q", id, c.Query, clip(got), clip(want))
			}
		}
	}
}

func clip(s string) string {
	if r := []rune(s); len(r) > 200 {
		return string(r[:100]) + " … " + string(r[len(r)-100:])
	}
	return strings.TrimSpace(s)
}
