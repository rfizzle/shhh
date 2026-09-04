package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// startFixture is the screen every test here starts from: the facts a Go
// checkout with a dirty tree produces, the two notes, and the three offers.
func startFixture() StartScreen {
	return StartScreen{
		Facts: []StartFact{
			{Text: "~/src/shhh", Lead: true},
			{Text: "go 1.24"},
			{Text: "git main"},
			{Text: "3 files changed", Tone: ToneOpen},
			{Text: "41 packages"},
		},
		Notes: []StartNote{
			{Label: "context", Value: "AGENTS.md", Detail: "in the system prompt"},
			{Label: "gate", Value: "default", Detail: "vet, test · runs without asking"},
		},
		Lead: "Some things worth doing first:",
		Suggestions: []StartSuggestion{
			{Glyph: "▸", Title: "pick up (last session)", Detail: "7 turns · $0.42 · 4m ago"},
			{Glyph: "⚙", Title: "explain what changed in the working tree", Detail: "reads only, no writes"},
			{Glyph: "⚙", Title: "run the default quality gate and triage what fails",
				Detail: "one approval, then it reports back"},
		},
		Hint: "[↑↓] choose · [enter] start · or just type what you want",
	}
}

func startView(s StartScreen, width int) string { return ansi.Strip(s.View(width)) }

func TestStartScreen_StatesWhatItAlreadyKnows(t *testing.T) {
	view := startView(startFixture(), 110)
	for _, want := range []string{
		"~/src/shhh", "go 1.24", "git main", "3 files changed", "41 packages",
		"context", "AGENTS.md", "gate", "default", "runs without asking",
		"Some things worth doing first:",
		"pick up (last session)", "7 turns · $0.42 · 4m ago",
		"[↑↓] choose",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view is missing %q:\n%s", want, view)
		}
	}
}

func TestStartScreen_FocusIsAPointerNotOnlyAHighlight(t *testing.T) {
	// A background survives no monochrome terminal, and the row's own glyph
	// already means something else — so focus has to be a character.
	s := startFixture()
	first := startView(s, 110)
	s.Focus = 2
	third := startView(s, 110)
	if first == third {
		t.Fatal("moving the pointer changed nothing in the stripped render")
	}
	lines := strings.Split(third, "\n")
	var focused, unfocused int
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "❯ "):
			focused++
		case strings.HasPrefix(line, "  ▸ "), strings.HasPrefix(line, "  ⚙ "):
			unfocused++
		}
	}
	if focused != 1 {
		t.Fatalf("focused rows = %d, want exactly 1:\n%s", focused, third)
	}
	if unfocused != 2 {
		t.Fatalf("unfocused rows = %d, want 2:\n%s", unfocused, third)
	}
}

func TestStartScreen_FactsDropFromTheRightAndKeepThePath(t *testing.T) {
	s := startFixture()
	line := strings.Split(startView(s, 30), "\n")[0]
	if !strings.Contains(line, "~/src/shhh") {
		t.Fatalf("the path was dropped from a narrow header: %q", line)
	}
	if strings.Contains(line, "41 packages") {
		t.Fatalf("a header this narrow cannot carry every clause: %q", line)
	}
	if lipgloss.Width(line) > 30 {
		t.Fatalf("header overflows 30 columns: %q", line)
	}
}

func TestStartScreen_DetailMovesUnderItsRowRatherThanBeingClipped(t *testing.T) {
	// The detail is the permission a suggestion costs; losing it to a clip
	// would leave the offer unpriced.
	view := startView(startFixture(), 62)
	if !strings.Contains(view, "one approval, then it reports back") {
		t.Fatalf("the approval cost was clipped away:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 62 {
			t.Fatalf("line overflows 62 columns: %q", line)
		}
	}
}

func TestStartScreen_NoteDetailMovesUnderItsValue(t *testing.T) {
	view := startView(startFixture(), 44)
	if !strings.Contains(view, "runs without asking") {
		t.Fatalf("the gate's detail was clipped away:\n%s", view)
	}
}

