package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
)

var errTest = errors.New("test error")

func drainStream(m GenerateModel, events int) GenerateModel {
	for i := 0; i < events; i++ {
		cmd := m.stream.waitForEvent()
		model, _ := m.Update(cmd())
		m = model.(GenerateModel)
	}
	return m
}

func TestGenerate_StartsInStreamingPhase(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")

	if m.Phase() != phaseStreaming {
		t.Errorf("expected phaseStreaming, got %v", m.Phase())
	}
}

func TestGenerate_TransitionsToActionOnDone(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")

	// token + done
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction, got %v", m.Phase())
	}
}

func TestGenerate_ActionBarAppearsInView(t *testing.T) {
	events := makeEvents("echo hello")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	view := m.View()
	if !strings.Contains(view, "Run") {
		t.Error("expected action bar visible after stream completes")
	}
	if !strings.Contains(view, "echo hello") {
		t.Error("expected command still visible after stream completes")
	}
}

func TestGenerate_SelectRunReturnsResult(t *testing.T) {
	events := makeEvents("rm -rf /tmp/test")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Press 'r'
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = model.(GenerateModel)

	if m.Phase() != phaseDone {
		t.Errorf("expected phaseDone, got %v", m.Phase())
	}
	r := m.Result()
	if r.Action != ActionRun {
		t.Errorf("expected ActionRun, got %v", r.Action)
	}
	if r.Command != "rm -rf /tmp/test" {
		t.Errorf("expected command 'rm -rf /tmp/test', got %q", r.Command)
	}

	// Should emit tea.Quit
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestGenerate_SelectCopyReturnsResult(t *testing.T) {
	events := makeEvents("docker ps")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = model.(GenerateModel)

	r := m.Result()
	if r.Action != ActionCopy {
		t.Errorf("expected ActionCopy, got %v", r.Action)
	}
	if r.Command != "docker ps" {
		t.Errorf("expected 'docker ps', got %q", r.Command)
	}
}

func TestGenerate_SelectCancelReturnsResult(t *testing.T) {
	events := makeEvents("whoami")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = model.(GenerateModel)

	r := m.Result()
	if r.Action != ActionCancel {
		t.Errorf("expected ActionCancel, got %v", r.Action)
	}
}

func TestGenerate_CancelDuringStreamQuitsImmediately(t *testing.T) {
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{Token: "partial"}
	cancel, called := testCancel()
	m := NewGenerateModel(ch, cancel, nil, nil, nil, "")

	// Receive token
	cmd := m.stream.waitForEvent()
	model, _ := m.Update(cmd())
	m = model.(GenerateModel)

	// Press Esc during stream
	model, quitCmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = model.(GenerateModel)

	if m.Phase() != phaseDone {
		t.Errorf("expected phaseDone after cancel, got %v", m.Phase())
	}
	if !m.Result().Cancelled {
		t.Error("expected result.Cancelled to be true")
	}
	if !*called {
		t.Error("expected cancel to be called")
	}

	msg := quitCmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
	close(ch)
}

func TestGenerate_ErrorDuringStreamQuitsImmediately(t *testing.T) {
	events := makeErrorEvents(errTest)
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")

	cmd := m.stream.waitForEvent()
	model, quitCmd := m.Update(cmd())
	m = model.(GenerateModel)

	if m.Phase() != phaseDone {
		t.Errorf("expected phaseDone after error, got %v", m.Phase())
	}
	if m.Result().Err == nil {
		t.Error("expected error in result")
	}

	msg := quitCmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestGenerate_StripsMarkdownBeforeActionBar(t *testing.T) {
	events := makeEvents("```bash\nfind . -name '*.log'\n```")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Command should be stripped by the time action bar appears
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = model.(GenerateModel)

	if m.Result().Command != "find . -name '*.log'" {
		t.Errorf("expected stripped command, got %q", m.Result().Command)
	}
}

func TestGenerate_NavigateThenEnter(t *testing.T) {
	events := makeEvents("pwd")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Navigate right to Copy, then Enter
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = model.(GenerateModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	if m.Result().Action != ActionCopy {
		t.Errorf("expected ActionCopy after nav+enter, got %v", m.Result().Action)
	}
}

func TestGenerate_ReviseOpensTextInput(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Press 'v' to revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)

	if m.Phase() != phaseRevise {
		t.Errorf("expected phaseRevise, got %v", m.Phase())
	}
}

func TestGenerate_ReviseEscReturnsToAction(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Enter revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)

	// Press Esc to cancel revision
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = model.(GenerateModel)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after Esc, got %v", m.Phase())
	}
}

