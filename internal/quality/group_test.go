//go:build !windows

package quality

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A check that runs past its timeout is killed, and so is everything it
// started: the slot goes back the moment the check is reported, and a build
// still running under it would be work nobody is counting. The script starts
// a sleeping child, writes the child's pid where the test can find it, and
// waits on it.
func TestRun_ATimedOutCheckTakesItsChildrenWithIt(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, `{"suites": {"default": {"timeout_seconds": 1, "checks": [
		{"name": "slow", "exe": "sh", "args": ["-c", "sleep 30 & echo $! > child.pid; wait"]}
	]}}}`)
	pidFile := filepath.Join(ws, "child.pid")
	// Whatever the verdict, nothing this test started is left on the host.
	// The pid is signalled alone — never a group — and only when it is a
	// real process id.
	t.Cleanup(func() {
		if pid, ok := readChildPID(pidFile); ok {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	r := &Runner{Workspace: ws}
	res := mustRun(t, r, "default")
	if !res.Checks[0].TimedOut {
		t.Fatalf("check = %+v, want a timeout", res.Checks[0])
	}
	pid, ok := readChildPID(pidFile)
	if !ok {
		t.Fatal("the check never recorded its child")
	}
	// The kill is delivered before the wait returns, but an orphan is reaped
	// by init on its own schedule, so give the table a moment to catch up.
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d the timed-out check started is still running", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// readChildPID reads the pid the script wrote, refusing anything that would
// make a signal mean more than one process.
func readChildPID(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return 0, false
	}
	return pid, true
}

// alive reports whether pid names a process: signal 0 checks without
// delivering anything, and EPERM is a process that exists and is not ours.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
