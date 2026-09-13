package components

// The running turn's status line (
// docs/interface/surfaces.md#the-input-frame). While a turn runs, this
// is the one line on screen that changes: a spinner frame, the phase, and
// ticking elapsed. It lives in the frame's activity slot and it *resolves
// into* the turn summary rather than being replaced by one — same line,
// `✓` where the spinner was, and the finished turn's account where the
// running line carried none.
//
// The running line states no account, and states none whatever a host puts
// in Cost: a turn in flight can only be priced at the full input rate,
// because the split between fresh and cached prompt reads arrives with the
// provider's report and not before. A dollar figure struck here is high by
// whatever the cache served, and it is high beside the billed totals the
// rails below carry — the newest figure on the frame, and the only wrong one.
// Tokens leave with it: the vitals rail a row down already states the pair
// (docs/interface/surfaces.md#the-input-frame).
//
// It names no call either. The act itself — the command, what it is doing,
// what it printed a moment ago — is the feed's live row, which has the width
// to hold a command whole and the grammar to bound one that runs past it. A
// copy of it here was the same command a second time, in a slot a hand wide,
// clipped mid-word (docs/interface/surfaces.md#the-activity-row).
//
// What is left is the turn's, and the elapsed says so: `turn 12.4s`, because
// the row below is ticking its own command's clock and two unlabelled figures
// a few rows apart are two readings of one operation to anybody who does not
// already know which is which. The clock is the running line's alone: when
// the turn stops the span is the transcript's, on the row the turn leaves
// behind, which is still carrying it when the turn is ten turns back and this
// line has moved on to the next one
// (docs/interface/surfaces.md#the-input-frame).
//
// Three rules are enforced here rather than left to the hosts. The phases are
// a closed vocabulary of four, so a state nobody defined has to pick the
// nearest rather than invent a fifth. The fields leave in one order as the
// terminal narrows — the resolved line's tool count, then the running line's
// elapsed — and the phase never leaves, because what the turn is doing is the
// thing the line exists to say. And the spinner frame is passed in rather
// than kept, so this line, the running activity row and anything else that
// moves show the same frame from the one tick source.

import "charm.land/lipgloss/v2"

// TurnPhase is the turn status's closed vocabulary. There are four; anything
// else is a phase nobody defined.
type TurnPhase int

const (
	// PhaseThinking is the model reasoning before it acts — the reasoning
	// stream, where a provider has one.
	PhaseThinking TurnPhase = iota
	// PhaseDeciding is the auto-mode classifier judging a call (the vitals
	// rail's `✦ checking`, seen from the frame).
	PhaseDeciding
	// PhaseRunning is a tool executing.
	PhaseRunning
	// PhaseStreaming is prose arriving.
	PhaseStreaming
)

// phaseWords is the vocabulary itself. The phase a call in flight puts the
// turn in is `acting…` and not `running`, because the feed's live row a few
// rows above already states `running…` of the one call it belongs to: one word
// with two subjects on one screen is read as one subject, and the reader who
// has to tell the turn from the command is exactly the reader watching a
// command run. Each vocabulary keeps the word about its own subject — the turn
// acts, over the act the row is reporting, and the command runs
// (docs/interface/surfaces.md#the-input-frame).
var phaseWords = map[TurnPhase]string{
	PhaseThinking:  "thinking…",
	PhaseDeciding:  "deciding…",
	PhaseRunning:   "acting…",
	PhaseStreaming: "streaming…",
}

// Word is the phase's word. A phase outside the vocabulary reads as thinking
// rather than as blank: the nearest of the four is the rule.
func (p TurnPhase) Word() string {
	if w, ok := phaseWords[p]; ok {
		return w
	}
	return phaseWords[PhaseThinking]
}

// Field-drop levels (guidelines/turnstatus-drop-order). Fields leave in this
// order and no other; the phase, the outcome and the resolved cost are not on
// the ladder. The guideline's first rung is gone with the field it shed: the
// line carries no tool argument to drop, so the counts rung — the resolved
// line's tool count — is what a narrowing slot reaches first. Each form
// reaches one of the two rungs with nothing to shed at it: the running line
// has no count, and the resolved line has no clock.
const (
	TurnDropNone    = iota // every field the host supplied
	TurnDropCounts         // the resolved line's tool count goes first
	TurnDropElapsed        // then the running line's elapsed; the floor is the word
)

