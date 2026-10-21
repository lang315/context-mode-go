package store

import (
	"fmt"
	"testing"
	"unicode/utf16"
)

func TestLevenshtein(t *testing.T) {
	u := func(s string) []uint16 { return utf16.Encode([]rune(s)) }
	for _, c := range []struct {
		a, b string
		want int
	}{{"", "abc", 3}, {"kitten", "sitting", 3}, {"conection", "connection", 1}, {"😀", "a", 2}} {
		if got := levenshtein(u(c.a), u(c.b)); got != c.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestFuzzyCorrectGolden(t *testing.T) {
	s := openTest(t)
	seedCorpus(t, s, "p")
	// Expected values from TS store.fuzzyCorrect on the same corpus.
	for _, c := range []struct{ in, want string }{
		{"conection", "connection"}, {"kubernets", "kubernetes"}, {"pool", ""},
		{"xy", ""}, {"zzzzzz", ""}, {"databse", "database"},
	} {
		got, err := s.fuzzyCorrect(c.in)
		if err != nil || got != c.want {
			t.Errorf("fuzzyCorrect(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestFuzzyCacheLRU(t *testing.T) {
	s := openTest(t)
	for i := range fuzzyCacheSize {
		s.fuzzyCorrect(fmt.Sprintf("word%03d", i))
	}
	s.fuzzyCorrect("word000") // promote: word001 is now oldest
	s.fuzzyCorrect("newword")
	if len(s.fuzzyKeys) != fuzzyCacheSize || len(s.fuzzyVals) != fuzzyCacheSize {
		t.Fatalf("cache size %d/%d", len(s.fuzzyKeys), len(s.fuzzyVals))
	}
	if _, ok := s.fuzzyVals["word001"]; ok {
		t.Fatal("oldest entry not evicted")
	}
	if _, ok := s.fuzzyVals["word000"]; !ok {
		t.Fatal("promoted entry evicted")
	}
	if s.fuzzyKeys[len(s.fuzzyKeys)-1] != "newword" {
		t.Fatalf("tail = %q", s.fuzzyKeys[len(s.fuzzyKeys)-1])
	}
}

func TestSearchWithFallbackGolden(t *testing.T) {
	s := openTest(t)
	seedCorpus(t, s, "p")
	search := func(q string) []SearchResult {
		t.Helper()
		res, err := s.SearchWithFallback(q, SearchOpts{Project: "p", Mode: "relevance"})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// Expected values from TS store.searchWithFallback(q, 3).
	checkHits(t, "connection pool", search("connection pool"), []hit{
		{"docA", "Connection Pool", Prose, -0.03278688524590164},
		{"log:run", "error: connection refused", Prose, -0.031746031746031744},
		{"docA", "Connection Pool > Timeout", Prose, -0.03225806451612903},
	})
	// Typos: first RRF pass finds nothing for "conection pol"; fuzzy retry does.
	checkHits(t, "conection pol", search("conection pol"), []hit{
		{"docA", "Connection Pool", Prose, -0.03278688524590164},
		{"log:run", "error: connection refused", Prose, -0.031746031746031744},
		{"docA", "Connection Pool > Timeout", Prose, -0.03225806451612903},
	})
	checkHits(t, "kubernets", search("kubernets"), []hit{
		{"docD", "Deploy", Prose, -0.01639344262295082},
	})
	if got := search("zzzz qqqq"); len(got) != 0 {
		t.Fatalf("zzzz qqqq: %d hits", len(got))
	}
	// Other projects are invisible.
	if res, _ := s.SearchWithFallback("connection", SearchOpts{Project: "nope"}); len(res) != 0 {
		t.Fatalf("project leak: %d", len(res))
	}
}

// Timeline = the relevance results, stable-sorted by IndexedAt ascending
// (searchAllSources in src/search/unified.ts).
func TestSearchTimeline(t *testing.T) {
	s := openTest(t)
	seedCorpus(t, s, "p")
	// Relevance order for "connection pool" is docA, log:run, docA (golden above).
	// Make log:run the oldest; both docA chunks share one indexed_at.
	// Every other source is older, so the newest three matches are these three.
	s.db.Exec("UPDATE sources SET indexed_at = '2025-12-31 00:00:00' WHERE label NOT IN ('log:run', 'docA')")
	s.db.Exec("UPDATE sources SET indexed_at = '2026-01-01 00:00:00' WHERE label = 'log:run'")
	s.db.Exec("UPDATE sources SET indexed_at = '2026-01-03 00:00:00' WHERE label = 'docA'")

	res, err := s.SearchWithFallback("connection pool", SearchOpts{Project: "p", Mode: "timeline"})
	if err != nil {
		t.Fatal(err)
	}
	// Both log:run chunks match and share one indexed_at; the later chunk is the newer.
	checkHits(t, "timeline", res, []hit{
		{"log:run", "warn: pool exhausted", Prose, -0.03125},
		{"docA", "Connection Pool", Prose, -0.03278688524590164}, // tie: relevance order kept
		{"docA", "Connection Pool > Timeout", Prose, -0.03225806451612903},
	})
	if got := res[0].IndexedAt.Format(sqliteTime); got != "2026-01-01 00:00:00" {
		t.Fatalf("oldest IndexedAt = %s", got)
	}
	// Fuzzy fallback also applies in timeline mode.
	res, _ = s.SearchWithFallback("conection pol", SearchOpts{Project: "p", Mode: "timeline"})
	if len(res) != 3 || res[0].Source != "log:run" {
		t.Fatalf("fuzzy timeline = %+v", res)
	}
	// Limit keeps the newest matches (spec §3.2); of the two docA chunks with
	// the same indexed_at the later one (higher id) is the newer.
	res, _ = s.SearchWithFallback("connection pool", SearchOpts{Project: "p", Mode: "timeline", Limit: 1})
	checkHits(t, "limit 1", res, []hit{{"docA", "Connection Pool > Timeout", Prose, -0.03225806451612903}})
	if res, err := s.SearchWithFallback("zzzz qqqq", SearchOpts{Project: "p", Mode: "timeline"}); err != nil || len(res) != 0 {
		t.Fatalf("non-matching timeline = %+v, %v", res, err)
	}
}

// Timeline takes the newest matches, not the best ranked ones: with three
// matches and limit 2 the lowest-ranked chunk is kept because it is newest,
// and the output stays oldest first.
func TestSearchTimelineKeepsNewestMatches(t *testing.T) {
	s := openTest(t)
	for _, c := range []struct {
		label, content, at string
	}{
		{"strong", "gizmo gizmo gizmo gizmo", "2026-01-01 00:00:00"},
		{"middle", "gizmo gizmo notes", "2026-01-02 00:00:00"},
		{"weak", "a long unrelated paragraph about many other things and only once the word gizmo", "2026-01-03 00:00:00"},
	} {
		if _, err := s.Index("p", c.label, []Chunk{{Title: c.label, Content: c.content}}); err != nil {
			t.Fatal(err)
		}
		s.db.Exec("UPDATE sources SET indexed_at = ? WHERE label = ?", c.at, c.label)
	}
	relevance, _ := s.SearchWithFallback("gizmo", SearchOpts{Project: "p", Limit: 3})
	if len(relevance) != 3 || relevance[2].Source != "weak" {
		t.Fatalf("setup: weak chunk must rank last, got %+v", relevance)
	}
	res, err := s.SearchWithFallback("gizmo", SearchOpts{Project: "p", Mode: "timeline", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Source != "middle" || res[1].Source != "weak" {
		t.Fatalf("timeline limit 2 = %+v, want middle then weak", res)
	}
}
