// Package keys is shhh's key register (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Before it existed, a key was written down twice: once as a literal in the
// handler that answers it, and once as prose in the hint that offers it. The
// two lived in different files and nothing made them agree — sixty-eight
// literals across twenty files, and a `/help` that had never heard of the
// handover chord, which is the single most load-bearing key in the
// mid-sentence rule.
//
// So a key is declared once here, as a binding carrying both halves: the
// keystrokes a handler matches, the spelling a hint prints, and the words
// that go beside it. A surface reads the same binding for both, which is what
// makes drift a compile error rather than a reading-comprehension exercise.
//
// Three things this register deliberately does *not* become.
//
//   - It is not a keymap a surface consults at runtime to decide what it
//     offers. Which of a row's keys are live is a question about state
//
// , and the surfaces answer it themselves; the register says what a
//
//	  key *is*, not whether it can be pressed right now.
//	- It does not own contextual words. `[r]` is "try again" on a failure
//	  row and "ask again from scratch" on a dropped stream — the same key
//	  answered by the same handler, meaning something more specific in each
//	  place. The binding fixes the key and the words a surface has no better
//	  ones for; a surface with better ones keeps them (see Words).
//	- It is not itself the rebinding layer. A file can move a key, and
//	  keymap.go is what reads one: it rewrites these declarations once,
//	  before anything draws, so a moved key is still one declaration and a
//	  hint and its handler still cannot disagree. This file holds the
//	  keyboard shhh ships and the words for it — nothing in it branches on
//	  what a file said, and nothing outside keymap.go reads a file at all.
package keys

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Binding is one key shhh offers: the keystrokes it answers to, the spelling
// it is printed as, and the words that go beside that spelling.
type Binding = key.Binding

// bind declares one. shown is what a hint prints — which is not always one of
// the keystrokes, because a pair that moves in two directions is offered as
// `j/k` and answered as four separate keys.
func bind(shown, words string, presses ...string) Binding {
	return key.NewBinding(key.WithKeys(presses...), key.WithHelp(shown, words))
}

// Match reports whether a keystroke is one of the given bindings. It is the
// register's own so a caller needs one import rather than two, and so the
// name does not collide with the `key` locals this tree is full of.
func Match(msg tea.KeyPressMsg, bs ...Binding) bool {
	return key.Matches(msg, bs...)
}

// Is reports the same for a keystroke already reduced to its string, which is
// the shape the transcript rows' dispatch is written in (focus.go).
func Is(pressed string, bs ...Binding) bool {
	for _, b := range bs {
		if !b.Enabled() {
			continue
		}
		for _, k := range b.Keys() {
			if k == pressed {
				return true
			}
		}
	}
	return false
}

// Step is which half of a two-directional binding was pressed: -1 for the
// half that goes back, +1 for the half that goes on, and 0 for a keystroke
// the binding does not answer at all.
//
// A pair like `j/k` is one offer, one row and one declaration — two bindings
// for one offer would put the same keystroke on the surface twice, which is
// the thing the register refuses — so what a handler still has to work out is
// which way the reader meant. The declaration says so by its order: the
// keystrokes are written in pairs, the half that goes back first, so `k, j,
// up, down` is the same fact as the `j/k` the hint prints. checkPairs
// (keymap.go) holds a file to the same shape, which is what makes the answer
// survive a rebind: `screen.move = ["shift+up", "shift+down"]` reads the way
// `up`/`down` did and no handler learns a new spelling.
func Step(pressed string, b Binding) int {
	switch n := Nth(pressed, b); {
	case n < 0:
		return 0
	case n%2 == 0:
		return -1
	default:
		return 1
	}
}

// Nth is where a keystroke sits among the ones a binding answers, and -1 for
// one it does not. It is how a binding that stands for a run rather than for
// a single act is read — the plan card's `1–5` is one offer over five rows,
// and which row is the position of the digit — and it is what Step is built
// on.
func Nth(pressed string, b Binding) int {
	if !b.Enabled() {
		return -1
	}
	for i, k := range b.Keys() {
		if k == pressed {
			return i
		}
	}
	return -1
}

// Typed reports that a keystroke is one a sentence produces. It is the test a
// surface being typed into applies to its own movement keys: while a query
// line is open a `j` is a letter, and the halves of the same binding that no
// sentence can produce — an arrow, a chord — are what still move
// (docs/interface/surfaces.md#selectors).
func Typed(pressed string) bool { return len([]rune(pressed)) == 1 }

// Shown is the spelling a hint prints, bare. Brackets are the surface's
// business: a card writes `[y]`, a key row writes `y`, and both read the
// same binding.
func Shown(b Binding) string { return b.Help().Key }

// Bracket is the bracketed spelling, which is what the product draws.
func Bracket(b Binding) string { return Bracketed(b.Help().Key) }

// Bracketed is the same notation over a spelling a surface is holding on its
// own — a key handed to it as the string Shown gave, where the declaration
// behind it did not travel with the value. Bracket is the form to reach for;
// this exists so the brackets themselves are written in one place.
func Bracketed(key string) string { return "[" + key + "]" }

// BracketPair is Bracket for two bindings that are one gesture in two
// directions, printed the way every hint row prints such a pair: `[shift+↑↓]`
// where the two spellings differ only in their last glyph, and `[a/b]` where
// they do not — a rebind that split the pair is shown as split.
//
// A pair with nothing in front of the arrows is the same fact with an empty
// prefix: the palette moves on `↑` and `↓`, and the row a reader learned on
// every other list says `[↑↓]`. Excluding it would leave one surface writing
// the same gesture as `[↑/↓]`, which is the notation this is for.
func BracketPair(up, down Binding) string {
	a, b := Shown(up), Shown(down)
	ra, rb := []rune(a), []rune(b)
	arrows := "↑↓←→"
	if len(ra) > 0 && len(rb) == len(ra) && string(ra[:len(ra)-1]) == string(rb[:len(rb)-1]) &&
		strings.ContainsRune(arrows, ra[len(ra)-1]) && strings.ContainsRune(arrows, rb[len(rb)-1]) {
		return "[" + a + string(rb[len(rb)-1]) + "]"
	}
	return "[" + a + "/" + b + "]"
}

// Words are the binding's own words. A surface that means something more
// specific says so instead — the register is not a style guide, and "try
// again" is worse than "ask again from scratch" on the row that means the
// second thing.
func Words(b Binding) string { return b.Help().Desc }

