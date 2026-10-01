package chat

// Attachments: pictures, recordings and files staged for the next message. An
// attachment shows as a chip carrying its mark, its name and its size —
// and, where it is text, how far it runs — on the frame's staged rail while
// it waits and on the user's own transcript row once it has gone. Nothing
// here draws the bytes: the preview card is the one surface that does, opened
// from the chip or by naming it and given the whole pane while it is up
// (preview.go, stagedstrip.go). What
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
	"os"
	"path/filepath"
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
	// atCursor is that the read came from the draft's own key rather than
	// from `/paste`, so what it stages leaves a fold where the cursor is.
	atCursor bool
}

// attachedFileMsg carries the result of attaching one named file.
type attachedFileMsg struct {
	attachment provider.Attachment
	// path is where the file was read from, which a picture's row names the
	// folder of once it is sent (sourcePhrase).
	path string
	err  error
	// atCursor is that the path was dragged into the draft rather than named
	// to `/paste` or mentioned, so the file leaves a fold where it landed.
	atCursor bool
}

// readClipboardCmd reads the clipboard off the render loop. atCursor says
// whether the read was asked for at the draft's cursor (stageAtCursor).
func readClipboardCmd(atCursor bool) tea.Cmd {
	return func() tea.Msg {
		clip, err := attachment.Read()
		return clipboardMsg{clip: clip, err: err, atCursor: atCursor}
	}
}

// attachFileCmd reads one file off disk as an attachment.
func attachFileCmd(path string, atCursor bool) tea.Cmd {
	return func() tea.Msg {
		a, err := attachment.FromFile(path)
		return attachedFileMsg{attachment: a, path: path, err: err, atCursor: atCursor}
	}
}

// handleClipboard stages what the clipboard held, or types it.
func (m Model) handleClipboard(msg clipboardMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.surfaceNotice("nothing attached — " + msg.err.Error())
	}
	if len(msg.clip.Attachments) > 0 {
		before := len(m.attachments)
		if msg.atCursor {
			return noteFrom(m.stageAtCursor(msg.clip.Attachments))(before, "from the clipboard")
		}
		return noteFrom(m.stage(msg.clip.Attachments))(before, "from the clipboard")
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
	before, from := len(m.attachments), sourcePhrase(msg.path)
	if msg.atCursor {
		return noteFrom(m.stageAtCursor([]provider.Attachment{msg.attachment}))(before, from)
	}
	return noteFrom(m.stage([]provider.Attachment{msg.attachment}))(before, from)
}

// noteFrom records where the pictures a door just staged came from, by the
// handles they were given: everything past before in the staging area is
// what the door added. It is written as a function of the door's own result
// so the door's command rides through untouched.
func noteFrom(next tea.Model, cmd tea.Cmd) func(before int, from string) (tea.Model, tea.Cmd) {
	return func(before int, from string) (tea.Model, tea.Cmd) {
		nm, ok := next.(Model)
		if !ok || from == "" || before >= len(nm.attachments) {
			return next, cmd
		}
		for _, a := range nm.attachments[before:] {
			if a.Kind != provider.AttachmentImage || a.Handle == "" {
				continue
			}
			if nm.staged.from == nil {
				nm.staged.from = map[string]string{}
			}
			nm.staged.from[a.Handle] = from
		}
		return nm, cmd
	}
}

// sourcePhrase is the folder a file was attached from, as a row says it:
// `from ~/Desktop`, the home directory written the way the reader types it.
// The folder and not the path, because the name is already on the row.
func sourcePhrase(path string) string {
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if dir == home {
			dir = "~"
		} else if rest, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok {
			dir = "~" + string(filepath.Separator) + rest
		}
	}
	return "from " + dir
}

