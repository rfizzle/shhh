package chat

// A line another session sent.
//
// Another shhh session on this machine can hand this one a sentence over the
// socket the host listens on — a sibling told the branch moved under it, most
// often. It is a steer from a fourth source (subagent.SteerFromSession): it
// joins a running turn at its next boundary, or starts one on an idle
// session, exactly as a sentence typed into the draft would. What it never is
// is the person at this keyboard. It joins in the session's own voice, framed
// by the wording that names who sent it, it goes nowhere near the slash
// commands, and it answers nothing: a card waiting when it arrives is waiting
// still. Where sessions.inbound holds it — the default under auto — it waits
// on a card of its own for the person to pass on or drop.
// See docs/capabilities/sessions-and-memory.md#a-session-can-hand-another-a-line.

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/rpc"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The three words sessions.inbound takes. Empty is hold under auto and
// accept otherwise (inboundPolicy).
const (
	InboundAccept = "accept"
	InboundHold   = "hold"
	InboundRefuse = "refuse"
)

// InboundLine is one line as the host's listener hands it over. Taken is
// answered once, with the word the sender is told, and is buffered by the
// host so answering it never waits on the sender.
type InboundLine struct {
	// From is the slot of the session that sent it, empty where no session
	// did — a script at a command line.
	From  string
	Text  string
	Taken chan<- string
}

// Inbound wires the listener in: the lines it hands over, and what
// sessions.inbound says to do with them.
type Inbound struct {
	Lines  <-chan InboundLine
	Policy string
}

// inboundState is the Model's one field for all of it.
type inboundState struct {
	lines  <-chan InboundLine
	policy string
	// held are the lines waiting on the card, oldest first; the card shows
	// the first.
	held []InboundLine
}

// WithInbound hands the session the lines its listener takes.
func (m Model) WithInbound(in Inbound) Model {
	m.inbound.lines = in.Lines
	m.inbound.policy = in.Policy
	return m
}

// inboundMsg is one line off the listener.
type inboundMsg struct{ line InboundLine }

// inboundOpenMsg asks again whether the held card can open, once a keyboard
// that was busy has had time to go quiet.
type inboundOpenMsg struct{}

// listenInbound waits for the next line. It is re-armed after every one, the
// way the sub-agent events are.
func listenInbound(ch <-chan InboundLine) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return inboundMsg{line: line}
	}
}

// inboundPolicy is what becomes of a line arriving now. The default follows
// the mode rather than being read once, because the mode moves under the
// session: auto is the one mode whose turn goes on to run commands nobody is
// asked about, so a line from elsewhere waits for a person there, and in
// every other mode each act it could lead to is still put to one.
func (m Model) inboundPolicy() string {
	switch p := strings.ToLower(strings.TrimSpace(m.inbound.policy)); p {
	case InboundAccept, InboundHold, InboundRefuse:
		return p
	}
	if !m.conversation && m.policy.mode == agent.ModeAuto {
		return InboundHold
	}
	return InboundAccept
}

// sentBy is how a steer row and the card name the sender.
func sentBy(from string) string {
	if from == "" {
		return "a command line"
	}
	return "session " + from
}

// takeInboundMsg answers one line off the listener and listens for the next.
func (m Model) takeInboundMsg(msg inboundMsg) (tea.Model, tea.Cmd) {
	next := listenInbound(m.inbound.lines)
	line := msg.line
	switch m.inboundPolicy() {
	case InboundRefuse:
		answerTaken(line, "")
		return m, next
	case InboundHold:
		answerTaken(line, rpc.TakenHeld)
		m.inbound.held = append(m.inbound.held, line)
		m.syncViewport()
		open := m.openHeldLine()
		return m, tea.Batch(next, open)
	}
	answerTaken(line, rpc.TakenDelivered)
	updated, cmd := m.passInbound(line)
	return updated, tea.Batch(next, cmd)
}

// answerTaken tells the sender what became of its line. The channel is the
// host's and buffered; a line handed over without one is a test's.
func answerTaken(line InboundLine, word string) {
	if line.Taken == nil {
		return
	}
	select {
	case line.Taken <- word:
	default:
	}
}

