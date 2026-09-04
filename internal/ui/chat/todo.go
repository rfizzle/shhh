package chat

// The project backlog in the session: /todo and its picker, the TODO block
// in the inspector rail, and the editor handover for an item. The backlog
// is files (docs/capabilities/todo.md#an-item-is-a-file-you-can-edit), so
// every surface here reads a store loaded from disk and reloads it after
// anything that could have changed a file — a /todo command, the editor
// coming back, a turn ending — rather than holding its own copy of the
// truth.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
	"github.com/rfizzle/shhh/internal/ui/markdown"
)

// Todos wires the backlog into the chat TUI. The zero value disables it.
type Todos struct {
	// Root is the checkout the backlog belongs to.
	Root string
	// Manage backs the textual /todo subcommands (list, show, add, block,
	// open, done, drop) and returns the transcript note.
	Manage func(args []string) string
	// Detail renders one item for the transcript.
	Detail func(*todo.Store, todo.Item) string
	// PublishReport writes a page the session built itself and answers with
	// its URL. It is what a closed sprint's report goes through. Nil is a
	// session with no report store — the close still happens and says so,
	// because the record of the set is the archived file and the page is
	// the readable form of it.
	PublishReport func(reports.Document) (string, error)
	// Extractor reads the session into proposed items behind a bare
	// /todo add. Nil, or disabled, leaves only the by-hand form.
	Extractor *todo.Extractor
	// Drafter turns the sentence after /todo new into one proposed item.
	// Nil, or disabled, leaves only the by-hand form.
	Drafter *todo.Drafter
	// NoCommit is the project's standing answer to whether a run ends in a
	// commit, from the config. The zero value is a commit, which is what
	// the setting's own default is: a host that says nothing gets the
	// runner's definition of done rather than the quieter one.
	NoCommit bool
	// ItemTimeout is how long one item of a sprint may take before the
	// sprint gives up on it. The zero value is no cap, which is the
	// setting's default: a cap that fires throws a run away, and what it
	// should be is a fact about the project rather than about shhh.
	ItemTimeout time.Duration
	// GroomStale is how many commits the tree may take after an item was
	// read against it before the surfaces say the reading has fallen
	// behind. The zero value is the setting's own default, which is what a
	// host that says nothing gets; a negative one turns the warning off.
	GroomStale int
	// Wordings are the stage instructions a run sends, as the host read
	// them. The zero value is the built-in set.
	Wordings run.Wordings
	// Profile is the vocabulary this project's backlog is written in, as
	// the host resolved it. It is handed over rather than defaulted here
	// because a screen that made up its own words would draw a backlog
	// nobody wrote.
	Profile todo.Profile
	// Pipeline is the steps a run of that backlog takes, resolved by the
	// host beside the vocabulary. The empty pipeline is the built-in one,
	// the way the empty wordings are the built-in set.
	Pipeline run.Pipeline
}

// WithTodos enables /todo and the TODO block.
func (m Model) WithTodos(t Todos) Model {
	m.todos = t
	m.reloadTodos()
	return m
}

// todosEnabled reports whether this session has a backlog wired.
func (m *Model) todosEnabled() bool { return m.todos.Manage != nil && m.codingSurfaces() }

// reloadTodos re-reads the backlog from disk. It is cheap — a directory
// listing and a handful of small files — and it is called only on events,
// never per frame, so the rail can be drawn from the cached store.
func (m *Model) reloadTodos() {
	if m.todos.Root == "" {
		m.todoStore = nil
		return
	}
	m.todoStore = todo.Load(m.todos.Profile, m.todos.Root)
	// How far behind each item's last reading has fallen is a question for
	// the repository, so it is asked here — where the backlog is read —
	// rather than by the surfaces that draw it every frame (todogroom.go).
	m.refreshGroomStale()
	// The screen is drawn from this store, so it is rebuilt wherever the
	// store is: a run archiving an item under a reader who is looking at the
	// list would otherwise leave the row it archived on screen.
	m.refreshTodoScreen()
}

