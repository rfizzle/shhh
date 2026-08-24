package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Subscription-Token")
		gotQuery = r.URL.Query().Get("q")
		gotCount = r.URL.Query().Get("count")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, braveFixture)
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "test-key", Endpoint: srv.URL}
	results, err := s.Search(context.Background(), "golang", 3)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("count"); got != "10" {
			t.Errorf("count = %q, want clamped to 10", got)
		}
		fmt.Fprint(w, braveFixture)
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "k", Endpoint: srv.URL}
	if _, err := s.Search(context.Background(), "q", 99); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(context.Background(), "q", 0); err != nil {
		t.Fatal(err)
	}
}

func TestSearcher_MissingKey(t *testing.T) {
	s := &Searcher{}
	if _, err := s.Search(context.Background(), "q", 5); err == nil {
		t.Fatal("expected missing-key error")
	}
}

func TestSearcher_ProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "bad", Endpoint: srv.URL}
	_, err := s.Search(context.Background(), "q", 5)
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("err = %v, want status 401", err)
	}
}

func TestSearcher_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	s := &Searcher{APIKey: "k", Endpoint: srv.URL}
	if _, err := s.Search(context.Background(), "q", 5); err == nil {
		t.Fatal("expected parse error")
	}
}
