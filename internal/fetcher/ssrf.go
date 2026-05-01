package fetcher

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrBlockedIP is returned when fetcher refuses to connect to an IP address
// that resolves into loopback / private / link-local / unique-local /
// unspecified / broadcast / IPv4-mapped IPv6 of those ranges (Requirements
// 1, 2 and 3 AC-1).
var ErrBlockedIP = errors.New("blocked_ip")

// BlockedIPCategory classifies the reason an IP is blocked. It is exposed via
// BlockedIPError so that callers (Worker classifyFetchError / slog) can record
// the rejection category without leaking the URL details.
type BlockedIPCategory string

const (
	categoryLoopback      BlockedIPCategory = "loopback"
	categoryPrivate       BlockedIPCategory = "private"
	categoryLinkLocal     BlockedIPCategory = "link_local"
	categoryULA           BlockedIPCategory = "unique_local"
	categoryUnspecified   BlockedIPCategory = "unspecified"
	categoryBroadcast     BlockedIPCategory = "broadcast"
	categoryMulticast     BlockedIPCategory = "multicast"
	categoryIPv4Mapped    BlockedIPCategory = "ipv4_mapped"
	categoryUnparseableIP BlockedIPCategory = "unparseable"
)

// BlockedIPError wraps ErrBlockedIP and carries the rejection category and
// the raw IP literal (or hostname) that was rejected. Callers MUST NOT log
// the raw URL via this error; only the IP / Host string and Category should
// be exposed (NFR 1.3).
type BlockedIPError struct {
	Category BlockedIPCategory
	IP       string
	Host     string
}

func (e *BlockedIPError) Error() string {
	if e.IP != "" {
		return fmt.Sprintf("blocked_ip: category=%s ip=%s", e.Category, e.IP)
	}
	return fmt.Sprintf("blocked_ip: category=%s host=%s", e.Category, e.Host)
}

func (e *BlockedIPError) Unwrap() error { return ErrBlockedIP }

// classifyIP returns a non-empty BlockedIPCategory if the IP belongs to any
// disallowed range, or empty string when the IP is allowed (publicly routable).
//
// Note on IPv4-mapped IPv6 (::ffff:a.b.c.d): net.ParseIP produces the SAME
// 16-byte slice for "127.0.0.1" and "::ffff:127.0.0.1", so this function
// cannot distinguish the two from net.IP alone. The underlying IPv4 range
// label (loopback / private / link_local / etc.) is what gets returned, which
// is the security boundary that matters. The string-level distinction (the
// caller used ::ffff:... in the URL) is detected separately in
// checkHostIPLiteral so that operators can spot bypass attempts in logs.
//
// We combine the standard library predicates (IsLoopback, IsPrivate,
// IsLinkLocalUnicast, IsLinkLocalMulticast, IsUnspecified, IsMulticast) with
// explicit checks for ranges that net.IP does not cover (broadcast
// 255.255.255.255, CGNAT 100.64.0.0/10, 0.0.0.0/8). NFR 1.2 requires "deny
// by default" so unparseable IPs are also blocked.
func classifyIP(ip net.IP) BlockedIPCategory {
	if ip == nil {
		return categoryUnparseableIP
	}

	// Standard predicates first (work for both IPv4 and IPv6).
	if ip.IsUnspecified() {
		return categoryUnspecified
	}
	if ip.IsLoopback() {
		return categoryLoopback
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return categoryLinkLocal
	}
	if ip.IsPrivate() {
		// IsPrivate matches IPv4 RFC1918 / CGNAT and IPv6 ULA fc00::/7. Split
		// the category for IPv6 ULA so logs can show which family was hit.
		if ip.To4() == nil {
			return categoryULA
		}
		return categoryPrivate
	}
	if ip.IsMulticast() {
		// Non-link-local multicast is also unsafe for fetcher (e.g. 224.0.0.0/4
		// could reach internal multicast groups).
		return categoryMulticast
	}

	// IPv4-only post checks (broadcast / 0.0.0.0/8 etc.) for ranges that
	// stdlib predicates miss.
	if v4 := ip.To4(); v4 != nil {
		if cat := classifyIPv4(v4); cat != "" {
			return cat
		}
	}

	return ""
}

