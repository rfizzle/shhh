package cli

// Sub-agent orchestration wiring: `shhh code` registers spawn_agent /
// agent_report and hands the chat model a supervisor whose children reuse the
// session provider with role-scoped toolsets. Researchers get read-only tools
// plus the web against the real workspace; writers get the full toolset
// against an isolated git worktree, commands contained when a mechanism is
// available. Child sessions are recorded linked to the parent session so
// observability attributes their spend.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// agentProfiles is the session's spawnable roles: the supervisor's view
// (worktree, mode, budgets) and, for the ones read from files, the full
// definition the child environment is built from. The built-in researcher
// and writer have no definition and keep their hand-written prompts.
type agentProfiles struct {
	profiles    subagent.Profiles
	definitions map[string]config.AgentDefinition
}

// loadAgentProfiles reads the user's agent profiles and lays them over the
// built-in roles. A file named researcher.toml or writer.toml replaces the
// built-in of that name, which is how the shipped roles get a different
// model, mode or prompt without a config key per field. The mode and
// reasoning names are checked here rather than in the loader because the
// loader is config plumbing and these are the agent's and provider's
// vocabularies (docs/capabilities/subagents.md#a-profile-is-a-file).
//
// A coding session reads the project's own profiles first; a conversation
// reads only the global ones, because a persona is the person's and not any
// project's (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
func loadAgentProfiles(projectScoped bool) (*agentProfiles, error) {
	var (
		defs map[string]config.AgentDefinition
		err  error
	)
	if projectScoped {
		cwd, wdErr := os.Getwd()
		if wdErr != nil {
			cwd = "."
		}
		defs, err = config.LoadAgentsFor(cwd)
	} else {
		defs, err = config.LoadAgents()
	}
	if err != nil {
		return nil, err
	}
	out := &agentProfiles{profiles: subagent.BuiltinProfiles(), definitions: defs}
	for _, def := range defs {
		p, err := profileFromDefinition(def)
		if err != nil {
			return nil, err
		}
		out.profiles[p.Name] = p
	}
	return out, nil
}

// profileFromDefinition is the supervisor's view of a profile file, with
// the mode and reasoning names checked.
func profileFromDefinition(def config.AgentDefinition) (subagent.Profile, error) {
	p := subagent.Profile{
		Name:        subagent.Role(def.Name),
		Description: def.Description,
		Writes:      def.Writes(),
		MaxTokens:   def.MaxTokens,
		MaxRounds:   def.MaxRounds,
	}
	if strings.TrimSpace(def.Mode) != "" {
		mode, err := agent.ParseMode(def.Mode)
		if err != nil {
			return p, fmt.Errorf("agent profile %s: mode: %w", def.Path, err)
		}
		p.Mode, p.HasMode = mode, true
	}
	if !def.InheritsReasoning() {
		if _, err := provider.ParseEffort(def.Reasoning); err != nil {
			return p, fmt.Errorf("agent profile %s: reasoning: %w", def.Path, err)
		}
	}
	return p, nil
}

// readers is the subset of profiles that can change nothing: what a
// conversation may spawn. The definitions map is kept whole — a reader's
// model and prompt still come from its file.
func (a *agentProfiles) readers() *agentProfiles {
	out := &agentProfiles{profiles: subagent.Profiles{}, definitions: a.definitions}
	for name, p := range a.profiles {
		if !p.Writes {
			out.profiles[name] = p
		}
	}
	return out
}

// effortFor is the reasoning level a child runs at: the profile's own when
// it names one, otherwise the session's live level — a level set with
// alt+t is true of the session and so of every child it spawns.
func (a *agentProfiles) effortFor(role subagent.Role, session provider.Effort) provider.Effort {
	if a == nil {
		return session
	}
	def, ok := a.definitions[string(role)]
	if !ok || def.InheritsReasoning() {
		return session
	}
	effort, err := provider.ParseEffort(def.Reasoning)
	if err != nil {
		return session // validated at load; unreachable
	}
	return effort
}

// modelFor is the model a child runs on: the spawn's own request, then the
// profile file's, then the [agents] config layer, then the session model.
func (a *agentProfiles) modelFor(cfg config.Config, role subagent.Role, requested, sessionModel string) string {
	if requested != "" {
		return requested
	}
	if a != nil {
		if def, ok := a.definitions[string(role)]; ok {
			if m := def.ProfileModel(); m != "" {
				return m
			}
		}
	}
	return cfg.AgentModel(string(role), sessionModel)
}

