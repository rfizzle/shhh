package components

// The rail's AGENTS block: the session map — the orchestrator and every
// child under it, with the detail row a live one draws. It is a file of its
// own because it is the only block whose rows are themselves sessions, so it
// carries a fold, a tally and a per-row layout none of the others need.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// InspectorAgent is one session in the AGENTS block — the orchestrator or one
// of its children. Steps is only set when the child declared a step count;
// without one the row shows its tool count rather than a fabricated ratio.
type InspectorAgent struct {
	Name   string
	Detail string
	Spend  string
	Tools  int
	// Step and Steps drive the five-cell lane meter. Steps == 0 means the
	// child declared no total, so the lane shows the spinner beside what it
	// is doing instead of a bar drawn against a denominator nobody supplied.
	Step, Steps int
	// State is the session's lifecycle state, in the same vocabulary a
	// fan-out lane and a manager row use, so one child cannot be drawn three
	// ways on one screen.
	State FanoutState
	// Outcome is the word a session that has stopped ends on. It is the
	// host's own word rather than one derived here: the block states how a
	// child ended, and the only authority on that is whatever ran it. Empty
	// for a session still moving, whose detail row already says what it is
	// doing.
	Outcome string
	// Focused marks the session the keyboard is in. It is the one thing the
	// rest of the rail cannot say for itself: every other block answers for
	// the session as a whole, so beside a child's transcript the mark is
	// what stops the numbers reading as the child's.
	Focused bool
	// Self marks the orchestrator's own row. It is not a child: it never
	// finishes, so it never folds, and it is left out of the tally the
	// heading states about what the children still owe you.
	Self bool
}

// agentsBlock is the session map: the orchestrator, then every child in spawn
// order, running or finished, with the row the keyboard is in marked. Every
// other block on the rail answers for the session as a whole, which is what
// makes the mark load-bearing rather than decorative — beside a child's
// transcript it is the only thing saying which session those numbers are
// about (docs/interface/surfaces.md#the-inspector-rail).
//
// A map of one session is not a map, so the host sends nothing where there
// are no children and the block is omitted like any other with nothing to
// say.
func (r InspectorRail) agentsBlock(width int) (railBlock, bool) {
	if len(r.Agents) == 0 {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("AGENTS", r.childTally(), sty.Dim, width)}
	shown, folded := r.mappedAgents()
	for _, a := range shown {
		b.rows = append(b.rows, a.railLines(r.Frame, width)...)
	}
	for _, a := range folded {
		b.hidden = append(b.hidden, a.railLines(r.Frame, width)...)
	}
	b.fold = func(hidden []railLine) string { return agentsFold(hidden, width) }
	return b, true
}

// inspectorAgentsSettled is how many finished children the map keeps on
// screen before the rest fold behind their count. The number is what the
// arithmetic allows rather than a taste: a finished child costs two rows —
// its own and the line saying what it found — and the marker costs one, so
// folding only starts saving room at the third. Below that the fold would
// spend a row to hide a row, and above it the block would grow into a log of
// everything the session ever started, which is the agent manager's job and
// not a standing overview's.
const inspectorAgentsSettled = 2

// mappedAgents splits the map into the rows it draws and the rows it folds.
// What folds is the surplus of finished children, earliest first: an outcome
// you have not read yet is the one that just landed, so the budget is spent
// from the newest backwards. A failure folds like anything else, because a
// child that wants something from you is blocked rather than failed, and a
// blocked child is never what folds. The orchestrator never folds because it
// never finishes, and the focused session never folds because the mark on it
// is the reason the rest of the rail can be read at all.
func (r InspectorRail) mappedAgents() (shown, folded []InspectorAgent) {
	drop := make(map[int]bool)
	budget := inspectorAgentsSettled
	for i := len(r.Agents) - 1; i >= 0; i-- {
		a := r.Agents[i]
		if a.Self || a.Focused || !a.State.settled() {
			continue
		}
		if budget > 0 {
			budget--
			continue
		}
		drop[i] = true
	}
	for i, a := range r.Agents {
		if drop[i] {
			folded = append(folded, a)
		} else {
			shown = append(shown, a)
		}
	}
	return shown, folded
}

// childTally is the heading's own sentence: what the children still owe you,
// in the words the fan-out header and the manager's title rail state about
// the same children. The orchestrator is not a child and is left out of it,
// or a session with nothing running would head its map with "1 running".
func (r InspectorRail) childTally() string {
	var states []FanoutState
	for _, a := range r.Agents {
		if !a.Self {
			states = append(states, a.State)
		}
	}
	return stateTally(states)
}

