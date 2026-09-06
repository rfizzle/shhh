package chat

// The run's row in the transcript. It is its own file because the row is the
// whole of what a run looks like, and it reads the machine rather than
// driving it — none of the stages beside it renders anything.
//
// The run is one row in the transcript, appended when it starts and redrawn
// on every transition (docs/interface/surfaces.md#the-backlog-runs-row). It
// used to be a scatter of one-line notices — a stage started, a verify
// passed, a lane landed — that a reader had to put back together by
// scrolling. It is a row because a run is a step of steps, and a step is
// what the transcript already has a shape for.
//
// The row stores a handle on the machine's own state rather than a copy of
// it, the way the fan-out block stores a batch number: everything it draws
// is read at render time, so it moves as the run moves and re-renders at any
// width. What it does keep is the strip's own memory — which stages this row
// watched go by — because a tick claims the row saw a stage finish, and a
// run picked up from a checkpoint saw nothing of the stages before it.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// runMark is one strip stage's state on the row.
type runMark int

const (
	runPending  runMark = iota // · not reached
	runRestored                // ↺ done in an earlier session; this row did not watch it
	runLive                    // ▸ where the run is now
	runPassed                  // ✓ watched finish
	runStopped                 // ✗ where the run blocked
)

// todoRunRow is the transcript's handle on one run.
type todoRunRow struct {
	// st is the machine's state, shared with the session while the run goes
	// and left behind as the row's final state once it ends.
	st *run.State
	// marks is the strip's record per stage; see the file comment.
	marks map[run.Stage]runMark
	// closed is how the session ended a run the machine did not finish:
	// `kept` for one let go of at its checkpoint, `stopped` for one
	// abandoned. Either is a different thing from done and from blocked,
	// and the row has to say which rather than freeze on whatever stage it
	// happened to be in.
	closed string
	// followUp is the item a blocked run's follow-up proposal was written
	// as, empty until the reader accepts it.
	followUp string
}

// newTodoRunRow starts a row on a state. A state that is already past the
// first stage is a run continued from a checkpoint: everything below where it
// resumed happened in a session this row never saw, so it starts as restored
// rather than as passed.
func newTodoRunRow(st *run.State) *todoRunRow {
	r := &todoRunRow{st: st, marks: map[run.Stage]runMark{}}
	for i, stage := range st.Shape().Strip() {
		if i < st.Shape().Place(st.Stage) {
			r.marks[stage] = runRestored
		}
	}
	return r
}

// observe records a transition. Every strip stage below the step's own is
// passed if this row watched it and restored if it did not; the step's own
// stage is where the run is now.
func (r *todoRunRow) observe(step run.Step) {
	if r == nil {
		return
	}
	place := r.st.Shape().Place(step.Stage)
	if place < 0 {
		// An ended run: the strip keeps what it has, and the end state below
		// settles the stage the run stopped in.
		r.settle(step)
		return
	}
	for i, stage := range r.st.Shape().Strip() {
		switch {
		case i > place:
			// A run that went back — a remediation round returns to
			// implement — has the stages above it ahead of it again, and a
			// tick on one of them would say it is behind.
			r.marks[stage] = runPending
		case i == place:
			r.marks[stage] = runLive
		case r.marks[stage] == runPending, r.marks[stage] == runRestored:
			// A stage below the run that this row never watched happen: it
			// was done in the session the checkpoint came from, and a tick
			// would claim otherwise.
			r.marks[stage] = runRestored
		default:
			r.marks[stage] = runPassed
		}
	}
	r.settle(step)
}

// settle marks where a run that has ended stopped: passed for the stage it
// finished in, broken for the one it blocked in.
func (r *todoRunRow) settle(step run.Step) {
	switch step.Action {
	case run.ActionBlocked:
		for stage, mark := range r.marks {
			if mark == runLive {
				r.marks[stage] = runStopped
			}
		}
	case run.ActionDone:
		for stage, mark := range r.marks {
			if mark == runLive {
				r.marks[stage] = runPassed
			}
		}
	}
}

// live reports whether the run is still moving, which is what keeps the
// block it sits in out of the render cache.
func (r *todoRunRow) live() bool {
	return r != nil && r.st != nil && !r.st.Over() && r.closed == ""
}

