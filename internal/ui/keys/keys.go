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
	// FollowUp queues the draft for after the turn — steering joins the
	// running conversation, a follow-up waits for it to finish. Idle, the
	// same chord is a newline: there is no turn to follow, and the chord
	// has inserted newlines on this surface since before it meant anything
	// else, so a terminal that cannot report shift+enter loses nothing.
	FollowUp Binding
	// PullQueued takes the newest queued message — a follow-up first, else
	// steering — back into the draft.
	PullQueued Binding
	Editor     Binding
	Attach     Binding
	Complete   Binding
	Palette    Binding
	Reasoning  Binding
	Mode       Binding

	HistoryPrev   Binding
	HistoryNext   Binding
	HistorySearch Binding

	ScrollUp   Binding
	ScrollDown Binding
	PageUp     Binding
	PageDown   Binding

	Reading Binding
	Agents  Binding
	Mouse   Binding
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
	// card stays a letter.
	Answer Binding

	Clear  Binding
	Cancel Binding
	Quit   Binding
}

// Draft's keys, in the order the input frame and /help name them.
var Draft = DraftKeys{
	Send:       bind("enter", "send the message", "enter"),
	Newline:    bind("shift+enter", "insert a newline", "shift+enter", "ctrl+j"),
	FollowUp:   bind("alt+enter", "queue a follow-up for after the turn (a newline while idle)", "alt+enter"),
	PullQueued: bind("alt+↑", "pull the newest queued message back into the draft", "alt+up"),
	Editor:     bind("ctrl+g", "open the draft in $EDITOR", "ctrl+g"),
	Attach:     bind("ctrl+v", "attach the clipboard", "ctrl+v"),
	Complete:   bind("tab", "complete a slash command", "tab"),
	Palette:    bind("ctrl+p", "the command palette", "ctrl+p"),
	Reasoning:  bind("ctrl+t", "cycle the reasoning level", "ctrl+t", "alt+t"),
	Mode:       bind("shift+tab", "cycle the permission mode", "shift+tab"),

	HistoryPrev:   bind("↑", "recall the previous input", "up"),
	HistoryNext:   bind("↓", "the next one", "down"),
	HistorySearch: bind("ctrl+r", "search the input history", "ctrl+r"),

	ScrollUp:   bind("shift+↑", "scroll the transcript a line", "shift+up", "ctrl+up"),
	ScrollDown: bind("shift+↓", "scroll it back", "shift+down", "ctrl+down"),
	PageUp:     bind("pgup", "page the transcript", "pgup"),
	PageDown:   bind("pgdn", "page it back", "pgdown"),

	Reading: bind("ctrl+o", "reading mode", "ctrl+o"),
	Agents:  bind("ctrl+b", "the agent manager", "ctrl+b"),
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
	// selection, dismisses the completion menu, detaches a level, leaves a
	// waiting decision waiting — and on an empty draft under a running turn
	// it does nothing at all, because the reflex that closes a diff must not
	// abandon minutes of work when the box happens to be empty. Interrupting
	// is the cancel chord's alone
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Clear:  bind("esc", "clear the input", "esc"),
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
	Half     Binding
	PageUp   Binding
	PageDown Binding
	List     Binding
	Back     Binding
}

// All is reading mode's keys in the order it offers them, which is the order
// `?` lists them in.
func (k ReadingKeys) All() []Binding {
	return []Binding{k.Move, k.Expand, k.Collapse, k.Copy, k.Half, k.PageUp, k.PageDown, k.List, k.Back}
}

var Reading = ReadingKeys{
	Move:     bind("j/k", "move", "j", "k", "down", "up"),
	Expand:   bind("enter", "expand", "enter"),
	Collapse: bind("-", "collapse", "-"),
	// Copy is [y] rather than [c], because c is "continue from here" on a
	// dropped stream's row (RowKeys) and a key is declared once.
	Copy: bind("y", "copy the row", "y"),
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

	// Refuse is Deny on a card the reader summoned rather than was handed.
	// Deny folds esc and ctrl+c into the answer, which is right for a card
	// that arrived on its own: there is nothing to go back to, so leaving
	// and declining are the same act. On a summoned card they are not — esc
	// is the way back to a screen the reader chose to leave — and a decline
	// that outlives the session is exactly the consequence esc may never
	// carry (docs/interface/principles.md#esc-is-always-the-safe-answer).
	// So the answer is the letter alone and the way out is Select.Cancel.
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
	Allow:  bind("y", "allow", "y", "Y", "enter"),
	Deny:   bind("n", "deny", "n", "N", "esc", "ctrl+c"),
	Always: bind("a", "always allow this session", "a"),
	Batch:  bind("A", "answer the marked", "A"),
	Diff:   bind("d", "full diff", "d", "D"),
	Refuse: bind("n", "no, and stop offering", "n", "N"),

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
	MoveJK: bind("↑↓/jk", "move", "up", "down", "j", "k"),
	Take:   bind("enter", "select", "enter"),
	Alt:    bind("d", "make it the default", "d"),
	Filter: bind("/", "filter", "/"),
	ClearQ: bind("ctrl+u", "clear the filter", "ctrl+u"),
	Toggle: bind("space", "toggle", " ", "space"),
	All:    bind("a", "all or none", "a"),
	Note:   bind("tab", "note or options", "tab"),
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
	Scroll:     bind("j/k", "scroll", "j", "k", "down", "up"),
	SideBySide: bind("s", "side-by-side", "s"),
	Hunk:       bind("n/p", "hunk", "n", "p"),
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
	Scroll:   bind("j/k", "scroll", "j", "k", "down", "up"),
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
	// Delete and Rename act on the focused chat from the list: the first
	// arms an inline confirm, the second opens a rename row.
	Delete Binding
	Rename Binding
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
	Delete: bind("x", "delete", "x"),
	Rename: bind("r", "rename", "r"),
	Action: bind("tab", "next action", "tab", "right"),
	Prev:   bind("shift+tab", "the previous one", "shift+tab"),
	Take:   bind("enter", "take it", "enter"),
	Back:   bind("esc", "back to the list", "esc", "h", "left"),
	Quit:   bind("q", "quit", "q", "esc"),
	Leave:  bind("q", "quit", "q"),
}
