package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/web"
)

// sourcesModel is a session that has read three pages and run one search,
// two of the pages by a child, with one page kept in the evidence store.
func sourcesModel(t *testing.T, width int) Model {
	t.Helper()
	ledger := web.NewLedger(nil)
	ledger.SetTurn(1)
	ledger.Record(web.Orchestrator, web.Source{Kind: web.KindSearch, Query: "tokio runtime", Results: 3})
	ledger.Record(web.Orchestrator, web.Source{Kind: web.KindFetch,
		Requested: "https://docs.rs/tokio", FinalURL: "https://docs.rs/tokio/latest/tokio/",
		Title: "tokio — Rust", Status: 200, Bytes: 61440, Evidence: "ev-1"})
	ledger.SetTurn(2)
	ledger.Record("web-researcher", web.Source{Kind: web.KindFetch,
		FinalURL: "https://docs.rs/tokio/latest/tokio/runtime/", Title: "tokio::runtime",
		Status: 200, Bytes: 12288, Cached: true})
	ledger.Record("web-researcher", web.Source{Kind: web.KindFetch,
		Requested: "https://example.com/gone", FinalURL: "https://example.com/gone", Status: 404})

	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithSources(ledger).
		WithEvidence(Evidence{Read: func(id string, limit int) (string, bool) {
			if id != "ev-1" {
				return "", false
			}
			return "Tokio is an asynchronous runtime.\nIt provides the building blocks.", true
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	return updated.(Model)
}

func TestSources_TheScreenListsWhatTheSessionRead(t *testing.T) {
	m := sendText(t, sourcesModel(t, 100), "/sources")
	if m.state != stateSources || m.sources == nil {
		t.Fatalf("/sources left the session in state %v", m.state)
	}
	if len(m.sources.Rows) != 4 {
		t.Fatalf("the screen holds %d rows for four reads", len(m.sources.Rows))
	}
	view := strings.Join(m.sourcesLines(), "\n")
	for _, want := range []string{
		"/sources",
		"2 pages · 1 host · 1 search", // counted from what was read
		"docs.rs",                     // the host is the group
		"searches",                    // and a search is filed under its own
		"404",                         // the outcome of the one that failed
		"back",                        // the way out, in the header
		"recorded by",                 // the field under the keys
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
}

// The row the pointer starts on is the last thing read, and its preview is
// the fields a reader checking a citation reads.
func TestSources_ThePreviewStatesTheRowsFields(t *testing.T) {
	m := sendText(t, sourcesModel(t, 110), "/sources")
	m.sources.Focus = 1
	view := strings.Join(m.sourcesLines(), "\n")
	for _, want := range []string{
		"https://docs.rs/tokio/latest/tokio/", // the URL that answered
		"asked for",                           // and the one that was asked for, since they differ
		"tokio — Rust",                        // the extraction's title
		"kept as",                             // where the page is
		"asynchronous runtime",                // the head of the entry itself
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the preview is missing %q:\n%s", want, view)
		}
	}
}

func TestSources_EnterOpensThePageThatWasKept(t *testing.T) {
	m := sendText(t, sourcesModel(t, 100), "/sources")
	m.sources.Focus = 1
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateOutputFull || m.fullOutput == nil {
		t.Fatalf("enter on a kept page left the session in state %v", m.state)
	}
	if !strings.Contains(strings.Join(m.fullOutput.Lines, "\n"), "asynchronous runtime") {
		t.Errorf("the viewer holds %q", m.fullOutput.Lines)
	}
	// Esc comes back to the list rather than to the prompt: looking at one
	// entry is not leaving the list.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := updated.(Model).state; got != stateSources {
		t.Errorf("esc from the page went to state %v, not back to the list", got)
	}
}

// A row whose page was never kept has nothing to open, so enter does not
// pretend it has.
func TestSources_EnterOnAPageThatWasNotKeptDoesNothing(t *testing.T) {
	m := sendText(t, sourcesModel(t, 100), "/sources")
	m.sources.Focus = 3
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := updated.(Model).state; got != stateSources {
		t.Errorf("enter on a row with no entry went to state %v", got)
	}
}

func TestSources_EscLeavesTheScreen(t *testing.T) {
	m := sendText(t, sourcesModel(t, 100), "/sources")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := updated.(Model).state; got == stateSources {
		t.Error("esc did not leave the sources screen")
	}
}

// A session with no web tools has no ledger, and says so rather than putting
// up an empty screen.
func TestSources_ASessionWithNoWebToolsSaysSo(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = sendText(t, updated.(Model), "/sources")
	if m.state == stateSources {
		t.Fatal("a session with no ledger opened the screen anyway")
	}
	if !strings.Contains(strings.Join(m.renderHistoryLines(), "\n"), "no web tools") {
		t.Error("nothing said why the screen did not open")
	}
}

// The sources a run's write-up is given are the ledger's pages, and then
// whatever the write-up cited that is not among them.
func TestRunSources_TheLedgerFirstAndTheInventedSecond(t *testing.T) {
	m := sourcesModel(t, 100)
	got := m.runSources("The answer is in https://docs.rs/tokio/latest/tokio/ and in " +
		"https://example.org/blog/never-fetched.")
	if len(got) != 3 {
		t.Fatalf("sources = %+v", got)
	}
	for i, want := range []struct {
		url  string
		read bool
	}{
		{"https://docs.rs/tokio/latest/tokio/", true},
		{"https://docs.rs/tokio/latest/tokio/runtime/", true},
		{"https://example.org/blog/never-fetched", false},
	} {
		if got[i].URL != want.url || got[i].Read != want.read {
			t.Errorf("sources[%d] = %+v, want %q read=%v", i, got[i], want.url, want.read)
		}
	}
	if got[0].Title != "tokio — Rust" {
		t.Errorf("the read page lost its title: %+v", got[0])
	}
	// A page that failed is not a source: nothing was read from it.
	for _, s := range got {
		if strings.Contains(s.URL, "example.com/gone") && s.Read {
			t.Error("a fetch that 404ed was listed as read")
		}
	}
}
