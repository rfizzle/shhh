package chat

// The queue (docs/interface/surfaces.md#the-input-frame): every message the
// reader typed while a turn ran and has not been sent yet — steering, which
// joins the turn at its next round, and follow-ups, which wait for it to end
// — drawn as rows above the box, in the order they will go out. A line the
// session queued on the reader's behalf is a row too, marked with what it
// is: every count of what is waiting reads these rows, so a line left off
// them would be counted in one place and not the other.
//
// The keyboard moves into it on its own key, and there a message can be
// pulled back into the draft or cancelled. Either takes the message out of
// the steering or follow-up list delivery reads, which is the whole of why
// nothing is ever sent twice: a pulled-back message is a sentence in the
// draft again, and sending it queues it afresh at the end like any other.
// The turn keeps running under the queue, so a message can be delivered
// while the pointer is on it; the pointer names the message rather than a
// row for exactly that, and a key aimed at one that has gone says so rather
// than acting on whatever slid into its place.
//
// Nothing here reaches the model. A cancelled message never does, and an
// edited one does as it is sent.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// queueState is the queue's own state on the session: the last id a queued
// message was given, and the one the pointer is on while the keyboard is in
// the queue.
type queueState struct {
	seq int
	sel int
	// returned says where the message under the pointer went when it went
	// back rather than out: a turn that broke gives its steering back
	// (restoreSteering) — a typed message to the draft, a line another
	// session sent to its held card — and a key aimed at it then must not
	// say it was sent.
	returned queueReturn
}

// queueReturn is where a message the turn gave back went.
type queueReturn int

const (
	queueNotReturned queueReturn = iota
	queueToDraft
	queueToHeldCard
)

// next is the id for a message being queued now. Ids are never reused, so a
// message delivered from under the pointer cannot be mistaken for a later
// one that took its place.
func (q *queueState) next() int {
	q.seq++
	return q.seq
}

// queueRailRows bounds the rows the queue takes above the box. Past it the
// oldest are drawn, since they go next, and the rest are counted.
const queueRailRows = 3

// queueKind is the word a line the session queued for itself is listed
// under. A message typed into the draft has none and is steering or a
// follow-up by the list it waits in.
type queueKind string

const (
	// queuedSession is the session's own announcement: a secret named or
	// forgotten mid-turn (secrets.go).
	queuedSession queueKind = "session"
	// queuedSent is a line another session handed this one (inbound.go).
	queuedSent queueKind = "sent"
	// queuedSkill is a skill's content, activated while the turn ran
	// (skills.go).
	queuedSkill queueKind = "skill"
	// queuedPrompt is a server's prompt, rendered while the turn ran
	// (mcp.go).
	queuedPrompt queueKind = "prompt"
	// queuedNote is the sentence written on an approval card, which goes out
	// as a steer behind the call it was written about (approval.go).
	queuedNote queueKind = "note"
)

// queuedRow is one message the queue lists and which list it waits in.
type queuedRow struct {
	steeringItem
	followUp bool
}

// queuedRows is everything waiting, in delivery order: the steering lines,
// which go at the next round, then the follow-ups, which go once the turn
// ends. What the session queued for itself is listed among the steering it
// joins with, under its own kind.
func (m Model) queuedRows() []queuedRow {
	var rows []queuedRow
	for _, item := range m.steering {
		rows = append(rows, queuedRow{steeringItem: item})
	}
	for _, item := range m.followUps {
		rows = append(rows, queuedRow{steeringItem: item, followUp: true})
	}
	return rows
}

// queuedForTurnCount is how many rows wait for the turn's next round, which
// is what every rail's `queued for this turn` states.
func (m Model) queuedForTurnCount() int {
	n := 0
	for _, r := range m.queuedRows() {
		if !r.followUp {
			n++
		}
	}
	return n
}

