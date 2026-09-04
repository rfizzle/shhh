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
// check output lands in the evidence store when one is open, scrubbed of
// the session's secrets on the way in.
func openQualityGate(cfg config.Config, red *evidence.Reducer, sc *scope.Scope) *quality.Runner {
	// The suites are command text out of a file that arrived with the
	// clone, and the tool runs them without an approval. So an untrusted
	// checkout gets no runner and no quality_gate tool at all, rather than a
	// tool that refuses when the model calls it: a registered tool is a
	// promise, and the withheld list is where this is reported.
	if !projectTrust().Allows() {
		return nil
	}
	ws, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: quality gate unavailable: %v\n", err)
		return nil
	}
	r := &quality.Runner{Workspace: ws}
	if red != nil {
		r.Evidence = red.Store().Put
	}
	// The gate is the third writer into the evidence store, and the only
	// one that reaches it without a tool result going by: a check's whole
	// output is stored under an id of its own, and the excerpt of it that
	// /gate result prints never passes the executor chain either. It takes
	// the scrub the reducer was given rather than a copy of the vault's,
	// because the toolset has to be complete before the session opens its
	// secrets — there is no vault yet at this point in the build, and this
	// method value reads the scrub at the moment a check's output is kept.
	// A session whose store would not open has no reducer to read it from
	// and keeps no durable copy either; the method is safe on the nil one.
	r.SetScrub(red.Scrub)
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

// onCloseGate is the workspace's on-close setting, read fresh off the
// trusted config the way a run reads it: the suite to run as a turn closes
// over work it changed, and how many failing verdicts are handed back before
// the turn ends whatever the last one was.
//
// A workspace with no config, or one that will not parse, is not an error
// here. The gate is optional — this repository ran without the tool at all
// until the file was added — and a surface that announced a broken config at
// every turn close would be reporting it to whoever is least able to fix it:
// nobody is watching an unattended run. It stays a clean no-op, and the
// broken file is reported where it is asked for, by the run that blocks on
// it.
func onCloseGate(r *quality.Runner) (suite string, retries int, ok bool) {
	if r == nil {
		return "", 0, false
	}
	cfg, err := quality.LoadConfig(r.Workspace)
	if err != nil || cfg.OnClose == "" {
		return "", 0, false
	}
	return cfg.OnClose, cfg.CloseRetries(), true
}

// gateManager backs the /gate slash command: "run [suite]" starts a suite in
// the background, "result" reports the latest verdict with staleness. The
// on-close toggle is answered by the session before the command reaches
// here, because what it switches is the session's own state and this runner
// has none; the usage line still names it, since a usage line that lists
// three of a command's four verbs is worse than none.
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
		return "Usage: /gate run [suite] | /gate result | /gate on | /gate off"
	}
}
