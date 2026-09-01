package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
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
	markAnthropicCache(&params)

	if params.System[0].CacheControl.Type == "" {
		t.Fatal("the system prompt must carry a marker: it and the tools are the prefix that never changes")
	}
}

func TestMarkAnthropicCacheRollsOverTheLastTurns(t *testing.T) {
	params := paramsWithTurns("be helpful", 4)
	markAnthropicCache(&params)

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
	markAnthropicCache(&params)

	got := markedMessages(params)
	last := len(params.Messages) - 1
	if len(got) != 2 || got[1] != last || got[0] != last-1 {
		t.Fatalf("the batch's own boundary and the one before it must both be marked, got %v", got)
	}
}

func TestMarkAnthropicCacheStaysUnderTheAPILimit(t *testing.T) {
	params := paramsWithTurns("be helpful", 40)
	markAnthropicCache(&params)

	if n := countMarks(t, params); n > maxAnthropicCacheMarks {
		t.Fatalf("a request may carry at most %d markers, got %d", maxAnthropicCacheMarks, n)
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
	markAnthropicCache(&params)

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
	markAnthropicCache(&params)

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
	markAnthropicCache(&after)

	strip := func(params anthropic.MessageNewParams) string {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		out := strings.ReplaceAll(string(raw), `,"cache_control":{"type":"ephemeral"}`, "")
		return strings.ReplaceAll(out, `"cache_control":{"type":"ephemeral"},`, "")
	}
	if strip(before) != strip(after) {
		t.Fatalf("marking rewrote the request:\n before %s\n  after %s", strip(before), strip(after))
	}
}
