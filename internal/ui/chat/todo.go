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
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
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
	// Extractor reads the session into proposed items behind a bare
	// /todo add. Nil, or disabled, leaves only the by-hand form.
	Extractor *todo.Extractor
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
	m.todoStore = todo.Load(m.todos.Root)
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
		t.Rows = append(t.Rows, todoRow(s, it, m.todoRunner.state))
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
		Size:     string(it.Size),
	}
	if row.Size == "" {
		row.Size = "-"
	}
	switch it.Status {
	case todo.StatusBlocked:
		row.State, row.Note = components.TodoBlocked, "blocked"
	case todo.StatusInProgress:
		row.State, row.Note = components.TodoRunning, "in progress"
		if running != nil && running.Slug == it.Slug {
			row.Note = string(running.Stage)
			if running.Round > 0 {
				row.Note += fmt.Sprintf(" %d/%d", running.Round, run.Rounds(running.Size))
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

// todoCommand is /todo from the input. Bare /todo opens the picker where
// there is something to pick; edit hands an item to the editor; everything
// else is textual and reloads the store afterwards, because it may have
// changed a file.
func (m Model) todoCommand(parts []string) (tea.Model, tea.Cmd) {
	if !m.todosEnabled() {
		return m.systemNotice("The backlog is unavailable in this session.")
	}
	if len(parts) == 1 {
		if picked, cmd, ok := m.openTodoPick(); ok {
			return picked, cmd
		}
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
	// Planning is the one sprint verb the session takes rather than the
	// manager: it proposes a set and writes nothing until the card is
	// accepted. Every other sprint verb is textual and goes through the
	// manager below, so a script gets the same words.
	if len(parts) >= 3 && parts[1] == "sprint" && parts[2] == "plan" {
		return m.startTodoSprintPlan(parts[3:])
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
	case "edit", "add", "block", "open", "done", "drop", "run", "stop":
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

// openTodoPick opens the backlog picker: every active item in working order,
// with its state where the picker draws a value. Enter shows the item in
// the transcript. It reports false with nothing to pick, so bare /todo
// falls through to the listing and its "no backlog" sentence.
func (m Model) openTodoPick() (tea.Model, tea.Cmd, bool) {
	s := m.todoStore
	if s == nil || s.Len() == 0 {
		return m, nil, false
	}
	items := s.Items
	opts := make([]components.SelectOption, len(items))
	for i, it := range items {
		size := string(it.Size)
		if size == "" {
			size = "-"
		}
		opts[i] = components.SelectOption{
			Label: it.Slug,
			Value: todoPickState(s, it),
			Desc:  fmt.Sprintf("%s · %s · %s", it.Priority, size, it.Title),
		}
	}
	model, cmd := m.openSearchPicker("Backlog — enter shows an item; /todo edit <slug> opens it", opts, 0, func(m *Model, idx int) (string, tea.Cmd) {
		return m.todos.Detail(s, items[idx]), nil
	})
	return model, cmd, true
}

// todoPickState is the picker row's value column: the status, or for an
// open item whether it is ready and what it waits on.
func todoPickState(s *todo.Store, it todo.Item) string {
	if it.Status != todo.StatusOpen {
		return string(it.Status)
	}
	if waiting := s.Waiting(it); len(waiting) > 0 {
		return "waits on " + strings.Join(waiting, ", ")
	}
	return "ready"
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
	it, err := todo.LoadFile(msg.path)
	if err != nil {
		return m.systemNotice(fmt.Sprintf("%s does not load as an item now — %v. It stays on disk; fix the header and it comes back.", filepath.Base(msg.path), err))
	}
	note := fmt.Sprintf("Saved %s: %s (%s, %s", it.Slug, it.Title, it.Priority, it.Status)
	if it.Size != "" {
		note += ", " + string(it.Size)
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
