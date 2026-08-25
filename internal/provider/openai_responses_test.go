package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// responsesServer stands in for the /v1/responses endpoint, capturing the
// request and replaying a scripted event stream.
func responsesServer(t *testing.T, events []string, capture *responsesRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("expected the responses endpoint, got %q", r.URL.Path)
		}
		if capture != nil {
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, capture); err != nil {
				t.Errorf("decode request: %v", err)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, ev := range events {
			fmt.Fprintf(w, "%s\n\n", ev)
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestResponses(baseURL, model string) *OpenAIResponses {
	return NewOpenAIResponsesWith(nil, "test-key", baseURL+"/v1", model, "")
}

func collect(t *testing.T, ch <-chan StreamEvent) (string, []ToolCall, *Usage, error) {
	t.Helper()
	var text string
	var calls []ToolCall
	var usage *Usage
	for ev := range ch {
		if ev.Err != nil {
			return text, calls, usage, ev.Err
		}
		text += ev.Token
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}
	return text, calls, usage, nil
}

func TestOpenAIResponses_Name(t *testing.T) {
	if got := newTestResponses("http://unused", "").Name(); got != "openai-responses" {
		t.Errorf("expected 'openai-responses', got %q", got)
	}
	if got := NewOpenAIResponsesWith(nil, "k", "http://unused", "m", "gateway").Name(); got != "gateway" {
		t.Errorf("a profile should be able to name it, got %q", got)
	}
}

func TestOpenAIResponses_StreamsText(t *testing.T) {
	srv := responsesServer(t, []string{
		`data: {"type":"response.created"}`,
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		`data: {"type":"response.output_text.delta","delta":", world"}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[],` +
			`"usage":{"input_tokens":11,"output_tokens":3,"input_tokens_details":{"cached_tokens":7}}}}`,
	}, nil)

	p := newTestResponses(srv.URL, "gpt-5.6-terra")
	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, calls, usage, err := collect(t, ch)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if text != "Hello, world" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no tool calls, got %v", calls)
	}
	if usage == nil || usage.PromptTokens != 11 || usage.CompletionTokens != 3 || usage.CachedTokens != 7 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestOpenAIResponses_ReadsToolCallsFromTheFinishedResponse(t *testing.T) {
	srv := responsesServer(t, []string{
		`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"bash"}}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\""}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[` +
			`{"type":"reasoning","summary":[]},` +
			`{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"},` +
			`{"type":"function_call","call_id":"call_2","name":"read","arguments":"{\"path\":\"go.mod\"}"}],` +
			`"usage":{"input_tokens":40,"output_tokens":9}}}`,
	}, nil)

	p := newTestResponses(srv.URL, "gpt-5.6-terra")
	ch, _ := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "list"}}, CompletionOpts{})
	_, calls, usage, err := collect(t, ch)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected both calls in order, got %v", calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "bash" || calls[0].Arguments != `{"cmd":"ls"}` {
		t.Fatalf("unexpected first call: %+v", calls[0])
	}
	if calls[1].ID != "call_2" {
		t.Fatalf("unexpected second call: %+v", calls[1])
	}
	if usage == nil || usage.PromptTokens != 40 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

// A gateway that relays item events but drops the finished output list should
// still produce usable tool calls.
func TestOpenAIResponses_FallsBackToItemEvents(t *testing.T) {
	srv := responsesServer(t, []string{
		`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"}}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":5,"output_tokens":1}}}`,
	}, nil)

	p := newTestResponses(srv.URL, "gpt-5.6-luna")
	ch, _ := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "list"}}, CompletionOpts{})
	_, calls, _, err := collect(t, ch)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Arguments != `{"cmd":"ls"}` {
		t.Fatalf("unexpected calls: %v", calls)
	}
}

func TestOpenAIResponses_EndsCleanlyWithoutATerminalEvent(t *testing.T) {
	srv := responsesServer(t, []string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
	}, nil)

	p := newTestResponses(srv.URL, "gpt-4.1")
	ch, _ := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
	text, _, _, err := collect(t, ch)
	if err != nil {
		t.Fatalf("a truncated stream should not error: %v", err)
	}
	if text != "partial" {
		t.Fatalf("the delivered text should survive, got %q", text)
	}
}

func TestOpenAIResponses_SurfacesAFailureEvent(t *testing.T) {
	srv := responsesServer(t, []string{
		`data: {"type":"response.failed","response":{"status":"failed","error":{"message":"context length exceeded","code":"context_length_exceeded"}}}`,
	}, nil)

	p := newTestResponses(srv.URL, "gpt-4.1")
	ch, _ := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
	_, _, _, err := collect(t, ch)
	if err == nil || !strings.Contains(err.Error(), "context length exceeded") {
		t.Fatalf("the API's message should reach the user, got %v", err)
	}
}

