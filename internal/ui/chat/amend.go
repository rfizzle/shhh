package chat

// Amending a command at the card.
//
// The alternative to this key is denying, explaining the flag in prose and
// waiting a round for the model to propose the line back — three rounds of
// conversation to add `--runInBand`. So the card offers to run the command as
// the reader would have written it, and everything the card said about the
// original is read again against the line that will actually run
// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).

import (
	"encoding/json"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// commandEdit is the command open in a field under the card: the line the
// call carried, so esc can put it back and the row can say what ran in place
// of what, and the field it is being rewritten in.
//
// The field is bubbles' own one-line input rather than a component of its
// own, for the reason the note field beside it is (components/input.go):
// what is shared is how a field is built and repainted, not what it is.
type commandEdit struct {
	// original is the line the model asked about, which is what the card
	// goes back to when the field is abandoned. It survives a second
	// amendment: what the model asked for does not change because the reader
	// changed their mind twice.
	original string
	// refused is the rule that turned the last confirm away, drawn under the
	// field until the reader answers it.
	refused string
	field   textinput.Model
}

// drawn is the field as the card will draw it: sized to the room the card
// leaves it and repainted from the palette as it stands now (input.go).
//
// Both the render and the cursor go through here rather than one of them
// reading the stored field, for the reason the note field's copy does: the
// field's own caret is clamped to its width, so asking an unsized copy where
// the caret is puts it past the card's right edge the moment the line
// outgrows the row.
func (e commandEdit) drawn(width int) textinput.Model {
	field := e.field
	field.SetWidth(components.FieldWidth(width))
	components.StyleTextInput(&field)
	return field
}

// amendOffer is the key the command card advertises beside its decision run,
// and nothing at all where there is no command to amend.
//
// It is an assistant's command and not a `/run` the reader typed: a line they
// wrote a moment ago is one they can retype, and there is no call waiting to
// be told what ran instead. The card is the exec card alone — amending a diff
// is a different problem, and a denial that says why covers most of it
// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).
//
// And it is one line. The field is a one-line input, which joins the lines it
// is handed with spaces rather than refusing them — a heredoc opened in it
// would come back as a single line that runs something nobody wrote, and the
// reader would have no way to see that it had happened. A key that cannot
// hold what it opened is not an offer.
func amendOffer(req *approvalRequest) []components.KeyOffer {
	if req == nil || req.kind != approvalExec || req.command == "" {
		return nil
	}
	if strings.Contains(req.command, "\n") {
		return nil
	}
	return []components.KeyOffer{{
		Key: keys.Bracket(keys.Decision.Amend), Label: "edit the command",
	}}
}

// amendKey answers the card's amend key: the command opens in a field, and
// nothing is decided by opening it. handled is false for every key this is
// not, and for a card that took the keyboard by arriving — that card claims
// its answers and nothing else, and this letter is the reader's sentence
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) amendKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if !keys.Match(msg, keys.Decision.Amend) || m.heldOnArrival {
		return m, nil, false
	}
	// The key answers exactly where the card offered it and nowhere else, so
	// the letter stays the reader's on every card that made no offer.
	if len(amendOffer(m.pendingApproval)) == 0 {
		return m, nil, false
	}
	req := m.pendingApproval
	field := components.NewTextInput()
	field.Prompt = ""
	// The terminal's own cursor rather than a painted one: this session
	// places a real cursor wherever it is being typed into (confirmCursor),
	// and a field painting a second one would draw two.
	field.SetVirtualCursor(false)
	field.SetValue(m.pendingRun)
	field.CursorEnd()
	cmd := field.Focus()
	original := req.command
	if req.amendedFrom != "" {
		original = req.amendedFrom
	}
	m.commandEdit = &commandEdit{original: original, field: field}
	m.syncViewport()
	return m, cmd, true
}

