package provider

import (
	"errors"
	"io"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// toolCallAccumulator is one call as the stream writes it. The arguments are
// a builder rather than a string because a large one arrives in very small
// pieces — a 300 KB file body comes in fragments of a few dozen bytes, and
// re-allocating the whole of it per fragment costs quadratic time on the
// goroutine that is reading the wire.
type toolCallAccumulator struct {
	id   string
	name string
	args strings.Builder
}

// toolCallSet holds a round's tool calls in the order the stream opened them,
// addressed by the id the rest of the session answers them by.
//
// The `index` field is not that address. A gateway is free to number a round's
// calls from 1, to leave a gap where a call was abandoned, or to omit the
// index from the continuation chunks altogether, and a set keyed by index
// answered all three by folding two calls into one: the model asks for two
// tools, gets a result for one, and the turn runs on owing an answer nobody
// will send. The Responses reader keys its calls by id for the same reason.
// The index survives here only as the address for chunks that carry no id,
// which in this dialect is every fragment after the one that opens a call.
type toolCallSet struct {
	order   []*toolCallAccumulator
	byID    map[string]*toolCallAccumulator
	byIndex map[int]*toolCallAccumulator
}

func newToolCallSet() *toolCallSet {
	return &toolCallSet{
		byID:    map[string]*toolCallAccumulator{},
		byIndex: map[int]*toolCallAccumulator{},
	}
}

// errUnaddressedToolCall is a chunk carrying neither of the two addresses.
// There is no honest place to put it: read as call 0, which is what this did
// once, it appends one call's arguments to another call's and hands a tool
// input the model never wrote — silently, and invisibly in the transcript. A
// round that ends here keeps the calls that were finished, so the session can
// still offer to continue from them.
var errUnaddressedToolCall = errors.New("tool call chunk carries neither an id nor an index")

// accumulate places one chunk's fragment on the call it belongs to, opening
// the call when this is the chunk that names it.
func (s *toolCallSet) accumulate(tc openai.ToolCall) (*toolCallAccumulator, error) {
	acc := s.find(tc)
	if acc == nil {
		if tc.ID == "" && tc.Index == nil {
			return nil, errUnaddressedToolCall
		}
		acc = &toolCallAccumulator{}
		s.order = append(s.order, acc)
	}
	if tc.ID != "" {
		acc.id = tc.ID
		s.byID[tc.ID] = acc
	}
	if tc.Index != nil {
		s.byIndex[*tc.Index] = acc
	}
	if tc.Function.Name != "" {
		acc.name = tc.Function.Name
	}
	acc.args.WriteString(tc.Function.Arguments)
	return acc, nil
}

// find resolves the addresses on a chunk against the calls already open. An
// index addresses only the call it was last seen on: a gateway that reuses a
// number for a second call names that call's id in the same chunk, so the id
// decides and the number follows the new call.
func (s *toolCallSet) find(tc openai.ToolCall) *toolCallAccumulator {
	if tc.ID != "" {
		if acc, ok := s.byID[tc.ID]; ok {
			return acc
		}
	}
	if tc.Index == nil {
		return nil
	}
	acc, ok := s.byIndex[*tc.Index]
	if !ok || (tc.ID != "" && acc.id != "" && acc.id != tc.ID) {
		return nil
	}
	return acc
}

// calls assembles the accumulated deltas in the order the calls were opened.
// It is called on a half-filled set too, when a stream breaks or a ceiling
// lands mid-accumulation, so a call that was opened and never named is still
// carried here and judged by whoever asks for whole calls.
func (s *toolCallSet) calls() []ToolCall {
	calls := make([]ToolCall, 0, len(s.order))
	for _, acc := range s.order {
		calls = append(calls, ToolCall{
			ID:        acc.id,
			Name:      acc.name,
			Arguments: acc.args.String(),
		})
	}
	return calls
}

func streamOpenAIToolCalls(stream *openai.ChatCompletionStream, classify func(error) error, watch *idleWatch) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		defer stream.Close()
		defer watch.stop()
		toolCalls := newToolCallSet()
		var usage *Usage
		var stop StopReason
		// The reason and the tokens arrive in separate chunks, so the ending
		// is held until both are in hand rather than sent on the first of
		// them. StopEnd is the zero value, so the flag is what says a reason
		// has been read at all.
		stopped := false

		for {
			resp, err := stream.Recv()
			// Every chunk pushes the idle deadline forward, the empty ones
			// this loop goes on to ignore included: what it watches for is
			// silence on the wire (idle.go).
			watch.alive()
			if errors.Is(err, io.EOF) {
				// A cancelled body can read as a clean end, so the deadline
				// is asked before the round is called finished.
				if idle := watch.err(nil); idle != nil {
					ch <- StreamEvent{ToolCalls: CompletedToolCalls(toolCalls.calls()), Err: classify(idle), Done: true}
					return
				}
				ch <- terminalOpenAIEvent(toolCalls, usage, stop)
				return
			}
			if err != nil {
				// The calls the model had finished writing travel with the
				// failure, so the session can offer to continue from them
				// rather than only from the top.
				ch <- StreamEvent{ToolCalls: CompletedToolCalls(toolCalls.calls()), Err: classify(watch.err(err)), Done: true}
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
				// With stream_options.include_usage the tokens for the whole
				// round come in a chunk of their own, after the chunk that
				// named the finish reason and with no choices in it. A round
				// that ended on the reason alone threw them away, so every
				// round that called a tool went unbilled and uncalibrated.
				if stopped {
					ch <- terminalOpenAIEvent(toolCalls, usage, stop)
					return
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
				acc, err := toolCalls.accumulate(tc)
				if err != nil {
					ch <- StreamEvent{ToolCalls: CompletedToolCalls(toolCalls.calls()), Err: classify(err), Done: true}
					return
				}
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
			// never as "tool_calls", so the ending is named by the reason
			// the model sent rather than derived from the calls in hand.
			if choice.FinishReason != "" {
				stop = openAIStop(string(choice.FinishReason))
				stopped = true
			}
		}
	}()
	return ch
}

// terminalOpenAIEvent is the event that ends the stream, whichever of the two
// endings got here: the usage chunk that follows the finish reason, or the
// body running out under it — which is also the ending of a gateway that
// sends no usage at all, and of one that folds usage into the finish-reason
// chunk. A ceiling reached mid-call keeps only the calls that are whole —
// half a JSON object would reach a tool as malformed input and be answered as
// though the model had asked for something.
func terminalOpenAIEvent(toolCalls *toolCallSet, usage *Usage, stop StopReason) StreamEvent {
	calls := toolCalls.calls()
	if stop == StopLength {
		calls = CompletedToolCalls(calls)
	}
	if len(calls) == 0 {
		// calls() returns an empty non-nil slice for an empty set, and a
		// terminal event carrying one is a round with tool calls to every
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
