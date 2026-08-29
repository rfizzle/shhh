package caps

// The kitty rung against the terminal that answered for it (S-158, §12h).

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

func testImage(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 0x80, 0xff})
		}
	}
	return img
}

// Everything here is spent on an answer, so a terminal that did not give one
// is written nothing at all. A caller asks unconditionally and lets the reply
// decide, which is the shape every other capability is spent in.
func TestGraphics_NothingWithoutTheCapability(t *testing.T) {
	var silent Terminal
	if cmd := silent.Transmit(testImage(8, 8), 4, 2, 9, 19); cmd != nil {
		t.Error("a terminal that never answered was sent a picture")
	}
	if rows := silent.Placement(4, 2); rows != nil {
		t.Error("a terminal that never answered got placement cells")
	}
	if cmd := silent.Delete(); cmd != nil {
		t.Error("a terminal that never answered was asked to forget one")
	}
}

// The cell size is the other half of the answer: a terminal that draws
// pictures but never said how big its cells are cannot be told how many
// pixels to draw, and the half-block picture is what it gets instead.
func TestGraphics_NeedsTheCellSizeToo(t *testing.T) {
	term := Terminal{Asked: true, Kitty: true}
	for _, c := range []struct{ cols, rows, w, h int }{
		{4, 2, 0, 19}, {4, 2, 9, 0}, {0, 2, 9, 19}, {4, 0, 9, 19},
	} {
		if cmd := term.Transmit(testImage(8, 8), c.cols, c.rows, c.w, c.h); cmd != nil {
			t.Errorf("%+v: a picture went out with a missing measurement", c)
		}
	}
	if cmd := term.Transmit(nil, 4, 2, 9, 19); cmd != nil {
		t.Error("no picture should be no transmission")
	}
}

// What goes out is a kitty graphics sequence carrying the id the placement
// will name, at the size the cells will draw.
func TestGraphics_TransmitsAtTheSizeTheCellsWillDraw(t *testing.T) {
	withProfile(t, colorprofile.ANSI256)
	term := Terminal{Asked: true, Kitty: true}
	seq := raw(t, term.Transmit(testImage(64, 32), 20, 5, 9, 19))
	// Direct transmission and RGBA are the encoder's own defaults and are
	// left off the wire; what has to be there is the id, the action, the grid
	// and the virtual placement the placeholders depend on.
	for _, want := range []string{"i=" + itoa(pictureID), "a=T", "c=20", "r=5", "U=1"} {
		if !strings.Contains(seq, want) {
			t.Errorf("the transmission never says %q", want)
		}
	}
	// 20 cells of 9px and 5 of 19px: the picture is scaled to the hole, not
	// sent whole.
	if !strings.Contains(seq, "s=180") || !strings.Contains(seq, "v=95") {
		t.Errorf("the picture was not scaled to 180×95 px")
	}
}

// Inside tmux every sequence has to be told it is passing through, or tmux
// eats it on the way to the terminal that would draw it — the same rule the
// question went out under.
func TestGraphics_WrapsTheTransmissionForTmux(t *testing.T) {
	withProfile(t, colorprofile.ANSI256)
	var term Terminal
	term.Query([]string{"TERM=xterm-ghostty", "TMUX=/tmp/tmux-1/default,1,0"})
	term.Kitty = true
	if seq := raw(t, term.Transmit(testImage(16, 16), 4, 2, 9, 19)); !strings.Contains(seq, "\x1bPtmux;") {
		t.Error("the transmission was not wrapped for tmux")
	}
	if seq := raw(t, term.Delete()); !strings.Contains(seq, "\x1bPtmux;") {
		t.Error("the release was not wrapped for tmux")
	}
}

// A placement is cells, and to everything drawing around it they are
// ordinary one-column cells — which is what keeps the width arithmetic of the
// card holding them true.
func TestGraphics_PlacementIsAGridOfOrdinaryCells(t *testing.T) {
	term := Terminal{Asked: true, Kitty: true}
	rows := term.Placement(20, 5)
	if len(rows) != 5 {
		t.Fatalf("%d placement rows, want 5", len(rows))
	}
	for i, row := range rows {
		if w := ansi.StringWidth(row); w != 20 {
			t.Errorf("row %d is %d columns, want 20", i, w)
		}
		if strings.Count(row, string(kitty.Placeholder)) != 20 {
			t.Errorf("row %d does not carry one placeholder per column", i)
		}
		if !strings.HasSuffix(row, ansi.ResetStyle) {
			t.Errorf("row %d leaves its colour on for whatever follows it", i)
		}
	}
	// The first cell of each row carries that row's coordinates; the terminal
	// continues the row itself.
	for i, row := range rows {
		if !strings.ContainsRune(row, kitty.Diacritic(i)) {
			t.Errorf("row %d never says which row it is", i)
		}
	}
}

// itoa keeps the test reading like the code it checks.
func itoa(n int) string {
	var b strings.Builder
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	for i := len(digits) - 1; i >= 0; i-- {
		b.WriteByte(digits[i])
	}
	return b.String()
}
