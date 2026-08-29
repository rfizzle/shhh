package raster

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// bands builds a picture of solid horizontal bands, top to bottom, with an
// alpha per band. It is what every test here reasons about: a sample's colour
// is the band it landed in, so a wrong grid shows up as the wrong band rather
// than as a slightly different number.
func bands(w, h int, cols ...color.NRGBA) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		c := cols[y*len(cols)/h]
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

var (
	red   = color.NRGBA{0xff, 0x00, 0x00, 0xff}
	blue  = color.NRGBA{0x00, 0x00, 0xff, 0xff}
	white = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	black = color.NRGBA{0x00, 0x00, 0x00, 0xff}
	clear = color.NRGBA{0x00, 0x00, 0x00, 0x00}
)

// The grid a picture gets has the picture's own proportion once the cells'
// shape is accounted for: a square picture on cells twice as tall as they are
// wide is twice as many columns as rows.
func TestFit_KeepsTheProportion(t *testing.T) {
	for _, c := range []struct {
		name             string
		w, h             int
		maxCols, maxRows int
		cell             Aspect
		cols, rows       int
	}{
		{"a square picture on 1×2 cells", 100, 100, 200, 200, DefaultAspect, 200, 100},
		{"twice as wide as tall", 200, 100, 200, 200, DefaultAspect, 200, 50},
		{"square cells give a square grid", 100, 100, 200, 200, Aspect{1, 1}, 200, 200},
		{"a real terminal's cells", 100, 100, 200, 200, Aspect{9, 19}, 200, 94},
	} {
		cols, rows := Fit(bands(c.w, c.h, red), c.maxCols, c.maxRows, c.cell)
		if cols != c.cols || rows != c.rows {
			t.Errorf("%s: %d×%d cells, want %d×%d", c.name, cols, rows, c.cols, c.rows)
		}
	}
}

// Whichever edge runs out first is the one that binds, and neither is ever
// overrun: a card that reserved ten rows never gets eleven.
func TestFit_BindsOnWhicheverEdgeRunsOutFirst(t *testing.T) {
	// Wide and short: the height binds long before the width does.
	cols, rows := Fit(bands(100, 400, red), 200, 10, DefaultAspect)
	if rows != 10 || cols != 5 {
		t.Fatalf("a tall picture in a short space = %d×%d cells, want 5×10", cols, rows)
	}
	// And a picture too small to fill either still gets at least one cell.
	if cols, rows = Fit(bands(1, 4000, red), 40, 10, DefaultAspect); cols != 1 || rows != 10 {
		t.Fatalf("a one-pixel column = %d×%d cells, want 1×10", cols, rows)
	}
}

func TestFit_NothingToDraw(t *testing.T) {
	if cols, rows := Fit(nil, 40, 10, DefaultAspect); cols != 0 || rows != 0 {
		t.Fatalf("no picture = %d×%d cells, want 0×0", cols, rows)
	}
	if cols, rows := Fit(bands(10, 10, red), 0, 10, DefaultAspect); cols != 0 || rows != 0 {
		t.Fatalf("no room = %d×%d cells, want 0×0", cols, rows)
	}
}

// A cell holds two samples stacked: the lower one is the glyph's foreground
// and the upper one its background, which is the whole reason half-blocks are
// the colour rung rather than one sample per cell.
func TestHalfblocks_AreTwoSamplesPerCell(t *testing.T) {
	cells := Halfblocks(bands(4, 4, red, blue), 1, 1)
	got := cells[0][0]
	if got.Rune != lowerHalf {
		t.Fatalf("glyph = %q, want %q", got.Rune, lowerHalf)
	}
	if !sameRGB(got.Fg, blue) {
		t.Errorf("foreground is the lower half: got %v, want blue", got.Fg)
	}
	if !sameRGB(got.Bg, red) {
		t.Errorf("background is the upper half: got %v, want red", got.Bg)
	}
}

// A transparent patch is not a black one. The picture's own holes stay holes,
// so a screenshot with a rounded corner does not grow a square of ink.
func TestHalfblocks_LeaveTransparencyUnpainted(t *testing.T) {
	for _, c := range []struct {
		name     string
		top, bot color.NRGBA
		glyph    rune
		fg, bg   bool
	}{
		{"both halves clear", clear, clear, noInk, false, false},
		{"only the lower half painted", clear, blue, lowerHalf, true, false},
		{"only the upper half painted", red, clear, upperHalf, true, false},
		{"both painted", red, blue, lowerHalf, true, true},
	} {
		got := Halfblocks(bands(4, 4, c.top, c.bot), 1, 1)[0][0]
		if got.Rune != c.glyph {
			t.Errorf("%s: glyph = %q, want %q", c.name, got.Rune, c.glyph)
		}
		if (got.Fg != nil) != c.fg || (got.Bg != nil) != c.bg {
			t.Errorf("%s: fg=%v bg=%v, want fg set %v / bg set %v", c.name, got.Fg, got.Bg, c.fg, c.bg)
		}
	}
}

