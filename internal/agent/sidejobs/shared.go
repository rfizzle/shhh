// Package sidejobs is the bounded calls outside the main turn: the title, the
// summary, the suggestion, the account, the handoff and the command
// explanation. Each is one request to a cheap model through the
// provider.Provider it is handed, no retries here, and a failed reading
// changes nothing (docs/architecture.md#one-agent-several-front-ends).
//
// It is a leaf under the agent: the agent's own readers (the classifier, the
// pattern writer, the start offers) and the front-ends import it, and it
// imports neither, so what the readers share (the model a reader asks with,
// the small bounds on what it is shown) lives in this file.
package sidejobs

import (
	"fmt"
	"strings"
)

// TargetJoin separates the things a person has asked for in one turn. It is a
// blank line because the target is quoted back to the model whole ("What was
// asked:"), and two instructions run together on one line read as one
// sentence that contradicts itself.
const TargetJoin = "\n\n"

// ModelAt is the model a bounded reader asks with: what at answers, where
// the surface handed one, and the fixed name it was built with otherwise.
// It is asked once per call and the answer kept for the whole of it, so a
// reader moved onto another model for the rest of a session takes it on its
// next call and one already in flight finishes on the model it started with.
// See docs/capabilities/configuration.md#a-session-can-hold-a-value-no-file-does.
func ModelAt(at func() string, fixed string) string {
	if at != nil {
		return strings.TrimSpace(at())
	}
	return strings.TrimSpace(fixed)
}

func FirstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// TruncateTail keeps the head of an oversized string with a note about what
// was dropped.
func TruncateTail(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	return fmt.Sprintf("%s\n... [%d characters omitted]", s[:maxChars], len(s)-maxChars)
}

// ClampTargetParts bounds an extended target for a field that has a budget,
// and returns what was asked as separate parts for the caller to lay out.
//
// Every bound in this package truncates the tail, and the tail of an extended
// target is the newest thing the person said. A long instruction with a steer
// after it would reach the reader as the instruction alone, and the reader
// would go on judging the work against words the person had already replaced
// — the failure ExtendTarget exists to end, arriving by way of the bound
// instead. So the budget is shared out evenly instead: one instruction is
// clamped exactly as it always was, and every further one is guaranteed a
// share of what is left.
func ClampTargetParts(target string, limit int) []string {
	parts := strings.Split(strings.TrimSpace(target), TargetJoin)
	share := limit
	if len(parts) > 1 {
		share = (limit - len(TargetJoin)*(len(parts)-1)) / len(parts)
	}
	if share < 2 {
		// Below two runes there is no share to give: a clamped string is at
		// least the ellipsis that marks it. The newest instruction is the one
		// that changes the answer, so it takes the whole budget and the rest
		// are dropped rather than every one of them being reduced to a mark.
		parts = parts[len(parts)-1:]
		share = limit
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, ClampRunes(strings.TrimSpace(p), share))
	}
	return out
}
