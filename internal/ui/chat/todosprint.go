package chat

// The sprint in the session: the board it is watched on, the planning card
// it is chosen with, and the page a closed one leaves behind. The set is
// chosen by the person — the session proposes the ready items under the
// budget and writes nothing until the card is taken, which is the rule
// `/todo add` already follows
// (docs/capabilities/todo.md#a-session-proposes-you-accept).
//
// Both of those are the backlog screen's sprint tab
// (docs/interface/surfaces.md#the-sprint-board): planning and watching are
// the same two questions about the same set — which items, and where is it —
// so they are one place rather than a card in the transcript and a listing
// behind a command.
//
// Everything else `/todo sprint` does — the view, add, drop, goal, close —
// is textual and lives with the rest of the backlog's verbs, so a session
// and a script give the same answers.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// sprintPlanRequest is a plan that has been asked for and not yet read: the
// budget it was asked for under. It is a request rather than a flag because
// the reading may be several turns away — the candidates are read against
// the tree first — and the budget has to survive them.
type sprintPlanRequest struct {
	budget todo.SprintBudget
}

// todoPlanState is a planning turn while it is going: what it was asked
// for, the candidates it was asked about, and where in the transcript it
// began, so the answer is read off the turn that asked for it.
type todoPlanState struct {
	going      bool
	budget     todo.SprintBudget
	candidates []todo.Item
	turn, mark int
	// prevMode is the session's mode before the turn, restored at the end
	// the way a run and a reading restore it.
	prevMode string
}

// startTodoSprintPlan proposes a sprint. The proposal is a reading rather
// than a sort: the candidates are read against the tree, then grouped by
// what makes a set coherent — a dependency chain, a shared package, a theme
// the titles share, a bug and the story that closes its cause — and every
// candidate left out is named with the word for why.
// See docs/capabilities/todo.md#a-sprint-is-what-ships-together.
func (m Model) startTodoSprintPlan(args []string) (tea.Model, tea.Cmd) {
	s := m.todoStore
	if s == nil {
		return m.systemNotice("The backlog is unavailable in this session.")
	}
	if !m.todos.Profile.Plans() {
		return m.systemNotice(fmt.Sprintf("The %s profile does not plan sets: it says nothing about what makes its items belong together, so a proposal would be a reading of nothing.", m.todos.Profile.Name))
	}
	if s.Sprint.Open() {
		return m.systemNotice(fmt.Sprintf("%s is still open — one sprint at a time. /todo sprint shows it; /todo sprint close ends it.", s.Sprint.Name))
	}
	// A reading in flight lands as its own card whenever it finishes. Two
	// cards cannot hold the surface, and the one that arrived second would
	// be the one the person answers — so the plan waits rather than being
	// replaced by proposals a moment after it is on screen.
	if m.todoExtracting {
		return m.systemNotice("Still reading the session for items — the proposals card opens when it is done, and /todo sprint plan works after it.")
	}
	if note, held := m.planHeld(); held {
		return m.systemNotice(note)
	}
	budget, err := parseSprintPlanArgs(m.todos.Profile, args)
	if err != nil {
		return m.systemNotice(sprintPlanUsage(m.todos.Profile) + " — " + err.Error())
	}
	candidates := s.Ready()
	if len(candidates) == 0 {
		return m.systemNotice("Nothing is ready, so there is no set to propose. /todo shows what each item waits on.")
	}
	if !budget.Fits(candidates) {
		return m.systemNotice("No ready item fits that budget. /todo sprint plan with no budget reads the whole ready list.")
	}
	// The candidates are read against the tree before they are grouped. A
	// recommendation over items that state what the code did last month
	// recommends the wrong week, and the readings are also what tell the
	// planner an item is already done or has grown — so it reads them
	// rather than reading the tree twice. A profile that plans its sets
	// without reading its items goes straight to the proposal: there is no
	// reading to take, and queueing one would send a turn with nothing in
	// it to instruct the turn.
	if unread := m.planReadingsNeeded(candidates); m.todos.Profile.Grooms() && len(unread) > 0 {
		m.todoGroomer = todoGroomState{
			queue: unread, prevMode: m.policy.mode.String(), stale: m.todoGroomer.stale,
			planAfter: &sprintPlanRequest{budget: budget},
		}
		next, _ := m.systemNotice(fmt.Sprintf("Reading %s against the tree first; the proposal comes after the readings.", plural(len(unread), "item")))
		return next.(Model).groomNext()
	}
	return m.startSprintPlanTurn(budget)
}

