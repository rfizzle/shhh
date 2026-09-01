package provider

// Telling a dialect which part of the request it has already read.
//
// A coding turn re-sends its whole opening every round. The system prompt
// carries the project's context file, the memory block, the skills catalog
// and the toolbox; the tool schemas sit in front of it; and the conversation
// beneath only ever grows at the end. Nothing before the newest round changes,
// and by the fiftieth round that unchanged head is most of the request — read
// and billed fifty times, at full price, for text the provider has already
// seen forty-nine times.
//
// Most dialects handle this themselves by matching the prefix they were sent
// last time, which is why there is only one function here. The Messages API
// does not guess: it caches the prefix ending at a marker the request carries,
// and a request with no marker caches nothing at all. So the marker is what
// this file places, and it is placed on every request rather than behind a
// setting — it changes what a round costs and nothing about what it says, so
// there is no session that wants it off.
// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.

import "github.com/anthropics/anthropic-sdk-go"

// maxAnthropicCacheMarks is how many cache_control markers one request may
// carry; the API refuses a fifth. Three are placed, and the spare is the
// margin that keeps a later marker from having to displace an existing one.
const maxAnthropicCacheMarks = 4

// anthropicRollingMarks is how many of the conversation's trailing messages
// are marked.
//
// One would be enough if the provider always found the previous round's
// marker on its own. It searches back a bounded number of blocks from the
// marker it is given, and a round that ran its tool calls in parallel appends
// more than that bound: eight requests and the eight results answering them
// is already sixteen blocks before the reasoning and the reply are counted.
// The second mark is the previous round's own boundary, named outright so
// the search never has to reach it.
const anthropicRollingMarks = 2

// markAnthropicCache places the request's cache markers: one after the fixed
// head — the tools and the system prompt, which the API caches as one prefix
// because the tools precede the system prompt in it — and one at the end of
// each of the last anthropicRollingMarks messages.
//
// It marks positions, never content: every marker lands on a block the
// request was already sending. A request the API declines to cache (a prefix
// under its minimum, a dialect that does not know the field) is answered
// exactly as it would have been without the markers, which is what makes this
// safe to send to a gateway too.
func markAnthropicCache(params *anthropic.MessageNewParams) {
	marks := 0

	// The head is one prefix, so it takes one marker: the tools sit in front
	// of the system prompt in what the API hashes, and a marker on the system
	// prompt therefore covers both. Marking the tools as well would spend a
	// second marker on a prefix the first one already contains.
	if n := len(params.System); n > 0 && marks < maxAnthropicCacheMarks {
		params.System[n-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
		marks++
	}

	for i := len(params.Messages) - 1; i >= 0 && marks < maxAnthropicCacheMarks; i-- {
		if len(params.Messages)-i > anthropicRollingMarks {
			break
		}
		if markLastBlock(params.Messages[i].Content) {
			marks++
		}
	}
}

// markLastBlock marks the last block of a message that can carry a marker,
// and reports whether it found one.
//
// It searches from the end rather than taking the final block outright
// because not every block type has the field — a turn that is nothing but
// reasoning ends on a block the API will not accept a marker on, and a marker
// placed there would be a request rejected for the sake of a saving. Marking
// an earlier block of the same message is worth doing anyway: the prefix it
// caches is shorter by the tail of one message and still covers everything
// before it.
func markLastBlock(blocks []anthropic.ContentBlockParamUnion) bool {
	for i := len(blocks) - 1; i >= 0; i-- {
		if cc := blocks[i].GetCacheControl(); cc != nil {
			*cc = anthropic.NewCacheControlEphemeralParam()
			return true
		}
	}
	return false
}
