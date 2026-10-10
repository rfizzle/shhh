package cli

import (
	"context"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// childSandboxProfile is the profile a child's commands run under: the
// configured one when a mechanism is available, which is exactly when
// childCommandRunner wraps them, and empty otherwise.
func childSandboxProfile(cfg config.Config) string {
	p, err := sandboxPolicy(cfg)
	if err != nil || !sandbox.Detect().OK {
		return ""
	}
	return string(p.Profile)
}

// childCommandRunner builds the execute_command runner for a sub-agent rooted
// at dir: contained with the workspace grant moved to dir when a mechanism is
// available, plain with cwd=dir otherwise (matching the parent session's
// uncontained fallback).
//
// Every form is bounded. A child has nobody in front of it, so a command that
// never finishes takes the child with it — and the parent is left waiting on
// a report that is not coming. A child whose every command is refused is
// given no runner at all, which the bound must not wrap into one.
func childCommandRunner(cfg config.Config, dir string, sc *scope.Scope, writer bool, avail sandbox.Availability) func(context.Context, string) tools.ExecResult {
	run := childCommandRunnerIn(cfg, dir, sc, writer, avail)
	if run == nil {
		return nil
	}
	return boundedRunner(run, cfg.CommandTimeout())
}

// childRequiresContainment reports whether a child's commands must run
// contained or not at all: where the session requires it of every command,
// and — unless agents.require_sandbox turns it off — for a writer, whose
// commands are the ones nobody watches as they happen.
// See docs/capabilities/containment.md#containment-can-be-required.
func childRequiresContainment(cfg config.Config, writer bool) bool {
	return cfg.Sandbox.Require || (writer && cfg.AgentsRequireSandboxEnabled())
}

// childCommandsRefused reports a child whose every command will be refused:
// a writer that must be contained on a host with nothing to contain it. It
// is what earns the child the paragraph saying so; a researcher and a
// reviewer hold no command to refuse.
func childCommandsRefused(cfg config.Config, writer bool, avail sandbox.Availability) bool {
	return writer && !avail.OK && childRequiresContainment(cfg, writer)
}

// childCommandRefusal is the answer every command a child makes gets before
// anything decides about it, or "" where its commands may run: one that must
// be contained on a host with nothing to contain it. The child's gate answers
// with it ahead of the card and its runner with it behind every approval, so
// the two cannot disagree about which commands are refused.
// See docs/capabilities/containment.md#containment-can-be-required.
func childCommandRefusal(cfg config.Config, writer bool, avail sandbox.Availability) string {
	if avail.OK || !childRequiresContainment(cfg, writer) {
		return ""
	}
	return childRefusal(cfg, avail)
}

// childRefusal is what a child's command is answered with where it must be
// contained and nothing can contain it: the session's own refusal where the
// session requires containment, and the writer's where only the writer
// default does. Both carry the doctor's fix.
func childRefusal(cfg config.Config, avail sandbox.Availability) string {
	if cfg.Sandbox.Require {
		return uncontainedRefusal(avail)
	}
	return writerUncontainedRefusal(avail)
}

// writerCommands is what a writer's commands run under on this host, in the
// words the spawn card, `/status` and the doctor state it in: contained, and
// by what; refused, because they must be and nothing here can; or
// uncontained, because the person turned the writer default off.
type writerCommands struct {
	// state is contained, refused or uncontained; mechanism names what
	// contains them, and is empty for the other two.
	state, mechanism, detail string
	// required is whether containment is demanded of them rather than
	// merely in force.
	required bool
}

// Writer command states.
const (
	writerContained   = "contained"
	writerRefused     = "refused"
	writerUncontained = "uncontained"
)

// writerContainment reads the writer default against the host. It is one
// function so the card a spawn is approved on, the session's status and the
// doctor say the same thing about the same child.
// See docs/capabilities/containment.md#containment-can-be-required.
func writerContainment(cfg config.Config, avail sandbox.Availability) writerCommands {
	required := childRequiresContainment(cfg, true)
	switch {
	case avail.OK:
		detail := ""
		if required {
			detail = "required"
		}
		if p, err := sandboxPolicy(cfg); err == nil {
			detail = joinDetail(joinDetail(detail, string(p.Profile)+" profile"), sandbox.NetworkWords(avail, p))
		}
		return writerCommands{state: writerContained, mechanism: avail.Mechanism, detail: detail, required: required}
	case required:
		return writerCommands{state: writerRefused, detail: "no containment mechanism is in force: " + avail.Detail, required: true}
	}
	return writerCommands{state: writerUncontained, detail: "agents.require_sandbox is off; they run as you"}
}

// value is the state with the mechanism beside it, which is the part of the
// answer a narrow card must not shed.
func (w writerCommands) value() string {
	return joinDetail(w.state, w.mechanism)
}

// line is the state as one line of `/status`.
func (w writerCommands) line() string {
	return "a writer's commands: " + w.value() + " — " + w.detail
}

// field is the state as the spawn card's row. An uncontained writer is an
// open door, which is what the card's level is read off.
func (w writerCommands) field() chat.GatedField {
	return chat.GatedField{Label: "commands", Value: w.value(), Detail: w.detail, Open: w.state == writerUncontained}
}

// childCommandRunnerUnbounded answers with the typed result the session's own
// runner does, so a child's command that never started names what it needed
// and one whose ending nobody read says so, on the child's own result line.
// The contained form reads its ending as the session's does (readContained):
// a shell the mechanism could not exec is a command that never started, not
// one that ran and exited.
// See docs/capabilities/containment.md#a-command-that-never-started-names-what-it-needed.
// childContainment is how a child's runner asks which mechanism the host has.
// It is a variable so a test can stand a mechanism in on a host that has none.
var childContainment = sandbox.Detect

func childCommandRunnerUnbounded(cfg config.Config, dir string, sc *scope.Scope, writer bool) func(context.Context, string) tools.ExecResult {
	return childCommandRunnerIn(cfg, dir, sc, writer, childContainment())
}

// childCommandRunnerIn is the runner over a host already asked what it has,
// or nil for a child whose every command is refused (childCommandRefusal).
func childCommandRunnerIn(cfg config.Config, dir string, sc *scope.Scope, writer bool, avail sandbox.Availability) func(context.Context, string) tools.ExecResult {
	required := childRequiresContainment(cfg, writer)
	if _, err := sandboxPolicy(cfg); err != nil {
		// A command that must be contained never falls back to running
		// bare because the policy could not be read.
		if required {
			return func(context.Context, string) tools.ExecResult { return runner.WrapFailure(dir, err) }
		}
	} else {
		if avail.OK {
			return func(ctx context.Context, command string) tools.ExecResult {
				// The policy is rebuilt per command so a directory the parent
				// added mid-session is writable in the child too.
				p, pErr := sandboxPolicy(cfg, sc.Dirs()...)
				if pErr != nil {
					return runner.WrapFailure(dir, pErr)
				}
				p.Workspace = dir
				p.Cwd = dir
				// The session's build cache, the one its gate's checks use:
				// five writers in five copies of the tree compile the
				// standard library once between them rather than five times.
				// See docs/capabilities/subagents.md#what-they-share.
				p.PrivateGoCache = true
				argv, wErr := sandbox.Wrap(avail, p, command)
				if wErr != nil {
					return runner.WrapFailure(dir, wErr)
				}
				return readContained(avail.Mechanism, runner.RunCaptureArgvInResult(ctx, dir, command, argv))
			}
		}
		// A child's command is the assistant's, and a child is the one place
		// with no card to refuse on and nobody to refuse to: a session that
		// requires containment has to refuse here as well, or the
		// requirement is one a fan-out walks around. A writer is required
		// to be contained by default whatever the session requires of its
		// own commands, because nobody watches a writer's commands as they
		// happen.
		//
		// The refusal itself is the gate's (Env.CommandRefusal), answered
		// ahead of every seam in the one spelling every surface shares, so
		// no command of this child can reach a runner. It is given none
		// rather than one that refuses in a second spelling, and none rather
		// than the plain runner below: a call that did get past the gate
		// would meet the supervisor's own "not available" and still not run.
		// See docs/capabilities/containment.md#containment-can-be-required.
		if childCommandRefusal(cfg, writer, avail) != "" {
			return nil
		}
	}
	return func(ctx context.Context, command string) tools.ExecResult {
		return runner.RunCaptureInResult(ctx, dir, command)
	}
}

// childCommands is a child's runner and the refusal its commands get. In a
// sandbox session a child that holds a command has nowhere contained to run
// it — a child's commands do not follow the session's into its container
// yet, and a writer's worktree is outside its one mount — and on the host
// they would be outside what the person asked for, so every one is refused
// and the child is given no runner (sandbox.go).
func childCommands(cfg config.Config, dir string, sc *scope.Scope, writer bool, avail sandbox.Availability, inSandbox bool) (chat.RunFunc, string) {
	if inSandbox {
		return nil, sandboxChildRefusal
	}
	return childCommandRunner(cfg, dir, sc, writer, avail), childCommandRefusal(cfg, writer, avail)
}