// planHeld is why a plan cannot start now. The reading is a turn, and the
// rules a run and a grooming pass keep about turn boundaries are this one's
// for the same reason: a turn started under another turn is not this one's
// to grade.
func (m Model) planHeld() (string, bool) {
	switch {
	case m.todoPlanner.going:
		return "A sprint is already being planned; the card opens when the turn is over.", true
	case m.todoGroomer.going():
		return fmt.Sprintf("A backlog item is being read against the tree (%s); the plan waits for the reading.", m.todoGroomer.slug), true
	case m.todoRunner.state != nil && !m.todoRunner.state.Over():
		st := m.todoRunner.state
		return fmt.Sprintf("A run is going (%s · %s); /todo stop ends it, and planning reads the files that run is working from.", st.Slug, st.Stage), true
	case m.turnState() != stateInput || m.working():
		return "A plan starts from an idle session; this turn has to finish first.", true
	}
	return "", false
}

// planReadingsNeeded is which candidates the planner has no reading of that
// still stands: one nobody has read, one read before the person last edited
// it, and one whose reading has fallen far enough behind the tree that the
// surfaces already say so.
func (m Model) planReadingsNeeded(candidates []todo.Item) []string {
	var out []string
	for _, it := range candidates {
		if _, ok := todo.LoadReading(m.todos.Root, it.Slug); ok && m.groomStaleNote(it.Slug) == "" {
			continue
		}
		out = append(out, it.Slug)
	}
	return out
}

// startSprintPlanTurn sends the planning turn. It runs in plan mode, like
// the reading before it: the whole pass changes nothing until the card is
// taken.
func (m Model) startSprintPlanTurn(budget todo.SprintBudget) (tea.Model, tea.Cmd) {
	m.reloadTodos()
	s := m.todoStore
	candidates := s.Ready()
	if len(candidates) == 0 {
		return m.systemNotice("Nothing is ready, so there is no set to propose. /todo shows what each item waits on.")
	}
	m.todoPlanner = todoPlanState{
		going: true, budget: budget, candidates: candidates,
		turn: int(m.turnCount) + 1, mark: len(m.transcript), prevMode: m.policy.mode.String(),
	}
	m.applyMode(agent.ModePlan)
	return m.sendUserMessageAs(s.PlanPrompt(candidates, budget.String()), "plan the sprint")
}

// todoPlanAfter is the turn-end hook, read the way the runner's and the
// reading's are: a planning turn ending is a transition, and no one handler
// could be trusted to send it.
func (m Model) todoPlanAfter(prev Model) (Model, tea.Cmd) {
	p := m.todoPlanner
	if !p.going || !prev.working() || m.working() {
		return m, nil
	}
	if m.turnState() != stateInput || m.pausedAtRoundLimit() || m.heldAtBoundary() {
		return m, nil
	}
	if int(m.turnCount) != p.turn {
		// The turn that ended is not the plan's — a compaction, a skill,
		// something a command started. Grading an answer that is not its
		// own would propose a set nobody asked for.
		next, cmd := m.endSprintPlan("The planning turn was displaced by another message.")
		return next.(Model), cmd
	}
	plan := todo.ParsePlan(m.todos.Profile, m.planAnswer(), p.candidates, p.budget)
	if len(plan.Items) == 0 {
		next, cmd := m.endSprintPlan("The reading proposed no set that could be read as items; nothing was written.")
		return next.(Model), cmd
	}
	next, cmd := m.openPlanCard(plan)
	return next.(Model), cmd
}

