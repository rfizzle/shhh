package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rfizzle/shhh/internal/testhttp"
)

// fakeTokens is a TokenSource that answers from a list: the first token
// until it is refused, then the next, and so on until the list runs out —
// a source that has nothing newer answers the refused token again, which is
// how a real one says it could not refresh.
type fakeTokens struct {
	mu      sync.Mutex
	tokens  []string
	cur     int
	asked   []string
	err     error
	errOnce bool
}

func (f *fakeTokens) Token(_ context.Context, refused string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, refused)
	if f.err != nil {
		err := f.err
		if f.errOnce {
			f.err = nil
		}
		return "", err
	}
	if len(f.tokens) == 0 {
		return "", nil
	}
	if refused != "" && refused == f.tokens[f.cur] && f.cur+1 < len(f.tokens) {
		f.cur++
	}
	return f.tokens[f.cur], nil
}

func (f *fakeTokens) refusals() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.asked {
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

// panicTokens is a source nothing may ask.
type panicTokens struct{ t *testing.T }

func (p panicTokens) Token(context.Context, string) (string, error) {
	p.t.Error("the key route asked the plan's token source")
	return "", errors.New("never")
}

// planEndpoint stands in for api.openai.com itself: the plan route's address
// is fixed, so the fixture answers under that host on a registry of its own.
func planEndpoint(t *testing.T, h http.HandlerFunc) *http.Client {
	t.Helper()
	var reg testhttp.Registry
	srv := reg.NewServer(h)
	srv.Alias("api.openai.com")
	t.Cleanup(srv.Close)
	return reg.Client()
}

func newTestChatGPT(t *testing.T, src TokenSource, h http.HandlerFunc) *OpenAIResponses {
	t.Helper()
	p, err := Resolve("chatgpt", ResolveOpts{
		TokenSource: src,
		HTTPClient:  planEndpoint(t, h),
		Model:       "gpt-5.6-terra",
		// Neither is read: the plan's token goes to OpenAI and nowhere else.
		BaseURL:       "http://gateway.invalid/v1",
		ConfigBaseURL: "http://config.invalid/v1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return p.(*OpenAIResponses)
}

const completedEvent = `data: {"type":"response.completed","response":{"status":"completed","output":[]}}`

func streamOK(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, ev := range append(events, completedEvent) {
		fmt.Fprintf(w, "%s\n\n", ev)
	}
}

func hi() []Message { return []Message{{Role: RoleUser, Content: "hi"}} }

func TestChatGPT_BearerComesFromTheSource(t *testing.T) {
	t.Setenv("SHHH_BASE_URL", "http://env.invalid/v1")
	src := &fakeTokens{tokens: []string{"plan-token-1"}}
	var auth, host, path string
	p := newTestChatGPT(t, src, func(w http.ResponseWriter, r *http.Request) {
		auth, host, path = r.Header.Get("Authorization"), r.Host, r.URL.Path
		streamOK(w)
	})
	ch, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, _, err := collect(t, ch); err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if auth != "Bearer plan-token-1" {
		t.Errorf("the bearer should be the source's token, got %q", auth)
	}
	if host != "api.openai.com" || path != "/v1/responses" {
		t.Errorf("the plan route goes to OpenAI's endpoint whatever is configured, got %s%s", host, path)
	}
	if p.Name() != "chatgpt" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestChatGPT_NeverReadsTheKeyEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env-openai")
	t.Setenv("SHHH_API_KEY", "sk-env-shhh")
	var auth string
	var reg testhttp.Registry
	srv := reg.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		streamOK(w)
	}))
	srv.Alias("api.openai.com")
	t.Cleanup(srv.Close)
	p, err := Resolve("chatgpt", ResolveOpts{
		TokenSource:  &fakeTokens{tokens: []string{"plan-token"}},
		HTTPClient:   reg.Client(),
		APIKey:       "sk-flag",
		ConfigAPIKey: "sk-config",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ch, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _, _, _ = collect(t, ch)
	if auth != "Bearer plan-token" {
		t.Fatalf("no key from the flag, the environment or the file may reach the plan route, got %q", auth)
	}
}

func TestOpenAIResponses_NeverReadsTheSource(t *testing.T) {
	var auth string
	srv := providerTestHTTP.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		streamOK(w)
	}))
	t.Cleanup(srv.Close)
	p, err := Resolve("openai-responses", ResolveOpts{
		APIKey:      "sk-key",
		BaseURL:     srv.URL + "/v1",
		TokenSource: panicTokens{t},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	p.(*OpenAIResponses).client = providerTestHTTP.Client()
	ch, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _, _, _ = collect(t, ch)
	if auth != "Bearer sk-key" {
		t.Fatalf("the key route sends its key, got %q", auth)
	}
}

func TestChatGPT_SignedOutFailsToResolve(t *testing.T) {
	signInRequired := errors.New("the ChatGPT sign-in has expired; run `shhh providers login chatgpt`")
	for _, c := range []struct {
		name string
		src  TokenSource
		want error
	}{
		{"no source at all", nil, ErrChatGPTSignedOut},
		{"a source with nothing in it", &fakeTokens{}, ErrChatGPTSignedOut},
		{"a source that cannot refresh", &fakeTokens{err: signInRequired}, signInRequired},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Resolve("chatgpt", ResolveOpts{TokenSource: c.src, APIKey: "sk-would-be-a-fallback"})
			if !errors.Is(err, c.want) {
				t.Fatalf("resolution should fail with the sign-in error, got %v", err)
			}
		})
	}
}