// TurnStatus is the line. A host fills the live fields while the turn runs
// and the resolved ones when it ends; View picks the widest form that fits.
type TurnStatus struct {
	// Frame is which of the eight braille frames to show, from the host's one
	// tick source. It is also the frame the label's sweep is on, so
	// the glyph and the word beside it move on the same instant.
	Frame int
	// Arriving is how much of the label's entrance is still to run (
	// anim.go). Zero — the value a host that does not stage one leaves — is
	// the settled label. The chat frame fills it from the turn's own age.
	Arriving int
	Phase    TurnPhase
	// Elapsed is the turn's wall time so far, pre-formatted by FormatElapsed:
	// tenths under ten seconds, whole seconds above. It renders behind the
	// word `turn`, which is what says it is not the clock on the command's
	// own row in the feed.
	Elapsed string

	// Done resolves the line into the summary it becomes: the account where
	// the clock was, with the outcome's glyph where the spinner was.
	Done    bool
	Outcome TurnState
	// Tools is what the turn ran and Cost what it was billed. Both are read
	// only when Done, because both are facts a turn has only once it is over.
	// Cost in particular: the host reads it off the close block, where it is
	// the ledger's own per-request total rather than a live pair re-priced at
	// the fresh rate. A field the host cannot report is left out rather than
	// reported as zero.
	//
	// The turn's wall time is not among them. A clock that has stopped is a
	// fact about the past, and the past is the transcript's: the close row
	// the host reads these off states the span, and states it still when the
	// turn has scrolled away from a line that only ever reports the last one
	// (docs/interface/surfaces.md#the-input-frame).
	Tools int
	Cost  string
}

// doneWords is the resolved line's word per outcome. It is lower case where
// the transcript's close row is capitalised: a status line is read
// while it happens, a row in history after the fact.
var doneWords = map[TurnState]string{
	TurnDone:      "done",
	TurnCancelled: "cancelled",
	TurnFailed:    "failed",
}

// doneGlyph is the resolved line's glyph and word. Both carry the outcome, so
// colour never carries it alone (invariant 1).
func (s TurnStatus) doneGlyph() (string, string, lipgloss.Style) {
	switch s.Outcome {
	case TurnCancelled:
		return "⊘", doneWords[TurnCancelled], sty.Dim
	case TurnFailed:
		return "✗", doneWords[TurnFailed], sty.Del
	}
	return "✓", doneWords[TurnDone], sty.Add
}

// View renders the line at the widest fidelity that fits width, dropping in
// the turn status's order. A width that cannot hold even the floor clips it
// rather than rendering nothing: a line that says only what it is doing is
// still the answer to the question the line exists for.
func (s TurnStatus) View(width int) string {
	if width <= 0 {
		return ""
	}
	for drop := TurnDropNone; ; drop++ {
		out := s.render(drop)
		if lipgloss.Width(out) <= width || drop >= TurnDropElapsed {
			return Clip(out, width)
		}
	}
}

// render lays the line out at one drop level, unclipped.
func (s TurnStatus) render(drop int) string {
	if s.Done {
		return s.renderDone(drop)
	}
	label := s.Phase.Word()
	// Elapsed, where the ladder left it standing, rides behind the label as
	// the animation's suffix: it is the host's own styling and the animation
	// never touches it, but it belongs to the same string so the line is
	// measured and clipped as one. The word in front of it is what makes it
	// the turn's clock rather than a second reading of the command's.
	var tail string
	if s.Elapsed != "" && drop < TurnDropElapsed {
		tail += sty.Dim.Render(" · " + turnClock(s.Elapsed))
	}
	// The line's moving part. The spinner's frame leads, outside the sweep
	// because its eight-frame cycle is not the label's; the label arrives
	// cell by cell when the turn starts and carries the light after that.
	return Anim{
		Frame:    s.Frame,
		Arriving: s.Arriving,
		Lead:     Spinner{Frame: s.Frame}.Glyph() + " ",
		Label:    label,
		Suffix:   tail,
	}.View()
}

// turnClock labels a span as the whole turn's. The feed under this line is
// full of clocks — every row that ran carries its own, and the command
// running right now is ticking one — so the frame's says whose it is rather
// than being the second bare figure on the screen
// (docs/interface/surfaces.md#the-input-frame).
func turnClock(span string) string { return "turn " + span }

// renderDone is the resolved line, and the only form that states a cost. It
// sheds the tool count, leaving the outcome and what the turn was billed.
func (s TurnStatus) renderDone(drop int) string {
	glyph, word, style := s.doneGlyph()
	out := style.Render(glyph + " " + word)
	if s.Tools > 0 && drop < TurnDropCounts {
		out += sty.Dim.Render(" · " + plural(s.Tools, "tool"))
	}
	if s.Cost != "" {
		out += sty.Body.Render(" · " + s.Cost)
	}
	return out
}
