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
// navigation key, because a bare letter here is a letter (invariant 5) —
// except KeyList, which is live only while the draft is empty and there is
// no sentence for it to be a letter of.
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
	Queue     Binding
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
	// the word suggests is spent — b is the agent manager, t is the
	// reasoning level — so this is simply the chord the register still had
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
	KeyList   Binding

	// Suspend hands the terminal back to the shell, and Redraw takes the
	// screen back. Neither is shhh's own idea: ctrl+z is what the shell does
	// with a foreground job, and ctrl+l is what every full-screen program
	// repaints on. A chord the reader's hands already produce is a chord
	// this surface has to answer, or the reflex lands somewhere worse.
	Suspend Binding
	Redraw  Binding

	// Answer is the handover: the one key a decision that arrived on top
	// of a sentence answers to, and the reason every other letter on the
	// card stays a letter.
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
	// empty draft and tab, which is what the `?` list says beside it.
	Palette:   bind("ctrl+/", "the command palette", "ctrl+/", "ctrl+_"),
	Reasoning: bind("ctrl+t", "cycle the reasoning level", "ctrl+t", "alt+t"),
	Mode:      bind("shift+tab", "cycle the permission mode", "shift+tab"),

	// The chord the palette had. It is spent here because holding is the act
	// that has to be reachable without looking — the person reaching for it
	// is about to lose the network — and `p` is the letter both halves of it
	// are named after.
	Pause: bind("ctrl+p", "hold the turn between rounds, or let a held one go", "ctrl+p"),

	HistoryPrev:   bind("↑", "recall the previous input", "up"),
	HistoryNext:   bind("↓", "the next one", "down"),
	HistorySearch: bind("ctrl+r", "search the input history", "ctrl+r"),

	PointUp:   bind("shift+↑", "move the pointer up a row of the pane", "shift+up"),
	PointDown: bind("shift+↓", "down a row", "shift+down"),
	Open:      bind("shift+→", "open or run the pointed row", "shift+right"),
	Close:     bind("shift+←", "close it", "shift+left"),
	PageUp:    bind("pgup", "page the transcript", "pgup"),
	PageDown:  bind("pgdn", "page it back", "pgdown"),

	Reading: bind("ctrl+o", "reading mode", "ctrl+o"),
	// The manager is on alt with the rest of the agent family — alt+[ and
	// alt+] walk the sessions, alt+a opens the list of them — because
	// ctrl+b is tmux's prefix and never reaches the program there, and
	// every ctrl letter the terminal delivers is spent or the line
	// editor's (docs/interface/reserved-keys.md). What alt costs is the
	// Option key: a stock Mac terminal composes a character for it until
	// the profile is told to send the escape prefix, so the doctor has a
	// row that reads the setting and the key list names it beside these
	// (docs/interface/reserved-keys.md#what-is-left).
	Agents:  bind("alt+a", "the agent manager", "alt+a"),
	Backlog: bind("ctrl+f", "the backlog screen", "ctrl+f"),

	// The brackets are next and previous in the shape the key caps already
	// have, and they are free twice over: nothing else in the register
	// answers them, and neither does the textarea, which spends its own alt
	// chords on words, case and the ends of the input. They are also the two
	// alt chords that have to be checked rather than assumed — esc-[ and
	// esc-] are the introducers for the terminal's own control sequences —
	// and the input decoder resolves both, reading a bare pair as the key
	// rather than as the start of something longer.
	NextAgent: bind("alt+]", "the next session in the rail's map", "alt+]"),
	PrevAgent: bind("alt+[", "the previous one", "alt+["),

	Mouse:   bind("ctrl+x", "mouse reporting on or off", "ctrl+x"),
	KeyList: bind("?", "the keys, on an empty draft", "?"),

	Suspend: bind("ctrl+z", "suspend shhh (idle only)", "ctrl+z"),
	Redraw:  bind("ctrl+l", "redraw the screen", "ctrl+l"),

	// ctrl+y is the same act, and it is here because ctrl+space does not
	// always arrive. macOS binds it system-wide to "select the previous
	// input source" and takes it before the terminal ever sees it, which
	// leaves the one load-bearing chord in the mid-sentence rule dead on the
	// most common desktop shhh runs on, with no way for the user to move it.
	//
	// The spelling stays ctrl+space, because it is the one that works
	// everywhere else and a hint offering two chords teaches neither. The
	// alias is in the `?` list and in /help, which is where somebody whose
	// chord does nothing goes looking.
	//
	// ctrl+y is free rather than merely unused: it is not in the bubbles
	// textarea keymap, and it is not one of the readline chords the draft
	// deliberately leaves to the line editor (see DraftKeys).
	Answer: bind("ctrl+space", "answer it", "ctrl+space", "ctrl+y"),

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
	Move     Binding
	Expand   Binding
	Collapse Binding
	Copy     Binding
	Search   Binding
	Match    Binding
	Half     Binding
	PageUp   Binding
	PageDown Binding
	List     Binding
	Back     Binding
}

