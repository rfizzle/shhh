// The session runner shared by `shhh chat` and `shhh code`: provider
// resolution, the tool chain, persistence, resume, and the program run.
// Each command builds its own chatSession and hands it here; what differs
// between them is what the session registers, and the runner wires only
// what was registered.
package cli

import (
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

// chatSession parameterizes the shared chat TUI entry point: `shhh chat` and
// `shhh code` run the same Bubble Tea model and differ only in system prompt,
// registered toolset, and title.
type chatSession struct {
	title string
	// kind labels recorded observability sessions: "chat" or "code".
	kind         string
	buildPrompt  func(shell.Info, ...string) string
	toolDefs     []provider.Tool
	flags        *resolve.Opts
	continueLast bool
	resumePick   bool
	// resumeName is a chat already chosen, by whoever could choose one:
	// `shhh chats` runs the browser before it builds a session, so a reader
	// with no provider configured can still tidy the store and nobody pays a
	// provider resolve to browse, and `--resume=<name>` names one outright,
	// which is the only way to say it to a run with no browser to draw.
	resumeName string
	// web is the guarded web toolset; nil leaves the web tools
	// unregistered (`shhh chat` today).
	web *web.Toolset
	// lsp is the language-server toolset: after-edit diagnostics plus
	// the definition/references tools; nil (no servers detected, or disabled)
	// is a clean no-op. `shhh code` only.
	lsp *lsp.Toolset
	// structural wraps external code tools (fd, ast-grep, sd, tokei,
	// jaq), each registered only when its binary is on PATH; nil leaves them
	// unregistered. `shhh code` only.
	structural *structural.Toolset
	// gate registers the quality-gate tool and /gate command;
	// `shhh code` only.
	gate bool
	// gateRunner is what that registration opened, filled in by
	// buildToolset and read again when a child's toolset is assembled. A
	// checkout the person has not trusted opens none and leaves it nil,
	// which is how a child comes to be offered exactly the gate the session
	// itself was offered (quality.go).
	gateRunner *quality.Runner
	// processes registers the long-running process supervisor: the
	// process tool (start approval-gated) and the /ps command; `shhh code`
	// only. Session end terminates every owned process tree.
	processes bool
	// agents registers the sub-agent orchestration tools and supervisor
	//; `shhh code` interactive sessions only.
	agents bool
	// proactive is agents.delegation set to proactive: the spawn_agent line
	// of every toolbox this session writes, a child's included, says to
	// divide work that divides (applyDelegation, subagents.go).
	proactive bool
	// memory registers the confirm-gated remember tool, which proposes a
	// durable memory for the user to save or decline: interactive sessions
	// only, because a run with nobody in front of it has nobody to confirm a
	// proposal. Recall is not this flag's — every surface that opens a
	// conversation takes it (memory.go).
	memory bool
	// ask registers the question tool, which puts a fork the model cannot
	// decide to the person as a card. It is a registration decision and not
	// a gate decision — a tool the run can only be refused is worse than one
	// it never saw (docs/capabilities/headless.md#a-run-can-delegate) —
	// which is why nothing anywhere else refuses it.
	//
	// The coding agent's interactive session, and a served session with a
	// client to draw the card at — the two surfaces where somebody is there
	// to answer. A scripted run has nobody, a served session in auto mode
	// has said so, and a conversation has somebody but no need: its turn
	// ends by talking to them, so a model that wants to know which of three
	// designs asks in the answer it was already about to write. The card is
	// for the turn that would otherwise stop for minutes mid-work
	// (docs/capabilities/chat.md#chat-changes-nothing). Over the protocol
	// the card is the client's to draw, and what comes back is the same
	// answer in the same words
	// (docs/capabilities/headless.md#a-client-answers-one-call-at-a-time).
	ask bool
	// skills is the catalog of Agent Skills the session discovered; nil
	// registers neither the tool nor the prompt section. Both `shhh chat`
	// and `shhh code`, headless included: activation is a read.
	skills *skill.Catalog
	// requireSandbox is --require-sandbox: the flag half of sandbox.require,
	// which it can only turn on. A session that requires containment and has
	// none refuses the assistant's commands rather than running them bare.
	requireSandbox bool
	// sandbox is --sandbox: the run's commands exec in a container started
	// from an image the declaration was prepared into, so the host's PATH
	// is not what they will find.
	sandbox bool
	// secretFlags are the --secret specs; vault is what they and
	// secrets.env resolved to, opened by openSecrets before anything that
	// runs a command. Both `shhh chat` and `shhh code`, headless included.
	secretFlags []string
	vault       *secret.Vault
	// secretsSaid is the secrets block as the prompt said it at launch, so a
	// session boundary can put the vault as it stands in its place.
	secretsSaid string
	// promptExtra is appended to the system prompt after config and project
	// context (e.g. the recalled-memory block).
	promptExtra string
	// sibling asks whether another session already has this checkout open.
	// It is pointed at the store as soon as one is open, because the system
	// prompt, the start screen and the tree reading all state the answer and
	// must state the same one.
	sibling sessionSibling
	// memoryOmitted is how many memories the recall budget left out of
	// promptExtra, carried here because recall runs before the chat model is
	// built and the rail is the only party that says so.
	memoryOmitted int
	// memoryBlock is the recalled block on its own, as well as inside
	// promptExtra. A child is handed it rather than reading the table again:
	// the memories are the session's, one query answers a whole fan-out, and
	// a child that queried for itself could be told something the session it
	// serves was never told.
	memoryBlock string
	// maxRounds overrides behavior.max_tool_rounds for this session, where 0
	// means no cap at all — the unattended `shhh code --max-rounds 0`, where
	// the round checkpoint has nobody to stop for. maxRoundsSet tells the two
	// zeroes apart exactly as printOpts does; `shhh chat` sets neither and
	// takes the config.
	maxRounds    int
	maxRoundsSet bool
	// addDirs are --add-dir directories: the working scope a session
	// starts with beyond the directory it was opened in, on top of
	// behavior.scope_dirs.
	addDirs []string
	// conversation marks `shhh chat`: nothing that writes is registered,
	// so the runner, containment, the changeset, git snapshots, the backlog
	// and the start screen's survey are not built, and the TUI draws none
	// of the surfaces that account for them. Sub-agents are offered, but
	// only the roles that read. See docs/capabilities/chat.md#chat-changes-nothing.
	conversation bool
	// notebook is the shared channel between the session's agents, opened
	// by openNotebook; nil registers no notebook tools.
	notebook *notebook.Store
	// workSteps says the session registered the steps tool: a coding session
	// somebody watches, whose rail draws the list. A conversation keeps none,
	// and a -p run or a served session has no rail to draw one on.
	workSteps bool
	// sources is the session's record of what it read, opened by
	// openSourceLedger; nil is a session with no web tools and no servers.
	sources *web.Ledger
	// mcp connects the MCP servers the catalog names; mcpTools and
	// mcpCatalog are what came of it. A conversation takes only servers
	// marked read-only (docs/capabilities/mcp.md#what-a-conversation-may-reach).
	mcp        bool
	mcpTools   *mcp.Toolset
	mcpCatalog *mcp.Catalog
}

// openSecrets resolves the session's vault and hands its values to every
// path a command runs through, and its scrub to everything that writes a
// copy of what came back. It must run before the first command and before
// the prompt is built, and it is the one place the values are put anywhere;
// everything after it works with the vault's name list and scrub.
// See docs/capabilities/secrets.md#a-secret-is-an-environment-variable.
func (s *chatSession) openSecrets(cmd *cobra.Command, red *evidence.Reducer, procSup *process.Supervisor, asks bool) error {
	cfg := ConfigFrom(cmd.Context())
	v, err := loadSecrets(cfg, s.secretFlags, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	s.vault = v
	// The mask goes on before the pairs do, so a declared secret whose name
	// the mask would have dropped is put back by the pairs: the user asked
	// for that one to be reachable, and the mask is for the ones nobody
	// asked for.
	mask := maskForSession(cfg)
	v.SetEnvMask(mask != nil)
	runner.SetEnvMask(mask)
	runner.SetSessionEnv(v.Environ())
	// The executor chain scrubs what the model reads; these three write the
	// copies that stay on disk after the turn — the evidence store's full
	// original, a process's spool on its way there, and the web response
	// cache the fetcher fills from under every one of those doors — and a
	// wrap around any of them sees the text only once it is already written.
	red.SetScrub(v.Scrub)
	if procSup != nil {
		procSup.SetEnv(v.Environ())
		procSup.SetScrub(v.Scrub)
	}
	scrubWebCache(s.web, v.Scrub)
	s.secretsSaid = secret.PromptBlock(v, asks)
	s.promptExtra = prompt.CombineExtra(s.promptExtra, s.secretsSaid)
	return nil
}

// boundaryPrompt brings the blocks folded into the standing extra at launch
// up to date in a prompt built at a session boundary. The vault may have moved
// under /secret and the scope under /add-dir since then, and each change was
// announced to the conversation the boundary drops. The secrets block goes
// first because an empty one is placed ahead of the scope block as it was
// said at launch.
func (s *chatSession) boundaryPrompt(text, scopeSaid string, sc *scope.Scope) string {
	text = resecretPrompt(text, s.secretsSaid, s.vault, scopeSaid)
	return rescopePrompt(text, scopeSaid, sc)
}

// openNotebook opens the session's shared notebook and registers its two
// tools. Every session gets one — a conversation's colleagues and a coding
// session's children alike, because what one child learns is what the next
// should not have to find again, and that is as true of a fan-out over a
// repository as of one over the web. It persists under the session slot when
// storage is open and lives in memory for the session otherwise.
//
// It runs after the secrets are open, because the vault's rewrite has to be
// on the store before anything can be written into it: a note outlives the
// turn that wrote it, so a wrap around the store would see a value only once
// the backend had already kept it.
// See docs/capabilities/subagents.md#what-they-share.
func (s *chatSession) openNotebook(db *storage.DB) {
	var backend notebook.Backend
	if db != nil {
		backend = db
	}
	s.notebook = notebook.New(backend)
	s.notebook.SetScrub(s.vault.Scrub)
	s.toolDefs = append(append([]provider.Tool{}, s.toolDefs...), notebook.Definitions()...)
}

// registerChat is the terminal session's registrations, in its own order: the
// roles before the store, then the servers, the memories with the tool that
// proposes one, the question tool, the working steps, the notebook, the
// sources and the skills. The order is what the model reads its tools and
// their paragraphs in, which is why it stays this surface's.
// See docs/architecture.md#a-session-is-assembled-in-one-place.
func registerChat(cmd *cobra.Command, a *assembly, session *chatSession) error {
	// Sub-agent orchestration: spawn_agent (approval-gated) and
	// agent_report join the toolset; the supervisor itself is built once the
	// provider is resolved.
	// The roles it can spawn are the built-in two plus whatever profiles the
	// user wrote to the agents directory; a profile that does not load is a
	// startup error naming the file, not a role that quietly went missing.
	applyDelegation(ConfigFrom(cmd.Context()), session)
	if session.agents {
		agents, err := loadAgentProfiles(!session.conversation)
		if err != nil {
			return err
		}
		if session.conversation {
			// A conversation offers the roles that read; a profile that
			// could write is left out rather than offered and refused
			// (docs/capabilities/chat.md#colleagues-not-workers).
			agents = agents.readers()
		}
		a.agents = agents
		// The models it may name are known once the provider is, and are
		// put on the definition then (offerOn).
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), subagent.Definitions(agents.profiles, subagent.Offer{})...)
	}

	db, storeErr := openStore()
	if storeErr != nil {
		fmt.Fprintf(os.Stderr, "warning: chat persistence unavailable: %v\n", storeErr)
	}
	if db != nil {
		a.closers = append(a.closers, func() { db.Close() })
	}
	a.db, a.storeErr = db, storeErr
	// Pointed at the store before the prompt and the screen are built from
	// it, because both of them state whether anybody else is here.
	session.sibling = readSibling(db)

	// MCP servers: every definition the catalog holds is connected at
	// once, and the tools of the ones that answered join the toolset. A
	// server that did not answer is a line before the session starts and
	// a row in /mcp, never a reason not to start
	// (docs/capabilities/mcp.md#a-server-that-did-not-answer-is-a-row).
	if session.mcp {
		a.closers = append(a.closers, session.attachMCP(cmd.Context(), db, session.conversation))
	}

	// Durable memory: recalled entries join the system prompt under a hard
	// entry/token budget — cited by id, zero model calls — the same recall
	// every surface takes (memory.go). The remember tool is the half that is
	// this surface's alone: it proposes a new memory, and a proposal is
	// confirmed by the user before it persists.
	a.mem = recallMemory(cmd, session, db)
	if a.mem != nil && session.memory {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), memory.ToolDefinition())
	}

	// And the question tool, on the same terms and for the same reason.
	session.toolDefs = askToolDefs(*session)

	// And the working list, which the rail's STEPS block draws.
	// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.
	if !session.conversation {
		session.workSteps = true
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), plan.StepsToolDefinition())
	}

	session.openNotebook(db)
	session.openSourceLedger(db)

	registerSkills(session)
	return nil
}

