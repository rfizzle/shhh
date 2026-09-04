package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/todo"
)

const draftFixture = `{"items": [
	{"title": "Give the cache a lifetime", "kind": "story", "priority": "high", "size": "M",
	 "story": "As a user, I want entries to expire so that stale answers stop being served.",
	 "acceptance_criteria": ["An entry past its lifetime is a miss"], "tasks": ["read the setting"],
	 "tests": ["go test ./internal/cache"], "notes": ["the default is an hour"],
	 "depends_on": ["a-high", "nothing-like-this"]}
]}`

// draftModel is a session with a backlog and a drafter, and a recorder for
// the signals it leaves in the record.
func draftModel(t *testing.T, root string, p *scriptedProvider, signals *[]string) Model {
	t.Helper()
	m := frameModel(t, 130, 40)
	m.sessionName = "2026-09-04 09:00:00"
	var dr *todo.Drafter
	if p != nil {
		dr = todo.NewDrafter(p, todo.ExtractConfig{Model: "m"}, todo.BuiltinCode())
	}
	m = m.WithTodos(Todos{Profile: todo.BuiltinCode(), Root: root, Manage: func([]string) string { return "usage" },
		Detail: func(*todo.Store, todo.Item) string { return "" }, Drafter: dr})
	if signals != nil {
		m = m.WithObserver(observe.Observer{Signal: func(_ observe.Pos, code, reason string) {
			*signals = append(*signals, code+":"+reason)
		}})
	}
	return m
}

// runDraft submits /todo new and delivers the drafting.
func runDraft(t *testing.T, m Model) Model {
	t.Helper()
	m.input.SetValue("/todo new the cache never expires anything")
	updated, cmd := m.submitInput()
	next := updated.(Model)
	if cmd == nil || !next.todoDrafting {
		t.Fatal("/todo new <sentence> should start a drafting")
	}
	if last := next.transcript[len(next.transcript)-1].text; !strings.HasPrefix(last, "Drafting the item") {
		t.Fatalf("start note = %q", last)
	}
	updated, _ = next.Update(cmd())
	return updated.(Model)
}

func pressKeys(t *testing.T, m Model, keys ...tea.KeyPressMsg) Model {
	t.Helper()
	for _, k := range keys {
		updated, _ := m.Update(k)
		m = updated.(Model)
	}
	return m
}

var (
	keySpace = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func TestTodoNew_TheCardWritesTheItem(t *testing.T) {
	root := todoTestRoot(t)
	var signals []string
	p := &scriptedProvider{args: draftFixture}
	m := runDraft(t, draftModel(t, root, p, &signals))
	if m.state != stateTodoDraft || m.todoDraft == nil {
		t.Fatalf("the card should be showing: state=%d", m.state)
	}
	// The sentence is what the drafting was given, and it travels as data.
	if !strings.Contains(p.prompt, "the cache never expires anything") || !strings.Contains(p.prompt, "a-high — High one") {
		t.Fatalf("request = %q", p.prompt)
	}
	card := strings.Join(m.todoDraftLines(), "\n")
	for _, want := range []string{
		"Give the cache a lifetime", "give-the-cache-a-lifetime",
		"kind", "story", "priority", "high", "size", "M",
		"waits on", "a-high", "An entry past its lifetime is a miss",
		"nothing in the backlog is named nothing-like-this",
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("the card lacks %q:\n%s", want, card)
		}
	}

	// The header is set in place: down to the size row and one step of the
	// scale, which is M → L.
	m = pressKeys(t, m, keyDown, keyDown, keySpace)
	if m.todoDraft.proposal.Fields["size"] != "L" {
		t.Fatalf("size = %q, want L", m.todoDraft.proposal.Fields["size"])
	}
	m = pressKeys(t, m, keyEnter)
	if m.state != stateInput || m.todoDraft != nil {
		t.Fatalf("enter should close the card, state=%d", m.state)
	}
	note := m.transcript[len(m.transcript)-1].text
	if !strings.HasPrefix(note, "Wrote give-the-cache-a-lifetime to") {
		t.Fatalf("note = %q", note)
	}

	// Every field is on the file the store reads back.
	it, ok := todo.Load(todo.BuiltinCode(), root).Find("give-the-cache-a-lifetime")
	if !ok {
		t.Fatal("the item was not written")
	}
	switch {
	case it.Title != "Give the cache a lifetime":
		t.Errorf("title = %q", it.Title)
	case it.Fields["kind"] != "story" || it.Priority != todo.PriorityHigh || it.Grade() != "L":
		t.Errorf("header = %+v", it)
	case it.Status != todo.StatusOpen || it.Created == "":
		t.Errorf("state = %+v", it)
	case it.Session != "2026-09-04 09:00:00":
		t.Errorf("session = %q", it.Session)
	case strings.Join(it.DependsOn, ",") != "a-high":
		t.Errorf("depends_on = %v", it.DependsOn)
	case !strings.Contains(it.Body, "An entry past its lifetime is a miss"):
		t.Errorf("body = %q", it.Body)
	case !strings.Contains(it.Body, "go test ./internal/cache"):
		t.Errorf("body = %q", it.Body)
	}
	if got := strings.Join(signals, " "); !strings.Contains(got, observe.SignalTodo+":"+observe.TodoNew) {
		t.Errorf("signals = %v", signals)
	}
}