// queuedMessages is the rows as the component draws them. A line the session
// queued is read and never pulled back — it was never the draft's — and an
// approval's note is not cancelled either, because the call it was written
// about has already been let through on it.
func queuedMessages(rows []queuedRow) []components.QueuedMessage {
	msgs := make([]components.QueuedMessage, 0, len(rows))
	for _, r := range rows {
		var handles []string
		for _, a := range r.atts {
			handles = append(handles, a.Handle)
		}
		msgs = append(msgs, components.QueuedMessage{
			FollowUp: r.followUp,
			Kind:     string(r.kind),
			Text:     r.text,
			Handles:  handles,
			ReadOnly: r.kind != "",
			Kept:     r.kind == queuedNote,
		})
	}
	return msgs
}

// queueRail is the queue above the box while the draft has the keyboard.
// Attached, the draft is a child's and this queue is the session's, so it is
// not drawn — the way the staged strip is not (attachments.go).
func (m Model) queueRail() []string {
	if m.attachedTo != "" {
		return nil
	}
	return components.QueueRows(queuedMessages(m.queuedRows()), m.contentWidth(), queueRailRows)
}

// takeQueued removes the message with this id from whichever list it waits
// in, and reports false when it is in neither — delivered, or never queued.
// The lists are rebuilt rather than cut in place, because a Model is copied
// on every message and a slice's array is shared by every copy.
func (m *Model) takeQueued(id int) (queuedRow, bool) {
	if id == 0 {
		return queuedRow{}, false
	}
	for i, item := range m.steering {
		if item.id == id {
			m.steering = append(append([]steeringItem(nil), m.steering[:i]...), m.steering[i+1:]...)
			return queuedRow{steeringItem: item}, true
		}
	}
	for i, item := range m.followUps {
		if item.id == id {
			m.followUps = append(append([]steeringItem(nil), m.followUps[:i]...), m.followUps[i+1:]...)
			if len(m.followUps) == 0 {
				m.followUpsHeld = false
			}
			return queuedRow{steeringItem: item, followUp: true}, true
		}
	}
	return queuedRow{}, false
}

// pullBack puts a queued message back in the draft with what rode with it
// back on the strip, and takes it out of the queue. A sentence already in the
// draft stays, on the line under it, the way a cancelled turn's steering
// comes back (stream.go).
func (m *Model) pullBack(id int) bool {
	row, ok := m.takeQueued(id)
	if !ok {
		return false
	}
	text := row.text
	if cur := m.input.Value(); strings.TrimSpace(cur) != "" {
		text += "\n" + cur
	}
	m.attachments = append(m.attachments, row.atts...)
	m.input.SetValue(text)
	m.input.MoveToEnd()
	return true
}

// openQueue answers the queue key: with anything queued, the keyboard moves
// into it with the pointer on the newest, which is the message most likely
// to have been sent in haste. It reports false where the key is not its to
// claim — the draft does not have the keyboard, it is a child's, or nothing
// is queued.
func (m Model) openQueue() (tea.Model, tea.Cmd, bool) {
	if !m.inputLive() || m.attachedTo != "" {
		return m, nil, false
	}
	rows := m.queuedRows()
	if len(rows) == 0 {
		return m, nil, false
	}
	m.queue.sel, m.queue.returned = rows[len(rows)-1].id, queueNotReturned
	m.enterSurface(stateQueue)
	m.syncViewport()
	return m, nil, true
}

// queueLines draws the queue with the keyboard in it.
func (m Model) queueLines() []string {
	rows := m.queuedRows()
	sel := -1
	for i, r := range rows {
		if r.id == m.queue.sel {
			sel = i
		}
	}
	card := components.QueueCard{
		Messages: queuedMessages(rows),
		Selected: sel,
		Held:     m.followUpsHeld && len(m.followUps) > 0,
	}
	return strings.Split(card.View(m.contentWidth()), "\n")
}

// queueAlreadySent is what a pull-back or a cancel says when the message it
// was aimed at went out first. The key lost the race and did nothing, and a
// message that has joined the conversation is taken back by rewinding to
// before it, which is the one way back a sent message has.
const queueAlreadySent = "already sent — that message reached the conversation before the key did; /rewind takes it back out"

