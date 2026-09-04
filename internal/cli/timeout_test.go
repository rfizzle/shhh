package cli

import (
	"context"
	"testing"
	"time"
)

func TestBoundedRunnerLeavesAFinishingCommandAlone(t *testing.T) {
	run := boundedRunner(func(context.Context, string) (string, int) {
		return "done\n", 0
	}, time.Minute)

	out, code := run(context.Background(), "echo done")
	if out != "done\n" || code != 0 {
		t.Fatalf("an ordinary command should pass through untouched: %q %d", out, code)
	}
}

// The wrapper's whole job is the deadline: it is what the surfaces with no
// reader are missing, and the runner reads it back off the context to decide
// what a command that reaches it deserves.
func TestBoundedRunnerPutsTheLimitOnTheContext(t *testing.T) {
	var limit time.Duration
	run := boundedRunner(func(ctx context.Context, _ string) (string, int) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("a bounded command must carry a deadline")
			return "", -1
		}
		limit = time.Until(deadline)
		return "", 0
	}, time.Minute)

	run(context.Background(), "sleep 30")
	if limit <= 0 || limit > time.Minute {
		t.Fatalf("the deadline should be the limit, got %s", limit)
	}
}

// Removing the ceiling leaves the command genuinely unbounded rather than
// bounded by something very large.
func TestBoundedRunnerImposesNoDeadlineWithoutALimit(t *testing.T) {
	var deadlineSet bool
	run := boundedRunner(func(ctx context.Context, _ string) (string, int) {
		_, deadlineSet = ctx.Deadline()
		return "x", 0
	}, 0)

	out, _ := run(context.Background(), "anything")
	if out != "x" {
		t.Fatalf("got %q", out)
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
	run := boundedRunner(func(ctx context.Context, _ string) (string, int) {
		<-ctx.Done()
		cause = ctx.Err()
		return "stopped", -1
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
