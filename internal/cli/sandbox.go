package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// sandboxPolicy builds the session containment policy: workspace is
// the current directory, profile and extensions come from config, and the
// working scope's directories join the write grants — a directory the
// user has said is part of the work is one contained commands may write to,
// which is the whole point of having said so.
func sandboxPolicy(cfg config.Config, scopeDirs ...string) (sandbox.Policy, error) {
	profile, err := sandbox.ParseProfile(cfg.Sandbox.Profile)
	if err != nil {
		return sandbox.Policy{}, fmt.Errorf("config sandbox.profile: %w", err)
	}
	ws, err := os.Getwd()
	if err != nil {
		return sandbox.Policy{}, err
	}
	write := append(append([]string{}, cfg.Sandbox.WriteExtra...), scopeDirs...)
	return sandbox.Policy{
		Workspace:  ws,
		Profile:    profile,
		DenyExtra:  cfg.Sandbox.DenyExtra,
		WriteExtra: write,
		// The environment goes in with the paths, from the one place that
		// builds a policy, because every command path resolves through here:
		// a sub-agent's command, the quality gate's check and a process
		// start would each otherwise decide separately what a contained
		// command may carry, which is how they come to disagree. What a
		// command gets is the session's own set — the vault's values
		// included — narrowed by the sandbox's allowlist, and the vault's
		// names are on that allowlist because the person named them.
		Env:         runner.Environ(),
		SecretNames: runner.SessionEnvNames(),
	}, nil
}

// logUnconfined writes down a session whose commands are not contained. The
// status line and the approval card both say so at the time, and neither is
// there afterwards: a run left alone overnight, or a headless one in CI, ends
// with the profile named in a config file and nothing anywhere saying it was
// never in force
// (docs/capabilities/configuration.md#a-failure-is-written-down).
//
// What is written is the profile that was asked for, which is a word from a
// closed set. The reason containment is unavailable is a sentence about this
// host — often naming the binary it looked for and where — and it is on the
// status line and in `shhh doctor`, where it is read by the person who can
// act on it rather than accumulated in a file two sessions share.
func logUnconfined(profile sandbox.Profile) {
	logs.Logger().Warn("commands run unconfined", "profile", string(profile))
}

// withRequiredContainment folds --require-sandbox into the resolved config,
// which is where every command path reads it afterwards: the session's own,
// a sub-agent's, and the headless approver's. It is the config rather than a
// value passed down because they are three different builders, and a flag
// carried to two of them is a flag the third silently ignores.
//
// The flag can only turn the setting on. There is no spelling of it that
// takes containment away, because a machine where nothing wraps a command is
// not a decision a command line should be able to make quietly.
func withRequiredContainment(cfg config.Config, flag bool) config.Config {
	cfg.Sandbox.Require = cfg.Sandbox.Require || flag
	return cfg
}

// uncontainedRefusal is what a session that requires containment answers an
// assistant command with on a host that has none: why nothing is containing
// it, and the doctor's own fix for this platform. It is the doctor's wording
// rather than a second one because the reader is being told to go and install
// something, and two spellings of that instruction are two things to keep
// true.
//
// It reads as a tool result because that is where it lands: the model is the
// one that asked, and it can say what it was going to do instead.
// See docs/capabilities/containment.md#containment-can-be-required.
func uncontainedRefusal(avail sandbox.Availability) string {
	var b strings.Builder
	b.WriteString("error: this session requires containment and no mechanism is in force: ")
	b.WriteString(avail.Detail)
	for _, line := range doctorSandbox(avail, "", runtime.GOOS).Fix {
		b.WriteString("\n  " + line)
	}
	return b.String()
}