// todoRailRows is how many backlog rows the block shows before it says how
// many more there are. Four is enough to see what is next and what is
// stuck; the whole list is one command away.
const todoRailRows = 4

// todoHintRail is the block's last row.
const todoHintRail = "/todo for the whole backlog"

// inspectorTodo is the TODO block: the active backlog in its working order,
// the first few rows drawn and the rest counted. Nil without a backlog, so
// the block is omitted rather than drawn empty.
func (m Model) inspectorTodo() *components.InspectorTodo {
	s := m.todoStore
	if s == nil || s.Len() == 0 {
		return nil
	}
	t := &components.InspectorTodo{
		Open:    s.Count(todo.StatusOpen) + s.Count(todo.StatusInProgress),
		Blocked: s.Count(todo.StatusBlocked),
		Hint:    todoHintRail,
	}
	t.Sprint, t.SprintDone, t.SprintTotal = m.inspectorSprint()
	// A sprint is working one item at a time and the block is where a reader
	// who is not watching the transcript finds out which. The stage comes
	// with it because a slug on its own says a sprint is going and not
	// whether it is moving.
	if st := m.todoRunner.state; st.Sprinting() {
		t.SprintItem, t.SprintStage = st.Slug, string(st.Stage)
	}
	for _, it := range todoRailOrder(s.Items, m.todoRunner.state) {
		if len(t.Rows) == todoRailRows {
			t.More++
			continue
		}
		t.Rows = append(t.Rows, m.todoStaleRow(todoRow(s, it, m.todoRunner.state)))
	}
	return t
}

// todoRailOrder is the backlog in working order with the item a run is
// working moved to the front. The block shows four rows of a backlog that
// may hold forty, and the one item the session is actually building is the
// one row it must never be the "… 36 more" that swallowed. Everything else
// keeps the store's order, so the list under it is still the queue.
func todoRailOrder(items []todo.Item, running *run.State) []todo.Item {
	if running == nil || running.Over() {
		return items
	}
	out := make([]todo.Item, 0, len(items))
	var rest []todo.Item
	for _, it := range items {
		if it.Slug == running.Slug {
			out = append(out, it)
			continue
		}
		rest = append(rest, it)
	}
	return append(out, rest...)
}

// todoRow is one item as the rail draws it.
func todoRow(s *todo.Store, it todo.Item, running *run.State) components.InspectorTodoRow {
	// Parse never leaves Priority empty — an unset or unknown one reads as
	// medium — so the first letter is always there to take.
	row := components.InspectorTodoRow{
		Slug:     it.Slug,
		Priority: strings.ToUpper(string(it.Priority[:1])),
		Grade:    todoGradeGlyph(it),
	}
	switch it.Status {
	case todo.StatusBlocked:
		row.State, row.Note = components.TodoBlocked, "blocked"
	case todo.StatusInProgress:
		row.State, row.Note = components.TodoRunning, "in progress"
		if running != nil && running.Slug == it.Slug {
			row.Note = string(running.Stage)
			if running.Round > 0 {
				row.Note += fmt.Sprintf(" %d/%d", running.Round, running.Rounds())
			}
			row.LanesDone, row.LanesTotal = laneProgress(running)
		}
	default:
		if waiting := s.Waiting(it); len(waiting) > 0 {
			row.State, row.Note = components.TodoWaiting, "needs "+waiting[0]
			if len(waiting) > 1 {
				row.Note += fmt.Sprintf(" +%d", len(waiting)-1)
			}
		}
	}
	return row
}

// todoGradeGlyph is the one letter a rail row draws an item's grade as,
// and a hyphen for one nobody has graded — a blank column would read as a
// row that is missing a field rather than a file that is.
func todoGradeGlyph(it todo.Item) string {
	f, ok := it.Profile.GradeField()
	if !ok {
		return "-"
	}
	if glyph := f.Glyph(it.Grade()); glyph != "" {
		return glyph
	}
	return "-"
}

