//go:build !windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	lines := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A child that outlives its parent shell unless the group is
		// signalled, and that leaves evidence behind if it does. It says so
		// once it is running, which is the moment there is a child to leave
		// behind.
		RunCaptureTail(ctx, "sh -c 'echo started; sleep 5; touch "+marker+"' & sleep 5", sendLine(lines))
	}()

	awaitLine(t, lines, "started")
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

	commandLife := 5 * time.Second
	lines := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Ignoring the interrupt is the whole case: asking it to stop does
		// nothing, so only the kill behind the grace ends it, and it leaves
		// evidence behind if it survives. It says so once the trap is in
		// place, since a drain before that stops it on the interrupt and
		// proves nothing.
		RunCaptureTail(context.Background(), "trap '' INT; echo armed; sleep 5; touch "+marker, sendLine(lines))
	}()

	awaitLine(t, lines, "armed")
	armed := time.Now()
	StopCaptured()
	if took := time.Since(armed); took > killGrace+time.Second {
		t.Errorf("the drain took %s; a quit is bounded at %s", took.Round(time.Millisecond), killGrace)
	}

	select {
	case <-done:
	case <-time.After(waitDelay + 5*time.Second):
		t.Fatal("the runner did not return after the drain")
	}

	// Past when the command would have written, had it survived.
	time.Sleep(time.Until(armed.Add(commandLife + 500*time.Millisecond)))
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a command that ignores the interrupt outlived the session that started it")
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

	StopCaptured()

	// The supervisor learns a process has gone through its own wait, so give
	// that a moment to have happened before asking it.
	time.Sleep(300 * time.Millisecond)
	if list := sup.List(); !strings.Contains(list, "running") {
		t.Errorf("the drain stopped a command it had already handed on:\n%s", list)
	}
}
