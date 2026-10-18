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

// AuditEscGlosses is how a test binary holds every rendered esc segment to the
// closed list: it starts listening to the glosses key rows paint and returns
// the verdict, the glosses heard that are not on the list.
func AuditEscGlosses() (verdict func() []string) {
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
			bad = append(bad, g)
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
