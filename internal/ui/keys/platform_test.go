package keys

// The keyboard each platform ships, against the rules the register holds a
// keymap file to. The rest of the package's tests run the Linux keyboard —
// the one a test binary starts with — so everything a platform's table could
// break is asked here again of each table in turn.

import (
	"slices"
	"strings"
	"testing"
)

// platforms is every platform with a keyboard of its own, the Linux one
// standing for Windows as well.
var platforms = []string{"linux", "darwin"}

// onPlatform runs f with a platform's shipped keyboard on the register and
// puts the test binary's own back afterwards.
func onPlatform(t *testing.T, platform string, f func(t *testing.T)) {
	t.Helper()
	t.Run(platform, func(t *testing.T) {
		t.Cleanup(UsePlatform(platform))
		f(t)
	})
}

// Each platform's keyboard passes the five rules a keymap file is refused
// on — destructive acts off movement keys, no bare key at the draft, pairs
// whole, no reserved chord, one act per keystroke per surface.
func TestShippedKeyboards_KeepTheFiveRules(t *testing.T) {
	for _, p := range platforms {
		onPlatform(t, p, func(t *testing.T) {
			if err := check(); err != nil {
				t.Fatalf("the %s keyboard breaks a rule: %v", p, err)
			}
		})
	}
}

// The input and a row's chords are two surfaces in the register and one
// keyboard on the screen: both are live while the draft is. So a keystroke
// answered at the input may not also be a row chord, on either keyboard —
// the check a per-surface rule cannot make, asked of the two together.
func TestShippedKeyboards_TheDraftAndTheRowChordsShareNoKeystroke(t *testing.T) {
	for _, p := range platforms {
		onPlatform(t, p, func(t *testing.T) {
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
		})
	}
}

// On a Mac nothing shhh ships answers an alt chord: the stock terminals
// compose a character for Option until a profile is changed, so an alt
// chord there is an offer that does nothing.
func TestShippedKeyboards_AMacShipsNoAltChord(t *testing.T) {
	onPlatform(t, "darwin", func(t *testing.T) {
		for _, s := range all() {
			for _, b := range s.Bindings {
				if NeedsOption(b) {
					t.Errorf("%s: %q (%s) ships on alt on a Mac", s.Name, Shown(b), Words(b))
				}
				if OptionSpelled(Shown(b)) {
					t.Errorf("%s: %q (%s) prints an alt chord on a Mac", s.Name, Shown(b), Words(b))
				}
			}
		}
	})
}

// The Mac's table moves only what was on alt, and every act it moves keeps
// its words: the two keyboards are one register with a different spelling in
// eleven places, not two registers.
func TestShippedKeyboards_TheMacMovesOnlyTheAltChords(t *testing.T) {
	for name, presses := range darwinMoves {
		linux, ok := ShippedOn("linux", name)
		if !ok {
			t.Errorf("the Mac's table names %s, which the register does not have", name)
			continue
		}
		if !slices.ContainsFunc(linux, OptionSpelled) {
			t.Errorf("%s moves on a Mac although it ships %v, which is not on alt", name, linux)
		}
		mac, _ := ShippedOn("darwin", name)
		if !slices.Equal(mac, presses) {
			t.Errorf("%s ships %v on a Mac, want the table's %v", name, mac, presses)
		}
	}
	for _, g := range keyboard(true) {
		for _, a := range g.Acts {
			linux, _ := ShippedOn("linux", a.Name)
			if _, moved := darwinMoves[a.Name]; !moved && slices.ContainsFunc(linux, OptionSpelled) {
				t.Errorf("%s ships %v on alt and the Mac's table does not move it", a.Name, linux)
			}
		}
	}
}

// The Linux keyboard is the declaration itself: nothing is applied over it,
// so what the register says in keys.go is what a Linux terminal answers.
func TestShippedKeyboards_LinuxIsTheDeclaration(t *testing.T) {
	onPlatform(t, "linux", func(t *testing.T) {
		for _, g := range Keyboard() {
			for _, a := range g.Acts {
				if a.Moved() {
					t.Errorf("%s answers %v on Linux, not the %v it was declared with", a.Name, a.Keys, a.Shipped)
				}
				if linux, _ := ShippedOn("linux", a.Name); !slices.Equal(a.Keys, linux) {
					t.Errorf("%s answers %v on Linux, want %v", a.Name, a.Keys, linux)
				}
			}
		}
	})
}

// A Mac's keyboard is what a listing measures a person's file against, so a
// Mac with no file lists nothing as moved even though the table moved
// eleven keys.
func TestShippedKeyboards_TheMacsTableIsNotAMove(t *testing.T) {
	onPlatform(t, "darwin", func(t *testing.T) {
		for _, g := range Keyboard() {
			for _, a := range g.Acts {
				if a.Moved() {
					t.Errorf("%s reads as moved on a Mac with no file: %v against %v", a.Name, a.Keys, a.Shipped)
				}
			}
		}
		if !Is("f5", RowChord.Retry) || Is("alt+r", RowChord.Retry) {
			t.Errorf("the Mac's retry chord answers %v", RowChord.Retry.Keys())
		}
	})
}

// A person's file is applied over the keyboard their platform ships and held
// to the same rules on both: the screen groups move, and the same two
// refusals fire.
func TestShippedKeyboards_AFileMovesTheSameOnBoth(t *testing.T) {
	for _, p := range platforms {
		onPlatform(t, p, func(t *testing.T) {
			restoreRegister(t)
			path := keymapFile(t, "[sources]\nlist = \"i\"\n[backlog]\nnew = \"a\"\n[rowchord]\nretry = \"alt+r\"\n")
			if err := Load(path); err != nil {
				t.Fatalf("a file moving a screen key and a row chord was refused: %v", err)
			}
			if !Is("i", Sources.List) || !Is("a", Backlog.New) || !Is("alt+r", RowChord.Retry) {
				t.Errorf("the moves did not land: %v %v %v", Sources.List.Keys(), Backlog.New.Keys(), RowChord.Retry.Keys())
			}
			for _, refused := range []struct{ body, says string }{
				{"[backlog]\nmove = \"up\"\n", "pairs"},
				{"[backlog]\ndrop = \"j\"\n", "moves the cursor"},
				{"[rowchord]\nretry = \"ctrl+b\"\n", "tmux"},
			} {
				if err := Load(keymapFile(t, refused.body)); err == nil || !strings.Contains(err.Error(), refused.says) {
					t.Errorf("%q: want a refusal saying %q, got %v", refused.body, refused.says, err)
				}
			}
		})
	}
}

// Check judges a file against the keyboard this platform ships: a Mac file
// moving a row chord back onto alt is read as a move of the Mac's key.
func TestShippedKeyboards_CheckIsAskedOfThisPlatformsKeyboard(t *testing.T) {
	onPlatform(t, "darwin", func(t *testing.T) {
		path := keymapFile(t, "[rowchord]\nretry = \"f6\"\n")
		if _, err := Check(path); err == nil || !strings.Contains(err.Error(), "continue from here") {
			t.Errorf("f6 is the Mac's continue; want a refusal naming it, got %v", err)
		}
		if !Is("f5", RowChord.Retry) {
			t.Errorf("Check left the register moved: %v", RowChord.Retry.Keys())
		}
	})
}
