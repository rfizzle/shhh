package chat

// Seeing what you attached (S-158,
// docs/interface/surfaces.md#a-staged-picture).
//
// The staged rail says `▣ shot.png 412 KB`, which is the right answer
// for a one-line strip above a live draft and the wrong one the moment two
// screenshots are staged and the question is which of them has the stack
// trace in it. `/paste show <name>` is the surface that answers it: the
// picture, full width of the pane, framed by the name and size the chip
// already carried.
//
// It is reached by name and not by a key, for §12g's own reason — a chip sits
// above a live draft, so the name printed on it is the handle and the
// completion menu offers the staged ones (S-079). The name is now the handle
// for two verbs rather than one, which is the argument for having made it the
// field a chip gives up last.
//
// Three rungs draw the picture, best first, and which one is used is a
// question already answered rather than one asked here: the terminal's own
// graphics protocol where it said it has one, half-blocks where there
// is colour, and the density ramp where there is not. The first
// is a sequence and so is composed in internal/ui/caps; the other two are
// arithmetic and live in internal/ui/raster.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
	"github.com/rfizzle/shhh/internal/ui/raster"
)

// showAttachment dispatches `/paste show`: the named staged attachment, or
// the only staged image when the name is left off.
//
// A name that is not staged is said out loud with the ones that are, the way
// `/paste drop` says it — a command that quietly did nothing is worse here
// than anywhere, because the whole point of asking was that the reader could
// not tell the files apart.
func (m Model) showAttachment(name string) (tea.Model, tea.Cmd) {
	if len(m.attachments) == 0 {
		return m.surfaceNotice("nothing is attached")
	}
	staged := strings.Join(attachment.Names(m.attachments), ", ")
	if name == "" {
		only, ok := onlyImage(m.attachments)
		if !ok {
			return m.surfaceNotice("/paste show needs a name — " + staged)
		}
		return m.openPicture(only)
	}
	for _, a := range m.attachments {
		if strings.EqualFold(a.Name, name) {
			return m.openPicture(a)
		}
	}
	return m.surfaceNotice(name + " is not attached — " + staged)
}

// onlyImage is the one staged image, when there is exactly one. Bare
// `/paste show` is worth having for the common staging area — a single
// screenshot, just pasted — and worth refusing for every other one, because
// guessing which of two pictures was meant is the mistake this surface
// exists to stop somebody making.
func onlyImage(atts []provider.Attachment) (provider.Attachment, bool) {
	var found provider.Attachment
	var n int
	for _, a := range atts {
		if a.Kind == provider.AttachmentImage {
			found, n = a, n+1
		}
	}
	return found, n == 1
}

// openPicture takes one attachment full-pane.
//
// A file that is not an image is refused rather than opened onto a note: a
// PDF and a markdown file are staged as themselves and there is nothing for
// this surface to say about them that the chip does not already say. A file
// that is an image and will not decode does open, because "this is staged and
// shhh cannot read it" is a fact about the send that follows.
func (m Model) openPicture(a provider.Attachment) (tea.Model, tea.Cmd) {
	if a.Kind != provider.AttachmentImage {
		return m.surfaceNotice(a.Name + " is not an image — " + string(a.Kind) +
			" attachments ride as themselves and have no preview")
	}
	view := &components.PictureView{Name: a.Name, Size: attachment.HumanSize(len(a.Data))}
	img, err := raster.Decode(a.Data)
	if err != nil {
		view.Note = err.Error()
	} else {
		view.Image = img
		view.Pixels = fmt.Sprintf("%d×%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	m.picture = view
	m.enterSurface(statePicture)
	return m, m.placePicture()
}

// placePicture sizes the card to the pane and, on a terminal that draws its
// own pictures, sends it the bytes and reserves the cells it will draw into.
//
// It runs on open and again on every resize, because a placement is cells at
// a size: the terminal is holding one picture scaled for one grid, and a pane
// that changed shape under it is a picture that no longer fits the hole left
// for it.
func (m *Model) placePicture() tea.Cmd {
	p := m.picture
	if p == nil {
		return nil
	}
	p.Height = m.viewportHeight()
	p.Placement = nil
	cellW, cellH := m.caps.CellSize(m.width, m.height)
	p.Cell = raster.Aspect{Width: cellW, Height: cellH}
	// The graphics rung needs both answers and not just the first: a terminal
	// that draws pictures but never said how big its cells are cannot be told
	// how many pixels to draw, and the half-block picture is what it gets.
	// In practice the two arrive together — the terminals that answer the
	// graphics query answer window op 14 as well.
	if p.Image == nil || !m.caps.Kitty || cellW < 1 || cellH < 1 {
		return nil
	}
	cols, rows := p.Fit(m.paneWidth())
	if cols < 1 || rows < 1 {
		return nil
	}
	p.Placement = m.caps.Placement(cols, rows)
	return m.caps.Transmit(p.Image, cols, rows, cellW, cellH)
}

// updatePicture routes the surface's keys. It offers two, both of which are
// the same thing said twice, and neither of which touches the staging area:
// esc never destroys, and the file the reader just looked at is still staged
// when they get back to their draft (invariant 3).
func (m Model) updatePicture(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	if keys.Match(msg, keys.Picture.Back) || keys.Match(msg, keys.Picture.Leave) {
		return m.closePicture()
	}
	return m, nil
}

// closePicture hands the pane back and releases whatever the terminal was
// holding for it.
func (m Model) closePicture() (tea.Model, tea.Cmd) {
	cmd := m.caps.Delete()
	m.picture = nil
	m.leaveSurface()
	m.syncViewport()
	return m, cmd
}

// renderPictureHint fills the input area while the picture shows. It names
// one of the two ways out, the way every other takeover's hint does.
func (m Model) renderPictureHint() string {
	return sty.SystemMsg.Render(keys.Shown(keys.Picture.Back)+" "+keys.Words(keys.Picture.Back)) +
		strings.Repeat("\n", inputHeight-1)
}
