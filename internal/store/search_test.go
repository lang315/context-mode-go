package store

import (
	"testing"
)

// hit is a TS searchWithFallback result: source, title, contentType, rank.
type hit struct {
	source, title string
	ct            ContentType
	rank          float64
}

// seedCorpus indexes the corpus used by the TS probe (store.index for the
// docs, store.indexPlainText for the log).
func seedCorpus(t *testing.T, s *Store, project string) {
	t.Helper()
	docs := [][2]string{
		{"docA", "# Connection Pool\nThe connection pool manages database connections for the app.\n## Timeout\nSet the pool timeout to 30s so idle connection slots close."},
		{"docB", "# HTTP Server\nThe server handles connection upgrades and websocket frames.\n```go\nfunc handleConnection(c net.Conn) { pool.Get() }\n```"},
		{"docC", "# Notes\nRandom notes about caching layers and pool sizing.\n## Caching\nThe caching layer sits in front of the database pool."},
		{"docD", "# Deploy\nKubernetes deployment rollout strategy.\n## Rollback\nRollback the deployment when the connection check fails."},
	}
	for _, d := range docs {
		if _, err := s.Index(project, d[0], ChunkMarkdown(d[1], ChunkMaxBytes)); err != nil {
			t.Fatal(err)
		}
	}
	log := "error: connection refused\nretrying connection pool\n\nwarn: pool exhausted\n\nfatal: giving up"
	if _, err := s.Index(project, "log:run", ChunkPlainText(log, 20)); err != nil {
		t.Fatal(err)
	}
}

func checkHits(t *testing.T, name string, got []SearchResult, want []hit) {
	t.Helper()
	if len(got) != len(want) {
		for _, r := range got {
			t.Logf("%s got %q %q %s %v", name, r.Source, r.Title, r.ContentType, r.Rank)
		}
		t.Fatalf("%s: %d hits, want %d", name, len(got), len(want))
	}
	for i, w := range want {
		g := hit{got[i].Source, got[i].Title, got[i].ContentType, got[i].Rank}
		if g != w {
			t.Errorf("%s[%d] = %+v, want %+v", name, i, g, w)
		}
	}
}

func TestSanitizeGolden(t *testing.T) {
	// Expected values from TS sanitizeQuery(q, "OR") / sanitizeTrigramQuery(q, "OR").
	cases := []struct{ in, porter, trigram string }{
		{"connection pool", "\"connection\" OR \"pool\"", "\"connection\" OR \"pool\""},
		{"the AND or", "\"the\"", "\"the\" OR \"AND\""},
		{"Error error ERROR", "\"Error\"", "\"Error\""},
		{"foo:bar (baz) \"quoted\" *star*", "\"foo\" OR \"bar\" OR \"baz\" OR \"quoted\" OR \"star\"", "\"foobar\" OR \"baz\" OR \"quoted\" OR \"star\""},
		{"a b", "\"a\" OR \"b\"", ""},
		{"x", "\"x\"", ""},
		{"update the tests", "\"update\" OR \"the\" OR \"tests\"", "\"update\" OR \"the\" OR \"tests\""},
		{"  near NEAR not  ", "\"\"", "\"near\""},
		{"ab abc abcd", "\"ab\" OR \"abc\" OR \"abcd\"", "\"abc\" OR \"abcd\""},
		{"café's naïve", "\"café\" OR \"s\" OR \"naïve\"", "\"cafés\" OR \"naïve\""},
	}
	for _, c := range cases {
		if got := SanitizeQuery(c.in); got != c.porter {
			t.Errorf("SanitizeQuery(%q) = %q, want %q", c.in, got, c.porter)
		}
		if got := SanitizeTrigramQuery(c.in); got != c.trigram {
			t.Errorf("SanitizeTrigramQuery(%q) = %q, want %q", c.in, got, c.trigram)
		}
	}
}

