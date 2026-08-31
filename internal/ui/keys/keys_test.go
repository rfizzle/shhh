package keys

// The register against itself (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The register's rule is that "a rule nobody can check against a list is a
// rule each new surface gets to rediscover". These are the checks the list
// makes possible: that every binding is in it, that no surface answers one
// keystroke twice, and that nothing on it is half-declared.

import (
	"strings"
	"testing"
)

// all is every surface in the product, chat and standalone alike.
func all() []Surface { return append(Surfaces(), Programs()...) }

// TestEveryBindingIsDeclaredWhole catches the half-written binding: keys with
// no words is a key no hint can offer, and words with no keys is an offer no
// handler answers. Either one is the drift this package exists to stop.
func TestEveryBindingIsDeclaredWhole(t *testing.T) {
	for _, s := range all() {
		for _, b := range s.Bindings {
			if len(b.Keys()) == 0 {
				t.Errorf("%s: a binding with no keystrokes (%q)", s.Name, Words(b))
			}
			if Shown(b) == "" || Words(b) == "" {
				t.Errorf("%s: %v has no help text", s.Name, b.Keys())
			}
		}
	}
}

// TestNoSurfaceAnswersOneKeystrokeTwice is the check the register could not
// make on a markdown table. Two bindings on one surface claiming the same
// keystroke is a surface where the first case in a switch silently wins —
// which is how [a] and [A] would have drifted apart on the approval card.
func TestNoSurfaceAnswersOneKeystrokeTwice(t *testing.T) {
	for _, s := range all() {
		seen := map[string]string{}
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if prev, ok := seen[k]; ok {
					t.Errorf("%s: %q is claimed by both %q and %q", s.Name, k, prev, Shown(b))
				}
				seen[k] = Shown(b)
			}
		}
	}
}

// TestShownIsAKeyOrASpellingOfOne guards the other direction: a hint may
// print `j/k` for four keystrokes and `↑` for one, but it may not print a key
// the binding does not answer. Where the spelling is not one of the
// keystrokes it has to be built from them, so every character of it that
// looks like a key is one.
func TestShownIsAKeyOrASpellingOfOne(t *testing.T) {
	// The spellings that are a composite of the binding's own keystrokes, or
	// the glyph the whole product draws for one. Each is checked against the
	// keys it stands for rather than waved through.
	// The spellings a hint prints that are not the keystroke itself: the
	// glyphs the whole product draws for the arrow and return keys, and the
	// two-letter abbreviation every key row in shhh has always used for the
	// pager keys.
	alias := map[string]string{
		"↑": "up", "↓": "down", "←": "left", "→": "right",
		"↵": "enter", "space": " ",
		"pgdn": "pgdown", "pgup": "pgup",
	}
	for _, s := range all() {
		for _, b := range s.Bindings {
			shown := Shown(b)
			ok := false
			for _, k := range b.Keys() {
				if shown == k {
					ok = true
				}
			}
			if ok {
				continue
			}
			// A composite. Every part of it — split on the slash, and then
			// on the character where a part is a run of single-character
			// keys like `jk` or `↑↓` — has to be one of the keystrokes.
			parts := strings.Split(shown, "/")
			ok = len(parts) > 0
			for _, part := range parts {
				ok = ok && covers(b.Keys(), part, alias)
			}
			if !ok {
				t.Errorf("%s: %q is shown as %q, which is not a spelling of its keys",
					s.Name, b.Keys(), shown)
			}
		}
	}
}

// TestEveryDeclaredBindingIsOnASurface is the register's completeness check:
// a binding declared in this package and left off every surface is a key
// nothing lists, which is exactly the state the register was written to end.
// covers reports whether a shown part names keys the binding answers: the
// part whole, or every character of it read as a key of its own.
func covers(bound []string, part string, alias map[string]string) bool {
	named := func(k string) bool {
		if a, ok := alias[k]; ok {
			k = a
		}
		for _, b := range bound {
			if b == k {
				return true
			}
		}
		return false
	}
	// The part whole, with any glyph inside it read as the key it draws —
	// `shift+↑` is the shift key and the up key, spelled the way every hint
	// row in the product spells it.
	spelled := part
	for glyph, key := range alias {
		if len([]rune(glyph)) == 1 {
			spelled = strings.ReplaceAll(spelled, glyph, key)
		}
	}
	if named(part) || named(spelled) {
		return true
	}
	// Or a run of single-character keys: `jk`, `↑↓`.
	for _, r := range part {
		if !named(string(r)) {
			return false
		}
	}
	return part != ""
}

