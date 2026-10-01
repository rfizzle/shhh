package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// startFixture is the screen every test here starts from: the facts a Go
// checkout with a dirty tree produces, the two notes, and the three offers.
func startFixture() StartScreen {
	return StartScreen{
		Facts: []StartFact{
			{Text: "go 1.24"},
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
		Hint: []KeyOffer{
			{Key: "[↑↓]", Label: "choose"},
			{Key: "[enter]", Label: "start"},
			{Key: "[ctrl+]]", Label: "keys"},
		},
		Typing: "or just type what you want",
	}
}

// startFirstRunFixture is the block a first session carries, in the host's
// words.
var startFirstRunFixture = []string{
	"enter sends what you type",
	"esc backs out of anything and never loses work",
	"ctrl+c twice stops a run",
}

func startView(s StartScreen, width int) string { return ansi.Strip(s.View(width)) }

func TestStartScreen_StatesWhatItAlreadyKnows(t *testing.T) {
	view := startView(startFixture(), 110)
	for _, want := range []string{
		"go 1.24", "3 files changed", "41 packages",
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

func TestStartScreen_FactsDropFromTheRightAndKeepTheFirst(t *testing.T) {
	s := startFixture()
	line := strings.Split(startView(s, 30), "\n")[0]
	if !strings.Contains(line, "go 1.24") {
		t.Fatalf("the first clause was dropped from a narrow line: %q", line)
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

// The one screen a new reader sees first writes its keys in the notation
// every other surface uses: the key in Key because this screen answers it,
// the words beside it in Dim, and the answer that costs nothing in Add. It is
// the shared painter that decides all three, so the screen cannot drift from
// the rest of the product on its own.
func TestStartScreen_KeyRowsWearTheBracketGrammar(t *testing.T) {
	s := startFixture()
	s.Nav = []KeyOffer{
		keyOfferAs(keys.Draft.PageUp, "scroll"),
		keyOfferAs(keys.Select.Cancel, backToPrompt),
	}
	view := s.View(110)
	for _, want := range []struct{ what, run string }{
		{"the list's own key", sty.Key.Render("[↑↓]") + sty.Dim.Render(" choose")},
		{"a navigation key", sty.Key.Render(keys.Bracket(keys.Draft.PageUp)) + sty.Dim.Render(" scroll")},
		{"the safe answer", sty.Add.Render("[esc]") + sty.Dim.Render(" "+backToPrompt)},
		{"the clause that is not a key", sty.Dim.Render("or just type what you want")},
	} {
		if !strings.Contains(view, want.run) {
			t.Fatalf("%s is not painted in the bracket grammar:\n%q", want.what, view)
		}
	}
}

// The typing clause is the alternative to the keys rather than one of them,
// and the one thing a first-time reader needs from the row, so it leads the
// one row where there is room and takes a row of its own above the keys
// where there is not — it is never dropped.
func TestStartScreen_TheTypingClauseLeadsTheOneRow(t *testing.T) {
	s := startFixture()
	s.Facts, s.Notes, s.Suggestions, s.Lead = nil, nil, nil, ""
	wide := strings.Split(strings.TrimLeft(startView(s, 110), "\n"), "\n")
	if len(wide) != 1 || !strings.HasPrefix(wide[0], "or just type what you want · [↑↓] choose") {
		t.Fatalf("a wide row should be one row led by the clause: %q", wide)
	}
	narrow := strings.Split(strings.TrimLeft(startView(s, 30), "\n"), "\n")
	if len(narrow) < 2 || narrow[0] != "or just type what you want" {
		t.Fatalf("a narrow row should give the clause a row of its own: %q", narrow)
	}
}

// The first session's block sits above the key row it introduces,
// and a session with none draws exactly what it drew before.
func TestStartScreen_TheFirstRunBlockSitsAboveTheKeyRow(t *testing.T) {
	s := startFixture()
	without := startView(s, 110)
	s.FirstRun = startFirstRunFixture
	rows := strings.Split(startView(s, 110), "\n")
	at := -1
	for i, row := range rows {
		if row == startFirstRunFixture[0] {
			at = i
		}
	}
	if at < 1 || rows[at-1] != "" {
		t.Fatalf("the block should open after a blank row:\n%s", strings.Join(rows, "\n"))
	}
	for i, want := range startFirstRunFixture {
		if rows[at+i] != want {
			t.Fatalf("block row %d = %q, want %q", i, rows[at+i], want)
		}
	}
	if gap, next := rows[at+len(startFirstRunFixture)], rows[at+len(startFirstRunFixture)+1]; gap != "" ||
		!strings.HasPrefix(next, "or just type what you want") {
		t.Fatalf("the key row should follow the block after one blank row, got %q then %q", gap, next)
	}
	if strings.Contains(without, startFirstRunFixture[1]) {
		t.Fatalf("a later session drew the block:\n%s", without)
	}
}

func TestStartScreen_WithoutSuggestionsTheKeysGoToo(t *testing.T) {
	// Typing dismisses the list; a key line with nothing to choose from is an
	// offer nothing accepts.
	s := startFixture()
	s.Suggestions, s.Lead, s.Hint, s.Typing = nil, "", nil, ""
	view := startView(s, 110)
	if strings.Contains(view, "[↑↓]") || strings.Contains(view, "worth doing first") {
		t.Fatalf("the dismissed list left its chrome behind:\n%s", view)
	}
	if !strings.Contains(view, "go 1.24") {
		t.Fatalf("dismissing the list took the facts with it:\n%s", view)
	}
}

// The face is the one thing on this screen worth rows rather than
// information, so it is the one thing the pane's height decides: three rows
// and a trail where there is room, one row of the name in the rule where
// there is not, and nothing at all from a host that never said how tall it is.
func TestStartScreen_TheFaceIsWhatTheHeightAllows(t *testing.T) {
	s := startFixture()
	for _, c := range []struct {
		name   string
		height int
		want   string
	}{
		{"a host that did not say", 0, "go 1.24"},
		{"a short pane", startFaceHeight - 1, "── shhh ─"},
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

// The face carries no fact — the line under it is where the reader is going —
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

// OfferAt answers a click: the rows an offer draws name it, wrapped detail
// included, and every other row names nothing.
func TestStartScreen_OfferAtNamesTheOffersRowsAndNothingElse(t *testing.T) {
	for _, width := range []int{110, 50} {
		s := startFixture()
		rows := strings.Split(startView(s, width), "\n")
		found := map[int]int{}
		for i, row := range rows {
			idx, ok := s.OfferAt(width, i)
			title := strings.Contains(row, "pick up") || strings.Contains(row, "explain what") ||
				strings.Contains(row, "quality gate")
			if title && !ok {
				t.Fatalf("w%d row %d draws an offer and names none: %q", width, i, row)
			}
			if ok && !title && width == 110 {
				t.Fatalf("w%d row %d is not an offer and yet names %d: %q", width, i, idx, row)
			}
			if ok {
				found[idx]++
			}
		}
		if len(found) != 3 {
			t.Fatalf("w%d should name three offers, found %v", width, found)
		}
		if width == 50 && found[2] < 2 {
			t.Fatalf("at w50 the third offer's detail wraps, and both rows should name it: %v", found)
		}
	}
	if _, ok := startFixture().OfferAt(110, -1); ok {
		t.Fatal("a row above the screen names nothing")
	}
}
