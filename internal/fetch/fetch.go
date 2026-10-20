package fetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Kind is the content route chosen from Content-Type (TS "__CM_CT__:<kind>").
type Kind string

const (
	KindJSON Kind = "json"
	KindHTML Kind = "html"
	KindText Kind = "text"
)

// Result is a fetched body ready for indexing: HTML already converted to
// Markdown, JSON pretty-printed (Kind text if it does not parse), trimmed.
type Result struct {
	Body string
	Kind Kind
}

type Fetcher struct {
	Strict       bool
	Timeout      time.Duration
	MaxBody      int64
	MaxRedirects int

	resolver *net.Resolver // nil = default resolver; tests inject a fake DNS
}

// ErrEmpty is returned when the fetched body is empty after trimming.
var ErrEmpty = errors.New("empty content")

func New(strict bool) *Fetcher {
	return &Fetcher{Strict: strict, Timeout: 30 * time.Second, MaxBody: 50 << 20, MaxRedirects: 5}
}

// dnsRetryHint is appended to transient DNS/network errors (S:3222-3223, S:3334-3335).
const dnsRetryHint = " — transient DNS error; retry once before falling back. If it keeps failing, the MCP host may be running under a network sandbox; restart the host with network access enabled."

// Fetch GETs rawURL and routes the body by Content-Type.
// Port of ssrfGuard (S:3168-3233), fetchOneUrl (S:3288-3370) and the
// subprocess main() (S:3079-3112), in-process.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return Result{}, errors.New("invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Result{}, fmt.Errorf("URL scheme %q not allowed (only http: and https:)", u.Scheme+":")
	}
	if u.Host == "" {
		return Result{}, errors.New("invalid URL")
	}

	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // a proxy would resolve the name itself and bypass the dial-time check
	tr.DialContext = dialContext(f.Strict, f.resolver)
	defer tr.CloseIdleConnections()

	resp, hop, err := f.follow(ctx, tr, u)
	if err != nil {
		return Result{}, describe(err, hop)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Result{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// safeText, S:3067-3077.
	if resp.ContentLength > f.MaxBody {
		return Result{}, fmt.Errorf("Response too large: Content-Length %d exceeds %d", resp.ContentLength, f.MaxBody)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBody+1))
	if err != nil {
		return Result{}, describe(err, hop)
	}
	if int64(len(b)) > f.MaxBody {
		return Result{}, fmt.Errorf("Response too large: %d bytes exceeds %d", len(b), f.MaxBody)
	}
	// resp.text() decodes UTF-8, dropping a BOM and replacing invalid bytes.
	text := strings.ToValidUTF8(strings.TrimPrefix(string(b), "\uFEFF"), "\uFFFD")

	ct := resp.Header.Get("Content-Type") // TS matches substrings case-sensitively; so do we
	var res Result
	switch {
	case strings.Contains(ct, "application/json") || strings.Contains(ct, "+json"):
		var buf bytes.Buffer
		if json.Indent(&buf, []byte(text), "", "  ") == nil {
			res = Result{buf.String(), KindJSON}
		} else {
			res = Result{text, KindText}
		}
	case strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml"):
		md, err := HTMLToMarkdown(text)
		if err != nil {
			return Result{}, err
		}
		res = Result{md, KindHTML}
	default:
		res = Result{text, KindText}
	}
	res.Body = strings.TrimSpace(res.Body)
	if res.Body == "" {
		return Result{}, ErrEmpty
	}
	return res, nil
}

// follow walks redirects by hand so every hop is dialed (and IP-checked) anew.
// Port of fetchWithManualRedirect, S:3013-3058. Returns the hop index that
// produced the response or the error. It uses RoundTrip, not http.Client,
// because the client rejects an unparseable Location before we can.
func (f *Fetcher) follow(ctx context.Context, rt http.RoundTripper, u *url.URL) (*http.Response, int, error) {
	for hop := 0; ; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, hop, err
		}
		resp, err := rt.RoundTrip(req)
		if err != nil {
			return nil, hop, err
		}
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" {
			return resp, hop, nil
		}
		resp.Body.Close()
		if hop == f.MaxRedirects {
			return nil, hop, fmt.Errorf("SSRF blocked: redirect chain exceeded %d hops", f.MaxRedirects)
		}
		next, err := u.Parse(loc)
		if err != nil {
			return nil, hop, errors.New("SSRF blocked: invalid redirect Location: " + loc)
		}
		if next.Scheme != "http" && next.Scheme != "https" {
			return nil, hop, errors.New("SSRF blocked: redirect to non-http(s) scheme " + next.Scheme + ":")
		}
		u = next
	}
}

// describe turns a transport error into the text TS shows the model.
func describe(err error, hop int) error {
	var se *ssrfError
	if errors.As(err, &se) {
		switch {
		case hop > 0 && net.ParseIP(se.host) != nil: // S:3038
			return fmt.Errorf("SSRF blocked: redirect to %s (%s)", se.host, se.verdict)
		case hop > 0: // S:3049-3050
			return fmt.Errorf("SSRF blocked: redirect target %s resolves to %s (%s)", se.host, se.ip, se.verdict)
		case se.verdict == "block": // S:3198
			return fmt.Errorf("URL %q resolves to %s — blocked (link-local / IMDS / multicast / reserved)", se.host, se.ip)
		default: // S:3206; names the env var cmgo actually reads
			return fmt.Errorf("URL %q resolves to private IP %s — blocked under CMGO_FETCH_STRICT=1", se.host, se.ip)
		}
	}
	var de *net.DNSError
	if errors.As(err, &de) { // S:3210-3230
		msg := fmt.Sprintf("DNS lookup failed for %q: %s", de.Name, de.Error())
		if de.IsTimeout || de.IsTemporary {
			msg += dnsRetryHint
		}
		return errors.New(msg)
	}
	msg := err.Error()
	if errors.Is(err, syscall.ETIMEDOUT) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EPERM) { // S:3333
		msg += dnsRetryHint
	}
	return errors.New(msg)
}