// planAnswer is the assistant's last message since the planning turn began.
func (m Model) planAnswer() string {
	for i := len(m.transcript) - 1; i >= m.todoPlanner.mark && i >= 0; i-- {
		if e := m.transcript[i]; e.kind == entryAssistant {
			return e.text
		}
	}
	return ""
}

// endSprintPlan closes the planning turn and puts the mode back.
func (m Model) endSprintPlan(why string) (tea.Model, tea.Cmd) {
	if prev, err := agent.ParseMode(m.todoPlanner.prevMode); err == nil {
		m.applyMode(prev)
	}
	m.todoPlanner = todoPlanState{}
	return m.systemNotice(why)
}

// openPlanCard puts the reading up as the proposal to answer, and records
// that a set of that size was recommended. The record holds what was
// recommended beside what was accepted and what shipped, which is the one
// way to tell a planner that reads the backlog from one that agrees with
// whoever is holding the keyboard.
func (m Model) openPlanCard(plan todo.Plan) (tea.Model, tea.Cmd) {
	if prev, err := agent.ParseMode(m.todoPlanner.prevMode); err == nil {
		m.applyMode(prev)
	}
	budget := m.todoPlanner.budget
	titles := map[string]string{}
	for _, it := range m.todoPlanner.candidates {
		titles[it.Slug] = it.Title
	}
	m.todoPlanner = todoPlanState{}
	m.signal(observe.SignalTodo, observe.PlanReason(len(plan.Items)))
	card := &components.SprintPlan{
		Budget:  sprintBudgetWords(budget),
		Goal:    sprintCardGoal(plan),
		Release: plan.ReleaseLine(),
		Rows:    make([]components.SprintPlanRow, len(plan.Items)),
	}
	for i, it := range plan.Items {
		card.Rows[i] = components.SprintPlanRow{Slug: it.Slug, Title: titles[it.Slug], Note: it.Why}
	}
	for _, l := range plan.Left {
		card.Left = append(card.Left, components.SprintPlanOut{
			Slug: l.Slug, Title: titles[l.Slug], Why: string(l.Why),
		})
	}
	// The proposal is drawn on the tab it is about, so the reader answering
	// it is looking at the board they are about to fill rather than at a
	// card floating over a transcript.
	return m.openWithPlan(card)
}

// sprintCardGoal is the sentence the card offers: the one the reading
// wrote, or the placeholder where it wrote none.
func sprintCardGoal(plan todo.Plan) string {
	if goal := strings.TrimSpace(plan.Goal); goal != "" {
		return goal
	}
	return todo.GoalPlaceholder
}

// sprintFileGoal is what the accepted card writes into the sprint file: the
// sentence, then the release line under it. The two are held apart on the
// card so that rewriting the sentence does not throw the reading's
// judgement away with it, and they are joined only here.
//
// A goal nobody has written takes no release line: the placeholder is what
// "nobody has said what this set is for" reads as, and a judgement about a
// set nobody has described would be the only sentence in the file.
func sprintFileGoal(p *components.SprintPlan) string {
	goal := strings.TrimSpace(p.Goal)
	if goal == "" || goal == sprintGoalPlaceholder || p.Release == "" {
		return goal
	}
	return goal + "\n\n" + p.Release
}

// dropTodoPlan retires a planning turn in flight, so a card cannot come up
// for a conversation that is gone — and the mode goes back with it, the way
// a reading let go of at the boundary puts it back.
func (m *Model) dropTodoPlan() {
	if !m.todoPlanner.going {
		return
	}
	if prev, err := agent.ParseMode(m.todoPlanner.prevMode); err == nil {
		m.applyMode(prev)
	}
	m.todoPlanner = todoPlanState{}
}

