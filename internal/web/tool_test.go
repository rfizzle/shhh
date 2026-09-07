package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	if _, err := ts.Execute(Orchestrator, "read_file", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected unknown-tool error")
	}
	if _, err := ts.Execute(Orchestrator, SearchToolName, json.RawMessage(`{"query":"x"}`)); err == nil {
		t.Fatal("search without a key must error, not dispatch")
	}
}

func TestToolset_FetchArgsValidation(t *testing.T) {
	ts := NewToolset(NewFetcher(Policy{}), nil)
	if _, err := ts.Execute(Orchestrator, FetchToolName, json.RawMessage(`{`)); err == nil {
		t.Error("malformed JSON accepted")
	}
	if _, err := ts.Execute(Orchestrator, FetchToolName, json.RawMessage(`{}`)); err == nil {
		t.Error("missing url accepted")
	}
	if _, err := ts.Execute(Orchestrator, FetchToolName, json.RawMessage(`{"url":"ftp://x/"}`)); err == nil {
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
	out, err := ts.Execute(Orchestrator, FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`"}`))
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
	out, err := ts.Execute(Orchestrator, SearchToolName, json.RawMessage(`{"query":"golang"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"3 result(s)", "1. Go", "https://go.dev/", "The Go programming language."} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}

	if _, err := ts.Execute(Orchestrator, SearchToolName, json.RawMessage(`{}`)); err == nil {
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
	exec := ts.WrapExecutor(Orchestrator, func(name string, args json.RawMessage) (string, error) {
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
	ts := NewToolset(NewFetcher(Policy{}), nil)
	out := ts.FormatFetchResult(jsonRes)
	if !strings.Contains(out, `{"a":1}`) {
		t.Errorf("json passthrough missing:\n%s", out)
	}

	binary := Result{FinalURL: "https://example.com/img", Status: 200, ContentType: "image/png", Body: make([]byte, 10)}
	out = ts.FormatFetchResult(binary)
	if !strings.Contains(out, "not rendered") {
		t.Errorf("binary note missing:\n%s", out)
	}

	cached := Result{FinalURL: "https://example.com/", Status: 200, ContentType: "text/plain", Body: []byte("x"), FromCache: true}
	if out = ts.FormatFetchResult(cached); !strings.Contains(out, "(cached)") {
		t.Errorf("cache note missing:\n%s", out)
	}

	truncated := Result{FinalURL: "https://example.com/", Status: 200, ContentType: "text/plain", Body: []byte("x"), Truncated: true}
	if out = ts.FormatFetchResult(truncated); !strings.Contains(out, "truncated") {
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
	out := NewToolset(NewFetcher(Policy{}), nil).FormatFetchResult(big)
	if !strings.Contains(out, "content truncated at inline limit") {
		t.Error("inline truncation notice missing")
	}
	if len(out) > MaxInlineBytes+500 {
		t.Errorf("inline result too large: %d bytes", len(out))
	}
}

// keeper is a stand-in for the session's evidence store: it records what was
// stored whole so a test can compare it with what the model was shown.
type keeper struct {
	tool  string
	kept  string
	calls int
	fail  bool
}

func (k *keeper) keep(tool, content string) (string, bool) {
	k.calls++
	if k.fail {
		return "", false
	}
	k.tool, k.kept = tool, content
	return "ev-0123456789abcdef", true
}

// longPage is an HTML document whose readable text comfortably exceeds what
// one result may carry inline.
func longPage() []byte {
	var b strings.Builder
	b.WriteString("<html><head><title>Manual</title></head><body><main>")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "<p>Paragraph %d of the manual, with enough words in it to be worth reading.</p>", i)
	}
	b.WriteString("</main></body></html>")
	return []byte(b.String())
}

// The whole page reaches the store and the conversation carries its opening
// bytes plus the notice that reads on from the cut.
func TestFormatFetchResult_LongPageIsKeptWholeAndPagedBack(t *testing.T) {
	body := longPage()
	k := &keeper{}
	ts := NewToolset(testFetcher(), nil)
	ts.UseEvidence(k.keep, nil)

	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/manual", Status: 200, ContentType: "text/html", Body: body})

	want := ExtractHTML(body).Text
	if k.kept != want {
		t.Fatalf("the store got %d bytes, the page extracts to %d", len(k.kept), len(want))
	}
	if len(k.kept) <= MaxInlineBytes {
		t.Fatalf("the fixture must exceed the inline bound; it is %d bytes", len(k.kept))
	}
	if k.tool != FetchToolName {
		t.Errorf("stored under tool %q", k.tool)
	}
	for _, s := range []string{"ev-0123456789abcdef", "retrieve it with the evidence tool (info/read/search)", "read from offset"} {
		if !strings.Contains(out, s) {
			t.Errorf("the notice is missing %q:\n%s", s, out[max(0, len(out)-400):])
		}
	}
	if strings.Contains(out, "content truncated at inline limit") {
		t.Error("a stored page must not carry the storeless notice")
	}
	// The inline view has to be the opening of the stored bytes, or the
	// offset the notice quotes lands somewhere else in the original.
	cut := out[strings.Index(out, want[:64]):]
	cut = cut[:strings.Index(cut, "\n… [page text cut at")]
	if !strings.HasPrefix(k.kept, cut) {
		t.Fatal("what the model read is not the opening of what was stored")
	}
	if !strings.Contains(out, fmt.Sprintf("read from offset %d", len(cut))) {
		t.Errorf("the notice must resume at the cut (%d bytes)", len(cut))
	}
}

// Without a store — a headless run with no state directory, a test — the cut
// is the end of the page and says so.
func TestFormatFetchResult_WithoutAStoreTheCutStands(t *testing.T) {
	ts := NewToolset(testFetcher(), nil)
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/manual", Status: 200, ContentType: "text/html", Body: longPage()})
	if !strings.Contains(out, "content truncated at inline limit") {
		t.Error("the storeless cut must say it is one")
	}
	if strings.Contains(out, "evidence") {
		t.Error("no store, no evidence id")
	}
}

// A store that will not take the page leaves the cut standing rather than a
// notice naming an entry nobody can read.
func TestFormatFetchResult_ARefusedStoreFallsBackToTheCut(t *testing.T) {
	k := &keeper{fail: true}
	ts := NewToolset(testFetcher(), nil)
	ts.UseEvidence(k.keep, nil)
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/manual", Status: 200, ContentType: "text/html", Body: longPage()})
	if k.calls != 1 {
		t.Fatalf("the store was asked %d times", k.calls)
	}
	if !strings.Contains(out, "content truncated at inline limit") {
		t.Error("a refused store must fall back to the plain cut")
	}
}

// A page that fits needs no entry: an id nothing will ever ask for is store
// space and a prune later.
func TestFormatFetchResult_AShortPageIsNotStored(t *testing.T) {
	k := &keeper{}
	ts := NewToolset(testFetcher(), nil)
	ts.UseEvidence(k.keep, nil)
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/", Status: 200, ContentType: "text/html",
		Body: []byte("<html><body><main><p>Short and complete.</p></main></body></html>")})
	if k.calls != 0 {
		t.Error("a page that fits inline must not be stored")
	}
	if !strings.Contains(out, "Short and complete.") {
		t.Errorf("result = %q", out)
	}
}

// The scrub runs once, before the split, so the stored copy and the inline
// view are the same text.
func TestFormatFetchResult_ScrubsOnceBeforeTheSplit(t *testing.T) {
	k := &keeper{}
	ts := NewToolset(testFetcher(), nil)
	ts.UseEvidence(k.keep, func(s string) string { return strings.ReplaceAll(s, "Paragraph", "[redacted]") })
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/manual", Status: 200, ContentType: "text/html", Body: longPage()})
	if strings.Contains(k.kept, "Paragraph") {
		t.Error("the stored copy was not scrubbed")
	}
	if strings.Contains(out, "Paragraph") {
		t.Error("the inline view was not scrubbed")
	}
	if !strings.Contains(k.kept, "[redacted]") {
		t.Error("the scrub did not run over the stored copy at all")
	}
}

// A large document that yields a handful of words is a shell for a script,
// and saying so is what stops the same URL being fetched again.
func TestFormatFetchResult_ScriptShell(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><head><title>App</title></head><body><main><p>You need to enable JavaScript.</p></main><script>`)
	b.WriteString(strings.Repeat("var x=1;", 4000))
	b.WriteString(`</script></body></html>`)
	body := []byte(b.String())

	k := &keeper{}
	ts := NewToolset(testFetcher(), nil)
	ts.UseEvidence(k.keep, nil)
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/app", Status: 200, ContentType: "text/html", Body: body})

	if !strings.Contains(out, "rendered by script; no readable text") {
		t.Fatalf("verdict missing:\n%s", out)
	}
	if !strings.Contains(out, formatBytes(int64(len(body)))) {
		t.Errorf("the verdict must carry the byte count:\n%s", out)
	}
	if !strings.Contains(out, "# App") {
		t.Error("the title is still worth having")
	}
	if k.calls != 0 {
		t.Error("there is nothing to store")
	}
}

// A short page with little text is a short page, not a script shell.
func TestFormatFetchResult_ShortPageIsNotAScriptShell(t *testing.T) {
	out := NewToolset(testFetcher(), nil).FormatFetchResult(Result{
		FinalURL: "https://example.com/", Status: 200, ContentType: "text/html",
		Body: []byte("<html><body><main><p>Down for maintenance.</p></main></body></html>"),
	})
	if strings.Contains(out, "rendered by script") {
		t.Errorf("a small document is not a shell:\n%s", out)
	}
}

func TestFormatFetchResult_PDFWithAReader(t *testing.T) {
	ts := NewToolset(testFetcher(), nil)
	ts.PDFText = stubPDFText(t, "The specification says the header is eight bytes.")
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/spec.pdf", Status: 200, ContentType: "application/pdf", Body: []byte("%PDF-1.7 ...")})
	if !strings.Contains(out, "the header is eight bytes") {
		t.Fatalf("the PDF's text is missing:\n%s", out)
	}
}

// With no reader the result names the binary, so the model reports a missing
// install instead of fetching the same bytes again.
func TestFormatFetchResult_PDFWithoutAReader(t *testing.T) {
	ts := NewToolset(testFetcher(), nil)
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/spec.pdf", Status: 200, ContentType: "application/pdf", Body: make([]byte, 8192)})
	if !strings.Contains(out, PDFTextBinary) {
		t.Fatalf("the result must name the reader it wanted:\n%s", out)
	}
	if !strings.Contains(out, "Fetching it again will not change that") {
		t.Errorf("the result must close the retry loop:\n%s", out)
	}
}

func TestFormatFetchResult_PDFWithNoTextLayer(t *testing.T) {
	ts := NewToolset(testFetcher(), nil)
	ts.PDFText = stubPDFText(t, "  \n ")
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/scan.pdf", Status: 200, ContentType: "application/pdf", Body: make([]byte, 4096)})
	if !strings.Contains(out, "no text layer") {
		t.Fatalf("a scanned PDF must say why it is empty:\n%s", out)
	}
}

// A long PDF is kept whole and paged like a page.
func TestFormatFetchResult_PDFIsKeptWhole(t *testing.T) {
	text := strings.Repeat("The specification is long. ", 4000)
	k := &keeper{}
	ts := NewToolset(testFetcher(), nil)
	ts.PDFText = stubPDFText(t, text)
	ts.UseEvidence(k.keep, nil)
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/spec.pdf", Status: 200, ContentType: "application/pdf", Body: []byte("%PDF-1.7 ...")})
	if k.kept != text {
		t.Fatalf("the store got %d of %d bytes", len(k.kept), len(text))
	}
	if !strings.Contains(out, "ev-0123456789abcdef") {
		t.Error("the notice is missing from a cut PDF")
	}
}

// The card promises what the session can actually do with the page.
func TestFetchPlan_ReceivesNamesTheStore(t *testing.T) {
	ts := NewToolset(testFetcher(), nil)
	plan, err := ts.FetchPlan(json.RawMessage(`{"url":"https://example.com/doc"}`))
	if err != nil {
		t.Fatalf("FetchPlan: %v", err)
	}
	if strings.Contains(plan.Receives, "evidence store") {
		t.Errorf("without a store the card must not promise one: %q", plan.Receives)
	}

	ts.UseEvidence((&keeper{}).keep, nil)
	plan, err = ts.FetchPlan(json.RawMessage(`{"url":"https://example.com/doc"}`))
	if err != nil {
		t.Fatalf("FetchPlan: %v", err)
	}
	if plan.Receives != "page text, whole, into the evidence store; the first 48 KB into the conversation" {
		t.Errorf("receives = %q", plan.Receives)
	}
}

// The cache keeps what was fetched, not what was extracted, so the second
// read of a long page costs no request and still lands whole in the store.
func TestExecuteFetch_ACachedPageStillPages(t *testing.T) {
	body := longPage()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cache, err := OpenCache(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := testFetcher()
	fetcher.Cache = cache
	k := &keeper{}
	ts := NewToolset(fetcher, nil)
	ts.UseEvidence(k.keep, nil)

	first, err := ts.Execute(Orchestrator, FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	firstKept := k.kept
	second, err := ts.Execute(Orchestrator, FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if requests != 1 {
		t.Fatalf("the second read cost %d requests", requests)
	}
	if !strings.Contains(second, "(cached)") {
		t.Error("the second read must say it came from the cache")
	}
	if k.kept != firstKept || len(k.kept) <= MaxInlineBytes {
		t.Fatal("a cached page must reach the store whole, like a fresh one")
	}
	if !strings.Contains(second, "read from offset") || !strings.Contains(first, "read from offset") {
		t.Error("both reads must page from the store")
	}
}

// A document that expands past the ceiling says so before the text, so what
// a cut page ends on is still the notice that pages it back.
func TestFormatFetchResult_PDFPastTheCeiling(t *testing.T) {
	fetcher := testFetcher()
	fetcher.MaxBodyBytes = 1024
	ts := NewToolset(fetcher, nil)
	ts.PDFText = stubPDFText(t, strings.Repeat("specification ", 500))
	out := ts.FormatFetchResult(Result{FinalURL: "https://example.com/spec.pdf", Status: 200, ContentType: "application/pdf", Body: []byte("%PDF-1.7 ...")})

	note := "(the extracted text passed the 1 KB ceiling and was cut there)"
	if !strings.Contains(out, note) {
		t.Fatalf("the ceiling must be said:\n%s", out)
	}
	if strings.Index(out, note) > strings.Index(out, "specification") {
		t.Error("the ceiling is said before the text, not after it")
	}
}
