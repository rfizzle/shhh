// Package raster turns a picture into terminal cells (S-158,
// docs/interface/surfaces.md#a-staged-picture).
//
// It is the half of the image preview that has nothing to do with a terminal:
// bytes in, a grid of cells out, no escape sequence and no palette. What it
// draws with is the drawing kit's own two half-blocks and the four-step
// density ramp beside them — which is why the same grid can be painted
// in colour, or read as shape alone where there is no colour to paint with.
//
// The rungs above and below live elsewhere for the reasons the design gives.
// The kitty graphics protocol is a sequence, so it stops at internal/ui/caps
// with every other sequence; the frame the picture sits in — its name,
// its size, the border — is a card, so it is in internal/ui/components with
// the rest of the catalog. This package is only the arithmetic between them.
//
// Crush reaches for two dependencies here: imaging for the resize and
// go-ansi-paintbrush for the blocks, the second of which weighs
// twenty-six glyphs against each other to pick a shape per cell. Neither is
// taken. A preview is a thumbnail of something the reader attached seconds
// ago and already knows the look of — its job is to say "yes, that one" — and
// a box filter over the source rectangle answers that at a fraction of the
// code, deterministically, which is what a golden file needs.
package raster

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"

	// The formats shhh can draw. They are registered for their side effect,
	// which is what image.Decode reads its sniffer from.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Cell is one character cell of a picture: a glyph from the kit, and the two
// colours it is drawn in. Either colour may be nil, which means the cell asks
// for none — the terminal's own background where the picture was transparent,
// and every cell of a picture drawn as shape alone.
type Cell struct {
	Rune   rune
	Fg, Bg color.Color
}

// The two half-blocks and the density ramp. A cell holds two samples
// stacked, so `▄` is the ordinary one — the lower sample in the foreground,
// the upper behind it — and `▀` is what a cell whose lower half is
// transparent draws instead, so the picture's own holes stay holes.
const (
	lowerHalf = '▄'
	upperHalf = '▀'
)

// ramp is the four steps of ink a cell is drawn with when there is no colour
// to draw it in, faintest first. Brighter is denser, which is the reading a
// dark terminal gives; the ramp is the picture's only channel there, so it is
// spent on luminance and nothing else.
//
// The darkest step is the light shade and not a space, which costs the ramp a
// fifth of its range and buys something worth more: a black corner of a
// picture and a hole through it are different facts, and drawing them alike
// would leave a dark photograph with no visible edges at all. A space means
// nothing is there (noInk).
var ramp = []rune{'░', '▒', '▓', '█'}

// noInk is the cell a transparent patch draws: the terminal's own background,
// which is what was behind the picture anyway.
const noInk = ' '

// Aspect is one character cell in pixels. It is what decides the shape of the
// grid a picture is fitted into: the same picture is half as many rows on a
// terminal whose cells are twice as tall.
type Aspect struct{ Width, Height int }

// DefaultAspect is the shape of a cell when the terminal did not say — asked
// and silent, or never asked. Two-to-one is the common ratio and the
// safe guess: a picture fitted to it on a terminal that is really 9×19 is off
// by a twentieth, which is a thumbnail slightly the wrong shape rather than
// one that does not fit.
var DefaultAspect = Aspect{Width: 1, Height: 2}

// clearAlpha is the alpha below which a patch counts as nothing rather than
// as a colour (sample.clear).
const clearAlpha = 0x8000

// Decode reads attachment bytes as a picture.
//
// It answers for PNG, JPEG and GIF, which is what the standard library
// decodes. WebP is the gap, and it is a real one — shhh accepts a WebP
// attachment (internal/attachment) and sends it to the model, so the preview
// is the only thing that cannot read it. It is left as a refusal that names
// the format rather than as a dependency: the picture is a convenience, the
// bytes still ride, and the reader is told which of those two happened.
func Decode(data []byte) (image.Image, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		if errors.Is(err, image.ErrFormat) {
			return nil, fmt.Errorf("shhh draws PNG, JPEG and GIF previews, and this is none of them")
		}
		return nil, err
	}
	if img.Bounds().Dx() <= 0 || img.Bounds().Dy() <= 0 {
		return nil, fmt.Errorf("the %s has no pixels in it", format)
	}
	return img, nil
}