// queueReturned is the same key aimed at a message the turn gave back when it
// broke: it is in the draft already, and was never sent.
const queueReturned = "not sent — the turn ended first and that message is back in the draft"

// queueReturnedToCard is that key aimed at a line another session sent: it
// went back to the card it waits on (inbound.go), and is named in that
// card's own words, since the card is where it is answered.
const queueReturnedToCard = "not sent — the turn ended first and that line is back on its held card, to pass it to the turn or drop it"

// queueGone is what a key aimed at a message that has left the queue says,
// by which way it left.
func (m Model) queueGone() string {
	switch m.queue.returned {
	case queueToDraft:
		return queueReturned
	case queueToHeldCard:
		return queueReturnedToCard
	}
	return queueAlreadySent
}

// answerQueue answers a key while the keyboard is in the queue.
func (m *Model) answerQueue(key tea.KeyPressMsg) (bool, overlayAction) {
	pressed := key.String()
	rows := m.queuedRows()
	at := -1
	for i, r := range rows {
		if r.id == m.queue.sel {
			at = i
		}
	}
	switch {
	case keys.Is(pressed, keys.Queue.Back):
		return true, overlayAction{close: true}
	case keys.Is(pressed, keys.Queue.Move):
		if len(rows) == 0 {
			return false, overlayAction{}
		}
		// A pointer whose message was sent from under it starts again at the
		// top rather than guessing which neighbour it meant.
		next := 0
		if at >= 0 {
			next = min(max(at+keys.Step(pressed, keys.Queue.Move), 0), len(rows)-1)
		}
		m.queue.sel, m.queue.returned = rows[next].id, queueNotReturned
		return false, overlayAction{}
	case keys.Is(pressed, keys.Queue.Edit):
		if at >= 0 && rows[at].kind != "" {
			return true, overlayAction{note: queueNotTheDrafts(rows[at].kind)}
		}
		if !m.pullBack(m.queue.sel) {
			return true, overlayAction{close: true, note: m.queueGone()}
		}
		return true, overlayAction{close: true}
	case keys.Is(pressed, keys.Queue.Cancel):
		if at >= 0 && rows[at].kind == queuedNote {
			return true, overlayAction{note: queueNotTheDrafts(queuedNote)}
		}
		row, ok := m.takeQueued(m.queue.sel)
		if !ok {
			return true, overlayAction{close: true, note: m.queueGone()}
		}
		m.appendEntry(entry{kind: entrySystem, notice: queueCancelNotice(row)})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		// The pointer goes to the message that took this one's place, or
		// the one before it at the end; with nothing left there is nothing
		// for the keyboard to be in.
		rest := m.queuedRows()
		if len(rest) == 0 {
			return true, overlayAction{close: true}
		}
		m.queue.sel = rest[min(at, len(rest)-1)].id
		m.syncViewport()
		return false, overlayAction{}
	}
	return false, overlayAction{}
}

// queueNotTheDrafts is what a key the row does not take says: a line the
// session queued was never typed into the draft, so there is nothing to pull
// back into it, and an approval's note went with the approval.
func queueNotTheDrafts(kind queueKind) string {
	if kind == queuedNote {
		return "kept — an approval's note goes with the call it let through, so it is neither pulled back nor cancelled"
	}
	return "not pulled back — a " + string(kind) + " line was never typed into the draft; " +
		keys.Bracket(keys.Queue.Cancel) + " cancels it"
}

// queueCancelNotice is the one row a cancel leaves: the message, what went
// with it, and that it went nowhere. A line the session queued is named by
// its kind beside its text, as its row was, since the text alone — a
// skill's opening tag, a secret's announcement — does not say what it was.
func queueCancelNotice(row queuedRow) *components.ActivityNotice {
	subject := firstLine(row.text)
	if row.kind != "" {
		subject = string(row.kind) + " · " + subject
	}
	for _, a := range row.atts {
		subject += " · " + a.Handle
	}
	return &components.ActivityNotice{Verb: "queued", Subject: subject, Outcome: "cancelled"}
}
