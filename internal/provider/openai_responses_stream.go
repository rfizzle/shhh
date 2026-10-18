package provider

// Reading a Responses stream. Where chat completions send choice deltas, this
// API sends named events: text arrives as deltas, tool calls are assembled
// from item events, and a terminal event carries the finished output and the
// usage. Tool calls are read from that terminal event's output list rather
// than accumulated from argument deltas — the same values, without the
// bookkeeping, and it stays correct if a gateway drops the delta events.
//
// The argument deltas are still read, for progress alone: they say how much
// of a call the model has written while it is writing it. They are addressed
// by the item they belong to, and the id a tool result is answered with is on
// the item event that opened it, so the two are paired here.
//
// Reasoning items are read the same way and for a different reason: they are
// the model's own thinking, and this API is the only one shhh speaks that
// hands it back as a blob to be given back untouched rather than as anything
// a reader could use.

import (
	"bufio"
	"encoding/json"
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
	eventArgsDelta    = "response.function_call_arguments.delta"
	eventItemDone     = "response.output_item.done"
	eventItemAdded    = "response.output_item.added"
)

// responseEvent is the union of the event payloads shhh reads. Everything
// else on the stream — created, in_progress, per-item content part
// boundaries — is ignored.
type responseEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	// ItemID addresses an argument delta to the item it is part of. It is
	// not the call id: the call id is on the item itself.
	ItemID   string `json:"item_id"`
	Response struct {
		Output []responseOutputItem `json:"output"`
		Usage  *responseUsage       `json:"usage"`
		Status string               `json:"status"`
		Error  *responseError       `json:"error"`
		// IncompleteDetails is why a response came back unfinished, and it
		// is the only place this API says so: the status is "incomplete"
		// whether the model filled its output budget or the endpoint
		// stopped it, and the two are not the same ending.
		IncompleteDetails *responseIncomplete `json:"incomplete_details"`
	} `json:"response"`
	Item    *responseOutputItem `json:"item"`
	Message string              `json:"message"`
	Code    string              `json:"code"`
}

type responseOutputItem struct {
	Type string `json:"type"`
	// ID is the item's own id, which the argument deltas are addressed to;
	// CallID is what a tool result is answered with. They are different
	// strings and this dialect is the only one that has both.
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	// Namespace is the tool group a call was made from, on a request that
	// grouped its tools (the ChatGPT plan route). The name beside it is the
	// function's own name inside the group.
	Namespace string `json:"namespace"`
	// EncryptedContent is a reasoning item's thinking, sealed. A response
	// the endpoint stores keeps its reasoning server-side and this field is
	// empty; shhh stores none, so it asks for the sealed form by name and
	// this is what comes back.
	EncryptedContent string `json:"encrypted_content"`
}

type responseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// CachedTokens is a pointer because a read the response sent as zero
	// and one it left out are different answers about the cache.
	InputTokensDetails struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// usage is the response's usage in Usage's terms.
func (u *responseUsage) usage() *Usage {
	out := &Usage{PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens}
	if c := u.InputTokensDetails.CachedTokens; c != nil {
		out.CachedTokens = *c
		out.Reports = FieldCachedTokens
	}
	return out
}

type responseIncomplete struct {
	Reason string `json:"reason"`
}

type responseError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// toolNamer maps a call's wire name, and the group it was made from, back
// to the name shhh registered the tool under. Nil is the identity, which is
// every route that sends its tools ungrouped.
type toolNamer func(namespace, name string) string