// updateCommandEdit routes a key while the field holds the keyboard. Two keys
// are the whole of what it answers and every other key is text — the digits,
// the card's own letters and its scroll chords included — because a surface
// being typed into keeps every letter as text, the way the note field beside
// it and the selector's query row already do
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) updateCommandEdit(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	open := *m.commandEdit
	switch {
	case keys.Match(msg, keys.Select.Cancel):
		// The original comes back and the decision is exactly where it was.
		// The line the reader was writing goes with the field: an edit
		// nobody confirmed is not a command, and keeping it would put a
		// half-written line in front of the next press of the key.
		m.commandEdit = nil
		m.syncViewport()
		return m, nil
	case keys.Match(msg, keys.Select.Take):
		return m.confirmCommandEdit(strings.TrimSpace(open.field.Value()))
	}
	// The field is replaced rather than written through the pointer: the
	// model is a value every update hands back a copy of, and a shared field
	// would put this keystroke into the copy the last frame was drawn from.
	// The refusal is cleared as the key arrives, so it stands for exactly as
	// long as the reader has not answered it.
	open.refused = ""
	var cmd tea.Cmd
	open.field, cmd = open.field.Update(msg)
	m.commandEdit = &open
	m.syncViewport()
	return m, cmd
}

// confirmCommandEdit is enter in the field: the line is read again from the
// top, and what happens next is whatever that reading says.
//
// Three outcomes, and they are the queue's own three. A rule refuses it, and
// the refusal is the amended line's — the card keeps the field, with the line
// still in it and the rule named, because the reader is still holding the
// decision the card was drawn for. A heavier line draws the heavier card: an
// amendment that introduces a risk is a card nobody has read yet, and running
// it on the strength of a keystroke aimed at the old one would be the card
// lying about what it was answered for. Anything else runs, which is what the
// key is for.
func (m Model) confirmCommandEdit(line string) (tea.Model, tea.Cmd) {
	open := *m.commandEdit
	req := m.pendingApproval
	if req == nil {
		m.commandEdit = nil
		m.syncViewport()
		return m, nil
	}
	// An empty field is not a command. It is refused where a rule's refusal
	// is refused rather than treated as a cancel, because a reader who
	// cleared the line and pressed enter meant to run something.
	if line == "" {
		open.refused = "a command cannot be empty"
		m.commandEdit = &open
		m.syncViewport()
		return m, nil
	}
	// The line as it already stood is not an amendment: the reader confirmed
	// what the card was asking about, which is the plain allow they could
	// have pressed. Reading it as one would put a `was` row and a sentence
	// to the model on a decision where nothing changed.
	if line == m.pendingRun {
		m.commandEdit = nil
		return m.approvePending("")
	}

	// The card's own reading of the new line, resolved before anything is
	// decided on it: the deny list, the working scope and the blast radius
	// all answer about the line that will run rather than the one that was
	// proposed. The old reading is kept beside it, because whether the
	// reader has to be asked again is a comparison of the two (asksAgain).
	was, wasReach := m.pendingBlast, m.pendingScope
	amended := *req
	amended.command = line
	amended.summary = firstLine(line)
	amended.dryCommand = dryRunForm(line)
	if amended.amendedFrom == "" {
		amended.amendedFrom = open.original
	}
	// Nothing that answered the original answers this. autoRule is what a
	// mode, a grant or the batch put on the request when it waved the call
	// through, and the line it waved through is not this one.
	amended.autoRule, amended.autoCost = "", 0
	reach := m.scopeReachFor(&amended)

	if why := m.amendRefusal(&amended, reach); why != "" {
		open.refused = why
		m.commandEdit = &open
		m.syncViewport()
		return m, nil
	}

	*req = amended
	m.pendingRun = line
	m.pendingScope = reach
	// pendingScope is read by the radius resolver, so it is set first.
	m.pendingBlast = m.resolveRadius(req)
	// The queue behind the card was marked against the shape of the command
	// the model proposed, and the reader has just made this one a different
	// shape. So the marks are taken again from the amended line rather than
	// carried or blanked: a line that is no longer batchable at all — a
	// safety-flagged one — loses them by the same rule that would have
	// refused them in the first place (batchCategory).
	m.pendingQueue, m.pendingBatch = m.resolveQueue(req)
	m.commandEdit = nil
	m.cardScroll, m.cardPan = 0, 0

	if asksAgain(was, m.pendingBlast, wasReach, reach) {
		// The reader is answering a card they have not read. It is drawn for
		// them, with the line they wrote on it, and the decision is waiting
		// exactly where it was.
		m.recordDecision(observe.DecisionAsk, observe.ReasonUserAmended)
		m.syncViewport()
		return m, nil
	}
	m.recordDecision(observe.DecisionAllow, observe.ReasonUserAmended)
	return m.executeRun()
}

