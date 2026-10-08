package cli

// Sub-agent orchestration wiring: `shhh code` registers spawn_agent /
// agent_report and hands the chat model a supervisor whose children reuse the
// session provider with role-scoped toolsets. Researchers get read-only tools
// plus the web against the real workspace; writers get the full toolset
// against an isolated git worktree, commands contained when a mechanism is
// available. Child sessions are recorded linked to the agent that spawned
// them — the session, or another child — so observability attributes their
// spend and reads back as the tree they ran as.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// agentProfiles is the session's spawnable roles: the supervisor's view
// (worktree, mode, budgets) and, for the ones read from files, the full
// definition the child environment is built from. The built-in researcher
// and writer have no definition and keep their hand-written prompts.
type agentProfiles struct {
	// mu is held while a profile saved from the agent manager is added, on
	// the screen's goroutine, and while the models the definitions name are
	// read, which a child's spawn does on its own: a map walked while it is
	// written ends the process.
	mu          sync.RWMutex
	profiles    subagent.Profiles
	definitions map[string]config.AgentDefinition
}

// definition is the file definition of a role, read under the read lock the
// manager's save takes to write it. A read of a running session's maps goes
// through this, writes or snapshot rather than the map. None of them is
// called with the lock already held: a recursive read lock deadlocks once a
// writer is waiting.
func (a *agentProfiles) definition(role subagent.Role) (config.AgentDefinition, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	def, ok := a.definitions[string(role)]
	return def, ok
}

// writes reports whether the role's profile grants a writing tier.
func (a *agentProfiles) writes(role subagent.Role) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.profiles[role].Writes
}

// snapshot is the profiles as they stand, copied under the read lock, for a
// reader that outlives the call: the supervisor's own set, which a save
// replaces through AddProfile rather than by writing into this map.
func (a *agentProfiles) snapshot() subagent.Profiles {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(subagent.Profiles, len(a.profiles))
	for k, v := range a.profiles {
		out[k] = v
	}
	return out
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
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return loadAgentProfilesIn(cwd, projectScoped)
}

