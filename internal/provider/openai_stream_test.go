package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/rfizzle/shhh/internal/testhttp"
	openai "github.com/sashabaranov/go-openai"
)

// openAISSEServer writes the given chunks as this dialect's event stream, one
// `data:` line each, and closes with the sentinel the client reads as the end.
func openAISSEServer(t *testing.T, chunks []string) *testhttp.Server {
	t.Helper()
	srv := providerTestHTTP.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// A round's calls are addressed by id and not by their position in the
// stream. Two calls numbered 1 and 2 — a gateway that starts at one, or one
// that left a gap where a call was abandoned — used to come back as a single
// call, so the model received a result for one tool it asked for and the turn
// ran on owing an answer for the other.
func TestStreamOpenAI_ParallelCallsAtIndexesThatAreNotADenseRun(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"id":"call_b","type":"function","function":{"name":"list_dir","arguments":""}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"path\":\"a.go\"}"}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"function":{"arguments":"{\"path\":\".\"}"}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := openAISSEServer(t, chunks)
	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "read a.go and list ."}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	fragments, final := drainFragments(t, ch)

	wantFragments(t, fragments, []ToolCallDelta{
		{ID: "call_a", Arguments: `{"path":"a.go"}`},
		{ID: "call_b", Arguments: `{"path":"."}`},
	})
	if len(final.ToolCalls) != 2 {
		t.Fatalf("the round asked for two tools and delivered %d: %+v", len(final.ToolCalls), final.ToolCalls)
	}
	want := []ToolCall{
		{ID: "call_a", Name: "read_file", Arguments: `{"path":"a.go"}`},
		{ID: "call_b", Name: "list_dir", Arguments: `{"path":"."}`},
	}
	for i, call := range final.ToolCalls {
		if call != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, call, want[i])
		}
	}
}

// A gateway that names the call on every chunk and the index on none of them
// still assembles one call: the id is the address, and the index is only the
// fallback for the chunks that carry no id.
func TestStreamOpenAI_AContinuationWithoutAnIndexReachesItsCallByID(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_abc","function":{"arguments":"{\"path\":"}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_abc","function":{"arguments":"\"main.go\"}"}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := openAISSEServer(t, chunks)
	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "read main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, final := drainFragments(t, ch)

	if final.Err != nil {
		t.Fatalf("the round failed: %v", final.Err)
	}
	want := ToolCall{ID: "call_abc", Name: "read_file", Arguments: `{"path":"main.go"}`}
	if len(final.ToolCalls) != 1 || final.ToolCalls[0] != want {
		t.Fatalf("calls = %+v, want the one call %+v", final.ToolCalls, want)
	}
}

// A chunk carrying neither address ends the round. Read as call 0 — which is
// what an index-keyed accumulator did with it — its arguments land inside a
// call the model wrote separately, and a tool runs on input nobody asked for.
func TestStreamOpenAI_AChunkWithNoIDAndNoIndexIsAFailure(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"{\"path\":\"other.go\"}"}}]}}]}`,
	}
	srv := openAISSEServer(t, chunks)
	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "read main.go"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, final := drainFragments(t, ch)

	if final.Err == nil {
		t.Fatal("a fragment addressed to nothing was folded into a call instead of failing the round")
	}
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].Arguments != `{"path":"main.go"}` {
		t.Errorf("the failure should carry the call that was whole: %+v", final.ToolCalls)
	}
}

// A large tool argument arrives as a very long run of very small fragments —
// a rewritten file is hundreds of kilobytes in pieces of a few dozen bytes —
// and it is assembled on the goroutine reading the wire, so the cost of
// assembling it is time the stream is not being read. Growing a string per
// fragment made that quadratic.
func BenchmarkToolCallSetAccumulate(b *testing.B) {
	const (
		fragment  = "0123456789abcdefghijklmnopqrstuvwxyz0123" // 40 bytes
		fragments = 300_000 / len(fragment)
	)
	id := "call_abc"
	index := 0
	b.ReportAllocs()
	for b.Loop() {
		set := newToolCallSet()
		if _, err := set.accumulate(openai.ToolCall{
			ID: id, Index: &index, Function: openai.FunctionCall{Name: "write_file"},
		}); err != nil {
			b.Fatal(err)
		}
		for i := 0; i < fragments; i++ {
			if _, err := set.accumulate(openai.ToolCall{
				Index: &index, Function: openai.FunctionCall{Arguments: fragment},
			}); err != nil {
				b.Fatal(err)
			}
		}
		if n := len(set.calls()[0].Arguments); n != fragments*len(fragment) {
			b.Fatalf("assembled %d bytes", n)
		}
	}
}
