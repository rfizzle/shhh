package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

func noopCancel() {}

func testCancel() (context.CancelFunc, *bool) {
	called := false
	return func() { called = true }, &called
}

func makeEvents(tokens ...string) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, len(tokens)+1)
	for _, t := range tokens {
		ch <- provider.StreamEvent{Token: t}
	}
	ch <- provider.StreamEvent{Done: true}
	close(ch)
	return ch
}

func makeErrorEvents(err error) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Err: err, Done: true}
	close(ch)
	return ch
}

func TestStreamModel_SpinnerBeforeFirstToken(t *testing.T) {
	ch := make(chan provider.StreamEvent, 1)
	m := NewStreamModel(ch, noopCancel)

	view := m.View()
	if !strings.Contains(view, "Thinking") {
		t.Errorf("expected spinner/thinking text before first token, got: %q", view)
	}
	close(ch)
}

func TestStreamModel_RendersTokens(t *testing.T) {
	events := makeEvents("ls ", "-la")
	m := NewStreamModel(events, noopCancel)

	// Drain two token messages
	cmd := m.waitForEvent()
	msg := cmd()
	model := m
	model, _ = model.Update(msg)

	cmd = model.waitForEvent()
	msg = cmd()
	model, _ = model.Update(msg)

	sm := model
	if sm.Output() != "ls -la" {
		t.Errorf("expected output 'ls -la', got %q", sm.Output())
	}
	if sm.Done() {
		t.Error("expected not done after tokens, before done msg")
	}
}

func TestStreamModel_DoneAfterStream(t *testing.T) {
	events := makeEvents("echo hi")
	m := NewStreamModel(events, noopCancel)

	model := m

	// Token
	cmd := model.waitForEvent()
	model, _ = model.Update(cmd())

	// Done
	cmd = model.waitForEvent()
	model, _ = model.Update(cmd())

	sm := model
	if !sm.Done() {
		t.Error("expected done after stream completes")
	}
	if sm.Err() != nil {
		t.Errorf("expected no error, got: %v", sm.Err())
	}
}

func TestStreamModel_ErrorSetsState(t *testing.T) {
	expected := errors.New("API rate limited")
	events := makeErrorEvents(expected)
	m := NewStreamModel(events, noopCancel)

	cmd := m.waitForEvent()
	model, _ := m.Update(cmd())

	sm := model
	if !sm.Done() {
		t.Error("expected done after error")
	}
	if sm.Err() == nil || sm.Err().Error() != expected.Error() {
		t.Errorf("expected error %q, got %v", expected, sm.Err())
	}
}

func TestStreamModel_ErrorView(t *testing.T) {
	expected := errors.New("connection refused")
	events := makeErrorEvents(expected)
	m := NewStreamModel(events, noopCancel)

	cmd := m.waitForEvent()
	model, _ := m.Update(cmd())

	view := model.View()
	if !strings.Contains(view, "connection refused") {
		t.Errorf("expected error in view, got: %q", view)
	}
}

func TestStreamModel_ViewShowsOutputAfterTokens(t *testing.T) {
	events := makeEvents("find . -name '*.go'")
	m := NewStreamModel(events, noopCancel)

	cmd := m.waitForEvent()
	model, _ := m.Update(cmd())

	view := model.View()
	if !strings.Contains(view, "find . -name '*.go'") {
		t.Errorf("expected command in view, got: %q", view)
	}
	if strings.Contains(view, "Thinking") {
		t.Error("spinner should be gone after first token")
	}
}

func TestStreamModel_ClosedChannelTriggersDone(t *testing.T) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	m := NewStreamModel(ch, noopCancel)

	cmd := m.waitForEvent()
	model, _ := m.Update(cmd())

	sm := model
	if !sm.Done() {
		t.Error("expected done when channel is closed")
	}
}

func TestStreamModel_EscCancelsStream(t *testing.T) {
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{Token: "partial"}
	cancel, called := testCancel()
	m := NewStreamModel(ch, cancel)

	// Receive one token
	model := m
	cmd := model.waitForEvent()
	model, _ = model.Update(cmd())

	// Press Esc
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	sm := model
	if !sm.Done() {
		t.Error("expected done after Esc")
	}
	if !sm.Cancelled() {
		t.Error("expected cancelled flag after Esc")
	}
	if !*called {
		t.Error("expected cancel function to be called")
	}
	close(ch)
}

func TestStreamModel_QCancelsStream(t *testing.T) {
	ch := make(chan provider.StreamEvent, 1)
	cancel, called := testCancel()
	m := NewStreamModel(ch, cancel)

	model, _ := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})

	sm := model
	if !sm.Done() {
		t.Error("expected done after q")
	}
	if !sm.Cancelled() {
		t.Error("expected cancelled flag after q")
	}
	if !*called {
		t.Error("expected cancel function to be called")
	}
	close(ch)
}

func TestStreamModel_KeyIgnoredAfterDone(t *testing.T) {
	events := makeEvents("echo hi")
	cancel, called := testCancel()
	m := NewStreamModel(events, cancel)

	model := m
	// Token
	cmd := model.waitForEvent()
	model, _ = model.Update(cmd())
	// Done
	cmd = model.waitForEvent()
	model, _ = model.Update(cmd())

	// Press q after already done — should not call cancel
	model, _ = model.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})

	sm := model
	if sm.Cancelled() {
		t.Error("should not set cancelled when already done")
	}
	if *called {
		t.Error("cancel should not be called when already done")
	}
}

func TestStreamModel_StripsMarkdownOnDone(t *testing.T) {
	events := makeEvents("```bash\nls -la\n```")
	m := NewStreamModel(events, noopCancel)

	model := m
	cmd := model.waitForEvent()
	model, _ = model.Update(cmd())
	cmd = model.waitForEvent()
	model, _ = model.Update(cmd())

	sm := model
	if sm.Output() != "ls -la" {
		t.Errorf("expected fences stripped, got %q", sm.Output())
	}
}

func TestStreamModel_StripsInlineBackticksOnDone(t *testing.T) {
	events := makeEvents("`docker ps -a`")
	m := NewStreamModel(events, noopCancel)

	model := m
	cmd := model.waitForEvent()
	model, _ = model.Update(cmd())
	cmd = model.waitForEvent()
	model, _ = model.Update(cmd())

	sm := model
	if sm.Output() != "docker ps -a" {
		t.Errorf("expected backticks stripped, got %q", sm.Output())
	}
}

func TestStreamModel_StripsOnCancel(t *testing.T) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Token: "```\npartial"}
	m := NewStreamModel(ch, noopCancel)

	model := m
	cmd := model.waitForEvent()
	model, _ = model.Update(cmd())

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	sm := model
	if sm.Output() != "partial" {
		t.Errorf("expected fences stripped on cancel, got %q", sm.Output())
	}
	close(ch)
}
