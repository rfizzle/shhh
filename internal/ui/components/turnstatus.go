package components

// The running turn's status line (
// docs/interface/surfaces.md#the-input-frame). While a turn runs, this
// is the one line on screen that changes: a spinner frame, the phase, and
// ticking elapsed. It lives in the frame's activity slot and it *resolves
// into* the turn summary rather than being replaced by one — same line,
// `✓` where the spinner was and the outcome's word where the phase was.
//
// Neither form states an account. The running line cannot: a turn in flight
// can only be priced at the full input rate, because the split between fresh and cached prompt reads arrives with the
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
// The resolved line states none either, though by then there is a bill: the
// close row the turn leaves a few rows above it states what the turn ran and
// what it was billed, so a count and a bill here were that row's fields a
// second time, minus its steps. What the line keeps is what only it says at
// the cursor — that the turn is over, and how it ended, in the place the
// reader was watching it run (docs/interface/surfaces.md#the-input-frame).
//
// Three rules are enforced here rather than left to the hosts. The phases are
// a closed vocabulary of five, so a state nobody defined has to pick the
// nearest rather than invent a sixth. The fields the line can shed as the
// terminal narrows are the running line's elapsed and then what a wait on the
// model has heard, and the phase never leaves, because what the turn is doing
// is the thing the line exists to say. And the
// spinner frame is passed in rather than kept, so this line, the running activity row and anything else that
// moves show the same frame from the one tick source.

import "charm.land/lipgloss/v2"

// TurnPhase is the turn status's closed vocabulary. There are five; anything
// else is a phase nobody defined.
type TurnPhase int

const (
	// PhaseThinking is the model reasoning before it acts: reasoning has
	// arrived on the stream. It is never drawn over a stream that has sent
	// nothing, which is PhaseWaiting.
	PhaseThinking TurnPhase = iota
	// PhaseDeciding is the auto-mode classifier judging a call (the vitals
	// rail's `✦ deciding`, seen from the frame).
	PhaseDeciding
	// PhaseActing is a tool executing. The word is `acting…`, for the reason
	// phaseWords gives.
	PhaseActing
	// PhaseStreaming is prose arriving.
	PhaseStreaming
	// PhaseWaiting is a request gone out with nothing drawable back from it
	// yet. The line says what it is waiting on and what has arrived, so a
	// model that is slow, a stream that is quiet and one that is silent read
	// as three things (docs/interface/surfaces.md#the-input-frame).
	PhaseWaiting
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
	PhaseActing:    "acting…",
	PhaseStreaming: "streaming…",
	PhaseWaiting:   "waiting…",
}

// word is the phase's word. A phase outside the vocabulary reads as thinking
// rather than as blank: the nearest of the five is the rule.
func (p TurnPhase) word() string {
	if w, ok := phaseWords[p]; ok {
		return w
	}
	return phaseWords[PhaseThinking]
}

// Field-drop levels (guidelines/turnstatus-drop-order). The phase and the
// outcome are not on the ladder. The guideline's earlier rungs are gone with
// the fields they shed: the line carries no tool argument and no tool count,
// so the running line's elapsed is the first field a narrowing slot reaches,
// what a wait on the model has heard the second, and the resolved line has
// nothing to shed at all. A wait's floor is the word, its own clock and its
// stretch word, or the retry where one is coming: those say whether the
// stream is alive, which is what the line is read for during a wait.
const (
	turnDropNone    = iota // every field the host supplied
	turnDropElapsed        // the running line's elapsed goes
	turnDropHeard          // what the wait has heard goes; the floor is the word and the wait
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

	// Wait is a wait on the model, stated after the word while the turn is
	// waiting or thinking: its own clock, labelled `model`, and what has
	// arrived since the request. Zero states nothing.
	Wait ModelWait

	// Done resolves the line into the summary it becomes: the outcome's glyph
	// where the spinner was and its word where the phase was, and nothing
	// after them. The turn's span, what it ran and what it was billed are the
	// close row's, which states them still when the turn has scrolled away
	// from a line that only ever reports the last one
	// (docs/interface/surfaces.md#the-input-frame).
	Done    bool
	Outcome TurnState
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
		return "⊘", doneWords[TurnCancelled], sty.dim
	case TurnFailed:
		return "✗", doneWords[TurnFailed], sty.del
	}
	return "✓", doneWords[TurnDone], sty.add
}

