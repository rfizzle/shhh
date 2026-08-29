package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

var errTest = errors.New("test error")

func drainStream(m GenerateModel, events int) GenerateModel {
	for i := 0; i < events; i++ {
		cmd := m.stream.waitForEvent()
		model, next := m.Update(cmd())
		m = settle(model.(GenerateModel), next)
	}
	return m
}

// settle runs cmd far enough to deliver any stream the surface has asked for
// and not waited on (S-132). Opening a stream is a round trip that happens
// off the event loop now, so a test that wants the stream open has to let
// that answer come back; everything else the cmd carries is left alone.
// drainStreamPending drains the stream and stops at the point the surface has
// asked for its next one and not yet been answered — the frame the reader
// sees while that request is out.
func drainStreamPending(m GenerateModel, events int) GenerateModel {
	for i := 0; i < events; i++ {
		cmd := m.stream.waitForEvent()
		model, _ := m.Update(cmd())
		m = model.(GenerateModel)
	}
	return m
}

// step delivers one message and settles whatever it asked for, which is what
// the program does between two frames.
func step(m GenerateModel, msg tea.Msg) GenerateModel {
	model, cmd := m.Update(msg)
	return settle(model.(GenerateModel), cmd)
}

func settle(m GenerateModel, cmd tea.Cmd) GenerateModel {
	// Only a surface that is waiting on an answer has anything to settle.
	// Running a cmd is not free — the one that waits on the next token
	// takes it — so nothing else here is touched.
	if (!m.opening && !m.checking) || cmd == nil {
		return m
	}
	msg := openMsg(cmd)
	if msg == nil {
		return m
	}
	model, next := m.Update(msg)
	return settle(model.(GenerateModel), next)
}

// openMsg runs cmd far enough to find the answer to something the surface
// asked for and did not wait on, and reports nothing if cmd holds no such
// answer.
func openMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			if found := openMsg(c); found != nil {
				return found
			}
		}
	case explainReadyMsg:
		return msg
	case streamReadyMsg:
		return msg
	case preflightDoneMsg:
		return msg
	}
	return nil
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

	view := m.View().Content
	if !strings.Contains(view, "[↵] run") {
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

	// This one is destructive, so running it is the deliberate key.
	model, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
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

	m = step(m, tea.KeyPressMsg{Code: 'c', Text: "c"})

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

	m = step(m, tea.KeyPressMsg{Code: tea.KeyEscape})

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
	m = step(m, cmd())

	// Press Esc during stream
	model, quitCmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.Result().Command != "find . -name '*.log'" {
		t.Errorf("expected stripped command, got %q", m.Result().Command)
	}
}

func TestGenerate_ArrowsAreNotNavigation(t *testing.T) {
	events := makeEvents("pwd")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// The bar has no cursor: an arrow changes nothing, and enter still runs.
	m = step(m, tea.KeyPressMsg{Code: tea.KeyRight})
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.Result().Action != ActionRun {
		t.Errorf("expected ActionRun after arrow+enter, got %v", m.Result().Action)
	}
}

func TestGenerate_ReviseOpensTextInput(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Press 'r' to revise
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})

	if m.Phase() != phaseRevise {
		t.Errorf("expected phaseRevise, got %v", m.Phase())
	}
}

func TestGenerate_ReviseEscReturnsToAction(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Enter revise
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})

	// Press Esc to cancel revision
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after Esc, got %v", m.Phase())
	}
}

func TestGenerate_ReviseSubmitWithoutStreamFuncQuits(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Enter revise
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})

	// Type feedback
	for _, r := range "add -la flag" {
		m = step(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	// Submit — no newStream func, so it falls back to quit
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})

	// Submit empty
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.Phase() != phaseRevise {
		t.Errorf("expected to stay in phaseRevise on empty submit, got %v", m.Phase())
	}
}

func TestGenerate_ReviseViewShowsFeedbackPrompt(t *testing.T) {
	events := makeEvents("echo hi")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})

	view := m.View().Content
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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})

	// Type and submit feedback
	for _, r := range "add -la" {
		m = step(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEscape})

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
		model, cmd := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = settle(model.(GenerateModel), cmd)
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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = typeKeys(m, "add -la")
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = model.(GenerateModel)

	if m.Phase() != phaseStreaming {
		t.Fatalf("expected phaseStreaming after revise submit, got %v", m.Phase())
	}

	// Init should return a batch command
	if cmd == nil {
		t.Fatal("expected Init cmd from new stream")
	}

	// The stream is asked for and not waited on (S-132), so let the open
	// come back before draining what it opened.
	m = settle(m, cmd)

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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = typeKeys(m, "add -la")
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = drainStream(m, 2)

	// Now select Run — result should have the NEW command
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = typeKeys(m, "add -la")
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = typeKeys(m, "fix it")
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = drainStream(m, 2)

	view := m.View().Content
	if !strings.Contains(view, "[↵] run") {
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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = typeKeys(m, "try again")
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = model.(GenerateModel)

	// The stream is opened off the event loop now (S-132), so the failure is
	// the answer to that open rather than something the keystroke returned.
	model, quitCmd := m.Update(openMsg(cmd))
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

	m = step(m, tea.KeyPressMsg{Code: 'e', Text: "e"})

	if m.Phase() != phaseEdit {
		t.Errorf("expected phaseEdit, got %v", m.Phase())
	}
}

