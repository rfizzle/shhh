package cli

import (
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// scopeNote tells a child that declared a write scope about it: other
// agents may be changing the rest of the repository at the same time.
func scopeNote(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return "\n\n# Scope\nYour changes are scoped to: " + strings.Join(paths, ", ") +
		". Other agents may be working elsewhere in the repository at the same time. Keep every change inside your scope; if the task appears to need a change outside it, describe that in your report instead of making it."
}

// childExtra is the standing context every child is given on top of its role
// prompt: what the config file adds to every prompt here, what the project
// says about itself, what this project and this person have asked for before,
// the checkout as it stands now, and where in it the child is standing.
//
// The workspace block is read again for each child rather than taken from the
// session's own prompt: a child spawned an hour in is being sent to look at
// the tree as it is then, and the branch and the dirty count the session
// opened on are the two facts most likely to have moved since. The
// instructions are the other way round — read once for the session, because
// they are files on disk that a spawn has no reason to have changed, and
// re-reading them cost every spawn, retry and handoff a full pass over them.
// The memories are the session's own recall for the same reason and one
// more: they are what the person told this session, and a child that queried
// the table for itself could be working from something the session it serves
// was never told (memory.go).
func childExtra(configExtra, instructions, memory, workspace string, worktree bool) string {
	return prompt.CombineExtra(configExtra, instructions, memory, workspace, worktreeNote(worktree))
}

// worktreeNote tells a child standing in an isolated copy of the checkout
// what git in there will tell it, because the workspace block above it
// describes the parent's directory and the two do not agree.
//
// The copy is stood on a commit of its own that already holds the parent's
// uncommitted work, which is what keeps the patch it hands back its own work
// alone. That leaves a child whose HEAD is on no branch and whose tree is
// clean, reading a block that names a branch and a count of changed paths.
// Unexplained, the child goes looking for the changes it was promised, finds
// none, and reports the workspace as wrong.
func worktreeNote(worktree bool) string {
	if !worktree {
		return ""
	}
	return "You are not standing in that directory: your workspace is an isolated copy of it, " +
		"and the facts above describe the checkout it was copied from. " +
		"Your own HEAD is a commit made for this copy that already contains those uncommitted changes, so it belongs to no branch and your tree reads as clean. " +
		"That is expected — do not commit, switch branches, or try to reconcile the two."
}

// profileEnv builds a custom profile's prompt, toolset and auto-run
// executor from its definition. The toolset is the tiers the profile
// granted, narrowed by its allowlist; the prompt is the generic profile
// prompt with the file's instructions appended, the reviewer's prompt with
// them appended when the profile reviews, or the file's instructions alone
// when it asked to replace the base. Gated is filled with the
// approval-routed tools that made it in.
//
// The prompt comes back as a function of the names the child ends up
// holding rather than as text, because these are not all of them: the
// delegation tools and everything the session shares with every child go on
// afterwards, and withSessionTools composes the prompt over the finished set.
func profileEnv(def config.AgentDefinition, spec subagent.Spec, info shell.Info, extra string,
	webTools *web.Toolset, gate *quality.Runner, gated map[string]bool) (func(names []string) string, []provider.Tool, agent.ToolExecutor) {
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
		defs = append(defs, execCommandDefinition(def))
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
			base = webTools.WrapExecutor(spec.Name, tools.Execute)
			if def.Allows(web.FetchToolName) {
				gated[web.FetchToolName] = true
			}
		}
	}
	// The project's own checks, and not part of the execute tier: the tool
	// names a suite out of the checkout's trusted config and can supply no
	// command text, so a role that must never run an arbitrary command can
	// still say whether the change compiles and its tests pass. It is
	// offered only where the session itself has a runner — an untrusted
	// checkout opens none — and only to a profile that changes nothing,
	// because a writer works in a copy of the checkout the runner is
	// pointed at and would be handed a verdict on a tree without its own
	// changes in it. Auto-run, so it is not added to gated: a child has no
	// approval card, and this is the one command-shaped tool that needs
	// none. It goes on after the web branch, which replaces the executor
	// rather than wrapping it.
	// See docs/capabilities/subagents.md#a-profile-that-changes-nothing-can-still-run-the-checks.
	if gate != nil && !def.Writes() && def.Allows(config.QualityGateTool) {
		defs = append(defs, quality.ToolDefinition())
		// Holding, because the supervisor takes the child's check slot in
		// front of this call and draws the wait on its lane; the runner
		// taking a second one behind it would wait on itself.
		base = gate.WrapExecutorHolding(base)
	}
	return func(names []string) string { return profilePrompt(def, spec, info, extra, names) }, defs, base
}

// execCommandDefinition is the command tool as a profile's child is told it:
// the description names edit_file as the way to change a file, which is
// true only where the profile was granted it.
func execCommandDefinition(def config.AgentDefinition) provider.Tool {
	if def.Has(config.PermissionWrite) && def.Allows(tools.EditFileName) {
		return tools.ExecCommandTool()
	}
	return tools.ExecCommandToolNoEdits()
}

