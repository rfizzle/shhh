package components

// The register on the page (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Every key row in this package is drawn from a binding rather than from a
// string, so the spelling a reader is offered is the spelling the handler
// beside it answers to.
//
// There is one notation and it is the bracket: `[y] allow` on a card, at a
// screen's foot, and on the hint line a selector draws for itself. The
// family's own default line went bare on the argument that a run of nothing
// but keys is read as keys without them — and the cost was that the same list
// arrived in two notations depending on whether its surface had supplied a
// row of its own, which is a distinction about this code and not about the
// keyboard. A reader who has learned that a bracket means a live key is worse
// served by two notations than by one.
//
// Words are the binding's own unless a surface has better ones. `[r]` is "try
// again" in the register and "ask again from scratch" on the row that means
// that; the key is what must not drift, and the register owns it.

import "github.com/rfizzle/shhh/internal/ui/keys"

// offer is a binding as one segment of a key row.
func offer(b keys.Binding) string { return keys.Bracket(b) + " " + keys.Words(b) }

// words is the same segment with the surface's own words.
func words(b keys.Binding, label string) string { return keys.Bracket(b) + " " + label }

// keyOffer is a binding as a bracketed offer.
func keyOffer(b keys.Binding) KeyOffer {
	return KeyOffer{Key: keys.Bracket(b), Label: keys.Words(b)}
}

// keyOfferAs is the same with the surface's own words.
func keyOfferAs(b keys.Binding, label string) KeyOffer {
	return KeyOffer{Key: keys.Bracket(b), Label: label}
}

// The two phrases a take-over screen states its way out in. Which of them a
// screen uses is a fact about where the reader was when they opened it and
// nothing else, and there is no third: leaving a screen that changes nothing
// is one act, and an act worded three ways is three acts to read
// (docs/interface/surfaces.md#the-supporting-screens).
const (
	backToShell  = "back to the shell"
	backToPrompt = "back to the prompt"
)

// wayOut is the way out as a screen's footer offers it. The header carries
// the same act in one word under the letter; the foot carries esc, because a
// frame that spelled one key the same way twice would be saying nothing the
// second time.
//
// The esc spelling comes from the cancel declaration rather than from a
// screen's own quit, which is spelled `[q]`: esc is one keystroke with one
// meaning wherever a surface holds the whole keyboard, and every screen in
// this family answers it.
func wayOut(phrase string) KeyOffer { return keyOfferAs(keys.Select.Cancel, phrase) }

// screenHeaderKeys is the pair every supporting TUI puts at the right end of
// its header: the key that shows the whole register, and the way out in the
// one word a header field is. The phrase belongs to the footer and to `[?]`;
// a header states what the key is for, not what it will do to the screen
// (docs/interface/surfaces.md#the-supporting-screens).
func screenHeaderKeys() string {
	return keys.Bracket(keys.Screen.List) + " " + keys.Words(keys.Screen.List) +
		" · " + keys.Bracket(keys.Screen.Quit) + " " + keys.Words(keys.Screen.Quit)
}

// hideKeysOffer is the same key again, once the list it opened is showing.
func hideKeysOffer() KeyOffer {
	return keyOfferAs(keys.Screen.List, "hide the keys")
}
