package chat

// Two-pane cockpit (docs/interface/surfaces.md#the-inspector-rail). At
// or above a 130-column terminal the surface splits: the transcript keeps the
// left pane, the inspector rail takes the right — 46 columns at the rung and
// wider with the terminal — and one dim │ column divides them. Under that the
// rail is dropped entirely and the single-pane layout is exactly what it was.
//
// The split is horizontal only — it is one of the two constraints the
// column half of the layout model resolves (layout.go), and the row
// budget it hands out is unchanged — and the prompt frame spans both panes,
// because steering is a session-level act. Takeover surfaces
// (approval cards, pickers, the full-screen diff, the agent list) span the
// full width and hide the rail, restoring it when they are dismissed. An
// attached child's session is not a takeover: the rail stays, and its AGENTS
// block marks the row the keyboard is in.
//
// Everything the rail shows is already known to the session; the rail is a
// passive renderer fed from here, like components.Cockpit. It is also
// somewhere to go from: its rows carry what they name, so a click on a
// changed file opens that file's diff and a click on a session attaches to it
// (railclick.go).
//
// THIS TURN is the turn; CHANGES, AGENTS, CONTEXT and SPEND are the session
//. The chat transcript is the turn-by-turn feed, so the rail is
// the standing overview beside it rather than a second copy of the same
// scroll.

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// paneDividerWidth is the single │ column between the panes.
	paneDividerWidth = 1
	// contextBurnSamples bounds the per-round context series to what the
	// widest rail's sparkline can draw. It is the ceiling rather than the
	// current width because the series outlives any one terminal size: a
	// window dragged wider must not find that the rounds it could now show
	// were thrown away while it was narrow.
	contextBurnSamples = components.SparkCellsRailMax
)

// twoPane reports whether the surface is split. Width is the first condition;
// a takeover surface is the second.
func (m Model) twoPane() bool { return m.columns().inspector.Dx() > 0 }

// inspectorHidden reports whether something is covering the rail. Takeover
// surfaces span both panes.
//
// An attached child is not one of them, and the obvious argument that it
// should be — the rail's numbers are the parent's — is the reason it is not.
// The changeset, the context and the bill are the session's whichever session
// the keyboard is in, so hiding all three to read one child's transcript
// costs every standing question the rail exists to answer and settles none.
// What keeps that honest is the map: it marks the row holding the keyboard,
// so the transcript on the left is visibly one child's and the numbers on the
// right are visibly the session's
// (docs/interface/surfaces.md#the-inspector-rail).
func (m Model) inspectorHidden() bool {
	if m.agentList != nil {
		return true
	}
	// A decision that draws above the frame is not a takeover: the panes above
	// it are what the reader is looking at, and a card landing must not reflow
	// the screen behind it. Every decision does that while the draft still
	// holds the keyboard (the mid-sentence rule), and the one-answer question
	// does it holding the keyboard too — one row of answer buys nothing with
	// the columns the rail is standing in (interrupt.go, question.go).
	if m.decisionRides() {
		return false
	}
	if m.activeChildAsk() != nil {
		return true
	}
	switch m.state {
	case stateConfirmRun, statePlanApprove, stateQuestion, statePick, stateTodoPropose, stateTodoDraft, statePasteDrop, stateScaffold, statePersona, stateTodoPause, stateDiffFull, stateOutputFull, stateReview, stateContext, stateSources, stateBacklog, stateConfig, stateModelList:
		return true
	}
	return false
}

// paneWidth is the transcript pane's own width: the reduced pane when the
// surface is split, the full content width otherwise. It is what the surfaces
// that take the pane over — the full-screen diff, review mode, the agent rows
// — render to, and the columns the body is drawn into (layout.go).
func (m Model) paneWidth() int { return m.columns().pane.Dx() }

