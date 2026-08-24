// Package web implements the guarded web fetch and search tools (S-066): an
// SSRF-guarded HTTP client with DNS pinning and post-connect address
// re-verification, bounded HTML-to-text extraction, a content-addressed
// response cache, and a search provider behind a configured API key.
//
// The policy in this file is the load-bearing part, ported from pi research's
// network guard: parse and classify every address, apply the class policy to
// every resolved answer (one blocked answer fails the whole target), pin the
// resolved set, and re-verify the address a socket actually connected to.
package web

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// ipClass is the address taxonomy the guard recognizes. Everything except
// classPublic is blocked by default; classMetadata (cloud credential
// endpoints) is never allowed, even when private networks are.
type ipClass string

const (
	classPublic        ipClass = "public"
	classLoopback      ipClass = "loopback"
	classPrivate       ipClass = "private"
	classLinkLocal     ipClass = "link-local"
	classCGNAT         ipClass = "cgnat"
	classDocumentation ipClass = "documentation"
	classBenchmark     ipClass = "benchmark"
	classMulticast     ipClass = "multicast"
	classUnspecified   ipClass = "unspecified"
	classReserved      ipClass = "reserved"
	classMetadata      ipClass = "metadata"
)

func mustPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// Metadata endpoints are classified before their enclosing link-local and
// CGNAT ranges, so a broad allow-private toggle can never reach a cloud
// credential service.
var metadataPrefixes4 = []netip.Prefix{
	mustPrefix("169.254.169.254/32"), // IMDS
	mustPrefix("169.254.170.0/24"),   // ECS/EKS task credentials
	mustPrefix("100.100.100.200/32"), // Alibaba metadata
}

func classifyIPv4(a netip.Addr) ipClass {
	for _, p := range metadataPrefixes4 {
		if p.Contains(a) {
			return classMetadata
		}
	}
	switch {
	case a == netip.MustParseAddr("0.0.0.0"):
		return classUnspecified
	case mustPrefix("127.0.0.0/8").Contains(a):
		return classLoopback
	case mustPrefix("10.0.0.0/8").Contains(a),
		mustPrefix("172.16.0.0/12").Contains(a),
		mustPrefix("192.168.0.0/16").Contains(a):
		return classPrivate
	case mustPrefix("169.254.0.0/16").Contains(a):
		return classLinkLocal
	case mustPrefix("100.64.0.0/10").Contains(a):
		return classCGNAT
	case mustPrefix("192.0.2.0/24").Contains(a),
		mustPrefix("198.51.100.0/24").Contains(a),
		mustPrefix("203.0.113.0/24").Contains(a):
		return classDocumentation
	case mustPrefix("198.18.0.0/15").Contains(a):
		return classBenchmark
	case mustPrefix("224.0.0.0/4").Contains(a):
		return classMulticast
	case mustPrefix("240.0.0.0/4").Contains(a),
		mustPrefix("0.0.0.0/8").Contains(a),
		mustPrefix("192.0.0.0/24").Contains(a),
		mustPrefix("192.88.99.0/24").Contains(a):
		return classReserved
	}
	return classPublic
}

// embeddedIPv4 returns the IPv4 destination a translated or tunneled IPv6
// address actually reaches, so `64:ff9b::a9fe:a9fe` is judged as the metadata
// service and not as an unremarkable public IPv6 address. IPv4-mapped
// (::ffff:a.b.c.d) is unwrapped by the caller via Unmap; this handles
// IPv4-compatible (::a.b.c.d), IPv4-translated (::ffff:0:a.b.c.d, RFC 6052),
// the NAT64 well-known prefix (64:ff9b::/96), and 6to4 (2002::/16).
func embeddedIPv4(a netip.Addr) (netip.Addr, bool) {
	b := a.As16()
	allZero := func(from, to int) bool {
		for i := from; i < to; i++ {
			if b[i] != 0 {
				return false
			}
		}
		return true
	}
	tail := func(from int) netip.Addr {
		return netip.AddrFrom4([4]byte{b[from], b[from+1], b[from+2], b[from+3]})
	}
	switch {
	case allZero(0, 12): // IPv4-compatible ::a.b.c.d (:: and ::1 classified earlier)
		return tail(12), true
	case allZero(0, 8) && b[8] == 0xff && b[9] == 0xff && b[10] == 0 && b[11] == 0:
		return tail(12), true // IPv4-translated ::ffff:0:a.b.c.d
	case mustPrefix("64:ff9b::/96").Contains(a):
		return tail(12), true // NAT64 well-known prefix
	case b[0] == 0x20 && b[1] == 0x02:
		return tail(2), true // 6to4 2002:a.b.c.d::/48
	}
	return netip.Addr{}, false
}

