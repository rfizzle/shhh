package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rfizzle/shhh/internal/testhttp"
	"time"
)

var fixtureHTTP testhttp.Registry

// testServer answers a client through an in-memory transport. The fetcher
// tests care about HTTP semantics, not whether this host permits a listener.
func testServer(t *testing.T, h http.Handler) *testhttp.Server {
	t.Helper()
	return fixtureHTTP.NewServer(h)
}

// testFetcher returns a fetcher whose policy admits fixture origins.
func testFetcher() *Fetcher {
	return fixtureFetcher(Policy{AllowPrivate: true})
}

func fixtureFetcher(policy Policy) *Fetcher {
	f := NewFetcher(policy)
	f.client = fixtureHTTP.Client()
	f.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return f
}

func TestFetch_Basic(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello world")
	}))
	defer srv.Close()

	res, err := testFetcher().Fetch(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(res.Body) != "hello world" {
		t.Errorf("body = %q", res.Body)
	}
	if res.Status != 200 || res.Truncated || res.FromCache {
		t.Errorf("unexpected result: %+v", res)
	}
	if !strings.HasPrefix(res.FinalURL, srv.URL) {
		t.Errorf("FinalURL = %q, want prefix %q", res.FinalURL, srv.URL)
	}
}

func TestFetch_RedirectFollowedAndFinalURLReported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "done")
	})
	srv := testServer(t, mux)
	defer srv.Close()

	res, err := testFetcher().Fetch(context.Background(), srv.URL+"/a", nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(res.Body) != "done" {
		t.Errorf("body = %q", res.Body)
	}
	if !strings.HasSuffix(res.FinalURL, "/b") {
		t.Errorf("FinalURL = %q, want .../b", res.FinalURL)
	}
}

func TestFetch_RedirectToBlockedAddressRefused(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	_, err := testFetcher().Fetch(context.Background(), srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "redirect blocked") {
		t.Fatalf("err = %v, want redirect blocked", err)
	}
	if !strings.Contains(err.Error(), "metadata") {
		t.Errorf("err = %v, want metadata mention", err)
	}
}

func TestFetch_RedirectCycleDetected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/a", http.StatusFound)
	})
	srv := testServer(t, mux)
	defer srv.Close()

	_, err := testFetcher().Fetch(context.Background(), srv.URL+"/a", nil)
	if err == nil || !strings.Contains(err.Error(), "redirect cycle") {
		t.Fatalf("err = %v, want redirect cycle", err)
	}
}

func TestFetch_TooManyRedirects(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/hop/%d", &n); err != nil {
			n = 0
		}
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", n+1), http.StatusFound)
	}))
	defer srv.Close()

	_, err := testFetcher().Fetch(context.Background(), srv.URL+"/hop/0", nil)
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("err = %v, want too many redirects", err)
	}
}

func TestFetch_CredentialHeadersStrippedCrossOrigin(t *testing.T) {
	var gotAuth, gotCookie, gotAccept atomic.Value
	other := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotCookie.Store(r.Header.Get("Cookie"))
		gotAccept.Store(r.Header.Get("Accept"))
		fmt.Fprint(w, "ok")
	}))
	defer other.Close()

	first := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A different port is a different origin, even on the same host.
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer first.Close()

	headers := map[string][]string{
		"Authorization": {"Bearer secret"},
		"Cookie":        {"session=1"},
		"Accept":        {"text/plain"},
	}
	if _, err := testFetcher().Fetch(context.Background(), first.URL, headers); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotAuth.Load() != "" {
		t.Errorf("Authorization crossed origins: %q", gotAuth.Load())
	}
	if gotCookie.Load() != "" {
		t.Errorf("Cookie crossed origins: %q", gotCookie.Load())
	}
	if gotAccept.Load() != "text/plain" {
		t.Errorf("Accept = %q, want preserved", gotAccept.Load())
	}
}

