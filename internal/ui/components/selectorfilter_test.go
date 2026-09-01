package components

// The filter row over the window (
// docs/interface/surfaces.md#selectors, ui_kits/cockpit/Lists.html). The
// window
// gave every picker one window; past a dozen entries walking that window is
// still the slow way, so the same component pins a query line above it. The
// rule the artboard settles and these tests hold is that the component never
// filters: the caller passes the matches and the query that produced them, so
// the match rule stays where it is chosen.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// catalog is the /model list the artboard draws: long enough to window, with
// a run of names a query can actually narrow.
func catalog() []SelectOption {
	names := []string{
		"gpt-5.2", "gpt-5.2-mini", "gpt-5.1", "gpt-5.1-mini", "o4-mini",
		"claude-opus-4.6", "claude-sonnet-4.6", "claude-sonnet-4.5",
		"claude-haiku-4.5", "gemini-3-pro", "gemini-3-flash", "grok-4.1",
		"deepseek-r2", "deepseek-v4", "qwen3-coder-72b", "llama-4-maverick",
		"llama-3.3-70b", "mistral-large-3", "phi-5-mini", "command-r-plus",
		"nova-pro-2", "jamba-2", "yi-2-34b", "solar-pro-3",
	}
	opts := make([]SelectOption, 0, len(names))
	for _, n := range names {
		opts = append(opts, SelectOption{Label: n})
	}
	return opts
}

// filtered is the card as a host hands it back: the matches, the query that
// made them, and the catalog they came out of.
func filtered(query string) *Select {
	all := catalog()
	var matches []SelectOption
	for _, o := range all {
		if strings.Contains(o.Label, query) {
			matches = append(matches, o)
		}
	}
	return &Select{
		Title: "Switch model", Options: matches, MaxLines: 10,
		Filterable: true, Filtering: true, Query: query, Total: len(all),
	}
}

// [/] opens the query line, and only on a card that offers it. A fixed set of
// answers is not a catalog to search.
func TestSelectFilter_SlashOpensTheRowOnlyWhereItIsOffered(t *testing.T) {
	s := &Select{Title: "Switch model", Options: catalog(), MaxLines: 10, Filterable: true}
	if view := ansi.Strip(s.View(70)); !strings.Contains(view, "/ filter") {
		t.Fatalf("a filterable card should offer the key that opens the row:\n%s", view)
	}
	s.Update(key("/"))
	if !s.Filtering {
		t.Fatal("/ should open the query line")
	}

	fixed := &Select{Title: "Switch mode", Options: planOptions()}
	if view := ansi.Strip(fixed.View(70)); strings.Contains(view, "/ filter") {
		t.Fatalf("a card that is a fixed set of answers offers no filter:\n%s", view)
	}
	fixed.Update(key("/"))
	if fixed.Filtering {
		t.Fatal("/ on a card that does not offer it should do nothing")
	}
}

// With the row open the query line is the surface, so every key that types is
// text — including the digits and the j that would otherwise move or jump. A
// model name with a 5 in it must not switch the model halfway through being
// typed.
func TestSelectFilter_TheQueryLineIsTheSurface(t *testing.T) {
	s := &Select{Title: "Switch model", Options: catalog(), MaxLines: 10,
		Filterable: true, Filtering: true, Total: len(catalog())}
	for _, k := range []string{"j", "5", "-", "m", "i", "n", "i"} {
		if done, _ := s.Update(key(k)); done {
			t.Fatalf("%q should type into the query, not act", k)
		}
	}
	if s.Query != "j5-mini" {
		t.Fatalf("the keys should have landed in the query, got %q", s.Query)
	}
	if s.Focus != 0 {
		t.Fatalf("no key should have moved the pointer, got focus %d", s.Focus)
	}
	// The arrows still move: the list is still a list.
	s.Update(key("down"))
	if s.Focus != 1 {
		t.Fatalf("the arrows should still move the pointer, got focus %d", s.Focus)
	}
}

