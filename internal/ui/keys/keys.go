// Package keys is shhh's key register (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Before it existed, a key was written down twice: once as a literal in the
// handler that answers it, and once as prose in the hint that offers it. The
// two lived in different files and nothing made them agree — sixty-eight
// literals across twenty files, and a `/help` that had never heard of ctrl+g,
// which is the single most load-bearing chord in the mid-sentence rule.
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
//	- It is not a rebinding layer. Nothing here reads config yet. The shape
//	  is the one that would make rebinding a config change rather than a
//	  code change, and that is as far as the register goes.
package keys

import (
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

// Shown is the spelling a hint prints, bare. Brackets are the surface's
// business: a card writes `[y]`, a key row writes `y`, and both read the
// same binding.
func Shown(b Binding) string { return b.Help().Key }

// Bracket is the bracketed spelling, which is what most of the product draws.
func Bracket(b Binding) string { return "[" + b.Help().Key + "]" }

// Words are the binding's own words. A surface that means something more
// specific says so instead — the register is not a style guide, and "try
// again" is worse than "ask again from scratch" on the row that means the
// second thing.
func Words(b Binding) string { return b.Help().Desc }

// Draft is the framed input: the keys that are live while the sentence
// being typed holds the keyboard. Every one of them is a chord or a
// navigation key, because a bare letter here is a letter (invariant 5).
type DraftKeys struct {
	Send      Binding
	Newline   Binding
	Attach    Binding
	Complete  Binding
	Palette   Binding
	Reasoning Binding
	Mode      Binding

	HistoryPrev Binding
	HistoryNext Binding

	ScrollUp   Binding
	ScrollDown Binding
	PageUp     Binding
	PageDown   Binding

	Reading Binding
	Detail  Binding
	Agents  Binding
	Mouse   Binding

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
	Send:      bind("enter", "send the message", "enter"),
	Newline:   bind("shift+enter", "insert a newline", "shift+enter", "alt+enter", "ctrl+j"),
	Attach:    bind("ctrl+v", "attach the clipboard", "ctrl+v"),
	Complete:  bind("tab", "complete a slash command", "tab"),
	Palette:   bind("ctrl+k", "the command palette", "ctrl+k"),
	Reasoning: bind("ctrl+r", "cycle the reasoning level", "ctrl+r"),
	Mode:      bind("shift+tab", "cycle the permission mode", "shift+tab"),

	HistoryPrev: bind("↑", "recall the previous input", "up"),
	HistoryNext: bind("↓", "the next one", "down"),

	ScrollUp:   bind("shift+↑", "scroll the transcript a line", "shift+up", "ctrl+up"),
	ScrollDown: bind("shift+↓", "scroll it back", "shift+down", "ctrl+down"),
	PageUp:     bind("pgup", "page the transcript", "pgup"),
	PageDown:   bind("pgdn", "page it back", "pgdown"),

	Reading: bind("ctrl+e", "reading mode", "ctrl+e"),
	Detail:  bind("ctrl+o", "one step's detail", "ctrl+o"),
	Agents:  bind("ctrl+a", "the agent manager", "ctrl+a"),
	Mouse:   bind("ctrl+x", "mouse reporting on or off", "ctrl+x"),

	Answer: bind("ctrl+g", "answer it", "ctrl+g"),

	Clear:  bind("esc", "clear the input", "esc"),
	Cancel: bind("ctrl+c", "cancel the response, then the input", "ctrl+c"),
	Quit:   bind("ctrl+d", "quit", "ctrl+d"),
}

// ReadingKeys are reading mode's own. It is a takeover, so its letters
// are live because nothing else is listening.
type ReadingKeys struct {
	Move     Binding
	Expand   Binding
	Detail   Binding
	Collapse Binding
	PageUp   Binding
	PageDown Binding
	List     Binding
	Back     Binding
}

// All is reading mode's keys in the order it offers them, which is the order
// `?` lists them in.
func (k ReadingKeys) All() []Binding {
	return []Binding{k.Move, k.Expand, k.Detail, k.Collapse, k.PageUp, k.PageDown, k.List, k.Back}
}

var Reading = ReadingKeys{
	Move:     bind("j/k", "move", "j", "k", "down", "up"),
	Expand:   bind("enter", "expand", "enter"),
	Detail:   bind("ctrl+o", "step detail", "ctrl+o"),
	Collapse: bind("-", "collapse", "-"),
	PageUp:   bind("pgup", "page up", "pgup"),
	PageDown: bind("pgdn", "page down", "pgdown"),
	// List is the same `?` the supporting TUIs offer: the
	// compact key row swapped for the whole list, in place, and swapped back
	// by the same key. It is live here and nowhere near the draft, for the
	// reason every bare letter in this file is.
	List: bind("?", "keys", "?"),
	Back: bind("q", "back to the prompt", "q", "esc", "ctrl+e", "ctrl+c"),
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
	Move: bind("↑↓/jk", "move", "up", "down", "j", "k"),
	// One key both folds and unfolds. A surface whose every group is a fold
	// would spend a second key saying what the glyph on the row already says.
	Expand: bind("enter", "expand or fold", "enter"),
	List:   bind("?", "keys", "?"),
	Back:   bind("q", "back to the prompt", "q", "esc", "ctrl+c"),
}

// RowKeys are the offers a transcript row carries. They are the register's
// awkward corner and its own subject: passive entries whose keys are answered
// by reading mode standing on the row, which is why the input keeps every one
// of these letters for typing.
type RowKeys struct {
	Review Binding
	Undo   Binding

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
	Review: bind("v", "review", "v"),
	Undo:   bind("u", "undo turn", "u"),

	Retry:    bind("r", "try again", "r"),
	Continue: bind("c", "continue from here", "c"),
	Key:      bind("e", "enter a new key", "e"),
	Provider: bind("p", "switch provider", "p"),

	Rounds: bind("+", "more rounds", "+"),
	Uncap:  bind("!", "let it run", "!"),
}

// DecisionKeys are the approval card's, the `/run` confirm's, the plan
// card's and a child's routed approval's. They arrive ungated:
// live only once Draft.Answer has handed the keyboard over, or on a card that
// landed on a draft nobody was typing into.
type DecisionKeys struct {
	Allow  Binding
	Deny   Binding
	Always Binding
	Batch  Binding
	Diff   Binding
}

var Decision = DecisionKeys{
	Allow:  bind("y", "allow", "y", "Y", "enter"),
	Deny:   bind("n", "deny", "n", "N", "esc", "ctrl+c"),
	Always: bind("a", "always allow this session", "a"),
	Batch:  bind("A", "answer the marked", "A"),
	Diff:   bind("d", "full diff", "d", "D"),
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
	Move    Binding
	MoveJK  Binding
	Take    Binding
	Alt     Binding
	Filter  Binding
	ClearQ  Binding
	Toggle  Binding
	All     Binding
	Note    Binding
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
	MoveJK: bind("↑↓/jk", "move", "up", "down", "j", "k"),
	Take:   bind("enter", "select", "enter"),
	Alt:    bind("d", "make it the default", "d"),
	Filter: bind("/", "filter", "/"),
	ClearQ: bind("ctrl+u", "clear the filter", "ctrl+u"),
	Toggle: bind("space", "toggle", " ", "space"),
	All:    bind("a", "all or none", "a"),
	Note:   bind("tab", "note or options", "tab"),
	Cancel: bind("esc", "cancel", "esc", "ctrl+c"),
	Quit:   bind("ctrl+d", "quit", "ctrl+d"),
	Palette: PaletteKeys{
		Prev:  bind("↑", "move", "up", "ctrl+p"),
		Next:  bind("↓", "move", "down", "ctrl+n"),
		Run:   bind("enter", "run it", "enter"),
		Write: bind("tab", "write it into the input", "tab"),
	},
}

// ReviewKeys are review mode's: a takeover over the whole screen, with
// staging per hunk. Nothing it does is applied.
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
	MoveFile:   bind("j/k", "file", "j", "k", "down", "up"),
	MoveHunk:   bind("n/p", "hunk", "n", "p"),
	StageHunk:  bind("space", "stage hunk", " ", "space"),
	StageFile:  bind("s", "file", "s"),
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
	Answer Binding
	Retry  Binding
	Cancel Binding
	Kill   Binding
	Back   Binding
	Detach Binding
}

