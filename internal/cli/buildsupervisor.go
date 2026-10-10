package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// buildSupervisor assembles a surface's sub-agent supervisor. untracked comes
// in because a writer starts from the parent's tree, and the files git has
// never heard of are the half of that tree only the surface itself can name:
// a session reads them off its changeset, and a run that keeps none passes
// nil, which is a writer starting from `git diff HEAD` alone.
//
// The rest of what it is built from is the surface's assembly — the config
// the environment resolved, the reducer, the store, the price table, the
// scope and the ledger — and the three things each surface makes after it:
// the record, the classifier and the hooks. The session is the surface's own
// copy as it stands at the call, which is the one a child inherits from.
// See docs/architecture.md#a-session-is-assembled-in-one-place.
func buildSupervisor(ctx context.Context, a *assembly, session chatSession, recorder *observeRecorder,
	classifier *agent.Classifier, hooks *hook.Runner, untracked func() []string) *subagent.Supervisor {
	cfg, env, red, db, prices, sc, ledger := a.env.cfg, a.env, a.ts.evidence, a.db, a.prices, a.sc, a.ledger
	agents := a.agents
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	if agents == nil {
		agents = &agentProfiles{profiles: subagent.BuiltinProfiles()}
	}
	spawnable := spawnModels{env: env, agents: agents, prices: prices}
	// The supervisor a child delegates through is the session's own, and
	// newEnv is what puts it on a child's chain — but newEnv is built here
	// and the supervisor is built from it, so the two are tied together
	// afterwards. Nothing reads it before the first spawn, which cannot
	// happen until New has returned.
	var sup *subagent.Supervisor
	// Where each agent's own record row is, so the row a delegated child
	// opens hangs under its spawner's rather than under the session's.
	var rows agentRows
	// The project's instruction files, read once for the session and handed
	// to every child that follows. They are read from disk and rendered
	// against a budget that walks the whole set; doing that inside newEnv
	// paid for it once per spawn, again per retry, and again at the handoff
	// a child over its budget is asked for — every one of them re-reading
	// files nobody edited in between.
	//
	// The parent's checkout, not the child's worktree: a writer's worktree
	// is a copy of this one, and the context a child is given is the context
	// the session it serves was given. A smaller budget than the session's,
	// because a child pays for it out of its own token budget rather than
	// once for a conversation (prompt.ChildInstructionBudget).
	childInstructions := project.InstructionBlock(
		project.Instructions(root, userInstructionsPath()), prompt.ChildInstructionBudget)

	newEnv := func(cctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		role, croot := spec.Role, spec.Root
		// A child runs commands through the same runner the parent does, so
		// it is told the same shell (internal/shell).
		info := shell.DetectExec()
		info.Cwd = croot
		extra := childExtra(cfg.Behavior.SystemPromptExtra, childInstructions,
			session.memoryBlock, env.workspaceBlock(), spec.Worktree)

		var sysPrompt string
		var rolePrompt func([]string) string
		var defs []provider.Tool
		gated := map[string]bool{}
		var base agent.ToolExecutor
		def, ok := agents.definition(role)
		if ok {
			rolePrompt, defs, base = profileEnv(def, spec, info, extra, session.web, session.gateRunner, gated)
		} else {
			sysPrompt, defs, base = builtinEnv(role, spec, info, extra, session.web, gated)
			rolePrompt = fixedPrompt(sysPrompt)
		}
		// An integration writer is the conflicting writer's own profile with
		// one more paragraph: what its copy holds and how to say it could not
		// reconcile the two (docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer).
		if spec.Integrates != "" {
			inner := rolePrompt
			rolePrompt = func(names []string) string { return inner(names) + "\n\n" + prompt.IntegrationWriter }
		}
		// And what this child may delegate, if anything. It goes on after
		// both role branches because the web branch replaces the executor
		// rather than wrapping it and the gate branch wraps what the web
		// branch left, so a delegation wrap installed before either would
		// disappear and the call would come back an unknown tool — the trap
		// the quality gate's own ordering already answers.
		defs, base = withDelegation(sup, agents, spawnable.offer(), def, spec, defs, base, gated)
		defs, base, sysPrompt, keepResult := withSessionTools(
			session, red, notebookSignature(sup, spec), croot, defs, base, rolePrompt)
		// A child handed its parent's last turns is told so, in place of the
		// sentence that says it can see none of the conversation.
		sysPrompt = prompt.Inherited(sysPrompt, spec.Inherit)

		autoExec, gatedExec, repeats := childExecutors(base, red, session.lsp, hooks)

		streamDefs := defs
		// The child's model is resolved by the supervisor (spawn argument →
		// role profile → agents.model → session model); an empty one here
		// would mean no resolution ran, so fall back to the session model.
		childModel := spec.Model
		if childModel == "" {
			childModel = env.modelName
		}
		// Each child bills itself. A fan-out is the one place where several
		// requesters spend at once, so "sub-agents" as a class is not a fine
		// enough answer to which of them spent it.
		childProvider := ledger.ForOrigin(env.prov, meter.Origin{Source: meter.SourceSubagent, Label: spec.Name})

		// Whether a read-only mode's paragraph is this child's to read: only
		// a child that holds a write or a command is, since every other
		// role's own prompt already says it can do neither.
		refusable := holdsRefusable(streamDefs)
		// Whether this child's commands are refused outright: a writer that
		// must be contained on a host with nothing to contain it. The host
		// is asked once, here, and the runner below is handed the same
		// answer, so the paragraph the child reads and the refusal its
		// commands get cannot disagree.
		writer := agents.writes(role)
		avail := childContainment()
		// A profile that may write and not execute holds no command, and a
		// paragraph about refused commands would describe a tool it never had.
		holds := holdsCommand(streamDefs)
		commandsRefused := childCommandsRefused(cfg, writer, avail) && holds
		sandboxChild := session.sandbox && holds
		runCommand, commandRefusal := childCommands(cfg, croot, sc, writer, avail, sandboxChild)
		if sandboxChild {
			commandsRefused = true
		}

		stream := childStream{
			ctx:             cctx,
			env:             env,
			agents:          agents,
			vault:           session.vault,
			sup:             sup,
			spec:            spec,
			readOnlyExtra:   cfg.Behavior.ReadOnlyCommands,
			defs:            streamDefs,
			model:           childModel,
			provider:        childProvider,
			refusable:       refusable,
			commandsRefused: commandsRefused,
			sandboxChild:    sandboxChild,
		}.stream()

		return subagent.Env{
			SystemPrompt: sysPrompt,
			Stream:       stream,
			Executor:     session.vault.WrapExecutor(subagent.RootedExecutor(croot, autoExec)),
			ExecuteGated: session.vault.WrapExecutor(gatedExec),
			RunCommand:   scrubResultRunner(session.vault, runCommand),
			// The refusal, answered ahead of the card: a command that could
			// run nowhere whatever the person said is never put to them. A
			// child it is set for is given no runner above.
			CommandRefusal: commandRefusal,
			// The same pipeline the parent's own commands go through, and
			// the same store behind it: a child's evidence entries land
			// beside the session's, so the id in a reduction notice is one
			// the child's evidence tool — registered above — can page.
			// Safe on a nil reducer, which reduces nothing.
			Reduce: red.Process,
			// The same store again, at the other end of the window: what a
			// child's trim elides is put there and the placeholder carries
			// the id, so a result that left the window is one the child's
			// own evidence tool can page back. Safe on a nil reducer, which
			// answers that it kept nothing.
			KeepResult: keepResult,
			Archive:    red.Keep,
			// And once more at the cap under both of them: what the bound on
			// a command result cuts out of the middle goes to the same
			// store, so a child whose reduction failed open is offered the
			// id rather than a byte count. Safe on a nil reducer.
			Keep:  red.Keep,
			Gated: gated,
			Scrub: session.vault.ScrubMessage,
			// A child is as unwatched as a headless run and is read for the
			// same reason. A fan-out multiplies the cost by its width, which
			// is what summary.subagents is there to turn off; off, the
			// roster says so, because an unread child is never listed as
			// steered and an empty field otherwise reads as good news.
			Summarizer: newSummarizer(cfg, env, ledger, cfg.SubagentSummaryEnabled()),
			Steering:   steering(cfg, env.prompts),
			Retries:    cfg.Behavior.ProviderRetries,
			// What the child's window-recovery step measures against: the
			// downloaded table's answer for the model this child was routed
			// to, which is routinely not the session's, and what its own
			// definitions cost on every request it makes.
			Window:     childWindow(prices, childModel),
			ToolTokens: childToolTokens(streamDefs),
			// The person's own commands at the child's two tool dispatchers,
			// outside the vault's scrub on both — a hook reads what the model
			// reads, which is how the session's own seams are ordered
			// (toolset.go puts the vault outside everything).
			WrapAuto:  childHookAuto(hooks),
			WrapGated: childHookGated(hooks),
			// And at the rest of its life: its start, its end, and either
			// side of a compaction of its conversation.
			Start:      childHookStart(hooks),
			Stop:       childHookStop(hooks),
			Compaction: childHookCompaction(hooks),
			// And the same detector the two dispatchers above were wrapped
			// with, asked what ground this child has been over: its readings
			// are the only thing watching it, and a child that has searched
			// one directory a dozen times without writing anything is the
			// shape they were reading as on target.
			Sweeps: repeats.Sweeps,
			// And the reading that tells the child its workspace moved under
			// it, taken where the child is standing: a writer's worktree, or
			// the parent's checkout for a reader.
			TreeCheck: childTree(cfg, session.sibling, croot, spec.Worktree),
		}, nil
	}

	// The generated paths come from the gate's own trusted config and run
	// through its runner, so a checkout nobody has trusted declares none —
	// there is no runner — and every file there merges as text.
	var generators subagent.Regenerator
	if session.gateRunner != nil {
		generators = session.gateRunner
	}
	sup = subagent.New(ctx, subagent.Options{
		Root:       root,
		NewEnv:     newEnv,
		Generators: generators,
		// The same table the session ledger bills against, so a child's own
		// bill and the session's share of it are the same arithmetic on the
		// same rates rather than two answers to reconcile.
		Prices: prices,
		Record: func(spec subagent.Spec, sysPrompt string) subagent.Recorder {
			// A child is recorded against the model it actually ran on. The
			// session model is the wrong one to price it at: agents.model and
			// a per-spawn model both routinely send children somewhere
			// cheaper, and a row priced at the parent's rate overstates them.
			model := spec.Model
			if model == "" {
				model = env.modelName
			}
			// And under the agent that spawned it, not under the session
			// flatly: the record keeps the same tree the map draws, so a
			// child a child asked for reads back as the level it ran at.
			r := startChildObserveRecorder(db, string(spec.Role), env.prov.Name(), model, spec.Name, prices,
				rows.under(spec.Parent, recorder))
			rows.keep(spec.Name, r)
			// The child's own provenance, not the parent's: it ran under its
			// own prompt, and a row that borrowed the parent's hash would put
			// the two on the same side of an edit that only touched one.
			// The checkout is the parent's, because that is the project the
			// child is working on — a writer's worktree is a temporary copy
			// of it, and fingerprinting that would give every writer a
			// cohort of one.
			//
			// The settings are the child's own for the same reason: its
			// mode after the profile and the clamp, its cap, and the level
			// its role thinks at. The classifier is the parent's, because
			// that is the one it asks.
			effort := env.effort
			if env.reasoning != nil {
				effort = env.reasoning()
			}
			r.stamp(env.prompts.fingerprintOf(sysPrompt), session.skills.Len(), projectFingerprintRoot(), sessionSettings(cfg, runSettings{
				mode:   spec.Mode.String(),
				effort: agents.effortFor(spec.Role, effort),
				rounds: spec.MaxRounds,
				// A child's own interval, not the configured one: it runs
				// with nobody in front of it, so its check-in is often the
				// only question it is ever put.
				checkIn:    subagent.ChildCheckInInterval,
				sandbox:    childSandboxProfile(cfg),
				model:      auxiliaryModel(cfg, env.provName, env.modelName),
				summary:    cfg.SubagentSummaryEnabled(),
				classifier: true,
			}))
			return subagent.Recorder{
				Observer: r.observer(),
				End:      r.endChild,
				Handoff: func(content []byte) (string, error) {
					if r == nil || db == nil {
						return "", nil
					}
					return db.SaveChildHandoff(r.sessionID(), content)
				},
			}
		},
		CommandAllowlist: cfg.Behavior.CommandAllowlist,
		CommandDenylist:  cfg.Behavior.CommandDenylist,
		AllowHosts:       cfg.Web.AllowHosts,
		DenyHosts:        cfg.Web.DenyHosts,
		ReadOnlyExtra:    cfg.Behavior.ReadOnlyCommands,
		ReadOnlyDisabled: !cfg.ReadOnlyAutoEnabled(),
		// Children get the same auto-mode classifier the parent uses, so an
		// auto-mode session does not turn into one prompt per child command.
		Classifier: classifier,
		ModelFor: func(role subagent.Role, depth int, requested string) string {
			return agents.modelFor(cfg, role, depth, requested, env.childModel())
		},
		CheckModel:    spawnable.check,
		Profiles:      agents.snapshot(),
		MaxConcurrent: cfg.Agents.MaxConcurrent,
		MaxDepth:      cfg.AgentMaxDepth(),
		MaxChildren:   cfg.Agents.MaxChildren,
		// One throttle on checks for the whole session: a child's build or
		// test run, a child's gate run and the session's own gate below.
		CheckSlots:    cfg.Agents.CheckSlots,
		CheckCommands: gateCommands(session.gateRunner),
		// Children answer to the directories the person added on top of
		// their own worktree, which is where their file edits are already
		// pinned (RootArgs). This is what stops a child *command* writing
		// somewhere the parent never put in scope. It is Dirs and not All:
		// the parent's own checkout is the one directory a writer must not
		// reach except through its patch, and Dirs is also the set the
		// child's contained runner may write to, so the card and the
		// containment agree about what a child's scope is.
		// See docs/capabilities/subagents.md#a-child-inherits-its-scope-not-more.
		ScopeDirs: sc.Dirs,
		Untracked: untracked,
		LoadHandoff: func(handle string) ([]byte, error) {
			if db == nil {
				return nil, fmt.Errorf("failure handoffs are unavailable without session storage")
			}
			return db.LoadChildHandoff(handle)
		},
		SettleHandoff: func(handle string, content []byte) error {
			if db == nil {
				return nil
			}
			return db.UpdateChildHandoff(handle, content)
		},
		EvidenceExists: func(handle string) bool {
			if red == nil || red.Store() == nil {
				return false
			}
			_, err := red.Store().Info(handle)
			return err == nil
		},
	})
	// The session's own gate takes a slot from the same throttle, so the
	// person's run and a child's never load the machine at once.
	if session.gateRunner != nil {
		session.gateRunner.Slot = sup.CheckSlot
	}
	return sup
}