// buildContainment assembles the containment setup agent sessions run
// commands through: a wrapped runner when a mechanism is available, plus the
// status line and doctor report either way. A wrap or spawn failure surfaces
// as the command's error result — a contained command never falls back to
// running bare. Session start also reconciles container-sandbox ownership
// records so crashed sessions' containers get reaped.
//
// The process supervisor is handed the same mechanism here rather than
// wiring its own: a start is the session's other way of spawning a command,
// and one command path deciding containment separately from the other is how
// they come to disagree. sup may be nil — a session without the process
// tool.
func buildContainment(cfg config.Config, sc *scope.Scope, sup *process.Supervisor) (chat.Containment, error) {
	// The policy is rebuilt per command rather than captured once: the
	// working scope grows mid-session, and a closure holding the
	// policy it was built with would keep refusing writes to a directory the
	// user has since granted.
	policyNow := func() (sandbox.Policy, error) { return sandboxPolicy(cfg, sc.Dirs()...) }
	policy, err := policyNow()
	if err != nil {
		return chat.Containment{}, err
	}
	reconcileOwnedSandboxes()
	avail := sandbox.Detect()
	c := chat.Containment{
		Report: sandbox.Report(avail, policy, runningProcesses(sup)),
		Manage: sandboxManage(cfg, sc, sup),
	}
	if !avail.OK {
		c.Status = "unconfined — " + avail.Detail
		c.Detail = avail.Detail
		// Nothing wraps the command, so nothing restricts what it reaches:
		// the approval card says so rather than reporting the profile's
		// answer, which is not in force.
		c.Network = true
		if cfg.Sandbox.Require {
			c.Refusal = uncontainedRefusal(avail)
		}
		logUnconfined(policy.Profile)
		return c, nil
	}
	c.Status = fmt.Sprintf("contained: %s (%s profile)", avail.Mechanism, policy.Profile)
	c.Mechanism, c.Profile, c.Required = avail.Mechanism, string(policy.Profile), cfg.Sandbox.Require
	c.Network = policy.Profile != sandbox.ProfileWorkspaceNetless
	wrap := func(command string) ([]string, error) {
		p, err := policyNow()
		if err != nil {
			return nil, err
		}
		return sandbox.Wrap(avail, p, command)
	}
	// A start runs the same argv the runner would have run, in a directory
	// the supervisor has already contained to the workspace, so the wrap is
	// WrapArgv with that directory as the policy's cwd — a mechanism that
	// chdirs uses it, and a policy resolved against shhh's own working
	// directory would put the process somewhere the model did not ask for.
	if sup != nil {
		sup.SetContainment(process.Containment{
			Mechanism: avail.Mechanism,
			Wrap: func(dir string, argv []string) ([]string, error) {
				p, err := policyNow()
				if err != nil {
					return nil, err
				}
				p.Cwd = dir
				return sandbox.WrapArgv(avail, p, argv)
			},
		})
	}
	c.Run = func(ctx context.Context, command string) (string, int) {
		argv, err := wrap(command)
		if err != nil {
			return "sandbox: " + err.Error(), -1
		}
		return runner.RunCaptureArgv(ctx, command, argv)
	}
	c.TailRun = func(ctx context.Context, command string, onLine func(string)) (string, int) {
		argv, err := wrap(command)
		if err != nil {
			return "sandbox: " + err.Error(), -1
		}
		return runner.RunCaptureArgvTail(ctx, command, argv, onLine)
	}
	return c, nil
}

// reconcileOwnedSandboxes reaps expired sandbox containers and drops records
// for vanished ones. Best-effort and quiet: it only does work when ownership
// records exist, and problems stay visible through /sandbox rather than
// blocking session start.
func reconcileOwnedSandboxes() {
	store, err := sandbox.OpenStore()
	if err != nil {
		return
	}
	if recs, err := store.List(); err != nil || len(recs) == 0 {
		return
	}
	sandbox.Reconcile(context.Background(), store, time.Now().UTC())
}

// containerSpec builds the sandbox-container spec from config: workspace is
// the current directory and the netless containment profile also removes the
// container's network.
func containerSpec(cfg config.Config) (sandbox.ContainerSpec, error) {
	profile, err := sandbox.ParseProfile(cfg.Sandbox.Profile)
	if err != nil {
		return sandbox.ContainerSpec{}, fmt.Errorf("config sandbox.profile: %w", err)
	}
	ws, err := os.Getwd()
	if err != nil {
		return sandbox.ContainerSpec{}, err
	}
	return sandbox.ContainerSpec{
		Image:     cfg.Sandbox.ContainerImage,
		Workspace: ws,
		Network:   profile != sandbox.ProfileWorkspaceNetless,
		Memory:    cfg.Sandbox.ContainerMemory,
		CPUs:      cfg.Sandbox.ContainerCPUs,
		PidsLimit: cfg.Sandbox.ContainerPids,
		TTL:       time.Duration(cfg.Sandbox.ContainerTTLHours) * time.Hour,
	}, nil
}

