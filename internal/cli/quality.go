package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/todo"
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
	// Beside the verdict's hook, which recordGateVerdicts sets once there is
	// a session to report to: the ledger needs none, so every surface built
	// on this runner — a served session's among them — counts its flakes
	// from the first run, and the session is named where one is recording.
	recordGateFlakes(r, todo.Root(ws), func() string { return "" })
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
		if policy, err := sandboxPolicy(cfg, sc.Beyond()...); err == nil {
			policy.PrivateGoCache = true
			r.Mechanism = avail.Mechanism
			r.Wrap = func(argv []string, allowWrite bool) ([]string, error) {
				p := policy
				p.ReadOnlyWorkspace = !allowWrite
				return sandbox.WrapArgv(avail, p, argv)
			}
			// A generator is a check that writes, and writes in the tree it
			// was pointed at — a copy of the checkout, or a writer's copy —
			// so the same policy is stood in that tree instead.
			r.WrapIn = func(dir string, argv []string) ([]string, error) {
				p := policy
				p.Workspace, p.ReadOnlyWorkspace = dir, false
				return sandbox.WrapArgv(avail, p, argv)
			}
		}
	}
	return r
}

// scopeGateToWrites scopes the gate's scoped checks to the files a run wrote
// rather than the dirty tree, so a turn that has already committed its work
// still gets the fast first pass instead of the whole module. written is read
// at each run; one that names nothing leaves the dirty tree as the scope,
// which is the runner's own default and is read here because a source that
// returns nothing reads to the runner as no files at all.
//
// A path a tool was given may be absolute, and the runner wants them from the
// workspace; one outside it is not the workspace's to scope.
func scopeGateToWrites(gate *quality.Runner, written func() []string) {
	if gate == nil || written == nil {
		return
	}
	ws := gate.Workspace
	gate.Changed = func() []string {
		var out []string
		for _, p := range written() {
			if filepath.IsAbs(p) {
				rel, err := filepath.Rel(ws, p)
				if err != nil {
					continue
				}
				p = rel
			}
			p = filepath.ToSlash(filepath.Clean(p))
			if p == ".." || strings.HasPrefix(p, "../") {
				continue
			}
			out = append(out, p)
		}
		if len(out) == 0 {
			return quality.DirtyChanged(ws)
		}
		return out
	}
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
	// The ledger is written whether or not the session records — it is the
	// checkout's count, not the session's, and openQualityGate already set
	// it — so all this adds is the session's name on the row.
	recordGateFlakes(gate, todo.Root(gate.Workspace), func() string { return recordedSession(rec) })
}

// recordGateFlakes points a gate runner at the checkout's flake ledger, under
// root — the checkout's, which a lane's runner does not stand in — so every
// runner that can rerun a check counts its flakes in the same rows. session
// names the session a flake happened in, read when it happens because the
// session's record row is opened after the runner is built.
//
// It is one function for the reason recordGateVerdicts is: a runner built
// without it reruns and calls a check flaked, and the count the next run
// reads says it never happened.
//
// Nothing it does can reach the verdict. A store that will not open or a
// write that fails is dropped here, and the check says it flaked without
// saying how often: the gate's reading of the code does not depend on the
// bookkeeping about it, and an error written to the terminal would land on
// top of whatever surface is drawing.
// See docs/capabilities/testing.md#a-flake-is-counted-where-it-happened.
func recordGateFlakes(gate *quality.Runner, root string, session func() string) {
	if gate == nil || root == "" {
		return
	}
	gate.Flakes = func(f quality.Flake) int {
		db, err := openStore()
		if err != nil {
			return 0
		}
		defer db.Close()
		before, err := db.RecordFlake(storage.Flake{
			Root: root, Suite: f.Suite, Check: f.Check, Command: f.Command,
			FirstExit: f.FirstExit, LastSession: session(),
		}, time.Now())
		if err != nil {
			return 0
		}
		return before
	}
}

// recordedSession is a session as the ledger names it: its record's row id,
// which `shhh observe session` opens, or nothing where nothing is recorded.
func recordedSession(rec *observeRecorder) string {
	if id := rec.sessionID(); id > 0 {
		return strconv.FormatInt(id, 10)
	}
	return ""
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
		case len(args) == 1 && args[0] == "flakes":
			return flakesText(gateFlakes(r)())
		}
		return "usage: /gate run [suite] · /gate result · /gate flakes · /gate on · /gate off"
	}
}

// gateFlakes reads the flake ledger of the checkout the runner stands in,
// which /gate flakes lists.
func gateFlakes(r *quality.Runner) func() ([]storage.Flake, error) {
	root := todo.Root(r.Workspace)
	return func() ([]storage.Flake, error) {
		db, err := openStore()
		if err != nil {
			return nil, err
		}
		defer db.Close()
		return db.FlakesFor(root)
	}
}

// flakesText is the ledger as one answer, for a surface with no screen to
// open it on: a line a check, the most recent flake first.
func flakesText(flakes []storage.Flake, err error) string {
	switch {
	case err != nil:
		return "the flake ledger could not be read: " + err.Error()
	case len(flakes) == 0:
		return "no check has flaked in this checkout"
	}
	lines := []string{"checks that failed and passed on their rerun in this checkout:"}
	for _, f := range flakes {
		lines = append(lines, fmt.Sprintf("  %s · %s · %s · last %s — %s",
			f.Check, f.Suite, countOf(f.Seen, "time", "times"), f.LastAt.Local().Format("2006-01-02 15:04"), f.Command))
	}
	return strings.Join(lines, "\n")
}
