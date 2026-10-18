package components

import "charm.land/lipgloss/v2"

// State is what a thing the product draws is doing or how it ended: a tool
// row, a child, a command, a turn, a process, a screen's row. It is the one
// key of the one table below, so a state learned on a row is the state on a
// close, an agent row and a screen
// (docs/interface/principles.md#closed-vocabularies).
//
// The table answers three questions per state: the glyph, the palette token
// the glyph and its word are painted in, and the word. A surface that has an
// exception takes it as a parameter on its own mapper — a read's glyph is
// chrome, a fan-out lane keeps its kind glyph, a turn's close draws its
// finished mark as `∗` — and never as a second table.
type State int

const (
	StateQueued    State = iota // · accepted, not started
	StateRunning                // ▸ in flight; a host that ticks draws the frame's spinner
	StateChecking               // ✦ the classifier is deciding
	StateOK                     // a command returned: the kind glyph stands, the word says it
	StateDone                   // ✓ a check passed, a turn or compaction came out well
	StateClosed                 // ∗ a turn's close: it worked, and here is what it cost
	StateFailed                 // ✗ it broke
	StateDenied                 // ⊘ you said no, or a rule did
	StateCancelled              // ⊘ you stopped it while it ran
	StateStalled                // ⚠ recoverable: it will resume, or you can steer it
	StateWaiting                // ⚠ stopped on something only you can answer
	StateRestored               // ↺ picked up from a checkpoint
	StateOff                    // ⊘ not in play: turned off, or waiting to be steered
	StateUnknown                // · nothing was kept to say
	StatePaused                 // ⚠ a turn held at its round limit
	StateCurrent                // ▸ the working step the agent is on
	StateToDo                   // · a step not reached yet
	StateUp                     // ✓ a tool source that is serving
	StateBlocked                // ⚠ a tool source that is refused until you act
	StateStarting               // ▸ a tool source that is still coming up
	StateError                  // ✗ a tool source that broke
	StateIdle                   // ⊘ a child whose turn was cancelled, waiting to be steered
	stateCount
)

// stateRow is one row of the state table: the mark, the token it is painted
// in, the word that rides beside it, and what the key list's legend says it
// means. The mark is empty for the one state that keeps the kind's glyph.
type stateRow struct {
	mark  string
	tone  func(styles) lipgloss.Style
	word  string
	means string
}

func errTone(s styles) lipgloss.Style { return s.err }
func addTone(s styles) lipgloss.Style { return s.add }

// stateTable is the one table. `ok` and `✓` are separate rows on purpose:
// they are different claims — a command returned, a check passed — and a
// row that merged them would say the second of a command that only said the
// first.
//
// Cancelled shares ⊘ with a refusal, because ⊘ is the mark of the reader's
// own no wherever it already stood — on a stopped turn, a stopped command, a
// stopped process, a request they stopped — and a new mark would have taught
// a second glyph for a fact the reader already reads one way. The word beside
// it is what tells the two apart, and the legend names both
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
var stateTable = [stateCount]stateRow{
	StateQueued:    {"·", dimTone, "queued", "accepted, not started"},
	StateRunning:   {"▸", spinTone, "running…", "running"},
	StateChecking:  {"✦", spinTone, "checking", "the classifier is deciding"},
	StateOK:        {"", addTone, "ok", "a command returned"},
	StateDone:      {"✓", addTone, "done", "a check passed, a turn or a compaction came out well"},
	StateClosed:    {"∗", dimTone, "worked", "a turn's close"},
	StateFailed:    {"✗", errTone, "failed", "failed"},
	StateDenied:    {"⊘", dimTone, "denied", "refused: dim by you, red by a rule"},
	StateCancelled: {"⊘", dimTone, "cancelled", "stopped by you while it ran"},
	StateStalled:   {"⚠", accentTone, "stalled", "stalled, and will resume or can be steered"},
	StateWaiting:   {"⚠", errTone, "needs you", "stopped on something only you can answer"},
	StateRestored:  {"↺", dimTone, "restored", "picked up from a checkpoint"},
	StateOff:       {"⊘", dimTone, "off", "not in play"},
	StateUnknown:   {"·", dimTone, "unknown", "nothing was kept to say"},
	StatePaused:    {"⚠", accentTone, "paused", "a turn held at its round limit"},
	StateCurrent:   {"▸", spinTone, "current", "the step the agent is on"},
	StateToDo:      {"·", dimTone, "to do", "a step not reached yet"},
	StateUp:        {"✓", addTone, "up", "a tool source that is serving"},
	StateBlocked:   {"⚠", accentTone, "blocked", "a tool source waiting on you"},
	StateStarting:  {"▸", spinTone, "starting", "a tool source still coming up"},
	StateError:     {"✗", errTone, "error", "a tool source that broke"},
	StateIdle:      {"⊘", dimTone, "idle", "a child waiting to be steered"},
}

// Glyph is the state's mark, unpainted: for the callers whose row runs the
// label through its own emphasis and cannot hold an escape sequence.
func (s State) Glyph() string { return stateTable[s].mark }

// Word is the state's word. It is drawn wherever the glyph is, which is what
// lets two states share a mark on a terminal with no colour.
func (s State) Word() string { return stateTable[s].word }

// WordBy is the word with who did it: `cancelled by you`. The word is the
// table's and the qualifier is the surface's, so a row never spells the state
// a second way to say who acted.
func (s State) WordBy(who string) string { return stateTable[s].word + " by " + who }

// Mark is the glyph in the state's token.
func (s State) Mark() string { return stateTable[s].tone(sty).Render(stateTable[s].mark) }

// Painted is the word in the state's token.
func (s State) Painted() string { return stateTable[s].tone(sty).Render(stateTable[s].word) }

// KindGlyph is the kind of act's mark, unpainted, from the same register the
// activity row draws it from.
func KindGlyph(k ActivityKind) string {
	if int(k) < len(kindGlyphs) {
		return kindGlyphs[k].mark
	}
	return kindGlyphs[ActivityTool].mark
}

// state is the table row an activity row's state reads. A row that simply
// finished is StateOK, which has no mark of its own.
func (s ActivityState) state() State {
	switch s {
	case ActivityQueued:
		return StateQueued
	case ActivityRunning:
		return StateRunning
	case ActivityChecking:
		return StateChecking
	case ActivityFailed:
		return StateFailed
	case ActivityDenied:
		return StateDenied
	}
	return StateOK
}

// stateLegend is every state with a mark of its own, as the key list draws
// them, in the order the legend reads. The marks two states share are named
// once per state, which is the legend's half of the rule that the word is
// drawn wherever the glyph is.
var stateLegend = [...]State{
	StateQueued, StateRunning, StateChecking, StateDone, StateClosed, StateFailed,
	StateDenied, StateCancelled, StateStalled, StateWaiting, StateRestored, StateOff,
}
