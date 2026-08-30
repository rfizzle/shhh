package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func todoTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := todo.Dir(root)
	if err := os.MkdirAll(filepath.Join(dir, todo.DoneSubdir), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a-high.md":    "---\ntitle: High one\npriority: high\nsize: M\n---\n",
		"b-waits.md":   "---\ntitle: Waits\ndepends_on: [a-high, c-blocked]\n---\n",
		"c-blocked.md": "---\ntitle: Stuck\nstatus: blocked\n---\n",
		"d-ready.md":   "---\ntitle: Ready\n---\n",
		"e-more.md":    "---\ntitle: Fifth\npriority: low\n---\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func todoModel(t *testing.T, root string) Model {
	t.Helper()
	m := frameModel(t, 130, 40)
	manage := func(args []string) string { return "managed " + strings.Join(args, " ") }
	return m.WithTodos(Todos{Root: root, Manage: manage, Detail: func(_ *todo.Store, it todo.Item) string { return "detail " + it.Slug }})
}

func TestInspectorTodo_RowsInWorkingOrderAndCounts(t *testing.T) {
	m := todoModel(t, todoTestRoot(t))
	block := m.inspectorTodo()
	if block == nil {
		t.Fatal("a backlog should put a TODO block in the rail")
	}
	if block.Open != 4 || block.Blocked != 1 || block.More != 1 || len(block.Rows) != todoRailRows {
		t.Fatalf("counts = %+v", block)
	}
	got := []string{}
	for _, r := range block.Rows {
		got = append(got, r.Slug+":"+r.Note)
	}
	want := "a-high: b-waits:needs a-high +1 c-blocked:blocked d-ready:"
	if strings.Join(got, " ") != want {
		t.Fatalf("rows = %q, want %q", strings.Join(got, " "), want)
	}
	if block.Rows[2].State != components.TodoBlocked || block.Rows[1].State != components.TodoWaiting || block.Rows[0].State != components.TodoReady {
		t.Fatalf("states = %+v", block.Rows)
	}
	rail := ansi.Strip(m.inspectorData().View(components.InspectorWidth, 0))
	for _, want := range []string{"TODO", "project · 4 open · 1 blocked", "H M a-high", "… 1 more", todoHintRail} {
		if !strings.Contains(rail, want) {
			t.Fatalf("the rail should contain %q, got:\n%s", want, rail)
		}
	}
}

func TestInspectorTodo_NoBacklogNoBlock(t *testing.T) {
	m := todoModel(t, t.TempDir())
	if m.inspectorTodo() != nil {
		t.Fatal("an empty backlog should draw no block")
	}
	plain := frameModel(t, 130, 40)
	if plain.inspectorTodo() != nil || plain.todosEnabled() {
		t.Fatal("a session without a backlog wired should draw no block")
	}
}

func TestTodoCommand_PickerAndSubcommands(t *testing.T) {
	m := todoModel(t, todoTestRoot(t))
	m.input.SetValue("/todo")
	updated, _ := m.submitInput()
	next := updated.(Model)
	if next.picker == nil || next.state != statePick {
		t.Fatal("bare /todo should open the picker")
	}
	if len(next.pickerAll) != 5 || next.pickerAll[0].Label != "a-high" || next.pickerAll[1].Value != "waits on a-high, c-blocked" {
		t.Fatalf("picker rows = %+v", next.pickerAll)
	}
	note := next.pickerApply(&next, 3, false)
	if note != "detail d-ready" {
		t.Fatalf("enter should show the item, got %q", note)
	}

	m.input.SetValue("/todo block d-ready why")
	updated, _ = m.submitInput()
	next = updated.(Model)
	last := next.transcript[len(next.transcript)-1]
	if last.kind != entrySystem || last.text != "managed block d-ready why" {
		t.Fatalf("subcommand note = %+v", last)
	}

	empty := todoModel(t, t.TempDir())
	empty.input.SetValue("/todo")
	updated, _ = empty.submitInput()
	next = updated.(Model)
	if next.picker != nil || next.transcript[len(next.transcript)-1].text != "managed " {
		t.Fatal("bare /todo with nothing to pick should fall through to the listing")
	}
}

