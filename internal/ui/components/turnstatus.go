package components

// The running turn's status line (S-118, DESIGN-TUI.md §8d, §10c). While a
// turn runs, this is the one line on screen that changes: a spinner frame,
// the phase, ticking elapsed, the turn's live token counts and what they have
// cost. It lives in the frame's activity slot (§12a) and it *resolves into*
// the turn summary rather than being replaced by one — same line, same facts,
// `✓` where the spinner was.
//
// Three rules are enforced here rather than left to the hosts. The phases are
// a closed vocabulary of four, so a state nobody defined has to pick the
// nearest rather than invent a fifth. The fields leave in one order as the
// terminal narrows — tool argument, then token counts, then elapsed — and the
// phase and the cost never leave, because what it is doing and what it is
// costing are the two things the line exists to say. And the spinner frame is
// passed in rather than kept, so this line, the running activity row and
// anything else that moves show the same frame from the one tick source
// (§10c).

import "github.com/charmbracelet/lipgloss"

// TurnPhase is the closed vocabulary of §8d. There are four; anything else is
// a phase nobody defined.
type TurnPhase int

const (
	// PhaseThinking is the model reasoning before it acts — the reasoning
	// stream, where a provider has one.
	PhaseThinking TurnPhase = iota
	// PhaseDeciding is the auto-mode classifier judging a call (the vitals
	// rail's `✦ checking`, seen from the frame).
	PhaseDeciding
	// PhaseRunning is a tool executing, named.
	PhaseRunning
	// PhaseStreaming is prose arriving.
	PhaseStreaming
)

// phaseWords is the vocabulary itself. The running phase carries its argument
// beside it, so it is the one word without an ellipsis.
var phaseWords = map[TurnPhase]string{
	PhaseThinking:  "thinking…",
	PhaseDeciding:  "deciding…",
	PhaseRunning:   "running",
	PhaseStreaming: "streaming…",
}

// Word is the phase's word. A phase outside the vocabulary reads as thinking
// rather than as blank: the nearest of the four is the rule (§8d).
func (p TurnPhase) Word() string {
	if w, ok := phaseWords[p]; ok {
		return w
	}
	return phaseWords[PhaseThinking]
}

// Field-drop levels (§8d, guidelines/turnstatus-drop-order). Fields leave in
// this order and no other; the phase and the cost are not on the ladder.
const (
	TurnDropNone    = iota // every field the host supplied
	TurnDropTool           // the tool argument goes first
	TurnDropTokens         // then the token counts
	TurnDropElapsed        // then elapsed — the floor is phase and cost
)

// TurnStatus is the line. A host fills the live fields while the turn runs
// and the resolved ones when it ends; View picks the widest form that fits.
type TurnStatus struct {
	// Frame is which of the eight braille frames to show, from the host's one
	// tick source (§10c). It is also the frame the label's sweep is on, so
	// the glyph and the word beside it move on the same instant.
	Frame int
	// Arriving is how much of the label's entrance is still to run (§10c,
	// anim.go). Zero — the value a host that does not stage one leaves — is
	// the settled label. The chat frame fills it from the turn's own age.
	Arriving int
	Phase    TurnPhase
	// Tool is the argument beside `running` — the call the grid's own naming
	// gives it (§6c). Read only in PhaseRunning, and the first field dropped.
	Tool string
	// Elapsed is the turn's wall time so far, pre-formatted by FormatElapsed:
	// tenths under ten seconds, whole seconds above.
	Elapsed string
	// Up and Down are the turn's live token counts ("41.2k", "2.1k"); both
	// are needed for either to render, because one arrow alone is half a
	// fact. Cost is what they have cost, and it never drops.
	Up, Down string
	Cost     string

	// Done resolves the line into the summary it becomes: the same fields
	// finished, with the outcome's glyph where the spinner was.
	Done    bool
	Outcome TurnState
	// Duration is the finished turn's wall time and Tools what it ran, both
	// read only when Done. A field the host cannot report is left out rather
	// than reported as zero.
	Duration string
	Tools    int
}

// doneWords is the resolved line's word per outcome. It is lower case where
// the transcript's close row (§16) is capitalised: a status line is read
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
// the §8d order. A width that cannot hold even the floor clips it rather than
// rendering nothing: a line that says only what it is doing is still the
// answer to the question the line exists for.
func (s TurnStatus) View(width int) string {
	if width <= 0 {
		return ""
	}
	for drop := TurnDropNone; ; drop++ {
		out := s.render(drop)
		if lipgloss.Width(out) <= width || drop >= TurnDropElapsed {
			return clip(out, width)
		}
	}
}

// render lays the line out at one drop level, unclipped.
func (s TurnStatus) render(drop int) string {
	if s.Done {
		return s.renderDone(drop)
	}
	label := s.Phase.Word()
	if s.Phase == PhaseRunning && s.Tool != "" && drop < TurnDropTool {
		label += " " + s.Tool
	}
	// The fields the ladder left standing ride behind the label as the
	// animation's suffix (§10c): they are the host's own styling and the
	// animation never touches them, but they belong to the same string so the
	// line is measured and clipped as one.
	var tail string
	if s.Elapsed != "" && drop < TurnDropElapsed {
		tail += sty.Dim.Render(" " + s.Elapsed)
	}
	if s.Up != "" && s.Down != "" && drop < TurnDropTokens {
		tail += sty.Dim.Render(" · ↑" + s.Up + " ↓" + s.Down)
	}
	if s.Cost != "" {
		tail += sty.Body.Render(" · " + s.Cost)
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

// renderDone is the resolved line. It sheds the same fields in the same
// order — the tool argument is spent, the counts become the tool count, and
// the duration is the last thing to go before the outcome and the cost.
func (s TurnStatus) renderDone(drop int) string {
	glyph, word, style := s.doneGlyph()
	out := style.Render(glyph + " " + word)
	if s.Duration != "" && drop < TurnDropElapsed {
		out += sty.Dim.Render(" · " + s.Duration)
	}
	if s.Tools > 0 && drop < TurnDropTokens {
		out += sty.Dim.Render(" · " + plural(s.Tools, "tool"))
	}
	if s.Cost != "" {
		out += sty.Body.Render(" · " + s.Cost)
	}
	return out
}
