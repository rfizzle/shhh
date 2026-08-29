package chat

// When shhh needs you and you are not there (S-157,
// docs/interface/surfaces.md#when-you-are-not-there).
//
// A turn runs for minutes and then stops, and what it stops on is usually a
// question: approve this command, apply this patch, the plan is ready. The
// reader who started it went to do something else. Until this file existed
// nothing told them, so they came back and found the turn had been waiting on
// one keystroke for four minutes — which is the whole cost of an agent that
// asks permission.
//
// Two rules keep the answer from becoming noise, and both are edges rather
// than states.
//
// **shhh notifies on the transition into waiting**, not while waiting: the
// one moment the session stops needing shhh and starts needing the reader.
// That moment is not a message it can be hung off — it is reached from a
// dozen handlers, and three of them are cancellations — so it is derived in
// Update from the model before against the model after, the way the spinner's
// tick is (S-119, spin.go). A property of the transition is read off the
// transition.
//
// **And only when the terminal has said the window is not the one in front.**
// That rule gates itself. Focus reporting is asked for on the View, and the
// only way to know the window is away is to have been told so — a terminal
// that does not report focus never sends the blur, so `away` stays false and
// shhh never decides it is being ignored on a guess. Crush asks the
// capability probe whether focus events are supported and trusts that answer;
// having actually received one is the stronger fact and it costs nothing to
// prefer, so §10k's FocusEvents stays a readout rather than a gate.

import (
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

// waiting reports whether the session has stopped and the next move is the
// reader's.
//
// It is working() read the other way round, plus the one decision that
// does not live in the turn state: a child agent's routed approval arrives
// while the parent's own turn is still streaming, and activeChildAsk
// already answers for whether it is in front of anyone — behind a surface it
// is nil, because a decision nobody can see has not arrived yet.
func (m Model) waiting() bool {
	return !m.working() || m.activeChildAsk() != nil
}

// notifyCmd is the notification this transition earns, or nil. prev is the
// model the message arrived at; the receiver is the model it produced.
func (m Model) notifyCmd(prev Model) tea.Cmd {
	if !m.notifyOn || !m.away || m.quitting {
		return nil
	}
	if prev.waiting() || !m.waiting() {
		return nil
	}
	// A turn that was never open did not end. The session reaches the input
	// from things that are not turns — a /run the reader started themselves,
	// a compaction — and shhh's summons is about work it was doing on their
	// behalf while they were elsewhere, not about a command they watched
	// start ten seconds ago (S-098: a /run finishing is not a turn ending).
	if m.turnState() == stateInput && !prev.turnOpen {
		return nil
	}
	title, body := m.notifyWords()
	if title == "" {
		return nil
	}
	return m.caps.Notify(notifyName+" · "+title, body)
}

// notifyName is what shhh calls itself in a notification title. OSC 99 has a
// field for the application name and OSC 777 does not, so the title says it
// either way rather than the reader having to guess which terminal window a
// bare "Approve command" came from.
const notifyName = "shhh"

// notifyWords is what the notification says: the title, and the line beneath
// it. They are the words already on the screen the reader is being called
// back to — a card's own title and headline, a turn close's own summary —
// because a summons that describes the screen in different words is a summons
// the reader has to reconcile when they arrive.
//
// The switch is interruptLines' switch read against the turn state
// rather than Model.state: a decision the turn reached while a surface held
// the screen is still the thing the session is waiting on, even though the
// screen is showing something else.
func (m Model) notifyWords() (title, body string) {
	if ask := m.activeChildAsk(); ask != nil {
		card := m.childAskCard(ask)
		return card.Title, card.Headline
	}
	switch m.turnState() {
	case stateConfirmRun:
		// The memory proposal confirms through its own prompt rather than the
		// card (S-070), and says its own thing.
		if m.memoryAsk != nil {
			if req := m.pendingApproval; req != nil {
				return "Remember this?", fmt.Sprintf("Assistant proposes a %s memory: %q", req.memoryDraft.Kind, firstLine(req.memoryDraft.Text))
			}
			return "Remember this?", ""
		}
		card := m.buildApprovalCard()
		return card.Title, card.Headline
	case statePlanApprove:
		card := m.planCard()
		body := "Waiting for your decision"
		if card.Chip != "" {
			body = card.Chip + " · " + body
		}
		return card.Title, body
	case stateInput:
		return m.turnCloseWords()
	}
	return "", ""
}

// turnCloseWords is the finished turn said in two lines: how it ended, and
// what it cost and changed.
func (m Model) turnCloseWords() (title, body string) {
	// A turn that stopped at its round limit closed with the pause row rather
	// than the close block (S-109), and the row is what the reader will find
	// on the screen — so it is what the notification says.
	if p := m.roundPause; m.pausedAtRoundLimit() && p != nil {
		return "Turn stopped at its round limit", fmt.Sprintf("%d of %d rounds used · %s", p.used, p.limit, p.detail())
	}
	return "Turn " + lowerFirst(m.turnOutcome.Word()), m.turnCloseData().Summary()
}

// lowerFirst is the close block's state word joined onto "Turn". The block
// draws it as a sentence of its own and starts it with a capital; here it is
// the second word of one.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'A' && r[0] <= 'Z' {
		r[0] += 'a' - 'A'
	}
	return string(r)
}

