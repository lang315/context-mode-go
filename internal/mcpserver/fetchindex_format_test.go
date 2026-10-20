package mcpserver

import (
	"strings"
	"testing"
)

func TestFetchCacheKey(t *testing.T) {
	if got := fetchCacheKey("", false, "https://x/a"); got != "https://x/a" {
		t.Fatalf("no source: %q", got)
	}
	if got := fetchCacheKey("docs", true, "https://x/a"); got != "docs::https://x/a" {
		t.Fatalf("source: %q", got)
	}
	if got := fetchCacheKey("", true, "https://x/a"); got != "::https://x/a" {
		t.Fatalf("empty source is still a source in TS: %q", got)
	}
}

func TestFetchAgeAndTTL(t *testing.T) {
	ages := map[int64]string{0: "just now", 59_999: "just now", 60_000: "1m ago", 3_599_999: "59m ago", 3_600_000: "1h ago", 90 * 60_000: "1h ago", -5000: "just now"}
	for ms, want := range ages {
		if got := fetchAge(ms); got != want {
			t.Errorf("fetchAge(%d) = %q, want %q", ms, got, want)
		}
	}
	ttls := map[int64]string{0: "0ms", 86_400_000: "1d", 172_800_000: "2d", 7_200_000: "2h", 300_000: "5m", 1500: "1500ms", 90_000_000: "25h"}
	for ms, want := range ttls {
		if got := fetchTTL(ms); got != want {
			t.Errorf("fetchTTL(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestFetchPreview(t *testing.T) {
	short := strings.Repeat("a", fetchPreviewLimit)
	if fetchPreview(short) != short {
		t.Fatal("body at the limit must be kept whole")
	}
	long := strings.Repeat("é", fetchPreviewLimit+10)
	want := strings.Repeat("é", fetchPreviewLimit) + "\n\n…[truncated — use ctx_search() for full content]"
	if got := fetchPreview(long); got != want {
		t.Fatalf("truncated preview wrong: len %d", len(got))
	}
}

func TestFetchPoolSize(t *testing.T) {
	defer func(f func() int) { fetchNumCPU = f }(fetchNumCPU)
	fetchNumCPU = func() int { return 2 }
	cases := []struct {
		req, jobs int
		byCPU     bool
		eff       int
		capped    bool
	}{
		{1, 3, false, 1, false},
		{4, 3, true, 2, true},  // CPU cap
		{8, 3, false, 3, true}, // job-count clamp also reports capped (runPool.ts)
		{2, 5, true, 2, false},
		{3, 1, false, 1, true},
	}
	for _, c := range cases {
		eff, capped := fetchPoolSize(c.req, c.jobs, c.byCPU)
		if eff != c.eff || capped != c.capped {
			t.Errorf("fetchPoolSize(%d,%d,%v) = %d,%v want %d,%v", c.req, c.jobs, c.byCPU, eff, capped, c.eff, c.capped)
		}
	}
}

func TestFetchSingleText(t *testing.T) {
	cached, isErr := fetchSingleText(fetchOutcome{kind: "cached", label: "docs::u", chunks: 4, age: "2h ago", ttl: "1d"})
	wantCached := "Cached: **docs::u** — 4 sections, indexed 2h ago (fresh, TTL: 1d).\nTo refresh: call ctx_fetch_and_index again with `force: true`.\n\nYou MUST call ctx_search() to answer questions about this content — this cached response contains no content.\nUse: ctx_search(queries: [...], source: \"docs::u\")"
	if cached != wantCached || isErr {
		t.Fatalf("cached text:\n%s", cached)
	}
	fetched, isErr := fetchSingleText(fetchOutcome{kind: "fetched", label: "u", chunks: 2, bytes: 2048, preview: "# Hi"})
	wantFetched := "Fetched and indexed **2 sections** (2.0KB) from: u\nFull content indexed in sandbox — use ctx_search(queries: [...], source: \"u\") for specific lookups.\n\n---\n\n# Hi"
	if fetched != wantFetched || isErr {
		t.Fatalf("fetched text:\n%s", fetched)
	}
	errs := map[fetchOutcome]string{
		{kind: "error", url: "u", reason: "empty", err: "empty content"}: "Fetched u but got empty content",
		{kind: "error", url: "u", reason: "exit", err: "HTTP 404"}:       "Failed to fetch u: HTTP 404",
		{kind: "error", url: "u", reason: "throw", err: "disk full"}:     "Fetch error: disk full",
	}
	for o, want := range errs {
		if got, isErr := fetchSingleText(o); got != want || !isErr {
			t.Errorf("error text %q (isError %v), want %q", got, isErr, want)
		}
	}
}

func TestFetchBatchText(t *testing.T) {
	defer func(f func() int) { fetchNumCPU = f }(fetchNumCPU)
	fetchNumCPU = func() int { return 2 }
	long := strings.Repeat("abc ", 125) // 500 chars; char 384 is a space, so the cut is trimmed
	outs := []fetchOutcome{
		{kind: "fetched", label: "a", chunks: 1, bytes: 1024, preview: long},
		{kind: "cached", label: "b", chunks: 3, age: "just now", ttl: "1d"},
		{kind: "error", url: "http://c", err: "HTTP 404"},
	}
	got, isErr := fetchBatchText(outs, 2, true)
	want := "fetched 3 c=2 cap=2/2cpu. ok=1 cache=1 err=1. 1 section 1.0KB.\n\n" +
		"- [new]   a — 1 sections (1.0KB)\n" +
		"- [cache] b — 3 sections (just now, TTL: 1d)\n" +
		"- [err]   http://c: HTTP 404\n\n" +
		"ctx_search(queries: [...], source: \"<label>\") for full content.\n\n---\n\n" +
		"### a\n\n" + strings.TrimSpace(long[:384]) + "…"
	if got != want || isErr {
		t.Fatalf("batch text:\n%s\nwant:\n%s", got, want)
	}
	allErr, isErr := fetchBatchText(outs[2:], 1, false)
	if !isErr || !strings.HasPrefix(allErr, "fetched 1 c=1. ok=0 cache=0 err=1. 0 sections 0.0KB.\n") || strings.Contains(allErr, "---") {
		t.Fatalf("all-error batch (isError %v):\n%s", isErr, allErr)
	}
}
