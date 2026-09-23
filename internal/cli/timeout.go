package cli

// The ceiling on one command, for the surfaces that have no reader to cancel
// it. The chat session applies its own (it has a cancel key, and it does not
// bound a command the reader typed); this is the wrapper every other runner
// goes through.
//
// The wrapper only sets the limit. What happens at it — the command handed to
// the process supervisor because it is still printing, or stopped because it
// is not, and the sentence saying which — belongs to the code holding the
// running command, which is the runner.
// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.

import (
	"context"
	"time"

	"github.com/rfizzle/shhh/internal/tools"
)

// boundedRunner returns run with a per-command deadline. A limit of zero or
// less returns run unchanged, so removing the ceiling costs no wrapper. The
// deadline remains live until the runner returns, so a timeout is not
// mistaken for an ordinary signal death by a later formatter.
func boundedRunner(run func(context.Context, string) tools.ExecResult, limit time.Duration) func(context.Context, string) tools.ExecResult {
	if limit <= 0 {
		return run
	}
	return func(ctx context.Context, command string) tools.ExecResult {
		ctx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		result := run(ctx, command)
		if result.ExitCode != 0 && ctx.Err() == context.DeadlineExceeded {
			result.Outcome = tools.ExecTimedOut
		}
		return result
	}
}