var Agent = AgentKeys{
	Move:   bind("j/k", "move", "j", "k", "down", "up"),
	Attach: bind("enter", "attach", "enter"),
	Answer: bind("a", "answer", "a"),
	Retry:  bind("r", "retry", "r"),
	Cancel: bind("x", "cancel", "x"),
	Kill:   bind("X", "kill", "X"),
	Back:   bind("esc", "back", "esc", "ctrl+c"),
	Detach: bind("esc", "back to your own session", "esc"),
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
	Scroll:     bind("j/k", "scroll", "j", "k", "down", "up"),
	SideBySide: bind("s", "side-by-side", "s"),
	Hunk:       bind("n/p", "hunk", "n", "p"),
	Back:       bind("esc", "back", "esc"),
	Leave:      bind("q", "back", "q", "ctrl+c"),
}

// PictureKeys are the staged image preview's. Two keys and no more:
// the surface has nothing to decide, nothing to scroll and nothing to stage —
// a thumbnail is fitted to the pane rather than panned around — so what it
// offers is the two spellings of leaving that every full-screen viewer in
// shhh has always answered to.
type PictureKeys struct {
	Back  Binding
	Leave Binding
}

var Picture = PictureKeys{
	Back:  bind("esc", "back", "esc"),
	Leave: bind("q", "back", "q", "ctrl+c"),
}

