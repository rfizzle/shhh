package chat

// Attachments: pictures, recordings and files staged for the next message. An
// attachment shows as a chip carrying its mark, its name and its size —
// and, where it is text, how far it runs — on the frame's staged rail while
// it waits and on the user's own transcript row once it has gone. Nothing
// here draws the bytes: `/paste show` is the one surface that does, opened by
// naming a chip and given the whole pane while it is up (preview.go). What
// the bytes are for is the request — they ride on the user message
// (internal/provider), and each provider carries them the way its API takes
// them.
//
// Four doors, one staging area. Ctrl+V reads the clipboard — a pasted
// screenshot or the files a file manager copied — and falls back to pasting
// text into the draft when the clipboard holds only text, so the chord never
// stops doing what it used to. A path dragged into the terminal arrives as a
// bracketed paste and is attached when it points at anything but a text file
// — a screenshot, a PDF, a voice memo — because that is what dragging one of
// those in means. A paste of text too big to
// compose around is staged as a file of its own rather than typed. `/paste`
// is the explicit form, and the only one that can name a file the clipboard
// never touched.
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
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	// A clipboard that holds a log is the same question a bracketed paste of
	// one asks, and gets the same answer: the door does not change what is
	// too big to compose around.
	if pasted := attachment.NormalizeNewlines(msg.clip.Text); m.pasteOverflows(pasted) {
		return m.stagePaste(pasted)
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

// WithPasteThresholds sets the shape past which a paste is staged rather than
// typed (appearance.paste_lines / appearance.paste_columns). Zero on either
// keeps that half at its default; what any other value means is
// attachment.PasteOverflows'.
func (m Model) WithPasteThresholds(lines, columns int) Model {
	if lines != 0 {
		m.pasteLines = lines
	}
	if columns != 0 {
		m.pasteColumns = columns
	}
	return m
}

// pasteOverflows is the session's own reading of attachment.PasteOverflows:
// this session's thresholds, against text whose line endings are already
// settled. Both doors onto the staging area ask it, so neither can drift into
// staging what the other would have typed.
func (m Model) pasteOverflows(text string) bool {
	return attachment.PasteOverflows(text, m.pasteLines, m.pasteColumns)
}

// stagePaste takes a paste too big for the draft and stages it as a file
// instead (docs/interface/surfaces.md#the-input-frame).
//
// It runs where the paste arrived rather than in a command: the bytes are
// already in hand, so there is nothing to read and nothing to wait for, and
// routing it through one would let the next keystroke land in the draft
// before the paste had decided it was not going there.
//
// A paste past the ceiling is refused with the ceiling named and the draft
// left exactly as it was. The alternative — typing it in after all — puts a
// megabyte in the box the reader then has to get back out, and the bytes are
// still on the clipboard either way.
func (m Model) stagePaste(text string) (tea.Model, tea.Cmd) {
	// The ceiling on a paste is the text ceiling and not the attachment one:
	// a paste has no file behind it, so it goes into the prompt verbatim and
	// what bounds it is the context window. The refusal says so in those
	// words, because attachment.FromBytes' answer — attach a smaller file, or
	// let the agent read it with a tool — names two things a reader who just
	// hit ⌘V does not have.
	if len(text) > attachment.MaxTextBytes {
		return m.surfaceNotice(fmt.Sprintf(
			"nothing attached — that paste is %s, and a paste rides in the prompt "+
				"itself, so the limit is %s. Save it to a file and ask for it by name; "+
				"the agent reads one with a tool.",
			attachment.HumanSize(len(text)), attachment.HumanSize(attachment.MaxTextBytes)))
	}
	a, err := attachment.FromBytes(nextPasteName(m.attachments), []byte(text))
	if err != nil {
		return m.surfaceNotice("nothing attached — " + err.Error())
	}
	// Bytes win over the extension everywhere else, and here they must not: a
	// paste that happens to begin with %PDF- is still a paste, and staging it
	// as a document called paste-1.txt would send the provider a name that
	// contradicts the part it was put in.
	if a.Kind != provider.AttachmentText {
		return m.surfaceNotice("nothing attached — that paste is not text, it reads as " + a.MediaType)
	}
	return m.stage([]provider.Attachment{a})
}

// nextPasteName is the name the paste being staged takes: the lowest number
// no chip is already using, matched the way `/paste drop` matches a name.
//
// It numbers what is staged rather than what the session has sent, because
// the name is a handle for `/paste drop` and `/paste show` and both can only
// reach what is staged now — a counter that climbed all session would make
// the first chip of an emptied strip `paste-9.txt`.
func nextPasteName(staged []provider.Attachment) string {
	for n := 1; ; n++ {
		name := attachment.PasteName(n)
		taken := false
		for _, a := range staged {
			if strings.EqualFold(a.Name, name) {
				taken = true
				break
			}
		}
		if !taken {
			return name
		}
	}
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
		chip := components.AttachmentChip{
			Kind: chipKind(a.Kind),
			Name: a.Name,
			Size: attachment.HumanSize(len(a.Data)),
		}
		// Only text has lines. A stat that cannot be reported is left out
		// rather than reported as zero
		// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
		if a.Kind == provider.AttachmentText {
			chip.Lines = attachment.LineCount(a.Data)
		}
		chips = append(chips, chip)
	}
	return chips
}

