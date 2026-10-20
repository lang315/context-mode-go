package mcpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lang315/context-mode-go/internal/fetch"
	"github.com/lang315/context-mode-go/internal/store"
)

// fetchSite serves fixed pages on loopback and counts hits per path.
type fetchSite struct {
	*httptest.Server
	hits map[string]*atomic.Int32
}

func newFetchSite(t *testing.T) *fetchSite {
	t.Helper()
	pages := map[string][2]string{ // path -> {content type, body}
		"/page":      {"text/html; charset=utf-8", "<html><head><title>T</title><script>x()</script></head><body><h1>Alpha Guide</h1><p>The zebrafish protocol.</p></body></html>"},
		"/data.json": {"application/json", `{"alpha":{"beta":"zebrafish"},"gamma":[1,2]}`},
		"/bad.json":  {"application/json", `{not json zebrafish`},
		"/plain":     {"text/plain", "line one zebrafish\nline two"},
		"/empty":     {"text/plain", "  \n "},
	}
	s := &fetchSite{hits: map[string]*atomic.Int32{}}
	for p := range pages {
		s.hits[p] = &atomic.Int32{}
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pg, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.hits[r.URL.Path].Add(1)
		w.Header().Set("Content-Type", pg[0])
		fmt.Fprint(w, pg[1])
	}))
	t.Cleanup(s.Close)
	return s
}

// skewedHarness returns a harness whose clock is time.Now()+*skew.
func skewedHarness(t *testing.T) (*harness, *time.Duration) {
	skew := new(time.Duration)
	return newHarness(t, func() time.Time { return time.Now().Add(*skew) }), skew
}

