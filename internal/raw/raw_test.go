package raw

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

type mockProvider struct {
	events []provider.StreamEvent
	err    error
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) StreamCompletion(_ context.Context, _ []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan provider.StreamEvent, len(m.events))
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func TestRun_OutputsCommand(t *testing.T) {
	p := &mockProvider{
		events: []provider.StreamEvent{
			{Token: "ls "},
			{Token: "-la"},
			{Done: true},
		},
	}

	var stdout bytes.Buffer
	err := Run(context.Background(), Opts{
		Provider: p,
		Model:    "test-model",
		Prompt:   "list files",
		Stdout:   &stdout,
		Stderr:   io.Discard,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "ls -la\n" {
		t.Errorf("got %q, want %q", stdout.String(), "ls -la\n")
	}
}

func TestRun_StripsFences(t *testing.T) {
	p := &mockProvider{
		events: []provider.StreamEvent{
			{Token: "```bash\n"},
			{Token: "find . -name '*.go'\n"},
			{Token: "```"},
			{Done: true},
		},
	}

	var stdout bytes.Buffer
	err := Run(context.Background(), Opts{
		Provider: p,
		Model:    "test-model",
		Prompt:   "find go files",
		Stdout:   &stdout,
		Stderr:   io.Discard,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "find . -name '*.go'\n" {
		t.Errorf("got %q, want %q", stdout.String(), "find . -name '*.go'\n")
	}
}

func TestRun_StreamError(t *testing.T) {
	p := &mockProvider{
		events: []provider.StreamEvent{
			{Token: "ls "},
			{Err: io.ErrUnexpectedEOF, Done: true},
		},
	}

	var stdout bytes.Buffer
	err := Run(context.Background(), Opts{
		Provider: p,
		Model:    "test-model",
		Prompt:   "list files",
		Stdout:   &stdout,
		Stderr:   io.Discard,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRun_ProviderInitError(t *testing.T) {
	p := &mockProvider{err: io.ErrClosedPipe}

	var stdout bytes.Buffer
	err := Run(context.Background(), Opts{
		Provider: p,
		Model:    "test-model",
		Prompt:   "anything",
		Stdout:   &stdout,
		Stderr:   io.Discard,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRun_ContextInPrompt(t *testing.T) {
	p := &capturingProvider{
		events: []provider.StreamEvent{
			{Token: "grep error app.log"},
			{Done: true},
		},
	}

	prompt := "<context>\nERROR: something broke at line 42\nWARNING: disk full\n</context>\n\nfix this error"

	var stdout bytes.Buffer
	err := Run(context.Background(), Opts{
		Provider: p,
		Model:    "test-model",
		Prompt:   prompt,
		Stdout:   &stdout,
		Stderr:   io.Discard,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(p.messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(p.messages))
	}

	userMsg := p.messages[1]
	if userMsg.Role != provider.RoleUser {
		t.Errorf("expected user role, got %s", userMsg.Role)
	}
	if userMsg.Content != prompt {
		t.Errorf("expected context in user message, got %q", userMsg.Content)
	}
}

type capturingProvider struct {
	events   []provider.StreamEvent
	messages []provider.Message
}

func (c *capturingProvider) Name() string { return "capturing" }

func (c *capturingProvider) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	c.messages = msgs
	ch := make(chan provider.StreamEvent, len(c.events))
	for _, ev := range c.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}
