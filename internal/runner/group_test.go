//go:build !windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The orphan this exists for: the runner holds a shell, and the work is the
// shell's children. A cancellation that signals only the shell leaves them
// running with nothing watching them.
func TestCancellingACommandKillsWhatItStarted(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "still-alive")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A child that outlives its parent shell unless the group is
		// signalled, and that leaves evidence behind if it does.
		RunCapture(ctx, "sh -c 'sleep 5; touch "+marker+"' & sleep 5")
	}()

	// Let the shell get as far as spawning its child before pulling the rug.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the runner did not return after cancellation")
	}

	// Past when the grandchild would have written, had it survived.
	time.Sleep(6 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a process the cancelled command started outlived it")
	}
}

// Cancellation has to return promptly even when something still holds the
// output pipe, which is what WaitDelay is for.
func TestCancellingReturnsWithoutWaitingOnAHeldPipe(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunCapture(ctx, "sleep 30")
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(waitDelay + 5*time.Second):
		t.Fatal("cancellation did not return inside the wait delay")
	}
}

// A deadline is a cancellation like any other, and the command's own output
// still comes back.
func TestADeadlineStopsACommandAndKeepsWhatItPrinted(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()

	out, code := RunCapture(ctx, "echo started; sleep 30")
	if !strings.Contains(out, "started") {
		t.Errorf("what the command printed before it was stopped should survive: %q", out)
	}
	if code == 0 {
		t.Error("a command that was killed did not exit cleanly")
	}
}

// An ordinary command is unaffected by any of this.
func TestAnOrdinaryCommandStillRuns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	out, code := RunCapture(context.Background(), "echo hello")
	if code != 0 || !strings.Contains(out, "hello") {
		t.Fatalf("out=%q code=%d", out, code)
	}
}