// liveTodoRunBlock is the earliest block holding a run that is still going.
// A run's row changes on transitions, and a transition lands no row of its
// own, so nothing from there on may be frozen.
func (m Model) liveTodoRunBlock(blocks []transcriptBlock) int {
	for i, blk := range blocks {
		for j := blk.start; j < blk.end && j < len(m.transcript); j++ {
			if e := m.transcript[j]; e.kind == entryTodoRun && e.todorun.live() {
				return i
			}
		}
	}
	return len(blocks)
}

// runRowGlyph pairs every mark with a glyph of its own, so a monochrome
// terminal keeps them apart (docs/interface/principles.md#colour-never-carries-meaning-alone).
func runRowGlyph(mark runMark) string {
	switch mark {
	case runPassed:
		return sty.Step.Done.Render("✓")
	case runLive:
		return sty.Step.Run.Render("▸")
	case runStopped:
		return sty.Step.Fail.Render("✗")
	case runRestored:
		return sty.Step.Dim.Render("↺")
	}
	return sty.Step.Dim.Render("·")
}

// outcome is what the row's stats field states: where the run is, in the
// words the record keys a transition on (run.Step.Name), with the one thing
// no stage word can carry said beside it — a run finished without a commit,
// where "done" alone would send the reader looking for one.
func (r *todoRunRow) outcome() string {
	switch {
	case r.closed != "":
		return r.closed
	case r.st.Stage == run.StageDone && r.st.NoCommit:
		return "done · not committed"
	case r.st.Paused != "":
		return "paused"
	}
	return string(r.st.Stage)
}

// glyph is the row's own state mark: running, finished, broken, or stopped
// where it stands.
func (r *todoRunRow) glyph() string {
	switch {
	case r.st.Stage == run.StageBlocked:
		return sty.Step.Fail.Render("✗")
	case r.st.Stage == run.StageDone:
		return sty.Step.Done.Render("✓")
	case r.closed != "", r.st.Paused != "":
		return sty.Step.Dim.Render("·")
	}
	return sty.Step.Run.Render("▸")
}

// elapsed is the run's span as of its last transition, in the whole-turn
// form: a run spends a model turn on most of its stages, and past a minute a
// count of seconds stops reading as a duration. It is measured between the
// two ends the checkpoint already stamps rather than against the clock,
// because a run advances in stages and a figure ticking between them would
// be the one thing on the row that is not a fact about the run.
func (r *todoRunRow) elapsed() time.Duration {
	if r.st.Updated.Before(r.st.Started) {
		return 0
	}
	return r.st.Updated.Sub(r.st.Started)
}

// title is the row's growing field.
func (r *todoRunRow) title() string {
	t := "todo run " + r.st.Slug
	if r.st.Grade != "" {
		t += " · " + r.st.Profile.Grade + " " + r.st.Grade
		if r.st.GradeBefore != r.st.Grade {
			t += " (was " + orDash(r.st.GradeBefore) + ")"
		}
	}
	return t
}

// headerLine is the row's own line, on the step header's grid so a run and
// the steps around it share one edge: the fold state, the state glyph in the
// ordinal column, the title, a faint rule, where the run is, and its span.
func (r *todoRunRow) headerLine(width int, folded bool) string {
	fold := "▾"
	if folded {
		fold = "▸"
	}
	lead := sty.Step.Dim.Render(fold) + " " + r.glyph() + " " + " "
	leadW := components.GridPointerWidth + stepOrdinalWidth + 1

	label := r.outcome()
	stats := sty.Step.Stats.Render(label)
	statsW := lipgloss.Width(label)

	titleStyle := sty.Step.Title
	if r.live() {
		titleStyle = sty.Step.LiveTitle
	}
	fixed := leadW + statsW + components.GridDurationWidth + 3
	title := clipRow(r.title(), width-fixed)
	rule := width - leadW - lipgloss.Width(title) - statsW - components.GridDurationWidth - 2
	if rule < 1 {
		rule = 1
	}
	line := lead + titleStyle.Render(title) + " " +
		sty.Step.Rule.Render(strings.Repeat("─", rule)) + " " + stats +
		stepDurationField(turnDuration(r.elapsed()), sty.Step.Stats)
	return strings.TrimRight(line, " ")
}

