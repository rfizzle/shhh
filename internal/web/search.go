package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Search provider (S-066): one provider first — Brave Search. The tool is
// registered only when web.search_api_key is configured; no key, no tool.
const (
	braveEndpoint      = "https://api.search.brave.com/res/v1/web/search"
	maxSearchResults   = 10
	searchTimeout      = 15 * time.Second
	maxSearchBodyBytes = 1 << 20
)

// SearchResult is one web search hit.
type SearchResult struct {
	Title       string
	URL         string
	Description string
}

// Searcher queries the configured search provider.
type Searcher struct {
	APIKey string
	// Endpoint overrides the provider URL (tests); empty means Brave.
	Endpoint string
	// HTTPClient overrides the client (tests); nil uses a bounded default.
	HTTPClient *http.Client
}

func (s *Searcher) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: searchTimeout}
}

func (s *Searcher) endpoint() string {
	if s.Endpoint != "" {
		return s.Endpoint
	}
	return braveEndpoint
}

// braveResponse is the slice of Brave's response shape the tool needs.
type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// Search runs one query and returns at most count results (clamped to
// maxSearchResults).
func (s *Searcher) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if s.APIKey == "" {
		return nil, fmt.Errorf("no search API key configured")
	}
	if count < 1 || count > maxSearchResults {
		count = maxSearchResults
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("count", strconv.Itoa(count))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint()+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", s.APIKey)

	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("search response unreadable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search provider returned status %d", resp.StatusCode)
	}
	var parsed braveResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("search response invalid: %w", err)
	}
	results := make([]SearchResult, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		if len(results) >= count {
			break
		}
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Description: r.Description})
	}
	return results, nil
}