// stageAtCursor stages what arrived at the draft's cursor — a picture off
// the clipboard, a path dragged in — and leaves a fold where the cursor was,
// the way a paste does, so the sentence can point at what it carries
// (docs/interface/surfaces.md#the-input-frame). `/paste <path>` does not come
// through here: the command is the sentence, and there is no cursor in it.
//
// The staging area's own sentence is what it says, because it already names
// the handle the fold carries and the way to reach the chip; several at once
// are folded side by side, a space apart, in the order they were staged.
// What the ceiling refused leaves no fold, for stagePaste's reason.
func (m Model) stageAtCursor(atts []provider.Attachment) (tea.Model, tea.Cmd) {
	staged, note := m.stageQuietly(atts)
	var tokens []string
	for _, a := range staged.attachments[len(m.attachments):] {
		if p, ok := pasteOf(a); ok {
			tokens = append(tokens, p.token)
		}
	}
	if len(tokens) > 0 {
		staged.input.InsertString(strings.Join(tokens, " "))
		staged.syncCompletions()
		staged.syncViewport()
	}
	if note == "" {
		return staged, nil
	}
	return staged.surfaceNotice(note)
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
	// The name is settled with the number, when stageQuietly hands out the
	// handle; until then it only has to read as text to the sniffer.
	a, err := attachment.FromBytes(attachment.PasteName(0), []byte(text))
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
	// Asked for by word: text is a file to the sniffer, and this door is the
	// one that knows there was never a file behind it.
	a.Handle = attachment.HandlePaste
	staged, note := m.stageQuietly([]provider.Attachment{a})
	if len(staged.attachments) == len(m.attachments) {
		// The ceiling refused it, and the refusal is what there is to say. A
		// fold for bytes that are not riding would be a sentence promising
		// something the message is not carrying.
		return staged.surfaceNotice(note)
	}
	return staged.insertPasteToken(staged.attachments[len(staged.attachments)-1])
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
		"%s is one character in your draft — its chip opens it, backspace over it drops all %s",
		p.token, countedPasteLines(p.lines)))
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
// the state this exists to make unreachable — `⟨Paste#1 · 214 line`, still
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
		if staged.Handle != a.Handle || !strings.EqualFold(staged.Name, a.Name) {
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
		// there is to say is what went with it. A picture or a file has no
		// lines to count, and is named the way every other drop names it.
		note := "dropped " + describeStaged(a)
		if p.paste {
			note = fmt.Sprintf("dropped %s — all %s of it", p.label, countedPasteLines(p.lines))
		}
		next, _ := m.surfaceNotice(note)
		return next, true
	}
	return m, false
}

// dropPasteToken takes the fold for one attachment back out of the draft. It is
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

// stagedPaste is one staged attachment as its fold: what to call it in a
// sentence (its handle), the fold that stands for it in the draft, the one
// figure the fold counts it by, and — for text — how far it runs and what it
// will cost the next request. paste is that it is the text the draft staged
// itself, which is the one kind with a reader of its own and a price on the
// vitals rail.
//
// It is derived from the staging area on every read rather than kept beside
// it. There is one staging area and it is already the answer to "what is
// riding on this message"; a second list would be a second answer, and the
// two would part company the first time a chip was dropped by a door that
// did not know about the list.
type stagedPaste struct {
	label  string
	token  string
	figure string
	paste  bool
	lines  int
	tokens int64
}

// stagedPastes is the pastes in the staging area, in the order they were
// staged — the folds with a reader and a price, not the pictures and files
// beside them.
func (m Model) stagedPastes() []stagedPaste {
	var out []stagedPaste
	for _, a := range m.attachments {
		if p, ok := pasteOf(a); ok && p.paste {
			out = append(out, p)
		}
	}
	return out
}

// pasteOf is the one reading of a fold, whatever it stands for: the token an
// attachment leaves in a sentence, spelled from its handle and its figure
// (docs/interface/surfaces.md#the-input-frame). Whether a given attachment
// has a fold is whether its token is in the sentence — a file attached by
// `/paste <path>` has none — so every door that removes, repaints or
// rebuilds a fold asks this and nothing else. An attachment with no handle,
// which only a message saved before handles existed carries, has no fold.
func pasteOf(a provider.Attachment) (stagedPaste, bool) {
	if a.Handle == "" {
		return stagedPaste{}, false
	}
	figure := a.Figure()
	word, _, _ := attachment.SplitHandle(a.Handle)
	p := stagedPaste{
		label:  a.Handle,
		token:  components.PasteToken(a.Handle, figure),
		figure: figure,
		paste:  word == attachment.HandlePaste && a.Kind == provider.AttachmentText,
	}
	if a.Kind == provider.AttachmentText {
		p.lines = attachment.LineCount(a.Data)
		p.tokens = agent.EstimateBytesTokens(a.Data)
	}
	return p, true
}

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
		a = m.assignHandle(a)
		m.attachments = append(m.attachments, a)
		added = append(added, describeStaged(a))
	}
	if len(added) == 0 {
		return m, ""
	}
	// The staged rail may have appeared: the viewport is one line shorter.
	m.syncViewport()
	// The sentence names the way in rather than a command: a command is only
	// the first word of an empty draft, and this is usually said halfway
	// through a sentence. Reading mode keeps that sentence and reaches the
	// strip (docs/interface/surfaces.md#a-staged-attachment).
	return m, "attached " + strings.Join(added, ", ") +
		" — it goes with your next message; " + keys.Shown(keys.Draft.Reading) +
		" reaches it to look or drop"
}

