package components

// The sliding window a long list scrolls through (S-116,
// docs/interface/surfaces.md#selectors). The old card sliced its rows from
// index 0 and replaced the last one with …, so a pointer moved past the fifth
// model walked off the bottom of the card and the reader was navigating a
// list they could no longer see.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// modelList is a catalog longer than any bottom panel will hold.
func modelList(n int) []SelectOption {
	opts := make([]SelectOption, 0, n)
	for i := 1; i <= n; i++ {
		opts = append(opts, SelectOption{
			Label: fmt.Sprintf("model-%02d", i),
			Desc:  fmt.Sprintf("description of model %d", i),
		})
	}
	return opts
}

func cardHeight(view string) int { return strings.Count(view, "\n") + 1 }

// focusedRow is the numbered label the pointer is on in a rendered card, or
// "" when the pointer is nowhere on it — which is the bug this story is
// about. It stops at the description column, which every row carries since
// S-126: what these tests are asking is which option the pointer found, not
// what that option says about itself.
func focusedRow(view string) string {
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		i := strings.Index(line, "❯ ")
		if i < 0 {
			continue
		}
		row := strings.TrimLeft(strings.TrimRight(line[i+len("❯ "):], "│ "), " ")
		label, _, _ := strings.Cut(row, "  ")
		return strings.TrimSpace(label)
	}
	return ""
}

// Walking down the whole list, the pointer is on the card at every step and
// the card never outgrows its bound.
func TestSelectWindow_FollowsTheFocusDown(t *testing.T) {
	s := &Select{Title: "Switch model", Options: modelList(20), MaxLines: 10}
	for i := 0; i < 20; i++ {
		view := s.View(70)
		want := fmt.Sprintf("%d. model-%02d", i+1, i+1)
		if got := focusedRow(view); got != want {
			t.Fatalf("at focus %d the pointer should be on %q, got %q:\n%s", i, want, got, view)
		}
		if h := cardHeight(view); h > s.MaxLines {
			t.Fatalf("at focus %d the card grew to %d rows, past its %d bound:\n%s", i, h, s.MaxLines, view)
		}
		s.Update(key("down"))
	}
}

// And back up again: the window follows in both directions, which the old
// fixed slice did in neither.
func TestSelectWindow_FollowsTheFocusUp(t *testing.T) {
	s := &Select{Title: "Switch model", Options: modelList(20), Focus: 19, MaxLines: 10}
	for i := 19; i >= 0; i-- {
		view := s.View(70)
		want := fmt.Sprintf("%d. model-%02d", i+1, i+1)
		if got := focusedRow(view); got != want {
			t.Fatalf("at focus %d the pointer should be on %q, got %q:\n%s", i, want, got, view)
		}
		s.Update(key("up"))
	}
}

// The window moves only when the focus leaves it. A list that re-centred on
// every keystroke would be unreadable.
func TestSelectWindow_StaysStillWhileTheFocusMovesInsideIt(t *testing.T) {
	s := &Select{Title: "Switch model", Options: modelList(20), MaxLines: 12}
	first := s.View(70)
	if !strings.Contains(ansi.Strip(first), "1. model-01") {
		t.Fatalf("the list should open at the top:\n%s", first)
	}
	// One step down is still inside the opening window.
	s.Update(key("down"))
	if view := ansi.Strip(s.View(70)); !strings.Contains(view, "1. model-01") {
		t.Fatalf("a move inside the window should not scroll it:\n%s", view)
	}
	// Far enough down and it has to move.
	for i := 0; i < 10; i++ {
		s.Update(key("down"))
	}
	if view := ansi.Strip(s.View(70)); strings.Contains(view, "1. model-01") {
		t.Fatalf("a move past the window's edge should scroll it:\n%s", view)
	}
}

// Invariant 4: a marker counts what it swallowed rather than only saying that
// it swallowed something.
func TestSelectWindow_MarkersCountWhatIsHidden(t *testing.T) {
	s := &Select{Title: "Switch model", Options: modelList(14), MaxLines: 10}

	top := ansi.Strip(s.View(70))
	if !strings.Contains(top, "↓ 8 more") {
		t.Fatalf("at the top the card should count what is below it:\n%s", top)
	}
	if strings.Contains(top, "↑ ") {
		t.Fatalf("nothing is hidden above the top of the list:\n%s", top)
	}

	s.Focus = 13
	bottom := ansi.Strip(s.View(70))
	if !strings.Contains(bottom, "↑ 8 more") {
		t.Fatalf("at the bottom the card should count what is above it:\n%s", bottom)
	}
	if strings.Contains(bottom, "↓ ") {
		t.Fatalf("nothing is hidden below the end of the list:\n%s", bottom)
	}

	// Mid-list, both markers show and both count. The window is where the
	// pointer pushed it from, so this walks down from the top rather than
	// reusing the card that was just scrolled to the bottom: arriving at an
	// option from above puts it at the foot of the window, from below at the
	// head, and the counts follow.
	walked := &Select{Title: "Switch model", Options: modelList(14), MaxLines: 10}
	for i := 0; i < 8; i++ {
		walked.View(70)
		walked.Update(key("down"))
	}
	middle := ansi.Strip(walked.View(70))
	for _, want := range []string{"↑ 4 more", "↓ 5 more"} {
		if !strings.Contains(middle, want) {
			t.Fatalf("mid-list the card should count both ways, expected %q:\n%s", want, middle)
		}
	}
}

