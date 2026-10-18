package components

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// The closed list of words esc is glossed with
// (docs/interface/principles.md#esc-is-always-the-safe-answer): `back`,
// `keeps waiting`, and the staged-work question, which a foot says as `back —
// asks first`. Either word may take a clause after ` — ` saying what is left
// as it was; the clause adds to the word and never replaces it.

// escClause is what separates the word from the clause that follows it.
const escClause = " — "

// EscGlossOnList reports whether a gloss beside esc is one of the closed
// list, with or without a clause saying what is left as it was.
func EscGlossOnList(gloss string) bool {
	for _, word := range []string{EscBack, EscWaits} {
		if gloss == word || strings.HasPrefix(gloss, word+escClause) {
			return true
		}
	}
	return false
}

// safeGlossSeen is the one test hook in the key rows: the function a test
// binary asks to hear every esc gloss a row paints. Nil outside a test.
var safeGlossSeen atomic.Pointer[func(string)]

// pendingEscRulings are the glosses of an esc that does a different act, each
// waiting on the person's ruling: call it one of the closed list because it is
// in fact the safe answer, or say esc should not carry the act. Nothing else is
// exempt from the audit, and a ruling removes its row.
var pendingEscRulings = []string{
	// esc ends a running act
	"stop",
	"stop, keeping every answer already given",
	"stop drafting",
	"stop drafting · every section keeps its last text",
	"stop drafting · the section keeps its last text",
	"stop · the profile stays as the file has it",
	"cancel the sign-in",
	// esc throws the draft away
	"discard the draft",
	"drop it",
	"don't remember it",
	// esc undoes a revision
	"take back its last revision",
	"take it back from every section it changed",
	// esc folds what is open
	"collapse",
	// esc clears the filter first
	"clear it",
	"clear the filter",
	"clear the filter, then close it",
	"clear, then close",
}

// AuditEscGlosses is how a test binary holds every rendered esc segment to the
// closed list: it starts listening to the glosses key rows paint and returns
// the verdict, the glosses heard that are neither on the list nor awaiting a
// ruling.
func AuditEscGlosses() (verdict func() []string) {
	exempt := pendingEscRulings
	var mu sync.Mutex
	seen := map[string]bool{}
	hear := func(g string) {
		mu.Lock()
		seen[g] = true
		mu.Unlock()
	}
	safeGlossSeen.Store(&hear)
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		var bad []string
		for g := range seen {
			if EscGlossOnList(g) {
				continue
			}
			named := false
			for _, e := range exempt {
				if g == e {
					named = true
				}
			}
			if !named {
				bad = append(bad, g)
			}
		}
		sort.Strings(bad)
		return bad
	}
}

// heardGloss tells the audit, if one is listening, which gloss a safe
// segment is painted with.
func heardGloss(o KeyOffer) {
	if !o.Safe || o.Key == "" || o.Label == "" {
		return
	}
	if f := safeGlossSeen.Load(); f != nil {
		(*f)(o.Label)
	}
}