// passInbound hands a line to the turn as a steer is handed: queued for the
// next boundary of a turn in flight, which leaves any card that turn is
// waiting on exactly where it was, or the next instruction of an idle one.
//
// An idle session starts its turn the way a typed one does (openTurn), so
// what the turn may spend and what the record files it under are what they
// would have been for a sentence typed into the box; only the message is in
// the session's own voice rather than the reader's.
func (m Model) passInbound(line InboundLine) (tea.Model, tea.Cmd) {
	item := steeringItem{text: line.Text, sent: true, from: line.From}
	if m.turnInFlight() || m.turnState() != stateInput {
		m.steering = append(m.steering, item)
		m.syncViewport()
		return m, nil
	}
	// The target starts empty and the line extends it as it joins
	// (injectSent), which is the one place a line becomes part of what the
	// turn is judged against, whether it opened the turn or arrived in one.
	m.openTurn("")
	m.injectSent(item, true)
	// Whatever the session had queued for itself joins behind it.
	m.injectSteering()
	return m.streamOpenedTurn()
}

// sessionSteerEntry is the row a line leaves where it joins the turn: a steer
// on the grid, naming who sent it, with the line itself open beneath.
func sessionSteerEntry(from, text string) entry {
	return entry{
		kind:       entrySystem,
		notice:     &components.ActivityNotice{Verb: "steer", Subject: "from " + sentBy(from)},
		toolResult: strings.TrimSpace(text),
		expanded:   true,
	}
}

// injectSent joins one line to the conversation: the wording that says who
// sent it, then the line, as the session's own message, and its row. opens
// is a line that starts the turn rather than arriving in one.
func (m *Model) injectSent(item steeringItem, opens bool) {
	msg := m.agent.Steering().SessionSteer(sentBy(item.from), item.text)
	if opens {
		m.agent.StartMachineTurn(msg)
	} else {
		m.agent.AppendMachine(msg)
	}
	m.appendEntry(sessionSteerEntry(item.from, item.text))
	m.summaryTarget = agent.ExtendTarget(m.summaryTarget, item.text)
	m.signal(observe.SignalSteer, string(subagent.SteerFromSession))
}

// openHeldLine puts the first held line on its card where nothing else is
// on the screen and nothing is being typed. A decision, a surface or a
// child's card waiting is left alone — the line waits behind it — and so is a
// sentence in the draft, whose next letter could otherwise be the card's
// answer. A keyboard that is still warm is asked again once it has gone
// quiet.
func (m *Model) openHeldLine() tea.Cmd {
	if len(m.inbound.held) == 0 || m.state == stateInboundHold {
		return nil
	}
	if m.state.isSurface() || m.interruptShowing() || m.coverOverlay() != nil ||
		m.attachedTo != "" || strings.TrimSpace(m.input.Value()) != "" {
		return nil
	}
	if since := time.Since(m.lastKeypress); since < graceQuiet {
		return tea.Tick(graceQuiet-since, func(time.Time) tea.Msg { return inboundOpenMsg{} })
	}
	m.enterSurface(stateInboundHold)
	m.syncViewport()
	return nil
}

// heldLineLines draws the card over the first held line.
func (m Model) heldLineLines() []string {
	if len(m.inbound.held) == 0 {
		return nil
	}
	line := m.inbound.held[0]
	card := components.HeldLine{From: sentBy(line.From), Text: line.Text, More: len(m.inbound.held) - 1}
	return strings.Split(card.View(m.contentWidth()), "\n")
}

// updateHeldLine answers the card. `y` passes the line to the turn and `n`
// drops it; nothing else answers it, because there is no default to fall
// back on — every other key is inert, the quit chord aside.
func (m Model) updateHeldLine(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pressed := key.String()
	pass := keys.Is(pressed, keys.Confirm.Yes)
	if !pass && !keys.Is(pressed, keys.Decision.Refuse) {
		return m, nil
	}
	if len(m.inbound.held) == 0 {
		m.leaveSurface()
		return m, nil
	}
	line := m.inbound.held[0]
	m.inbound.held = m.inbound.held[1:]
	m.leaveSurface()
	if !pass {
		m.appendEntry(entry{kind: entrySystem, notice: &components.ActivityNotice{
			Verb: "steer", Subject: "from " + sentBy(line.From), Outcome: "dropped"}})
		m.syncViewport()
		open := m.openHeldLine()
		return m, open
	}
	updated, cmd := m.passInbound(line)
	next := updated.(Model)
	open := next.openHeldLine()
	return next, tea.Batch(cmd, open)
}