func classifyIPv6(a netip.Addr) ipClass {
	switch {
	case a == netip.MustParseAddr("fd00:ec2::254"): // IMDS over IPv6, inside ULA
		return classMetadata
	case a == netip.IPv6Unspecified():
		return classUnspecified
	case a == netip.MustParseAddr("::1"):
		return classLoopback
	}
	if embedded, ok := embeddedIPv4(a); ok {
		return classifyIPv4(embedded)
	}
	switch {
	case mustPrefix("64:ff9b:1::/48").Contains(a):
		// Variable-prefix NAT64 has no fixed IPv4 embedding to decode, so it
		// is refused wholesale rather than guessed at.
		return classReserved
	case mustPrefix("fc00::/7").Contains(a):
		return classPrivate
	case mustPrefix("fe80::/10").Contains(a):
		return classLinkLocal
	case mustPrefix("ff00::/8").Contains(a):
		return classMulticast
	case mustPrefix("2001:db8::/32").Contains(a):
		return classDocumentation
	case mustPrefix("2001:2::/48").Contains(a):
		return classBenchmark
	case mustPrefix("100::/64").Contains(a), mustPrefix("2001::/23").Contains(a):
		return classReserved
	case !mustPrefix("2000::/3").Contains(a):
		// Only 2000::/3 is global unicast; an address the guard does not
		// understand is never treated as public.
		return classReserved
	}
	return classPublic
}

// classify judges one address. IPv4-mapped IPv6 is unwrapped first, so a deny
// on 127.0.0.1 cannot be walked around by spelling it ::ffff:127.0.0.1.
func classify(a netip.Addr) ipClass {
	if a.Is4() || a.Is4In6() {
		return classifyIPv4(a.Unmap())
	}
	return classifyIPv6(a)
}

// Policy is the compiled network policy one Fetcher enforces on every target:
// the initial URL, each redirect hop, every resolved address, and the address
// a socket actually connected to.
type Policy struct {
	// AllowPrivate permits loopback, private, link-local, and CGNAT addresses
	// (for intranet or local-dev fetching) and lifts the port allowlist.
	// Metadata endpoints stay blocked regardless — a credential service is
	// never a research target.
	AllowPrivate bool
	// AllowedPorts overrides the port allowlist; empty means 80 and 443
	// (any port when AllowPrivate is set).
	AllowedPorts []int
}

// privateClasses are what AllowPrivate may unblock; metadata is deliberately
// absent.
var privateClasses = map[ipClass]bool{
	classLoopback:  true,
	classPrivate:   true,
	classLinkLocal: true,
	classCGNAT:     true,
}

// EvaluateAddr applies the class policy to one address; nil means allowed.
func (p Policy) EvaluateAddr(a netip.Addr) error {
	class := classify(a)
	switch {
	case class == classPublic:
		return nil
	case class == classMetadata:
		return fmt.Errorf("address blocked (cloud metadata endpoint)")
	case p.AllowPrivate && privateClasses[class]:
		return nil
	}
	return fmt.Errorf("address blocked (%s)", class)
}

func (p Policy) portAllowed(port int) bool {
	if len(p.AllowedPorts) > 0 {
		for _, allowed := range p.AllowedPorts {
			if port == allowed {
				return true
			}
		}
		return false
	}
	if p.AllowPrivate {
		return true
	}
	return port == 80 || port == 443
}

// Target is one validated fetch destination.
type Target struct {
	URL  *url.URL
	Host string // hostname without brackets
	Port int
	// Literal is set when Host is an IP literal rather than a DNS name; the
	// address policy judged it during validation.
	Literal    netip.Addr
	HasLiteral bool
}

