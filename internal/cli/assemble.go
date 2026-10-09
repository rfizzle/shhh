package cli

import (
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/spf13/cobra"
)

// assembly is what every surface that runs a conversation stands on: the
// working scope, the toolset, the store, the roles it may spawn, the memories
// it recalled, the price table and the ledger every request is billed
// through, and the environment the session's provider and prompt resolved to.
// It is one value because the terminal session, a scripted run, a served
// session and the supervisor of their children all read the same pieces, and
// four copies of the steps that make them were four orders to keep in step.
type assembly struct {
	sc *scope.Scope
	ts *toolset
	// db is the local store, nil where it would not open; storeErr is why,
	// for the surface that says so.
	db       *storage.DB
	storeErr error
	// agents is nil where the session offers no roles.
	agents *agentProfiles
	mem    *memory.Store
	// scopeSaid is what the model was told about the scope, kept for the
	// session boundary that builds a prompt again.
	scopeSaid string
	prices    *pricing.Table
	ledger    *meter.Ledger
	env       *sessionEnv

	// closers end what the assembly opened, in the reverse order it was
	// opened.
	closers []func()
}

// close ends what the assembly opened, last first.
func (a *assembly) close() {
	for i := len(a.closers) - 1; i >= 0; i-- {
		a.closers[i]()
	}
	a.closers = nil
}

// assemblyOpts is what a surface hands the builder: the two steps whose
// content is its own, at the points the trunk runs them.
type assemblyOpts struct {
	// kind is how the toolset's record names the surface.
	kind string
	// toolset is run between the scope and the toolset, and answers what the
	// toolset is opened with: what a surface names before it — the record its
	// git stager reads — is named there.
	toolset func(*scope.Scope) toolsetOpts
	// register is the surface's registrations, run once the toolset is open
	// and before anything is said about it. Their order is the surface's own,
	// because it is the order the model is offered its tools in.
	register func(*cobra.Command, *assembly, *chatSession) error
	// grantable says a person can grant a directory mid-session.
	grantable bool
	// db is the store a surface was handed rather than opens itself.
	db *storage.DB
}

// assembleSession builds the trunk every surface runs, in the one order it
// has to keep, and stops at the environment, where the surfaces stop agreeing
// about what comes next. On an error it closes what it opened and returns
// nothing; otherwise the caller closes the assembly when the session ends.
// The session is written through in place, the way the toolset writes it.
// See docs/architecture.md#a-session-is-assembled-in-one-place.
func assembleSession(cmd *cobra.Command, session *chatSession, opts assemblyOpts) (_ *assembly, err error) {
	a := &assembly{db: opts.db}
	defer func() {
		if err != nil {
			a.close()
		}
	}()

	// The working scope: the directory the session was opened in plus
	// whatever config and --add-dir put beside it. Containment writes to it,
	// the approval cards ask before anything leaves it, and /add-dir grows it
	// mid-session where there is somebody to ask. It is built first because
	// everything that runs a command — the gate, sub-agents, the session's
	// own runner — takes it.
	a.sc, err = sessionScope(ConfigFrom(cmd.Context()), session.addDirs)
	if err != nil {
		return nil, err
	}

	// Everything every surface registers, on the conditions they all register
	// it under: the reducer, the web tools, the language server, the
	// structural tools, the quality gate, the process supervisor, the report
	// publisher and the vault (toolset.go) — one definition rather than a copy
	// per surface that agrees with the others on the day it is written.
	a.ts, err = buildToolset(cmd, session, opts.kind, opts.toolset(a.sc))
	if err != nil {
		return nil, err
	}
	a.closers = append(a.closers, a.ts.close)

	if err := opts.register(cmd, a, session); err != nil {
		return nil, err
	}

	// The model is told where the work is, so an out-of-scope path is a
	// question it asks rather than a call the user refuses — and where
	// nobody can be asked for a directory mid-flight, knowing the boundary is
	// the difference between a report that names it and a round spent
	// retrying.
	a.scopeSaid = scopePromptBlock(a.sc, opts.grantable)
	session.promptExtra = prompt.CombineExtra(session.promptExtra, a.scopeSaid)

	// …and what it has to work with. Every optional tool above is
	// registered on a condition — a language server was found, a binary is on
	// PATH, a key is configured — so this is the last point where the whole
	// toolset is known, and it has to be said after the last one joins.
	// A server that joins later is a registration too, and says this again
	// over the new set (mcpJoin), so what was said here is kept.
	session.toolboxSaid = prompt.Toolbox(session.toolDefs, session.proactive)
	session.promptExtra = prompt.CombineExtra(session.promptExtra, session.toolboxSaid)

	// The spend ledger is opened before the session's provider, because the
	// provider is handed out through it: every request shhh makes is billed
	// at the gate rather than by the feature that made it. An unattended run
	// bills through the same gate, and one that under-reported would be the
	// harder to notice, because nobody is watching a rail while it works.
	// See docs/architecture.md#spend-is-counted-at-the-provider.
	a.prices = loadPricing()
	a.ledger = meter.New(a.prices)

	a.env, err = buildSessionEnv(cmd, *session, a.ledger)
	if err != nil {
		return nil, err
	}
	if a.agents != nil {
		session.toolDefs = spawnModels{env: a.env, agents: a.agents, prices: a.prices}.offerOn(a.agents.snapshot(), session.toolDefs)
	}
	return a, nil
}

