package chat

// The session's key rows against the register (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// /help's columns are held to the register beside the text they head
// (help_test.go); this is the same check for the three rows a reader meets
// without asking for a list — the palette's, the completion menu's and the
// plan card's. Each was written out by hand once, and the failure that
// invites is not a compile error: the hint keeps offering `tab` after the
// keymap file has moved it, and the row is the only thing a reader has to go
// on.
//
// What is checked is the spelling and not the words. The words are the
// surface's — "complete" is what tab does to the input in the palette, which
// the register says at length — and a key row that could not say something
// more specific than the declaration would be a worse row.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

// bracketed is every `[…]` run in a hint row. The brackets are the notation a
// live key is written in, so what they hold is what has to be a declaration.
var bracketed = regexp.MustCompile(`\[([^\]]+)\]`)

// shownKeys is every spelling the register offers, from both halves of it:
// the session's surfaces and the programs beside it.
func shownKeys() map[string]bool {
	shown := map[string]bool{}
	for _, s := range append(keys.Surfaces(), keys.Programs()...) {
		for _, b := range s.Bindings {
			shown[keys.Shown(b)] = true
		}
	}
	return shown
}

// declared reports that a bracketed run is a declaration's own spelling. A
// pair printed as one run passes on its halves — `[↑↓]` is two bindings on
// most surfaces and one on the rest — and so does a run of two keys joined by
// a slash.
func declared(token string, shown map[string]bool) bool {
	if shown[token] {
		return true
	}
	parts := strings.Split(token, "/")
	if len(parts) > 1 {
		for _, p := range parts {
			if !shown[p] {
				return false
			}
		}
		return true
	}
	r := []rune(token)
	if len(r) == 2 && shown[string(r[0])] && shown[string(r[1])] {
		return true
	}
	return false
}

func checkKeyRow(t *testing.T, surface string, segments []string, shown map[string]bool) {
	t.Helper()
	for _, seg := range segments {
		for _, m := range bracketed.FindAllStringSubmatch(seg, -1) {
			if !declared(m[1], shown) {
				t.Errorf("%s offers %q in %q, which no binding is spelled",
					surface, m[1], seg)
			}
		}
	}
}

func TestKeyRowsComeFromTheRegister(t *testing.T) {
	shown := shownKeys()

	checkKeyRow(t, "the palette", paletteHint(), shown)
	checkKeyRow(t, "the plan card", planHint(), shown)

	// The menu says three different things depending on what it is
	// completing, and each of the three is a row a reader is shown.
	var m Model
	checkKeyRow(t, "the completion menu", m.completionHint(), shown)
	m.complete.files = true
	checkKeyRow(t, "the completion menu over files", m.completionHint(), shown)
	m.complete.files, m.complete.arg = false, true
	if !m.completionRunsInput() {
		t.Fatal("an argument menu on an empty token is the row enter runs the line from")
	}
	checkKeyRow(t, "the completion menu over arguments", m.completionHint(), shown)
}

// And every one of those rows brackets its keys. A row that spelled a live
// key bare would be the one row in the product a reader has to learn a second
// notation for.
func TestKeyRowsBracketTheirKeys(t *testing.T) {
	var m Model
	rows := map[string][]string{
		"the palette":         paletteHint(),
		"the plan card":       planHint(),
		"the completion menu": m.completionHint(),
	}
	for surface, segments := range rows {
		for _, seg := range segments {
			if !strings.HasPrefix(seg, "[") {
				t.Errorf("%s offers %q, which does not open with a bracketed key", surface, seg)
			}
		}
	}
}
