package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/tools"
)

func TestBoundedRunnerLeavesAFinishingCommandAlone(t *testing.T) {
	run := boundedRunner(func(context.Context, string) tools.ExecResult {
		return tools.ExecResult{Output: "done\n", Outcome: tools.ExecSucceeded}
	}, time.Minute)

	got := run(context.Background(), "echo done")
	if got.Output != "done\n" || got.ExitCode != 0 || got.Outcome != tools.ExecSucceeded {
		t.Fatalf("an ordinary command should pass through untouched: %+v", got)
	}
}

// The wrapper's whole job is the deadline: it is what the surfaces with no
// reader are missing, and the runner reads it back off the context to decide
// what a command that reaches it deserves.
func TestBoundedRunnerPutsTheLimitOnTheContext(t *testing.T) {
	var limit time.Duration
	run := boundedRunner(func(ctx context.Context, _ string) tools.ExecResult {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("a bounded command must carry a deadline")
			return tools.ExecResult{ExitCode: -1, Outcome: tools.ExecDidNotStart}
		}
		limit = time.Until(deadline)
		return tools.ExecResult{Outcome: tools.ExecSucceeded}
	}, time.Minute)

	run(context.Background(), "sleep 30")
	if limit <= 0 || limit > time.Minute {
		t.Fatalf("the deadline should be the limit, got %s", limit)
	}
}

// Removing the ceiling leaves the command genuinely unbounded rather than
// bounded by something very large.
func TestBoundedRunner_ClassifiesTimeout(t *testing.T) {
	run := boundedRunner(func(ctx context.Context, _ string) tools.ExecResult {
		<-ctx.Done()
		return tools.ExecResult{Output: "partial output", ExitCode: -9, Outcome: tools.ExecSignaled}
	}, 10*time.Millisecond)

	result := run(context.Background(), "sleep 30")
	if result.Outcome != tools.ExecTimedOut {
		t.Fatalf("outcome = %q, want timeout", result.Outcome)
	}
	formatted := tools.FormatExecResult(result)
	if !strings.HasPrefix(formatted, "error:") || !strings.Contains(formatted, "partial output") {
		t.Fatalf("timeout must be an error result that retains output: %q", formatted)
	}
}

func TestBoundedRunnerImposesNoDeadlineWithoutALimit(t *testing.T) {
	var deadlineSet bool
	run := boundedRunner(func(ctx context.Context, _ string) tools.ExecResult {
		_, deadlineSet = ctx.Deadline()
		return tools.ExecResult{Output: "x", Outcome: tools.ExecSucceeded}
	}, 0)

	if got := run(context.Background(), "anything"); got.Output != "x" {
		t.Fatalf("got %q", got.Output)
	}
	if deadlineSet {
		t.Error("a removed ceiling must not put a deadline on the context")
	}
}

// A caller's own cancellation still reaches the command, and arrives as a
// cancellation rather than as the limit having been reached — the two get
// different answers at the other end.
func TestBoundedRunnerPassesACancellationThrough(t *testing.T) {
	var cause error
	run := boundedRunner(func(ctx context.Context, _ string) tools.ExecResult {
		<-ctx.Done()
		cause = ctx.Err()
		return tools.ExecResult{Output: "stopped", ExitCode: -1, Outcome: tools.ExecStopped}
	}, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	run(ctx, "sleep 30")
	if cause != context.Canceled {
		t.Errorf("a cancellation is not a timeout: %v", cause)
	}
}
