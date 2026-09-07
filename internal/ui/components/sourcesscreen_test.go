package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// sourceRows is a session that searched once and read two pages under one
// host, one of them kept in the evidence store.
func sourceRows() []SourcesRow {
	return []SourcesRow{
		{ID: "s1", Group: "searches", Kind: "search", Label: "tokio runtime",
			Status: "3 results", Turn: "turn 1", Agent: "orchestrator", State: ActivityDone},
		{ID: "s2", Group: "docs.rs", Kind: "fetch", Label: "/tokio/latest/tokio/",
			Requested: "https://docs.rs/tokio", FinalURL: "https://docs.rs/tokio/latest/tokio/",
			Title: "tokio — Rust", Status: "200", Bytes: "60 KB", Turn: "turn 1",
			Agent: "orchestrator", Evidence: "ev-1", State: ActivityDone,
			Head: []string{"Tokio is an asynchronous runtime."}},
		{ID: "s3", Group: "docs.rs", Kind: "fetch", Label: "/tokio/latest/tokio/runtime/",
			FinalURL: "https://docs.rs/tokio/latest/tokio/runtime/", Title: "tokio::runtime",
			Status: "200", Bytes: "12 KB", Cached: true, Turn: "turn 2",
			Agent: "web-researcher", State: ActivityDone},
	}
}

func TestSourcesScreen_GroupsUnderItsHosts(t *testing.T) {
	s := &SourcesScreen{Rows: sourceRows(), Focus: 1, Subject: "2 pages · 1 host · 1 search", MaxLines: 14}
	view := ansi.Strip(s.View(110))
	for _, want := range []string{"/sources", "searches", "docs.rs", "tokio — Rust", "[?] keys", "back"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
	// The host labels the run of rows under it and is drawn once, however
	// many of them there are.
	if got := strings.Count(view, "\ndocs.rs "); got != 1 {
		t.Errorf("the host header is drawn %d times:\n%s", got, view)
	}
}

// The pointer steps over the group headers: no key can land on one.
func TestSourcesScreen_MovementStepsOverTheHeaders(t *testing.T) {
	s := &SourcesScreen{Rows: sourceRows(), MaxLines: 14}
	s.View(110)
	for i := 0; i < 2; i++ {
		if done, _ := s.Update(tea.KeyPressMsg{Code: tea.KeyDown}); done {
			t.Fatal("moving closed the screen")
		}
	}
	if s.Focus != 2 {
		t.Errorf("two presses landed on row %d, want the third", s.Focus)
	}
}

func TestSourcesScreen_EnterOnlyAnswersForAPageThatWasKept(t *testing.T) {
	s := &SourcesScreen{Rows: sourceRows(), Focus: 1, MaxLines: 14}
	s.View(110)
	done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !result.Open || result.Evidence != "ev-1" {
		t.Fatalf("enter on a kept page = %v, %+v", done, result)
	}
	if !strings.Contains(s.View(110), keys.Bracket(keys.Sources.Open)) {
		t.Error("the key row does not offer the key that just worked")
	}

	s.Focus = 2
	if done, _ := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done {
		t.Error("enter closed the screen for a row with no entry")
	}
	if strings.Contains(s.View(110), keys.Bracket(keys.Sources.Open)) {
		t.Error("a key that cannot act is still offered")
	}
}

func TestSourcesScreen_AnEmptyLedgerSaysSo(t *testing.T) {
	s := &SourcesScreen{MaxLines: 14}
	view := s.View(90)
	if !strings.Contains(view, "nothing has been read") {
		t.Errorf("an empty screen says nothing:\n%s", view)
	}
	if strings.Contains(view, "recorded by the fetch") {
		t.Error("the foot annotates a key row over no rows")
	}
}

// Stacked, the list gives way to the preview's floor rather than the other
// way round, which is the family's rule.
func TestSourcesScreen_NarrowStacksThePanes(t *testing.T) {
	s := &SourcesScreen{Rows: sourceRows(), Focus: 1, Subject: "2 pages", MaxLines: 16}
	view := s.View(60)
	if !strings.Contains(view, "⇢ /tokio/latest/tokio/") ||
		!strings.Contains(view, "https://docs.rs/tokio/latest/tokio/") {
		t.Errorf("the stacked screen dropped a pane:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", line)
		}
	}
}