// openWithPlan puts the screen up with the proposal on its sprint tab. The
// plan is kept only where the screen actually opened: a proposal held by a
// session with nowhere to draw it is one no key could ever answer.
func (m Model) openWithPlan(plan *components.SprintPlan) (tea.Model, tea.Cmd) {
	next, cmd := m.openTodoScreen()
	model := next.(Model)
	if model.backlog == nil {
		return model, cmd
	}
	model.sprintPlan, model.backlog.Plan = plan, plan
	return model, cmd
}

// sprintGoalCommand is `/todo sprint goal <text>` while a proposal is in
// flight: the goal goes on the plan and the card comes back up, because
// there is no file to write it to until the card is taken. The same command
// over an open sprint is a line edit on the file and goes through the
// manager with every other verb.
func (m Model) sprintGoalCommand(goal string) (tea.Model, tea.Cmd) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return m.systemNotice("Usage: " + strings.TrimSpace(sprintGoalPrefix) + " <what the set is for>")
	}
	plan := m.sprintPlan
	plan.Goal = goal
	return m.openWithPlan(plan)
}

// sprintBudgetWords is what the plan card's header says the proposal was
// bounded by. The set is a judgement, and the budget is the one part of it
// the reader can check without reading the items.
func sprintBudgetWords(budget todo.SprintBudget) string {
	if len(budget) == 0 {
		return "everything ready"
	}
	return budget.String()
}

// parseSprintPlanArgs reads what follows `/todo sprint plan`. An unknown
// word is refused rather than ignored: a mistyped flag that was skipped
// would write a sprint of the whole ready list, which is the answer the
// budget was there to avoid.
func parseSprintPlanArgs(profile todo.Profile, args []string) (todo.SprintBudget, error) {
	spec := ""
	// The one flag is named for the profile's grading field, the way the
	// command's is: a backlog nobody grades is offered no budget, so every
	// word after the verb is a word it does not take.
	name, _, graded := todo.BudgetFlag(profile)
	flag := "--" + name
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case graded && strings.HasPrefix(a, flag+"="):
			spec = strings.TrimPrefix(a, flag+"=")
		case graded && a == flag:
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s needs a budget", flag)
			}
			i++
			spec = args[i]
		default:
			return nil, fmt.Errorf("%q is not a flag this takes", a)
		}
	}
	return todo.ParseSprintBudget(profile, spec)
}

// sprintPlanUsage is the verb's own line, with the budget flag the profile
// gives it or nothing at all where it grades no work.
func sprintPlanUsage(profile todo.Profile) string {
	name, shape, graded := todo.BudgetFlag(profile)
	if !graded {
		return "Usage: /todo sprint plan"
	}
	return fmt.Sprintf("Usage: /todo sprint plan [--%s %s]", name, shape)
}

// writeSprintPlan writes the accepted set as the sprint file, in the order
// it was proposed. The set's own order is the file's, and the file is
// where it is changed afterwards: /todo sprint add and drop are one line
// each, and reordering is what an editor is for.
func (m *Model) writeSprintPlan(chosen []string, goal string) string {
	if len(chosen) == 0 {
		return "Nothing was kept; no sprint was written."
	}
	created := time.Now().Format("2006-01-02")
	sp := todo.Sprint{
		// The date names the sprint because a set chosen in one sitting is
		// most often looked for by when it was chosen, and a name typed at
		// the card would be one more thing to answer before any of it is
		// on screen. `/todo sprint goal` says what it is for; the header's
		// name line is a line like any other to change.
		Name:    freeSprintName(m.todos.Root, todo.Slugify(created)),
		Status:  todo.SprintOpen,
		Created: created,
		Session: m.sessionName,
		Goal:    goal,
		Slugs:   chosen,
	}
	path, err := todo.CreateSprint(m.todos.Root, sp)
	if err != nil {
		return "The sprint could not be written — " + err.Error()
	}
	m.reloadTodos()
	var b strings.Builder
	fmt.Fprintf(&b, "Wrote %s to %s: %s in this order.", sp.Name, path, plural(len(chosen), "item"))
	for _, slug := range chosen {
		b.WriteString("\n  " + slug)
	}
	if goal == sprintGoalPlaceholder {
		b.WriteString("\n" + strings.TrimSpace(sprintGoalPrefix) + " <text> says what the set is for; it rides in every item's research prompt.")
	}
	return b.String()
}