// amendRefusal is what refuses an amended line, in words the card can print,
// or "" where nothing does.
//
// It asks the two questions the queue asks before a card is ever drawn, and
// only those two: the deny list, which is the reader's own standing answer,
// and the working scope, which is the one refusal no grant can lift. A mode,
// a session grant and the classifier are all ways of *allowing* without
// asking, and there is nobody left to spare here — the reader is at the card,
// answering it (docs/capabilities/approvals-and-safety.md#a-deny-list-is-answered-before-anything-can-allow).
func (m Model) amendRefusal(req *approvalRequest, reach scopeReach) string {
	if m.deniedByRule(req) {
		_, _, why := m.ruleDenial(req)
		return why
	}
	if reach.class == scope.Refused {
		return "outside the working scope — " + reach.reason
	}
	// Containment is deliberately not asked again. It answers whether this
	// session runs the assistant's commands at all, which no line the reader
	// writes can change — a card is on the screen, so it already said yes
	// (advanceApprovalQueue).
	return ""
}

// asksAgain reports whether the amended line's reading asks more of the
// reader than the one they were answering, and so has to be put to them as a
// card rather than run on the keystroke that confirmed the field.
//
// Three things count. A higher severity and a risk where the old card
// carried none are the two that change what the card offers — a flagged card
// offers no blanket approval at all. The third is what the line reaches
// outside the working scope, which is here because it is the one of the
// three that does not always show up in the other two: reaching a directory
// only raises the severity from below medium
// (commandRadiusIn), so an amendment that is already medium for some
// unrelated reason can reach a directory nobody has been asked about and
// look no heavier at all — and approving it grants that directory for the
// session (applyScopeGrant). A grant made by a keystroke aimed at a card
// that never named the directory is exactly the inheritance this key must
// not allow.
func asksAgain(was, now blastRadius, wasReach, nowReach scopeReach) bool {
	if now.severity != was.severity {
		return now.severity > was.severity
	}
	if len(was.risks) == 0 && len(now.risks) > 0 {
		return true
	}
	if nowReach.class > wasReach.class {
		return true
	}
	for _, dir := range nowReach.dirs {
		if !slices.Contains(wasReach.dirs, dir) {
			return true
		}
	}
	return false
}

// execArguments is a command line as the exec tool's own arguments, for the
// readers that key on arguments rather than on the request: the repeat
// detector is the one, and an amended line has to reach it as itself.
//
// It is built rather than taken from the call because the call's arguments
// are the model's and are deliberately never rewritten — the model asked for
// what it asked for, and the account of that must not be edited under it.
func execArguments(command string) string {
	args, err := json.Marshal(struct {
		Command string `json:"command"`
	}{command})
	if err != nil {
		// A string always marshals. If that ever stops being true, the
		// detector seeing nothing is better than it seeing a broken key.
		return "{}"
	}
	return string(args)
}

// amendedNotice is what the model is told when the line that ran is not the
// line it asked for. It leads the result rather than joining it, because a
// model handed a bare success reads it as a success of the command it
// proposed and carries `npm test` into the next round when what ran was
// `npm test -- --runInBand`.
//
// One sentence, and both lines whole in it. Which is which cannot be left to
// the model to work out from the result of a command it did not write, and
// neither line is shortened: the offer is made only on a command that is one
// line, so there is nothing here a first-line reading would save.
func amendedNotice(was, ran string) string {
	return "The user edited this command before it ran: " + ran +
		" ran in place of " + was + "."
}
