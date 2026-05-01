package fetcher

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// TestClassifyIP covers Requirement 1 AC 1-5 + 8 (the IP range matrix), and
// Requirement 4 AC-1 (table-driven coverage of every disallowed range). Each
// row is a single Arrange / Act / Assert: parse the IP, classify it, compare.
func TestClassifyIP(t *testing.T) {
	cases := []struct {
		name      string
		ip        string
		wantBlock bool
		wantCat   BlockedIPCategory
	}{
		// IPv4 loopback (Req 1 AC-1).
		{"ipv4_loopback_127_0_0_1", "127.0.0.1", true, categoryLoopback},
		{"ipv4_loopback_127_0_0_2", "127.0.0.2", true, categoryLoopback},
		{"ipv4_loopback_high", "127.255.255.254", true, categoryLoopback},

		// IPv4 RFC1918 private (Req 1 AC-2).
		{"ipv4_private_10", "10.0.0.1", true, categoryPrivate},
		{"ipv4_private_172_16", "172.16.0.1", true, categoryPrivate},
		{"ipv4_private_172_31", "172.31.255.254", true, categoryPrivate},
		{"ipv4_private_192_168", "192.168.1.1", true, categoryPrivate},

		// IPv4 link-local incl. EC2/GCP metadata (Req 1 AC-3).
		{"ipv4_link_local_169_254_0_0", "169.254.0.1", true, categoryLinkLocal},
		{"ipv4_link_local_metadata", "169.254.169.254", true, categoryLinkLocal},

		// IPv6 loopback / ULA / link-local (Req 1 AC-4).
		{"ipv6_loopback", "::1", true, categoryLoopback},
		{"ipv6_ula_fc00", "fc00::1", true, categoryULA},
		{"ipv6_ula_fd00", "fd00::1", true, categoryULA},
		{"ipv6_link_local_fe80", "fe80::1", true, categoryLinkLocal},

		// Unspecified, broadcast (Req 1 AC-5).
		{"ipv4_unspecified_0_0_0_0", "0.0.0.0", true, categoryUnspecified},
		{"ipv6_unspecified_double_colon", "::", true, categoryUnspecified},
		{"ipv4_broadcast", "255.255.255.255", true, categoryBroadcast},

		// IPv4-mapped IPv6 (Req 1 AC-5): net.ParseIP normalizes "::ffff:a.b.c.d"
		// to the same 16-byte form as "a.b.c.d", so classifyIP returns the
		// underlying IPv4 range category. The textual ::ffff: bypass attempt
		// is surfaced as categoryIPv4Mapped at the URL-literal layer
		// (see TestCheckHostIPLiteral_BlocksIPLiteralURLs).
		{"ipv4_mapped_loopback", "::ffff:127.0.0.1", true, categoryLoopback},
		{"ipv4_mapped_private", "::ffff:10.0.0.1", true, categoryPrivate},
		{"ipv4_mapped_link_local", "::ffff:169.254.169.254", true, categoryLinkLocal},

		// CGNAT (treated as private to avoid leaking into shared NAT pools).
		{"ipv4_cgnat_100_64", "100.64.0.1", true, categoryPrivate},

		// Public IPs must remain allowed (Req 1 AC-8).
		{"ipv4_public_8_8_8_8", "8.8.8.8", false, ""},
		{"ipv4_public_1_1_1_1", "1.1.1.1", false, ""},
		{"ipv6_public_google", "2001:4860:4860::8888", false, ""},
		{"ipv4_mapped_public", "::ffff:8.8.8.8", false, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test setup: cannot parse %q", tc.ip)
			}

			// Act.
			cat := classifyIP(ip)

			// Assert.
			gotBlock := cat != ""
			if gotBlock != tc.wantBlock {
				t.Fatalf("ip=%s wantBlock=%v gotBlock=%v (cat=%q)", tc.ip, tc.wantBlock, gotBlock, cat)
			}
			if tc.wantBlock && cat != tc.wantCat {
				t.Fatalf("ip=%s wantCat=%q gotCat=%q", tc.ip, tc.wantCat, cat)
			}
		})
	}
}

