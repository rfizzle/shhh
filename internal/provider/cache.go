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
// last time. The Messages API does not guess: it caches the prefix ending at
// a marker the request carries, and a request with no marker caches nothing
// at all. So the marker is what this file places, and it is placed on every
// request rather than behind a setting — it changes what a round costs and
// nothing about what it says, so there is no session that wants it off. How
// long the head it marks stays cached is a setting; whether it is marked at
// all is not.
//
// Two paths reach that API and both have to be told. One speaks it directly;
// the other sends the OpenAI shape to a gateway that forwards it, honouring
// whatever breakpoints the request happened to carry. The positions have to
// be the same on both, so they are chosen once here and applied twice — a
// rule written down twice is a rule that gets corrected once.
// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// maxCacheMarks is how many cache_control markers one request may carry; the
// API refuses a fifth. Three are placed, and the spare is the margin that
// keeps a later marker from having to displace an existing one.
const maxCacheMarks = 4

// rollingCacheMarks is how many of the conversation's trailing messages are
// marked.
//
// One would be enough if the provider always found the previous round's
// marker on its own. It searches back a bounded number of blocks from the
// marker it is given, and a round that ran its tool calls in parallel appends
// more than that bound: eight requests and the eight results answering them
// is already sixteen blocks before the reasoning and the reply are counted.
// The second mark is the previous round's own boundary, named outright so
// the search never has to reach it.
const rollingCacheMarks = 2

// CacheTTL is how long a cached prefix outlives the request that wrote it.
// The API takes these two lifetimes and no others.
type CacheTTL string

const (
	CacheTTL5m CacheTTL = "5m"
	CacheTTL1h CacheTTL = "1h"
)

// DefaultCacheTTL is how long the request's fixed head stays cached when
// nothing chose. An hour, because the short lifetime is measured from the
// last read and an interactive session idles past five minutes constantly —
// the reader is looking at a diff, answering somebody, away from the desk —
// and the head is both the largest block of the request and the one that
// never changes. The longer lifetime costs more to write, which is why it is
// the head's alone and not everything's.
const DefaultCacheTTL = CacheTTL1h

// rollingCacheTTL is the lifetime the conversation's trailing markers get,
// and it is not configurable. They are replaced every round, so paying the
// dearer write for a prefix the next round supersedes buys nothing; only the
// head is worth the choice.
const rollingCacheTTL = CacheTTL5m

// ParseCacheTTL maps a config value to the lifetime it names. Empty is the
// default, the way an unset key is everywhere else.
func ParseCacheTTL(s string) (CacheTTL, error) {
	switch CacheTTL(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return DefaultCacheTTL, nil
	case CacheTTL5m:
		return CacheTTL5m, nil
	case CacheTTL1h:
		return CacheTTL1h, nil
	}
	return DefaultCacheTTL, fmt.Errorf("unknown cache lifetime %q (valid: 5m, 1h)", s)
}

// Describe is the one-line explanation the config screen shows beside each
// lifetime.
func (t CacheTTL) Describe() string {
	if t == CacheTTL1h {
		return "the opening survives a long pause; dearer to write, and the default"
	}
	return "the opening is written cheaply and expires while you read"
}

// CacheTTLCycle is every lifetime, shortest first — what the config editor
// offers.
func CacheTTLCycle() []CacheTTL {
	return []CacheTTL{CacheTTL5m, CacheTTL1h}
}

// cacheTTLOrDefault is the lifetime to go out with, given whatever the
// session was configured with. Nothing it cannot read reaches the wire: an
// unset value is the default, and so is a value that is neither lifetime,
// because every surface that writes the key already refuses anything else
// and what arrives here instead is a hand-edited file — where a typo should
// cost a setting rather than the ability to run.
func cacheTTLOrDefault(s string) CacheTTL {
	ttl, err := ParseCacheTTL(s)
	if err != nil {
		return DefaultCacheTTL
	}
	return ttl
}

// cacheMarks is where one request's markers go, as positions rather than in
// any dialect's own types: the fixed head — the tools and the system prompt,
// which the API caches as one prefix because the tools precede the system
// prompt in it — and the trailing messages whose ends are the round
// boundaries.
//
// The head takes one marker and not two. A marker on the system prompt covers
// the tools with it, so marking the tools as well would spend a second marker
// on a prefix the first one already contains.
type cacheMarks struct {
	// Head is whether there is a head to mark at all.
	Head bool
	// Messages are indexes into the conversation under the head, ascending.
	Messages []int
}

// planCacheMarks chooses a request's marker positions from whether it has a
// head and how many messages sit under it.
//
// It marks positions, never content: every marker lands on something the
// request was already sending. A request the API declines to cache (a prefix
// under its minimum, a dialect that does not know the field) is answered
// exactly as it would have been without the markers, which is what makes this
// safe to send to a gateway too.
func planCacheMarks(head bool, messages int) cacheMarks {
	marks := cacheMarks{Head: head}
	spent := 0
	if head {
		spent = 1
	}
	for i := max(messages-rollingCacheMarks, 0); i < messages && spent < maxCacheMarks; i++ {
		marks.Messages = append(marks.Messages, i)
		spent++
	}
	return marks
}

