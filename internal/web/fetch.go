package web

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Fetch defaults; config can override the first two (web.fetch_max_bytes,
// web.fetch_timeout_seconds).
const (
	DefaultMaxBodyBytes = 2 << 20
	DefaultFetchTimeout = 30 * time.Second
	maxRedirects        = 5
	dialTimeout         = 10 * time.Second
	userAgent           = "shhh-web/1.0 (+https://github.com/rfizzle/shhh)"
)

// Resolver resolves a hostname to all of its addresses; injectable so tests
// exercise pinning and split-horizon answers without touching the network.
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

func defaultResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// Result is one fetched response.
type Result struct {
	FinalURL    string
	Status      int
	ContentType string
	Body        []byte
	Truncated   bool
	FromCache   bool
}

// Fetcher is the guarded HTTP client: every connection resolves through the
// policy, dials only pinned addresses, and re-verifies the connected peer;
// every redirect hop is re-validated and credential headers are stripped
// cross-origin.
type Fetcher struct {
	Policy       Policy
	MaxBodyBytes int64
	Timeout      time.Duration
	Cache        *Cache // optional response cache
	Resolve      Resolver

	client *http.Client
}

// NewFetcher builds a Fetcher with defaults applied.
func NewFetcher(policy Policy) *Fetcher {
	return &Fetcher{
		Policy:       policy,
		MaxBodyBytes: DefaultMaxBodyBytes,
		Timeout:      DefaultFetchTimeout,
		Resolve:      defaultResolver,
	}
}

// httpClient lazily builds the pinned-dial client. Proxies are deliberately
// disabled: a proxy would carry the connection past the dial-time guard.
func (f *Fetcher) httpClient() *http.Client {
	if f.client != nil {
		return f.client
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           f.dialPinned,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: dialTimeout,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	f.client = &http.Client{
		Transport: transport,
		// Redirects are followed manually in Fetch so each hop is validated;
		// the transport must never follow one on its own.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return f.client
}

// dialPinned resolves the host, applies the address policy to EVERY answer
// (one blocked address in a split-horizon answer fails the whole target),
// dials only addresses from that pinned set, and re-verifies the address the
// socket actually connected to before handing the connection back.
func (f *Fetcher) dialPinned(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address: %w", err)
	}

	var pinned []netip.Addr
	if literal, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		pinned = []netip.Addr{literal}
	} else {
		resolve := f.Resolve
		if resolve == nil {
			resolve = defaultResolver
		}
		pinned, err = resolve(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve host: %w", err)
		}
	}
	if len(pinned) == 0 {
		return nil, fmt.Errorf("host resolved to no addresses")
	}
	for _, a := range pinned {
		if err := f.Policy.EvaluateAddr(a); err != nil {
			return nil, fmt.Errorf("blocked by network policy: %w", err)
		}
	}

	dialer := &net.Dialer{Timeout: dialTimeout}
	var lastErr error
	for _, a := range pinned {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), portStr))
		if err != nil {
			lastErr = err
			continue
		}
		if err := verifyConnected(f.Policy, pinned, conn.RemoteAddr()); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
	return nil, fmt.Errorf("cannot connect: %w", lastErr)
}

// verifyConnected is the second half of the rebinding defense: the peer the
// socket reached must be in the pinned set and must still pass policy.
func verifyConnected(policy Policy, pinned []netip.Addr, remote net.Addr) error {
	tcp, ok := remote.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("connected address unverifiable")
	}
	connected, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return fmt.Errorf("connected address unverifiable")
	}
	if err := policy.EvaluateAddr(connected); err != nil {
		return fmt.Errorf("connected address rejected: %w", err)
	}
	for _, a := range pinned {
		if a.Unmap() == connected.Unmap() {
			return nil
		}
	}
	return fmt.Errorf("connected address is not in the pinned set")
}

// Fetch GETs a URL under the policy: bounded time and bytes, redirects
// re-validated per hop with credential headers stripped cross-origin, and a
// cache hit (fresh, same URL) short-circuiting the network entirely.
// initialHeaders, if any, are sent on the first hop and credential-scoped to
// its origin.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, initialHeaders map[string][]string) (Result, error) {
	target, err := f.Policy.ValidateURL(rawURL)
	if err != nil {
		return Result{}, err
	}

	requested := target.URL.String()
	if f.Cache != nil {
		if res, ok := f.Cache.Get(requested); ok {
			res.FromCache = true
			return res, nil
		}
	}

	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	headers := make(map[string][]string, len(initialHeaders))
	for k, v := range initialHeaders {
		headers[http.CanonicalHeaderKey(k)] = v
	}
	origin := target
	visited := map[string]bool{target.hopIdentity(): true}

	for hop := 0; ; hop++ {
		resp, err := f.doRequest(ctx, target, headers)
		if err != nil {
			return Result{}, err
		}

		if loc := redirectLocation(resp); loc != "" {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if hop+1 > maxRedirects {
				return Result{}, fmt.Errorf("too many redirects (max %d)", maxRedirects)
			}
			next, err := target.URL.Parse(loc)
			if err != nil {
				return Result{}, fmt.Errorf("invalid redirect location: %w", err)
			}
			nextTarget, err := f.Policy.ValidateURL(next.String())
			if err != nil {
				return Result{}, fmt.Errorf("redirect blocked: %w", err)
			}
			if visited[nextTarget.hopIdentity()] {
				return Result{}, fmt.Errorf("redirect cycle detected")
			}
			visited[nextTarget.hopIdentity()] = true
			stripCredentialHeaders(headers, origin, nextTarget)
			target = nextTarget
			continue
		}

		body, truncated, err := readBounded(resp.Body, f.maxBody())
		resp.Body.Close()
		if err != nil {
			return Result{}, fmt.Errorf("cannot read response: %w", err)
		}
		res := Result{
			FinalURL:    target.URL.String(),
			Status:      resp.StatusCode,
			ContentType: resp.Header.Get("Content-Type"),
			Body:        body,
			Truncated:   truncated,
		}
		if f.Cache != nil && resp.StatusCode == http.StatusOK {
			f.Cache.Put(requested, target.URL.String(), res)
		}
		return res, nil
	}
}

func (f *Fetcher) maxBody() int64 {
	if f.MaxBodyBytes > 0 {
		return f.MaxBodyBytes
	}
	return DefaultMaxBodyBytes
}

func (f *Fetcher) doRequest(ctx context.Context, target Target, headers map[string][]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL.String(), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header[k] = v
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/html, application/json;q=0.9, text/*;q=0.8, */*;q=0.1")
	}
	resp, err := f.httpClient().Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("fetch timed out after %s", f.Timeout)
		}
		return nil, fmt.Errorf("fetch failed: %w", unwrapURLError(err))
	}
	return resp, nil
}

// unwrapURLError drops the *url.Error wrapper so tool errors do not echo the
// full URL twice.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func redirectLocation(resp *http.Response) string {
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return resp.Header.Get("Location")
	}
	return ""
}

// readBounded reads at most max bytes and reports whether the stream had
// more.
func readBounded(r io.Reader, max int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}