func TestFetch_SameOriginRedirectKeepsHeaders(t *testing.T) {
	var gotAuth atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		fmt.Fprint(w, "ok")
	})
	srv := testServer(t, mux)
	defer srv.Close()

	headers := map[string][]string{"Authorization": {"Bearer tok"}}
	if _, err := testFetcher().Fetch(context.Background(), srv.URL+"/a", headers); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotAuth.Load() != "Bearer tok" {
		t.Errorf("Authorization = %q, want kept on same origin", gotAuth.Load())
	}
}

func TestFetch_BodyCeiling(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 100))
	}))
	defer srv.Close()

	f := testFetcher()
	f.MaxBodyBytes = 10
	res, err := f.Fetch(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.Truncated || len(res.Body) != 10 {
		t.Errorf("Truncated=%v len=%d, want true/10", res.Truncated, len(res.Body))
	}
}

func TestFetch_Timeout(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	f := testFetcher()
	f.Timeout = 100 * time.Millisecond
	if _, err := f.Fetch(context.Background(), srv.URL, nil); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestFetch_BlockedLiteralNeverConnects(t *testing.T) {
	_, err := NewFetcher(Policy{}).Fetch(context.Background(), "http://127.0.0.1:80/", nil)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("err = %v, want loopback block before any connection", err)
	}
}

func TestFetch_DialBlocksResolvedPrivateAddress(t *testing.T) {
	// A DNS name that resolves to a private address must be blocked at dial
	// time, after resolution — the guard against internal-name SSRF.
	f := NewFetcher(Policy{})
	f.Resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil
	}
	_, err := f.Fetch(context.Background(), "http://internal.example.com/", nil)
	if err == nil || !strings.Contains(err.Error(), "blocked by network policy") {
		t.Fatalf("err = %v, want dial-time policy block", err)
	}
}

func TestFetch_SplitHorizonAnswerFailsWholeTarget(t *testing.T) {
	// One blocked address among the resolved answers fails the target, even
	// though a public one is also present.
	f := NewFetcher(Policy{})
	f.Resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
			netip.MustParseAddr("10.0.0.5"),
		}, nil
	}
	_, err := f.Fetch(context.Background(), "http://split.example.com/", nil)
	if err == nil || !strings.Contains(err.Error(), "blocked by network policy") {
		t.Fatalf("err = %v, want split-horizon block", err)
	}
}

func TestFetch_ResolvedHostDialsPinnedAddress(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "pinned")
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	f := testFetcher()
	srv.Alias("docs.example.com")
	f.Resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		if host != "docs.example.com" {
			return nil, fmt.Errorf("unexpected host %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	res, err := f.Fetch(context.Background(), "http://docs.example.com:"+u.Port()+"/", nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(res.Body) != "pinned" {
		t.Errorf("body = %q", res.Body)
	}
}

