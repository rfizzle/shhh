package cli

// Backlog wiring: `shhh todo` prints the project's backlog the way a session
// here would read it — ready items first, then what each waiting item is
// waiting on, then the files that could not be read.
//
// The verbs are registered twice over one implementation. A second terminal,
// a script and a CI job all reach the backlog through the command, and the
// session reaches it through `/todo`; both go through todoVerb, so a refusal
// is the same refusal and a confirmation is the same sentence. Two
// implementations of "archive an item" would be two answers to the question
// of what archiving is, and only one of them would be tested.
// See docs/capabilities/todo.md#from-outside-the-session.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/markdown"
	"github.com/spf13/cobra"
)

// loadTodos reads the backlog of the checkout dir is in.
func loadTodos(dir string) *todo.Store {
	return todo.Load(todoProfile(), todo.Root(dir))
}

// todoCwd is the directory `shhh todo` reads the backlog of. An unreadable
// working directory falls back to ".", which is the same directory under
// another name.
func todoCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// todoShow prints one item, or says which slug it could not find.
//
// On a terminal the body goes through the renderer a session lays prose out
// with — headings, lists and fences drawn rather than left as marks —
// because somebody is reading it. Redirected, it is the file byte for byte:
// what a script asked `show` for is the item, and a rendering of prose is
// not one, so `shhh todo show x > x.md` gives back a file the backlog would
// load again.
//
// It is separate from the command so the answer can be asked of a stated
// checkout rather than of wherever the process is standing.
func todoShow(w io.Writer, dir, slug string) error {
	s := loadTodos(dir)
	it, ok := s.Find(slug)
	if !ok {
		return fmt.Errorf("no backlog item %q; `shhh todo` lists them", slug)
	}
	if !todoTerminal(w) {
		data, err := os.ReadFile(it.Path)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := report.Fprint(w, todoItemReport(s, it, false)); err != nil {
		return err
	}
	body := strings.TrimSpace(it.Body)
	if body == "" {
		return nil
	}
	_, err := fmt.Fprintf(w, "\n%s\n", markdown.Render(body, markdown.Options{
		Width: report.Width(w), Mono: report.Mono(w),
	}))
	return err
}

// todoTerminal reports that output is going to a terminal rather than to a
// pipe, a file or a test's buffer. It is asked of the writer the command was
// given and not of stdout, because a command whose listing is redirected is
// writing for a script whatever its own stdout is attached to.
func todoTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}

// todoList prints the backlog of the checkout dir is in.
func todoList(w io.Writer, dir string) error {
	return report.Fprint(w, todoReport(loadTodos(dir)))
}

// todoListing is the `shhh todo` output: one row per active item in backlog
// order, marked ready or naming what it waits on, then warnings and
// diagnostics. Ready is stated per row rather than as a separate list so a
// blocked item and a waiting item are told apart where they sit.
func todoListing(s *todo.Store) string { return todoReport(s).String() }

// todoReport is the backlog as a report. A file that would not load is a note
// rather than a line after the listing: it is why an item the reader expected
// is not on the screen.
func todoReport(s *todo.Store) report.Report {
	r := report.Report{Title: "shhh todo"}
	if s.Len() == 0 && len(s.Diagnostics) == 0 {
		// The way out is short enough to survive a narrow terminal, and the
		// directory it resolves to goes on the line under it: a way out that
		// clips is a way out the reader cannot take.
		empty := report.Empty("no backlog here", "write .shhh/todo/<name>.md with a --- header")
		empty.Body = []string{todo.Dir(s.Root)}
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{empty}})
		return r
	}
	ready := fmt.Sprintf("%d ready", len(s.Ready()))
	// With a sprint open, "ready" is the sprint's ready set and not the
	// backlog's, and rows outside the sprint still read as ready on their
	// own terms. Naming the sprint here is what stops the two counts from
	// looking like a contradiction.
	if s.Sprint.Open() {
		ready += " in " + s.Sprint.Name
	}
	tally := []string{ready}
	if n := s.Count(todo.StatusBlocked); n > 0 {
		tally = append(tally, fmt.Sprintf("%d blocked", n))
	}
	if n := len(s.Done); n > 0 {
		tally = append(tally, fmt.Sprintf("%d archived", n))
	}
	r.Subject = countOf(s.Len(), "item", "items")
	r.Tally = strings.Join(tally, " · ")

	rows := make([]report.Row, 0, s.Len())
	for _, it := range s.Items {
		rows = append(rows, todoRow(s, it))
		r.Notes = append(r.Notes, todoItemNotes(it)...)
	}
	if len(rows) > 0 {
		r.Sections = []report.Section{{Rows: rows}}
	}
	for _, d := range s.Diagnostics {
		r.Notes = append(r.Notes, report.Note{State: report.Fail, Text: d})
	}
	return r
}