// Draft is the framed input: the keys that are live while the sentence
// being typed holds the keyboard. Every one of them is a chord or a
// navigation key, and there is no exception but the two the rule itself
// names — enter sends the sentence and esc goes back — because a bare key
// here is a letter
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The chords the shell's own line editor uses — ctrl+a, ctrl+e, ctrl+k,
// ctrl+u, ctrl+w, alt+b, alt+f — are deliberately absent: the draft is a
// readline-shaped editor and those keys reach it, so declaring one here
// would take a shell user's muscle memory to open a surface they did not
// ask for.
type DraftKeys struct {
	Send    Binding
	Newline Binding
	// Queue is the follow-up queue's one chord, read two ways by the draft's
	// emptiness: with a turn live and something typed, the draft joins the
	// queue for after the turn (steering joins the running conversation, a
	// follow-up waits for it to finish); on an empty draft, the newest
	// queued message — a follow-up first, else steering — comes back. It
	// was two chords, alt+enter and alt+↑, and both are Windows Terminal's
	// (docs/interface/reserved-keys.md); one key a reader learns once is
	// the better trade anyway, since the two halves cannot be confused.
	// The chord was the textarea's next-line, which the arrows already
	// are, the way ctrl+p was its previous-line before the hold took it.
	Queue Binding
	// Queued moves the keyboard into what is queued, drawn above the box:
	// every message waiting for the turn, where one can be pulled back into
	// the draft or cancelled before it is sent. Queue above stays the
	// shortcut for the newest.
	//
	// It is a function key because the draft spends chords only and every
	// ctrl letter the terminal delivers is spent or the line editor's; f2 is
	// the free one that already means "edit the thing selected" to anyone
	// who has renamed a file in a desktop file manager
	// (docs/interface/reserved-keys.md#what-is-left).
	Queued    Binding
	Editor    Binding
	Attach    Binding
	Complete  Binding
	Palette   Binding
	Reasoning Binding
	Mode      Binding

	// Pause holds a working turn at its next round boundary and lets a held
	// one go on. It is one key for both halves because it is one act read
	// twice — the turn is either running or parked, and the chip on the rail
	// says which (docs/interface/surfaces.md#the-input-frame).
	Pause Binding

	HistoryPrev   Binding
	HistoryNext   Binding
	HistorySearch Binding

	// TakeSuggestion puts the next step an idle session offers into the
	// draft, cursor at its end, to be edited or sent like anything typed. It
	// is live only on an empty draft with an offer drawn in it, which is the
	// one state where the arrow has no act of its own — there is no cursor
	// to move right past nothing — so taking it moves nothing a reader
	// already relied on (docs/capabilities/chat.md#the-next-step-is-offered-not-typed).
	TakeSuggestion Binding

	// PointUp and PointDown move the pane's pointer — reading mode's cursor,
	// seen from the prompt — and Open and Close act on the row it names,
	// with the keyboard never leaving the draft. They are the arrows under
	// shift because the pointer is the one thing on first contact that has
	// to work everywhere, and shift on an arrow is reported by every
	// terminal and taken by no desktop (docs/interface/reserved-keys.md).
	// Line scrolling, which had the pair, left the prompt: pgup/pgdn and
	// the wheel are the scroll, and a pointer walk scrolls the pane anyway.
	PointUp   Binding
	PointDown Binding
	Open      Binding
	Close     Binding
	PageUp    Binding
	PageDown  Binding

	Reading Binding
	Agents  Binding
	// Backlog opens the project's backlog as a screen. There is no
	// mnemonic in the chord and there was none left to find: every letter
	// the word suggests is spent — t is the reasoning level — so this is
	// simply the chord the register still had
	// free among the ones every terminal delivers. The door people will
	// use is /todo; this is the one for hands already on the keyboard.
	Backlog Binding
	// NextAgent and PrevAgent walk the rail's session map — the orchestrator
	// and every child, in spawn order — moving the keyboard one session along
	// without opening the manager. The manager is where a child is answered,
	// retried, cancelled or killed; this pair is for seeing and moving
	// (docs/interface/surfaces.md#the-inspector-rail).
	NextAgent Binding
	PrevAgent Binding
	Mouse     Binding
	// KeyList prints the whole register as a system row. It was `?` on an
	// empty draft, which is the door Claude Code taught and the one bare key
	// the input had left: a sentence that opens with a question mark opened
	// the key list instead, and "on an empty draft" is a condition a reader
	// discovers by having it fire.
	//
	// The chord is ctrl rather than alt: an alt chord costs the Option
	// setting on a stock macOS terminal
	// (docs/interface/reserved-keys.md#the-draft-spends-chords-only), and a
	// key list behind a setting is a door that only opens once you no longer
	// need it, so it took ctrl+] from the free set instead.
	KeyList Binding

	// Suspend hands the terminal back to the shell, and Redraw takes the
	// screen back. Neither is shhh's own idea: ctrl+z is what the shell does
	// with a foreground job, and ctrl+l is what every full-screen program
	// repaints on. A chord the reader's hands already produce is a chord
	// this surface has to answer, or the reflex lands somewhere worse.
	Suspend Binding
	Redraw  Binding

	// Answer is the handover: the one key a decision that arrived on top
	// of a sentence answers to, and the reason every other letter on the
	// card stays a letter. With no decision waiting it hands the keyboard to
	// the row the reader has selected: a recovery or round-limit row, whose
	// letters are live once it has, or the commit card of a changed-files
	// row.
	Answer Binding

	Clear  Binding
	Cancel Binding
	Quit   Binding
}

// Draft's keys, in the order the input frame and /help name them.
var Draft = DraftKeys{
	Send:     bind("enter", "send the message", "enter"),
	Newline:  bind("shift+enter", "insert a newline", "shift+enter", "ctrl+j"),
	Queue:    bind("ctrl+n", "queue the draft for after the turn; on an empty draft, pull the newest queued message back", "ctrl+n"),
	Queued:   bind("f2", "see what is queued, to pull one back or cancel it", "f2"),
	Editor:   bind("ctrl+g", "open the draft in $EDITOR", "ctrl+g"),
	Attach:   bind("ctrl+v", "attach the clipboard", "ctrl+v"),
	Complete: bind("tab", "complete a slash command", "tab"),
	// The palette moved off ctrl+p to make room for the hold, and it went to
	// the slash key because `/` is already the command prefix: a chord that
	// opens the list of commands reads as the key the commands start with.
	// Both spellings are declared because they are the same keystroke seen
	// two ways — a terminal in the enhanced keyboard protocol reports the
	// slash with a ctrl modifier, and every other one sends the single byte
	// the decoder resolves to ctrl+_. A terminal that sends neither (Windows
	// conhost is the known one) still reaches the palette through `/` on an
	// empty draft and tab, which is what the key list says beside it.
	Palette:   bind("ctrl+/", "the command palette", "ctrl+/", "ctrl+_"),
	Reasoning: bind("ctrl+t", "cycle the reasoning level", "ctrl+t"),
	Mode:      bind("shift+tab", "cycle the permission mode", "shift+tab"),

	// The chord the palette had. It is spent here because holding is the act
	// that has to be reachable without looking — the person reaching for it
	// is about to lose the network — and `p` is the letter both halves of it
	// are named after.
	Pause: bind("ctrl+p", "hold the turn between rounds, or let a held one go", "ctrl+p"),

	HistoryPrev:   bind("↑", "recall the previous input", "up"),
	HistoryNext:   bind("↓", "the next one", "down"),
	HistorySearch: bind("ctrl+r", "search the input history", "ctrl+r"),

	TakeSuggestion: bind("→", "take the suggestion into an empty draft", "right"),

	PointUp:   bind("shift+↑", "move the pointer up a row of the pane", "shift+up"),
	PointDown: bind("shift+↓", "down a row", "shift+down"),
	Open:      bind("shift+→", "open or run the pointed row", "shift+right"),
	Close:     bind("shift+←", "close it", "shift+left"),
	PageUp:    bind("pgup", "page the transcript", "pgup"),
	PageDown:  bind("pgdn", "page it back", "pgdown"),

	Reading: bind("ctrl+o", "reading mode", "ctrl+o"),
	// The agent family is on the function row, at its right-hand end: the
	// manager on the plain key, and the walk on two neighbours under shift,
	// back first. Every ctrl letter the terminal delivers is spent or the
	// line editor's, ctrl+b is tmux's prefix besides, and alt is dead on a
	// stock Mac terminal until a profile setting is ticked — while the
	// function row arrives on every terminal with nothing set, and none of
	// these three is a desktop's or a terminal's
	// (docs/interface/reserved-keys.md#what-is-left).
	Agents:  bind("f12", "the agent manager", "f12"),
	Backlog: bind("ctrl+f", "the backlog screen", "ctrl+f"),

	NextAgent: bind("shift+f8", "the next session in the rail's map", "shift+f8"),
	PrevAgent: bind("shift+f7", "the previous one", "shift+f7"),

	Mouse:   bind("ctrl+x", "mouse reporting on or off", "ctrl+x"),
	KeyList: bind("ctrl+]", "the keys", "ctrl+]"),

	Suspend: bind("ctrl+z", "suspend shhh (idle only)", "ctrl+z"),
	Redraw:  bind("ctrl+l", "redraw the screen", "ctrl+l"),

	// ctrl+y is the same act, and it is here because ctrl+space does not
	// always arrive. macOS binds it system-wide to "select the previous
	// input source" and takes it before the terminal ever sees it, which
	// leaves the one load-bearing chord in the mid-sentence rule dead on the
	// most common desktop shhh runs on, with no way for the user to move it.
	//
	// The spelling names both, ctrl+space first because it is the one that
	// works everywhere else. It named only ctrl+space once, on the reasoning
	// that a hint offering two chords teaches neither, and that left the
	// person it was for — typing on a Mac when a card arrives, pressing the
	// chord the card named and getting nothing — with no way to learn the
	// alias from the card that needed it. Nothing shhh can read tells it the
	// desktop took the press (the decoder reads a delivered NUL or a kitty
	// ctrl+space as this key, so a miss is the chord never arriving), so the
	// card cannot swap one spelling for the other on evidence; it says both.
	// It is the spelling a keymap file's rebind prints as well, presses
	// joined by a slash.
	//
	// ctrl+y is free rather than merely unused: it is not in the bubbles
	// textarea keymap, and it is not one of the readline chords the draft
	// deliberately leaves to the line editor (see DraftKeys).
	Answer: bind("ctrl+space/ctrl+y", "answer it", "ctrl+space", "ctrl+y"),

	// Esc goes back and never stops anything. It clears the draft, drops a
	// selection, dismisses the completion menu, detaches a level, folds every
	// row the reader opened, leaves a waiting decision waiting — and on an
	// empty draft under a running turn it does nothing else at all, because
	// the reflex that closes a diff must not abandon minutes of work when the
	// box happens to be empty. Interrupting is the cancel chord's alone
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Clear:  bind("esc", "clear the input, or fold what is open", "esc"),
	Cancel: bind("ctrl+c", "cancel the turn (press twice), then the input", "ctrl+c"),
	Quit:   bind("ctrl+d", "quit (press twice; a live turn asks)", "ctrl+d"),
}

