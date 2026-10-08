package chat

import "github.com/rfizzle/shhh/internal/ui/components"

// approvalState is the decision card's own state on the session: the call it
// asks about and what was resolved for it, the queue behind it, the field or
// list open under it. Approval, the queue, amend and grant all read it; the
// card itself is rebuilt every frame, so whatever has to outlive one frame
// lives here. Whether a decision holds the keyboard is not the card's: every
// decision card shares it (interruptstate.go).
//
// It is a value, held by value on the Model and copied with it every frame
// like the rest of the Model, so it takes no pointer of its own: the pointer
// fields below (request, list, note, edit, grant) were pointers on the Model
// before they were gathered here, and nothing keys a memo on their identity.
//
// What a new card must not inherit from the last one is cleared in one place:
// setTurnState calls clear on every arrival at stateConfirmRun (turn.go).
// Everything else here belongs to the decision as a whole — the queue it
// heads — and is set and reset where that changes (queue.go).
type approvalState struct {
	// request is the head of the agent's approval queue while its confirm
	// prompt is showing, with everything needed to preview and execute it.
	request *approvalRequest
	// checks are the refusals that stand in front of a gated tool's
	// preview, run off the screen's goroutine (WithGatedChecks). Session
	// wiring, so clear leaves it.
	checks map[string]GatedCheckFunc
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
