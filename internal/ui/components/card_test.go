package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// The run between a card's title and its chips is the same rule a take-over
// screen's title rule is made of, in colour and in mono alike — one material
// is what makes a card and a screen read as one product rather than as two
// widgets that happen to be in the same binary.
func TestCard_TopEdgeAndTheScreenRuleAreTheSameMaterial(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	top := func() string {
		return strings.SplitN(ansi.Strip(Card{Title: "Approve command"}.Render([]string{"row"}, 60)), "\n", 2)[0]
	}

	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	SetMono(false)
	color := top()
	if !strings.Contains(color, strings.Repeat(plainMark, 8)) {
		t.Fatalf("a card's top edge should be filled with the rule, got %q", color)
	}
	if rule := ansi.Strip(titleRule(60)); rule != strings.Repeat(plainMark, 60) {
		t.Fatalf("a screen's title rule should be the flat rule, got %q", rule)
	}
	if rule := ansi.Strip(screenRule(60)); rule != strings.Repeat(plainMark, 60) {
		t.Fatalf("a pane divider is the same rule, got %q", rule)
	}

	// Mono has one grey for chrome and nothing to decorate with, and the
	// row it draws is the same row.
	SetMono(true)
	if mono := top(); mono != color {
		t.Fatalf("the top edge should not change under mono:\n%q\n%q", color, mono)
	}
}

// The fill takes only what carries nothing. A card's border colour says how
// much the decision on it weighs, so the corners, the title's lead-in and the
// chips keep it and only the run between them is drawn as chrome.
func TestCard_FillNeverEatsTheTitleOrTheChips(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	SetMono(false)
	for _, width := range []int{minCardWidth, 24, 60, 130} {
		top := strings.SplitN(ansi.Strip(
			Card{Title: "Approve command", Chips: []string{"⚠ medium"}}.Render([]string{"row"}, width)), "\n", 2)[0]
		if lipgloss.Width(top) != width {
			t.Fatalf("width %d: top edge measures %d: %q", width, lipgloss.Width(top), top)
		}
		if !strings.HasPrefix(top, "╭─ ") || !strings.HasSuffix(top, "╮") {
			t.Fatalf("width %d: the frame lost a corner: %q", width, top)
		}
		if strings.Contains(top, plainMark+"Approve") || strings.Contains(top, "medium"+plainMark) {
			t.Fatalf("width %d: the fill ran into a field: %q", width, top)
		}
	}
}

// A card is drawn from the same kit the input frame is, so the two shapes on
// screen read as one material at two weights rather than as two kinds of
// object with nothing to learn from the difference. The chip run keeps a rule
// cell between the last chip and the corner, so the chip sits on the rail the
// way the title does instead of being wedged into the join.
func TestCard_CornersAreTheFramesAndTheChipSitsOnTheRail(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	card := ansi.Strip(Card{
		Title: "Approve edit",
		Chips: []string{"⚠ low"},
	}.Render([]string{"row", cardRule, "keys"}, 60))
	lines := strings.Split(card, "\n")
	top, rule, bottom := lines[0], lines[2], lines[len(lines)-1]

	if !strings.HasPrefix(top, "╭─ Approve edit ") || !strings.HasSuffix(top, "╮") {
		t.Fatalf("the top edge is not the frame's: %q", top)
	}
	if !strings.HasSuffix(top, " ⚠ low ─╮") {
		t.Fatalf("the chip should end on a rule cell before the corner: %q", top)
	}
	if !strings.HasPrefix(bottom, "╰") || !strings.HasSuffix(bottom, "╯") {
		t.Fatalf("the bottom edge is not the frame's: %q", bottom)
	}
	// The inner divider still meets the walls: it separates the body from
	// the keys, it does not end the card.
	if !strings.HasPrefix(rule, "├") || !strings.HasSuffix(rule, "┤") {
		t.Fatalf("the divider should meet the walls: %q", rule)
	}
	for _, square := range []string{"┌", "┐", "└", "┘"} {
		if strings.Contains(card, square) {
			t.Fatalf("a card draws no square corner, found %q:\n%s", square, card)
		}
	}
}
