package cli

import (
	"context"
	"runtime"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func probeSandbox(_ context.Context, cfg config.Config) doctorFinding {
	policy, err := sandboxPolicy(cfg)
	if err != nil {
		return doctorFinding{
			Subject: "the containment policy is not readable", Detail: err.Error(),
			Outcome: "misconfigured", State: components.DoctorFailed,
			Consequence: "every contained command will fail until the policy is fixed; none of them runs bare",
			FixLabel:    "show the setting to fix",
			Fix:         []string{"shhh config set --global sandbox.profile workspace"},
		}
	}
	avail := sandbox.Detect()
	return withWriterDefault(doctorSandbox(avail, policy, runtime.GOOS), writerContainment(cfg, avail))
}

// withWriterDefault names on the sandbox row what a writer's commands run
// under, because a writer's rule is not the session's: on a host with no
// mechanism a session may run its own commands unconfined while every
// writer's is refused. It is added here rather than in doctorSandbox, whose
// fix lines are the refusal's own.
// See docs/capabilities/containment.md#containment-can-be-required.
func withWriterDefault(f doctorFinding, w writerCommands) doctorFinding {
	switch w.state {
	case writerContained:
		words := "writers must be contained"
		if !w.required {
			words = "writers contained, not required (agents.require_sandbox off)"
		}
		f.Detail = joinDetail(f.Detail, words)
	case writerRefused:
		f.Consequence = joinConsequence(f.Consequence, "a writer's commands are refused until one is")
	case writerUncontained:
		f.Consequence = joinConsequence(f.Consequence, "a writer's commands run as you too (agents.require_sandbox off)")
	}
	return f
}

// joinConsequence adds a clause to a finding's consequence.
func joinConsequence(head, tail string) string {
	if head == "" {
		return tail
	}
	return head + "; " + tail
}

// doctorSandbox reads the containment mechanism. This is the check the
// artboard leads its failure with, and the consequence is quoted from the
// surface the reader will actually meet it on: the approval card promotes
// `⚠ UNCONTAINED` to its title bar when nothing wraps the command.
//
// The row says the temporary directory is private because the answer used to
// be the other one: /tmp was a writable bind of the host's, and a reader who
// learned that is owed the correction where they are already looking. What
// the private one is, and what a grant of the host's would change, is
// docs/capabilities/containment.md#the-temporary-directory-is-the-sessions-own
// and `/sandbox doctor`, which resolves the whole policy.
func doctorSandbox(avail sandbox.Availability, policy sandbox.Policy, goos string) doctorFinding {
	if avail.OK {
		detail := joinDetail(joinDetail(joinDetail(avail.Detail, string(policy.Profile)+" profile"), sandbox.NetworkWords(avail, policy)), "private tmpdir")
		// The repository's hooks and config, said where they are read-only
		// and not claimed where they are not (GitStoreWords is "" then): git
		// runs them on the host, and this row's reader would not know.
		// See docs/capabilities/containment.md#the-repositorys-own-programs-are-read-only.
		return doctorFinding{
			Subject: avail.Mechanism,
			Detail:  joinDetail(detail, sandbox.GitStoreWords(avail, policy)),
			Outcome: "contained",
		}
	}
	f := doctorFinding{
		Subject: "no containment mechanism", Detail: avail.Detail,
		Outcome: "uncontained", State: components.DoctorFailed,
		Consequence: "every approval will show ⚠ UNCONTAINED, and an approved command runs as you",
		FixLabel:    "show the fix for this host",
	}
	if len(policy.AllowHosts) > 0 {
		// A list with no wall to hold it is a list nothing reads, and the
		// row is where the reader who wrote one is looking.
		f.Consequence += "; sandbox.allow_hosts is not in force, so every host is reachable"
	}
	switch goos {
	case "linux":
		f.Fix = []string{
			"sudo apt install bubblewrap        (or the package your distribution ships)",
			"unprivileged user namespaces must be enabled: sysctl kernel.unprivileged_userns_clone=1",
			"shhh doctor                        to check it took",
		}
	case "darwin":
		f.Fix = []string{
			"sandbox-exec ships with macOS; a PATH that hides /usr/bin is the usual cause",
			"shhh doctor                        to check it took",
		}
	default:
		// Nothing to install: the platform has no mechanism at all, and
		// naming one that does not exist would be worse than saying so.
		f.FixLabel = "show what can be done instead"
		f.Fix = []string{
			"there is no containment mechanism on " + goos,
			"run the agent inside a container sandbox instead: shhh code -p --sandbox",
		}
	}
	return f
}

func probeEngine(_ context.Context, cfg config.Config) doctorFinding {
	eng := sandbox.DetectEngine(cfg.Sandbox.ContainerEngine)
	image := sandboxImageFor(cfg)
	imageErr := sandbox.ValidateImage(image, cfg.Sandbox.ImageAllowlist)
	f := doctorEngine(eng, image, imageErr, ownedSandboxCount())
	if eng.OK && len(cfg.Sandbox.AllowHosts) > 0 {
		// A container's network is a switch, so the list is not what a
		// --sandbox run's commands are held to, and the row is where that
		// is said rather than left for the reader to assume.
		// See docs/capabilities/containment.md#a-contained-commands-network-can-be-a-list-of-hosts.
		f.Detail = joinDetail(f.Detail, "sandbox.allow_hosts not held; a container's network is the profile's switch")
	}
	return f
}

// probeImage is the doctor's reading of the image a --sandbox run would start
// from. It asks the engine and prepares nothing: a diagnostic looks and does
// not touch, and a preparation is minutes of installs.
func probeImage(ctx context.Context, cfg config.Config) doctorFinding {
	tc, declared, err := project.LoadToolchain(projectTrust())
	r := preparedReading{declared: declared && sandbox.NeedsPreparing(tc), broken: err != nil}
	if !r.declared {
		return doctorImage(r)
	}
	eng := sandbox.DetectEngine(cfg.Sandbox.ContainerEngine)
	base := sandboxImageFor(cfg)
	if !eng.OK || sandbox.ValidateImage(base, cfg.Sandbox.ImageAllowlist) != nil {
		// The engine row says which, and why.
		return doctorImage(r)
	}
	r.sandbox = true
	r.id, _, r.err = sandbox.LookupPrepared(ctx, eng, base, tc.Digest)
	return doctorImage(r)
}

// preparedReading is what the doctor found of the image a --sandbox run
// would start from.
type preparedReading struct {
	// declared is a declaration that loaded and names something to install.
	declared bool
	// broken is a declaration that is there, trusted, and does not load.
	broken bool
	// sandbox is an engine and a base a container could start from.
	sandbox bool
	// id is the prepared image, where one is current.
	id string
	// err is an engine that could not say.
	err error
}

// doctorImage reads the image a --sandbox run starts from: the declaration it
// is prepared from, the image, and whether it is current — prepared from the
// declaration and the base as they stand now. Its outcome is the answer,
// because an outcome is the one field a row never clips. An image not yet
// prepared is not a fault; the next run prepares it, and the row says so
// rather than leaving the wait to surprise the reader. A declaration that
// does not load is the needs row's to explain, since one fault is not two.
// See docs/capabilities/containment.md#a-sandbox-starts-from-an-image-prepared-from-it.
func doctorImage(r preparedReading) doctorFinding {
	switch {
	case r.broken:
		return doctorFinding{Subject: "unreadable", Detail: project.ToolchainFile + " · --sandbox refuses until it loads",
			Outcome: "not checked", State: components.DoctorSkipped}
	case !r.declared:
		return doctorFinding{Subject: "nothing to prepare", Detail: "the base image runs as it is",
			Outcome: "empty", State: components.DoctorSkipped}
	case !r.sandbox:
		return doctorFinding{Subject: project.ToolchainFile, Detail: "no container sandbox to prepare it for",
			Outcome: "not checked", State: components.DoctorSkipped}
	case r.err != nil:
		return doctorFinding{Subject: project.ToolchainFile, Detail: r.err.Error(),
			Outcome: "unknown", State: components.DoctorWarned,
			Consequence: "the engine could not say whether an image was prepared from it"}
	case r.id == "":
		return doctorFinding{Subject: project.ToolchainFile, Detail: "the next --sandbox run prepares it, once",
			Outcome: "not prepared", State: components.DoctorSkipped}
	}
	return doctorFinding{Subject: project.ToolchainFile, Detail: "prepared as " + shortPrepared(r.id), Outcome: "current"}
}

// ownedSandboxCount is how many sandbox containers this machine still owns.
// An unreadable ownership store answers -1, which the reading states rather
// than passing off as none.
func ownedSandboxCount() int {
	store, err := sandbox.OpenStore()
	if err != nil {
		return -1
	}
	recs, err := store.List()
	if err != nil {
		return -1
	}
	return len(recs)
}

// doctorEngine reads container sandboxes, which are opt-in: `shhh code
// -p --sandbox` asks for one and nothing else does. So a machine with no
// engine is `⊘ not checked` rather than a failure — the row states what is
// not available instead of claiming something is broken (invariant 4).
func doctorEngine(eng sandbox.Engine, image string, imageErr error, owned int) doctorFinding {
	if !eng.OK {
		return doctorFinding{
			Subject: "no container engine", Detail: eng.Detail,
			Outcome: "not available", State: components.DoctorSkipped,
			Consequence: "shhh code -p --sandbox will refuse to start; nothing else needs one",
			FixLabel:    "show what a sandbox needs",
			Fix: []string{
				"install podman (rootless, preferred) or docker",
				"shhh config set --global sandbox.container_image <name>@sha256:<digest>",
			},
		}
	}
	f := doctorFinding{Subject: eng.Name, Detail: eng.Detail, Outcome: "ok"}
	if owned > 0 {
		f.Detail = joinDetail(f.Detail, countOf(owned, "container owned", "containers owned"))
	}
	if imageErr != nil {
		f.Detail = imageErr.Error()
		f.Outcome = "no image"
		f.State = components.DoctorWarned
		f.Consequence = "the engine is there, so only the image stands between this host and a sandbox"
		f.FixLabel = "show the setting to fix"
		f.Fix = []string{"shhh config set --global sandbox.container_image <name>@sha256:<digest>"}
		return f
	}
	f.Detail = joinDetail(f.Detail, "image "+shortImage(image))
	return f
}

// shortImage is a digest-pinned reference with the digest cut to its first
// twelve characters — enough to tell two pins apart, short enough for a row.
func shortImage(image string) string {
	name, digest, found := strings.Cut(image, "@sha256:")
	if !found || len(digest) <= 12 {
		return image
	}
	return name + "@sha256:" + digest[:12] + "…"
}