// sprintGoalPlaceholder is the goal a freshly planned sprint is written with;
// the file itself defines what it means (todo.GoalPlaceholder).
const sprintGoalPlaceholder = todo.GoalPlaceholder

// sprintGoal is what a run carries into its research stage: the open
// sprint's goal, or nothing at all.
func (m Model) sprintGoal() string {
	s := m.todoStore
	if s == nil {
		return ""
	}
	return s.Sprint.Purpose()
}

// freeSprintName is the first name in the date's series the archive does
// not already hold. Two sprints planned on one day would otherwise share a
// name, and the second could never be filed under it.
func freeSprintName(root, base string) string {
	if !todo.SprintNameTaken(root, base) {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s-%d", base, n)
		if !todo.SprintNameTaken(root, name) {
			return name
		}
	}
}

// closeFinishedSprint archives the sprint when the item just archived was
// the last one it named, and says so. It answers "" when there was nothing
// to close, which is every archive outside a sprint.
func (m *Model) closeFinishedSprint() string {
	// The item that finished was archived on the filesystem a line ago and
	// this session's copy of the backlog does not know it yet, so the set
	// is read from disk before it is read out. A page built from the stale
	// copy reports the very item that closed the sprint as unfinished, and
	// leaves its report off the page.
	m.reloadTodos()
	// Then the set is captured before it is closed, because closing renames
	// the file out from under every reader of it — the page is written from
	// what the sprint said at the moment it stopped being a plan.
	sp, entries := m.sprintAsClosed()
	to, err := todo.CloseSprintIfDone(m.todos.Profile, m.todos.Root)
	if err != nil {
		return "\nThe sprint could not be closed — " + err.Error()
	}
	if to == "" {
		return ""
	}
	m.reloadTodos()
	turns, cost := m.sprintSpendNow()
	return "\nThat was the last item in the sprint; it is closed and archived to " + to + "." +
		m.sprintReportPage(sp, entries, turns, cost)
}

// closeSprintCommand is `/todo sprint close` from the session. The verb
// itself is the manager's, so the words and the refusals are the ones a
// script gets; what the session adds is the page, which needs the set as it
// stood and a publisher only a session has.
func (m Model) closeSprintCommand() (tea.Model, tea.Cmd) {
	m.reloadTodos()
	sp, entries := m.sprintAsClosed()
	turns, cost := m.sprintSpendNow()
	note := m.todos.Manage([]string{"sprint", "close"})
	m.reloadTodos()
	if m.todoStore.Sprint == nil {
		note += m.sprintReportPage(sp, entries, turns, cost)
	}
	m.refreshTodoScreen()
	return m.systemNotice(note)
}

// sprintAsClosed is the sprint and its entries as they stand, for the page
// written about them once they are gone.
func (m Model) sprintAsClosed() (*todo.Sprint, []todo.SprintEntry) {
	if m.todoStore == nil {
		return nil, nil
	}
	return m.todoStore.Sprint, m.todoStore.SprintEntries()
}

// sprintSpendNow is what the set has cost, from the checkpoint where one is
// still there and from this session alone where it is not — a sprint that
// ended keeps no checkpoint, and the session that closed it is the only
// account of the last item left.
func (m Model) sprintSpendNow() (int, float64) {
	if sp, live := run.Live(m.todos.Root); live {
		return m.sprintSpend(sp)
	}
	return int(m.turnCount), m.sessionSpend().Cost
}

