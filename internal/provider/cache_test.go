package provider

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	openai "github.com/sashabaranov/go-openai"
)

// paramsWithTurns builds a request whose conversation has n ordinary turns,
// which is all these tests need to see where the markers land.
func paramsWithTurns(system string, n int) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	for i := 0; i < n; i++ {
		params.Messages = append(params.Messages,
			anthropic.NewUserMessage(anthropic.NewTextBlock("ask")),
			anthropic.NewAssistantMessage(anthropic.NewTextBlock("answer")),
		)
	}
	return params
}

// markedMessages is the indexes of params.Messages carrying a marker.
func markedMessages(params anthropic.MessageNewParams) []int {
	var out []int
	for i, msg := range params.Messages {
		for _, block := range msg.Content {
			if cc := block.GetCacheControl(); cc != nil && cc.Type != "" {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

func countMarks(t *testing.T, params anthropic.MessageNewParams) int {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), `"cache_control"`)
}

func TestMarkAnthropicCacheMarksTheFixedHead(t *testing.T) {
	params := paramsWithTurns("be helpful", 1)
	markAnthropicCache(&params, DefaultCacheTTL)

	if params.System[0].CacheControl.Type == "" {
		t.Fatal("the system prompt must carry a marker: it and the tools are the prefix that never changes")
	}
}

func TestMarkAnthropicCacheRollsOverTheLastTurns(t *testing.T) {
	params := paramsWithTurns("be helpful", 4)
	markAnthropicCache(&params, DefaultCacheTTL)

	last := len(params.Messages) - 1
	want := []int{last - 1, last}
	got := markedMessages(params)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("rolling markers must sit on the last two messages, want %v got %v", want, got)
	}
}

// The round the second rolling marker exists for: a batch of parallel calls
// appends more blocks than the provider searches back through, so the
// previous round's boundary has to be named rather than found.
func TestMarkAnthropicCacheNamesThePreviousRoundAcrossAParallelBatch(t *testing.T) {
	params := paramsWithTurns("be helpful", 1)
	var calls, results []anthropic.ContentBlockParamUnion
	// The loop's own ceiling on parallel calls, spelled out rather than
	// imported: the agent package is downstream of this one.
	const parallel = 8
	for i := 0; i < parallel; i++ {
		calls = append(calls, anthropic.ContentBlockParamUnion{
			OfToolUse: &anthropic.ToolUseBlockParam{ID: "t", Name: "search", Input: map[string]any{}},
		})
		results = append(results, anthropic.NewToolResultBlock("t", "hit", false))
	}
	params.Messages = append(params.Messages,
		anthropic.NewAssistantMessage(calls...),
		anthropic.NewUserMessage(results...),
	)
	markAnthropicCache(&params, DefaultCacheTTL)

	got := markedMessages(params)
	last := len(params.Messages) - 1
	if len(got) != 2 || got[1] != last || got[0] != last-1 {
		t.Fatalf("the batch's own boundary and the one before it must both be marked, got %v", got)
	}
}

func TestMarkAnthropicCacheStaysUnderTheAPILimit(t *testing.T) {
	params := paramsWithTurns("be helpful", 40)
	markAnthropicCache(&params, DefaultCacheTTL)

	if n := countMarks(t, params); n > maxCacheMarks {
		t.Fatalf("a request may carry at most %d markers, got %d", maxCacheMarks, n)
	}
}

func TestMarkAnthropicCacheSkipsABlockThatCannotCarryOne(t *testing.T) {
	params := paramsWithTurns("be helpful", 1)
	// A turn whose reasoning is its last block: thinking takes no marker, so
	// the text in front of it is what must be marked.
	params.Messages = append(params.Messages, anthropic.NewAssistantMessage(
		anthropic.NewTextBlock("thinking out loud"),
		anthropic.NewThinkingBlock("sig", "why"),
	))
	markAnthropicCache(&params, DefaultCacheTTL)

	blocks := params.Messages[len(params.Messages)-1].Content
	if blocks[0].OfText.CacheControl.Type == "" {
		t.Fatal("a marker must fall back to the last block that can carry one")
	}
	if countMarks(t, params) == 0 {
		t.Fatal("the request lost its markers entirely")
	}
}

func TestMarkAnthropicCacheOnAnEmptyRequest(t *testing.T) {
	params := anthropic.MessageNewParams{}
	markAnthropicCache(&params, DefaultCacheTTL)

	if n := countMarks(t, params); n != 0 {
		t.Fatalf("nothing to mark, got %d markers", n)
	}
}

// The markers must be the only difference a request carries: they are a
// statement about billing, and a dialect that ignores them has to see the
// same conversation it would have seen.
func TestMarkAnthropicCacheChangesNothingButTheMarkers(t *testing.T) {
	before := paramsWithTurns("be helpful", 3)
	after := paramsWithTurns("be helpful", 3)
	markAnthropicCache(&after, DefaultCacheTTL)

	strip := func(params anthropic.MessageNewParams) string {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		out := regexp.MustCompile(`,?"cache_control":\{[^}]*\},?`).ReplaceAllStringFunc(string(raw), func(m string) string {
			if strings.HasPrefix(m, ",") && strings.HasSuffix(m, ",") {
				return ","
			}
			return ""
		})
		return out
	}
	if strip(before) != strip(after) {
		t.Fatalf("marking rewrote the request:\n before %s\n  after %s", strip(before), strip(after))
	}
}

// The head is the block worth the longer lifetime and the rolling markers
// are not: they are replaced every round, so the dearer write would buy a
// prefix that is superseded within the minute.
func TestMarkAnthropicCacheGivesTheHeadTheLongerLifetime(t *testing.T) {
	params := paramsWithTurns("be helpful", 2)
	markAnthropicCache(&params, DefaultCacheTTL)

	if got := params.System[0].CacheControl.TTL; got != anthropic.CacheControlEphemeralTTL(CacheTTL1h) {
		t.Errorf("the head's lifetime is %q, want an hour by default", got)
	}
	for _, i := range markedMessages(params) {
		for _, block := range params.Messages[i].Content {
			cc := block.GetCacheControl()
			if cc == nil || cc.Type == "" {
				continue
			}
			if cc.TTL != anthropic.CacheControlEphemeralTTL(CacheTTL5m) {
				t.Errorf("message %d's marker is %q, want the five minutes a rolling marker keeps", i, cc.TTL)
			}
		}
	}
}

func TestMarkAnthropicCacheFollowsTheConfiguredHeadLifetime(t *testing.T) {
	params := paramsWithTurns("be helpful", 2)
	markAnthropicCache(&params, CacheTTL5m)

	if got := params.System[0].CacheControl.TTL; got != anthropic.CacheControlEphemeralTTL(CacheTTL5m) {
		t.Errorf("the head's lifetime is %q, want the one that was chosen", got)
	}
}

func TestParseCacheTTL(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want CacheTTL
		bad  bool
	}{
		{in: "", want: DefaultCacheTTL},
		{in: "5m", want: CacheTTL5m},
		{in: " 1H ", want: CacheTTL1h},
		{in: "10m", want: DefaultCacheTTL, bad: true},
		{in: "forever", want: DefaultCacheTTL, bad: true},
	} {
		got, err := ParseCacheTTL(tc.in)
		if (err != nil) != tc.bad {
			t.Errorf("ParseCacheTTL(%q) error = %v, want bad = %v", tc.in, err, tc.bad)
		}
		if got != tc.want {
			t.Errorf("ParseCacheTTL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// openAIBody is a chat-completions request in the shape the OpenAI-speaking
// providers build, which is what the gateway's breakpoints have to be added
// to without disturbing.
func openAIBody(t *testing.T, model string, msgs ...Message) []byte {
	t.Helper()
	raw, err := json.Marshal(openai.ChatCompletionRequest{
		Model:    model,
		Messages: toOpenAIMessages(msgs),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// bodyMarks is the lifetime of every marker in a chat-completions body, keyed
// by the index of the message carrying it.
func bodyMarks(t *testing.T, body []byte) map[int]string {
	t.Helper()
	var decoded struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the marked body no longer decodes: %v\n%s", err, body)
	}
	out := map[int]string{}
	for i, msg := range decoded.Messages {
		// An unmarked message keeps its plain string content, which is the
		// point: the parts array appears only where a marker went.
		var parts []struct {
			CacheControl *struct {
				Type string `json:"type"`
				TTL  string `json:"ttl"`
			} `json:"cache_control"`
		}
		if json.Unmarshal(msg.Content, &parts) != nil {
			continue
		}
		for _, part := range parts {
			if part.CacheControl == nil {
				continue
			}
			if part.CacheControl.Type != "ephemeral" {
				t.Errorf("message %d carries a marker of type %q", i, part.CacheControl.Type)
			}
			out[i] = part.CacheControl.TTL
		}
	}
	return out
}

func TestMarkOpenAICacheBodyMarksTheSamePositionsAsTheNativePath(t *testing.T) {
	body := markOpenAICacheBody(openAIBody(t, "anthropic/claude-sonnet-4-6",
		Message{Role: RoleSystem, Content: "be helpful"},
		Message{Role: RoleUser, Content: "one"},
		Message{Role: RoleAssistant, Content: "two"},
		Message{Role: RoleUser, Content: "three"},
		Message{Role: RoleAssistant, Content: "four"},
	), DefaultCacheTTL)

	got := bodyMarks(t, body)
	want := map[int]string{0: string(CacheTTL1h), 3: string(CacheTTL5m), 4: string(CacheTTL5m)}
	if len(got) != len(want) {
		t.Fatalf("markers = %v, want %v", got, want)
	}
	for i, ttl := range want {
		if got[i] != ttl {
			t.Errorf("message %d's marker = %q, want %q", i, got[i], ttl)
		}
	}
}

// The head sits inside the message list here and beside it on the native
// path, so a conversation with no system message must still put its rolling
// markers on the last two turns rather than one short.
func TestMarkOpenAICacheBodyWithNoSystemMessage(t *testing.T) {
	body := markOpenAICacheBody(openAIBody(t, "anthropic/claude-opus-5",
		Message{Role: RoleUser, Content: "one"},
		Message{Role: RoleAssistant, Content: "two"},
		Message{Role: RoleUser, Content: "three"},
	), DefaultCacheTTL)

	got := bodyMarks(t, body)
	if len(got) != 2 || got[1] == "" || got[2] == "" {
		t.Fatalf("markers = %v, want the last two messages", got)
	}
}

// A breakpoint on a dialect that has never heard of one is what makes this
// safe to send to a gateway; a request the rule does not claim is safe
// because it is not touched at all.
func TestMarkOpenAICacheBodyLeavesOtherVendorsAlone(t *testing.T) {
	for _, model := range []string{"openai/gpt-5.2", "google/gemini-3.1-pro-preview", "claude-opus-5"} {
		before := openAIBody(t, model,
			Message{Role: RoleSystem, Content: "be helpful"},
			Message{Role: RoleUser, Content: "one"},
			Message{Role: RoleAssistant, Content: "two"},
		)
		if after := markOpenAICacheBody(before, DefaultCacheTTL); string(after) != string(before) {
			t.Errorf("%s: the request was rewritten\n before %s\n  after %s", model, before, after)
		}
	}
}

// The round every agentic turn ends on: the last message is a tool call with
// no content to hang a marker on. The request has to stay well formed and
// keep the markers it could place.
func TestMarkOpenAICacheBodyWhenTheLastMessageCannotHoldAMarker(t *testing.T) {
	before := openAIBody(t, "anthropic/claude-sonnet-4-6",
		Message{Role: RoleSystem, Content: "be helpful"},
		Message{Role: RoleUser, Content: "one"},
		Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "t", Name: "search", Arguments: "{}"}}},
	)
	body := markOpenAICacheBody(before, DefaultCacheTTL)

	got := bodyMarks(t, body)
	if len(got) != 2 || got[0] != string(CacheTTL1h) || got[1] != string(CacheTTL5m) {
		t.Fatalf("markers = %v, want the head and the one message that can carry one", got)
	}

	var req openai.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("the marked body is not a request any more: %v", err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("the marked body has %d messages, want 3", len(req.Messages))
	}
	if calls := req.Messages[2].ToolCalls; len(calls) != 1 || calls[0].Function.Name != "search" {
		t.Errorf("the tool call did not survive the marking: %+v", req.Messages[2])
	}
}

// The markers must be the only difference the gateway's request carries, for
// the same reason they are on the native path: they say what a round costs
// and nothing about what it means.
func TestMarkOpenAICacheBodyChangesNothingButTheMarkers(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "be helpful"},
		{Role: RoleUser, Content: "one"},
		{Role: RoleAssistant, Content: "two"},
	}
	before := openAIBody(t, "anthropic/claude-sonnet-4-6", msgs...)
	after := markOpenAICacheBody(before, DefaultCacheTTL)

	var was, is openai.ChatCompletionRequest
	if err := json.Unmarshal(before, &was); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &is); err != nil {
		t.Fatal(err)
	}
	if was.Model != is.Model || len(was.Messages) != len(is.Messages) {
		t.Fatalf("the request changed shape: %+v against %+v", was, is)
	}
	for i, msg := range was.Messages {
		text := is.Messages[i].Content
		if parts := is.Messages[i].MultiContent; len(parts) > 0 {
			text = parts[len(parts)-1].Text
		}
		if msg.Role != is.Messages[i].Role || msg.Content != text {
			t.Errorf("message %d says something else now: %q/%q against %q/%q",
				i, msg.Role, msg.Content, is.Messages[i].Role, text)
		}
	}
}