// SearchKeys are the input history search's: the incremental reverse search
// ctrl+r opens over what was typed before. It is a mode of the draft rather
// than a panel of its own — typing edits the query, so the only keys it
// declares are the three that do something other than filter.
type SearchKeys struct {
	Older  Binding
	Keep   Binding
	Cancel Binding
}

var Search = SearchKeys{
	Older: bind("ctrl+r", "an older match", "ctrl+r"),
	Keep:  bind("enter", "keep it in the draft", "enter"),
	// The safe answer: the draft comes back exactly as it was
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Cancel: bind("esc", "put the draft back", "esc", "ctrl+c"),
}

// ReadingKeys are reading mode's own. It is a takeover, so its letters
// are live because nothing else is listening.
type ReadingKeys struct {
	Move       Binding
	StripLeft  Binding
	StripRight Binding
	Expand     Binding
	Collapse   Binding
	Copy       Binding
	CopyBlock  Binding
	Search     Binding
	Match      Binding
	Half       Binding
	PageUp     Binding
	PageDown   Binding
	List       Binding
	Back       Binding
}

// All is reading mode's keys in the order it offers them, which is the order
// `?` lists them in.
func (k ReadingKeys) All() []Binding {
	return []Binding{k.Move, k.StripLeft, k.StripRight, k.Expand, k.Collapse, k.Copy, k.CopyBlock, k.Search,
		k.Match, k.Half, k.PageUp, k.PageDown, k.List, k.Back}
}

var Reading = ReadingKeys{
	Move: bind("j/k", "move", "k", "j", "up", "down"),
	// StripLeft and StripRight walk an open card's strip a call at a time
	// (docs/interface/surfaces.md#the-step). They are the arrows because
	// the strip is one row and j/k already walk the rows: along the row is
	// the other axis, which is the gesture ←→ is on every strip here. Two
	// bindings rather than one pair, so a keymap file can move either half.
	StripLeft:  bind("←", "along the strip, back", "left"),
	StripRight: bind("→", "along the strip, on", "right"),
	Expand:     bind("enter", "expand", "enter"),
	Collapse:   bind("-", "collapse", "-"),
	// Copy is [y] rather than [c]: the row is the whole message, and c is
	// the narrower copy beside it.
	Copy: bind("y", "copy the row", "y"),
	// CopyBlock is [c], one fenced block of the reply under the cursor. It
	// shares the letter with a dropped stream's "continue from here"
	// (RowKeys) the way Half shares u with a steer's withdraw: the two are
	// offered on different rows — a drop row is never a reply with a block —
	// and the dispatch asks the row first.
	CopyBlock: bind("c", "copy a block", "c"),
	// Search is the slash every pager in the terminal opens a query with, and
	// it is free here for the reason the bare letters are: nothing else on
	// this surface is listening. On the input it is the palette's other door,
	// which is a different surface and so a different key.
	Search: bind("/", "search", "/"),
	// Match walks what the query found, and like Move it is one binding both
	// ways with the dispatch reading which half was pressed. It is offered
	// only while a search is live: with nothing found, n and N are letters
	// and belong to the draft.
	Match: bind("n/N", "match", "N", "n"),
	// Half is the pager pair u/d: half the viewport, so the reader keeps
	// context while moving quickly. Like Move it is one binding both ways,
	// and the dispatch reads which half was pressed. On a turn-close row
	// [u] is that row's own undo offer first; everywhere else it pages.
	Half:     bind("u/d", "half page", "u", "d"),
	PageUp:   bind("pgup", "page up", "pgup"),
	PageDown: bind("pgdn", "page down", "pgdown"),
	// List is the same `?` the supporting TUIs offer: the
	// compact key row swapped for the whole list, in place, and swapped back
	// by the same key. It is live here and nowhere near the draft, for the
	// reason every bare letter in this file is.
	List: bind("?", "keys", "?"),
	// Back does not answer the chord that opened the mode: that chord is
	// declared once, on the input, and typing is the other way out anyway —
	// a reader who forgot which pane they were in loses a mode, not a
	// sentence.
	Back: bind("q", "back to the prompt", "q", "esc", "ctrl+c"),
}

// StagedKeys are reading mode's with its cursor on the staged strip — the
// row of chips under the transcript, which is the last thing the mode's
// cursor reaches (docs/interface/surfaces.md#a-staged-attachment). The strip
// sits above a live draft, so no key is ever printed on a chip; these are
// live only once reading mode holds the keyboard, which is what makes the
// letter a key rather than a letter of the sentence.
type StagedKeys struct {
	Pick Binding
	Open Binding
	Drop Binding
	Back Binding
}

// All is the strip's keys in the order its bar offers them.
func (k StagedKeys) All() []Binding { return []Binding{k.Pick, k.Open, k.Drop, k.Back} }

var Staged = StagedKeys{
	// Pick walks the chips, and like Select.Tab it is one binding both ways
	// with the dispatch reading which half was pressed: the strip is one row,
	// so the arrows along it are the gesture j/k is down the transcript.
	Pick: bind("←→", "chip", "left", "right"),
	Open: bind("enter", "open", "enter"),
	// Drop is the paste reader's letter for the same act, and it is free
	// here for the reason every letter on reading mode's bar is: nothing
	// else is listening.
	Drop: bind("x", "drop", "x"),
	// The safe answer: the draft comes back as it was, with the chip still
	// staged (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Back: bind("esc", "back to the draft", "esc"),
}

// QueueKeys are the queue's: the messages typed while a turn runs and not
// yet sent, drawn above the box, once Draft.Queued has moved the keyboard
// into them (docs/interface/surfaces.md#the-input-frame). The draft does not
// have the keyboard while they are live, which is what lets two of them be
// letters.
type QueueKeys struct {
	Move   Binding
	Edit   Binding
	Cancel Binding
	Back   Binding
}

// All is the queue's keys in the order its row offers them.
func (k QueueKeys) All() []Binding { return []Binding{k.Move, k.Edit, k.Cancel, k.Back} }

var Queue = QueueKeys{
	Move: bind("↑↓", "select a message", "up", "down", "k", "j"),
	// Edit takes the message out of the queue and puts it in the draft: it
	// is the reader's sentence again, and sending it queues it afresh at the
	// end like any other.
	Edit: bind("enter", "pull it back into the draft", "enter"),
	// Cancel is the letter the staged strip drops a chip on, for the same
	// act on a different thing.
	Cancel: bind("x", "cancel it", "x"),
	// The safe answer: the draft comes back exactly as it was and nothing
	// queued moves (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Back: bind("esc", "back to the draft", "esc", "ctrl+c"),
}

// KeyListKeys are the key list's: every key the register binds, by group,
// over the session, once Draft.KeyList or a bare /help has opened it
// (docs/interface/surfaces.md#the-key-list). It is typed into from the first
// keystroke, like the palette — every letter filters — so none of these is
// a letter, and the draft keeps its sentence underneath.
type KeyListKeys struct {
	Move  Binding
	Page  Binding
	Ends  Binding
	Close Binding
}

// All is the key list's keys in the order its row offers them.
func (k KeyListKeys) All() []Binding { return []Binding{k.Move, k.Page, k.Ends, k.Close} }

var KeyList = KeyListKeys{
	Move: bind("↑↓", "move", "up", "down"),
	Page: bind("pgup/pgdn", "page", "pgup", "pgdown"),
	Ends: bind("home/end", "first or last", "home", "end"),
	// The safe answer: nothing was chosen and the list holds nothing a
	// reader would lose, so it only goes away
	// (docs/interface/principles.md#esc-is-always-the-safe-answer). The
	// chord that opened it closes it too, answered as that chord so a file
	// that moves one moves both.
	Close: bind("esc", "close it", "esc", "ctrl+c"),
}

// FindKeys are the transcript search's query row — what reading mode's bar
// becomes while `[/]` is open. It is typed into, so every letter is text
// there and the mode's own letters are not live: the two keys below are the
// only ones the row has, which is why it is a surface of its own rather than
// two more bindings on the mode underneath it.
type FindKeys struct {
	Keep  Binding
	Clear Binding
}

// All is the query row's keys in the order it offers them.
func (k FindKeys) All() []Binding { return []Binding{k.Keep, k.Clear} }

var Find = FindKeys{
	// Enter closes the row and leaves the search standing, which is what
	// hands n and N back: they are letters while the row is open.
	Keep: bind("enter", "keep the search", "enter"),
	// The safe answer, one level in: the query, the marks and the count on
	// the rail go together, and the mode the reader was in is still there
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Clear: bind("esc", "clear it", "esc", "ctrl+c"),
}

