package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// sandboxPolicy builds the session containment policy (S-062): workspace is
// the current directory, profile and extensions come from config.
func sandboxPolicy(cfg config.Config) (sandbox.Policy, error) {
	profile, err := sandbox.ParseProfile(cfg.Sandbox.Profile)
	if err != nil {
		return sandbox.Policy{}, fmt.Errorf("config sandbox.profile: %w", err)
	}
	ws, err := os.Getwd()
	if err != nil {
		return sandbox.Policy{}, err
	}
	return sandbox.Policy{
		Workspace:  ws,
		Profile:    profile,
		DenyExtra:  cfg.Sandbox.DenyExtra,
		WriteExtra: cfg.Sandbox.WriteExtra,
	}, nil
}

// buildContainment assembles the containment setup agent sessions run
// commands through: a wrapped runner when a mechanism is available, plus the
// status line and doctor report either way. A wrap or spawn failure surfaces
// as the command's error result — a contained command never falls back to
// running bare. Session start also reconciles container-sandbox ownership
// records (S-063) so crashed sessions' containers get reaped.
func buildContainment(cfg config.Config) (chat.Containment, error) {
	policy, err := sandboxPolicy(cfg)
	if err != nil {
		return chat.Containment{}, err
	}
	reconcileOwnedSandboxes()
	avail := sandbox.Detect()
	procReport := sandbox.Report(avail, policy)
	c := chat.Containment{
		Report: procReport,
		Manage: sandboxManage(cfg, procReport),
	}
	if !avail.OK {
		c.Status = "unconfined — " + avail.Detail
		c.Detail = avail.Detail
		// Nothing wraps the command, so nothing restricts what it reaches:
		// the approval card says so rather than reporting the profile's
		// answer, which is not in force (S-101).
		c.Network = true
		return c, nil
	}
	c.Status = fmt.Sprintf("contained: %s (%s profile)", avail.Mechanism, policy.Profile)
	c.Mechanism, c.Profile = avail.Mechanism, string(policy.Profile)
	c.Network = policy.Profile != sandbox.ProfileWorkspaceNetless
	c.Run = func(ctx context.Context, command string) (string, int) {
		argv, err := sandbox.Wrap(avail, policy, command)
		if err != nil {
			return "sandbox: " + err.Error(), -1
		}
		return runner.RunCaptureArgv(ctx, argv)
	}
	c.TailRun = func(ctx context.Context, command string, onLine func(string)) (string, int) {
		argv, err := sandbox.Wrap(avail, policy, command)
		if err != nil {
			return "sandbox: " + err.Error(), -1
		}
		return runner.RunCaptureArgvTail(ctx, argv, onLine)
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
func startSandbox(ctx context.Context, cfg config.Config) (run func(context.Context, string) (string, int), cleanup func(), err error) {
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
		return runner.RunCaptureArgv(ctx, c.ExecArgv(command))
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
	var b strings.Builder
	b.WriteString("Container sandboxes:\n")
	if eng.OK {
		fmt.Fprintf(&b, "  engine:    %s\n", eng.Detail)
	} else {
		fmt.Fprintf(&b, "  engine:    unavailable — %s\n", eng.Detail)
	}
	if err := sandbox.ValidateImage(cfg.Sandbox.ContainerImage, cfg.Sandbox.ImageAllowlist); err != nil {
		fmt.Fprintf(&b, "  image:     %v\n", err)
	} else {
		fmt.Fprintf(&b, "  image:     %s\n", cfg.Sandbox.ContainerImage)
	}
	fmt.Fprintf(&b, "  owned:     %s\n", ownedSummary())
	b.WriteString(sandbox.IsolationReport(sandbox.Detect(), eng))
	return strings.TrimRight(b.String(), "\n")
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

// sandboxManage handles the /sandbox subcommands (doctor, list, status,
// destroy, prune) against the durable ownership records. Engine probes run
// on demand here, never eagerly at session start.
func sandboxManage(cfg config.Config, procReport string) func(args []string) string {
	return func(args []string) string {
		if len(args) == 0 {
			return "Usage: /sandbox [doctor|list|status|destroy <id>|prune]"
		}
		ctx := context.Background()
		switch args[0] {
		case "doctor":
			return procReport + "\n\n" + containerReport(cfg)
		case "status":
			return containerReport(cfg)
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
		return "Usage: /sandbox [doctor|list|status|destroy <id>|prune]"
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
	if len(recs) == 0 {
		return "No sandbox containers."
	}
	var b strings.Builder
	b.WriteString("Sandbox containers:\n")
	now := time.Now().UTC()
	for _, r := range recs {
		state := "unknown"
		if path, err := exec.LookPath(r.Engine); err != nil {
			state = "engine missing"
		} else if s, gone, err := sandbox.ContainerState(ctx, path, r.Name); gone {
			state = "vanished"
		} else if err == nil {
			state = s
		}
		expiry := "expires " + r.ExpiresAt.Local().Format("Jan 2 15:04")
		if r.Expired(now) {
			expiry = "expired"
		}
		fmt.Fprintf(&b, "  %s  %s  %s  %s  %s\n    %s\n", r.ID, r.Engine, state, expiry, r.Workspace, r.Image)
	}
	return strings.TrimRight(b.String(), "\n")
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
	return "Destroyed sandbox " + id + "."
}

func sandboxPrune(ctx context.Context) string {
	store, err := sandbox.OpenStore()
	if err != nil {
		return "Error: " + err.Error()
	}
	res := sandbox.Reconcile(ctx, store, time.Now().UTC())
	out := fmt.Sprintf("Pruned: %d reaped (TTL), %d dropped (vanished), %d kept.",
		len(res.Reaped), len(res.Dropped), len(res.Kept))
	if len(res.Errors) > 0 {
		out += "\nProblems:\n  " + strings.Join(res.Errors, "\n  ")
	}
	return out
}
