package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// fakeClassifierProvider scripts StreamCompletion responses for the decision
// pipeline tests.
type fakeClassifierProvider struct {
	fn    func(attempt int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error)
	calls int
	// msgs is the last request's messages, for the tests that assert which
	// half of the prompt went in which channel.
	msgs []provider.Message
}

func (f *fakeClassifierProvider) StreamCompletion(ctx context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	f.calls++
	f.msgs = msgs
	return f.fn(f.calls, opts)
}

func (f *fakeClassifierProvider) Name() string { return "fake" }

func eventsOf(evs ...provider.StreamEvent) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	return ch
}

func decisionCall(args string) provider.StreamEvent {
	return provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "d1", Name: DecisionToolName, Arguments: args}},
		Usage:     &provider.Usage{PromptTokens: 100, CompletionTokens: 20},
		Done:      true,
	}
}

func testRequest() ClassifierRequest {
	return ClassifierRequest{
		Tool:      "execute_command",
		Arguments: `{"command":"go test ./..."}`,
		CWD:       "/work",
		Recent: []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "run the tests"},
		},
	}
}

func TestClassifier_ToolCallAllow(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(decisionCall(`{"decision":"allow","reason":"runs the requested tests"}`)), nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest())
	if v.Decision != Allow || v.Failed {
		t.Fatalf("expected a clean allow, got %+v", v)
	}
	if v.Reason != "runs the requested tests" {
		t.Fatalf("reason = %q", v.Reason)
	}
	if v.Usage.PromptTokens != 100 || v.Usage.CompletionTokens != 20 {
		t.Fatalf("usage should be captured, got %+v", v.Usage)
	}
	if p.calls != 1 {
		t.Fatalf("expected one attempt, got %d", p.calls)
	}
}

func TestClassifier_ToolCallDeny(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(decisionCall(`{"decision":"DENY","reason":"user said read only"}`)), nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest())
	if v.Decision != Deny || v.Failed || v.Reason != "user said read only" {
		t.Fatalf("expected a clean deny, got %+v", v)
	}
}

func TestClassifier_TextFallbacks(t *testing.T) {
	cases := []struct {
		text   string
		want   Decision
		reason string
	}{
		{`{"decision":"deny","reason":"out of scope"}`, Deny, "out of scope"},
		{"```json\n{\"decision\":\"allow\",\"reason\":\"fine\"}\n```", Allow, "fine"},
		{"The verdict follows. {\"decision\":\"deny\",\"reason\":\"nope\"} Thanks.", Deny, "nope"},
		{"ALLOW: matches the request", Allow, "matches the request"},
		{"deny - touches credentials", Deny, "touches credentials"},
		{"DENY", Deny, "the action is not safe to run automatically"},
	}
	for _, c := range cases {
		p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
			return eventsOf(provider.StreamEvent{Token: c.text, Done: true}), nil
		}}
		v := NewClassifier(p, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest())
		if v.Decision != c.want || v.Failed || v.Reason != c.reason {
			t.Errorf("text %q: got %+v, want %v %q", c.text, v, c.want, c.reason)
		}
	}
}

func TestClassifier_InvalidResponseRetriesThenFailsClosed(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(provider.StreamEvent{Token: "I am not sure what to do here.", Done: true}), nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m", Retries: 2}).Judge(context.Background(), testRequest())
	if v.Decision != Ask || !v.Failed {
		t.Fatalf("invalid responses must fail closed to Ask, got %+v", v)
	}
	if !strings.Contains(v.Reason, "invalid decision") {
		t.Fatalf("reason should mention the invalid decision, got %q", v.Reason)
	}
	if p.calls != 3 {
		t.Fatalf("expected retries+1 = 3 attempts, got %d", p.calls)
	}
}

func TestClassifier_RequestErrorFailsClosed(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return nil, errors.New("401 unauthorized")
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m", Retries: 1}).Judge(context.Background(), testRequest())
	if v.Decision != Ask || !v.Failed || !strings.Contains(v.Reason, "401 unauthorized") {
		t.Fatalf("request failures must fail closed with the error, got %+v", v)
	}
	if p.calls != 2 {
		t.Fatalf("failed requests should still be retried, got %d attempts", p.calls)
	}
}

func TestClassifier_StreamErrorFailsClosed(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(provider.StreamEvent{Err: errors.New("connection reset")}), nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest())
	if v.Decision != Ask || !v.Failed || !strings.Contains(v.Reason, "connection reset") {
		t.Fatalf("stream errors must fail closed, got %+v", v)
	}
}

func TestClassifier_TimeoutFailsClosed(t *testing.T) {
	// A provider that ignores cancellation and never sends anything.
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return make(chan provider.StreamEvent), nil
	}}
	c := NewClassifier(p, ClassifierConfig{Model: "m", Timeout: 20 * time.Millisecond, Retries: 1})
	v := c.Judge(context.Background(), testRequest())
	if v.Decision != Ask || !v.Failed {
		t.Fatalf("timeouts must fail closed to Ask, got %+v", v)
	}
	if p.calls != 2 {
		t.Fatalf("a timed-out attempt should be retried, got %d attempts", p.calls)
	}
}

