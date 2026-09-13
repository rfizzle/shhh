package components

// The rewind scope card: the card the /rewind picker opens once a turn has
// been taken (docs/interface/surfaces.md#the-rewind).
//
// A rewind is two rewinds arriving as one word. The files a run of turns
// wrote are on disk and the turns themselves are in the window, and going
// back to before a turn while leaving what it wrote on disk is a state
// neither the reader nor the model asked for — so the card states the two
// halves apart, in the same field block an approval states a blast radius in,
// and offers each on its own as well as both together.
//
// It is a passive renderer like every other card here. What the fields say is
// the host's reading of the session and the changeset; what this owns is the
// shape.

import (
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// RewindCard is the card, in the three statements a reader needs before
// pressing a key: what comes back on disk, what leaves the window, and
// whether any of it can be taken back.
type RewindCard struct {
	// Title names the point the session would return to, e.g. `Rewind to
	// before turn 5`.
	Title string
	// Code is what a file restore would put back, and Talk what a
	// conversation rewind would fold out of the window. Both are CardFields
	// because the question this card answers is the question an approval
	// card answers: what will this touch, and can I take it back.
	Code CardField
	Talk CardField
	// Undo is the third field, and it is the one that is the same on every
	// rewind: a rewind is a turn, so the row it lands as is undoable like
	// any other.
	//
	// There is no fourth state for a rewind whose files cannot come back.
	// The picker's own row says so before the card is reached, and a card
	// offering one answer is a card asking a question that has none — so
	// that answer is given rather than put (chat/rewind.go).
	Undo CardField
}

// View renders the card at the given width.
func (c RewindCard) View(width int) string {
	inner := Card{}.Inner(width)
	rows := []string{"", c.Code.render(inner), c.Talk.render(inner), c.Undo.render(inner), cardRule}
	rows = append(rows, runRows(rewindRun(), inner)...)
	rows = append(rows, rewindEscRow(inner))
	// The tone is the mutation rail's, the same Accent the rows a restore
	// would put back are drawn in, and the chip says the level in words
	// beside it (invariant 1). Low rather than higher because everything a
	// rewind touches is on record: the files come back from the session's
	// own changeset and the turns it folds out are kept as a branch.
	style := SeverityLow.tone()
	return Card{
		Title: c.Title,
		Chips: []string{SeverityLow.Word()},
		Style: &style,
	}.Render(rows, width)
}

// rewindRun is the card's three answers, drawn from the register so the
// spelling offered is the spelling answered. Both leads, because it is the
// usual reading of "go back" and the one a reader who did not come here to
// think about scope means.
func rewindRun() []string {
	return []string{
		offerSegment(keys.Bracket(keys.Rewind.Both), keys.Words(keys.Rewind.Both)+" — the usual meaning of going back"),
		offerSegment(keys.Bracket(keys.Rewind.Code), keys.Words(keys.Rewind.Code)),
		offerSegment(keys.Bracket(keys.Rewind.Talk), keys.Words(keys.Rewind.Talk)),
	}
}

// rewindEscRow is the row the card ends on. Esc is the safe answer in the
// fullest sense the product has: nothing was restored, no turn left the
// window, and the picker that offered this is still there
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func rewindEscRow(inner int) string {
	esc := keys.Shown(keys.Rewind.Cancel)
	words := keys.Words(keys.Rewind.Cancel) + " — back to the picker, nothing restored"
	return Clip(safeSegment(esc, fitClauses("["+esc+"] ", words, inner)), inner)
}