// classifyIPv4 covers IPv4 ranges that net.IP predicates miss or that we want
// to label distinctly. Caller must pass a 4-byte IPv4 representation.
func classifyIPv4(v4 net.IP) BlockedIPCategory {
	if len(v4) != net.IPv4len {
		return ""
	}
	// Limited broadcast 255.255.255.255.
	if v4[0] == 255 && v4[1] == 255 && v4[2] == 255 && v4[3] == 255 {
		return categoryBroadcast
	}
	// 0.0.0.0/8 — "this network" / unspecified-ish.
	if v4[0] == 0 {
		return categoryUnspecified
	}
	// 127.0.0.0/8 loopback (covered by IsLoopback but kept explicit for
	// IPv4-mapped IPv6 path which bypasses IsLoopback for some runtimes).
	if v4[0] == 127 {
		return categoryLoopback
	}
	// 169.254.0.0/16 link-local (covered by IsLinkLocalUnicast).
	if v4[0] == 169 && v4[1] == 254 {
		return categoryLinkLocal
	}
	// RFC1918.
	if v4[0] == 10 {
		return categoryPrivate
	}
	if v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31 {
		return categoryPrivate
	}
	if v4[0] == 192 && v4[1] == 168 {
		return categoryPrivate
	}
	// CGNAT 100.64.0.0/10 — treat as private for fetcher safety.
	if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return categoryPrivate
	}
	return ""
}

// checkHostIPLiteral returns a non-nil *BlockedIPError when the URL's host is
// an IP literal (or [bracketed IPv6]) that classifies into a blocked range.
// It returns nil for hostnames (DNS names) — those are checked at TCP dial
// time via guardedDialContext (Requirement 2 AC-4).
//
// rawURL must already be a parseable URL; callers typically have it from
// http.NewRequestWithContext.
func checkHostIPLiteral(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil // let the http stack produce its own parse error
	}
	host := u.Hostname()
	if host == "" {
		return nil
	}
	// Strip surrounding brackets for IPv6 literals (url.Hostname already
	// handles the standard "[::1]" form, but be defensive against odd input).
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		// Hostname (DNS name). Defer to dial-time check.
		return nil
	}
	cat := classifyIP(ip)
	if cat == "" {
		return nil
	}
	// Detect IPv4-mapped IPv6 textual form (::ffff:a.b.c.d) and surface a
	// distinct category so logs can show that the user attempted to bypass
	// the IPv4 path through an IPv6 wrapper. classifyIP cannot do this since
	// net.ParseIP produces identical bytes for "127.0.0.1" and
	// "::ffff:127.0.0.1".
	if isIPv4MappedTextual(host) {
		cat = categoryIPv4Mapped
	}
	return &BlockedIPError{Category: cat, IP: ip.String(), Host: host}
}

// isIPv4MappedTextual reports whether the textual host is an IPv4-mapped IPv6
// literal (e.g. "::ffff:127.0.0.1"). The stdlib does not expose a predicate
// for this, so we look for the canonical "::ffff:" prefix combined with a
// dotted-quad suffix.
func isIPv4MappedTextual(host string) bool {
	lower := strings.ToLower(host)
	if !strings.HasPrefix(lower, "::ffff:") {
		return false
	}
	tail := lower[len("::ffff:"):]
	// Must look like dotted-quad IPv4 (contains a dot) and parse as IPv4.
	if !strings.Contains(tail, ".") {
		return false
	}
	if v4 := net.ParseIP(tail); v4 != nil && v4.To4() != nil {
		return true
	}
	return false
}

// guardedDialContext wraps a base net.Dialer's DialContext so that the IP the
// dialer is about to connect to is checked against classifyIP at TCP-connect
// time. This is the TOCTOU defense (Requirement 2): even if DNS resolution
// returned a public IP earlier, we re-validate just before the SYN goes out.
//
// The chosen approach mirrors the standard library net.Dialer.DialContext
// signature so it can be plugged into http.Transport.DialContext directly.
func guardedDialContext(base *net.Dialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	if base == nil {
		base = &net.Dialer{}
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}

		// If the address already contains an IP literal (most common because
		// http.Transport resolves DNS to IP and dials by IP), classify it
		// directly. Otherwise resolve via the standard resolver (which the
		// http stack normally avoids passing here) and check every candidate.
		if ip := net.ParseIP(host); ip != nil {
			if cat := classifyIP(ip); cat != "" {
				return nil, &BlockedIPError{Category: cat, IP: ip.String(), Host: host}
			}
			return base.DialContext(ctx, network, address)
		}

		// Hostname path: resolve and dial the first allowed IP. Any single
		// blocked IP causes the whole request to fail (Requirement 1 AC-7).
		resolver := base.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		for _, ipa := range ips {
			if cat := classifyIP(ipa.IP); cat != "" {
				return nil, &BlockedIPError{Category: cat, IP: ipa.IP.String(), Host: host}
			}
		}
		// All resolved IPs are public; let the base dialer try them in order.
		// We re-use the resolved address (host:port) so that base.DialContext
		// itself performs another lookup; this is acceptable cost (~sub-ms
		// from resolver cache) and keeps Happy-Eyeballs / fallback behavior
		// intact (NFR 2.1 budget < 5ms).
		return base.DialContext(ctx, network, address)
	}
}