// ScreenKeys are the supporting TUIs': `shhh config`, `shhh history`,
// `shhh metrics`, `shhh doctor`. They are where `?` was invented — the
// compact key row swapped for the whole list, in place — which is the idiom
// reading mode borrows.
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
	Fix     Binding
	Again   Binding
	Apply   Binding
}

var Screen = ScreenKeys{
	Move:   bind("↑↓/jk", "move", "up", "down", "j", "k"),
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
	Fix:     bind("f", "show the fix", "f"),
	Again:   bind("r", "run the checks again", "r"),
	// Apply is the one key on a supporting screen that changes the machine
	// rather than reporting on it, and it is why doctor grew a confirm: none
	// of these screens writes without asking first
	// (docs/interface/surfaces.md#the-supporting-screens).
	Apply: bind("a", "apply it", "a"),
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
	Quit:         bind("esc", "quit", "esc"),
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

// BrowseKeys are the saved-chat browser's.
type BrowseKeys struct {
	Move   Binding
	Open   Binding
	Filter Binding
	Action Binding
	Prev   Binding
	Take   Binding
	Back   Binding
	Quit   Binding
	// Leave is the detail pane's `q`. The list's Quit answers esc as well;
	// in the detail esc is Back, so the two cannot be one binding.
	Leave Binding
}

var Browse = BrowseKeys{
	Move:   bind("j/k", "move", "j", "k", "down", "up"),
	Open:   bind("enter", "open it", "enter", "l", "right"),
	Filter: bind("/", "filter", "/"),
	Action: bind("tab", "next action", "tab", "right"),
	Prev:   bind("shift+tab", "the previous one", "shift+tab"),
	Take:   bind("enter", "take it", "enter"),
	Back:   bind("esc", "back to the list", "esc", "h", "left"),
	Quit:   bind("q", "quit", "q", "esc"),
	Leave:  bind("q", "quit", "q"),
}
