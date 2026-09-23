package components

// The rail's AGENTS block: the session map — the orchestrator and every
// child under it, with the detail row a live one draws. It is a file of its
// own because it is the only block whose rows are themselves sessions, so it
// carries a fold, a tally and a per-row layout none of the others need.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rfizzle/shhh/internal/ui/keys"
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
	// Outcome is the word a session that has stopped ends on, and the word a
	// session parked mid-run has stopped at. It is the host's own word rather
	// than one derived here: the block states how a child ended, and the only
	// authority on that is whatever ran it. Empty for a session still moving,
	// whose detail row already says what it is doing.
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
	// Fresh and Budget are what the child has taken in and what it was given
	// to take in. They are the one denominator nobody has to declare, so a
	// child that named no step count still has a bar to draw once it is close
	// enough to the ceiling for the bar to be news
	// (docs/interface/surfaces.md#the-inspector-rail). Zero Budget is a child
	// nothing bounded, which is the spinner's case either way.
	Fresh, Budget int64
	// Steers is how many times this turn the child has been told it looks to
	// have left its task. The row states it from two, never from one: one
	// steer is the machinery working as designed, and a row that shouted
	// about it would be a warning on every healthy fan-out
	// (docs/capabilities/subagents.md#they-are-visible-while-they-run).
	Steers int
	// Handoff marks a stopped child that left a record a replacement could
	// resume from. It is a flag rather than the record's own handle because
	// the row does nothing with it: the rail says the record was kept and
	// names the key, and the key is the manager's.
	Handoff bool
	// Depth is how far under the orchestrator the session sits — 0 for the
	// orchestrator, 1 for a child it spawned, 2 for that child's own child.
	// A depth past 1 draws the row one column in behind a corner, so a run
	// two levels deep reads as two levels rather than as five siblings.
	Depth int
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
	rows := make([][]railLine, len(shown))
	for i, a := range shown {
		rows[i] = a.railLines(r.Frame, width)
	}
	orderGiving(shown, rows)
	for _, group := range rows {
		b.rows = append(b.rows, group...)
	}
	for _, a := range folded {
		b.hidden = append(b.hidden, a.railLines(r.Frame, width)...)
	}
	if hint := r.AgentsHint; hint != "" && r.hasChild() {
		// The trailer goes before anything else the block owns, because a map
		// missing one session is still a map and a hint standing over no map
		// is not a hint (docs/interface/surfaces.md#the-inspector-rail). It
		// goes rather than folds: it is not a session, so the marker has
		// nothing to count for it and would cost the row it saved.
		b.rows = append(b.rows, railLine{
			text: indentRow(sty.Hint.Render(hint), width), give: giveFirst, shed: true,
		})
		if r.AgentsOption {
			// Under the trailer and shed before it: the rail takes the last
			// of two rows that cost the same, so the row about the terminal
			// goes before the row about the product.
			b.rows = append(b.rows, railLine{
				text: indentRow(optionLine(), width), give: giveFirst, shed: true,
			})
		}
	}
	b.fold = func(hidden []railLine) string { return agentsFold(hidden, width) }
	return b, true
}

// giveFirst is the give of a row the block would rather lose than any other.
// It is below every give orderGiving hands out, so the trailer goes before
// the first finished child does.
const giveFirst = -1

// orderGiving numbers the map's rows in the order height truncation may take
// them: every session that has stopped, oldest first, then every session that
// has not started or has been parked, and — because both of a session's rows
// are one session — the line under a row immediately before the row itself.
// A session still working is pinned, so its number is never reached: a
// pinned row is taken only once nothing else on the whole rail has one left
// to give (docs/interface/surfaces.md#the-inspector-rail).
//
// The two passes are the order rather than a sort key on the state, because
// what a rail short of height can afford to lose is not a property of the
// state: a finished child's outcome has been read once already and is still
// in the transcript, and a queued child has nothing to report yet.
func orderGiving(agents []InspectorAgent, rows [][]railLine) {
	give := 0
	for _, settled := range []bool{true, false} {
		for i, a := range agents {
			if a.State.settled() != settled {
				continue
			}
			for j := len(rows[i]) - 1; j >= 0; j-- {
				rows[i][j].give = give
				give++
			}
		}
	}
}

