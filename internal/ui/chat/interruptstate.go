package chat

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// interruptState is whether the decision on screen holds the keyboard, and
// the grace window that guards a card which took it by arriving
// (interrupt.go). It serves every decision card alike — the approval card,
// the /run confirm, the plan card, the model's own question and a child
// agent's routed ask — which is why it is not the approval card's.
//
// It is a value, held by value on the Model and copied with it every frame
// like the rest of the Model, and it takes no pointer of its own: its fields
// are flags, times and a counter, and nothing keys a memo on them.
//
// clear drops the hold and closes the window, which is what a decision
// leaving does: releaseDecision and armDecision (interrupt.go) call it, and
// so does purging the child's ask that held the keyboard (attach.go).
// graceSeq and lastLeft outlive it on purpose: the sequence only moves
// forward, so a repaint tick scheduled for an earlier window stays stale,
// and the departure stamp is what the next card's arrival reads (armGrace).
type interruptState struct {
	// held is whether the decision on screen holds the keyboard. A card that
	// arrives on top of a sentence never does: until the handover chord it
	// renders its keys as not-yet-live and every letter goes into the draft.
	// One that arrives on an empty draft does, because there is no sentence
	// for the letters to belong to — with the grace window covering the keys
	// a warm keyboard could still have in flight (interrupt.go). It covers
	// every decision card, the plan card and the question included.
	held bool
	// heldOnArrival narrows that: the decision holds the keyboard because it
	// landed on an idle draft, not because the handover gave it to it. A
	// card in that state answers only what it was walked up to be asked and
	// hands the keyboard back for everything else (components/approval.go).
	heldOnArrival bool
	// graceFrom is when the decision now holding the keyboard by arrival
	// landed on a keyboard still warm — the open grace window (interrupt.go).
	// Zero when no window is open; graceSeq names the window's current end,
	// so a repaint tick scheduled for an end a key moved is stale.
	graceFrom time.Time
	graceSeq  int
	// lastLeft is when a decision last left the screen, which is how a card
	// replacing another (the queue advancing) is told apart from a card
	// landing on fresh typing.
	lastLeft time.Time
}

// clear hands the keyboard back to the draft and closes any grace window,
// leaving the sequence and the departure stamp where they were.
func (i *interruptState) clear() {
	i.held, i.heldOnArrival = false, false
	i.graceFrom = time.Time{}
}

// graceOpen reports whether the grace window is open for the key being
// routed, given whether a decision is showing. It reads what settleGrace
// left, so it is only meaningful on the keystroke path, after the settle.
func (i interruptState) graceOpen(showing bool) bool {
	return !i.graceFrom.IsZero() && showing && i.held && i.heldOnArrival
}

// decisionAt is what the session knows about the decision on screen that the
// interrupt's own fields do not, read once for the key being routed.
type decisionAt struct {
	// showing is whether a decision is on screen (interruptShowing).
	showing bool
	// escLeaves is whether esc hands the keyboard back rather than
	// answering (escLeavesWaiting).
	escLeaves bool
	// floating is whether the state is one of the decision cards the
	// register places above the frame.
	floating bool
	// discards is whether the grace window swallows this key
	// (graceDiscards).
	discards bool
}

// decisionKey is what the card's keyboard made of a key, for updateKey to
// carry out on the session.
type decisionKey int

const (
	// decisionPass is a key that is not the card's; routing goes on.
	decisionPass decisionKey = iota
	// decisionGate is the handover: give the card the whole keyboard
	// (gateDecision).
	decisionGate
	// decisionDiscard is a key the grace window swallowed. It already moved
	// the window's end (graceSeq), so the repaint is rescheduled.
	decisionDiscard
	// decisionUngate is esc leaving the decision waiting and the keyboard
	// back with the draft (ungateDecision).
	decisionUngate
	// decisionRoute is a key the floating card answers itself (routeOverlay).
	decisionRoute
)

// update reads a key for the decision card before the rest of the keyboard
// gets it, and says what it is.
func (i interruptState) update(msg tea.KeyPressMsg, at decisionAt) (interruptState, decisionKey) {
	ungated := at.showing && !i.held
	// The handover means one thing in both states a decision can be in:
	// give the card the whole keyboard. From ungated it is the mid-sentence
	// rule's transfer — every letter belonged to the draft, and now none do.
	// From a card holding the keyboard by arrival it buys the keys that card
	// left alone on purpose ([a], [d], [A]).
	if at.showing && keys.Match(msg, keys.Draft.Answer) && (ungated || i.heldOnArrival) {
		return i, decisionGate
	}
	// The grace window on a card that took the keyboard by arriving on a
	// warm keyboard (interrupt.go): the keys that would answer it are
	// discarded until the keyboard has been quiet for a beat, because a key
	// this soon after typing was part of the typing. The discard still moves
	// the window's end, so the repaint is rescheduled off the bumped
	// sequence (graceTickCmd).
	if i.graceOpen(at.showing) && at.discards {
		i.graceSeq++
		return i, decisionDiscard
	}
	// A decision that arrived on top of a sentence is inert until it holds the
	// keyboard
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard):
	// ungated, the handover above is the only key that is its own, and every
	// letter belongs to the draft.
	if !ungated {
		if keys.Match(msg, keys.Draft.Clear) && at.escLeaves {
			// Esc leaves the decision waiting rather than denying it; [n]
			// is how you say no.
			return i, decisionUngate
		}
		// The decision cards are the only modes the register places
		// floating: they answer only once the handover has given them the
		// keyboard.
		if at.floating {
			return i, decisionRoute
		}
	}
	return i, decisionPass
}
