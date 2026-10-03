package components

// The list half the supporting screens share
// (docs/architecture.md#a-list-screen-is-one-shape-with-its-own-rows). The
// screens' goldens prove what each draws through it; these hold the walk, the
// filter and the frame on their own.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The pointer walks the rows on the movement binding and stops at the ends,
// and the row under it is read from the screen's own rows.
func TestListScreen_WalksTheRowsAndStopsAtTheEnds(t *testing.T) {
	rows := []string{"a", "b", "c"}
	var l listScreen[string]
	if !l.moved(rows, "j", keys.Screen.Move) || *l.current(rows) != "b" {
		t.Fatalf("j should move to b, focus %d", l.Focus)
	}
	l.moved(rows, "j", keys.Screen.Move)
	l.moved(rows, "j", keys.Screen.Move)
	if l.Focus != 2 {
		t.Fatalf("the walk ran past the end: %d", l.Focus)
	}
	if l.moved(rows, "x", keys.Screen.Move) {
		t.Fatal("a key that is not movement moved the pointer")
	}
	if l.moved(nil, "j", keys.Screen.Move) || l.current(nil) != nil {
		t.Fatal("an empty list has nothing to walk or point at")
	}
	l.Focus = 9
	l.clamp(len(rows))
	if l.Focus != 2 {
		t.Fatalf("clamp left the pointer at %d", l.Focus)
	}
}

// With the query line open every letter is text and the query re-matches;
// the pointer goes to the first row left, walks only what is showing, and a
// clear on an empty query closes the line.
func TestListScreen_TheFilterIsTheLinesKeys(t *testing.T) {
	rows := []string{"alpha", "beta", "gamma", "delta"}
	fields := func(s string) []string { return []string{s} }
	var l listScreen[string]
	l.Focus = 1
	l.list.Filtering = true
	for _, r := range "ta" {
		open, changed := l.filterKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		if !open || !changed {
			t.Fatalf("%q was not typed into the open line", r)
		}
	}
	l.refocus(rows, fields)
	l.shown = l.match(rows, fields)
	if got := l.shown; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("ta should leave beta and delta, got %v", got)
	}
	if *l.currentShown(rows) != "beta" {
		t.Fatalf("the pointer should be on the first row left, got %d", l.Focus)
	}
	if l.movedShown("j", keys.Screen.Move) {
		t.Fatal("a j typed into the filter moved the pointer")
	}
	if !l.movedShown("down", keys.Screen.Move) || *l.currentShown(rows) != "delta" {
		t.Fatalf("down should walk the rows showing, got %d", l.Focus)
	}
	l.showFiltered([]SelectOption{{Label: "beta"}, {Label: "delta"}}, len(rows), "type")
	pane := ansi.Strip(strings.Join(l.queryListRows(len(rows), func(n int) string { return plural(n, "row") }, 80, 0), "\n"))
	for _, want := range []string{"▸ ta", "2 of 4 match", "❯ delta", "2 rows hidden by the filter"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the pane is missing %q:\n%s", want, pane)
		}
	}
	if head := l.filteredBy(nil); len(head) != 1 || !strings.Contains(ansi.Strip(head[0].Text), `filtered by "ta"`) {
		t.Fatalf("the header should state the query: %+v", head)
	}

	clear := tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	l.filterKey(clear)
	if l.list.Query != "" || !l.list.Filtering {
		t.Fatalf("the first clear should empty the line and leave it open: %q", l.list.Query)
	}
	if open, _ := l.filterKey(clear); !open || l.list.Filtering {
		t.Fatal("a clear on an empty query should close the line")
	}
	if open, _ := l.filterKey(clear); open {
		t.Fatal("a closed line answers no keys")
	}
}

// The frame draws the chrome around the panes, and nothing at no width.
func TestListScreen_DrawsTheFrame(t *testing.T) {
	s := &testListScreen{rows: []string{"one", "two"}}
	if s.View(0) != "" {
		t.Fatal("no width draws nothing")
	}
	view := ansi.Strip(s.View(80))
	for _, want := range []string{"/test", "[?] keys · [q] back", "❯ one", "the row is one", "[esc] back to the prompt"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the frame is missing %q:\n%s", want, view)
		}
	}
	s.keys = true
	if head := ansi.Strip(strings.SplitN(s.View(80), "\n", 2)[0]); !strings.Contains(head, "[?] hide the keys") {
		t.Fatalf("the open register should offer to hide itself: %q", head)
	}
	if rows := (&testListScreen{}).listRows(nil, "nothing here", 40, 0); len(rows) != 1 || ansi.Strip(rows[0]) != "nothing here" {
		t.Fatalf("an empty list should say its sentence: %q", rows)
	}
}

// testListScreen is the least a screen supplies to the frame.
type testListScreen struct {
	listScreen[string]
	rows []string
}

func (s *testListScreen) View(width int) string { return s.view(width, s) }

func (s *testListScreen) sync() {
	s.clamp(len(s.rows))
	opts := make([]SelectOption, 0, len(s.rows))
	for _, r := range s.rows {
		opts = append(opts, SelectOption{Label: r})
	}
	s.show(opts, 0, s.Focus)
}

func (s *testListScreen) chrome(width int) screenChrome {
	offers := offersBeside(keyOffer(keys.Screen.Move), []KeyOffer{wayOut(backToPrompt)}, "", width)
	return screenChrome{
		header: screenHeader{left: []RailSegment{screenTitle("/test")}, keys: s.headerKeys(keys.Screen.List, keys.Screen.Quit)},
		foot:   s.footer(offers, offers, "").rows(width),
	}
}

func (s *testListScreen) panes() screenPanes {
	return screenPanes{
		stackAt: 60, listMin: 20, listMax: 40, minPreview: 1,
		list: func(width, budget int) []string { return s.listRows(s.rows, "nothing", width, budget) },
		preview: func(width int) []string {
			return []string{"the row is " + *s.current(s.rows)}
		},
	}
}
