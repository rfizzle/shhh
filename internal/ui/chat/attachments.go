package chat

// Attachments (S-134): images and files staged for the next message. The
// an attachment shows as a chip carrying its mark, its name and its size, on
// the frame's staged rail while it waits and on the user's own
// transcript row once it has gone. Nothing here draws a picture: `/paste
// show` is the one surface that does, opened by naming a chip and given the
// whole pane while it is up (S-158, §12h, preview.go). What the bytes are for
// is the request — they ride on the user message (internal/provider), and
// each provider carries them the way its API takes them.
//
// Three doors, one staging area. Ctrl+V reads the clipboard — a pasted
// screenshot or the files a file manager copied — and falls back to pasting
// text into the draft when the clipboard holds only text, so the chord never
// stops doing what it used to. A path dragged into the terminal arrives as a
// bracketed paste and is attached when it points at an image or a document,
// because that is what dragging one in means. `/paste` is the explicit form,
// and the only one that can name a file the clipboard never touched.
//
// Reading a clipboard shells out (osascript, wl-paste, xclip), which is slow
// enough to be felt, so it happens in a command rather than in Update.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// clipboardMsg carries the result of one clipboard read back into Update.
// The text travels with it so the fall-back path — nothing attachable, paste
// what is there — costs no second read of a clipboard that shells out.
type clipboardMsg struct {
	clip attachment.Clipboard
	err  error
}

// attachedFileMsg carries the result of attaching one named file.
type attachedFileMsg struct {
	attachment provider.Attachment
	err        error
}

// readClipboardCmd reads the clipboard off the render loop.
func readClipboardCmd() tea.Cmd {
	return func() tea.Msg {
		clip, err := attachment.Read()
		return clipboardMsg{clip: clip, err: err}
	}
}

// attachFileCmd reads one file off disk as an attachment.
func attachFileCmd(path string) tea.Cmd {
	return func() tea.Msg {
		a, err := attachment.FromFile(path)
		return attachedFileMsg{attachment: a, err: err}
	}
}

// handleClipboard stages what the clipboard held, or types it.
func (m Model) handleClipboard(msg clipboardMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.surfaceNotice("nothing attached — " + msg.err.Error())
	}
	if len(msg.clip.Attachments) > 0 {
		return m.stage(msg.clip.Attachments)
	}
	// Something was on the clipboard and shhh could not take it: say which,
	// rather than silently pasting the text fallback of a screenshot.
	if msg.clip.Rejected != nil {
		return m.surfaceNotice("nothing attached — " + msg.clip.Rejected.Error())
	}
	if msg.clip.Text == "" {
		return m, nil
	}
	// Ctrl+V over ordinary text keeps doing what it always did.
	m.input.InsertString(msg.clip.Text)
	m.syncCompletions()
	m.syncViewport()
	return m, nil
}

// handleAttachedFile stages one file, or says why it could not.
func (m Model) handleAttachedFile(msg attachedFileMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.surfaceNotice("nothing attached — " + msg.err.Error())
	}
	return m.stage([]provider.Attachment{msg.attachment})
}

// stage adds attachments to the pending set, refusing what would push it
// past the total ceiling and saying so rather than truncating quietly.
func (m Model) stage(atts []provider.Attachment) (tea.Model, tea.Cmd) {
	var added []string
	for _, a := range atts {
		if provider.AttachmentBytes(m.attachments)+len(a.Data) > attachment.MaxTotalBytes {
			note := fmt.Sprintf("%s was not attached — one message carries at most %s, and %s is already staged",
				a.Name, attachment.HumanSize(attachment.MaxTotalBytes),
				attachment.HumanSize(provider.AttachmentBytes(m.attachments)))
			m.syncViewport()
			return m.surfaceNotice(note)
		}
		m.attachments = append(m.attachments, a)
		added = append(added, fmt.Sprintf("%s (%s)", a.Name, attachment.HumanSize(len(a.Data))))
	}
	if len(added) == 0 {
		return m, nil
	}
	// The staged rail may have appeared: the viewport is one line shorter.
	m.syncViewport()
	return m.surfaceNotice("attached " + strings.Join(added, ", ") +
		" — it goes with your next message (/paste clear drops it)")
}

// takeAttachments hands the staged set to the message being sent and empties
// the staging area. Every send path goes through it, so an attachment can
// only ever ride once.
func (m *Model) takeAttachments() []provider.Attachment {
	if len(m.attachments) == 0 {
		return nil
	}
	atts := m.attachments
	m.attachments = nil
	return atts
}