// inspectorSprint is the sprint row above the backlog list: the set's name
// and how much of it is done. It is nil without an open sprint, so the row
// is absent rather than empty.
func (m Model) inspectorSprint() (name string, done, total int) {
	s := m.todoStore
	if s == nil || !s.Sprint.Open() {
		return "", 0, 0
	}
	done, total = s.SprintProgress()
	return s.Sprint.Name, done, total
}

// closedSprint is the sprint this session closed: its name, and the page it
// wrote. It is kept for the session because the file is gone the moment it
// closes — it is renamed into the archive — and the board would otherwise
// vanish along with the one row offering the report of what the set did.
type closedSprint struct {
	name   string
	goal   string
	report string
}

// sprintBoard is the sprint tab as the screen draws it: the goal, the set's
// items in the file's order with where each one stands, how far through it
// is, what it has cost, and how it stopped. nil is a project with no sprint
// and none closed here, which is a tab that is absent rather than empty.
func (m Model) sprintBoard() *components.SprintBoard {
	s := m.todoStore
	if s == nil {
		return nil
	}
	if s.Sprint.Open() {
		return m.openSprintBoard(s)
	}
	if c := m.sprintClosed; c != nil {
		// A closed sprint is a record, so its board is the goal and the
		// page and nothing that reads as still to do.
		return &components.SprintBoard{
			Name: c.name, Goal: c.goal, Closed: true, Report: c.report,
		}
	}
	return nil
}

// openSprintBoard is the board over the sprint file that is open.
func (m Model) openSprintBoard(s *todo.Store) *components.SprintBoard {
	done, total := s.SprintProgress()
	board := &components.SprintBoard{
		Name: s.Sprint.Name, Goal: s.Sprint.Goal, Done: done, Total: total,
	}
	entries := s.SprintEntries()
	for _, e := range entries {
		board.Rows = append(board.Rows, m.sprintBoardRow(s, e))
	}
	// A sprint stops on the first block and attempts nothing after it, so
	// the block belongs on the board rather than only in the transcript of
	// the session that hit it — the reader who comes back to a stopped set
	// is asking what stopped it.
	for _, e := range entries {
		if e.State != todo.SprintItemBlocked {
			continue
		}
		board.Stopped = e.Slug + " blocked"
		if why := firstLine(todo.ItemBlock(e.Item)); why != "" {
			board.Stopped += " — " + why
		}
		break
	}
	if sp, live := run.Live(m.todos.Root); live {
		board.Spend = sprintSpendWords(m.sprintSpend(sp))
		if next, ok := sp.Peek(s); ok {
			board.Next = next.Slug
		}
	}
	if c := m.sprintClosed; c != nil && c.report != "" && c.name == s.Sprint.Name {
		board.Report = c.report
	}
	return board
}

// sprintBoardRow is one of the set's slugs as a row. It is the item's own
// row with the sprint's reading of it in the note field: where a slug
// stands in the set is not the same question as its status in the backlog,
// and the one running says which stage it is at.
func (m Model) sprintBoardRow(s *todo.Store, e todo.SprintEntry) components.BacklogRow {
	if e.State == todo.SprintItemDropped {
		// The slug names nothing, so there is no item to draw and no verb
		// that would work on it. It wears the glyph of an item that needs a
		// person, because that is what it needs: the sprint file still says
		// this is part of the set.
		return components.BacklogRow{
			Slug: e.Slug, State: components.BacklogBlocked, Note: "dropped from the backlog",
			Body: "The backlog no longer holds this item, and the sprint still names it.\n\n" +
				"`/todo sprint drop " + e.Slug + "` takes it out of the set.",
		}
	}
	row := m.todoScreenRow(s, e.Item)
	row.Note = string(e.State)
	if e.State == todo.SprintItemWaiting && len(e.Waiting) > 0 {
		row.Note = "waits on " + e.Waiting[0]
		if rest := len(e.Waiting) - 1; rest > 0 {
			row.Note += fmt.Sprintf(" +%d", rest)
		}
	}
	// The one item being worked says which stage it is at. A slug that only
	// says "in progress" tells the reader a sprint is going and not whether
	// it is moving, which is the question a board is opened to answer.
	if st := m.todoRunner.state; st.Sprinting() && !st.Over() && st.Slug == e.Slug {
		row.Note = string(st.Stage)
	}
	return row
}

