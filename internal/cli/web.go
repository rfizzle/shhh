package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/web"
)

// openWebTools builds the guarded web toolset `shhh code` registers:
// the SSRF-guarded fetcher with its response cache, and — only when a search
// API key is configured — the search tool. A missing cache directory just
// disables caching; an unknown search provider disables search with a
// warning.
func openWebTools(cfg config.Config) *web.Toolset {
	// The two host lists reach the fetcher as well as the approval policy,
	// and for the one thing only the fetcher can see: a redirect. A denied
	// host is refused at every hop, and a hop that starts on a granted host
	// may not end on one nobody has answered for
	// (docs/capabilities/approvals-and-safety.md#a-host-is-granted-once).
	deny := cfg.Web.DenyHosts
	fetcher := web.NewFetcher(web.Policy{
		AllowPrivate: cfg.Web.AllowPrivate,
		DenyHost:     func(host string) bool { return agent.HostMatches(deny, host) },
	})
	// The standing grants are in force before any session says anything;
	// an interactive session replaces this as [a] adds to it.
	allow := cfg.Web.AllowHosts
	fetcher.SetGrantedHosts(func(host string) bool { return agent.HostMatches(allow, host) })
	if cfg.Web.FetchMaxBytes > 0 {
		fetcher.MaxBodyBytes = cfg.Web.FetchMaxBytes
	}
	if cfg.Web.FetchTimeoutSeconds > 0 {
		fetcher.Timeout = time.Duration(cfg.Web.FetchTimeoutSeconds) * time.Second
	}

	ttl := web.DefaultCacheTTL
	if cfg.Web.CacheTTLMinutes > 0 {
		ttl = time.Duration(cfg.Web.CacheTTLMinutes) * time.Minute
	}
	if base, err := storage.Dir(); err == nil {
		if cache, err := web.OpenCache(filepath.Join(base, "webcache"), ttl); err == nil {
			fetcher.Cache = cache
		}
	}

	// Each backend is registered on what it needs and nothing else: Brave on
	// its key, a SearXNG instance on its URL. A backend named without what
	// it needs leaves search unregistered rather than registering a tool
	// whose every call fails
	// (docs/capabilities/evidence.md#search-has-more-than-one-backend).
	var searcher *web.Searcher
	switch cfg.Web.SearchProvider {
	case "", web.ProviderBrave:
		if key := cfg.WebSearchAPIKey(); key != "" {
			searcher = &web.Searcher{APIKey: key}
		}
	case web.ProviderSearXNG:
		if url := strings.TrimSpace(cfg.Web.SearchURL); url != "" {
			searcher = &web.Searcher{Provider: web.ProviderSearXNG, Endpoint: url}
		} else {
			fmt.Fprintln(os.Stderr, "warning: web.search_provider is searxng but web.search_url names no instance; web_search disabled")
		}
	default:
		fmt.Fprintf(os.Stderr, "warning: unknown web.search_provider %q (supported: %s, %s); web_search disabled\n",
			cfg.Web.SearchProvider, web.ProviderBrave, web.ProviderSearXNG)
	}

	ts := web.NewToolset(fetcher, searcher)
	// PATH is probed once here, with the structural tools' probes, rather
	// than on the fetch that turns out to be a PDF: the answer cannot change
	// while the session runs, and the fetch that needs it is already the
	// slowest call in the session.
	ts.PDFText = web.DetectPDFText()
	return ts
}

// scrubWebCache hands the session's rewrite to the response cache. The cache
// is the one copy of a fetched page written from inside the fetcher — below
// the door that scrubs what the model reads and what the evidence store
// keeps — so a page holding a vaulted value, and a URL carrying a token in
// its query string, would otherwise sit under the state directory in the
// clear for the length of the TTL.
//
// It takes the same function the evidence store does, so the cached page and
// the stored page say the same thing, and it is called where the vault opens
// rather than where the cache is built: a session does not know what its
// secrets are until then.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func scrubWebCache(ts *web.Toolset, scrub func(string) string) {
	if ts == nil || ts.Fetcher == nil {
		return
	}
	// A nil cache is a session with nowhere to keep one; SetScrub is safe on
	// it.
	ts.Fetcher.Cache.SetScrub(scrub)
}

// recordSearches points a session's web tools at its record, so every search
// lands beside the rest of what the session did under the backend that
// answered it — and under nothing else: the query stays in the session's own
// sources ledger.
//
// It is one function rather than a line at each surface for the reason the
// gate's is: three surfaces build a web toolset today, and a surface that
// searched and recorded nothing produces a rate over the surfaces that
// remembered.
func recordSearches(ts *web.Toolset, rec *observeRecorder) {
	if ts == nil {
		return
	}
	// A session that is not recording leaves the hook nil, which the toolset
	// reads as "record nothing".
	ts.UseObserver(observe.SearchHook(rec.observer()))
}
