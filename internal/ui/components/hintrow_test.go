package components

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A row too narrow for its run gives segments up whole, the largest priority
// first and the rightmost of two equal ones before the other, and the safe
// answer only once nothing else is left; where even that is too wide its
// gloss is cut with `…`, and nowhere else.
func TestFit_ShedsByPriorityAndKeepsTheSafeAnswerLast(t *testing.T) {
	run := []KeyOffer{
		{Key: "[y]", Label: "allow"},
		{Key: "[n]", Label: "deny"},
		{Key: "[?]", Label: "keys", Priority: 2},
		{Key: "[/]", Label: "filter", Priority: 1},
		{Key: "[esc]", Label: "keeps waiting", Safe: true},
	}
	cases := []struct {
		width int
		want  string
	}{
		{80, "[y] allow · [n] deny · [?] keys · [/] filter · [esc] keeps waiting"},
		{55, "[y] allow · [n] deny · [/] filter · [esc] keeps waiting"},
		{44, "[y] allow · [n] deny · [esc] keeps waiting"},
		{33, "[y] allow · [esc] keeps waiting"},
		{19, "[esc] keeps waiting"},
		{12, "[esc] keeps…"},
	}
	for _, c := range cases {
		got := ansi.Strip(liveTones().Fit(run, c.width))
		if got != c.want {
			t.Errorf("at %d columns the row is %q, want %q", c.width, got, c.want)
		}
	}
}

// Shed's forms are the order every one-row surface narrows through: one
// segment fewer each, and the safe answer outlasts a floor segment that came
// before it.
func TestShed_DropsTheFloorBeforeTheSafeAnswer(t *testing.T) {
	forms := Shed([]KeyOffer{
		{Key: "[esc]", Label: "back", Safe: true},
		{Key: "[↑↓]", Label: "move"},
		{Key: "[enter]", Label: "open"},
	})
	if len(forms) != 3 {
		t.Fatalf("%d forms, want 3", len(forms))
	}
	last := forms[len(forms)-1]
	if len(last) != 1 || !last[0].Safe {
		t.Errorf("the last form is %v, want the safe answer alone", last)
	}
	if got := forms[1]; len(got) != 2 || got[1].Key != "[↑↓]" {
		t.Errorf("the rightmost floor segment goes first, got %v", got)
	}
}

// A run that packs breaks between segments and never inside one, and the
// safe answer that will not fit beside the row before it takes a row of its
// own with its words whole.
func TestPack_GivesTheSafeAnswerARowRatherThanItsWords(t *testing.T) {
	rows := cardTones().Pack([]KeyOffer{
		{Key: "[a]", Label: "pick which of the 2 to start"},
		{Key: "[?]", Label: "keys"},
		{Key: "[esc]", Label: "keeps waiting", Safe: true},
	}, 46)
	var plain []string
	for _, r := range rows {
		plain = append(plain, ansi.Strip(r))
	}
	want := []string{"[a] pick which of the 2 to start · [?] keys", "[esc] keeps waiting"}
	if len(plain) != len(want) || plain[0] != want[0] || plain[1] != want[1] {
		t.Errorf("packed to %q, want %q", plain, want)
	}
}
