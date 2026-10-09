package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// replaceDefinitions swaps each definition in defs for the one of the same
// name in fresh, leaving its place and every other definition as they were.
func replaceDefinitions(defs, fresh []provider.Tool) []provider.Tool {
	byName := make(map[string]provider.Tool, len(fresh))
	for _, t := range fresh {
		byName[t.Name] = t
	}
	out := make([]provider.Tool, 0, len(defs))
	for _, t := range defs {
		if f, ok := byName[t.Name]; ok {
			t = f
		}
		out = append(out, t)
	}
	return out
}

// withNotebook wires a child into the session's shared notebook: the two
// tools, an executor that signs what it writes with the signature the child
// answers to, and the titles already in the notebook, so the child starts by
// reading rather than re-finding.
//
// It is one call outside every role branch, because every child gets this
// and no child gets a variant of it. A writer works in an isolated copy of
// the checkout and still writes into the parent's notebook — the notebook
// belongs to the session, not to a tree — and no child gets a way to remove
// anything from it, because there is no such tool to hand out.
// See docs/capabilities/subagents.md#what-they-share.
func withNotebook(nb *notebook.Store, signature string, defs []provider.Tool, base agent.ToolExecutor, sysPrompt string) (
	[]provider.Tool, agent.ToolExecutor, string) {
	if nb == nil {
		return defs, base, sysPrompt
	}
	defs = append(defs, notebook.Definitions()...)
	base = nb.WrapExecutor(signature, base)
	return defs, base, prompt.CombineExtra(sysPrompt, notebook.PromptBlock(nb.List()))
}

// notebookSignature is what a child signs a note with: its own name where
// the session spawned it, and its lineage from the root child down where an
// agent did. A grandchild's bare name says which agent wrote a note and
// nothing about whose task it was written under, and the task is the half a
// reader coming back to the notebook is missing.
// See docs/capabilities/subagents.md#what-nesting-does-to-the-rest-of-it.
//
// The walk up is bounded by the deepest agent the session allows, for the
// reason the rail's walk is bounded by the number of agents: parent links
// that ever came to point in a circle would hang the spawn rather than sign
// one note oddly.
func notebookSignature(sup *subagent.Supervisor, spec subagent.Spec) string {
	lineage := []string{spec.Name}
	if sup == nil {
		return notebook.Signature(lineage)
	}
	for at := spec.Parent; at != "" && len(lineage) < sup.MaxDepth(); {
		lineage = append([]string{at}, lineage...)
		parent, ok := sup.Parent(at)
		if !ok {
			break
		}
		at = parent
	}
	return notebook.Signature(lineage)
}

// withDelegation puts the orchestration tools on a child that has a level
// below it, so a task with parts can be split one level down the way it is
// at the top. The wrap takes the child's own name: that is what is written
// as the parent of anything it spawns, and what bounds the other three tools
// to the agents this one started.
// See docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth.
//
// A child at the deepest level is handed none of them rather than a spawn
// that always refuses. The refusal exists and is what a race or a caller in
// code meets, but a tool a child can only ever be told no by is a round and
// a schema it pays for and can never use — the same reason the web tools
// appear only where the session has them.
//
// The four are one grant, named by either of the two the profile format
// lists. An agent that could start a child but not collect it would spend a
// slot on a report it can never read, and one that could collect but not
// spawn has nothing to collect; splitting them buys a profile author no
// choice worth having.
//
// offer is the session's list as it stands at the spawn: a child names the
// same models its parent may, and is refused the same others.
func withDelegation(sup *subagent.Supervisor, agents *agentProfiles, offer subagent.Offer, def config.AgentDefinition,
	spec subagent.Spec, defs []provider.Tool, base agent.ToolExecutor, gated map[string]bool) (
	[]provider.Tool, agent.ToolExecutor) {
	if sup == nil || spec.Depth >= sup.MaxDepth() {
		return defs, base
	}
	if !def.Allows(subagent.SpawnToolName) && !def.Allows(subagent.ReportToolName) {
		return defs, base
	}
	defs = append(defs, subagent.Definitions(agents.snapshot(), offer)...)
	// Starting an agent is a decision wherever it is taken, so a child's
	// spawn is carded like its commands and its edits and reaches the person
	// the same way. The other three start nothing and are auto-run, as they
	// are for the session (docs/capabilities/subagents.md#spawning-is-a-decision).
	gated[subagent.SpawnToolName] = true
	return defs, sup.WrapExecutor(spec.Name, base)
}

