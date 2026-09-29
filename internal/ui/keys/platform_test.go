package keys

// The keyboard shhh ships, against the rules the register holds a keymap
// file to, and against the one thing that made a keyboard per platform
// necessary: an alt chord, which a stock Mac terminal does not deliver.

import (
	"strings"
	"testing"
)

// The shipped keyboard passes the five rules a keymap file is refused on —
// destructive acts off movement keys, no bare key at the draft, pairs whole,
// no reserved chord, one act per keystroke per surface.
func TestShippedKeyboard_KeepsTheFiveRules(t *testing.T) {
	if err := check(); err != nil {
		t.Fatalf("the shipped keyboard breaks a rule: %v", err)
	}
}

// Every surface positioned at the input is one keyboard on the screen: all of
// them are live while the draft is. So a keystroke answered by one of them
// may not also answer for another — the check a per-surface rule cannot make,
// asked of them together.
func TestShippedKeyboard_TheInputSurfacesShareNoKeystroke(t *testing.T) {
	seen := map[string]string{}
	for _, s := range Surfaces() {
		if s.Position != Home {
			continue
		}
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if prev, ok := seen[k]; ok && prev != Words(b) {
					t.Errorf("%q is both %q and %q while the draft holds the keyboard", k, prev, Words(b))
				}
				seen[k] = Words(b)
			}
		}
	}
}

// Nothing shhh ships answers an alt chord, on any platform: the stock Mac
// terminals compose a character for Option until a profile is changed, so an
// alt chord there is an offer that does nothing, and one keyboard for every
// desk holds only while this does
// (docs/interface/reserved-keys.md#a-mac-ships-without-alt).
func TestShippedKeyboard_ShipsNoAltChord(t *testing.T) {
	for _, s := range all() {
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if strings.Contains(k, "alt+") {
					t.Errorf("%s: %q (%s) ships on alt", s.Name, k, Words(b))
				}
			}
			if strings.Contains(Shown(b), "alt+") {
				t.Errorf("%s: %q (%s) prints an alt chord", s.Name, Shown(b), Words(b))
			}
		}
	}
}

// The shipped keyboard is the declaration itself: nothing is applied over
// it, so what the register says in keys.go is what every terminal answers.
func TestShippedKeyboard_IsTheDeclaration(t *testing.T) {
	for _, g := range Keyboard() {
		for _, a := range g.Acts {
			if a.Moved() {
				t.Errorf("%s answers %v, not the %v it was declared with", a.Name, a.Keys, a.Shipped)
			}
		}
	}
}

// A person's file is applied over the shipped keyboard: the screen groups
// move, a draft chord may go back on alt, and the refusals fire.
func TestShippedKeyboard_AFileMovesIt(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[sources]\nlist = \"i\"\n[backlog]\nnew = \"a\"\n[draft]\nagents = \"alt+a\"\n")
	if err := Load(path); err != nil {
		t.Fatalf("a file moving a screen key and a draft chord was refused: %v", err)
	}
	if !Is("i", Sources.List) || !Is("a", Backlog.New) || !Is("alt+a", Draft.Agents) {
		t.Errorf("the moves did not land: %v %v %v", Sources.List.Keys(), Backlog.New.Keys(), Draft.Agents.Keys())
	}
	for _, refused := range []struct{ body, says string }{
		{"[backlog]\nmove = \"up\"\n", "pairs"},
		{"[backlog]\ndrop = \"j\"\n", "moves the cursor"},
		{"[draft]\nagents = \"ctrl+b\"\n", "tmux"},
	} {
		if err := Load(keymapFile(t, refused.body)); err == nil || !strings.Contains(err.Error(), refused.says) {
			t.Errorf("%q: want a refusal saying %q, got %v", refused.body, refused.says, err)
		}
	}
}

// Check judges a file against the shipped keyboard: a file moving the agent
// manager onto the walk's own key is refused by that key's name, and the
// register is left as it was.
func TestShippedKeyboard_CheckIsAskedOfTheShippedKeyboard(t *testing.T) {
	path := keymapFile(t, "[draft]\nagents = \"shift+f8\"\n")
	if _, err := Check(path); err == nil || !strings.Contains(err.Error(), "the next session") {
		t.Errorf("shift+f8 is the next session; want a refusal naming it, got %v", err)
	}
	if !Is("f12", Draft.Agents) {
		t.Errorf("Check left the register moved: %v", Draft.Agents.Keys())
	}
}
