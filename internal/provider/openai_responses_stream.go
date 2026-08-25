package provider

// Reading a Responses stream. Where chat completions send choice deltas, this
// API sends named events: text arrives as deltas, tool calls are assembled
// from item events, and a terminal event carries the finished output and the
// usage. Tool calls are read from that terminal event's output list rather
// than accumulated from argument deltas — the same values, without the
// bookkeeping, and it stays correct if a gateway drops the delta events.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Response stream event types.
const (
	eventTextDelta    = "response.output_text.delta"
	eventRefusalDelta = "response.refusal.delta"
	eventCompleted    = "response.completed"
	eventIncomplete   = "response.incomplete"
	eventFailed       = "response.failed"
	eventError        = "error"
	eventArgsDone     = "response.function_call_arguments.done"
	eventItemDone     = "response.output_item.done"
)

// responseEvent is the union of the event payloads shhh reads. Everything
// else on the stream — created, in_progress, per-item added/delta, content
// part boundaries — is ignored.
type responseEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	Response struct {
		Output []responseOutputItem `json:"output"`
		Usage  *responseUsage       `json:"usage"`
		Status string               `json:"status"`
		Error  *responseError       `json:"error"`
	} `json:"response"`
	Item    *responseOutputItem `json:"item"`
	Message string              `json:"message"`
	Code    string              `json:"code"`
}

type responseOutputItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type responseError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// streamResponses converts the SSE body into shhh's stream events.
func streamResponses(body io.ReadCloser, classify func(error) error) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		defer body.Close()

		scanner := bufio.NewScanner(body)
		// A single event carries a whole tool-call payload, well past the
		// 64KB default token size.
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

		// Tool calls seen on item events, kept as a fallback for a stream
		// that ends without a terminal event.
		seen := map[string]ToolCall{}
		var order []string
		var usage *Usage

		for scanner.Scan() {
			payload, ok := sseData(scanner.Text())
			if !ok {
				continue
			}
			var ev responseEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				// A frame shhh can't read is not worth failing the turn over.
				continue
			}

			switch ev.Type {
			case eventTextDelta, eventRefusalDelta:
				if ev.Delta != "" {
					ch <- StreamEvent{Token: ev.Delta}
				}

			case eventItemDone, eventArgsDone:
				if call, ok := toolCallFrom(ev.Item); ok {
					if _, exists := seen[call.ID]; !exists {
						order = append(order, call.ID)
					}
					seen[call.ID] = call
				}

			case eventCompleted, eventIncomplete:
				if ev.Response.Usage != nil {
					usage = &Usage{
						PromptTokens:     ev.Response.Usage.InputTokens,
						CompletionTokens: ev.Response.Usage.OutputTokens,
					}
				}
				calls := toolCallsFromOutput(ev.Response.Output)
				if len(calls) == 0 {
					calls = orderedCalls(seen, order)
				}
				ch <- StreamEvent{ToolCalls: calls, Usage: usage, Done: true}
				return

			case eventFailed, eventError:
				ch <- StreamEvent{Err: classify(responseFailure(ev)), Done: true}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Err: classify(err), Done: true}
			return
		}
		// The stream ended without a terminal event: finish with whatever it
		// did deliver rather than dropping a completed turn.
		ch <- StreamEvent{ToolCalls: orderedCalls(seen, order), Usage: usage, Done: true}
	}()
	return ch
}

// sseData returns the JSON payload of a `data:` line. Event-name lines,
// comments, and blank separators carry nothing shhh needs, since every
// payload names its own type.
func sseData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return "", false
	}
	return payload, true
}

func toolCallFrom(item *responseOutputItem) (ToolCall, bool) {
	if item == nil || item.Type != "function_call" || item.CallID == "" {
		return ToolCall{}, false
	}
	return ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments}, true
}

// toolCallsFromOutput reads the finished response's output list, which holds
// every call the model made, in order.
func toolCallsFromOutput(output []responseOutputItem) []ToolCall {
	var calls []ToolCall
	for i := range output {
		if call, ok := toolCallFrom(&output[i]); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

func orderedCalls(seen map[string]ToolCall, order []string) []ToolCall {
	if len(order) == 0 {
		return nil
	}
	calls := make([]ToolCall, 0, len(order))
	for _, id := range order {
		calls = append(calls, seen[id])
	}
	return calls
}

// responseFailure turns a failure event into an error, preferring the API's
// own message.
func responseFailure(ev responseEvent) error {
	switch {
	case ev.Response.Error != nil && ev.Response.Error.Message != "":
		return fmt.Errorf("%s", ev.Response.Error.Message)
	case ev.Message != "":
		return fmt.Errorf("%s", ev.Message)
	default:
		return fmt.Errorf("the response failed without a message")
	}
}
