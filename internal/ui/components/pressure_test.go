package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// pressureFixture is the card every test here starts from: the §17b artboard,
// in the numbers a real session would have produced.
func pressureFixture() *PressureCard {
	return &PressureCard{
		Pct: 94, Tokens: 188_000, Window: 200_000,
		Warn: 60, Alert: 80,
		Rows: []PressureRow{
			{Tokens: 88_000, Label: "tool output", Detail: "6 results"},
			{Tokens: 54_000, Label: "the conversation", Detail: "14 messages"},
			{Tokens: 31_000, Label: "system prompt"},
			{Tokens: 15_000, Label: "project context"},
		},
		Keeps:       "the plan, 3 changed files and the last 2 turns",
		Drops:       "the older tool output",
		Recovers:    96_000,
		RecoversPct: 48,
		Continuing:  "keeping going asks nothing further",
		Keys: []KeyOffer{
			{Key: "[enter]", Label: "compact now"},
			{Key: "[n]", Label: "new session"},
			{Key: "[esc]", Label: "keep going"},
		},
	}
}

func pressureView(c *PressureCard, width int) string {
	return ansi.Strip(c.View(width))
}

func TestPressureCard_StatesTheOccupancyAndItsParts(t *testing.T) {
	got := pressureView(pressureFixture(), 82)

	for _, want := range []string{
		"Context is nearly full",
		"94% · 188k / 200k",
		"88k  tool output — 6 results",
		"54k  the conversation — 14 messages",
		"recovers about 96k (48%)",
		"[enter] compact now",
		"[esc] keep going",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("card should state %q, got:\n%s", want, got)
		}
	}
}

// The bar is the rail's bar: same component, same cell count, same
// thresholds. A card drawing its own meter is a card that will disagree with
// the rail about what colour 84% is.
func TestPressureCard_MeterMatchesTheRails(t *testing.T) {
	c := pressureFixture()
	got := pressureView(c, 82)

	bar := strings.Repeat("▰", 20) + strings.Repeat("▱", 2)
	if !strings.Contains(got, bar) {
		t.Fatalf("the meter should be %d cells filled to 94%%, got:\n%s", MeterCellsRail, got)
	}
	rail := Meter{Pct: 94, Cells: MeterCellsRail, Tone: MeterPressure, Warn: 60, Alert: 80}
	if c.meter() != rail {
		t.Fatalf("the card's meter %+v should be the rail's %+v", c.meter(), rail)
	}
	if !rail.Style().GetBold() {
		t.Fatal("past the alert threshold the meter should be the bold alert state")
	}
}

// The border carries the meter's colour, which is what puts the bar and the
// numbers on the title rail in one colour (§10c).
func TestPressureCard_BorderTakesTheMeterColour(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	c := pressureFixture()
	probe := c.meter().Style().Render("\u250c")
	probe = strings.TrimSuffix(probe, ansi.ResetStyle)
	if got := c.View(82); !strings.Contains(got, probe) {
		t.Fatalf("the frame should be drawn in the meter's own colour (%q), got:\n%q", probe, got)
	}
}

func TestPressureCard_EstimatedTotalSaysSo(t *testing.T) {
	c := pressureFixture()
	c.Estimated = true
	if got := pressureView(c, 82); !strings.Contains(got, "~188k / 200k") {
		t.Fatalf("an estimated total should be marked, got:\n%s", got)
	}
}

// A card with nothing to keep and nothing to recover still renders: the
// prediction is composed from what is true, never padded to a shape.
func TestPressureCard_PredictionDropsWhatItCannotSay(t *testing.T) {
	c := pressureFixture()
	c.Keeps, c.Recovers, c.RecoversPct = "", 0, 0
	got := pressureView(c, 82)
	if strings.Contains(got, "compacting keeps") {
		t.Fatalf("a session with nothing to keep should not claim to keep it:\n%s", got)
	}
	if strings.Contains(got, "recovers about") {
		t.Fatalf("no prediction should be printed when there is none:\n%s", got)
	}
	if !strings.Contains(got, "compacting drops the older tool output") {
		t.Fatalf("what it drops should still be stated:\n%s", got)
	}
}

// The counts line up on their last character, so the column reads as one
// however wide the numbers are.
func TestPressureCard_CountsAreRightAligned(t *testing.T) {
	c := pressureFixture()
	c.Rows = []PressureRow{
		{Tokens: 188_000, Label: "tool output"},
		{Tokens: 2_000, Label: "tool definitions"},
	}
	got := pressureView(c, 82)
	if !strings.Contains(got, "188k  tool output") {
		t.Fatalf("the widest count sets the field:\n%s", got)
	}
	if !strings.Contains(got, "2.0k  tool definitions") {
		t.Fatalf("a narrower count should be padded into it:\n%s", got)
	}
}

func TestPressureCard_KeysResolveAndEscDeclines(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want string
	}{
		{"enter", "enter"},
		{"n", "n"},
		{"esc", ""},
		{"ctrl+c", ""},
	} {
		c := pressureFixture()
		done, result := c.Update(pressFor(tc.key))
		if !done {
			t.Fatalf("%q should resolve the card", tc.key)
		}
		if got, _ := result.(string); got != tc.want {
			t.Fatalf("%q should resolve to %q, got %q", tc.key, tc.want, got)
		}
	}

	c := pressureFixture()
	if done, _ := c.Update(tea.KeyPressMsg{Code: 'z', Text: "z"}); done {
		t.Fatal("a key the card does not offer should not resolve it")
	}
}

// pressFor maps the test's key names onto the presses bubbletea would have
// produced for them.
func pressFor(key string) tea.KeyPressMsg {
	switch key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
}
