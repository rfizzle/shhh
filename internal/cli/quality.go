package cli

import (
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
)

// openQualityGate builds the session's quality-gate runner: suites
// come from the workspace's trusted .shhh/quality.json, checks run contained
// with a read-only workspace when a mechanism is available, and full
// check output lands in the evidence store when one is open.
func openQualityGate(cfg config.Config, red *evidence.Reducer, sc *scope.Scope) *quality.Runner {
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
		if policy, err := sandboxPolicy(cfg, sc.Dirs()...); err == nil {
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

// recordGateVerdicts points a session's gate at its record, so every run the
// gate completes lands beside the rest of what the session did.
//
// It is one function rather than a line at each surface because the gate is
// wired in two places today and the record is the sort of thing a third
// would forget: a surface that runs the gate and records nothing produces a
// pass rate over the surfaces that remembered, which is the shape of number
// the record exists to avoid.
// See docs/capabilities/sessions-and-memory.md#whether-it-worked.
func recordGateVerdicts(gate *quality.Runner, rec *observeRecorder) {
	if gate == nil {
		return
	}
	// A session that is not recording leaves the hook nil, which the runner
	// reads as "record nothing".
	gate.Observe = observe.GateHook(rec.observer())
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
