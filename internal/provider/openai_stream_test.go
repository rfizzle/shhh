package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// openAISSEServer writes the given chunks as this dialect's event stream, one
// `data:` line each, and closes with the sentinel the client reads as the end.
func openAISSEServer(t *testing.T, chunks []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// toolRoundChunks is a round that called a tool, in the order the API
// documents for stream_options.include_usage: the call in fragments, then a
// chunk carrying only the finish reason, then a chunk carrying only the usage
// for the whole round, with no choices in it at all.
var toolRoundChunks = []string{
	`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
	`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
	`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
	`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}}]}}]}`,
	`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":999,"completion_tokens":42,"total_tokens":1041,"prompt_tokens_details":{"cached_tokens":128}}}`,
}

func openAIDialects() []struct {
	name  string
	build func(baseURL string) Provider
} {
	return []struct {
		name  string
		build func(baseURL string) Provider
	}{
		{"openai", func(u string) Provider { return newTestOpenAI(u+"/v1", "gpt-4o") }},
		{"openrouter", func(u string) Provider { return newTestOpenRouter(u+"/v1", "test-model") }},
		{"openai-compatible", func(u string) Provider { return newTestCompat(u+"/v1", "llama3") }},
	}
}

// A round that called a tool is the round most of a coding session is made
// of, and its tokens arrive after the reason that ended it. Reading the
// stream only as far as the reason billed none of them: the ledger, the
// context rail and the calibration all went by the text rounds alone.
func TestStreamOpenAI_AToolRoundReportsTheUsageThatFollowsIt(t *testing.T) {
	for _, dialect := range openAIDialects() {
		t.Run(dialect.name, func(t *testing.T) {
			srv := openAISSEServer(t, toolRoundChunks)
			p := dialect.build(srv.URL)

			ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "read main.go"}}, CompletionOpts{})
			if err != nil {
				t.Fatal(err)
			}
			fragments, final := drainFragments(t, ch)

			wantFragments(t, fragments, []ToolCallDelta{
				{ID: "call_abc", Arguments: `{"path":`},
				{ID: "call_abc", Arguments: `"main.go"}`},
			})
			if final.Stop != StopTool {
				t.Errorf("stop = %q, want %q", final.Stop, StopTool)
			}
			if len(final.ToolCalls) != 1 || final.ToolCalls[0].Arguments != `{"path":"main.go"}` {
				t.Errorf("the terminal event lost the finished call: %+v", final.ToolCalls)
			}
			if final.Usage == nil {
				t.Fatal("a tool round reported no usage, so nothing downstream can bill it")
			}
			want := Usage{PromptTokens: 999, CompletionTokens: 42, CachedTokens: 128}
			if *final.Usage != want {
				t.Errorf("usage = %+v, want %+v", *final.Usage, want)
			}
		})
	}
}

// A gateway that sends no usage at all must still end the round, and end it
// once the body runs out rather than waiting on tokens that are not coming.
func TestStreamOpenAI_AToolRoundStillEndsWithoutAUsageChunk(t *testing.T) {
	srv := openAISSEServer(t, toolRoundChunks[:len(toolRoundChunks)-1])
	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "read main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, final := drainFragments(t, ch)

	if !final.Done || final.Err != nil {
		t.Fatalf("the round did not end cleanly: %+v", final)
	}
	if final.Stop != StopTool {
		t.Errorf("stop = %q, want %q", final.Stop, StopTool)
	}
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].Name != "read_file" {
		t.Errorf("the terminal event lost the finished call: %+v", final.ToolCalls)
	}
	if final.Usage != nil {
		t.Errorf("usage came from nowhere: %+v", *final.Usage)
	}
}

// Some gateways fold the usage into the chunk that names the finish reason
// instead of sending one of its own. That round has both in hand at the same
// moment and ends when the body does, still carrying its tokens.
func TestStreamOpenAI_AToolRoundReportsUsageFoldedIntoTheFinishChunk(t *testing.T) {
	chunks := append([]string{}, toolRoundChunks[:len(toolRoundChunks)-1]...)
	chunks[len(chunks)-1] = `{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":999,"completion_tokens":42,"total_tokens":1041}}`
	srv := openAISSEServer(t, chunks)
	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "read main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, final := drainFragments(t, ch)

	if final.Stop != StopTool {
		t.Errorf("stop = %q, want %q", final.Stop, StopTool)
	}
	if final.Usage == nil || final.Usage.PromptTokens != 999 {
		t.Fatalf("usage = %+v, want 999 prompt tokens", final.Usage)
	}
}
