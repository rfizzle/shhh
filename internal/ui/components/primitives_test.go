package components

// The primitives audit (S-126, docs/interface/departures.md). Seven Go
// renderers were written before the design system had a page to check them
// against; these are the divergences the audit closed, held open as tests so
// the two cannot drift apart again the quiet way they did the first time.
//
// What is asserted here is what a reader sees on the row — the description
// column, the meta field, the numbering column — rather than the shape of the
// props behind it. A divergence in naming is not a finding; one that changes
// what is on screen is.

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// rowsOf strips a card down to the option rows inside it, in order.
func rowsOf(view string) []string {
	var out []string
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if !strings.HasPrefix(line, "│") {
			continue
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │"))
	}
	return out
}

// columnOf is the display column text starts at in a row, counted in
// characters: a pointer glyph is three bytes and one column.
func columnOf(row, text string) int {
	i := strings.Index(row, text)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(row[:i])
}

// rowWith is the first option row carrying label, or "" when none does.
func rowWith(view, label string) string {
	for _, row := range rowsOf(view) {
		if strings.Contains(row, label) {
			return row
		}
	}
	return ""
}

// priced is the catalog shape the audit is about: every option carries its
// own words, and the short field at the end of the row carries its price.
func priced(n int) []SelectOption {
	names := []string{
		"gpt-5.2", "gpt-5.2-mini", "claude-opus-4.6", "claude-sonnet-4.6",
		"claude-haiku-4.5", "gemini-3-pro", "gemini-3-flash", "grok-4.1",
		"deepseek-r2", "deepseek-v4", "qwen3-coder-72b", "mistral-large-3",
	}
	opts := make([]SelectOption, 0, n)
	for i := 0; i < n; i++ {
		opts = append(opts, SelectOption{
			Label: names[i%len(names)],
			Desc:  "what this one is for",
			Meta:  "$3 / $15",
		})
	}
	return opts
}

// Every row carries its own description, not only the row the pointer
// happens to be on. A catalog you have to walk to read is a catalog you
// cannot compare, which is the whole reason /model shows prices at all.
func TestPrimitives_DescriptionRidesEveryRow(t *testing.T) {
	s := &Select{Title: "Switch model", Options: []SelectOption{
		{Label: "gpt-5.2", Desc: "current default"},
		{Label: "claude-opus-4.6", Desc: "deepest reasoning"},
	}}
	view := s.View(70)
	for _, want := range []string{"current default", "deepest reasoning"} {
		row := rowWith(view, want)
		if row == "" {
			t.Fatalf("every row should carry its own description, %q is missing:\n%s", want, view)
		}
		if !strings.Contains(row, "gpt-5.2") && !strings.Contains(row, "claude-opus-4.6") {
			t.Fatalf("the description belongs on the option's own row, got %q:\n%s", row, view)
		}
	}
}

// The meta field ends at the card's inner edge, on the focused row and on the
// others alike: it is a column, so it has to be readable as one.
func TestPrimitives_MetaEndsAtTheCardEdge(t *testing.T) {
	s := &Select{Title: "Switch model", Options: priced(4)}
	view := s.View(70)
	rows := rowsOf(view)
	inner := 70 - cardFrameWidth
	for _, row := range rows {
		if !strings.Contains(row, "$3 / $15") {
			continue
		}
		if got := strings.TrimRight(row, " "); !strings.HasSuffix(got, "$3 / $15") {
			t.Fatalf("the meta field should end the row, got %q:\n%s", row, view)
		}
		if got := utf8.RuneCountInString(row); got > inner {
			t.Fatalf("the row overran the card interior (%d > %d):\n%s", got, inner, view)
		}
	}
}

// Option 9 and option 10 start their labels in the same column: the number is
// right-aligned in a column of its own, so a list of a dozen entries does not
// step sideways at ten.
func TestPrimitives_NumberingColumnRightAligns(t *testing.T) {
	s := &Select{Title: "Switch model", Options: priced(12)}
	view := s.View(90)
	ninth, tenth := rowWith(view, "deepseek-r2"), rowWith(view, "deepseek-v4")
	if ninth == "" || tenth == "" {
		t.Fatalf("both options should be on the card:\n%s", view)
	}
	a, b := columnOf(ninth, "deepseek-r2"), columnOf(tenth, "deepseek-v4")
	if a != b {
		t.Fatalf("labels should start in the same column, got %d and %d:\n%s", a, b, view)
	}
}

// The description column is measured over the whole list, not over the
// window, so it does not shift under the reader as the window slides.
func TestPrimitives_DescriptionColumnHoldsStillWhileTheWindowSlides(t *testing.T) {
	s := &Select{Title: "Switch model", Options: priced(12), MaxLines: 8}
	head := rowsOf(s.View(90))
	for i := 0; i < 11; i++ {
		s.View(90)
		s.Update(key("down"))
	}
	tail := rowsOf(s.View(90))
	column := func(rows []string) int {
		for _, row := range rows {
			if i := columnOf(row, "what this one is for"); i >= 0 {
				return i
			}
		}
		return -1
	}
	if a, b := column(head), column(tail); a < 0 || a != b {
		t.Fatalf("the description column moved with the window: %d then %d", a, b)
	}
}

