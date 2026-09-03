package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

// drainFragments reads a stream to its end, keeping the argument fragments in
// the order they arrived and the event that closed it. Every parser is read
// through here, because the promise being checked is the same in all four:
// the fragments come before the terminal event and the terminal event still
// carries the finished calls.
func drainFragments(t *testing.T, ch <-chan StreamEvent) (fragments []ToolCallDelta, final StreamEvent) {
	t.Helper()
	for ev := range ch {
		switch {
		case ev.ToolCallDelta != nil:
			if ev.Done || ev.Err != nil || len(ev.ToolCalls) > 0 {
				t.Errorf("a fragment event carried more than a fragment: %+v", ev)
			}
			if final.Done || final.Err != nil {
				t.Errorf("a fragment arrived after the terminal event: %+v", *ev.ToolCallDelta)
			}
			fragments = append(fragments, *ev.ToolCallDelta)
		case ev.Done || ev.Err != nil:
			final = ev
		}
	}
	return fragments, final
}

func wantFragments(t *testing.T, got []ToolCallDelta, want []ToolCallDelta) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d fragments, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fragment %d: expected %+v, got %+v", i, want[i], got[i])
		}
	}
}

func TestStream_AnthropicReportsArgumentFragmentsInOrder(t *testing.T) {
	srv := anthropicSSEServer(t, func(w http.ResponseWriter) {
		sseEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5","stop_reason":null,"usage":{"input_tokens":20,"output_tokens":1}}}`)
		sseEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"write_file","input":{}}}`)
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`)
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"main.go\"}"}}`)
		sseEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sseEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":15}}`)
		sseEvent(w, "message_stop", `{"type":"message_stop"}`)
	})
	defer srv.Close()

	p, err := NewAnthropic(ResolveOpts{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "write main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	fragments, final := drainFragments(t, ch)
	wantFragments(t, fragments, []ToolCallDelta{
		{ID: "toolu_1", Arguments: `{"path":`},
		{ID: "toolu_1", Arguments: `"main.go"}`},
	})
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].Name != "write_file" || final.ToolCalls[0].Arguments != `{"path":"main.go"}` {
		t.Errorf("the terminal event lost the finished call: %+v", final.ToolCalls)
	}
}

func TestStream_OpenAIReportsArgumentFragmentsInOrder(t *testing.T) {
	idx0 := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []openai.ChatCompletionStreamResponse{
			{Choices: []openai.ChatCompletionStreamChoice{{Delta: openai.ChatCompletionStreamChoiceDelta{ToolCalls: []openai.ToolCall{
				{Index: &idx0, ID: "call_abc", Function: openai.FunctionCall{Name: "write_file", Arguments: `{"pa`}},
			}}}}},
			{Choices: []openai.ChatCompletionStreamChoice{{Delta: openai.ChatCompletionStreamChoiceDelta{ToolCalls: []openai.ToolCall{
				{Index: &idx0, Function: openai.FunctionCall{Arguments: `th":"main.go"}`}},
			}}}}},
			{Choices: []openai.ChatCompletionStreamChoice{{FinishReason: "tool_calls"}}},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "write main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	fragments, final := drainFragments(t, ch)
	wantFragments(t, fragments, []ToolCallDelta{
		{ID: "call_abc", Arguments: `{"pa`},
		{ID: "call_abc", Arguments: `th":"main.go"}`},
	})
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].Arguments != `{"path":"main.go"}` {
		t.Errorf("the terminal event lost the finished call: %+v", final.ToolCalls)
	}
}

func TestStream_OpenAIResponsesReportsArgumentFragmentsInOrder(t *testing.T) {
	srv := responsesServer(t, []string{
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"write_file"}}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"pa"}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"th\":\"main.go\"}"}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"write_file","arguments":"{\"path\":\"main.go\"}"}]}}`,
	}, nil)

	p := newTestResponses(srv.URL, "gpt-5")
	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "write main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	fragments, final := drainFragments(t, ch)
	wantFragments(t, fragments, []ToolCallDelta{
		{ID: "call_abc", Arguments: `{"pa`},
		{ID: "call_abc", Arguments: `th":"main.go"}`},
	})
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].ID != "call_abc" {
		t.Errorf("the terminal event lost the finished call: %+v", final.ToolCalls)
	}
}

// A fragment addressed to an item nobody opened is dropped rather than
// reported under a guessed id: a gateway that replays the argument deltas
// without the item event that names the call has nothing to address them to.
func TestStream_OpenAIResponsesDropsFragmentsItCannotAddress(t *testing.T) {
	srv := responsesServer(t, []string{
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":\"main.go\"}"}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"write_file","arguments":"{\"path\":\"main.go\"}"}]}}`,
	}, nil)

	p := newTestResponses(srv.URL, "gpt-5")
	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "write main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	fragments, final := drainFragments(t, ch)
	if len(fragments) != 0 {
		t.Errorf("expected no fragments, got %+v", fragments)
	}
	if len(final.ToolCalls) != 1 {
		t.Errorf("the terminal event lost the finished call: %+v", final.ToolCalls)
	}
}

// Gemini never sends a call in pieces, so its fragment is the arguments
// entire — reported all the same, and before the terminal event, so a reader
// following fragments does not have to know which dialect it is following.
func TestStream_GeminiReportsTheWholeCallAsOneFragment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, `data: {"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_abc","name":"write_file","args":{"path":"main.go"}}}]}}]}`+"\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      "test-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gemini{client: client, model: "gemini-2.5-flash", classify: newClassifier("gemini", "SHHH_API_KEY", "test-key")}

	ch, err := g.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "write main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	fragments, final := drainFragments(t, ch)
	wantFragments(t, fragments, []ToolCallDelta{{ID: "call_abc", Arguments: `{"path":"main.go"}`}})
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].Name != "write_file" {
		t.Errorf("the terminal event lost the finished call: %+v", final.ToolCalls)
	}
}

// The boundary a broken stream is read at is unchanged by the fragments: what
// travels with the failure is the calls that are whole, and a call that was
// still being written is a fragment, not a call — however many fragments of
// it were reported on the way.
func TestStream_ABrokenStreamKeepsOnlyTheCallsThatAreWhole(t *testing.T) {
	idx0, idx1 := 0, 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []openai.ChatCompletionStreamResponse{
			{Choices: []openai.ChatCompletionStreamChoice{{Delta: openai.ChatCompletionStreamChoiceDelta{ToolCalls: []openai.ToolCall{
				{Index: &idx0, ID: "call_done", Function: openai.FunctionCall{Name: "read_file", Arguments: `{"path":"main.go"}`}},
			}}}}},
			{Choices: []openai.ChatCompletionStreamChoice{{Delta: openai.ChatCompletionStreamChoiceDelta{ToolCalls: []openai.ToolCall{
				{Index: &idx1, ID: "call_half", Function: openai.FunctionCall{Name: "write_file", Arguments: `{"path":"ma`}},
			}}}}},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		// The connection dies here, which is the failure the recovery is
		// for: a clean end would be a finished turn.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.(*net.TCPConn).SetLinger(0)
		_ = conn.Close()
	}))
	defer srv.Close()

	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "write main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	fragments, final := drainFragments(t, ch)
	wantFragments(t, fragments, []ToolCallDelta{
		{ID: "call_done", Arguments: `{"path":"main.go"}`},
		{ID: "call_half", Arguments: `{"path":"ma`},
	})
	if final.Err == nil {
		t.Fatalf("expected the stream to fail, got %+v", final)
	}
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].ID != "call_done" {
		t.Errorf("expected only the finished call to survive, got %+v", final.ToolCalls)
	}
}