func TestEveryDeclaredBindingIsOnASurface(t *testing.T) {
	on := map[string]bool{}
	for _, s := range all() {
		for _, b := range s.Bindings {
			on[strings.Join(b.Keys(), ",")+"|"+Shown(b)] = true
		}
	}
	declared := []struct {
		group string
		bs    []Binding
	}{
		{"Draft", []Binding{Draft.Send, Draft.Newline, Draft.FollowUp,
			Draft.PullQueued, Draft.Editor, Draft.Attach,
			Draft.Complete, Draft.Palette, Draft.Reasoning, Draft.Mode,
			Draft.HistoryPrev, Draft.HistoryNext, Draft.HistorySearch,
			Draft.ScrollUp, Draft.ScrollDown, Draft.PageUp, Draft.PageDown, Draft.Reading,
			Draft.Agents, Draft.Mouse, Draft.KeyList, Draft.Suspend, Draft.Redraw,
			Draft.Answer, Draft.Clear,
			Draft.Cancel, Draft.Quit}},
		{"Search", []Binding{Search.Older, Search.Keep, Search.Cancel}},
		{"Reading", Reading.All()},
		{"Context", Context.All()},
		{"Row", []Binding{Row.Review, Row.Undo, Row.Retry, Row.Continue, Row.Key,
			Row.Provider, Row.Rounds, Row.Uncap}},
		{"Decision", []Binding{Decision.Allow, Decision.Deny, Decision.Always,
			Decision.Batch, Decision.Diff, Decision.ScrollUp, Decision.ScrollDown,
			Decision.PanLeft, Decision.PanRight}},
		{"Confirm", []Binding{Confirm.Yes, Confirm.No, Confirm.Force}},
		{"Select", []Binding{Select.Move, Select.MoveJK, Select.Take, Select.Alt,
			Select.Filter, Select.ClearQ, Select.Toggle, Select.All, Select.Note,
			Select.Delete, Select.Rename, Select.Cancel, Select.Palette.Prev, Select.Palette.Next,
			Select.Palette.Run, Select.Palette.Write}},
		{"Review", []Binding{Review.MoveFile, Review.MoveHunk, Review.StageHunk,
			Review.StageFile, Review.StageAll, Review.SideBySide, Review.PageUp,
			Review.PageDown, Review.Apply, Review.Back}},
		{"Agent", []Binding{Agent.Move, Agent.Attach, Agent.Answer, Agent.Retry,
			Agent.Cancel, Agent.Kill, Agent.Back}},
		{"Wait", []Binding{Wait.Fallback, Wait.Stop, Wait.Compact, Wait.NewSession,
			Wait.KeepGoing, Wait.UseKey, Wait.KeepKey}},
		{"Diff", []Binding{Diff.Scroll, Diff.Hunk, Diff.SideBySide, Diff.Back, Diff.Leave}},
		{"Output", []Binding{Output.Scroll, Output.PageUp, Output.PageDown,
			Output.Collapse, Output.Back, Output.Leave}},
		{"Preview", []Binding{Preview.Back, Preview.Leave}},
		{"Screen", []Binding{Screen.Move, Screen.Take, Screen.Filter, Screen.ClearQ,
			Screen.List, Screen.Quit, Screen.Reset, Screen.Write, Screen.Keep,
			Screen.Copy, Screen.Rerun, Screen.Snippet, Screen.Delete, Screen.Fix,
			Screen.Again}},
		{"OneShot", []Binding{OneShot.Run, OneShot.Confirm,
			OneShot.Step, OneShot.DryRun, OneShot.Edit, OneShot.Revise, OneShot.Back,
			OneShot.Alternatives, OneShot.Explain, OneShot.Copy, OneShot.Save,
			OneShot.Quit}},
		{"Setup", []Binding{Setup.Wizard, Setup.Paste, Setup.Local}},
		{"Browse", []Binding{Browse.Move, Browse.Open, Browse.Filter, Browse.Delete,
			Browse.Rename, Browse.Action,
			Browse.Prev, Browse.Take, Browse.Back, Browse.Quit, Browse.Leave}},
	}
	for _, g := range declared {
		for _, b := range g.bs {
			if !on[strings.Join(b.Keys(), ",")+"|"+Shown(b)] {
				t.Errorf("%s: %q (%s) is declared but on no surface", g.group, Shown(b), Words(b))
			}
		}
	}
}

