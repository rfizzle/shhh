package inline

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
	if stdout.String() != "ls -la" {
		t.Errorf("got %q, want %q", stdout.String(), "ls -la")
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
	if stdout.String() != "find . -name '*.go'" {
		t.Errorf("got %q, want %q", stdout.String(), "find . -name '*.go'")
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