// origin identifies a target for credential scoping: scheme, host, and port.
func (t Target) origin() string {
	return fmt.Sprintf("%s://%s:%d", t.URL.Scheme, strings.ToLower(t.Host), t.Port)
}

// hopIdentity identifies a target for redirect-cycle detection: everything
// but the fragment, so a cycle differing only by fragment is still a cycle.
func (t Target) hopIdentity() string {
	return fmt.Sprintf("%s://%s:%d%s?%s", t.URL.Scheme, strings.ToLower(t.Host), t.Port, t.URL.Path, t.URL.RawQuery)
}

const maxURLChars = 4096

var (
	controlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	percentNull  = regexp.MustCompile(`(?i)%00`)
	dnsLabel     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	// numericLabel matches decimal, octal-looking, and hex labels — the
	// spellings an inet_aton-style resolver might interpret as an IPv4
	// address. A host made only of these that is not a canonical dotted quad
	// is ambiguous (two resolvers could disagree about which host was
	// requested) and is refused rather than interpreted.
	numericLabel = regexp.MustCompile(`^(0[xX][0-9a-fA-F]+|[0-9]+)$`)
)

// ValidateURL parses and policy-checks one URL without resolving or
// connecting: scheme, userinfo, control characters, embedded nulls, IPv6 zone
// identifiers, host syntax, ambiguous numeric IPv4 spellings, the port
// allowlist, and — for an IP-literal host — the address policy.
func (p Policy) ValidateURL(raw string) (Target, error) {
	if raw == "" || len(raw) > maxURLChars {
		return Target{}, fmt.Errorf("invalid url: empty or too long")
	}
	if strings.ContainsRune(raw, 0) || percentNull.MatchString(raw) {
		return Target{}, fmt.Errorf("invalid url: embedded null byte")
	}
	if controlChars.MatchString(raw) {
		return Target{}, fmt.Errorf("invalid url: control character")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Target{}, fmt.Errorf("only http and https URLs are allowed (got %q)", u.Scheme)
	}
	if u.User != nil {
		return Target{}, fmt.Errorf("URLs with embedded credentials are not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return Target{}, fmt.Errorf("invalid url: missing host")
	}
	if strings.Contains(host, "%") {
		return Target{}, fmt.Errorf("invalid url: IPv6 zone identifiers are not allowed")
	}

	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if ps := u.Port(); ps != "" {
		if _, err := fmt.Sscanf(ps, "%d", &port); err != nil || port < 1 || port > 65535 {
			return Target{}, fmt.Errorf("invalid url: bad port")
		}
	}
	if !p.portAllowed(port) {
		return Target{}, fmt.Errorf("port %d is not allowed", port)
	}

	t := Target{URL: u, Host: host, Port: port}
	if addr, err := netip.ParseAddr(host); err == nil {
		if err := p.EvaluateAddr(addr); err != nil {
			return Target{}, err
		}
		t.Literal = addr
		t.HasLiteral = true
		return t, nil
	}

	// Not a canonical IP literal: reject ambiguous numeric spellings, then
	// require DNS-name syntax.
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(host), "."), ".")
	numeric := true
	for _, label := range labels {
		if !numericLabel.MatchString(label) {
			numeric = false
			break
		}
	}
	if numeric {
		return Target{}, fmt.Errorf("ambiguous numeric host %q (use a canonical IP address)", host)
	}
	if len(host) > 253 {
		return Target{}, fmt.Errorf("invalid url: host too long")
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || !dnsLabel.MatchString(label) {
			return Target{}, fmt.Errorf("invalid url: malformed host")
		}
	}
	return t, nil
}

// credentialHeaders are request headers that must not follow a redirect to a
// different origin.
var credentialHeaders = []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Api-Key"}

// stripCredentialHeaders removes credential-bearing headers from h when the
// hop's origin differs from the origin the headers were meant for. It reports
// whether anything was removed.
func stripCredentialHeaders(h map[string][]string, from, to Target) bool {
	if from.origin() == to.origin() {
		return false
	}
	stripped := false
	for _, name := range credentialHeaders {
		if _, ok := h[name]; ok {
			delete(h, name)
			stripped = true
		}
	}
	return stripped
}