// A dependency the drafting invented is warned about on the card and left off
// the file: a slug that resolves to nothing would hold the item back forever.
func TestTodoNew_AMissingDependencyIsAWarningAndIsNotWritten(t *testing.T) {
	root := todoTestRoot(t)
	m := runDraft(t, draftModel(t, root, &scriptedProvider{args: draftFixture}, nil))
	if w := m.todoDraft.fields.Warning; !strings.Contains(w, "nothing-like-this") || strings.Contains(w, "a-high") {
		t.Fatalf("warning = %q", w)
	}
	m = pressKeys(t, m, keyEnter)
	note := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(note, "Dropped dependencies that name nothing in the backlog: nothing-like-this.") {
		t.Fatalf("note = %q", note)
	}
	it, _ := todo.Load(todo.BuiltinCode(), root).Find("give-the-cache-a-lifetime")
	if strings.Join(it.DependsOn, ",") != "a-high" {
		t.Fatalf("depends_on = %v", it.DependsOn)
	}
}

func TestTodoNew_EscapeWritesNothing(t *testing.T) {
	root := todoTestRoot(t)
	var signals []string
	m := runDraft(t, draftModel(t, root, &scriptedProvider{args: draftFixture}, &signals))
	m = pressKeys(t, m, keyEsc)
	if m.state != stateInput || m.todoDraft != nil {
		t.Fatalf("esc should close the card, state=%d", m.state)
	}
	if last := m.transcript[len(m.transcript)-1].text; !strings.Contains(last, "Nothing written") {
		t.Fatalf("note = %q", last)
	}
	if _, err := os.Stat(filepath.Join(todo.Dir(root), "give-the-cache-a-lifetime.md")); !os.IsNotExist(err) {
		t.Fatal("esc wrote a file")
	}
	for _, s := range signals {
		if strings.HasPrefix(s, observe.SignalTodo) {
			t.Fatalf("a dropped draft was recorded: %v", signals)
		}
	}
}

