package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// A gateway's ping is an event on the stream: it carries no text and nothing
// else, and the answer around it arrives whole. One case per dialect that
// sends them, each over the package's fake transport. Gemini's SDK hands back
// whole responses and no dialect of its own sends a ping, so it has no case.
func TestStream_AKeepaliveIsAnEvent(t *testing.T) {
	chunk := func(delta, finish string) string {
		return fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":%s%s}]}`, delta, finish)
	}
	openAIChunks := []string{
		`{"id":"c1","object":"chat.completion.chunk","choices":[]}`,
		chunk(`{"content":"hi"}`, ""),
		chunk(`{}`, `,"finish_reason":"stop"`),
	}
	anthropic := func(w http.ResponseWriter) {
		sseEvent(w, "message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"claude-opus-5","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`)
		fmt.Fprint(w, ": gateway keepalive\n\n")
		sseEvent(w, "ping", `{"type":"ping"}`)
		sseEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
		sseEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sseEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`)
		sseEvent(w, "message_stop", `{"type":"message_stop"}`)
	}
	responses := []string{
		`data: {"type":"response.created","response":{}}`,
		`: keepalive`,
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
	}

	cases := []struct {
		name  string
		want  int
		start func(t *testing.T) (<-chan StreamEvent, error)
	}{
		{"anthropic", 2, func(t *testing.T) (<-chan StreamEvent, error) {
			srv := anthropicSSEServer(t, anthropic)
			t.Cleanup(srv.Close)
			p := newTestAnthropic(ResolveOpts{APIKey: "sk-test", BaseURL: srv.URL})
			return p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
		}},
		{"openai", 1, func(t *testing.T) (<-chan StreamEvent, error) {
			p := newTestOpenAI(openAISSEServer(t, openAIChunks).URL+"/v1", "gpt-4o")
			return p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
		}},
		{"openai-compatible", 1, func(t *testing.T) (<-chan StreamEvent, error) {
			p := newTestCompat(openAISSEServer(t, openAIChunks).URL+"/v1", "llama3")
			return p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
		}},
		{"openrouter", 1, func(t *testing.T) (<-chan StreamEvent, error) {
			p := newTestOpenRouter(openAISSEServer(t, openAIChunks).URL+"/v1", "m")
			return p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
		}},
		{"openai-responses", 1, func(t *testing.T) (<-chan StreamEvent, error) {
			p := newTestResponses(responsesServer(t, responses, nil).URL, "gpt-5")
			return p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := tc.start(t)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			keepalives := 0
			var last StreamEvent
			for ev := range ch {
				if ev.Keepalive {
					keepalives++
					if ev.Token != "" || ev.Thinking != "" || ev.ToolCallDelta != nil || len(ev.ToolCalls) > 0 || ev.Done || ev.Err != nil {
						t.Errorf("a keepalive carries nothing else, got %+v", ev)
					}
					continue
				}
				text += ev.Token
				last = ev
			}
			if keepalives != tc.want {
				t.Errorf("keepalive events = %d, want %d", keepalives, tc.want)
			}
			if text != "hi" || !last.Done || last.Err != nil {
				t.Errorf("the answer around a ping should arrive whole: text %q, last %+v", text, last)
			}
		})
	}
}

// The sniffer reads a ping that is split across reads, and never mistakes a
// line that only starts like one.
func TestStream_ThePingSnifferReadsAcrossReads(t *testing.T) {
	var got int
	var s pingSniffer
	s.onPing = func() { got++ }
	for _, piece := range []string{"event: pi", "ng\r\ndata: {}\n\nevent: pings\n", ": hi", "\n\n", "data: :\n"} {
		s.ReadCloser = readCloser(piece)
		if _, err := s.Read(make([]byte, 64)); err != nil {
			t.Fatal(err)
		}
	}
	if got != 2 {
		t.Errorf("pings = %d, want 2 (the event line and the comment)", got)
	}
}

type readCloser string

func (r readCloser) Read(b []byte) (int, error) { return copy(b, r), nil }
func (r readCloser) Close() error               { return nil }