func TestClassifier_SessionCancelStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return make(chan provider.StreamEvent), nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m", Retries: 3}).Judge(ctx, testRequest())
	if v.Decision != Ask || !v.Failed {
		t.Fatalf("a cancelled session must fail closed, got %+v", v)
	}
	if p.calls > 1 {
		t.Fatalf("session cancellation must not retry, got %d attempts", p.calls)
	}
}

func TestClassifier_NotConfiguredFailsClosed(t *testing.T) {
	cases := map[string]*Classifier{
		"nil classifier": nil,
		"nil provider":   NewClassifier(nil, ClassifierConfig{Model: "m"}),
		"no model":       NewClassifier(&fakeClassifierProvider{}, ClassifierConfig{}),
	}
	for name, c := range cases {
		v := c.Judge(context.Background(), testRequest())
		if v.Decision != Ask || !v.Failed || !strings.Contains(v.Reason, "not configured") {
			t.Errorf("%s: must fail closed as unconfigured, got %+v", name, v)
		}
	}
}

func TestClassifier_UsageAccumulatesAcrossAttempts(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(attempt int, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		if attempt == 1 {
			return eventsOf(provider.StreamEvent{
				Token: "unparseable",
				Usage: &provider.Usage{PromptTokens: 50, CompletionTokens: 5},
				Done:  true,
			}), nil
		}
		return eventsOf(decisionCall(`{"decision":"allow","reason":"ok"}`)), nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m", Retries: 1}).Judge(context.Background(), testRequest())
	if v.Decision != Allow {
		t.Fatalf("second attempt should succeed, got %+v", v)
	}
	if v.Usage.PromptTokens != 150 || v.Usage.CompletionTokens != 25 {
		t.Fatalf("usage should sum across attempts, got %+v", v.Usage)
	}
}

func TestResolveAuto_Backstop(t *testing.T) {
	flagged := Action{Kind: ActionCommand, Command: "git reset --hard", SafetyFlagged: true}
	plain := Action{Kind: ActionCommand, Command: "go test"}

	if d, _ := ResolveAuto(plain, ClassifierVerdict{Decision: Allow, Reason: "ok"}); d != Allow {
		t.Fatal("a clean allow should pass through")
	}
	if d, reason := ResolveAuto(flagged, ClassifierVerdict{Decision: Allow, Reason: "ok"}); d != Ask || !strings.Contains(reason, "safety-flagged") {
		t.Fatalf("safety-flagged actions must ask even after classifier ALLOW, got %v %q", d, reason)
	}
	if d, reason := ResolveAuto(flagged, ClassifierVerdict{Decision: Deny, Reason: "no"}); d != Deny || reason != "no" {
		t.Fatal("deny passes through with the classifier's reason")
	}
	if d, _ := ResolveAuto(plain, ClassifierVerdict{Decision: Ask, Reason: "unavailable", Failed: true}); d != Ask {
		t.Fatal("a failed-closed verdict stays an Ask")
	}
}

func TestRecentContext_Bounds(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "system prompt"},
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleTool, Content: "tool output", ToolCallID: "t1"},
		{Role: provider.RoleAssistant, Content: "working on it"},
		{Role: provider.RoleUser, Content: "second"},
	}
	got := RecentContext(msgs, 2, 1000)
	if strings.Contains(got, "system prompt") || strings.Contains(got, "tool output") {
		t.Fatalf("system and tool messages must be excluded, got %q", got)
	}
	if strings.Contains(got, "first") {
		t.Fatalf("only the last 2 messages should survive, got %q", got)
	}
	if !strings.Contains(got, "[Assistant]\nworking on it") || !strings.Contains(got, "[User]\nsecond") {
		t.Fatalf("recent messages should be labeled by role, got %q", got)
	}

	long := RecentContext([]provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("x", 500) + "TAIL"},
	}, 10, 100)
	if len(long) > 100 || !strings.HasSuffix(long, "TAIL") || !strings.HasPrefix(long, "[earlier context omitted]") {
		t.Fatalf("oversized context must keep the tail under the cap, got %d chars: %q", len(long), long)
	}
}

// thinkingProvider models what every current frontier model does: it spends
// part of the output ceiling on a thought before it says anything, and a
// ceiling that does not reach the end of the thought ends the response with
// nothing in it — no tool call, no text, no error.
type thinkingProvider struct {
	spend  int
	answer provider.StreamEvent
	seen   provider.CompletionOpts
}

func (t *thinkingProvider) Name() string { return "thinking" }

func (t *thinkingProvider) StreamCompletion(_ context.Context, _ []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	t.seen = opts
	if opts.MaxTokens <= t.spend {
		return eventsOf(provider.StreamEvent{Done: true}), nil
	}
	return eventsOf(t.answer), nil
}

// lowBudget is what a shallow thought costs on the dialects whose knob is a
// number rather than a name — the floor the ceilings here have to clear.
func lowBudget() int { return provider.EffortLow.ThinkingBudget(0) }

