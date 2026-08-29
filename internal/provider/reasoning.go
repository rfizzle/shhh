package provider

// Reasoning effort: how much thinking the session asks a model to do
// before it answers.
//
// Every provider shhh speaks has the knob and every one of them spells it
// differently — a named effort on the Responses API and on chat completions,
// a token budget on Anthropic and Gemini. The session should not have to know
// which: it picks one of four levels and each provider translates.
//
// Off is the default and it means the field is not sent at all. That matters
// more than it sounds: `reasoning_effort` on a model that has no reasoning
// (gpt-4o, gpt-4.1) is a 400, so "no opinion" has to stay distinguishable
// from "the lowest opinion". A session that never touches the key gets
// exactly the requests it got before this existed.

import (
	"fmt"
	"strings"
)

// Effort is the reasoning level asked of the model.
type Effort int

const (
	// EffortOff sends no reasoning field: the endpoint's own default applies,
	// and a model that has no reasoning knob is not handed one.
	EffortOff Effort = iota
	EffortLow
	EffortMedium
	EffortHigh
)

func (e Effort) String() string {
	switch e {
	case EffortLow:
		return "low"
	case EffortMedium:
		return "medium"
	case EffortHigh:
		return "high"
	default:
		return "off"
	}
}

// Describe is the one-line explanation shown by /reasoning and /help.
func (e Effort) Describe() string {
	switch e {
	case EffortLow:
		return "a little thinking before answering — the cheapest level that still reasons"
	case EffortMedium:
		return "a working budget for multi-step problems"
	case EffortHigh:
		return "the most thinking the model will do; slowest and dearest"
	default:
		return "no reasoning requested — the model's own default, and what every session did before this key existed"
	}
}

// On reports whether an effort asks for anything.
func (e Effort) On() bool { return e != EffortOff }

// ParseEffort maps a config value, a flag, or a /reasoning argument to its
// Effort. "none" and "minimal" are accepted as spellings of off because two
// of the APIs use them for the same idea.
func ParseEffort(s string) (Effort, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off", "none", "no", "minimal":
		return EffortOff, nil
	case "low":
		return EffortLow, nil
	case "medium", "med":
		return EffortMedium, nil
	case "high", "max":
		return EffortHigh, nil
	}
	return EffortOff, fmt.Errorf("unknown reasoning level %q (valid: off, low, medium, high)", s)
}

// EffortCycle is the order the cycle key walks, lowest first.
func EffortCycle() []Effort {
	return []Effort{EffortOff, EffortLow, EffortMedium, EffortHigh}
}

// NextEffort returns the level after current, wrapping around.
func NextEffort(current Effort) Effort {
	cycle := EffortCycle()
	for i, e := range cycle {
		if e == current {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

// OpenAIEffort is the `reasoning_effort` / `reasoning.effort` value, or ""
// when nothing should be sent.
func (e Effort) OpenAIEffort() string {
	if !e.On() {
		return ""
	}
	return e.String()
}

// thinkingBudgets is the token ladder for the providers whose knob is a
// number rather than a name. The ceiling is the smaller of the two Gemini 2.5
// maxima (flash's 24576), so one ladder serves both families.
var thinkingBudgets = map[Effort]int{
	EffortLow:    4096,
	EffortMedium: 12288,
	EffortHigh:   24576,
}

// MinThinkingBudget is the smallest budget the Messages API accepts; a
// ceiling that cannot fit it means no thinking rather than a rejected
// request.
const MinThinkingBudget = 1024

// ThinkingBudget is the token budget this effort asks for, clamped to
// ceiling. It returns 0 when nothing should be sent — either the effort is
// off, or the ceiling leaves no room for a budget the API would accept.
func (e Effort) ThinkingBudget(ceiling int) int {
	budget := thinkingBudgets[e]
	if budget == 0 {
		return 0
	}
	if ceiling > 0 && budget > ceiling {
		budget = ceiling
	}
	if budget < MinThinkingBudget {
		return 0
	}
	return budget
}

// ReasoningBlock is one provider-native reasoning block the model produced,
// carried back into the conversation exactly as it arrived.
//
// It exists for one requirement: with extended thinking on, the Messages API
// rejects a follow-up request whose last assistant turn contains tool_use but
// not the thinking blocks that led to it. So the blocks are not decoration —
// dropping them turns the second round of every thinking turn into a 400.
// Text and Signature are the two halves of a thinking block; Redacted holds
// the opaque payload of a safety-redacted one, which travels the same way and
// has no readable content.
type ReasoningBlock struct {
	Text      string
	Signature string
	Redacted  string
}