// applyDelegation puts agents.delegation on a session before anything is
// registered. Off takes the orchestration tools away entirely, the way a
// session with nobody to answer the spawn card never has them: a tool the
// model may never use is one it should not be shown. The other two leave the
// tools and the card where they were and differ only in the toolbox line,
// because the policy is the model's side of the decision — when to ask — and
// the card is the person's answer.
// See docs/capabilities/subagents.md#spawning-is-a-decision.
func applyDelegation(cfg config.Config, session *chatSession) {
	policy := cfg.AgentDelegation()
	session.agents = session.agents && policy != config.DelegationOff
	session.proactive = policy == config.DelegationProactive
}

// delegationWords is the policy as a surface states it: the word the
// settings file takes and what it does, so a reader of /status or a
// headless run's stderr does not have to go and look the word up.
func delegationWords(policy string) string {
	switch policy {
	case config.DelegationOff:
		return "off — no sub-agents are offered (agents.delegation)"
	case config.DelegationProactive:
		return "proactive — work that divides is divided; each spawn still asks (agents.delegation)"
	}
	return "explicit — sub-agents when asked for; each spawn asks (agents.delegation)"
}

// withSessionTools puts on a child everything the session shares with every
// child, whatever its role and whatever its profile granted: the navigation
// toolset, the evidence tool, the skills catalog, the notebook, the servers
// the person marked read-only, what the vault will scrub — and last, over the
// set all of that produced, the toolbox that says what each of them is for.
//
// It returns the definitions, the auto-run chain, the prompt and the trim's
// keep-back rule. It is one function rather than a run of branches inside the
// spawn closure because the toolbox has to be built over the finished set: a
// block assembled halfway through would describe a toolset the child does not
// have, which is the one failure the toolbox exists to prevent.
//
// The report tool is deliberately not among them. A report is the session's
// answer surface to the user; a child answers its parent, and a page the user
// is never handed a link to is spent tokens. What a child found reaches a
// page through the parent's own report call, the same way it reaches the
// transcript.
func withSessionTools(session chatSession, red *evidence.Reducer, signature, croot string,
	defs []provider.Tool, base agent.ToolExecutor, rolePrompt func(names []string) string) (
	[]provider.Tool, agent.ToolExecutor, string, func(string) bool) {
	// The blocks below follow the role's own prompt, which is composed last
	// for the same reason the toolbox is.
	var sysPrompt string
	defs, base = withNavigation(session.lsp, childStructural(session.structural, croot), defs, base)
	if red != nil {
		defs = append(defs, evidence.ToolDefinition())
	}
	// Children see the same skills the session does: a writer told to follow
	// the project's documentation skill has to be able to read it, and the
	// catalog is a read whatever the child's tier — and its instructions have
	// to survive the child's window trim, the same as they survive the
	// session's.
	var keepResult func(string) bool
	if session.skills.Len() > 0 {
		defs = append(defs, skill.ToolDefinition(session.skills))
		base = session.skills.WrapExecutor(base)
		sysPrompt = prompt.CombineExtra(sysPrompt, skill.PromptBlock(session.skills))
		keepResult = skill.IsContent
	}
	defs, base, sysPrompt = withNotebook(session.notebook, signature, defs, base, sysPrompt)
	// Every role keeps a working list its lane is counted by; the supervisor
	// answers the call on the child's own (internal/subagent/steps.go).
	defs = append(defs, plan.StepsToolDefinition())
	// The servers the person marked read-only are reads, and a child gets
	// them the way it gets the skills catalog. Every other server's tools
	// need a card, and a child has no card of its own
	// (docs/capabilities/mcp.md#what-a-conversation-may-reach).
	//
	// The servers are the ones that have joined when the child is spawned,
	// read with their block in one look, and the child keeps that set: a
	// server that joins later is the parent's from its next turn, and a
	// child that learned one of its names from its task cannot reach it
	// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	if ts := session.mcpTools; ts != nil {
		if ro, block := ts.ReadOnlyView(); len(ro) > 0 {
			defs = append(defs, ro...)
			held := make(map[string]bool, len(ro))
			for _, d := range ro {
				held[d.Name] = true
			}
			chain := ts.WrapReadOnlyExecutor(signature, base)
			base = func(name string, args json.RawMessage) (string, error) {
				if !held[name] && ts.Has(name) {
					return "", fmt.Errorf("%s is not available to this agent: its server joined the session after this agent started", name)
				}
				return chain(name, args)
			}
			sysPrompt = prompt.CombineExtra(sysPrompt, block)
		}
	}
	// Secrets are read at spawn rather than at session start, so a child
	// knows what /secret added since. The block is asked for whether or not
	// anything is declared, because it also says that credential-shaped
	// variables are masked out of a command's environment — and a child runs
	// its commands through the same masked environment the session does.
	sysPrompt = prompt.CombineExtra(sysPrompt, secret.PromptBlock(session.vault, false))
	// And last, what is in the box. A child met most of these tools as bare
	// schemas, which is the tool you reach for last if at all — the evidence
	// tool among them, whose whole job is to answer a reduction notice the
	// child had nothing telling it what to do about.
	//
	// The role's prompt goes in front, and it is composed here, over the same
	// finished set: a profile's tool section is read off the names it is
	// handed, and handed only what its grants registered it would tell a
	// child holding the notebook, a server's reads and the delegation tools
	// that it holds none of them.
	names := make([]string, len(defs))
	for i, t := range defs {
		names[i] = t.Name
	}
	return defs, base, prompt.CombineExtra(rolePrompt(names), sysPrompt, prompt.Toolbox(defs, session.proactive)), keepResult
}

