package sandbox

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestParseHosts(t *testing.T) {
	got, err := ParseHosts([]string{" Registry.NPMjs.org. ", "proxy.golang.org", "registry.npmjs.org", "", "10.0.0.2", "[::1]"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"registry.npmjs.org", "proxy.golang.org", "10.0.0.2", "::1"}
	if !slices.Equal(got, want) {
		t.Fatalf("ParseHosts = %v, want %v", got, want)
	}
	// Each of these is near a host and is not one; read as the nearest host,
	// the list would allow what nobody wrote.
	for _, bad := range []string{"*.npmjs.org", "https://registry.npmjs.org", "registry.npmjs.org:443", "registry.npmjs.org/path", "a..b", "-a.org", "user@host"} {
		if _, err := ParseHosts([]string{bad}); err == nil {
			t.Errorf("ParseHosts(%q) should be refused", bad)
		}
	}
}

// The list is a narrowing of the workspace profile's network: held where the
// mechanism can hold it, ignored under netless, and the profile's switch
// where nothing wraps the command.
func TestResolveHostList(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	policy.AllowHosts = []string{"registry.npmjs.org"}

	for _, mechanism := range []string{"bwrap", "sandbox-exec"} {
		s, err := resolvePolicy(policy, mechanism)
		if err != nil {
			t.Fatal(err)
		}
		if s.network || !slices.Equal(s.hosts, policy.AllowHosts) {
			t.Fatalf("%s: a list must take the command's own network away and hold the list: network=%v hosts=%v", mechanism, s.network, s.hosts)
		}
	}

	s, err := resolvePolicy(policy, "")
	if err != nil {
		t.Fatal(err)
	}
	if !s.network || len(s.hosts) != 0 {
		t.Fatalf("nothing wraps the command, so the list is not held: network=%v hosts=%v", s.network, s.hosts)
	}

	policy.Profile = ProfileWorkspaceNetless
	s, err = resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if s.network || len(s.hosts) != 0 {
		t.Fatalf("netless ignores the list: network=%v hosts=%v", s.network, s.hosts)
	}
}

// fakeListener stands in for the proxy's socket so an argv can be built
// without binding anything.
type fakeListener struct {
	addr net.Addr
	done chan struct{}
	once sync.Once
}

func (l *fakeListener) Accept() (net.Conn, error) { <-l.done; return nil, errors.New("closed") }
func (l *fakeListener) Close() error              { l.once.Do(func() { close(l.done) }); return nil }
func (l *fakeListener) Addr() net.Addr            { return l.addr }

// stubProxyListener replaces the proxy's listener for the life of the test
// and forgets every proxy it started.
func stubProxyListener(t *testing.T) {
	t.Helper()
	old := listenProxy
	listenProxy = func(network, addr string) (net.Listener, error) {
		a := net.Addr(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 45678})
		if network == "unix" {
			a = &net.UnixAddr{Name: addr, Net: "unix"}
		}
		return &fakeListener{addr: a, done: make(chan struct{})}, nil
	}
	t.Cleanup(func() {
		listenProxy = old
		proxies.Lock()
		clear(proxies.m)
		proxies.Unlock()
	})
}

func TestWrapBwrapBridgesAHostList(t *testing.T) {
	testHome(t)
	stubProxyListener(t)
	exe := filepath.Join(t.TempDir(), "shhh")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	old := bridgeProgram
	bridgeProgram = func() (string, error) { return exe, nil }
	t.Cleanup(func() { bridgeProgram = old })

	policy, _ := workspacePolicy(t)
	policy.AllowHosts = []string{"registry.npmjs.org", "proxy.golang.org"}
	tmp := stubHostTemp(t)
	argv, err := Wrap(Availability{Mechanism: "bwrap", OK: true}, policy, "npm install")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argv, "--unshare-net") {
		t.Fatalf("a host list is a namespace with no network of its own: %v", argv)
	}
	inExe, inSock := bridgePaths(tmp)
	if i := slices.Index(argv, resolvedPath(t, exe)); i < 1 || argv[i-1] != "--ro-bind" || argv[i+1] != inExe {
		t.Fatalf("the bridge program must be bound onto the private /tmp: %v", argv)
	}
	if i := slices.Index(argv, inSock); i < 2 || argv[i-2] != "--bind" || !strings.HasSuffix(argv[i-1], ".sock") {
		t.Fatalf("the proxy's socket must be bound onto the private /tmp: %v", argv)
	}
	// After every mask: a bind that came before the tmpfs over /tmp would be
	// covered by it.
	if slices.Index(argv, inSock) < slices.Index(argv, tmp) {
		t.Fatalf("the socket is bound before the tmpfs that would hide it: %v", argv)
	}
	sep := slices.Index(argv, "--")
	want := []string{inExe, BridgeArg, inSock, "--", shellPath(), "-c", "npm install"}
	if !slices.Equal(argv[sep+1:], want) {
		t.Fatalf("the bridge must run in front of the command:\n got %v\nwant %v", argv[sep+1:], want)
	}
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if i := slices.Index(argv, name); i < 1 || argv[i-1] != "--setenv" || argv[i+1] != "http://127.0.0.1:"+bridgePort {
			t.Fatalf("%s must point at the bridge: %v", name, argv)
		}
	}
}