func TestChatGPT_401RefreshesOnceThenFails(t *testing.T) {
	t.Run("a refreshed token is sent once more", func(t *testing.T) {
		src := &fakeTokens{tokens: []string{"old-token", "new-token"}}
		var seen []string
		var mu sync.Mutex
		p := newTestChatGPT(t, src, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.Header.Get("Authorization"))
			mu.Unlock()
			if r.Header.Get("Authorization") == "Bearer old-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"token expired","code":"invalid_token"}}`))
				return
			}
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte(`"input"`)) {
				t.Errorf("the second request should carry the same body, got %s", body)
			}
			streamOK(w, `data: {"type":"response.output_text.delta","delta":"ok"}`)
		})
		ch, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
		if err != nil {
			t.Fatalf("a refused token that refreshed should go through, got %v", err)
		}
		text, _, _, err := collect(t, ch)
		if err != nil || text != "ok" {
			t.Fatalf("got %q, %v", text, err)
		}
		if len(seen) != 2 || seen[1] != "Bearer new-token" {
			t.Fatalf("one retry under the new token, got %v", seen)
		}
		if got := src.refusals(); len(got) != 1 || got[0] != "old-token" {
			t.Fatalf("the source is told which token was refused, once: %v", got)
		}
	})

	t.Run("a source that cannot refresh leaves the 401", func(t *testing.T) {
		src := &fakeTokens{tokens: []string{"only-token"}}
		var requests atomic.Int32
		p := newTestChatGPT(t, src, func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad token"}}`))
		})
		_, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
		f, ok := AsFailure(err)
		if !ok || f.Class != ClassAuth {
			t.Fatalf("a refused token is an auth failure, got %v", err)
		}
		if n := requests.Load(); n != 1 {
			t.Fatalf("the same token is not sent twice, got %d requests", n)
		}
		if f.KeyEnv != "shhh providers login chatgpt" || f.KeyTail != "" {
			t.Fatalf("the auth row offers the sign-in and names no key: env %q tail %q", f.KeyEnv, f.KeyTail)
		}
	})

	t.Run("a second 401 is not answered again", func(t *testing.T) {
		src := &fakeTokens{tokens: []string{"a", "b", "c"}}
		var requests atomic.Int32
		p := newTestChatGPT(t, src, func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		})
		_, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("got %v", err)
		}
		if n := requests.Load(); n != 2 {
			t.Fatalf("one refresh and no more, got %d requests", n)
		}
	})
}

type netFailure struct{}

func (netFailure) Error() string   { return "dial tcp: lookup auth.openai.com: no such host" }
func (netFailure) Timeout() bool   { return false }
func (netFailure) Temporary() bool { return false }

var _ net.Error = netFailure{}

func TestChatGPT_ASourceThatCannotAnswerIsClassified(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want Class
	}{
		{"cancelled", context.Canceled, ClassCancelled},
		{"the sign-in server unreachable", netFailure{}, ClassNetwork},
		{"the sign-in expired", errors.New("the ChatGPT sign-in has expired; run `shhh providers login chatgpt`"), ClassAuth},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := &fakeTokens{tokens: []string{"tok"}}
			var requests atomic.Int32
			p := newTestChatGPT(t, src, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				streamOK(w)
			})
			src.mu.Lock()
			src.err = c.err
			src.mu.Unlock()
			_, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
			f, ok := AsFailure(err)
			if !ok || f.Class != c.want {
				t.Fatalf("want %q, got %v", c.want, err)
			}
			if requests.Load() != 0 {
				t.Fatal("nothing is sent without a token")
			}
			if c.want == ClassAuth && (f.KeyEnv != chatGPTKeyEnv || !strings.Contains(f.Message, "sign-in has expired")) {
				t.Fatalf("the source's own words and the sign-in offer should survive: %+v", f)
			}
		})
	}
}