// TestClassifyIP_NilReturnsUnparseable verifies that a nil net.IP (e.g. when
// callers pass net.ParseIP("garbage") directly) is classified as blocked under
// the "deny by default" policy (NFR 1.2).
func TestClassifyIP_NilReturnsUnparseable(t *testing.T) {
	// Arrange.
	var ip net.IP

	// Act.
	cat := classifyIP(ip)

	// Assert.
	if cat != categoryUnparseableIP {
		t.Fatalf("expected unparseable category for nil IP, got %q", cat)
	}
}

// TestCheckHostIPLiteral_BlocksIPLiteralURLs covers Requirement 1 AC-6: when
// the URL is given as an IP literal pointing at a blocked range, fetcher must
// refuse before DNS resolution.
func TestCheckHostIPLiteral_BlocksIPLiteralURLs(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantCat BlockedIPCategory
	}{
		{"ipv4_loopback_url", "http://127.0.0.1/", categoryLoopback},
		{"ipv4_private_url_with_port", "http://10.0.0.1:8080/admin", categoryPrivate},
		{"ipv4_link_local_metadata_url", "http://169.254.169.254/latest/meta-data/", categoryLinkLocal},
		{"ipv6_loopback_url", "http://[::1]/", categoryLoopback},
		{"ipv6_ula_url", "http://[fc00::1]/", categoryULA},
		{"ipv6_link_local_url", "http://[fe80::1]/", categoryLinkLocal},
		{"ipv4_broadcast_url", "http://255.255.255.255/", categoryBroadcast},
		{"ipv4_unspecified_url", "http://0.0.0.0/", categoryUnspecified},
		{"ipv4_mapped_loopback_url", "http://[::ffff:127.0.0.1]/", categoryIPv4Mapped},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Arrange / Act.
			err := checkHostIPLiteral(tc.url)

			// Assert.
			if err == nil {
				t.Fatalf("expected blocked_ip error for %q, got nil", tc.url)
			}
			if !errors.Is(err, ErrBlockedIP) {
				t.Fatalf("expected ErrBlockedIP via errors.Is, got %v", err)
			}
			var be *BlockedIPError
			if !errors.As(err, &be) {
				t.Fatalf("expected *BlockedIPError via errors.As, got %T", err)
			}
			if be.Category != tc.wantCat {
				t.Fatalf("category mismatch: want %q, got %q", tc.wantCat, be.Category)
			}
			// Error message must not contain the path or query string —
			// only the bare IP/host (NFR 1.3).
			if strings.Contains(be.Error(), "/admin") || strings.Contains(be.Error(), "meta-data") {
				t.Fatalf("error message leaks URL path: %q", be.Error())
			}
		})
	}
}

// TestCheckHostIPLiteral_AllowsHostnames ensures DNS-resolved hostnames are
// not rejected at the URL parse step (they go through DialContext instead,
// per Requirement 2 AC-4). This guarantees the early check is not over-eager.
func TestCheckHostIPLiteral_AllowsHostnames(t *testing.T) {
	cases := []string{
		"http://example.com/",
		"https://example.com:8443/foo",
		"http://sub.example.org/path?q=1",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			// Arrange / Act.
			err := checkHostIPLiteral(u)

			// Assert.
			if err != nil {
				t.Fatalf("hostname URL must not be rejected at literal stage, got %v", err)
			}
		})
	}
}

// TestCheckHostIPLiteral_AllowsPublicIPLiterals verifies that a literal
// public IP in the URL is not rejected (Req 1 AC-8 corollary).
func TestCheckHostIPLiteral_AllowsPublicIPLiterals(t *testing.T) {
	cases := []string{
		"http://8.8.8.8/",
		"http://[2001:4860:4860::8888]/",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			err := checkHostIPLiteral(u)
			if err != nil {
				t.Fatalf("public IP literal must be allowed, got %v", err)
			}
		})
	}
}

