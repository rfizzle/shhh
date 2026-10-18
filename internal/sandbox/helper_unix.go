//go:build !windows

package sandbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rfizzle/shhh/internal/runner"
	"golang.org/x/sys/unix"
)

// RunExec is the helper: it runs the argv after `--` in a process group of
// its own and returns the status the child left — its exit code, or 128 plus
// the signal that ended it, which is what a shell reports for one.
//
// The group is stopped, interrupt then grace then freeze-and-kill, when the
// helper's stdin hangs up (unless --hangup=ignore), when --timeout runs out,
// or when the helper itself is sent an interrupt or a termination. The
// sequence is the runner's own, imported rather than copied.
//
// With --id the child's group is recorded under that token while it runs,
// and `--stop=<token>` — a second exec, the second leg of the stop — runs
// the same sequence against a recorded group (helperstop_unix.go).
//
// args are the helper's flags, "--", and the command's argv.
func RunExec(args []string) int {
	f, err := parseExecArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shhh: sandbox exec: %v\n", err)
		return 126
	}
	if f.version {
		fmt.Println(helperBanner + HelperVersion)
		return 0
	}
	if f.stop != "" {
		return stopRecorded(f.stop)
	}

	stop := make(chan struct{})
	var once sync.Once
	halt := func() { once.Do(func() { close(stop) }) }

	// The helper outlives its child so it can hand back the child's status,
	// so the signals that would end it are caught. A hang-up of a terminal
	// arrives as SIGHUP rather than on the descriptor, and is the same
	// hang-up; an interrupt or a termination sent to the helper is somebody
	// asking for the command to stop, whatever --hangup says.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		for sig := range sigs {
			debugf("%v seen", sig)
			if sig == syscall.SIGHUP && f.hangup == hangupIgnore {
				continue
			}
			halt()
		}
	}()
	if f.hangup == hangupStop {
		go func() {
			if watchHangup(int(os.Stdin.Fd())) {
				debugf("hang-up seen")
				halt()
			}
		}()
	}
	if f.timeout > 0 {
		t := time.AfterFunc(f.timeout, halt)
		defer t.Stop()
	}

	cmd := exec.Command(f.argv[0], f.argv[1:]...)
	if f.stdin == stdinInherit {
		cmd.Stdin = os.Stdin
	}
	// Wrapped so that os/exec copies the output through a pipe of its own
	// rather than handing the child the descriptor: the wait then lasts as
	// long as anything the command started still holds the output, and a
	// group with a member left in it is a group the stop can still reach.
	cmd.Stdout = struct{ io.Writer }{os.Stdout}
	cmd.Stderr = struct{ io.Writer }{os.Stderr}
	var recorded func()
	started := func(pgid int) {
		if f.id != "" {
			recorded = recordGroup(f.id, pgid)
		}
	}
	err = runner.RunGroupUntil(cmd, stop, started)
	if recorded != nil {
		// The wait has returned: from here the number is not this
		// command's, and a second exec must find nothing to signal.
		recorded()
	}
	debugf("the command has ended")
	return exitStatus(err)
}

// debugEnv makes the helper say on its stderr what it saw — a hang-up, a
// signal, a stop — for a person diagnosing an engine. Nothing reads it.
const debugEnv = "SHHH_SANDBOX_EXEC_DEBUG"

func debugf(format string, args ...any) {
	if os.Getenv(debugEnv) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "shhh: sandbox exec: "+format+"\n", args...)
}

// exitStatus is the status the helper exits with for the child's ending.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exit.ExitCode()
	}
	// The program could not be started: the status a shell gives a command
	// it could not find, with the reason on stderr.
	fmt.Fprintf(os.Stderr, "shhh: sandbox exec: %v\n", err)
	return 127
}

// watchHangup blocks until fd hangs up and reports true, or reports false
// when there is nothing to watch. It never reads: with --stdin=inherit the
// descriptor's bytes are the child's, and a read here would take them. Where
// a poll that asks for no input events is woken by the hang-up and an error
// alone (Linux), a stream that is merely quiet costs nothing. Where it is
// not (macOS reports nothing for a pipe polled that way), the poll asks for
// input too, and a wake with bytes waiting is told from end-of-file by how
// many bytes are queued, still without reading them.
func watchHangup(fd int) bool {
	fds := []unix.PollFd{{Fd: int32(fd), Events: pollHangupEvents | pollInputEvents}}
	for {
		_, err := unix.Poll(fds, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || fds[0].Revents&unix.POLLNVAL != 0 {
			return false
		}
		if fds[0].Revents&(unix.POLLHUP|unix.POLLERR|pollHangupEvents) != 0 {
			return true
		}
		if fds[0].Revents&unix.POLLIN != 0 {
			n, err := queuedInput(fd)
			if err != nil || n == 0 {
				return true
			}
			// Bytes the child has not read yet keep the poll ready; look
			// again after a pause rather than spinning on them.
			time.Sleep(pendingInputBackoff)
		}
	}
}

// pendingInputBackoff is the pause between looks while unread input keeps
// the descriptor readable.
const pendingInputBackoff = 50 * time.Millisecond