// ContextKeys are the occupancy surface's own. It is a takeover in the chat
// rather than a `shhh` sub-command, so its way out is worded as going back to
// the prompt and not as quitting: the session it is a reading of is still
// running underneath it.
type ContextKeys struct {
	Move   Binding
	Expand Binding
	List   Binding
	Back   Binding
}

// All is the surface's keys in the order it offers them, which is the order
// `?` lists them in.
func (k ContextKeys) All() []Binding {
	return []Binding{k.Move, k.Expand, k.List, k.Back}
}

var Context = ContextKeys{
	Move: bind("↑↓/jk", "move", "up", "down", "k", "j"),
	// One key both folds and unfolds. A surface whose every group is a fold
	// would spend a second key saying what the glyph on the row already says.
	Expand: bind("enter", "expand or fold", "enter"),
	List:   bind("?", "keys", "?"),
	Back:   bind("q", "back to the prompt", "q", "esc", "ctrl+c"),
}

// SourcesKeys are the sources screen's own — the ledger of what the session
// read. It is a takeover in the chat like the context surface, so its way out
// goes back to the prompt rather than quitting.
//
// Its keys are the family's, not new ones: a list moves, `enter` takes what
// the pointer is on, `?` shows the register. What `enter` takes here is the
// page's whole stored text, which is why the binding says so in its own
// words.
type SourcesKeys struct {
	Move Binding
	Open Binding
	List Binding
	Back Binding
}

// All is the surface's keys in the order it offers them, which is the order
// `?` lists them in.
func (k SourcesKeys) All() []Binding {
	return []Binding{k.Move, k.Open, k.List, k.Back}
}

var Sources = SourcesKeys{
	Move: bind("↑↓/jk", "move", "up", "down", "k", "j"),
	Open: bind("enter", "read the page that was kept", "enter"),
	List: bind("?", "keys", "?"),
	Back: bind("q", "back to the prompt", "q", "esc", "ctrl+c"),
}

// NotesKeys are the notes screen's own — the session's shared notebook. It
// is a takeover in the chat like the sources screen, so its way out goes
// back to the prompt rather than quitting.
//
// Its keys are the family's, not new ones: a list moves, `enter` takes what
// the pointer is on, `?` shows the register. Drop is the one key here that
// destroys something, so it says so in its own words and asks through the
// inline confirm before it acts — dropping a note is the person's alone, and
// no agent has a tool that reaches it.
type NotesKeys struct {
	Move Binding
	Read Binding
	Drop Binding
	List Binding
	Back Binding
}

// All is the surface's keys in the order it offers them, which is the order
// `?` lists them in.
func (k NotesKeys) All() []Binding {
	return []Binding{k.Move, k.Read, k.Drop, k.List, k.Back}
}

var Notes = NotesKeys{
	Move: bind("↑↓/jk", "move", "up", "down", "k", "j"),
	Read: bind("enter", "read the whole note", "enter"),
	Drop: bind("d", "drop it", "d"),
	List: bind("?", "keys", "?"),
	Back: bind("q", "back to the prompt", "q", "esc", "ctrl+c"),
}

// BacklogKeys are the backlog screen's own. It is a takeover in the chat
// like the context surface, so its way out goes back to the prompt rather
// than quitting.
//
// It is the one list in the product that does not move on j/k, and the
// reason is on the row beside it: `k` cycles the kind filter here. Four
// letters select on this screen — status, priority, kind, ready — and a
// screen where one of them also moved the pointer would answer a keystroke
// twice, which is the thing the register exists to make impossible. So the
// pointer moves on the arrows alone, and every list that has no filter
// letters keeps the pair.
type BacklogKeys struct {
	Move Binding
	Read Binding
	Page Binding
	Tab  Binding

	Filter   Binding
	ClearQ   Binding
	Status   Binding
	Priority Binding
	Kind     Binding
	Ready    Binding

	Depends Binding
	Edit    Binding
	Run     Binding
	Block   Binding
	Reopen  Binding
	Archive Binding
	Drop    Binding
	New     Binding
	Sprint  Binding
	Groom   Binding

	List Binding
	Back Binding
}

// All is the screen's keys in the order it offers them, which is the order
// `?` lists them in.
func (k BacklogKeys) All() []Binding {
	return []Binding{
		k.Move, k.Read, k.Page, k.Tab,
		k.Filter, k.ClearQ, k.Status, k.Priority, k.Kind, k.Ready,
		k.Depends, k.Edit, k.Run, k.Block, k.Reopen, k.Archive, k.Drop,
		k.New, k.Sprint, k.Groom, k.List, k.Back,
	}
}

var Backlog = BacklogKeys{
	Move: bind("↑↓", "move", "up", "down"),
	Read: bind("enter", "read the body", "enter"),
	Page: bind("pgup/pgdn", "page the body", "pgup", "pgdown"),
	Tab:  bind("tab", "the backlog, the sprint, or what shipped", "tab"),

	Filter: bind("/", "filter by text", "/"),
	ClearQ: bind("ctrl+u", "clear the filter", "ctrl+u"),
	Status: bind("s", "cycle the status filter", "s"),
	// Priority and Kind are the two words a header field uses for what an
	// item is: how soon and what sort. They are separate filters because
	// they answer separate questions — "what is urgent" and "what is
	// broken" — and a reader asking the second one is not asking the first.
	Priority: bind("p", "cycle the priority filter", "p"),
	Kind:     bind("k", "cycle the kind filter", "k"),
	Ready:    bind("r", "only what can be started now", "r"),

	Depends: bind("w", "jump to what it waits on", "w"),
	Edit:    bind("e", "open it in $EDITOR", "e"),
	// Running an item is the one key here that starts work rather than
	// recording it, and it is the shifted letter for that reason: the row
	// under the pointer moves as the list is filtered, and a run started by
	// a mistyped lowercase letter is minutes of the model's work on the
	// wrong item.
	Run:     bind("R", "run it", "R"),
	Block:   bind("b", "block it", "b"),
	Reopen:  bind("o", "reopen it", "o"),
	Archive: bind("d", "archive it", "d"),
	Drop:    bind("x", "drop it, deleting the file", "x"),
	New:     bind("n", "a new item", "n"),
	Sprint:  bind("S", "add it to the sprint, or drop it from one", "S"),
	// Grooming reads rather than writes, which is why it keeps the
	// unshifted letter a run gave up: the worst a mistyped `g` costs is one
	// read-only turn, and nothing it finds reaches the file without a card.
	Groom: bind("g", "read it against the tree", "g"),

	List: bind("?", "keys", "?"),
	Back: bind("q", "back to the prompt", "q", "esc", "ctrl+c"),
}

// SprintKeys are the sprint plan card's, on the backlog screen's sprint tab.
//
// The card holds the keyboard for as long as it is up, so none of the
// screen's own row letters is live under it — which is what frees `j/k`
// here on the one screen whose list moves on the arrows alone, and what
// lets `g` mean the goal here and the grooming key one tab over. A card
// that is answered and gone is the shortest-lived surface in the register,
// and it is a surface for exactly that reason: while it is up it is the
// only thing listening.
type SprintKeys struct {
	Move   Binding
	Toggle Binding
	Left   Binding
	Goal   Binding
	Take   Binding
	Cancel Binding
}

// All is the card's keys in the order it offers them.
func (k SprintKeys) All() []Binding {
	return []Binding{k.Move, k.Toggle, k.Left, k.Goal, k.Take, k.Cancel}
}

var Sprint = SprintKeys{
	Move: bind("↑↓/jk", "move", "up", "down", "k", "j"),
	// Dropping is a toggle rather than a removal because the row is the
	// only record of what was proposed: a row that left the card could not
	// be put back, and the reader would have to plan again to see it.
	Toggle: bind("space", "drop it, or put it back", " ", "space"),
	// The left-out list is folded rather than absent: the words behind a
	// recommendation are what makes it arguable, and a proposal that showed
	// only what it took could not be argued with.
	Left:   bind("o", "what was left out, and why", "o"),
	Goal:   bind("g", "write what the set is for", "g"),
	Take:   bind("enter", "write the sprint", "enter"),
	Cancel: bind("esc", "write nothing", "esc"),
}

