package chat

// Invariant 5 across the keyed surfaces (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The acute case is fixed: a decision that arrives unbidden is inert until
// the handover gives it the keyboard (interrupt.go). The audit behind this
// file is the same rule asked of every other surface that offers a bare
// single-character key, and most of them answered by construction — a picker,
// review mode, the agent list, the undo confirm, the pressure card and
// reading mode all take the keyboard the moment they open, so their letters
// are live because nothing else is listening.
//
// Four surfaces answered differently, and they are the ones this file is
// about: the changeset row a turn closes with, a provider failure's row
//, a dropped stream's, and a round-limit pause's. They are transcript
// entries, not takeovers. Their keys are handled by reading mode on the row
// (focus.go), so while the draft below has the keyboard `v` is a letter, `[v]
// review` is an offer nothing accepts, and the row was painting it in info —
// the colour that means "you can press this".
//
// So a transcript row renders its keys live only while reading mode's cursor
// is standing on it. Everywhere else they go grey and the one key that hands
// the keyboard to the transcript is offered beside them, in the live
// treatment they do not have.

import (
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// rowOffer is one offer a transcript row makes, in both the spellings it has:
// the letter reading mode answers with its cursor on the row, and the chord
// the draft answers wherever that letter is a letter of the sentence being
// typed (keys.RowChord). The row draws whichever of the two is true where it
// stands, and neither the row nor the hint bar has to know which — they read
// the same offer.
func rowOffer(b keys.Binding, label string) components.KeyOffer {
	return rowOfferAs(b, keys.Bracket(b), label)
}

// rowOfferAs is the same offer where the row draws something other than the
// keystroke: the round-limit pause draws the grant as the block it grants
// (`[+50]`) rather than as the `+` that takes it.
func rowOfferAs(b keys.Binding, shown, label string) components.KeyOffer {
	o := components.KeyOffer{Key: shown, Label: label}
	if c, ok := keys.ChordFor(b); ok {
		o.Chord = keys.Bracket(c)
	}
	return o
}

// namesOptionRow reports that a row about to be built is the first in the
// session to offer a chord, so it is the row that names the profile setting
// an alt chord needs on a stock macOS terminal
// (docs/interface/reserved-keys.md#the-draft-spends-chords-only). It is asked
// of the transcript rather than remembered, because the answer is the same
// question either way: is there already a row up there saying it.
func (m Model) namesOptionRow(row entry) bool {
	for _, e := range *m.entries() {
		if sameOfferRow(e, row) {
			return true
		}
		if m.offersRowKeys(e) {
			return false
		}
	}
	return false
}

// firstRowOffer is the same question asked by a row that is being built and
// is not in the transcript yet: nothing up there offers anything, so this is
// the row that names the setting.
func (m Model) firstRowOffer() bool {
	for _, e := range *m.entries() {
		if m.offersRowKeys(e) {
			return false
		}
	}
	return true
}

// offersRowKeys reports that an entry carries offers of its own — the rows
// whose keys keys.Row declares. It is the list the Option note counts and the
// chord walks, so both read one answer.
func (m Model) offersRowKeys(e entry) bool {
	switch e.kind {
	case entryTurnClose:
		return e.close != nil &&
			((e.close.Changes != nil && len(e.close.Changes.Keys) > 0) ||
				(e.close.Checks != nil && len(e.close.Checks.Keys) > 0))
	case entryFailure:
		return e.fail != nil && len(m.failureKeys(e.fail)) > 0
	case entryStreamDrop:
		return e.resume != nil && len(m.dropKeys(e.resume)) > 0
	case entryRoundPause:
		return e.pause != nil && len(e.pause.keys()) > 0
	case entryTodoRun:
		return e.todorun != nil && len(e.todorun.offers()) > 0
	case entrySystem:
		return len(m.steerOffers(e)) > 0
	}
	return false
}

// sameOfferRow reports that two entries are the same row on the screen. The
// entries hold slices and cannot be compared, and the pointer each kind hangs
// its state off is unique to the row, so that is the identity.
func sameOfferRow(a, b entry) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case entryTurnClose:
		return a.close != nil && a.close == b.close
	case entryFailure:
		return a.fail != nil && a.fail == b.fail
	case entryStreamDrop:
		return a.resume != nil && a.resume == b.resume
	case entryRoundPause:
		return a.pause != nil && a.pause == b.pause
	case entryTodoRun:
		return a.todorun != nil && a.todorun == b.todorun
	case entrySystem:
		// The notice an automatic steer left, which is the one system row
		// that offers a key. What it hangs the offer off is the record of the
		// interruption (intervene.go), so that is its identity here.
		return a.intervened != nil && a.intervened == b.intervened
	}
	return false
}

// rowHandover is the key a transcript row offers beside keys that are not
// live yet, or "" where the row has nothing to offer.
//
// It is keys.Draft.Reading, which hands the keyboard from the draft to
// the transcript — a control chord for the same reason the handover is: no
// sentence can produce it, so it can be live while the draft is.
//
// It is offered only where it is live. Reading mode cannot be opened from
// under a gated decision or from inside a takeover surface, and a key that
// does nothing is the thing invariant 5 exists to stop — so on those screens
// the row's keys are simply grey, and the surface holding the keyboard says
// what its own keys are. A row whose keys are already live has nothing to
// hand over.
func (m Model) rowHandover(keysLive bool) string {
	if keysLive || !m.inputLive() {
		return ""
	}
	return keys.Shown(keys.Draft.Reading)
}

// gateRow stamps a recovery row with the state the keyboard puts it in. It is
// one place for the same reason applyNotYetLive is: no surface gets to decide
// on its own that its keys are live.
func (m Model) gateRow(row components.RecoveryRow, keysLive bool) components.RecoveryRow {
	row.KeysWaiting = !keysLive
	row.Handover = m.rowHandover(keysLive)
	return row
}