// The window is where the pointer pushed it: an option reached from above
// sits at the foot of the window, and the same option reached from below sits
// at its head. Neither is a jump, which is the point.
func TestSelectWindow_TracksTheEdgeTheFocusCrossed(t *testing.T) {
	fromAbove := &Select{Title: "Switch model", Options: modelList(14), MaxLines: 10}
	for i := 0; i < 8; i++ {
		fromAbove.View(70)
		fromAbove.Update(key("down"))
	}
	if view := ansi.Strip(fromAbove.View(70)); !strings.Contains(view, "↑ 4 more") {
		t.Fatalf("reached from above, the option should sit at the foot of the window:\n%s", view)
	}

	fromBelow := &Select{Title: "Switch model", Options: modelList(14), Focus: 13, MaxLines: 10}
	for i := 13; i > 8; i-- {
		fromBelow.View(70)
		fromBelow.Update(key("up"))
	}
	if view := ansi.Strip(fromBelow.View(70)); !strings.Contains(view, "↑ 8 more") {
		t.Fatalf("reached from below, the option should sit at the head of the window:\n%s", view)
	}
}

// A digit sets the focus and closes the card, so the window has nothing to
// scroll for that key alone — but any render that follows a focus set from
// outside has to bring it into view, which is what /memory's list and the
// note selector's digit jump rely on.
func TestSelectWindow_AFocusSetFromOutsideIsBroughtIntoView(t *testing.T) {
	s := &Select{Title: "Switch model", Options: modelList(20), MaxLines: 10}
	s.View(70) // open at the top, window at 0
	s.Focus = s.selectableIndex(9)
	if got := focusedRow(s.View(70)); got != "9. model-09" {
		t.Fatalf("a jump should scroll the window to the option it landed on, got %q", got)
	}
	s.Focus = s.selectableIndex(2)
	if got := focusedRow(s.View(70)); got != "2. model-02" {
		t.Fatalf("and back up again, got %q", got)
	}
}

// A list that fits is not windowed at all: no markers, no rows spent saying
// that nothing was hidden.
func TestSelectWindow_ShortListIsNotWindowed(t *testing.T) {
	s := &Select{Title: "Switch mode", Options: planOptions(), MaxLines: 12}
	view := ansi.Strip(s.View(70))
	for _, unwanted := range []string{"↑ ", "↓ ", "…"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("a list that fits should render no overflow markers, found %q:\n%s", unwanted, view)
		}
	}
	if !strings.Contains(view, "4. Reject plan") {
		t.Fatalf("every option should show:\n%s", view)
	}
}

// The card's height is the bottom panel's accounting, so the window may never
// buy itself a row.
func TestSelectWindow_NeverOutgrowsMaxLines(t *testing.T) {
	for _, maxLines := range []int{5, 6, 8, 10, 14, 20} {
		for focus := 0; focus < 20; focus++ {
			s := &Select{Title: "Switch model", Options: modelList(20), Focus: focus, MaxLines: maxLines}
			if h := cardHeight(s.View(70)); h > maxLines {
				t.Fatalf("MaxLines %d, focus %d: card is %d rows:\n%s", maxLines, focus, h, s.View(70))
			}
		}
	}
}

// Headers label the options under them; they are not options, so a marker
// does not offer to scroll to one.
func TestSelectWindow_MarkersCountOptionsAndNotHeaders(t *testing.T) {
	opts := []SelectOption{{Label: "COMMANDS", Header: true}}
	opts = append(opts, modelList(6)...)
	opts = append(opts, SelectOption{Label: "FILES", Header: true})
	opts = append(opts, SelectOption{Label: "internal/agent/loop.go"})

	s := &Select{Title: "Palette", Options: opts, Unnumbered: true, MaxLines: 8}
	s.Focus = len(opts) - 1
	view := ansi.Strip(s.View(70))
	if !strings.Contains(view, "FILES") {
		t.Fatalf("the window should carry the header it scrolled to:\n%s", view)
	}
	// The window starts at model-05, so what it hid is the COMMANDS rail and
	// the four models above it — and the marker counts four, because a rail
	// is not something the pointer can be scrolled to.
	if !strings.Contains(view, "↑ 4 more") {
		t.Fatalf("the marker should count the options it hid, not the rails:\n%s", view)
	}
}

// The palette rebuilds its options on every keystroke (S-112), so the window
// has to survive a list that got shorter under it without the host resetting
// anything.
func TestSelectWindow_SurvivesAListThatShrinks(t *testing.T) {
	s := &Select{Title: "Palette", Options: modelList(30), Unnumbered: true, MaxLines: 10}
	s.Focus = 29
	s.View(70) // scrolled to the bottom

	s.Options = modelList(3)
	s.Focus = s.FirstSelectable()
	view := ansi.Strip(s.View(70))
	if got := focusedRow(s.View(70)); got != "model-01" {
		t.Fatalf("a shortened list should render from its top, pointer on %q:\n%s", got, view)
	}
	if strings.Contains(view, "↑ ") {
		t.Fatalf("and with no window at all, since it now fits:\n%s", view)
	}
}

// The note field is pinned under the list (§4c), so a long list scrolls
// rather than pushing the note off the card.
func TestNoteSelectWindow_KeepsTheNoteFieldOnTheCard(t *testing.T) {
	ns := NewNoteSelect("Remember this?", modelList(20))
	ns.Select.MaxLines = 12
	ns.Select.Focus = 17

	view := ansi.Strip(ns.View(70))
	if !strings.Contains(view, "note (optional)") {
		t.Fatalf("the note field should survive a long list:\n%s", view)
	}
	if !strings.Contains(view, "tab note/options") {
		t.Fatalf("and so should the hints:\n%s", view)
	}
	if got := focusedRow(view); got != "18. model-18" {
		t.Fatalf("the pointer should be on the card, got %q:\n%s", got, view)
	}
	if h := cardHeight(view); h > ns.Select.MaxLines {
		t.Fatalf("the card is %d rows, past its %d bound:\n%s", h, ns.Select.MaxLines, view)
	}
}
