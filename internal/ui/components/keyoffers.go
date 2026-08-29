package components

// The register on the page (S-153,
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Every key row in this package is drawn from a binding rather than from a
// string, so the spelling a reader is offered is the spelling the handler
// beside it answers to. There are two shapes and the difference is not
// decorative: a card's key row brackets its keys because they sit in a
// sentence (`[y] allow`), and the selector family's hint line does not
// because it is already a run of nothing but keys.
//
// Words are the binding's own unless a surface has better ones. `[r]` is "try
// again" in the register and "ask again from scratch" on the row that means
// that; the key is what must not drift, and the register owns it.

import "github.com/rfizzle/shhh/internal/ui/keys"

// offer is a binding as one segment of an unbracketed key row.
func offer(b keys.Binding) string { return keys.Shown(b) + " " + keys.Words(b) }

// words is the same segment with the surface's own words.
func words(b keys.Binding, label string) string { return keys.Shown(b) + " " + label }

// keyOffer is a binding as a bracketed offer.
func keyOffer(b keys.Binding) KeyOffer {
	return KeyOffer{Key: keys.Bracket(b), Label: keys.Words(b)}
}

// keyOfferAs is the same with the surface's own words.
func keyOfferAs(b keys.Binding, label string) KeyOffer {
	return KeyOffer{Key: keys.Bracket(b), Label: label}
}

// screenHeaderKeys is the pair every supporting TUI puts at the right end of
// its header: the key that shows the whole register, and the way out.
func screenHeaderKeys() string {
	return keys.Bracket(keys.Screen.List) + " " + keys.Words(keys.Screen.List) +
		" · " + keys.Bracket(keys.Screen.Quit) + " " + keys.Words(keys.Screen.Quit)
}

// hideKeysOffer is the same key again, once the list it opened is showing.
func hideKeysOffer() KeyOffer {
	return keyOfferAs(keys.Screen.List, "hide the keys")
}
