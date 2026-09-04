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
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// startTodoSprintPlan proposes a sprint. The proposal is the ready list in
// backlog order under the budget: a filter, not a recommendation, so what
// it offers is something the reader can recompute from the item headers
// rather than a judgement they have to take on trust.
func (m Model) startTodoSprintPlan(args []string) (tea.Model, tea.Cmd) {
	s := m.todoStore
	if s == nil {
		return m.systemNotice("The backlog is unavailable in this session.")
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
	budget, err := parseSprintPlanArgs(args)
	if err != nil {
		return m.systemNotice("Usage: /todo sprint plan [--size S=n,M=n,L=n] — " + err.Error())
	}
	proposed := s.ProposeSprint(budget)
	if len(proposed) == 0 {
		if budget != nil {
			return m.systemNotice("No ready item fits that budget. /todo sprint plan with no budget offers the whole ready list.")
		}
		return m.systemNotice("Nothing is ready, so there is no set to propose. /todo shows what each item waits on.")
	}
	plan := &components.SprintPlan{
		Budget: sprintBudgetWords(budget),
		Goal:   todo.GoalPlaceholder,
		Rows:   make([]components.SprintPlanRow, len(proposed)),
	}
	for i, it := range proposed {
		plan.Rows[i] = components.SprintPlanRow{
			Slug: it.Slug, Title: it.Title, Note: sprintPlanNote(s, it),
		}
	}
	// The proposal is drawn on the tab it is about, so the reader answering
	// it is looking at the board they are about to fill rather than at a
	// card floating over a transcript.
	return m.openWithPlan(plan)
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
// filtered to. A proposal whose filter is not stated reads as a
// recommendation, and this one is a filter over the ready list.
func sprintBudgetWords(budget todo.SprintBudget) string {
	if len(budget) == 0 {
		return "everything ready"
	}
	var parts []string
	for _, size := range []todo.Size{todo.SizeS, todo.SizeM, todo.SizeL} {
		if n, ok := budget[size]; ok {
			parts = append(parts, fmt.Sprintf("%s=%d", size, n))
		}
	}
	return strings.Join(parts, " ")
}

// parseSprintPlanArgs reads what follows `/todo sprint plan`. An unknown
// word is refused rather than ignored: a mistyped flag that was skipped
// would write a sprint of the whole ready list, which is the answer the
// budget was there to avoid.
func parseSprintPlanArgs(args []string) (todo.SprintBudget, error) {
	spec := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case strings.HasPrefix(a, "--size="):
			spec = strings.TrimPrefix(a, "--size=")
		case a == "--size":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--size needs a budget")
			}
			i++
			spec = args[i]
		default:
			return nil, fmt.Errorf("%q is not a flag this takes", a)
		}
	}
	return todo.ParseSprintBudget(spec)
}

// sprintPlanNote is the card row's reason: why this item is in the set, in
// the facts a filter has. Its priority and size are what put it where it is
// in the order and what the budget spent on it, and what it unblocks is what
// makes taking it now worth more than taking it later.
//
// It is the filter's own account rather than a sentence asked of a model.
// The proposal is deliberately something the reader can recompute from the
// item headers, and a reason nobody can check would turn it into a
// recommendation — which is the one thing this set is not.
func sprintPlanNote(s *todo.Store, it todo.Item) string {
	meta := string(it.Priority) + " · " + sizeOrDash(it.Size)
	if n := s.Unblocks(it.Slug); n > 0 {
		meta += fmt.Sprintf(" · unblocks %d", n)
	}
	return meta
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
	to, err := todo.CloseSprintIfDone(m.todos.Root)
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