// stagedRail is the frame's staged rail: one chip per attachment
// waiting to ride, drawn by components.AttachmentChips. It is
// orchestrator-scoped like the notice rail above it — attached, the keyboard
// is pointed at a child and ctrl+v is a textarea key again, so the
// orchestrator's staging area is not what the reader is looking at.
func (m Model) stagedRail() string {
	if m.attachedTo != "" {
		return ""
	}
	return components.AttachmentChips(m.attachmentChips(), m.contentWidth())
}

// attachmentChips is the staged set as the strip draws it.
func (m Model) attachmentChips() []components.AttachmentChip {
	chips := make([]components.AttachmentChip, 0, len(m.attachments))
	for _, a := range m.attachments {
		chips = append(chips, components.AttachmentChip{
			Kind: chipKind(a.Kind),
			Name: a.Name,
			Size: attachment.HumanSize(len(a.Data)),
		})
	}
	return chips
}

// chipKind maps what the sniffer decided onto the mark the strip draws. The
// two vocabularies stay separate on purpose: one is how a provider carries
// the bytes, the other is a glyph in a closed set.
func chipKind(k provider.AttachmentKind) components.ChipKind {
	switch k {
	case provider.AttachmentImage:
		return components.ChipImage
	case provider.AttachmentDocument:
		return components.ChipDocument
	}
	return components.ChipText
}

// runPaste dispatches `/paste`: bare reads the clipboard, `clear` drops what
// is staged, `drop <name>` drops one chip, `show <name>` opens one as a
// picture (S-158), and anything else is a path.
func (m Model) runPaste(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) == 1 {
		return m, readClipboardCmd()
	}
	arg := strings.TrimSpace(strings.Join(parts[1:], " "))
	if strings.EqualFold(arg, "clear") {
		if len(m.attachments) == 0 {
			return m.surfaceNotice("nothing is attached")
		}
		dropped := attachment.Summarize(m.attachments)
		m.attachments = nil
		m.syncViewport()
		return m.surfaceNotice("dropped " + dropped)
	}
	if rest, ok := cutFold(arg, "drop"); ok {
		return m.dropAttachment(rest)
	}
	if rest, ok := cutFold(arg, "show"); ok {
		return m.showAttachment(rest)
	}
	return m, attachFileCmd(attachment.Expand(arg))
}

// cutFold splits a leading subcommand word off an argument, matched the way
// `clear` is: case-insensitively, and on the whole word — so a file called
// `dropbox.png` is still a path.
func cutFold(arg, word string) (string, bool) {
	if len(arg) < len(word) || !strings.EqualFold(arg[:len(word)], word) {
		return "", false
	}
	rest := arg[len(word):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// dropAttachment takes one staged attachment back out by name — the per-chip
// half of what `clear` does to the whole strip.
//
// A chip carries no key of its own: it sits above a live draft, so the
// name printed on it is the handle instead, and the completion menu offers
// the staged names (S-079) so it is never typed from memory. A name that is
// not staged is said out loud with the ones that are, for the same reason a
// refused attachment is: a drop that quietly did nothing is a message that
// goes out carrying the file you meant to remove.
func (m Model) dropAttachment(name string) (tea.Model, tea.Cmd) {
	if len(m.attachments) == 0 {
		return m.surfaceNotice("nothing is attached")
	}
	staged := strings.Join(attachment.Names(m.attachments), ", ")
	if name == "" {
		return m.surfaceNotice("/paste drop needs a name — " + staged)
	}
	for i, a := range m.attachments {
		if !strings.EqualFold(a.Name, name) {
			continue
		}
		dropped := fmt.Sprintf("%s (%s)", a.Name, attachment.HumanSize(len(a.Data)))
		// A full slice expression, because the staged set is handed off whole
		// by takeAttachments and must not be shortened through a shared array.
		m.attachments = append(m.attachments[:i:i], m.attachments[i+1:]...)
		m.syncViewport()
		return m.surfaceNotice("dropped " + dropped)
	}
	return m.surfaceNotice(name + " is not attached — " + staged)
}

// pastedFileAttachment reports the file a bracketed paste was pointing at,
// when attaching it is the only sensible reading of the paste. A dragged-in
// screenshot is that; a pasted path to a source file is not — the agent
// reads those with a tool, and swallowing the text would take away the only
// way to write one into a sentence.
// It runs on the event loop — a paste is a keystroke — so it only peeks at
// the file's first bytes; the read that attaches it happens in a command.
func pastedFileAttachment(pasted string) (string, bool) {
	path, ok := attachment.LooksLikeFile(pasted)
	if !ok {
		return "", false
	}
	kind, err := attachment.PeekKind(path)
	if err != nil || kind == provider.AttachmentText {
		return "", false
	}
	return path, true
}