// todoStaleRow adds what a rail row says about a reading that has fallen
// behind the tree. It is a separate pass over the row because the note it
// writes is about the item's prose rather than about its state, and the
// state is what every other field on the row is a reading of.
func (m Model) todoStaleRow(row components.InspectorTodoRow) components.InspectorTodoRow {
	if note := m.groomStaleNote(row.Slug); note != "" && row.Note == "" {
		row.Note, row.Stale = note, true
	}
	return row
}

// todoCommand is /todo from the input. Bare /todo opens the picker where
// there is something to pick; edit hands an item to the editor; everything
// else is textual and reloads the store afterwards, because it may have
// changed a file.
func (m Model) todoCommand(parts []string) (tea.Model, tea.Cmd) {
	if !m.todosEnabled() {
		return m.systemNotice("The backlog is unavailable in this session.")
	}
	if len(parts) == 1 {
		return m.openTodoScreen()
	}
	if len(parts) >= 2 && parts[1] == "status" {
		return m.todoRunStatus()
	}
	if len(parts) >= 2 && m.working() && todoWrites(parts[1:]) {
		return m.systemNotice("Not while the turn is running: /todo " + strings.Join(todoWriteVerb(parts[1:]), " ") +
			" changes the backlog files the model may be working from. /todo, /todo show and /todo sprint still read.")
	}
	if len(parts) == 2 && parts[1] == "add" {
		return m.startTodoExtract()
	}
	if len(parts) >= 2 && parts[1] == "new" {
		return m.startTodoDraft(strings.Join(parts[2:], " "))
	}
	// Planning is the one sprint verb the session takes rather than the
	// manager: it proposes a set and writes nothing until the card is
	// accepted. Every other sprint verb is textual and goes through the
	// manager below, so a script gets the same words.
	if len(parts) >= 3 && parts[1] == "sprint" && parts[2] == "plan" {
		return m.startTodoSprintPlan(parts[3:])
	}
	// Closing goes through the manager like every other sprint verb; what
	// the session adds afterwards is the report page, which needs a
	// publisher a script has not got.
	if len(parts) == 3 && parts[1] == "sprint" && parts[2] == "close" {
		return m.closeSprintCommand()
	}
	// A goal written while a proposal is in flight goes on the proposal:
	// there is no sprint file to edit until the card is taken.
	if len(parts) >= 3 && parts[1] == "sprint" && parts[2] == "goal" && m.sprintPlan != nil {
		return m.sprintGoalCommand(strings.Join(parts[3:], " "))
	}
	if len(parts) >= 2 && parts[1] == "run" {
		opt, ok := parseTodoRunArgs(parts[2:])
		if !ok {
			return m.systemNotice(todoRunUsage)
		}
		if opt.all {
			return m.startTodoSprint(opt)
		}
		return m.startTodoRun(opt.arg, opt.noCommit)
	}
	if len(parts) == 2 && parts[1] == "stop" {
		return m.stopTodoRun()
	}
	// Grooming is the second verb the session takes rather than the
	// manager, for the reason planning is: it spends a turn and proposes,
	// and nothing is written until the card is accepted.
	if len(parts) >= 2 && parts[1] == "groom" {
		return m.startTodoGroom(parts[2:])
	}
	if len(parts) >= 2 && parts[1] == "edit" {
		if len(parts) != 3 {
			return m.systemNotice("Usage: /todo edit <slug>")
		}
		return m.openTodoEditor(parts[2])
	}
	note := m.todos.Manage(parts[1:])
	m.reloadTodos()
	return m.systemNotice(note)
}

