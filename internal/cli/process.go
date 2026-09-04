package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/runner"
)

// openProcessSupervisor builds the session's long-running process supervisor
// , rooted at the current directory, with full logs retained through
// the evidence store when one is open. Failure disables the process tool for
// the session with a warning instead of blocking it.
func openProcessSupervisor(red *evidence.Reducer) *process.Supervisor {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: process supervisor unavailable: %v\n", err)
		return nil
	}
	var store process.StoreFunc
	if red != nil {
		store = red.Store().Put
	}
	sup, err := process.New(cwd, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: process supervisor unavailable: %v\n", err)
		return nil
	}
	// Where a session has a supervisor, a command that is still printing when
	// it reaches its ceiling becomes one of its processes instead of being
	// killed. It is installed here rather than beside each runner because
	// every command path leads to the same place, and a ceiling that
	// backgrounded on one and killed on another would be a dev server that
	// lives or dies by whether containment was available on the machine.
	// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.
	runner.SetAdopter(func(h runner.Handover) (string, io.Writer, error) {
		return sup.Adopt(process.Adoption{
			Command: h.Command,
			PID:     h.PID,
			Started: h.Started,
			Wait:    h.Wait,
		})
	})
	return sup
}

// processManager backs the /ps slash command: the session's process list.
func processManager(sup *process.Supervisor) func(args []string) string {
	return func(args []string) string {
		if len(args) > 0 {
			return "Usage: /ps — lists the processes this session owns"
		}
		return sup.List()
	}
}