// The dependency row opens the backlog rather than stepping a scale, and
// checking nothing there is how an item's dependencies are cleared.
func TestTodoNew_TheDependencyPickerSetsThem(t *testing.T) {
	root := todoTestRoot(t)
	m := runDraft(t, draftModel(t, root, &scriptedProvider{args: draftFixture}, nil))
	m = pressKeys(t, m, keyDown, keyDown, keyDown, keySpace)
	if m.todoDraft.picker == nil {
		t.Fatal("the dependency row should open the picker")
	}
	card := strings.Join(m.todoDraftLines(), "\n")
	if !strings.Contains(card, "a-high") || !strings.Contains(card, "d-ready") {
		t.Fatalf("the picker should offer the backlog:\n%s", card)
	}
	// a-high is the first row and is checked, because the draft names it.
	if !m.todoDraft.picker.Checked[0] {
		t.Fatal("a dependency the draft names should open checked")
	}
	// Uncheck it and take the empty answer.
	m = pressKeys(t, m, keySpace, keyEnter)
	if m.todoDraft.picker != nil {
		t.Fatal("the picker should close on enter")
	}
	if len(m.todoDraft.proposal.DependsOn) != 0 {
		t.Fatalf("depends_on = %v", m.todoDraft.proposal.DependsOn)
	}
	if m.todoDraft.fields.Warning != "" {
		t.Fatalf("clearing the dependencies should clear the warning: %q", m.todoDraft.fields.Warning)
	}
	// Now check the second row and keep it.
	m = pressKeys(t, m, keySpace, keyDown, keySpace, keyEnter)
	if strings.Join(m.todoDraft.proposal.DependsOn, ",") != "b-waits" {
		t.Fatalf("depends_on = %v", m.todoDraft.proposal.DependsOn)
	}
}

// The proposals card's own way into a header: `e` on the focused row opens
// the same draft card, and what it settles goes back onto the row.
func TestTodoAdd_TheHeaderIsSetOnTheProposalsCard(t *testing.T) {
	root := todoTestRoot(t)
	m := runExtract(t, extractModel(t, root, &scriptedProvider{args: proposalsFixture}))
	if !strings.Contains(strings.Join(m.todoProposeLines(), "\n"), "its header") {
		t.Fatal("the proposals card should offer the header key")
	}
	m = pressKeys(t, m, tea.KeyPressMsg{Code: 'e', Text: "e"})
	if m.state != stateTodoDraft || m.todoDraft == nil || m.todoDraft.from != 0 {
		t.Fatalf("e should open the focused proposal's header: state=%d", m.state)
	}
	// The other proposals' titles resolve to slugs when they are accepted,
	// so they are not warned about; a name nothing answers is.
	if w := m.todoDraft.fields.Warning; !strings.Contains(w, "nothing-like-this") || strings.Contains(w, "Build the store") {
		t.Fatalf("warning = %q", w)
	}
	// Size row, one step: M → L.
	m = pressKeys(t, m, keyDown, keyDown, keySpace, keyEnter)
	if m.state != stateTodoPropose || m.todoDraft != nil {
		t.Fatalf("enter should go back to the proposals card, state=%d", m.state)
	}
	if m.todoProposals[0].Fields["size"] != "L" {
		t.Fatalf("the proposal keeps the header it was given: %+v", m.todoProposals[0])
	}
	if !strings.Contains(m.todoPropose.Options[0].Meta, "story · high · L") {
		t.Fatalf("the row = %q", m.todoPropose.Options[0].Meta)
	}
	// And the file lands with it.
	m = pressKeys(t, m, keyEnter)
	it, ok := todo.Load(todo.BuiltinCode(), root).Find("show-the-backlog-in-the-rail")
	if !ok || it.Grade() != "L" {
		t.Fatalf("written item = %+v", it)
	}
}

// esc on a header opened from the proposals card leaves the proposal as it
// was and puts the card back, rather than dropping every proposal.
func TestTodoAdd_LeavingAHeaderKeepsTheProposals(t *testing.T) {
	m := runExtract(t, extractModel(t, todoTestRoot(t), &scriptedProvider{args: proposalsFixture}))
	m = pressKeys(t, m, tea.KeyPressMsg{Code: 'e', Text: "e"}, keyDown, keyDown, keySpace, keyEsc)
	if m.state != stateTodoPropose || m.todoDraft != nil || len(m.todoProposals) != 3 {
		t.Fatalf("esc should go back to the proposals card: state=%d", m.state)
	}
	if m.todoProposals[0].Fields["size"] != "M" {
		t.Fatalf("the proposal should be as it was: %+v", m.todoProposals[0])
	}
}

