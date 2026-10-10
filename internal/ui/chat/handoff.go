package chat

// /handoff: what a person leaving mid-task writes down for the sitting that
// picks the work up — what was done, what is open, what was decided, the
// files touched and the item in flight. The session writes it on the
// reading's flow, seeded with the person's note; the person edits it or
// accepts it on a card; and it is kept on the conversation's slot only on
// the yes. The next sitting opens on it: the start screen's resume offer
// names its first line, and a resume puts it in front of everything else
// (docs/capabilities/sessions-and-memory.md#a-session-can-leave-a-handoff).
//
// It is not memory. A memory is a durable fact every later session reads; a
// handoff is one session's note to its own next sitting, asked for by name
// and replaced by the next one (docs/product.md#what-shhh-will-not-add).
//
// The card is the toolchain draft card's shape (toolchaindraft.go): the
// writing is a background command, a result arriving after the session moved
// on is dropped by the pointer it was started under, [e] hands the draft to
// the editor over a temporary file, and esc leaves with nothing kept.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// handoffCommandName is the command, named once so the register, the card's
// notes and the quit's offer cannot drift apart.
const handoffCommandName = "/handoff"

// handoffState is the command's state on the session.
type handoffState struct {
	// writing is the writing in flight or the draft waiting on its card.
	writing *handoffWriting
	// quitAfter marks a writing the quit's offer started: the card's yes
	// keeps the handoff and then quits.
	quitAfter bool
	// offered marks the quit's confirm as the offer of a handoff, whose no
	// quits rather than staying.
	offered bool
	// kept is the handoff this sitting kept, which every later save of the
	// conversation carries to whatever slot it is written to.
	kept string
}

// handoffWriting is one writing. The result message carries the pointer it
// was started under, so an answer arriving after the session boundary
// dropped it is dropped too.
type handoffWriting struct {
	busy   bool
	cancel context.CancelFunc
	draft  string
}

// handoffDraftMsg is a writing's answer.
type handoffDraftMsg struct {
	flow    *handoffWriting
	verdict agent.HandoffVerdict
}

// handoffWired reports a session that can write a handoff and keep it.
func (m Model) handoffWired() bool {
	return m.wiring.Handoff.Enabled() && m.wiring.DB != nil
}

// slotHandoff is the handoff the conversation on a slot holds: the one this
// sitting kept, or failing that the one the slot carried in from the last
// sitting. Every copy of the conversation takes it from here, so a branch or
// a named copy never lacks the handoff its original has.
func (m Model) slotHandoff(slot string) string {
	if m.handoff.kept != "" {
		return m.handoff.kept
	}
	if m.wiring.DB == nil {
		return ""
	}
	held, _ := m.wiring.DB.ChatHandoff(slot)
	return held
}

// handoffNotSaved says a handoff could not be written to a slot a save
// reached. The save itself stands; only the handoff is missing, and it is
// said where the reader is, as a failed save is (model.go).
func (m *Model) handoffNotSaved(slot string, err error) {
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
		"the handoff could not be written to %q: %v. The conversation is saved, but the next sitting may not open on it — %s writes it again",
		slot, err, handoffCommandName)})
	m.syncViewport()
}

// handoffCommand is /handoff [note]: the card when a draft waits on it and no
// new note was given, the writing otherwise.
func (m Model) handoffCommand(note string) (tea.Model, tea.Cmd) {
	note = strings.TrimSpace(note)
	switch {
	case m.wiring.DB == nil:
		return m.systemNotice("nowhere to keep a handoff: this session has no store")
	case !m.wiring.Handoff.Enabled():
		return m.systemNotice("no model is configured to write a handoff")
	}
	if w := m.handoff.writing; w != nil {
		if w.busy {
			return m.systemNotice("still writing the handoff — the card opens when it is done")
		}
		if note == "" && w.draft != "" {
			return m.openHandoff()
		}
	}
	req := agent.HandoffRequestFrom(note, m.compactSummary, agent.StripResumeContext(m.agent.Messages()))
	if req.Empty() {
		m.handoff.quitAfter = false
		return m.systemNotice("nothing to hand off yet: the session has no conversation")
	}
	if m.changes != nil {
		req.Files = m.changes.Paths()
	}
	if r := m.todo.runner; r.state != nil && r.item.Slug != "" {
		req.Item = r.item.Slug
		if r.item.Title != "" {
			req.Item += " — " + r.item.Title
		}
	}
	m.dropHandoffWriting()
	ctx, cancel := context.WithCancel(context.Background())
	w := &handoffWriting{busy: true, cancel: cancel}
	m.handoff.writing = w
	writer := m.wiring.Handoff
	next, notice := m.systemNotice("writing a handoff from the conversation — the card opens when it is done")
	return next, tea.Batch(notice, func() tea.Msg {
		defer cancel()
		return handoffDraftMsg{flow: w, verdict: writer.Write(ctx, req)}
	})
}

