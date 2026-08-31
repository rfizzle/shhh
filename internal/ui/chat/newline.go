package chat

// Shift+Enter as a newline. Enter sends, so the draft needed a
// second key that does not; alt+enter was it, and nobody found it. Shift
// +Enter is the key people reach for, and it is the one key a terminal is
// least likely to report: in the legacy encoding Enter is a bare CR with
// nowhere to put a modifier, so a terminal that has not been asked to say
// more sends the same byte for both and the distinction never reaches us.
//
// Under v1 this file was two halves: a hand-written request for xterm's
// modifyOtherKeys, and a parser for the raw CSI sequence that came back,
// because v1 had no name for a modified Enter and delivered it as the bytes
// the terminal sent.
//
// v2 does both. It asks every terminal for modifyOtherKeys *and* the
// Kitty keyboard protocol on every render, and puts both back on the way out
// — through the Kitty stack, which is a thing the hand-written request had no
// way to do — and its decoder reads what comes back. So a modified Enter now
// arrives named, and what is left here is the rule the two halves existed to
// serve.
//
// It is still not guaranteed: a terminal that ignores both requests keeps
// sending a bare CR, and no amount of decoding invents the shift. So
// alt+enter stays bound, ctrl+j joins it, and the rail names shift+enter
// because that is what works nearly everywhere and the other two are the
// fallback rather than the instruction.

import (
	tea "charm.land/bubbletea/v2"
)

// newlineKey reports whether a message is Enter with a modifier on it —
// shift, ctrl, or any combination that is not alt alone.
//
// A modified Enter means the same thing here: put a line break in the
// draft, do not send it. Alt+enter is the one exception, because it is a
// key of its own on this surface — while a turn is live it queues the
// draft as a follow-up (docs/interface/surfaces.md#the-input-frame) — so
// it passes through named rather than being folded into the rewrite, and
// falls back to the newline it always was when there is no turn to follow.
func newlineKey(msg tea.Msg) bool {
	key, ok := msg.(tea.KeyPressMsg)
	return ok && key.Code == tea.KeyEnter && key.Mod != 0 && key.Mod != tea.ModAlt
}

// ctrlJ is the key the textarea's newline binding listens for. A modified
// Enter is rewritten to it rather than handled here, so there is one newline
// path through the surface instead of two.
var ctrlJ = tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
