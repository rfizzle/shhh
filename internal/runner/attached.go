package runner

// An attached command: one whose program reaches the work through a stream.
//
// A sandbox container's command is an engine's exec, and the engine forwards
// no signal, so the process this runner holds is a client and the work is in
// the container on the other end of it. What does cross is the client's
// stdin: the helper at the far end watches it, and a hang-up there is its
// cue to stop the command. So an attached command is given a stdin of its
// own, held open for as long as the command should run, and the stop starts
// by closing it. A command that ends by itself has it closed once its wait
// returns.
//
// The client is not signalled when the stop starts. An engine ends the
// exec's one process — the helper, not the group it leads — when its client
// goes, so a client stopped at once can take the helper with it before the
// helper has finished stopping the command, and what the command started is
// left running in the container with nothing watching it. So the stop waits:
// the helper exits once its group is gone, which ends the exec and so the
// client, and a client that ends inside the far end's grace is a stop that
// arrived. A client still there when that grace is up is one whose hang-up
// did not arrive — an engine may hold the stream open — and the second leg
// runs: one more exec, through the same engine, of the helper's own stop
// against the group it recorded. Only then is the client killed. Every step
// is bounded, so a cancel returns within the far end's grace, the second
// exec's own timeout and the kill, whatever the engine does.
//
// The read end goes on the command as an *os.File, so os/exec hands it over
// as the descriptor and starts no copying goroutine: nothing is written to
// it, and a goroutine waiting to would only hold the wait for WaitDelay.
//
// The plain forms are not attached and keep the empty stdin they always had:
// a host command reading its stdin finds nothing, rather than waiting on a
// stream nobody writes.
// See docs/capabilities/containment.md#a-cancelled-command-takes-its-children-with-it.

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/rfizzle/shhh/internal/tools"
)

const (
	// farGrace is how long a hung-up client is left to end by itself: the
	// far end's own grace, which is this package's, and a margin for the
	// helper to exit and the engine to end the exec.
	farGrace = killGrace + 2*time.Second
	// stopExecTimeout bounds the second exec: the far end's grace again,
	// since the helper it runs stops the group with the same sequence, and
	// a margin for the engine to start it.
	stopExecTimeout = killGrace + 5*time.Second
	// attachedWaitDelay is os/exec's own kill of the client, after the
	// whole of the stop above; it is the backstop and never the stop.
	attachedWaitDelay = farGrace + stopExecTimeout + waitDelay
)

// holdStdin gives the command a stdin whose write end the group keeps, and
// returns the read end for the caller to close once the command has started.
// It makes the command's stop the attached one, with stop as its second
// exec; a nil stop has none, and the client is killed after the far end's
// grace.
func holdStdin(g *group, stop []string) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	g.cmd.Stdin = r
	g.stdin = w
	g.cmd.WaitDelay = attachedWaitDelay
	g.cmd.Cancel = func() error {
		g.hangUp()
		go stopAttached(g, stop)
		return nil
	}
	return r, nil
}

// stopAttached is the attached stop after the hang-up: wait for the client
// to end by itself, then the second exec, then the kill. It runs beside the
// wait rather than in it, since the client ending is the wait returning.
func stopAttached(g *group, stop []string) {
	if awaitExit(g, farGrace) {
		return
	}
	if len(stop) > 0 {
		runStopExec(stop)
	}
	killGroup(g)
}

// awaitExit reports whether the command's wait returned inside the window.
func awaitExit(g *group, window time.Duration) bool {
	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-g.exited:
		return true
	case <-timer.C:
		return false
	}
}

// runStopExec runs the second exec to its end or its timeout. What it says
// is not the command's output, and a failure of it leaves nothing to do but
// the kill that follows.
func runStopExec(stop []string) {
	ctx, cancel := context.WithTimeout(context.Background(), stopExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, stop[0], stop[1:]...)
	cmd.Env = Environ()
	cmd.WaitDelay = reapGrace
	_ = RunGrouped(cmd)
}

// RunCaptureArgvInAttached is RunCaptureArgvInResult for an attached argv:
// the same capture, ceiling and drain, with a stdin held open until the
// command is to stop, and stop the second exec that reaches the far end
// when the hang-up did not.
func RunCaptureArgvInAttached(ctx context.Context, dir, command string, argv, stop []string) tools.ExecResult {
	return capture(ctx, dir, command, argv, spawnAttached, stop, nil)
}

// RunCaptureArgvTailAttached is RunCaptureArgvTailResult for an attached
// argv, reporting each completed line to onLine as it appears.
func RunCaptureArgvTailAttached(ctx context.Context, command string, argv, stop []string, onLine func(string)) tools.ExecResult {
	return capture(ctx, "", command, argv, spawnAttached, stop, onLine)
}

// RunGroupedAttached is RunGrouped for an attached argv: the command leads a
// group of its own, holds its stdin open until it is to stop, and is stopped
// the attached way — hang-up, the wait for the far end, the second exec stop
// names, then the kill. A caller that spawns its own commands rather than
// capturing them, as the quality gate's checks are, stops a command in a
// container through it. cmd.Stdin and cmd.WaitDelay are the stop's and are
// set here.
func RunGroupedAttached(cmd *exec.Cmd, stop []string) error {
	g := &group{cmd: cmd, exited: make(chan struct{})}
	cmd.SysProcAttr = sysProcAttr()
	held, err := holdStdin(g, stop)
	if err != nil {
		return err
	}
	err = g.start()
	// The child has its own copy now; this one would keep the stream open
	// from this end after the write end is closed.
	_ = held.Close()
	if err != nil {
		g.hangUp()
		return err
	}
	return g.wait()
}