// assignHandle gives one attachment being staged its handle
// (docs/capabilities/chat.md#what-can-ride-with-a-message). It is the one
// place a handle is handed out, so every door — the clipboard, a dragged
// path, `/paste <path>`, an overflowing paste, a recalled one — numbers
// through the same count.
//
// The count runs for the conversation rather than for what is staged: a
// handle is the word a reader will use for the thing, and the word the model
// will be handed for it, so `Image#1` must not come to mean a second picture
// because the first one left the strip.
//
// What arrives carrying a whole handle keeps it — a recalled paste is the
// same bytes it was, and it is the same Paste#2 — unless a chip already
// staged holds that handle, which would put two chips behind one word. What
// arrives carrying only a word is numbered under it; that is how a paste
// asks to be a paste. Anything else is numbered by its kind. A paste's name
// follows its number, so paste-3.txt is always Paste#3.
func (m *Model) assignHandle(a provider.Attachment) provider.Attachment {
	word, n, kept := attachment.SplitHandle(a.Handle)
	if kept && (m.stagedHandle(a.Handle) ||
		(word == attachment.HandlePaste && m.stagedName(attachment.PasteName(n)))) {
		kept = false
	}
	if kept {
		m.handles.Saw(a.Handle)
	} else {
		if word == "" {
			word = a.Handle
		}
		if word != attachment.HandlePaste {
			word = attachment.HandleWord(a.Kind)
		}
		for {
			a.Handle = m.handles.Next(word)
			_, n, _ = attachment.SplitHandle(a.Handle)
			// A file somebody attached as paste-2.txt would otherwise share
			// a name, and so a fold, with Paste#2.
			if word != attachment.HandlePaste || !m.stagedName(attachment.PasteName(n)) {
				break
			}
		}
	}
	if word == attachment.HandlePaste {
		a.Name = attachment.PasteName(n)
	}
	return a
}

// stagedHandle reports whether a chip already staged carries a handle.
func (m Model) stagedHandle(handle string) bool {
	for _, a := range m.attachments {
		if a.Handle == handle {
			return true
		}
	}
	return false
}

// stagedName reports whether a chip already staged carries a name, matched
// the way the verbs match one.
func (m Model) stagedName(name string) bool {
	for _, a := range m.attachments {
		if strings.EqualFold(a.Name, name) {
			return true
		}
	}
	return false
}

// seedHandles puts the conversation's count back to what a conversation
// already holds: the handles on its saved messages, and on whatever is staged
// and about to ride into it. Nothing is stored for the count itself — every
// handle ever sent is on the message that carried it, so the highest of each
// word is already on the rows.
func (m *Model) seedHandles(msgs []provider.Message) {
	m.handles = attachment.Handles{}
	// A count that starts over hands its handles out again, so what was
	// known of where the last conversation's pictures came from goes with
	// it; what is still staged keeps its own.
	from := map[string]string{}
	for _, a := range m.attachments {
		if f, ok := m.staged.from[a.Handle]; ok {
			from[a.Handle] = f
		}
	}
	m.staged.from = from
	for _, msg := range msgs {
		for _, a := range msg.Attachments {
			m.handles.Saw(a.Handle)
		}
	}
	for _, a := range m.attachments {
		m.handles.Saw(a.Handle)
	}
}

