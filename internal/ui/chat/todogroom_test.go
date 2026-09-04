package chat

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The item the grooming tests read: two criteria and a note, so a reading
// has a criterion to tick and a sentence to restate.
const groomItem = `---
title: Give the cache a lifetime
priority: high
size: M
---

## Acceptance criteria
- [ ] internal/cache/store.go:88 takes the lifetime from the config
- [ ] The reader drops an entry past its age

## Notes
Today the reader serves a stale entry rather than refusing.
`

// The answer a reading comes back with: one correction, one criterion the
// tree already satisfies, and one claim that holds.
const groomAnswer = `claim: - [ ] internal/cache/store.go:88 takes the lifetime from the config
verdict: moved
now: - [ ] internal/cache/reader.go:120 takes the lifetime from the config
evidence: the constructor moved to reader.go in 9f2a11c

claim: - [ ] The reader drops an entry past its age
verdict: holds
evidence: reader.go:44 still does it

claim: Today the reader serves a stale entry rather than refusing.
verdict: changed
now: Today the reader refuses a stale entry.
evidence: reader.go:52 returns ErrStale
`

func groomModel(t *testing.T, items map[string]string) (Model, string) {
	t.Helper()
	root := t.TempDir()
	dir := todo.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range items {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := frameModel(t, 130, 40)
	m.changes = changeset.New(1 << 20)
	m.policy.mode = agent.ModeManual
	m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "" },
		Detail: func(*todo.Store, todo.Item) string { return "" }})
	return m, root
}

// groom sends /todo groom and ends its turn with the answer, leaving the
// card up.
func groom(t *testing.T, m Model, command, answerText string) Model {
	t.Helper()
	m.input.SetValue(command)
	updated, _ := m.submitInput()
	m = updated.(Model)
	if !m.working() || m.policy.mode != agent.ModePlan {
		t.Fatalf("the reading should be in flight in plan mode: mode=%s working=%t", m.policy.mode, m.working())
	}
	return answer(t, m, answerText)
}

func TestTodoGroom_TheCardIsTheProposedLinesAndTheStamp(t *testing.T) {
	m, _ := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
	m = groom(t, m, "/todo groom cache-ttl", groomAnswer)
	if m.state != stateTodoGroom || m.todoGroom == nil {
		t.Fatalf("no card: state = %d", m.state)
	}
	// Two corrections and the stamp; the claim that holds proposes nothing
	// and is counted in the title instead.
	if got := len(m.todoGroom.Options); got != 3 {
		t.Fatalf("rows = %d: %+v", got, m.todoGroom.Options)
	}
	if !strings.Contains(m.todoGroom.Title, "1 holds") {
		t.Errorf("title = %q", m.todoGroom.Title)
	}
	for i, c := range m.todoGroom.Checked {
		if !c {
			t.Errorf("row %d starts unchecked", i)
		}
	}
}

func TestTodoGroom_AcceptingWritesTheNamedLinesAndTheStamp(t *testing.T) {
	var signals []string
	m, root := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
	m = m.WithObserver(observe.Observer{Signal: func(_ observe.Pos, code, reason string) {
		signals = append(signals, code+":"+reason)
	}})
	m = groom(t, m, "/todo groom cache-ttl", groomAnswer)
	m = press(t, m, "enter")

	it, ok := todo.Load(todo.BuiltinCode(), root).Find("cache-ttl")
	if !ok {
		t.Fatal("the item is gone")
	}
	if !strings.Contains(it.Body, "reader.go:120") || strings.Contains(it.Body, "store.go:88") {
		t.Errorf("the moved reference was not rewritten:\n%s", it.Body)
	}
	if !strings.Contains(it.Body, "Today the reader refuses a stale entry.") {
		t.Errorf("the restated sentence is missing:\n%s", it.Body)
	}
	if it.Groomed == "" {
		t.Error("the header was not stamped")
	}
	// The stamp is what makes the reading one a run can be handed.
	if block := todo.GroomingBlock(root, "cache-ttl"); !strings.Contains(block, "moved") {
		t.Errorf("the accepted reading was not written down: %q", block)
	}
	// The record says how many of the proposed lines were accepted, which is
	// the whole point of counting them: how often the backlog was wrong.
	want := observe.SignalTodo + ":" + observe.GroomReason(3)
	if !slices.Contains(signals, want) {
		t.Errorf("signals = %v, want one of them %q", signals, want)
	}
	if m.policy.mode != agent.ModeManual {
		t.Errorf("the mode was not restored: %s", m.policy.mode)
	}
}