func TestGenerate_ReviseSubmitWithoutStreamFuncQuits(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Enter revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)

	// Type feedback
	for _, r := range "add -la flag" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(GenerateModel)
	}

	// Submit — no newStream func, so it falls back to quit
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	if m.Phase() != phaseDone {
		t.Errorf("expected phaseDone, got %v", m.Phase())
	}
	r := m.Result()
	if r.Action != ActionRevise {
		t.Errorf("expected ActionRevise, got %v", r.Action)
	}
	if r.Feedback != "add -la flag" {
		t.Errorf("expected feedback 'add -la flag', got %q", r.Feedback)
	}

	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestGenerate_ReviseEmptyIgnored(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Enter revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)

	// Submit empty
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	if m.Phase() != phaseRevise {
		t.Errorf("expected to stay in phaseRevise on empty submit, got %v", m.Phase())
	}
}

func TestGenerate_ReviseViewShowsFeedbackPrompt(t *testing.T) {
	events := makeEvents("echo hi")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)

	view := m.View()
	if !strings.Contains(view, "Feedback") {
		t.Error("expected 'Feedback' label in revise view")
	}
	if !strings.Contains(view, "echo hi") {
		t.Error("expected command still visible during revision")
	}
}

func TestGenerate_MessagesPreservedFromConstructor(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "You are a shell assistant."},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, nil, nil, "")

	msgs := m.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 initial messages, got %d", len(msgs))
	}
	if msgs[0].Role != provider.RoleSystem {
		t.Errorf("expected system role, got %v", msgs[0].Role)
	}
	if msgs[1].Content != "list files" {
		t.Errorf("expected 'list files', got %q", msgs[1].Content)
	}
}

func TestGenerate_MessagesNotAliased(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "user prompt"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, nil, nil, "")

	initial[1].Content = "mutated"
	if m.Messages()[1].Content == "mutated" {
		t.Error("expected constructor to copy messages, not alias the slice")
	}
}

func TestGenerate_AssistantAppendedOnStreamComplete(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, initial, nil, nil, "")
	m = drainStream(m, 2)

	msgs := m.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after stream, got %d", len(msgs))
	}
	if msgs[2].Role != provider.RoleAssistant {
		t.Errorf("expected assistant role, got %v", msgs[2].Role)
	}
	if msgs[2].Content != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", msgs[2].Content)
	}
}

func TestGenerate_ReviseAppendsFeedbackToMessages(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, nil, nil, "")
	m = drainStream(m, 2)

	// Enter revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)

	// Type and submit feedback
	for _, r := range "add -la" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(GenerateModel)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	msgs := m.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages after revise, got %d", len(msgs))
	}
	if msgs[2].Role != provider.RoleAssistant {
		t.Errorf("expected assistant at index 2, got %v", msgs[2].Role)
	}
	if msgs[3].Role != provider.RoleUser {
		t.Errorf("expected user at index 3, got %v", msgs[3].Role)
	}
	if msgs[3].Content != "add -la" {
		t.Errorf("expected feedback 'add -la', got %q", msgs[3].Content)
	}
}

func TestGenerate_ReviseEscDoesNotAppendMessage(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, nil, nil, "")
	m = drainStream(m, 2)

	// Enter revise then cancel
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = model.(GenerateModel)

	msgs := m.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (no feedback added on Esc), got %d", len(msgs))
	}
}

func mockNewStream(tokens ...string) NewStreamFunc {
	return func(messages []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return makeEvents(tokens...), noopCancel, nil
	}
}

func typeKeys(m GenerateModel, s string) GenerateModel {
	for _, r := range s {
		model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(GenerateModel)
	}
	return m
}