func TestOpenAIResponses_ClassifiesHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	p := newTestResponses(srv.URL, "gpt-4.1")
	_, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "invalid API key") {
		t.Fatalf("a 401 should read as an auth failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Fatalf("the API's own message should survive, got %v", err)
	}
}

func TestOpenAIResponses_BuildsTheInputList(t *testing.T) {
	var got responsesRequest
	srv := responsesServer(t, []string{
		`data: {"type":"response.completed","response":{"status":"completed","output":[]}}`,
	}, &got)

	p := newTestResponses(srv.URL, "gpt-5.6-terra")
	temp := 0.3
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleSystem, Content: "be terse"},
		{Role: RoleUser, Content: "list files"},
		{Role: RoleAssistant, Content: "on it", ToolCalls: []ToolCall{{ID: "call_1", Name: "bash", Arguments: `{"cmd":"ls"}`}}},
		{Role: RoleTool, ToolCallID: "call_1", Content: "go.mod"},
	}, CompletionOpts{
		Tools:       []Tool{{Name: "bash", Description: "run a command", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice:  "auto",
		Temperature: &temp,
		MaxTokens:   2048,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	if got.Instructions != "be terse" {
		t.Fatalf("the system prompt belongs in instructions, got %q", got.Instructions)
	}
	if got.Store {
		t.Fatal("shhh sends the whole conversation; the endpoint should not store it")
	}
	if !got.Stream || got.MaxOutput != 2048 || got.Temperature == nil || *got.Temperature != 0.3 {
		t.Fatalf("unexpected request options: %+v", got)
	}
	if len(got.Input) != 4 {
		t.Fatalf("expected four input items, got %d: %+v", len(got.Input), got.Input)
	}
	if got.Input[0].Type != "message" || got.Input[0].Role != "user" || got.Input[0].Content[0].Type != "input_text" {
		t.Fatalf("unexpected user item: %+v", got.Input[0])
	}
	if got.Input[1].Content[0].Type != "output_text" {
		t.Fatalf("assistant text uses output_text, got %+v", got.Input[1])
	}
	if got.Input[2].Type != "function_call" || got.Input[2].CallID != "call_1" || got.Input[2].Arguments != `{"cmd":"ls"}` {
		t.Fatalf("the call should be its own item: %+v", got.Input[2])
	}
	if got.Input[3].Type != "function_call_output" || got.Input[3].CallID != "call_1" || got.Input[3].Output != "go.mod" {
		t.Fatalf("the result should be its own item: %+v", got.Input[3])
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Name != "bash" {
		t.Fatalf("tools are flattened in this dialect: %+v", got.Tools)
	}
	if got.Tools[0].Strict {
		t.Fatal("strict mode would reject shhh's permissive schemas")
	}
}

func TestOpenAIResponses_OmitsUnsetOptions(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	p := newTestResponses(srv.URL, "gpt-5.6-terra")
	ch, _ := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
	for range ch {
	}

	// A reasoning model rejects a temperature it never asked for; anything
	// the session did not set must not appear on the wire.
	for _, key := range []string{"temperature", "max_output_tokens", "tools", "tool_choice", "instructions"} {
		if _, present := got[key]; present {
			t.Fatalf("%q should be omitted when unset, got %v", key, got[key])
		}
	}
}

func TestOpenAIResponses_ListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Errorf("expected the models endpoint, got %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.6-terra"},{"id":"text-embedding-3-small"}]}`))
	}))
	defer srv.Close()

	names, err := newTestResponses(srv.URL, "gpt-5.6-terra").ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 1 || names[0] != "gpt-5.6-terra" {
		t.Fatalf("unexpected catalog: %v", names)
	}
}

func TestNewOpenAIResponses_RequiresAKey(t *testing.T) {
	t.Setenv("SHHH_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := NewOpenAIResponses(ResolveOpts{}); err == nil {
		t.Fatal("expected a missing-key error")
	}
	p, err := NewOpenAIResponses(ResolveOpts{APIKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != defaultResponsesModel || p.baseURL != defaultResponsesBaseURL {
		t.Fatalf("unexpected defaults: %+v", p)
	}
}

func TestOpenAIResponses_IsRegistered(t *testing.T) {
	p, err := Resolve("openai-responses", ResolveOpts{APIKey: "k", Model: "gpt-5.6-terra"})
	if err != nil {
		t.Fatalf("the dialect should be resolvable: %v", err)
	}
	if p.Name() != "openai-responses" {
		t.Fatalf("unexpected name: %q", p.Name())
	}
	if _, ok := p.(ModelLister); !ok {
		t.Fatal("the dialect should support model discovery")
	}
	if d := Defaults("openai-responses"); d.BaseURL != defaultResponsesBaseURL {
		t.Fatalf("unexpected defaults: %+v", d)
	}
}
