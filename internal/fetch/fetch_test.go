package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func testFetcher(strict bool) *Fetcher {
	f := New(strict)
	f.Timeout = 5 * time.Second
	return f
}

// serve starts a server answering every request with status, content type and body.
func serve(t *testing.T, status int, ct, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func mustFetch(t *testing.T, f *Fetcher, u string) Result {
	t.Helper()
	res, err := f.Fetch(context.Background(), u)
	if err != nil {
		t.Fatalf("Fetch(%s): %v", u, err)
	}
	return res
}

func wantErr(t *testing.T, f *Fetcher, u, want string) {
	t.Helper()
	_, err := f.Fetch(context.Background(), u)
	if err == nil || err.Error() != want {
		t.Fatalf("Fetch(%s) error = %v, want %q", u, err, want)
	}
}

func TestFetchHTML(t *testing.T) {
	res := mustFetch(t, testFetcher(false), serve(t, 200, "text/html; charset=utf-8", testPage))
	if res.Kind != KindHTML {
		t.Fatalf("kind = %s", res.Kind)
	}
	if !strings.Contains(res.Body, "| Name | Value |\n|---|---|\n| alpha | 1 |") {
		t.Errorf("no GFM table in:\n%s", res.Body)
	}
	for _, gone := range []string{"SCRIPTBODY", "NAVLINK"} {
		if strings.Contains(res.Body, gone) {
			t.Errorf("%q not removed:\n%s", gone, res.Body)
		}
	}
	res = mustFetch(t, testFetcher(false), serve(t, 200, "application/xhtml+xml", "<p>x<b>y</b></p>"))
	if res.Kind != KindHTML || res.Body != "x**y**" {
		t.Errorf("xhtml: %+v", res)
	}
}

func TestFetchJSONPrettyPrintsKeepingKeyOrder(t *testing.T) {
	res := mustFetch(t, testFetcher(false), serve(t, 200, "application/json", `{"b":1,"a":[1,"x"]}`))
	want := "{\n  \"b\": 1,\n  \"a\": [\n    1,\n    \"x\"\n  ]\n}"
	if res.Kind != KindJSON || res.Body != want {
		t.Fatalf("got %+v", res)
	}
	res = mustFetch(t, testFetcher(false), serve(t, 200, "application/problem+json", `{"title":"t"}`))
	if res.Kind != KindJSON || res.Body != "{\n  \"title\": \"t\"\n}" {
		t.Fatalf("+json: %+v", res)
	}
}

func TestFetchInvalidJSONIsText(t *testing.T) {
	res := mustFetch(t, testFetcher(false), serve(t, 200, "application/json", "{not json"))
	if res.Kind != KindText || res.Body != "{not json" {
		t.Fatalf("got %+v", res)
	}
}

func TestFetchPlainTextTrimmed(t *testing.T) {
	res := mustFetch(t, testFetcher(false), serve(t, 200, "text/plain", "\uFEFF\n  line one\nline two \n\n"))
	if res.Kind != KindText || res.Body != "line one\nline two" {
		t.Fatalf("got %+v", res)
	}
}

func TestFetchErrors(t *testing.T) {
	f := testFetcher(false)
	wantErr(t, f, serve(t, 404, "text/plain", "nope"), "HTTP 404")
	wantErr(t, f, serve(t, 302, "", ""), "HTTP 302") // 3xx without Location is final
	wantErr(t, f, serve(t, 200, "text/html", "<script>x()</script>  "), "empty content")
	wantErr(t, f, serve(t, 200, "text/plain", " \n "), "empty content")
}

func TestFetchRejectsBadURLs(t *testing.T) {
	f := testFetcher(false)
	wantErr(t, f, "file:///etc/passwd", `URL scheme "file:" not allowed (only http: and https:)`)
	wantErr(t, f, "javascript:alert(1)", `URL scheme "javascript:" not allowed (only http: and https:)`)
	wantErr(t, f, "not a url", "invalid URL")
	wantErr(t, f, "http://", "invalid URL")
	wantErr(t, f, "http://169.254.169.254/latest/meta-data/",
		`URL "169.254.169.254" resolves to 169.254.169.254 — blocked (link-local / IMDS / multicast / reserved)`)
}

func TestFetchStrictBlocksLoopback(t *testing.T) {
	u := serve(t, 200, "text/plain", "ok")
	if res := mustFetch(t, testFetcher(false), u); res.Body != "ok" {
		t.Fatalf("non-strict: %+v", res)
	}
	wantErr(t, testFetcher(true), u, `URL "127.0.0.1" resolves to private IP 127.0.0.1 — blocked under CMGO_FETCH_STRICT=1`)
}

// redirector serves /r/N -> /r/N-1 ... /r/0 = "done".
func redirector(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/r/"))
		if n == 0 {
			fmt.Fprint(w, "done")
			return
		}
		http.Redirect(w, r, "/r/"+strconv.Itoa(n-1), http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFetchRedirectLimit(t *testing.T) {
	base := redirector(t)
	f := testFetcher(false)
	if res := mustFetch(t, f, base+"/r/5"); res.Body != "done" {
		t.Fatalf("5 hops: %+v", res)
	}
	wantErr(t, f, base+"/r/6", "SSRF blocked: redirect chain exceeded 5 hops")
}

func TestFetchRedirectTargetsRevalidated(t *testing.T) {
	to := func(loc string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", loc)
			w.WriteHeader(http.StatusMovedPermanently)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	f := testFetcher(false)
	wantErr(t, f, to("file:///etc/passwd"), "SSRF blocked: redirect to non-http(s) scheme file:")
	wantErr(t, f, to("http://169.254.169.254/latest/meta-data/"), "SSRF blocked: redirect to 169.254.169.254 (block)")
	wantErr(t, f, to("http://[fe80::1]/"), "SSRF blocked: redirect to fe80::1 (block)")
	wantErr(t, f, to("http://%zz/"), "SSRF blocked: invalid redirect Location: http://%zz/")
}

func TestFetchBodyCap(t *testing.T) {
	f := testFetcher(false)
	f.MaxBody = 10
	chunked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.(http.Flusher).Flush() // no Content-Length: forces the LimitReader path
		fmt.Fprint(w, strings.Repeat("x", 100))
	}))
	defer chunked.Close()
	wantErr(t, f, chunked.URL, "Response too large: 11 bytes exceeds 10")

	withCL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "20")
		fmt.Fprint(w, strings.Repeat("y", 20))
	}))
	defer withCL.Close()
	wantErr(t, f, withCL.URL, "Response too large: Content-Length 20 exceeds 10")

	if res := mustFetch(t, f, serve(t, 200, "text/plain", "0123456789")); res.Body != "0123456789" {
		t.Fatalf("exactly at cap: %+v", res)
	}
}

