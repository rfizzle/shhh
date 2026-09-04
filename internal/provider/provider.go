package provider

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	// Attachments are the non-conversational parts the message carries —
	// pasted images, files off the clipboard. Each provider's
	// converter decides how to put them on the wire; see attachment.go.
	Attachments []Attachment
	// Reasoning is the thinking the model did before this message, kept in
	// the provider's own form (reasoning.go). Only the assistant turn
	// that requested tools needs it, and only the providers that require it
	// back put it on the wire.
	Reasoning []ReasoningBlock
}

type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
	// Signature is the opaque per-part reasoning token some providers attach
	// to the tool call itself and require back on the next request.
	// Gemini 3 is the one that does: the thought signature rides the
	// functionCall part, and a history that drops it hands the model a plan
	// it cannot recognise as its own. It is base64 where the provider's is
	// binary, so it survives the JSON a resumed session is stored as.
	Signature string
}

// ToolCallDelta is one fragment of a tool call's arguments, as the model
// writes them. It carries the call's id and the fragment and nothing else:
// the name, the finished arguments and the order the calls were made in are
// all on the terminal event, which is the only place any of them is complete.
// A reader that treated a fragment as a call would be reading half-written
// JSON — the failure partial.go exists to prevent.
// See docs/capabilities/providers.md#tool-arguments-arrive-as-fragments.
type ToolCallDelta struct {
	ID        string
	Arguments string
}

type Usage struct {
	// PromptTokens is every input token the request is billed for, cached
	// ones included. The dialects do not agree on that: most report a prompt
	// count that already contains what they served from cache, while the
	// Messages API reports the three parts side by side and leaves the sum to
	// the reader. The converter is where they are made to agree, because a
	// figure whose meaning depends on which provider answered cannot be
	// added up, and the session ledger adds it up across all of them.
	PromptTokens     int
	CompletionTokens int
	// CachedTokens is the part of PromptTokens the provider served from its
	// prompt cache, and CacheCreationTokens the part it wrote into the cache
	// for the next request to read. Both are subsets of PromptTokens, they
	// never overlap, and zero means "not reported", not "nothing cached".
	//
	// They are separate because they are priced apart: a read is a fraction
	// of the input rate and a write is a premium over it.
	// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
	CachedTokens        int
	CacheCreationTokens int
}

type StreamEvent struct {
	Token     string
	ToolCalls []ToolCall
	Usage     *Usage
	Done      bool
	Err       error
	// Reasoning is the thinking blocks this response produced. It
	// rides the terminal event beside ToolCalls, and for the same reason:
	// what the model finished has to survive into the next request.
	Reasoning []ReasoningBlock
	// Thinking is reasoning text as it arrives, the way Token is answer text
	// as it arrives. It is a second channel rather than more Token because
	// the two are different acts and the transcript draws them as different
	// things — thinking is a row of its own, and a provider that streamed it
	// as a token would print the model's private murmur as its reply.
	//
	// Reasoning above is still what travels back on the next request: the
	// blocks are the provider's own signed form, and this text is only what
	// the screen can show of them. A provider that has one and not the other
	// is normal in both directions.
	Thinking string
	// ToolCallDelta is a tool call's arguments as they are written, the way
	// Token is answer text as it arrives. A model rewriting two hundred lines
	// spends most of a round inside one JSON blob, and nothing about that
	// round is reportable until the blob closes — which is a surface that
	// looks stopped at the moment the model is busiest.
	//
	// It is progress and never content. What a round acts on is ToolCalls on
	// the terminal event, unchanged; a fragment is never dispatched, stored
	// or replayed, and a stream that breaks mid-fragment still hands back
	// only the calls that are whole (partial.go).
	// See docs/capabilities/providers.md#tool-arguments-arrive-as-fragments.
	ToolCallDelta *ToolCallDelta
	// Stop is why the model stopped writing, on the terminal event and
	// nowhere else. The empty value is a stream that ended without saying —
	// a gateway that dropped the field, an event the parser never reached —
	// and it reads as StopEnd everywhere, because that is what every reader
	// assumed before there was a field at all.
	// See docs/capabilities/providers.md#a-reply-says-why-it-stopped.
	Stop StopReason
}

// StopReason is why a response ended, in shhh's own words rather than any
// dialect's. It is a closed set of five: the four endings a caller acts on
// differently and one for everything else.
//
// The set is small on purpose. Each dialect names more reasons than this —
// stop sequences, recitation, malformed calls, a paused turn — and a harness
// that tracked all of them would be tracking four vocabularies that agree on
// nothing. What a caller does about an ending is what earns it a name here:
// a reply that is finished, one that is owed tool results, one that was cut
// off mid-sentence and can be continued, and one the model declined to give.
// See docs/capabilities/providers.md#a-reply-says-why-it-stopped.
type StopReason string

const (
	// StopEnd is a reply the model finished, and the zero value with it: a
	// stream that never said why it ended is read as one that ended, which
	// is what every caller assumed before the field existed. A dialect that
	// drops the field on a truncated reply is the one thing that costs, and
	// no dialect shhh speaks does — they all report the ceiling by name.
	StopEnd StopReason = ""
	// StopTool is a reply that ended by asking for tools, and is owed their
	// results before the model can go on.
	StopTool StopReason = "tool"
	// StopLength is a reply cut off at the model's output ceiling. The words
	// are real and the sentence is not finished: continuing it is a different
	// act from asking the question again.
	StopLength StopReason = "length"
	// StopRefusal is a reply the model declined to give. Continuing it asks
	// for the same refusal a second time.
	StopRefusal StopReason = "refusal"
	// StopOther is an ending a dialect named and this set does not — a stop
	// sequence, a recitation block, a call the API could not parse. It is a
	// case of its own so that an unknown ending is a value a reader can see
	// rather than a silent StopEnd.
	StopOther StopReason = "other"
)