// todoRow is one item as every listing draws it. The narrower verbs share it
// with the whole backlog so that an item cannot read one way under `ready`
// and another way under `shhh todo`.
func todoRow(s *todo.Store, it todo.Item) report.Row {
	return report.Row{
		State:   todoRowState(s, it),
		Name:    it.Slug,
		Subject: clipRunes(it.Title, 72),
		Detail:  joinDetail(todoFieldDetail(it), joinDetail(string(it.Priority), it.Grade())),
		Outcome: todoState(s, it),
	}
}

// todoItemNotes are an item's warnings as report notes, each naming the file
// to fix: a warning about a header line is only actionable beside the path.
func todoItemNotes(it todo.Item) []report.Note {
	notes := make([]report.Note, 0, len(it.Warnings))
	for _, w := range it.Warnings {
		notes = append(notes, report.Note{State: report.Warn, Text: it.Path + ": " + w})
	}
	return notes
}

// todoSetReport is part of the backlog as a listing — the ready set, the next
// item — in the rows the whole backlog draws. An empty set says nothing is
// ready rather than that there is no backlog: they are different answers and
// the way out of each is a different one.
func todoSetReport(s *todo.Store, title string, items []todo.Item) report.Report {
	r := report.Report{Title: title}
	if len(items) == 0 {
		// No tally beside the title: "0 items" over a row that already says
		// nothing is ready is the same fact twice, and the row is the one
		// carrying the way out.
		return emptyInto(r, "nothing is ready", "`shhh todo` says what each open item waits on")
	}
	r.Subject = countOf(len(items), "item", "items")
	rows := make([]report.Row, 0, len(items))
	for _, it := range items {
		rows = append(rows, todoRow(s, it))
		r.Notes = append(r.Notes, todoItemNotes(it)...)
	}
	r.Sections = []report.Section{{Rows: rows}}
	return r
}

// todoNext is the next ready item as a set of at most one, so the `next`
// verb draws and serialises through the same two functions `ready` does.
func todoNext(s *todo.Store) []todo.Item {
	if it, ok := s.Next(); ok {
		return []todo.Item{it}
	}
	return nil
}

// todoRowState is the glyph an item wears: what it is waiting on is the whole
// reason to scan this listing, so a blocked item and one waiting on a
// dependency do not look like one that could be started now.
func todoRowState(s *todo.Store, it todo.Item) report.State {
	switch {
	case it.Status == todo.StatusBlocked:
		return report.Fail
	case it.Status == todo.StatusInProgress:
		return report.Run
	case it.Status != todo.StatusOpen:
		return report.Pass
	case len(s.Waiting(it)) > 0:
		return report.Skip
	}
	return report.Queue
}

// todoFieldDetail is the item's header fields other than the priority and
// the grade, which the row states either side of it. An item that declared
// none says nothing rather than a dash.
func todoFieldDetail(it todo.Item) string {
	detail := ""
	for _, f := range it.Profile.Fields {
		if f.Orders() || f.Name == it.Profile.Grade {
			continue
		}
		detail = joinDetail(detail, it.Fields[f.Name])
	}
	return detail
}

// todoState is the row's state column: the status, or for an open item
// whether it is ready and if not what it waits on.
func todoState(s *todo.Store, it todo.Item) string {
	if it.Status != todo.StatusOpen {
		return string(it.Status)
	}
	if waiting := s.Waiting(it); len(waiting) > 0 {
		return "waits on " + strings.Join(waiting, ", ")
	}
	return "ready"
}

// todoSprintReport is the sprint as a report: the goal, then each slug in
// the file's order with where it stands, then the count. A slug the
// backlog no longer holds and one waiting on a dependency both say so on
// their row — the whole reason to read this is which of the set can move.
func todoSprintReport(s *todo.Store) report.Report {
	r := report.Report{Title: "shhh todo sprint"}
	sp := s.Sprint
	if sp == nil {
		empty := report.Empty("no sprint here", "/todo sprint plan proposes one from the ready items")
		empty.Body = []string{todo.SprintPath(s.Root)}
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{empty}})
		return r
	}
	r.Subject = sp.Name
	entries := s.SprintEntries()
	done, total := s.SprintProgress()
	if sp.Goal != "" {
		r.Sections = append(r.Sections, report.Section{Header: "GOAL", Body: sp.Goal})
	}
	rows := make([]report.Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, report.Row{
			State:   todoSprintRowState(e),
			Name:    e.Slug,
			Subject: clipRunes(e.Item.Title, 72),
			Detail:  joinDetail(string(e.Item.Priority), e.Item.Grade()),
			Outcome: todoSprintOutcome(e),
		})
	}
	if len(rows) > 0 {
		r.Sections = append(r.Sections, report.Section{Rows: rows})
	}
	for _, w := range sp.Warnings {
		r.Notes = append(r.Notes, report.Note{State: report.Warn, Text: sp.Path + ": " + w})
	}
	r.Tally = fmt.Sprintf("%d of %d done · %s", done, total, sp.Status)
	return r
}

