package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/testhttp"
)

const braveFixture = `{
	"web": {
		"results": [
			{"title": "Go", "url": "https://go.dev/", "description": "The Go programming language."},
			{"title": "Go docs", "url": "https://go.dev/doc/", "description": "Documentation."},
			{"title": "Go blog", "url": "https://go.dev/blog/", "description": ""}
		]
	}
}`

func TestSearcher_Search(t *testing.T) {
	var gotToken, gotQuery, gotCount string
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Subscription-Token")
		gotQuery = r.URL.Query().Get("q")
		gotCount = r.URL.Query().Get("count")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, braveFixture)
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "test-key", Endpoint: srv.URL, HTTPClient: fixtureHTTP.Client()}
	results, err := s.Search(context.Background(), SearchQuery{Query: "golang", Count: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotToken != "test-key" {
		t.Errorf("token = %q", gotToken)
	}
	if gotQuery != "golang" || gotCount != "3" {
		t.Errorf("query/count = %q/%q", gotQuery, gotCount)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Title != "Go" || results[0].URL != "https://go.dev/" {
		t.Errorf("first result = %+v", results[0])
	}
}

func TestSearcher_CountClampedAndBounded(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("count"); got != "10" {
			t.Errorf("count = %q, want clamped to 10", got)
		}
		fmt.Fprint(w, braveFixture)
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "k", Endpoint: srv.URL, HTTPClient: fixtureHTTP.Client()}
	if _, err := s.Search(context.Background(), SearchQuery{Query: "q", Count: 99}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(context.Background(), SearchQuery{Query: "q"}); err != nil {
		t.Fatal(err)
	}
}

func TestSearcher_MissingKey(t *testing.T) {
	s := &Searcher{}
	if _, err := s.Search(context.Background(), SearchQuery{Query: "q", Count: 5}); err == nil {
		t.Fatal("expected missing-key error")
	}
}

func TestSearcher_ProviderError(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "bad", Endpoint: srv.URL, HTTPClient: fixtureHTTP.Client()}
	_, err := s.Search(context.Background(), SearchQuery{Query: "q", Count: 5})
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("err = %v, want status 401", err)
	}
}

func TestSearcher_InvalidJSON(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "k", Endpoint: srv.URL, HTTPClient: fixtureHTTP.Client()}
	if _, err := s.Search(context.Background(), SearchQuery{Query: "q", Count: 5}); err == nil {
		t.Fatal("expected parse error")
	}
}

// stubSearch answers every request from the fixture and hands back what the
// query string carried, which is the whole of what a mapping test checks:
// the parameter reached the wire under the backend's own spelling.
func stubSearch(t *testing.T, body string) (*testhttp.Server, func() url.Values) {
	t.Helper()
	var sent url.Values
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() url.Values { return sent }
}

// Brave's own spelling of the three parameters: freshness is a two-letter
// code, a site is the query operator because the API has no field for one,
// and offset is a page index.
func TestSearcher_BraveMapsTheParameters(t *testing.T) {
	srv, sent := stubSearch(t, braveFixture)
	s := &Searcher{APIKey: "k", Endpoint: srv.URL, HTTPClient: fixtureHTTP.Client()}
	if _, err := s.Search(context.Background(), SearchQuery{
		Query: "generics", Count: 5, Freshness: FreshnessWeek, Site: "go.dev", Offset: 2,
	}); err != nil {
		t.Fatal(err)
	}
	q := sent()
	if got := q.Get("freshness"); got != "pw" {
		t.Errorf("freshness = %q, want pw", got)
	}
	if got := q.Get("q"); got != "generics site:go.dev" {
		t.Errorf("q = %q, want the site operator folded in", got)
	}
	if got := q.Get("offset"); got != "2" {
		t.Errorf("offset = %q, want 2", got)
	}
}

// The four ages each have a code, and nothing else does.
func TestFreshnessFor_MapsTheFourAgesAndRefusesTheRest(t *testing.T) {
	for age, want := range map[string]string{
		FreshnessDay: "pd", FreshnessWeek: "pw", FreshnessMonth: "pm", FreshnessYear: "py",
	} {
		if got := freshnessFor(ProviderBrave, age); got != want {
			t.Errorf("brave %s = %q, want %q", age, got, want)
		}
		if got := freshnessFor(ProviderSearXNG, age); got != age {
			t.Errorf("searxng %s = %q, want %q", age, got, age)
		}
	}
	for _, age := range []string{"hour", "decade", ""} {
		if got := freshnessFor(ProviderBrave, age); got != "" {
			t.Errorf("brave %q = %q, want no code", age, got)
		}
		if got := freshnessFor(ProviderSearXNG, age); got != "" {
			t.Errorf("searxng %q = %q, want no code", age, got)
		}
	}
}

// A parameter a backend cannot express is refused by name, so the model can
// drop it and ask again. The refusal is the whole answer: nothing is sent.
func TestSearcher_RefusesByName(t *testing.T) {
	brave := &Searcher{APIKey: "k"}
	got := brave.Refuse(SearchQuery{Query: "q", Freshness: "hour"})
	if !strings.Contains(got, "freshness") || !strings.Contains(got, "hour") {
		t.Errorf("refusal does not name the parameter and its value: %q", got)
	}
	got = brave.Refuse(SearchQuery{Query: "q", Offset: braveMaxOffset + 1})
	if !strings.Contains(got, "offset") {
		t.Errorf("refusal does not name offset: %q", got)
	}
	if got := brave.Refuse(SearchQuery{Query: "q", Offset: braveMaxOffset, Freshness: FreshnessDay, Site: "go.dev"}); got != "" {
		t.Errorf("a query brave can ask whole was refused: %q", got)
	}
	// A SearXNG instance pages as far as it is asked to, so the ceiling is
	// Brave's and not the tool's.
	searx := &Searcher{Provider: ProviderSearXNG, Endpoint: "https://searx.example/search"}
	if got := searx.Refuse(SearchQuery{Query: "q", Offset: braveMaxOffset + 1}); got != "" {
		t.Errorf("searxng refused an offset it can page to: %q", got)
	}
}

// A refused query never reaches the backend: the request is not made at all,
// rather than made without the parameter.
func TestSearcher_ARefusedQueryIsNotSent(t *testing.T) {
	var asked bool
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = true
		fmt.Fprint(w, braveFixture)
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "k", Endpoint: srv.URL, HTTPClient: fixtureHTTP.Client()}
	if _, err := s.Search(context.Background(), SearchQuery{Query: "q", Freshness: "hour"}); err == nil {
		t.Fatal("expected a refusal")
	}
	if asked {
		t.Error("a refused search still left the machine")
	}
}

func TestSearcher_Name(t *testing.T) {
	if got := (&Searcher{}).Name(); got != ProviderBrave {
		t.Errorf("an unset provider is %q, want %q", got, ProviderBrave)
	}
	if got := (&Searcher{Provider: ProviderSearXNG}).Name(); got != ProviderSearXNG {
		t.Errorf("Name = %q", got)
	}
}

func TestSearcher_UnknownProvider(t *testing.T) {
	s := &Searcher{Provider: "duckduckgo"}
	_, err := s.Search(context.Background(), SearchQuery{Query: "q"})
	if err == nil || !strings.Contains(err.Error(), "duckduckgo") {
		t.Fatalf("err = %v, want the unknown provider named", err)
	}
}
