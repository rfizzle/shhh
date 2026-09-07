package web

import (
	"net/netip"
	"strings"
	"testing"
)

func TestEvaluateAddr_Strict(t *testing.T) {
	p := Policy{}
	blocked := []struct {
		addr string
		want string
	}{
		{"127.0.0.1", "loopback"},
		{"10.1.2.3", "private"},
		{"172.16.0.1", "private"},
		{"192.168.1.1", "private"},
		{"169.254.5.5", "link-local"},
		{"100.64.0.1", "cgnat"},
		{"0.0.0.0", "unspecified"},
		{"192.0.2.1", "documentation"},
		{"198.18.0.1", "benchmark"},
		{"224.0.0.1", "multicast"},
		{"255.255.255.255", "reserved"},
		{"169.254.169.254", "metadata"},
		{"169.254.170.2", "metadata"},
		{"100.100.100.200", "metadata"},
		{"::1", "loopback"},
		{"::", "unspecified"},
		{"fd00::1", "private"},
		{"fe80::1", "link-local"},
		{"2001:db8::1", "documentation"},
		{"fd00:ec2::254", "metadata"},
		// Every wrapped spelling of a blocked IPv4 destination is judged by
		// the embedded address, not the wrapper.
		{"::ffff:127.0.0.1", "loopback"},   // IPv4-mapped
		{"::ffff:0:7f00:1", "loopback"},    // IPv4-translated (RFC 6052)
		{"64:ff9b::a9fe:a9fe", "metadata"}, // NAT64 of the IMDS endpoint
		{"2002:7f00:1::", "loopback"},      // 6to4 of 127.0.0.1
		{"64:ff9b:1::1", "reserved"},       // variable-prefix NAT64: refused wholesale
	}
	for _, tc := range blocked {
		err := p.EvaluateAddr(netip.MustParseAddr(tc.addr))
		if err == nil {
			t.Errorf("EvaluateAddr(%s) = nil, want blocked (%s)", tc.addr, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("EvaluateAddr(%s) = %v, want mention of %q", tc.addr, err, tc.want)
		}
	}

	for _, addr := range []string{"8.8.8.8", "93.184.216.34", "2600::1", "64:ff9b::808:808"} {
		if err := p.EvaluateAddr(netip.MustParseAddr(addr)); err != nil {
			t.Errorf("EvaluateAddr(%s) = %v, want allowed", addr, err)
		}
	}
}

func TestEvaluateAddr_AllowPrivate(t *testing.T) {
	p := Policy{AllowPrivate: true}
	for _, addr := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.5.5", "100.64.0.1", "::1", "fd00::1"} {
		if err := p.EvaluateAddr(netip.MustParseAddr(addr)); err != nil {
			t.Errorf("EvaluateAddr(%s) with AllowPrivate = %v, want allowed", addr, err)
		}
	}
	// Metadata endpoints are never allowed, even inside allowed ranges.
	for _, addr := range []string{"169.254.169.254", "169.254.170.2", "100.100.100.200", "fd00:ec2::254"} {
		if err := p.EvaluateAddr(netip.MustParseAddr(addr)); err == nil {
			t.Errorf("EvaluateAddr(%s) with AllowPrivate = nil, want metadata block", addr)
		}
	}
}

func TestValidateURL(t *testing.T) {
	strict := Policy{}
	cases := []struct {
		name    string
		policy  Policy
		url     string
		wantErr string
	}{
		{"https ok", strict, "https://example.com/path?q=1", ""},
		{"http ok", strict, "http://example.com/", ""},
		{"ftp scheme", strict, "ftp://example.com/", "only http and https"},
		{"file scheme", strict, "file:///etc/passwd", "only http and https"},
		{"userinfo", strict, "https://user:pass@example.com/", "embedded credentials"},
		{"percent null", strict, "https://example.com/%00", "null byte"},
		{"control char", strict, "https://example.com/\x01", "control character"},
		{"empty", strict, "", "empty"},
		{"missing host", strict, "https:///path", "missing host"},
		{"ambiguous short ipv4", strict, "https://127.1/", "ambiguous numeric host"},
		{"ambiguous hex ipv4", strict, "https://0x7f000001/", "ambiguous numeric host"},
		{"ambiguous decimal ipv4", strict, "https://2130706433/", "ambiguous numeric host"},
		{"ambiguous zero-padded ipv4", strict, "https://127.0.0.01/", "ambiguous numeric host"},
		{"loopback literal", strict, "https://127.0.0.1/", "loopback"},
		{"v6 loopback literal", strict, "https://[::1]/", "loopback"},
		{"metadata literal", strict, "http://169.254.169.254/latest/meta-data/", "metadata"},
		{"zone id", strict, "http://[fe80::1%25eth0]/", "zone identifier"},
		{"underscore host", strict, "https://exa_mple.com/", "malformed host"},
		{"non-default port", strict, "https://example.com:8443/", "port 8443 is not allowed"},
		{"port allowlisted", Policy{AllowedPorts: []int{8443}}, "https://example.com:8443/", ""},
		{"private allowed", Policy{AllowPrivate: true}, "http://127.0.0.1:8080/", ""},
		{"metadata still blocked", Policy{AllowPrivate: true}, "http://169.254.169.254/", "metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, err := tc.policy.ValidateURL(tc.url)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateURL(%q) = %v, want ok", tc.url, err)
				}
				if target.URL == nil || target.Host == "" {
					t.Fatalf("ValidateURL(%q) returned incomplete target %+v", tc.url, target)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateURL(%q) = %v, want error containing %q", tc.url, err, tc.wantErr)
			}
		})
	}
}

