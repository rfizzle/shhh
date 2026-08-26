package components

// Inspector rail (S-092, DESIGN-TUI.md §15). Past 130 content columns the
// transcript stops being the whole screen: a 46-column rail on the right
// answers the three standing questions — what is it doing, what has it
// changed, what is it costing — so the session stops being interrogated with
// /stats and /diff for what it already knows.
//
// The rail is passive, like Cockpit: the host feeds it this turn's numbers and
// renders View every frame. It owns no keys, no state and no goroutines, and
// the block order is fixed — THIS TURN, PLAN, CHANGES, AGENTS, CONTEXT,
// SPEND. A block with nothing to say is omitted rather than rendered empty
// (§15b), and a rail that does not fit its height truncates its longest block
// first and says how many rows it swallowed.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	// InspectorWidth is the rail's column count — the only supported value at
	// ≥ InspectorMinContentWidth (§8c, §15).
	InspectorWidth = 46
	// InspectorMinContentWidth is the top rung of the width ladder (§8c): at
	// or above it the surface splits into transcript pane + rail, below it the
	// rail is dropped entirely.
	InspectorMinContentWidth = 130

	// inspectorIndent is the two columns every block heading and row starts
	// at; a changed-file row spends the third on the mutation rail (§14).
	inspectorIndent = 2
	// Meter and sparkline cell counts (§15a) — the shared roles from §10c,
	// so the rail's runs are the same runs every other surface draws.
	inspectorTurnCells = MeterCellsRail
	inspectorCtxCells  = MeterCellsRail
	inspectorSparkCell = SparkCells
)

// InspectorTurn is the THIS TURN block: how far through its steps the turn is,
// how many tools it has spent, and how long it has been running.
type InspectorTurn struct {
	// Step and Steps drive the progress meter and the "step 3 of 4" heading.
	// Steps == 0 means the turn declared none, so no ratio is fabricated —
	// the block states its tool count and elapsed time alone (S-094).
	Step, Steps int
	Tools       int
	Elapsed     time.Duration
	// Running says the turn is still in flight, so the elapsed time is a
	// running total rather than a final one.
	Running bool
}

// PlanStepState is one checklist step's state in the PLAN block. It is the
// same four states the step outline draws (§13b), because an approved plan's
// step and the transcript's step are the same step (S-104).
type PlanStepState int

const (
	// PlanStepQueued is declared and not started; its duration is —.
	PlanStepQueued PlanStepState = iota
	// PlanStepRunning is the step in flight, the one the reader is looking
	// for, so it is the one the block emphasises.
	PlanStepRunning
	// PlanStepDone finished with nothing to report.
	PlanStepDone
	// PlanStepFailed finished and contained a failure.
	PlanStepFailed
)

// InspectorPlanStep is one step of the approved plan in the PLAN block.
type InspectorPlanStep struct {
	Number int
	Title  string
	State  PlanStepState
	// Elapsed is how long the step took; blank for one that has not finished,
	// so the column carries a number only where there is one.
	Elapsed string
}

// InspectorPlan is the PLAN block: an approved plan as a live checklist
// (S-104). An approved plan is not a message that scrolls away — it is the
// answer to "where are we", and the rail is where that answer belongs.
type InspectorPlan struct {
	Steps []InspectorPlanStep
	// Done counts the steps that finished, which is what the heading states.
	// A failed step finished.
	Done int
	// Drift is the one-line note when the run has departed from the plan —
	// work the plan never named, steps taken out of order, steps skipped.
	// Empty while the run is following it, because "no drift" is not news.
	Drift string
	// Hint is the row under the list naming how to read the whole plan.
	Hint string
}

// InspectorFile is one changed file in the CHANGES block.
type InspectorFile struct {
	Path           string
	Added, Removed int
}

// InspectorChanges is the CHANGES block: what this turn wrote, and whether
// anything it ran came back broken.
type InspectorChanges struct {
	Files          []InspectorFile
	Added, Removed int
	// Failure is the command that failed this turn (empty when none) and
	// FailureNote its outcome, e.g. "exit 1" — the turn's failing-test state
	// as far as the session can currently see it.
	Failure     string
	FailureNote string
}

// InspectorAgent is one running child in the AGENTS block. Steps is only set
// when the child declared a step count; without one the row shows its tool
// count rather than a fabricated ratio (S-094).
type InspectorAgent struct {
	Name   string
	Detail string
	Spend  string
	Tools  int
	// Step and Steps drive the five-cell lane meter. Steps == 0 means the
	// child declared no total, so the lane shows the spinner beside what it
	// is doing instead of a bar drawn against a denominator nobody supplied.
	Step, Steps int
	Blocked     bool
}