// childExecutors wires a child's two dispatchers on top of its base chain:
// the one its auto-run calls go through and the one an approved gated call
// goes through, both wrapped by the same repeat detector, which comes back
// too because the child's environment asks it what ground the child has
// covered.
func childExecutors(base agent.ToolExecutor, red *evidence.Reducer, ts *lsp.Toolset,
	hooks *hook.Runner) (autoExec, gatedExec agent.ToolExecutor, repeats *agent.RepeatDetector) {
	// Approved non-exec gated calls: file mutations dispatch through their
	// own path (never the auto-run executor), everything else falls back to
	// the child's base chain.
	gatedExec = agent.ToolExecutor(func(name string, args json.RawMessage) (string, error) {
		if tools.IsMutating(name) {
			return tools.ExecuteMutating(name, args)
		}
		return base(name, args)
	})
	autoExec = base
	if red != nil {
		autoExec = red.WrapExecutor(autoExec)
		gatedExec = red.WrapExecutor(gatedExec)
	}
	// Repeat detection, one detector per child so its window is
	// its own work, and shared across both paths so an approved call and
	// an auto-run one are the same history. A sub-agent is the least
	// supervised thing the session runs, and its rounds are spent out of
	// sight.
	repeats = agent.NewRepeatDetector()
	autoExec = repeats.WrapExecutor(autoExec)
	gatedExec = repeats.WrapExecutor(gatedExec)
	// An applied edit carries the language server's verdict on the file
	// it touched, as one applied on the session's own screen does — with
	// the session's queue of late answers left alone, which is what
	// childMutationHook is for.
	//
	// It sits outside the reduction rather than inside it, where the
	// session's own hook sits. A verdict is bounded by the server that
	// gave it and by this package's own caps, so there is nothing for the
	// reduction to do to it, and a block replaced by a notice saying an
	// id can be paged would cost the child the round the block was there
	// to save. The vault's scrub is outside both, so the text still
	// passes through it.
	//
	// The person's own post-tool hooks ride the same seam they ride in a
	// session: a write and an edit are dispatched through the mutating
	// tools, which is the one place a write can be seen, so a formatter
	// that runs after an edit goes on running after a child's edits too.
	gatedExec = withDiagnostics(
		chainMutation(childMutationHook(ts), childPostMutation(hooks)), gatedExec)
	return autoExec, gatedExec, repeats
}

