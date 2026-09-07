package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
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

	var searcher *web.Searcher
	if key := cfg.WebSearchAPIKey(); key != "" {
		switch cfg.Web.SearchProvider {
		case "", "brave":
			searcher = &web.Searcher{APIKey: key}
		default:
			fmt.Fprintf(os.Stderr, "warning: unknown web.search_provider %q (supported: brave); web_search disabled\n", cfg.Web.SearchProvider)
		}
	}

	ts := web.NewToolset(fetcher, searcher)
	// PATH is probed once here, with the structural tools' probes, rather
	// than on the fetch that turns out to be a PDF: the answer cannot change
	// while the session runs, and the fetch that needs it is already the
	// slowest call in the session.
	ts.PDFText = web.DetectPDFText()
	return ts
}
