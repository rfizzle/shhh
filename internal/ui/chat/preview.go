package chat

// Seeing what you attached (
// docs/interface/surfaces.md#a-staged-attachment).
//
// The staged rail says `▣ shot.png 412 KB`, which is the right answer
// for a one-line strip above a live draft and the wrong one the moment two
// screenshots are staged and the question is which of them has the stack
// trace in it. `/paste show <name>` is the surface that answers it: the
// attachment itself, full width of the pane, framed by the name and size the
// chip already carried.
//
// A paste asks the same question harder. It arrived with no name it was given
// and no file behind it to open in something else, so `paste-1.txt 4 KB` on
// the rail is the whole of what a reader knows about bytes they are about to
// send — and the two things they want to check, that it is the right log and
// that it is all there, are both answered by looking at it. Neither body
// scrolls, because the question is whether to send it rather than what is in
// it: what did not fit is counted, and the model reads the whole of it.
//
// It is reached by name and not by a key, for the staged rail's own reason —
// a chip sits above a live draft, so the name printed on it is the handle and
// the completion menu offers the staged ones. The name is now the
// handle for two verbs rather than one, which is the argument for having made
// it the field a chip gives up last.
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
// the only one the surface can open when the name is left off.
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
		only, ok := onlyPreviewable(m.attachments)
		if !ok {
			return m.surfaceNotice("/paste show needs a name — " + staged)
		}
		return m.openPreview(only)
	}
	for _, a := range m.attachments {
		if strings.EqualFold(a.Name, name) {
			return m.openPreview(a)
		}
	}
	return m.surfaceNotice(name + " is not attached — " + staged)
}

// onlyPreviewable is the one staged attachment this surface can open, when
// there is exactly one. Bare `/paste show` is worth having for the common
// staging area — a single screenshot or a single paste, just made — and worth
// refusing for every other one, because guessing which of two was meant is
// the mistake this surface exists to stop somebody making.
func onlyPreviewable(atts []provider.Attachment) (provider.Attachment, bool) {
	var found provider.Attachment
	var n int
	for _, a := range atts {
		if a.Kind == provider.AttachmentImage || a.Kind == provider.AttachmentText {
			found, n = a, n+1
		}
	}
	return found, n == 1
}

// openPreview takes one attachment full-pane, drawn as whichever of the two
// things it is.
//
// A PDF is refused rather than opened onto a note: shhh does not render one,
// so there is nothing this surface could say about it that the chip does not
// already say. An image that will not decode does open, because "this is
// staged and shhh cannot read it" is a fact about the send that follows.
func (m Model) openPreview(a provider.Attachment) (tea.Model, tea.Cmd) {
	view := &components.AttachmentView{Name: a.Name, Size: attachment.HumanSize(len(a.Data))}
	switch a.Kind {
	case provider.AttachmentImage:
		img, err := raster.Decode(a.Data)
		if err != nil {
			view.Note = err.Error()
		} else {
			view.Image = img
			view.Pixels = fmt.Sprintf("%d×%d", img.Bounds().Dx(), img.Bounds().Dy())
		}
	case provider.AttachmentText:
		view.Text = strings.Split(strings.TrimSuffix(string(a.Data), "\n"), "\n")
	default:
		return m.surfaceNotice(a.Name + " is not an image or text — " + string(a.Kind) +
			" attachments ride as themselves and have no preview")
	}
	m.preview = view
	m.enterSurface(statePreview)
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
	p := m.preview
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

// updatePreview routes the surface's keys. It offers two, both of which are
// the same thing said twice, and neither of which touches the staging area:
// esc never destroys, and the file the reader just looked at is still staged
// when they get back to their draft (invariant 3).
func (m *Model) answerPreview(msg tea.KeyPressMsg) (bool, overlayAction) {
	if !keys.Match(msg, keys.Preview.Back) && !keys.Match(msg, keys.Preview.Leave) {
		return false, overlayAction{}
	}
	return true, m.closePreview()
}

// closePreview hands the pane back and releases whatever the terminal was
// holding for it.
func (m *Model) closePreview() overlayAction {
	cmd := m.caps.Delete()
	m.preview = nil
	return overlayAction{close: true, run: cmd}
}

// renderPreviewHint fills the input area while the preview shows. It names
// one of the two ways out, the way every other takeover's hint does.
func (m Model) renderPreviewHint() string {
	return sty.SystemMsg.Render(keys.Shown(keys.Preview.Back)+" "+keys.Words(keys.Preview.Back)) +
		strings.Repeat("\n", inputHeight-1)
}
