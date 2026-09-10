package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const searxngFixture = `{
	"query": "generics",
	"results": [
		{"url": "https://go.dev/doc/tutorial/generics", "title": "Generics", "content": "A tutorial."},
		{"url": "https://go.dev/blog/intro-generics", "title": "Intro to generics", "content": ""}
	]
}`

// The second page of the same query, so a paging test has two pages to tell
// apart rather than one page asked for twice.
const searxngPageTwo = `{
	"query": "generics",
	"results": [
		{"url": "https://go.dev/blog/why-generics", "title": "Why generics", "content": "The case."}
	]
}`

func TestSearXNG_ResultsHaveTheSameThreeFields(t *testing.T) {
	srv, sent := stubSearch(t, searxngFixture)
	s := &Searcher{Provider: ProviderSearXNG, Endpoint: srv.URL + "/search", HTTPClient: fixtureHTTP.Client()}
	results, err := s.Search(context.Background(), SearchQuery{Query: "generics", Count: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := sent().Get("format"); got != "json" {
		t.Errorf("format = %q, want json", got)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results", len(results))
	}
	want := SearchResult{
		Title: "Generics", URL: "https://go.dev/doc/tutorial/generics", Description: "A tutorial.",
	}
	if results[0] != want {
		t.Errorf("first result = %+v, want %+v", results[0], want)
	}
}

// An instance takes no key, and its snippet field is `content` — read into
// the same Description a Brave hit carries, so the tool result reads the same
// whichever backend answered.
func TestSearXNG_NeedsNoKeyButNeedsAnInstance(t *testing.T) {
	s := &Searcher{Provider: ProviderSearXNG}
	_, err := s.Search(context.Background(), SearchQuery{Query: "q"})
	if err == nil || !strings.Contains(err.Error(), "web.search_url") {
		t.Fatalf("err = %v, want the missing instance named with its setting", err)
	}
}

func TestSearXNG_MapsTheParameters(t *testing.T) {
	srv, sent := stubSearch(t, searxngFixture)
	s := &Searcher{Provider: ProviderSearXNG, Endpoint: srv.URL + "/search", HTTPClient: fixtureHTTP.Client()}
	if _, err := s.Search(context.Background(), SearchQuery{
		Query: "generics", Count: 5, Freshness: FreshnessMonth, Site: "go.dev", Offset: 3,
	}); err != nil {
		t.Fatal(err)
	}
	q := sent()
	if got := q.Get("time_range"); got != "month" {
		t.Errorf("time_range = %q, want month", got)
	}
	if got := q.Get("q"); got != "generics site:go.dev" {
		t.Errorf("q = %q, want the site operator folded in", got)
	}
	// Offset counts pages from zero and SearXNG counts them from one.
	if got := q.Get("pageno"); got != "4" {
		t.Errorf("pageno = %q, want 4", got)
	}
}

// The offset is the next page of the same query and not the same page again.
func TestSearXNG_OffsetPagesOn(t *testing.T) {
	srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pageno") == "2" {
			fmt.Fprint(w, searxngPageTwo)
			return
		}
		fmt.Fprint(w, searxngFixture)
	}))
	defer srv.Close()

	s := &Searcher{Provider: ProviderSearXNG, Endpoint: srv.URL + "/search", HTTPClient: fixtureHTTP.Client()}
	first, err := s.Search(context.Background(), SearchQuery{Query: "generics", Count: 5})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Search(context.Background(), SearchQuery{Query: "generics", Count: 5, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].URL == first[0].URL {
		t.Fatalf("the second page repeated the first: %+v", second)
	}
}

// An instance that has not been told to serve JSON hands back its results
// page. It is a configuration fact with a fix, not an empty search.
func TestSearXNG_AnHTMLAnswerIsAConfigurationFault(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{"declared html", "text/html; charset=utf-8", "<!doctype html><html><body>results</body></html>", 200},
		{"undeclared html", "", "<!doctype html><html><body>results</body></html>", 200},
		{"the format is refused outright", "text/plain", "Forbidden", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			err := checkSearXNG(context.Background(), srv.URL+"/search", fixtureHTTP.Client())
			if !errors.Is(err, ErrSearXNGFormat) {
				t.Fatalf("err = %v, want the format fault", err)
			}
			if !strings.Contains(err.Error(), "search.formats") {
				t.Errorf("the fault does not name the setting: %v", err)
			}
		})
	}
}

func TestCheckSearXNG_PassesAnInstanceThatAnswersJSON(t *testing.T) {
	srv, _ := stubSearch(t, searxngFixture)
	if err := checkSearXNG(context.Background(), srv.URL+"/search", fixtureHTTP.Client()); err != nil {
		t.Fatalf("a working instance failed its check: %v", err)
	}
}

// A URL with no path of its own is the instance's root, and the API is at
// /search. A URL that already names a path is left as it was written.
func TestSearxngURL_FillsInTheSearchPath(t *testing.T) {
	for in, want := range map[string]string{
		"https://searx.example":         "https://searx.example/search",
		"https://searx.example/":        "https://searx.example/search",
		"https://searx.example/search":  "https://searx.example/search",
		"https://searx.example/s/found": "https://searx.example/s/found",
	} {
		if got := searxngURL(in); got != want {
			t.Errorf("searxngURL(%q) = %q, want %q", in, got, want)
		}
	}
}
