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
	"sync"
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

	// mu guards the three fields below, each of which is written while
	// fetches are in flight on other goroutines: the grant predicate the
	// session replaces whenever a host is granted or revoked, the client
	// built on first use, and the limiter built with it. A parent and its
	// children fetch through one Fetcher, so "first use" is a race unless it
	// is taken here — two children starting together each built a client,
	// and the loser's was the one every later fetch used.
	mu      sync.Mutex
	granted func(host string) bool
	client  *http.Client
	limit   *limiter
}

// SetGrantedHosts installs the predicate that reports whether a host is one
// this session reaches without asking. It is consulted on redirects and
// nowhere else: a fetch that ran because its host was granted may not be
// handed on to a host that was not, which would make one answered card the
// answer for a site nobody looked at. A fetch a person approved on a card
// follows its redirects as it always did, and so does a session that has
// granted nothing.
//
// It is safe to call while fetches are running, which is what the lock is
// for: a grant is recorded on the UI goroutine and read on whichever
// goroutine a round's fetch landed on.
// See docs/capabilities/approvals-and-safety.md#a-host-is-granted-once.
func (f *Fetcher) SetGrantedHosts(granted func(host string) bool) {
	f.mu.Lock()
	f.granted = granted
	f.mu.Unlock()
}

func (f *Fetcher) grantedHost() func(host string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.granted
}

// pacing is the session's one limiter, built on first use so a zero-value
// Fetcher paces like any other.
func (f *Fetcher) pacing() *limiter {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.limit == nil {
		f.limit = newLimiter(hostGap)
	}
	return f.limit
}

// Waiting reports how much of a host's refusal this session is still sitting
// out, and false where it is not waiting on that host. It is what a surface
// asks to say so on the row: a fetch that has gone quiet for twenty seconds
// is indistinguishable from a hang unless the screen says which it is.
// See docs/capabilities/evidence.md#a-site-is-read-at-the-pace-it-answers.
func (f *Fetcher) Waiting(host string) (time.Duration, bool) {
	return f.pacing().waiting(host)
}

// AbandonWaits gives up every wait in flight. It is the turn's cancel
// reaching the one part of a fetch that is not a request: a person who
// stopped the turn is not asking to sit out the rest of a rate limit for a
// page nobody will now read.
func (f *Fetcher) AbandonWaits() { f.pacing().abandonWaits() }

// NewFetcher builds a Fetcher with defaults applied.
func NewFetcher(policy Policy) *Fetcher {
	return &Fetcher{
		Policy:       policy,
		MaxBodyBytes: DefaultMaxBodyBytes,
		Timeout:      DefaultFetchTimeout,
		Resolve:      defaultResolver,
	}
}

// httpClient lazily builds the pinned-dial client, once for the session and
// every child in it. Proxies are deliberately disabled: a proxy would carry
// the connection past the dial-time guard.
func (f *Fetcher) httpClient() *http.Client {
	f.mu.Lock()
	defer f.mu.Unlock()
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
//
// Requests are paced per host across the session and its children, and a
// host that refuses with "come back later" is believed once: the wait it
// named is sat out with its turn still held, and the request made again. A
// second refusal is the answer, as an error naming the host, the status and
// the wait already spent, because a third identical request is the one thing
// a rate limit is asking the session not to make.
// See docs/capabilities/evidence.md#a-site-is-read-at-the-pace-it-answers.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, initialHeaders map[string][]string) (Result, error) {
	target, err := f.Policy.ValidateURL(rawURL)
	if err != nil {
		return Result{}, err
	}

	// The cache answers before the limiter. A page two children both want
	// costs one request, and the second child is answered out of the store
	// rather than queued behind the first to be told what was already there.
	requested := target.URL.String()
	if f.Cache != nil {
		if res, ok := f.Cache.Get(requested); ok {
			res.FromCache = true
			return res, nil
		}
	}

	pace := f.pacing()
	release, err := pace.take(ctx, target.Host)
	if err != nil {
		return Result{}, err
	}
	defer release()

	var waited time.Duration
	for try := 0; ; try++ {
		at, err := f.fetchChain(ctx, target, initialHeaders)
		if err != nil {
			return Result{}, err
		}
		if !refused(at.res.Status) {
			if f.Cache != nil && at.res.Status == http.StatusOK {
				f.Cache.Put(requested, at.res.FinalURL, at.res)
			}
			return at.res, nil
		}
		if try > 0 {
			// Named rather than returned as a 429 body, because what the
			// model has to do next is not in that body: the page is not
			// coming, and the next call should be a different source.
			return Result{}, fmt.Errorf("%s refused the request twice (%d) after waiting %s: "+
				"the host is rate limiting this session — read a different source rather than asking it again",
				at.host, at.res.Status, waited.Round(time.Second))
		}
		waited = refusalWait(at.retryAfter)
		// The wait is registered under the host whose turn is held, which is
		// the host the request was made to and the one the card and the row
		// name — not the host a redirect may have ended on. A row looking up
		// a wait has only the URL the model asked for.
		if err := pace.waitOut(ctx, target.Host, waited); err != nil {
			return Result{}, err
		}
	}
}

