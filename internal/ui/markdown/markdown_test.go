package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// plain is a render reduced to what a reader sees: no escapes, no padding.
func plain(t *testing.T, src string, width int) []string {
	t.Helper()
	var rows []string
	for _, row := range strings.Split(Render(src, Options{Width: width}), "\n") {
		rows = append(rows, strings.TrimRight(ansi.Strip(row), " "))
	}
	return rows
}

func wants(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d:\ngot:  %q\nwant: %q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d:\ngot  %q\nwant %q", i, got[i], want[i])
		}
	}
}

// The four defects this package was written for. Each one is a rendering that
// changed what the text said, so each one is pinned rather than described.

// A loose item's second paragraph is a paragraph. glamour ran the two
// together into `2. secondnested paragraph under item two`, which is not a
// layout choice — it is a different sentence.
func TestLooseListItemKeepsItsParagraphs(t *testing.T) {
	wants(t, plain(t, "1. first\n2. second\n\n   nested paragraph under item two\n", 60), []string{
		"  1. first",
		"",
		"  2. second",
		"",
		"     nested paragraph under item two",
	})
}

// A wrapped item hangs under its text, not under its marker. Starting the
// continuation at the bullet's column reads as a second item.
func TestWrappedListItemHangsUnderItsText(t *testing.T) {
	rows := plain(t, "- bullet with a line long enough to wrap somewhere near the middle\n", 40)
	if len(rows) < 2 {
		t.Fatalf("expected a wrap, got %q", rows)
	}
	if !strings.HasPrefix(rows[0], "  • ") {
		t.Fatalf("first row = %q", rows[0])
	}
	if !strings.HasPrefix(rows[1], "    ") || strings.HasPrefix(strings.TrimLeft(rows[1], " "), "•") {
		t.Fatalf("continuation should hang under the text, got %q", rows[1])
	}
}

// Code is folded at the column, never wrapped between words. The test is the
// strong form: the rows concatenate back to exactly the source line, so
// nothing moved, nothing was dropped, and no space at a fold boundary was
// quietly eaten.
//
// Only the last row is trimmed. Every other row is exactly the fold width, so
// trimming them all would delete a real trailing space and call it padding —
// which is what an earlier version of this test did, and it reported the
// renderer as broken when the renderer was right.
func TestCodeIsFoldedNotReflowed(t *testing.T) {
	const line = `func main() { fmt.Println("a code line that is quite long and will exceed the width") }`
	const width = 40
	rows := strings.Split(Render("```go\n"+line+"\n```\n", Options{Width: width, Mono: true}), "\n")
	if len(rows) < 2 {
		t.Fatalf("expected the long line to fold, got %q", rows)
	}
	prefix := strings.Repeat(" ", Margin+codeIndent)
	var joined strings.Builder
	for i, row := range rows {
		body := strings.TrimPrefix(row, prefix)
		if i == len(rows)-1 {
			body = strings.TrimRight(body, " ")
		}
		joined.WriteString(body)
	}
	if got := joined.String(); got != line {
		t.Errorf("folded code does not reconstruct the source:\ngot  %q\nwant %q", got, line)
	}
}

// A tab in a code block is drawn as spaces, because a literal tab measures
// zero and occupies eight — which pads the row to the wrong width and pushes
// it past the pane.
func TestTabsInCodeBecomeSpaces(t *testing.T) {
	const width = 40
	out := Render("```go\nfunc f() {\n\treturn 1\n}\n```\n", Options{Width: width, Mono: true})
	if strings.ContainsRune(out, '\t') {
		t.Errorf("a tab survived into the render: %q", out)
	}
	for _, row := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(row); w > (Options{Width: width}).FillWidth() {
			t.Errorf("row %q is %d wide, past the pane", row, w)
		}
	}
}

// Padding is plain. It is still there — the selection reads it as the record
// of how far the wrapper was allowed to go — but a space carries no colour,
// so it carries no escape either.
func TestPaddingCostsNothing(t *testing.T) {
	const src = "A paragraph long enough to fill more than one row at this width, twice over.\n"
	out := Render(src, Options{Width: 60})
	visible := len(ansi.Strip(out))
	if ratio := float64(len(out)) / float64(visible); ratio > 1.5 {
		t.Errorf("%d bytes for %d visible (%.1fx); the padding is carrying escapes again", len(out), visible, ratio)
	}
	// And it is really padding, not a trim: every row but the last is filled.
	rows := strings.Split(out, "\n")
	for i, row := range rows[:len(rows)-1] {
		if w := ansi.StringWidth(row); w != (Options{Width: 60}).FillWidth() {
			t.Errorf("row %d is %d wide, want %d", i, w, (Options{Width: 60}).FillWidth())
		}
	}
}

