package keys

// Reserved chords (docs/interface/reserved-keys.md): the keystrokes a desktop,
// a stock terminal, a multiplexer or the line discipline take before shhh
// sees them, or that arrive as some other key.
//
// A chord on this list is not a chord. A hint that offers it is a false offer
// on at least one platform shhh runs on — the press goes to Mission Control,
// or to the terminal's own tab bar, or nowhere — and the register exists to
// make a false offer impossible rather than documented. So two things read
// the list. The register's own test refuses a shipped binding that spends
// one, unless the binding is in `kept` with a sentence saying why. And the
// keymap file's checks refuse a file that moves any key onto one, naming the
// taker and the platform, because a keyboard that is legal on one desk and
// dead on another is a document describing neither.
//
// The list is refused on every platform, not the reader's. shhh cannot know
// which desk a keymap file will be carried to, and the keyboard it ships is
// one keyboard everywhere; that is the product's claim, and it is what makes
// the `?` list true on the machine the reader is holding. If the list ever
// leaves too few chords, the floor is macOS: Tier A's macOS rows and Tiers
// C and D stay refusals, and the rest become a warning.
//
// The table is data and the document is the table printed: `make docs`
// writes the tiers into reserved-keys.md from here, and `make docs-check`
// fails when they drift.

import (
	"fmt"
	"strings"
)

// Tier is why a chord is reserved, which decides which platform's name
// goes in the refusal.
type Tier int

const (
	// TierDesktop is taken before the terminal sees it: the window manager,
	// the input-source switcher, the task switcher.
	TierDesktop Tier = iota
	// TierTerminal is taken or retyped by the stock terminal at its default
	// profile: tabs, panes, find, the clipboard, zoom.
	TierTerminal
	// TierPrefix is a multiplexer's prefix, which never reaches the program.
	TierPrefix
	// TierByte is the line discipline, or a spelling that is the same byte as
	// another key, so a binding on one is a binding on the other.
	TierByte
)

// String is the tier's name as the document heads its table.
func (t Tier) String() string {
	switch t {
	case TierDesktop:
		return "the desktop takes it before the terminal sees it"
	case TierTerminal:
		return "the stock terminal takes it or retypes it"
	case TierPrefix:
		return "a multiplexer's prefix"
	default:
		return "the line discipline, and spellings that are another key"
	}
}

// Reserved is one chord and who has it.
type Reserved struct {
	// Key is the keystroke as the decoder names it: modifiers in the order
	// ctrl, alt, shift, then the key.
	Key string
	// Taker is what the press reaches instead of shhh.
	Taker string
	// On is where: a desktop, a terminal, a program.
	On   string
	Tier Tier
}

