package raw

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/rfizzle/llm"
)

type mockProvider struct {
	events []llm.StreamEvent
	err    error
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) StreamCompletion(_ context.Context, _ []llm.Message, _ llm.CompletionOpts) (<-chan llm.StreamEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan llm.StreamEvent, len(m.events))
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func TestRun_OutputsCommand(t *testing.T) {
	p := &mockProvider{
		events: []llm.StreamEvent{
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
		events: []llm.StreamEvent{
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
		events: []llm.StreamEvent{
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
		events: []llm.StreamEvent{
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
	if userMsg.Role != llm.RoleUser {
		t.Errorf("expected user role, got %s", userMsg.Role)
	}
	if userMsg.Content != prompt {
		t.Errorf("expected context in user message, got %q", userMsg.Content)
	}
}

// Run prints exactly what Generate returns, so the widget and the eval suite,
// which ask through Generate, read the answer shhh cmd --raw prints.
func TestRunPrintsWhatGenerateReturns(t *testing.T) {
	events := []llm.StreamEvent{{Token: "```sh\nls -la\n```\n"}, {Done: true}}

	got, err := Generate(context.Background(), Opts{Provider: &mockProvider{events: events}, Prompt: "list files"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var stdout bytes.Buffer
	if err := Run(context.Background(), Opts{Provider: &mockProvider{events: events}, Prompt: "list files", Stdout: &stdout}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "ls -la" || stdout.String() != got+"\n" {
		t.Errorf("Generate = %q, Run printed %q", got, stdout.String())
	}
}

// A caller that names a shell gets a prompt that names it, whatever the
// machine's own is.
func TestGenerateNamesThePinnedShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/fish")
	p := &capturingProvider{events: []llm.StreamEvent{{Token: "ls"}, {Done: true}}}
	if _, err := Generate(context.Background(), Opts{Provider: p, Prompt: "list files", Shell: "bash"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	sys := p.messages[0].Content
	if !strings.Contains(sys, "bash") || strings.Contains(sys, "fish") {
		t.Errorf("the prompt should name bash and not the detected fish:\n%s", sys)
	}
}

type capturingProvider struct {
	events   []llm.StreamEvent
	messages []llm.Message
}

func (c *capturingProvider) Name() string { return "capturing" }

func (c *capturingProvider) StreamCompletion(_ context.Context, msgs []llm.Message, _ llm.CompletionOpts) (<-chan llm.StreamEvent, error) {
	c.messages = msgs
	ch := make(chan llm.StreamEvent, len(c.events))
	for _, ev := range c.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}
