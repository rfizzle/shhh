package cli

// The ceiling on one command, for the surfaces that have no reader to cancel
// it. The chat session applies its own (it has a cancel key, and it does not
// bound a command the reader typed); this is the wrapper every other runner
// goes through.
// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// boundedRunner returns run with a per-command deadline. A limit of zero or
// less returns run unchanged, so removing the ceiling costs no wrapper.
//
// The notice is appended here rather than left to the caller because a killed
// command is indistinguishable from a broken one in what comes back — output
// and an exit code — and a model told only that reads a timeout as a failing
// command and debugs the command.
func boundedRunner(run func(context.Context, string) (string, int), limit time.Duration) func(context.Context, string) (string, int) {
	if limit <= 0 {
		return run
	}
	return func(ctx context.Context, command string) (string, int) {
		ctx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		out, code := run(ctx, command)
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out, code
		}
		notice := fmt.Sprintf(
			"… command stopped after %s: it reached the time limit for one command and was cancelled, along with everything it had started. "+
				"It did not fail — it did not finish. Run it in a way that completes (narrow the work, or start it as a background process), or raise behavior.command_timeout_seconds.",
			components.FormatElapsed(limit))
		if strings.TrimSpace(out) == "" {
			return notice, code
		}
		return strings.TrimRight(out, "\n") + "\n" + notice, code
	}
}