// sprintSpend is what the set has cost so far: the checkpoint's running
// total, plus what this session has spent on the item in flight. The two
// halves are separate because the ledger is reset at every session
// boundary, and a sprint crosses one between each pair of items.
func (m Model) sprintSpend(sp *run.Sprint) (turns int, cost float64) {
	return sp.Turns + int(m.turnCount), sp.Cost + m.sessionSpend().Cost
}

// sprintSpendWords is the spend as the board says it. A set that has spent
// nothing yet says nothing rather than "0 turns · $0.0000", because a board
// opened before the first item started is not reporting a free sprint.
func sprintSpendWords(turns int, cost float64) string {
	if turns == 0 && cost == 0 {
		return ""
	}
	words := plural(turns, "turn")
	if cost > 0 {
		words += " · " + formatCost(cost)
	}
	return words
}

// sprintTitle is what the terminal's tab says while a sprint is working:
// how far through the set it is and which item it is on. A reader with
// eight windows open is asking exactly those two things of the one they
// left running, and the tab is where they see it from the next window over.
func (m Model) sprintTitle() string {
	st := m.todoRunner.state
	if !st.Sprinting() || st.Over() {
		return ""
	}
	done, total := m.todoStore.SprintProgress()
	if total == 0 {
		// A sprint over the whole ready list has no file to count against,
		// so the cap is the only denominator there is. Without one the tab
		// says which item and no ratio: a count with an invented total is
		// worse than no count.
		if sp, live := run.Live(m.todos.Root); live {
			done, total = len(sp.Done), sp.Max
		}
	}
	if total > 0 {
		return fmt.Sprintf("sprint %d/%d · %s", done, total, st.Slug)
	}
	return "sprint · " + st.Slug
}

// sprintReportPage publishes the closed sprint as a report page and answers
// the line naming it, or "" where this session cannot publish one.
//
// It is the first page shhh writes for the person rather than the model, and
// it is the same blocks the model's own pages are made of: a set's report
// has a stat band, a table of its items and the prose of what stopped it,
// and inventing a second vocabulary for it would give the product two report
// designs to keep in step.
func (m *Model) sprintReportPage(sp *todo.Sprint, entries []todo.SprintEntry, turns int, cost float64) string {
	if m.todos.PublishReport == nil || sp == nil {
		return ""
	}
	url, err := m.todos.PublishReport(sprintReportDoc(sp, entries, turns, cost))
	if err != nil {
		return "\nThe sprint's report page could not be written — " + err.Error()
	}
	m.sprintClosed = &closedSprint{name: sp.Name, goal: sp.Goal, report: url}
	return "\nThe set's report is a page: " + url
}