// The body a turn with tools, an image and a reasoning level goes out as on
// the plan route, whole. What it pins: the fields the docs reject are
// absent, the ones the docs leave open are not sent until a call against the
// route has shown each is accepted, and the tools travel in one namespace
// with their own names and descriptions.
func TestPlanShape_PinsTheBody(t *testing.T) {
	image := Attachment{Kind: AttachmentImage, Name: "shot.png", Handle: "Image#1", MediaType: "image/png", Data: []byte("not-a-png")}
	messages := []Message{
		{Role: RoleSystem, Content: "You are shhh."},
		{Role: RoleUser, Content: "what does this show", Attachments: []Attachment{image}},
		{
			Role:      RoleAssistant,
			Reasoning: []ReasoningBlock{{Signature: "rs_1", Redacted: "sealed"}},
			ToolCalls: []ToolCall{{ID: "call_1", Name: "bash", Arguments: `{"command":"ls"}`}},
		},
		{Role: RoleTool, ToolCallID: "call_1", Content: "go.mod"},
	}
	tools := []Tool{
		{Name: "bash", Description: "Run a command.", Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "read_file", Description: "Read a file.", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	for _, c := range []struct {
		name string
		opts CompletionOpts
		want string
	}{
		{
			name: "a turn with tools, an image and a reasoning level",
			opts: CompletionOpts{
				Tools: tools, ToolChoice: ToolChoiceAuto, Effort: EffortHigh,
				Temperature: ptrFloat(0.2), MaxTokens: 4096,
			},
			want: `{
				"model": "gpt-5.6-terra",
				"instructions": "You are shhh.",
				"stream": true,
				"store": false,
				"input": [
					{"type":"message","role":"user","content":[
						{"type":"input_text","text":` + jsonString(image.Label()) + `},
						{"type":"input_image","image_url":` + jsonString(image.DataURL()) + `},
						{"type":"input_text","text":"what does this show"}
					]},
					{"type":"function_call","call_id":"call_1","name":"bash","namespace":"shhh","arguments":"{\"command\":\"ls\"}"},
					{"type":"function_call_output","call_id":"call_1","output":"go.mod"}
				],
				"tools": [
					{"type":"namespace","name":"shhh","description":"The tools this session can call.","tools":[
						{"type":"function","name":"bash","description":"Run a command.","parameters":{"type":"object"},"strict":false},
						{"type":"function","name":"read_file","description":"Read a file.","parameters":{"type":"object"},"strict":false}
					]}
				]
			}`,
		},
		{
			name: "a turn on which no tool may be called",
			opts: CompletionOpts{Tools: tools, ToolChoice: ToolChoiceNone},
			want: `{
				"model": "gpt-5.6-terra",
				"instructions": "You are shhh.",
				"stream": true,
				"store": false,
				"input": [
					{"type":"message","role":"user","content":[
						{"type":"input_text","text":` + jsonString(image.Label()) + `},
						{"type":"input_image","image_url":` + jsonString(image.DataURL()) + `},
						{"type":"input_text","text":"what does this show"}
					]},
					{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}"},
					{"type":"function_call_output","call_id":"call_1","output":"go.mod"}
				]
			}`,
		},
		{
			name: "a bounded call that asked for a schema",
			opts: CompletionOpts{
				MaxTokens:      512,
				ResponseSchema: &ResponseSchema{Name: "verdict", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)},
			},
			want: `{
				"model": "gpt-5.6-terra",
				"instructions": "You are shhh.",
				"stream": true,
				"store": false,
				"input": [
					{"type":"message","role":"user","content":[
						{"type":"input_text","text":` + jsonString(image.Label()) + `},
						{"type":"input_image","image_url":` + jsonString(image.DataURL()) + `},
						{"type":"input_text","text":"what does this show"}
					]},
					{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}"},
					{"type":"function_call_output","call_id":"call_1","output":"go.mod"}
				]
			}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var raw []byte
			p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
				raw, _ = io.ReadAll(r.Body)
				streamOK(w)
			})
			ch, err := p.StreamCompletion(context.Background(), messages, c.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			_, _, _, _ = collect(t, ch)
			if got, want := canonicalJSON(t, raw), canonicalJSON(t, []byte(c.want)); got != want {
				t.Fatalf("the plan body drifted:\n got %s\nwant %s", got, want)
			}
			for _, field := range []string{
				"temperature", "max_output_tokens", "prompt_cache_retention", "truncation", "metadata",
				"user", "safety_identifier", "top_p", "top_logprobs", "max_tool_calls", "background",
				"conversation", "moderation", "multi_agent", "prompt", "previous_response_id",
			} {
				var top map[string]json.RawMessage
				_ = json.Unmarshal(raw, &top)
				if _, ok := top[field]; ok {
					t.Errorf("%s is rejected on the plan route and must never be sent", field)
				}
			}
		})
	}
}

// The key route is not shaped: the same turn there keeps every field it
// sent before the plan route existed.
func TestPlanShape_LeavesTheKeyRouteAlone(t *testing.T) {
	var got responsesRequest
	events := []string{completedEvent}
	p := newTestResponses(responsesServer(t, events, &got).URL, "gpt-5.6-terra")
	ch, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{
		Tools:      []Tool{{Name: "bash", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: ToolChoiceAuto, Effort: EffortHigh, MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _, _, _ = collect(t, ch)
	if got.MaxOutput != 100 || got.Reasoning == nil || got.ToolChoice != "auto" || got.PromptCacheKey == "" {
		t.Fatalf("the key route's request should be untouched: %+v", got)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" {
		t.Fatalf("the key route's tools stay flat: %+v", got.Tools)
	}
}

func TestPlanTools_RoundTripNames(t *testing.T) {
	p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{
			`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","namespace":"shhh","arguments":"{}"}}`,
			`data: {"type":"response.completed","response":{"status":"completed","output":[` +
				`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","namespace":"shhh","arguments":"{}"},` +
				`{"type":"function_call","id":"fc_2","call_id":"call_2","name":"shhh.bash","arguments":"{}"},` +
				`{"type":"function_call","id":"fc_3","call_id":"call_3","name":"shhh.unknown","arguments":"{}"}]}}`,
		} {
			fmt.Fprintf(w, "%s\n\n", ev)
		}
	})
	tools := []Tool{{Name: "bash"}, {Name: "read_file"}}
	ch, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{Tools: tools})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, calls, _, err := collect(t, ch)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	var names []string
	for _, c := range calls {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "read_file,bash,shhh.unknown" {
		t.Fatalf("calls should come back under shhh's own names, and an unknown one as it came: %s", got)
	}
}