// attempt is what one whole request chain came back with.
type attempt struct {
	res Result
	// host is the host that answered — the last one in the chain rather than
	// the one the fetch asked for, since a redirect may have handed the
	// request on before it was refused.
	host string
	// retryAfter is the wait a refusing host named, and zero for one that
	// named nothing.
	retryAfter time.Duration
}

// fetchChain is one attempt: the request, every redirect it is handed, and
// the response that ends the chain.
//
// The per-fetch timeout is applied here rather than around the retry,
// because the ceiling the person set is how long one request may take — time
// a host explicitly asked shhh to wait is not that request running long, and
// counting it there would make a twenty-second Retry-After a guaranteed
// timeout on a thirty-second budget.
//
// The chain restarts from the requested URL on a retry, headers and all: the
// redirects are the site's answer to this request, not a route to be
// remembered, and the credential scoping has to be decided over the hops
// actually taken.
func (f *Fetcher) fetchChain(ctx context.Context, origin Target, initialHeaders map[string][]string) (attempt, error) {
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
	target := origin
	visited := map[string]bool{target.hopIdentity(): true}

	for hop := 0; ; hop++ {
		resp, err := f.doRequest(ctx, target, headers)
		if err != nil {
			return attempt{}, err
		}

		if loc := redirectLocation(resp); loc != "" {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if hop+1 > maxRedirects {
				return attempt{}, fmt.Errorf("too many redirects (max %d)", maxRedirects)
			}
			next, err := target.URL.Parse(loc)
			if err != nil {
				return attempt{}, fmt.Errorf("invalid redirect location: %w", err)
			}
			nextTarget, err := f.Policy.ValidateURL(next.String())
			if err != nil {
				return attempt{}, fmt.Errorf("redirect blocked: %w", err)
			}
			if visited[nextTarget.hopIdentity()] {
				return attempt{}, fmt.Errorf("redirect cycle detected")
			}
			if err := f.hopAllowed(target.Host, nextTarget.Host); err != nil {
				return attempt{}, err
			}
			visited[nextTarget.hopIdentity()] = true
			stripCredentialHeaders(headers, origin, nextTarget)
			target = nextTarget
			continue
		}

		body, truncated, err := readBounded(resp.Body, f.maxBody())
		resp.Body.Close()
		if err != nil {
			return attempt{}, fmt.Errorf("cannot read response: %w", err)
		}
		return attempt{
			res: Result{
				FinalURL:    target.URL.String(),
				Status:      resp.StatusCode,
				ContentType: resp.Header.Get("Content-Type"),
				Body:        body,
				Truncated:   truncated,
			},
			host:       target.Host,
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}, nil
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

// hopAllowed answers a redirect that leaves one host for another. It refuses
// only where the hop starts on a granted host: the person pressed [a] on
// that host, and a redirect is that host choosing the next one. The message
// names the URL to ask for, so the model's next call is the card for the new
// host rather than the same fetch again.
//
// The hop it judges is the one being taken, not the one the chain started
// on. Anchoring it to the first host instead lets a chain launder a grant in
// two steps: a fetch a person approved on a card reaches a granted host,
// and from there reaches anywhere, because the first host was never granted
// and the check had already stood down.
func (f *Fetcher) hopAllowed(fromHost, nextHost string) error {
	granted := f.grantedHost()
	if granted == nil || nextHost == fromHost {
		return nil
	}
	if !granted(fromHost) || granted(nextHost) {
		return nil
	}
	return fmt.Errorf("%s redirected to %s, which this session has not been asked about: "+
		"fetch the URL on %s directly so the request can be decided", fromHost, nextHost, nextHost)
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