// builtinEnv is the environment of a built-in role no profile file
// replaced: its prompt, its tool definitions and the executor they dispatch
// through. Gated is filled with the approval-routed tools it holds.
//
// The reviewer is not handed the web. Its mode is read-only, which refuses
// every fetch whatever host it names, and a review judges a diff against the
// tree it lands in rather than against a page; offered the tools, it would
// spend a round learning the refusal its prompt never warned of.
// See docs/capabilities/subagents.md#two-kinds-and-the-difference-is-what-they-may-touch.
func builtinEnv(role subagent.Role, spec subagent.Spec, info shell.Info, extra string,
	webTools *web.Toolset, gated map[string]bool) (string, []provider.Tool, agent.ToolExecutor) {
	var sysPrompt string
	var defs []provider.Tool
	base := agent.ToolExecutor(tools.Execute)
	switch role {
	case subagent.RoleReviewer:
		return prompt.BuildReviewer(info, prompt.ProfileSpec{}, extra), tools.Definitions(), base
	case subagent.RoleWriter:
		sysPrompt = prompt.BuildWriter(info, extra)
		sysPrompt += scopeNote(spec.Paths)
		defs = tools.DefinitionsFull()
		gated[tools.ExecCommandName] = true
		gated[tools.WriteFileName] = true
		gated[tools.EditFileName] = true
	default:
		// The child is told which half of the web it has before it is
		// registered below, because the sentence is part of the prompt and
		// the prompt is built once.
		sysPrompt = prompt.BuildResearcher(info, webToolsFor(webTools), extra)
		defs = tools.Definitions()
	}
	if webTools != nil {
		defs = append(defs, webTools.Definitions()...)
		base = webTools.WrapExecutor(spec.Name, tools.Execute)
		gated[web.FetchToolName] = true
	}
	return sysPrompt, defs, base
}

// profilePrompt is a profile's prompt over the names the child holds. The
// tool section is read off those names, so it is only as true as the list:
// handed the profile's grants alone, it would tell a child with the notebook
// and a server's reads that it has neither.
func profilePrompt(def config.AgentDefinition, spec subagent.Spec, info shell.Info, extra string, names []string) string {
	own := strings.TrimSpace(def.Prompt) + commandsSection(def, names)
	var sysPrompt string
	switch {
	case strings.EqualFold(strings.TrimSpace(def.PromptMode), config.PromptReplace):
		sysPrompt = own
		if extra != "" {
			sysPrompt += "\n\n" + extra
		}
	// A reviewing profile is run as a review: it is handed the change ahead
	// of its task and stopped at its round cap with a request for its
	// report. Its permissions say only that it changes nothing, so the
	// generic prompt they select is the reader's — asked for findings and
	// evidence, told nothing about ranking by severity, about reading the
	// declared evidence before anything else, or about ending on a verdict.
	// The child would be run under one contract and instructed in another,
	// and the instructions are the half it can act on. The file's own
	// prompt follows the reviewer's the way it follows the reader's, and the
	// name, the purpose and the toolset go with it: a review is still this
	// profile, holding what this profile was registered with.
	// See docs/capabilities/subagents.md#a-review-is-bounded-by-what-it-is-given.
	case def.Reviews:
		sysPrompt = prompt.BuildReviewer(info, prompt.ProfileSpec{
			Name:        def.Name,
			Description: def.Description,
			Tools:       names,
		}, prompt.CombineExtra(own, extra))
	default:
		sysPrompt = prompt.BuildProfile(info, prompt.ProfileSpec{
			Name:        def.Name,
			Description: def.Description,
			Write:       def.Has(config.PermissionWrite),
			Execute:     def.Has(config.PermissionExecute),
			Tools:       names,
			Isolated:    def.Writes(),
		}, prompt.CombineExtra(own, extra))
	}
	if def.Writes() {
		sysPrompt += scopeNote(spec.Paths)
	}
	return sysPrompt
}

// commandsSection is the profile's Commands fields as the child reads them,
// under the prompt it wrote: what its commands are for and what it must
// never run. It is there only where the child holds the command tool — a
// child that runs nothing has nothing to read it against — and says the
// refusals are refused whatever the spelling, which is true because they
// are matched the way the person's own deny list is.
// See docs/capabilities/subagents.md#a-profile-is-a-file.
func commandsSection(def config.AgentDefinition, names []string) string {
	intent := strings.TrimSpace(def.Intent)
	var deny []string
	for _, d := range def.Deny {
		if d = strings.TrimSpace(d); d != "" {
			deny = append(deny, "`"+d+"`")
		}
	}
	if (intent == "" && len(deny) == 0) || !slices.Contains(names, tools.ExecCommandName) {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Commands\n")
	if intent != "" {
		b.WriteString("\nYour commands are for: " + intent + "\n")
	}
	if len(deny) > 0 {
		b.WriteString("\nNever run a command beginning " + strings.Join(deny, ", ") + ". Each is refused however it is spelled or wrapped.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// webToolsFor is what a child is told about the web: both tools where a
// search backend is configured, fetch alone where none is, and neither where
// the session registered no web tools at all.
func webToolsFor(ts *web.Toolset) prompt.WebTools {
	if ts == nil {
		return prompt.WebTools{}
	}
	return prompt.WebTools{Fetch: true, Search: ts.Searcher != nil}
}