func TestPlanModels_KeepsListedInOrder(t *testing.T) {
	var auth, path string
	p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[
			{"slug":"gpt-6.1-sol","display_name":"GPT-6.1 Sol","visibility":"list"},
			{"slug":"internal-x","display_name":"Hidden","visibility":"hide"},
			{"slug":"gpt-5.6-terra","display_name":"GPT-5.6 Terra","visibility":"list"},
			{"slug":"a-model","display_name":"","visibility":"list"}
		]}`))
	})
	slugs, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(slugs, ","); got != "gpt-6.1-sol,gpt-5.6-terra,a-model" {
		t.Fatalf("listed entries in the server's order, got %s", got)
	}
	if auth != "Bearer tok" || path != "/v1/models" {
		t.Fatalf("the catalog is asked with the plan's token at the plan's endpoint: %q %q", auth, path)
	}
	labels := p.ModelLabels()
	if labels["gpt-6.1-sol"] != "GPT-6.1 Sol" || labels["a-model"] != "a-model" || len(labels) != 3 {
		t.Fatalf("display names are kept by slug: %v", labels)
	}
	if got := ChatGPTDefaultModel(slugs); got != "gpt-5.6-terra" {
		t.Errorf("a plan that serves the usual default keeps it, got %q", got)
	}
	if got := ChatGPTDefaultModel([]string{"gpt-6.1-sol", "x"}); got != "gpt-6.1-sol" {
		t.Errorf("otherwise the first listed slug, got %q", got)
	}
}

func TestPlanModels_RefusesAnotherShape(t *testing.T) {
	p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.6-terra"}]}`))
	})
	if _, err := p.ListModels(context.Background()); !errors.Is(err, ErrMalformed) {
		t.Fatalf("the key route's catalog shape is not the plan's, got %v", err)
	}
}