// loadAgentProfilesIn is loadAgentProfiles for a named directory, which is
// what `shhh agents` and its test hand it.
func loadAgentProfilesIn(cwd string, projectScoped bool) (*agentProfiles, error) {
	var (
		defs map[string]config.AgentDefinition
		err  error
	)
	// An untrusted checkout keeps its own profiles to itself. A profile
	// carries a permission set, a tool allowlist and a prompt, so a clone
	// that could add one would be a clone choosing what a spawned agent may
	// do (docs/capabilities/subagents.md#a-profile-is-a-file).
	if projectScoped && projectTrust().Allows() {
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
		p.Checkout = fromCheckout(def, cwd)
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
		Reviews:     def.Reviews,
		MaxTokens:   def.MaxTokens,
		MaxRounds:   def.MaxRounds,
		Inherit:     def.Inherit,
		Deny:        def.Deny,
		Intent:      def.Intent,
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

// fromCheckout reports a profile that is not the person's own: anything not
// read from one of their config directories' agents/, which is the
// checkout's directory in practice. The words in such a file are the
// checkout's, so what it says its commands are for is shown on the spawn
// card and never handed to the classifier as evidence it trusts to narrow.
// It asks where the file is rather than whether it is in the checkout, so a
// path neither reading expected falls on the side that sends nothing.
// See docs/capabilities/approvals-and-safety.md#a-profile-can-narrow-the-classifier-never-widen-it.
func fromCheckout(def config.AgentDefinition, cwd string) bool {
	dir := filepath.Dir(def.Path)
	if def.Path == "" || dir == config.ProjectAgentDir(cwd) {
		return true
	}
	return !slices.Contains(config.AgentDirs(), dir)
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

// roleBuiltIn is where a role that shhh ships lives, in the field the two
// scopes are named in. It is not a place, which is the fact the row carries:
// there is no file to open and nothing to edit.
const roleBuiltIn = "built-in"

// roles is the spawnable roles as the agent manager reads them: what each is
// called, what it is for, and where the file that says so lives. It is the
// set this session can actually spawn — a conversation's is the readers —
// rather than every profile on the machine, because the manager answers
// "what has this session got" (docs/interface/surfaces.md#the-agent-manager).
//
// A definition's scope is read off its path against the project directory
// the loader searched first, so the word says which of two same-named files
// won rather than which one exists.
func (a *agentProfiles) roles(cwd string) []chat.SpawnableRole {
	if a == nil {
		return nil
	}
	projectDir := config.ProjectAgentDir(cwd)
	out := make([]chat.SpawnableRole, 0, len(a.profiles))
	for name, p := range a.profiles {
		role := chat.SpawnableRole{Name: string(name), Description: p.Description, Scope: roleBuiltIn}
		if def, ok := a.definitions[string(name)]; ok && def.Path != "" {
			role.Path, role.Scope = def.Path, string(persona.ScopeGlobal)
			role.Older = !def.Current()
			if filepath.Dir(def.Path) == projectDir {
				role.Scope = string(persona.ScopeProject)
			}
		}
		out = append(out, role)
	}
	// By name: the map has no order, and a list of roles that reshuffled
	// between two openings of the manager would be a list nobody can point at.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// effortFor is the reasoning level a child runs at: the profile's own when
// it names one, otherwise the session's live level — a level set with
// ctrl+t is true of the session and so of every child it spawns.
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

// modelFor is the model an agent at a depth runs on: the spawn's own
// request, then the profile file's, then the [agents] config layer — the
// role's own entry, then the depth's, then the agents-wide default — and
// last the session model.
//
// The profile file is above every config layer for the reason it is above
// the role's own config entry: a file that names a model is a role somebody
// wrote a model into, and it takes that model wherever in the tree it runs.
// See docs/capabilities/subagents.md#the-model-a-depth-runs-on.
func (a *agentProfiles) modelFor(cfg config.Config, role subagent.Role, depth int, requested, sessionModel string) string {
	if requested != "" {
		return requested
	}
	if a != nil {
		if def, ok := a.definition(role); ok {
			if m := def.ProfileModel(); m != "" {
				return m
			}
		}
	}
	return cfg.AgentModel(string(role), depth, sessionModel)
}

// answerChildAsks answers the approval requests a child routes to a parent
// that is not a person: a scripted run, or a served session whose protocol
// draws cards for the calls of the turn it is running and has no vocabulary
// for a child's. It reads until the supervisor is closed and the event
// channel with it, and a nil supervisor starts nothing.
//
// It is not optional where a supervisor exists. The supervisor blocks
// delivering an event, so a surface that spawned a child and read nothing
// would stop the child at its first routed request and itself behind it.
//
// A patch is the one request it approves, and only where the run may write at
// all. The lanes a patch comes from were checked disjoint before they were
// spawned, and what a writer built is verified afterwards by the surface that
// asked for it; the one thing left to refuse is a patch that overlaps one
// already applied, which is exactly what the supervisor flags. Everything
// else is refused: the answer to the spawn was the answer to the child, and a
// call the child's own policy stopped to ask about is one nobody here was
// given the standing to allow.
// See docs/capabilities/subagents.md#a-child-answers-to-the-session.
//
// answerChildAsk is that rule on its own, because the loop around it is a
// goroutine over a channel and the rule is the part worth asserting.
//
// A surface with somebody to ask hands answerChildAsks an answerer of its own
// instead, and this stays what answers where there is nobody: a scripted run,
// and a served session the operator has said nobody is attached to
// (docs/capabilities/headless.md#a-run-can-delegate).
func answerChildAsk(ask *subagent.Ask, writes bool) bool {
	return writes && ask.Kind == subagent.AskPatch && len(ask.Warnings) == 0
}

// answer is what a routed request is put to, or nil for the rule above; state
// is told every child state change the supervisor reports, or nil where
// nothing is watching. An answerer is the served session's alone: a run
// printing to a terminal has nobody to put a request to. A state reader is the
// served session's and a `-p` run streaming events (childLives, print.go).
func answerChildAsks(sup *subagent.Supervisor, writes bool, wrote func(...string),
	answer func(*subagent.Ask) bool, state func(subagent.Status)) {
	if sup == nil {
		return
	}
	go func() {
		for ev := range sup.Events() {
			// Every event carries the child's status, and the state it
			// reports goes out before whatever else the event is for: a
			// request put to a client ahead of the line saying the child is
			// blocked would be a card for an agent the client last heard was
			// running.
			if state != nil {
				state(ev.Status)
			}
			switch ev.Kind {
			case subagent.EventAsk:
				if ev.Ask == nil {
					continue
				}
				if answer != nil {
					// On a goroutine of its own, because an answerer with
					// somebody to ask takes as long as a person does and the
					// supervisor blocks delivering the events behind this
					// one: a second child's request, or its ending, would
					// wait on the first child's card. The rule below takes no
					// time at all and stays where it is.
					go func(as *subagent.Ask) { as.Respond(answer(as)) }(ev.Ask)
					continue
				}
				ev.Ask.Respond(answerChildAsk(ev.Ask, writes))
			case subagent.EventPatch:
				if wrote == nil || ev.Patch == nil {
					continue
				}
				paths := make([]string, 0, len(ev.Patch.Files))
				for _, f := range ev.Patch.Files {
					paths = append(paths, f.Path)
				}
				wrote(paths...)
			}
		}
	}()
}
