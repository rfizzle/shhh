package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeLedgerBackend is a backend that keeps rows in memory, so the store's
// own SQL is not what these tests are about.
type fakeLedgerBackend struct {
	saved map[string][]Source
	next  int64
	fail  bool
}

func newFakeLedgerBackend() *fakeLedgerBackend {
	return &fakeLedgerBackend{saved: map[string][]Source{}}
}

func (f *fakeLedgerBackend) SaveSource(session string, s Source) (int64, error) {
	if f.fail {
		return 0, fmt.Errorf("no")
	}
	f.next++
	s.ID = f.next
	f.saved[session] = append(f.saved[session], s)
	return s.ID, nil
}

func (f *fakeLedgerBackend) LoadSources(session string) ([]Source, error) {
	out := make([]Source, len(f.saved[session]))
	copy(out, f.saved[session])
	return out, nil
}

func TestLedger_AParentAndAChildEachSignTheirOwnReads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Doc</title></head><body><main><p>Body text.</p></main></body></html>`)
	}))
	defer srv.Close()

	ts := NewToolset(testFetcher(), nil)
	ledger := NewLedger(nil)
	ledger.SetTurn(4)
	ts.UseLedger(ledger)

	// The one toolset a session builds is the object its children fetch
	// through, so a parent and a child are two wraps over it.
	parent := ts.WrapExecutor(Orchestrator, nil)
	child := ts.WrapExecutor("web-researcher", nil)
	if _, err := parent(FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`/a"}`)); err != nil {
		t.Fatalf("parent fetch: %v", err)
	}
	if _, err := child(FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`/b"}`)); err != nil {
		t.Fatalf("child fetch: %v", err)
	}

	rows := ledger.List()
	if len(rows) != 2 {
		t.Fatalf("two fetches left %d rows", len(rows))
	}
	if rows[0].Agent != Orchestrator || rows[1].Agent != "web-researcher" {
		t.Errorf("rows are signed %q and %q", rows[0].Agent, rows[1].Agent)
	}
	for _, row := range rows {
		if row.Kind != KindFetch || row.Status != 200 || row.Turn != 4 {
			t.Errorf("row = %+v", row)
		}
		if row.Title != "Doc" {
			t.Errorf("row's title = %q, want the extraction's", row.Title)
		}
		if row.Bytes == 0 {
			t.Error("a row that read a page counted no bytes")
		}
	}
	if rows[0].FinalURL != srv.URL+"/a" || rows[1].FinalURL != srv.URL+"/b" {
		t.Errorf("final URLs = %q, %q", rows[0].FinalURL, rows[1].FinalURL)
	}
}

func TestLedger_ASearchIsARowWithItsQueryAndNoURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, braveFixture)
	}))
	defer srv.Close()

	ts := NewToolset(NewFetcher(Policy{}), &Searcher{APIKey: "k", Endpoint: srv.URL})
	ledger := NewLedger(nil)
	ts.UseLedger(ledger)
	if _, err := ts.Execute(Orchestrator, SearchToolName, json.RawMessage(`{"query":"golang"}`)); err != nil {
		t.Fatalf("search: %v", err)
	}

	rows := ledger.List()
	if len(rows) != 1 {
		t.Fatalf("one search left %d rows", len(rows))
	}
	row := rows[0]
	if row.Kind != KindSearch || row.Query != "golang" {
		t.Errorf("row = %+v", row)
	}
	if row.FinalURL != "" || row.Requested != "" {
		t.Errorf("a search named a URL: %+v", row)
	}
	if row.Results != 3 {
		t.Errorf("results = %d, want the fixture's 3", row.Results)
	}
}

func TestLedger_AFetchThatKeptItsPageNamesTheEntry(t *testing.T) {
	long := strings.Repeat("word ", (MaxInlineBytes/5)+100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, long)
	}))
	defer srv.Close()

	ts := NewToolset(testFetcher(), nil)
	ts.UseEvidence(func(tool, content string) (string, bool) { return "ev-1", true }, nil)
	ledger := NewLedger(nil)
	ts.UseLedger(ledger)
	if _, err := ts.Execute(Orchestrator, FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`"}`)); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := ledger.List()[0].Evidence; got != "ev-1" {
		t.Errorf("evidence id = %q, want the one the store gave", got)
	}
}