func TestFetch_CacheHitSkipsNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "cache me")
	}))
	defer srv.Close()

	cache, err := OpenCache(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f := testFetcher()
	f.Cache = cache

	first, err := f.Fetch(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if first.FromCache {
		t.Error("first fetch must not be cached")
	}
	second, err := f.Fetch(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if !second.FromCache {
		t.Error("second fetch should hit the cache")
	}
	if string(second.Body) != "cache me" {
		t.Errorf("cached body = %q", second.Body)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}
}

func TestVerifyConnected(t *testing.T) {
	strict := Policy{}
	pinned := []netip.Addr{netip.MustParseAddr("93.184.216.34")}
	remote := &net.TCPAddr{IP: net.ParseIP("93.184.216.34"), Port: 443}
	if err := verifyConnected(strict, pinned, remote); err != nil {
		t.Errorf("matching public peer rejected: %v", err)
	}

	// A peer outside the pinned set is refused even when its class is fine.
	other := &net.TCPAddr{IP: net.ParseIP("1.1.1.1"), Port: 443}
	if err := verifyConnected(strict, pinned, other); err == nil {
		t.Error("unpinned peer accepted")
	}

	// A pinned peer that fails policy is refused: the policy re-check is what
	// makes a rebind between resolution and connection harmless.
	privatePinned := []netip.Addr{netip.MustParseAddr("10.0.0.5")}
	privateRemote := &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 80}
	if err := verifyConnected(strict, privatePinned, privateRemote); err == nil {
		t.Error("policy-failing peer accepted")
	}

	// IPv4-mapped spelling of a pinned IPv4 address still matches.
	mappedRemote := &net.TCPAddr{IP: net.ParseIP("::ffff:93.184.216.34"), Port: 443}
	if err := verifyConnected(strict, pinned, mappedRemote); err != nil {
		t.Errorf("mapped spelling of pinned peer rejected: %v", err)
	}
}

// loopbackResolver answers every name with 127.0.0.1, so a test can give one
// local server two host names and exercise what "cross-host" means.
func loopbackResolver(context.Context, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

// A fetch that ran because its host was granted may not be handed on to a
// host nobody has answered for: the person pressed [a] on one site, and a
// redirect is that site choosing the next one. The refusal names the URL to
// ask for, so the model's next call is the card for the new host rather than
// the same fetch again.
func TestFetch_AGrantedHostCannotRedirectToAnUngrantedOne(t *testing.T) {
	var reached bool
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost"+portOf(r.Host)+"/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		reached = true
		fmt.Fprint(w, "elsewhere")
	})
	srv := testServer(t, mux)
	defer srv.Close()

	f := fixtureFetcher(Policy{AllowPrivate: true})
	srv.Alias("localhost")
	loopbackURL := srv.Alias("127.0.0.1")
	f.Resolve = loopbackResolver
	f.SetGrantedHosts(func(host string) bool { return host == "127.0.0.1" })

	_, err := f.Fetch(context.Background(), loopbackURL+"/a", nil)
	if err == nil {
		t.Fatal("a granted host handed the request to an ungranted one")
	}
	if !strings.Contains(err.Error(), "localhost") || !strings.Contains(err.Error(), "decided") {
		t.Errorf("the refusal does not say which host to ask about: %v", err)
	}
	if reached {
		t.Error("the ungranted host was fetched anyway")
	}

	// Grant the destination too and the hop is not a decision any more.
	f.SetGrantedHosts(func(host string) bool { return true })
	if _, err := f.Fetch(context.Background(), loopbackURL+"/a", nil); err != nil {
		t.Fatalf("a hop between two granted hosts was refused: %v", err)
	}
	if !reached {
		t.Error("the granted destination was not fetched")
	}
}

// A fetch a person approved on a card follows its redirects as it always
// did, and so does a session that has granted nothing: the rule exists to
// stop a grant widening itself, not to make every redirect a second card.
func TestFetch_AnUngrantedFetchFollowsItsRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost"+portOf(r.Host)+"/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "done") })
	srv := testServer(t, mux)
	defer srv.Close()

	f := fixtureFetcher(Policy{AllowPrivate: true})
	srv.Alias("localhost")
	f.Resolve = loopbackResolver
	f.SetGrantedHosts(func(host string) bool { return host == "docs.python.org" })
	res, err := f.Fetch(context.Background(), srv.URL+"/a", nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(res.Body) != "done" {
		t.Errorf("body = %q", res.Body)
	}
}

// The host deny list is asked at every hop, which is the one place a refused
// host could be reached without any decision being taken about it.
func TestFetch_ADeniedHostIsRefusedOnTheFirstHopAndOnARedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost"+portOf(r.Host)+"/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "denied content") })
	srv := testServer(t, mux)
	defer srv.Close()

	f := fixtureFetcher(Policy{
		AllowPrivate: true,
		DenyHost:     func(host string) bool { return host == "localhost" },
	})
	srv.Alias("localhost")
	f.Resolve = loopbackResolver
	if _, err := f.Fetch(context.Background(), srv.URL+"/a", nil); err == nil ||
		!strings.Contains(err.Error(), "refused for this session") {
		t.Fatalf("a redirect reached a denied host: %v", err)
	}
	if _, err := f.Fetch(context.Background(), "http://localhost/b", nil); err == nil ||
		!strings.Contains(err.Error(), "refused for this session") {
		t.Fatalf("a denied host was fetched directly: %v", err)
	}
}