// todoSprintRowState is the glyph a sprint row wears. A slug that is
// waiting is skipped rather than failed: the run passes over it and comes
// back, which is not the same as a person having to unblock it.
func todoSprintRowState(e todo.SprintEntry) report.State {
	switch e.State {
	case todo.SprintItemDone:
		return report.Pass
	case todo.SprintItemRunning:
		return report.Run
	case todo.SprintItemBlocked:
		return report.Fail
	case todo.SprintItemWaiting, todo.SprintItemDropped:
		return report.Skip
	}
	return report.Queue
}

// todoSprintOutcome is the row's last field: where the slug stands, and for
// a waiting one what it is waiting on.
func todoSprintOutcome(e todo.SprintEntry) string {
	if e.State == todo.SprintItemWaiting {
		return "waits on " + strings.Join(e.Waiting, ", ")
	}
	if e.State == todo.SprintItemDropped {
		return "dropped from the backlog"
	}
	return string(e.State)
}

// todoDetail is one item for the transcript: the header as read, then the
// body as written.
func todoDetail(s *todo.Store, it todo.Item) string {
	return todoItemReport(s, it, true).String()
}

// todoItemReport is one item as a report. body is false for a caller that
// lays the prose out itself — the command does, on a terminal, through the
// markdown renderer — and true where the body belongs in the report verbatim.
func todoItemReport(s *todo.Store, it todo.Item, body bool) report.Report {
	r := report.Report{
		Title:   "shhh todo " + it.Slug,
		Subject: clipRunes(it.Title, 72),
	}
	pairs := []report.Pair{{Key: "status", Value: todoState(s, it)}, {Key: "priority", Value: string(it.Priority)}}
	rest := make([]report.Pair, 0, len(it.Profile.Fields)+3)
	for _, f := range it.Profile.Fields {
		if !f.Orders() {
			rest = append(rest, report.Pair{Key: f.Name, Value: it.Fields[f.Name]})
		}
	}
	rest = append(rest,
		report.Pair{Key: "depends on", Value: strings.Join(it.DependsOn, ", ")},
		report.Pair{Key: "created", Value: it.Created},
		report.Pair{Key: "session", Value: it.Session})
	for _, p := range rest {
		if p.Value != "" {
			pairs = append(pairs, p)
		}
	}
	for _, f := range it.Extra {
		pairs = append(pairs, report.Pair{Key: f.Key, Value: f.Value})
	}
	pairs = append(pairs, report.Pair{Key: "file", Value: it.Path})
	r.Sections = []report.Section{{Pairs: pairs}}
	if text := strings.TrimSpace(it.Body); body && text != "" {
		r.Sections = append(r.Sections, report.Section{Body: text})
	}
	for _, w := range it.Warnings {
		r.Notes = append(r.Notes, report.Note{State: report.Warn, Text: w})
	}
	return r
}

func newTodoCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "todo",
		Short: "List the project's backlog",
		Long:  "List the backlog items under the checkout's .shhh/todo directory in the order a session would work them, with what each is waiting on and why any file failed to load.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := loadTodos(todoCwd())
			if asJSON {
				return writeJSON(cmd, todoJSON(s, s.Items))
			}
			return report.Fprint(cmd.OutOrStdout(), todoReport(s))
		},
	}
	todoJSONFlag(cmd, &asJSON, "the backlog")
	cmd.AddCommand(
		newTodoSetCmd("ready", "List the items that can be started now",
			func(s *todo.Store) []todo.Item { return s.Ready() }),
		newTodoSetCmd("next", "Show the item a run would take next",
			todoNext),
		newTodoSprintCmd(),
		newTodoRunCmd(),
		newTodoGroomCmd(),
		newTodoShowCmd(),
	)
	cmd.AddCommand(newTodoStateCmds()...)
	return cmd
}

// todoJSONFlag puts `--json` on a listing verb. The report is presentation
// and its shape follows the terminal; the JSON is the store, so a script and
// the screen are reading one backlog rather than agreeing to differ about it.
func todoJSONFlag(cmd *cobra.Command, into *bool, what string) {
	cmd.Flags().BoolVar(into, "json", false, "emit "+what+" as JSON, warnings included")
}

// newTodoSetCmd registers a verb that lists part of the backlog. It draws and
// serialises through the whole listing's own functions, so `ready` and `next`
// cannot come to disagree with `shhh todo` about what an item waits on.
func newTodoSetCmd(verb, short string, set func(*todo.Store) []todo.Item) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   verb,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := loadTodos(todoCwd())
			items := set(s)
			if asJSON {
				return writeJSON(cmd, todoJSON(s, items))
			}
			return report.Fprint(cmd.OutOrStdout(), todoSetReport(s, "shhh todo "+verb, items))
		},
	}
	todoJSONFlag(cmd, &asJSON, "the items")
	return cmd
}

func newTodoSprintCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sprint",
		Short: "Show the sprint: its goal, its items and how many are done",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := loadTodos(todoCwd())
			if asJSON {
				return writeJSON(cmd, todoJSON(s, todoSprintItems(s)))
			}
			return report.Fprint(cmd.OutOrStdout(), todoSprintReport(s))
		},
	}
	todoJSONFlag(cmd, &asJSON, "the sprint")
	cmd.AddCommand(newTodoSprintPlanCmd())
	return cmd
}

// newTodoSprintPlanCmd is the reading behind a sprint with nobody watching.
// It spends one read-only turn over the ready items and prints the set it
// recommends, the line behind each item, the goal and what it left out.
//
// It writes nothing — not the sprint file, and not a reading of any item.
// That is the whole difference between this and `/todo sprint plan`: what a
// set costs is a week of somebody's work, and choosing one is a decision.
// A script wants the recommendation, which is what it gets.
// See docs/capabilities/todo.md#a-sprint-is-what-ships-together.
func newTodoSprintPlanCmd() *cobra.Command {
	var asJSON bool
	var size string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Recommend the set of items that should go next, and say why",
		Long: "Read the ready items and recommend the set that belongs together: the items in " +
			"the order they should be worked with one line each, a goal for the set, what kind " +
			"of release it reads as, and every candidate it left out with the word for why. " +
			"Nothing is written; the set is accepted on the card `/todo sprint plan` puts up " +
			"in a session.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return todoSprintPlanHeadless(cmd, size, asJSON)
		},
	}
	// The flag is the profile's grading field, because what a set is
	// budgeted in is what a run spends: a backlog of readings is bounded in
	// depths and has no size to be asked for. A profile that does not grade
	// its work is offered no budget at all rather than one it cannot spend.
	if name, shape, graded := todo.BudgetFlag(todoProfile()); graded {
		cmd.Flags().StringVar(&size, name, "", "the budget the set has to fit, as "+shape)
	}
	// Not todoJSONFlag: that one promises the store's warnings with the
	// listing, and a proposal is a reading rather than a listing — it
	// carries reasons, not diagnostics.
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the proposal as JSON")
	return cmd
}

// todoSprintPlanHeadless spends the one turn and reads its answer against
// the candidates it was asked about.
func todoSprintPlanHeadless(cmd *cobra.Command, spec string, asJSON bool) error {
	if !todoProfile().Plans() {
		return fmt.Errorf("the %s profile does not plan sets: it says nothing about what makes its items belong together, so there is no proposal to read", todoProfile().Name)
	}
	budget, err := todo.ParseSprintBudget(todoProfile(), spec)
	if err != nil {
		return err
	}
	root := todo.Root(todoCwd())
	s := todo.Load(todoProfile(), root)
	if s.Sprint.Open() {
		return fmt.Errorf("%s is still open — one sprint at a time; `shhh todo sprint` shows it", s.Sprint.Name)
	}
	candidates := s.Ready()
	if len(candidates) == 0 {
		return fmt.Errorf("nothing is ready, so there is no set to propose")
	}
	if !budget.Fits(candidates) {
		name, _, _ := todo.BudgetFlag(todoProfile())
		return fmt.Errorf("no ready item fits %s; without --%s the whole ready list is read", budget, name)
	}
	// The driver is built with no commit to make: planning changes nothing,
	// so the refusal a run gives outside a repository is not this command's
	// to give.
	d, err := newTodoDriver(cmd.OutOrStdout(), root, ConfigFrom(cmd.Context()), true)
	if err != nil {
		return err
	}
	plan, err := todoSprintPlanRead(cmd.Context(), d, s, candidates, budget)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(cmd, plan)
	}
	return report.Fprint(cmd.OutOrStdout(), todoSprintPlanReport(s, plan))
}

// todoSprintPlanRead spends the one turn and reads its answer against the
// candidates it was asked about.
func todoSprintPlanRead(ctx context.Context, d *todoDriver, s *todo.Store, candidates []todo.Item, budget todo.SprintBudget) (todo.Plan, error) {
	// The step names no stage: planning a set is not a stage of working
	// one, and nothing here is gated, checkpointed or continued.
	turn, err := d.turn(ctx, time.Time{}, run.Step{
		Action: run.ActionPrompt, Mode: run.ModePlan,
		Prompt: s.PlanPrompt(candidates, budget.String()), Shown: "plan the sprint",
	})
	if err != nil {
		return todo.Plan{}, err
	}
	return todo.ParsePlan(s.Profile, turn.text, candidates, budget), nil
}

