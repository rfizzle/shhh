package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
)

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
	m := NewStreamModel(ch)

	view := m.View()
	if !strings.Contains(view, "Thinking") {
		t.Errorf("expected spinner/thinking text before first token, got: %q", view)
	}
	close(ch)
}

func TestStreamModel_RendersTokens(t *testing.T) {
	events := makeEvents("ls ", "-la")
	m := NewStreamModel(events)

	// Drain two token messages
	cmd := m.waitForEvent()
	msg := cmd()
	var model tea.Model = m
	model, _ = model.(StreamModel).Update(msg)

	cmd = model.(StreamModel).waitForEvent()
	msg = cmd()
	model, _ = model.(StreamModel).Update(msg)

	sm := model.(StreamModel)
	if sm.Output() != "ls -la" {
		t.Errorf("expected output 'ls -la', got %q", sm.Output())
	}
	if sm.Done() {
		t.Error("expected not done after tokens, before done msg")
	}
}

func TestStreamModel_DoneAfterStream(t *testing.T) {
	events := makeEvents("echo hi")
	m := NewStreamModel(events)

	var model tea.Model = m

	// Token
	cmd := model.(StreamModel).waitForEvent()
	model, _ = model.(StreamModel).Update(cmd())

	// Done
	cmd = model.(StreamModel).waitForEvent()
	model, _ = model.(StreamModel).Update(cmd())

	sm := model.(StreamModel)
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
	m := NewStreamModel(events)

	cmd := m.waitForEvent()
	model, _ := m.Update(cmd())

	sm := model.(StreamModel)
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
	m := NewStreamModel(events)

	cmd := m.waitForEvent()
	model, _ := m.Update(cmd())

	view := model.(StreamModel).View()
	if !strings.Contains(view, "connection refused") {
		t.Errorf("expected error in view, got: %q", view)
	}
}

func TestStreamModel_ViewShowsOutputAfterTokens(t *testing.T) {
	events := makeEvents("find . -name '*.go'")
	m := NewStreamModel(events)

	cmd := m.waitForEvent()
	model, _ := m.Update(cmd())

	view := model.(StreamModel).View()
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
	m := NewStreamModel(ch)

	cmd := m.waitForEvent()
	model, _ := m.Update(cmd())

	sm := model.(StreamModel)
	if !sm.Done() {
		t.Error("expected done when channel is closed")
	}
}