func searchSource(t *testing.T, h *harness, q, label string) []store.SearchResult {
	t.Helper()
	res, err := h.Deps.Store.SearchWithFallback(q, store.SearchOpts{Project: h.Deps.Cfg.ProjectKey, Source: label, SourceExact: true, Limit: 3, Mode: "relevance"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFetchAndIndexSingleHTML(t *testing.T) {
	site := newFetchSite(t)
	h, _ := skewedHarness(t)
	u := site.URL + "/page"
	text, isErr := h.call("ctx_fetch_and_index", fmt.Sprintf(`{"url": %q}`, u))
	want := "Fetched and indexed **1 sections** (0.0KB) from: " + u + "\n" +
		"Full content indexed in sandbox — use ctx_search(queries: [...], source: \"" + u + "\") for specific lookups.\n\n---\n\n" +
		"# Alpha Guide\n\nThe zebrafish protocol."
	if isErr || text != want {
		t.Fatalf("isError=%v text:\n%s", isErr, text)
	}
	res := searchSource(t, h, "zebrafish", u)
	if len(res) == 0 || res[0].Title != "Alpha Guide" || res[0].Source != u {
		t.Fatalf("not searchable: %+v", res)
	}
}

func TestFetchAndIndexCacheTTLForce(t *testing.T) {
	site := newFetchSite(t)
	h, skew := skewedHarness(t)
	u := site.URL + "/page"
	call := func(extra string) string {
		t.Helper()
		text, isErr := h.call("ctx_fetch_and_index", fmt.Sprintf(`{"url": %q%s}`, u, extra))
		if isErr {
			t.Fatalf("unexpected error: %s", text)
		}
		return text
	}
	call("")

	got := call("")
	want := "Cached: **" + u + "** — 1 sections, indexed just now (fresh, TTL: 1d).\nTo refresh: call ctx_fetch_and_index again with `force: true`.\n\nYou MUST call ctx_search() to answer questions about this content — this cached response contains no content.\nUse: ctx_search(queries: [...], source: \"" + u + "\")"
	if got != want {
		t.Fatalf("cached text:\n%s", got)
	}
	*skew = 90 * time.Minute
	if got := call(""); !strings.HasPrefix(got, "Cached: **"+u+"** — 1 sections, indexed 1h ago (fresh, TTL: 1d).") {
		t.Fatalf("aged cache text:\n%s", got)
	}
	if got := call(`, "ttl": "7200000"`); !strings.Contains(got, "indexed 1h ago (fresh, TTL: 2h)") {
		t.Fatalf("ttl override text:\n%s", got)
	}
	if n := site.hits["/page"].Load(); n != 1 {
		t.Fatalf("cache hits must not fetch; hits=%d", n)
	}

	for i, extra := range []string{`, "force": true`, `, "ttl": 0`, `, "ttl": 3600000`, `, "force": "true"`} {
		if got := call(extra); !strings.HasPrefix(got, "Fetched and indexed **1 sections**") {
			t.Fatalf("%s must refetch:\n%s", extra, got)
		}
		if n := site.hits["/page"].Load(); n != int32(i+2) {
			t.Fatalf("%s: hits=%d", extra, n)
		}
		*skew = 90 * time.Minute // a refetch restamps the source; keep it 90m old for the stale-ttl case
	}
}

func TestFetchAndIndexSourceLabel(t *testing.T) {
	site := newFetchSite(t)
	h, _ := skewedHarness(t)
	u := site.URL + "/page"
	text, _ := h.call("ctx_fetch_and_index", fmt.Sprintf(`{"url": %q, "source": "src"}`, u))
	if !strings.HasPrefix(text, "Fetched and indexed **1 sections** (0.0KB) from: src::"+u+"\n") {
		t.Fatalf("text:\n%s", text)
	}
	if src, err := h.Deps.Store.SourceByLabel(h.Deps.Cfg.ProjectKey, "src::"+u); err != nil || src == nil {
		t.Fatalf("source src::url missing: %v", err)
	}
	if text, _ := h.call("ctx_fetch_and_index", fmt.Sprintf(`{"url": %q, "source": "src"}`, u)); !strings.HasPrefix(text, "Cached: **src::"+u+"**") {
		t.Fatalf("second call must hit the src::url key:\n%s", text)
	}
}

func TestFetchAndIndexContentRouting(t *testing.T) {
	site := newFetchSite(t)
	h, _ := skewedHarness(t)
	cases := []struct{ path, title, preview string }{
		{"/data.json", "alpha", "{\n  \"alpha\": {\n    \"beta\": \"zebrafish\"\n  },\n  \"gamma\": [\n    1,\n    2\n  ]\n}"},
		{"/bad.json", "Output", "{not json zebrafish"},
		{"/plain", "Output", "line one zebrafish\nline two"},
	}
	for _, c := range cases {
		u := site.URL + c.path
		text, isErr := h.call("ctx_fetch_and_index", fmt.Sprintf(`{"url": %q}`, u))
		if isErr || !strings.HasSuffix(text, "\n\n---\n\n"+c.preview) {
			t.Fatalf("%s: isError=%v text:\n%s", c.path, isErr, text)
		}
		res := searchSource(t, h, "zebrafish", u)
		if len(res) == 0 || res[0].Title != c.title {
			t.Fatalf("%s: want chunk titled %q, got %+v", c.path, c.title, res)
		}
	}
}

func TestFetchAndIndexSingleErrors(t *testing.T) {
	site := newFetchSite(t)
	h, _ := skewedHarness(t)
	cases := map[string]string{
		site.URL + "/missing": "Failed to fetch " + site.URL + "/missing: HTTP 404",
		site.URL + "/empty":   "Fetched " + site.URL + "/empty but got empty content",
		"ftp://x/y":           `Failed to fetch ftp://x/y: URL scheme "ftp:" not allowed (only http: and https:)`,
	}
	for u, want := range cases {
		if text, isErr := h.call("ctx_fetch_and_index", fmt.Sprintf(`{"url": %q}`, u)); !isErr || text != want {
			t.Errorf("%s: isError=%v text %q, want %q", u, isErr, text, want)
		}
	}
}

func TestFetchAndIndexArgs(t *testing.T) {
	h, _ := skewedHarness(t)
	if text, isErr := h.call("ctx_fetch_and_index", `{}`); !isErr || text != "ctx_fetch_and_index requires either `url` (single) or `requests: [{url, source?}, ...]` (batch)." {
		t.Fatalf("missing url: %v %q", isErr, text)
	}
	for _, args := range []string{`{"requests": []}`, `{"url": "http://x", "concurrency": 9}`, `{"url": "http://x", "ttl": -1}`, `{"url": "http://x", "ttl": 1.5}`, `{"url": "http://x", "force": "yes"}`, `{"requests": [{"source": "s"}]}`} {
		if text, isErr := h.call("ctx_fetch_and_index", args); !isErr || !strings.HasPrefix(text, "Invalid arguments: ") {
			t.Errorf("%s: want arg error, got %v %q", args, isErr, text)
		}
	}
}

func TestFetchAndIndexBatch(t *testing.T) {
	site := newFetchSite(t)
	h, _ := skewedHarness(t)
	page, plain, missing := site.URL+"/page", site.URL+"/plain", site.URL+"/missing"
	h.call("ctx_fetch_and_index", fmt.Sprintf(`{"url": %q}`, plain)) // warm the cache for one entry
	args := fmt.Sprintf(`{"requests": [{"url": %q, "source": "docs"}, {"url": %q}, {"url": %q}], "concurrency": 2}`, page, plain, missing)
	text, isErr := h.call("ctx_fetch_and_index", args)
	eff := min(2, runtime.NumCPU())
	capNote := ""
	if eff < 2 {
		capNote = fmt.Sprintf(" cap=%d/%dcpu", eff, runtime.NumCPU())
	}
	want := fmt.Sprintf("fetched 3 c=%d%s. ok=1 cache=1 err=1. 1 section 0.0KB.\n\n", eff, capNote) +
		"- [new]   docs::" + page + " — 1 sections (0.0KB)\n" +
		"- [cache] " + plain + " — 1 sections (just now, TTL: 1d)\n" +
		"- [err]   " + missing + ": HTTP 404\n\n" +
		"ctx_search(queries: [...], source: \"<label>\") for full content.\n\n---\n\n" +
		"### docs::" + page + "\n\n# Alpha Guide\n\nThe zebrafish protocol."
	if isErr || text != want {
		t.Fatalf("isError=%v text:\n%s", isErr, text)
	}

	// One entry in requests still gets the batch shape.
	text, isErr = h.call("ctx_fetch_and_index", fmt.Sprintf(`{"requests": [{"url": %q}]}`, missing))
	if !isErr || text != "fetched 1 c=1. ok=0 cache=0 err=1. 0 sections 0.0KB.\n\n- [err]   "+missing+": HTTP 404\n\nctx_search(queries: [...], source: \"<label>\") for full content." {
		t.Fatalf("all-failing batch: isError=%v text:\n%s", isErr, text)
	}
}

func TestFetchAndIndexConcurrencyCap(t *testing.T) {
	site := newFetchSite(t)
	h, _ := skewedHarness(t)
	reqs := fmt.Sprintf(`[{"url": %q}, {"url": %q}, {"url": %q}]`, site.URL+"/page", site.URL+"/plain", site.URL+"/data.json")
	header := regexp.MustCompile(`^fetched 3 c=(\d+)( cap=\d+/\d+cpu)?\. `)

	defer func(f func() int) { fetchNumCPU = f }(fetchNumCPU)
	fetchNumCPU = func() int { return 2 } // the schema max (8) is below this machine's CPU count, so pin it
	text, _ := h.call("ctx_fetch_and_index", fmt.Sprintf(`{"requests": %s, "concurrency": 4, "force": true}`, reqs))
	if m := header.FindStringSubmatch(text); m == nil || m[1] != "2" || m[2] != " cap=2/2cpu" {
		t.Fatalf("CPU cap header:\n%s", text)
	}

	fetchNumCPU = runtime.NumCPU
	text, _ = h.call("ctx_fetch_and_index", fmt.Sprintf(`{"requests": %s, "concurrency": "8", "force": true}`, reqs))
	eff := min(8, runtime.NumCPU(), 3) // runPool also reports the job-count clamp as capped
	if m := header.FindStringSubmatch(text); m == nil || m[1] != fmt.Sprint(eff) || m[2] != fmt.Sprintf(" cap=%d/%dcpu", eff, runtime.NumCPU()) {
		t.Fatalf("job clamp header:\n%s", text)
	}
}

func TestFetchAndIndexStrictSSRF(t *testing.T) {
	site := newFetchSite(t)
	h, _ := skewedHarness(t)
	h.Deps.Fetch = fetch.New(true)
	u := site.URL + "/page"
	text, isErr := h.call("ctx_fetch_and_index", fmt.Sprintf(`{"url": %q}`, u))
	if !isErr || text != "Failed to fetch "+u+`: URL "127.0.0.1" resolves to private IP 127.0.0.1 — blocked under CMGO_FETCH_STRICT=1` {
		t.Fatalf("isError=%v text:\n%s", isErr, text)
	}
	if n := site.hits["/page"].Load(); n != 0 {
		t.Fatalf("strict mode must not reach the server; hits=%d", n)
	}
}