// The name answers 127.0.0.1 on the first lookup and 169.254.169.254 after
// (DNS rebinding). The redirect back to the same name is dialed again
// (Connection: close) and the dialed IP, not the name, is rejected.
func TestFetchDNSRebindingCheckedAtDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		fmt.Fprint(w, "reached the rebound address")
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	var lookups atomic.Int32
	f := testFetcher(false)
	f.resolver = fakeResolver(func(name string, ty dnsmessage.Type) []net.IP {
		if name != "rebind.test." || ty != dnsmessage.TypeA {
			return nil
		}
		if lookups.Add(1) == 1 {
			return []net.IP{net.ParseIP("127.0.0.1")}
		}
		return []net.IP{net.ParseIP("169.254.169.254")}
	})
	wantErr(t, f, fmt.Sprintf("http://rebind.test:%d/", port),
		"SSRF blocked: redirect target rebind.test resolves to 169.254.169.254 (block)")
	if n := lookups.Load(); n != 2 {
		t.Fatalf("A lookups = %d, want 2 (one per dial)", n)
	}
}

func TestFetchDNSErrors(t *testing.T) {
	f := testFetcher(false)
	f.resolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "udp", Err: errors.New("network is down")}
	}}
	_, err := f.Fetch(context.Background(), "http://down.test/")
	if err == nil || !strings.HasPrefix(err.Error(), `DNS lookup failed for "down.test": `) || !strings.HasSuffix(err.Error(), dnsRetryHint) {
		t.Fatalf("transient DNS: %v", err)
	}

	f.resolver = fakeResolver(func(string, dnsmessage.Type) []net.IP { return nil })
	_, err = f.Fetch(context.Background(), "http://nxdomain.test/")
	if err == nil || !strings.HasPrefix(err.Error(), `DNS lookup failed for "nxdomain.test": `) || strings.Contains(err.Error(), "retry once") {
		t.Fatalf("not-found DNS: %v", err)
	}
}

func TestFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	f := testFetcher(false)
	f.Timeout = 100 * time.Millisecond
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("error should not repeat the URL: %v", err)
	}
}
