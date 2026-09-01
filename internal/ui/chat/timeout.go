package chat

// Saying that a command was stopped rather than that it failed.
//
// A cancelled command comes back the same way a broken one does: whatever it
// managed to print, and an exit code that only says it did not exit normally.
// Left at that, a model reads a timeout as a failing build and spends the
// next rounds debugging a command that was working fine and merely never
// going to finish — and a reader watching the row sees the same thing.
//
// So the reason is put where both of them will read it: on the end of the
// output, in the words that say what to do next.
// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// noteTimeout appends the timeout notice to a command's output when the
// deadline is what stopped it. Any other outcome — it finished, the reader
// cancelled it — is returned untouched, because only this one is invisible in
// what comes back.
func noteTimeout(out string, cause error, limit time.Duration) string {
	if !errors.Is(cause, context.DeadlineExceeded) {
		return out
	}
	notice := fmt.Sprintf(
		"… command stopped after %s: it reached the time limit for one command and was cancelled, along with everything it had started. "+
			"It did not fail — it did not finish. Run it in a way that completes (narrow the work, or start it as a background process), or raise behavior.command_timeout_seconds.",
		components.FormatElapsed(limit))
	if strings.TrimSpace(out) == "" {
		return notice
	}
	return strings.TrimRight(out, "\n") + "\n" + notice
}