// finishHandoff takes a writing's answer. The card opens only where the
// screen is free for it, for the reason the toolchain draft's does.
func (m Model) finishHandoff(msg handoffDraftMsg) (tea.Model, tea.Cmd) {
	w := m.handoff.writing
	if w == nil || w != msg.flow {
		return m, nil
	}
	w.busy = false
	if msg.verdict.Failed {
		m.handoff.writing = nil
		note := "could not write a handoff: " + msg.verdict.Err
		if m.handoff.quitAfter {
			m.handoff.quitAfter = false
			note += ". Nothing was kept, and the session is still open"
		}
		return m.systemNotice(note)
	}
	w.draft = msg.verdict.Handoff.Text()
	if !m.screenIsFree() || m.interruptShowing() {
		return m.systemNotice("the handoff is ready — " + handoffCommandName + " opens it")
	}
	return m.openHandoff()
}

// openHandoff puts the card up. It is a takeover: the reader asked for it.
func (m Model) openHandoff() (tea.Model, tea.Cmd) {
	m.enterSurface(stateHandoff)
	m.syncViewport()
	return m, nil
}

// dropHandoffWriting retires a writing in flight or waiting.
func (m *Model) dropHandoffWriting() {
	w := m.handoff.writing
	if w == nil {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	m.handoff.writing = nil
}

// dropHandoff is the session boundary's: the writing, the offer and what this
// sitting kept all belong to the conversation being left.
func (m *Model) dropHandoff() {
	m.dropHandoffWriting()
	if m.state == stateHandoff {
		m.leaveSurface()
	}
	m.handoff = handoffState{}
}

// handoffCard is the card the draft is answered on: the first line as what
// is kept, each labelled line under it, wrapped rather than clipped, because
// the card is where the handoff is read before it is kept.
func (m Model) handoffCard() *components.ApprovalCard {
	card := &components.ApprovalCard{
		Variant:  components.ApprovalGeneric,
		Title:    "Approve handoff",
		ActGlyph: "✎",
		Act:      "keep this handoff on " + m.sessionName,
		Summary:  "the next sitting of this conversation opens on it",
		Answer:   "keep it",
		Decline:  "nothing kept",
		Return:   "leave — nothing kept, and the draft waits",
		ExtraHints: []components.KeyOffer{
			{Key: keys.Bracket(keys.Decision.Revise), Label: "edit first"},
		},
		MaxLines: m.planPanelBound(),
		KeyList:  true,
	}
	w := m.handoff.writing
	if w == nil || w.draft == "" {
		return card
	}
	// The value column is what the card's frame and the label column leave.
	room := max(m.contentWidth()-4-10, 12)
	add := func(label, value string) {
		for i, line := range strings.Split(ansi.Wordwrap(value, room, ""), "\n") {
			if i > 0 {
				label = ""
			}
			card.Fields = append(card.Fields, components.CardField{Label: label, Value: line})
		}
	}
	for i, line := range strings.Split(w.draft, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 {
			add("summary", line)
			continue
		}
		label, value, ok := strings.Cut(line, ": ")
		if !ok || strings.Contains(label, " ") {
			label, value = "", line
		}
		add(label, value)
	}
	return card
}

// answerHandoff routes the card's keys: esc leaves with the draft waiting and
// nothing kept, [n] drops it, [e] hands it to the editor, and only the yes
// writes anything.
func (m *Model) answerHandoff(msg tea.KeyPressMsg) (bool, overlayAction) {
	w := m.handoff.writing
	switch {
	case keys.Match(msg, keys.Select.Cancel):
		m.handoff.quitAfter = false
		return true, overlayAction{close: true, note: "nothing kept; " + handoffCommandName + " opens the draft again"}
	case keys.Match(msg, keys.Decision.Refuse):
		m.handoff.quitAfter = false
		m.handoff.writing = nil
		return true, overlayAction{close: true, note: "nothing kept; " + handoffCommandName + " writes another"}
	case keys.Match(msg, keys.Decision.Revise):
		return true, m.editHandoff()
	case keys.Match(msg, keys.Decision.Accept):
		if w == nil || w.draft == "" {
			return true, overlayAction{close: true}
		}
		return true, m.keepHandoff(w.draft)
	}
	return false, overlayAction{}
}

// keepHandoff writes the draft to the slot. A slot the conversation has not
// been saved to yet takes it with the save this starts, which carries what
// this sitting kept.
func (m *Model) keepHandoff(text string) overlayAction {
	m.handoff.writing = nil
	m.handoff.kept = text
	act := overlayAction{close: true, note: "handoff kept on " + m.sessionName + " — the next sitting of this conversation opens on it"}
	var missing storage.ChatNotFoundError
	if err := m.wiring.DB.SetChatHandoff(m.sessionName, text); errors.As(err, &missing) {
		act.run = m.autosaveCmd()
	} else if err != nil {
		m.handoff.kept = ""
		m.handoff.quitAfter = false
		return overlayAction{close: true, note: "could not keep the handoff: " + err.Error()}
	}
	if m.handoff.quitAfter {
		m.handoff.quitAfter = false
		act.run = tea.Sequence(act.run, m.quitNow())
	}
	return act
}

// handoffEditorDoneMsg is the editor's exit from a draft.
type handoffEditorDoneMsg struct {
	path string
	err  error
}

// editHandoff hands the draft to the editor over a temporary file, the way
// the toolchain draft is: nothing is kept until the card's yes.
func (m *Model) editHandoff() overlayAction {
	w := m.handoff.writing
	if w == nil || w.draft == "" {
		return overlayAction{close: true}
	}
	if m.working() || m.frameWorking() {
		return overlayAction{note: "not while the turn is running — the editor takes the terminal with it. The draft is still on the card"}
	}
	f, err := os.CreateTemp("", "shhh-handoff-*.txt")
	if err == nil {
		_, err = f.WriteString(w.draft + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}
	if err != nil {
		return overlayAction{note: "could not write the draft out — " + err.Error() + ". The draft is still on the card"}
	}
	path := f.Name()
	argv := editorArgv(editorCommand(), path, 1, 1)
	proc := exec.Command(argv[0], argv[1:]...)
	return overlayAction{close: true, run: tea.ExecProcess(proc, func(err error) tea.Msg {
		return handoffEditorDoneMsg{path: path, err: err}
	})}
}

// handoffEditorFinished takes the edit back onto the card. An edit that
// emptied the file leaves the draft as it was: an empty handoff is not one.
func (m Model) handoffEditorFinished(msg handoffEditorDoneMsg) (tea.Model, tea.Cmd) {
	defer func() { _ = os.Remove(msg.path) }()
	w := m.handoff.writing
	if w == nil || w.draft == "" {
		return m, nil
	}
	back := func(note string) (tea.Model, tea.Cmd) {
		m.enterSurface(stateHandoff)
		m.syncViewport()
		if note == "" {
			return m, nil
		}
		return m.systemNotice(note)
	}
	if msg.err != nil {
		return back("The editor exited with an error, so the draft is as it was — " + msg.err.Error())
	}
	content, err := os.ReadFile(msg.path)
	if err != nil {
		return back("Could not read the draft back, so it is as it was — " + err.Error())
	}
	text := strings.TrimSpace(string(content))
	if text == "" {
		return back("The edit left nothing, so the draft is as it was")
	}
	w.draft = text
	return back("")
}

// handoffLines renders the card, one row per line.
func (m Model) handoffLines() []string {
	return strings.Split(m.handoffCard().View(m.contentWidth()), "\n")
}

// handoffOwed reports a quit that should offer a handoff first: a session
// that can write one, has kept none this sitting, and has a turn whose
// changes are not committed — the work a next sitting would otherwise have
// to reconstruct from the tree. A close a resume restored is the last
// sitting's, which either left a handoff or was already asked.
func (m Model) handoffOwed() bool {
	if !m.handoffWired() || m.handoff.kept != "" {
		return false
	}
	prev := int64(0)
	for _, e := range m.transcript {
		if e.kind != entryTurnClose || e.close == nil {
			continue
		}
		if !e.restored && e.close.Commit == nil {
			if e.close.Changes != nil || m.steeredTurnsWrote(prev, e.turn) {
				return true
			}
		}
		prev = e.turn
	}
	return false
}

// steeredTurnsWrote reports whether a turn between two closes changed files.
// A steer is a turn of its own and a run closes once, on its last turn, so
// what the turns before it wrote is on no close row of theirs; a close whose
// own turn wrote nothing still follows that work, and it is uncommitted
// because no close of theirs could have carried the commit.
func (m Model) steeredTurnsWrote(after, upTo int64) bool {
	for n := after + 1; n < upTo; n++ {
		if t, ok := m.changes.Turn(n); ok && t.Files() > 0 {
			return true
		}
	}
	return false
}

// openHandoffOffer is the quit's confirm with the offer in a sentence on it,
// not a card: the person asked to leave, and a card would be a second
// decision in their way. Its no quits, which is what they asked for; its yes
// writes the handoff and quits once it is kept.
func (m Model) openHandoffOffer() (tea.Model, tea.Cmd) {
	// Short enough that the answers still fit beside it at sixty columns: a
	// confirm clips its prompt and its keys together.
	m.quitAsk = &components.Confirm{Prompt: "Write a handoff first? Changes are uncommitted.", KeyList: true}
	m.quitAskYes = (*Model).acceptHandoffOffer
	m.handoff.offered = true
	m.enterSurface(stateQuitConfirm)
	m.syncViewport()
	return m, nil
}

// acceptHandoffOffer is the offer's yes: the writing, marked to quit once the
// card's yes has kept it.
func (m *Model) acceptHandoffOffer() tea.Cmd {
	m.handoff.offered = false
	m.handoff.quitAfter = true
	next, cmd := m.handoffCommand("")
	*m = next.(Model)
	return cmd
}