// transcriptWidth is the width the transcript wraps to: the pane less the
// scroll gutter's column, which the pane reserves whether or
// not there is anything to draw in it. Everything the viewport shows — the
// feed, reading mode's gutter render, an attached child's session, the start
// screen — wraps to this, and so does the selection's coordinate space, so
// the gutter is never inside anything a drag can reach.
func (m Model) transcriptWidth() int { return max(m.columns().feed.Dx(), 1) }

// turnStartIndex is the first entry of the current turn — the last user entry
// in the transcript. It returns len(transcript) when no turn has started.
func (m Model) turnStartIndex() int {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryUser {
			return i
		}
	}
	return len(m.transcript)
}

// turnEntries is the current turn's slice of the transcript.
func (m Model) turnEntries() []entry {
	return m.transcript[m.turnStartIndex():]
}

// turnElapsed is how long the current turn has been running — live while it
// works, final once it is done.
func (m Model) turnElapsed() time.Duration {
	if m.turnStarted.IsZero() {
		return 0
	}
	if m.working() || m.turnEnded.IsZero() {
		return time.Since(m.turnStarted)
	}
	return m.turnEnded.Sub(m.turnStarted)
}

// inspectorData is the rail a frame draws, resolved onto the frame the first
// time it is asked for and read back from there after (layout.go). Outside a
// paint there is no frame and every caller resolves its own.
func (m Model) inspectorData() components.InspectorRail {
	if m.framed == nil {
		return m.resolveInspector()
	}
	if key := m.railKey(); m.framed.rail == nil || m.framed.rail.key != key {
		m.framed.rail = &railBlock{key: key, rail: m.resolveInspector()}
	}
	return m.framed.rail.rail
}

// railBlock is the rail resolved, with the reading it was resolved under.
type railBlock struct {
	key  railKey
	rail components.InspectorRail
}

// railKey is what the rail was drawn against: the spinner's frame, what the
// transcript reads, and which turn the session is on. Between them they move
// on every tick the rail has anything new to say, which is what makes them
// the reading to compare rather than the whole of it.
//
// It is a key and not an assumption because everything else the rail reads —
// the changeset, a child's token count, the bill — moves on its own clock. A
// frame is one paint (layout.go), and inside one paint none of those can
// move; a memo that outlived a paint would be answering with a reading that
// had, and comparing the key is what stops that from being something the next
// reader has to know.
type railKey struct {
	frame      int
	transcript transcriptReading
	turn       int64
}

// railKey reads the three the rail is keyed on off the session. It is here
// rather than beside the fields because what the rail depends on is the
// rail's own business.
func (m Model) railKey() railKey {
	return railKey{frame: m.spinFrame, transcript: m.transcriptReading(), turn: m.turnCount}
}

// transcriptReading is what a reading taken off the transcript was taken
// against: how many rows there were, and how many of them have since been
// rewritten where they lie (model.go).
//
// It is the pair and not either half. The count is what moves when a row
// lands, which is most of what happens to a transcript and is free to read.
// The revision is the half the count cannot see: the context trim replaces
// a result with its placeholder and leaves the length exactly as it found it
// (context.go), so a reading keyed on the count alone answers a trimmed
// session with what it said before the trim — and for the alert scan below
// that means re-reporting a failure the quality gate has already answered
// and the turn's close row has already called green (resolved.go).
type transcriptReading struct {
	entries int
	rev     int64
}

// transcriptReading reads that pair off the session.
func (m Model) transcriptReading() transcriptReading {
	return transcriptReading{entries: len(m.transcript), rev: m.transcriptRev}
}