// startSandbox creates the disposable container behind `shhh code -p
// --sandbox` and returns the runner that execs agent commands into it plus
// the cleanup that destroys it. Any unverifiable requirement — no engine, an
// unpinned or unlisted image, or a required isolation level that cannot be
// met — fails creation; there is no silent downgrade to a weaker sandbox.
func startSandbox(ctx context.Context, cfg config.Config, env []string) (run func(context.Context, string) (string, int), cleanup func(), err error) {
	store, err := sandbox.OpenStore()
	if err != nil {
		return nil, nil, err
	}
	sandbox.Reconcile(ctx, store, time.Now().UTC())

	eng := sandbox.DetectEngine(cfg.Sandbox.ContainerEngine)
	required := sandbox.IsolationContainer
	if s := cfg.Sandbox.RequireIsolation; s != "" {
		configured, err := sandbox.ParseIsolation(s)
		if err != nil {
			return nil, nil, fmt.Errorf("config sandbox.require_isolation: %w", err)
		}
		if configured.Rank() > required.Rank() {
			required = configured
		}
	}
	if err := sandbox.VerifyIsolation(required, sandbox.Detect(), eng); err != nil {
		return nil, nil, err
	}

	spec, err := containerSpec(cfg)
	if err != nil {
		return nil, nil, err
	}
	c, err := sandbox.CreateContainer(ctx, eng, spec, cfg.Sandbox.ImageAllowlist, store)
	if err != nil {
		return nil, nil, err
	}

	run = func(ctx context.Context, command string) (string, int) {
		return runner.RunCaptureArgv(ctx, command, c.ExecArgv(command, env...))
	}
	cleanup = func() {
		// The parent ctx is usually done by cleanup time; destruction gets its
		// own bounded lifetime so the container never outlives the run by
		// accident (and the TTL reaper backstops a failure here).
		dctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := sandbox.DestroyContainer(dctx, eng.Path, store, c.Record); err != nil {
			fmt.Fprintf(os.Stderr, "warning: sandbox %s not destroyed: %v (TTL reaping will retry)\n", c.Record.ID, err)
		}
	}
	return run, cleanup, nil
}

// containerReport is the container-sandbox half of the doctor output: engine
// discovery, the isolation-level ladder, image policy, and owned containers.
func containerReport(cfg config.Config) string {
	eng := sandbox.DetectEngine(cfg.Sandbox.ContainerEngine)
	engine := eng.Detail
	if !eng.OK {
		engine = "unavailable — " + eng.Detail
	}
	image := cfg.Sandbox.ContainerImage
	if err := sandbox.ValidateImage(cfg.Sandbox.ContainerImage, cfg.Sandbox.ImageAllowlist); err != nil {
		image = err.Error()
	}
	r := report.Report{Title: "/sandbox status", Sections: []report.Section{{Pairs: []report.Pair{
		{Key: "engine", Value: engine},
		{Key: "image", Value: image},
		{Key: "owned", Value: ownedSummary()},
	}}}}
	return r.String() + "\n\n" + sandbox.IsolationReport(sandbox.Detect(), eng)
}

func ownedSummary() string {
	store, err := sandbox.OpenStore()
	if err != nil {
		return fmt.Sprintf("unknown (%v)", err)
	}
	recs, err := store.List()
	if err != nil {
		return fmt.Sprintf("unknown (%v)", err)
	}
	if len(recs) == 0 {
		return "no sandbox containers"
	}
	return fmt.Sprintf("%d sandbox container(s) — /sandbox list", len(recs))
}

// sandboxReportNow re-resolves the containment report against the working
// scope as it stands now, so `/sandbox doctor` names the directories a
// command may write to at the moment it is asked rather than at session
// start — and counts the processes running at that moment for the same
// reason: at session start there are none, and the interesting number is the
// one the reader is asking about.
func sandboxReportNow(cfg config.Config, sc *scope.Scope, sup *process.Supervisor) string {
	avail := sandbox.Detect()
	policy, err := sandboxPolicy(cfg, sc.Dirs()...)
	if err != nil {
		return "Command containment: policy unreadable — " + err.Error()
	}
	return sandbox.Report(avail, policy, runningProcesses(sup))
}

// runningProcesses is the supervisor's count for the surfaces that report
// containment; a session without the process tool has none to report.
func runningProcesses(sup *process.Supervisor) int {
	if sup == nil {
		return 0
	}
	return sup.Running()
}