// TestReadlineChordsStayWithTheTextarea holds the draft to the shell's own
// line editing: the chords readline spends on the line are declared on no
// surface, so they reach the textarea, which binds all three by default.
func TestReadlineChordsStayWithTheTextarea(t *testing.T) {
	for _, freed := range []string{"ctrl+a", "ctrl+e", "ctrl+k"} {
		for _, s := range all() {
			for _, b := range s.Bindings {
				for _, k := range b.Keys() {
					if k == freed {
						t.Errorf("%s: %q is claimed by %q; it belongs to the textarea",
							s.Name, freed, Shown(b))
					}
				}
			}
		}
	}
}

// TestRealignedChordsHaveOneHome pins the realignment: the editor, reading
// mode and the decision handover each answer on exactly one surface, so none
// of them can quietly grow a second meaning the way the freed chords had.
func TestRealignedChordsHaveOneHome(t *testing.T) {
	for _, chord := range []string{"ctrl+g", "ctrl+o", "ctrl+space"} {
		var homes []string
		for _, s := range all() {
			for _, b := range s.Bindings {
				for _, k := range b.Keys() {
					if k == chord {
						homes = append(homes, s.Name)
					}
				}
			}
		}
		if len(homes) != 1 {
			t.Errorf("%q is bound on %d surfaces (%v), want exactly one", chord, len(homes), homes)
		}
	}
}

// TestReadingRowKeysHaveOneHome pins reading mode's bare letters the way the
// realigned chords are pinned: the copy and half-page bindings belong to
// reading mode and to no other surface. The keystrokes themselves have other
// lives — [y] answers a card, [d] marks a selector's default — but those are
// other surfaces' bindings; these two may not quietly grow a second home.
func TestReadingRowKeysHaveOneHome(t *testing.T) {
	for _, b := range []Binding{Reading.Copy, Reading.Half} {
		sig := strings.Join(b.Keys(), ",") + "|" + Shown(b) + "|" + Words(b)
		var homes []string
		for _, s := range all() {
			for _, sb := range s.Bindings {
				if strings.Join(sb.Keys(), ",")+"|"+Shown(sb)+"|"+Words(sb) == sig {
					homes = append(homes, s.Name)
				}
			}
		}
		if len(homes) != 1 || homes[0] != "reading mode" {
			t.Errorf("%q is bound on %v, want reading mode alone", Shown(b), homes)
		}
	}
}

// TestSurfacesAreNamedAndPlaced keeps the register readable as a table:
// every row says what it is, which section is normative for it, and how it
// gets the keyboard.
func TestSurfacesAreNamedAndPlaced(t *testing.T) {
	for _, s := range all() {
		if s.Name == "" || s.Section == "" || s.Reached == "" || len(s.Bindings) == 0 {
			t.Errorf("incomplete surface: %+v", s.Name)
		}
	}
}

// TestOnlyTakeoversHoldBareLetters is invariant 5 asked of the register
// . A surface that does not hold the keyboard may not answer a bare
// letter without the register also declaring the key that hands the keyboard
// over — which for both such surfaces is a chord no sentence can produce.
func TestOnlyTakeoversHoldBareLetters(t *testing.T) {
	handover := map[string]bool{
		Shown(Draft.Reading): true,
		Shown(Draft.Answer):  true,
	}
	for _, s := range all() {
		if s.Position != Beside {
			continue
		}
		named := false
		for h := range handover {
			named = named || strings.Contains(s.Reached, h)
		}
		if !named {
			t.Errorf("%s does not hold the keyboard and names no handover key (reached: %q)",
				s.Name, s.Reached)
		}
	}
}