// todoWrites reports whether a /todo subcommand changes a file. Reading is
// fine mid-turn; changing a file the model may be editing at the same
// moment is not, and neither is suspending the program under a running
// turn for the editor. Bare `/todo sprint` only reads, so it is the verbs
// after it that are refused rather than the word itself.
func todoWrites(args []string) bool {
	switch args[0] {
	case "edit", "add", "new", "block", "open", "done", "drop", "run", "stop", "groom":
		return true
	case "sprint":
		return len(args) > 1
	}
	return false
}

// todoWriteVerb is what the refusal names: the subcommand, and the verb
// after it where the subcommand alone would not say what was refused.
func todoWriteVerb(args []string) []string {
	if args[0] == "sprint" && len(args) > 1 {
		return args[:2]
	}
	return args[:1]
}

// todoRunUsage is the one place the command's shape is written, so the
// refusal and the help cannot come to describe different commands.
const todoRunUsage = "Usage: /todo run [<slug>|--next|--all] [--no-commit] [--max <n>]"

// todoRunArgs is what follows `/todo run`: which item, and the answers the
// person gave about how it is worked.
type todoRunArgs struct {
	// arg is the slug, `--next`, or empty for the next ready item.
	arg string
	// noCommit ends each run after the review with the change in the tree.
	noCommit bool
	// all is the sprint: item after item, each in a session of its own.
	all bool
	// max bounds how many items the sprint starts, 0 for as many as are
	// ready.
	max int
}

// parseTodoRunArgs reads what follows `/todo run`. A word it does not know is
// refused rather than taken as the slug, because `/todo run --no-commmit`
// would otherwise start a committing run on an item named after the typo —
// which is the answer the person was trying to avoid.
func parseTodoRunArgs(args []string) (todoRunArgs, bool) {
	var out todoRunArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--no-commit":
			out.noCommit = true
		case a == "--all":
			out.all = true
		case a == "--max" || strings.HasPrefix(a, "--max="):
			value := strings.TrimPrefix(a, "--max=")
			if value == "--max" {
				i++
				if i >= len(args) {
					return todoRunArgs{}, false
				}
				value = args[i]
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return todoRunArgs{}, false
			}
			out.max = n
		case strings.HasPrefix(a, "-") && a != "--next":
			return todoRunArgs{}, false
		case out.arg == "":
			out.arg = a
		default:
			return todoRunArgs{}, false
		}
	}
	// A sprint takes its items from the ready list, so naming one alongside
	// it — or asking for the next one as well — is two different requests in
	// one command; and a cap on how many items are worked says nothing about
	// a run of a single item.
	if out.all && out.arg != "" {
		return todoRunArgs{}, false
	}
	if out.max > 0 && !out.all {
		return todoRunArgs{}, false
	}
	return out, true
}

// todoEditorDoneMsg is the editor's exit from an item file. Unlike the
// draft's editor, the file is the item itself and is never removed.
type todoEditorDoneMsg struct {
	slug string
	path string
	err  error
}

// openTodoEditor hands an item file to the editor. The same refusals as
// the draft's editor apply: the editor takes the terminal, and a turn or a
// decision in flight would be lost behind it.
func (m Model) openTodoEditor(slug string) (tea.Model, tea.Cmd) {
	if reason, refused := m.editorRefusal(); refused {
		return m.surfaceNotice(reason)
	}
	it, ok := m.todoStore.Find(slug)
	if !ok {
		return m.systemNotice(fmt.Sprintf("No backlog item %q; /todo lists them.", slug))
	}
	argv := editorArgv(editorCommand(), it.Path, 1, 1)
	proc := exec.Command(argv[0], argv[1:]...)
	return m, tea.ExecProcess(proc, func(err error) tea.Msg {
		return todoEditorDoneMsg{slug: slug, path: it.Path, err: err}
	})
}