// buildSupervisor assembles the session's sub-agent supervisor.
func buildSupervisor(ctx context.Context, cfg config.Config, session chatSession, env *sessionEnv, agents *agentProfiles,
	red *evidence.Reducer, recorder *observeRecorder, db *storage.DB, prices *pricing.Table,
	classifier *agent.Classifier, sc *scope.Scope, ledger *meter.Ledger) *subagent.Supervisor {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	if agents == nil {
		agents = &agentProfiles{profiles: subagent.BuiltinProfiles()}
	}

	newEnv := func(cctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		role, croot := spec.Role, spec.Root
		info := shell.Detect()
		info.Cwd = croot
		extra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra, project.FindContext())

		var sysPrompt string
		var defs []provider.Tool
		gated := map[string]bool{}
		base := agent.ToolExecutor(tools.Execute)
		if def, ok := agents.definitions[string(role)]; ok {
			sysPrompt, defs, base = profileEnv(def, spec, info, extra, session.web, gated)
		} else {
			switch role {
			case subagent.RoleReviewer:
				sysPrompt = prompt.BuildReviewer(info, extra)
				defs = tools.Definitions()
			case subagent.RoleWriter:
				sysPrompt = prompt.BuildWriter(info, extra)
				sysPrompt += scopeNote(spec.Paths)
				defs = tools.DefinitionsFull()
				gated[tools.ExecCommandName] = true
				gated[tools.WriteFileName] = true
				gated[tools.EditFileName] = true
			default:
				sysPrompt = prompt.BuildResearcher(info, extra)
				defs = tools.Definitions()
			}
			if session.web != nil {
				defs = append(defs, session.web.Definitions()...)
				base = session.web.WrapExecutor(tools.Execute)
				gated[web.FetchToolName] = true
			}
		}
		if red != nil {
			defs = append(defs, evidence.ToolDefinition())
		}
		// The report tool is deliberately not here. A report is the
		// session's answer surface to the user; a child answers its parent,
		// and a page the user is never handed a link to is spent tokens.
		// What a child found reaches a page through the parent's own report
		// call, the same way it reaches the transcript.
		// Children see the same skills the session does: a writer told to
		// follow the project's documentation skill has to be able to read
		// it, and the catalog is a read whatever the child's tier.
		if session.skills.Len() > 0 {
			defs = append(defs, skill.ToolDefinition(session.skills))
			base = session.skills.WrapExecutor(base)
			sysPrompt = prompt.CombineExtra(sysPrompt, skill.PromptBlock(session.skills))
		}
		// The shared notebook, signed with the child's name, and the
		// titles already in it so the child starts by reading rather than
		// re-finding (docs/capabilities/chat.md#what-they-share).
		if session.notebook != nil {
			defs = append(defs, notebook.Definitions()...)
			base = session.notebook.WrapExecutor(spec.Name, base)
			sysPrompt = prompt.CombineExtra(sysPrompt, notebook.PromptBlock(session.notebook.List()))
		}
		// The servers the person marked read-only are reads, and a child
		// gets them the way it gets the skills catalog. Every other
		// server's tools need a card, and a child has no card of its own
		// (docs/capabilities/mcp.md#what-a-conversation-may-reach).
		if session.mcpTools != nil {
			if ro := session.mcpTools.ReadOnlyDefinitions(); len(ro) > 0 {
				defs = append(defs, ro...)
				base = session.mcpTools.WrapReadOnlyExecutor(base)
				sysPrompt = prompt.CombineExtra(sysPrompt, mcp.ReadOnlyPromptBlock(session.mcpTools))
			}
		}
		// Secrets are read at spawn rather than at session start, so a
		// child knows what /secret added since.
		if session.vault.Len() > 0 {
			sysPrompt = prompt.CombineExtra(sysPrompt, secret.PromptBlock(session.vault))
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
		// Repeat detection, one detector per child so its window is
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
		// Each child bills itself. A fan-out is the one place where several
		// requesters spend at once, so "sub-agents" as a class is not a fine
		// enough answer to which of them spent it.
		childProvider := ledger.ForOrigin(env.prov, meter.Origin{Source: meter.SourceSubagent, Label: spec.Name})

		stream := agent.StreamFunc(func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			sctx, cancel := context.WithCancel(cctx)
			// Children think as hard as the session does unless their
			// profile says otherwise: the level is a session setting, and
			// one that stopped at the orchestrator would be true of the
			// rail and false of the work.
			effort := env.effort
			if env.reasoning != nil {
				effort = env.reasoning()
			}
			effort = agents.effortFor(role, effort)
			msgs = session.vault.ScrubMessages(msgs)
			ev, sErr := childProvider.StreamCompletion(sctx, msgs, provider.CompletionOpts{
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
			Executor:     session.vault.WrapExecutor(subagent.RootedExecutor(croot, autoExec)),
			ExecuteGated: session.vault.WrapExecutor(gatedExec),
			RunCommand:   scrubRunner(session.vault, childCommandRunner(cfg, croot, sc)),
			Gated:        gated,
			Scrub:        session.vault.ScrubMessage,
		}, nil
	}

	return subagent.New(ctx, subagent.Options{
		Root:   root,
		NewEnv: newEnv,
		Record: func(spec subagent.Spec) subagent.Recorder {
			// A child is recorded against the model it actually ran on. The
			// session model is the wrong one to price it at: agents.model and
			// a per-spawn model both routinely send children somewhere
			// cheaper, and a row priced at the parent's rate overstates them.
			model := spec.Model
			if model == "" {
				model = env.modelName
			}
			r := startChildObserveRecorder(db, string(spec.Role), env.prov.Name(), model, prices, recorder.sessionID())
			return subagent.Recorder{Usage: r.usage, ToolCall: r.toolCallOutcome, End: r.end}
		},
		CommandAllowlist: cfg.Behavior.CommandAllowlist,
		ReadOnlyExtra:    cfg.Behavior.ReadOnlyCommands,
		ReadOnlyDisabled: !cfg.ReadOnlyAutoEnabled(),
		// Children get the same auto-mode classifier the parent uses, so an
		// auto-mode session does not turn into one prompt per child command.
		Classifier: classifier,
		ModelFor: func(role subagent.Role, requested string) string {
			return agents.modelFor(cfg, role, requested, env.modelName)
		},
		Profiles:      agents.profiles,
		MaxConcurrent: cfg.Agents.MaxConcurrent,
		// Children answer to the parent's working scope on top of
		// their own worktree, which is where their file edits are already
		// pinned (RootArgs). This is what stops a child *command* writing
		// somewhere the parent never put in scope.
		ScopeDirs: sc.All,
	})
}

// scopeNote tells a child that declared a write scope about it: other
// agents may be changing the rest of the repository at the same time.
func scopeNote(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return "\n\n# Scope\nYour changes are scoped to: " + strings.Join(paths, ", ") +
		". Other agents may be working elsewhere in the repository at the same time. Keep every change inside your scope; if the task appears to need a change outside it, describe that in your report instead of making it."
}

// profileEnv builds a custom profile's prompt, toolset and auto-run
// executor from its definition. The toolset is the tiers the profile
// granted, narrowed by its allowlist; the prompt is either the generic
// profile prompt with the file's instructions appended, or the file's
// instructions alone when it asked to replace the base. Gated is filled
// with the approval-routed tools that made it in.
func profileEnv(def config.AgentDefinition, spec subagent.Spec, info shell.Info, extra string,
	webTools *web.Toolset, gated map[string]bool) (string, []provider.Tool, agent.ToolExecutor) {
	var defs []provider.Tool
	for _, t := range tools.Definitions() {
		if def.Allows(t.Name) {
			defs = append(defs, t)
		}
	}
	if def.Has(config.PermissionWrite) {
		for _, d := range tools.Mutating() {
			if def.Allows(d.Tool.Name) {
				defs = append(defs, d.Tool)
				gated[d.Tool.Name] = true
			}
		}
	}
	if def.Has(config.PermissionExecute) && def.Allows(tools.ExecCommandName) {
		defs = append(defs, tools.ExecCommandTool())
		gated[tools.ExecCommandName] = true
	}
	base := agent.ToolExecutor(tools.Execute)
	if def.Has(config.PermissionWeb) && webTools != nil {
		var admitted []provider.Tool
		for _, t := range webTools.Definitions() {
			if def.Allows(t.Name) {
				admitted = append(admitted, t)
			}
		}
		if len(admitted) > 0 {
			defs = append(defs, admitted...)
			base = webTools.WrapExecutor(tools.Execute)
			if def.Allows(web.FetchToolName) {
				gated[web.FetchToolName] = true
			}
		}
	}

	names := make([]string, len(defs))
	for i, t := range defs {
		names[i] = t.Name
	}
	var sysPrompt string
	if strings.EqualFold(strings.TrimSpace(def.PromptMode), config.PromptReplace) {
		sysPrompt = strings.TrimSpace(def.Prompt)
		if extra != "" {
			sysPrompt += "\n\n" + extra
		}
	} else {
		sysPrompt = prompt.BuildProfile(info, prompt.ProfileSpec{
			Name:        def.Name,
			Description: def.Description,
			Write:       def.Has(config.PermissionWrite),
			Execute:     def.Has(config.PermissionExecute),
			Web:         def.Has(config.PermissionWeb),
			Tools:       names,
			Isolated:    def.Writes(),
		}, prompt.CombineExtra(strings.TrimSpace(def.Prompt), extra))
	}
	if def.Writes() {
		sysPrompt += scopeNote(spec.Paths)
	}
	return sysPrompt, defs, base
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
				// added mid-session is writable in the child too.
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