// The component does not filter. It reports that the query changed and waits
// for the caller to say what matches it; the options it was given are the
// options it renders.
func TestSelectFilter_TheComponentDoesNotFilter(t *testing.T) {
	s := &Select{Title: "Switch model", Options: catalog(), MaxLines: 10,
		Filterable: true, Filtering: true, Total: len(catalog())}
	s.Update(key("m"))
	if !s.QueryChanged() {
		t.Fatal("a typed key should tell the host the query moved")
	}
	if s.QueryChanged() {
		t.Fatal("QueryChanged should clear itself, or a host re-filters forever")
	}
	if len(s.Options) != len(catalog()) {
		t.Fatalf("the card filtered on its own: %d options left", len(s.Options))
	}
	if s.Update(key("down")); s.QueryChanged() {
		t.Fatal("a move is not a query change")
	}
}

// ctrl+u clears the query and says so, and esc leaves the picker rather than
// closing the row: a filter you have to escape twice is a mode.
func TestSelectFilter_ClearAndLeave(t *testing.T) {
	s := filtered("mini")
	s.Update(key("ctrl+u"))
	if s.Query != "" || !s.QueryChanged() {
		t.Fatalf("ctrl+u should clear the query and report it, got %q", s.Query)
	}

	if !s.Filtering {
		t.Fatal("clearing a query that had something in it leaves the row open")
	}

	s = filtered("mini")
	done, result := s.Update(key("esc"))
	if !done || !result.(SelectResult).Canceled {
		t.Fatalf("esc should leave the picker changing nothing, got %v %v", done, result)
	}
}

// A card that opens as a search has handed its letters to the query line, so
// ctrl+u has a second reading on an empty query: it closes the row and gives
// the card its own keys back, without leaving it. The key row names which of
// the two readings the key has.
func TestSelectFilter_ClearingAnEmptyQueryClosesTheRow(t *testing.T) {
	s := filtered("")
	s.AltKey, s.AltLabel = "d", "and make it default"

	if view := ansi.Strip(s.View(70)); !strings.Contains(view, "ctrl+u row keys") {
		t.Fatalf("an empty query offers the way back to the row's keys:\n%s", view)
	}

	s.Update(key("ctrl+u"))
	if s.Filtering {
		t.Fatal("ctrl+u on an empty query should close the row")
	}
	if s.QueryChanged() {
		t.Fatal("closing the row is not a query change: there is nothing to re-filter")
	}
	if view := ansi.Strip(s.View(70)); !strings.Contains(view, "d and make it default") {
		t.Fatalf("the card's own keys are back on the row:\n%s", view)
	}

	done, result := s.Update(key("d"))
	if !done || !result.(SelectResult).Alt {
		t.Fatalf("the letter should be a key again, got %v %v", done, result)
	}
}

// A card with nothing behind its query row — no numbers, no keys of its own —
// offers no way to close it, because closing it would hand back nothing.
func TestSelectFilter_NoRowKeysNoWayBackOffered(t *testing.T) {
	s := filtered("")
	s.Unnumbered = true

	if view := ansi.Strip(s.View(70)); strings.Contains(view, "ctrl+u") {
		t.Fatalf("a card with no row keys should not offer the key that hands them back:\n%s", view)
	}
}

// The filter makes a new list: the numbering counts the matches, both markers
// count the matches, and the query row states both counts so the catalog it
// came out of is never hidden.
func TestSelectFilter_MakesANewList(t *testing.T) {
	s := filtered("mini")
	view := ansi.Strip(s.View(70))
	for _, want := range []string{"6 of 24 match", "1. gpt-5.2-mini", "6. phi-5-mini"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q on the filtered card:\n%s", want, view)
		}
	}
	if strings.Contains(view, " more") {
		t.Fatalf("six matches fit, so nothing is hidden and no marker is spent:\n%s", view)
	}

	// A filter that still overflows counts matches on the markers, not the
	// catalog behind them.
	long := filtered("-")
	long.MaxLines = 8
	view = ansi.Strip(long.View(70))
	if !strings.Contains(view, " more") {
		t.Fatalf("a filtered list longer than the card still windows:\n%s", view)
	}
	if strings.Contains(view, "↓ 20 more") {
		t.Fatalf("the marker should count matches, not the catalog:\n%s", view)
	}
}