// todoSprintPlanReport is one proposal printed: the set in the order it
// would be worked with the line behind each item, then the candidates it
// left out with the word. The left-out rows are rows and not a footnote —
// what a recommendation left out is half of what makes it arguable.
func todoSprintPlanReport(s *todo.Store, plan todo.Plan) report.Report {
	rows := make([]report.Row, 0, len(plan.Items)+len(plan.Left))
	for _, it := range plan.Items {
		item, _ := s.Find(it.Slug)
		rows = append(rows, report.Row{
			State: report.Queue, Name: it.Slug,
			Subject: clipRunes(item.Title, 72), Outcome: it.Why,
		})
	}
	for _, l := range plan.Left {
		item, _ := s.Find(l.Slug)
		rows = append(rows, report.Row{
			State: report.Skip, Name: l.Slug,
			Subject: clipRunes(item.Title, 72), Outcome: string(l.Why),
		})
	}
	rep := report.Report{
		Title:    "shhh todo sprint plan",
		Subject:  fmt.Sprintf("%d in the set · %d left out", len(plan.Items), len(plan.Left)),
		Sections: []report.Section{{Rows: rows}},
		Tally:    "nothing written; `/todo sprint plan` takes the set in a session",
	}
	if goal := plan.GoalText(); goal != "" {
		rep.Sections = append([]report.Section{{Header: "GOAL", Body: goal}}, rep.Sections...)
	}
	if len(rows) == 0 {
		rep.Sections[len(rep.Sections)-1].Rows = []report.Row{
			report.Empty("the reading proposed no set that could be read as items", "try it again"),
		}
	}
	return rep
}

func newTodoShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "show <slug>",
		Short:             "Show one backlog item, header and body",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: todoSlugs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return todoShow(cmd.OutOrStdout(), todoCwd(), args[0])
		},
	}
}

// newTodoStateCmds are the verbs that change one item. Each is the session's
// own verb with a command around it: the same words, the same refusals, and
// the refusal in the exit status as well, because a script has no screen to
// read it off.
func newTodoStateCmds() []*cobra.Command {
	verbs := []struct {
		use, short string
		args       cobra.PositionalArgs
	}{
		{"block <slug> [why]", "Mark an item blocked, with the reason written on it", cobra.MinimumNArgs(1)},
		{"open <slug>", "Put a blocked or in-progress item back to open", cobra.ExactArgs(1)},
		{"done <slug>", "Archive a finished item with its record", cobra.ExactArgs(1)},
		{"drop <slug>", "Delete an item outright; the file goes", cobra.ExactArgs(1)},
	}
	out := make([]*cobra.Command, 0, len(verbs))
	for _, v := range verbs {
		verb, _, _ := strings.Cut(v.use, " ")
		out = append(out, &cobra.Command{
			Use:               v.use,
			Short:             v.short,
			Args:              v.args,
			ValidArgsFunction: todoSlugs,
			RunE: func(cmd *cobra.Command, args []string) error {
				text, err := todoVerb(todo.Root(todoCwd()), append([]string{verb}, args...))
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), text)
				return err
			},
		})
	}
	return out
}