func TestValidateURL_TooLong(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", maxURLChars)
	if _, err := (Policy{}).ValidateURL(long); err == nil {
		t.Fatal("expected over-long URL to be rejected")
	}
}

func TestValidateURL_Ports(t *testing.T) {
	if _, err := (Policy{}).ValidateURL("http://example.com:80/"); err != nil {
		t.Errorf("port 80 should be allowed: %v", err)
	}
	if _, err := (Policy{}).ValidateURL("https://example.com:443/"); err != nil {
		t.Errorf("port 443 should be allowed: %v", err)
	}
	if _, err := (Policy{AllowPrivate: true}).ValidateURL("https://example.com:8443/"); err != nil {
		t.Errorf("AllowPrivate should lift the port allowlist: %v", err)
	}
}

func TestStripCredentialHeaders(t *testing.T) {
	p := Policy{}
	from, err := p.ValidateURL("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	sameOrigin, err := p.ValidateURL("https://example.com/other")
	if err != nil {
		t.Fatal(err)
	}
	crossOrigin, err := p.ValidateURL("https://evil.example.net/")
	if err != nil {
		t.Fatal(err)
	}

	headers := map[string][]string{
		"Authorization": {"Bearer tok"},
		"Cookie":        {"session=1"},
		"Accept":        {"text/html"},
	}
	if stripped := stripCredentialHeaders(headers, from, sameOrigin); stripped {
		t.Error("same-origin hop must keep credential headers")
	}
	if headers["Authorization"] == nil {
		t.Error("Authorization removed on same-origin hop")
	}

	if stripped := stripCredentialHeaders(headers, from, crossOrigin); !stripped {
		t.Error("cross-origin hop must strip credential headers")
	}
	if headers["Authorization"] != nil || headers["Cookie"] != nil {
		t.Errorf("credential headers survived a cross-origin hop: %v", headers)
	}
	if headers["Accept"] == nil {
		t.Error("non-credential header must survive")
	}
}

func TestHopIdentity_IgnoresFragment(t *testing.T) {
	p := Policy{}
	a, err := p.ValidateURL("https://example.com/page?x=1#one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.ValidateURL("https://example.com/page?x=1#two")
	if err != nil {
		t.Fatal(err)
	}
	if a.hopIdentity() != b.hopIdentity() {
		t.Error("hop identity must ignore the fragment")
	}
}

// The fixture exception is one origin's. It reaches the server it names on
// the port it names, and lifts nothing else: another port on the same
// loopback address is a different server, and a policy that never named a
// fixture is unchanged.
func TestFixturePolicy_ReachesOneOriginAndNothingElse(t *testing.T) {
	p := FixturePolicy("127.0.0.1:41234")
	if _, err := p.ValidateURL("http://127.0.0.1:41234/index.html"); err != nil {
		t.Fatalf("the fixture's own origin must be reachable: %v", err)
	}
	for _, raw := range []string{
		"http://127.0.0.1:3000/",           // another server on the same address
		"http://127.0.0.2:41234/",          // another loopback address, same port
		"http://192.168.1.4:41234/",        // a private address that is not loopback
		"http://169.254.169.254/",          // the metadata endpoint, always refused
		"http://example.com/",              // the public web, which is not reproducible
		"http://[::1]:41234/",              // loopback spelled the other way
		"http://[::ffff:127.0.0.1]:41234/", // and the mapped spelling of the same
	} {
		if _, err := p.ValidateURL(raw); err == nil {
			t.Errorf("%s must be refused by a fixture policy", raw)
		}
	}
	if _, err := (Policy{}).ValidateURL("http://127.0.0.1:41234/"); err == nil {
		t.Error("a policy that named no fixture must still refuse loopback")
	}
}

// The exception has to survive the guard's second and third looks — the
// address policy applied to every resolved answer, and the re-verification of
// the peer a socket reached — or a fixture URL validates and then fails to
// dial.
func TestFixturePolicy_AppliesToTheAddressAndThePeer(t *testing.T) {
	p := FixturePolicy("127.0.0.1:41234")
	loopback := netip.MustParseAddr("127.0.0.1")
	if err := p.evaluateAddrPort(loopback, 41234); err != nil {
		t.Errorf("the fixture's address on its own port: %v", err)
	}
	if err := p.evaluateAddrPort(loopback, 3000); err == nil {
		t.Error("the same address on another port is another server")
	}
	if err := p.EvaluateAddr(loopback); err == nil {
		t.Error("the port-blind form knows no fixture and must still refuse loopback")
	}
	if err := p.evaluateAddrPort(netip.MustParseAddr("169.254.169.254"), 41234); err == nil {
		t.Error("the metadata endpoint is never a fixture")
	}
	// And naming it as the fixture does not make it one: the exception is
	// loopback's, and the class is judged rather than taken from the origin.
	metadata := FixturePolicy("169.254.169.254:80")
	if err := metadata.evaluateAddrPort(netip.MustParseAddr("169.254.169.254"), 80); err == nil {
		t.Error("a fixture policy naming the metadata endpoint must still refuse it")
	}
	if _, err := metadata.ValidateURL("http://169.254.169.254/"); err == nil {
		t.Error("a fixture policy naming the metadata endpoint must still refuse its URL")
	}
}