func TestTodoCommand_SubcommandReloadsTheStore(t *testing.T) {
	root := todoTestRoot(t)
	m := frameModel(t, 130, 40)
	m = m.WithTodos(Todos{Root: root, Manage: func(args []string) string {
		_ = todo.SetStatus(filepath.Join(todo.Dir(root), "d-ready.md"), todo.StatusBlocked)
		return "changed"
	}, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m.input.SetValue("/todo block d-ready")
	updated, _ := m.submitInput()
	next := updated.(Model)
	if next.todoStore.Count(todo.StatusBlocked) != 2 {
		t.Fatal("the store should be re-read after a subcommand")
	}
}

func TestTodoEditor_ReloadsAndReportsTheFile(t *testing.T) {
	root := todoTestRoot(t)
	m := todoModel(t, root)
	path := filepath.Join(todo.Dir(root), "d-ready.md")

	if err := os.WriteFile(path, []byte("---\ntitle: Ready now\nsize: xl\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.todoEditorFinished(todoEditorDoneMsg{slug: "d-ready", path: path})
	next := updated.(Model)
	last := next.transcript[len(next.transcript)-1].text
	if !strings.HasPrefix(last, "Saved d-ready: Ready now (medium, open).") || !strings.Contains(last, `unknown size "XL"`) {
		t.Fatalf("note = %q", last)
	}
	if it, _ := next.todoStore.Find("d-ready"); it.Title != "Ready now" {
		t.Fatal("the store was not re-read after the editor")
	}

	if err := os.WriteFile(path, []byte("no header\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, _ = m.todoEditorFinished(todoEditorDoneMsg{slug: "d-ready", path: path})
	next = updated.(Model)
	last = next.transcript[len(next.transcript)-1].text
	if !strings.Contains(last, "does not load as an item now") || !strings.Contains(last, "stays on disk") {
		t.Fatalf("broken-header note = %q", last)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the item file must never be removed by the editor path")
	}

	os.Remove(path)
	updated, _ = m.todoEditorFinished(todoEditorDoneMsg{slug: "d-ready", path: path})
	next = updated.(Model)
	if !strings.Contains(next.transcript[len(next.transcript)-1].text, "is gone") {
		t.Fatal("a deleted file should be reported")
	}
}

func TestTodoCommand_MutationsRefusedMidTurn(t *testing.T) {
	m := todoModel(t, todoTestRoot(t))
	m.setTurnState(stateStreaming)
	for _, text := range []string{"/todo done d-ready", "/todo drop d-ready", "/todo edit d-ready", "/todo add x"} {
		m.input.SetValue(text)
		updated, cmd := m.submitInput()
		next := updated.(Model)
		last := next.transcript[len(next.transcript)-1].text
		if cmd != nil || !strings.Contains(last, "Not while the turn is running") {
			t.Fatalf("%s mid-turn: note = %q", text, last)
		}
	}
	m.input.SetValue("/todo show d-ready")
	updated, _ := m.submitInput()
	if last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text; last != "managed show d-ready" {
		t.Fatalf("reading mid-turn should still work, got %q", last)
	}
}

func TestTodoCommand_EditArityAndUnknownSlug(t *testing.T) {
	m := todoModel(t, todoTestRoot(t))
	for text, want := range map[string]string{"/todo edit": "Usage: /todo edit <slug>", "/todo edit a b": "Usage: /todo edit <slug>", "/todo edit nope": `No backlog item "nope"`} {
		m.input.SetValue(text)
		updated, cmd := m.submitInput()
		last := updated.(Model).transcript[len(updated.(Model).transcript)-1].text
		if cmd != nil || !strings.Contains(last, want) {
			t.Fatalf("%s: note = %q, want %q", text, last, want)
		}
	}
	if opts := todoSlugArgs(&m); len(opts) != 5 || opts[0].value != "a-high" || opts[0].desc != "High one" {
		t.Fatalf("slug completion = %+v", opts)
	}
}

func TestTodo_TurnEndReloadsTheStore(t *testing.T) {
	root := todoTestRoot(t)
	m := todoModel(t, root)
	m.setTurnState(stateStreaming)
	m.turnStarted = time.Now()
	if err := todo.SetStatus(filepath.Join(todo.Dir(root), "d-ready.md"), todo.StatusBlocked); err != nil {
		t.Fatal(err)
	}
	m.setTurnState(stateInput)
	if m.todoStore.Count(todo.StatusBlocked) != 2 {
		t.Fatal("a turn ending should re-read the backlog")
	}
	if row := todoRow(m.todoStore, todo.Item{Slug: "x", Priority: todo.PriorityLow}, nil); row.Size != "-" || row.Priority != "L" {
		t.Fatalf("ungraded row = %+v", row)
	}
}

func TestTodoEditor_RefusedWhileWorking(t *testing.T) {
	m := todoModel(t, todoTestRoot(t))
	m.setTurnState(stateStreaming)
	updated, cmd := m.openTodoEditor("d-ready")
	if cmd != nil {
		t.Fatal("the editor must not be launched over a running turn")
	}
	if !strings.Contains(lipgloss.NewStyle().Render(updated.(Model).transcript[len(updated.(Model).transcript)-1].text), "not while the turn is running") {
		t.Fatal("the refusal should say why")
	}
}
