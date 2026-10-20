package fetch

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestClassifyIP(t *testing.T) {
	cases := []struct {
		ip               string
		blocked, private bool
	}{
		// IPv4 always blocked
		{"169.254.169.254", true, false}, // IMDS
		{"169.254.0.1", true, false},
		{"0.0.0.0", true, false},
		{"0.255.255.255", true, false},
		{"224.0.0.1", true, false},
		{"239.255.255.250", true, false},
		{"240.0.0.1", true, false},
		{"255.255.255.255", true, false},
		// IPv4 private
		{"127.0.0.1", false, true},
		{"127.255.255.254", false, true},
		{"10.0.0.1", false, true},
		{"10.255.255.255", false, true},
		{"172.16.0.1", false, true},
		{"172.31.255.255", false, true},
		{"192.168.1.1", false, true},
		// IPv4 public (edges just outside the ranges)
		{"8.8.8.8", false, false},
		{"172.15.255.255", false, false},
		{"172.32.0.0", false, false},
		{"169.253.255.255", false, false},
		{"192.169.0.1", false, false},
		{"223.255.255.255", false, false},
		{"1.1.1.1", false, false},
		// IPv6 always blocked
		{"::", true, false},
		{"fe80::1", true, false},
		{"febf:ffff::1", true, false},
		{"ff02::1", true, false},
		{"ff00::", true, false},
		// IPv6 private
		{"::1", false, true},
		{"fc00::1", false, true},
		{"fd12:3456::1", false, true},
		// IPv6 public
		{"2001:4860:4860::8888", false, false},
		{"fec0::1", false, false}, // just past fe80::/10
		{"fe7f::1", false, false},
		{"fe8::1", false, false}, // 0fe8:: — TS string prefix check got this wrong
		// IPv4-mapped IPv6 uses the IPv4 rules
		{"::ffff:127.0.0.1", false, true},
		{"::ffff:169.254.169.254", true, false},
		{"::ffff:10.1.2.3", false, true},
		{"::ffff:8.8.8.8", false, false},
		{"::ffff:7f00:1", false, true}, // hex form of ::ffff:127.0.0.1
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("bad test IP %q", c.ip)
		}
		b, p := ClassifyIP(ip)
		if b != c.blocked || p != c.private {
			t.Errorf("ClassifyIP(%s) = (%v,%v), want (%v,%v)", c.ip, b, p, c.blocked, c.private)
		}
	}
	if b, _ := ClassifyIP(nil); !b {
		t.Error("nil IP must be blocked (malformed)")
	}
	if b, _ := ClassifyIP(net.IP{1, 2, 3}); !b {
		t.Error("3-byte IP must be blocked (malformed)")
	}
}

func dialErr(t *testing.T, strict bool, r *net.Resolver, addr string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialContext(strict, r)(ctx, "tcp", addr)
	if c != nil {
		c.Close()
	}
	return err
}

func TestDialerBlocksIMDSBeforeConnect(t *testing.T) {
	err := dialErr(t, false, nil, "169.254.169.254:80")
	var se *ssrfError
	if !errors.As(err, &se) {
		t.Fatalf("want ssrfError, got %v", err)
	}
	if se.Error() != "SSRF blocked at connect-time: 169.254.169.254 resolves to 169.254.169.254 (block)" {
		t.Fatalf("got %q", se.Error())
	}
}

func TestDialerLoopbackAllowedUnlessStrict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	if err := dialErr(t, false, nil, addr); err != nil {
		t.Fatalf("non-strict loopback dial failed: %v", err)
	}
	var se *ssrfError
	if err := dialErr(t, true, nil, addr); !errors.As(err, &se) || se.verdict != "private" {
		t.Fatalf("strict loopback dial: want private ssrfError, got %v", err)
	}
}

// fakeDNS is a net.Conn speaking DNS over a stream (2-byte length prefix,
// which Go's resolver uses for conns that are not net.PacketConn).
type fakeDNS struct {
	answer func(name string, t dnsmessage.Type) []net.IP
	out    bytes.Buffer
}

func (c *fakeDNS) Write(b []byte) (int, error) {
	var p dnsmessage.Parser
	h, err := p.Start(b[2:])
	if err != nil {
		return 0, err
	}
	q, err := p.Question()
	if err != nil {
		return 0, err
	}
	bld := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true})
	bld.StartQuestions()
	bld.Question(q)
	bld.StartAnswers()
	rh := dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET}
	for _, ip := range c.answer(q.Name.String(), q.Type) {
		if v4 := ip.To4(); v4 != nil && q.Type == dnsmessage.TypeA {
			bld.AResource(rh, dnsmessage.AResource{A: [4]byte(v4)})
		} else if v4 == nil && q.Type == dnsmessage.TypeAAAA {
			bld.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: [16]byte(ip)})
		}
	}
	msg, err := bld.Finish()
	if err != nil {
		return 0, err
	}
	c.out.Write([]byte{byte(len(msg) >> 8), byte(len(msg))})
	c.out.Write(msg)
	return len(b), nil
}
func (c *fakeDNS) Read(b []byte) (int, error)       { return c.out.Read(b) }
func (c *fakeDNS) Close() error                     { return nil }
func (c *fakeDNS) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *fakeDNS) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *fakeDNS) SetDeadline(time.Time) error      { return nil }
func (c *fakeDNS) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeDNS) SetWriteDeadline(time.Time) error { return nil }

// fakeResolver answers every lookup from answer (A and AAAA queries).
func fakeResolver(answer func(name string, t dnsmessage.Type) []net.IP) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return &fakeDNS{answer: answer}, nil
	}}
}

func TestDialerChecksResolvedIPNotHostname(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	r := fakeResolver(func(name string, ty dnsmessage.Type) []net.IP {
		if name == "evil.test." && ty == dnsmessage.TypeA {
			return []net.IP{net.ParseIP("127.0.0.1")}
		}
		return nil
	})
	addr := net.JoinHostPort("evil.test", u.Port())
	if err := dialErr(t, false, r, addr); err != nil {
		t.Fatalf("non-strict: evil.test -> 127.0.0.1 should connect, got %v", err)
	}
	err := dialErr(t, true, r, addr)
	var se *ssrfError
	if !errors.As(err, &se) {
		t.Fatalf("strict: want ssrfError, got %v", err)
	}
	if se.host != "evil.test" || se.ip != "127.0.0.1" || se.verdict != "private" {
		t.Fatalf("got %+v", se)
	}
}
