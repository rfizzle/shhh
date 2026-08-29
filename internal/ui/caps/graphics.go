package caps

// The other half of the kitty graphics question (S-158,
// docs/interface/surfaces.md#a-staged-picture).
//
// Query asks the terminal whether it draws inline images; this is shhh asking
// it to draw one. The two are here together for the probe's rule — a terminal
// sequence composed anywhere else would be the second place in the tree that
// speaks the wire — and for the same reason the notification is: the reply
// that says which dialect this terminal speaks is the only thing that decides
// what goes out.
//
// Only kitty is spent. Sixel is detected (Terminal.Sixel) and deliberately
// not drawn: it is a second encoder, a second escape vocabulary and a second
// set of scrolling quirks for a rung the half-block picture already fills
// legibly on every terminal there is. Detection is kept because `/ui
// terminal` is a diagnostic and "this terminal offered sixel and shhh does
// not take it" is a truthful thing for it to be able to say.
//
// Nothing here caches. Crush keys a cache on (id, cols, rows) because its
// preview follows a file picker's cursor and re-transmits on every keystroke;
// shhh's preview is opened by name with a slash command, so a transmission is
// one deliberate act by the reader and there is nothing to spare.

import (
	"bytes"
	"image"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/rfizzle/shhh/internal/ui/raster"
)

// pictureID is the image number shhh's previews live under.
//
// A constant rather than a hash of the file, because shhh shows one picture
// at a time: transmitting under the same number replaces what was there,
// which is exactly the lifecycle a single preview surface has. It spells
// `shh` in its low three bytes so the fourth is zero — an id that fits in
// three bytes is one whose placement needs two diacritics per cell rather
// than three — and so that a number shared with every other program on the
// terminal is at least an unlikely one to collide with.
const pictureID = 's'<<16 | 'h'<<8 | 'h'

// Transmit hands the terminal one picture, scaled to the cells it will fill,
// and asks it to hold it for a placement the screen draws (Placement).
//
// It returns nil unless the terminal answered the graphics query, so a caller
// can ask unconditionally and let the answer decide — which is the shape
// every other capability here is spent in.
func (t Terminal) Transmit(img image.Image, cols, rows, cellW, cellH int) tea.Cmd {
	if !t.Kitty || img == nil || cols < 1 || rows < 1 || cellW < 1 || cellH < 1 {
		return nil
	}
	scaled := raster.Scale(img, cols*cellW, rows*cellH)
	var buf bytes.Buffer
	err := kitty.EncodeGraphics(&buf, scaled, &kitty.Options{
		ID:           pictureID,
		Action:       kitty.TransmitAndPut,
		Transmission: kitty.Direct,
		Format:       kitty.RGBA,
		ImageWidth:   scaled.Bounds().Dx(),
		ImageHeight:  scaled.Bounds().Dy(),
		Columns:      cols,
		Rows:         rows,
		// A virtual placement is one the terminal does not put anywhere: it
		// waits for the placeholder cells to say where, which is the only
		// form that survives a full-screen redraw of a surface that scrolls
		// and reflows.
		VirtualPlacement: true,
		// Nothing reads the terminal's acknowledgements — the reply channel
		// belongs to Update, and a graphics OK is not one of the five answers
		// it knows — so they are asked not to be sent rather than swallowed.
		Quiet: 2,
		Chunk: true,
		ChunkFormatter: func(chunk string) string {
			if t.tmux {
				return ansi.TmuxPassthrough(chunk)
			}
			return chunk
		},
	})
	if err != nil {
		// A picture that will not encode is a picture the surface draws its
		// own way. Saying so would be a second answer to a question the card
		// already has a rung for.
		return nil
	}
	return tea.Raw(buf.String())
}

// Placement is the rows of cells a transmitted picture is drawn in: the
// terminal fills them from the image it is holding, and to everything else
// they are ordinary one-column cells, which is what keeps the width
// arithmetic of every surface around them true.
//
// The id travels in the cells' foreground colour and the row and column in
// combining diacritics on them. Only the first cell of a row needs its
// coordinates — the terminal continues the row itself — but the colour goes
// on every cell, because the run is inside a card that paints its own border
// on both sides of it.
func (t Terminal) Placement(cols, rows int) []string {
	if !t.Kitty || cols < 1 || rows < 1 {
		return nil
	}
	// The id, spelled as a colour: red, green and blue are the three bytes
	// the protocol reads it out of, which is the other reason pictureID fits
	// in three.
	fg := ansi.NewStyle().ForegroundColor(ansi.RGBColor{
		R: pictureID >> 16 & 0xff, G: pictureID >> 8 & 0xff, B: pictureID & 0xff,
	}).String()
	out := make([]string, rows)
	for y := range out {
		var b bytes.Buffer
		b.WriteString(fg)
		b.WriteRune(kitty.Placeholder)
		b.WriteRune(kitty.Diacritic(y))
		b.WriteRune(kitty.Diacritic(0))
		for range cols - 1 {
			b.WriteString(fg)
			b.WriteRune(kitty.Placeholder)
		}
		// The row hands the pen back. What follows it on the line is a card
		// border drawn in the palette's own colour, and a picture that leaked
		// its id into it would be the one place in the interface where a
		// colour means a number.
		b.WriteString(ansi.ResetStyle)
		out[y] = b.String()
	}
	return out
}

// Delete releases the picture the terminal is holding. It goes out when the
// preview closes: an image left transmitted is memory the terminal keeps for
// a surface nobody is looking at, and the next preview would otherwise be
// drawn over the last one's placement for the frame between the two writes.
func (t Terminal) Delete() tea.Cmd {
	if !t.Kitty {
		return nil
	}
	seq := ansi.KittyGraphics(nil, "a=d", "d=I", "i="+strconv.Itoa(pictureID), "q=2")
	if t.tmux {
		seq = ansi.TmuxPassthrough(seq)
	}
	return tea.Raw(seq)
}
