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
// The card's body does not scroll, because the question is whether to send
// the thing rather than what is in it: what did not fit is counted, and the
// model reads the whole of it either way.
//
// A paste asks the same question harder. It arrived with no name it was given
// and no file behind it to open in something else, so `paste-1.txt 4 KB` on
// the rail is the whole of what a reader knows about bytes they are about to
// send — and what they want to check is that it is the right log, that all of
// it is there, and whether to send it at all. So the name routes to the paste
// reader below rather than to the card: the surface the fold in the draft
// leads to, which scrolls and can drop what it is showing.
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
	if _, ok := pasteOf(a); ok {
		// A paste has a surface of its own, and the name is a second door
		// onto it rather than a second reading of it: one thing drawn two
		// ways depending on which door the reader came in by is two things
		// to learn about one file.
		return m.openPasteReader(a)
	}
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
	return seg(keys.Preview.Back).render()
}

// pasteReader is the staged paste opened for reading — the surface the fold
// in the draft leads to (docs/interface/surfaces.md#the-input-frame).
//
// It is a surface of its own rather than the card above, and the difference
// is what each is asked. The card answers *is this the file I meant*, which a
// thumbnail settles in one look, so it fits its body to the pane and decides
// nothing. A paste is two hundred lines the reader is about to pay for, and
// the questions are whether it is the right log, whether all of it is there
// and whether to send it at all — only the last of which can be answered
// without moving.
//
// So it scrolls, it can drop what it is showing, and it wears the labelled
// rail every surface holding the keyboard wears rather than a card's border:
// what is on screen is the paste, and the rail is what says the keyboard is
// here and how far through it the reader has got (invariant 5).
type pasteReader struct {
	// name is the handle back to the staging area, because the surface can
	// outlive the chip — a queued `/paste clear`, a send that took
	// everything staged. Reading it back by name is what lets the key that
	// drops the paste notice that there is nothing left to drop.
	name  string
	label string
	lines []string
	page  components.Pager
}

// openStagedPaste opens the fold the draft is holding. With one paste staged
// it opens that one; with several it opens the one the cursor is standing in
// or after, which is the one the reader was looking at when they pressed the
// key, and falls back to the first — a surface that refused to guess would be
// asking the reader to type a name that is on the screen in front of them.
func (m Model) openStagedPaste() (tea.Model, tea.Cmd) {
	staged := m.stagedPastes()
	if len(staged) == 0 {
		return m.surfaceNotice("no paste is staged — " + keys.Bracket(keys.Draft.Attach) +
			" attaches the clipboard, and a paste too big for the draft stages itself")
	}
	want := staged[0].label
	if len(staged) > 1 {
		if under, ok := m.pasteUnderCursor(staged); ok {
			want = under
		}
	}
	for _, a := range m.attachments {
		if p, ok := pasteOf(a); ok && p.label == want {
			return m.openPasteReader(a)
		}
	}
	return m, nil
}

// pasteUnderCursor is the fold the draft's cursor is inside or nearest behind
// — the last one that opens at or before it. A cursor in front of every fold
// in the sentence names none, and the caller takes the first.
func (m Model) pasteUnderCursor(staged []stagedPaste) (string, bool) {
	value := m.input.Value()
	at := m.draftOffset(value)
	label, found := "", false
	for _, p := range staged {
		from := strings.Index(value, p.token)
		if from < 0 || len([]rune(value[:from])) > at {
			continue
		}
		label, found = p.label, true
	}
	return label, found
}

// openPasteReader puts one paste on the pane, from the top.
func (m Model) openPasteReader(a provider.Attachment) (tea.Model, tea.Cmd) {
	p, ok := pasteOf(a)
	if !ok {
		return m, nil
	}
	m.pasteRead = &pasteReader{
		name:  a.Name,
		label: p.label,
		lines: strings.Split(strings.TrimSuffix(string(a.Data), "\n"), "\n"),
	}
	m.enterSurface(statePasteView)
	m.syncViewport()
	return m, nil
}

// pasteReaderLines draws the surface: the labelled rail naming the paste and
// where in it the reader is, the lines themselves indented under it, and the
// bare rule that closes a body.
//
// The rail is reading mode's, not a second one shaped like it (interrupt.go).
// Opening a paste from the draft is the keyboard leaving the sentence for a
// body of text, which is what reading mode is, and a surface inventing its
// own rule for the same act would be a fourth spelling of "the keyboard is
// here".
func (m Model) pasteReaderLines(width, height int) []string {
	r := m.pasteRead
	if r == nil || width <= 0 {
		return nil
	}
	// Two rows go to the rail above the body and the rule under it.
	r.page.Height = max(height-2, 1)
	body := r.page.Window(r.lines)
	lines := []string{keyboardRail(r.railLabel(), width)}
	for _, line := range body {
		lines = append(lines, strings.Repeat(" ", pasteBodyIndent)+
			sty.Frame.PasteBody.Render(components.Clip(line, max(width-pasteBodyIndent, 1))))
	}
	return append(lines, dividerStyle(width))
}

// pasteBodyIndent is how far the body sits inside the rail above it: the two
// columns every body in this transcript is set in from the mark that owns it.
const pasteBodyIndent = 2

// railLabel is what the rail says: which paste, and which of its lines are on
// screen. The span is the count's whole job here — a body that scrolls has to
// say where in itself it is, and the total on its own is the one number the
// chip already gave the reader.
func (r *pasteReader) railLabel() string {
	from := r.page.Held() + 1
	to := min(r.page.Held()+r.page.Height, len(r.lines))
	return fmt.Sprintf("%s · lines %d–%d of %d",
		strings.ToUpper(r.label), from, to, len(r.lines))
}

// updatePasteReader routes the surface's keys: the pair that scrolls, the one
// that drops what is showing, and the two that leave.
func (m Model) updatePasteReader(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.pasteRead
	if r == nil {
		m.leaveSurface()
		return m, nil
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Paste.Scroll):
		r.page.Offset += keys.Step(pressed, keys.Paste.Scroll)
		return m, nil
	case keys.Is(pressed, keys.Paste.Remove):
		// The one key here that changes anything. It leaves as well as
		// removes: the surface was showing bytes that are no longer staged,
		// and a pane left open on them would be reading a file the session
		// has forgotten.
		for _, a := range m.attachments {
			if strings.EqualFold(a.Name, r.name) {
				m.closePasteReader()
				next, _ := m.removePaste(a)
				return next, nil
			}
		}
		m.closePasteReader()
		return m, nil
	case keys.Is(pressed, keys.Paste.Leave), keys.Is(pressed, keys.Paste.Back):
		// Esc never destroys and neither does q: the paste is still staged,
		// and the cursor is still where the sentence left it (invariant 3).
		m.closePasteReader()
		return m, nil
	}
	return m, nil
}

// closePasteReader hands the pane back to the turn.
func (m *Model) closePasteReader() {
	m.pasteRead = nil
	m.leaveSurface()
	m.syncViewport()
}

// renderPasteReaderHint fills the input area while the paste is open, the way
// every other pane overlay's hint does.
func (m Model) renderPasteReaderHint() string {
	return joinSegs([]hintSeg{
		seg(keys.Paste.Scroll), seg(keys.Paste.Remove), seg(keys.Paste.Leave),
	})
}
