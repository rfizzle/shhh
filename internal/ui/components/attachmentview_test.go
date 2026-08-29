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
//
// Both bodies are here, and the text one carries a program's own bytes —
// colour, a bare carriage return, a line far too long. Those are what a paste
// is made of, and each of them is a way for a row to end up a column out.
func TestAttachmentView_IsExactlyAsWideAsTheCard(t *testing.T) {
	colourPicture(t)
	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	cards := []AttachmentView{
		{Name: "shot.png", Size: "412 KB", Pixels: "640×400",
			Image: testPicture(64, 40), Height: 9},
		{Name: "paste-1.txt", Size: "4 KB", Height: 9, Text: []string{
			"\x1b[31mFAIL\x1b[0m TestRoundLimit",
			"downloading 10%\rdownloading 90%",
			strings.Repeat("a line with no end in sight ", 20),
			"\x1b[1;4mbold and underlined\x1b[0m",
			"世界世界世界世界世界",
		}},
	}
	for _, p := range cards {
		for _, mono := range []bool{false, true} {
			SetMono(mono)
			for _, width := range []int{60, 80, 110, 130} {
				for i, row := range strings.Split(p.View(width), "\n") {
					if w := ansi.StringWidth(row); w != width {
						t.Errorf("%s mono=%v w=%d: row %d is %d columns",
							p.Name, mono, width, i, w)
					}
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
	rows := AttachmentView{Image: testPicture(64, 40), Height: 9}.body(40, 7)
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
	view := ansi.Strip(AttachmentView{Name: "shot.webp", Size: "12 KB",
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
	p := AttachmentView{Image: testPicture(64, 40), Height: 9}
	cols, rows := p.Fit(80)
	drawn := p.body(80-cardFrameWidth, p.Height-viewChrome)
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
	p := AttachmentView{Name: "shot.png", Image: testPicture(64, 40), Height: 7,
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

// The text body is laid out from the top and from the left: prose and a
// stack trace are both read that way, and centring either would make the
// first character's column depend on the longest line under it.
func TestAttachmentView_TextStartsAtTheTopLeft(t *testing.T) {
	p := AttachmentView{Name: "paste-1.txt", Size: "4 KB", Height: 9,
		Text: []string{"goroutine 1 [running]:", "\tmain.main()"}}
	rows := p.body(80-cardFrameWidth, p.Height-viewChrome)
	// The body fills the pane it was given, the way the picture does; the
	// two lines are its first two rows and the rest is blank.
	if len(rows) != p.Height-viewChrome {
		t.Fatalf("two lines drew %d rows into %d", len(rows), p.Height-viewChrome)
	}
	if got := ansi.Strip(rows[0]); !strings.HasPrefix(got, "goroutine") {
		t.Fatalf("the first row is %q, want it flush left", got)
	}
	if got := ansi.Strip(rows[2]); strings.TrimSpace(got) != "" {
		t.Fatalf("the third row is %q, want it blank", got)
	}
	if got := ansi.Strip(p.View(80)); !strings.Contains(got, "2 lines") {
		t.Fatalf("the border should count the lines:\n%s", got)
	}
}

// What did not fit is counted rather than dropped, and the count is the last
// row: a body that trailed off would not say there was more.
func TestAttachmentView_TextPastThePaneIsCounted(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "goroutine 1 [running]:"
	}
	p := AttachmentView{Name: "paste-1.txt", Size: "4 KB", Height: 9, Text: lines}
	rows := p.body(80-cardFrameWidth, p.Height-viewChrome)
	if len(rows) != p.Height-viewChrome {
		t.Fatalf("the body drew %d rows into %d", len(rows), p.Height-viewChrome)
	}
	if got, want := ansi.Strip(rows[len(rows)-1]), "+14 more lines"; !strings.Contains(got, want) {
		t.Fatalf("the last row is %q, want %q", got, want)
	}
}

// One row and more than one line: the count is the honest thing to spend it
// on, because a single line of a log says nothing about the log.
func TestAttachmentView_OneRowSpendsItOnTheCount(t *testing.T) {
	p := AttachmentView{Name: "paste-1.txt", Height: 3, Text: []string{"a", "b", "c"}}
	rows := p.body(40, 1)
	if len(rows) != 1 || !strings.Contains(ansi.Strip(rows[0]), "+3 more lines") {
		t.Fatalf("a one-row body drew %v", rows)
	}
}

// A picture wins over text: an attachment is one or the other, and the
// caption measures whichever it is.
func TestAttachmentView_PixelsAndLinesAreNeverBothReported(t *testing.T) {
	pic := AttachmentView{Name: "shot.png", Size: "412 KB", Pixels: "640×400",
		Image: testPicture(32, 16), Height: 9}
	if got := ansi.Strip(pic.View(80)); strings.Contains(got, "lines") {
		t.Fatalf("a picture reported lines:\n%s", got)
	}
	text := AttachmentView{Name: "paste-1.txt", Size: "4 KB", Height: 9, Text: []string{"a"}}
	if got := ansi.Strip(text.View(80)); strings.Contains(got, "×") {
		t.Fatalf("a paste reported pixels:\n%s", got)
	}
}

// A paste is usually something a terminal printed, so the body is the one
// place in this card where bytes shhh did not write reach the screen. A bare
// carriage return would otherwise send the rest of the line back over the
// card's left border, and a colourised log would paint its own reds inside a
// palette that owns every other colour on the screen.
func TestAttachmentView_ForeignBytesArePaintedIntoThePalette(t *testing.T) {
	p := AttachmentView{Name: "paste-1.txt", Height: 5,
		Text: []string{"\x1b[31mFAIL\x1b[0m TestRoundLimit", "10%\rdone"}}
	rows := p.body(40, 3)
	for _, row := range rows {
		if strings.ContainsRune(row, '\r') {
			t.Fatalf("a carriage return reached the card: %q", row)
		}
	}
	if got := ansi.Strip(rows[0]); got != "FAIL TestRoundLimit" {
		t.Fatalf("row 0 = %q, want the words with the program's colour gone", got)
	}
	// A bare carriage return is a progress bar starting the line again: what
	// it overwrote is gone rather than drawn twice.
	if got := strings.TrimSpace(ansi.Strip(rows[1])); got != "done" {
		t.Fatalf("row 1 = %q, want just what survived the overwrite", got)
	}
}
