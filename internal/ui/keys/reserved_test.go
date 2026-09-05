package keys

// The reserved list against the register it guards, and against the keymap
// file it refuses.

import (
	"os"
	"strings"
	"testing"
)

// The keyboard shhh ships spends no chord the desktop, the terminal or a
// multiplexer takes — unless it says why.
func TestRegisterSpendsNoReservedChord(t *testing.T) {
	for _, s := range all() {
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				r, ok := Reservation(k)
				if !ok {
					continue
				}
				if _, earned := Kept(k, Words(b)); !earned {
					t.Errorf("%s: %q (%s) is %s's on %s and is not in kept", s.Name, k, Words(b), r.Taker, r.On)
				}
			}
		}
	}
}

// Every kept entry is a chord the register actually spends, is actually
// reserved, and carries a sentence: a stale entry would be a hole in the
// guard, and a bare one a hole in the reason.
func TestKeptChordsAreSpentReservedAndReasoned(t *testing.T) {
	spent := map[string]map[string]bool{}
	for _, s := range all() {
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if spent[k] == nil {
					spent[k] = map[string]bool{}
				}
				spent[k][Words(b)] = true
			}
		}
	}
	for k, why := range kept {
		if len(spent[k]) == 0 || len(shipped[k]) == 0 {
			t.Errorf("kept names %q, which nothing in the register spends", k)
		}
		if _, ok := Reservation(k); !ok {
			t.Errorf("kept names %q, which is not reserved", k)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("kept has no reason for %q", k)
		}
	}
}

// Every reserved chord is spelled the way the decoder spells a keystroke —
// lower case, modifiers in the order ctrl, alt, shift — or the refusal
// would never match what a file wrote.
func TestReservedChordsAreSpelledAsKeystrokes(t *testing.T) {
	order := map[string]int{"ctrl": 0, "alt": 1, "shift": 2}
	seen := map[string]bool{}
	for _, r := range reserved {
		if seen[r.Key] {
			t.Errorf("%q is reserved twice", r.Key)
		}
		seen[r.Key] = true
		if r.Key != strings.ToLower(r.Key) || strings.Contains(r.Key, " ") {
			t.Errorf("%q is not spelled as a keystroke", r.Key)
		}
		// The plus key spells as a trailing "+", which a split on "+"
		// would read as an empty modifier: the key is taken off first.
		mods := r.Key
		if strings.HasSuffix(mods, "++") {
			mods = strings.TrimSuffix(mods, "++") + "+plus"
		}
		parts := strings.Split(mods, "+")
		last := -1
		for _, mod := range parts[:len(parts)-1] {
			at, ok := order[mod]
			if !ok || at <= last {
				t.Errorf("%q: modifiers are not in the order ctrl, alt, shift", r.Key)
				break
			}
			last = at
		}
		if r.Taker == "" || r.On == "" {
			t.Errorf("%q names no taker or no platform", r.Key)
		}
	}
}

func TestLoad_RefusesAChordTheDesktopTakes(t *testing.T) {
	restoreRegister(t)
	err := Load(keymapFile(t, "[reading]\ncopy = \"ctrl+up\"\n"))
	if err == nil {
		t.Fatal("a chord Mission Control takes should be refused")
	}
	for _, want := range []string{"\"ctrl+up\"", "Mission Control", "macOS", "reserved-keys.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if !Is("y", Reading.Copy) {
		t.Errorf("a refused file left the register at %v", Reading.Copy.Keys())
	}
}

func TestLoad_RefusesAChordTheTerminalTakes(t *testing.T) {
	restoreRegister(t)
	err := Load(keymapFile(t, "[reading]\ncopy = \"ctrl+shift+t\"\n"))
	if err == nil || !strings.Contains(err.Error(), "Windows Terminal") {
		t.Fatalf("a chord the terminal takes should be refused naming the terminal, got %v", err)
	}
}

func TestLoad_RefusesAMultiplexersPrefix(t *testing.T) {
	restoreRegister(t)
	err := Load(keymapFile(t, "[reading]\ncopy = \"ctrl+b\"\n"))
	if err == nil || !strings.Contains(err.Error(), "tmux") {
		t.Fatalf("tmux's prefix should be refused naming tmux, got %v", err)
	}
}

func TestLoad_RefusesTheLineDisciplinesBytes(t *testing.T) {
	restoreRegister(t)
	err := Load(keymapFile(t, "[reading]\ncopy = \"ctrl+s\"\n"))
	if err == nil || !strings.Contains(err.Error(), "flow control") {
		t.Fatalf("flow control should be refused naming it, got %v", err)
	}
	err = Load(keymapFile(t, "[reading]\ncopy = \"ctrl+shift+x\"\n"))
	if err == nil || !strings.Contains(err.Error(), "ctrl+x") {
		t.Fatalf("a shifted ctrl letter is the unshifted one without the protocol, got %v", err)
	}
}

// The exemption is the shipped act's alone: a file that moves some other
// act onto a kept chord is refused like any other.
func TestLoad_RefusesMovingAnotherActOntoAKeptChord(t *testing.T) {
	restoreRegister(t)
	for _, chord := range []string{"ctrl+z", "ctrl+v", "ctrl+space"} {
		err := Load(keymapFile(t, "[reading]\ncopy = \""+chord+"\"\n"))
		if err == nil || !strings.Contains(err.Error(), chord) {
			t.Errorf("%s is kept for one act only, got %v", chord, err)
		}
		if !Is("y", Reading.Copy) {
			t.Errorf("a refused file left the register at %v", Reading.Copy.Keys())
		}
	}
}

// A modifier on the navigation row is reported everywhere and taken by
// nobody, which is where the three-key chords live.
func TestLoad_AppliesAChordNobodyTakes(t *testing.T) {
	restoreRegister(t)
	if err := Load(keymapFile(t, "[reading]\ncopy = \"alt+pgup\"\n")); err != nil {
		t.Fatalf("alt+pgup should be free: %v", err)
	}
	if !Is("alt+pgup", Reading.Copy) {
		t.Errorf("the move did not land: %v", Reading.Copy.Keys())
	}
}

// reservedDoc is the document the inventory lives in, from this package.
const reservedDoc = "../../../docs/interface/reserved-keys.md"

func updatingDocs() bool { return os.Getenv("SHHH_UPDATE_DOCS") != "" }

func TestReference_ReservedKeysAreCurrent(t *testing.T) {
	stale, err := WriteReservedReference(reservedDoc, updatingDocs())
	if err != nil {
		t.Fatal(err)
	}
	if stale && !updatingDocs() {
		t.Errorf("%s no longer matches the reserved table — run: make docs", reservedDoc)
	}
}

func TestReference_HoldsEveryTierAndEveryKeptChord(t *testing.T) {
	ref := ReservedReference()
	for _, want := range []string{"### Tier A", "### Tier B", "### Tier C", "### Tier D", "### Kept on purpose", "`ctrl+up`", "Mission Control"} {
		if !strings.Contains(ref, want) {
			t.Errorf("the reference lacks %q", want)
		}
	}
	for k := range kept {
		if !strings.Contains(ref, "`"+k+"`") {
			t.Errorf("kept chord %q is not in the reference", k)
		}
	}
}
