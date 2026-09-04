package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// The run between a card's title and its chips is texture, and it is the same
// texture a take-over screen's title rule is made of — that is the whole of
// what makes a card and a screen read as one product rather than as two
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
	if !strings.Contains(color, textureMark) || strings.Contains(color, plainMark+plainMark) {
		t.Fatalf("a card's top edge should be filled with the texture, got %q", color)
	}
	if rule := ansi.Strip(titleRule(60)); !strings.Contains(rule, textureMark) {
		t.Fatalf("a screen's title rule should be the same texture, got %q", rule)
	}
	// A rule inside a surface divides rather than bounds, and stays flat.
	if rule := ansi.Strip(screenRule(60)); strings.Contains(rule, textureMark) {
		t.Fatalf("a pane divider is not an edge, got %q", rule)
	}

	// Mono has one grey for chrome and no second tone to spend on a
	// decoration, so both collapse to the flat rule they were.
	SetMono(true)
	mono := top()
	if strings.Contains(mono, textureMark) {
		t.Fatalf("the texture should be declined in mono, got %q", mono)
	}
	if !strings.Contains(mono, strings.Repeat(plainMark, 8)) {
		t.Fatalf("mono should leave the flat rule behind, got %q", mono)
	}
	if rule := ansi.Strip(titleRule(60)); rule != strings.Repeat(plainMark, 60) {
		t.Fatalf("the mono title rule should be the flat one, got %q", rule)
	}
	if lipgloss.Width(color) != lipgloss.Width(mono) {
		t.Fatalf("the two edges measure differently: %d vs %d", lipgloss.Width(color), lipgloss.Width(mono))
	}
}

// The texture fills only what carries nothing. A card's border colour says
// how much the decision on it weighs, so the corners, the title's lead-in and
// the chips keep it and only the run between them is drawn as chrome.
func TestCard_TextureNeverEatsTheTitleOrTheChips(t *testing.T) {
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
		if strings.Contains(top, textureMark+"Approve") || strings.Contains(top, "medium"+textureMark) {
			t.Fatalf("width %d: the texture ran into a field: %q", width, top)
		}
	}
}
