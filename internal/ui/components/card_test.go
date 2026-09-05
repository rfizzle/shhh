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
		if !strings.HasPrefix(top, "┌─ ") || !strings.HasSuffix(top, "┐") {
			t.Fatalf("width %d: the frame lost a corner: %q", width, top)
		}
		if strings.Contains(top, plainMark+"Approve") || strings.Contains(top, "medium"+plainMark) {
			t.Fatalf("width %d: the fill ran into a field: %q", width, top)
		}
	}
}