// Mono is the invariant applied to prose: when the colour goes the marks come
// back, and the render carries no escapes at all.
func TestMonoKeepsTheMarksAndDropsTheEscapes(t *testing.T) {
	out := Render("# Title\n\n**bold** and *it* and `name` and ~~gone~~\n", Options{Width: 60, Mono: true})
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("mono render carries escapes: %q", out)
	}
	for _, want := range []string{"# Title", "**bold**", "*it*", "`name`", "~~gone~~"} {
		if !strings.Contains(out, want) {
			t.Errorf("mono render lost %s:\n%s", want, out)
		}
	}
}

// In colour the marks are gone and the treatment carries them instead, which
// is the other half of the same rule.
func TestColourDropsTheMarks(t *testing.T) {
	out := ansi.Strip(Render("**bold** and `name`\n", Options{Width: 60}))
	for _, gone := range []string{"**", "`"} {
		if strings.Contains(out, gone) {
			t.Errorf("colour render kept %q: %q", gone, out)
		}
	}
	if !strings.Contains(out, "bold") || !strings.Contains(out, "name") {
		t.Errorf("colour render lost the content: %q", out)
	}
}

// The seam between two top-level blocks is one padded blank row, always. The
// streaming cache writes that row itself rather than measuring it, so a change
// here is a change to the glue (chat/streammd.go).
func TestSeamIsOnePaddedBlankRow(t *testing.T) {
	o := Options{Width: 60}
	rows := Blocks("first paragraph\n\nsecond paragraph\n", o)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %q", len(rows), rows)
	}
	if want := strings.Repeat(" ", o.FillWidth()); rows[1] != want {
		t.Errorf("seam = %q, want %q", rows[1], want)
	}
}