// streamResponses converts the SSE body into the caller's stream events.
func streamResponses(body io.ReadCloser, classify func(error) error, watch *idleWatch, rename toolNamer) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		defer body.Close()
		defer watch.stop()

		scanner := bufio.NewScanner(body)
		// A single event carries a whole tool-call payload, well past the
		// 64KB default token size.
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

		// Tool calls seen on item events, kept as a fallback for a stream
		// that ends without a terminal event.
		seen := map[string]ToolCall{}
		var order []string
		var usage *Usage
		// Item id to call id, so an argument fragment can be reported under
		// the id the rest of the session addresses that call by. An endpoint
		// that never opens the item sends no fragments anybody can place,
		// and reports no progress rather than progress under a wrong id.
		itemCalls := map[string]string{}
		// Reasoning items seen on item events, the same fallback the calls
		// have, deduplicated by item id because a stream is free to name an
		// item on more than one event.
		var reasoning []ReasoningBlock
		reasoningSeen := map[string]bool{}

		for scanner.Scan() {
			// Every line pushes the idle deadline forward, including the
			// blank separators and the keep-alive comments this reader has no
			// use for: what it watches for is silence on the wire (idle.go).
			watch.alive()
			line := scanner.Text()
			if strings.HasPrefix(line, ":") {
				ch <- StreamEvent{Keepalive: true}
				continue
			}
			payload, ok := sseData(line)
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

			case eventItemAdded:
				if ev.Item != nil && ev.Item.Type == "function_call" && ev.Item.ID != "" && ev.Item.CallID != "" {
					itemCalls[ev.Item.ID] = ev.Item.CallID
				}

			case eventArgsDelta:
				if id := itemCalls[ev.ItemID]; id != "" && ev.Delta != "" {
					ch <- StreamEvent{ToolCallDelta: &ToolCallDelta{ID: id, Arguments: ev.Delta}}
				}

			case eventItemDone, eventArgsDone:
				if call, ok := toolCallFrom(ev.Item, rename); ok {
					if _, exists := seen[call.ID]; !exists {
						order = append(order, call.ID)
					}
					seen[call.ID] = call
				}
				if block, ok := reasoningFrom(ev.Item); ok && !reasoningSeen[block.Signature] {
					reasoningSeen[block.Signature] = true
					reasoning = append(reasoning, block)
				}

			case eventCompleted, eventIncomplete:
				if ev.Response.Usage != nil {
					usage = ev.Response.Usage.usage()
				}
				calls := toolCallsFromOutput(ev.Response.Output, rename)
				if len(calls) == 0 {
					calls = orderedCalls(seen, order)
				}
				blocks := reasoningFromOutput(ev.Response.Output)
				if len(blocks) == 0 {
					blocks = reasoning
				}
				stop := responsesStop(ev)
				if stop == StopLength {
					// A ceiling reached mid-call keeps only the calls that
					// are whole. This API sends a call's finished arguments
					// on the item that closes it, so a call the ceiling
					// landed inside of usually never arrives at all — but
					// the terminal output list is written from whatever the
					// model produced, half an argument string included.
					calls = CompletedToolCalls(calls)
				}
				ch <- StreamEvent{ToolCalls: calls, Reasoning: blocks, Usage: usage, Stop: stopForCalls(stop, calls), Done: true}
				return

			case eventFailed, eventError:
				// The items that finished travel with the failure, so a
				// dropped stream can be continued — the thinking behind them
				// included, since continuing is what sends them again.
				ch <- StreamEvent{ToolCalls: CompletedToolCalls(orderedCalls(seen, order)), Reasoning: reasoning, Err: classify(responseFailure(ev)), Done: true}
				return
			}
		}

		// The deadline is asked even where the scanner stopped without an
		// error: a cancelled body can read as a clean end of stream.
		if err := watch.err(scanner.Err()); err != nil {
			ch <- StreamEvent{ToolCalls: CompletedToolCalls(orderedCalls(seen, order)), Reasoning: reasoning, Err: classify(err), Done: true}
			return
		}
		// The stream ended without a terminal event: finish with whatever it
		// did deliver rather than dropping a completed turn. There was no
		// status to read a reason off, so the calls are all there is to name
		// the ending by.
		trailing := orderedCalls(seen, order)
		ch <- StreamEvent{ToolCalls: trailing, Reasoning: reasoning, Usage: usage, Stop: stopForCalls(StopEnd, trailing), Done: true}
	}()
	return ch
}

// responsesStop maps a terminal response event onto the caller's closed set. A
// completed response ended; an incomplete one says why in a field of its own,
// and the one reason that is not a refusal is the output budget.
func responsesStop(ev responseEvent) StopReason {
	if ev.Type != eventIncomplete && ev.Response.Status != "incomplete" {
		return StopEnd
	}
	reason := ""
	if ev.Response.IncompleteDetails != nil {
		reason = ev.Response.IncompleteDetails.Reason
	}
	switch reason {
	case "max_output_tokens":
		return StopLength
	case "content_filter":
		return StopRefusal
	default:
		// An unfinished response the API would not say more about. It is not
		// a finished answer and it is not a truncation anything can continue
		// from, so it is neither.
		return StopOther
	}
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

// toolCallFrom reads one call item. The name is mapped back here, on every
// item, because the item events and the terminal output list are two copies
// of the same call and a session that answered under the wire name would be
// answering a tool it never registered.
func toolCallFrom(item *responseOutputItem, rename toolNamer) (ToolCall, bool) {
	if item == nil || item.Type != "function_call" || item.CallID == "" {
		return ToolCall{}, false
	}
	name := item.Name
	if rename != nil {
		name = rename(item.Namespace, item.Name)
	}
	return ToolCall{ID: item.CallID, Name: name, Arguments: item.Arguments}, true
}

// reasoningFrom reads a reasoning item. There is nothing readable on one:
// shhh asks for no summary, so the block carries the item's id and its sealed
// thinking and no text at all.
//
// An item with no sealed thinking is not one that can be replayed. It is an
// identifier for a response the endpoint was told not to keep, and sending it
// back names an item the server has never heard of — a refused request in
// place of a round that would merely have thought again.
func reasoningFrom(item *responseOutputItem) (ReasoningBlock, bool) {
	if item == nil || item.Type != "reasoning" || item.ID == "" || item.EncryptedContent == "" {
		return ReasoningBlock{}, false
	}
	return ReasoningBlock{Signature: item.ID, Redacted: item.EncryptedContent}, true
}

// reasoningFromOutput reads the finished response's reasoning items, in the
// order the model produced them.
func reasoningFromOutput(output []responseOutputItem) []ReasoningBlock {
	var blocks []ReasoningBlock
	for i := range output {
		if block, ok := reasoningFrom(&output[i]); ok {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// toolCallsFromOutput reads the finished response's output list, which holds
// every call the model made, in order.
func toolCallsFromOutput(output []responseOutputItem, rename toolNamer) []ToolCall {
	var calls []ToolCall
	for i := range output {
		if call, ok := toolCallFrom(&output[i], rename); ok {
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
// own message. The code travels with it: the ChatGPT plan route ends a
// stream that has already begun on a spent allowance this way, with no
// status to read, and the code is the only thing that says so.
func responseFailure(ev responseEvent) error {
	failure := &apiError{Code: ev.Code}
	if ev.Response.Error != nil && ev.Response.Error.Code != "" {
		failure.Code = ev.Response.Error.Code
	}
	switch {
	case ev.Response.Error != nil && ev.Response.Error.Message != "":
		failure.Message = ev.Response.Error.Message
	case ev.Message != "":
		failure.Message = ev.Message
	default:
		failure.Message = "the response failed without a message"
	}
	return failure
}