// resolveInspector assembles the rail from what the session already tracks. A
// block with nothing to say is left nil, and the component omits it.
//
// The approved plan's checklist is read once and handed to both blocks that
// need it: THIS TURN takes its denominator from it and PLAN draws it, and
// reading the transcript twice to tell one story would be waste.
func (m Model) resolveInspector() components.InspectorRail {
	steps := m.planChecklist()
	return components.InspectorRail{
		Summary:    m.inspectorSummary(),
		Turn:       m.inspectorTurn(steps),
		Plan:       m.inspectorPlan(steps),
		Todo:       m.inspectorTodo(),
		Alerts:     m.inspectorAlerts(),
		Changes:    m.inspectorChanges(),
		Agents:     m.inspectorAgents(),
		AgentsHint: agentsHintRail(),
		// The trailer is all chords, so it is a row that says what a chord
		// needs — but only while it is the session's first, which is the same
		// question every transcript row asks before saying it (inertkeys.go).
		AgentsOption: m.firstRowOffer(),
		Tools:        m.inspectorTools(),
		Context:      m.inspectorContext(),
		Spend:        m.inspectorSpend(),
		Frame:        m.spinFrame,
	}
}

// inspectorTurn counts this turn's steps and tools. Without an approved plan
// the step count is observed, not declared, so it feeds "step 3" and no
// meter. An approved plan is the one place a total is authoritative,
// and only then does the progress meter have a true denominator.
func (m Model) inspectorTurn(steps []components.InspectorPlanStep) *components.InspectorTurn {
	es := m.turnEntries()
	t := components.InspectorTurn{Running: m.working()}
	if len(steps) > 0 {
		t.Step, t.Steps = planProgress(steps), len(steps)
	} else {
		for _, blk := range m.blocksOf(es) {
			if blk.step != nil && !blk.step.queued() {
				t.Step = blk.step.ordinal
			}
		}
	}
	for _, e := range es {
		if isActivityEntry(e) {
			t.Tools++
		}
	}
	// The turn's own files come from the same changeset its close row reads
	//, so THIS TURN and the row it leaves in the transcript cannot
	// report the turn two ways.
	if turn, ok := m.changes.Turn(m.turnCount); ok {
		t.Files, t.Added, t.Removed = turn.Files(), turn.Added, turn.Removed
	}
	if t.Tools == 0 && t.Files == 0 && !t.Running {
		return nil
	}
	return &t
}

// inspectorPlan is the PLAN block: the approved plan as a live checklist, so
// "where are we" never needs asking. It follows the plan rather
// than the turn or the session, because a plan that spans two turns is still
// the answer to the same question and is retired by the next instruction
// rather than by the clock.
func (m Model) inspectorPlan(steps []components.InspectorPlanStep) *components.InspectorPlan {
	if m.planRun == nil || len(steps) == 0 || !m.codingSurfaces() {
		return nil
	}
	return &components.InspectorPlan{
		Steps: steps,
		Done:  planStepsDone(steps),
		Drift: m.planRun.driftLabel(),
		Hint:  planHintRail,
	}
}

// inspectorChanges is the session's net change to the workspace (
// every path this session has touched, collapsed to one row each with
// the turns behind it, and the commands still coming back broken above them.
//
// It is session-scoped deliberately. A file edited in turn 2 is still on
// screen in turn 8, because "what has this session done to my machine" does
// not reset when the agent starts a new turn — the turn-by-turn feed is the
// transcript's job, and THIS TURN is the one block that answers for the turn.
// The rows are read from the changeset store rather than from the transcript,
// so an undo nets out and a child's applied patch counts.
func (m Model) inspectorChanges() *components.InspectorChanges {
	if !m.codingSurfaces() {
		return nil
	}
	var c components.InspectorChanges
	touched := map[string]bool{}
	if t, ok := m.changes.Turn(m.turnCount); ok {
		for _, r := range t.Records {
			touched[r.Path] = true
		}
	}
	for _, f := range m.changes.SessionFiles() {
		c.Added += f.Added
		c.Removed += f.Removed
		c.Files = append(c.Files, components.InspectorFile{
			Path:     f.Path,
			Added:    f.Added,
			Removed:  f.Removed,
			Turns:    f.Turns,
			ThisTurn: touched[f.Path],
			Mode:     f.ModeChange,
		})
	}
	// What the session has banked, and what it deliberately left floating
	// beside it. Both are read once, when the commit was made, rather than
	// off the tree here: this runs on every frame, and a rail that shelled
	// out to git to draw itself would be paying for a fact that only changes
	// when a commit does (commit.go).
	if st := m.commit; st != nil && st.banked != nil {
		c.Committed, c.Foreign = st.banked, st.leaves
	}
	if len(c.Foreign) == 0 {
		// A resume that dropped a file because the tree moved under it
		// still names the path: it is no longer this sitting's to undo
		// or commit, and leaving it off the rail would look like the
		// session never touched it.
		c.Foreign = m.changes.Drifted()
	}
	if len(c.Files) == 0 && len(c.Foreign) == 0 {
		return nil
	}
	return &c
}