// All is reading mode's keys in the order it offers them, which is the order
// `?` lists them in.
func (k ReadingKeys) All() []Binding {
	return []Binding{k.Move, k.Expand, k.Collapse, k.Copy, k.Search, k.Match,
		k.Half, k.PageUp, k.PageDown, k.List, k.Back}
}

var Reading = ReadingKeys{
	Move:     bind("j/k", "move", "k", "j", "up", "down"),
	Expand:   bind("enter", "expand", "enter"),
	Collapse: bind("-", "collapse", "-"),
	// Copy is [y] rather than [c], because c is "continue from here" on a
	// dropped stream's row (RowKeys) and a key is declared once.
	Copy: bind("y", "copy the row", "y"),
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
	Tab:  bind("tab", "the backlog or the archive", "tab"),

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
// of these letters for typing.
type RowKeys struct {
	Review Binding

	// Undo is `[u]`, which two rows offer with different words, the way
	// Retry below does: a turn's changeset row puts the files back, and the
	// notice an automatic steer left takes that message out of the
	// conversation. Both are the same gesture on the row in front of you —
	// undo what this row is telling you about — and both are answered by
	// reading mode standing on it, so the draft keeps the letter either way.
	Undo Binding

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

	// Reopen is `[o]` on the row a backlog run blocked on: the item it
	// blocked goes back to open, which is the one act a reader who has just
	// read the block wants and would otherwise have to compose a command
	// for. It is `o` because that is the word — the textual form is
	// `/todo open` — and because every other letter on a transcript row is
	// already spent.
	Reopen Binding

	// Commit is the offer a turn's changed-files row makes while its
	// changeset is uncommitted: banking the turn is one key rather than a
	// sentence somebody composes.
	//
	// It is `[g]` for git rather than the letter the word starts with,
	// because `[c]` is "continue from here" on a dropped stream's row and a
	// key is declared once — the same reason reading mode's copy is `[y]`.
	// The word beside it carries the act, which is what a letter never has
	// to (docs/interface/principles.md#colour-never-carries-meaning-alone).
	Commit Binding

	// Rerun is `[t]` on the row a turn's checks left: the suite runs again
	// over the tree as it now stands. It is offered only where there is a
	// suite to run again — a command the turn happened to run is not one,
	// because re-running that would be shhh choosing to execute a line
	// nobody is looking at.
	Rerun Binding
}

var Row = RowKeys{
	Review: bind("v", "review", "v"),
	Undo:   bind("u", "undo turn", "u"),

	Retry:    bind("r", "try again", "r"),
	Continue: bind("c", "continue from here", "c"),
	Key:      bind("e", "enter a new key", "e"),
	Provider: bind("p", "switch provider", "p"),

	Rounds: bind("+", "more rounds", "+"),
	Uncap:  bind("!", "let it run", "!"),

	Reopen: bind("o", "reopen the item", "o"),

	Commit: bind("g", "commit", "g"),
	Rerun:  bind("t", "run the checks again", "t"),
}

// CommitKeys are the commit card's — the card a turn's changed-files row
// opens, which stages exactly what that turn changed and nothing else
// (docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too).
//
// It is a takeover rather than a card beside the draft, because the row that
// opened it was answered from reading mode: the keyboard was already out of
// the draft when the card arrived, so there is nothing to hand over and every
// letter here is live.
type CommitKeys struct {
	Take Binding
	// Edit opens the proposed message as a draft. It is the letter the word
	// starts with, free here for the reason every bare letter on a takeover
	// is: nothing else is listening while the card is up.
	Edit Binding
	// Hunks opens the staging surface that /diff and a review open, rather
	// than a second one: what may be committed is one question with one
	// answer, however it was reached.
	Hunks  Binding
	Cancel Binding
}

// All is the card's keys in the order it offers them.
func (k CommitKeys) All() []Binding {
	return []Binding{k.Take, k.Edit, k.Hunks, k.Cancel}
}

var Commit = CommitKeys{
	Take:  bind("enter", "commit", "enter"),
	Edit:  bind("e", "edit the message", "e"),
	Hunks: bind("s", "pick hunks", "s"),
	// The words say what is left standing rather than "cancel": a changeset
	// nobody committed is still there, and so is the offer
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Cancel: bind("esc", "don't", "esc"),
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

var Decision = DecisionKeys{
	Allow:      bind("y", "allow", "y", "enter"),
	Deny:       bind("n", "deny", "n", "esc", "ctrl+c"),
	AllowNoted: bind("Y", "allow, and say what to do next", "Y"),
	DenyNoted:  bind("N", "deny, and say why", "N"),
	Always:     bind("a", "allow without asking — choose how long", "a"),
	Batch:      bind("A", "open the queue", "A"),
	Diff:       bind("d", "full diff", "d", "D"),
	DryRun:     bind("t", "try the harmless form", "t"),
	Explain:    bind("x", "explain what the command does", "x"),
	Amend:      bind("e", "edit the command before it runs", "e"),
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

// ReviewKeys are review mode's: a takeover over the whole screen. Nothing
// it does is applied.
//
// The hunk key and the file key are declared apart because what staging can
// honour is the host's, not the surface's: a patch a child is offering is
// separable hunk by hunk, and a turn already on disk is put back a file at a
// time, so a host that can only act per file promotes the file key and says
// what staging one hunk of five really costs.
type ReviewKeys struct {
	MoveFile   Binding
	MoveHunk   Binding
	StageHunk  Binding
	StageFile  Binding
	StageAll   Binding
	SideBySide Binding
	PageUp     Binding
	PageDown   Binding
	Apply      Binding
	Back       Binding
}

var Review = ReviewKeys{
	MoveFile: bind("j/k", "file", "k", "j", "up", "down"),
	MoveHunk: bind("n/p", "hunk", "p", "n"),
	// s and S rather than space and s: the two staging keys are one
	// gesture apart, and the shifted one is the whole file, which is the
	// bigger act. Space stays bound to the hunk because a box in a list is
	// a thing people press space on.
	StageHunk:  bind("s", "stage hunk", "s", " ", "space"),
	StageFile:  bind("S", "file", "S"),
	StageAll:   bind("A", "all", "a", "A"),
	SideBySide: bind("\\", "side by side", "\\"),
	PageUp:     bind("pgup", "page up", "pgup"),
	PageDown:   bind("pgdn", "page down", "pgdown"),
	Apply:      bind("enter", "take the staged hunks", "enter"),
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
	Retry  Binding
	Cancel Binding
	Kill   Binding
	Back   Binding
	Detach Binding
}

var Agent = AgentKeys{
	Move:   bind("j/k", "move", "k", "j", "up", "down"),
	Attach: bind("enter", "attach", "enter"),
	Go:     bind("g", "go to the agent that asked", "g"),
	Answer: bind("a", "answer", "a"),
	Retry:  bind("r", "retry", "r"),
	Cancel: bind("x", "cancel", "x"),
	Kill:   bind("X", "kill", "X"),
	Back:   bind("esc", "back", "esc", "ctrl+c"),
	Detach: bind("esc", "back to your own session", "esc"),
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
	Note: bind("tab", "note or options", "tab"),
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

// PreviewKeys are the staged attachment preview's. Two keys and no more:
// the surface has nothing to decide, nothing to scroll and nothing to stage —
// a thumbnail is fitted to the pane and a paste is clipped with what did not
// fit counted at the foot, rather than either being panned around — so what it
// offers is the two spellings of leaving that every full-screen viewer in
// shhh has always answered to.
type PreviewKeys struct {
	Back  Binding
	Leave Binding
}

var Preview = PreviewKeys{
	Back:  bind("esc", "back", "esc"),
	Leave: bind("q", "back", "q", "ctrl+c"),
}

// ScreenKeys are the supporting TUIs': `shhh config`, `shhh history`,
// `shhh metrics`, `shhh doctor`, `shhh snippets` and the saved-chat browser.
// They are where `?` was invented — the compact key row swapped for the whole
// list, in place — which is the idiom reading mode borrows.
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
	Run:          bind("↵", "run", "enter"),
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

// PlanKeys are the plan-approval card's own two: the five rows are the
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
}

var Plan = PlanKeys{
	Jump: bind("1–5", "jump to a row", "1", "2", "3", "4", "5"),
	Save: bind("s", "save the plan", "s", "S"),
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
	Rub: bind("backspace", "take a rune back", "backspace"),
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