// todoSlugs completes an item's slug. The backlog is a directory of small
// files and is read on every completion rather than cached: an item another
// terminal wrote a second ago is one this has to offer, and re-reading it
// costs a directory listing.
func todoSlugs(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, it := range loadTodos(todoCwd()).Items {
		if strings.HasPrefix(it.Slug, prefix) {
			out = append(out, it.Slug+"\t"+it.Title)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// todoManager backs the textual /todo subcommands, in the words the session
// puts in its transcript: a mistyped name is the row every mistyped name
// gets, a verb given nothing is the usage line, and anything else that went
// wrong is the sentence the verb refused with.
func todoManager(root string) func(args []string) string {
	return func(args []string) string {
		text, err := todoVerb(root, args)
		if err == nil {
			return text
		}
		var miss todoNotFound
		var usage todoUsage
		switch {
		case errors.As(err, &miss):
			return notFound(miss.kind, miss.slug, "/todo")
		case errors.As(err, &usage):
			return usage.text
		}
		return "Error: " + err.Error()
	}
}

// todoNotFound is a verb that named nothing. It is a type of its own because
// the two surfaces say it differently — the session draws the row every
// mistyped name gets, a command hands the sentence to its error page — and
// the fact that a name was the problem must survive the trip between them.
type todoNotFound struct {
	kind, slug string
}

func (e todoNotFound) Error() string {
	return fmt.Sprintf("no %s %q; `shhh todo` lists them", e.kind, e.slug)
}

// todoUsage is a verb that was not given what it needs. Only the session can
// reach it: a command's arguments are counted by cobra before the verb runs.
type todoUsage struct{ text string }

func (e todoUsage) Error() string { return e.text }

// todoVerb carries out one /todo subcommand and answers with what to print,
// or with why it refused. Every write goes through the store's line edits, so
// a verb changes the fact it names and nothing else in the file.
// See docs/capabilities/todo.md#an-item-is-a-file-you-can-edit.
func todoVerb(root string, args []string) (string, error) {
	usage := todoUsage{"Usage: /todo [list] · /todo show <slug> · /todo add <text> · /todo block <slug> [why] · /todo open <slug> · /todo done <slug> · /todo drop <slug> · /todo edit <slug> · /todo sprint"}
	s := todo.Load(todoProfile(), root)
	if len(args) == 0 || (len(args) == 1 && args[0] == "list") {
		return todoListing(s), nil
	}
	slug := ""
	if len(args) > 1 {
		slug = args[1]
	}
	switch args[0] {
	case "show":
		if slug == "" {
			return "", usage
		}
		it, ok := s.Find(slug)
		if !ok {
			return "", todoNotFound{"backlog item", slug}
		}
		return todoDetail(s, it), nil
	case "add":
		title := strings.TrimSpace(strings.Join(args[1:], " "))
		if title == "" {
			return "", usage
		}
		// An item typed at the command line takes every field's default,
		// which is what the profile is for: nothing here decides what a
		// new item is called or how big it is.
		profile := todoProfile()
		it := todo.Item{
			Slug:    todo.Slugify(title),
			Title:   title,
			Fields:  map[string]string{},
			Created: time.Now().Format("2006-01-02"),
			Body:    todoTemplate,
			Profile: profile,
		}
		for _, f := range profile.Fields {
			if f.Orders() {
				it.Priority = todo.Priority(f.Default)
				continue
			}
			if f.Default != "" {
				it.Fields[f.Name] = f.Default
			}
		}
		path, err := todo.Create(profile, root, it)
		if err != nil {
			return "", err
		}
		return report.Report{Sections: []report.Section{{Rows: []report.Row{
			report.Done("added", it.Slug+" · "+joinDetail(todoFieldDetail(it), string(it.Priority))),
			{State: report.Run, Subject: "fill in the criteria", Detail: "/todo edit " + it.Slug, Body: []string{path}},
		}}}}.String(), nil
	case "block":
		if slug == "" {
			return "", usage
		}
		it, ok := activeItem(s, slug)
		if !ok {
			return "", todoNotFound{"active backlog item", slug}
		}
		if err := todoHeld(root, slug); err != nil {
			return "", err
		}
		if err := todo.SetStatus(it.Path, todo.StatusBlocked); err != nil {
			return "", err
		}
		why := strings.TrimSpace(strings.Join(args[2:], " "))
		if why != "" {
			if err := todo.Append(it.Path, "## Blocked\n"+why); err != nil {
				return "", err
			}
		}
		return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("blocked", slug)}}}}.String(), nil
	case "open":
		if slug == "" {
			return "", usage
		}
		it, ok := activeItem(s, slug)
		if !ok {
			return "", todoNotFound{"active backlog item", slug}
		}
		if err := todoHeld(root, slug); err != nil {
			return "", err
		}
		if err := todo.SetStatus(it.Path, todo.StatusOpen); err != nil {
			return "", err
		}
		return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("reopened", slug)}}}}.String(), nil
	case "done":
		if slug == "" {
			return "", usage
		}
		if err := todoHeld(root, slug); err != nil {
			return "", err
		}
		to, err := todo.Archive(root, slug, "")
		if err != nil {
			return "", err
		}
		rows := []report.Row{report.Done("archived", slug+" → "+to)}
		// Archiving by hand is one of the two ways a sprint's last
		// slug is accounted for, so the close is checked here as well
		// as at the end of a run.
		if closed, err := todo.CloseSprintIfDone(todoProfile(), root); err != nil {
			rows = append(rows, report.Row{State: report.Warn, Subject: "the sprint could not be closed", Detail: err.Error()})
		} else if closed != "" {
			rows = append(rows, report.Done("sprint closed", closed))
		}
		return report.Report{Sections: []report.Section{{Rows: rows}}}.String(), nil
	case "sprint":
		return todoSprintManage(root, s, args[1:])
	case "drop":
		if slug == "" {
			return "", usage
		}
		if err := todoHeld(root, slug); err != nil {
			return "", err
		}
		if err := todo.Remove(root, slug); err != nil {
			return "", err
		}
		return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("dropped", slug+" · the file is deleted")}}}}.String(), nil
	}
	return "", usage
}

// todoHeld refuses a verb that would change an item a run has in flight.
//
// Every stage prompt states the item as the file stands when the stage
// starts, so blocking, reopening, archiving or deleting one under a live run
// changes what its next stage is working from — and the run says nothing,
// because it reads the file rather than watching it. The refusal names the
// session because that is the only handle the person has on the other half
// of the work: it is where the run can be stopped, and until it is, this is
// the same item in two places.
func todoHeld(root, slug string) error {
	h, held := run.HeldBy(root, slug)
	switch {
	case !held:
		return nil
	case h.Sprint:
		return fmt.Errorf("the sprint in session %s holds %s; `/todo stop` there ends it, and `shhh todo run --all` carries it on here",
			h.Session, slug)
	}
	return fmt.Errorf("the run in session %s holds %s at the %s stage; `/todo stop` there ends it, and `shhh todo run %s` carries it on here",
		h.Session, slug, h.Stage, slug)
}