// hasChild reports whether the map has a session under the orchestrator. The
// orchestrator alone is not a map, and the trailer naming how to reach the
// others would be naming nothing.
func (r InspectorRail) hasChild() bool {
	for _, a := range r.Agents {
		if !a.Self {
			return true
		}
	}
	return false
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
	ordered := r.mapOrder()
	drop := make(map[int]bool)
	budget := inspectorAgentsSettled
	for i := len(ordered) - 1; i >= 0; i-- {
		a := ordered[i]
		if a.Self || a.Focused || !a.State.settled() {
			continue
		}
		if budget > 0 {
			budget--
			continue
		}
		drop[i] = true
	}
	// A session keeps its row while anything under it is still drawn: the
	// nested row hangs off it behind a corner, and with the parent folded
	// away that corner would hang off whichever stranger was drawn above.
	for i := range ordered {
		if !drop[i] {
			continue
		}
		for j := i + 1; j < len(ordered) && ordered[j].Depth > ordered[i].Depth; j++ {
			if !drop[j] {
				delete(drop, i)
				break
			}
		}
	}
	for i, a := range ordered {
		if drop[i] {
			folded = append(folded, a)
		} else {
			shown = append(shown, a)
		}
	}
	return shown, folded
}

// mapOrder is the order the map draws its sessions in: the orchestrator, then
// every child waiting on an answer, then everything else — each group in the
// tree order the host handed over. A child that wants something from you is
// the only row on this block that is a job rather than a reading, and a run
// of six leaves those rows wherever the fan-out happened to reach them.
//
// What floats is the subtree and not the row, the way the manager and a
// fan-out lane float theirs: a nested row hangs off the row above it, so a
// row lifted out on its own — or one floating up between a parent and the
// session it started — would leave a corner under a stranger. A subtree
// floats when anything in it is waiting, because a parent whose delegate is
// waiting on you is a parent waiting on you
// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
//
// The chord does not float with them: it walks spawn order whole, which is
// where the map and the chord may differ
// (docs/interface/surfaces.md#the-inspector-rail). A key whose destination
// moved every time a child blocked or was answered would be a key nobody
// could aim, and the map is read rather than aimed.
func (r InspectorRail) mapOrder() []InspectorAgent {
	ordered := make([]InspectorAgent, 0, len(r.Agents))
	var children []InspectorAgent
	for _, a := range r.Agents {
		if a.Self {
			ordered = append(ordered, a)
		} else {
			children = append(children, a)
		}
	}
	depths := make([]int, len(children))
	for i, a := range children {
		depths[i] = a.Depth
	}
	var rest []InspectorAgent
	for _, g := range depthGroups(depths) {
		waiting := false
		for _, i := range g {
			waiting = waiting || children[i].State == FanoutBlocked
		}
		for _, i := range g {
			if waiting {
				ordered = append(ordered, children[i])
			} else {
				rest = append(rest, children[i])
			}
		}
	}
	return append(ordered, rest...)
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
	// marked row starts in the same column as an unmarked one. The mark is
	// ❯ and the row is lit under it, the way the keyboard's place is said on
	// every list: ▸ here read as running, which is what the glyph means three
	// rows above on a session that is actually working.
	lead := PointerColumn()
	if a.Focused {
		lead = sty.FocusPointer.Render("❯") + " "
	}
	lead += a.nesting()
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
	row := railRow(lead+AgentProgress{State: a.State}.rowGlyph()+" "+sty.Body.Render(a.Name),
		a.rightField(), width, 0)
	if a.Focused {
		row = LitRow(row, GridPointerWidth, width)
	}
	// The orchestrator, the session the keyboard is in, a child waiting on an
	// answer and a child still working are the rows the map exists to keep on
	// screen; truncation takes them only when nothing else is left. Both of
	// a session's rows are pinned together, because half a pinned session is
	// a name with no line saying what it is doing — which is the whole of
	// what the pin was keeping.
	pinned := a.Self || a.Focused || a.State == FanoutBlocked || a.State == FanoutRunning
	rows := []railLine{{
		text:   row,
		pinned: pinned,
		// The fold counts sessions, and this is the row that is one.
		counted: true,
		target:  target,
	}}
	if detail := a.detailRow(frame, width); detail != "" {
		rows = append(rows, railLine{text: detail, pinned: pinned, target: target})
	}
	return rows
}