// RowKeys are the offers a transcript row carries. They are the register's
// awkward corner and its own subject: passive entries whose keys are answered
// by reading mode standing on the row, which is why the input keeps every one
// of these letters for typing. From the prompt they are reached the way a
// card's are, through Draft.Answer on the selected row, and they have no
// chord of their own: an act that answers the product is a bare letter
// behind the handover
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// There is no review among them. A turn's review is opened by the row that
// states what the turn changed — clicked, or selected and opened with enter —
// because that is the one gesture that names which turn is meant. A letter or
// a chord for it was the same act on a second key, and drawn on every turn's
// close it was a key that did not say which turn it would open
// (docs/interface/surfaces.md#the-turns-close).
//
// Nor is there an act on the work among them. Undoing a turn, committing it,
// running its checks again and reopening a backlog run's item each have a
// command or a tool — /undo, the model's commit, /gate run, /todo open — and
// a letter on the row was a second path to keep correct beside them. What
// stays is what answers the product rather than the work
// (docs/interface/surfaces.md#the-turns-close).
type RowKeys struct {
	// Withdraw is `[u]` on the notice an automatic steer left: it takes that
	// message back out of the conversation. It answers the machinery rather
	// than acting on the work, and nothing else reaches it.
	Withdraw Binding

	// Retry is `[r]`, which two rows offer with different words: a failure
	// asks again, a dropped stream asks again *from scratch*. Same key, same
	// dispatch, and the words belong to the row.
	Retry Binding
	// Continue is `[c]`: continue from a partial answer on a drop row, and
	// compact-then-retry on a context failure. It is [c] rather than the
	// artboard's [enter] because enter belongs to the draft.
	Continue Binding
	// Key is `[e]` rather than the artboard's `[k]`, because k is reading mode's
	// own.
	Key      Binding
	Provider Binding

	// Rounds is the tool-round checkpoint's pair. The row draws the
	// grant as the block it grants (`[+50]`); the hint bar names the literal
	// key, which is what Shown carries.
	Rounds Binding
	Uncap  Binding
}

var Row = RowKeys{
	Withdraw: bind("u", "take the steer back", "u"),

	Retry:    bind("r", "try again", "r"),
	Continue: bind("c", "continue from here", "c"),
	Key:      bind("e", "enter a new key", "e"),
	Provider: bind("p", "switch provider", "p"),

	Rounds: bind("+", "more rounds", "+"),
	Uncap:  bind("!", "let it run", "!"),
}

// CommitKeys are the commit card's — the card the handover opens on a
// selected changed-files row, which stages exactly what that turn changed and
// nothing else
// (docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too).
//
// It is a takeover rather than a card beside the draft, because it is
// reached through Draft.Answer: the handover has already taken the keyboard
// out of the draft when the card arrives, so there is nothing to hand over
// and every letter here is live.
type CommitKeys struct {
	Take Binding
	// Edit opens the proposed message as a draft. It is the letter the word
	// starts with, free here for the reason every bare letter on a takeover
	// is: nothing else is listening while the card is up.
	Edit Binding
	// Hunks opens the turn's review, the surface /diff and the row itself
	// open, rather than a second one: what is about to be committed is read
	// in the one place a turn's hunks are read.
	Hunks Binding
	// Override commits a diff the card found a credential shape in, on the
	// reader's own say. It is offered only while a finding stands, and it is
	// `!` because that is the keystroke a row's own override already is
	// (Row.Uncap): the one press that takes the product's stop off on
	// purpose (docs/capabilities/secrets.md#a-secret-does-not-get-committed).
	Override Binding
	Cancel   Binding
}

// All is the card's keys in the order it offers them.
func (k CommitKeys) All() []Binding {
	return []Binding{k.Take, k.Edit, k.Hunks, k.Override, k.Cancel}
}

var Commit = CommitKeys{
	Take:     bind("enter", "commit", "enter"),
	Edit:     bind("e", "edit the message", "e"),
	Hunks:    bind("s", "read the hunks", "s"),
	Override: bind("!", "commit with the secret", "!"),
	// The words say what is left standing rather than "cancel": a changeset
	// nobody committed is still there, and so is the offer
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Cancel: bind("esc", "not now", "esc"),
}

// DecisionKeys are the approval card's, the `/run` confirm's, the plan
// card's and a child's routed approval's. They arrive ungated:
// live only once Draft.Answer has handed the keyboard over, or on a card that
// landed on a draft nobody was typing into.
type DecisionKeys struct {
	Allow Binding
	Deny  Binding

	// Always opens the grants the card can make rather than making one: a
	// list of what each would cover and when it would end, taken with the
	// selector's own keys
	// (docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends).
	//
	// The keystroke did not move and the label did. It used to read "always
	// allow this session", which was the whole of what one press bought and
	// is no longer either half of it — the length is the reader's now, and
	// the press that used to grant only offers. A register whose label
	// described the old act would be the one place the rule it exists to
	// enforce could not be checked: one surface, one keystroke, one act, and
	// the label is how the act is named.
	Always Binding

	// Batch opens the queue behind the card as a list the reader checks and
	// unchecks, rather than answering a set they cannot see. The keystroke is
	// the one it always was and only the act behind it changed: a reader who
	// learned it as "answer the rest like this one" presses it, sees the rest,
	// and enter is still that answer
	// (docs/interface/surfaces.md#the-approval-card).
	Batch Binding
	Diff  Binding

	// AllowNoted and DenyNoted are the same two answers with a sentence
	// attached: the key opens a one-line field under the card, and what is
	// written there travels with the answer
	// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).
	//
	// They are the shifted spellings of the answers they carry, which is a
	// choice about legibility and not about scarcity: `e`, `x` and every
	// unshifted letter but `y`, `n`, `a`, `d` and `t` are unclaimed on this
	// surface. A shifted letter says "the same answer, more of it" without
	// asking the reader to learn a second alphabet, and the pairing is
	// visible in the run the card prints.
	//
	// The cost is that Allow and Deny give the shifted letters up: a surface
	// answers one keystroke once (register.go), so `Y` and `N` could not go
	// on staying second spellings of `y` and `n` here. What a reader who has
	// been pressing `N` for a year gets is the field, open, with the denial
	// still waiting behind it — and enter on an empty field is the denial
	// they meant, byte for byte. One extra keystroke for a reflex, and the
	// thing the reflex was reaching for is still what it lands on.
	//
	// On the cards that offer no note — a /run the reader typed, a child's
	// routed ask — the shifted letters answer nothing at all, and neither is
	// drawn there: a card offers exactly the keys it answers, so there is
	// nothing on those cards for a reflex to land on and be surprised by.
	AllowNoted Binding
	DenyNoted  Binding

	// DryRun is the command card's offer to find out what the command would
	// do without doing it, where the command has a form that can be asked
	// rather than told (internal/dryrun). It answers the hesitation the card
	// is there for without answering the card
	// (docs/interface/surfaces.md#the-approval-card).
	//
	// It is not [d], which a reader of the one-shot's action bar would
	// expect: on this surface that keystroke is already the full view, and
	// one surface answering a keystroke with two bindings is what this
	// register refuses (register.go). So the key is [t] — try it — and the
	// card's own words say what it would try.
	DryRun Binding

	// Explain is the command card's other question about the command rather
	// than about the decision: a cheap model says what the thing in front of
	// the reader does, on the screen the dry run opens on, and the decision
	// is still waiting behind it
	// (docs/interface/surfaces.md#the-approval-card).
	//
	// It is [x] because the one-shot's action bar already spends [x] on
	// explaining a command (OneShot.Explain), and a reader who learned the
	// letter there is looking at the same question here. That is an argument
	// for the letter and not a claim to it — the two surfaces are never up
	// at once, so nothing would have stopped a different one. What settles
	// it is that [x] is unclaimed on this card: [e] would have to be taken
	// from nothing and mean explain on one surface and edit on the other,
	// which is the cost this register exists to make visible.
	Explain Binding

	// Amend is the command card's offer to run the line as the reader would
	// have written it: the command opens in a field, prefilled, and what
	// runs is what they left there
	// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).
	//
	// It is [e] — edit — the letter the one-shot's action bar already spends
	// on the same act (OneShot.Edit), and it is unclaimed here: this card
	// spends `y Y n N a A d D t x g` and the draft's own editor is a chord.
	// So this is a declaration rather than a move, and the one argument that
	// could have taken the letter first — the explain key wanting a
	// mnemonic — was settled the other way when it was made (Explain).
	Amend Binding

	// Revise is the drafted-file card's offer to change the file before it
	// is written: the draft opens in $EDITOR and what comes back is read by
	// the same loader before the card shows it
	// (docs/capabilities/containment.md#a-declaration-can-be-drafted-for-you).
	// It is [e] for the reason Amend is — edit is the word — and the two are
	// never on one card: Amend is the command card's and this is a card that
	// writes a file.
	Revise Binding

	// Accept and Refuse are Allow and Deny on a card the reader summoned
	// rather than was handed.
	//
	// Deny folds esc and ctrl+c into the answer, which is right for a card
	// that arrived on its own: there is nothing to go back to, so leaving
	// and declining are the same act. On a summoned card they are not — esc
	// is the way back to a screen the reader chose to leave — and a decline
	// that outlives the session is exactly the consequence esc may never
	// carry (docs/interface/principles.md#esc-is-always-the-safe-answer).
	// So the answer is the letter alone and the way out is Select.Cancel.
	//
	// Accept exists for the other half of the same difference. A summoned
	// card has no sentence to attach to its yes — nothing is waiting on the
	// answer to read one — so the shifted letter is unspent there and stays
	// the second spelling of yes it has always been, rather than being taken
	// away from that surface by a field it does not offer.
	Accept Binding
	Refuse Binding

	// The card's own scroll, for a body taller or wider than the panel
	// (docs/interface/surfaces.md#the-approval-card). The same chords the
	// draft scrolls the transcript with, answered by whichever of the two
	// holds the keyboard: a card that was handed it scrolls itself, and a
	// card inert beside a live draft leaves the chords to the transcript,
	// exactly as it leaves every other key.
	ScrollUp   Binding
	ScrollDown Binding
	PanLeft    Binding
	PanRight   Binding
}