// childStream is what a child's stream is built from: the spawn it serves,
// the session it inherits its effort, its vault and its supervisor from, and
// what newEnv already settled about it — the definitions it is offered, the
// model and the provider it bills through, and which of the two mode
// paragraphs are its to read.
type childStream struct {
	ctx             context.Context
	env             *sessionEnv
	agents          *agentProfiles
	vault           *secret.Vault
	sup             *subagent.Supervisor
	spec            subagent.Spec
	readOnlyExtra   []string
	defs            []provider.Tool
	model           string
	provider        provider.Provider
	refusable       bool
	commandsRefused bool
	// sandboxChild is a child in a sandbox session, whose commands are
	// refused for the container's reason rather than the host's.
	sandboxChild bool
}

// stream is the child's stream function. Each request is a fresh read of the
// effort and the mode, because both move under a running child.
func (c childStream) stream() agent.StreamFunc {
	return agent.StreamFunc(func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		sctx, cancel := context.WithCancel(c.ctx)
		// Children think as hard as the session does unless their
		// profile says otherwise: the level is a session setting, and
		// one that stopped at the orchestrator would be true of the
		// rail and false of the work.
		effort := c.env.effort
		if c.env.reasoning != nil {
			effort = c.env.reasoning()
		}
		effort = c.agents.effortFor(c.spec.Role, effort)
		msgs = c.vault.ScrubMessages(msgs)
		// The mode is read at each request rather than taken from the
		// spawn, because a child's mode moves under it: the parent's
		// ceiling changes, or the person sets it from the lane.
		if c.refusable && choice != provider.ToolChoiceNone {
			mode := c.spec.Mode
			if m, ok := c.sup.AgentMode(c.spec.Name); ok {
				mode = m
			}
			msgs = withModeInstructions(msgs, mode, c.readOnlyExtra, c.defs)
		}
		if c.commandsRefused && choice != provider.ToolChoiceNone {
			if c.sandboxChild {
				msgs = withSystemParagraph(msgs, prompt.SandboxChildInstructions)
			} else {
				msgs = withRefusedCommands(msgs)
			}
		}
		ev, sErr := c.provider.StreamCompletion(sctx, msgs, provider.CompletionOpts{
			Model:      c.model,
			Tools:      c.defs,
			ToolChoice: choice,
			Effort:     effort,
		})
		if sErr != nil {
			cancel()
			return nil, nil, sErr
		}
		return ev, cancel, nil
	})
}