func TestTodoGroom_EscWritesNothing(t *testing.T) {
	m, root := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
	m = groom(t, m, "/todo groom cache-ttl", groomAnswer)
	m = press(t, m, "esc")
	if m.state == stateTodoGroom {
		t.Fatal("the card is still up")
	}
	data, err := os.ReadFile(filepath.Join(todo.Dir(root), "cache-ttl.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != groomItem {
		t.Errorf("esc wrote something:\n%s", data)
	}
	if todo.GroomingBlock(root, "cache-ttl") != "" {
		t.Error("a declined reading was written down")
	}
}

// --all works the backlog in order with a card each, and esc stops the pass
// while keeping what an earlier card already accepted.
func TestTodoGroom_AllStopsOnEscKeepingWhatWasAccepted(t *testing.T) {
	m, root := groomModel(t, map[string]string{
		"cache-ttl.md":  groomItem,
		"cache-warm.md": strings.Replace(groomItem, "priority: high", "priority: low", 1),
	})
	m = groom(t, m, "/todo groom --all", groomAnswer)
	if m.todoGroomer.item.Slug != "cache-ttl" {
		t.Fatalf("backlog order not kept: %q first", m.todoGroomer.item.Slug)
	}
	m = press(t, m, "enter")
	if !m.working() || m.todoGroomer.item.Slug != "cache-warm" {
		t.Fatalf("the pass did not carry on: working=%t on %q", m.working(), m.todoGroomer.item.Slug)
	}
	m = answer(t, m, groomAnswer)
	m = press(t, m, "esc")
	if m.todoGroomer.going() {
		t.Error("esc did not stop the pass")
	}
	s := todo.Load(todo.BuiltinCode(), root)
	if first, _ := s.Find("cache-ttl"); first.Groomed == "" {
		t.Error("the accepted item lost its stamp")
	}
	if second, _ := s.Find("cache-warm"); second.Groomed != "" {
		t.Error("the declined item was written")
	}
}

// The rail and the screen say how far behind a reading has fallen, and say
// nothing at all about an item nobody has read that way.
func TestTodoGroom_StaleIsDrawnOnlyForAnItemThatWasRead(t *testing.T) {
	m, _ := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
	m.todoGroomer.stale = map[string]int{"cache-ttl": 62}
	rail := m.inspectorTodo()
	if rail == nil || len(rail.Rows) != 1 {
		t.Fatalf("rail = %+v", rail)
	}
	if !rail.Rows[0].Stale || !strings.Contains(rail.Rows[0].Note, "62") {
		t.Errorf("row = %+v", rail.Rows[0])
	}
	row := m.todoScreenRow(m.todoStore, m.todoStore.Items[0])
	if len(row.Warnings) != 1 || !strings.Contains(row.Warnings[0], "62 commits ago") {
		t.Errorf("warnings = %v", row.Warnings)
	}
	m.todoGroomer.stale = nil
	if rail := m.inspectorTodo(); rail.Rows[0].Stale {
		t.Error("an item nobody read is not stale")
	}
}

// The screen's [g] is the session's own act, taken through the same handler
// a typed command reaches.
func TestTodoGroom_TheScreenKeyStartsTheReading(t *testing.T) {
	m, _ := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
	opened, _ := m.openTodoScreen()
	m = opened.(Model)
	m = press(t, m, keys.Shown(keys.Backlog.Groom))
	if m.state == stateBacklog {
		t.Fatal("the screen should have closed for the reading")
	}
	if !m.todoGroomer.going() || m.todoGroomer.slug != "cache-ttl" {
		t.Fatalf("groomer = %+v", m.todoGroomer)
	}
}

// A turn that is not the reading's stops the pass rather than being graded
// as its answer.
func TestTodoGroom_ADisplacedTurnStopsThePass(t *testing.T) {
	m, root := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
	m.input.SetValue("/todo groom cache-ttl")
	updated, _ := m.submitInput()
	m = updated.(Model)
	m.turnCount++
	m = answer(t, m, groomAnswer)
	if m.todoGroomer.going() || m.state == stateTodoGroom {
		t.Fatal("the pass should have stopped")
	}
	data, _ := os.ReadFile(filepath.Join(todo.Dir(root), "cache-ttl.md"))
	if string(data) != groomItem {
		t.Errorf("a displaced turn wrote something:\n%s", data)
	}
}

// The tally a pass over several items leaves counts every line it wrote,
// which is the pass's own record and not the last card's.
func TestTodoGroom_AllTalliesWhatThePassWrote(t *testing.T) {
	m, _ := groomModel(t, map[string]string{
		"cache-ttl.md":  groomItem,
		"cache-warm.md": strings.Replace(groomItem, "priority: high", "priority: low", 1),
	})
	m = groom(t, m, "/todo groom --all", groomAnswer)
	m = press(t, m, "enter")
	m = answer(t, m, groomAnswer)
	m = press(t, m, "enter")
	if m.todoGroomer.going() {
		t.Fatal("the pass should be over")
	}
	if note := lastSystem(t, m); !strings.Contains(note, "6 lines") || !strings.Contains(note, "2 items") {
		t.Errorf("tally = %q", note)
	}
}

// The session boundary takes the pass with the conversation, and puts the
// mode back: plan mode was the reading's and the reading is gone.
func TestTodoGroom_TheSessionBoundaryDropsThePass(t *testing.T) {
	m, _ := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
	m = groom(t, m, "/todo groom cache-ttl", groomAnswer)
	if m.state != stateTodoGroom {
		t.Fatalf("no card: state = %d", m.state)
	}
	m.startNewSession()
	if m.todoGroomer.going() || m.todoGroom != nil || m.state == stateTodoGroom {
		t.Errorf("the pass survived the boundary: %+v", m.todoGroomer)
	}
	if m.policy.mode != agent.ModeManual {
		t.Errorf("the mode was not restored: %s", m.policy.mode)
	}
}