func TestSeatbeltProfileAllowsOnlyTheProxyPort(t *testing.T) {
	testHome(t)
	stubProxyListener(t)
	policy, _ := workspacePolicy(t)
	policy.AllowHosts = []string{"registry.npmjs.org"}

	argv, err := Wrap(Availability{Mechanism: "sandbox-exec", OK: true}, policy, "npm install")
	if err != nil {
		t.Fatal(err)
	}
	profile := argv[2]
	deny := strings.Index(profile, "(deny network*)")
	allow := strings.Index(profile, "(allow network-outbound\n  (remote tcp \"localhost:45678\"))")
	if deny < 0 || allow < deny {
		t.Fatalf("the profile must deny the network and then allow the proxy's port alone:\n%s", profile)
	}
	if strings.Contains(profile, "registry.npmjs.org") {
		t.Fatalf("SBPL cannot name a host, so the list must not be written into the profile:\n%s", profile)
	}
	if !slices.Contains(argv, "HTTPS_PROXY=http://127.0.0.1:45678") {
		t.Fatalf("the command must be pointed at the proxy: %v", argv)
	}
}

// A report resolves the policy and must not start what it reports.
func TestReportNamesTheHostsAndStartsNothing(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	policy.AllowHosts = []string{"registry.npmjs.org", "proxy.golang.org"}

	r := Report(Availability{Mechanism: "bwrap", OK: true}, policy, 0)
	if !strings.Contains(r, "network: 2 hosts — registry.npmjs.org, proxy.golang.org") || !strings.Contains(r, "HTTPS_PROXY") {
		t.Fatalf("the report must name the list and the variables a command gets:\n%s", r)
	}
	proxies.Lock()
	n := len(proxies.m)
	proxies.Unlock()
	if n != 0 {
		t.Fatalf("a report started %d proxies", n)
	}
}

