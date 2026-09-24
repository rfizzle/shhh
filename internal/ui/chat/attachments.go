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
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
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
				"the agent reads one with a tool",
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
	staged, note := m.stageQuietly([]provider.Attachment{a})
	if !staged.isStaged(a.Name) {
		// The ceiling refused it, and the refusal is what there is to say. A
		// fold for bytes that are not riding would be a sentence promising
		// something the message is not carrying.
		return staged.surfaceNotice(note)
	}
	return staged.insertPasteToken(a)
}

// isStaged reports whether the staging area is holding a name.
func (m Model) isStaged(name string) bool {
	for _, a := range m.attachments {
		if strings.EqualFold(a.Name, name) {
			return true
		}
	}
	return false
}

// insertPasteToken puts the fold in the draft where the paste was pasted and
// says what the reader now has (docs/interface/surfaces.md#the-input-frame).
//
// The token goes in at the cursor rather than at the end, because the cursor
// is where the log was going to land: a reader who pasted a stack trace into
// the middle of a sentence gets the mark in the middle of the sentence, and
// goes on typing after it.
//
// It says this instead of the staging area's own sentence rather than as well
// as it: they are one act, and the chip is on screen saying what is riding, so
// what is left to say is the two things only this row can — that the fold is
// one character, and the key that opens it. The backspace is named here and
// not on the rail, because it is the line editor's key and not an offer: what
// the fold changes is how much one press of it takes.
func (m Model) insertPasteToken(a provider.Attachment) (tea.Model, tea.Cmd) {
	p, ok := pasteOf(a)
	if !ok {
		return m, nil
	}
	m.input.InsertString(p.token)
	m.syncCompletions()
	m.syncViewport()
	return m.surfaceNotice(fmt.Sprintf(
		"%s is one character in your draft — %s opens it, backspace over it drops all %s",
		p.token, keys.Bracket(keys.Draft.OpenPaste), countedPasteLines(p.lines)))
}

// countedPasteLines is a paste's height in the words the chip and the fold
// row already count it in, for the sentences that name it inside prose.
func countedPasteLines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return strconv.Itoa(n) + " lines"
}

// pasteFoldKey answers the four keys a fold changes the reach of, and
// reports whether it took one (docs/interface/surfaces.md#the-input-frame).
//
// A fold is one character to the keyboard: an arrow that would land inside
// the run steps over the whole of it, and a delete that would eat one rune of
// it takes the token and the paste it stands for together. Half a token is
// the state this exists to make unreachable — `⟨paste 1 · 214 line`, still
// attached, still priced, and no longer anything the reader can act on.
//
// Deleting the fold drops the paste. The alternative is a staging area with a
// log in it that the sentence no longer mentions, which is the same message
// going out with two hundred lines nobody meant to send.
func (m Model) pasteFoldKey(msg tea.KeyPressMsg) (tea.Model, bool) {
	if !m.inputLive() || m.attachedTo != "" || len(m.attachments) == 0 {
		return m, false
	}
	if msg.Mod != 0 {
		// A modified arrow is a word jump or a selection, which the textarea
		// owns and which a fold has nothing to say about.
		return m, false
	}
	value := m.input.Value()
	at := m.draftOffset(value)
	for _, a := range m.attachments {
		p, ok := pasteOf(a)
		if !ok {
			continue
		}
		from := strings.Index(value, p.token)
		if from < 0 {
			continue
		}
		lo := len([]rune(value[:from]))
		hi := lo + len([]rune(p.token))
		switch {
		case msg.Code == tea.KeyLeft && at == hi:
			m.setDraft(value, lo)
		case msg.Code == tea.KeyRight && at == lo:
			m.setDraft(value, hi)
		case (msg.Code == tea.KeyBackspace && at == hi) ||
			(msg.Code == tea.KeyDelete && at == lo):
			return m.removePaste(a)
		default:
			continue
		}
		return m, true
	}
	return m, false
}

// draftOffset is the cursor's place in the draft as a rune offset into the
// whole value, which is the one coordinate a token's span can be compared
// against: the textarea reports a row and a column, and a fold that wrapped
// sits in two of them.
func (m Model) draftOffset(value string) int {
	lines := strings.Split(value, "\n")
	row := min(m.input.Line(), len(lines)-1)
	at := 0
	for _, line := range lines[:max(row, 0)] {
		at += len([]rune(line)) + 1
	}
	return at + m.input.Column()
}