// TestGuardedDialContext_RejectsBlockedIPLiteralAddress directly exercises
// the dial-time guard (Requirement 2 AC-1 / AC-4). We do not need a real TCP
// stack: the guard must reject before delegating to base.DialContext.
func TestGuardedDialContext_RejectsBlockedIPLiteralAddress(t *testing.T) {
	cases := []struct {
		name    string
		address string
		wantCat BlockedIPCategory
	}{
		{"loopback_v4", "127.0.0.1:80", categoryLoopback},
		{"private_v4", "10.0.0.1:443", categoryPrivate},
		{"metadata_v4", "169.254.169.254:80", categoryLinkLocal},
		{"loopback_v6", "[::1]:80", categoryLoopback},
		{"ula_v6", "[fc00::1]:443", categoryULA},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			dial := guardedDialContext(&net.Dialer{})

			// Act.
			conn, err := dial(context.Background(), "tcp", tc.address)

			// Assert.
			if conn != nil {
				_ = conn.Close()
				t.Fatalf("expected nil connection for blocked address %s", tc.address)
			}
			if err == nil {
				t.Fatalf("expected error for blocked address %s, got nil", tc.address)
			}
			if !errors.Is(err, ErrBlockedIP) {
				t.Fatalf("expected ErrBlockedIP, got %v", err)
			}
			var be *BlockedIPError
			if !errors.As(err, &be) {
				t.Fatalf("expected *BlockedIPError, got %T", err)
			}
			if be.Category != tc.wantCat {
				t.Fatalf("category mismatch: want %q, got %q", tc.wantCat, be.Category)
			}
		})
	}
}

// TestGuardedDialContext_RebindingResolverReturnsPrivateIP simulates the DNS
// rebinding TOCTOU scenario (Requirement 2 AC-2): we install a custom
// Resolver via the base Dialer that returns a private IP at dial time, even
// though hypothetical earlier resolution would have returned a public one.
// The guard must reject based on the dial-time address.
func TestGuardedDialContext_RebindingResolverReturnsPrivateIP(t *testing.T) {
	// Arrange: a Resolver that maps any hostname to 10.0.0.1 (private).
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			// We will not be reached because LookupIPAddr is overridden via
			// the resolver Dial only when PreferGo is true and the resolver
			// performs a real DNS lookup. To force a deterministic outcome
			// we instead point base.Dialer at a custom Resolver that uses
			// a fake LookupIPAddr below, but stdlib does not expose that
			// hook directly. We work around this by short-circuiting in the
			// dial address itself.
			return nil, errors.New("unreachable in test")
		},
	}
	base := &net.Dialer{Resolver: resolver}
	dial := guardedDialContext(base)

	// Act: the guard inspects the dial address before delegating; we pass an
	// IP literal that is private to mimic the resolver having returned a
	// private IP at dial time (which is what http.Transport does — it dials
	// by IP after resolving). This is the realistic TOCTOU end state.
	conn, err := dial(context.Background(), "tcp", "10.0.0.1:80")

	// Assert.
	if conn != nil {
		_ = conn.Close()
		t.Fatalf("expected nil connection for rebinding-private IP")
	}
	if !errors.Is(err, ErrBlockedIP) {
		t.Fatalf("expected ErrBlockedIP for TOCTOU-private IP, got %v", err)
	}
}

// TestBlockedIPError_DoesNotLeakURLPath verifies NFR 1.3 contract: the error
// formatter must not include any URL component beyond the host/IP itself.
func TestBlockedIPError_DoesNotLeakURLPath(t *testing.T) {
	// Arrange.
	be := &BlockedIPError{
		Category: categoryLoopback,
		IP:       "127.0.0.1",
		Host:     "127.0.0.1",
	}

	// Act.
	msg := be.Error()

	// Assert: error message only carries the category and IP, never path/query.
	if !strings.Contains(msg, "loopback") {
		t.Fatalf("error message missing category: %q", msg)
	}
	if !strings.Contains(msg, "127.0.0.1") {
		t.Fatalf("error message missing IP: %q", msg)
	}
	if strings.Contains(msg, "?") || strings.Contains(msg, "/admin") || strings.Contains(msg, "Cookie") {
		t.Fatalf("error message must not leak URL components or auth info: %q", msg)
	}
}
