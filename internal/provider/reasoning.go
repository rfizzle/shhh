package provider

// Reasoning effort: how much thinking the session asks a model to do
// before it answers.
//
// Every provider shhh speaks has the knob and every one of them spells it
// differently — a named effort on the Responses API and on chat completions,
// a named effort under adaptive thinking on the current Anthropic models, a
// token budget on the older ones and on Gemini. The session should not have
// to know which: it picks one level and each provider translates, after
// fitting the level to what the model in front of it accepts
// (docs/capabilities/providers.md#a-level-is-fitted-to-the-model-before-it-is-sent).
//
// Off means the field is not sent at all. That matters more than it sounds:
// `reasoning_effort` on a model that has no reasoning (gpt-4o, gpt-4.1) is a
// 400, so "no opinion" has to stay distinguishable from "the lowest opinion".
// It is no longer the default — a session that says nothing starts on medium
// (docs/capabilities/providers.md#a-session-never-starts-without-thinking) —
// but it is still a level a session can choose.

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
	// EffortXHigh and EffortMax are the two rungs above high that only some
	// models have. Fit lowers them to the highest rung the model does have.
	EffortXHigh
	EffortMax
)

// DefaultEffort is the level a session starts on when nothing chose one.
const DefaultEffort = EffortMedium

func (e Effort) String() string {
	switch e {
	case EffortLow:
		return "low"
	case EffortMedium:
		return "medium"
	case EffortHigh:
		return "high"
	case EffortXHigh:
		return "xhigh"
	case EffortMax:
		return "max"
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
		return "a working budget for multi-step problems — the default"
	case EffortHigh:
		return "deep thinking for hard problems; slower and dearer"
	case EffortXHigh:
		return "above high, on the models that have it; the best setting for long agentic work"
	case EffortMax:
		return "the most thinking the model will do, on the models that have it; slowest and dearest"
	default:
		return "no reasoning requested — the model's own default"
	}
}

// On reports whether an effort asks for anything.
func (e Effort) On() bool { return e != EffortOff }

// ParseEffort maps a config value, a flag, or a /reasoning argument to its
// Effort. "none" is accepted as a spelling of off and "minimal" as one of
// low because the APIs use them for the same ideas. Empty is an error's
// absence, not a level: the caller decides what nothing means, and for a
// session that is DefaultEffort.
func ParseEffort(s string) (Effort, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return DefaultEffort, nil
	case "off", "none", "no":
		return EffortOff, nil
	case "low", "minimal":
		return EffortLow, nil
	case "medium", "med":
		return EffortMedium, nil
	case "high":
		return EffortHigh, nil
	case "xhigh", "x-high", "extra-high", "extrahigh":
		return EffortXHigh, nil
	case "max", "maximum":
		return EffortMax, nil
	}
	return EffortOff, fmt.Errorf("unknown reasoning level %q (valid: off, low, medium, high, xhigh, max)", s)
}

// EffortCycle is every level, lowest first — what the config editor offers
// and what the cycle key walks on a model whose rungs are not known.
func EffortCycle() []Effort {
	return []Effort{EffortOff, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}
}

// Levels is the rungs a model actually has, lowest first. A model whose
// rungs are not known gets all of them: better to offer a level Fit will
// lower than to hide one the model has.
func Levels(c Capabilities) []Effort {
	out := make([]Effort, 0, 6)
	for _, e := range EffortCycle() {
		if e.Fit(c) == e || !c.Known {
			out = append(out, e)
		}
	}
	return out
}

// NextEffort returns the level after current within levels, wrapping around.
// A current level that is not in the list restarts the walk.
func NextEffort(current Effort, levels []Effort) Effort {
	if len(levels) == 0 {
		levels = EffortCycle()
	}
	for i, e := range levels {
		if e == current {
			return levels[(i+1)%len(levels)]
		}
	}
	return levels[0]
}

// Fit lowers the level to one the model accepts. A rung the model lacks
// becomes the highest one it has; a model with no reasoning knob gets off,
// whatever was asked, because the alternative is a request the API refuses.
// A model nobody could describe is left alone — the caller knows its own
// endpoint's tolerance for a field it may not understand.
func (e Effort) Fit(c Capabilities) Effort {
	if !e.On() {
		return EffortOff
	}
	if c.Known && !c.Reasoning {
		return EffortOff
	}
	if e == EffortMax && c.Known && !c.Max {
		e = EffortXHigh
	}
	if e == EffortXHigh && c.Known && !c.XHigh {
		e = EffortHigh
	}
	return e
}

// OpenAIEffort is the `reasoning_effort` / `reasoning.effort` value, or ""
// when nothing should be sent. Max is not a word that API knows; the
// highest it spells is xhigh, so that is what an unfitted max becomes.
func (e Effort) OpenAIEffort() string {
	switch e {
	case EffortOff:
		return ""
	case EffortMax:
		return EffortXHigh.String()
	}
	return e.String()
}

// thinkingBudgets is the token ladder for the providers whose knob is a
// number rather than a name. The ceiling is the smaller of the two Gemini 2.5
// maxima (flash's 24576), so one ladder serves both families; xhigh and max
// have no more room to ask for than high does.
var thinkingBudgets = map[Effort]int{
	EffortLow:    4096,
	EffortMedium: 12288,
	EffortHigh:   24576,
	EffortXHigh:  24576,
	EffortMax:    24576,
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
