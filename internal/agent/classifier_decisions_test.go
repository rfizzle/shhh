package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// todaysWording is the digest of the built-in instruction as it stood before
// the classifier had a second backend. The instruction was split into the
// pieces the decisions predicate shares with it, and the split had to leave
// every byte where it was: a classifier left on the default is asked
// exactly what it was asked before. Change the wording on purpose and this
// moves with it; change it by accident and this is what says so.
const todaysWording = "45dbcafdb1417c0229513a66ae45368a6917dcb55137775dc10ff8952532ed09"

// Unset, or `completion`, the request is the one the classifier always sent:
// the same instruction to the byte, the evidence as the user turn, the same
// ceiling, level, schema and tool — and no Decisions API request at all, even
// on a provider that could answer one.
func TestClassifier_DefaultBackendSendsTodaysRequest(t *testing.T) {
	sum := sha256.Sum256([]byte(ClassifierWording()))
	if got := hex.EncodeToString(sum[:]); got != todaysWording {
		t.Fatalf("the built-in wording moved: %s", got)
	}
	for _, backend := range []string{"", BackendCompletion, " Completion "} {
		var seen provider.CompletionOpts
		d := &fakeDecider{fakeClassifierProvider: &fakeClassifierProvider{fn: func(_ int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
			seen = opts
			return eventsOf(decisionCall(`{"decision":"allow","reason":"runs the tests"}`)), nil
		}}, offers: true}
		v := NewClassifier(d, ClassifierConfig{Model: "m", Backend: backend, Threshold: 10}).Judge(context.Background(), testRequest())
		if v.Failed || v.Decision != Allow || v.Reason != "runs the tests" {
			t.Fatalf("%q: verdict = %+v", backend, v)
		}
		if d.decided != 0 {
			t.Fatalf("%q: the completion backend asked the Decisions API", backend)
		}
		if len(d.msgs) != 2 || d.msgs[0].Content != ClassifierWording() {
			t.Fatalf("%q: the system message is not the built-in wording", backend)
		}
		const evidence = "UNTRUSTED EVIDENCE:\n" +
			`{"proposed_action":{"arguments":"{\"command\":\"go test ./...\"}","tool":"execute_command"},` +
			`"recent_conversation":"[User]\nrun the tests","working_directory":"/work"}`
		if d.msgs[1].Content != evidence {
			t.Fatalf("%q: evidence =\n%s", backend, d.msgs[1].Content)
		}
		if seen.Model != "m" || seen.MaxTokens != DefaultClassifierMaxTokens || seen.Effort != provider.EffortLow ||
			seen.ResponseSchema == nil || seen.ResponseSchema.Name != DecisionToolName ||
			len(seen.Tools) != 1 || seen.ToolChoice != "auto" {
			t.Fatalf("%q: options = %+v", backend, seen)
		}
	}
}

// fakeDecider is a classifier provider that also answers the Decisions API,
// one scripted result per attempt.
type fakeDecider struct {
	*fakeClassifierProvider
	offers  bool
	decide  func(attempt int, ctx context.Context) (provider.DecisionResult, error)
	decided int
	req     provider.DecisionRequest
}

func (f *fakeDecider) OffersDecisions(string) bool { return f.offers }

func (f *fakeDecider) Decide(ctx context.Context, req provider.DecisionRequest) (provider.DecisionResult, error) {
	f.decided++
	f.req = req
	return f.decide(f.decided, ctx)
}

func predicate(p float64) provider.DecisionResult {
	return provider.DecisionResult{
		Answers: []provider.DecisionAnswer{{Type: provider.AnswerPredicate, Name: decisionsQuestionName, Probability: p}},
		Usage:   provider.Usage{PromptTokens: 40},
	}
}

// With `decisions` set, the evidence the completion backend sends is the
// request's input and the rules are one predicate's instructions, and never
// the other way round; a replaced wording, written for a reply in words, is
// not sent. The probability is held against the threshold — at or above is
// Allow, below is Deny — and the sentence the card draws names both.
func TestClassifier_DecisionsBackendAsksAPredicate(t *testing.T) {
	d := &fakeDecider{fakeClassifierProvider: &fakeClassifierProvider{}, offers: true,
		decide: func(int, context.Context) (provider.DecisionResult, error) { return predicate(0.795), nil }}
	cfg := ClassifierConfig{Model: "gpt-6-luna", Backend: BackendDecisions, Prompt: "MY OWN WORDING"}
	v := NewClassifier(d, cfg).Judge(context.Background(), testRequest())
	if v.Failed || v.Decision != Deny {
		t.Fatalf("verdict = %+v", v)
	}
	const deny = "the classifier put the chance this may run unasked at 79%, under the 80% threshold"
	if v.Reason != deny {
		t.Fatalf("reason = %q", v.Reason)
	}
	if v.Usage.PromptTokens != 40 || v.Usage.CompletionTokens != 0 {
		t.Fatalf("usage = %+v", v.Usage)
	}
	if d.calls != 0 {
		t.Fatal("the decisions backend sent a completion")
	}
	req := d.req
	if req.Model != "gpt-6-luna" || len(req.Questions) != 1 {
		t.Fatalf("request = %+v", req)
	}
	q := req.Questions[0]
	if q.Type != provider.QuestionPredicate || q.Name != decisionsQuestionName || q.Instructions != decisionsPredicate {
		t.Fatalf("question = %+v", q)
	}
	if !strings.HasPrefix(req.Input, "UNTRUSTED EVIDENCE:\n{\"proposed_action\":") || !strings.Contains(req.Input, "run the tests") {
		t.Fatalf("input = %q", req.Input)
	}
	if strings.Contains(q.Instructions, "run the tests") || strings.Contains(q.Instructions, "UNTRUSTED EVIDENCE") {
		t.Fatal("the evidence must never be in the instructions")
	}
	if strings.Contains(q.Instructions, "MY OWN WORDING") || strings.Contains(req.Input, "MY OWN WORDING") {
		t.Fatal("a replaced wording is not sent to the decisions backend")
	}
	for _, rule := range []string{classifierAllowWhen, classifierDenyWhen, classifierHostStanding} {
		if !strings.Contains(q.Instructions, rule) || !strings.Contains(ClassifierWording(), rule) {
			t.Fatal("both backends are asked under the same rules")
		}
	}

	for _, c := range []struct {
		p         float64
		threshold int
		want      Decision
		reason    string
	}{
		{0.80, 0, Allow, "the classifier put the chance this may run unasked at 80%, at or over the 80% threshold"},
		{0.93, 0, Allow, "the classifier put the chance this may run unasked at 93%, at or over the 80% threshold"},
		{0.29, 30, Deny, "the classifier put the chance this may run unasked at 29%, under the 30% threshold"},
		{0.5, 50, Allow, "the classifier put the chance this may run unasked at 50%, at or over the 50% threshold"},
		{1, 101, Deny, "the classifier put the chance this may run unasked at 100%, under the 101% threshold"},
	} {
		d.decide = func(int, context.Context) (provider.DecisionResult, error) { return predicate(c.p), nil }
		cfg.Threshold = c.threshold
		v := NewClassifier(d, cfg).Judge(context.Background(), testRequest())
		if v.Failed || v.Decision != c.want || v.Reason != c.reason {
			t.Errorf("p=%v threshold=%d: %+v", c.p, c.threshold, v)
		}
	}
}

