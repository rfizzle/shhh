package chat

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// approvalState is the decision card's own state on the session: the call it
// asks about and what was resolved for it, the queue behind it, the field or
// list open under it, and whether it holds the keyboard. Approval, the queue,
// the grace window, amend and grant all read it; the card itself is rebuilt
// every frame, so whatever has to outlive one frame lives here.
//
// It is a value, held by value on the Model and copied with it every frame
// like the rest of the Model, so it takes no pointer of its own: the pointer
// fields below (request, list, note, edit, grant) were pointers on the Model
// before they were gathered here, and nothing keys a memo on their identity.
//
// What a new card must not inherit from the last one is cleared in one place:
// setTurnState calls clear on every arrival at stateConfirmRun (turn.go).
// Everything else here belongs to the decision as a whole — the queue it
// heads, and the keyboard every decision card shares — and is set and reset
// where those change (queue.go, interrupt.go).
type approvalState struct {
	// request is the head of the agent's approval queue while its confirm
	// prompt is showing, with everything needed to preview and execute it.
	request *approvalRequest
	// blast is the card's blast-radius block for the decision showing now,
	// resolved once when the confirm is armed because it reads the
	// filesystem and git.
	blast blastRadius
	// scope is what that decision reaches outside the working scope,
	// resolved with the blast radius and consumed when the decision is
	// answered: approving it grants the directories, refusing it grants
	// nothing.
	scope scopeReach
	// The approval queue made visible: strip is the strip above the card,
	// batch the queued call IDs [A] would put on the list with the current
	// one, and batchAnswered how that list answered them — allowed or
	// denied — for calls that have not reached the head yet, so each is
	// carried out when its turn comes instead of asking again. list is the
	// list itself while it is open, and nil the rest of the time (queue.go).
	// total is how many decisions this tool round queued, so the card can
	// say "2 of 5" once two have been answered.
	strip         components.QueueStrip
	batch         []string
	batchAnswered map[string]bool
	list          *queueList
	total         int
	// spawns are the children the card in front of the reader would start —
	// the decision's own and every spawn of the round answered along with
	// it. They are resolved with the strip and for the same reason: the card
	// is rebuilt every frame and reading each queued spawn's arguments on
	// all of them is work no frame changes (queue.go).
	spawns []components.SpawnRow

	// The card's scroll (docs/interface/surfaces.md#the-approval-card): the
	// card is rebuilt every frame, so its offsets live here and are reset
	// whenever the card changes (clear).
	scroll int
	pan    int
	// note is the one-line field a decision card's shifted answer opened,
	// and the answer it will carry
	// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).
	// It lives here for the reason the scroll does — the card is rebuilt
	// every frame and what is typed has to outlive one — and it is cleared
	// wherever the card changes (clear).
	note *decisionNote
	// edit is the command card's other field: the command itself, open for
	// the reader to change before it runs (amend.go). It lives here for the
	// same reason and is cleared in the same place; only one of the two is
	// ever open, because only one thing can hold a keyboard.
	edit *commandEdit
	// grant is the third surface the card can open under itself: the grants
	// the always-allow key offers, each with what it covers and when it ends
	// (grant.go). It lives here and is cleared where the other two are, and
	// only one of the three is ever open.
	grant *grantChoice

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

// clear drops what a card must not carry onto the next one: its scroll, and
// whichever field or list the reader had open under it. setTurnState calls it
// on every arrival at a card.
func (a *approvalState) clear() {
	// Nor can a card inherit the last one's scroll: the offsets describe a
	// body that has just been replaced, and a stale pan would blank the new
	// card's rows outright.
	a.scroll, a.pan = 0, 0
	// Nor the last one's half-written note: the sentence was about the call
	// that has just been answered, and carrying it onto the next card would
	// attach the reader's words to a decision they were written about
	// something else (approval.go).
	a.note = nil
	// Nor a half-written amendment, and for a sharper version of the same
	// reason: a line left over from the last card would be a command the
	// reader wrote about a different call, one enter away from running
	// (amend.go).
	a.edit = nil
	// Nor a grant list left open over a card that has just been answered:
	// its rows name the command, the directory or the host that belonged to
	// that decision, and a row taken now would grant something the card in
	// front of the reader never showed them (grant.go).
	a.grant = nil
}

// graceOpen reports whether the grace window is open for the key being
// routed, given whether a decision is showing. It reads what settleGrace
// left, so it is only meaningful on the keystroke path, after the settle.
func (a approvalState) graceOpen(showing bool) bool {
	return !a.graceFrom.IsZero() && showing && a.held && a.heldOnArrival
}

// decisionAt is what the session knows about the decision on screen that the
// card's own fields do not, read once for the key being routed.
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
func (a approvalState) update(msg tea.KeyPressMsg, at decisionAt) (approvalState, decisionKey) {
	ungated := at.showing && !a.held
	// The handover means one thing in both states a decision can be in:
	// give the card the whole keyboard. From ungated it is the mid-sentence
	// rule's transfer — every letter belonged to the draft, and now none do.
	// From a card holding the keyboard by arrival it buys the keys that card
	// left alone on purpose ([a], [d], [A]).
	if at.showing && keys.Match(msg, keys.Draft.Answer) && (ungated || a.heldOnArrival) {
		return a, decisionGate
	}
	// The grace window on a card that took the keyboard by arriving on a
	// warm keyboard (interrupt.go): the keys that would answer it are
	// discarded until the keyboard has been quiet for a beat, because a key
	// this soon after typing was part of the typing. The discard still moves
	// the window's end, so the repaint is rescheduled off the bumped
	// sequence (graceTickCmd).
	if a.graceOpen(at.showing) && at.discards {
		a.graceSeq++
		return a, decisionDiscard
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
			return a, decisionUngate
		}
		// The decision cards are the only modes the register places
		// floating: they answer only once the handover has given them the
		// keyboard.
		if at.floating {
			return a, decisionRoute
		}
	}
	return a, decisionPass
}