func TestNetworkWords(t *testing.T) {
	contained := Availability{Mechanism: "sandbox-exec", OK: true}
	list := []string{"a.org", "b.org"}
	for _, tc := range []struct {
		avail Availability
		p     Policy
		want  string
	}{
		{contained, Policy{Profile: ProfileWorkspace}, "network preserved"},
		{contained, Policy{Profile: ProfileWorkspaceNetless}, "network disabled"},
		{contained, Policy{Profile: ProfileWorkspace, AllowHosts: list}, "network: 2 hosts — a.org, b.org"},
		{contained, Policy{Profile: ProfileWorkspaceNetless, AllowHosts: list}, "network disabled; sandbox.allow_hosts is not read under this profile"},
		{Availability{Detail: "none"}, Policy{Profile: ProfileWorkspace, AllowHosts: list}, "network preserved; sandbox.allow_hosts is not in force, because nothing here can hold it"},
	} {
		if got := NetworkWords(tc.avail, tc.p); got != tc.want {
			t.Errorf("NetworkWords(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

// fixtureAddress is what every name resolves to in the proxy tests that are
// not about resolution: TEST-NET-1, documentation space, which is neither
// this machine nor a private network.
const fixtureAddress = "192.0.2.10"

// proxyExchange puts one request to the proxy over a pipe and hands back its
// answer and every address the proxy dialled. The far end answers with
// "UPSTREAM" after reading whatever the proxy wrote to it. Every name
// resolves to fixtureAddress.
func proxyExchange(t *testing.T, hosts []string, request string) (answer string, dialled []string, forwarded string) {
	t.Helper()
	return proxyExchangeResolving(t, hosts, nil, request)
}

// proxyExchangeResolving is proxyExchange with a fixture resolver: a name in
// resolved answers with its addresses, and any other with fixtureAddress. The
// suite never asks a real resolver.
func proxyExchangeResolving(t *testing.T, hosts []string, resolved map[string][]string, request string) (answer string, dialled []string, forwarded string) {
	t.Helper()
	p := newHostProxy(hosts)
	p.resolve = func(_ context.Context, host string) ([]net.IP, error) {
		addrs, ok := resolved[host]
		if !ok {
			addrs = []string{fixtureAddress}
		}
		var ips []net.IP
		for _, a := range addrs {
			ips = append(ips, net.ParseIP(a))
		}
		return ips, nil
	}
	var mu sync.Mutex
	var sent strings.Builder
	p.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		mu.Lock()
		dialled = append(dialled, addr)
		mu.Unlock()
		near, far := net.Pipe()
		go func() {
			defer far.Close()
			br := bufio.NewReader(far)
			if req, err := http.ReadRequest(br); err == nil {
				mu.Lock()
				_ = req.Write(&sent)
				mu.Unlock()
			}
			_, _ = io.WriteString(far, "UPSTREAM")
		}()
		return near, nil
	}
	client, server := net.Pipe()
	go p.serveConn(server)
	go func() { _, _ = io.WriteString(client, request) }()
	out, _ := io.ReadAll(client)
	mu.Lock()
	defer mu.Unlock()
	return string(out), dialled, sent.String()
}

func TestProxyTunnelsOnlyAListedHost(t *testing.T) {
	hosts := []string{"registry.npmjs.org"}

	answer, dialled, forwarded := proxyExchange(t, hosts, "CONNECT Registry.NPMjs.org:443 HTTP/1.1\r\nHost: registry.npmjs.org:443\r\n\r\nGET / HTTP/1.1\r\nHost: x\r\n\r\n")
	if !strings.HasPrefix(answer, "HTTP/1.1 200") || !strings.HasSuffix(answer, "UPSTREAM") {
		t.Fatalf("a listed host is tunnelled to: %q", answer)
	}
	if !slices.Equal(dialled, []string{fixtureAddress + ":443"}) || !strings.HasPrefix(forwarded, "GET / HTTP/1.1") {
		t.Fatalf("the tunnel must reach the address the host resolved to and carry what was sent: dialled %v, forwarded %q", dialled, forwarded)
	}

	// Refused before anything is resolved or dialled: a host that is not on
	// the list is never looked up.
	for _, request := range []string{
		"CONNECT npmjs.org:443 HTTP/1.1\r\nHost: npmjs.org:443\r\n\r\n",
		"CONNECT evil.example:443 HTTP/1.1\r\nHost: registry.npmjs.org:443\r\n\r\n",
		"GET http://evil.example/ HTTP/1.1\r\nHost: registry.npmjs.org\r\n\r\n",
	} {
		answer, dialled, _ := proxyExchange(t, hosts, request)
		if !strings.HasPrefix(answer, "HTTP/1.1 403") || !strings.Contains(answer, "sandbox.allow_hosts") {
			t.Errorf("%q must be refused naming the list: %q", request, answer)
		}
		if len(dialled) != 0 {
			t.Errorf("%q dialled %v before it was refused", request, dialled)
		}
	}
}

func TestProxyCarriesOnePlainRequestToAListedHost(t *testing.T) {
	answer, dialled, forwarded := proxyExchange(t, []string{"deb.debian.org"},
		"GET http://deb.debian.org/debian/dists HTTP/1.1\r\nHost: elsewhere.example\r\nProxy-Connection: keep-alive\r\n\r\n")
	if answer != "UPSTREAM" || !slices.Equal(dialled, []string{fixtureAddress + ":80"}) {
		t.Fatalf("a plain request goes to the listed host's address on port 80: answer %q, dialled %v", answer, dialled)
	}
	// Origin form, the host it was sent to in the Host header, and closed
	// after one exchange — a kept-alive connection could name another host
	// next.
	for _, want := range []string{"GET /debian/dists HTTP/1.1\r\n", "Host: deb.debian.org\r\n", "Connection: close\r\n"} {
		if !strings.Contains(forwarded, want) {
			t.Errorf("forwarded request lacks %q:\n%s", want, forwarded)
		}
	}
	if strings.Contains(forwarded, "Proxy-Connection") {
		t.Errorf("a proxy header reached the host:\n%s", forwarded)
	}
}

func TestProxyRefusesARequestThatIsNotForAProxy(t *testing.T) {
	answer, dialled, _ := proxyExchange(t, []string{"a.org"}, "GET / HTTP/1.1\r\nHost: a.org\r\n\r\n")
	if !strings.HasPrefix(answer, "HTTP/1.1 400") || len(dialled) != 0 {
		t.Fatalf("an origin-form request is not a proxy request: %q, dialled %v", answer, dialled)
	}
}

func TestProxyRefusesAListedNameThatResolvesToThisMachine(t *testing.T) {
	resolved := map[string][]string{
		"registry.test": {"127.0.0.1"},
		"split.test":    {"203.0.113.7", "10.1.2.3"},
		"metadata.test": {"169.254.169.254"},
		"lan.test":      {"192.168.1.20"},
		"ula.test":      {"fd00::1"},
		"zero.test":     {"0.0.0.0"},
		"six.test":      {"::1"},
	}
	for name, addrs := range resolved {
		for _, request := range []string{
			"CONNECT " + name + ":443 HTTP/1.1\r\nHost: " + name + ":443\r\n\r\n",
			"GET http://" + name + "/ HTTP/1.1\r\nHost: " + name + "\r\n\r\n",
		} {
			answer, dialled, _ := proxyExchangeResolving(t, []string{name}, resolved, request)
			if !strings.HasPrefix(answer, "HTTP/1.1 403") || !strings.Contains(answer, name+" is in sandbox.allow_hosts but resolves to") {
				t.Errorf("%s resolving to %v must be refused with the reason: %q", name, addrs, answer)
			}
			if len(dialled) != 0 {
				t.Errorf("%s dialled %v before it was refused", name, dialled)
			}
		}
	}
	answer, _, _ := proxyExchangeResolving(t, []string{"registry.test"}, resolved, "CONNECT registry.test:443 HTTP/1.1\r\n\r\n")
	if !strings.Contains(answer, "127.0.0.1, a loopback address") {
		t.Errorf("the refusal names the address and why it is refused: %q", answer)
	}
}

func TestProxyDialsAListedAddressAsWritten(t *testing.T) {
	// Somebody who listed the address itself meant it; nothing is resolved
	// and the address is dialled as written.
	for _, tc := range []struct{ host, request, want string }{
		{"127.0.0.1", "CONNECT 127.0.0.1:8080 HTTP/1.1\r\n\r\nGET / HTTP/1.1\r\nHost: x\r\n\r\n", "127.0.0.1:8080"},
		{"::1", "CONNECT [::1]:8080 HTTP/1.1\r\n\r\nGET / HTTP/1.1\r\nHost: x\r\n\r\n", "[::1]:8080"},
		{"10.0.0.5", "GET http://10.0.0.5/x HTTP/1.1\r\nHost: 10.0.0.5\r\n\r\n", "10.0.0.5:80"},
	} {
		resolved := map[string][]string{tc.host: {"203.0.113.9"}}
		answer, dialled, _ := proxyExchangeResolving(t, []string{tc.host}, resolved, tc.request)
		if strings.HasPrefix(answer, "HTTP/1.1 403") || !slices.Equal(dialled, []string{tc.want}) {
			t.Errorf("listed %s: answer %q, dialled %v, want %s", tc.host, answer, dialled, tc.want)
		}
	}
}

func TestProxyTriesEachResolvedAddressInTurn(t *testing.T) {
	p := newHostProxy([]string{"two.test"})
	p.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.1"), net.ParseIP("203.0.113.2")}, nil
	}
	var dialled []string
	p.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		dialled = append(dialled, addr)
		if len(dialled) == 1 {
			return nil, errors.New("refused")
		}
		near, far := net.Pipe()
		go func() { _, _ = io.Copy(io.Discard, far) }()
		return near, nil
	}
	client, server := net.Pipe()
	go p.serveConn(server)
	go func() { _, _ = io.WriteString(client, "CONNECT two.test:443 HTTP/1.1\r\n\r\n") }()
	line, _ := bufio.NewReader(client).ReadString('\n')
	_ = client.Close()
	if !strings.HasPrefix(line, "HTTP/1.1 200") || !slices.Equal(dialled, []string{"203.0.113.1:443", "203.0.113.2:443"}) {
		t.Fatalf("a failed address falls through to the next checked one: %q, dialled %v", line, dialled)
	}
}
