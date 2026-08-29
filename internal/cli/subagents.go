package cli

// Sub-agent orchestration wiring (S-068): `shhh code` registers spawn_agent /
// agent_report and hands the chat model a supervisor whose children reuse the
// session provider with role-scoped toolsets. Researchers get read-only tools
// plus the web against the real workspace; writers get the full toolset
// against an isolated git worktree, commands contained when a mechanism is
// available. Child sessions are recorded linked to the parent session so
// observability (S-065) attributes their spend.

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// buildSupervisor assembles the session's sub-agent supervisor.
func buildSupervisor(ctx context.Context, cfg config.Config, session chatSession, env *sessionEnv,
	red *evidence.Reducer, recorder *observeRecorder, db *storage.DB, prices *pricing.Table,
	classifier *agent.Classifier, sc *scope.Scope) *subagent.Supervisor {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}

	newEnv := func(cctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		role, croot := spec.Role, spec.Root
		info := shell.Detect()
		info.Cwd = croot
		extra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra, project.FindContext())

		var sysPrompt string
		var defs []provider.Tool
		gated := map[string]bool{}
		switch role {
		case subagent.RoleWriter:
			sysPrompt = prompt.BuildWriter(info, extra)
			// A writer that declared a scope is told about it: other agents
			// may be changing the rest of the repository at the same time.
			if len(spec.Paths) > 0 {
				sysPrompt += "\n\n# Scope\nYour changes are scoped to: " + strings.Join(spec.Paths, ", ") +
					". Other agents may be working elsewhere in the repository at the same time. Keep every change inside your scope; if the task appears to need a change outside it, describe that in your report instead of making it."
			}
			defs = tools.DefinitionsFull()
			gated[tools.ExecCommandName] = true
			gated[tools.WriteFileName] = true
			gated[tools.EditFileName] = true
		default:
			sysPrompt = prompt.BuildResearcher(info, extra)
			defs = tools.Definitions()
		}
		base := agent.ToolExecutor(tools.Execute)
		if session.web != nil {
			defs = append(defs, session.web.Definitions()...)
			base = session.web.WrapExecutor(tools.Execute)
			gated[web.FetchToolName] = true
		}
		if red != nil {
			defs = append(defs, evidence.ToolDefinition())
		}

		// Approved non-exec gated calls: file mutations dispatch through their
		// own path (never the auto-run executor), everything else falls back to
		// the child's base chain.
		gatedExec := agent.ToolExecutor(func(name string, args json.RawMessage) (string, error) {
			if tools.IsMutating(name) {
				return tools.ExecuteMutating(name, args)
			}
			return base(name, args)
		})
		autoExec := base
		if red != nil {
			autoExec = red.WrapExecutor(autoExec)
			gatedExec = red.WrapExecutor(gatedExec)
		}
		// Repeat detection (S-164), one detector per child so its window is
		// its own work, and shared across both paths so an approved call and
		// an auto-run one are the same history. A sub-agent is the least
		// supervised thing the session runs, and its rounds are spent out of
		// sight.
		repeats := agent.NewRepeatDetector()
		autoExec = repeats.WrapExecutor(autoExec)
		gatedExec = repeats.WrapExecutor(gatedExec)

		streamDefs := defs
		// The child's model is resolved by the supervisor (spawn argument →
		// role profile → agents.model → session model); an empty one here
		// would mean no resolution ran, so fall back to the session model.
		childModel := spec.Model
		if childModel == "" {
			childModel = env.modelName
		}
		stream := agent.StreamFunc(func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			sctx, cancel := context.WithCancel(cctx)
			// Children think as hard as the session does: the level is a
			// session setting, and one that stopped at the orchestrator
			// would be true of the rail and false of the work (S-139).
			effort := env.effort
			if env.reasoning != nil {
				effort = env.reasoning()
			}
			ev, sErr := env.prov.StreamCompletion(sctx, msgs, provider.CompletionOpts{
				Model:      childModel,
				Tools:      streamDefs,
				ToolChoice: "auto",
				Effort:     effort,
			})
			if sErr != nil {
				cancel()
				return nil, nil, sErr
			}
			return ev, cancel, nil
		})

		return subagent.Env{
			SystemPrompt: sysPrompt,
			Stream:       stream,
			Executor:     subagent.RootedExecutor(croot, autoExec),
			ExecuteGated: gatedExec,
			RunCommand:   childCommandRunner(cfg, croot, sc),
			Gated:        gated,
		}, nil
	}

	return subagent.New(ctx, subagent.Options{
		Root:   root,
		NewEnv: newEnv,
		Record: func(role subagent.Role) subagent.Recorder {
			r := startChildObserveRecorder(db, string(role), env.prov.Name(), env.modelName, prices, recorder.sessionID())
			return subagent.Recorder{Usage: r.usage, ToolCall: r.toolCallOutcome, End: r.end}
		},
		CommandAllowlist: cfg.Behavior.CommandAllowlist,
		ReadOnlyExtra:    cfg.Behavior.ReadOnlyCommands,
		ReadOnlyDisabled: !cfg.ReadOnlyAutoEnabled(),
		// Children get the same auto-mode classifier the parent uses, so an
		// auto-mode session does not turn into one prompt per child command.
		Classifier: classifier,
		ModelFor: func(role subagent.Role, requested string) string {
			if requested != "" {
				return requested
			}
			return cfg.AgentModel(string(role), env.modelName)
		},
		MaxConcurrent: cfg.Agents.MaxConcurrent,
		// Children answer to the parent's working scope (S-141) on top of
		// their own worktree, which is where their file edits are already
		// pinned (RootArgs). This is what stops a child *command* writing
		// somewhere the parent never put in scope.
		ScopeDirs: sc.All,
	})
}

// childCommandRunner builds the execute_command runner for a sub-agent rooted
// at dir: contained with the workspace grant moved to dir when a mechanism is
// available, plain with cwd=dir otherwise (matching the parent session's
// uncontained fallback).
func childCommandRunner(cfg config.Config, dir string, sc *scope.Scope) func(context.Context, string) (string, int) {
	if _, err := sandboxPolicy(cfg); err == nil {
		if avail := sandbox.Detect(); avail.OK {
			return func(ctx context.Context, command string) (string, int) {
				// The policy is rebuilt per command so a directory the parent
				// added mid-session (S-141) is writable in the child too.
				p, pErr := sandboxPolicy(cfg, sc.Dirs()...)
				if pErr != nil {
					return "sandbox: " + pErr.Error(), -1
				}
				p.Workspace = dir
				p.Cwd = dir
				argv, wErr := sandbox.Wrap(avail, p, command)
				if wErr != nil {
					return "sandbox: " + wErr.Error(), -1
				}
				return runner.RunCaptureArgvIn(ctx, dir, argv)
			}
		}
	}
	return func(ctx context.Context, command string) (string, int) {
		return runner.RunCaptureIn(ctx, dir, command)
	}
}
