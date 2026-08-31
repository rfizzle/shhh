package chat

// The shell's own continuation, in the draft
// (docs/interface/surfaces.md#the-input-frame): enter on a line that ends
// in a backslash is a newline, not a send. The reflex that ends every
// shell line with enter would otherwise send a prompt mid-thought, and the
// escape hatch is the shell's too — a doubled backslash sends, carrying
// one literal one.

import (
	"strings"
)

// holdSend answers enter on a draft whose last character is a backslash:
// the backslash is removed and a newline inserted in its place, and the
// send is held. A draft ending in a doubled backslash sends with a single
// literal one instead — the draft is rewritten in place and the send goes
// on. It reports false — send as usual — on every other draft, and steps
// aside entirely while the completion menu owns what enter means.
func (m *Model) holdSend() bool {
	if m.completionActive() {
		return false
	}
	val := strings.TrimRight(m.input.Value(), " \t")
	if !strings.HasSuffix(val, `\`) {
		return false
	}
	if strings.HasSuffix(val, `\\`) {
		// An escaped backslash: send, with the pair collapsed to the one
		// literal character it spells.
		m.input.SetValue(strings.TrimSuffix(val, `\`))
		m.input.MoveToEnd()
		return false
	}
	m.input.SetValue(strings.TrimSuffix(val, `\`) + "\n")
	m.input.MoveToEnd()
	m.syncViewport()
	return true
}