func TestGenerate_ReviseRestreamsWithNewResponse(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, mockNewStream("ls -la"), nil, "")
	m = drainStream(m, 2)

	// Enter revise, type feedback, submit
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)
	m = typeKeys(m, "add -la")
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	if m.Phase() != phaseStreaming {
		t.Fatalf("expected phaseStreaming after revise submit, got %v", m.Phase())
	}

	// Init should return a batch command
	if cmd == nil {
		t.Fatal("expected Init cmd from new stream")
	}

	// Drain the new stream (token + done)
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after re-stream, got %v", m.Phase())
	}
	if m.stream.Output() != "ls -la" {
		t.Errorf("expected new command 'ls -la', got %q", m.stream.Output())
	}
}

func TestGenerate_ReviseUpdatesCommandInResult(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, mockNewStream("ls -la"), nil, "")
	m = drainStream(m, 2)

	// Revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)
	m = typeKeys(m, "add -la")
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)
	m = drainStream(m, 2)

	// Now select Run — result should have the NEW command
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = model.(GenerateModel)

	if m.Result().Command != "ls -la" {
		t.Errorf("expected revised command 'ls -la', got %q", m.Result().Command)
	}
}

func TestGenerate_ReviseMessagesAccumulate(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, mockNewStream("ls -la"), nil, "")
	m = drainStream(m, 2)

	// Revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)
	m = typeKeys(m, "add -la")
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	// After submit, before re-stream completes: sys, user, assistant("ls"), user("add -la")
	msgs := m.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages before re-stream, got %d", len(msgs))
	}

	// Drain re-stream — assistant("ls -la") appended
	m = drainStream(m, 2)

	msgs = m.Messages()
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages after re-stream, got %d", len(msgs))
	}
	if msgs[4].Role != provider.RoleAssistant {
		t.Errorf("expected assistant at index 4, got %v", msgs[4].Role)
	}
	if msgs[4].Content != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", msgs[4].Content)
	}
}

func TestGenerate_ReviseActionBarReappearsAfterRestream(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, mockNewStream("ls -la"), nil, "")
	m = drainStream(m, 2)

	// Revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)
	m = typeKeys(m, "fix it")
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)
	m = drainStream(m, 2)

	view := m.View()
	if !strings.Contains(view, "Run") {
		t.Error("expected action bar visible after re-stream")
	}
	if !strings.Contains(view, "ls -la") {
		t.Error("expected new command visible after re-stream")
	}
}

func TestGenerate_ReviseStreamErrorQuitsWithError(t *testing.T) {
	events := makeEvents("ls")
	failStream := func(messages []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return nil, nil, errors.New("API error")
	}
	m := NewGenerateModel(events, noopCancel, nil, failStream, nil, "")
	m = drainStream(m, 2)

	// Revise
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)
	m = typeKeys(m, "try again")
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	// The cmd carries the error as a reviseErrMsg
	model, quitCmd := m.Update(cmd())
	m = model.(GenerateModel)

	if m.Phase() != phaseDone {
		t.Errorf("expected phaseDone after stream error, got %v", m.Phase())
	}
	if m.Result().Err == nil {
		t.Error("expected error in result")
	}

	msg := quitCmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestGenerate_EditOpensTextInput(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = model.(GenerateModel)

	if m.Phase() != phaseEdit {
		t.Errorf("expected phaseEdit, got %v", m.Phase())
	}
}

func TestGenerate_EditPrePopulatesCommand(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = model.(GenerateModel)

	view := m.View()
	if !strings.Contains(view, "ls -la") {
		t.Errorf("expected command pre-populated in edit view, got: %q", view)
	}
	if !strings.Contains(view, "Edit") {
		t.Error("expected 'Edit' label in edit view")
	}
}

func TestGenerate_EditEscReturnsToAction(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = model.(GenerateModel)

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = model.(GenerateModel)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after Esc, got %v", m.Phase())
	}
	if m.stream.Output() != "ls" {
		t.Errorf("expected original command unchanged after Esc, got %q", m.stream.Output())
	}
}

func TestGenerate_EditSubmitUpdatesCommand(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, nil, nil, "")
	m = drainStream(m, 2)

	// Enter edit
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = model.(GenerateModel)

	// Clear and type new command (select all not available, so we manipulate directly)
	// The text input has "ls" pre-populated; type " -la" to append
	for _, r := range " -la" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(GenerateModel)
	}

	// Submit
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after edit submit, got %v", m.Phase())
	}
	if m.stream.Output() != "ls -la" {
		t.Errorf("expected edited command 'ls -la', got %q", m.stream.Output())
	}
}

