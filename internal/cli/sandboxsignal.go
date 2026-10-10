package cli

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rfizzle/shhh/internal/runner"
)

// sandboxSignalBound is how long a hang-up or a termination waits for the
// session's container to be removed before it lets the process end: the
// commands attached to it are drained first, inside the runner's own grace,
// and the removal is an engine call that normally takes a moment. A removal
// that is slower than this is left to the reaper.
const sandboxSignalBound = 10 * time.Second

// closeOnSignal removes a sandbox session's container when one of sigs
// arrives while the screen is up. The screen turns SIGTERM into its ordinary
// quit and leaves SIGHUP to the runtime's default, which ends the process
// with nothing deferred run, so the container would wait for the reaper. The
// attached commands are stopped first, the way a quit stops them, then the
// container is removed, and the whole is bounded by sandboxSignalBound.
//
// Once the container is gone a hang-up is handed back by reraise, so the
// default ends the process; a termination is not, because the screen is
// already carrying out its own quit, autosave and terminal restore included,
// and ending the process under it would cut that short. The returned stop
// ends the watch.
// See docs/capabilities/containment.md#a-session-can-run-in-the-sandbox.
func closeOnSignal(sigs <-chan os.Signal, drain, closeContainer func(), reraise func(os.Signal)) (stop func()) {
	quit := make(chan struct{})
	go func() {
		select {
		case <-quit:
		case s := <-sigs:
			done := make(chan struct{})
			go func() {
				defer close(done)
				drain()
				closeContainer()
			}()
			select {
			case <-done:
			case <-time.After(sandboxSignalBound):
			}
			if s == syscall.SIGHUP {
				if s == syscall.SIGHUP {
					reraise(s)
				}
			}
		}
	}()
	return func() { close(quit) }
}

// watchSandboxSignals is closeOnSignal over the process's own SIGHUP and
// SIGTERM. It is the one place that touches the real signals; the tests drive
// closeOnSignal with a channel.
func watchSandboxSignals(closeContainer func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM)
	endWatch := closeOnSignal(ch, runner.StopCaptured, closeContainer, func(s os.Signal) {
		signal.Stop(ch)
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(s)
		}
	})
	return func() {
		endWatch()
		signal.Stop(ch)
	}
}