// reserved is the inventory of 2026-09-05, read from the vendors' own lists;
// the sources are in the document. Every key is spelled the way Keystroke
// spells it, which a test holds.
var reserved = func() []Reserved {
	var rs []Reserved
	add := func(tier Tier, taker, on string, keys ...string) {
		for _, k := range keys {
			rs = append(rs, Reserved{Key: k, Taker: taker, On: on, Tier: tier})
		}
	}
	// Tier A — the desktop.
	add(TierDesktop, "Mission Control", "macOS", "ctrl+up")
	add(TierDesktop, "the front app's windows", "macOS", "ctrl+down")
	add(TierDesktop, "moving a space", "macOS", "ctrl+left", "ctrl+right")
	add(TierDesktop, "the input-source switcher", "macOS", "ctrl+space", "ctrl+alt+space")
	add(TierDesktop, "keyboard focus to the menu bar, Dock, window, toolbar, floating window and status menus", "macOS",
		"ctrl+f2", "ctrl+f3", "ctrl+f4", "ctrl+f5", "ctrl+f6", "ctrl+f7", "ctrl+f8", "ctrl+shift+f6")
	add(TierDesktop, "the window switcher", "Windows and GNOME", "alt+tab", "alt+esc")
	add(TierDesktop, "closing the window", "Windows", "alt+f4")
	add(TierDesktop, "the window menu", "Windows", "alt+space")
	add(TierDesktop, "the sign-in screen's password reveal", "Windows", "alt+f8")
	add(TierDesktop, "the Start menu", "Windows", "ctrl+esc")
	add(TierDesktop, "Task Manager", "Windows", "ctrl+shift+esc")
	add(TierDesktop, "the security screen, or the power-off dialog", "Windows and GNOME", "ctrl+alt+delete")
	add(TierDesktop, "the app switcher, or focus to the top bar", "Windows and GNOME", "ctrl+alt+tab")
	add(TierDesktop, "the context menu", "Windows", "shift+f10")
	add(TierDesktop, "the run-a-command window", "GNOME", "alt+f2")
	add(TierDesktop, "workspace switching", "older GNOME and several other desktops",
		"ctrl+alt+up", "ctrl+alt+down", "ctrl+alt+left", "ctrl+alt+right")
	// Tier B — the stock terminal.
	add(TierTerminal, "the next and previous tab", "Terminal.app and Windows Terminal", "ctrl+tab", "ctrl+shift+tab")
	add(TierTerminal, "paste", "Windows Terminal", "ctrl+v", "shift+insert")
	add(TierTerminal, "copy", "Windows Terminal", "ctrl+insert")
	add(TierTerminal, "full screen", "Windows Terminal", "alt+enter")
	add(TierTerminal, "full screen", "Windows Terminal and GNOME Terminal", "f11")
	add(TierTerminal, "pane focus", "Windows Terminal", "alt+up", "alt+down", "alt+left", "alt+right")
	add(TierTerminal, "resizing the pane", "Windows Terminal", "alt+shift+up", "alt+shift+down", "alt+shift+left", "alt+shift+right")
	add(TierTerminal, "splitting the pane", "Windows Terminal", "alt+shift+d", "alt+shift+-", "alt+shift++")
	add(TierTerminal, "select all, copy, duplicate the tab, find, mark mode, a new window, a new tab, paste, close the pane, the tab dropdown, the settings file", "Windows Terminal",
		"ctrl+shift+a", "ctrl+shift+c", "ctrl+shift+d", "ctrl+shift+f", "ctrl+shift+m", "ctrl+shift+n", "ctrl+shift+t",
		"ctrl+shift+v", "ctrl+shift+w", "ctrl+shift+space", "ctrl+shift+,")
	add(TierTerminal, "scrolling the buffer", "Windows Terminal",
		"ctrl+shift+up", "ctrl+shift+down", "ctrl+shift+pgup", "ctrl+shift+pgdown", "ctrl+shift+home", "ctrl+shift+end")
	add(TierTerminal, "a new tab by profile", "Windows Terminal",
		"ctrl+shift+1", "ctrl+shift+2", "ctrl+shift+3", "ctrl+shift+4", "ctrl+shift+5", "ctrl+shift+6", "ctrl+shift+7", "ctrl+shift+8", "ctrl+shift+9")
	add(TierTerminal, "switching to a tab, and the defaults file", "Windows Terminal",
		"ctrl+alt+1", "ctrl+alt+2", "ctrl+alt+3", "ctrl+alt+4", "ctrl+alt+5", "ctrl+alt+6", "ctrl+alt+7", "ctrl+alt+8", "ctrl+alt+,")
	add(TierTerminal, "settings and the font size", "Windows Terminal and GNOME Terminal", "ctrl+,", "ctrl+0", "ctrl++", "ctrl+-")
	add(TierTerminal, "close the window and the tab, find next and previous, clear the highlight", "GNOME Terminal",
		"ctrl+shift+q", "ctrl+shift+g", "ctrl+shift+h", "ctrl+shift+j")
	add(TierTerminal, "switching and moving tabs", "GNOME Terminal", "ctrl+pgup", "ctrl+pgdown")
	add(TierTerminal, "switching to a tab", "GNOME Terminal",
		"alt+0", "alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9")
	add(TierTerminal, "scrolling a line and jumping between commands", "GNOME Terminal", "ctrl+shift+left", "ctrl+shift+right")
	add(TierTerminal, "scrolling the buffer", "GNOME Terminal, xterm and most Linux terminals", "shift+pgup", "shift+pgdown", "shift+home", "shift+end")
	add(TierTerminal, "help and the menu bar", "GNOME Terminal", "f1", "f10")
	// Tier C — a multiplexer's prefix.
	add(TierPrefix, "tmux's prefix", "tmux", "ctrl+b")
	add(TierPrefix, "screen's prefix", "GNU screen", "ctrl+a")
	// Tier D — the line discipline and the bytes.
	add(TierByte, "flow control", "a cooked terminal, or an ssh hop that keeps it", "ctrl+s", "ctrl+q")
	add(TierByte, "SIGQUIT", "a cooked terminal", "ctrl+\\")
	add(TierByte, "interrupt, suspend and end of input", "the line discipline", "ctrl+c", "ctrl+z", "ctrl+d")
	add(TierByte, "the same byte as backspace, tab, newline, enter, esc and NUL", "every terminal",
		"ctrl+h", "ctrl+i", "ctrl+j", "ctrl+m", "ctrl+[", "ctrl+@")
	add(TierByte, "enter, without the enhanced keyboard protocol", "Terminal.app, Windows Terminal and tmux", "shift+enter", "ctrl+enter")
	return rs
}()

