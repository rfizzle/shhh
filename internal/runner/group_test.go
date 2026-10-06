//go:build !windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The orphan this exists for: the runner holds a shell, and the work is the
// shell's children. A cancellation that signals only the shell leaves them
// running with nothing watching them.
func TestCancellingACommandKillsWhatItStarted(t *testing.T) {
	needShell(t)
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	marker := filepath.Join(dir, "still-alive")

	ctx, cancel := context.WithCancel(context.Background())
	lines := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A child that outlives its parent shell unless the group is
		// signalled, and that leaves evidence behind if it does. The shell
		// names itself, and so the group, and the child says once it is
		// running, which is the moment there is a child to leave behind.
		// Its life is long beside the kill's grace, so a slow host cannot
		// let it reach the marker before the kill was ever due.
		RunCaptureTail(ctx, "echo shell $$; sh -c 'echo started; sleep 20; touch "+marker+"' & sleep 20", sendLine(lines))
	}()

	pgid := awaitGroup(t, lines)
	awaitLine(t, lines, "started")
	cancel()

	select {
	case <-done:
	case <-time.After(waitDelay + 10*time.Second):
		t.Fatal("the runner did not return after cancellation")
	}

	// The group being empty is the fact: nothing in it can write the marker
	// any more, so its absence then is the answer rather than a guess.
	awaitGroupGone(t, pgid)
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
	lines := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunCaptureTail(ctx, "echo started; sleep 30", sendLine(lines))
	}()
	// Cancelled once it is running, so the wait delay is what is measured
	// and not a command that never got as far as starting.
	awaitLine(t, lines, "started")
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

// The orphan the group mechanism exists to stop, on the path where nobody is
// left to notice it: quitting cancels, and a command that ignores the
// interrupt goes on holding the port or the lock the next attempt needs,
// because the kill that would have followed was a timer inside the process
// that just exited.
func TestQuittingStopsACommandThatIgnoresTheInterrupt(t *testing.T) {
	needShell(t)
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	marker := filepath.Join(dir, "still-alive")

	// The command's own life, long beside the drain's bound, so a drain that
	// waited for the command instead of killing it shows as one that took
	// this long, on any host.
	const commandLife = 20 * time.Second
	lines := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Ignoring the interrupt is the whole case: asking it to stop does
		// nothing, so only the kill behind the grace ends it, and it leaves
		// evidence behind if it survives. It names its group once the trap
		// is in place, since a drain before that stops it on the interrupt
		// and proves nothing.
		RunCaptureTail(context.Background(), "trap '' INT; echo shell $$; sleep 20; touch "+marker, sendLine(lines))
	}()

	pgid := awaitGroup(t, lines)
	armed := time.Now()
	StopCaptured()
	if took := time.Since(armed); took >= commandLife {
		t.Errorf("the drain took %s, as long as the command lives; a quit is bounded at %s", took.Round(time.Millisecond), killGrace)
	}

	select {
	case <-done:
	case <-time.After(waitDelay + 10*time.Second):
		t.Fatal("the runner did not return after the drain")
	}

	// The group being empty is the fact: nothing in it can write the marker
	// any more, so its absence then is the answer rather than a guess.
	awaitGroupGone(t, pgid)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a command that ignores the interrupt outlived the session that started it")
	}
}

// awaitGroup reads the line a fixture shell prints as `shell $$` and returns
// the process group that shell leads, which is the group the runner made for
// the command.
func awaitGroup(t *testing.T, lines <-chan string) int {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case line := <-lines:
			pid, ok := strings.CutPrefix(line, "shell ")
			if !ok {
				continue
			}
			n, err := strconv.Atoi(pid)
			if err != nil {
				t.Fatalf("the fixture named its shell as %q", line)
			}
			pgid, err := syscall.Getpgid(n)
			if err != nil {
				t.Fatalf("the fixture's shell %d has no group: %v", n, err)
			}
			return pgid
		case <-timeout:
			t.Fatal("the command never named its shell")
		}
	}
}

// awaitGroupGone blocks until no process is left in the group. Its bound is
// longer than any fixture here lives, so a survivor finishes its work — and
// leaves its marker — before the wait gives up, rather than being missed.
func awaitGroupGone(t *testing.T, pgid int) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(60 * time.Second)
	for syscall.Kill(-pgid, 0) == nil {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("process group %d is still there", pgid)
		}
	}
}

// awaitLine blocks until the command under test prints want, which is how a
// fixture says it has reached the state the test is about. Waiting on the
// fixture rather than on a timer is what keeps a loaded machine from signalling
// a shell that has not got that far yet.
func awaitLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case line := <-lines:
			if line == want {
				return
			}
		case <-timeout:
			t.Fatalf("the command never printed %q", want)
		}
	}
}

// sendLine hands each line the command prints to lines, dropping what does not
// fit: the writer calling it is the one draining the command's output, and a
// test that has stopped reading must not stall it.
func sendLine(lines chan<- string) func(string) {
	return func(line string) {
		select {
		case lines <- line:
		default:
		}
	}
}

// The pid a pending kill names is the group's only while the command is
// there. Once the wait has returned it has been reaped and the machine is
// free to hand that number to something else, so the kill must not be sent.
func TestTheKillIsNotSentOnceTheWaitHasCompleted(t *testing.T) {
	cmd, waited := standInGroup(t)

	exited := make(chan struct{})
	close(exited)
	killAfterGrace(cmd.Process.Pid, exited, 10*time.Millisecond)

	select {
	case <-waited:
		t.Fatal("a group whose wait had already returned was signalled anyway")
	case <-time.After(500 * time.Millisecond):
	}
}

// The other half of the same guard: a group that really is still there is
// still killed when its grace is up.
func TestTheKillArrivesWhileTheGroupIsStillThere(t *testing.T) {
	cmd, waited := standInGroup(t)

	killAfterGrace(cmd.Process.Pid, make(chan struct{}), 10*time.Millisecond)

	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		t.Fatal("a group that ignored the interrupt outlived its grace")
	}
}

// standInGroup spawns a process group that will not end on its own, and the
// wait that reports when it has. It stands in for a captured command in the
// tests above, which are about the signal and not about the shell.
func standInGroup(t *testing.T) (*exec.Cmd, chan error) {
	t.Helper()
	needShell(t)
	cmd := exec.Command("sh", "-c", "sleep 30")
	cmd.SysProcAttr = sysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cannot spawn a stand-in group: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd, waited
}

// A command the ceiling handed to the supervisor belongs to the supervisor:
// the taker decides when it stops, so the drain must leave it where it is.
func TestAHandedOverCommandIsNotDrained(t *testing.T) {
	needShell(t)
	sup := withSupervisor(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, _ := RunCapture(ctx, "echo listening; sleep 30")
	if !strings.Contains(out, "moved to the background") {
		t.Fatalf("the ceiling did not hand the command over: %q", out)
	}

	// The drain signals only what is on the live list, so a command missing
	// from it is one the drain cannot reach: that is the fact, read before
	// the drain rather than inferred from a pause after it.
	liveMu.Lock()
	still := len(live)
	liveMu.Unlock()
	if still != 0 {
		t.Fatalf("a handed-over command is still on the drain's list (%d live)", still)
	}
	StopCaptured()

	if list := sup.List(); !strings.Contains(list, "running") {
		t.Errorf("the drain stopped a command it had already handed on:\n%s", list)
	}
}