func TestTodoNew_UsageNoDrafterAndDoubleStart(t *testing.T) {
	root := todoTestRoot(t)
	m := draftModel(t, root, nil, nil)
	m.input.SetValue("/todo new")
	updated, cmd := m.submitInput()
	if cmd != nil || !strings.Contains(lastNote(updated.(Model)), todoNewUsage) {
		t.Fatalf("a bare /todo new should say what it takes: %q", lastNote(updated.(Model)))
	}
	m.input.SetValue("/todo new something")
	updated, cmd = m.submitInput()
	if cmd != nil || !strings.Contains(lastNote(updated.(Model)), "No model is configured") {
		t.Fatalf("without a drafter the by-hand form should be offered: %q", lastNote(updated.(Model)))
	}

	m = draftModel(t, root, &scriptedProvider{args: draftFixture}, nil)
	m.input.SetValue("/todo new one")
	updated, _ = m.submitInput()
	m = updated.(Model)
	m.input.SetValue("/todo new two")
	updated, cmd = m.submitInput()
	if cmd != nil || !strings.Contains(lastNote(updated.(Model)), "Still drafting") {
		t.Fatal("a second drafting must not start over a first")
	}
	// A drafting the session has moved past is dropped.
	late := todoDraftMsg{runID: 0, result: todo.ExtractResult{Proposals: []todo.Proposal{{Title: "late"}}}}
	updated, _ = m.Update(late)
	if updated.(Model).state == stateTodoDraft {
		t.Fatal("a stale drafting opened the card")
	}
}

func TestTodoNew_AFailedDraftingIsASentence(t *testing.T) {
	m := runDraft(t, draftModel(t, todoTestRoot(t), &scriptedProvider{args: "not json"}, nil))
	if m.state != stateInput || m.todoDrafting {
		t.Fatal("a failed drafting should return to the input")
	}
	if last := lastNote(m); !strings.Contains(last, "could not be drafted") || !strings.Contains(last, "by hand") {
		t.Fatalf("note = %q", last)
	}
}