// Every way of not getting a probability fails closed: a refusal, an error, a
// timeout, an answer missing for the question, and a model the provider says
// does not offer the API — and so does a provider that cannot speak it at
// all. A person in front of the session is asked; a run with nobody there
// refuses. A refusal spends no retry; the rest take the retries a completion
// would.
func TestClassifier_DecisionsBackendFailsClosed(t *testing.T) {
	refusal := provider.DecisionResult{Answers: []provider.DecisionAnswer{{Type: provider.AnswerRefusal, Name: decisionsQuestionName}}}
	missing := provider.DecisionResult{Answers: []provider.DecisionAnswer{{Type: provider.AnswerPredicate, Name: "other", Probability: 1}}}
	cases := []struct {
		name     string
		offers   bool
		decide   func(int, context.Context) (provider.DecisionResult, error)
		attempts int
		reason   string
	}{
		{"refusal", true, func(int, context.Context) (provider.DecisionResult, error) { return refusal, nil }, 1, "declined"},
		{"error", true, func(int, context.Context) (provider.DecisionResult, error) {
			return provider.DecisionResult{}, errors.New("503 overloaded")
		}, 2, "503 overloaded"},
		{"timeout", true, func(_ int, ctx context.Context) (provider.DecisionResult, error) {
			<-ctx.Done()
			return provider.DecisionResult{}, ctx.Err()
		}, 2, "deadline"},
		{"missing answer", true, func(int, context.Context) (provider.DecisionResult, error) { return missing, nil }, 2, "no answer"},
		{"not offered", false, func(int, context.Context) (provider.DecisionResult, error) { return predicate(1), nil }, 0, "does not offer"},
	}
	plain := Action{Kind: ActionCommand, Command: "go test ./..."}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &fakeDecider{fakeClassifierProvider: &fakeClassifierProvider{}, offers: c.offers, decide: c.decide}
			cfg := ClassifierConfig{Model: "gpt-6-luna", Backend: BackendDecisions, Retries: 1, Timeout: 20 * time.Millisecond}
			v := NewClassifier(d, cfg).Judge(context.Background(), testRequest())
			if v.Decision != Ask || !v.Failed || !strings.Contains(v.Reason, c.reason) {
				t.Fatalf("verdict = %+v", v)
			}
			if d.decided != c.attempts {
				t.Fatalf("attempts = %d, want %d", d.decided, c.attempts)
			}
			if got, _ := ResolveAuto(plain, v); got != Ask {
				t.Errorf("ResolveAuto = %v, want the person asked", got)
			}
			if got, _ := ResolveUnattended(plain, v); got != Deny {
				t.Errorf("ResolveUnattended = %v, want a refusal", got)
			}
		})
	}

	// A provider that cannot speak the API at all is the same as a model
	// that does not offer it.
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		t.Fatal("a decisions classifier fell back to a completion")
		return nil, nil
	}}
	v := NewClassifier(p, ClassifierConfig{Model: "m", Backend: BackendDecisions}).Judge(context.Background(), testRequest())
	if v.Decision != Ask || !v.Failed || !strings.Contains(v.Reason, "does not offer") {
		t.Fatalf("a provider with no Decider = %+v", v)
	}
	// And a backend word nobody knows asks nothing at all.
	v = NewClassifier(p, ClassifierConfig{Model: "m", Backend: "oracle"}).Judge(context.Background(), testRequest())
	if v.Decision != Ask || !v.Failed {
		t.Fatalf("an unknown backend = %+v", v)
	}
}

func TestParseClassifierBackend(t *testing.T) {
	for in, want := range map[string]string{"": BackendCompletion, "completion": BackendCompletion, " DECISIONS ": BackendDecisions} {
		if got, err := ParseClassifierBackend(in); err != nil || got != want {
			t.Errorf("%q = %q, %v", in, got, err)
		}
	}
	if _, err := ParseClassifierBackend("oracle"); err == nil || !strings.Contains(err.Error(), "completion, decisions") {
		t.Errorf("an unknown backend should name the valid ones: %v", err)
	}
}