// todoEditorFinished reloads the backlog after the editor exits and says
// what the edited file now reads as — including why it no longer loads, if
// that is what the edit did, since a file that silently dropped out of the
// list would look finished.
func (m Model) todoEditorFinished(msg todoEditorDoneMsg) (tea.Model, tea.Cmd) {
	m.reloadTodos()
	if msg.err != nil {
		return m.surfaceNotice("the editor exited with an error — " + msg.err.Error())
	}
	if _, err := os.Stat(msg.path); err != nil {
		return m.systemNotice(fmt.Sprintf("%s is gone; the backlog no longer has %s.", filepath.Base(msg.path), msg.slug))
	}
	it, err := todo.LoadFile(m.todos.Profile, msg.path)
	if err != nil {
		return m.systemNotice(fmt.Sprintf("%s does not load as an item now — %v. It stays on disk; fix the header and it comes back.", filepath.Base(msg.path), err))
	}
	m.signal(observe.SignalTodo, observe.TodoEdit)
	note := fmt.Sprintf("Saved %s: %s (%s, %s", it.Slug, it.Title, it.Priority, it.Status)
	if grade := it.Grade(); grade != "" {
		note += ", " + grade
	}
	note += ")."
	for _, w := range it.Warnings {
		note += "\nwarning: " + w
	}
	return m.systemNotice(note)
}

// todoSlugArgs completes an active item's slug for the /todo subcommands
// that take one.
func todoSlugArgs(m *Model) []argOption {
	if m.todoStore == nil {
		return nil
	}
	var out []argOption
	for _, it := range m.todoStore.Items {
		out = append(out, argOption{it.Slug, it.Title})
	}
	return out
}

// The backlog screen: bare /todo and the chord open it, and the keys on it
// resolve to acts this host carries out against the files
// (docs/interface/surfaces.md#the-backlog-screen). It is the same store
// every other surface here reads, re-read after anything that could have
// changed a file, because the backlog is files and not a copy of them.

// todoScreenWhy is what the screen's footer says while a turn is running.
// It is the refusal `/todo` gives in words, drawn as inert keys instead of
// waiting to be pressed and then refused: a key that looked live and was not
// is the thing invariant 5 exists to stop
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
const todoScreenWhy = "the turn is running; these change the files it may be working from"

// openTodoScreen puts the backlog up as a takeover. It can be opened during
// a turn: reading the backlog while the model works from it is exactly when
// the question gets asked, and the keys that would change a file are inert
// until the turn is over.
func (m Model) openTodoScreen() (tea.Model, tea.Cmd) {
	if !m.todosEnabled() {
		return m.systemNotice("The backlog is unavailable in this session.")
	}
	m.backlog = &components.BacklogScreen{Prose: todoProse, Plan: m.sprintPlan}
	m.backlog.Priority, m.backlog.Fields = todoScreenFieldSet(m.todos.Profile)
	m.reloadTodos()
	m.enterSurface(stateBacklog)
	return m, nil
}

// todoProse lays an item's sections out through the renderer the transcript
// lays an answer out with, fences and all. An item's body is prose somebody
// wrote, so a heading in an item and a heading in an answer are the same
// heading; a second renderer here would be a second design system.
func todoProse(src string, width int) []string {
	return markdown.Blocks(src, mdOptions(width))
}

// backlogPane renders the screen into the rectangle the pane left it.
//
// The turn's state is read here rather than at the opening because a turn can
// start while the screen is up — a queued follow-up going out, a child
// finishing — and the keys have to go grey the moment it does. Reading it per
// frame is a field on the session; the rows are not, and they are rebuilt
// only when something changed a file.
func (m Model) backlogPane(width, height int) string {
	m.backlog.ReadOnly = m.working()
	m.backlog.SetSize(width, height)
	return m.backlog.View(width)
}

// updateTodoScreen routes a key while the screen is up and carries out what
// it asked for.
func (m Model) updateTodoScreen(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.backlog == nil {
		m.shutTodoScreen()
		return m, nil
	}
	m.backlog.ReadOnly = m.working()
	done, result := m.backlog.Update(key)
	if done {
		m.shutTodoScreen()
		return m, nil
	}
	if result.Do == nil {
		return m, nil
	}
	return m.todoScreenAct(*result.Do)
}