// kept are the reserved chords the keyboard shhh ships spends on purpose,
// each with the sentence that earns it. Adding to this map is a code change
// beside a reason, never a keymap file's decision — and the exemption is the
// shipped acts' alone (shipped, below): a file that moved some other act
// onto one of these is refused like any other.
var kept = map[string]string{
	"ctrl+c":      "the interrupt is shhh's own in raw mode, and it does what the hand expects on every surface: cancel, back, then quit",
	"ctrl+z":      "the suspend is shhh's own in raw mode, and it hands the terminal back the way the shell would",
	"ctrl+d":      "end of input is shhh's own in raw mode, and it quits the way a shell does",
	"ctrl+space":  "its alias ctrl+y is the cover where the desktop takes it, which the register says beside it",
	"ctrl+j":      "the newline's own byte, declared as the cover for shift+enter",
	"shift+enter": "the key nearly every terminal reports for a newline; ctrl+j covers the ones that do not",
	"ctrl+v":      "Windows Terminal's paste is what the chord means there, and pasted text arrives as a paste event",
}

// shipped is which acts spent each kept chord when the register was
// declared — the words of every binding on it, taken before any keymap file
// could move one. The words are the one part of a declaration a file cannot
// change, so they name the act the exemption belongs to.
var shipped = map[string]map[string]bool{}

func init() {
	for _, s := range append(Surfaces(), Programs()...) {
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if _, ok := kept[k]; !ok {
					continue
				}
				if shipped[k] == nil {
					shipped[k] = map[string]bool{}
				}
				shipped[k][Words(b)] = true
			}
		}
	}
}

// Reservation reports who has a keystroke, if anyone.
func Reservation(key string) (Reserved, bool) {
	for _, r := range reserved {
		if r.Key == key {
			return r, true
		}
	}
	// The legacy encoding cannot carry shift on a ctrl'd letter or an
	// alt'd one, and the list does not enumerate the alphabet.
	if k, ok := strings.CutPrefix(key, "ctrl+shift+"); ok && len(k) == 1 && k[0] >= 'a' && k[0] <= 'z' {
		return Reserved{Key: key, Taker: "the same byte as ctrl+" + k + ", without the enhanced keyboard protocol",
			On: "Terminal.app, Windows Terminal and tmux", Tier: TierByte}, true
	}
	if k, ok := strings.CutPrefix(key, "alt+shift+"); ok && len(k) == 1 && k[0] >= 'a' && k[0] <= 'z' {
		return Reserved{Key: key, Taker: "the same bytes as alt+" + k + ", without the enhanced keyboard protocol",
			On: "Terminal.app, Windows Terminal and tmux", Tier: TierByte}, true
	}
	return Reserved{}, false
}

// Kept reports whether the act with these words is one the shipped keyboard
// spends the keystroke on, and why. Any other act on the same chord is not
// covered: the exemption is the shipped acts', not the chord's.
func Kept(key, words string) (string, bool) {
	why, ok := kept[key]
	if !ok || !shipped[key][words] {
		return "", false
	}
	return why, true
}

// checkReserved refuses a register that spends a reserved chord it has not
// earned. Asked of a file after its moves are applied, and of the shipped
// register by its test.
func checkReserved() error {
	for _, s := range append(Surfaces(), Programs()...) {
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				r, ok := Reservation(k)
				if !ok {
					continue
				}
				if _, earned := Kept(k, Words(b)); earned {
					continue
				}
				return fmt.Errorf("%q is %s's on %s, so it cannot be %q on %s; a modifier on the arrow, function and navigation rows is reported everywhere, and the free ones are listed at docs/interface/reserved-keys.md#what-is-left",
					k, r.Taker, r.On, Words(b), s.Name)
			}
		}
	}
	return nil
}
