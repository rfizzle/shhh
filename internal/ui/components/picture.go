package components

// The staged image preview (S-158,
// docs/interface/surfaces.md#a-staged-picture). The card `/paste show` opens:
// one attachment's picture, framed by the name and the size the chip strip
// already carries.
//
// The strip says a file called shot.png is staged and how big it is (§12g).
// That is the right answer for a rail sitting above a live draft, and it is
// the wrong one the moment two screenshots are staged and the question is
// which of them is the one with the stack trace in it. Nothing else in shhh
// can answer that, because until now nothing drew the bytes.
//
// The card is a frame and not a renderer. Which rung the picture was drawn at
// — the terminal's own graphics protocol, half-blocks, or the density ramp of
// a terminal with no colour — is decided before it gets here: the first is a
// sequence and stops at internal/ui/caps (§10k), and the other two are
// arithmetic and live in internal/ui/raster. What this owns is the border,
// the caption, and the centring.

import (
	"image"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/ui/raster"
)

// pictureChrome is what the card costs a picture: the two border rows.
const pictureChrome = 2

// PictureView is one staged attachment shown as a picture.
type PictureView struct {
	// Name and Size are the chip's own two fields (§12g), and they ride the
	// top border rather than a caption row: the picture is what the surface
	// is for, so it gets every row the frame does not need.
	Name, Size string
	// Pixels is the picture's real size, `1440×900`. It is the one fact the
	// chip strip could never carry and the card can, and it is what says a
	// preview is a thumbnail rather than the thing itself.
	Pixels string

	// Image is the decoded picture, drawn here when Placement is empty.
	Image image.Image
	// Cell is one character cell in pixels, as the terminal reported it
	// (§10k). Its zero value is the default guess.
	Cell raster.Aspect

	// Placement is the picture already drawn by the terminal itself — the
	// rows of graphics-protocol placeholders a kitty-capable terminal fills
	// in (§10k). When it is set the card draws it instead of rastering, and
	// the rows are used at the size the caller reserved for them.
	Placement []string

	// Note is why there is no picture, in words: a format shhh cannot decode,
	// bytes that will not open. It is drawn where the picture would be, so
	// the surface is never blank without saying why.
	Note string

	// Height is the rows the surface gave the card, borders included.
	Height int
}

// View draws the card.
func (p PictureView) View(width int) string {
	rows := p.picture(width-cardFrameWidth, p.Height-pictureChrome)
	return renderChromeCard(cardChrome{title: p.Name, chips: p.captions()}, rows, width)
}

// captions are the top border's chips, in the order they are given up as the
// terminal narrows — chips drop from the front (cardTop), so the size goes
// before the pixel dimensions do. The dimensions are the fact this surface
// added; the size is on the chip strip already.
func (p PictureView) captions() []string {
	var out []string
	if p.Size != "" {
		out = append(out, sty.Dim.Render(p.Size))
	}
	if p.Pixels != "" {
		out = append(out, sty.Dim.Render(p.Pixels))
	}
	return out
}

// Fit is the cell grid the picture gets inside a card of the given width, at
// the height the view was already given.
//
// It is exported for the one caller that has to know the answer before the
// card is drawn: a terminal that renders the picture itself is sent the
// pixels ahead of the frame that places them, so the size has to be agreed
// between the two, and the card is where the arithmetic about the card's own
// chrome belongs.
func (p PictureView) Fit(width int) (cols, rows int) {
	return raster.Fit(p.Image, width-cardFrameWidth, p.Height-pictureChrome, p.Cell)
}

// picture draws the body: the placement the terminal will fill, the note that
// says why there is nothing to draw, or the picture rastered into the space.
func (p PictureView) picture(width, height int) []string {
	if width < 1 || height < 1 {
		return nil
	}
	switch {
	case len(p.Placement) > 0:
		return centre(p.Placement, width, height)
	case p.Note != "":
		return centre([]string{sty.Dim.Render(clip(p.Note, width))}, width, height)
	case p.Image == nil:
		return nil
	}
	cols, rows := raster.Fit(p.Image, width, height, p.Cell)
	if cols < 1 || rows < 1 {
		return nil
	}
	var cells [][]raster.Cell
	if PictureInColour() {
		cells = raster.Halfblocks(p.Image, cols, rows)
	} else {
		cells = raster.Ramp(p.Image, cols, rows)
	}
	lines := make([]string, len(cells))
	for i, row := range cells {
		lines[i] = pictureRow(row)
	}
	return centre(lines, width, height)
}

// PictureInColour reports whether a picture is drawn in colour here, or as
// the density ramp of §10e.
//
// Both halves of the answer are already settled elsewhere and neither is this
// package's to re-decide. Mono is the swap of §10f — and a photograph is the
// one place there that keeps its shape when its hue goes, which is why the
// ramp exists rather than a refusal. The profile is S-155's single answer to
// what the terminal can carry, and below sixteen colours there is nothing to
// carry a picture in.
func PictureInColour() bool { return !Mono() && Profile() >= colorprofile.ANSI }

// pictureRow paints one row of cells, coalescing the runs that share a
// colour.
//
// The run is not an optimisation of the drawing, it is one of the string: a
// cell rendered on its own carries its own escape sequence, and a hundred of
// those across a row is several kilobytes of a surface that is a hundred
// columns wide. Runs are what keep a captured picture a file a person can
// read (§11's golden suite).
func pictureRow(cells []raster.Cell) string {
	var b strings.Builder
	for i := 0; i < len(cells); {
		j := i + 1
		for j < len(cells) && sameInk(cells[i], cells[j]) {
			j++
		}
		var run strings.Builder
		for _, c := range cells[i:j] {
			run.WriteRune(c.Rune)
		}
		b.WriteString(pictureStyle(cells[i]).Render(run.String()))
		i = j
	}
	return b.String()
}

// pictureStyle is the style one cell is painted with. A picture's colours are
// its own — the fifteen tokens of §10a are the interface's, and a photograph
// is content, like the text of a message — so they are set literally and
// converted to the profile's rung here, which is the job Token.Color does for
// everything the palette does own (S-155).
func pictureStyle(c raster.Cell) lipgloss.Style {
	s := lipgloss.NewStyle()
	if c.Fg != nil {
		s = s.Foreground(Profile().Convert(c.Fg))
	}
	if c.Bg != nil {
		s = s.Background(Profile().Convert(c.Bg))
	}
	return s
}

// sameInk reports whether two cells are painted alike, and so can share one
// escape sequence.
func sameInk(a, b raster.Cell) bool {
	return sameColor(a.Fg, b.Fg) && sameColor(a.Bg, b.Bg)
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// centre lays the picture in the space the card gave it: the same number of
// blank rows above and below, and the same blank columns each side.
//
// A thumbnail that hugged a corner would read as a layout mistake rather than
// as a picture, and the space around it is what says the frame is the card's
// and the edges are the picture's.
func centre(lines []string, width, height int) []string {
	if len(lines) > height {
		lines = lines[:height]
	}
	out := make([]string, 0, height)
	top := (height - len(lines)) / 2
	for range top {
		out = append(out, "")
	}
	for _, line := range lines {
		if pad := (width - lipgloss.Width(line)) / 2; pad > 0 {
			line = strings.Repeat(" ", pad) + line
		}
		out = append(out, line)
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
}