// AlwaysRouted is Decision.Always's words on a child's routed command card.
// The keystroke is the same one and so is the surface, but the act is not:
// that card makes one fixed grant — the command's shape, for every agent,
// until this turn ends — and draws no list of lengths, so the register's own
// words would promise a choice the card does not offer. It is words and not
// a second binding, because one surface answering one keystroke with two
// bindings is what the register refuses
// (docs/interface/surfaces.md#the-agent-manager).
const AlwaysRouted = "allow this command for every agent, this turn"

var Decision = DecisionKeys{
	Allow:      bind("y", "allow", "y", "enter"),
	Deny:       bind("n", "deny", "n", "esc", "ctrl+c"),
	AllowNoted: bind("Y", "allow with a note", "Y"),
	DenyNoted:  bind("N", "deny with a note", "N"),
	Always:     bind("a", "allow without asking — choose how long", "a"),
	Batch:      bind("A", "open the queue", "A"),
	Diff:       bind("d", "full diff", "d", "D"),
	DryRun:     bind("t", "dry run", "t"),
	Explain:    bind("x", "explain what the command does", "x"),
	Amend:      bind("e", "edit the command before it runs", "e"),
	Revise:     bind("e", "edit the file before it is written", "e"),
	Accept:     bind("y", "yes", "y", "Y", "enter"),
	Refuse:     bind("n", "no, and stop offering", "n", "N"),

	ScrollUp:   bind("shift+↑", "scroll the card up", "shift+up"),
	ScrollDown: bind("shift+↓", "scroll the card", "shift+down"),
	PanLeft:    bind("shift+←", "pan a wide body back", "shift+left"),
	PanRight:   bind("shift+→", "pan a wide body", "shift+right"),
}

// ConfirmKeys are the inline one-liner's and the undo confirm's. They
// are not DecisionKeys with fewer fields: enter means the opposite thing.
// A card asks a question the reader walked up to and enter takes the offer;
// a confirm interrupts something already in motion and enter is the default,
// which is No. Two bindings rather than one is how that difference stays
// visible to whoever adds the third confirm.
type ConfirmKeys struct {
	Yes   Binding
	No    Binding
	Force Binding
}

var Confirm = ConfirmKeys{
	Yes: bind("y", "yes", "y", "Y"),
	No:  bind("N", "no — the default", "n", "N", "enter", "esc", "ctrl+c"),
	// Force is the undo confirm's second reading: the same shape, with the
	// destructive answer spelled differently so it is not the one a reflex
	// presses.
	Force: bind("f", "force", "f", "F"),
}

// SelectKeys are the selector family's, the model picker's, the rewind
// picker's and the palette's. All takeovers.
type SelectKeys struct {
	Move   Binding
	MoveJK Binding
	Take   Binding
	Alt    Binding
	Filter Binding
	ClearQ Binding
	Toggle Binding
	All    Binding
	Note   Binding
	// Tab steps between the parts of one card — the questions of a call that
	// asked several, and the submit that ends them.
	//
	// It is the arrows and not `tab`, which is the keystroke a tab strip has
	// everywhere else in the product, because on this family `tab` is already
	// the note and one keystroke may answer one act on one surface. Of the
	// three ways out of that — move the note, give the strip a chord, or give
	// it the arrows — the arrows are the only one that costs nothing: `↑↓`
	// already walk the rows, so `←→` walking the strip is the same gesture
	// one axis over, and no reader has to unlearn where the note is. The
	// decision is the family's rather than one card's, so the next selector
	// that grows tabs finds the keys already spent.
	Tab Binding
	// Long puts the marked row's own long form on the full screen and gives
	// the screen back with nothing answered — the panel is too narrow for a
	// second column, and the full view is where a card already sends what
	// will not fit (docs/interface/surfaces.md#the-approval-card).
	//
	// It is `d`, which is the letter the approval card's full diff already
	// spends on the same act: take what is under the pointer to the screen
	// the whole product reads long things on. `Alt` spends `d` too and no
	// surface offers both — a card with a default to set has no long form
	// behind it and the question card has no default to set.
	Long Binding
	// Delete and Rename are the saved-chat picker's housekeeping keys,
	// answered on the focused row: the first arms an inline confirm, the
	// second opens a rename row. Bare letters, so like Alt they are text
	// while the query line is open.
	Delete  Binding
	Rename  Binding
	Cancel  Binding
	Quit    Binding
	Palette PaletteKeys
}

// PaletteKeys are the palette surface's, which differ from the rest of the
// family in one place: it is typed into from the first keystroke, so its
// movement keys are the arrows and the readline chords, never j/k.
type PaletteKeys struct {
	Prev  Binding
	Next  Binding
	Run   Binding
	Write Binding
}

var Select = SelectKeys{
	Move:   bind("↑↓", "move", "up", "down"),
	MoveJK: bind("↑↓/jk", "move", "up", "down", "k", "j"),
	Take:   bind("enter", "select", "enter"),
	Alt:    bind("d", "make it the default", "d"),
	Filter: bind("/", "filter", "/"),
	ClearQ: bind("ctrl+u", "clear the filter", "ctrl+u"),
	Toggle: bind("space", "toggle", " ", "space"),
	All:    bind("a", "all or none", "a"),
	Note:   bind("tab", "note or options", "tab"),
	Tab:    bind("←→", "the next question", "left", "right"),
	Long:   bind("d", "the full answer", "d"),
	Delete: bind("x", "delete", "x"),
	Rename: bind("r", "rename", "r"),
	Cancel: bind("esc", "cancel", "esc", "ctrl+c"),
	Quit:   bind("ctrl+d", "quit", "ctrl+d"),
	Palette: PaletteKeys{
		Prev:  bind("↑", "move", "up", "ctrl+p"),
		Next:  bind("↓", "move", "down", "ctrl+n"),
		Run:   bind("enter", "run it", "enter"),
		Write: bind("tab", "write it into the input", "tab"),
	},
}

// ReviewKeys are review mode's: a takeover over the whole screen, and a
// reading of it. It offers no staging: a turn is taken back with /undo and a
// file of it by asking for it, so the surface moves, pages and leaves
// (docs/interface/surfaces.md#the-turns-close).
type ReviewKeys struct {
	MoveFile   Binding
	MoveHunk   Binding
	SideBySide Binding
	PageUp     Binding
	PageDown   Binding
	Back       Binding
}

var Review = ReviewKeys{
	MoveFile:   bind("j/k", "file", "k", "j", "up", "down"),
	MoveHunk:   bind("n/p", "hunk", "p", "n"),
	SideBySide: bind("\\", "side by side", "\\"),
	PageUp:     bind("pgup", "page up", "pgup"),
	PageDown:   bind("pgdn", "page down", "pgdown"),
	Back:       bind("esc", "back", "esc", "ctrl+c"),
}

// AgentKeys are the agent manager's.
type AgentKeys struct {
	Move   Binding
	Attach Binding
	// Go is the way into the manager from a child's routed approval: the
	// card steps aside and the agent that asked is the one attached. It is
	// declared here rather than with the card's answers because what it
	// reaches is the manager, and because it is not an answer — the ask is
	// still waiting when the reader comes back.
	Go     Binding
	Answer Binding
	// Steer is the redirect typed into the row it is aimed at, without
	// attaching: a child that has drifted is corrected where it was seen to
	// drift, and the correction is cheaper than the kill beside it. It is
	// the pointer's row rather than the list's, like cancel and kill,
	// because its target must never be guessed
	// (docs/interface/surfaces.md#the-agent-manager).
	Steer Binding
	Retry Binding
	// Review puts a stopped writer's kept patch on the card a finishing
	// writer's patch is put on, with the diff open over it
	// (docs/capabilities/subagents.md#a-failed-child-leaves-a-handoff).
	Review Binding
	// Migrate sends a role written before the five sections to the drafter
	// to be moved into them, and Edit opens a role's file in the editor now
	// that enter opens it on the drafter. Both are a role row's, live only
	// over a row with a file, and Migrate only over one in the older shape
	// (docs/capabilities/subagents.md#an-older-profile-is-moved-into-sections-not-rewritten).
	Migrate Binding
	Edit    Binding
	Cancel  Binding
	Kill    Binding
	// KillAll ends every child at once, and it is the capital of Kill's own
	// letter for the reason Kill is a capital at all: an act nobody can undo
	// may not sit under a keystroke a reader presses without reading the row
	// (keymap.go's destructive list).
	KillAll Binding
	Back    Binding
	Detach  Binding
}