// removePaste takes one paste out of the staging area and its fold out of the
// draft, and says what went. It is what the reader's backspace over a token
// does and what the reader's key on the open paste does, because they are the
// same act reached from the two places the paste is on screen.
func (m Model) removePaste(a provider.Attachment) (tea.Model, bool) {
	p, ok := pasteOf(a)
	if !ok {
		return m, false
	}
	for i, staged := range m.attachments {
		if !strings.EqualFold(staged.Name, a.Name) {
			continue
		}
		// A full slice expression, for takeAttachments' reason: the staged
		// set is handed off whole and must not be shortened through a shared
		// array.
		m.attachments = append(m.attachments[:i:i], m.attachments[i+1:]...)
		m.dropPasteToken(a)
		m.syncViewport()
		// Its own sentence rather than `/paste drop`'s, and only one: the
		// reader is looking at the sentence the fold came out of, so what
		// there is to say is what went with it.
		next, _ := m.surfaceNotice(fmt.Sprintf("dropped %s — all %s of it",
			p.label, countedPasteLines(p.lines)))
		return next, true
	}
	return m, false
}

// dropPasteToken takes the fold for one paste back out of the draft. It is
// called wherever a chip leaves the staging area — the reader's backspace,
// `/paste drop`, the reader's `[x]` — because a token standing for bytes
// that are no longer staged is a sentence that lies about what it carries.
//
// Only the first occurrence goes. A reader who copied their own token into
// the sentence twice meant the second one to be there as text, and this is
// not the surface that gets to decide otherwise.
func (m *Model) dropPasteToken(a provider.Attachment) {
	p, ok := pasteOf(a)
	if !ok {
		return
	}
	value := m.input.Value()
	at := strings.Index(value, p.token)
	if at < 0 {
		return
	}
	before := len([]rune(value[:at]))
	m.setDraft(value[:at]+value[at+len(p.token):], before)
}

// setDraft replaces the draft and puts the cursor at a rune offset into it.
// The textarea leaves the cursor at the end of whatever it was given, which
// is the one place a reader who was editing the middle of a sentence never
// wanted it.
func (m *Model) setDraft(value string, offset int) {
	runes := []rune(value)
	before := string(runes[:min(max(offset, 0), len(runes))])
	m.input.SetValue(value)
	m.input.MoveToBegin()
	for range strings.Count(before, "\n") {
		m.input.CursorDown()
	}
	m.input.SetCursorColumn(len([]rune(before[strings.LastIndex(before, "\n")+1:])))
	m.syncCompletions()
	m.syncViewport()
}

// stagedPaste is one staged paste as everything but the chip strip sees it:
// what to call it in a sentence, the fold that stands for it in the draft,
// how far it runs, and what it will cost the next request.
//
// It is derived from the staging area on every read rather than kept beside
// it. There is one staging area and it is already the answer to "what is
// riding on this message"; a second list would be a second answer, and the
// two would part company the first time a chip was dropped by a door that
// did not know about the list.
type stagedPaste struct {
	label  string
	token  string
	lines  int
	tokens int64
}

// stagedPastes is the pastes in the staging area, in the order they were
// staged. A staged file that somebody happened to name paste-2.txt is one of
// these as far as the draft is concerned, which is the right answer: the
// fold stands for whatever the chip stands for.
func (m Model) stagedPastes() []stagedPaste {
	var out []stagedPaste
	for _, a := range m.attachments {
		if p, ok := pasteOf(a); ok {
			out = append(out, p)
		}
	}
	return out
}

// pasteOf reads one attachment as a paste, or reports that it is not one.
func pasteOf(a provider.Attachment) (stagedPaste, bool) {
	label, ok := pasteLabel(a.Name)
	if !ok || a.Kind != provider.AttachmentText {
		return stagedPaste{}, false
	}
	lines := attachment.LineCount(a.Data)
	return stagedPaste{
		label:  label,
		token:  components.PasteToken(label, lines),
		lines:  lines,
		tokens: agent.EstimateBytesTokens(a.Data),
	}, true
}