// markAnthropicCache places the plan's markers on a Messages API request.
func markAnthropicCache(params *anthropic.MessageNewParams, headTTL CacheTTL) {
	marks := planCacheMarks(len(params.System) > 0, len(params.Messages))
	if marks.Head {
		params.System[len(params.System)-1].CacheControl = cacheControl(cacheTTLOrDefault(string(headTTL)))
	}
	for _, i := range marks.Messages {
		markLastBlock(params.Messages[i].Content, rollingCacheTTL)
	}
}

// cacheControl is one marker at a chosen lifetime.
func cacheControl(ttl CacheTTL) anthropic.CacheControlEphemeralParam {
	cc := anthropic.NewCacheControlEphemeralParam()
	cc.TTL = anthropic.CacheControlEphemeralTTL(ttl)
	return cc
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
func markLastBlock(blocks []anthropic.ContentBlockParamUnion, ttl CacheTTL) bool {
	for i := len(blocks) - 1; i >= 0; i-- {
		if cc := blocks[i].GetCacheControl(); cc != nil {
			*cc = cacheControl(ttl)
			return true
		}
	}
	return false
}

// markOpenAICacheBody places the same plan on a chat-completions body bound
// for a gateway that forwards it to the Messages API.
//
// The shape is the one that dialect has for a breakpoint: `cache_control` on
// a content part, so a message whose content is a plain string becomes a
// one-element parts array carrying the same text. The annotation is made on
// the encoded body rather than on the request that produced it because the Go
// client's content part is a closed struct with nowhere to put the field, and
// the encoded body is the only place this request exists as JSON.
//
// Anything it cannot read is returned untouched — a body that is not an
// object, a model the gateway does not route to that API, a message with no
// content to hold a marker — so a request that gains nothing here is byte for
// byte the request that was sent before any of this existed.
func markOpenAICacheBody(body []byte, headTTL CacheTTL) []byte {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}
	var model string
	if err := json.Unmarshal(envelope["model"], &model); err != nil || !anthropicRouted(model) {
		return body
	}
	var msgs []json.RawMessage
	if err := json.Unmarshal(envelope["messages"], &msgs); err != nil {
		return body
	}

	// The head is the leading system message. It travels inside the message
	// list in this shape and beside it in the native one, so it comes off the
	// front before the conversation is counted — otherwise the two paths
	// would mark different rounds of the same conversation.
	head := -1
	if len(msgs) > 0 && messageRole(msgs[0]) == string(RoleSystem) {
		head = 0
	}
	marks := planCacheMarks(head == 0, len(msgs)-(head+1))

	marked := false
	if marks.Head {
		marked = markOpenAIContentPart(&msgs[head], cacheTTLOrDefault(string(headTTL)))
	}
	for _, i := range marks.Messages {
		if markOpenAIContentPart(&msgs[head+1+i], rollingCacheTTL) {
			marked = true
		}
	}
	if !marked {
		return body
	}

	raw, err := json.Marshal(msgs)
	if err != nil {
		return body
	}
	envelope["messages"] = raw
	out, err := json.Marshal(envelope)
	if err != nil {
		return body
	}
	return out
}

// messageRole is one message's role, or the empty string when it is not an
// object carrying one.
func messageRole(msg json.RawMessage) string {
	var fields struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(msg, &fields); err != nil {
		return ""
	}
	return fields.Role
}

// markOpenAIContentPart marks the last text part of one message, promoting a
// string content to the parts array the field lives on, and reports whether
// it placed a marker.
//
// A message with nothing to mark is left exactly as it was: an assistant turn
// that is only tool calls sends no content at all, and inventing an empty
// part to hang a marker on would change what the request says.
func markOpenAIContentPart(msg *json.RawMessage, ttl CacheTTL) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(*msg, &fields); err != nil {
		return false
	}
	content, ok := fields["content"]
	if !ok {
		return false
	}

	var parts []map[string]json.RawMessage
	var text string
	switch {
	case json.Unmarshal(content, &text) == nil:
		if text == "" {
			return false
		}
		quoted, err := json.Marshal(text)
		if err != nil {
			return false
		}
		parts = []map[string]json.RawMessage{{
			"type": json.RawMessage(`"text"`),
			"text": quoted,
		}}
	case json.Unmarshal(content, &parts) == nil:
	default:
		return false
	}

	// From the end, for markLastBlock's reason: an attached image is a part
	// that takes no marker, and the text in front of it caches a prefix one
	// attachment shorter rather than none at all.
	at := -1
	for i := len(parts) - 1; i >= 0; i-- {
		if string(parts[i]["type"]) == `"text"` {
			at = i
			break
		}
	}
	if at < 0 {
		return false
	}
	parts[at]["cache_control"] = json.RawMessage(`{"type":"ephemeral","ttl":"` + string(ttl) + `"}`)

	raw, err := json.Marshal(parts)
	if err != nil {
		return false
	}
	fields["content"] = raw
	out, err := json.Marshal(fields)
	if err != nil {
		return false
	}
	*msg = out
	return true
}