// stripSegments draws the stages the run passes through, in order, each with
// its own glyph and its own word. The word is the stage's, which is the word
// the record keys the transition on, so the row and the record cannot
// disagree about what happened (run.Step.Name).
func (r *todoRunRow) stripSegments() []string {
	strip := r.st.Shape().Strip()
	segs := make([]string, 0, len(strip))
	for _, stage := range strip {
		mark := r.marks[stage]
		style := sty.Step.Dim
		switch mark {
		case runLive:
			style = sty.Step.LiveTitle
		case runPassed, runStopped:
			style = sty.Step.Title
		}
		segs = append(segs, runRowGlyph(mark)+" "+style.Render(string(stage)))
	}
	return segs
}

// stripLines packs the stages into as many lines as the width needs. The
// strip is a closed set of five words and the row exists to show all of
// them, so it wraps where every other growing field clips: a terminal too
// narrow for one line would otherwise be told the run has four stages
// (docs/interface/principles.md#fold-never-hide).
func (r *todoRunRow) stripLines(width int) []string {
	sep := sty.Step.Dim.Render(" · ")
	var lines []string
	line, room := "", 0
	for _, seg := range r.stripSegments() {
		w := lipgloss.Width(seg)
		switch {
		case line == "":
			line, room = seg, w
		case room+3+w <= width:
			line, room = line+sep+seg, room+3+w
		default:
			lines = append(lines, line)
			line, room = seg, w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// notes are the lines under the strip: what a stage the strip cannot say in
// one word is doing. Each names its stage, because the strip is a row of
// words and a number floating under it belongs to whichever one the reader
// guesses.
func (r *todoRunRow) notes() []runRowLine {
	var out []runRowLine
	if restored := r.restoredStages(); len(restored) > 0 {
		out = append(out, runRowLine{text: "restored from the checkpoint: " + strings.Join(restored, " · ")})
	}
	if n := len(r.st.Lanes); n > 0 {
		out = append(out, runRowLine{painted: sty.Step.Stats.Render("implement  ") + r.laneMeter().View() +
			sty.Step.Stats.Render("  "+strings.Join(laneNames(r.st), " · "))})
	}
	if r.st.Round > 0 {
		out = append(out, runRowLine{text: "remediate  " + r.rounds()})
	}
	if r.st.Verdict != "" {
		out = append(out, runRowLine{text: "review  " + r.st.Verdict})
	}
	if r.st.Paused != "" {
		out = append(out, runRowLine{text: "paused  " + r.st.Paused})
	}
	if r.st.Blocked != "" {
		out = append(out, runRowLine{text: "blocked  " + firstLine(r.st.Blocked)})
		if r.followUp != "" {
			out = append(out, runRowLine{text: "follow-up  " + r.followUp})
		}
	}
	return out
}

// runRowLine is one line under the row. Its text is plain, so the row can
// wrap it at whatever width it is drawn at; painted is the alternative for a
// line whose colouring is not the row's to decide — the lane meter, whose bar
// and number are one field and turn colour together — and it clips, because
// it is a row of fields rather than prose.
type runRowLine struct {
	text    string
	painted string
	// head marks the word that names what the lines under it are, which is
	// the only thing under a run's row drawn in body weight: everything else
	// there is the detail it heads.
	head bool
	// under is a line belonging to the heading above it. The step it takes
	// is the row's, applied after the wrap rather than written into the
	// text, so a wrapped path lands under the one before it and not back at
	// the heading's own column.
	under bool
}

// rounds is the remediation note: which round is being spent while one is,
// and how many were spent once the run has moved on. A round count is a fact
// about a run that outlives the round — the note stays on the row after the
// stage is ticked — and `round 1/2` read beside a finished run would say a
// round is in flight when none is.
func (r *todoRunRow) rounds() string {
	n, of := r.st.Round, r.st.Rounds()
	if r.st.Remediating() {
		return fmt.Sprintf("round %d/%d", n, of)
	}
	return fmt.Sprintf("%d/%d rounds spent", n, of)
}

// restoredStages names the stages the row did not watch happen, in strip
// order. A glyph on the strip says a stage is not a tick; this says in words
// what it is instead (invariant 1).
func (r *todoRunRow) restoredStages() []string {
	var out []string
	for _, stage := range r.st.Shape().Strip() {
		if r.marks[stage] == runRestored {
			out = append(out, string(stage))
		}
	}
	return out
}

// laneMeter is a large item's lanes as one meter: how many of the writers'
// patches have landed against how many were spawned.
func (r *todoRunRow) laneMeter() components.Meter {
	done, total := laneProgress(r.st)
	// The lane meter, which is what a fan-out's own lanes are drawn with: a
	// run's lanes are that fan-out, and one count drawn two ways would be two
	// answers to how far along it is.
	m, _ := components.AgentMeter(done, total)
	m.Text = fmt.Sprintf("%d/%d landed", done, total)
	return m
}

// laneProgress counts the lanes whose patch has landed.
func laneProgress(st *run.State) (done, total int) {
	for _, l := range st.Lanes {
		if l.Done {
			done++
		}
	}
	return done, len(st.Lanes)
}

// laneNames is the lanes in the order they were divided.
func laneNames(st *run.State) []string {
	out := make([]string, 0, len(st.Lanes))
	for _, l := range st.Lanes {
		out = append(out, l.Name)
	}
	return out
}

// runAnswerLines bounds one stage answer under an opened row.
const runAnswerLines = 12

// answers are what the row opens to: the stage answers the run has, in the
// order the run produced them. This is the fold's other half — folded, the
// row says a run happened and where it got to; opened, it says what each
// stage answered (docs/interface/principles.md#fold-never-hide).
func (r *todoRunRow) answers() []runRowLine {
	var out []runRowLine
	head := func(word string) { out = append(out, runRowLine{text: word, head: true}) }
	body := func(lines ...string) {
		for i, l := range lines {
			if i == runAnswerLines && len(lines) > runAnswerLines+1 {
				// The bound is a fold and not a loss: a failing verify's
				// output runs to forty lines and reaches the transcript on
				// its own row, so what this one owes the reader is the head
				// of it and an honest count of the rest
				// (docs/interface/principles.md#fold-never-hide).
				out = append(out, runRowLine{
					text:  fmt.Sprintf("… %d more lines", len(lines)-runAnswerLines),
					under: true,
				})
				return
			}
			out = append(out, runRowLine{text: l, under: true})
		}
	}
	if len(r.st.Steps) > 0 {
		head("plan")
		for i, step := range r.st.Steps {
			body(fmt.Sprintf("%d. %s", i+1, step))
		}
	}
	if len(r.st.Questions) > 0 {
		head("questions")
		for _, q := range r.st.Questions {
			body("? " + q)
		}
	}
	if len(r.st.Lanes) > 0 {
		head("lanes")
		for _, lane := range r.st.Lanes {
			body(lane.Name + "  " + strings.Join(lane.Paths, ", "))
		}
	}
	if r.st.Findings != "" {
		head("findings")
		body(strings.Split(strings.TrimRight(r.st.Findings, "\n"), "\n")...)
	}
	if r.st.Blocked != "" {
		head("blocked")
		body(strings.Split(strings.TrimRight(r.st.Blocked, "\n"), "\n")...)
	}
	if r.st.Report != "" {
		head("report")
		body(strings.Split(strings.TrimRight(r.st.Report, "\n"), "\n")...)
	}
	if len(r.st.Files) > 0 {
		head("files")
		body(strings.Join(r.st.Files, ", "))
	}
	return out
}

// offers are the keys the row carries: a blocked run's item can be reopened
// from the row it blocked on, which is where the reader is when they decide
// to.
func (r *todoRunRow) offers() []components.TurnKey {
	if r.st.Stage != run.StageBlocked {
		return nil
	}
	return []components.TurnKey{{Key: keys.Bracket(keys.Row.Reopen), Label: keys.Words(keys.Row.Reopen)}}
}

// todoRunRowView renders the row: its header, the strip, the notes each
// stage adds, and — opened — the answers the stages gave.
func (m Model) todoRunRowView(e entry, width int, keysLive bool) string {
	r := e.todorun
	indent := strings.Repeat(" ", components.GridDetailIndent)
	inner := max(width-components.GridDetailIndent, 1)
	lines := []string{r.headerLine(width, !e.expanded)}
	clipped := func(s string) { lines = append(lines, indent+clipRow(s, inner)) }
	// Wrapped rather than clipped, for the reason the notices with bodies
	// wrap: these are sentences and paths, and half of either is worse than
	// a line that costs two.
	wrapped := func(l runRowLine) {
		if l.painted != "" {
			clipped(l.painted)
			return
		}
		style, step := sty.Step.Stats, ""
		if l.head {
			style = sty.Step.Title
		}
		if l.under {
			step = "  "
		}
		for _, text := range strings.Split(m.wordWrap(l.text, max(inner-len(step), 1)), "\n") {
			lines = append(lines, indent+step+style.Render(text))
		}
	}
	for _, line := range r.stripLines(inner) {
		clipped(line)
	}
	for _, note := range r.notes() {
		wrapped(note)
	}
	if e.expanded {
		for _, answer := range r.answers() {
			wrapped(answer)
		}
	}
	if offers := components.KeyRun(r.offers(), !keysLive, m.rowHandover(keysLive)); offers != "" {
		clipped(offers)
	}
	return strings.Join(lines, "\n")
}

// openTodoRunRow appends the row a run is drawn on and remembers where it
// landed. One row per run: it is appended when the run starts — including a
// run continued from a checkpoint, which is a second sitting of the same run
// and gets a second row, because the first one is in a transcript this
// session no longer has.
func (m *Model) openTodoRunRow() {
	m.appendEntry(entry{kind: entryTodoRun, todorun: newTodoRunRow(m.todoRunner.state)})
	m.todoRunner.rowIdx = len(m.transcript)
}

// todoRunRowEntry is the run's row, or nil where no run is drawn.
func (m Model) todoRunRowEntry() *todoRunRow {
	if m.todoRunner.rowIdx <= 0 || m.todoRunner.rowIdx > len(m.transcript) {
		return nil
	}
	return m.transcript[m.todoRunner.rowIdx-1].todorun
}

// observeTodoRunRow tells the row a transition happened. The row is redrawn
// from the machine's state, so the cache that has already seen this entry has
// to be told it changed.
func (m *Model) observeTodoRunRow(step run.Step) {
	r := m.todoRunRowEntry()
	if r == nil {
		return
	}
	r.observe(step)
	m.invalidateRenderCache()
}

// closeTodoRunRow says how the session ended a run the machine did not
// finish, and lets the row go: the next run opens its own.
func (m *Model) closeTodoRunRow(how string) {
	if r := m.todoRunRowEntry(); r != nil {
		r.closed = how
		m.invalidateRenderCache()
	}
	m.todoRunner.rowIdx = 0
}

// todoRunRowIndexOf finds the transcript index of a run row, or -1.
func todoRunRowIndexOf(es []entry, idx int) int {
	if idx < 0 || idx >= len(es) || es[idx].kind != entryTodoRun || es[idx].todorun == nil {
		return -1
	}
	return idx
}

// todoRunReopen is `[o]` on a blocked run's row: the item it blocked goes
// back to open. It is refused on a row whose item another run is working —
// reopening it under a run would put the run's own item back to a state the
// run does not expect.
func (m Model) todoRunReopen(idx int) (tea.Model, tea.Cmd, bool) {
	if todoRunRowIndexOf(m.transcript, idx) < 0 {
		return m, nil, false
	}
	r := m.transcript[idx].todorun
	if r.st.Stage != run.StageBlocked {
		return m, nil, false
	}
	slug := r.st.Slug
	if m.todoRunner.state != nil && !m.todoRunner.state.Over() && m.todoRunner.state.Slug == slug {
		next, cmd := m.systemNotice(fmt.Sprintf("%s is being run again; /todo stop ends that run first.", slug))
		return next, cmd, true
	}
	it, ok := m.todoStore.Find(slug)
	if !ok {
		next, cmd := m.systemNotice(fmt.Sprintf("No backlog item %q; it may have been archived or renamed since the run blocked.", slug))
		return next, cmd, true
	}
	if err := todo.SetStatus(it.Path, todo.StatusOpen); err != nil {
		next, cmd := m.systemNotice("Could not reopen " + slug + " — " + err.Error())
		return next, cmd, true
	}
	m.reloadTodos()
	next, cmd := m.systemNotice(fmt.Sprintf("%s is open again; /todo run %s starts it over.", slug, slug))
	return next, cmd, true
}