// Fit is the cell grid one picture gets inside the space it is given: as
// large as the space allows, in the proportion the picture actually has once
// the cells' own shape is accounted for.
//
// The number of samples a cell holds does not enter it. A cell grid's
// proportion is a fact about cells, and half-blocks change how much detail
// fits in one, not how many of them the picture spans.
func Fit(img image.Image, maxCols, maxRows int, cell Aspect) (cols, rows int) {
	if img == nil || maxCols < 1 || maxRows < 1 {
		return 0, 0
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	if cell.Width <= 0 || cell.Height <= 0 {
		cell = DefaultAspect
	}
	// Widest first, then the height that proportion asks for; if that is
	// taller than the space, the height is what binds and the width follows.
	cols, rows = maxCols, cols2rows(maxCols, w, h, cell)
	if rows > maxRows {
		rows = maxRows
		cols = rows2cols(maxRows, w, h, cell)
	}
	return max(min(cols, maxCols), 1), max(min(rows, maxRows), 1)
}

// cols2rows and rows2cols are the one proportion read in its two directions:
// cols·cellWidth over rows·cellHeight is the picture's own width over its
// height. Integer arithmetic throughout, because a golden file is a promise
// that the same picture draws the same way twice.
func cols2rows(cols, w, h int, cell Aspect) int {
	return cols * cell.Width * h / (cell.Height * w)
}

func rows2cols(rows, w, h int, cell Aspect) int {
	return rows * cell.Height * w / (cell.Width * h)
}

// Halfblocks draws the picture into cols×rows cells at two samples per cell,
// which is the full resolution a terminal has: the lower sample is the
// glyph's foreground and the upper one its background, so a cell says two
// things where a whole-cell glyph says one.
func Halfblocks(img image.Image, cols, rows int) [][]Cell {
	s := samples(img, cols, rows*2)
	out := make([][]Cell, rows)
	for y := range out {
		out[y] = make([]Cell, cols)
		for x := range out[y] {
			out[y][x] = halfblock(s[y*2][x], s[y*2+1][x])
		}
	}
	return out
}

// halfblock is one cell of two stacked samples. Which glyph it is depends on
// which halves have ink in them: a cell is only ever asked to paint the
// halves the picture actually filled, so a transparent corner is the
// terminal's own background rather than a black square.
func halfblock(top, bottom sample) Cell {
	switch {
	case top.clear() && bottom.clear():
		return Cell{Rune: noInk}
	case top.clear():
		return Cell{Rune: lowerHalf, Fg: bottom.color()}
	case bottom.clear():
		return Cell{Rune: upperHalf, Fg: top.color()}
	}
	return Cell{Rune: lowerHalf, Fg: bottom.color(), Bg: top.color()}
}

// Ramp draws the picture into cols×rows cells at one sample per cell, as
// density rather than colour: the four steps of the drawing kit's ramp, and a
// space for nothing at all.
//
// This is the rung for a terminal with no colour to give — mono, NO_COLOR,
// `TERM=dumb` — and it is a picture rather than a refusal for a reason worth
// stating, because mono declines foreign colour everywhere else. What it
// declines there is a *recolouring*: a diff body already carries its meaning
// in `+` and `−`, so a grey ladder over the top is decoration. A picture has
// no other channel. Take its hue away and what is left is still the picture;
// refuse to draw it and there is nothing.
func Ramp(img image.Image, cols, rows int) [][]Cell {
	s := samples(img, cols, rows)
	out := make([][]Cell, rows)
	for y := range out {
		out[y] = make([]Cell, cols)
		for x := range out[y] {
			out[y][x] = Cell{Rune: rampRune(s[y][x])}
		}
	}
	return out
}

// rampRune picks the step of ink one sample is worth. Luminance is Rec. 601,
// which is the weighting that matches what an eye reads as brightness rather
// than what the arithmetic mean gives.
func rampRune(s sample) rune {
	if s.clear() {
		return noInk
	}
	lum := (299*s.r + 587*s.g + 114*s.b) / 1000
	// The top of the range is inclusive, so full white is the last step
	// rather than one past the end.
	i := lum * len(ramp) / (0xffff + 1)
	return ramp[min(max(i, 0), len(ramp)-1)]
}

// sample is one averaged patch of the source: four colour channels at the
// sixteen-bit depth image/color works in, un-premultiplied, so a patch can be
// read either as the colour it is or as the coverage it has.
type sample struct{ r, g, b, a int }

// clear reports whether there was too little of anything in the patch to
// count as ink. Half the alpha range is the only defensible line without
// knowing the terminal's own background: above it the patch paints as if it
// were opaque, below it the cell is left for the background to fill.
func (s sample) clear() bool { return s.a < clearAlpha }

func (s sample) color() color.Color {
	return color.RGBA{R: uint8(s.r >> 8), G: uint8(s.g >> 8), B: uint8(s.b >> 8), A: 0xff}
}

func (s sample) nrgba() color.NRGBA {
	return color.NRGBA{R: uint8(s.r >> 8), G: uint8(s.g >> 8), B: uint8(s.b >> 8), A: uint8(s.a >> 8)}
}

// samples reduces the picture to a cols×rows grid by averaging each patch.
//
// A box filter, and deliberately: a thumbnail's job is to be recognised, and
// averaging every source pixel that lands in a patch is what keeps a
// one-pixel line in a screenshot from disappearing between two sample points
// the way nearest-neighbour would drop it.
func samples(img image.Image, cols, rows int) [][]sample {
	b := img.Bounds()
	out := make([][]sample, rows)
	for y := range out {
		out[y] = make([]sample, cols)
		y0, y1 := span(b.Min.Y, b.Dy(), y, rows)
		for x := range out[y] {
			x0, x1 := span(b.Min.X, b.Dx(), x, cols)
			out[y][x] = average(img, x0, y0, x1, y1)
		}
	}
	return out
}

// span is the source rows or columns one sample covers. It is at least one
// pixel wide: a grid asked for more samples than the picture has pixels
// repeats them rather than averaging over nothing.
func span(origin, size, i, n int) (lo, hi int) {
	lo = origin + i*size/n
	hi = origin + (i+1)*size/n
	if hi <= lo {
		hi = lo + 1
	}
	return lo, hi
}

// average is the mean colour of one patch, weighted by coverage.
//
// image/color is alpha-premultiplied, so the sums are already the colour a
// half-transparent pixel contributes; dividing them by the alpha sum rather
// than by the pixel count is what un-premultiplies the average, and gives a
// patch that is one opaque pixel beside three empty ones the colour of the
// pixel rather than a quarter of it. The alpha itself is the ordinary mean:
// it is coverage, and coverage averages over the whole patch.
func average(img image.Image, x0, y0, x1, y1 int) sample {
	var sr, sg, sb, sa uint64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			sr, sg, sb, sa = sr+uint64(r), sg+uint64(g), sb+uint64(b), sa+uint64(a)
		}
	}
	n := uint64((x1 - x0) * (y1 - y0))
	if n == 0 || sa == 0 {
		return sample{}
	}
	return sample{
		r: int(sr * 0xffff / sa),
		g: int(sg * 0xffff / sa),
		b: int(sb * 0xffff / sa),
		a: int(sa / n),
	}
}

// Scale is the picture redrawn at a given pixel size, by the same box filter
// the cells are averaged with.
//
// It exists for the rung this package does not draw: a terminal that renders
// the picture itself is sent the pixels, and sending five megabytes of
// screenshot to fill a strip of cells a hundred wide would spend a second of
// somebody's afternoon on detail no cell can hold. What goes out is the size
// the terminal will actually draw.
func Scale(img image.Image, w, h int) *image.NRGBA {
	if img == nil || w < 1 || h < 1 {
		return nil
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y, row := range samples(img, w, h) {
		for x, s := range row {
			out.SetNRGBA(x, y, s.nrgba())
		}
	}
	return out
}
