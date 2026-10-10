package runner

// An attached command: one whose program reaches the work through a stream.
//
// A sandbox container's command is an engine's exec, and the engine forwards
// no signal, so the process this runner holds is a client and the work is in
// the container on the other end of it. What does cross is the client's
// stdin: the helper at the far end watches it, and a hang-up there is its
// cue to stop the command. So an attached command is given a stdin of its
// own, held open for as long as the command should run, and the stop starts
// by closing it — before the client is signalled, so the far end starts its
// own interrupt and grace at once rather than when the client dies. A
// command that ends by itself has it closed once its wait returns.
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

	"github.com/rfizzle/shhh/internal/tools"
)

// holdStdin gives the command a stdin whose write end the group keeps, and
// returns the read end for the caller to close once the command has started.
func holdStdin(g *group) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	g.cmd.Stdin = r
	g.stdin = w
	return r, nil
}

// RunCaptureArgvInAttached is RunCaptureArgvInResult for an attached argv:
// the same capture, ceiling and drain, with a stdin held open until the
// command is to stop.
func RunCaptureArgvInAttached(ctx context.Context, dir, command string, argv []string) tools.ExecResult {
	return capture(ctx, dir, command, argv, spawnAttached, nil)
}

// RunCaptureArgvTailAttached is RunCaptureArgvTailResult for an attached
// argv, reporting each completed line to onLine as it appears.
func RunCaptureArgvTailAttached(ctx context.Context, command string, argv []string, onLine func(string)) tools.ExecResult {
	return capture(ctx, "", command, argv, spawnAttached, onLine)
}