// portOf is the ":port" of a host header, for a redirect that has to name a
// different host on the same local server.
func portOf(hostPort string) string {
	if i := strings.LastIndex(hostPort, ":"); i >= 0 {
		return hostPort[i:]
	}
	return ""
}

// The hop that is judged is the one being taken, not the one the chain
// started on. A chain that launders a grant in two steps — a fetch approved
// on a card reaches a granted host, and from there reaches anywhere — is the
// failure a check anchored to the first host does not see.
func TestFetch_AGrantIsNotLaunderedThroughAThirdHost(t *testing.T) {
	var reached bool
	mux := http.NewServeMux()
	// The chain is 127.0.0.1 (ungranted, approved on a card) → granted.test
	// → elsewhere.test, all on the one loopback server.
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://granted.test"+portOf(r.Host)+"/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.test"+portOf(r.Host)+"/c", http.StatusFound)
	})
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) {
		reached = true
		fmt.Fprint(w, "laundered")
	})
	srv := testServer(t, mux)
	defer srv.Close()

	f := fixtureFetcher(Policy{AllowPrivate: true})
	srv.Alias("granted.test")
	srv.Alias("elsewhere.test")
	f.Resolve = loopbackResolver
	f.SetGrantedHosts(func(host string) bool { return host == "granted.test" })

	_, err := f.Fetch(context.Background(), srv.URL+"/a", nil)
	if err == nil || !strings.Contains(err.Error(), "elsewhere.test") {
		t.Fatalf("the granted host handed the request on: err = %v", err)
	}
	if reached {
		t.Error("the third host was fetched anyway")
	}
}

// A fan-out is three researchers and one site. Requests to one host go out
// one at a time; a second host is not made to wait behind them.
func TestFetch_OneHostIsAskedOneRequestAtATime(t *testing.T) {
	var mu sync.Mutex
	var live, mostLive int
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		live++
		mostLive = max(mostLive, live)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		live--
		mu.Unlock()
		fmt.Fprint(w, "page")
	}))
	defer srv.Close()

	f := testFetcher()
	f.Resolve = loopbackResolver
	newFakeWaits(f.pacing())
	docsURL := srv.Alias("docs.test")

	// Two children reading the same host, at the same moment.
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.Fetch(context.Background(), docsURL+"/a", nil); err != nil {
				t.Errorf("Fetch: %v", err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	serialised := mostLive
	mu.Unlock()
	if serialised != 1 {
		t.Fatalf("%d requests to one host were in flight at once", serialised)
	}

	// A second host runs while the first is held, which is what "per host"
	// means: the first fetch is still in flight when the second returns.
	held := make(chan struct{})
	slow := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-held
		fmt.Fprint(w, "slow")
	}))
	defer slow.Close()
	slowURL := slow.Alias("slow.test")
	go func() {
		_, _ = f.Fetch(context.Background(), slowURL+"/a", nil)
	}()
	if _, err := f.Fetch(context.Background(), docsURL+"/b", nil); err != nil {
		t.Fatalf("a second host waited for the first: %v", err)
	}
	close(held)
}

// A host that asks for the request again later is believed once: the wait it
// named is sat out and the request made again.
func TestFetch_ARefusalIsWaitedOutOnceAndRetried(t *testing.T) {
	var hits atomic.Int32
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "page")
	}))
	defer srv.Close()

	f := testFetcher()
	waits := newFakeWaits(f.pacing())
	res, err := f.Fetch(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(res.Body) != "page" || res.Status != 200 {
		t.Errorf("result = %d %q, want the page the retry got", res.Status, res.Body)
	}
	if hits.Load() != 2 {
		t.Errorf("requests = %d, want the first and one retry", hits.Load())
	}
	if got := waits.recorded(); len(got) != 1 || got[0] != 2*time.Second {
		t.Errorf("waits = %v, want the 2s the host asked for", got)
	}
}