// todoSprintManage backs `/todo sprint` and its verbs. Every write is a
// line edit on the sprint file, the way every item write is, so a verb
// changes the fact it names and leaves the goal and the order alone.
// See docs/capabilities/todo.md#a-sprint-is-a-file-that-names-its-items.
func todoSprintManage(root string, s *todo.Store, args []string) (string, error) {
	usage := todoUsage{"Usage: /todo sprint · /todo sprint add <slug> · /todo sprint drop <slug> · /todo sprint goal <text> · /todo sprint close"}
	if len(args) == 0 {
		return todoSprintReport(s).String(), nil
	}
	sp := s.Sprint
	if sp == nil {
		return "There is no sprint. /todo sprint plan proposes one from the ready items.", nil
	}
	rest := strings.TrimSpace(strings.Join(args[1:], " "))
	// A file marked closed by hand is a record that was never filed, and
	// editing a record is the one thing the archive exists to prevent.
	// Close is still offered, because filing it is the way out.
	if !sp.Open() && args[0] != "close" {
		return fmt.Sprintf("%s is closed; /todo sprint close files it in the archive and /todo sprint plan starts the next one.", sp.Name), nil
	}
	switch args[0] {
	case "add":
		if rest == "" {
			return "", usage
		}
		if _, ok := activeItem(s, rest); !ok {
			return "", todoNotFound{"active backlog item", rest}
		}
		if err := todo.SprintAdd(sp.Path, rest); err != nil {
			return "", err
		}
		return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("added to "+sp.Name, rest)}}}}.String(), nil
	case "drop":
		if rest == "" {
			return "", usage
		}
		if err := todo.SprintDrop(sp.Path, rest); err != nil {
			return "", err
		}
		return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("dropped from "+sp.Name, rest+" · the item itself is untouched")}}}}.String(), nil
	case "goal":
		if rest == "" {
			return "", usage
		}
		if err := todo.SprintSetGoal(sp.Path, rest); err != nil {
			return "", err
		}
		return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("goal of "+sp.Name, "rewritten")}}}}.String(), nil
	case "close":
		to, err := todo.CloseSprint(todoProfile(), root)
		if err != nil {
			return "", err
		}
		rows := []report.Row{report.Done("closed "+sp.Name, to)}
		for _, e := range s.SprintEntries() {
			if !e.Done() {
				rows = append(rows, report.Row{State: report.Skip, Name: e.Slug,
					Subject: "left undone", Outcome: todoSprintOutcome(e)})
			}
		}
		return report.Report{Sections: []report.Section{{Rows: rows}}}.String(), nil
	}
	return "", usage
}

// todoSprintItems are the sprint's slugs as backlog items, in the file's
// order, leaving out any the backlog no longer holds — those are on the
// sprint's own rows, where a slug that vanished is a state rather than a
// missing item.
func todoSprintItems(s *todo.Store) []todo.Item {
	entries := s.SprintEntries()
	out := make([]todo.Item, 0, len(entries))
	for _, e := range entries {
		if e.State != todo.SprintItemDropped {
			out = append(out, e.Item)
		}
	}
	return out
}

// activeItem finds an item that is not archived.
func activeItem(s *todo.Store, slug string) (todo.Item, bool) {
	it, ok := s.Find(slug)
	if !ok || it.Archived {
		return todo.Item{}, false
	}
	return it, true
}

// todoTemplate is the body a one-line /todo add starts with: the sections
// a worked item carries, empty, so the file says what is still to be
// written rather than looking complete.
const todoTemplate = `**As a** …, **I want** … **so that** ….

## Acceptance criteria
- [ ]

## Tasks
- [ ]

## Tests
-

## Notes
`

// newTodoGroomCmd is the reading of an item against the tree with nobody
// watching. It spends one read-only turn per item and prints the verdicts;
// it writes nothing at all — not the corrections, and not the header's
// stamp.
//
// That is the whole difference between this and `/todo groom`, and it is
// deliberate. The verdicts that matter are claims about what the code does
// today, and accepting one is a decision; a command that wrote them would be
// the runner writing the backlog rather than working it. What a script wants
// from a grooming is the reading, which is what it gets.
// See docs/capabilities/todo.md#an-item-is-checked-before-it-is-worked.
func newTodoGroomCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "groom [<slug>]",
		Short: "Read a backlog item against the code as it stands now",
		Long: "Read one backlog item — or every active item with --all — against the tree, and " +
			"print a verdict for each claim it makes: whether it holds, where a reference has " +
			"moved to, what has changed, what is gone, which acceptance criteria the tree already " +
			"satisfies, and what could not be settled. Nothing is written; the corrections are " +
			"accepted on the card `/todo groom` puts up in a session.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: todoSlugs,
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := ""
			if len(args) == 1 {
				slug = args[0]
			}
			return todoGroomHeadless(cmd, slug, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "read every active item, in backlog order")
	return cmd
}