// shutTodoScreen takes the screen down and hands the keyboard back to the
// turn, which may have moved on while the surface was up.
func (m *Model) shutTodoScreen() {
	m.backlog = nil
	m.leaveSurface()
	m.syncViewport()
}

// todoScreenAct carries out one of the screen's acts. Three of them leave
// with the screen: the editor takes the terminal, a run takes the session,
// and a new item is a card in the panel the screen is covering. The rest
// change one file and the screen stays up over fresh rows.
func (m Model) todoScreenAct(cmd components.BacklogCommand) (tea.Model, tea.Cmd) {
	switch cmd.Act {
	case components.BacklogEdit:
		m.shutTodoScreen()
		return m.openTodoEditor(cmd.Slug)
	case components.BacklogRun:
		m.shutTodoScreen()
		return m.startTodoRun(cmd.Slug, m.todos.NoCommit)
	case components.BacklogNew:
		m.shutTodoScreen()
		return m.composeTodoNew()
	case components.BacklogGroom:
		// Grooming leaves with the screen for the reason a run does: it
		// spends a turn, and its card is in the panel this screen covers.
		m.shutTodoScreen()
		return m.startTodoGroom([]string{cmd.Slug})
	case components.BacklogSprintTake:
		note := m.writeSprintPlan(cmd.Slugs, sprintFileGoal(m.backlog.Plan))
		m.backlog.Plan, m.sprintPlan = nil, nil
		m.reloadTodos()
		m.backlog.Notice = firstLine(note)
		return m.systemNotice(note)
	case components.BacklogSprintCancel:
		m.backlog.Plan, m.sprintPlan = nil, nil
		m.refreshTodoScreen()
		return m.systemNotice("Nothing written; no sprint was planned.")
	case components.BacklogSprintGoal:
		// A goal is a sentence and the card has nowhere to type one, so the
		// key hands the keyboard back with the command already in the box —
		// the same handover the new-item key makes.
		m.shutTodoScreen()
		return m.composeSprintGoal()
	}
	note := m.todoScreenVerb(cmd)
	m.reloadTodos()
	if m.backlog == nil {
		return m.systemNotice(note)
	}
	// The screen says what happened in one line and the transcript keeps the
	// whole answer, which is the answer the same verb typed into the input
	// would have left: a change made from a surface must be in the record of
	// the session, or closing the surface loses it.
	m.backlog.Notice = firstLine(note)
	return m.systemNotice(note)
}

// composeTodoNew is what `n` leaves behind: the command in the draft box with
// the cursor after it, waiting for the sentence. A draft is made from a
// sentence and the screen has nowhere to type one, so the key hands the
// keyboard back with the question already asked rather than opening a card
// with nothing in it. A draft already in the box is not thrown away for it.
func (m Model) composeTodoNew() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.input.Value()) != "" {
		return m.systemNotice("There is a draft in the input; " + todoNewUsage + " once it is sent or cleared.")
	}
	m.input.SetValue(todoNewPrefix)
	m.input.MoveToEnd()
	m.syncCompletions()
	m.syncViewport()
	return m, nil
}

// todoScreenVerb is the act as the verb it is. Every one of them goes
// through the same handler `/todo` and `shhh todo` go through, so a refusal
// on this screen is the refusal those give and a confirmation is their
// sentence — two implementations of "archive an item" would be two answers
// to what archiving is.
func (m Model) todoScreenVerb(cmd components.BacklogCommand) string {
	switch cmd.Act {
	case components.BacklogBlock:
		return m.todos.Manage([]string{"block", cmd.Slug})
	case components.BacklogArchive:
		return m.todos.Manage([]string{"done", cmd.Slug})
	case components.BacklogDrop:
		return m.todos.Manage([]string{"drop", cmd.Slug})
	case components.BacklogSprintAdd:
		return m.todos.Manage([]string{"sprint", "add", cmd.Slug})
	case components.BacklogSprintDrop:
		return m.todos.Manage([]string{"sprint", "drop", cmd.Slug})
	case components.BacklogReopen:
		return m.todoReopen(cmd.Slug)
	}
	return ""
}