// agentsFold is the marker the map folds behind: a count of sessions, which
// is what the counted rows are.
func agentsFold(hidden []railLine, width int) string {
	n := 0
	for _, h := range hidden {
		if h.counted {
			n++
		}
	}
	if n == 0 {
		// Height truncation can take a session's detail row on its own,
		// leaving hidden rows that belong to sessions still on screen. They
		// are not sessions the marker is hiding, and they are not nothing
		// either, so the marker falls back to counting what it has.
		n = len(hidden)
	}
	return indentRow(sty.Hint.Render(fmt.Sprintf("… %d more", n)), width)
}

// railLines is one session's rows: who it is, how it is and what it has
// spent, and under that what it is doing or what it found.
func (a InspectorAgent) railLines(frame, width int) []railLine {
	// The mark sits in the indent every other row spends on nothing, so a
	// marked row starts in the same column as an unmarked one. It is a mark
	// and not a cursor: the manager's ❯ says where the selection is, and this
	// says where the keyboard is, which are different questions.
	lead := strings.Repeat(" ", inspectorIndent)
	if a.Focused {
		lead = sty.FocusRow.Render("▸") + " "
	}
	// Both of a session's rows point at that session. They are one thing
	// drawn on two lines — the name and what it is doing — and a pointer that
	// answered on the first and not the second would make the target half a
	// row tall for no reason the reader can see.
	target := RailTarget{Kind: RailTargetSession, Name: a.Name}
	if a.Self {
		// The rail's own session has no name to attach to: it is where the
		// keyboard goes back to, which every host spells as no name at all.
		target.Name = ""
	}
	rows := []railLine{{
		text: railRow(lead+AgentProgress{State: a.State}.glyph()+" "+sty.Body.Render(a.Name),
			a.rightField(), width, 0),
		// The orchestrator, the session the keyboard is in and a child
		// waiting on an answer are the rows the map exists to keep on
		// screen; truncation takes them only when nothing else is left.
		pinned: a.Self || a.Focused || a.State == FanoutBlocked,
		// The fold counts sessions, and this is the row that is one.
		counted: true,
		target:  target,
	}}
	if detail := a.detailRow(frame, width); detail != "" {
		rows = append(rows, railLine{text: detail, target: target})
	}
	return rows
}

// rightField is what a session's row reports: the word it ended on where it
// has ended, and the spend, which it has whatever it is doing.
func (a InspectorAgent) rightField() string {
	spend := ""
	if a.Spend != "" {
		spend = sty.Dim.Render(a.Spend)
	}
	if a.Outcome == "" {
		return spend
	}
	word := outcomeStyle(a.State).Render(a.Outcome)
	if spend == "" {
		return word
	}
	return word + "  " + spend
}

// outcomeStyle is the weight the word a session ended on carries: a failure
// is the only one of them that asks anything of the reader.
func outcomeStyle(s FanoutState) lipgloss.Style {
	switch s {
	case FanoutFailed:
		return sty.Err
	case FanoutDone:
		return sty.Add
	}
	return sty.Dim
}

// detailRow is the line under a session. A session that has stopped moving
// gets neither a bar nor a spinner: a bar against a finished child measures
// nothing, and motion beside one is motion where there is none.
func (a InspectorAgent) detailRow(frame, width int) string {
	var parts []string
	switch m, ok := AgentMeter(a.Step, a.Steps); {
	case a.State == FanoutDone || a.State == FanoutFailed:
		if a.Detail != "" {
			parts = append(parts, sty.Dimmer.Render(a.Detail))
		}
	case ok:
		// A declared step count earns a bar; the lane is info whatever
		// the child's health, and states its count beside it.
		parts = append(parts, m.View())
		if a.Detail != "" {
			parts = append(parts, sty.Dimmer.Render(a.Detail))
		}
	case a.Detail == "":
	case a.State != FanoutRunning:
		// Waiting on an answer, waiting for a slot, or waiting to be
		// steered: none of them is running, so none of them gets motion.
		parts = append(parts, sty.Dimmer.Render(a.Detail))
	default:
		// No declared total: motion beside the word naming what is
		// running, never a fabricated ratio.
		parts = append(parts, Spinner{Frame: frame, Label: a.Detail}.View())
	}
	if a.Tools > 0 {
		parts = append(parts, sty.Dimmer.Render(plural(a.Tools, "tool")))
	}
	if len(parts) == 0 {
		return ""
	}
	return railRow(strings.Join(parts, sty.Dimmer.Render(" · ")), "", width, inspectorIndent+2)
}
