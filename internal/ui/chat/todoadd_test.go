package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/todo"
)

// scriptedProvider answers every request with one tool call.
type scriptedProvider struct {
	args   string
	prompt string
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	// The digest is the user turn; the instructions are the system message.
	p.prompt = msgs[len(msgs)-1].Content
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{Name: todo.ExtractToolName, Arguments: p.args}}}
	ch <- provider.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

const proposalsFixture = `{"items": [
	{"title": "Show the backlog in the rail", "kind": "story", "priority": "high", "size": "M", "acceptance_criteria": ["A block appears"], "depends_on": ["Build the store", "a-high", "nothing-like-this"]},
	{"title": "Build the store", "kind": "chore", "priority": "medium", "size": "S", "acceptance_criteria": ["loads"]},
	{"title": "High one", "kind": "bug", "priority": "low", "size": "S", "acceptance_criteria": ["dup slug"]}
]}`

func extractModel(t *testing.T, root string, p *scriptedProvider) Model {
	t.Helper()
	m := frameModel(t, 130, 40)
	m.sessionName = "2026-08-30 12:00:00"
	m.transcript = []entry{
		{kind: entryUser, text: "Let's design the backlog feature"},
		{kind: entryAssistant, text: "Here is the plan: a store, a rail block."},
		{kind: entrySystem, text: "noise"},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"go.mod"}`, toolResult: "SECRET-TOOL-OUTPUT"},
	}
	var ex *todo.Extractor
	if p != nil {
		ex = todo.NewExtractor(p, todo.ExtractConfig{Model: "m"})
	}
	return m.WithTodos(Todos{Root: root, Manage: func([]string) string { return "usage" }, Detail: func(*todo.Store, todo.Item) string { return "" }, Extractor: ex})
}

// runExtract submits a bare /todo add and delivers the reading.
func runExtract(t *testing.T, m Model) Model {
	t.Helper()
	m.input.SetValue("/todo add")
	updated, cmd := m.submitInput()
	next := updated.(Model)
	if cmd == nil || !next.todoExtracting {
		t.Fatal("a bare /todo add should start a reading")
	}
	if last := next.transcript[len(next.transcript)-1].text; !strings.HasPrefix(last, "Reading the session") {
		t.Fatalf("start note = %q", last)
	}
	msg := cmd()
	updated, _ = next.Update(msg)
	return updated.(Model)
}

func TestTodoAdd_ProposalsCardWritesTheCheckedOnes(t *testing.T) {
	root := todoTestRoot(t)
	p := &scriptedProvider{args: proposalsFixture}
	m := runExtract(t, extractModel(t, root, p))
	if m.state != stateTodoPropose || m.todoPropose == nil || len(m.todoProposals) != 3 {
		t.Fatalf("the card should be showing: state=%d", m.state)
	}
	for _, want := range []string{"Let's design the backlog feature", "Here is the plan", "a-high — High one", "UNTRUSTED DIGEST"} {
		if !strings.Contains(p.prompt, want) {
			t.Errorf("digest lacks %q", want)
		}
	}
	if strings.Contains(p.prompt, "noise") || strings.Contains(p.prompt, "SECRET-TOOL-OUTPUT") {
		t.Error("system rows and tool output are not evidence")
	}
	if !strings.Contains(p.prompt, "read_file") {
		t.Error("the tool call itself is evidence")
	}
	for i, c := range m.todoPropose.Checked {
		if !c {
			t.Fatalf("proposal %d should start checked", i)
		}
	}
	if m.todoPropose.Options[0].Meta != "story · high · M · after Build the store, a-high, nothing-like-this" {
		t.Fatalf("row = %+v", m.todoPropose.Options[0])
	}
	card := strings.Join(m.todoProposeLines(), "\n")
	if !strings.Contains(card, "Show the backlog in the rail") || !strings.Contains(card, "chore · medium · S") {
		t.Fatalf("the card should draw the titles and the facts:\n%s", card)
	}

	// Uncheck the second (focus down, toggle), then write.
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeySpace, Text: " "}, {Code: tea.KeyEnter}} {
		updated, _ := m.Update(k)
		m = updated.(Model)
	}
	if m.state != stateInput || m.todoPropose != nil {
		t.Fatalf("enter should close the card, state=%d", m.state)
	}
	note := m.transcript[len(m.transcript)-1].text
	if !strings.HasPrefix(note, "Wrote 2 backlog items to") || !strings.Contains(note, "show-the-backlog-in-the-rail") || !strings.Contains(note, "  high-one  low") {
		t.Fatalf("note = %q", note)
	}
	if !strings.Contains(note, `show-the-backlog-in-the-rail → "Build the store"`) || !strings.Contains(note, `→ "nothing-like-this"`) {
		t.Fatalf("dropped deps should be named: %q", note)
	}
	s := todo.Load(root)
	it, ok := s.Find("show-the-backlog-in-the-rail")
	if !ok || strings.Join(it.DependsOn, ",") != "a-high" || it.Session != "2026-08-30 12:00:00" || it.Size != todo.SizeM {
		t.Fatalf("written item = %+v", it)
	}
	if _, err := os.Stat(filepath.Join(todo.Dir(root), "build-the-store.md")); !os.IsNotExist(err) {
		t.Fatal("an unchecked proposal was written")
	}
	if m.todoStore.Len() != 7 {
		t.Fatalf("store should be re-read after writing, has %d", m.todoStore.Len())
	}
}

func TestTodoAdd_CancelWritesNothing(t *testing.T) {
	root := todoTestRoot(t)
	m := runExtract(t, extractModel(t, root, &scriptedProvider{args: proposalsFixture}))
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput || !strings.Contains(m.transcript[len(m.transcript)-1].text, "Nothing written") {
		t.Fatal("esc should drop the proposals")
	}
	if todo.Load(root).Len() != 5 {
		t.Fatal("cancel wrote files")
	}
}

func TestTodoAdd_FailedReadingIsASentence(t *testing.T) {
	m := runExtract(t, extractModel(t, todoTestRoot(t), &scriptedProvider{args: "not json"}))
	if m.state != stateInput || m.todoExtracting {
		t.Fatal("a failed reading should return to the input")
	}
	if last := m.transcript[len(m.transcript)-1].text; !strings.Contains(last, "could not be read into items") || !strings.Contains(last, "by hand") {
		t.Fatalf("note = %q", last)
	}
}

func TestTodoAdd_NoExtractorAndDoubleStart(t *testing.T) {
	m := extractModel(t, todoTestRoot(t), nil)
	m.input.SetValue("/todo add")
	updated, cmd := m.submitInput()
	if cmd != nil || !strings.Contains(updated.(Model).transcript[len(updated.(Model).transcript)-1].text, "No model is configured") {
		t.Fatal("without an extractor the by-hand form should be offered")
	}

	m = extractModel(t, todoTestRoot(t), &scriptedProvider{args: proposalsFixture})
	m.input.SetValue("/todo add")
	updated, _ = m.submitInput()
	m = updated.(Model)
	m.input.SetValue("/todo add")
	updated, cmd = m.submitInput()
	if cmd != nil || !strings.Contains(updated.(Model).transcript[len(updated.(Model).transcript)-1].text, "Still reading") {
		t.Fatal("a second reading must not start over a first")
	}
	// A result from a run the session has moved past is dropped.
	late := todoProposalsMsg{runID: 0, result: todo.ExtractResult{Proposals: []todo.Proposal{{Title: "late"}}}}
	updated, _ = m.Update(late)
	if updated.(Model).state == stateTodoPropose {
		t.Fatal("a stale reading opened the card")
	}
}

func TestTodoAdd_ClearDropsAReadingInFlight(t *testing.T) {
	m := extractModel(t, todoTestRoot(t), &scriptedProvider{args: proposalsFixture})
	m.input.SetValue("/todo add")
	updated, cmd := m.submitInput()
	m = updated.(Model)
	m.startNewSession()
	if m.todoExtracting {
		t.Fatal("/clear should retire the reading")
	}
	updated, _ = m.Update(cmd())
	if updated.(Model).state == stateTodoPropose {
		t.Fatal("a reading from before /clear opened the card")
	}
}

func TestWriteProposals_DuplicateTitlesAndHostileFields(t *testing.T) {
	root := t.TempDir()
	m := frameModel(t, 130, 40)
	m = m.WithTodos(Todos{Root: root, Manage: func([]string) string { return "" }, Detail: func(*todo.Store, todo.Item) string { return "" }})
	m.todoStore = nil
	ps, ok := todo.ParseProposals(`{"items": [
		{"title": "Fix it", "acceptance_criteria": ["a"]},
		{"title": "fix it", "acceptance_criteria": ["b"], "depends_on": ["Fix it"]},
		{"title": "Say \"hi\" # not a comment\n---\nstatus: done", "story": "---", "acceptance_criteria": ["x\n---\ny"]}
	]}`)
	if !ok || len(ps) != 3 {
		t.Fatalf("fixture: %v %d", ok, len(ps))
	}
	note, _ := m.writeProposals(ps, []int{0, 1, 2})
	if !strings.HasPrefix(note, "Wrote 3 backlog items") {
		t.Fatalf("note = %q", note)
	}
	s := todo.Load(root)
	if s.Len() != 3 || len(s.Diagnostics) != 0 {
		t.Fatalf("store = %d items, diagnostics %v", s.Len(), s.Diagnostics)
	}
	second, _ := s.Find("fix-it-2")
	if strings.Join(second.DependsOn, ",") != "fix-it" {
		t.Errorf("duplicate title resolved to %v", second.DependsOn)
	}
	hostile, _ := s.Find("say-hi-not-a-comment-status-done")
	if hostile.Title != `Say "hi" # not a comment --- status: done` || hostile.Status != todo.StatusOpen || !strings.Contains(hostile.Body, "- [ ] x --- y") {
		t.Errorf("hostile fields changed the header: %+v", hostile)
	}
}

func TestUniqueSlug(t *testing.T) {
	taken := map[string]bool{"a": true, "a-2": true}
	got := uniqueSlug("a", taken)
	if got != "a-3" {
		t.Errorf("uniqueSlug = %q", got)
	}
	hyphenAtCut := strings.Repeat("x", 45) + "-yy"
	got = uniqueSlug(hyphenAtCut, map[string]bool{hyphenAtCut: true})
	if err := todo.ValidSlug(got); err != nil || !strings.HasSuffix(got, "-2") {
		t.Errorf("cut on a hyphen: %q %v", got, err)
	}
	long := strings.Repeat("x", todo.MaxSlugLen)
	got = uniqueSlug(long, map[string]bool{long: true})
	if len(got) > todo.MaxSlugLen || !strings.HasSuffix(got, "-2") {
		t.Errorf("long uniqueSlug = %q", got)
	}
}