var Agent = AgentKeys{
	Move:    bind("j/k", "move", "k", "j", "up", "down"),
	Attach:  bind("enter", "attach", "enter"),
	Go:      bind("g", "go to the agent that asked", "g"),
	Answer:  bind("a", "answer", "a"),
	Steer:   bind("s", "steer", "s"),
	Retry:   bind("r", "retry", "r"),
	Review:  bind("p", "review", "p"),
	Migrate: bind("m", "move it into sections", "m"),
	Edit:    bind("e", "open its file", "e"),
	Cancel:  bind("x", "cancel", "x"),
	Kill:    bind("X", "kill", "X"),
	KillAll: bind("K", "kill all", "K"),
	Back:    bind("esc", "back", "esc", "ctrl+c"),
	Detach:  bind("esc", "back to your own session", "esc"),
}

// ProfileKeys are the profile drafter's. The surface is a flow rather than a
// list, which is what makes its way out a step back rather than a way out:
// esc unwinds the flow one exchange at a time and leaves from the first one,
// so a person who mistyped an answer is not made to start again. It is still
// the safe answer at every step — nothing on this surface writes until the
// draft card's own row does (invariant 3).
type ProfileKeys struct {
	Move       Binding
	Take       Binding
	Refine     Binding
	RefineAll  Binding
	Edit       Binding
	Clear      Binding
	Migrate    Binding
	Note       Binding
	ScrollUp   Binding
	ScrollDown Binding
	Back       Binding
}

var Profile = ProfileKeys{
	// The arrows and not j/k: every step of this surface has a text field on
	// it, and on a surface being typed into a j is a letter — the reading the
	// palette and the open query line already have.
	Move: bind("↑↓", "move", "up", "down"),
	Take: bind("enter", "take it", "enter"),
	// The draft step's three acts on the selected section. e and x are the
	// letters the same acts spend everywhere else: backlog.edit and
	// commit.edit open the editor, and every drop and remove is x.
	Refine: bind("enter", "refine it with a note", "enter"),
	Edit:   bind("e", "edit it yourself", "e"),
	Clear:  bind("x", "clear it", "x"),
	// The one note for every section is refine widened, and a widened act
	// is the capital here the way the manager's kill all is: R is free on
	// the draft step, and pressed again while its note is still empty it
	// says the sections the person wrote go too.
	RefineAll: bind("R", "refine the whole draft with one note", "R"),
	// A profile opened from a file in the older shape is moved into the
	// sections from its own draft step, on the manager's letter for it.
	Migrate: bind("m", "move it into sections", "m"),
	// tab hands the keyboard between the draft's sections and the card that
	// writes it.
	Note: bind("tab", "the sections or the card", "tab"),
	// The profile is the longest thing on the card and the one the decision
	// is about, so it scrolls where the options do not. Shift is what every
	// other card scrolls its body under.
	ScrollUp:   bind("shift+↑", "scroll the profile up", "shift+up"),
	ScrollDown: bind("shift+↓", "scroll the profile", "shift+down"),
	Back:       bind("esc", "back a step", "esc", "ctrl+c"),
}

// WaitKeys are the surfaces that open on their own and take the keyboard with
// them: the retry countdown and the context-pressure card, and
// the masked key prompt an auth failure opens.
type WaitKeys struct {
	Fallback Binding
	Stop     Binding

	Compact    Binding
	NewSession Binding
	KeepGoing  Binding

	UseKey  Binding
	KeepKey Binding
}

var Wait = WaitKeys{
	Fallback: bind("m", "finish this turn on the fallback model", "m"),
	Stop:     bind("esc", "stop waiting", "esc"),

	Compact:    bind("enter", "compact now", "enter"),
	NewSession: bind("n", "new session", "n"),
	KeepGoing:  bind("esc", "keep going", "esc"),

	UseKey:  bind("enter", "use it for this session", "enter"),
	KeepKey: bind("esc", "keep the current key", "esc"),
}

// DiffKeys are the full-screen viewer's.
type DiffKeys struct {
	Scroll     Binding
	SideBySide Binding
	Hunk       Binding
	Back       Binding
	// Leave is the full-screen form's other ways out, and it is separate
	// from Back for a reason worth the second field: the viewer is also a
	// transcript row, and there `q` is reading mode's own. Only
	// the surface that has the whole screen can claim it, so only that host
	// answers this one.
	Leave Binding
}

var Diff = DiffKeys{
	Scroll:     bind("j/k", "scroll", "k", "j", "up", "down"),
	SideBySide: bind("s", "side-by-side", "s"),
	Hunk:       bind("n/p", "hunk", "p", "n"),
	Back:       bind("esc", "back", "esc"),
	Leave:      bind("q", "back", "q", "ctrl+c"),
}

// OutputKeys are the full-screen output viewer's: a command's output, or a
// read's, opened whole from reading mode when the bounded body was not all
// of it (docs/interface/surfaces.md#the-activity-row). It is the diff
// viewer's host with prose-free content, so it scrolls and leaves and does
// nothing else.
type OutputKeys struct {
	Scroll   Binding
	PageUp   Binding
	PageDown Binding
	// Collapse is [enter], mirroring the diff's cycle: the depth past full
	// screen is closed, so the key that opened the row all the way is the
	// key that puts it away.
	Collapse Binding
	Back     Binding
	// Leave is separate from Back the way the diff viewer's is: only the
	// surface holding the whole screen can claim a bare letter.
	Leave Binding
}

var Output = OutputKeys{
	Scroll:   bind("j/k", "scroll", "k", "j", "up", "down"),
	PageUp:   bind("pgup", "page up", "pgup"),
	PageDown: bind("pgdn", "page down", "pgdown"),
	Collapse: bind("enter", "close the row", "enter"),
	Back:     bind("esc", "back", "esc"),
	Leave:    bind("q", "back", "q", "ctrl+c"),
}

// PreviewKeys are the staged attachment preview's. The surface has nothing
// to scroll and nothing to stage — a thumbnail is fitted to the pane and a
// text file is clipped with what did not fit counted at the foot, rather
// than either being panned around — so what it offers is the two spellings
// of leaving every full-screen viewer in shhh has always answered to, and the
// one act this card is the moment for: a wrong screenshot is recognised
// here, so here is where it is dropped
// (docs/interface/surfaces.md#a-staged-attachment).
type PreviewKeys struct {
	Remove Binding
	Back   Binding
	Leave  Binding
}

// All is the card's keys in the order its hint row offers them.
func (k PreviewKeys) All() []Binding { return []Binding{k.Remove, k.Back, k.Leave} }

var Preview = PreviewKeys{
	// The paste reader's letter, because it is the same act on the same
	// staging area from the surface beside it.
	Remove: bind("x", "remove", "x"),
	Back:   bind("esc", "back", "esc"),
	Leave:  bind("q", "back", "q", "ctrl+c"),
}

// PasteKeys are the staged paste's reader — the surface the draft's fold
// opens onto (docs/interface/surfaces.md#the-input-frame).
//
// It is not the preview above it and the difference is what each is asked.
// The preview answers *is this the file I meant*, which a thumbnail settles
// in one look, so it scrolls nowhere and decides nothing. A paste is two
// hundred lines the reader is about to pay for: the questions are whether it
// is the right log, whether all of it is there, and whether to send it at
// all, and only the last of those can be answered without moving.
//
// So it scrolls, and it carries the one destructive key on any reading
// surface in the product. That key is a letter rather than a chord because
// this is a takeover and nothing else is listening, and it is `x` rather
// than `d` because `d` pages half a screen everywhere a body scrolls.
type PasteKeys struct {
	Scroll Binding
	Remove Binding
	Leave  Binding
	Back   Binding
}

// All is the reader's keys in the order its row offers them.
func (k PasteKeys) All() []Binding { return []Binding{k.Scroll, k.Remove, k.Leave, k.Back} }