// ModelWait is a request's wait as the line states it. Every field is
// pre-formatted by the host, which owns the clock; the line owns the words
// that join them and the order they leave in.
type ModelWait struct {
	// Since is the time since the request went out, behind the word `model`:
	// the turn's clock beside it is labelled `turn`, so the two are told
	// apart by their words and not by their sizes.
	Since string
	// Stretch is the wait's floor: `silent`, `quiet`, or a count of the
	// reasoning that has arrived. It never drops.
	Stretch string
	// Heard is what arrived and since when — `nothing arrived`,
	// `keepalives only, last 3s ago`, `last 0.4s ago` — the second field a
	// narrowing slot gives up.
	Heard string
	// RetryIn is set in the last stretch before the stream's idle deadline,
	// and takes Heard's place in the floor: a retry coming is the one thing
	// about a silent wait the reader can act on.
	RetryIn string
}

// clause is the wait's fields as the line draws them at a drop level, each
// led by its separator.
func (w ModelWait) clause(phase TurnPhase, drop int) string {
	if w.Since == "" {
		return ""
	}
	out := " · model " + w.Since
	if w.Stretch == "" {
		return out
	}
	out += " · " + w.Stretch
	switch {
	case w.RetryIn != "":
		out += " — retry in " + w.RetryIn
	case w.Heard != "" && drop < turnDropHeard:
		// A count reads on into its clause; a stretch word is named and
		// then described.
		sep := " — "
		if phase == PhaseThinking {
			sep = ", "
		}
		out += sep + w.Heard
	}
	return out
}

// View renders the line at the widest fidelity that fits width, dropping in
// the turn status's order. A width that cannot hold even the floor clips it
// rather than rendering nothing: a line that says only what it is doing is
// still the answer to the question the line exists for.
func (s TurnStatus) View(width int) string {
	if width <= 0 {
		return ""
	}
	for drop := turnDropNone; ; drop++ {
		out := s.render(drop)
		if lipgloss.Width(out) <= width || drop >= turnDropHeard {
			return Clip(out, width)
		}
	}
}

// render lays the line out at one drop level, unclipped.
func (s TurnStatus) render(drop int) string {
	if s.Done {
		return s.renderDone()
	}
	label := s.Phase.word()
	// Elapsed, where the ladder left it standing, rides behind the label as
	// the animation's suffix: it is the host's own styling and the animation
	// never touches it, but it belongs to the same string so the line is
	// measured and clipped as one. The word in front of it is what makes it
	// the turn's clock rather than a second reading of the command's.
	// The wait comes first: it is what the turn is in, and the turn's clock
	// is the field that goes first.
	tail := s.Wait.clause(s.Phase, drop)
	if s.Elapsed != "" && drop < turnDropElapsed {
		tail += " · " + turnClock(s.Elapsed)
	}
	if tail != "" {
		tail = sty.dim.Render(tail)
	}
	// The line's moving part. The spinner's frame leads, outside the sweep
	// because its eight-frame cycle is not the label's; the label arrives
	// cell by cell when the turn starts and carries the light after that.
	return animLabel{
		frame:    s.Frame,
		arriving: s.Arriving,
		lead:     Spinner{Frame: s.Frame}.Glyph() + " ",
		label:    label,
		suffix:   tail,
	}.view()
}

// turnClock labels a span as the whole turn's. The feed under this line is
// full of clocks — every row that ran carries its own, and the command
// running right now is ticking one — so the frame's says whose it is rather
// than being the second bare figure on the screen
// (docs/interface/surfaces.md#the-input-frame).
func turnClock(span string) string { return "turn " + span }

// renderDone is the resolved line: the outcome, and only the outcome.
func (s TurnStatus) renderDone() string {
	glyph, word, style := s.doneGlyph()
	return style.Render(glyph + " " + word)
}
