package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// A contained command confined to a list of hosts has no network of its own:
// bubblewrap gives it an empty namespace and Seatbelt denies every socket but
// one. What it can reach is this proxy, which runs in shhh's own process,
// outside containment, and opens a connection only to a host on the list.
//
// The proxy is where the list is read, so it is also where it can leak, and
// three things keep it from doing so. It checks the host a request names
// before it resolves anything, so a host that is not listed is never looked
// up, let alone reached; the command cannot look one up either, since its
// resolver is behind the same wall. It never follows a redirect — a CONNECT
// is a tunnel it cannot see into, and a plain request is answered once and
// the connection closed — so a listed host that points somewhere else hands
// the command an address it has to ask for again, and is refused there. And
// it matches exactly, the way the fetcher's own host list does: a listed
// `npmjs.org` does not cover `registry.npmjs.org`.
// See docs/capabilities/containment.md#a-contained-commands-network-can-be-a-list-of-hosts.
type hostProxy struct {
	hosts []string
	// dial is how an allowed request reaches its host, a field so a test
	// can answer for one proxy without a network.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// addr is where the command reaches the proxy: a loopback address for
	// Seatbelt, and a socket file for bubblewrap, whose namespace has no
	// route to the host's loopback at all.
	addr string
}

// proxyHeaderTimeout bounds how long a connection may take to say where it
// is going. A command that opens the proxy and says nothing holds a
// goroutine, and nothing else would ever end it.
const proxyHeaderTimeout = 30 * time.Second

// proxyDialTimeout bounds the connection to an allowed host.
const proxyDialTimeout = 30 * time.Second

// proxyDial is how a new proxy reaches an allowed host; a variable so the
// containment checks can put a real command through a real proxy to a
// listener of their own rather than to the internet.
var proxyDial = (&net.Dialer{Timeout: proxyDialTimeout}).DialContext

func newHostProxy(hosts []string) *hostProxy {
	return &hostProxy{hosts: hosts, dial: proxyDial}
}

func (p *hostProxy) allowed(host string) bool {
	return slices.Contains(p.hosts, normalHost(host))
}

func (p *hostProxy) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go p.serveConn(c)
	}
}

// serveConn answers one connection: a CONNECT is a tunnel to a listed host,
// an absolute-URI request is one plain HTTP exchange with one, and anything
// else is refused. Each connection carries one request, because a keep-alive
// connection that named a listed host first could name any host second.
func (p *hostProxy) serveConn(c net.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	target := req.Host
	if req.Method != http.MethodConnect {
		if req.URL.Host == "" || req.URL.Scheme != "http" {
			refuse(c, http.StatusBadRequest, "shhh: this is a proxy for the hosts in sandbox.allow_hosts; send it a CONNECT or an absolute http:// request")
			return
		}
		target = req.URL.Host
		if _, _, err := net.SplitHostPort(target); err != nil {
			target = net.JoinHostPort(normalHost(target), "80")
		}
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		refuse(c, http.StatusBadRequest, "shhh: a CONNECT names a host and a port")
		return
	}
	if !p.allowed(host) {
		refuse(c, http.StatusForbidden, fmt.Sprintf("shhh: %s is not in sandbox.allow_hosts; a contained command reaches only %s", normalHost(host), strings.Join(p.hosts, ", ")))
		return
	}
	up, err := p.dial(context.Background(), "tcp", target)
	if err != nil {
		refuse(c, http.StatusBadGateway, "shhh: "+err.Error())
		return
	}
	defer up.Close()

	if req.Method == http.MethodConnect {
		if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return
		}
		// Whatever the client sent after the header — a TLS hello that did
		// not wait for the answer — is already in the reader.
		if n := br.Buffered(); n > 0 {
			buffered, _ := br.Peek(n)
			if _, err := up.Write(buffered); err != nil {
				return
			}
		}
		pipe(c, up)
		return
	}

	// A plain request goes on in origin form to the host it named, with the
	// Host header made to agree and the connection closed after it.
	req.Host = req.URL.Host
	req.Close = true
	req.Header.Del("Proxy-Connection")
	req.Header.Del("Proxy-Authorization")
	if err := req.Write(up); err != nil {
		return
	}
	_, _ = io.Copy(c, up)
}

// refuse answers a request the proxy will not carry, in HTTP, so the tool
// that asked reports the reason as the proxy's rather than as a dropped
// connection.
func refuse(c net.Conn, status int, reason string) {
	body := reason + "\n"
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(body), body)
}

// pipe copies both ways until either side is done, then closes both, so a
// tunnel whose far end hangs up does not leave the near end waiting.
func pipe(a, b net.Conn) {
	var once sync.Once
	closeBoth := func() { _ = a.Close(); _ = b.Close() }
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(a, b); once.Do(closeBoth) }()
	go func() { defer wg.Done(); _, _ = io.Copy(b, a); once.Do(closeBoth) }()
	wg.Wait()
}

// proxies are the listeners this process has opened, one per mechanism and
// host list. A proxy is started the first time a command is wrapped with a
// list, never by a report of one, and lives as long as the process: the
// wrap is built per command and there is no seam that runs when a session
// ends, which is the same answer the session's own tmpdir has.
var proxies = struct {
	sync.Mutex
	m map[string]*hostProxy
}{m: map[string]*hostProxy{}}

// listenProxy is how a proxy's listener is opened; a variable so the argv
// tests can wrap a command without binding anything.
var listenProxy = func(network, addr string) (net.Listener, error) { return net.Listen(network, addr) }

// proxyFor is the running proxy for this mechanism and list, started on
// first use.
func proxyFor(mechanism string, hosts []string) (*hostProxy, error) {
	key := mechanism + "\x00" + strings.Join(hosts, ",")
	proxies.Lock()
	defer proxies.Unlock()
	if p, ok := proxies.m[key]; ok {
		return p, nil
	}
	p := newHostProxy(hosts)
	var ln net.Listener
	var err error
	switch mechanism {
	case "sandbox-exec":
		ln, err = listenProxy("tcp", "127.0.0.1:0")
	case "bwrap":
		// A socket file rather than a port: the command's namespace has a
		// loopback of its own and no route to this one, so the file is bound
		// into it and the bridge inside carries a port there to it. It lives
		// in this process's own scratch directory, which the next session
		// sweeps once this one is gone.
		var dir string
		dir, err = sessionTmpDir()
		if err == nil {
			ln, err = listenProxy("unix", filepath.Join(dir, fmt.Sprintf("net%d.sock", len(proxies.m))))
		}
	default:
		return nil, fmt.Errorf("wrap unsupported: %s cannot hold a host list", mechanism)
	}
	if err != nil {
		return nil, fmt.Errorf("wrap unsupported: cannot start the proxy for sandbox.allow_hosts: %v", err)
	}
	p.addr = ln.Addr().String()
	go p.serve(ln)
	proxies.m[key] = p
	return p, nil
}
