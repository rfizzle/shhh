package agent

// Noticing a session ask the same question twice (S-164).
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

import (
	"encoding/json"
	"fmt"
	"sync"
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

// WrapExecutor wraps a tool executor so an interaction the session has
// already had comes back saying so. The notice leads the result, where the
// reduction pipeline puts its own: a notice below a hundred lines of output
// is one the model has already stopped reading by the time it arrives.
func (d *RepeatDetector) WrapExecutor(next ToolExecutor) ToolExecutor {
	if d == nil {
		return next
	}
	return func(name string, args json.RawMessage) (string, error) {
		out, err := next(name, args)
		if err != nil {
			return out, err
		}
		if n := d.Note(name, args, out); n >= repeatNoticeAfter {
			return repeatNotice(name, n) + "\n" + out, nil
		}
		return out, nil
	}
}

func repeatNotice(tool string, n int) string {
	return fmt.Sprintf(
		"[repeat: this exact %s call has now run %d times and returned exactly this each time — "+
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
