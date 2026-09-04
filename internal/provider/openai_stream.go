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
		var stop StopReason

		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				ch <- terminalOpenAIEvent(toolArgs, usage, stop)
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
				// The arguments as they are written. This dialect puts the
				// id on the first chunk of a call and omits it from the
				// rest, so the fragment is addressed from the accumulator
				// rather than from the chunk. A chunk that somehow arrives
				// before any id does still accumulates into the call and is
				// simply not reported: a fragment addressed to nothing would
				// count toward a call nobody made.
				if acc.id != "" && tc.Function.Arguments != "" {
					ch <- StreamEvent{ToolCallDelta: &ToolCallDelta{ID: acc.id, Arguments: tc.Function.Arguments}}
				}
			}

			// The reason is empty on every chunk but the last of a choice.
			// A ceiling reached mid-call is reported here as "length" and
			// never as "tool_calls", which is why the stop is read before
			// the tool-call ending rather than derived from it.
			if choice.FinishReason != "" {
				stop = openAIStop(string(choice.FinishReason))
			}
			if stop == StopTool {
				ch <- terminalOpenAIEvent(toolArgs, usage, stop)
				return
			}
		}
	}()
	return ch
}

// terminalOpenAIEvent is the event that ends the stream, whichever of the two
// endings got here: the finish reason the model sent, or the body running out
// under it. A ceiling reached mid-call keeps only the calls that are whole —
// half a JSON object would reach a tool as malformed input and be answered as
// though the model had asked for something.
func terminalOpenAIEvent(toolArgs map[int]*toolCallAccumulator, usage *Usage, stop StopReason) StreamEvent {
	calls := buildToolCalls(toolArgs)
	if stop == StopLength {
		calls = CompletedToolCalls(calls)
	}
	if len(calls) == 0 {
		// buildToolCalls returns an empty non-nil slice for an empty map, and
		// a terminal event carrying one is a round with tool calls to every
		// reader that only checks the length.
		calls = nil
	}
	return StreamEvent{ToolCalls: calls, Usage: usage, Stop: stop, Done: true}
}

// openAIStop maps this dialect's finish reason onto shhh's closed set. The
// legacy `function_call` spelling is here because a gateway speaking an older
// revision of this API still sends it, and a round of tools read as a
// finished answer would close the turn owing results.
func openAIStop(reason string) StopReason {
	switch reason {
	case "stop", "":
		return StopEnd
	case "tool_calls", "function_call":
		return StopTool
	case "length":
		return StopLength
	case "content_filter":
		return StopRefusal
	default:
		return StopOther
	}
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