// pasteLabel is what a staged paste is called in a sentence — `paste 1` for
// paste-1.txt. The file name is the handle `/paste drop` and `/paste show`
// take and it has to be typable; the label is what a reader reads inside
// their own sentence, and a file extension in the middle of one is noise
// about a file that does not exist.
func pasteLabel(name string) (string, bool) {
	digits, ok := strings.CutPrefix(name, pasteNamePrefix)
	if !ok {
		return "", false
	}
	digits, ok = strings.CutSuffix(digits, pasteNameSuffix)
	if !ok || digits == "" {
		return "", false
	}
	if _, err := strconv.Atoi(digits); err != nil {
		return "", false
	}
	return "paste " + digits, true
}

// The two halves of attachment.PasteName, so a label can be read back off a
// name that package wrote. They are split off it rather than spelled again
// here, because a name composed from two constants and taken apart by a
// third spelling is a pair that drifts the day either end moves.
var pasteNamePrefix, pasteNameSuffix = func() (string, string) {
	name := attachment.PasteName(0)
	digit := strings.LastIndex(name, "0")
	return name[:digit], name[digit+1:]
}()

// pasteCost is the vitals rail's clause while pastes are staged: what the
// next message is about to pay for them. A paste is the one keystroke that
// can double the price of a request, and the rail is where the price of this
// session is already read (docs/interface/surfaces.md#the-input-frame).
//
// Several pastes are counted rather than listed. The clause is a field on a
// rail that already sheds fields, and three of them spelled out would push
// the context meter off the row to say three times what one sentence says.
func (m Model) pasteCost() string {
	staged := m.stagedPastes()
	if len(staged) == 0 {
		return ""
	}
	var total int64
	subject := staged[0].label
	for _, p := range staged {
		total += p.tokens
	}
	if len(staged) > 1 {
		subject = fmt.Sprintf("%d pastes", len(staged))
	}
	return fmt.Sprintf("%s will cost ~%s tokens", subject, components.FormatCount(total))
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
	next, note := m.stageQuietly(atts)
	if note == "" {
		return next, nil
	}
	return next.surfaceNotice(note)
}

// stageQuietly is the staging without the sentence about it, and the sentence
// it would have said. A caller with a better one to say — the paste, which
// has a fold in the draft to point at — takes the answer and says its own,
// because two notices about one act read as two acts.
func (m Model) stageQuietly(atts []provider.Attachment) (Model, string) {
	var added []string
	for _, a := range atts {
		if provider.AttachmentBytes(m.attachments)+len(a.Data) > attachment.MaxTotalBytes {
			m.syncViewport()
			return m, fmt.Sprintf("%s was not attached — one message carries at most %s, and %s is already staged",
				a.Name, attachment.HumanSize(attachment.MaxTotalBytes),
				attachment.HumanSize(provider.AttachmentBytes(m.attachments)))
		}
		m.attachments = append(m.attachments, a)
		added = append(added, fmt.Sprintf("%s (%s)", a.Name, attachment.HumanSize(len(a.Data))))
	}
	if len(added) == 0 {
		return m, ""
	}
	// The staged rail may have appeared: the viewport is one line shorter.
	m.syncViewport()
	return m, "attached " + strings.Join(added, ", ") +
		" — it goes with your next message (/paste clear drops it)"
}

// pasteFold is one paste as the transcript keeps it after the send: the fold
// row's own facts, and the lines behind them
// (docs/interface/surfaces.md#the-input-frame).
//
// The body is copied onto the entry rather than left in the staging area,
// because the staging area is emptied by the send and the row outlives it by
// the whole session. It is the same bytes the request carried, which is what
// makes the row an account of what was sent rather than of what is staged.
type pasteFold struct {
	label  string
	lines  int
	tokens int64
	body   []string
}

// userEntry is the transcript row one sent message leaves. What rode with it
// is named two ways and the sentence decides which: a paste whose fold is in
// the words is kept as that fold, and everything else is named on the
// `attached:` line under them.
//
// The split is the sentence's because that is where the reader will look. A
// log the reader put in the middle of what they were asking is accounted for
// where they put it; a screenshot they attached is a thing the message
// carried and has nowhere in the words to be.
func userEntry(text string, atts []provider.Attachment) entry {
	e := entry{kind: entryUser, text: text}
	for _, a := range atts {
		p, ok := pasteOf(a)
		if !ok || !strings.Contains(text, p.token) {
			e.attached = append(e.attached, attachment.Names([]provider.Attachment{a})...)
			continue
		}
		e.pastes = append(e.pastes, pasteFold{
			label:  p.label,
			lines:  p.lines,
			tokens: p.tokens,
			body:   strings.Split(strings.TrimSuffix(string(a.Data), "\n"), "\n"),
		})
	}
	return e
}

