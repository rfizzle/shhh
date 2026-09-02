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
