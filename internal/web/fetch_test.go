package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testFetcher returns a fetcher whose policy admits the loopback addresses
// and random ports httptest servers listen on.
func testFetcher() *Fetcher {
	return NewFetcher(Policy{AllowPrivate: true})
}

func TestFetch_Basic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := httptest.NewServer(mux)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := testFetcher().Fetch(context.Background(), srv.URL+"/a", nil)
	if err == nil || !strings.Contains(err.Error(), "redirect cycle") {
		t.Fatalf("err = %v, want redirect cycle", err)
	}
}

func TestFetch_TooManyRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotCookie.Store(r.Header.Get("Cookie"))
		gotAccept.Store(r.Header.Get("Accept"))
		fmt.Fprint(w, "ok")
	}))
	defer other.Close()

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := httptest.NewServer(mux)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "pinned")
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	f := testFetcher()
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