// wantsResume reports whether the session was told to open a stored
// conversation rather than start one.
func (s chatSession) wantsResume() bool {
	return s.continueLast || s.resumePick || s.resumeName != ""
}

// reopenedChat is a conversation taken back out of the store: the slot it
// lives in and the messages it holds. An empty slot is a resume that did not
// happen — nothing has ever been saved — and the front-end opens a fresh
// conversation instead.
type reopenedChat struct {
	slot     string
	messages []provider.Message
	// cancelled is the picker closed without a choice. It is neither a
	// failure nor a session: the person asked to see the list and then said
	// no, and there is nothing left for the command to do.
	cancelled bool
}

// resumeChat resolves --continue, --resume and a conversation `shhh chats`
// has already picked into the one to open on.
//
// It is one function over the store rather than a step inside the session
// runner because both front-ends resume: an interactive session hands what
// comes back to the chat model, and an unattended run puts it in front of
// its prompt. A run told to continue that started from nothing instead is
// the failure this prevents, and unattended is exactly where nobody is
// there to notice it.
// See docs/capabilities/sessions-and-memory.md#an-unattended-run-comes-back-too.
func (s chatSession) resumeChat(db *storage.DB) (reopenedChat, error) {
	if db == nil {
		return reopenedChat{}, fmt.Errorf("chat persistence is unavailable, cannot resume")
	}
	// A conversation that has already been named is the one that opens. The
	// newest slot is the answer to a different question, and answering this
	// one with it opens somebody else's conversation under the name the
	// person typed.
	name := s.resumeName
	switch {
	case s.resumePick:
		picked, err := pickSavedChat(db)
		if err != nil {
			return reopenedChat{}, err
		}
		if picked == "" {
			return reopenedChat{cancelled: true}, nil
		}
		name = picked
	case name == "":
		// --continue is the newest slot, whatever it was called: every
		// session autosaves to a slot of its own, so "the last session" is
		// a query rather than a name.
		recent, ok, err := db.MostRecentChat()
		if err != nil {
			return reopenedChat{}, err
		}
		// The newest slot can be one another running session is autosaving
		// into, and the offer on the start screen quietly takes the one
		// before it. This is not an offer. Somebody asked for the last
		// session, and answering an instruction with a different
		// conversation is the failure that is worst where nobody is
		// watching, so it is named and refused instead. Naming it is also
		// the way through: a slot asked for by name is opened whoever holds
		// it, which is the rule the branch above already follows.
		if recent.Held != "" {
			return reopenedChat{}, fmt.Errorf(
				"%q is %s; resume it by name to open it anyway", recent.Held, livePhrase)
		}
		if ok {
			name = recent.Name
		}
	}
	var (
		messages []provider.Message
		loadErr  error
	)
	if name == "" {
		loadErr = fmt.Errorf("no saved chats")
	} else {
		messages, loadErr = db.LoadChat(name)
	}
	if loadErr != nil {
		// A name that is not there is worth stopping for; "the most recent"
		// on a machine with no history is a first run, and starting is the
		// answer it wanted.
		if !s.continueLast {
			return reopenedChat{}, loadErr
		}
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Skip,
			Subject: "no previous session", Detail: "starting fresh"})
		return reopenedChat{}, nil
	}
	tools.NoteRestoredReads(messages)
	return reopenedChat{slot: name, messages: messages}, nil
}

// outranking is what a session tells a slash command that writes a default:
// the flag or the environment variable that beats the file, and where
// neither does, the checkout's own settings file where it decided that key.
// A default written into the user's file and then overruled reads exactly
// like one that was never saved, which is how `/model default` came to look
// broken while writing the file correctly every time — and a checkout's file
// is the one rank that stays in force after the terminal is closed.
func outranking(above string, proj config.Project, key string) string {
	if above != "" {
		return above
	}
	if !proj.Sets(key) {
		return ""
	}
	return proj.Display + " in this checkout sets " + key
}

// setInProject is what follows a key named as the one that decided
// something: the checkout's settings file when that file set it, and nothing
// otherwise. A project's choice read as the person's own sends them to edit a
// file that did not decide it. `/model` and the doctor's model row both
// word it here, so the two cannot name the file differently.
// See docs/capabilities/configuration.md#two-files-one-resolution-order.
func setInProject(key string, proj config.Project) string {
	if !proj.Sets(key) {
		return ""
	}
	return " in " + proj.Display
}