// The matched run is bold and never tinted: three background tints already
// mean one thing each, and bold is the emphasis that survives mono.
func TestSelectFilter_TheMatchedRunIsBoldNotTinted(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	for _, monoOn := range []bool{false, true} {
		was := Mono()
		SetMono(monoOn)
		s := filtered("mini")
		s.Focus = 1 // off the first row, so row 1 is an ordinary row
		view := s.View(70)
		SetMono(was)
		if !strings.Contains(view, "\x1b[1mmini") {
			t.Fatalf("mono=%v: the matched run should be bold:\n%q", monoOn, view)
		}
		if strings.Contains(view, "\x1b[48;5;62m1. gpt") {
			t.Fatalf("mono=%v: the run should not be tinted:\n%q", monoOn, view)
		}
		if plain := ansi.Strip(view); !strings.Contains(plain, "1. gpt-5.2-mini") {
			t.Fatalf("mono=%v: the emphasis must not disturb the text:\n%s", monoOn, plain)
		}
	}
}

// No match is a row, not an empty pane: the card keeps its frame, the query
// row keeps both counts, a line names the nearest thing that does exist, and
// the key that clears the filter stays on the key row.
func TestSelectFilter_NoMatchIsARowNotAnEmptyPane(t *testing.T) {
	s := filtered("sonnet-5")
	s.Closest = "claude-sonnet-4.6"
	view := ansi.Strip(s.View(70))
	for _, want := range []string{
		"0 of 24 match", `no match for "sonnet-5"`,
		"closest is claude-sonnet-4.6", "ctrl+u clear the filter", "esc cancel",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q on the empty card:\n%s", want, view)
		}
	}
	if strings.Contains(view, "enter select") {
		t.Fatalf("nothing on the card can be selected, so enter is not offered:\n%s", view)
	}
	// And enter does not act, because a key that cannot act does not act.
	if done, _ := s.Update(key("enter")); done {
		t.Fatal("enter on a card that matched nothing should do nothing")
	}
	// esc still changes nothing.
	done, result := s.Update(key("esc"))
	if !done || !result.(SelectResult).Canceled {
		t.Fatalf("esc should still leave without changing anything, got %v %v", done, result)
	}
}

// Both counts belong to the row, so a terminal too narrow to carry them side
// by side stacks them rather than dropping one (invariant 4).
func TestSelectFilter_NarrowRowStacksBothCounts(t *testing.T) {
	s := filtered("mini")
	view := ansi.Strip(s.View(26))
	if !strings.Contains(view, "▸ mini█") || !strings.Contains(view, "6 of 24 match") {
		t.Fatalf("both the query and its counts should survive a narrow card:\n%s", view)
	}
}

// The title rail carries the catalog's size and, when the card could not show
// all of it, how much is on screen — and spends nothing on a list that fits.
func TestSelectWindow_TheRailCountsTheCatalog(t *testing.T) {
	s := &Select{Title: "Switch model", Options: catalog(), MaxLines: 10, Filterable: true}
	if view := ansi.Strip(s.View(70)); !strings.Contains(view, "24 available · 6 showing") {
		t.Fatalf("a windowed card should say how much of the catalog it holds:\n%s", view)
	}
	short := &Select{Title: "Switch mode", Options: planOptions(), MaxLines: 12}
	if view := ansi.Strip(short.View(70)); strings.Contains(view, "available") {
		t.Fatalf("a list that fits spends no rail saying nothing was hidden:\n%s", view)
	}
}

