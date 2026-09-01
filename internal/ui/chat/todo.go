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
	"strings"

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
	for _, it := range s.Items {
		if len(t.Rows) == todoRailRows {
			t.More++
			continue
		}
		t.Rows = append(t.Rows, todoRow(s, it, m.todoRun))
	}
	return t
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
	// Reading is fine mid-turn; changing a file the model may be editing
	// at the same moment is not, and neither is suspending the program
	// under a running turn for the editor.
	if len(parts) >= 2 && parts[1] == "status" {
		return m.systemNotice(m.todoRunStatus())
	}
	if len(parts) >= 2 && m.working() {
		switch parts[1] {
		case "edit", "add", "block", "open", "done", "drop", "run", "stop":
			return m.systemNotice("Not while the turn is running: /todo " + parts[1] + " changes the backlog files the model may be working from. /todo and /todo show still read.")
		}
	}
	if len(parts) == 2 && parts[1] == "add" {
		return m.startTodoExtract()
	}
	if len(parts) >= 2 && parts[1] == "run" {
		arg := ""
		if len(parts) == 3 {
			arg = parts[2]
		}
		return m.startTodoRun(arg)
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
	model, cmd := m.openSearchPicker("Backlog — enter shows an item; /todo edit <slug> opens it", opts, 0, func(m *Model, idx int) string {
		return m.todos.Detail(s, items[idx])
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