// InspectorContext is the CONTEXT block: occupancy of the model's window,
// the tokens behind it, and the per-round burn.
type InspectorContext struct {
	Pct              int
	Tokens, Window   int64
	Tokens1, Tokens2 string // the ↑in and ↓out labels
	// Burn is the per-round context series behind the sparkline, fed from the
	// session's vitals history (S-093). One sample is a dot, not a trend, so
	// the host sends nothing until it has two and the row says "estimated"
	// instead of drawing a flat line.
	Burn []float64
	// WarnPct/AlertPct override the meter's threshold colors (0 keeps the
	// defaults), so the rail matches the host's own trim warnings.
	WarnPct, AlertPct int
	// Estimated says the occupancy is the host's own estimate rather than a
	// provider-reported size, and the block says so in words (S-093) — a
	// number nobody vouched for should not look like one that was.
	Estimated bool
}

// InspectorSpend is the SPEND block: this turn's cost, how it split between
// the orchestrator and its children, and the session total.
type InspectorSpend struct {
	Turn     string
	Main     string
	Children string
	Session  string
	Model    string
}

// InspectorRail is the whole rail. A nil block pointer (or an empty agent
// list) is a block with nothing to say, and is omitted.
type InspectorRail struct {
	Turn    *InspectorTurn
	Plan    *InspectorPlan
	Changes *InspectorChanges
	Agents  []InspectorAgent
	Context *InspectorContext
	Spend   *InspectorSpend
	// Frame is the host's spinner frame index, for the lanes of children that
	// declared no step count. The rail stays passive: it animates nothing, it
	// just draws the frame it is handed.
	Frame int
}

// Empty reports whether every block is omitted, so the host can skip the
// split rather than draw an empty column.
func (r InspectorRail) Empty() bool {
	return r.Turn == nil && r.Plan == nil && r.Changes == nil && len(r.Agents) == 0 &&
		r.Context == nil && r.Spend == nil
}

// railBlock is one headed block under assembly: its heading line, its rows,
// and how many rows truncation has taken off the end.
type railBlock struct {
	heading string
	rows    []string
	hidden  int
}

func (b railBlock) height() int {
	h := 1 + len(b.rows)
	if b.hidden > 0 {
		h++
	}
	return h
}

func (b railBlock) lines() []string {
	out := append([]string{b.heading}, b.rows...)
	if b.hidden > 0 {
		out = append(out, indentRow(hintStyle.Render(fmt.Sprintf("… %d more", b.hidden)), InspectorWidth))
	}
	return out
}

// View renders the rail at width columns and, when height > 0, within height
// rows. Blocks are separated by one blank row; the rail never scrolls.
func (r InspectorRail) View(width, height int) string {
	return strings.Join(r.Lines(width, height), "\n")
}

// Lines is View split into rows, for hosts joining the rail beside a
// transcript pane line by line.
func (r InspectorRail) Lines(width, height int) []string {
	blocks := r.blocks(width)
	if len(blocks) == 0 {
		return nil
	}
	if height > 0 {
		blocks = fitBlocks(blocks, height)
	}
	var out []string
	for i, b := range blocks {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, b.lines()...)
	}
	if height > 0 && len(out) > height {
		out = out[:height]
	}
	return out
}

// blocks assembles the present blocks in their fixed order.
func (r InspectorRail) blocks(width int) []railBlock {
	var blocks []railBlock
	for _, b := range []func(int) (railBlock, bool){
		r.turnBlock, r.planBlock, r.changesBlock, r.agentsBlock, r.contextBlock, r.spendBlock,
	} {
		if blk, ok := b(width); ok {
			blocks = append(blocks, blk)
		}
	}
	return blocks
}

// fitBlocks truncates the rail into height rows, taking rows off the longest
// block first (§15b). A truncated block keeps its heading and says how many
// rows it is hiding, so the rail never ends silently.
func fitBlocks(blocks []railBlock, height int) []railBlock {
	for total(blocks) > height {
		longest, rows := -1, 0
		for i, b := range blocks {
			if len(b.rows) > rows {
				longest, rows = i, len(b.rows)
			}
		}
		if longest < 0 {
			// Every block is down to its heading: nothing left to give.
			break
		}
		b := &blocks[longest]
		b.rows = b.rows[:len(b.rows)-1]
		b.hidden++
	}
	return blocks
}

func total(blocks []railBlock) int {
	n := len(blocks) - 1 // one blank row between blocks
	for _, b := range blocks {
		n += b.height()
	}
	return n
}

