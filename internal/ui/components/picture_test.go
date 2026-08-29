package components

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/raster"
)

// testPicture is four solid horizontal bands. Flat regions are what makes a
// captured picture readable: the runs collapse, so a golden file is a picture
// a person can look at rather than a wall of escape sequences.
//
// The four are one per step of the ramp, deliberately. Four colours of
// similar brightness are four distinct bands in colour and one flat block
// without it, and a golden sheet that showed that would be a sheet nobody
// could read the mono half of — which is exactly the half worth capturing
// here.
func testPicture(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	bands := []color.NRGBA{
		{0x00, 0x00, 0xaf, 0xff},
		{0xff, 0x00, 0x00, 0xff},
		{0x5f, 0xd7, 0x5f, 0xff},
		{0xff, 0xff, 0x87, 0xff},
	}
	for y := range h {
		c := bands[y*len(bands)/h]
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// colourPicture puts the profile somewhere a colour can be drawn, the way the
// golden capture does — a test binary's stdout is not a terminal.
func colourPicture(t *testing.T) {
	t.Helper()
	was := Profile()
	SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { SetProfile(was) })
}

// The card is a card: every row of it is exactly as wide as it was given, in
// both palettes and at every width. It shares the pane with a border the
// transcript's own surfaces draw, so a row a column out is a frame that does
// not close.
func TestPicture_IsExactlyAsWideAsTheCard(t *testing.T) {
	colourPicture(t)
	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	p := PictureView{Name: "shot.png", Size: "412 KB", Pixels: "640×400",
		Image: testPicture(64, 40), Height: 9}
	for _, mono := range []bool{false, true} {
		SetMono(mono)
		for _, width := range []int{60, 80, 110, 130} {
			for i, row := range strings.Split(p.View(width), "\n") {
				if w := ansi.StringWidth(row); w != width {
					t.Errorf("mono=%v w=%d: row %d is %d columns", mono, width, i, w)
				}
			}
		}
	}
}

// Invariant 1, on the one surface in shhh whose content is colour. A
// photograph keeps its shape when its hue goes, so mono draws the picture
// rather than declining it — but it draws it with no colour at all, which is
// what mono asks of every source of colour the palette does not own.
func TestPicture_MonoAsksForNoColourAtAll(t *testing.T) {
	colourPicture(t)
	was := Mono()
	t.Cleanup(func() { SetMono(was) })

	SetMono(false)
	if !PictureInColour() {
		t.Fatal("a coloured palette on a 256-colour terminal should draw in colour")
	}
	SetMono(true)
	if PictureInColour() {
		t.Fatal("mono must not draw a picture in colour")
	}
	rows := PictureView{Image: testPicture(64, 40), Height: 9}.picture(40, 7)
	for i, row := range rows {
		if row != ansi.Strip(row) {
			t.Fatalf("mono row %d carries an escape sequence: %q", i, row)
		}
		if strings.ContainsRune(row, '▄') || strings.ContainsRune(row, '▀') {
			t.Fatalf("mono row %d is drawn as half-blocks: %q", i, row)
		}
	}
}

// A terminal with no colour to give is the ramp too, whatever the palette
// says: below sixteen colours there is nothing to carry a picture in.
func TestPicture_NoColourProfileIsTheRamp(t *testing.T) {
	was := Profile()
	t.Cleanup(func() { SetProfile(was) })
	for _, c := range []struct {
		profile colorprofile.Profile
		colour  bool
	}{
		{colorprofile.NoTTY, false},
		{colorprofile.Ascii, false},
		{colorprofile.ANSI, true},
		{colorprofile.TrueColor, true},
	} {
		SetProfile(c.profile)
		if PictureInColour() != c.colour {
			t.Errorf("%v: drawn in colour = %v, want %v", c.profile, !c.colour, c.colour)
		}
	}
}

// A picture that will not decode is a card that says so where the picture
// would be. The surface is never blank without a reason on it.
func TestPicture_SaysWhyWhenThereIsNothingToDraw(t *testing.T) {
	colourPicture(t)
	view := ansi.Strip(PictureView{Name: "shot.webp", Size: "12 KB",
		Note: "shhh draws PNG, JPEG and GIF previews", Height: 7}.View(60))
	if !strings.Contains(view, "shhh draws PNG") {
		t.Fatalf("the card should carry the reason:\n%s", view)
	}
	if !strings.Contains(view, "shot.webp") {
		t.Fatalf("the card should still name the file:\n%s", view)
	}
}

// Fit is the answer the graphics rung transmits against, so it has to be the
// same grid the card would have drawn itself.
func TestPicture_FitIsTheGridItDraws(t *testing.T) {
	colourPicture(t)
	p := PictureView{Image: testPicture(64, 40), Height: 9}
	cols, rows := p.Fit(80)
	drawn := p.picture(80-cardFrameWidth, p.Height-pictureChrome)
	var painted []string
	for _, row := range drawn {
		if row != "" {
			painted = append(painted, row)
		}
	}
	if len(painted) != rows {
		t.Fatalf("Fit says %d rows, the card drew %d", rows, len(painted))
	}
	// The rows are centred, so each is the picture's width plus its left pad.
	for i, row := range painted {
		if w := ansi.StringWidth(row); w < cols {
			t.Fatalf("row %d is %d columns, narrower than the %d Fit promised", i, w, cols)
		}
	}
}

// A placement is cells the terminal fills in itself, so the card draws them
// as they were handed over rather than rastering anything of its own.
func TestPicture_DrawsAPlacementVerbatim(t *testing.T) {
	colourPicture(t)
	p := PictureView{Name: "shot.png", Image: testPicture(64, 40), Height: 7,
		Placement: []string{"<row one>", "<row two>"}}
	view := ansi.Strip(p.View(60))
	if !strings.Contains(view, "<row one>") || !strings.Contains(view, "<row two>") {
		t.Fatalf("the placement should be what is drawn:\n%s", view)
	}
	if strings.ContainsRune(view, '▄') {
		t.Fatalf("the card rastered over a placement:\n%s", view)
	}
}

// Runs are what keep the string a size a person can read. A row of a flat
// picture is a handful of escape sequences, not one per cell.
func TestPicture_CoalescesRunsOfOneColour(t *testing.T) {
	colourPicture(t)
	flat := make([]raster.Cell, 100)
	for i := range flat {
		flat[i] = raster.Cell{Rune: '▄', Fg: color.RGBA{0x5f, 0xd7, 0x5f, 0xff}}
	}
	row := pictureRow(flat)
	if got := strings.Count(row, "\x1b["); got > 2 {
		t.Fatalf("a flat row carries %d escape sequences, want at most 2", got)
	}
	if w := ansi.StringWidth(row); w != len(flat) {
		t.Fatalf("the row is %d columns, want %d", w, len(flat))
	}
}