// A ceiling sized for the verdict alone never reaches one on a model that
// thinks first: the classifier reads the empty response as an invalid answer
// and fails closed to Ask, which is auto mode silently behaving as though it
// were switched off.
func TestClassifier_CeilingLeavesRoomForTheThought(t *testing.T) {
	answer := decisionCall(`{"decision":"allow","reason":"runs the requested tests"}`)

	cramped := &thinkingProvider{spend: lowBudget(), answer: answer}
	v := NewClassifier(cramped, ClassifierConfig{Model: "m", MaxTokens: 1024}).Judge(context.Background(), testRequest())
	if v.Decision != Ask || !v.Failed {
		t.Fatalf("a ceiling under the thought should fail closed, got %+v", v)
	}

	roomy := &thinkingProvider{spend: lowBudget(), answer: answer}
	v = NewClassifier(roomy, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest())
	if v.Failed || v.Decision != Allow {
		t.Fatalf("the default ceiling should reach a verdict, got %+v", v)
	}
	if DefaultClassifierMaxTokens <= lowBudget() {
		t.Fatalf("the default ceiling %d does not clear a low thought", DefaultClassifierMaxTokens)
	}
}

// The instruction goes in the dialect's own instruction channel and the
// evidence in the user turn, so the prompt's warning that the evidence is
// data is backed by the structure and not only by the sentence. The retry's
// line is an instruction and joins the instructions.
func TestClassifier_RequestShape(t *testing.T) {
	var seen provider.CompletionOpts
	p := &fakeClassifierProvider{fn: func(attempt int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		seen = opts
		if attempt == 1 {
			return eventsOf(provider.StreamEvent{Token: "nonsense", Done: true}), nil
		}
		return eventsOf(decisionCall(`{"decision":"deny","reason":"unrelated"}`)), nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest())
	if v.Failed || v.Decision != Deny {
		t.Fatalf("verdict = %+v", v)
	}
	if len(p.msgs) != 2 || p.msgs[0].Role != provider.RoleSystem || p.msgs[1].Role != provider.RoleUser {
		t.Fatalf("messages = %+v", p.msgs)
	}
	if !strings.Contains(p.msgs[0].Content, "security permission classifier") {
		t.Errorf("the instruction should be the system message, got %q", p.msgs[0].Content)
	}
	if !strings.Contains(p.msgs[0].Content, "did not contain a valid") {
		t.Error("the retry's line is an instruction and belongs with the instructions")
	}
	if strings.Contains(p.msgs[0].Content, "UNTRUSTED EVIDENCE") || strings.Contains(p.msgs[1].Content, "security permission classifier") {
		t.Error("the evidence and the instruction must not share a message")
	}
	if !strings.Contains(p.msgs[1].Content, "run the tests") {
		t.Errorf("the evidence should be the user turn, got %q", p.msgs[1].Content)
	}
	if seen.Effort != provider.EffortLow {
		t.Errorf("effort = %v", seen.Effort)
	}
	if seen.MaxTokens != DefaultClassifierMaxTokens {
		t.Errorf("max tokens = %d", seen.MaxTokens)
	}
}

// The verdict is asked for twice on one request: as a schema the answer must
// match, and as the tool a model that takes no schema is offered. The
// provider picks, so both have to be there — and either way the classifier
// fails closed when the request itself fails.
func TestClassifier_OffersASchemaAndTheToolTogether(t *testing.T) {
	var seen provider.CompletionOpts
	p := &fakeClassifierProvider{fn: func(_ int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		seen = opts
		// What a model answering to a schema sends back: the object alone,
		// with no tool call under it.
		return eventsOf(provider.StreamEvent{Token: `{"decision":"deny","reason":"unrelated"}`, Done: true}), nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest())
	if v.Failed || v.Decision != Deny || v.Reason != "unrelated" {
		t.Fatalf("a schema-shaped answer should be read, got %+v", v)
	}
	if seen.ResponseSchema == nil || seen.ResponseSchema.Name != DecisionToolName {
		t.Fatalf("response schema = %+v", seen.ResponseSchema)
	}
	if !bytes.Equal(seen.ResponseSchema.Schema, decisionSchema) {
		t.Errorf("the schema and the tool describe one shape, got %s", seen.ResponseSchema.Schema)
	}
	if len(seen.Tools) != 1 || seen.Tools[0].Name != DecisionToolName {
		t.Errorf("the tool must still be offered, got %+v", seen.Tools)
	}
	// Strict validation is refused on a schema that leaves an object open.
	var shape map[string]any
	if err := json.Unmarshal(decisionSchema, &shape); err != nil {
		t.Fatal(err)
	}
	if shape["additionalProperties"] != false {
		t.Errorf("the schema must close, got %v", shape)
	}

	failing := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return nil, errors.New("503 overloaded")
	}}
	if v := NewClassifier(failing, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest()); v.Decision != Ask || !v.Failed {
		t.Fatalf("a provider error still fails closed, got %+v", v)
	}
}