func TestLedger_ResumeBringsBackWhatTheSlotRead(t *testing.T) {
	backend := newFakeLedgerBackend()
	first := NewLedger(backend)
	if err := first.Bind("2026-09-07"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	first.SetTurn(2)
	first.Record(Orchestrator, Source{Kind: KindFetch, FinalURL: "https://go.dev/doc"})

	resumed := NewLedger(backend)
	if err := resumed.Bind("2026-09-07"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	rows := resumed.List()
	if len(rows) != 1 || rows[0].FinalURL != "https://go.dev/doc" || rows[0].Turn != 2 {
		t.Fatalf("resume read back %+v", rows)
	}
	// Another slot's rows are not this one's.
	other := NewLedger(backend)
	if err := other.Bind("2026-09-06"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if other.Len() != 0 {
		t.Errorf("a second slot loaded %d rows", other.Len())
	}
}

func TestLedger_RowsRecordedBeforeTheBindAreWrittenThrough(t *testing.T) {
	backend := newFakeLedgerBackend()
	l := NewLedger(backend)
	l.Record(Orchestrator, Source{Kind: KindFetch, FinalURL: "https://go.dev/"})
	if err := l.Bind("slot"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got := len(backend.saved["slot"]); got != 1 {
		t.Fatalf("the pending row was written through %d times", got)
	}
	if l.Len() != 1 {
		t.Errorf("the ledger holds %d rows after the bind", l.Len())
	}
}

func TestLedger_ABackendThatWillNotWriteStillRecords(t *testing.T) {
	backend := newFakeLedgerBackend()
	backend.fail = true
	l := NewLedger(backend)
	_ = l.Bind("slot")
	l.Record(Orchestrator, Source{Kind: KindFetch, FinalURL: "https://go.dev/"})
	if l.Len() != 1 {
		t.Error("a failed write cost the session its row")
	}
}

func TestLedger_TheScrubReachesTheRowBeforeItIsKept(t *testing.T) {
	l := NewLedger(nil)
	l.SetScrub(func(s string) string { return strings.ReplaceAll(s, "sk-secret", "[secret]") })
	row := l.Record(Orchestrator, Source{Kind: KindFetch, FinalURL: "https://api.example.com/?key=sk-secret"})
	if strings.Contains(row.FinalURL, "sk-secret") {
		t.Errorf("the vaulted value reached the ledger: %q", row.FinalURL)
	}
}

func TestPages_WhatWasReadAndNotWhatWasTried(t *testing.T) {
	rows := []Source{
		{Kind: KindSearch, Query: "tokio"},
		{Kind: KindFetch, FinalURL: "https://docs.rs/tokio/", Title: "Tokio", Status: 200},
		{Kind: KindFetch, FinalURL: "https://docs.rs/tokio", Title: "Tokio again", Status: 200},
		{Kind: KindFetch, FinalURL: "https://go.dev/doc", Status: 200},
		{Kind: KindFetch, FinalURL: "https://example.com/gone", Status: 404},
		{Kind: KindFetch, FinalURL: "https://example.com/refused", Status: 0},
	}
	pages := Pages(rows)
	if len(pages) != 2 {
		t.Fatalf("pages = %d, want the two that answered: %+v", len(pages), pages)
	}
	if pages[0].Title != "Tokio" {
		t.Errorf("the row kept is %q, want the first read of the page", pages[0].Title)
	}
	for _, p := range pages {
		if strings.Contains(p.FinalURL, "example.com") {
			t.Errorf("a fetch that returned nothing was listed as a page: %+v", p)
		}
	}
}

func TestCanonicalURL_TwoSpellingsOfOnePage(t *testing.T) {
	same := []struct{ a, b string }{
		{"https://Go.dev/doc/", "https://go.dev/doc"},
		{"https://go.dev/doc#install", "https://go.dev/doc"},
		{"https://go.dev:443/doc", "https://go.dev/doc"},
		{"http://go.dev:80/doc", "http://go.dev/doc"},
	}
	for _, c := range same {
		if CanonicalURL(c.a) != CanonicalURL(c.b) {
			t.Errorf("%q and %q are different pages: %q vs %q", c.a, c.b, CanonicalURL(c.a), CanonicalURL(c.b))
		}
	}
	if CanonicalURL("https://go.dev/a") == CanonicalURL("https://go.dev/b") {
		t.Error("two paths were folded into one page")
	}
}

func TestCitedURLs_TheAddressesInAWriteUp(t *testing.T) {
	text := "Read [the docs](https://go.dev/doc) and https://docs.rs/tokio/, " +
		"plus <https://example.com/a>. Also https://en.wikipedia.org/wiki/Go_(language). " +
		"The docs again: https://go.dev/doc."
	got := CitedURLs(text)
	want := []string{
		"https://go.dev/doc",
		"https://docs.rs/tokio/",
		"https://example.com/a",
		"https://en.wikipedia.org/wiki/Go_(language)",
	}
	if len(got) != len(want) {
		t.Fatalf("cited = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cited[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(CitedURLs("no addresses here, only ftp://x/ and http:// alone")) != 0 {
		t.Error("something that is not an http address was read as one")
	}
}

func TestSourceHost_TheOneAScreenGroupsBy(t *testing.T) {
	s := Source{Kind: KindFetch, Requested: "https://Docs.RS/tokio", FinalURL: ""}
	if got := s.Host(); got != "docs.rs" {
		t.Errorf("host = %q, want the requested one case-folded", got)
	}
	s.FinalURL = "https://docs.python.org:443/3/library/"
	if got := s.Host(); got != "docs.python.org" {
		t.Errorf("host = %q", got)
	}
	if got := (Source{Kind: KindSearch, Query: "x"}).Host(); got != "" {
		t.Errorf("a search has host %q", got)
	}
}

// A fetch that never got an answer is a row too: the ledger says what the
// session tried to read, which is why a source a reader expected is missing.
func TestLedger_AFetchThatWasRefusedIsStillARow(t *testing.T) {
	ts := NewToolset(NewFetcher(Policy{}), nil)
	ledger := NewLedger(nil)
	ts.UseLedger(ledger)
	if _, err := ts.Execute(Orchestrator, FetchToolName,
		json.RawMessage(`{"url":"http://169.254.169.254/latest/meta-data/"}`)); err == nil {
		t.Fatal("a metadata address was fetched")
	}
	rows := ledger.List()
	if len(rows) != 1 {
		t.Fatalf("a refused fetch left %d rows", len(rows))
	}
	if rows[0].Status != 0 || rows[0].FinalURL != "" ||
		rows[0].Requested != "http://169.254.169.254/latest/meta-data/" {
		t.Errorf("row = %+v", rows[0])
	}
	if len(Pages(rows)) != 0 {
		t.Error("a fetch that read nothing was counted as a page")
	}
}

// A page that turned out to be a shell for a script is still a row: it was
// fetched and it answered, and the row is what says the answer had nothing
// in it. Nothing was kept, so it names no evidence entry.
func TestLedger_APageThatWasAScriptShellIsARowWithNoEntry(t *testing.T) {
	shell := `<html><head><title>Docs</title></head><body><div id="app"></div>` +
		strings.Repeat("<span class=\"pad\"></span>", 400) + `</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, shell)
	}))
	defer srv.Close()

	ts := NewToolset(testFetcher(), nil)
	ts.UseEvidence(func(tool, content string) (string, bool) { return "ev-1", true }, nil)
	ledger := NewLedger(nil)
	ts.UseLedger(ledger)
	out, err := ts.Execute(Orchestrator, FetchToolName, json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(out, "rendered by script") {
		t.Fatalf("the fixture is not read as a script shell:\n%s", out)
	}
	row := ledger.List()[0]
	if row.Title != "Docs" || row.Status != 200 || row.Bytes == 0 {
		t.Errorf("row = %+v", row)
	}
	if row.Evidence != "" {
		t.Errorf("a page nothing was kept from names entry %q", row.Evidence)
	}
}