// notifyStatus describes the current notification state for /ui, naming the
// reason there is nothing to hear when there is one. A setting that is on and
// silent is the case worth explaining: the terminal is the half of this that
// shhh does not control.
func (m Model) notifyStatus() string {
	if !m.notifyOn {
		return "off"
	}
	switch {
	case !m.caps.Asked:
		return "on, but there is no terminal to notify"
	case !m.away && !m.caps.FocusEvents:
		return "on, but this terminal has not reported focus — nothing will fire until it does"
	case m.caps.Notifications:
		return "on (OSC 99)"
	}
	return "on (OSC 777, the blind fallback)"
}

// setNotify flips notifications and persists the answer, so the choice
// outlives the process that made it — the same bargain /ui mouse makes. A
// session with no writer still flips, and says only what it could not do.
func (m *Model) setNotify(on bool) string {
	m.notifyOn = on
	note := notifyNote(on)
	if m.writeConfig == nil {
		return note + "\nThis session cannot write the config file, so it is for this session only."
	}
	if err := m.writeConfig("appearance.notify", strconv.FormatBool(on)); err != nil {
		return note + "\nIt could not be saved: " + err.Error()
	}
	return note + " Saved — new sessions start this way."
}

// notifyNote says what the new state means rather than repeating it, because
// what "on" buys is not obvious: it is not "shhh will tell you things", it is
// "shhh will tell you the one thing, and only when you cannot see it".
func notifyNote(on bool) string {
	if on {
		return "Desktop notifications on: when a turn stops and your terminal has said the window is not in front, shhh raises one notification saying what it stopped on."
	}
	return "Desktop notifications off: a turn that stops while you are elsewhere waits silently."
}

// notifyCommand handles /ui notify. It is a setting rather than a default
// nobody can reach for the same reason mouse reporting is: it does something
// outside the terminal shhh is drawing in, and a thing that leaves the window
// is a thing you are owed a switch for.
func (m *Model) notifyCommand(parts []string) string {
	if len(parts) == 2 {
		return "Desktop notifications: " + m.notifyStatus() +
			".\nUsage: /ui notify <on|off> — on, a turn that stops while the window is not in front raises one notification; nothing fires while you are looking at the screen."
	}
	if len(parts) != 3 {
		return "Usage: /ui notify <on|off>"
	}
	var on bool
	switch parts[2] {
	case "on", "true", "yes":
		on = true
	case "off", "false", "no":
		on = false
	default:
		return fmt.Sprintf("Error: unknown notify setting %q (on, off)", parts[2])
	}
	if on == m.notifyOn {
		return "Desktop notifications are already " + m.notifyStatus() + "."
	}
	return m.setNotify(on)
}

// WithNotify sets whether the session may raise desktop notifications
// (appearance.notify). Hosts that do not call it get them, which is the
// default the config resolves to.
func (m Model) WithNotify(on bool) Model {
	m.notifyOn = on
	return m
}