func TestRRFRerankGolden(t *testing.T) {
	s := openTest(t)
	seedCorpus(t, s, "p")
	seedCorpus(t, s, "other") // must not leak into project p
	search := func(q string, o SearchOpts) []SearchResult {
		t.Helper()
		o.Project = "p"
		if o.Limit == 0 {
			o.Limit = 3
		}
		res, err := s.rrfSearch(q, o.Limit, o)
		if err != nil {
			t.Fatal(err)
		}
		return rerank(res, q)
	}
	// Expected values from TS store.searchWithFallback (relevance path).
	checkHits(t, "connection pool", search("connection pool", SearchOpts{}), []hit{
		{"docA", "Connection Pool", Prose, -0.03278688524590164},
		{"log:run", "error: connection refused", Prose, -0.031746031746031744},
		{"docA", "Connection Pool > Timeout", Prose, -0.03225806451612903},
	})
	checkHits(t, "websocket", search("websocket", SearchOpts{}), []hit{
		{"docB", "HTTP Server", Code, -0.03278688524590164},
	})
	checkHits(t, "handleConnection", search("handleConnection", SearchOpts{}), []hit{
		{"docB", "HTTP Server", Code, -0.03278688524590164},
	})
	checkHits(t, "pool timeout", search("pool timeout", SearchOpts{}), []hit{
		{"docA", "Connection Pool > Timeout", Prose, -0.03278688524590164},
		{"log:run", "warn: pool exhausted", Prose, -0.03225806451612903},
		{"docA", "Connection Pool", Prose, -0.031746031746031744},
	})
	checkHits(t, "caching layer", search("caching layer", SearchOpts{}), []hit{
		{"docC", "Notes > Caching", Prose, -0.03278688524590164},
		{"docC", "Notes", Prose, -0.03225806451612903},
	})
	checkHits(t, "the pool", search("the pool", SearchOpts{}), []hit{
		{"log:run", "warn: pool exhausted", Prose, -0.03278688524590164},
		{"docA", "Connection Pool", Prose, -0.03225806451612903},
		{"docA", "Connection Pool > Timeout", Prose, -0.031746031746031744},
	})
	checkHits(t, "deployment rollback", search("deployment rollback", SearchOpts{}), []hit{
		{"docD", "Deploy > Rollback", Prose, -0.03278688524590164},
		{"docD", "Deploy", Prose, -0.03225806451612903},
	})
	checkHits(t, "connection", search("connection", SearchOpts{}), []hit{
		{"log:run", "error: connection refused", Prose, -0.03278688524590164},
		{"docA", "Connection Pool", Prose, -0.03225806451612903},
		{"docA", "Connection Pool > Timeout", Prose, -0.031746031746031744},
	})
	checkHits(t, "source like", search("connection", SearchOpts{Source: "doc"}), []hit{
		{"docA", "Connection Pool", Prose, -0.03278688524590164},
		{"docA", "Connection Pool > Timeout", Prose, -0.03225806451612903},
		{"docD", "Deploy > Rollback", Prose, -0.03149801587301587},
	})
	checkHits(t, "source exact", search("connection", SearchOpts{Source: "docB", SourceExact: true}), []hit{
		{"docB", "HTTP Server", Code, -0.03278688524590164},
	})
	checkHits(t, "code only", search("connection", SearchOpts{ContentType: Code}), []hit{
		{"docB", "HTTP Server", Code, -0.03278688524590164},
	})
	checkHits(t, "pool limit 5", search("pool", SearchOpts{Limit: 5}), []hit{
		{"log:run", "warn: pool exhausted", Prose, -0.03278688524590164},
		{"docA", "Connection Pool", Prose, -0.03225806451612903},
		{"docA", "Connection Pool > Timeout", Prose, -0.031746031746031744},
		{"log:run", "error: connection refused", Prose, -0.031009615384615385},
		{"docC", "Notes", Prose, -0.031009615384615385},
	})
	if got := search("zzzz qqqq", SearchOpts{}); len(got) != 0 {
		t.Fatalf("zzzz qqqq: %d hits", len(got))
	}
	// LIKE metacharacters are literal: "doc_" matches nothing.
	if got := search("connection", SearchOpts{Source: "doc_"}); len(got) != 0 {
		t.Fatalf("doc_ matched %d", len(got))
	}
	// Empty porter query ("") must not error.
	if _, err := s.rrfSearch("near not", 3, SearchOpts{Project: "p"}); err != nil {
		t.Fatal(err)
	}

	res := search("websocket frames", SearchOpts{Limit: 1})
	want := "# HTTP Server\nThe server handles connection upgrades and \x02websocket\x03 \x02frames\x03.\n```go\nfunc handleConnection(c net.Conn) { pool.Get() }\n```"
	if len(res) != 1 || res[0].Highlighted != want {
		t.Fatalf("highlighted = %+v", res)
	}
	if res[0].IndexedAt.IsZero() {
		t.Fatal("IndexedAt not set")
	}
}

func TestRerankHelpers(t *testing.T) {
	if got := findMinSpan([][]int{{0, 50}, {10, 60}, {55}}); got != 10 {
		t.Fatalf("findMinSpan = %d", got)
	}
	// "foo foo bar": the single "bar" pairs with one "foo" only.
	if got := countAdjacentPairs([][]int{{0, 4}, {8}}, []string{"foo", "bar"}, 30); got != 1 {
		t.Fatalf("countAdjacentPairs = %d", got)
	}
}