// todoReopen is the one act with two meanings, because the done tab put a
// second kind of row under the same key: an active item that was blocked
// goes back to open through the verb, and an archived one comes back out of
// the archive, which is a move no verb has.
//
// The move needs no check against a run holding the item. A run works an
// active item and archives it at the end, so an item in the archive is one
// no run still has in flight.
func (m Model) todoReopen(slug string) string {
	if it, ok := m.todoStore.Find(slug); ok && it.Archived {
		to, err := todo.Reopen(m.todos.Root, slug)
		if err != nil {
			return "Error: " + err.Error()
		}
		return fmt.Sprintf("Reopened %s; the file is back in the backlog at %s.", slug, to)
	}
	return m.todos.Manage([]string{"open", slug})
}

// refreshTodoScreen rebuilds the screen's rows from the store. It is called
// after every act rather than rebuilding the screen, because the pointer,
// the filters and the tab the reader is on have to survive a change to one
// file.
func (m Model) refreshTodoScreen() {
	if m.backlog == nil {
		return
	}
	s := m.todoStore
	m.backlog.Rows = m.todoScreenRows(s, false)
	m.backlog.Done = m.todoScreenRows(s, true)
	m.backlog.Board = m.sprintBoard()
	m.backlog.Sprint = ""
	if s != nil && s.Sprint.Open() {
		m.backlog.Sprint = s.Sprint.Name
	}
	m.backlog.ReadOnly = m.working()
	m.backlog.Why = todoScreenWhy
}

// todoScreenFieldSet is the project's vocabulary as the screen filters and
// letters it: the field that orders the list, then the rest in the order
// the profile declares them.
func todoScreenFieldSet(p todo.Profile) (components.BacklogField, []components.BacklogField) {
	var priority components.BacklogField
	var fields []components.BacklogField
	for _, f := range p.Fields {
		screen := components.BacklogField{Name: f.Name}
		for _, v := range f.Values {
			screen.Values = append(screen.Values, components.BacklogValue{Word: v.Name, Glyph: v.Glyph})
		}
		if f.Orders() {
			priority = screen
			continue
		}
		fields = append(fields, screen)
	}
	return priority, fields
}

// todoScreenRows is one tab's rows: the active backlog in its working order,
// or the archive. The files that would not parse go on the end of whichever
// directory they were found in — a row rather than a gap, because the file
// is still there and a list that dropped it would say the work is gone.
func (m Model) todoScreenRows(s *todo.Store, archived bool) []components.BacklogRow {
	if s == nil {
		return nil
	}
	items := s.Items
	if archived {
		items = s.Done
	}
	rows := make([]components.BacklogRow, 0, len(items))
	for _, it := range items {
		rows = append(rows, m.todoScreenRow(s, it))
	}
	for _, u := range s.Unreadable {
		if u.Archived != archived {
			continue
		}
		rows = append(rows, components.BacklogRow{
			Slug: u.Slug, Path: u.Path, Reason: u.Reason,
			State: components.BacklogUnreadable,
		})
	}
	return rows
}