// inspectorAlerts is the workspace's standing bad news: every command this
// session ran that came back broken, in the order they broke, each with the
// turn it broke in and how many runs it has taken since
// (docs/interface/surfaces.md#the-inspector-rail).
//
// An alert follows the workspace rather than the turn — it is answered by the
// same command coming back clean, not by a new turn starting. That is the
// whole point of the block: a red row that clears itself because the agent
// moved on is the failure this rail exists to prevent.
//
// The other thing that answers one is the repository's own suite coming back
// clean over the tree the command failed on, which answers that failure
// whether or not the same line is ever run again. The rail asks the same
// resolution the close row does, so it cannot be red about a turn the close
// row called green (resolved.go).
//
// Neither answer deletes the alert. An answered one is marked and kept, so
// the block can count what it took to get to green without any of it being
// on screen as a current failure — which is the block's own rule and not
// this reading's (inspectoralerts.go).
//
// The scan walks every command the session has run, so it costs the whole
// transcript every time it is asked — and the rail asks twice a frame, on a
// spinner tick that moves whether or not anything happened. So the answer is
// kept with the reading it was taken against and handed back until the
// transcript reads differently, which is the whole of what an alert is a
// function of: a command lands, or the trim rewrites what a landed one says
// (transcriptReading).
func (m Model) inspectorAlerts() components.InspectorAlerts {
	if m.alertMemo == nil {
		return m.scanAlerts()
	}
	reading := m.transcriptReading()
	// The zero box is the reading of a session with nothing in it, whose
	// answer is no alerts — which is the answer, so the first read of an
	// empty transcript is a hit that happens to be right rather than a miss.
	if m.alertMemo.reading != reading {
		*m.alertMemo = alertMemo{reading: reading, alerts: m.scanAlerts()}
	}
	return m.alertMemo.alerts
}

// alertMemo is the last scan and the reading it was a scan of. A reading that
// still matches is a scan that is still true (model.go).
type alertMemo struct {
	reading transcriptReading
	alerts  components.InspectorAlerts
}