func (r InspectorRail) turnBlock(width int) (railBlock, bool) {
	t := r.Turn
	if t == nil {
		return railBlock{}, false
	}
	meta := ""
	if t.Steps <= 0 && t.Step > 0 {
		// Steps observed, none declared: the ordinal is true, the ratio would
		// not be, so no denominator and no meter (S-094).
		meta = fmt.Sprintf("step %d", t.Step)
	}
	b := railBlock{heading: railHeading("THIS TURN", meta, dimStyle, width)}
	if m, ok := StepMeter(t.Step, t.Steps, inspectorTurnCells, t.Running); ok {
		// The count sits beside the bar rather than in the heading, because a
		// bar is never the only carrier of its value (§10c).
		b.rows = append(b.rows, indentRow(m.View(), width))
	}
	elapsed := "elapsed"
	if !t.Running {
		elapsed = "total"
	}
	b.rows = append(b.rows, indentRow(dimStyle.Render(fmt.Sprintf("%s · %s %s",
		plural(t.Tools, "tool"), FormatElapsed(t.Elapsed), elapsed)), width))
	return b, true
}

// planBlock is the PLAN checklist (S-104). It sits under THIS TURN because it
// is that block's detail: THIS TURN says how far through, PLAN says through
// what. The keys it prints are the host's, like [v] and [u] on CHANGES.
func (r InspectorRail) planBlock(width int) (railBlock, bool) {
	p := r.Plan
	if p == nil || len(p.Steps) == 0 {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("PLAN",
		fmt.Sprintf("%d of %d done", p.Done, len(p.Steps)), dimStyle, width)}
	for _, s := range p.Steps {
		glyph, style := planStepTone(s.State)
		// A step with no duration yet gets no right-hand field at all, so the
		// title has the whole row rather than a column reserved for nothing.
		elapsed := ""
		if s.Elapsed != "" {
			elapsed = dimStyle.Render(s.Elapsed)
		}
		b.rows = append(b.rows, railRow(glyph+" "+style.Render(s.Title), elapsed, width, inspectorIndent))
	}
	if p.Drift != "" {
		b.rows = append(b.rows, railRow(accentStyle.Render("⚠")+" "+dimStyle.Render(p.Drift),
			"", width, inspectorIndent))
	}
	if p.Hint != "" {
		b.rows = append(b.rows, indentRow(hintStyle.Render(p.Hint), width))
	}
	return b, true
}

// planStepTone is a checklist step's glyph and the weight its title carries.
// The running step is the one being looked for, so it is the bright one; a
// finished step recedes to chrome, having nothing left to ask of anyone.
func planStepTone(s PlanStepState) (string, lipgloss.Style) {
	switch s {
	case PlanStepRunning:
		return spinTextStyle.Render("▸"), brightStyle()
	case PlanStepDone:
		return addStyle.Render("✓"), dimStyle
	case PlanStepFailed:
		return errStyle.Render("✗"), bodyStyle
	}
	return dimStyle.Render("·"), dimStyle
}

func (r InspectorRail) changesBlock(width int) (railBlock, bool) {
	c := r.Changes
	if c == nil || (len(c.Files) == 0 && c.Failure == "") {
		return railBlock{}, false
	}
	meta := ""
	if len(c.Files) > 0 {
		meta = addStyle.Render(fmt.Sprintf("+%d", c.Added)) + " " + delStyle.Render(fmt.Sprintf("−%d", c.Removed))
	}
	b := railBlock{heading: railHeading("CHANGES", meta, dimStyle, width)}
	for _, f := range c.Files {
		// The changed-file row carries the mutation rail and the edit glyph,
		// so the close of a turn looks like the rows that produced it (§14).
		lead := accentStyle.Render("▎") + accentStyle.Render("✎") + " "
		stats := addStyle.Render(fmt.Sprintf("+%d", f.Added)) + " " + delStyle.Render(fmt.Sprintf("−%d", f.Removed))
		b.rows = append(b.rows, railRow(lead+bodyStyle.Render(f.Path), stats, width, inspectorIndent))
	}
	if c.Failure != "" {
		lead := " " + errStyle.Render("✗") + " "
		b.rows = append(b.rows, railRow(lead+bodyStyle.Render(c.Failure), errStyle.Render(c.FailureNote), width, inspectorIndent))
	}
	return b, true
}