// A 503 with no header is waited out on the default rather than not at all:
// the status is the host saying come back, header or no header.
func TestFetch_OverloadWithNoHeaderWaitsTheDefault(t *testing.T) {
	var hits atomic.Int32
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "page")
	}))
	defer srv.Close()

	f := testFetcher()
	waits := newFakeWaits(f.pacing())
	res, err := f.Fetch(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(res.Body) != "page" {
		t.Errorf("body = %q, want the page the retry got", res.Body)
	}
	if got := waits.recorded(); len(got) != 1 || got[0] != defaultRefusalWait {
		t.Errorf("waits = %v, want one wait of %s", got, defaultRefusalWait)
	}
}

// The second refusal is the answer, and it says enough for the model to go
// somewhere else: the host, the status, and the wait already spent.
func TestFetch_RefusedTwiceNamesTheHostTheStatusAndTheWait(t *testing.T) {
	var hits atomic.Int32
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	f := testFetcher()
	f.Resolve = loopbackResolver
	newFakeWaits(f.pacing())
	_, err := f.Fetch(context.Background(), srv.Alias("docs.test")+"/a", nil)
	if err == nil {
		t.Fatal("a host that refused twice answered anyway")
	}
	for _, want := range []string{"docs.test", "429", "3s", "different source"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if hits.Load() != 2 {
		t.Errorf("requests = %d, want two — the refusal is waited out once, not repeatedly", hits.Load())
	}
}

// Nothing else is retried. A 5xx that is not a 503 is a server that broke
// and a 4xx is a page that is not there; both are final on the first answer,
// and the status is the result rather than an error.
func TestFetch_ServerErrorAndNotFoundAreFinal(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound} {
		var hits atomic.Int32
		srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(status)
			fmt.Fprint(w, "no")
		}))
		f := testFetcher()
		waits := newFakeWaits(f.pacing())
		res, err := f.Fetch(context.Background(), srv.URL, nil)
		srv.Close()
		if err != nil {
			t.Fatalf("%d: Fetch: %v", status, err)
		}
		if res.Status != status {
			t.Errorf("status = %d, want %d", res.Status, status)
		}
		if hits.Load() != 1 {
			t.Errorf("%d: requests = %d, want one", status, hits.Load())
		}
		if got := waits.recorded(); len(got) != 0 {
			t.Errorf("%d: waits = %v, want none", status, got)
		}
	}
}

// The cache is asked before the limiter: a page two children both want costs
// one request, and the second is answered out of the store rather than
// queued behind the first.
func TestFetch_TheCacheAnswersBeforeTheLimiter(t *testing.T) {
	var hits atomic.Int32
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "page")
	}))
	defer srv.Close()

	cache, err := OpenCache(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("OpenCache: %v", err)
	}
	f := testFetcher()
	f.Cache = cache
	if _, err := f.Fetch(context.Background(), srv.URL, nil); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// The host's turn is taken and never given back: a fetch that had to
	// queue for it would block here forever.
	release, err := f.pacing().take(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	defer release()

	done := make(chan Result, 1)
	go func() {
		res, err := f.Fetch(context.Background(), srv.URL, nil)
		if err != nil {
			t.Errorf("cached Fetch: %v", err)
			close(done)
			return
		}
		done <- res
	}()
	select {
	case res := <-done:
		if !res.FromCache {
			t.Error("the second read was not the cached one")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cached page queued behind another request to the host")
	}
	if hits.Load() != 1 {
		t.Errorf("requests = %d, want one for two reads of the same page", hits.Load())
	}
}

// A timeout is not retried. The ceiling is the person's setting, and asking
// again is asking the same slow host to answer inside the same budget.
func TestFetch_ATimeoutIsNotRetried(t *testing.T) {
	var hits atomic.Int32
	blocked := make(chan struct{})
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-blocked
	}))
	defer func() { close(blocked); srv.Close() }()

	f := testFetcher()
	f.Timeout = 50 * time.Millisecond
	waits := newFakeWaits(f.pacing())
	if _, err := f.Fetch(context.Background(), srv.URL, nil); err == nil ||
		!strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if hits.Load() != 1 {
		t.Errorf("requests = %d, want one", hits.Load())
	}
	if got := waits.recorded(); len(got) != 0 {
		t.Errorf("waits = %v, want none", got)
	}
}
