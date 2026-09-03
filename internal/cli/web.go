package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	fetcher := web.NewFetcher(web.Policy{AllowPrivate: cfg.Web.AllowPrivate})
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

	return web.NewToolset(fetcher, searcher)
}