// describeStaged is one staged attachment the way a sentence about the strip
// names it: `Image#1 (clipboard.png, 412 KB)`, the handle first because it is
// what the two verbs take. An attachment with no handle is named by its name.
func describeStaged(a provider.Attachment) string {
	size := attachment.HumanSize(len(a.Data))
	if a.Handle == "" {
		return fmt.Sprintf("%s (%s)", a.Name, size)
	}
	return fmt.Sprintf("%s (%s, %s)", a.Handle, a.Name, size)
}

// describeAllStaged is the whole strip in describeStaged's words, for the
// refusals that list what could have been meant.
func describeAllStaged(atts []provider.Attachment) string {
	out := make([]string, len(atts))
	for i, a := range atts {
		out[i] = describeStaged(a)
	}
	return strings.Join(out, ", ")
}

// findStaged is the staged attachment a word from `/paste show` or `/paste
// drop` names, or the sentence refusing it. A handle is matched first, then a
// name, both case-folded. A name two chips share is refused with the handles
// that tell them apart rather than taken as the first of them, because taking
// the first is how three pasted screenshots came to be one reachable chip
// and two nobody could name.
func (m Model) findStaged(word string) (int, string) {
	for i, a := range m.attachments {
		if a.Handle != "" && strings.EqualFold(a.Handle, word) {
			return i, ""
		}
	}
	var hits []int
	for i, a := range m.attachments {
		if strings.EqualFold(a.Name, word) {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 0:
		return -1, word + " is not attached — " + describeAllStaged(m.attachments)
	case 1:
		return hits[0], ""
	}
	handles := make([]string, len(hits))
	for i, at := range hits {
		handles[i] = m.attachments[at].Handle
	}
	return -1, fmt.Sprintf("%s is the name of %d attachments — say which: %s",
		word, len(hits), strings.Join(handles, ", "))
}

// pasteFold is one attachment as the transcript keeps it after the send: the
// row the tray draws for it, and — for text — the lines behind it
// (docs/interface/surfaces.md#the-input-frame).
//
// The attachment is kept on the row rather than left in the staging area,
// because the staging area is emptied by the send and the row outlives it by
// the whole session. It is the same bytes the request carried, which is what
// makes the row an account of what was sent rather than of what is staged,
// and what a recall stages again under the handle it rode under — the same
// bytes are the same attachment (recall.go) — and what a picture's row
// opens onto the card.
type pasteFold struct {
	att   provider.Attachment
	label string
	// token is the fold the attachment left in the sentence, spelled from
	// its handle and its figure, or "" for one with no handle. Whether the
	// sentence holds it decides nothing about the row: a picture staged with
	// `/paste <path>` put no fold in the words and has a row like the rest.
	token  string
	figure string
	tokens int64
	// pages is a document's page count, read once at the send rather than
	// on every frame, and zero where it cannot be read.
	pages int
	// from is where a picture came from — `from the clipboard`, `from
	// ~/Desktop` — as the door that staged it knew, or "" where nothing
	// said: a reopened session has the bytes and the name of what it sent,
	// and never the folder it was in.
	from string
	// last is the tray's last row, which the message's rule is drawn under.
	last bool
	// body is a text attachment's lines, and empty for a picture or a
	// document, which have no body the transcript can open in place.
	body []string
}

// picture reports whether the row stands for a picture, which it opens onto
// the preview card rather than in place.
func (p pasteFold) picture() bool {
	return p.att.Kind == provider.AttachmentImage
}

// sentEntries is the transcript rows one sent message leaves: its words, and
// a row under them for every attachment it carried, in the order they rode.
//
// Every attachment has a row, whether its fold is in the words or not. The
// fold in the sentence says where the reader put it; the row is the way back
// to it, and an attachment staged with `/paste <path>` — which put no fold in
// the words — or one whose fold the reader deleted is no less sent, and no
// less something the reader may want to look at again.
func (m Model) sentEntries(text string, atts []provider.Attachment) []entry {
	out := []entry{{kind: entryUser, text: text, trayed: len(atts) > 0}}
	for _, a := range atts {
		f := &pasteFold{att: a, label: a.Handle, figure: a.Figure()}
		if p, ok := pasteOf(a); ok {
			f.token = p.token
		}
		switch a.Kind {
		case provider.AttachmentText:
			f.tokens = agent.EstimateBytesTokens(a.Data)
			f.body = strings.Split(strings.TrimSuffix(string(a.Data), "\n"), "\n")
		case provider.AttachmentImage:
			f.from = m.staged.from[a.Handle]
		case provider.AttachmentDocument:
			f.pages = attachment.PageCount(a.Data)
		}
		out = append(out, entry{kind: entryTray, fold: f})
	}
	if n := len(out); n > 1 {
		out[n-1].fold.last = true
	}
	return out
}

// appendSent puts one sent message on the transcript, its tray with it.
func (m *Model) appendSent(text string, atts []provider.Attachment) {
	for _, e := range m.sentEntries(text, atts) {
		m.appendEntry(e)
	}
}

// trayOpens reports whether a tray row holds anything a press can open — a
// text body in place, or a picture onto its card. A document or a recording
// has nothing: shhh does not render one, so there is nothing a card could
// say that the row has not, and the row is no stop for reading mode and no
// target for a click.
func trayOpens(e entry) bool {
	return e.kind == entryTray && e.fold != nil && (len(e.fold.body) > 0 || e.fold.picture())
}

// trayPicture is the picture a tray row opens onto the card. Every press on
// the row opens it: the row has no open state of its own, so there is never
// a second press that only closes something.
func trayPicture(e entry) (provider.Attachment, bool) {
	if e.kind == entryPicture && e.picture != nil {
		// A picture a call returned opens through the same door
		// (card.go).
		return *e.picture, true
	}
	if e.kind != entryTray || e.fold == nil || !e.fold.picture() {
		return provider.Attachment{}, false
	}
	return e.fold.att, true
}

// openTrayPicture opens the picture the tray row at idx names. Opened from
// reading mode, the card's way out is back to the row; from the draft, back
// to the draft.
func (m Model) openTrayPicture(idx int) (tea.Model, tea.Cmd) {
	es := *m.entries()
	if idx < 0 || idx >= len(es) {
		return m, nil
	}
	a, ok := trayPicture(es[idx])
	if !ok {
		return m, nil
	}
	turn := sentTurn(es, idx)
	if es[idx].kind == entryPicture {
		// A call returned it; nobody sent it with a turn.
		turn = 0
	}
	return m.openSent(a, turn, m.state == stateFocus)
}

// openSent opens an attachment that has already been sent on the card the
// staging area's chips open, titled with the turn that carried it. The card
// leaves out its drop: there is nothing staged to take back.
func (m Model) openSent(a provider.Attachment, turn int, fromRow bool) (tea.Model, tea.Cmd) {
	next, cmd := m.openPreview(a)
	nm, ok := next.(Model)
	if !ok || nm.state != statePreview || nm.preview == nil {
		return next, cmd
	}
	if turn > 0 {
		nm.preview.Sent = fmt.Sprintf("sent with turn %d", turn)
	}
	nm.staged.row = fromRow
	return nm, cmd
}

// sentTurn is the turn the row at idx was sent with: the reader's rows up to
// it, counted the way the compaction receipt numbers turns (turnsIn), so the
// card and the receipt cannot disagree about which turn is meant.
func sentTurn(es []entry, idx int) int {
	n := 0
	for i := 0; i <= idx && i < len(es); i++ {
		if es[i].kind == entryUser {
			n++
		}
	}
	return n
}

// trayRowFor is the row's facts in the order a narrowing pane gives them up:
// a picture's source before its dimensions, a paste's tokens before its
// lines. The handle and the size are never given up.
func trayRowFor(f *pasteFold) components.TrayRow {
	a := f.att
	r := components.TrayRow{Kind: chipKind(a.Kind), Handle: a.Handle, Name: a.Name,
		Size: attachment.HumanSize(len(a.Data))}
	switch a.Kind {
	case provider.AttachmentImage:
		// A picture that will not decode has its size as its figure, which
		// the row already prints at the right.
		if f.figure != r.Size {
			r.Facts = append(r.Facts, f.figure)
		}
		if f.from != "" {
			r.Facts = append(r.Facts, f.from)
		}
	case provider.AttachmentText:
		r.Facts = append(r.Facts, f.figure, components.FormatCount(f.tokens)+" tokens")
	case provider.AttachmentDocument:
		if f.pages > 0 {
			r.Facts = append(r.Facts, plural(f.pages, "page"))
		}
	}
	return r
}

// trayBlock is one tray row as the transcript draws it, and — for text the
// reader has opened — the lines under it on the same band, bounded the way a
// tool's output is, with the bound counting what it held back. The paste is
// in the context window; it does not have to be in the scrollback as well,
// and enter on the row again gives the whole of it back on its own screen
// (outputview.go). The last row carries the rule that closes the message.
func trayBlock(e entry, width int, sel rowSel) string {
	f := e.fold
	row := trayRowFor(f)
	row.Lit = sel != rowUnselected
	out := row.View(width) + "\n"
	if e.expanded && len(f.body) > 0 {
		body := f.body
		if len(body) > maxToolResultLines {
			body = body[:maxToolResultLines]
		}
		for _, line := range body {
			out += components.TrayLine(line, false, width) + "\n"
		}
		if rest := len(f.body) - len(body); rest > 0 {
			// The one key the tray prints, and it is in the hint grey: the
			// draft holds enter while the reader looks at this, so a key in
			// the key colour would be an offer nothing accepts
			// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
			out += components.TrayLine(fmt.Sprintf("… %d more · %s again for the rest",
				rest, keys.Bracket(keys.Reading.Expand)), true, width) + "\n"
		}
	}
	if f.last {
		out += promptRule(width) + "\n"
	}
	return out
}

// trayOverflows reports whether a tray row's text held back more than the
// opened window shows, which is what makes the depth past it an offer rather
// than a press that changes nothing.
func trayOverflows(e entry) bool {
	return e.fold != nil && len(e.fold.body) > maxToolResultLines
}

// pasteOutputView is the whole of a tray row's text on its own screen — the
// third depth, where the bound stops applying.
func pasteOutputView(e entry) *components.OutputView {
	return &components.OutputView{Title: e.fold.label, Lines: e.fold.body}
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
			Kind:   chipKind(a.Kind),
			Handle: a.Handle,
			Name:   a.Name,
			Size:   attachment.HumanSize(len(a.Data)),
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
// is staged, `drop <handle>` drops one chip, `show <handle>` opens one as a
// picture — either also takes a name — and anything else is a path.
func (m Model) runPaste(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) == 1 {
		return m, readClipboardCmd(false)
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
	return m, attachFileCmd(m.inWorkspace(attachment.Expand(arg)), false)
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

// dropAttachment takes one staged attachment back out by handle or name —
// the per-chip half of what `clear` does to the whole strip.
//
// A chip carries no key of its own: it sits above a live draft, so by name
// the handle printed on it is what is typed, and the completion menu offers
// the staged handles so none is typed from memory. The other doors — the
// strip's key in reading mode, the card's — go through dropStagedAt too
// (stagedstrip.go). A word that names
// nothing staged is said out loud with what is, for the same reason a
// refused attachment is: a drop that quietly did nothing is a message that
// goes out carrying the file you meant to remove.
func (m Model) dropAttachment(name string) (tea.Model, tea.Cmd) {
	if len(m.attachments) == 0 {
		return m.surfaceNotice("nothing is attached")
	}
	if name == "" {
		return m.surfaceNotice("/paste drop needs a handle — " + describeAllStaged(m.attachments))
	}
	i, refusal := m.findStaged(name)
	if i < 0 {
		return m.surfaceNotice(refusal)
	}
	return m.surfaceNotice(m.dropStagedAt(i))
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
			Prompt: fmt.Sprintf("Drop %s?", describeStaged(a)),
		}
	default:
		opts := make([]components.SelectOption, len(m.attachments))
		for i, a := range m.attachments {
			// The handle leads, as it does on the chip: two rows reading
			// clipboard.png are told apart by nothing else.
			label := a.Name
			if a.Handle != "" {
				label = a.Handle + " · " + a.Name
			}
			opts[i] = components.SelectOption{Label: label, Meta: attachment.HumanSize(len(a.Data))}
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
			dropped = append(dropped, describeStaged(a))
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