// scanAlerts is the walk itself.
//
// An alert is one command rather than one command line: an agent that runs a
// formatter over three directories has one thing wrong with its workspace and
// not three, and the rail drew three rows for it. The runs are collapsed onto
// the last one, because the last run is what the workspace is currently like.
//
// Nor is it one command in one turn. A suite still failing in the fourth turn
// running is the same one piece of news it was in the first, so the standing
// alert follows the command across those turns: the earlier turn's row is
// superseded by the later failure, and the one still standing says the turn
// the command first broke in and what every turn since has thrown at it. That
// is what makes the heading's count the number of rows under it rather than
// the number of attempts behind them.
func (m Model) scanAlerts() components.InspectorAlerts {
	type group struct {
		// at is where the group's last run sits in the transcript, which is
		// the position a later verification is asked about.
		at     int
		runs   int
		note   string
		broken bool
	}
	// standing is the alert still standing for a command: where it started,
	// how much is behind it, and which of its turns is the one that states
	// all that — the last turn it broke in, so the row sits where the most
	// recent failure is and the block draws it as the recent news it is.
	type standing struct {
		first int64
		runs  int
		turns int
		last  alertKey
	}
	verified := lastVerification(m.transcript)
	groups := map[alertKey]*group{}
	var order []alertKey
	// cleared is where each command last came back clean. A clean run answers
	// every failure of that command before it, in whatever turn it ran: the
	// key groups the rows, and it is the command rather than the key that the
	// workspace is either wrong about or not.
	cleared := map[string]int{}
	for i, e := range m.transcript {
		if e.kind != entryCommand {
			continue
		}
		label := firstLine(e.text)
		if label == "" {
			continue
		}
		// A command that never exited says what ended it rather than a
		// status it never had, the way its row does (activity.go) — and the
		// one the reader stopped is neither. This block is answered by a
		// command coming back clean, and somebody who cancelled a run is not
		// waiting for it to do that; nor did the run reach a verdict about
		// the tree for anything to be answered by. So a stop leaves the
		// group exactly as it found it, rather than overwriting the failure
		// the run before it reported.
		outcome := commandOutcome(e)
		if outcome == components.OutcomeStopped {
			continue
		}
		k := alertKey{name: alertName(label), turn: e.turn}
		g, ok := groups[k]
		if !ok {
			g = &group{}
			groups[k], order = g, append(order, k)
		}
		g.at, g.runs, g.note = i, g.runs+1, outcome
		if e.exitCode == 0 && e.end.outcome == "" {
			g.broken = false
			cleared[k.name] = i
			continue
		}
		g.broken = true
	}
	// A group is answered where the workspace has since been said to be right
	// about that command, by either answer the session has.
	answered := func(k alertKey, at int) bool {
		return verified.settled(at) || cleared[k.name] > at
	}
	still := map[string]*standing{}
	for _, k := range order {
		g := groups[k]
		if !g.broken || answered(k, g.at) {
			continue
		}
		st, ok := still[k.name]
		if !ok {
			st = &standing{first: k.turn}
			still[k.name] = st
		}
		st.runs, st.turns, st.last = st.runs+g.runs, st.turns+1, k
	}
	var alerts components.InspectorAlerts
	for _, k := range order {
		g := groups[k]
		if !g.broken {
			continue
		}
		// Every group but the one the standing alert is stated at is
		// superseded: an answered one by what answered it, an earlier live
		// one by the failure that came after it.
		alert := components.InspectorAlert{
			Label: k.name, Note: g.note, Runs: g.runs, Turn: k.turn,
			Turns: 1, Superseded: true,
		}
		if st := still[k.name]; st != nil && st.last == k {
			alert = components.InspectorAlert{
				Label: k.name, Note: g.note, Runs: st.runs, Turn: st.first, Turns: st.turns,
			}
		}
		alerts = append(alerts, alert)
	}
	return alerts
}

// commandOutcome is what a command's run came to, in the word its own row
// states: the exit code, or what ended it where nothing let it exit.
func commandOutcome(e entry) string {
	if e.end.outcome != "" {
		return e.end.outcome
	}
	return components.OutcomeExit(e.exitCode)
}

// alertKey is what the walk groups a command's runs by: the command, in the
// turn that ran it. The turn is half the key because a run count is a turn's
// own — three runs of a formatter in one turn are one attempt at one thing —
// while the alert those groups roll up into spans every turn the command has
// gone on breaking in.
type alertKey struct {
	name string
	turn int64
}

// alertName is what an alert calls a command line: its first word, and the
// subcommand after it where the second word is a bare one rather than a flag
// or a path. That is the difference between the collapse worth making and
// the one that is not — `gofmt -w a.go` and `gofmt -w b.go` are one thing
// wrong with the workspace, and `go test ./...` and `go build ./...` are two
// (docs/interface/surfaces.md#the-inspector-rail).
//
// The environment a line sets in front of the command is not part of its
// name. Two commands run under one variable are still two commands, and a
// name read off the assignment would say the same thing about both.
func alertName(command string) string {
	fields := strings.Fields(command)
	for len(fields) > 0 && strings.Contains(fields[0], "=") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 1 && bareWord(fields[0]) && bareWord(fields[1]) {
		return fields[0] + " " + fields[1]
	}
	return fields[0]
}