// todoGroomHeadless resolves what to read and reads it, one turn per item.
func todoGroomHeadless(cmd *cobra.Command, slug string, all bool) error {
	if !todoProfile().Grooms() {
		return fmt.Errorf("the %s profile does not groom: it says nothing about what one of its items claims, so there is nothing to read one against", todoProfile().Name)
	}
	if all && slug != "" {
		return fmt.Errorf("--all reads the whole backlog, so it does not take an item as well")
	}
	root := todo.Root(todoCwd())
	store := todo.Load(todoProfile(), root)
	items, err := todoGroomTargets(store, slug, all)
	if err != nil {
		return err
	}
	// The driver is built with no commit to make: grooming changes nothing,
	// so the refusal a run gives outside a repository is not this command's
	// to give.
	d, err := newTodoDriver(cmd.OutOrStdout(), root, ConfigFrom(cmd.Context()), true)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, it := range items {
		r, err := todoGroomRead(cmd.Context(), d, it)
		if err != nil {
			return err
		}
		if err := report.Fprint(out, todoGroomReport(it, r)); err != nil {
			return err
		}
	}
	return nil
}

// todoGroomTargets is which items the command reads.
func todoGroomTargets(s *todo.Store, slug string, all bool) ([]todo.Item, error) {
	if all {
		if len(s.Items) == 0 {
			return nil, fmt.Errorf("the backlog has no active items to read")
		}
		return s.Items, nil
	}
	if slug == "" {
		return nil, fmt.Errorf("name an item to read, or --all for every active one")
	}
	it, ok := activeItem(s, slug)
	if !ok {
		return nil, todoNotFound{"active backlog item", slug}
	}
	return []todo.Item{it}, nil
}

// todoGroomRead spends the one turn and reads its answer against the file.
func todoGroomRead(ctx context.Context, d *todoDriver, it todo.Item) (todo.Reading, error) {
	turn, err := d.turn(ctx, time.Time{}, run.Step{
		Action: run.ActionPrompt, Stage: run.StageGroom, Mode: run.ModePlan,
		Prompt: run.GroomPrompt(todoPipeline(), it), Shown: "groom " + it.Slug,
	})
	if err != nil {
		return todo.Reading{}, err
	}
	r, err := todo.Groom(it, turn.text)
	if err != nil {
		return todo.Reading{}, err
	}
	r.Head = project.Head(d.root)
	return r, nil
}

// todoGroomReport is one reading printed: a row per claim, in the order the
// item states them, with the line the verdict would write under it. The
// verdicts that propose nothing are rows too — "everything else holds" is
// the half of a reading a person most wants to be able to trust, and a
// report that listed only the corrections could not say it.
func todoGroomReport(it todo.Item, r todo.Reading) report.Report {
	rows := make([]report.Row, 0, len(r.Findings))
	for _, f := range r.Findings {
		row := report.Row{
			State: todoGroomState(f.Verdict), Name: string(f.Verdict),
			Subject: strings.TrimSpace(f.Claim), Detail: f.Evidence,
		}
		if f.Edits() {
			row.Body = []string{orDashLine(f.Now)}
		}
		rows = append(rows, row)
	}
	rep := report.Report{
		Title:    "shhh todo groom " + it.Slug,
		Subject:  fmt.Sprintf("%d read · %d proposed", len(r.Findings), len(r.Changes())),
		Sections: []report.Section{{Rows: rows}},
		Tally:    "nothing written; `/todo groom " + it.Slug + "` accepts the corrections in a session",
	}
	if len(r.Findings) == 0 {
		rep.Sections[0].Rows = []report.Row{report.Empty("the reading answered in no shape that could be read as verdicts", "try it again")}
	}
	if r.Finished() {
		rep.Notes = append(rep.Notes, report.Note{State: report.Warn,
			Text: "every acceptance criterion reads already done; `shhh todo done " + it.Slug + "` archives it"})
	}
	return rep
}

// todoGroomState is the mark a verdict prints under. A claim that no longer
// holds is a warning rather than a failure: the item is wrong, and an item
// being wrong is what this command is for finding.
func todoGroomState(v todo.Verdict) report.State {
	switch v {
	case todo.VerdictHolds:
		return report.Pass
	case todo.VerdictUnknown:
		return report.Skip
	case todo.VerdictDone:
		return report.Queue
	}
	return report.Warn
}

// orDashLine is the line a verdict would write, and the word for one that
// would write none because the line goes.
func orDashLine(now string) string {
	if strings.TrimSpace(now) == "" {
		return "(the line goes)"
	}
	return strings.TrimSpace(now)
}
