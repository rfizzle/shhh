package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DefaultCacheTTL is how long a cached response stays fresh; config overrides
// it (web.cache_ttl_minutes).
const DefaultCacheTTL = time.Hour

// Cache is a content-addressed on-disk response cache: entries are keyed by
// the SHA-256 of the requested URL, carry a fetch timestamp, and expire after
// the TTL. Files are user-only (0700 dirs / 0600 files), like the evidence
// store.
type Cache struct {
	dir string
	ttl time.Duration
}

type cacheMeta struct {
	URL         string    `json:"url"`
	FinalURL    string    `json:"final_url"`
	Status      int       `json:"status"`
	ContentType string    `json:"content_type"`
	Truncated   bool      `json:"truncated,omitempty"`
	Fetched     time.Time `json:"fetched"`
}

// OpenCache opens (creating if needed) the cache directory and prunes expired
// entries.
func OpenCache(dir string, ttl time.Duration) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	c := &Cache{dir: dir, ttl: ttl}
	c.Prune()
	return c, nil
}

func cacheKey(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) metaPath(key string) string { return filepath.Join(c.dir, key+".json") }
func (c *Cache) bodyPath(key string) string { return filepath.Join(c.dir, key+".dat") }

// Get returns the cached response for a URL when a fresh entry exists.
func (c *Cache) Get(url string) (Result, bool) {
	key := cacheKey(url)
	data, err := os.ReadFile(c.metaPath(key))
	if err != nil {
		return Result{}, false
	}
	var meta cacheMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Result{}, false
	}
	if time.Since(meta.Fetched) > c.ttl {
		return Result{}, false
	}
	body, err := os.ReadFile(c.bodyPath(key))
	if err != nil {
		return Result{}, false
	}
	return Result{
		FinalURL:    meta.FinalURL,
		Status:      meta.Status,
		ContentType: meta.ContentType,
		Body:        body,
		Truncated:   meta.Truncated,
	}, true
}

// Put stores a response under both the requested and the final URL's key, so
// a later fetch of either hits. Failures are ignored: the cache is hygiene,
// never correctness.
func (c *Cache) Put(requestedURL, finalURL string, res Result) {
	meta := cacheMeta{
		URL:         requestedURL,
		FinalURL:    res.FinalURL,
		Status:      res.Status,
		ContentType: res.ContentType,
		Truncated:   res.Truncated,
		Fetched:     time.Now().UTC(),
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return
	}
	keys := []string{cacheKey(requestedURL)}
	if finalURL != "" && finalURL != requestedURL {
		keys = append(keys, cacheKey(finalURL))
	}
	for _, key := range keys {
		if err := os.WriteFile(c.bodyPath(key), res.Body, 0o600); err != nil {
			continue
		}
		_ = os.WriteFile(c.metaPath(key), data, 0o600)
	}
}

// Prune removes expired entries (and orphaned bodies).
func (c *Cache) Prune() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		key := name[:len(name)-len(".json")]
		data, err := os.ReadFile(c.metaPath(key))
		var meta cacheMeta
		expired := err != nil || json.Unmarshal(data, &meta) != nil || time.Since(meta.Fetched) > c.ttl
		if expired {
			_ = os.Remove(c.metaPath(key))
			_ = os.Remove(c.bodyPath(key))
		}
	}
	// Orphaned bodies (no meta) are stale by definition.
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".dat" {
			continue
		}
		key := name[:len(name)-len(".dat")]
		if _, err := os.Stat(c.metaPath(key)); os.IsNotExist(err) {
			_ = os.Remove(c.bodyPath(key))
		}
	}
}