func TestGenerate_EditPrePopulatesCommand(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	m = step(m, tea.KeyPressMsg{Code: 'e', Text: "e"})

	view := m.View().Content
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

	m = step(m, tea.KeyPressMsg{Code: 'e', Text: "e"})

	m = step(m, tea.KeyPressMsg{Code: tea.KeyEscape})

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
	m = step(m, tea.KeyPressMsg{Code: 'e', Text: "e"})

	// Clear and type new command (select all not available, so we manipulate directly)
	// The text input has "ls" pre-populated; type " -la" to append
	for _, r := range " -la" {
		m = step(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	// Submit
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	m = step(m, tea.KeyPressMsg{Code: 'e', Text: "e"})
	for _, r := range " -la" {
		m = step(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	m = step(m, tea.KeyPressMsg{Code: 'e', Text: "e"})
	for _, r := range " -la" {
		m = step(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	// Now select Run — result should have the edited command
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.Result().Command != "ls -la" {
		t.Errorf("expected 'ls -la' in result, got %q", m.Result().Command)
	}
}

func TestGenerate_EditEmptyIgnored(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	// Enter edit
	m = step(m, tea.KeyPressMsg{Code: 'e', Text: "e"})

	// Clear the input by pressing backspace twice
	m = step(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = step(m, tea.KeyPressMsg{Code: tea.KeyBackspace})

	// Submit empty
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = typeKeys(m, "add -l")
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = drainStream(m, 2)

	if m.stream.Output() != "ls -l" {
		t.Errorf("expected 'ls -l' after first revision, got %q", m.stream.Output())
	}

	// Second revision
	m = step(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = typeKeys(m, "also add -a")
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = drainStream(m, 2)

	if m.stream.Output() != "ls -la" {
		t.Errorf("expected 'ls -la' after second revision, got %q", m.stream.Output())
	}

	// Messages: sys, user, asst("ls"), user("add -l"), asst("ls -l"), user("also add -a"), asst("ls -la")
	msgs := m.Messages()
	if len(msgs) != 7 {
		t.Fatalf("expected 7 messages after two revisions, got %d", len(msgs))
	}

	// Enter runs — should get final command
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Result().Command != "ls -la" {
		t.Errorf("expected final command 'ls -la', got %q", m.Result().Command)
	}
}

func mockExplainStream(tokens ...string) ExplainStreamFunc {
	return func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return makeEvents(tokens...), noopCancel, nil
	}
}

func drainExplainStream(m GenerateModel, events int) GenerateModel {
	for i := 0; i < events; i++ {
		cmd := m.explainStream.waitForEvent()
		model, next := m.Update(cmd())
		m = settle(model.(GenerateModel), next)
	}
	return m
}

func TestGenerate_ExplainOpensExplainPhase(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files"), "")
	m = drainStream(m, 2)

	m = step(m, tea.KeyPressMsg{Code: 'x', Text: "x"})

	if m.Phase() != phaseExplain {
		t.Errorf("expected phaseExplain, got %v", m.Phase())
	}
}

func TestGenerate_ExplainStreamsAndReturnsToAction(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files in detail"), "")
	m = drainStream(m, 2)

	m = step(m, tea.KeyPressMsg{Code: 'x', Text: "x"})

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

	m = step(m, tea.KeyPressMsg{Code: 'x', Text: "x"})

	// Receive token
	cmd := m.explainStream.waitForEvent()
	m = step(m, cmd())

	view := m.View().Content
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

	m = step(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = drainExplainStream(m, 2)

	// Now in phaseAction — explanation should still be visible
	view := m.View().Content
	if !strings.Contains(view, "lists files in detail") {
		t.Error("expected explanation to persist in action bar view")
	}
	if !strings.Contains(view, "[↵] run") {
		t.Error("expected action bar visible after explain")
	}
}

func TestGenerate_ExplainNilFuncIgnored(t *testing.T) {
	events := makeEvents("ls")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)

	m = step(m, tea.KeyPressMsg{Code: 'x', Text: "x"})

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
	m = step(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = drainExplainStream(m, 2)

	// Now run
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})

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

func TestGenerate_LongExplainEntersExplainPhase(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files"), "").WithExplain(ExplainLong)
	m = drainStream(m, 2)

	if m.Phase() != phaseExplain {
		t.Errorf("expected phaseExplain after stream completes with auto-explain, got %v", m.Phase())
	}
}

func TestGenerate_LongExplainStreamsThenShowsActionBar(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files in detail"), "").WithExplain(ExplainLong)
	m = drainStream(m, 2)
	m = drainExplainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction after auto-explain completes, got %v", m.Phase())
	}
	view := m.View().Content
	if !strings.Contains(view, "lists files in detail") {
		t.Error("expected explanation text to persist in action view")
	}
	if !strings.Contains(view, "[↵] run") {
		t.Error("expected action bar visible after auto-explain")
	}
}

func TestGenerate_ExplainWithoutExplainFuncFallsBackToAction(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, nil, "").WithExplain(ExplainLong)
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction when no explain func configured, got %v", m.Phase())
	}
}

func TestGenerate_SilentExplainGoesStraightToAction(t *testing.T) {
	events := makeEvents("ls -la")
	m := NewGenerateModel(events, noopCancel, nil, nil, mockExplainStream("lists files"), "").
		WithExplain(ExplainNone)
	m = drainStream(m, 2)

	if m.Phase() != phaseAction {
		t.Errorf("expected phaseAction with explanations suppressed, got %v", m.Phase())
	}
	if strings.Contains(m.View().Content, "lists files") {
		t.Error("silent mode explained the command anyway")
	}
}