// A run that hid nothing selectable keeps the bare …: writing "↑ 1 more"
// there would promise an option that does not exist.
func TestSelectWindow_ARunThatHidNoOptionKeepsTheBareMarker(t *testing.T) {
	opts := []SelectOption{{Label: "COMMANDS", Header: true}}
	opts = append(opts, modelList(6)...)
	s := &Select{Title: "Palette", Options: opts, Unnumbered: true, MaxLines: 8}
	s.Focus = 1
	// Push the window down by exactly the rail: the first option stays, so
	// what went above the fold is the rail alone.
	s.window.scroll = 1
	view := ansi.Strip(s.View(70))
	if !strings.Contains(view, "…") {
		t.Fatalf("a run that hid only a rail still marks that it hid something:\n%s", view)
	}
	if strings.Contains(view, "↑ 1 more") {
		t.Fatalf("the marker must not offer an option that does not exist:\n%s", view)
	}
}

// The plan card is the exception the artboard names: its decisions are what
// you are answering, so they render whole and the step list shrinks instead.
func TestPlanCard_DecisionsRenderWholeAndTheStepsShrink(t *testing.T) {
	c := PlanCard{
		Title: "Plan · make the round limit recoverable",
		Steps: []PlanStep{
			{Number: 1, Title: "Locate the round accounting", Kind: "read only"},
			{Number: 2, Title: "Add a RoundsExhausted sentinel", Kind: "✎ creates 1 file"},
			{Number: 3, Title: "Return it from runRound", Kind: "✎ edits 1 file"},
			{Number: 4, Title: "Handle it in Run", Kind: "✎ edits 1 file"},
			{Number: 5, Title: "Offer more rounds in the chat model", Kind: "✎ edits 1 file"},
		},
		Options: []SelectOption{
			{Label: "Run the whole plan"},
			{Label: "Step through it"},
			{Label: "Edit the plan"},
			{Label: "Just answer the question instead"},
		},
		MaxLines: 10,
	}
	view := ansi.Strip(c.View(70))
	for _, want := range []string{
		"Run the whole plan", "Step through it", "Edit the plan",
		"Just answer the question instead",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("every decision should render, missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "more steps") {
		t.Fatalf("the steps are what shrinks, and they are counted when they do:\n%s", view)
	}
	if strings.Contains(view, "↓ ") || strings.Contains(view, "↑ ") {
		t.Fatalf("the option list is not windowed here:\n%s", view)
	}
}

// The note selector pins its query row the same way, and still windows
// through the same path — the note is the answer, not a row, so it never
// scrolls off.
func TestNoteSelectFilter_PinsItsQueryRowAndKeepsTheNote(t *testing.T) {
	ns := NewNoteSelect("Remember this?", modelList(20))
	ns.Select.MaxLines = 12
	ns.Select.Filterable = true
	ns.Update(key("/"))
	if !ns.Select.Filtering {
		t.Fatal("/ should open the query line on the note selector too")
	}
	ns.Select.Total = 20
	for _, k := range []string{"1", "9"} {
		ns.Update(key(k))
	}
	if ns.Select.Query != "19" {
		t.Fatalf("digits are text on an open query line, got %q", ns.Select.Query)
	}
	view := ansi.Strip(ns.View(70))
	for _, want := range []string{"▸ 19█", "20 of 20 match", "note (optional)", "ctrl+u clear"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q on the card:\n%s", want, view)
		}
	}
	if h := cardHeight(view); h > ns.Select.MaxLines {
		t.Fatalf("the card is %d rows, past its %d bound:\n%s", h, ns.Select.MaxLines, view)
	}
	// Tab still reaches the note, and the note takes the keys back.
	ns.Update(key("tab"))
	if !ns.FocusNote {
		t.Fatal("tab should still reach the note field")
	}
}
