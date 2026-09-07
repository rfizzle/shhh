package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SearXNG: a metasearch instance the person runs themselves, asked over its
// JSON API. It needs no key, which is the whole reason it is here — a
// machine with no Brave subscription still has a search.
//
// A scraped results page would need no instance at all, and is refused for
// two reasons that outlast any one engine: the markup changes without notice
// and the terms of every engine forbid it. An instance is somebody's own
// machine answering a documented API.
// See docs/capabilities/evidence.md#search-has-more-than-one-backend.

// ErrSearXNGFormat is an instance that will not answer in JSON. It is a
// configuration fact rather than a network one — the instance is up, it just
// has not been told to serve this format — so it is a sentinel the doctor
// matches to say which setting to change.
var ErrSearXNGFormat = errors.New(
	"the SearXNG instance did not answer in JSON: its settings.yml must list json under search.formats")

// searxngResponse is the slice of SearXNG's JSON the tool needs. Its
// snippet field is `content` where Brave's is `description`.
type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

func (s *Searcher) searxngRequest(ctx context.Context, q SearchQuery) (*http.Request, error) {
	if strings.TrimSpace(s.Endpoint) == "" {
		return nil, fmt.Errorf("no SearXNG instance configured: set web.search_url")
	}
	v := url.Values{}
	v.Set("q", searchWords(q))
	v.Set("format", "json")
	if q.Freshness != "" {
		v.Set("time_range", freshnessFor(ProviderSearXNG, q.Freshness))
	}
	if q.Offset > 0 {
		// SearXNG pages from one, and the tool's offset counts from zero.
		v.Set("pageno", strconv.Itoa(q.Offset+1))
	}
	// An instance decides how many results a page holds, so the count the
	// model asked for is not on the wire: it is applied to what came back.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searxngURL(s.Endpoint)+"?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// Some instances refuse a request with no user-agent as a bot. The
	// fetcher's is the same string, so an instance's operator sees one name
	// for everything shhh sends.
	req.Header.Set("User-Agent", userAgent)
	return req, nil
}

// searxngURL is the instance's search endpoint. A URL with no path of its
// own is the instance's root, and the search API lives at /search — the
// alternative is a 404 that says nothing about which of the two the person
// meant to configure.
func searxngURL(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/search"
		return u.String()
	}
	return endpoint
}

// parseSearXNG reads an instance's answer, and tells a JSON body from the
// results page an instance that does not serve JSON hands back instead. The
// page parses as nothing and holds no results, so without this the model
// would be told the search found nothing — which is a fact about the world,
// not about a setting, and it would search again for the same silence.
func parseSearXNG(body []byte, contentType string) ([]SearchResult, error) {
	if isHTML(contentType, body) {
		return nil, ErrSearXNGFormat
	}
	var parsed searxngResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("search response invalid: %w", err)
	}
	results := make([]SearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Description: r.Content})
	}
	return results, nil
}

// isHTML reads the declared type first and the bytes second: an instance
// serving its results page usually says text/html, and one that says nothing
// still opens with a tag.
func isHTML(contentType string, body []byte) bool {
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		switch mt {
		case "text/html", "application/xhtml+xml":
			return true
		}
	}
	return strings.HasPrefix(strings.TrimSpace(string(body)), "<")
}

// CheckSearXNG asks an instance one small question and reports what stopped
// it answering. The instance is a machine the person runs, so the diagnostic
// reaches it rather than reading a setting off it: whether json is among its
// formats is not something this side can know without asking.
func CheckSearXNG(ctx context.Context, endpoint string) error {
	s := &Searcher{Provider: ProviderSearXNG, Endpoint: endpoint}
	_, err := s.Search(ctx, SearchQuery{Query: "shhh", Count: 1})
	return err
}
