package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolset_Definitions(t *testing.T) {
	withoutSearch := NewToolset(NewFetcher(Policy{}), nil)
	defs := withoutSearch.Definitions()
	if len(defs) != 1 || defs[0].Name != FetchToolName {
		t.Fatalf("without a search key, want only %s; got %d defs", FetchToolName, len(defs))
	}

	withSearch := NewToolset(NewFetcher(Policy{}), &Searcher{APIKey: "k"})
	defs = withSearch.Definitions()
	if len(defs) != 2 || defs[1].Name != SearchToolName {
		t.Fatalf("with a search key, want fetch+search; got %d defs", len(defs))
	}
}

func TestToolset_Has(t *testing.T) {
	ts := NewToolset(NewFetcher(Policy{}), nil)
	if !ts.Has(FetchToolName) {
		t.Error("fetch should be registered")
	}
	if ts.Has(SearchToolName) {
		t.Error("search must not be registered without a key")
	}
	if ts.Has("read_file") {
		t.Error("non-web tool claimed")
	}
}

func TestToolset_ExecuteUnknown(t *testing.T) {
	ts := NewToolset(NewFetcher(Policy{}), nil)
	if _, err := ts.Execute("read_file", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected unknown-tool error")
	}
	if _, err := ts.Execute(SearchToolName, json.RawMessage(`{"query":"x"}`)); err == nil {
		t.Fatal("search without a key must error, not dispatch")
	}
}

func TestToolset_FetchArgsValidation(t *testing.T) {
	ts := NewToolset(NewFetcher(Policy{}), nil)
	if _, err := ts.Execute(FetchToolName, json.RawMessage(`{`)); err == nil {
		t.Error("malformed JSON accepted")
	}
	if _, err := ts.Execute(FetchToolName, json.RawMessage(`{}`)); err == nil {
		t.Error("missing url accepted")
	}
	if _, err := ts.Execute(FetchToolName, json.RawMessage(`{"url":"ftp://x/"}`)); err == nil {
		t.Error("bad scheme accepted")
	}
}

func TestToolset_FetchSummary(t *testing.T) {
	ts := NewToolset(NewFetcher(Policy{}), nil)
	summary, err := ts.FetchSummary(json.RawMessage(`{"url":"https://example.com/doc"}`))
	if err != nil {
		t.Fatalf("FetchSummary: %v", err)
	}
	if summary != "GET https://example.com/doc" {
		t.Errorf("summary = %q", summary)
	}
	if _, err := ts.FetchSummary(json.RawMessage(`{"url":"http://169.254.169.254/"}`)); err == nil {
		t.Error("blocked URL must fail the preview")
	}
	if _, err := ts.FetchSummary(json.RawMessage(`{}`)); err == nil {
		t.Error("missing url must fail the preview")
	}
}

func TestToolset_ExecuteFetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Doc</title></head><body><main><h1>Hello</h1><p>Body text.</p></main></body></html>`)
	}))
	defer srv.Close()

	ts := NewToolset(testFetcher(), nil)
	out, err := ts.Execute(FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"URL: " + srv.URL, "Status: 200", "Content-Type: text/html", "# Doc", "# Hello", "Body text."} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}
}

func TestToolset_ExecuteSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, braveFixture)
	}))
	defer srv.Close()

	ts := NewToolset(NewFetcher(Policy{}), &Searcher{APIKey: "k", Endpoint: srv.URL})
	out, err := ts.Execute(SearchToolName, json.RawMessage(`{"query":"golang"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"3 result(s)", "1. Go", "https://go.dev/", "The Go programming language."} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}

	if _, err := ts.Execute(SearchToolName, json.RawMessage(`{}`)); err == nil {
		t.Error("missing query accepted")
	}
}

func TestToolset_WrapExecutor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "plain")
	}))
	defer srv.Close()

	ts := NewToolset(testFetcher(), nil)
	nextCalled := false
	exec := ts.WrapExecutor(func(name string, args json.RawMessage) (string, error) {
		nextCalled = true
		return "next:" + name, nil
	})

	out, err := exec(FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("exec fetch: %v", err)
	}
	if nextCalled {
		t.Error("web tool leaked to the next executor")
	}
	if !strings.Contains(out, "plain") {
		t.Errorf("fetch output = %q", out)
	}

	out, err = exec("read_file", json.RawMessage(`{}`))
	if err != nil || out != "next:read_file" {
		t.Errorf("fallthrough = %q, %v", out, err)
	}
	if !nextCalled {
		t.Error("next executor not reached for non-web tool")
	}
}

func TestFormatFetchResult(t *testing.T) {
	jsonRes := Result{FinalURL: "https://api.example.com/x", Status: 200, ContentType: "application/json", Body: []byte(`{"a":1}`)}
	out := FormatFetchResult(jsonRes)
	if !strings.Contains(out, `{"a":1}`) {
		t.Errorf("json passthrough missing:\n%s", out)
	}

	binary := Result{FinalURL: "https://example.com/img", Status: 200, ContentType: "image/png", Body: make([]byte, 10)}
	out = FormatFetchResult(binary)
	if !strings.Contains(out, "not rendered") {
		t.Errorf("binary note missing:\n%s", out)
	}

	cached := Result{FinalURL: "https://example.com/", Status: 200, ContentType: "text/plain", Body: []byte("x"), FromCache: true}
	if out = FormatFetchResult(cached); !strings.Contains(out, "(cached)") {
		t.Errorf("cache note missing:\n%s", out)
	}

	truncated := Result{FinalURL: "https://example.com/", Status: 200, ContentType: "text/plain", Body: []byte("x"), Truncated: true}
	if out = FormatFetchResult(truncated); !strings.Contains(out, "truncated") {
		t.Errorf("truncation note missing:\n%s", out)
	}
}

func TestFormatFetchResult_InlineBound(t *testing.T) {
	big := Result{
		FinalURL:    "https://example.com/big",
		Status:      200,
		ContentType: "text/plain",
		Body:        []byte(strings.Repeat("a", MaxInlineBytes+100)),
	}
	out := FormatFetchResult(big)
	if !strings.Contains(out, "content truncated at inline limit") {
		t.Error("inline truncation notice missing")
	}
	if len(out) > MaxInlineBytes+500 {
		t.Errorf("inline result too large: %d bytes", len(out))
	}
}
