package provider

import (
	"errors"
	"io"

	openai "github.com/sashabaranov/go-openai"
)

type toolCallAccumulator struct {
	id   string
	name string
	args string
}

func streamOpenAIToolCalls(stream *openai.ChatCompletionStream, classify func(error) error) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		defer stream.Close()
		toolArgs := map[int]*toolCallAccumulator{}
		var usage *Usage

		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				if len(toolArgs) > 0 {
					ch <- StreamEvent{ToolCalls: buildToolCalls(toolArgs), Usage: usage, Done: true}
				} else {
					ch <- StreamEvent{Usage: usage, Done: true}
				}
				return
			}
			if err != nil {
				// The calls the model had finished writing travel with the
				// failure, so the session can offer to continue from them
				// rather than only from the top.
				ch <- StreamEvent{ToolCalls: CompletedToolCalls(buildToolCalls(toolArgs)), Err: classify(err), Done: true}
				return
			}

			if resp.Usage != nil {
				usage = &Usage{
					PromptTokens:     resp.Usage.PromptTokens,
					CompletionTokens: resp.Usage.CompletionTokens,
				}
				if d := resp.Usage.PromptTokensDetails; d != nil {
					usage.CachedTokens = d.CachedTokens
				}
			}

			if len(resp.Choices) == 0 {
				continue
			}

			choice := resp.Choices[0]
			delta := choice.Delta

			if delta.Content != "" {
				ch <- StreamEvent{Token: delta.Content}
			}

			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				acc, exists := toolArgs[idx]
				if !exists {
					acc = &toolCallAccumulator{}
					toolArgs[idx] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args += tc.Function.Arguments
			}

			if choice.FinishReason == "tool_calls" {
				ch <- StreamEvent{ToolCalls: buildToolCalls(toolArgs), Usage: usage, Done: true}
				return
			}
		}
	}()
	return ch
}

// buildToolCalls assembles the accumulated deltas in index order. It is
// called on a half-filled map too, when a stream breaks mid-accumulation
// , so a gap in the indices is skipped rather than dereferenced.
func buildToolCalls(accumulators map[int]*toolCallAccumulator) []ToolCall {
	calls := make([]ToolCall, 0, len(accumulators))
	for i := 0; i < len(accumulators); i++ {
		acc, ok := accumulators[i]
		if !ok {
			continue
		}
		calls = append(calls, ToolCall{
			ID:        acc.id,
			Name:      acc.name,
			Arguments: acc.args,
		})
	}
	return calls
}
