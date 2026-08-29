package components

// Invariant 5 on a transcript row (S-125,
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The approval card's answer to "this surface does not hold the keyboard" is
// a whole key row plus a handover row (§7b, card.go): the keys dimmed with
// `not live yet` beside them, and `[ctrl+g] answer it` underneath. A
// transcript row cannot spend three lines saying it. It is one line on the
// §6a grid, and its keys are live only while reading mode's cursor is
// standing on it — which is most of the time not the case, because most of
// the time the draft below has the keyboard and `v` is a letter.
//
// So a row says the same two things in the space it has: the keys grey, then
// the one key that hands the keyboard over, live, carrying the words that say
// the others are waiting for it. A key that is not yet live is a different
// thing from one that cannot be pressed at all (§18a's ⊘) — that one is not
// rendered as a key at all — and the difference is said in words, so a
// monochrome terminal reads it as well as a coloured one (invariant 1).
//
// The ladder, when even one line is not enough: the grey keys go first and
// the handover goes last, because a key that is not live yet is not an offer
// and the key that turns it into one is.

import "strings"

// handoverWords trail the key that hands a row the keyboard. They are the
// row-sized form of the card's `not live yet`, and they are the component's
// own rather than the caller's for the same reason handoverRow's are: §7b
// fixes what this sentence says.
const handoverWords = "to use them"

// handoverWord is the whole run once the grey keys have dropped, with nothing
// left for `them` to point at: the key and what it opens.
const handoverWord = "read"

// keyOffers renders a run of offers live: every key the interface offers is
// info, the words for it dim.
func keyOffers(keys []TurnKey) string {
	var parts []string
	for _, k := range keys {
		parts = append(parts, sty.Info.Render(k.Key)+sty.Dim.Render(" "+k.Label))
	}
	return strings.Join(parts, sty.Dim.Render(" · "))
}

// inertOffers renders the same run for a surface that does not hold the
// keyboard. The keys drop out of info — which is the colour that means "you
// can press this" — and go grey with their words, the same treatment
// the card's not-yet-live key row takes.
func inertOffers(keys []TurnKey) string {
	var parts []string
	for _, k := range keys {
		parts = append(parts, sty.Dimmer.Render(k.Key)+sty.Dim.Render(" "+k.Label))
	}
	return strings.Join(parts, sty.Dim.Render(" · "))
}

// handoverOffer is the one live key on a row whose own keys are not: the key
// in info, its words in body text, so the live half of the run is the half
// that reads as an offer.
func handoverOffer(key, words string) string {
	return sty.Info.Render("["+key+"]") + sty.Body.Render(" "+words)
}

// keyRun renders a row's offers in the state the keyboard puts them in.
// Waiting is the row's own claim — a host that makes none keeps the live
// treatment the run always had, which is what leaves the one-shot's printed
// rows and every component test untouched.
//
// A waiting run with no handover named is the third state and it is a real
// one: reading mode holds the keyboard with its cursor on some other row, so
// these keys are not live and ctrl+e is not the way to them either — the
// mode's own bar names that, and it is `j/k`. The keys go grey and the row
// offers nothing, which is exactly true.
func keyRun(keys []TurnKey, waiting bool, handover string) string {
	if len(keys) == 0 {
		return ""
	}
	if !waiting {
		return keyOffers(keys)
	}
	if handover == "" {
		return inertOffers(keys)
	}
	return inertOffers(keys) + sty.Dim.Render(" · ") + handoverOffer(handover, handoverWords)
}

// keyRunNarrow is the same run once the terminal has run out of room for the
// keys that are not live yet. It differs only where there is a handover to
// keep: with nothing live in the run there is nothing to prefer, and the keys
// clip like any other field.
func keyRunNarrow(keys []TurnKey, waiting bool, handover string) string {
	if len(keys) == 0 || !waiting || handover == "" {
		return keyRun(keys, waiting, handover)
	}
	return handoverOffer(handover, handoverWord)
}