var Paste = PasteKeys{
	Scroll: bind("j/k", "scroll", "k", "j", "up", "down"),
	Remove: bind("x", "remove the paste", "x"),
	// The words are the whole promise, and the promise is the reason the
	// reader can be talked into opening this at all: the sentence they were
	// half way through is still there, with the cursor in it.
	Leave: bind("q", "back to the draft, cursor where you left it", "q", "ctrl+c"),
	Back:  bind("esc", "back to the draft", "esc"),
}

// ScreenKeys are the supporting TUIs': `shhh config`, `shhh history`,
// `shhh metrics`, `shhh doctor`, `shhh snippets` and the saved-chat browser.
// They are where `?` was invented — the compact key row swapped for the whole
// list, in place — which is the idiom reading mode borrows. List is also the
// `?` every card that holds the keyboard answers, and the profile drafter's:
// one key, the same answer on every surface.
type ScreenKeys struct {
	Move   Binding
	Take   Binding
	Filter Binding
	ClearQ Binding
	List   Binding
	Quit   Binding

	Reset Binding
	Write Binding
	Keep  Binding
	// Scope moves the config screen's write between the checkout's own file
	// and the person's, where the screen stands in a checkout and so has
	// two to choose from. It is the screen's `--global`, as a toggle.
	Scope Binding

	Copy    Binding
	Rerun   Binding
	Snippet Binding
	Delete  Binding
	// Rename opens the one-line rename row over the item under the pointer.
	// It is the screens' half of the key the picker inside a session answers
	// on the same act (Select.Rename), which is why the two are spelled the
	// same and declared apart: one belongs to a card beside a live draft and
	// this one to a surface that holds the whole keyboard.
	Rename Binding
	Fix    Binding
	Again  Binding
	Apply  Binding

	// Worked, Failed and Skip are `shhh rate`'s three answers. They are
	// bare letters on a takeover, like every other key in this group, and
	// they are here rather than on the one-shot's action bar because the
	// question is about a command that already ran rather than about one
	// waiting to.
	Worked Binding
	Failed Binding
	Skip   Binding
}

var Screen = ScreenKeys{
	Move:   bind("↑↓/jk", "move", "up", "down", "k", "j"),
	Take:   bind("enter", "take it", "enter"),
	Filter: bind("/", "filter", "/"),
	ClearQ: bind("ctrl+u", "clear the filter", "ctrl+u"),
	List:   bind("?", "keys", "?"),
	Quit:   bind("q", "quit", "q", "esc", "ctrl+c"),

	Reset: bind("r", "reset to default", "r"),
	Write: bind("w", "write the file", "w"),
	Keep:  bind("esc", "keep the current value", "esc"),
	Scope: bind("g", "switch the file", "g"),

	Copy:    bind("c", "copy it", "c"),
	Rerun:   bind("enter", "re-run it", "enter"),
	Snippet: bind("s", "save it as a snippet", "s"),
	Delete:  bind("x", "delete it", "x"),
	Rename:  bind("r", "rename it", "r"),
	Fix:     bind("f", "show the fix", "f"),
	Again:   bind("r", "run the checks again", "r"),
	// Apply is the one key on a supporting screen that changes the machine
	// rather than reporting on it, and it is why doctor grew a confirm: none
	// of these screens writes without asking first
	// (docs/interface/surfaces.md#the-supporting-screens).
	Apply: bind("a", "apply it", "a"),

	Worked: bind("y", "worked", "y"),
	Failed: bind("n", "did not", "n"),
	Skip:   bind("s", "skip", "s"),
}

// OneShotKeys are the action bar's: the row under a generated command,
// which is the one surface in the product where a bare letter is live beside
// no input at all.
type OneShotKeys struct {
	// Run is enter, whose words depend on the command's rating: it runs a
	// safe one and shows what a dangerous one would touch. One key,
	// two readings — the words are the bar's, the key is the register's.
	Run          Binding
	Confirm      Binding
	Step         Binding
	DryRun       Binding
	Edit         Binding
	Revise       Binding
	Back         Binding
	Alternatives Binding
	Explain      Binding
	Copy         Binding
	Save         Binding
	Quit         Binding
}

var OneShot = OneShotKeys{
	Run:          bind("enter", "run", "enter"),
	Confirm:      bind("y", "run it", "y"),
	Step:         bind("t", "step by step", "t"),
	DryRun:       bind("d", "dry run", "d"),
	Edit:         bind("e", "edit", "e"),
	Revise:       bind("r", "revise", "r"),
	Back:         bind("u", "back", "u"),
	Alternatives: bind("a", "the other commands", "a"),
	Explain:      bind("x", "explain", "x"),
	Copy:         bind("c", "copy", "c"),
	Save:         bind("s", "save", "s"),
	// The bar is the whole screen while it is up, so the letter every
	// full-screen viewer in shhh leaves on is live here too. The hint prints
	// the chord a reader reaches for first and answers to both.
	Quit: bind("esc", "quit", "esc", "q"),
}

// PlanKeys are the plan-approval card's own three: the five rows are the
// selector family's, so the card answers Select.MoveJK, Select.Take and
// Select.Cancel like every other list, and these are what it has that a
// selector does not.
type PlanKeys struct {
	// Jump takes a row by its number, which the card prints beside each one.
	// One binding for the five digits rather than five: what a reader learns
	// is "the number on the row", and the handler reads which digit from the
	// keystroke the way a pair reads which half.
	Jump Binding
	// Save writes the plan to a file. It is on the card rather than on a
	// card of its own because saving is not a decision — it is something you
	// do on the way to one — so it neither answers the question nor takes
	// the card down.
	Save Binding
	// Implement approves the plan and runs it in a new session, which is the
	// one answer that is both where and how: it crosses the boundary
	// Wait.NewSession crosses and starts the execution turn on the far side,
	// in the mode its words name
	// (docs/capabilities/coding-agent.md#an-approved-plan-is-an-artifact).
	Implement Binding
}

var Plan = PlanKeys{
	Jump:      bind("1–5", "jump to a row", "1", "2", "3", "4", "5"),
	Save:      bind("s", "save the plan", "s", "S"),
	Implement: bind("i", "implement in a new session", "i"),
}

// QueryKeys are the filter row's own. Every list in the product that filters
// opens the same row and answers the same key on it, so it is declared once
// here rather than three times in three groups: a row being typed into takes
// its text from what was typed, and the one keystroke on it that is neither
// text nor a way out is the one that takes a rune back.
type QueryKeys struct {
	Rub Binding
}

var Query = QueryKeys{
	Rub: bind("backspace", "delete a character", "backspace"),
}

// SetupKeys are first contact's and the provider card's.
type SetupKeys struct {
	Wizard Binding
	Paste  Binding
	Local  Binding
}

var Setup = SetupKeys{
	Wizard: bind("enter", "setup wizard", "enter"),
	Paste:  bind("p", "paste a key", "p"),
	Local:  bind("o", "a local model", "o"),
}

// RewindKeys are the rewind scope card's — the card the /rewind picker opens
// once a turn has been taken (docs/interface/surfaces.md#the-rewind).
//
// A rewind is two rewinds arriving as one word: the files a run of turns
// wrote, and the turns themselves. The card offers each on its own and both
// together, because going back to before a turn and leaving what it wrote on
// disk is a real answer and so is the reverse — and the person is the only
// one who knows which they meant.
//
// It is a takeover, so its letters are bare: the picker that opened it had
// already taken the keyboard, and there is nothing left to hand over.
type RewindKeys struct {
	// Both is the usual reading of "go back", and the first key offered.
	Both Binding
	// Code puts the files back and keeps the conversation: the approach was
	// wrong, the knowledge was not.
	Code Binding
	// Talk rewinds the conversation and leaves the files: the model went
	// down a bad path, and the work it left is worth keeping. It is the one
	// answer that still works past a turn whose code cannot be restored.
	Talk   Binding
	Cancel Binding

	// Diff is the picker's own key rather than the card's: the change a
	// rewind to the row under the pointer would take back, read full screen
	// before the row is taken. It is `d` for the diff it opens, and like
	// every letter on a card that opens as a search it is live once the
	// query row is closed.
	Diff Binding
}

// All is the card's keys in the order it offers them.
func (k RewindKeys) All() []Binding { return []Binding{k.Both, k.Code, k.Talk, k.Cancel} }

var Rewind = RewindKeys{
	Both: bind("b", "both", "b"),
	Code: bind("c", "code only", "c"),
	Talk: bind("t", "talk only", "t"),
	// The words say what is still standing rather than "cancel": nothing was
	// restored, nothing left the window, and the picker is still there
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Cancel: bind("esc", "don't", "esc"),

	Diff: bind("d", "what the turns after it changed", "d"),
}