func TestGenerate_EditUpdatesMessages(t *testing.T) {
	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, initial, nil, nil, "")
	m = drainStream(m, 2)

	// Enter edit and append
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = model.(GenerateModel)
	for _, r := range " -la" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(GenerateModel)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	msgs := m.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[2].Content != "ls -la" {
		t.Errorf("expected assistant message updated to 'ls -la', got %q", msgs[2].Content)
	}
}

func TestGenerate_EditedCommandFlowsToResult(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Edit
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = model.(GenerateModel)
	for _, r := range " -la" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(GenerateModel)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	// Now select Run — result should have the edited command
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = model.(GenerateModel)

	if m.Result().Command != "ls -la" {
		t.Errorf("expected 'ls -la' in result, got %q", m.Result().Command)
	}
}

func TestGenerate_EditEmptyIgnored(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Enter edit
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = model.(GenerateModel)

	// Clear the input by pressing backspace twice
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = model.(GenerateModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = model.(GenerateModel)

	// Submit empty
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)

	if m.Phase() != phaseEdit {
		t.Errorf("expected to stay in phaseEdit on empty submit, got %v", m.Phase())
	}
}

func TestGenerate_MultipleRevisionsWork(t *testing.T) {
	revision := 0
	responses := []string{"ls", "ls -l", "ls -la"}
	multiStream := func(messages []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		revision++
		return makeEvents(responses[revision]), noopCancel, nil
	}

	initial := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "list files"},
	}
	events := makeEvents(responses[0])
	m := NewGenerateModel(events, noopCancel, initial, multiStream, nil, "")
	m = drainStream(m, 2)

	// First revision
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)
	m = typeKeys(m, "add -l")
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)
	m = drainStream(m, 2)

	if m.stream.Output() != "ls -l" {
		t.Errorf("expected 'ls -l' after first revision, got %q", m.stream.Output())
	}

	// Second revision
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = model.(GenerateModel)
	m = typeKeys(m, "also add -a")
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(GenerateModel)
	m = drainStream(m, 2)

	if m.stream.Output() != "ls -la" {
		t.Errorf("expected 'ls -la' after second revision, got %q", m.stream.Output())
	}

	// Messages: sys, user, asst("ls"), user("add -l"), asst("ls -l"), user("also add -a"), asst("ls -la")
	msgs := m.Messages()
	if len(msgs) != 7 {
		t.Fatalf("expected 7 messages after two revisions, got %d", len(msgs))
	}

	// Select Run — should get final command
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = model.(GenerateModel)
	if m.Result().Command != "ls -la" {
		t.Errorf("expected final command 'ls -la', got %q", m.Result().Command)
	}
}

func mockExplainStream(tokens ...string) ExplainStreamFunc {
	return func(command string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return makeEvents(tokens...), noopCancel, nil
	}
}

func drainExplainStream(m GenerateModel, events int) GenerateModel {
	for i := 0; i < events; i++ {
		cmd := m.explainStream.waitForEvent()
		model, _ := m.Update(cmd())
		m = model.(GenerateModel)
	}
	return m
}

func TestGenerate_ExplainOpensExplainPhase(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files"), "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = model.(GenerateModel)

	if m.Phase() != phaseExplain {
		t.Errorf("expected phaseExplain, got %v", m.Phase())
	}
}

func TestGenerate_ExplainStreamsAndReturnsToAction(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files in detail"), "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = model.(GenerateModel)

	// Drain explain stream (token + done)
	m = drainExplainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after explain completes, got %v", m.Phase())
	}
}

func TestGenerate_ExplainViewShowsExplanation(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files"), "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = model.(GenerateModel)

	// Receive token
	cmd := m.explainStream.waitForEvent()
	model, _ = m.Update(cmd())
	m = model.(GenerateModel)

	view := m.View()
	if !strings.Contains(view, "Explanation") {
		t.Error("expected 'Explanation' label in explain view")
	}
	if !strings.Contains(view, "lists files") {
		t.Error("expected explanation text in view")
	}
}

