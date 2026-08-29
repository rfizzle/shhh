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
