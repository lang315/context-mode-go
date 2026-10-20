// Package fetch fetches a URL in-process with an SSRF guard at dial time and
// converts the body to indexable text (Markdown, pretty JSON or plain text).
package fetch

import (
	"context"
	"net"
	"strings"
	"syscall"
)

// ClassifyIP reports whether ip is always blocked (link-local/IMDS, 0/8,
// multicast/reserved, ::, fe80::/10, ff00::/8, malformed) or private
// (loopback, RFC1918, ::1, fc00::/7; blocked only in strict mode).
// Port of classifyIp, S:3244-3286. IPv4-mapped IPv6 goes through the IPv4 rules.
func ClassifyIP(ip net.IP) (blocked, private bool) {
	if v4 := ip.To4(); v4 != nil {
		a, b := v4[0], v4[1]
		switch {
		case a == 169 && b == 254, a == 0, a >= 224:
			return true, false
		case a == 127, a == 10, a == 172 && b >= 16 && b <= 31, a == 192 && b == 168:
			return false, true
		}
		return false, false
	}
	if len(ip) != net.IPv6len {
		return true, false // malformed
	}
	switch {
	case ip.IsUnspecified(), ip[0] == 0xfe && ip[1]&0xc0 == 0x80, ip[0] == 0xff:
		return true, false
	case ip.IsLoopback(), ip[0]&0xfe == 0xfc:
		return false, true
	}
	return false, false
}

// ssrfError is returned by the dialer's Control hook; Fetch formats it with
// the TS wording for the hop it happened on.
type ssrfError struct {
	host, ip, verdict string // verdict: "block" | "private"
}

func (e *ssrfError) Error() string {
	// TS connect-time wording (S:2915-2919).
	return "SSRF blocked at connect-time: " + e.host + " resolves to " + e.ip + " (" + e.verdict + ")"
}

// dialContext returns a DialContext that remembers the pre-resolution host and
// rejects, in Control, every address actually dialed that ClassifyIP blocks.
// resolver nil means the default resolver (tests inject a fake one).
func dialContext(strict bool, resolver *net.Resolver) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		d := net.Dialer{
			Resolver: resolver,
			Control: func(_, address string, _ syscall.RawConn) error {
				ipStr, _, err := net.SplitHostPort(address)
				if err != nil {
					ipStr = address
				}
				ipStr, _, _ = strings.Cut(ipStr, "%") // drop IPv6 zone before classifying (TS does too)
				blocked, private := ClassifyIP(net.ParseIP(ipStr))
				if blocked {
					return &ssrfError{host, ipStr, "block"}
				}
				if private && strict {
					return &ssrfError{host, ipStr, "private"}
				}
				return nil
			},
		}
		return d.DialContext(ctx, network, addr)
	}
}
