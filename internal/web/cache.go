package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// mu guards the scrub, which a session installs while the fetches of a
	// round may already be writing entries on other goroutines.
	mu    sync.Mutex
	scrub func(string) string
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

// SetScrub installs the rewrite an entry goes through before it is written.
// A cached page is a copy that outlives the turn — a file under the state
// directory for the TTL — and it is written from inside the fetcher, below
// the door that scrubs what the model reads, so a vaulted value reaching one
// has leaked in the way nothing on screen would report. The URL is scrubbed
// with the body: what was asked for carries whatever the model put in its
// query string.
//
// It is the same rewrite the evidence store is given rather than one of its
// own, so the page in the cache and the page in the store say the same
// thing. Nil is a session that scrubs nothing, and it is safe on a nil
// Cache, which is a session with nowhere to keep one.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func (c *Cache) SetScrub(scrub func(string) string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.scrub = scrub
	c.mu.Unlock()
}

func cacheKey(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) metaPath(key string) string { return filepath.Join(c.dir, key+".json") }
func (c *Cache) bodyPath(key string) string { return filepath.Join(c.dir, key+".dat") }

// Get returns the cached response for a URL when a fresh entry exists.
//
// Freshness is read from the entry's own timestamp, because this is the
// answer a fetch is about to be given: an entry whose meta says it is stale
// is stale whatever the directory says about the file. The sweep, which only
// has to be approximately right, is the cheap way round (see Prune).
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
//
// What is stored goes through the session's scrub first; what it is keyed by
// does not. The key is a SHA-256, which carries nothing back to anyone
// reading the directory, and keying on the scrubbed form would mean the next
// fetch of the same URL missed the entry it had just written.
func (c *Cache) Put(requestedURL, finalURL string, res Result) {
	c.mu.Lock()
	scrub := c.scrub
	c.mu.Unlock()

	requested, final, body := requestedURL, res.FinalURL, res.Body
	if scrub != nil {
		requested, final = scrub(requestedURL), scrub(res.FinalURL)
		// Only a text body is rewritten. A byte-wise substitution inside a
		// PDF or an image is corruption rather than hygiene — the offsets
		// around it are part of the format — and the text such a page is
		// read as goes through this same rewrite where it is extracted, on
		// the way to the model and to the evidence store.
		if textualBody(res.ContentType) {
			body = []byte(scrub(string(res.Body)))
		}
	}

	meta := cacheMeta{
		URL:         requested,
		FinalURL:    final,
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
		if err := os.WriteFile(c.bodyPath(key), body, 0o600); err != nil {
			continue
		}
		_ = os.WriteFile(c.metaPath(key), data, 0o600)
	}
}

// textualBody reports whether a body is text — the media types a fetch reads
// as prose rather than declines as binary.
func textualBody(contentType string) bool {
	mediaType := contentType
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		mediaType = mt
	}
	switch {
	case mediaType == "":
		// A response that named no type is treated as text, because an
		// unlabelled body is otherwise the way around this door.
		return true
	case strings.HasPrefix(mediaType, "text/"):
		return true
	case mediaType == "application/json" || mediaType == "application/xml" ||
		mediaType == "application/xhtml+xml":
		return true
	case strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml"):
		return true
	}
	return false
}

// Prune removes expired entries (and orphaned bodies).
//
// Expiry is decided from the directory entry's modification time rather than
// from the timestamp inside the entry. An entry is written once and never
// touched again, so the two say the same thing — and this runs on the path
// that opens a session, where the difference is a stat per entry against
// opening, reading and JSON-parsing every file in the directory before
// anything paints. An entry whose meta is unreadable is therefore left where
// it is rather than swept: Get misses on it, and the sweep past its TTL takes
// it away with the rest.
func (c *Cache) Prune() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	fresh := make(map[string]bool, len(entries))
	var bodies []string
	for _, e := range entries {
		name := e.Name()
		switch filepath.Ext(name) {
		case ".json":
			key := strings.TrimSuffix(name, ".json")
			info, err := e.Info()
			if err != nil || time.Since(info.ModTime()) > c.ttl {
				_ = os.Remove(c.metaPath(key))
				_ = os.Remove(c.bodyPath(key))
				continue
			}
			fresh[key] = true
		case ".dat":
			bodies = append(bodies, strings.TrimSuffix(name, ".dat"))
		}
	}
	// Orphaned bodies — no meta, or one this sweep has just removed — are
	// stale by definition. They are collected rather than statted because
	// the directory listing has already said which keys have a meta.
	for _, key := range bodies {
		if !fresh[key] {
			_ = os.Remove(c.bodyPath(key))
		}
	}
}
