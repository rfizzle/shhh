package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// plainGutter is the gutter with its colour taken off — the glyph column a
// reader on a monochrome terminal sees, which is the whole of what it says
// (invariant 1).
func plainGutter(rows []string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(ansi.Strip(r))
	}
	return b.String()
}

func TestScrollbar_NothingToScrollDrawsNothing(t *testing.T) {
	for _, c := range []struct {
		name                              string
		height, content, viewport, offset int
	}{
		{"content shorter than the pane", 10, 4, 10, 0},
		{"content exactly the pane", 10, 10, 10, 0},
		{"no pane at all", 0, 100, 10, 0},
		{"no viewport", 10, 100, 0, 0},
	} {
		if rows := Scrollbar(c.height, c.content, c.viewport, c.offset); rows != nil {
			t.Fatalf("%s: gutter should be empty, got %q", c.name, plainGutter(rows))
		}
	}
}

func TestScrollbar_ThumbIsTheVisibleShare(t *testing.T) {
	for _, c := range []struct {
		name                              string
		height, content, viewport, offset int
		want                              string
	}{
		{"half the transcript fits", 10, 20, 10, 0, "┃┃┃┃┃│││││"},
		{"half, scrolled to the end", 10, 20, 10, 10, "│││││┃┃┃┃┃"},
		{"half, one line in", 10, 20, 10, 1, "│┃┃┃┃┃││││"},
		{"a fifth fits", 10, 50, 10, 0, "┃┃││││││││"},
		{"a transcript far longer than the pane", 8, 4000, 8, 0, "┃│││││││"},
		{"and at its end", 8, 4000, 8, 3992, "│││││││┃"},
	} {
		got := plainGutter(Scrollbar(c.height, c.content, c.viewport, c.offset))
		if got != c.want {
			t.Fatalf("%s: gutter = %q, want %q", c.name, got, c.want)
		}
	}
}

// The thumb touching an end is a claim about the transcript, not a rounding
// artefact: it leaves the top as soon as one line is above it, and returns to
// the bottom only at the live end.
func TestScrollbar_TouchesAnEndOnlyAtThatEnd(t *testing.T) {
	const height, content, viewport = 20, 400, 20
	maxOffset := content - viewport
	for offset := 1; offset < maxOffset; offset++ {
		rows := plainGutter(Scrollbar(height, content, viewport, offset))
		if strings.HasPrefix(rows, scrollThumb) {
			t.Fatalf("offset %d: thumb sits on the top with %d lines above", offset, offset)
		}
		if strings.HasSuffix(rows, scrollThumb) {
			t.Fatalf("offset %d: thumb sits on the bottom with %d lines below", offset, maxOffset-offset)
		}
	}
	if !strings.HasPrefix(plainGutter(Scrollbar(height, content, viewport, 0)), scrollThumb) {
		t.Fatal("at the top the thumb is on the top row")
	}
	if !strings.HasSuffix(plainGutter(Scrollbar(height, content, viewport, maxOffset)), scrollThumb) {
		t.Fatal("at the live end the thumb is on the last row")
	}
}

// An offset outside the transcript is clamped rather than drawn: a pane that
// grew under a scrolled reader reports one for the frame before syncViewport
// catches up.
func TestScrollbar_ClampsAnImpossibleOffset(t *testing.T) {
	for _, c := range []struct {
		offset int
		want   string
	}{
		{11, "│││││┃┃┃┃┃"},
		{400, "│││││┃┃┃┃┃"},
		{-3, "┃┃┃┃┃│││││"},
	} {
		if got := plainGutter(Scrollbar(10, 20, 10, c.offset)); got != c.want {
			t.Fatalf("offset %d: gutter = %q, want %q", c.offset, got, c.want)
		}
	}
}

// The gutter is one column wide everywhere, in both palettes: a row of it is
// one cell, so the pane's reservation is never wrong.
func TestScrollbar_IsOneColumnWide(t *testing.T) {
	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	for _, mono := range []bool{false, true} {
		SetMono(mono)
		for _, row := range Scrollbar(6, 60, 6, 20) {
			if w := ansi.StringWidth(ansi.Strip(row)); w != ScrollGutterWidth {
				t.Fatalf("mono=%v: a gutter row is %d columns, want %d", mono, w, ScrollGutterWidth)
			}
		}
	}
}