// nesting is the column a session started by another session is drawn in
// behind, and nothing at all for a session the orchestrator started itself.
// It is the shared one (agentNesting), because the map, the manager and a
// fan-out lane draw the same child and must indent it by the same rule.
func (a InspectorAgent) nesting() string { return agentNesting(a.Depth) }

// detailIndent is where a session's second line starts: under its own name,
// so a nested session's line moves in with the row it belongs to rather than
// lining up with a sibling of its parent.
func (a InspectorAgent) detailIndent() int {
	return inspectorIndent + 2 + max(a.Depth-1, 0)
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
//
// The meter leads this line rather than sitting on the name row: its head is
// the one slot saying how a child is moving, shared with the budget's bar and
// the spinner, and the name row is what has to survive the rail's narrowest
// width (docs/interface/departures.md#the-agents-blocks-meter-is-on-the-detail-row).
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
	case a.State == FanoutRunning && a.pastHalfItsBudget():
		// Nobody declared a step count, but somebody set a ceiling, and the
		// child is close enough to it for the distance to be news. The bar
		// is the budget's rather than a step count's, and it takes the
		// spinner's place: a child near its ceiling is doing one thing worth
		// watching, and it is not the fact that it is still moving.
		parts = append(parts, a.budgetMeter().View())
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
	if a.Steers >= inspectorSteersWorthSaying {
		// The count and not a flag: one steer is the machinery working, and a
		// second is an interruption delivered, answered, and the next reading
		// finding the same departure — which is the thing worth knowing forty
		// rounds before the report says it
		// (docs/capabilities/subagents.md#they-are-visible-while-they-run).
		parts = append(parts, sty.Del.Render(fmt.Sprintf("⚠ off task ×%d", a.Steers)))
	}
	if a.Tools > 0 {
		parts = append(parts, sty.Dimmer.Render(plural(a.Tools, "tool")))
	}
	if a.Handoff {
		// The row ends on what can still be done about it, and does nothing
		// about it: the key is the manager's, and the trailer under the block
		// is how a reader gets there
		// (docs/interface/surfaces.md#the-inspector-rail).
		parts = append(parts, sty.Dimmer.Render("handoff kept"),
			sty.Hint.Render(keys.Bracket(keys.Agent.Retry)+" "+keys.Words(keys.Agent.Retry)))
	}
	if len(parts) == 0 {
		return ""
	}
	return railRow(strings.Join(parts, sty.Dimmer.Render(" · ")), "", width, a.detailIndent())
}

// inspectorSteersWorthSaying is the steer count the map draws a warning at.
// One is the machinery working as designed — a child told once and back on
// task is the case it was built for — so a row that said so would carry a
// warning on every healthy fan-out
// (docs/capabilities/subagents.md#they-are-visible-while-they-run).
const inspectorSteersWorthSaying = 2

// pastHalfItsBudget reports whether the child has taken in half or more of
// the fresh tokens it was given. Half is where the distance to the ceiling
// stops being arithmetic and starts being a decision: under it there is
// nothing to do about the number, and over it there is.
func (a InspectorAgent) pastHalfItsBudget() bool {
	return a.Budget > 0 && a.Fresh*2 >= a.Budget
}

// budgetMeter is the lane drawn against the budget instead of against a step
// count: the same five cells and the same info tone every lane meter takes,
// with the intake and the ceiling stated beside it, because the bar is never
// the only carrier of the value.
func (a InspectorAgent) budgetMeter() Meter {
	return Meter{
		Pct:   int(min(a.Fresh*100/a.Budget, 100)),
		Cells: MeterCellsAgent,
		Tone:  MeterAgent,
		Text:  formatTokens(a.Fresh) + " of " + formatTokens(a.Budget),
	}
}