// fixedPrompt is the role prompt of a built-in role, whose tool section is
// written for the role rather than read off what it holds.
func fixedPrompt(sysPrompt string) func([]string) string {
	return func([]string) string { return sysPrompt }
}

// withNavigation puts the session's navigation toolset on a child: the six
// questions a language server answers and the read-only structural tools,
// each registered exactly when the session registered it.
//
// It is one call outside every role branch, for the same reason withNotebook
// is: every child gets this and no child gets a variant of it. A researcher
// without `references` is worse at searching than the session that delegated
// the search to it, which is the one thing a delegated search must not be;
// and until this, a researcher had no way to read git history at all, having
// neither the git tool nor a command to run one with.
//
// `git_write` is not here and cannot be: the write half is registered by a
// surface calling AllowWrites, and the toolset built below is the child's
// own, which never has. A child has no approval card of its own, so a commit
// it asked for would have nobody to ask.
// See docs/capabilities/subagents.md#a-child-searches-with-what-the-session-searches-with.
func withNavigation(ls *lsp.Toolset, st *structural.Toolset, defs []provider.Tool, base agent.ToolExecutor) (
	[]provider.Tool, agent.ToolExecutor) {
	if ls != nil {
		defs = append(defs, ls.Definitions()...)
		base = ls.WrapExecutor(base)
	}
	if st != nil {
		defs = append(defs, st.Definitions()...)
		base = st.WrapExecutor(base)
	}
	return defs, base
}

// childStructural is the child's own structural toolset: the session's own
// probe, contained to the child's workspace rather than the parent's, and
// reading git rather than writing it.
//
// A writer's root is its worktree, and every path argument these tools take
// is resolved against the root the toolset was built with and refused if it
// leaves it — so `fd` inside a writer lists the copy it is editing rather
// than the checkout it was copied from. The session's own toolset cannot be
// handed over for that reason, and for a second one: a coding session has
// given it the writing half of git, which a child must not have.
//
// nil where the session registered none, so a conversation's children are
// left exactly as they were.
// See docs/capabilities/subagents.md#a-child-searches-with-what-the-session-searches-with.
func childStructural(session *structural.Toolset, root string) *structural.Toolset {
	return session.Rooted(root)
}

// childPostMutation is the person's post-tool hooks on a child's writes and
// on nothing else.
//
// A child's gated dispatcher carries every gated call, a fetch among them,
// where a session's mutating dispatcher carries only writes — and a fetch has
// already met the seam behind it on the approval path. Without the guard, one
// call would be put to one hook twice.
//
// It rides the same place the language server's verdict does, which is
// outside the reduction, so what the hook is told the call produced is the
// text the child is about to read — which is what a seam behind a call is
// for.
//
// It reports no position, as the session's own mutation seam does not: the
// chain is built when the child's environment is, which is before there is a
// child to ask where it has got to. The seams in front of a child's calls do
// report one, and a hook that needs the round can read it from those.
func childPostMutation(r *hook.Runner) chat.MutationHook {
	post := hookPostMutation(r)
	if post == nil {
		return nil
	}
	return func(name string, args json.RawMessage, result string) string {
		if !tools.IsMutating(name) {
			return result
		}
		return post(name, args, result)
	}
}

// withDiagnostics appends the language server's verdict on a file a call just
// wrote to that call's result, and leaves every other result alone — which is
// the hook's own reading of which calls those are, not a second copy of it.
// A nil hook (no server detected) is the executor unchanged.
// See docs/capabilities/subagents.md#a-child-searches-with-what-the-session-searches-with.
func withDiagnostics(hook chat.MutationHook, exec agent.ToolExecutor) agent.ToolExecutor {
	if hook == nil {
		return exec
	}
	return func(name string, args json.RawMessage) (string, error) {
		result, err := exec(name, args)
		if err != nil {
			return result, err
		}
		return hook(name, args, result), nil
	}
}

// childWindow is the child model's context window as the downloaded price
// table answers it, and 0 where the table has no row for it — the family
// floor behind that is the subagent package's own fallback.
//
// The table is asked here because this is where it is open, and it is asked
// first because it is the better answer: a child is routinely routed to a
// model the session is not on, and the floor is a reading of a model's name.
func childWindow(prices *pricing.Table, model string) int64 {
	if prices == nil {
		return 0
	}
	window, _ := prices.ContextWindow(model)
	return window
}