// Gluing two renders at a block boundary is the render of the whole, byte for
// byte. Everything the streaming cache does rests on this.
func TestGluedBlocksAreTheWholeRender(t *testing.T) {
	o := Options{Width: 50}
	const head = "A first paragraph that runs on a bit.\n\n- a list\n- of two items\n"
	const tail = "```go\nx := 1\n```\n\nAnd a closing paragraph.\n"
	glued := append(append([]string{}, Blocks(head, o)...), strings.Repeat(" ", o.FillWidth()))
	glued = append(glued, Blocks(tail, o)...)
	if got, want := strings.Join(glued, "\n"), Render(head+"\n"+tail, o); got != want {
		t.Errorf("glued render differs from the whole:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// A pane too narrow to lay anything out still returns rows rather than
// panicking or looping: the floor is a floor, not a layout.
func TestNarrowPaneStillRenders(t *testing.T) {
	for _, width := range []int{0, 1, 3, 5} {
		out := Render("# Title\n\n- an item that cannot fit\n\n```\ncode\n```\n", Options{Width: width})
		if out == "" {
			t.Errorf("width %d rendered nothing", width)
		}
	}
}

// What the renderer this replaced could do, and this one therefore must.
//
// glamour's extension set was GFM plus definition lists plus emoji, and it
// emitted OSC 8 hyperlinks. Each of these was found missing after the swap by
// asking the question rather than assuming the answer, so each is pinned.

func TestTaskListKeepsItsBoxes(t *testing.T) {
	wants(t, plain(t, "- [ ] not done\n- [x] done\n", 40), []string{
		"  [ ] not done",
		"  [x] done",
	})
}

// A task item's marker is the box. Drawing a bullet as well gives it two.
func TestTaskItemHasOneMarker(t *testing.T) {
	rows := plain(t, "- [ ] a task item long enough to wrap at this narrow width\n", 30)
	if strings.Contains(rows[0], "•") {
		t.Errorf("task item drew a bullet as well as a box: %q", rows[0])
	}
	if len(rows) > 1 && !strings.HasPrefix(rows[1], strings.Repeat(" ", Margin+TaskBoxWidth)) {
		t.Errorf("continuation should hang under the text, got %q", rows[1])
	}
}

func TestEmojiShortcodesResolve(t *testing.T) {
	if got := plain(t, "ship it :tada: now\n", 40)[0]; !strings.Contains(got, "🎉") {
		t.Errorf("emoji shortcode not resolved: %q", got)
	}
}

// Entities and backslash escapes are syntax, and the reader should see what
// they stand for. goldmark hands them over raw and expects the renderer to
// resolve them.
func TestEntitiesAndEscapesResolve(t *testing.T) {
	if got := plain(t, "a &amp; b &lt;c&gt;\n", 40)[0]; got != "  a & b <c>" {
		t.Errorf("entities: got %q", got)
	}
	if got := plain(t, `a \*not emphasis\* b`, 40)[0]; got != "  a *not emphasis* b" {
		t.Errorf("escapes: got %q", got)
	}
	// Inside a code span they are characters, not syntax.
	if got := plain(t, "`a &amp; b`\n", 40)[0]; !strings.Contains(got, "&amp;") {
		t.Errorf("a code span should keep its entity literal: %q", got)
	}
}

func TestDefinitionListIndentsItsDescription(t *testing.T) {
	wants(t, plain(t, "Term\n: definition\n", 40), []string{
		"  Term",
		"    definition",
	})
}

// A link is clickable where the terminal supports it, and still prints its
// URL for the terminals that do not.
func TestLinksAreClickable(t *testing.T) {
	out := Render("see [the docs](https://example.com/a)\n", Options{Width: 60})
	if !strings.Contains(out, "\x1b]8;;https://example.com/a") {
		t.Errorf("no OSC 8 hyperlink in %q", out)
	}
	if !strings.Contains(ansi.Strip(out), "https://example.com/a") {
		t.Errorf("the URL should still be printed: %q", ansi.Strip(out))
	}
	// And it costs nothing to measure: a hyperlink is zero width, so the row
	// is padded exactly as any other.
	for _, row := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(row); w != (Options{Width: 60}).FillWidth() && row != strings.Split(out, "\n")[len(strings.Split(out, "\n"))-1] {
			t.Errorf("row %q measures %d", row, w)
		}
	}
}

// A cell too wide for its column wraps. Cutting it drops the end of a
// sentence and says nothing about having done so.
func TestTableCellsWrapRatherThanTruncate(t *testing.T) {
	const cell = "a cell whose content is far too wide to fit the pane given"
	rows := plain(t, "| col |\n|---|\n| "+cell+" |\n", 40)
	var body strings.Builder
	for _, row := range rows[2:] {
		body.WriteString(strings.TrimSpace(row) + " ")
	}
	for _, word := range strings.Fields(cell) {
		if !strings.Contains(body.String(), word) {
			t.Fatalf("the table dropped %q:\n%s", word, strings.Join(rows, "\n"))
		}
	}
}

// The alignment is the author's: `|---:|` means the column is numbers.
func TestTableHonoursAlignment(t *testing.T) {
	rows := plain(t, "| l | c | r |\n|:--|:-:|--:|\n| a | b | c |\n", 46)
	last := rows[len(rows)-1]
	if !strings.HasPrefix(last, "  a ") {
		t.Errorf("left column not left-justified: %q", last)
	}
	if !strings.HasSuffix(last, "c") {
		t.Errorf("right column not right-justified: %q", last)
	}
}

// A link is drawn as one run however long it is.
//
// This is the trap, not a hypothetical. lipgloss v2 renders Underline and
// Strikethrough a character at a time, and the wrapper used to draw each word
// separately, so an underlined eight-letter label came back as eight escape
// pairs wrapped in eight OSC 8 hyperlinks — which some terminals show as
// eight separate links. The register carries a link with colour and one
// hyperlink instead, and the wrapper coalesces a run that spans words.
//
// The assertion is that the count does not grow with the label, rather than
// that it equals a number: whether the label and the URL beside it share a
// run depends on whether the terminal reported colour, and both answers are
// correct.
func TestALinkIsOneRunHoweverLongItIs(t *testing.T) {
	const url = "https://example.com/a"
	counts := map[int]bool{}
	for _, label := range []string{"x", "the docs", "a rather longer label than that one"} {
		out := Render("see ["+label+"]("+url+") now\n", Options{Width: 90})
		n := strings.Count(out, ansi.SetHyperlink(url))
		if n < 1 || n > 2 {
			t.Errorf("label %q: %d hyperlink runs, want 1 or 2", label, n)
		}
		counts[n] = true
	}
	if len(counts) != 1 {
		t.Errorf("the hyperlink run count grew with the label: %v", counts)
	}
}

// The same coalescing for ordinary emphasis: a bold phrase is one escape
// pair, not one per word.
func TestAStyledPhraseIsOneRun(t *testing.T) {
	out := Render("a **bold phrase of several words** here\n", Options{Width: 90})
	if n := strings.Count(out, "\x1b[1m"); n != 1 {
		t.Errorf("%d bold runs, want 1: %q", n, out)
	}
}
