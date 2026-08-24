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

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// buildSupervisor assembles the session's sub-agent supervisor.
func buildSupervisor(ctx context.Context, cfg config.Config, session chatSession, env *sessionEnv,
	red *evidence.Reducer, recorder *observeRecorder, db *storage.DB, prices *pricing.Table) *subagent.Supervisor {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}

	newEnv := func(cctx context.Context, role subagent.Role, croot string) (subagent.Env, error) {
		info := shell.Detect()
		info.Cwd = croot
		extra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra, project.FindContext())

		var sysPrompt string
		var defs []provider.Tool
		gated := map[string]bool{}
		switch role {
		case subagent.RoleWriter:
			sysPrompt = prompt.BuildWriter(info, extra)
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

		streamDefs := defs
		stream := agent.StreamFunc(func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			sctx, cancel := context.WithCancel(cctx)
			ev, sErr := env.prov.StreamCompletion(sctx, msgs, provider.CompletionOpts{
				Model:      env.modelName,
				Tools:      streamDefs,
				ToolChoice: "auto",
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
			RunCommand:   childCommandRunner(cfg, croot),
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
	})
}

// childCommandRunner builds the execute_command runner for a sub-agent rooted
// at dir: contained with the workspace grant moved to dir when a mechanism is
// available, plain with cwd=dir otherwise (matching the parent session's
// uncontained fallback).
func childCommandRunner(cfg config.Config, dir string) func(context.Context, string) (string, int) {
	policy, err := sandboxPolicy(cfg)
	if err == nil {
		if avail := sandbox.Detect(); avail.OK {
			p := policy
			p.Workspace = dir
			p.Cwd = dir
			return func(ctx context.Context, command string) (string, int) {
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