// childTree is the reading that tells a child its workspace moved under it,
// or nil where the config turned the reading off. It is the session's own
// reading (session.go) pointed at where the child is standing, which for a
// writer is its worktree and not the checkout that worktree was copied from.
//
// The subtrahend is not filled in here: what a child has written is the
// supervisor's own record, and it fills that in when it starts the attempt.
//
// One thing the reading over-reports, deliberately: the record of what has
// been shown to a model is one record per process, because the files are, so
// a reader standing in the parent's own directory can be told a file "you
// have read" changed when it was the session that read it. Telling the two
// apart would need a second record keyed by who was shown what, and the
// sentence the notice ends on — re-read it before you use it — is the right
// advice either way.
//
// The likeliest-author clause is for a child standing in the parent's
// checkout. A writer's worktree is a directory nobody else has open, so
// naming another session there would be answering a question that could not
// have been asked.
func childTree(cfg config.Config, sib sessionSibling, root string, worktree bool) *agent.TreeCheck {
	c := treeCheck(cfg)
	if c == nil {
		return nil
	}
	c.Dir = root
	if worktree {
		return c
	}
	return withSibling(c, sib)
}

// childToolTokens is what a child's definitions cost on every request it
// makes. They are not in its conversation, so a child measuring only its
// messages thinks it has a toolset's worth of room it does not have.
func childToolTokens(defs []provider.Tool) int64 {
	var total int64
	for _, t := range toolDefTokens(defs) {
		total += t.Tokens
	}
	return total
}

// holdsRefusable reports a toolset holding a call a read-only mode refuses
// outright: a file write or a command. A child with neither is a researcher,
// a reviewer or a profile that changes nothing, and its own prompt already
// says it cannot edit or run anything, which is the whole of what the mode's
// paragraph would tell it.
func holdsRefusable(defs []provider.Tool) bool {
	for _, d := range defs {
		if d.Name == tools.ExecCommandName || tools.IsMutating(d.Name) {
			return true
		}
	}
	return false
}

// holdsCommand reports a toolset holding execute_command.
func holdsCommand(defs []provider.Tool) bool {
	return slices.ContainsFunc(defs, func(d provider.Tool) bool { return d.Name == tools.ExecCommandName })
}

// runsCommands reports whether a child of role is handed execute_command:
// a profile file that grants execute and does not list it away, or the
// built-in writer. It is what the spawn card asks before it states what a
// writer's commands run under, since a writer that may only edit has none.
// checkoutIntent is what a checkout's profile says its commands are for, for
// the spawn card: the one place those words reach, since the classifier is
// never handed them. Empty for the person's own profiles and for one that
// states nothing.
func (a *agentProfiles) checkoutIntent(role subagent.Role) string {
	if p, ok := a.snapshot()[role]; ok && p.Checkout {
		return strings.TrimSpace(p.Intent)
	}
	return ""
}

func (a *agentProfiles) runsCommands(role subagent.Role) bool {
	if def, ok := a.definition(role); ok {
		return def.Has(config.PermissionExecute) && def.Allows(tools.ExecCommandName)
	}
	return role == subagent.RoleWriter
}

// withModeInstructions is a child's request with its mode's paragraph on the
// system prompt, the way a session's request carries it: a child in
// read-only or plan mode that is not told so spends its rounds on edits and
// commands the mode refuses one at a time. The conversation is left as it
// was, so a child whose mode is lifted stops being told. It is the session's
// paragraph built from the child's own toolset and the same inspection list,
// so the list a child reads is the one its policy runs.
// See docs/capabilities/subagents.md#a-child-answers-to-the-session.
func withModeInstructions(msgs []provider.Message, mode agent.Mode, extra []string, defs []provider.Tool) []provider.Message {
	return withSystemParagraph(msgs, agent.ModeInstructions(mode, extra, toolNames(defs)))
}

// withRefusedCommands is a writer's request with the paragraph saying its
// commands are refused on this host, on the system prompt the way the mode's
// paragraph rides: a writer that must be contained on a machine with nothing
// to contain it would otherwise spend its rounds learning that one refused
// build at a time and report the refusals as the change failing.
// See docs/capabilities/containment.md#containment-can-be-required.
func withRefusedCommands(msgs []provider.Message) []provider.Message {
	return withSystemParagraph(msgs, prompt.UncontainedWriterInstructions)
}

// withSystemParagraph appends block to a request's system prompt without
// touching the conversation it was read from.
func withSystemParagraph(msgs []provider.Message, block string) []provider.Message {
	if block == "" || len(msgs) == 0 || msgs[0].Role != provider.RoleSystem {
		return msgs
	}
	out := slices.Clone(msgs)
	out[0].Content += "\n\n" + block
	return out
}