func TestGenerate_ExplainPersistsAfterReturn(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files in detail"), "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = model.(GenerateModel)
	m = drainExplainStream(m, 2)

	// Now in phaseAction — explanation should still be visible
	view := m.View()
	if !strings.Contains(view, "lists files in detail") {
		t.Error("expected explanation to persist in action bar view")
	}
	if !strings.Contains(view, "Run") {
		t.Error("expected action bar visible after explain")
	}
}

func TestGenerate_ExplainNilFuncIgnored(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = model.(GenerateModel)

	// Should stay in phaseAction since no explain func
	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction when explain func is nil, got %v", m.Phase())
	}
}

func TestGenerate_ExplainDoesNotAffectResult(t *testing.T) {
	events := makeEvents("docker ps")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("shows running containers"), "")
	m = drainStream(m, 2)

	// Explain
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = model.(GenerateModel)
	m = drainExplainStream(m, 2)

	// Now run
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = model.(GenerateModel)

	if m.Result().Command != "docker ps" {
		t.Errorf("expected 'docker ps', got %q", m.Result().Command)
	}
	if m.Result().Action != ActionRun {
		t.Errorf("expected ActionRun, got %v", m.Result().Action)
	}
}

func TestGenerate_PreflightAutoCorrectsBadBinary(t *testing.T) {
	// First stream returns a command with a nonexistent binary
	events := makeEvents("nonexistentbinary123 --foo")
	correctedStream := mockNewStream("ls -la")

	m := NewGenerateModel(events, noopCancel, nil, correctedStream, nil, "bash")
	m = drainStream(m, 2)

	// After preflight failure, it should have auto-restreamed
	if m.Phase() != phaseStreaming {
		t.Fatalf("expected phaseStreaming after preflight failure, got %v", m.Phase())
	}

	// Drain the correction stream
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after correction, got %v", m.Phase())
	}
	if m.Result().Command != "" {
		// Result is only set when an action is chosen
	}
	output := m.stream.Output()
	if output != "ls -la" {
		t.Errorf("expected corrected command 'ls -la', got %q", output)
	}
}

func TestGenerate_PreflightRespectsMaxRetries(t *testing.T) {
	// Both initial and corrected streams return bad commands
	events := makeEvents("nonexistentbinary123 --foo")
	badStream := func(messages []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return makeEvents("anothernonexistent456 --bar"), noopCancel, nil
	}

	m := NewGenerateModel(events, noopCancel, nil, badStream, nil, "bash")
	m = drainStream(m, 2) // first stream done -> preflight fails -> retry 1
	m = drainStream(m, 2) // second stream done -> preflight fails -> retry 2
	m = drainStream(m, 2) // third stream done -> max retries hit, accept as-is

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after max retries, got %v", m.Phase())
	}
}

func TestGenerate_PreflightSkippedWithEmptyShell(t *testing.T) {
	// With empty shell, preflight binary check still runs but syntax check doesn't
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction (no preflight with empty shell), got %v", m.Phase())
	}
}

func TestGenerate_AutoExplainEntersExplainPhase(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files"), "").WithAutoExplain(true)
	m = drainStream(m, 2)

	if m.Phase() != phaseExplain {
		t.Errorf("expected phaseExplain after stream completes with auto-explain, got %v", m.Phase())
	}
}

func TestGenerate_AutoExplainStreamsThenShowsActionBar(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files in detail"), "").WithAutoExplain(true)
	m = drainStream(m, 2)
	m = drainExplainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after auto-explain completes, got %v", m.Phase())
	}
	view := m.View()
	if !strings.Contains(view, "lists files in detail") {
		t.Error("expected explanation text to persist in action view")
	}
	if !strings.Contains(view, "Run") {
		t.Error("expected action bar visible after auto-explain")
	}
}

func TestGenerate_AutoExplainWithoutExplainFuncFallsBackToAction(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "").WithAutoExplain(true)
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction when no explain func configured, got %v", m.Phase())
	}
}

func TestGenerate_AutoExplainDisabledGoesStraightToAction(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files"), "")
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction without auto-explain, got %v", m.Phase())
	}
}