// pasteFoldBlock is the row a sent paste leaves under the sentence it was
// pasted into: `▸ paste 1 · 214 lines · 6.1k tokens · [enter] expand`, and
// the lines themselves when the reader has opened it
// (docs/interface/surfaces.md#the-input-frame).
//
// The row is the fold every other body in the transcript wears, on the same
// grid: the mark takes the glyph column and what it swallowed starts in the
// verb column, so a paste and a folded run of calls line up
// (docs/interface/principles.md#fold-never-hide). What it counts is the two
// figures the reader cannot get anywhere else — how far the log runs, and
// what it cost the request that carried it.
//
// Open, it is bounded the way a tool's output is and the bound counts what it
// held back. The paste is in the context window; it does not have to be in
// the scrollback as well, and the depth past this one gives the whole of it
// back on its own screen (outputview.go).
func (m Model) pasteFoldBlock(p pasteFold, open bool, width int) string {
	mark, offer := "▸", "expand"
	if open {
		mark, offer = "▾", "fold it back up"
	}
	const sep = " · "
	held := fmt.Sprintf("%s %s%s%s%s%s tokens", mark, p.label, sep,
		countedPasteLines(p.lines), sep, components.FormatCount(p.tokens))
	key := keys.Bracket(keys.Reading.Expand) + " " + offer
	lead := strings.Repeat(" ", components.GridVerbColumn-2)
	out := lead + sty.SystemMsg.Render(held+sep) + sty.Hint.Key.Render(key) + "\n"
	if !open {
		return out
	}
	body := p.body
	if len(body) > maxToolResultLines {
		body = body[:maxToolResultLines]
	}
	inner := max(width-components.GridDetailIndent, 1)
	for _, line := range body {
		out += strings.Repeat(" ", components.GridDetailIndent) +
			sty.Frame.PasteBody.Render(components.Clip(line, inner)) + "\n"
	}
	if rest := len(p.body) - len(body); rest > 0 {
		out += strings.Repeat(" ", components.GridDetailIndent) +
			sty.Hint.Dim.Render(components.Clip(fmt.Sprintf("… %s more, %s opens the whole of it",
				countedPasteLines(rest), keys.Bracket(keys.Reading.Expand)), inner)) + "\n"
	}
	return out
}

// pasteFoldOverflows reports whether any of a row's folds held back more than
// the opened window shows, which is what makes the depth past it an offer
// rather than a press that changes nothing.
func pasteFoldOverflows(e entry) bool {
	for _, p := range e.pastes {
		if len(p.body) > maxToolResultLines {
			return true
		}
	}
	return false
}

// pasteOutputView is the whole of a row's pastes on their own screen — the
// third depth, where the bound stops applying. Several folds on one message
// are one view with each named where it starts, because they were one send
// and the reader opened the row rather than one of them.
func pasteOutputView(e entry) *components.OutputView {
	var lines []string
	title := ""
	for i, p := range e.pastes {
		if i == 0 {
			title = p.label
		} else {
			lines = append(lines, "", strings.ToUpper(p.label))
		}
		lines = append(lines, p.body...)
	}
	if len(e.pastes) > 1 {
		title = fmt.Sprintf("%d pastes", len(e.pastes))
	}
	return &components.OutputView{Title: title, Lines: lines}
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
		for _, a := range m.attachments {
			m.dropPasteToken(a)
		}
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
		m.dropPasteToken(a)
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
			keys.Bracket(keys.Select.Toggle), keys.Bracket(keys.Select.Take), keys.Bracket(keys.Select.Cancel)), opts)
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
		done, yes := c.Update(msg)
		if !done {
			return m, nil
		}
		m.pasteDropConfirm = nil
		m.leaveSurface()
		m.syncViewport()
		if yes && len(m.attachments) == 1 {
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
			m.dropPasteToken(a)
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