// The screen's `n` has nowhere to type a sentence, so it leaves the command
// in the draft box instead of opening a card with nothing in it.
func TestTodoScreen_NewLeavesTheCommandInTheInput(t *testing.T) {
	root := todoTestRoot(t)
	m := draftModel(t, root, &scriptedProvider{args: draftFixture}, nil)
	updated, _ := m.openTodoScreen()
	m = updated.(Model)
	m = pressKeys(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.state != stateInput || m.input.Value() != todoNewPrefix {
		t.Fatalf("n should compose the command: state=%d input=%q", m.state, m.input.Value())
	}

	// A draft already in the box is not thrown away for it.
	updated, _ = m.openTodoScreen()
	m = updated.(Model)
	m.input.SetValue("a paragraph somebody wrote")
	m = pressKeys(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.input.Value() != "a paragraph somebody wrote" {
		t.Fatalf("input = %q", m.input.Value())
	}
}

// The editor is the same handoff /todo edit makes, over a temporary file that
// does not survive it, and what comes back is the draft.
func TestTodoNew_TheEditorHandsTheItemBack(t *testing.T) {
	root := todoTestRoot(t)
	// The handoff writes into the temp directory, and the test wants to read
	// the one file it wrote there.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	m := runDraft(t, draftModel(t, root, &scriptedProvider{args: draftFixture}, nil))
	done, act := (&m).answerTodoDraft(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if !done || !act.close || act.run == nil {
		t.Fatalf("e should take the card down and hand the file to the editor: %+v", act)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the handoff should have written one file: %v %v", entries, err)
	}
	path := filepath.Join(tmp, entries[0].Name())
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "title: Give the cache a lifetime") {
		t.Fatalf("the editor was given %q", content)
	}
	if err := os.WriteFile(path, []byte("---\ntitle: A lifetime, renamed\npriority: low\nsize: S\n---\n\nRewritten by hand.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.leaveSurface()
	updated, _ := m.Update(todoDraftEditorDoneMsg{path: path})
	m = updated.(Model)
	if m.state != stateTodoDraft {
		t.Fatalf("the card should come back up: state=%d", m.state)
	}
	if m.todoDraft.proposal.Title != "A lifetime, renamed" || m.todoDraft.proposal.Fields["size"] != "S" {
		t.Fatalf("the edit should be the draft: %+v", m.todoDraft.proposal)
	}
	if !strings.Contains(m.todoDraft.body, "Rewritten by hand.") {
		t.Fatalf("body = %q", m.todoDraft.body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the temporary file outlived the handoff")
	}
	// What was written by hand is what lands.
	m = pressKeys(t, m, keyEnter)
	it, ok := todo.Load(todo.BuiltinCode(), root).Find("a-lifetime-renamed")
	if !ok || it.Grade() != "S" || !strings.Contains(it.Body, "Rewritten by hand.") {
		t.Fatalf("written item = %+v", it)
	}
}

// How the backlog grew is in the record, and the three ways are told apart:
// a session proposing items, a sentence drafted into one, and an editor's
// pass over an item that already exists.
func TestTodo_TheRecordSaysHowTheBacklogGrew(t *testing.T) {
	root := todoTestRoot(t)
	var signals []string
	record := observe.Observer{Signal: func(_ observe.Pos, code, reason string) {
		signals = append(signals, code+":"+reason)
	}}

	m := extractModel(t, root, &scriptedProvider{args: proposalsFixture}).WithObserver(record)
	m = pressKeys(t, runExtract(t, m), keyEnter)
	if len(signals) != 1 || signals[0] != observe.SignalTodo+":"+observe.TodoAdd {
		t.Fatalf("accepting proposals = %v", signals)
	}

	// The editor coming back off an item file is the third.
	signals = nil
	m = draftModel(t, root, nil, nil).WithObserver(record)
	it, ok := m.todoStore.Find("a-high")
	if !ok {
		t.Fatal("the fixture should hold a-high")
	}
	updated, _ := m.todoEditorFinished(todoEditorDoneMsg{slug: it.Slug, path: it.Path})
	if len(signals) != 1 || signals[0] != observe.SignalTodo+":"+observe.TodoEdit {
		t.Fatalf("editing an item = %v", signals)
	}
	if !strings.HasPrefix(lastNote(updated.(Model)), "Saved a-high") {
		t.Fatalf("the editor's own sentence should be unchanged: %q", lastNote(updated.(Model)))
	}
}

// Live work belongs to the session that started it: a drafting still in
// flight when the session boundary is crossed does not open a card in the
// next one, where it would be written with that session's name on it.
func TestTodoNew_TheSessionBoundaryRetiresADraftingInFlight(t *testing.T) {
	m := draftModel(t, todoTestRoot(t), &scriptedProvider{args: draftFixture}, nil)
	m.input.SetValue("/todo new the cache never expires anything")
	updated, cmd := m.submitInput()
	m = updated.(Model)
	m.startNewSession()
	if m.todoDrafting {
		t.Fatal("/new should retire the drafting")
	}
	updated, _ = m.Update(cmd())
	if updated.(Model).state == stateTodoDraft {
		t.Fatal("a drafting from before the boundary opened the card")
	}
}

// The dependency row has no list to open on an empty backlog, and a key that
// cannot act says why.
func TestTodoNew_AnEmptyBacklogSaysWhyTheRowDoesNotOpen(t *testing.T) {
	root := t.TempDir()
	m := draftModel(t, root, &scriptedProvider{args: draftFixture}, nil)
	m = runDraft(t, m)
	m = pressKeys(t, m, keyDown, keyDown, keyDown, keySpace)
	if m.todoDraft == nil || m.todoDraft.picker != nil {
		t.Fatal("there is no backlog to pick from, and the card should still be up")
	}
	if !strings.Contains(lastNote(m), "Nothing in the backlog to wait on") {
		t.Fatalf("note = %q", lastNote(m))
	}
}