// unattendedRegistration is the registrations of the two surfaces nobody
// answers at a keyboard: a scripted run, which opens its own store and says
// its delegation policy on stderr, and a served session, which is handed the
// store its server holds.
func unattendedRegistration(ownStore, sayDelegation bool) func(*cobra.Command, *assembly, *chatSession) error {
	return func(cmd *cobra.Command, a *assembly, session *chatSession) error {
		if ownStore {
			// The local store is opened here rather than with the recorder
			// because trust for a project MCP server is read from it.
			a.db, a.storeErr = openSessionStore(cmd.Context())
			if db := a.db; db != nil {
				a.closers = append(a.closers, func() { db.Close() })
			}
		}
		// Pointed at the store before the prompt and the tree reading are
		// built from it, exactly as a session does. A run nobody is watching
		// is the one that most needs the answer: told nothing, it sets about
		// explaining or reverting a change another session made, and there is
		// nobody there to stop it.
		// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
		session.sibling = readSibling(a.db)
		// MCP servers, mirroring the interactive session. A read-only
		// server's tools run; every other server's calls are gated and
		// resolved the way web_fetch is — --yes or a client opts in, the
		// default denies. A conversation takes only the
		// servers marked read-only, here as on the screen: which servers a
		// surface may reach is the surface's, not the screen's.
		// See docs/capabilities/mcp.md#what-a-conversation-may-reach.
		// Nobody watches a rail here, so the connects are waited for: a
		// first round without the servers' tools is a worse answer nobody
		// can see was worse
		// (docs/capabilities/mcp.md#a-server-that-did-not-answer-is-a-row).
		if session.mcp {
			a.closers = append(a.closers, session.attachMCP(cmd.Context(), a.db, session.conversation, true))
		}
		// What the run read, kept the way a session keeps it and stated to
		// whoever reads the run, because a write-up nobody watched is judged
		// against it.
		// See docs/capabilities/headless.md#a-run-says-what-it-read.
		session.openSourceLedger(a.db)

		registerSkills(session)

		// The durable memories this project has accumulated, recalled the
		// way a session recalls them (memory.go). The remember tool does not
		// come with them: a proposal has to be confirmed, and neither surface
		// carries a card to confirm it on.
		a.mem = recallMemory(cmd, session, a.db)
		// The question tool does come, where there is a client to draw its
		// card: the protocol carries a question and its answer. A scripted
		// run has nobody to ask, and is handed its toolset unchanged.
		session.toolDefs = askToolDefs(*session)

		// Sub-agent orchestration, where the surface has an answer to the
		// spawn card: a client, --yes, or auto mode's classifier (code.go,
		// serve.go). The roles are the built-in two plus whatever profiles
		// the user wrote; a profile that does not load stops the surface
		// naming the file, exactly as it stops a session.
		// See docs/capabilities/headless.md#a-run-can-delegate.
		//
		// Where a scripted run could delegate, the policy it delegates under
		// is said on stderr before anything starts: off is the one answer
		// that takes the tools away, and a script that passed --yes expecting
		// children would otherwise read their absence as a model that chose
		// not to.
		if sayDelegation && session.agents {
			fmt.Fprintf(os.Stderr, "» delegation: %s\n", delegationWords(ConfigFrom(cmd.Context()).AgentDelegation()))
		}
		applyDelegation(ConfigFrom(cmd.Context()), session)
		if session.agents {
			agents, err := loadAgentProfiles(true)
			if err != nil {
				return err
			}
			a.agents = agents
			session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), subagent.Definitions(agents.snapshot(), subagent.Offer{})...)
		}
		return nil
	}
}