// todoScreenRow is one item as the screen draws it: the header's fields in
// the interface's own words, both ends of every dependency edge it is on,
// and the prose under it.
func (m Model) todoScreenRow(s *todo.Store, it todo.Item) components.BacklogRow {
	row := components.BacklogRow{
		Slug: it.Slug, Path: it.Path, Title: it.Title,
		Priority: string(it.Priority), Values: it.Fields,
		Status: todoStatusWords(it.Status),
		State:  todoScreenState(s, it), Body: it.Body,
		Waits: s.Waiting(it), Blocks: todoBlockedBy(s, it.Slug),
		Warnings: it.Warnings,
	}
	// A reading the tree has left behind is exactly what a warning is for
	// on this screen — something odd about the file that does not stop it
	// loading — and it says how far behind, because "stale" without a count
	// is a word nobody can act on.
	if note := m.groomStaleNote(it.Slug); note != "" {
		row.Warnings = append(row.Warnings, note+"; "+keys.Bracket(keys.Backlog.Groom)+" reads it against the tree again")
	}
	row.Fields = todoScreenFields(it, row.Status)
	if it.Archived {
		// The archive's body is the report, because what the done tab is for
		// is reading what shipped and how — the criteria were the question,
		// and the report is the answer. An item archived by hand has none,
		// and its own body is the honest thing to show instead.
		if report := todo.ItemReport(it); report != "" {
			row.Body = report
		} else {
			row.Fields = append(row.Fields, "no report")
		}
	}
	row.InSprint = s.Sprint.Open() && slices.Contains(s.Sprint.Slugs, it.Slug)
	return row
}

// todoScreenFields is the compact row above an item's body: the header's
// own fields in the project's order, where the item stands, and when it was
// written. The file has them one per line, which is right for a file;
// beside a list they are a row.
//
// The grade carries its field's name and an ungraded item says so, because
// a bare letter off a scale is not a fact and a missing one is worth
// stating: it is what a run reads to decide how much ceremony to spend.
func todoScreenFields(it todo.Item, status string) []string {
	fields := []string{}
	for _, f := range it.Profile.Fields {
		switch {
		case f.Orders():
			fields = append(fields, string(it.Priority))
		case f.Name == it.Profile.Grade:
			if grade := it.Fields[f.Name]; grade != "" {
				fields = append(fields, f.Name+" "+grade)
			} else {
				fields = append(fields, "ungraded")
			}
		case it.Fields[f.Name] != "":
			fields = append(fields, it.Fields[f.Name])
		}
	}
	fields = append(fields, status)
	if it.Created != "" {
		fields = append(fields, "written "+it.Created)
	}
	return fields
}

// todoStatusWords is a status as the interface says it. The file spells the
// working state with a hyphen because it is a value in a header; a screen
// that filtered on "in-progress" would be showing the reader the file format
// rather than the state.
func todoStatusWords(s todo.Status) string {
	if s == todo.StatusInProgress {
		return "in progress"
	}
	return string(s)
}

// todoScreenState is which glyph and which state field a row gets.
func todoScreenState(s *todo.Store, it todo.Item) components.BacklogState {
	if it.Archived {
		return components.BacklogArchived
	}
	switch it.Status {
	case todo.StatusBlocked:
		return components.BacklogBlocked
	case todo.StatusInProgress:
		return components.BacklogRunning
	}
	if len(s.Waiting(it)) > 0 {
		return components.BacklogWaiting
	}
	return components.BacklogReady
}

// todoBlockedBy names the active items whose dependencies name this slug. It
// is the other end of the edge the listing has always shown one end of, and
// it is what decides whether finishing this item is worth anything.
func todoBlockedBy(s *todo.Store, slug string) []string {
	var out []string
	for _, it := range s.Items {
		if slices.Contains(it.DependsOn, slug) {
			out = append(out, it.Slug)
		}
	}
	return out
}

// renderTodoScreenHint fills the input area while the backlog has the
// screen. The surface's own footer carries the keys; this says what is up.
func (m Model) renderTodoScreenHint() string {
	return sty.SystemMsg.Render("backlog · "+keys.Bracket(keys.Backlog.Back)+" "+
		keys.Words(keys.Backlog.Back)) + strings.Repeat("\n", inputHeight-1)
}