// gateCommands answers the command lines the project's trusted quality config
// declares as checks, read fresh at each command the way a run reads it; nil
// where the checkout has no runner, which an untrusted one never does.
func gateCommands(r *quality.Runner) func() []string {
	if r == nil {
		return nil
	}
	return func() []string {
		cfg, err := quality.LoadConfig(r.Workspace)
		if err != nil {
			return nil
		}
		return cfg.Commands()
	}
}

// sessionUntracked lists the files the session itself created that git does
// not know about, so a writer's worktree can start from them the way it
// starts from everything `git diff HEAD` reports.
//
// The list is the session's own record and deliberately not `git status`:
// a checkout has untracked files nobody in this conversation put there — a
// scratch note, a core dump, a directory of build output — and carrying them
// into every writer's worktree would copy the person's desk rather than their
// work (docs/capabilities/subagents.md#a-writer-starts-from-your-tree).
//
// The paths are named the way the session named them, relative to where it is
// standing or absolute; the worktree resolves them against the repository.
func sessionUntracked(changes *changeset.Store) []string {
	var out []string
	seen := map[string]bool{}
	for _, turn := range changes.Turns() {
		for _, r := range turn.Records {
			// AfterExists, because a file the session created and then
			// deleted has nothing to carry, and the record still holds it.
			if r.Track != changeset.TrackUntracked || !r.AfterExists || seen[r.Path] {
				continue
			}
			seen[r.Path] = true
			out = append(out, r.Path)
		}
	}
	return out
}
