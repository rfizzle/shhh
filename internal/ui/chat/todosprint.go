package chat

// The sprint in the session: the planning card behind `/todo sprint plan`
// and the close a finished run triggers. The set is chosen by the person —
// the session proposes the ready items under the budget and writes nothing
// until the card is accepted, which is the rule `/todo add` already follows
// (docs/capabilities/todo.md#a-session-proposes-you-accept).
//
// Everything else `/todo sprint` does — the view, add, drop, goal, close —
// is textual and lives with the rest of the backlog's verbs, so a session
// and a script give the same answers.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	opts := make([]components.SelectOption, len(proposed))
	slugs := make([]string, len(proposed))
	for i, it := range proposed {
		slugs[i] = it.Slug
		opts[i] = components.SelectOption{Label: it.Slug + " · " + it.Title, Meta: sprintPlanMeta(s, it)}
	}
	card := components.NewMultiSelect(fmt.Sprintf("%s for the sprint, in backlog order — %s toggles, %s all or none, %s writes the sprint, %s writes nothing",
		plural(len(proposed), "ready item"), keys.Shown(keys.Select.Toggle), keys.Shown(keys.Select.All),
		keys.Shown(keys.Select.Take), keys.Shown(keys.Select.Cancel)), opts)
	for i := range card.Checked {
		card.Checked[i] = true
	}
	card.MaxLines = m.maxConfirmPanelHeight()
	// The card is the proposals card: the same surface, the same keys, the
	// same "nothing is written until you accept". todoSprintPlan is what
	// tells the two apart when it closes — set here, nil for proposed
	// items — because the answer is a sprint file rather than new items.
	// Whichever opens the card owns both fields: a card left holding the
	// other's would apply this card's answer to the other's list.
	m.todoPropose = card
	m.todoSprintPlan = slugs
	m.todoProposals = nil
	m.enterSurface(stateTodoPropose)
	m.syncViewport()
	return m, nil
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

// sprintPlanMeta is the card row's right-hand field: why this item is in
// the set, in the facts a filter has. Its priority and size are what put it
// where it is in the order and what the budget spent on it, and what it
// unblocks is what makes taking it now worth more than taking it later.
// It is kept short deliberately — a meta the row cannot fit is dropped
// whole, so a long one would take the reasoning off the widest rows first.
func sprintPlanMeta(s *todo.Store, it todo.Item) string {
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
func (m *Model) writeSprintPlan(slugs []string, accepted []int) string {
	var chosen []string
	for _, i := range accepted {
		if i >= 0 && i < len(slugs) {
			chosen = append(chosen, slugs[i])
		}
	}
	if len(chosen) == 0 {
		return "Nothing checked; no sprint was written."
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
		Goal:    sprintGoalPlaceholder,
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
	b.WriteString("\n/todo sprint goal <text> says what the set is for; it rides in every item's research prompt.")
	return b.String()
}

// sprintGoalPlaceholder is the goal a freshly planned sprint carries until
// somebody writes one. It is a sentence rather than an empty paragraph so
// the file reads as unfinished instead of as a sprint with no purpose, and
// nothing sends it to a model: an unwritten goal is not carried into an
// item's research prompt.
const sprintGoalPlaceholder = "No goal written yet. `/todo sprint goal <text>` says what this set is for."

// sprintGoal is what a run carries into its research stage: the open
// sprint's goal, or nothing at all. The placeholder counts as nothing —
// telling a model the goal has not been written is worse than telling it
// there is no sprint, because it invites the model to invent one.
func (m Model) sprintGoal() string {
	s := m.todoStore
	if s == nil || !s.Sprint.Open() {
		return ""
	}
	goal := strings.TrimSpace(s.Sprint.Goal)
	if goal == sprintGoalPlaceholder {
		return ""
	}
	return goal
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
	to, err := todo.CloseSprintIfDone(m.todos.Root)
	if err != nil {
		return "\nThe sprint could not be closed — " + err.Error()
	}
	if to == "" {
		return ""
	}
	m.reloadTodos()
	return "\nThat was the last item in the sprint; it is closed and archived to " + to + "."
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