// The ramp is the rung with no colour to spend, so what it spends is density,
// and it spends all of it on luminance. Nothing is ever the space: a black
// patch and a hole are different facts (§10e).
func TestRamp_IsDensityAndNeverColour(t *testing.T) {
	for _, c := range []struct {
		name  string
		col   color.NRGBA
		glyph rune
	}{
		{"white is the densest step", white, '█'},
		{"black is the faintest, not nothing", black, '░'},
		{"transparent is nothing", clear, noInk},
	} {
		got := Ramp(bands(4, 4, c.col), 1, 1)[0][0]
		if got.Rune != c.glyph {
			t.Errorf("%s: glyph = %q, want %q", c.name, got.Rune, c.glyph)
		}
		if got.Fg != nil || got.Bg != nil {
			t.Errorf("%s: the ramp asked for a colour (%v/%v)", c.name, got.Fg, got.Bg)
		}
	}
	// Luminance and not the arithmetic mean: pure green reads brighter than
	// pure blue at the same channel value, which is what an eye does.
	green := Ramp(bands(4, 4, color.NRGBA{0, 0xff, 0, 0xff}), 1, 1)[0][0].Rune
	if blueRune := Ramp(bands(4, 4, blue), 1, 1)[0][0].Rune; green <= blueRune {
		t.Errorf("green (%q) should be denser than blue (%q)", green, blueRune)
	}
}

// Every cell of the grid is drawn, at both rungs — a row short or a column
// short is a card whose picture does not fill the hole reserved for it.
func TestGridIsTheSizeItWasAskedFor(t *testing.T) {
	img := bands(37, 91, red, blue, white)
	for _, c := range []struct {
		name  string
		cells [][]Cell
	}{
		{"half-blocks", Halfblocks(img, 11, 7)},
		{"the ramp", Ramp(img, 11, 7)},
	} {
		if len(c.cells) != 7 {
			t.Fatalf("%s: %d rows, want 7", c.name, len(c.cells))
		}
		for i, row := range c.cells {
			if len(row) != 11 {
				t.Fatalf("%s: row %d is %d cells, want 11", c.name, i, len(row))
			}
		}
	}
}

// Scale is what the graphics rung sends the terminal, so it keeps the alpha
// the cells only ever read as a yes or a no.
func TestScale_KeepsCoverage(t *testing.T) {
	out := Scale(bands(8, 8, clear, red), 2, 2)
	if out == nil {
		t.Fatal("no picture back")
	}
	if b := out.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Fatalf("scaled to %v, want 2×2", b)
	}
	if _, _, _, a := out.At(0, 0).RGBA(); a != 0 {
		t.Errorf("the transparent band came back with alpha %d", a)
	}
	if _, _, _, a := out.At(0, 1).RGBA(); a != 0xffff {
		t.Errorf("the opaque band came back with alpha %d", a)
	}
	if Scale(nil, 2, 2) != nil || Scale(bands(4, 4, red), 0, 2) != nil {
		t.Error("nothing to scale should be nothing back")
	}
}

func TestDecode_ReadsAPNGAndRefusesWhatItCannot(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, bands(6, 4, red, blue)); err != nil {
		t.Fatal(err)
	}
	img, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatalf("a PNG should decode: %v", err)
	}
	if img.Bounds().Dx() != 6 || img.Bounds().Dy() != 4 {
		t.Fatalf("decoded %v, want 6×4", img.Bounds())
	}
	// A format shhh accepts as an attachment and cannot draw — WebP — is a
	// refusal that names what it can, not a crash and not a blank card.
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
	_, err = Decode(webp)
	if err == nil {
		t.Fatal("a WebP should be refused")
	}
	for _, want := range []string{"PNG", "JPEG", "GIF"} {
		if !bytes.Contains([]byte(err.Error()), []byte(want)) {
			t.Errorf("the refusal never names %s: %q", want, err)
		}
	}
}

// sameRGB compares a cell's colour with the band it came from, ignoring the
// alpha a cell no longer carries.
func sameRGB(got color.Color, want color.NRGBA) bool {
	if got == nil {
		return false
	}
	r, g, b, _ := got.RGBA()
	wr, wg, wb, _ := color.NRGBAModel.Convert(want).(color.NRGBA).RGBA()
	return r == wr && g == wg && b == wb
}