// A list where nothing has a description or a meta field spends no columns on
// either — a fixed menu of answers renders exactly as wide as its rows.
func TestPrimitives_APlainMenuSpendsNoColumnsOnEmptyOnes(t *testing.T) {
	s := &Select{Title: "Switch mode", Options: []SelectOption{
		{Label: "manual"}, {Label: "auto"}, {Label: "plan"},
	}}
	for _, row := range rowsOf(s.View(70)) {
		if !strings.Contains(row, "manual") {
			continue
		}
		if got := strings.TrimRight(row, " "); got != "❯ 1. manual" {
			t.Fatalf("a menu with nothing to continue should be label-tight, got %q", got)
		}
	}
}

// The plan card is the one list that keeps its descriptions under the focus:
// there they are consequences of taking the option, and four consequences
// stacked at once is a wall rather than a choice (§4d).
func TestPrimitives_PlanCardKeepsItsConsequenceUnderTheFocus(t *testing.T) {
	c := &PlanCard{Title: "Plan", Options: []SelectOption{
		{Label: "run the whole plan", Desc: "4 steps, stopping only for the two edits"},
		{Label: "step through it", Desc: "every edit and every command asks you first"},
	}}
	view := ansi.Strip(c.View(90))
	if !strings.Contains(view, "run the whole plan") {
		t.Fatalf("the options should render:\n%s", view)
	}
	if strings.Contains(rowWith(c.View(90), "run the whole plan"), "4 steps") {
		t.Fatalf("the plan card's consequence belongs under its option, not on it:\n%s", view)
	}
	if !strings.Contains(view, "4 steps, stopping only for the two edits") {
		t.Fatalf("the focused option's consequence should show:\n%s", view)
	}
	if strings.Contains(view, "every edit and every command asks you first") {
		t.Fatalf("only the focused option explains itself:\n%s", view)
	}
}

// An unavailable row keeps the ⊘ that says so and the words that say why —
// in both palettes, because a shade alone survives neither mono nor a reader
// who cannot tell the two greys apart (invariant 1).
func TestPrimitives_UnavailableRowSaysSoInGlyphAndInWords(t *testing.T) {
	s := &Select{Title: "Switch model", Options: []SelectOption{
		{Label: "gpt-5.2"},
		{Label: "deepseek-r2", Desc: "no tool use", Meta: "not usable here", Dim: true},
	}}
	for _, want := range []bool{false, true} {
		was := mono
		SetMono(want)
		row := rowWith(s.View(80), "deepseek-r2")
		SetMono(was)
		for _, field := range []string{"⊘", "no tool use", "not usable here"} {
			if !strings.Contains(row, field) {
				t.Fatalf("mono=%v: the row should carry %q, got %q", want, field, row)
			}
		}
	}
}

// The row sheds from the right: the meta field is one clause and survives,
// the description is the row explaining itself and gives up its width first.
func TestPrimitives_ANarrowRowKeepsTheMetaAndGivesUpTheDescription(t *testing.T) {
	s := &Select{Title: "m", Options: []SelectOption{
		{Label: "claude-sonnet-4.6", Desc: "a description far too long for this card", Meta: "$3 / $15"},
	}}
	row := rowWith(s.View(40), "claude-sonnet-4.6")
	if !strings.Contains(row, "$3 / $15") {
		t.Fatalf("the meta field should survive the narrow row, got %q", row)
	}
	if strings.Contains(row, "a description far too long") {
		t.Fatalf("the description should have given up its width, got %q", row)
	}
}

// The query row names what the key was for until something is typed into it,
// and then stops: from that point the row is showing what it is doing.
func TestPrimitives_QueryHintGoesOnceAnythingIsTyped(t *testing.T) {
	s := &Select{Title: "Switch model", Options: priced(4),
		Filterable: true, Filtering: true, QueryHint: "type to filter"}
	if !strings.Contains(ansi.Strip(s.View(70)), "type to filter") {
		t.Fatalf("an empty query row should say what it is for:\n%s", s.View(70))
	}
	s.Update(key("g"))
	if strings.Contains(ansi.Strip(s.View(70)), "type to filter") {
		t.Fatalf("the placeholder should go once the row has content:\n%s", s.View(70))
	}
}

// A staging list states what each hunk costs on the hunk's own row: the
// counts are what you are deciding about (§4b).
func TestPrimitives_MultiSelectStatesItsCountsOnTheRow(t *testing.T) {
	s := NewMultiSelect("Apply which hunks?", []SelectOption{
		{Label: "@@ -84,9   return the sentinel", Meta: "+2 −1"},
		{Label: "@@ -131,4  handle it in Run", Meta: "+9 −1"},
	})
	view := s.View(70)
	row := rowWith(view, "return the sentinel")
	if !strings.Contains(row, "+2 −1") {
		t.Fatalf("the row should state its counts, got %q:\n%s", row, view)
	}
	if got := strings.TrimRight(row, " "); !strings.HasSuffix(got, "+2 −1") {
		t.Fatalf("the counts should end the row, got %q", got)
	}
}
