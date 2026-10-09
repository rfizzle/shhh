package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// The handoff is written around the person's note, from the person's asks
// and the assistant's prose, and the files and the item are the code's facts
// put in as they are.
func TestHandoffWriter_WritesTheChildsShape(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(_ int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		if opts.Model != "small" {
			t.Errorf("opts = %+v", opts)
		}
		return eventsOf(provider.StreamEvent{
			ToolCalls: []provider.ToolCall{{ID: "h1", Name: HandoffToolName, Arguments: `{"summary":"Retry backoff half done","done":["capped the backoff"],"open":["the timer test flakes"],"decisions":["no jitter"]}`}},
			Done:      true,
		}), nil
	}}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "fix the retry backoff"},
		{Role: provider.RoleUser, Content: "a check-in", Machine: true},
		{Role: provider.RoleAssistant, Content: "capped it at 30s"},
		{Role: provider.RoleTool, Content: "a tool result nobody may read"},
	}
	req := HandoffRequestFrom("pick up at the flake", "", msgs)
	req.Files, req.Item = []string{"retry.go", "retry_test.go"}, "retry-cap"
	v := NewHandoffWriter(p, HandoffConfig{Model: "small"}).Write(context.Background(), req)
	if v.Failed {
		t.Fatalf("expected a handoff, got %+v", v)
	}
	evidence := p.msgs[1].Content
	for _, want := range []string{"pick up at the flake", "fix the retry backoff", "capped it at 30s", "retry_test.go"} {
		if !strings.Contains(evidence, want) {
			t.Errorf("the evidence is missing %q: %s", want, evidence)
		}
	}
	for _, never := range []string{"a check-in", "a tool result"} {
		if strings.Contains(evidence, never) {
			t.Errorf("the evidence should not carry %q: %s", never, evidence)
		}
	}
	want := "Retry backoff half done\nnote: pick up at the flake\ndone: capped the backoff\nopen: the timer test flakes\ndecided: no jitter\nfiles: retry.go, retry_test.go\nitem: retry-cap"
	if got := v.Handoff.Text(); got != want {
		t.Fatalf("text =\n%s\nwant\n%s", got, want)
	}
	if HandoffFirstLine("\n  "+want) != "Retry backoff half done" {
		t.Fatalf("first line = %q", HandoffFirstLine(want))
	}
}

// A writer with no model asks nothing.
func TestHandoffWriter_UnconfiguredAsksNothing(t *testing.T) {
	v := NewHandoffWriter(nil, HandoffConfig{}).Write(context.Background(), HandoffRequest{First: "q"})
	if !v.Failed || v.Err == "" {
		t.Fatalf("expected a failed writing, got %+v", v)
	}
}
