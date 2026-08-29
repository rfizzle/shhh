package cli

import (
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/process"
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
