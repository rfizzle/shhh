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
// (focus.go), so while the draft below has the keyboard `u` is a letter, `[u]
// undo turn` is an offer nothing accepts, and the row was painting it in info —
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
	case entryRewound:
		return len(m.rewoundOffers(e)) > 0
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
	case entryRewound:
		return a.rewound != nil && a.rewound == b.rewound
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

// rowSel is where a transcript row stands against the selection, which is
// the whole of what decides whether its offers are live
// (docs/interface/surfaces.md#the-turns-close). A row offer acts on one row,
// the one the reader can see is selected, so a row nobody has selected draws
// nothing live: its offers go grey beside the key that hands the keyboard
// over, and the chords stay off it. That is also what keeps the same live
// chord from being printed on every turn a session has closed, each of them
// promising to act on a turn it would not have acted on.
type rowSel int

const (
	// rowUnselected is every row in the plain feed, and every row but one
	// under the gutter.
	rowUnselected rowSel = iota
	// rowPointed is the row the pointer lit from the prompt names. The draft
	// still has the keyboard, so the row's letters are text and its chords
	// are what is live.
	rowPointed
	// rowUnderCursor is the row reading mode's cursor stands on, where the
	// letters are live because nothing else is listening.
	rowUnderCursor
)

// lettersLive reports whether the row's own letters answer where it stands.
func (s rowSel) lettersLive() bool { return s == rowUnderCursor }

// selOffers is a row's offers as the selection lets it draw them. A row that
// is not selected draws its chords grey, beside the handover, rather than
// live: a chord acts only on the selected row, and a live chord on any other
// would be an offer the dispatch does not answer. It keeps the chord's
// spelling rather than going back to the letter, because a letter drawn
// beside a live draft is a letter of the sentence being typed.
func selOffers(offers []components.KeyOffer, sel rowSel) []components.KeyOffer {
	if sel != rowUnselected || len(offers) == 0 {
		return offers
	}
	out := make([]components.KeyOffer, len(offers))
	for i, o := range offers {
		if o.Chord != "" {
			o.Key = o.Chord
		}
		o.Chord = ""
		out[i] = o
	}
	return out
}

// reviewTurnWords are what enter does on a row that states what a turn
// changed: it opens that turn's review.
const reviewTurnWords = "review turn"

// reviewTurnOffer is that act drawn on the selected row. It is enter under
// either spelling — the pointer's open and the cursor's are the same key —
// so the chord it carries is itself, which keeps a run of chords a run of
// chords. It is a label for what the row does once it is selected, like the
// `[enter] expand` a fold draws, and not a promise that enter reaches the
// transcript while a sentence in the draft owns it.
func reviewTurnOffer() components.KeyOffer {
	k := keys.Bracket(keys.Reading.Expand)
	return components.KeyOffer{Key: k, Chord: k, Label: reviewTurnWords}
}

// reviewableRow is the turn a row opens a review of: a turn's close that
// changed files, or the round-limit pause that stands where that close will
// be. It is the session's own transcript only, like every row offer.
func (m Model) reviewableRow(idx int) (int64, bool) {
	if m.attachedTo != "" || idx < 0 || idx >= len(m.transcript) {
		return 0, false
	}
	e := m.transcript[idx]
	switch {
	case e.kind == entryTurnClose && e.close != nil && e.close.Changes != nil:
		return e.turn, true
	case e.kind == entryRoundPause && e.pause != nil && e.pause.files > 0:
		return e.turn, true
	}
	return 0, false
}

// closeFor is a turn's close block as the selection lets it draw. Its offers
// are drawn only on the selected block: the same keys on every turn a
// session has closed told the reader nothing about which turn they would
// act on. Selected, the changed-files row leads with enter's own act.
func (m Model) closeFor(c components.TurnClose, sel rowSel) components.TurnClose {
	live := sel.lettersLive()
	c.KeysWaiting, c.Handover = !live, ""
	if ch := c.Changes; ch != nil {
		cp := *ch
		cp.Keys = nil
		if sel != rowUnselected {
			cp.Keys = append([]components.KeyOffer{reviewTurnOffer()}, ch.Keys...)
		}
		c.Changes = &cp
	}
	if ck := c.Checks; ck != nil && sel == rowUnselected {
		cp := *ck
		cp.Keys = nil
		c.Checks = &cp
	}
	return c
}

// gateRow stamps a recovery row with the state the keyboard puts it in. It is
// one place for the same reason applyNotYetLive is: no surface gets to decide
// on its own that its keys are live.
func (m Model) gateRow(row components.RecoveryRow, sel rowSel) components.RecoveryRow {
	row.Keys = selOffers(row.Keys, sel)
	row.KeysWaiting = !sel.lettersLive()
	row.Handover = m.rowHandover(sel.lettersLive())
	return row
}
