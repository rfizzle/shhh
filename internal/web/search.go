package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Search backends: Brave, a paid API that needs a key, and a SearXNG
// instance the person runs themselves, which needs none. The tool is
// registered only when one of them is configured — no backend, no tool.
// See docs/capabilities/evidence.md#search-has-more-than-one-backend.
const (
	ProviderBrave   = "brave"
	ProviderSearXNG = "searxng"
)

const (
	braveEndpoint      = "https://api.search.brave.com/res/v1/web/search"
	maxSearchResults   = 10
	searchTimeout      = 15 * time.Second
	maxSearchBodyBytes = 1 << 20
	// braveMaxOffset is how far Brave pages. Its offset is an index into
	// pages of `count` and the API refuses anything above this, so a request
	// past it is a parameter the backend cannot express rather than a page
	// that happens to be empty.
	braveMaxOffset = 9
)

// The ages a search may be narrowed to. They are shhh's words rather than
// any backend's: Brave spells them pd/pw/pm/py and SearXNG spells them as
// they are, and a tool parameter that changed spelling with the configured
// backend would make the model's call depend on a setting it cannot see.
const (
	FreshnessDay   = "day"
	FreshnessWeek  = "week"
	FreshnessMonth = "month"
	FreshnessYear  = "year"
)

// SearchResult is one web search hit.
type SearchResult struct {
	Title       string
	URL         string
	Description string
}

// SearchQuery is one search as the model asked for it: the words, how many
// results it wants back, and the three parameters that narrow the question.
// Each backend maps them onto its own spelling, and refuses by name what it
// cannot express.
type SearchQuery struct {
	Query string
	// Count is how many results to return, clamped to maxSearchResults.
	Count int
	// Freshness limits results by age, in the words above; "" is no limit.
	Freshness string
	// Site limits results to one host's pages. Neither backend has a
	// parameter for it — both take the `site:` operator in the query — so
	// this is folded into the words rather than sent beside them.
	Site string
	// Offset is which page of results to return, counting from zero. It is
	// pages and not results because that is what both backends page in, and
	// a result index would have to be divided by a page size the caller
	// cannot see.
	Offset int
}

// Searcher queries the configured search backend.
type Searcher struct {
	// Provider is the backend to ask: ProviderBrave (the default) or
	// ProviderSearXNG.
	Provider string
	// APIKey is Brave's subscription token. A SearXNG instance takes none.
	APIKey string
	// Endpoint is the SearXNG instance's search URL, and overrides Brave's
	// endpoint in tests.
	Endpoint string
	// HTTPClient overrides the client (tests); nil uses a bounded default.
	HTTPClient *http.Client
}

// Name is the backend this searcher asks, as the record and the diagnostics
// spell it. An unset provider is Brave, which is what shhh had before there
// was a second one.
func (s *Searcher) Name() string {
	if s.Provider == "" {
		return ProviderBrave
	}
	return s.Provider
}

func (s *Searcher) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: searchTimeout}
}

// braveURL is the endpoint Brave is asked at; a set Endpoint overrides it,
// which is how the suite points a search at a stub.
func (s *Searcher) braveURL() string {
	if s.Endpoint != "" {
		return s.Endpoint
	}
	return braveEndpoint
}

// Refuse is the sentence a search is turned down with when the configured
// backend cannot express one of its parameters, and "" for a query it can
// ask whole. It names the parameter, because the alternative is sending the
// search without it: a question narrowed to last week that comes back
// unnarrowed is a page of year-old results the model reads as this week's,
// and nothing on the way back says otherwise.
//
// The tool asks this before it calls Search, so a search that never left the
// machine is not recorded as one that did.
// See docs/capabilities/evidence.md#a-search-is-refused-rather-than-widened.
func (s *Searcher) Refuse(q SearchQuery) string {
	var bad []string
	if q.Freshness != "" && freshnessFor(s.Name(), q.Freshness) == "" {
		bad = append(bad, "freshness "+strconv.Quote(q.Freshness))
	}
	if s.Name() == ProviderBrave && q.Offset > braveMaxOffset {
		bad = append(bad, fmt.Sprintf("offset %d (it pages no further than %d)", q.Offset, braveMaxOffset))
	}
	if len(bad) == 0 {
		return ""
	}
	return s.Name() + " cannot search by " + strings.Join(bad, " or ") +
		": drop the parameter and search again, or narrow the query itself"
}

// freshnessFor maps one of shhh's four ages onto a backend's spelling, and
// returns "" for an age that backend has no equivalent for.
func freshnessFor(provider, age string) string {
	switch provider {
	case ProviderBrave:
		// Brave's freshness codes: past day, week, month, year.
		switch age {
		case FreshnessDay:
			return "pd"
		case FreshnessWeek:
			return "pw"
		case FreshnessMonth:
			return "pm"
		case FreshnessYear:
			return "py"
		}
	case ProviderSearXNG:
		// SearXNG's time_range takes shhh's own four words.
		switch age {
		case FreshnessDay, FreshnessWeek, FreshnessMonth, FreshnessYear:
			return age
		}
	}
	return ""
}

// searchWords is the query as it goes on the wire: the model's words, plus
// the `site:` operator when it asked for one host. Neither backend has a
// field for it, and both pass the operator through to the engines they front.
func searchWords(q SearchQuery) string {
	words := strings.TrimSpace(q.Query)
	if site := strings.TrimSpace(q.Site); site != "" {
		words += " site:" + site
	}
	return words
}

// Search runs one query against the configured backend and returns at most
// q.Count results (clamped to maxSearchResults).
func (s *Searcher) Search(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	if refusal := s.Refuse(q); refusal != "" {
		return nil, errors.New(refusal)
	}
	count := q.Count
	if count < 1 || count > maxSearchResults {
		count = maxSearchResults
	}

	var req *http.Request
	var err error
	switch s.Name() {
	case ProviderSearXNG:
		req, err = s.searxngRequest(ctx, q)
	case ProviderBrave:
		req, err = s.braveRequest(ctx, q, count)
	default:
		return nil, fmt.Errorf("unknown search provider %q", s.Name())
	}
	if err != nil {
		return nil, err
	}

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
		// An instance whose formats do not list json refuses the request
		// outright rather than answering in another format, so the two
		// shapes of "no JSON here" arrive as one fact with one fix.
		if s.Name() == ProviderSearXNG && resp.StatusCode == http.StatusForbidden {
			return nil, ErrSearXNGFormat
		}
		return nil, fmt.Errorf("search provider returned status %d", resp.StatusCode)
	}

	var results []SearchResult
	if s.Name() == ProviderSearXNG {
		results, err = parseSearXNG(body, resp.Header.Get("Content-Type"))
	} else {
		results, err = parseBrave(body)
	}
	if err != nil {
		return nil, err
	}
	if len(results) > count {
		results = results[:count]
	}
	return results, nil
}

func (s *Searcher) braveRequest(ctx context.Context, q SearchQuery, count int) (*http.Request, error) {
	if s.APIKey == "" {
		return nil, fmt.Errorf("no search API key configured")
	}
	v := url.Values{}
	v.Set("q", searchWords(q))
	v.Set("count", strconv.Itoa(count))
	if q.Freshness != "" {
		v.Set("freshness", freshnessFor(ProviderBrave, q.Freshness))
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.braveURL()+"?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", s.APIKey)
	return req, nil
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

func parseBrave(body []byte) ([]SearchResult, error) {
	var parsed braveResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("search response invalid: %w", err)
	}
	results := make([]SearchResult, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Description: r.Description})
	}
	return results, nil
}
