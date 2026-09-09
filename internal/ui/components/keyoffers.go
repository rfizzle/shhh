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

// words is a binding as one segment of a key row drawn in a single tone,
// with the surface's own words. A row whose key is live is built from
// KeyOffer instead, so the key can wear Info and the words beside it Dim; a
// header states a key rather than offering one, and states it in the one
// tone the header wears.
func words(b keys.Binding, label string) string { return keys.Bracket(b) + " " + label }

// keyOffer is a binding as a bracketed offer.
func keyOffer(b keys.Binding) KeyOffer {
	return keyOfferAs(b, keys.Words(b))
}

// keyOfferAs is the same with the surface's own words.
//
// Whether the offer is the safe one is decided here, off the spelling, and
// not by each row: esc is the answer that changes nothing wherever a surface
// holds the whole keyboard, and a row that had to remember to say so is a row
// that will one day forget
// (docs/interface/principles.md#esc-is-always-the-safe-answer). A binding
// answered by esc but spelled `q` is not it — the reader pressed a letter,
// and what the letter does is the surface's to say.
func keyOfferAs(b keys.Binding, label string) KeyOffer {
	return KeyOffer{Key: keys.Bracket(b), Label: label, Safe: keys.Shown(b) == safeSpelling}
}

// safeSpelling is how the register spells the key that changes nothing.
const safeSpelling = "esc"

// Offer is a binding as one offer on a key row, for a surface outside this
// package that lays its own. It is the door the brackets are written behind:
// a host that spelled a key itself would be the second place a rebind has to
// reach.
func Offer(b keys.Binding) KeyOffer { return keyOffer(b) }

// OfferAs is the same with the surface's own words, which is what a host
// reaches for wherever it means something more specific than the register
// does.
func OfferAs(b keys.Binding, label string) KeyOffer { return keyOfferAs(b, label) }

// offerRun is a run of offers as the plain sentences a row drawn in one tone
// needs. A live row paints the key apart from the words beside it, so it
// keeps the offers themselves; the runs where no key is live — the card
// waiting for the draft, the one being typed into — wear a single grey, and
// a seam nothing paints across is a seam worth flattening
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func offerRun(offers []KeyOffer) []string {
	out := make([]string, 0, len(offers))
	for _, o := range offers {
		out = append(out, o.Key+" "+o.Label)
	}
	return out
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

// screenBackKeys is the same pair for a screen a session opened rather than a
// command line. One word differs, and it is the one word that is not true of
// both: what is underneath is a prompt to go back to and not a shell to quit
// to (docs/interface/surfaces.md#the-supporting-screens).
func screenBackKeys() string {
	return keys.Bracket(keys.Screen.List) + " " + keys.Words(keys.Screen.List) +
		" · " + words(keys.Screen.Quit, "back")
}

// hideKeysOffer is the same key again, once the list it opened is showing.
func hideKeysOffer() KeyOffer {
	return keyOfferAs(keys.Screen.List, "hide the keys")
}