// scopeReport is `/sandbox scope`: the working scope as containment sees it.
// The session's own /add-dir says the same thing in the session's words; this
// is the sandbox's answer, next to the mechanism that enforces it.
func scopeReport(sc *scope.Scope) string {
	r := report.Report{Title: "/sandbox scope", Sections: []report.Section{{
		Pairs: []report.Pair{{Key: "root", Value: sc.Root()}},
	}}}
	dirs := sc.Dirs()
	if len(dirs) == 0 {
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{
			report.Empty("nothing added to the scope", "/add-dir <path> puts a directory in it")}})
		return r.String()
	}
	rows := make([]report.Row, 0, len(dirs))
	for _, dir := range dirs {
		rows = append(rows, report.Row{State: report.Pass, Subject: dir})
	}
	r.Sections = append(r.Sections, report.Section{Header: "ADDED", Rows: rows})
	return r.String()
}

// sandboxManage handles the /sandbox subcommands (doctor, scope, list,
// status, destroy, prune) against the durable ownership records. Engine
// probes run on demand here, never eagerly at session start.
func sandboxManage(cfg config.Config, sc *scope.Scope, sup *process.Supervisor) func(args []string) string {
	return func(args []string) string {
		if len(args) == 0 {
			return "Usage: /sandbox [doctor|scope|list|status|destroy <id>|prune]"
		}
		ctx := context.Background()
		switch args[0] {
		case "doctor":
			return sandboxReportNow(cfg, sc, sup) + "\n\n" + containerReport(cfg)
		case "status":
			return containerReport(cfg)
		case "scope":
			return scopeReport(sc)
		case "list":
			return sandboxList(ctx)
		case "destroy":
			if len(args) != 2 {
				return "Usage: /sandbox destroy <id>"
			}
			return sandboxDestroy(ctx, args[1])
		case "prune":
			return sandboxPrune(ctx)
		}
		return "Usage: /sandbox [doctor|scope|list|status|destroy <id>|prune]"
	}
}

func sandboxList(ctx context.Context) string {
	store, err := sandbox.OpenStore()
	if err != nil {
		return "Error: " + err.Error()
	}
	recs, err := store.List()
	if err != nil {
		return "Error: " + err.Error()
	}
	out := report.Report{Title: "/sandbox list", Subject: countOf(len(recs), "container", "containers")}
	if len(recs) == 0 {
		return emptyInto(out, "no sandbox containers", "/sandbox doctor").String()
	}
	now := time.Now().UTC()
	rows := make([]report.Row, 0, len(recs))
	for _, rec := range recs {
		state, live := "unknown", report.Skip
		if path, err := exec.LookPath(rec.Engine); err != nil {
			state = "engine missing"
			live = report.Fail
		} else if s, gone, err := sandbox.ContainerState(ctx, path, rec.Name); gone {
			state = "vanished"
		} else if err == nil {
			state, live = s, report.Pass
		}
		expiry := "expires " + rec.ExpiresAt.Local().Format("Jan 2 15:04")
		if rec.Expired(now) {
			expiry, live = "expired", report.Skip
		}
		rows = append(rows, report.Row{
			State: live, Name: rec.ID, Subject: rec.Engine,
			Detail: joinDetail(rec.Workspace, expiry), Outcome: state,
			Body: []string{rec.Image},
		})
	}
	out.Sections = []report.Section{{Rows: rows}}
	return out.String()
}

func sandboxDestroy(ctx context.Context, id string) string {
	store, err := sandbox.OpenStore()
	if err != nil {
		return "Error: " + err.Error()
	}
	rec, err := store.Get(id)
	if err != nil {
		return "Error: " + err.Error()
	}
	path, err := exec.LookPath(rec.Engine)
	if err != nil {
		return fmt.Sprintf("Error: engine %s not found for sandbox %s", rec.Engine, id)
	}
	if err := sandbox.DestroyContainer(ctx, path, store, rec); err != nil {
		return "Error: " + err.Error()
	}
	return report.Report{Sections: []report.Section{{Rows: []report.Row{
		report.Done("destroyed sandbox", id)}}}}.String()
}

func sandboxPrune(ctx context.Context) string {
	store, err := sandbox.OpenStore()
	if err != nil {
		return "Error: " + err.Error()
	}
	res := sandbox.Reconcile(ctx, store, time.Now().UTC())
	out := report.Report{Sections: []report.Section{{Rows: []report.Row{
		report.Done("pruned", fmt.Sprintf("%d reaped (TTL) · %d dropped (vanished) · %d kept",
			len(res.Reaped), len(res.Dropped), len(res.Kept)))}}}}
	for _, e := range res.Errors {
		out.Notes = append(out.Notes, report.Note{State: report.Warn, Text: e})
	}
	return out.String()
}
