package cli

import (
	"context"
	"strings"
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

// The notice is the whole point: output and an exit code cannot tell a
// command that failed from one that was stopped, and a model given only those
// debugs the command.
func TestBoundedRunnerSaysWhyACommandStopped(t *testing.T) {
	run := boundedRunner(func(ctx context.Context, _ string) (string, int) {
		<-ctx.Done()
		return "partial output", -1
	}, 50*time.Millisecond)

	out, _ := run(context.Background(), "sleep 30")
	if !strings.Contains(out, "partial output") {
		t.Errorf("what the command printed should survive: %q", out)
	}
	if !strings.Contains(out, "did not finish") {
		t.Errorf("the notice must distinguish stopped from failed: %q", out)
	}
	if !strings.Contains(out, "command_timeout_seconds") {
		t.Errorf("the notice should name the way out: %q", out)
	}
}

func TestBoundedRunnerNotesATimeoutThatPrintedNothing(t *testing.T) {
	run := boundedRunner(func(ctx context.Context, _ string) (string, int) {
		<-ctx.Done()
		return "", -1
	}, 50*time.Millisecond)

	out, _ := run(context.Background(), "sleep 30")
	if !strings.Contains(out, "did not finish") {
		t.Errorf("a silent command that timed out still has to say so: %q", out)
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

// A caller's own cancellation is not a timeout, and must not be reported as
// one — the reader pressing the cancel key has not hit any limit.
func TestBoundedRunnerDoesNotCallACancellationATimeout(t *testing.T) {
	run := boundedRunner(func(ctx context.Context, _ string) (string, int) {
		<-ctx.Done()
		return "stopped", -1
	}, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	out, _ := run(ctx, "sleep 30")
	if strings.Contains(out, "time limit") {
		t.Errorf("a cancellation is not a timeout: %q", out)
	}
}
