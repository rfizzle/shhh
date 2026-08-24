package cli

import (
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/sandbox"
)

// openQualityGate builds the session's quality-gate runner (S-067): suites
// come from the workspace's trusted .shhh/quality.json, checks run contained
// with a read-only workspace when a mechanism is available (S-062), and full
// check output lands in the evidence store (S-064) when one is open.
func openQualityGate(cfg config.Config, red *evidence.Reducer) *quality.Runner {
	ws, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: quality gate unavailable: %v\n", err)
		return nil
	}
	r := &quality.Runner{Workspace: ws}
	if red != nil {
		r.Evidence = red.Store().Put
	}
	if avail := sandbox.Detect(); avail.OK {
		if policy, err := sandboxPolicy(cfg); err == nil {
			r.Mechanism = avail.Mechanism
			r.Wrap = func(argv []string, allowWrite bool) ([]string, error) {
				p := policy
				p.ReadOnlyWorkspace = !allowWrite
				return sandbox.WrapArgv(avail, p, argv)
			}
		}
	}
	return r
}

// gateManager backs the /gate slash command: "run [suite]" starts a suite in
// the background, "result" reports the latest verdict with staleness.
func gateManager(r *quality.Runner) func(args []string) string {
	return func(args []string) string {
		switch {
		case len(args) >= 1 && len(args) <= 2 && args[0] == "run":
			suite := ""
			if len(args) == 2 {
				suite = args[1]
			}
			return r.Start(suite)
		case len(args) == 1 && args[0] == "result":
			return r.Status()
		}
		return "Usage: /gate run [suite] | /gate result"
	}
}
