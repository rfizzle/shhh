package keys

// The keyboard shhh ships is one keyboard per platform
// (docs/interface/reserved-keys.md#a-mac-ships-without-alt).
//
// keys.go declares it once, and on Linux and Windows that declaration is
// what ships. On macOS it is not: the two stock terminals send Option+letter
// as the character it composes until a profile setting is ticked, so every
// alt chord in the declaration is dead there by default, and a hint offering
// one is a false offer on the desktop shhh is most often run on. So a Mac
// ships the same acts with the alt ones moved to the function row, which
// every terminal delivers with nothing set — plain F keys for the offers a
// reader meets most, shift on them for the rest, and the reasoning level's
// alt alias simply dropped, since its ctrl chord already works.
//
// The table is a keymap file the program carries: the same dotted names, the
// same apply, the same five rules. It is applied once, before anything reads
// the register, and what it leaves is what "shipped" means on this machine —
// the keyboard a person's own keybindings.toml is applied over and measured
// against, and the one `shhh keys` marks a moved key beside. Hints and
// handlers read one declaration either way, so they move together.

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// darwinMoves is the Mac's keyboard, as the moves it makes over the one
// keys.go declares. Only the alt chords move; every other key is the same on
// both, which is what keeps the two keyboards one register rather than two.
//
// Why the function row and not ctrl: every ctrl letter the terminal delivers
// is already spent at the input or is the line editor's, and the free set
// that is left is the function keys and modified navigation keys
// (docs/interface/reserved-keys.md#what-is-left) — the same reason the alt
// chords were alt in the first place. The draft's shift arrows are the
// pointer's, so the function row it is.
var darwinMoves = map[string][]string{
	// The alias is dropped rather than moved: ctrl+t is the chord, and the
	// alias only ever existed for the readers whose alt arrives.
	"draft.reasoning": {"ctrl+t"},

	// The agent family, at the right-hand end of the row: the manager on the
	// plain key, and the walk on two neighbours under shift, back first.
	"draft.agents":     {"f12"},
	"draft.prev_agent": {"shift+f7"},
	"draft.next_agent": {"shift+f8"},
	"draft.open_paste": {"shift+f12"},
}

// PlatformEnv names the platform whose keyboard this process ships, in place
// of the one it runs on. It exists for two readers: the scene harness, which
// drives the binary on whatever host it is given and whose snaps wait for the
// Linux spelling, and a person on a Mac who has ticked the Option setting and
// would rather have the alt keyboard.
const PlatformEnv = "SHHH_KEYS_PLATFORM"

// Platform is the platform whose shipped keyboard this process runs, which is
// also what a surface asks when a sentence it prints depends on the desk
// rather than on a key: how the line editor moves by word, say.
func Platform() string { return running }

// running is Platform's answer: the override where one is set, else the
// platform this process runs on. A test binary runs the Linux keyboard unless
// told otherwise, because the goldens and the assertions that print a chord
// are written against the keyboard the CI runner ships, and a suite that
// passed on one desk and failed on the next would be testing the desk.
var running = func() string {
	if p := os.Getenv(PlatformEnv); p != "" {
		return p
	}
	if testing.Testing() {
		return "linux"
	}
	return runtime.GOOS
}()

// movesFor is the table a platform applies over the declaration: the Mac's,
// or nothing.
func movesFor(platform string) map[string][]string {
	if platform == "darwin" {
		return darwinMoves
	}
	return nil
}

// written is the register exactly as keys.go declares it, before any
// platform's table: the keyboard Linux and Windows ship.
var written = snapshot()

// shippedOn is each platform's keyboard, taken once. The Mac's is the
// declaration with its table applied; applying it is also where it is held
// to the five rules, so a table that broke one is a program that does not
// start rather than a keyboard nobody checked.
var shippedOn = map[string][]reflect.Value{
	"linux":  written,
	"darwin": shippedFrom("darwin"),
}

// shippedFrom applies a platform's table to the declaration and returns the
// keyboard that makes, leaving the register as it found it.
func shippedFrom(platform string) []reflect.Value {
	held := snapshot()
	defer settle(held)
	settle(written)
	if _, err := apply(movesFor(platform)); err != nil {
		panic(fmt.Sprintf("keys: the %s keyboard breaks the register's rules: %v", platform, err))
	}
	return snapshot()
}

// ship puts a platform's keyboard on the register and returns it, which is
// what declared is.
func ship(platform string) []reflect.Value {
	board, ok := shippedOn[platform]
	if !ok {
		board = written
	}
	settle(board)
	return board
}

// UsePlatform puts the keyboard a platform ships on the register, as though
// the process had started there, and returns the call that puts the previous
// one back. It is for tests: the process start has already chosen, and a
// surface drawn while the register moved under it would be offering keys
// from two keyboards.
func UsePlatform(platform string) (restore func()) {
	was, wasDeclared, wasRunning := snapshot(), declared, running
	declared, running = ship(platform), platform
	return func() {
		settle(was)
		declared, running = wasDeclared, wasRunning
	}
}

// ShippedOn is a key's keystrokes as a platform ships them, by the dotted
// name a file writes it by, and false for a name the register does not have.
// The reference is written from it, so the document states both keyboards
// whichever one the machine running `make docs` would have started with.
func ShippedOn(platform, name string) ([]string, bool) {
	board, ok := shippedOn[platform]
	if !ok {
		board = written
	}
	var out []string
	found := false
	for i, g := range movable() {
		walk(g.name, board[i], func(n string, b Binding) {
			if n == name {
				out, found = b.Keys(), true
			}
		})
	}
	return out, found
}

// OptionSpelled reports that a spelling a hint prints names an alt chord —
// the one kind of chord a stock macOS terminal does not deliver until its
// Option key is set to send the escape prefix. It is how a surface decides
// whether the note naming that setting has anything to be about.
func OptionSpelled(spelling string) bool { return strings.Contains(spelling, "alt+") }

// NeedsOption reports that any of the bindings answers an alt chord in this
// process — the shipped Linux keyboard, or a keymap file that put one there.
func NeedsOption(bs ...Binding) bool {
	for _, b := range bs {
		for _, k := range b.Keys() {
			if OptionSpelled(k) {
				return true
			}
		}
	}
	return false
}
