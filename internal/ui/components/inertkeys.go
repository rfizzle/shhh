package components

// Invariant 5 on a transcript row (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The approval card's answer to "this surface does not hold the keyboard" is
// a whole key row plus a handover row (card.go): the keys dimmed with
// `not live yet` beside them, and the handover key with `answer it`
// underneath. A transcript row cannot spend three lines saying it. It is one
// line on the column grid, and its keys are live only while reading mode's
// cursor is standing on it — which is most of the time not the case, because
// most of the time the draft below has the keyboard and `v` is a letter.
//
// So a row says the same two things in the space it has: the keys grey, then
// the one key that hands the keyboard over, live, carrying the words that say
// the others are waiting for it. A key that is not yet live is a different
// thing from one that cannot be pressed at all (the palette's ⊘) — that one is not
// rendered as a key at all — and the difference is said in words, so a
// monochrome terminal reads it as well as a coloured one (invariant 1).
//
// The ladder, when even one line is not enough: the grey keys go first and
// the handover goes last, because a key that is not live yet is not an offer
// and the key that turns it into one is.

import "strings"

// handoverWords trail the key that hands a row the keyboard. They are the
// row-sized form of the card's `not live yet`, and they are the component's
// own rather than the caller's for the same reason handoverRow's are: the
// mid-sentence rule fixes what this sentence says.
const handoverWords = "to use them"

// handoverWord is the whole run once the grey keys have dropped, with nothing
// left for `them` to point at: the key and what it opens.
const handoverWord = "read"

// keyOffers renders a run of offers live: every key the interface offers is
// info, the words for it dim.
func keyOffers(keys []TurnKey) string {
	var parts []string
	for _, k := range keys {
		tone := sty.Info
		if k.Safe {
			tone = sty.Add
		}
		parts = append(parts, tone.Render(k.Key)+sty.Dim.Render(" "+k.Label))
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

// chorded reports that every offer in the run carries the chord that reaches
// it while the row's letters are not live. All or none: a run drawn half in
// chords and half in letters would be asking the reader to tell which of two
// notations each bracket is in, mid-sentence, which is the moment the rule
// this file is about exists to protect.
func chorded(keys []TurnKey) bool {
	for _, k := range keys {
		if k.Chord == "" {
			return false
		}
	}
	return len(keys) > 0
}

// asChords is the run with each offer spelled the way it is pressed from the
// draft.
func asChords(keys []TurnKey) []TurnKey {
	out := make([]TurnKey, len(keys))
	for i, k := range keys {
		k.Key = k.Chord
		out[i] = k
	}
	return out
}

// optionRow is what the first row in a session to offer a chord says under
// it: an alt chord composes a character rather than arriving on the two stock
// macOS terminals until the profile is told to send the escape prefix, and
// the doctor's keys row is what reads that setting and says which box
// (docs/interface/reserved-keys.md#the-draft-spends-chords-only). It is said
// once, because it is a fact about the terminal and not about this row.
//
// It takes a line of its own rather than trailing the keys. A row's key run
// is already the widest thing on it, and a sentence appended to that run is a
// sentence the narrowing drops first — which would leave the note on the wide
// terminals that least need it and off the narrow ones that most do.
const optionRow = "alt needs Option as Meta — shhh doctor"

// optionLine is that sentence as the line a row appends.
func optionLine() string { return sty.Dim.Render(optionRow) }

// namesTheOption reports that this run is the one that says it: the offers
// are being drawn as chords, and this row is the session's first to do it.
func namesTheOption(keys []TurnKey, waiting, option bool) bool {
	return option && waiting && chorded(keys)
}

// keyRun renders a row's offers in the state the keyboard puts them in.
// Waiting is the row's own claim — a host that makes none keeps the live
// treatment the run always had, which is what leaves the one-shot's printed
// rows and every component test untouched.
//
// A waiting run whose offers carry chords is live: the row's letters are not,
// but the chords are, and they are live from the draft and from reading mode
// standing on some other row alike. So the run is drawn in the treatment that
// says "you can press this", because you can, and no key hands anything over
// — there is nothing left waiting.
//
// A waiting run with no chords is the older shape, and both its states are
// real: with a handover named, the keys go grey beside the one key that makes
// them live; with none, reading mode holds the keyboard with its cursor on
// some other row, so the keys are grey and the row offers nothing, which is
// exactly true.
func keyRun(keys []TurnKey, waiting bool, handover string) string {
	if len(keys) == 0 {
		return ""
	}
	if !waiting {
		return keyOffers(keys)
	}
	if chorded(keys) {
		return keyOffers(asChords(keys))
	}
	if handover == "" {
		return inertOffers(keys)
	}
	return inertOffers(keys) + sty.Dim.Render(" · ") + handoverOffer(handover, handoverWords)
}

// keyRunNarrow is the same run once the terminal has run out of room for the
// keys that are not live yet. It differs only where there is a handover to
// keep: with nothing live in the run there is nothing to prefer, and the keys
// clip like any other field. A chorded run has nothing to prefer either:
// every offer in it is live.
func keyRunNarrow(keys []TurnKey, waiting bool, handover string) string {
	if len(keys) == 0 || !waiting || handover == "" || chorded(keys) {
		return keyRun(keys, waiting, handover)
	}
	return handoverOffer(handover, handoverWord)
}

// KeyRun is that run for a row drawn outside this package. The step outline
// and the backlog run's row live in internal/ui/chat because they group
// history rather than render a widget (AGENTS.md), and invariant 5 is not a
// rule a surface gets to keep a second copy of.
func KeyRun(keys []TurnKey, waiting bool, handover string) string {
	return keyRun(keys, waiting, handover)
}

// KeyRunOption is the sentence such a row appends the first time a session
// offers a chord, or "" where it is not that row.
func KeyRunOption(keys []TurnKey, waiting, option bool) string {
	if !namesTheOption(keys, waiting, option) {
		return ""
	}
	return optionLine()
}
