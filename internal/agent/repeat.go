package agent

// Noticing a session ask the same question twice.
//
// The failure this exists for looks like work: forty searches over the same
// directory, each returning what the one nine rounds ago returned, until the
// round cap ends the turn with nothing changed. Nothing in the loop was
// broken — the model simply could not tell it was going in a circle, because
// every round looks like progress from inside one.
//
// So the loop says so. The signature is the whole interaction — the tool, its
// arguments, and the output it produced — which is what makes it safe to
// apply to every tool: `go test` run twice is two different interactions the
// moment its output differs, and identical only when nothing has changed,
// which is exactly when running it again is pointless. Crush stops the turn
// on the same signal; we tell the model instead, because the model is the one
// that can pick a different approach, and a turn stopped is a turn the user
// has to restart.
//
// It sits on both tiers, because the calls a session circles on are mostly
// the ones somebody has to answer for: the failing command run again, the
// refused edit issued again. A detector wrapped around the auto chain alone
// would be one that cannot see the example in the paragraph above.
// See docs/capabilities/coding-agent.md#a-call-the-session-has-already-made-is-answered-by-saying-so.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/provider"
)

const (
	// repeatWindow is how many recent tool interactions are remembered. Long
	// enough to catch a circling investigation, short enough that a
	// legitimate revisit many rounds later is not called a repeat.
	repeatWindow = 24

	// repeatNoticeAfter is the occurrence within the window that earns a
	// notice. The second identical interaction is already one too many:
	// waiting for a third spends another round to say the same thing.
	repeatNoticeAfter = 2
)

// RepeatDetector watches a session's tool interactions for exact repeats.
// The zero value is not usable; call NewRepeatDetector.
type RepeatDetector struct {
	mu     sync.Mutex
	recent []string
}

func NewRepeatDetector() *RepeatDetector {
	return &RepeatDetector{recent: make([]string, 0, repeatWindow)}
}

// Note records one completed interaction and returns how many times this
// exact one has occurred inside the window, itself included. Safe on a nil
// detector, which counts nothing.
func (d *RepeatDetector) Note(tool string, args json.RawMessage, result string) int {
	if d == nil {
		return 0
	}
	key := interactionKey(tool, args, result)

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.recent) == repeatWindow {
		d.recent = d.recent[1:]
	}
	d.recent = append(d.recent, key)

	n := 0
	for _, k := range d.recent {
		if k == key {
			n++
		}
	}
	return n
}

// Notice is one completed interaction as the model should read it: the notice
// leads the result where this exact call has run before, and the result is
// itself otherwise. Every tier goes through it, so the auto executor, the
// gated resolver and a front-end that dispatches a call itself all apply the
// same rule to the same window. Safe on a nil detector.
func (d *RepeatDetector) Notice(tool string, args json.RawMessage, result string) string {
	if n := d.Note(tool, args, result); n >= repeatNoticeAfter {
		failed := strings.HasPrefix(result, errorPrefix)
		return leadNotice(repeatNotice(tool, n, failed), result)
	}
	return result
}

// WrapExecutor wraps a tool executor so an interaction the session has
// already had comes back saying so. The notice leads the result, where the
// reduction pipeline puts its own: a notice below a hundred lines of output
// is one the model has already stopped reading by the time it arrives.
//
// A call that failed is counted too, keyed on the error the model reads. A
// path that does not exist, an argument the tool will never accept and a
// refusal that stands are circling as surely as a search that keeps returning
// the same hits, and a failure repeated is the shape a stuck turn most often
// takes.
func (d *RepeatDetector) WrapExecutor(next ToolExecutor) ToolExecutor {
	if d == nil {
		return next
	}
	return func(name string, args json.RawMessage) (string, error) {
		out, err := next(name, args)
		if err != nil {
			if n := d.Note(name, args, errorPrefix+err.Error()); n >= repeatNoticeAfter {
				// The caller puts errorPrefix back in front of this, so what
				// the model reads is what leadNotice builds for a failure.
				// The error is wrapped rather than replaced: a caller that
				// tests it with errors.Is is asking about the failure and not
				// about how often it has happened.
				return out, fmt.Errorf("%s\n%w", repeatNotice(name, n, true), err)
			}
			return out, err
		}
		return d.Notice(name, args, out), nil
	}
}

// WrapResolver wraps the gated tier the way WrapExecutor wraps the auto one.
// A command and a file modification are resolved rather than dispatched — the
// decision and the call are one function — so the tier the detector was
// written for is reachable only here, and a run that wraps one chain and not
// the other cannot see the case its own comment names.
func (d *RepeatDetector) WrapResolver(next func(provider.ToolCall) string) func(provider.ToolCall) string {
	if d == nil {
		return next
	}
	return func(tc provider.ToolCall) string {
		return d.Notice(tc.Name, json.RawMessage(tc.Arguments), next(tc))
	}
}

// IsRepeatNotice reports whether a result leads with the detector's notice,
// so a front-end can count how often the session was told it was circling
// without knowing how the notice is worded.
func IsRepeatNotice(result string) bool {
	return strings.HasPrefix(result, repeatNoticePrefix) ||
		strings.HasPrefix(result, errorPrefix+repeatNoticePrefix)
}

const repeatNoticePrefix = "[repeat:"

// errorPrefix is the error convention every executor and every resolver
// follows, and the one thing allowed in front of the notice.
const errorPrefix = "error: "

// leadNotice puts the notice at the head of the result, behind the error
// prefix where there is one. Everything that reads a result tells a failure
// by that prefix — the digest's outcome word, the record's failure class, the
// transcript row — so a notice written in front of it would turn every
// repeated refusal into a success in all three.
func leadNotice(notice, result string) string {
	if rest, ok := strings.CutPrefix(result, errorPrefix); ok {
		return errorPrefix + notice + "\n" + rest
	}
	return notice + "\n" + result
}

// repeatNotice is what the model reads. A call that keeps working and a call
// that keeps failing are the same circle and take the same first sentence,
// but not the same way out: "widen the search" is advice for an answer that
// is not the one wanted, and nonsense for a command that will not run or an
// edit a policy has refused twice.
func repeatNotice(tool string, n int, failed bool) string {
	if failed {
		return fmt.Sprintf(
			repeatNoticePrefix+" this exact %s call has now come back the same way %d times — "+
				"the earlier result is still above, unchanged. Making it again will not change it. "+
				"Answer what the result says, or reach the same end another way.]",
			tool, n)
	}
	return fmt.Sprintf(
		repeatNoticePrefix+" this exact %s call has now run %d times and returned exactly this each time — "+
			"the earlier result is still above, unchanged. Running it again will not answer the question. "+
			"Read one of the files it names, widen or narrow the search, or use a different tool.]",
		tool, n)
}

// interactionKey identifies one tool interaction. Arguments are canonicalised
// through a decode/encode round trip so the same call written two ways — a
// different key order, different spacing — is recognised as the same call.
func interactionKey(tool string, args json.RawMessage, result string) string {
	canonical := string(args)
	var v any
	if err := json.Unmarshal(args, &v); err == nil {
		if b, err := json.Marshal(v); err == nil {
			canonical = string(b)
		}
	}
	return tool + "\x00" + canonical + "\x00" + result
}