func TestPlanAttachments_RefusesAudioBeforeSending(t *testing.T) {
	var requests atomic.Int32
	var raw []byte
	p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		raw, _ = io.ReadAll(r.Body)
		streamOK(w)
	})
	memo := Attachment{Kind: AttachmentAudio, Name: "memo.mp3", MediaType: "audio/mpeg", Data: []byte("mp3")}
	_, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "listen", Attachments: []Attachment{memo}},
	}, CompletionOpts{})
	f, ok := AsFailure(err)
	if !ok || f.Class != ClassUnsupported || f.Param != "input_audio" || !strings.Contains(f.Message, "memo.mp3") {
		t.Fatalf("a recording is refused with the reason, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("nothing is sent for a request that would be refused")
	}

	// One further back came from another provider: it goes as a line, so
	// the session is not refused on every turn for something it cannot drop.
	ch, err := p.StreamCompletion(context.Background(), []Message{
		{Role: RoleUser, Content: "listen", Attachments: []Attachment{memo}},
		{Role: RoleAssistant, Content: "heard"},
		{Role: RoleUser, Content: "and now?"},
	}, CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _, _, _ = collect(t, ch)
	if bytes.Contains(raw, []byte("input_audio")) || !bytes.Contains(raw, []byte("this recording was not sent")) {
		t.Fatalf("an earlier recording goes as a note, got %s", raw)
	}
}

// The plan's error family, over the wire, each row classified before the
// status table would have answered it.
func TestPlanFailure_ClassifiesTheFamily(t *testing.T) {
	for _, c := range []struct {
		name      string
		status    int
		body      string
		class     Class
		code      string
		param     string
		message   string
		recovered bool
	}{
		{"a spent allowance", 429, `{"error":{"message":"You have hit your usage limit.","code":"subscription_sharing_usage_limit_exceeded"}}`, ClassUsageLimit, "subscription_sharing_usage_limit_exceeded", "", "You have hit your usage limit.", false},
		{"usage unavailable", 503, `{"error":{"message":"try later","code":"subscription_sharing_usage_unavailable"}}`, ClassOverloaded, "subscription_sharing_usage_unavailable", "", "try later", true},
		{"user unavailable", 503, `{"error":{"message":"try later","code":"subscription_sharing_user_unavailable"}}`, ClassOverloaded, "subscription_sharing_user_unavailable", "", "try later", true},
		{"admission unavailable", 503, `{"detail":"Direct routing is not enabled"}`, ClassOverloaded, "", "", "Direct routing is not enabled", true},
		{"not eligible", 403, `{"error":{"message":"not available","code":"subscription_sharing_user_not_eligible"}}`, ClassNotEligible, "subscription_sharing_user_not_eligible", "", "not available", false},
		{"route not supported", 403, `{"error":{"message":"no","code":"subscription_sharing_route_not_supported"}}`, ClassNotEligible, "subscription_sharing_route_not_supported", "", "no", false},
		{"scope not authorized", 403, `{"error":{"message":"no","code":"chatpass_v2_scope_not_authorized"}}`, ClassNotEligible, "chatpass_v2_scope_not_authorized", "", "no", false},
		{"bad authorization context", 403, `{"error":{"message":"no","code":"chatpass_v2_invalid_authorization_context"}}`, ClassNotEligible, "chatpass_v2_invalid_authorization_context", "", "no", false},
		{"unsupported capability", 400, `{"error":{"message":"Unsupported parameter","code":"subscription_sharing_unsupported_capability","param":"tools[0].type"}}`, ClassUnsupported, "subscription_sharing_unsupported_capability", "tools[0].type", "Unsupported parameter", false},
		{"invalid user", 401, `{"error":{"message":"bad subscriber","code":"subscription_sharing_invalid_user"}}`, ClassAuth, "subscription_sharing_invalid_user", "", "bad subscriber", false},
		{"a bare 401", 401, `{"detail":"The signed identity was not accepted"}`, ClassAuth, "", "", "The signed identity was not accepted", false},
		{"a bare 403", 403, `{"detail":"region"}`, ClassAuth, "", "", "region", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("x-request-id", "req_123")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			})
			_, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
			f, ok := AsFailure(err)
			if !ok {
				t.Fatalf("expected a classified failure, got %v", err)
			}
			if f.Class != c.class || f.Code != c.code || f.Param != c.param || f.Message != c.message {
				t.Fatalf("got class %q code %q param %q message %q", f.Class, f.Code, f.Param, f.Message)
			}
			if f.Status != c.status || f.RequestID != "req_123" || f.Provider != "chatgpt" {
				t.Fatalf("the status, request id and provider are kept: %+v", f)
			}
			if f.Recoverable() != c.recovered {
				t.Fatalf("Recoverable() = %v", f.Recoverable())
			}
			if c.param != "" && !strings.Contains(strings.Join(f.Detail(), "\n"), "param: "+c.param) {
				t.Fatalf("the refused part is in the detail: %v", f.Detail())
			}
		})
	}
}