func TestStartScreen_WithoutSuggestionsTheKeysGoToo(t *testing.T) {
	// Typing dismisses the list; a key line with nothing to choose from is an
	// offer nothing accepts.
	s := startFixture()
	s.Suggestions, s.Lead, s.Hint = nil, "", ""
	view := startView(s, 110)
	if strings.Contains(view, "[↑↓]") || strings.Contains(view, "worth doing first") {
		t.Fatalf("the dismissed list left its chrome behind:\n%s", view)
	}
	if !strings.Contains(view, "~/src/shhh") {
		t.Fatalf("dismissing the list took the facts with it:\n%s", view)
	}
}

// The face is the one thing on this screen worth rows rather than
// information, so it is the one thing the pane's height decides: three rows
// and a trail where there is room, one row of the name in the texture where
// there is not, and nothing at all from a host that never said how tall it is.
func TestStartScreen_TheFaceIsWhatTheHeightAllows(t *testing.T) {
	s := startFixture()
	for _, c := range []struct {
		name   string
		height int
		want   string
	}{
		{"a host that did not say", 0, "~/src/shhh"},
		{"a short pane", startFaceHeight - 1, "╱╱ shhh ╱"},
		{"room for the wordmark", startFaceHeight, startWordmark[0]},
	} {
		s.Height = c.height
		first := strings.SplitN(startView(s, 80), "\n", 2)[0]
		if !strings.HasPrefix(first, c.want) {
			t.Fatalf("%s: first row = %q, want it to open %q", c.name, first, c.want)
		}
	}
}

// The trail is the working label's own birth mark, so the name reads as still
// arriving; it thins in spacing because the palette has one dim to spend.
func TestStartScreen_TheWordmarkTrailsOffInBirthMarks(t *testing.T) {
	s := startFixture()
	s.Height = startFaceHeight
	middle := strings.Split(startView(s, 80), "\n")[1]
	if !strings.HasPrefix(middle, startWordmark[1]+" "+animBirthMark) {
		t.Fatalf("the trail should follow the middle row: %q", middle)
	}
	if strings.Count(middle, animBirthMark) != len(startTrailGaps) {
		t.Fatalf("the trail should carry %d marks: %q", len(startTrailGaps), middle)
	}
	// A pane with no room behind the name keeps the name and drops the trail
	// rather than clipping it to a row that ends in nothing visible.
	if got := strings.Split(startView(s, startWordmarkWidth+1), "\n")[1]; got != startWordmark[1] {
		t.Fatalf("a narrow pane should draw the name alone, got %q", got)
	}
}

// The face carries no fact — the path under it is where the reader is going —
// so a palette with two greys to spend declines it whole (invariant 1).
func TestStartScreen_TheFaceIsDeclinedInMono(t *testing.T) {
	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	for _, height := range []int{startFaceHeight - 1, startFaceHeight} {
		s := startFixture()
		s.Height = height
		SetMono(true)
		mono := startView(s, 80)
		s.Height = 0
		SetMono(false)
		if faceless := startView(s, 80); mono != faceless {
			t.Fatalf("height %d: mono should draw the screen with no face:\n%s", height, mono)
		}
	}
}

func TestStartScreen_FocusAfterStaysInTheList(t *testing.T) {
	s := startFixture()
	if got := s.FocusAfter(-1); got != 0 {
		t.Fatalf("FocusAfter(-1) at the top = %d, want 0", got)
	}
	s.Focus = len(s.Suggestions) - 1
	if got := s.FocusAfter(1); got != len(s.Suggestions)-1 {
		t.Fatalf("FocusAfter(1) at the bottom = %d, want %d", got, len(s.Suggestions)-1)
	}
	s.Suggestions = nil
	if got := s.FocusAfter(1); got != 0 {
		t.Fatalf("FocusAfter on an empty list = %d, want 0", got)
	}
}