// sprintReportDoc is the closed sprint as a report document: what the set
// was for, what it cost, every item with what it produced, and what it did
// not finish.
func sprintReportDoc(sp *todo.Sprint, entries []todo.SprintEntry, turns int, cost float64) reports.Document {
	doc := reports.Document{Title: "Sprint " + sp.Name}
	if goal := strings.TrimSpace(sp.Goal); goal != "" && goal != todo.GoalPlaceholder {
		doc.Blocks = append(doc.Blocks, reports.Block{
			Type: reports.BlockProse, Heading: "Goal", Text: goal,
		})
	}
	done := 0
	for _, e := range entries {
		if e.State == todo.SprintItemDone {
			done++
		}
	}
	stats := []reports.Stat{
		{Label: "Items done", Value: fmt.Sprintf("%d", done), Delta: fmt.Sprintf("of %d in the set", len(entries))},
		{Label: "Turns", Value: fmt.Sprintf("%d", turns)},
	}
	if cost > 0 {
		stats = append(stats, reports.Stat{Label: "Spend", Value: formatCost(cost)})
	}
	doc.Blocks = append(doc.Blocks, reports.Block{Type: reports.BlockStats, Stats: stats})
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{e.Slug, string(e.State), sprintItemOutcome(e)})
	}
	if len(rows) > 0 {
		doc.Blocks = append(doc.Blocks, reports.Block{
			Type: reports.BlockTable, Heading: "Items",
			Columns: []string{"item", "state", "what it produced"}, Rows: rows,
		})
	}
	// The notes are the page's second block because they are what the page
	// is opened for: after a set closes the person's next act is a tag, and
	// what a tag message wants is this list, not the table above it or the
	// reports below.
	doc.Blocks = append(doc.Blocks, reports.Block{
		Type: reports.BlockProse, Heading: "Notes", Text: todo.SprintNotes(sp, entries),
	})
	// The table's cell is one line because a cell is, and a run's report
	// names its commit in prose further down — so the reports go on the
	// page whole as well. It is one block rather than one per item: a set
	// of thirty would otherwise be thirty blocks against a page's cap.
	if work := sprintReportWork(entries); work != "" {
		doc.Blocks = append(doc.Blocks, reports.Block{
			Type: reports.BlockProse, Heading: "What each item produced", Text: work,
		})
	}
	if blocks := sprintReportBlocks(entries); blocks != "" {
		doc.Blocks = append(doc.Blocks, reports.Block{
			Type: reports.BlockProse, Heading: "What stopped", Text: blocks,
		})
	}
	return doc
}

// sprintItemOutcome is an item's cell in the report's table: what the run
// wrote about it, or why there is nothing to write. The report is copied
// into the page rather than pointed at, for the reason the archived sprint
// file copies it — an archived item can be edited afterwards, and what the
// set produced is what it produced at the time.
func sprintItemOutcome(e todo.SprintEntry) string {
	switch e.State {
	case todo.SprintItemDropped:
		return "dropped from the backlog before the sprint closed"
	case todo.SprintItemDone:
		if report := firstLine(todo.ItemReport(e.Item)); report != "" {
			return report
		}
		return "archived with no report"
	}
	return "not finished"
}

// sprintReportWork is the page's prose about what the set produced: every
// finished item with the report its run wrote, which is where the commit
// it made is named.
func sprintReportWork(entries []todo.SprintEntry) string {
	var parts []string
	for _, e := range entries {
		if e.State != todo.SprintItemDone {
			continue
		}
		if report := todo.ItemReport(e.Item); report != "" {
			parts = append(parts, e.Slug+" — "+report)
		}
	}
	return strings.Join(parts, "\n\n")
}

// sprintReportBlocks is the page's prose about what the set did not get
// through: each blocked item with the evidence the run left on it.
func sprintReportBlocks(entries []todo.SprintEntry) string {
	var parts []string
	for _, e := range entries {
		if e.State != todo.SprintItemBlocked {
			continue
		}
		why := todo.ItemBlock(e.Item)
		if why == "" {
			why = "blocked with no evidence written."
		}
		parts = append(parts, e.Slug+" — "+why)
	}
	return strings.Join(parts, "\n\n")
}

// sprintGoalPrefix is what the plan card's goal key leaves in the draft
// box. It is the verb the goal is written with everywhere else, so the key
// teaches the command rather than hiding it.
const sprintGoalPrefix = "/todo sprint goal "

// composeSprintGoal is what the plan card's `[g]` leaves behind: the
// command in the draft box with the cursor after it, waiting for the
// sentence. A draft already in the box is not thrown away for it.
func (m Model) composeSprintGoal() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.input.Value()) != "" {
		return m.systemNotice("There is a draft in the input; " + strings.TrimSpace(sprintGoalPrefix) +
			" <text> writes the goal once it is sent or cleared.")
	}
	m.input.SetValue(sprintGoalPrefix)
	m.input.MoveToEnd()
	m.syncCompletions()
	m.syncViewport()
	return m, nil
}