// chipKind maps what the sniffer decided onto the mark the strip draws. The
// two vocabularies stay separate on purpose: one is how a provider carries
// the bytes, the other is a glyph in a closed set — which is why a recording
// takes the document mark rather than a fourth glyph. Both are one artifact
// the model takes whole and neither has lines to count, and the other reading
// available, the text mark, promises a body in the prompt that a recording
// has not got.
func chipKind(k provider.AttachmentKind) components.ChipKind {
	switch k {
	case provider.AttachmentImage:
		return components.ChipImage
	case provider.AttachmentDocument, provider.AttachmentAudio:
		return components.ChipDocument
	}
	return components.ChipText
}

// runPaste dispatches `/paste`: bare reads the clipboard, `clear` drops what
// is staged, `drop <name>` drops one chip, `show <name>` opens one as a
// picture, and anything else is a path.
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
		if rest == "" {
			return m.openPasteDrop()
		}
		return m.dropAttachment(rest)
	}
	if rest, ok := cutFold(arg, "show"); ok {
		return m.showAttachment(rest)
	}
	return m, attachFileCmd(m.inWorkspace(attachment.Expand(arg)))
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
// the staged names so it is never typed from memory. A name that is
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

// openPasteDrop is a bare `/paste drop`: the keyboard path to taking a
// chip back out without typing its name. Nothing staged says so; exactly
// one chip asks through the inline confirm, named, defaulting to No;
// several open the selector — checked chips are dropped on enter, esc
// drops none (docs/interface/surfaces.md#a-staged-attachment).
func (m Model) openPasteDrop() (tea.Model, tea.Cmd) {
	switch len(m.attachments) {
	case 0:
		return m.surfaceNotice("nothing is attached")
	case 1:
		a := m.attachments[0]
		m.pasteDropConfirm = &components.Confirm{
			Prompt: fmt.Sprintf("Drop %s (%s)?", a.Name, attachment.HumanSize(len(a.Data))),
		}
	default:
		opts := make([]components.SelectOption, len(m.attachments))
		for i, a := range m.attachments {
			opts[i] = components.SelectOption{Label: a.Name, Meta: attachment.HumanSize(len(a.Data))}
		}
		card := components.NewMultiSelect(fmt.Sprintf(
			"Drop staged attachments — %s toggles, %s drops the checked ones, %s drops none",
			keys.Shown(keys.Select.Toggle), keys.Shown(keys.Select.Take), keys.Shown(keys.Select.Cancel)), opts)
		card.MaxLines = m.maxConfirmPanelHeight()
		m.pasteDrop = card
	}
	m.enterSurface(statePasteDrop)
	m.syncViewport()
	return m, nil
}

// updatePasteDrop routes keys while the drop selector or its one-chip
// confirm is up.
func (m Model) updatePasteDrop(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if c := m.pasteDropConfirm; c != nil {
		done, result := c.Update(msg)
		if !done {
			return m, nil
		}
		m.pasteDropConfirm = nil
		m.leaveSurface()
		m.syncViewport()
		if yes, _ := result.(bool); yes && len(m.attachments) == 1 {
			return m.dropAttachment(m.attachments[0].Name)
		}
		return m.surfaceNotice("nothing dropped")
	}
	if m.pasteDrop == nil {
		m.leaveSurface()
		return m, nil
	}
	done, res := m.pasteDrop.Update(msg)
	if !done {
		return m, nil
	}
	m.pasteDrop = nil
	m.leaveSurface()
	m.syncViewport()
	if res.Canceled {
		return m.surfaceNotice("nothing dropped")
	}
	return m.dropAttachments(res.Indices)
}

// dropAttachments removes the chosen chips and names what went, the way a
// single drop does.
func (m Model) dropAttachments(indices []int) (tea.Model, tea.Cmd) {
	chosen := map[int]bool{}
	for _, i := range indices {
		chosen[i] = true
	}
	var kept []provider.Attachment
	var dropped []string
	for i, a := range m.attachments {
		if chosen[i] {
			dropped = append(dropped, fmt.Sprintf("%s (%s)", a.Name, attachment.HumanSize(len(a.Data))))
		} else {
			kept = append(kept, a)
		}
	}
	if len(dropped) == 0 {
		return m.surfaceNotice("nothing dropped")
	}
	m.attachments = kept
	m.syncViewport()
	return m.surfaceNotice("dropped " + strings.Join(dropped, ", "))
}

// pasteDropLines renders whichever form the drop question took.
func (m Model) pasteDropLines() []string {
	if m.pasteDropConfirm != nil {
		return []string{m.pasteDropConfirm.View(m.contentWidth())}
	}
	if m.pasteDrop == nil {
		return nil
	}
	return strings.Split(m.pasteDrop.View(m.contentWidth()), "\n")
}

// pastedFileAttachment reports the file a bracketed paste was pointing at,
// when attaching it is the only sensible reading of the paste. A dragged-in
// screenshot or voice memo is that; a pasted path to a source file is not —
// the agent reads those with a tool, and swallowing the text would take away
// the only way to write one into a sentence.
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
