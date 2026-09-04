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
			Draft.Agents, Draft.Backlog, Draft.NextAgent, Draft.PrevAgent,
			Draft.Mouse, Draft.KeyList, Draft.Suspend, Draft.Redraw,
			Draft.Answer, Draft.Clear,
			Draft.Cancel, Draft.Quit}},
		{"Search", []Binding{Search.Older, Search.Keep, Search.Cancel}},
		{"Reading", Reading.All()},
		{"Find", Find.All()},
		{"Context", Context.All()},
		{"Backlog", Backlog.All()},
		{"Row", []Binding{Row.Review, Row.Undo, Row.Retry, Row.Continue, Row.Key,
			Row.Provider, Row.Rounds, Row.Uncap}},
		{"Decision", []Binding{Decision.Allow, Decision.Deny, Decision.Refuse,
			Decision.Always, Decision.Batch, Decision.Diff,
			Decision.ScrollUp, Decision.ScrollDown,
			Decision.PanLeft, Decision.PanRight}},
		{"Confirm", []Binding{Confirm.Yes, Confirm.No, Confirm.Force}},
		{"Select", []Binding{Select.Move, Select.MoveJK, Select.Take, Select.Alt,
			Select.Filter, Select.ClearQ, Select.Toggle, Select.All, Select.Note,
			Select.Delete, Select.Rename, Select.Cancel, Select.Palette.Prev, Select.Palette.Next,
			Select.Palette.Run, Select.Palette.Write}},
		{"Review", []Binding{Review.MoveFile, Review.MoveHunk, Review.StageHunk,
			Review.StageFile, Review.StageAll, Review.SideBySide, Review.PageUp,
			Review.PageDown, Review.Apply, Review.Back}},
		{"Profile", []Binding{Profile.Move, Profile.Take, Profile.Note,
			Profile.ScrollUp, Profile.ScrollDown, Profile.Back}},
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
			Screen.Again, Screen.Worked, Screen.Failed, Screen.Skip}},
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

// TestMapCycleChordsHaveOneHome pins the pair the rail's session map is
// walked with, the way the realigned chords are pinned. A chord taken from
// the draft is only free if it is free everywhere: a second home for either
// bracket is a surface that answers the keyboard's own movement key with
// something else, on a keystroke no sentence can produce and nobody would
// think to check.
func TestMapCycleChordsHaveOneHome(t *testing.T) {
	for _, chord := range []string{"alt+[", "alt+]"} {
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
		if len(homes) != 1 || homes[0] != "the input" {
			t.Errorf("%q is bound on %v, want the input alone", chord, homes)
		}
	}
}

// TestReadingRowKeysHaveOneHome pins reading mode's bare letters the way the
// realigned chords are pinned: the copy, half-page and search bindings belong
// to reading mode and to no other surface. The keystrokes themselves have
// other lives — [y] answers a card, [d] marks a selector's default, [/] opens
// a filter and [n] steps a hunk — but those are other surfaces' bindings;
// these four may not quietly grow a second home.
func TestReadingRowKeysHaveOneHome(t *testing.T) {
	for _, b := range []Binding{Reading.Copy, Reading.Half, Reading.Search, Reading.Match} {
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

// TestBacklogChordHasOneHome pins the chord the backlog screen was given the
// way the realigned chords are pinned. The register is the only thing that
// can say a chord is free, and "free" has to keep meaning that: a second
// home for this one is a surface answering the keyboard's own door with
// something else, on a keystroke no sentence can produce and nobody would
// think to check.
func TestBacklogChordHasOneHome(t *testing.T) {
	var homes []string
	for _, s := range all() {
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if k == "ctrl+f" {
					homes = append(homes, s.Name)
				}
			}
		}
	}
	if len(homes) != 1 || homes[0] != "the input" {
		t.Errorf("ctrl+f is bound on %v, want the input alone", homes)
	}
}

// The backlog screen is the one list in the product whose pointer does not
// move on j/k, and the reason is that four letters select on it. This holds
// the two apart: a movement binding that grew `k` back would answer the
// kind filter's keystroke as well, and the first case in the switch would
// silently win.
func TestBacklogMovementLeavesTheFilterLetters(t *testing.T) {
	for _, b := range []Binding{Backlog.Status, Backlog.Priority, Backlog.Kind, Backlog.Ready} {
		for _, k := range b.Keys() {
			if Is(k, Backlog.Move) {
				t.Errorf("%q is both %q and the pointer's movement", k, Words(b))
			}
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

// The handover answers to two chords, not one, because the canonical one is
// taken by the desktop on macOS. Both must reach the same act, and the hint
// must still print only the canonical spelling — a hint that offers two
// chords teaches neither.
func TestHandoverHasASecondChord(t *testing.T) {
	if got := Shown(Draft.Answer); got != "ctrl+space" {
		t.Errorf("hint spelling = %q, want ctrl+space", got)
	}
	for _, chord := range []string{"ctrl+space", "ctrl+y"} {
		if !Is(chord, Draft.Answer) {
			t.Errorf("%q does not reach the handover", chord)
		}
	}
}

// And the alias is free: nothing in the draft's own register answers to it,
// so binding it takes no key away from the sentence being typed.
func TestHandoverAliasIsNotClaimedElsewhere(t *testing.T) {
	var homes []string
	for _, s := range all() {
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if k == "ctrl+y" {
					homes = append(homes, s.Name)
				}
			}
		}
	}
	if len(homes) != 1 {
		t.Errorf("ctrl+y is bound on %d surfaces (%v), want exactly one", len(homes), homes)
	}
}