func TestPlanFailure_MidStreamClassifiesTheSame(t *testing.T) {
	for _, c := range []struct {
		event string
		class Class
	}{
		{`data: {"type":"response.failed","response":{"status":"failed","error":{"message":"usage limit reached","code":"subscription_sharing_usage_limit_exceeded"}}}`, ClassUsageLimit},
		{`data: {"type":"response.failed","response":{"status":"failed","error":{"message":"usage check failed","code":"subscription_sharing_usage_unavailable"}}}`, ClassOverloaded},
		{`data: {"type":"error","code":"subscription_sharing_usage_limit_exceeded","message":"usage limit reached"}`, ClassUsageLimit},
	} {
		p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"par\"}\n\n%s\n\n", c.event)
		})
		ch, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
		if err != nil {
			t.Fatalf("the stream opened, got %v", err)
		}
		_, _, _, err = collect(t, ch)
		f, ok := AsFailure(err)
		if !ok || f.Class != c.class || f.Code == "" {
			t.Fatalf("%s: want %q with its code, got %v", c.event, c.class, err)
		}
	}
}

func TestChatGPT_OffersNoDecisions(t *testing.T) {
	var requests atomic.Int32
	p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	})
	if p.OffersDecisions("gpt-6-luna") {
		t.Error("the plan route offers the Decisions API for no model")
	}
	if DecisionsDeclared("chatgpt", "gpt-6-luna") {
		t.Error("the chatgpt name declares no model for the Decisions API")
	}
	if _, err := p.Decide(context.Background(), DecisionRequest{Model: "gpt-6-luna"}); !errors.Is(err, ErrNotEligible) {
		t.Errorf("a caller that skipped the question is refused, got %v", err)
	}
	if requests.Load() != 0 {
		t.Error("nothing is sent to a route the plan does not cover")
	}
	if d := Defaults("chatgpt"); d.BaseURL != "https://api.openai.com/v1" || d.Model == "" || d.CheapModel == "" {
		t.Errorf("unexpected defaults: %+v", d)
	}
}

// No error this route builds carries the bearer, whichever way it failed.
func TestChatGPT_ErrorsCarryNoToken(t *testing.T) {
	const token = "plan-secret-token-value"
	for _, h := range []http.HandlerFunc{
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) },
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit"}}`))
		},
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
	} {
		p := newTestChatGPT(t, &fakeTokens{tokens: []string{token}}, h)
		_, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
		if err == nil {
			t.Fatal("expected a failure")
		}
		f, _ := AsFailure(err)
		for _, s := range append([]string{err.Error(), f.KeyEnv, f.KeyTail}, f.Detail()...) {
			if strings.Contains(s, token) || strings.Contains(s, token[len(token)-8:]) {
				t.Fatalf("the bearer leaked into a failure: %q", s)
			}
		}
	}
}

// A redirect is never followed on the plan route: the bearer would be copied
// to it, and a redirect to plain http would carry the token in clear.
func TestChatGPT_NeverFollowsARedirect(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	p := newTestChatGPT(t, &fakeTokens{tokens: []string{"tok"}}, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String()+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		http.Redirect(w, r, "http://api.openai.com/elsewhere", http.StatusTemporaryRedirect)
	})
	_, err := p.StreamCompletion(context.Background(), hi(), CompletionOpts{})
	if err == nil {
		t.Fatal("a redirect is a failed request, not a followed one")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "https://api.openai.com/v1/responses ") {
		t.Fatalf("the bearer should reach only the first URL, got %v", seen)
	}
}

func ptrFloat(v float64) *float64 { return &v }

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func canonicalJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	out, _ := json.Marshal(v)
	return string(out)
}
