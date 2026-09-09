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

	"github.com/rfizzle/shhh/internal/ui/components"
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

// offerRow is a run of offers as the sentences the check reads, which is the
// one place the two halves of an offer are joined back up.
func offerRow(offers []components.KeyOffer) []string {
	out := make([]string, 0, len(offers))
	for _, o := range offers {
		out = append(out, o.Key+" "+o.Label)
	}
	return out
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

	checkKeyRow(t, "the palette", offerRow(paletteHint()), shown)
	checkKeyRow(t, "the plan card", offerRow(planHint()), shown)

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
		"the palette":         offerRow(paletteHint()),
		"the plan card":       offerRow(planHint()),
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

// The frame's own rails are the same row again, and the one a reader meets
// first: every state it swaps through offers its keys in the brackets, and
// nothing on it is a bare spelling.
func TestFrameRailsBracketEveryKeyTheyOffer(t *testing.T) {
	shown := shownKeys()
	m := frameModel(t, 200, 40)
	rails := map[string]string{"the idle rail": stripANSI(m.frameHints(200))}

	m.state = stateStreaming
	rails["the streaming rail"] = stripANSI(m.frameHints(200))

	held := heldModel(t)
	held.width, held.height = 200, 40
	rails["the held rail"] = stripANSI(held.frameHints(200))

	attached := frameModel(t, 200, 40)
	attached.attachedTo = "researcher-1"
	rails["the attached rail"] = stripANSI(attached.frameHints(200))

	for surface, rail := range rails {
		checkKeyRow(t, surface, strings.Split(rail, " · "), shown)
		for _, seg := range strings.Split(rail, " · ") {
			if !strings.HasPrefix(seg, "[") {
				t.Errorf("%s offers %q, which does not open with a bracketed key", surface, seg)
			}
		}
	}
}

// A key that acts only on its second press says so before the first. The
// window that opens after that press names the same key in the same
// notation, so the two readings of one chord cannot look like two keys.
func TestFrameRailsStateTheSecondPressBeforeTheFirst(t *testing.T) {
	m := frameModel(t, 200, 40)
	if want := keys.Bracket(keys.Draft.Quit) + " ×2 quit"; !strings.Contains(stripANSI(m.frameHints(200)), want) {
		t.Errorf("the idle rail should offer %q, got %q", want, stripANSI(m.frameHints(200)))
	}
	m.state = stateStreaming
	if want := keys.Bracket(keys.Draft.Cancel) + " ×2 stop the run"; !strings.Contains(stripANSI(m.frameHints(200)), want) {
		t.Errorf("the working rail should offer %q, got %q", want, stripANSI(m.frameHints(200)))
	}
}