// bareWord reports whether a token is a word a command is named by rather
// than something it was given: a flag, a path, an assignment, a glob and a
// redirection each have a character in them that a subcommand does not.
func bareWord(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// inspectorAgents is the session map: this session first, then every child in
// spawn order — running, waiting or finished. Listing only what is in flight
// answers "what is running now" and leaves "what has this run done" to a
// surface somebody has to open, which is the interrogation the rail exists to
// end. Carrying every session is also what makes the rail usable while the
// keyboard is in a child: the row it is in is marked, so the blocks under it
// are visibly the session's rather than that child's.
//
// The order is the supervisor's own, which is spawn order, and it is the same
// order the cycle walks (attach.go), so moving one row on the keyboard moves
// one row on screen.
func (m Model) inspectorAgents() []components.InspectorAgent {
	if m.subagents == nil {
		return nil
	}
	snapshot := m.subagents.Snapshot()
	if len(snapshot) == 0 {
		return nil
	}
	agents := []components.InspectorAgent{m.orchestratorAgent()}
	for _, st := range snapshot {
		// Every surface that draws a child reads it through the same
		// progress struct — the fan-out lane, the manager's row and this —
		// so what the rail says about a child cannot drift from what the
		// transcript beside it says about the same child.
		p := m.childProgress(st)
		a := components.InspectorAgent{
			Name:    st.Name,
			Detail:  st.Detail,
			Spend:   p.Spend,
			Tools:   p.Tools,
			Step:    p.Step,
			Steps:   p.Steps,
			State:   p.State,
			Focused: st.Name == m.attachedTo,
			// What the child has taken in against what it was given, so the
			// row can draw the ceiling coming rather than the word it died
			// on; how many times this turn it has been told it left its
			// task; and whether a replacement could pick its work up.
			Fresh:   st.Tokens.Fresh,
			Budget:  st.Budget,
			Steers:  st.Steers,
			Handoff: st.Handoff != "",
			Depth:   m.sessionDepth(st.Name, len(snapshot)),
		}
		switch st.State {
		case subagent.StateDone, subagent.StateFailed:
			// The supervisor's own word for how it ended, because the
			// supervisor is the only thing that knows; the line under it
			// says what it found or why it broke, rather than repeating the
			// word with a tool count on it.
			a.Outcome, a.Detail = st.State.String(), childNote(st)
			// The supervisor writes the record's handle into the same line,
			// for a surface with room to print it. This row has none: it is
			// forty-odd columns and it says the record was kept in words,
			// with the key that uses it, so the handle here would be a
			// truncated identifier crowding out the offer.
			if a.Handoff {
				a.Detail = strings.TrimSuffix(a.Detail, " · handoff "+st.Handoff)
			}
		}
		agents = append(agents, a)
	}
	return agents
}

// agentsHintRail is the trailer under the map: the manager, the chord to the
// next session, and the pointer. The letters are the register's, so a rebind
// moves the row with it (docs/interface/surfaces.md#the-inspector-rail).
//
// The chord walks both ways and only the forward key is named. The row is
// forty-four columns at the rail's floor, which is exactly what these three
// clauses take, and the reverse of a chord whose forward key is on screen is
// the one thing a reader can guess — where a fourth clause would clip one of
// the other three off the narrowest rail there is.
func agentsHintRail() string {
	return strings.Join([]string{
		keys.Shown(keys.Draft.Agents) + " manager",
		keys.Shown(keys.Draft.NextAgent) + " next",
		"click to attach",
	}, " · ")
}

// sessionDepth is how far under the orchestrator a session sits: 1 for a
// child this session spawned, 2 for that child's own child. The map draws a
// depth past 1 one column in, so a run several levels deep reads as the tree
// it is rather than as a flat list of siblings.
//
// The walk is bounded by the number of sessions there are: a supervisor whose
// parent links ever came to point in a circle would otherwise hang the paint
// rather than draw one row wrong, and this runs on every frame.
func (m Model) sessionDepth(name string, sessions int) int {
	depth := 0
	for at := name; at != "" && depth <= sessions; {
		parent, ok := m.subagents.Parent(at)
		if !ok {
			break
		}
		depth++
		at = parent
	}
	return depth
}

// orchestratorAgent is the map's first row: this session itself. Its state is
// what the session is doing rather than a lifecycle — nothing spawned the
// orchestrator and nothing collects it — so it is running while a turn is,
// waiting while a decision stands in front of you, and idle otherwise. That
// is the same three answers the manager's row gives in words, and it is built
// from that row so the two cannot come to disagree.
func (m Model) orchestratorAgent() components.InspectorAgent {
	row := m.orchestratorRow()
	a := components.InspectorAgent{
		Name:    row.Name,
		Detail:  row.Status,
		Spend:   row.Spend,
		Self:    true,
		Focused: m.attachedTo == "",
		State:   components.FanoutIdle,
	}
	switch {
	case m.working():
		a.State = components.FanoutRunning
	case m.state == stateConfirmRun || m.state == statePlanApprove || m.state == stateQuestion:
		a.State = components.FanoutBlocked
	}
	return a
}

// inspectorContext reports occupancy against the model's window, with the
// same thresholds the status bar and the trim warnings use, plus the
// per-round burn behind the sparkline. A session with fewer than two
// rounds reported has no trend, and the row says the number is an estimate
// rather than drawing a flat line.
func (m Model) inspectorContext() *components.InspectorContext {
	b := m.contextAccounting()
	tokens := b.total()
	if tokens <= 0 {
		return nil
	}
	window := m.contextWindow()
	c := components.InspectorContext{
		Pct:       int(tokens * 100 / window),
		Tokens:    tokens,
		Window:    window,
		WarnPct:   warnThresholdPercent,
		AlertPct:  trimThresholdPercent,
		Estimated: b.estimated(),
		Corrected: b.Corrected,
	}
	if m.TotalTokensIn != 0 || m.TotalTokensOut != 0 {
		c.Tokens1 = "↑" + formatTokenCount(m.TotalTokensIn)
		c.Tokens2 = "↓" + formatTokenCount(m.TotalTokensOut)
	}
	c.Burn = m.vitals.series()
	return &c
}

// inspectorSpend splits the cost between this session's own requests and its
// children. The session figure is the ledger's — the agent's turns, the
// permission classifier, the session summary and every child, each priced
// against the model that actually answered it — so the rail's bottom line is
// the whole bill rather than the part of it the main agent ran up.
//
// All four rows are read down the block as shares of one bill, so all four
// are the priced-as-it-went figure. A row re-priced from its token counts
// would charge the input the provider served from its cache at the fresh
// rate, and the block would show a turn costing more than the session it is
// part of (attach.go).
func (m Model) inspectorSpend() *components.InspectorSpend {
	total := m.sessionSpend()
	children := m.childSpend()
	if total.In == 0 && total.Out == 0 && children.In == 0 && children.Out == 0 {
		return nil
	}
	s := components.InspectorSpend{
		Turn:    m.totalsLabel(m.turnSpend()),
		Main:    m.totalsLabel(m.mainSpend()),
		Session: m.totalsLabel(total),
		Model:   m.modelName,
	}
	if children.In != 0 || children.Out != 0 {
		s.Children = m.totalsLabel(children)
	}
	return &s
}

// childSpend is what every sub-agent has cost. The session ledger is the
// answer where there is one: it prices each child against the model that
// child ran on, which a fan-out across several models makes the only
// defensible figure. A session with no ledger sums the children's own bills,
// which are priced the same way — each child keeps a ledger of its own — and
// so is a roll-up rather than a token pair.
func (m Model) childSpend() meter.Totals {
	if m.ledger != nil {
		return m.ledger.SourceTotal(meter.SourceSubagent)
	}
	var t meter.Totals
	if m.subagents != nil {
		for _, st := range m.subagents.Snapshot() {
			t = t.Plus(st.Spend)
		}
	}
	return t
}

// totalsLabel formats a ledger roll-up: the cost it was priced at, or a token
// count where the pricing table knew none of the models involved. It is the
// label for any spend the session priced as it went, because the roll-up
// carries what each request was actually billed — the cache split included —
// and nothing here has to price it a second time.
func (m Model) totalsLabel(t meter.Totals) string {
	if t.In == 0 && t.Out == 0 {
		return ""
	}
	if t.Priced {
		return formatCost(t.Cost)
	}
	return m.freshRateLabel(t.In, t.Out)
}

// WithRailWidth fixes the inspector rail's column count for this session.
// Zero — an unset key, or `/ui rail auto` — leaves it to the width ladder.
func (m Model) WithRailWidth(cols int) Model {
	m.railCols = cols
	return m
}

// inspectorStatus is the /stats-adjacent line describing the split, used by
// /ui to say what the current layout is. It names the rail's width as it
// resolved, not as it was asked for: a number the ladder would not allow at
// this terminal is the whole reason a person reads this line.
func (m Model) inspectorStatus() string {
	if m.twoPane() {
		return fmt.Sprintf("two panes — %d-column transcript + %d-column inspector rail (%s)",
			m.paneWidth(), m.columns().inspector.Dx(), m.railSource())
	}
	return fmt.Sprintf("one pane — %d columns", m.contentWidth())
}

// railSource says where the rail's width came from, in the parenthesis the
// readout ends with: the ladder, the session's own setting, or a setting one
// of the two limits moved. The limits are named apart because they are
// different answers to "why is this not the number I typed" — one of them
// goes away on a wider terminal and the other never does — and a reader who
// typed a number and got a different one is owed which.
func (m Model) railSource() string {
	if m.railCols <= 0 {
		return "auto"
	}
	switch got := m.columns().inspector.Dx(); {
	case got > m.railCols:
		return fmt.Sprintf("set to %d, widened to the narrowest rail there is", m.railCols)
	case got < m.railCols:
		return fmt.Sprintf("set to %d, as wide as this terminal allows", m.railCols)
	}
	return "set"
}

// railCommand handles /ui rail: how many columns the inspector rail takes.
// `auto` hands it back to the width ladder.
func (m *Model) railCommand(parts []string) string {
	if len(parts) == 2 {
		return fmt.Sprintf("Layout: %s.\n%s", m.inspectorStatus(), railUsage)
	}
	if len(parts) != 3 {
		return railUsage
	}
	cols, err := components.ParseRailWidth(parts[2])
	if err != nil {
		return "Error: " + err.Error()
	}
	m.railCols = cols
	m.invalidateRenderCache()
	// A rail that changed width is a transcript that changed width, and
	// nothing else on this path resizes the viewport: without this the feed
	// stays wrapped to the old pane until the next terminal resize, which
	// reads as a command that half worked.
	m.syncViewport()
	if m.contentWidth() < components.InspectorMinContentWidth {
		// Nothing on screen changes at this width, so the reply has to carry
		// the whole answer: the setting took, and the rung is why it is not
		// visible.
		return fmt.Sprintf("Inspector rail %s — this terminal is too narrow to split, so nothing changes until it is %d columns wide.",
			railSetting(cols), components.InspectorMinContentWidth+horizontalPadding*2)
	}
	return fmt.Sprintf("Inspector rail %s — %s.", railSetting(cols), m.inspectorStatus())
}

// railSetting is the setting in the words the reply leads with.
func railSetting(cols int) string {
	if cols <= 0 {
		return "back on the width ladder"
	}
	return fmt.Sprintf("set to %d columns", cols)
}

// railUsage is the one line /ui rail answers with, on its own and when the
// value is not one it takes.
const railUsage = "Usage: /ui rail <auto|columns> — auto widens the rail with the terminal; a number fixes it, held to what the terminal has room for."