// String names the zero value, which is the one member of the set with no
// word of its own on the wire. Every %s and %q of a stop reason goes through
// it, so a message about one never reads as an empty string.
func (s StopReason) String() string {
	if s == StopEnd {
		return "end"
	}
	return string(s)
}

// stopForCalls names the ending two of the four dialects have no word for.
// They finish a round of tool calls on the same status they finish an answer
// on — "stop", "completed" — so the calls are the only thing that says the
// round is owed results, and a reader of the closed set would otherwise see
// `end` from those two where it sees `tool` from the other two.
//
// It never overrides a reason the dialect did name. A round cut at the
// ceiling stays `length` however many calls survived it, because what a
// caller does about that is the thing this whole set exists to tell it.
func stopForCalls(stop StopReason, calls []ToolCall) StopReason {
	if stop == StopEnd && len(calls) > 0 {
		return StopTool
	}
	return stop
}

type CompletionOpts struct {
	Model       string
	Temperature *float64
	// MaxTokens caps the reply. Zero sends no cap, which is what a session
	// and an unattended run both do: the reply is as long as the model makes
	// it, and the provider's own ceiling is the only one there is. The
	// bounded readings — a title, a classification, a summary — set one,
	// because a paragraph where a word was asked for is a defect.
	//
	// One dialect requires the field, so its converter supplies a default
	// rather than omitting it. A reply that reaches whichever ceiling
	// applied comes back with StopLength, which is what makes an unset cap
	// safe: nothing has to know the number to notice it was hit.
	MaxTokens int
	Tools     []Tool
	// ToolChoice is what the request says about calling a tool:
	// ToolChoiceAuto leaves it to the model, ToolChoiceNone describes the
	// tools and forbids calling one. The empty string sends no field, which
	// every dialect reads as auto.
	//
	// It is a bare string because each dialect spells it differently and the
	// converter is where they are made to agree. Those two are the whole set
	// a caller may send: every provider honours both, and a value outside
	// them is one some dialects forward and others quietly drop.
	// See docs/capabilities/providers.md#a-request-says-whether-a-tool-may-be-called.
	ToolChoice string
	// Effort is the reasoning level asked of the model (
	// reasoning.go). EffortOff — the zero value — sends nothing.
	Effort Effort
	// ResponseSchema is the shape the answer is asked to take, for a caller
	// that wants an object rather than prose. It is an offer and not an
	// instruction: a model that cannot be told to match a schema is sent
	// none, and the request falls back to whatever else it carried.
	//
	// A request may carry a schema or offer tools, never both — see
	// SchemaFor for what happens when a caller sends both, and why.
	// See docs/capabilities/providers.md#a-bounded-call-asks-for-the-shape-of-its-answer.
	ResponseSchema *ResponseSchema
}

// ResponseSchema is a JSON Schema the answer is validated against before it
// is sent, so a missing key or an invented one never reaches the caller.
type ResponseSchema struct {
	// Name is what the schema is called on the two dialects that require a
	// name for it. It must be a word — letters, digits, underscores and
	// hyphens — because that is all one of them accepts.
	Name string
	// Schema is the JSON Schema itself, put on the request as it was
	// written. Two dialects validate it strictly, which means every object
	// in it has to close (additionalProperties: false) and require every
	// key it names; a schema that leaves either open is a refused request
	// rather than a looser answer.
	Schema json.RawMessage
}

// SchemaFor is the schema this request may ask the named model to match, or
// nil when it may not and the tools it also carries are what goes out.
//
// The judge is the same one that decides the thinking level and the output
// ceiling, so a caller offers the schema unconditionally and gets the older
// path back wherever the newer one cannot be used.
//
// A schema and tools are alternatives and not a pair: Gemini refuses the two
// together outright, and where they are accepted the model may answer with a
// tool call the schema does not describe — which is the failure a schema is
// asked for in order to prevent. The schema wins, because it is the more
// specific of the two.
//
// It has to be an object and not merely valid JSON. Every dialect names the
// schema's keys in its own request, and one that is an array or a bare
// string describes nothing they can carry — so a converter handed one would
// send a format with no shape under it while having already dropped the
// tools, which is the one combination that is worse than either path.
// See docs/capabilities/providers.md#a-bounded-call-asks-for-the-shape-of-its-answer.
func (o CompletionOpts) SchemaFor(model string) *ResponseSchema {
	s := o.ResponseSchema
	if s == nil || !isJSONObject(s.Schema) {
		return nil
	}
	if !CapabilitiesFor(model).StructuredOutputs {
		return nil
	}
	return s
}

// isJSONObject reports whether raw is a JSON object. Unmarshalling into a map
// is the check: it rejects the scalars and the array that json.Valid accepts,
// and it is the same decode every converter then does for itself.
func isJSONObject(raw json.RawMessage) bool {
	var fields map[string]any
	return json.Unmarshal(raw, &fields) == nil && fields != nil
}

// The two values ToolChoice may carry. Naming a specific tool is
// deliberately not among them: the newest models refuse a forced choice
// outright, so a caller built on one is built on something being withdrawn.
// A caller that wants a particular tool asks for it in the prompt.
// See docs/capabilities/providers.md#a-request-says-whether-a-tool-may-be-called.
const (
	// ToolChoiceAuto lets the model call a tool or answer in text.
	ToolChoiceAuto = "auto"
	// ToolChoiceNone leaves the tools on the request and forbids calling
	// one, so the model answers in text without the tool schemas moving.
	ToolChoiceNone = "none"
)

type Provider interface {
	StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error)
	Name() string
}