func (r InspectorRail) agentsBlock(width int) (railBlock, bool) {
	if len(r.Agents) == 0 {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("AGENTS", fmt.Sprintf("%d running", len(r.Agents)), dimStyle, width)}
	for _, a := range r.Agents {
		glyph := infoStyle.Render("◇")
		if a.Blocked {
			glyph = errStyle.Render("⚠")
		}
		b.rows = append(b.rows, railRow(glyph+" "+bodyStyle.Render(a.Name), dimStyle.Render(a.Spend), width, inspectorIndent))
		var parts []string
		switch m, ok := AgentMeter(a.Step, a.Steps); {
		case ok:
			// A declared step count earns a bar; the lane is info whatever
			// the child's health, and states its count beside it (§10c).
			parts = append(parts, m.View())
			if a.Detail != "" {
				parts = append(parts, dimmerStyle.Render(a.Detail))
			}
		case a.Detail == "":
		case a.Blocked:
			// A blocked lane is not running, so it gets no motion either.
			parts = append(parts, dimmerStyle.Render(a.Detail))
		default:
			// No declared total: motion beside the word naming what is
			// running, never a fabricated ratio (S-094).
			parts = append(parts, Spinner{Frame: r.Frame, Label: a.Detail}.View())
		}
		if a.Tools > 0 {
			parts = append(parts, dimmerStyle.Render(plural(a.Tools, "tool")))
		}
		if len(parts) > 0 {
			b.rows = append(b.rows, railRow(strings.Join(parts, dimmerStyle.Render(" · ")), "", width, inspectorIndent+2))
		}
	}
	return b, true
}

func (r InspectorRail) contextBlock(width int) (railBlock, bool) {
	c := r.Context
	if c == nil || c.Window <= 0 {
		return railBlock{}, false
	}
	pct := min(max(c.Pct, 0), 100)
	meter := Meter{Pct: pct, Cells: inspectorCtxCells, Tone: MeterPressure,
		Warn: c.WarnPct, Alert: c.AlertPct}
	style := meter.Style()
	b := railBlock{heading: railHeading("CONTEXT",
		style.Render(fmt.Sprintf("%d%% of %s", pct, formatTokens(c.Window))), style, width)}
	count := formatTokens(c.Tokens)
	if c.Estimated {
		count = "~" + count
	}
	// The bar's number is the token count at the rail's right edge, in the
	// meter's own colour — the bar never carries the value alone (§10c).
	b.rows = append(b.rows, railRow(meter.Bar(), style.Render(count), width, inspectorIndent))
	tokens := strings.TrimSpace(c.Tokens1 + " " + c.Tokens2)
	lead := ""
	switch {
	case len(c.Burn) > 0:
		lead = Sparkline{Values: c.Burn, Cells: inspectorSparkCell}.View() + " " + dimStyle.Render("per round")
	case c.Estimated:
		// No series yet and no reported size: the block still has to say
		// where its number came from.
		lead = dimStyle.Render("estimated")
	}
	if lead != "" || tokens != "" {
		b.rows = append(b.rows, railRow(lead, dimStyle.Render(tokens), width, inspectorIndent))
	}
	return b, true
}

func (r InspectorRail) spendBlock(width int) (railBlock, bool) {
	s := r.Spend
	if s == nil || (s.Turn == "" && s.Main == "" && s.Session == "") {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("SPEND", bodyStyle.Render(s.Turn), bodyStyle, width)}
	var split []string
	if s.Model != "" {
		split = append(split, s.Model)
	}
	if s.Main != "" {
		split = append(split, s.Main+" main")
	}
	if s.Children != "" {
		split = append(split, s.Children+" ◇")
	}
	if len(split) > 0 {
		b.rows = append(b.rows, railRow(dimStyle.Render(strings.Join(split, " · ")), "", width, inspectorIndent))
	}
	if s.Session != "" {
		b.rows = append(b.rows, railRow(dimStyle.Render("session total "+s.Session), "", width, inspectorIndent))
	}
	return b, true
}

// railHeading is a block heading: the label in info, its count or value
// right-aligned at the rail's edge (§15a).
func railHeading(label, meta string, metaStyle lipgloss.Style, width int) string {
	if meta != "" && !strings.Contains(meta, "\x1b") {
		meta = metaStyle.Render(meta)
	}
	return railRow(headlineStyle.Render(label), meta, width, inspectorIndent)
}

// railRow lays one row out: indent, left field, right field against the
// rail's right edge. The left field clips when the two would collide — the
// right field is the number, and a clipped number is a wrong number.
func railRow(left, right string, width, indent int) string {
	room := width - indent - lipgloss.Width(right)
	if right != "" {
		room-- // at least one space between the fields
	}
	left = clip(left, max(room, 0))
	gap := width - indent - lipgloss.Width(left) - lipgloss.Width(right)
	return strings.Repeat(" ", indent) + left + strings.Repeat(" ", max(gap, 0)) + right
}

// indentRow is railRow with nothing on the right.
func indentRow(s string, width int) string {
	return railRow(s, "", width, inspectorIndent)
}

// FormatElapsed is the shared wall-clock format: seconds under a minute,
// "1m 04s" above it. One implementation, so the rail and /stats cannot
// report the same duration two ways.
func FormatElapsed(d time.Duration) string {
	if d < time.Minute {
		if d < 10*time.Second {
			return fmt.Sprintf("%.1fs", d.Seconds())
		}
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// formatTokens is the rail's token count: 124k, 200k, 1.2M.
func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
