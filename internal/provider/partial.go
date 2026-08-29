package provider

// What a broken stream keeps (S-107,
// docs/interface/surfaces.md#the-recovery-row).
//
// Every dialect used to drop what it had accumulated the moment the transport
// failed. The text was already on screen, so it survived by accident, but the
// tool calls the model had finished writing went out with the error. That is
// what made "continue from here" impossible to offer honestly: the turn could
// only be asked again from the top, and asking again is not the same question
// once three of the four calls were already written.
//
// The error event carries them now. *Finished* is the whole question — a call
// whose arguments stopped halfway is not a call, it is a fragment that would
// reach a tool as JSON and be rejected there — so only the calls that are
// whole travel with the failure.

import (
	"encoding/json"
	"strings"
)

// CompletedToolCalls is the subset of calls a dropped stream may hand back:
// an id to answer with a result, a name to dispatch on, and arguments that
// parse. Everything else is a fragment, and a fragment is worse than nothing
// — it would be executed against arguments the model never finished choosing.
func CompletedToolCalls(calls []ToolCall) []ToolCall {
	var out []ToolCall
	for _, tc := range calls {
		if tc.ID == "" || tc.Name == "" || !wholeArguments(tc.Arguments) {
			continue
		}
		out = append(out, tc)
	}
	return out
}

// wholeArguments reports whether a call's argument buffer is a complete JSON
// value. Empty counts as whole: a tool that takes no arguments is written as
// `{}` by some dialects and as nothing at all by others.
func wholeArguments(args string) bool {
	if strings.TrimSpace(args) == "" {
		return true
	}
	return json.Valid([]byte(args))
}
