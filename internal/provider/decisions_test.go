package provider

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// decisionsServer records the one request it is sent and answers with reply.
func decisionsServer(t *testing.T, reply string) (*OpenAI, *http.Request, *[]byte) {
	t.Helper()
	got := &http.Request{}
	body := new([]byte)
	srv := providerTestHTTP.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r
		*body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	o := &OpenAI{model: "gpt-6-luna", decisions: decisionsEndpoint{
		client: providerTestHTTP.Client(), baseURL: srv.URL + "/v1", apiKey: "sk-test",
	}}
	return o, got, body
}

// The request shape is the Decisions API guide's, read on 2026-10-09 at
// https://developers.openai.com/api/docs/guides/decisions (and the reference
// at https://developers.openai.com/api/reference/resources/decisions), where
// the API is a public beta: POST {base_url}/decisions with the model, the
// evidence as `input`, and each question typed and named with its
// proposition as `instructions`. If the beta's shape moves, this is the test
// that says so, and decisions.go is the one place it is spelled.
func TestDecider_RequestShape(t *testing.T) {
	o, got, body := decisionsServer(t, `{"answers":[{"type":"predicate","name":"may_run","probability":0.75}]}`)
	var _ Decider = o

	res, err := o.Decide(context.Background(), DecisionRequest{
		Model: "gpt-6-luna",
		Input: "UNTRUSTED EVIDENCE:\n{}",
		Questions: []DecisionQuestion{{
			Type: QuestionPredicate, Name: "may_run", Instructions: "The call may run.",
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Method != http.MethodPost || got.URL.Path != "/v1/decisions" {
		t.Fatalf("request = %s %s, want POST /v1/decisions", got.Method, got.URL.Path)
	}
	if auth := got.Header.Get("Authorization"); auth != "Bearer sk-test" {
		t.Fatalf("authorization = %q", auth)
	}
	const want = `{"model":"gpt-6-luna","input":"UNTRUSTED EVIDENCE:\n{}","questions":[{"type":"predicate","name":"may_run","instructions":"The call may run."}]}`
	if string(*body) != want {
		t.Fatalf("body =\n%s\nwant\n%s", *body, want)
	}

	a, ok := res.Answer("may_run")
	if !ok || a.Type != AnswerPredicate || a.Probability != 0.75 {
		t.Fatalf("answer = %+v (found %v)", a, ok)
	}
	// The guide documents no usage block, so a response without one is
	// billed at shhh's own count of what it sent, and says so.
	if !res.Counted || res.Usage.PromptTokens == 0 || res.Usage.CompletionTokens != 0 {
		t.Fatalf("usage = %+v counted=%v, want a counted input-only figure", res.Usage, res.Counted)
	}
}

// A usage block, where a response carries one, is the figure billed.
func TestDecider_ReportedUsageIsTheFigureBilled(t *testing.T) {
	o, _, _ := decisionsServer(t, `{"answers":[{"type":"predicate","name":"may_run","probability":1}],"usage":{"input_tokens":321}}`)
	res, err := o.Decide(context.Background(), DecisionRequest{Model: "gpt-6-luna", Input: "x",
		Questions: []DecisionQuestion{{Type: QuestionPredicate, Name: "may_run", Instructions: "p"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Counted || res.Usage.PromptTokens != 321 {
		t.Fatalf("usage = %+v counted=%v, want the reported 321", res.Usage, res.Counted)
	}
}

// A refusal is an answer of its own kind. It is never read as a probability
// — a zero would be a confident no — and a predicate that comes back with no
// probability at all is a failed request rather than an answer.
func TestDecider_RefusalIsNotAProbability(t *testing.T) {
	o, _, _ := decisionsServer(t, `{"answers":[{"type":"refusal","name":"may_run"}]}`)
	req := DecisionRequest{Model: "gpt-6-luna", Input: "x",
		Questions: []DecisionQuestion{{Type: QuestionPredicate, Name: "may_run", Instructions: "p"}}}
	res, err := o.Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a, ok := res.Answer("may_run")
	if !ok || a.Type != AnswerRefusal || a.Probability != 0 {
		t.Fatalf("answer = %+v, want a refusal with no probability", a)
	}

	for _, reply := range []string{
		`{"answers":[{"type":"predicate","name":"may_run"}]}`,
		`{"answers":[{"type":"predicate","name":"may_run","probability":1.5}]}`,
	} {
		o, _, _ := decisionsServer(t, reply)
		if _, err := o.Decide(context.Background(), req); err == nil {
			t.Errorf("%s: a predicate with no usable probability was accepted", reply)
		}
	}
}

// A refused request is named by the failure taxonomy, with its status, like
// any dialect's.
func TestDecider_ARefusedRequestIsAClassifiedFailure(t *testing.T) {
	srv := providerTestHTTP.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"no such route"}}`)
	}))
	defer srv.Close()
	_, err := PostDecisions(context.Background(), providerTestHTTP.Client(), srv.URL, "k", "gateway",
		DecisionRequest{Model: "m", Input: "x"})
	f, ok := AsFailure(err)
	if !ok || f.Status != http.StatusNotFound || f.Provider != "gateway" {
		t.Fatalf("err = %v (%+v)", err, f)
	}
}

// Whether a model offers the API is declared and never guessed: the native
// providers answer from their floor, which names gpt-6-luna and nothing
// else — not its siblings, not the families the capability floor describes.
func TestCapabilities_DecisionsAreDeclaredNotGuessed(t *testing.T) {
	for _, m := range []string{"gpt-6-luna", "gpt-6-luna-2026-10-01", "openai/gpt-6-luna"} {
		if !DecisionsOnFloor(m) {
			t.Errorf("%s: the floor should offer it", m)
		}
	}
	for _, m := range []string{"gpt-6-terra", "gpt-5.6-luna", "gpt-4o", "claude-opus-5", ""} {
		if DecisionsOnFloor(m) {
			t.Errorf("%s: offered by a floor that does not name it", m)
		}
	}
	if !DecisionsDeclared("openai", "gpt-6-luna") || !DecisionsDeclared("openai-responses", "gpt-6-luna") {
		t.Error("the native providers should declare the floor")
	}
	if DecisionsDeclared("openai-compatible", "gpt-6-luna") {
		t.Error("an endpoint nothing declares should offer nothing")
	}
	var compat Provider = &OpenAICompat{}
	if _, ok := compat.(Decider); ok {
		t.Error("a bare compatible endpoint should not claim the capability")
	}
	o := &OpenAI{}
	if !o.OffersDecisions("gpt-6-luna") || o.OffersDecisions("gpt-5.6-luna") {
		t.Error("the native provider answers from the floor")
	}
}
