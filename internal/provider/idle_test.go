package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/testhttp"
	openai "github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

// testIdle is the deadline the streams below run under, and testBeat the gap
// the live stream writes at. They are short because the thing under test is
// the timer and not the number, and every quiet test here spends the whole
// deadline — but the ratio between them is not arbitrary: the live test
// demands that six gaps in a row are each read as "still writing", so a
// scheduler hiccup has to eat five sixths of the deadline before it can turn
// a live stream into a spurious failure.
const (
	testIdle = 300 * time.Millisecond
	testBeat = 50 * time.Millisecond
)

// quietServer answers with the headers of a stream and then writes nothing at
// all — the endpoint that accepts a request and holds it, which is the whole
// reason there is a deadline. The handler returns when the client goes away,
// so the server closes rather than hanging the test binary.
func quietServer(t *testing.T) *testhttp.Server {
	t.Helper()
	srv := providerTestHTTP.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wentQuiet reads a stream to its end and demands the ending a stalled
// endpoint owes: a failure, classified as the network class the retry
// schedule waits out, and naming the deadline rather than the cancellation it
// was enforced with.
func wentQuiet(t *testing.T, ch <-chan StreamEvent, err error) {
	t.Helper()
	if err != nil {
		// Two of the dialects open the stream synchronously, so the deadline
		// can land before there is a channel to read.
		assertIdleFailure(t, err)
		return
	}
	var last StreamEvent
	for ev := range ch {
		last = ev
	}
	if last.Err == nil {
		t.Fatalf("a stream that never wrote should fail, got %+v", last)
	}
	assertIdleFailure(t, last.Err)
}

func assertIdleFailure(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrStreamIdle) {
		t.Errorf("the failure should name the deadline, got %v", err)
	}
	f, ok := AsFailure(err)
	if !ok {
		t.Fatalf("the failure should be classified, got %T", err)
	}
	if f.Class != ClassNetwork {
		t.Errorf("class = %q, want %q", f.Class, ClassNetwork)
	}
	if !f.Recoverable() {
		t.Error("a stream that went quiet is a stall the session can come back from")
	}
}

func TestIdle_AnthropicStreamThatGoesQuietFails(t *testing.T) {
	srv := quietServer(t)
	p := newTestAnthropic(ResolveOpts{APIKey: "sk-test", BaseURL: srv.URL})
	p.idleDeadline = idleDeadline{after: testIdle}

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, CompletionOpts{})
	wentQuiet(t, ch, err)
}

func TestIdle_OpenAIStreamThatGoesQuietFails(t *testing.T) {
	srv := quietServer(t)
	p := newTestOpenAI(srv.URL+"/v1", "gpt-4o")
	p.idleDeadline = idleDeadline{after: testIdle}

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, CompletionOpts{})
	wentQuiet(t, ch, err)
}

func TestIdle_OpenAICompatStreamThatGoesQuietFails(t *testing.T) {
	srv := quietServer(t)
	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = srv.URL + "/v1"
	cfg.HTTPClient = providerTestHTTP.Client()
	p := NewOpenAICompatWith(openai.NewClientWithConfig(cfg), "llama3", cfg.BaseURL)
	p.idleDeadline = idleDeadline{after: testIdle}

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, CompletionOpts{})
	wentQuiet(t, ch, err)
}

func TestIdle_OpenAIResponsesStreamThatGoesQuietFails(t *testing.T) {
	srv := quietServer(t)
	p := newTestResponses(srv.URL, "gpt-5")
	p.idleDeadline = idleDeadline{after: testIdle}

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, CompletionOpts{})
	wentQuiet(t, ch, err)
}

func TestIdle_GeminiStreamThatGoesQuietFails(t *testing.T) {
	srv := quietServer(t)
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      "test-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
		HTTPClient:  providerTestHTTP.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gemini{
		client:       client,
		model:        "gemini-2.5-flash",
		idleDeadline: idleDeadline{after: testIdle},
		classify:     newClassifier("gemini", "SHHH_API_KEY", "test-key"),
	}

	ch, err := g.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, CompletionOpts{})
	wentQuiet(t, ch, err)
}

// A model that thinks for far longer than the deadline is not a model that
// stopped: the thinking deltas are events, they push the deadline forward,
// and the turn ends the way it would have anyway. This is the failure the
// mechanism would cause if it watched for an answer rather than for silence.
func TestIdle_ASlowLiveThinkingStreamIsNotAFailure(t *testing.T) {
	const beats = 8
	srv := anthropicSSEServer(t, func(w http.ResponseWriter) {
		flusher, _ := w.(http.Flusher)
		sseEvent(w, "message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":1}}}`)
		sseEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
		flusher.Flush()
		for i := range beats {
			time.Sleep(testBeat)
			sseEvent(w, "content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"beat %d "}}`, i))
			flusher.Flush()
		}
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`)
		sseEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sseEvent(w, "content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)
		sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"done"}}`)
		sseEvent(w, "content_block_stop", `{"type":"content_block_stop","index":1}`)
		sseEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`)
		sseEvent(w, "message_stop", `{"type":"message_stop"}`)
		flusher.Flush()
	})
	defer srv.Close()

	p := newTestAnthropic(ResolveOpts{APIKey: "sk-test", BaseURL: srv.URL})
	p.idleDeadline = idleDeadline{after: testIdle}

	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "think"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var thinking, text string
	var last StreamEvent
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("a live stream failed: %v", ev.Err)
		}
		thinking += ev.Thinking
		text += ev.Token
		if ev.Done {
			last = ev
		}
	}
	if !last.Done {
		t.Fatal("the stream never reached its terminal event")
	}
	if text != "done" {
		t.Errorf("the reply = %q, want %q", text, "done")
	}
	if thinking == "" {
		t.Error("the thinking deltas should have arrived")
	}
}

// The setting as the file spells it: unset is the built-in deadline, a number
// is that many seconds, and a negative is a machine that would rather wait.
func TestIdle_TheSettingIsReadAsWritten(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"unset", 0, DefaultStreamIdle},
		{"seconds", 30, 30 * time.Second},
		{"off", -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := idleDeadlineOf(tc.seconds).idle(); got != tc.want {
				t.Errorf("%d seconds = %s, want %s", tc.seconds, got, tc.want)
			}
		})
	}
}

// A provider built from a finished client — every gateway profile's — starts
// with no deadline written down, and that has to read as the built-in one
// rather than as none.
func TestIdle_AProviderBuiltFromAClientStillHasTheDeadline(t *testing.T) {
	p := NewOpenAIResponsesWith(nil, "k", "http://unused", "gpt-5", "gw")
	if got := p.idle(); got != DefaultStreamIdle {
		t.Errorf("deadline = %s, want %s", got, DefaultStreamIdle)
	}
	p.SetStreamIdle(-1)
	if got := p.idle(); got != 0 {
		t.Errorf("after being turned off, deadline = %s, want none", got)
	}
}

// The deadline is switched off by a negative, and a stream under it is left
// alone. A stalled endpoint would hang the turn, so the case is checked
// against a live one: what is being demanded is that nothing cancels it.
func TestIdle_ADeadlineTurnedOffCancelsNothing(t *testing.T) {
	ctx, watch := idleDeadline{after: -1}.guard(context.Background())
	defer watch.stop()
	watch.alive()
	time.Sleep(2 * time.Millisecond)
	if err := ctx.Err(); err != nil {
		t.Errorf("a stream with no deadline should not be cancelled: %v", err)
	}
	if err := watch.err(nil); err != nil {
		t.Errorf("a stream with no deadline reports no idle failure, got %v", err)
	}
}
