package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/update"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

// assembled, when set, takes the wiring runChatSession has just handed the
// screen instead of running the program over it. See the call site for why
// it is here.
var assembled func(chat.Wiring) error

// screenBuild is the terminal session between the assembly and the screen:
// what each phase produced that a later one reads. The phases run in the
// order the dependencies between them require, and the screen's wiring is
// built from what they left here.
// See docs/architecture.md#the-screen-is-handed-its-wiring-as-one-value.
type screenBuild struct {
	cmd     *cobra.Command
	session *chatSession
	asm     *assembly
	env     *sessionEnv
	cfg     config.Config
	proj    config.Project

	// The policy.
	mode        agent.Mode
	cycle       []agent.Mode
	containment chat.Containment
	hooks       *hook.Runner

	// The readers built before the containment, in the order they always
	// were.
	classifier *agent.Classifier
	explainer  *agent.Explainer
	summarizer *agent.Summarizer
	titler     *agent.Titler

	// The record and the boundary.
	recorder   *observeRecorder
	newSession chat.NewSession
	joiner     *mcpJoin
	resume     string

	// The stores and the children. changes is named before the assembly and
	// opened after it: the git stager reads it through this field rather
	// than through a copy of what it held at registration, because what may
	// be staged is what the session has changed by the time the call is
	// made, not what it had changed when the toolset was built.
	changes  *changeset.Store
	sup      *subagent.Supervisor
	executor agent.ToolExecutor
	repeats  *agent.RepeatDetector
	cwd      string
}

func runChatSession(cmd *cobra.Command, args []string, session chatSession) error {
	b := &screenBuild{cmd: cmd, session: &session}
	asm, err := assembleSession(cmd, &session, assemblyOpts{
		kind:      session.kind,
		toolset:   b.toolset,
		register:  registerChat,
		grantable: true,
	})
	if err != nil {
		return err
	}
	defer asm.close()
	b.asm, b.env, b.cfg = asm, asm.env, asm.env.cfg
	b.proj = ProjectConfigFrom(cmd.Context())

	if err := b.policy(); err != nil {
		return err
	}
	b.openingHooks()
	b.executor = asm.ts.executor(session)
	b.record()
	defer b.recorder.end()
	b.stores()
	b.children()
	if b.sup != nil {
		defer b.sup.Close()
	}

	w := b.wiring()
	model := chat.New(b.env.messages, b.env.stream, w)

	// The screen is built here, and everything past it needs a terminal: a
	// TTY on stdin, an alternate screen, a program loop. So this is the one
	// point at which what the session was given can be read back and
	// asserted, and the hook is how a test gets there. It is nil in every run
	// of the binary; only a test sets it, and it ends the session rather than
	// returning to a program it deliberately did not start.
	if assembled != nil {
		return assembled(w)
	}

	// The socket another session hands this one a line through, opened only
	// once the session is certain to run, and closed with it (send.go).
	lines, closeInbox := openInbox(cmd.Context(), b.cfg.Sessions.Inbound)
	defer closeInbox()
	if lines != nil {
		model = model.WithInbound(chat.Inbound{Lines: lines, Policy: b.cfg.Sessions.Inbound})
	}
	model, done, err := b.opening(model, args)
	if err != nil || done {
		return err
	}

	// Piped stdin becomes context for the first message; the TUI then
	// reads keys from the terminal directly.
	initialPrompt := ""
	if len(args) > 0 {
		initialPrompt = args[0]
	}
	var programOpts []tea.ProgramOption
	stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if !stdinIsTTY {
		maxChars := b.cfg.EffectiveContextMaxTokens() * 4
		content, err := stdin.Read(os.Stdin, maxChars)
		if err != nil {
			return err
		}
		if content != "" {
			if initialPrompt == "" {
				initialPrompt = "Take a look at this."
			}
			initialPrompt = stdin.FormatPromptWithContext(initialPrompt, content)
		}
		tty, err := os.Open("/dev/tty")
		if err != nil {
			return fmt.Errorf("chat needs a terminal for input: %w", err)
		}
		defer tty.Close()
		programOpts = append(programOpts, tea.WithInput(tty))
	}
	if initialPrompt != "" {
		model = model.WithInitialPrompt(initialPrompt)
	}
	return b.run(model, programOpts)
}

// toolset is what this surface registers: the git verbs that write, which a
// conversation has none of, and the conveniences only somebody at a terminal
// wants.
func (b *screenBuild) toolset(sc *scope.Scope) toolsetOpts {
	cmd, session := b.cmd, b.session
	var gitWrites *structural.Writes
	if !session.conversation {
		gitWrites = &structural.Writes{
			Files: func() []string { return b.changes.Paths() },
			// A commit hook is a program git runs as whoever opened
			// the session, and a checkout can point git at one inside
			// itself, so it is behind the same answer every other
			// thing a checkout declares is behind.
			// See docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.
			Hooks:        projectTrust().RunsOwnPrograms(),
			SecretIgnore: ConfigFrom(cmd.Context()).Commit.SecretIgnore,
		}
	}
	// A session pops a browser for a page the model published,
	// because somebody is here to read it.
	return toolsetOpts{scope: sc, browser: true, resident: true, gitWrites: gitWrites, asks: true}
}

// policy is the mode and its cycle, the readers the mode leans on, and the
// containment with what the model is told of it. The containment comes first
// among what follows because what contains a command is what contains a hook.
func (b *screenBuild) policy() error {
	cfg, env, ledger, session := b.cfg, b.env, b.asm.ledger, b.session
	// Permission mode: starting mode and Shift+Tab cycle come from
	// config; the default is manual (everything prompts).
	b.mode = agent.ModeManual
	if s := cfg.Behavior.DefaultMode; s != "" {
		mode, err := agent.ParseMode(s)
		if err != nil {
			return fmt.Errorf("config behavior.default_mode: %w", err)
		}
		b.mode = mode
	}
	cycle, err := agent.ParseCycle(cfg.Behavior.ModeCycle)
	if err != nil {
		return fmt.Errorf("config behavior.mode_cycle: %w", err)
	}
	b.cycle = cycle
	// A conversation has one policy whatever the setting says, and the record
	// stamps the mode it really runs under
	// (docs/capabilities/chat.md#a-conversation-has-one-mode).
	if session.conversation {
		b.mode = agent.ModeManual
	}

	// Auto mode's permission classifier, built the way every surface that
	// has one builds it (approvals.go).
	b.classifier = buildClassifier(cfg, env, ledger)
	// The card's own reading of a command, on the same model. Only this
	// surface builds one: it is answered by a keystroke, and an unattended
	// run has nobody to press it (approvals.go).
	b.explainer = buildExplainer(cfg, env, ledger)

	// The session summary resolves its model the same way: summary.model
	// overrides, and empty falls down the bounded-call chain —
	// provider.cheap_model, the provider's small model, the session's own.
	b.summarizer = newSummarizer(cfg, env, ledger, !cfg.Summary.Disabled)
	// Session titles ask the same model. On unless the config says
	// otherwise, since a model always resolves down the chain; a name the
	// user gives wins either way.
	b.titler = agent.NewTitler(ledger.For(env.prov, meter.SourceSummary), agent.TitleConfig{
		ModelAt:  env.flowModelAt(cfg, flowTitle),
		Timeout:  time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
		Disabled: !cfg.TitlesEnabled(),
	})

	if session.conversation {
		return nil
	}
	// Process containment: assistant commands run wrapped when a
	// mechanism is available; the confirm prompt shows the state either way.
	procSup := b.asm.ts.proc
	b.containment, err = buildContainment(cfg, b.asm.sc, procSup)
	if err != nil {
		return err
	}
	// …and what the model is told about it, beside where it was told the
	// work is (scope.go). It is joined to the prompt already built rather
	// than folded in with the scope block, because the containment is
	// resolved after the provider is and the prompt had to exist for
	// that; it is joined to the session's own extra too, so the next
	// /new builds a conversation that was told the same thing.
	c := b.containment
	commandEnv := commandEnvironmentBlock(commandEnvironment{
		Mechanism: c.Mechanism,
		Profile:   c.Profile,
		Network:   c.Network,
		Hosts:     c.Hosts,
		Refused:   c.Refusal != "",
		Ceiling:   cfg.CommandTimeout(),
		// A ceiling backgrounds a command that is still printing only
		// where there is a supervisor to hand it to (process.go).
		Backgrounds: procSup != nil,
	})
	env.addBuiltPrompt(commandEnv)
	session.promptExtra = prompt.CombineExtra(session.promptExtra, commandEnv)
	// …and which of the tools the checkout declared its commands will
	// not find, where any: this is the one surface with a person to
	// offer the install to, so it is told that they were (toolchain.go).
	missing := toolchainPromptBlock(c.Toolchain.Missing, c.Toolchain.Runnable())
	env.addBuiltPrompt(missing)
	session.promptExtra = prompt.CombineExtra(session.promptExtra, missing)
	// …and the offer to draft or review the declaration itself, which
	// is this surface's alone: its answer is a card, and only a session
	// has somebody to put one to (toolchaindraft.go).
	wireToolchainDraft(&b.containment.Toolchain, ledger.For(env.prov, meter.SourceToolchain), env.flowModelAt(cfg, flowToolchain), session.vault.Scrub)
	return nil
}

// openingHooks are the person's own commands at this session's seams
// (hooks.go). They are assembled after the containment, because what the
// session contains its commands with is what it contains a hook with: a hook
// is a command the session runs, and one that ran on the host while the
// assistant's ran in a container would be the hole the containment was turned
// on to close.
func (b *screenBuild) openingHooks() {
	env, session := b.env, b.session
	hookCwd, _ := os.Getwd()
	hooked := hookSet(b.cfg)
	b.hooks = buildHooks(b.cfg, hooked, b.containment.Wrap, hookCwd)
	for _, note := range hookNotes(hooked) {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: "hooks: " + note})
	}
	// A session opening is the first seam, and the one place where adding to
	// what the model has been told is still free. The prompt is already built
	// — it had to be, for the provider to be resolved — so what the hook says
	// is joined to it here, and to the extra the next `/new` will build one
	// from, so both conversations are told the same thing.
	start := b.hooks.SessionStart(b.cmd.Context())
	for _, note := range start.Notes {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: "hooks: " + note})
	}
	if start.Context != "" {
		session.promptExtra = prompt.CombineExtra(session.promptExtra, start.Context)
		env.sysPrompt = prompt.CombineExtra(env.sysPrompt, start.Context)
		if len(env.messages) > 0 && env.messages[0].Role == provider.RoleSystem {
			env.messages[0].Content = env.sysPrompt
		}
	}
}

// record opens the session's record and the boundary that closes it and
// opens the next.
func (b *screenBuild) record() {
	cmd, cfg, env, session := b.cmd, b.cfg, b.env, b.session
	// Session observability: content-free events (usage, tool calls,
	// mode decisions) are recorded to storage; failure just disables recording.
	recorder := startObserveRecorder(b.asm.db, session.kind, env.prov.Name(), env.modelName, b.asm.prices)
	b.recorder = recorder
	// The phases paid before this row existed are written to it now, and
	// the first paint, still to come, as it lands (startup.go).
	startupFrom(cmd.Context()).Attach(recorder.startupRow)
	b.hooks.SetSession(hookSession(recorder.sessionID()))
	// The starting mode is stamped here, as a setting, rather than left to
	// the mode-change signal: that signal fires only on a change, so a
	// session that ran start to finish in the configured default would
	// record no mode at all, and absence is also what a session that
	// recorded nothing looks like.
	//
	// The models are read through the session's own values, so a flow moved
	// on the config screen is what the row states from the change on: the
	// row is stamped again when one moves, and every row a boundary opens
	// after it is stamped with it
	// (docs/capabilities/configuration.md#a-session-can-hold-a-value-no-file-does).
	settings := func() storage.AgentSettings {
		in := env.flows.over(cfg)
		return sessionSettings(in, runSettings{
			mode:       b.mode.String(),
			effort:     env.effort,
			rounds:     roundCapFor(maxRoundsFor(cfg, session.maxRounds, session.maxRoundsSet)),
			checkIn:    checkInFor(cfg.Behavior.CheckInIntervalRounds),
			sandbox:    b.containment.Profile,
			model:      auxiliaryModel(in, env.provName, env.modelName),
			summary:    !cfg.Summary.Disabled,
			classifier: true,
		})
	}
	stamped := env.sysPrompt
	recorder.stamp(env.prompts.fingerprintOf(stamped), session.skills.Len(), projectFingerprintRoot(), settings())
	env.flowsMoved = func() {
		recorder.stamp(env.prompts.fingerprintOf(stamped), session.skills.Len(), projectFingerprintRoot(), settings())
	}

	// The session boundary: /new ends this session and begins another without
	// the process restarting, so the two halves of a launch the front end
	// cannot do for itself are done here. The record is closed and another
	// opened — one row per conversation, or every rate computed over the row
	// is an average of two sittings — and the prompt is built again from the
	// checkout as it stands now. The settings are the ones this process
	// resolved and are stamped again as they were: the second row is the same
	// session assembled the same way, and what changed between the two
	// conversations is the tree, which is what the prompt hash records.
	//
	// The row the new session opens on names the same resume command the exit
	// banner does, read from one place so the two cannot come to name
	// different ones.
	b.resume = "shhh " + session.kind + " --continue"
	// What the conversation says about servers that joined after the launch,
	// which a new session's prompt says from its start (mcpJoin).
	if session.mcpJoins {
		b.joiner = newMCPJoin(*session)
		// A join rewrites the prompt the conversation sends, so the row's
		// fingerprint follows it: a later turn's hash matches its request.
		b.joiner.sent = func(text string) {
			stamped = text
			recorder.stamp(env.prompts.fingerprintOf(stamped), session.skills.Len(), projectFingerprintRoot(), settings())
		}
	}
	b.newSession = func() chat.SessionStart {
		text, projectTokens, _ := session.systemPrompt(cfg.Behavior.SystemPromptExtra)
		text = session.boundaryPrompt(text, b.asm.scopeSaid, b.asm.sc)
		text = b.joiner.fresh(text)
		if recorder.restart() {
			stamped = text
			recorder.stamp(env.prompts.fingerprintOf(text), session.skills.Len(), projectFingerprintRoot(), settings())
		}
		return chat.SessionStart{Prompt: text, Resume: b.resume, ProjectTokens: projectTokens}
	}
	// The gate's verdict is the record's one objective reading of whether
	// the work was right. It is wired here rather than where the runner is
	// built because the runner has no session to report to until one is
	// open.
	recordGateVerdicts(b.asm.ts.gate, recorder)
	scopeGateToWrites(b.asm.ts.gate, b.changes.Paths)
	recordSearches(session.web, recorder)
}

// stores opens the changeset here rather than where the screen takes it,
// because a writer child starts from the parent's uncommitted work and the
// supervisor is built next. A conversation never hands it to the screen and
// leaves it empty.
func (b *screenBuild) stores() {
	b.changes = changeset.New(changeset.DefaultMaxBytes)
	if b.asm.db != nil {
		// The store is where those records outlive the sitting, so a
		// conversation opened again can still take one of its turns back.
		// Without one they end with the process, which makes closing the
		// terminal the same act as accepting every edit the session made.
		b.changes.Persist(b.asm.db)
	}
}

// children builds the sub-agent supervisor and the executor chain in its one
// order: the toolset's executor, wrapped by the supervisor, then by the
// repeat detector — and last, inside the screen's constructor, by the hooks.
func (b *screenBuild) children() {
	session, env := b.session, b.env
	// Sub-agent supervisor: spawn_agent and agent_report short-circuit
	// on the executor chain; Close cancels the child tree and removes
	// leftover worktrees when the session ends.
	if session.agents {
		b.sup = buildSupervisor(b.cmd.Context(), b.asm, *session, b.recorder, b.classifier,
			b.hooks, func() []string { return sessionUntracked(b.changes) })
		b.executor = b.sup.WrapExecutor("", b.executor)
		// Before the chat is handed the two switches below, so the ones it
		// calls are the ones that also move what spawn_agent offers.
		followSpawnModels(env, spawnModels{env: env, agents: b.asm.agents, prices: b.asm.prices}, b.sup.Profiles)
	}

	// Repeat detection goes on last, so it sees every tool the chain
	// can dispatch and the result the model will actually read. The same
	// detector goes to the screen, because the tier it dispatches itself —
	// an approved command, an applied edit — never reaches this chain, and
	// the two commonest circles a session falls into are there.
	b.repeats = agent.NewRepeatDetector()
	b.executor = b.repeats.WrapExecutor(b.executor)

	// The directory the session's own paths belong to, read once: shhh never
	// chdirs, so a session that asked again would be re-answering a settled
	// question. An unreadable working directory leaves it empty, which is
	// the same relative resolution the surface did before it was told.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	b.cwd = cwd
}

// wiring is everything the screen is given. Each part is read in the order
// the session always built it, so whatever a reader's constructor registers
// with the ledger it registers in the same order as before.
func (b *screenBuild) wiring() chat.Wiring {
	cmd, cfg, env, session, asm := b.cmd, b.cfg, b.env, b.session, b.asm
	db, ledger, proj := asm.db, asm.ledger, b.proj
	startedBy := resolve.ModelFrom(*session.flags)
	w := chat.Wiring{
		Title:                session.title,
		Workspace:            b.cwd,
		Observer:             b.recorder.observer(),
		FirstPaint:           firstPaint(startupFrom(cmd.Context())),
		NewSession:           b.newSession,
		Sessions:             sessionsFor(db),
		WorkspaceBlock:       env.workspaceBlock,
		ToolDefinitions:      toolDefTokens(session.toolDefs),
		ProjectContextTokens: env.projectTokens,
		Executor:             b.executor,
		Repeats:              b.repeats,
		DB:                   db,
		PersistenceError:     asm.storeErr,
		Prices:               asm.prices,
		ModelName:            env.modelName,
		Ledger:               ledger,
		Secrets:              chat.Secrets{Manage: secretsManager(session.vault), Scrub: session.vault.ScrubMessage},
		Scope:                asm.sc,
		MaxToolRounds:        maxRoundsFor(cfg, session.maxRounds, session.maxRoundsSet),
		ConfigWriter:         configWriter(proj),
		ConfigScreen:         configSessionOpener(env),
		MouseOff:             !cfg.MouseEnabled(),
		Verbosity:            cfg.Appearance.Verbosity,
		PasteLines:           cfg.Appearance.PasteLines,
		PasteColumns:         cfg.Appearance.PasteColumns,
		ClipboardRead:        attachment.Read,
		RailWidth:            components.RailWidthOrAuto(cfg.Appearance.RailWidth),
		NotifyOff:            !cfg.NotifyEnabled(),
		WindowTitleOff:       !cfg.WindowTitleEnabled(),
		Defaults: chat.Defaults{
			Model:      cfg.Provider.Model,
			AgentModel: cfg.Agents.Model,
			Outranked:  outranking(resolve.ModelOutranks(*session.flags), proj, "provider.model"),
			Started:    env.modelName,
			StartedBy:  startedBy + setInProject(startedBy, proj),
			Delegation: delegationWords(cfg.AgentDelegation()),
			File:       writeFileName(),
		},
		Mode:            b.mode,
		Cycle:           b.cycle,
		Steering:        steering(cfg, env.prompts),
		ProgressCalls:   cfg.Behavior.ProgressIntervalCalls,
		ProgressElapsed: time.Duration(cfg.Behavior.ProgressIntervalSeconds) * time.Second,
		RetryLimit:      cfg.Behavior.ProviderRetries,
		StreamIdle:      time.Duration(cfg.ProviderStreamIdle()) * time.Second,
		Classifier:      b.classifier,
		Explainer:       b.explainer,
		Summarizer:      b.summarizer,
		Titler:          b.titler,
		Titles:          cfg.TitlesEnabled(),
		Accountant:      newAccountant(cfg, env, ledger),
		AccountEvery:    cfg.AccountInterval(),
		Handoff:         newHandoffWriter(cfg, env, ledger),
		// The next step offered in the empty draft is this surface's alone:
		// a -p run, a served session and a child have no draft to offer it
		// in, and none of them is built here.
		Suggester:       newSuggester(cfg, env, ledger),
		Suggestions:     cfg.SuggestionsEnabled(),
		SwitchModel:     env.switchModel,
		Effort:          env.effort,
		SwitchEffort:    env.switchReasoning,
		EffortDefault:   cfg.Provider.Reasoning,
		EffortOutranked: outranking(resolve.ReasoningOutranks(*session.flags), proj, "provider.reasoning"),
		ProviderName:    env.provName,
		ReplaceKey:      env.replaceKey,
		SwitchProvider:  env.switchProvider,
		ModelOptions:    provider.KnownModels(env.prov.Name()),
		ModelLister:     env.endpointModels.picker(),
		EndpointWindows: endpointWindowsFor(env.prov),
		Notebook:        session.notebook,
		Sources:         session.sources,

		CommitSecretIgnore: cfg.Commit.SecretIgnore,
	}
	b.wireCoding(&w)
	b.wireSurfaces(&w)
	b.wireGated(&w)
	// The person's own commands at the session's seams. The constructor puts
	// them on the executor last of all, because the tool seams ask the screen
	// which calls it gates and everything above is part of that answer
	// (chat/hooks.go).
	w.Hooks = b.hooks
	return w
}

// wireCoding is what differs between the two sessions: a conversation has no
// start screen and one mode; the coding agent has the machinery for acting
// and accounting for it.
func (b *screenBuild) wireCoding(w *chat.Wiring) {
	cfg, env, session, db := b.cfg, b.env, b.session, b.asm.db
	patterns, hasPatterns := newPatternsHost(db, b.cwd, b.asm.mem, newPatternWriter(cfg, env, b.asm.ledger))
	if session.conversation {
		// No start screen, but the header still names the checkout the
		// conversation was opened in, from the survey the prompt was built on.
		survey := env.survey
		w.Conversation, w.Checkout = true, &survey
		return
	}
	// The coding agent's machinery for acting and accounting for it:
	// the command runners, containment, the changeset behind review
	// and undo, git snapshots behind rewind, and the start screen's
	// survey of the checkout. A conversation has no act to account for.
	w.Runner = scrubResultRunner(session.vault, runner.RunCaptureResult)
	w.TailRunner = scrubTailRunner(session.vault, runner.RunCaptureTailResult)
	w.Containment = scrubContainment(session.vault, b.containment)
	w.CommandAllowlist = cfg.Behavior.CommandAllowlist
	w.CommandDenylist = cfg.Behavior.CommandDenylist
	w.CommandTimeout = cfg.CommandTimeout()
	w.ReadOnlyCommands, w.ReadOnlyOff = cfg.Behavior.ReadOnlyCommands, !cfg.ReadOnlyAutoEnabled()
	w.GitSnapshots = gitSnapshot
	w.Changeset, w.Tracker = b.changes, changeset.NewTracker(".")
	w.TreeCheck = withSibling(treeCheck(cfg), session.sibling)
	// First contact: the empty session's start screen, surveyed
	// once here rather than assembled per frame.
	start := withPatternsCount(buildStartInfo(env.survey, db, b.asm.ts.gate != nil, chatTrust(db), b.proj,
		projectWordings(cfg.Prompts, projectPrompts())), patterns, hasPatterns)
	w.Start = &start
	// And the reading that writes the offers the table cannot,
	// asked once the screen has drawn and on the next step's
	// switch.
	w.StartOfferer, w.StartOffersGather = newStartOfferer(cfg, env, b.asm.ledger), startOffersEvidence(env.survey)
	// The one thing the screen offers that writes: scaffolding the
	// checkout's own context file, behind a card.
	w.Scaffold = buildScaffold(db, b.cwd)
	if hasPatterns {
		// What the record says this checkout kept doing, each as a
		// proposal answered on a card (patterns.go).
		w.Patterns = patterns.chat()
	}
}

// wireSurfaces is what both sessions offer that is wired only where its
// source exists: the evidence store, the mutation seam, the gate, the
// processes, the skills, the backlog, the question card and memory.
func (b *screenBuild) wireSurfaces(w *chat.Wiring) {
	cfg, session, ts, db, mem := b.cfg, b.session, b.asm.ts, b.asm.db, b.asm.mem
	if red := ts.evidence; red != nil {
		w.Evidence = chat.Evidence{
			Reduce: red.Process,
			Manage: evidenceManager(red),
			// The window trim writes into the same store, so what it elides
			// is retrievable the way a reduced result is.
			Keep: red.Keep,
			// And the sources screen reads back out of it: a ledger row
			// that names an entry can show the page it named.
			Read: evidenceReader(red),
		}
	}
	// The mutation seam: the language server's diagnostics, then the
	// post-tool hooks. It is one field and two things want it, because a
	// write and an edit reach no executor and this is where they can be seen
	// (hooks.go).
	if mutation := chainMutation(lspMutationHook(session.lsp), hookPostMutation(b.hooks)); mutation != nil {
		w.MutationHook = mutation
	}
	if gate := ts.gate; gate != nil {
		w.Gate = chat.Gate{Manage: gateManager(gate), Run: gate.Run, Flakes: gateFlakes(gate),
			FlakeAlertCount: cfg.EffectiveFlakeAlertCount(), FlakeAlertDays: cfg.EffectiveFlakeAlertDays()}
	}
	if procSup := ts.proc; procSup != nil {
		w.Processes = chat.Processes{
			Manage:    processManager(procSup),
			Contained: procSup.Contained,
		}
	}
	if session.skills != nil {
		w.Skills, w.SkillsList = session.skills, skillsListing
	}
	// The backlog is wired into both sessions. It is not a coding surface:
	// one file per item, a status, a ready rule and an archive say nothing
	// about code, and a conversation that keeps a reading list wants them.
	// What a conversation cannot do is asked of a run's own steps when one
	// is asked for, one step at a time (internal/todo/run/pipeline.go).
	// See docs/capabilities/chat.md#the-backlog-is-here-too.
	if b.cwd != "" {
		w.Todos = b.todos()
	}
	if session.ask {
		// The card is only ever drawn for a tool the session handed the
		// model: a question the model could not have asked is a call the
		// session does not have, and it is answered as one.
		w.Ask = true
	}
	if mem != nil {
		w.Memory = chat.Memory{
			Manage:       memoryManager(mem),
			Save:         memorySaver(mem),
			ProjectScope: mem.Project(),
			EntryText:    memoryText(mem),
			Rewrite:      memoryRewriter(mem),
			Omitted:      session.memoryOmitted,
			Declined:     memoryDeclined(db, mem.Project()),
			Decline:      memoryDecliner(db, mem.Project()),
		}
	}
}

// todos is the backlog under the session's directory.
func (b *screenBuild) todos() chat.Todos {
	cfg, env, session, ledger := b.cfg, b.env, b.session, b.asm.ledger
	root := todo.Root(b.cwd)
	// The vocabulary the backlog is written in is resolved once, for the
	// process, and handed to every reader of it rather than reached for:
	// which words an item carries is the project's answer, so there is
	// no default to fall back on (todoprofile.go).
	profile := todoProfile()
	// What the reading is a reading of, in the words the person would
	// use for it. A prompt that called a conversation a coding session
	// would be asking the model to read something that did not happen.
	// todo.model names the model the two backlog readings below run on,
	// and unset they fall down the bounded-call chain like every other
	// digest, so the session model is never spent on one by accident
	// (docs/capabilities/providers.md#a-bounded-call-runs-on-the-small-model).
	reading := todo.ExtractConfig{ModelAt: env.flowModelAt(cfg, flowBacklog), Session: todo.CodingSession, Scrub: session.vault.Scrub}
	if session.conversation {
		reading.Session = todo.Conversation
	}
	return chat.Todos{
		Root: root, Manage: todoManager(root), Detail: todoDetail,
		Profile: profile,
		// The reading is metered against the backlog, so /stats names
		// it under whichever model the chain answered with.
		Extractor: todo.NewExtractor(ledger.For(env.prov, meter.SourceBacklog), reading, profile),
		// Drafting an item from a sentence is the same judgement in one
		// paragraph rather than over a whole session, so it goes to the
		// same model and is metered against the same source.
		Drafter:       todo.NewDrafter(ledger.For(env.prov, meter.SourceBacklog), reading, profile),
		NoCommit:      !cfg.TodoCommitEnabled(),
		ItemTimeout:   cfg.TodoItemTimeout(),
		SprintCostCap: cfg.TodoSprintCostCap(),
		GroomStale:    cfg.TodoGroomStale(),
		// A closed sprint's report is a page, and it goes through the
		// same publisher the model's own pages do — one report store,
		// one server, one retention rule, whoever asked for the page.
		PublishReport: sprintReportPublisher(b.asm.ts.reports),
		Wordings:      env.prompts.todo,
		Pipeline:      todoPipeline(),
		// A sprint working several items at once is this binary's own
		// unattended runner, started in a process of its own.
		Parallel: todoParallelStarter(root),
	}
}

// wireGated is what goes through the approval queue as a generic external
// action — a fetch, a spawn, a git write, a server call — and the readings
// beside it: the children, the servers, the tool sources and /safety.
// Manual and accept-edits prompt, auto defers to the classifier. A
// conversation's fetch never reaches the card: its policy allows one past
// the host deny list (docs/capabilities/chat.md#a-conversation-has-one-mode).
func (b *screenBuild) wireGated(w *chat.Wiring) {
	cfg, session, db := b.cfg, b.session, b.asm.db
	gatedPreviews := map[string]chat.GatedPreviewFunc{}
	gatedChecks := map[string]chat.GatedCheckFunc{}
	// What this session may fetch without asking, which a spawn card states
	// and a host grant grows. It is read and written on the UI goroutine
	// alone — the fetcher keeps its own copy behind its own lock, because
	// that one is read while a fetch is in flight.
	reach := &hostReach{hosts: cfg.Web.AllowHosts}
	if session.web != nil {
		b.wireFetch(w, reach, gatedPreviews)
	}
	if b.sup != nil {
		gatedPreviews[subagent.SpawnToolName] = b.spawnPreview(reach)
		gatedChecks[subagent.SpawnToolName] = spawnModelCheck(b.sup)
		w.Subagents = b.sup
		w.Personas = buildPersonas(*session, b.env, b.asm.agents, b.sup, b.asm.ledger, b.asm.prices)
	}
	// The writing half of git is gated at the write tier: it proceeds where
	// an edit proceeds and is asked where an edit is asked, because what it
	// changes is the repository rather than the world outside the machine.
	if session.structural != nil && session.structural.Has(structural.GitWriteToolName) {
		st := session.structural
		gatedPreviews[structural.GitWriteToolName] = func(args json.RawMessage) (chat.GatedPreview, error) {
			return gitWriteGatedPreview(st, args)
		}
	}
	if session.mcpTools != nil {
		b.wireMCP(w, gatedPreviews)
	}
	// Where every tool came from, read again whenever the tools screen asks:
	// the servers as the rail and /mcp read them, the language servers, the
	// binaries on PATH and the web tools. Both sessions, since a conversation
	// has servers and web tools too (toolsources.go).
	w.ToolSources = sessionToolSources(*session, cfg, db)
	// The readings /safety needs that the chat cannot make itself, each the
	// one its owning command already makes: the trust the start screen is
	// handed, the servers as /mcp reports them, and the vault's names. It is
	// wired in both sessions, because a conversation has a boundary too
	// (docs/capabilities/approvals-and-safety.md#one-reading-of-the-boundary).
	w.Safety = chat.Safety{
		Trust:   chatTrust(db),
		Servers: safetyServers(session.mcpTools),
		Secrets: session.vault.Names,
		EnvMask: cfg.SecretsEnvMaskEnabled(),
	}
	if len(gatedPreviews) > 0 {
		w.GatedTools, w.GatedChecks = gatedPreviews, gatedChecks
	}
}

// wireFetch is a fetch decided on its host: the two config lists, and the
// sink the session's own grants go to, since the fetcher is what the decision
// has to reach for a redirect to be answered by it too.
func (b *screenBuild) wireFetch(w *chat.Wiring, reach *hostReach, gatedPreviews map[string]chat.GatedPreviewFunc) {
	webTools := b.session.web
	w.AllowHosts, w.DenyHosts = b.cfg.Web.AllowHosts, b.cfg.Web.DenyHosts
	// The pacing a fan-out puts on one host is the fetcher's, and so
	// is the wait a refusal costs; the session only says it on the
	// row and gives it up when the turn is cancelled.
	w.FetchWaiting, w.AbandonFetchWaits = webTools.Fetcher.Waiting, webTools.Fetcher.AbandonWaits
	w.HostGrants = func(hosts []string) {
		reachable := append([]string(nil), hosts...)
		reach.hosts = reachable
		webTools.Fetcher.SetGrantedHosts(func(host string) bool {
			return agent.HostMatches(reachable, host)
		})
	}
	gatedPreviews[web.FetchToolName] = func(args json.RawMessage) (chat.GatedPreview, error) {
		summary, err := webTools.FetchSummary(args)
		if err != nil {
			return chat.GatedPreview{}, err
		}
		// The card's blast-radius block for an outbound request:
		// where it goes, what leaves with it, what comes back.
		plan, err := webTools.FetchPlan(args)
		if err != nil {
			return chat.GatedPreview{}, err
		}
		// Host is the card's own domain row, and it is what [a] grants:
		// the reader answers for the site the card named.
		return chat.GatedPreview{Action: "fetch", Summary: summary, Host: plan.Host, Fields: []chat.GatedField{
			{Label: "domain", Value: plan.Host, Detail: "the request leaves this machine", Open: true},
			{Label: "sends", Value: plan.Sends, Detail: "no file contents, no credentials"},
			{Label: "receives", Value: plan.Receives, Detail: "it counts against the context window"},
		}}, nil
	}
}

// spawnPreview draws the card a spawn is decided on.
func (b *screenBuild) spawnPreview(reach *hostReach) chat.GatedPreviewFunc {
	session, agents, sup := b.session, b.asm.agents, b.sup
	writerCommands := writerContainment(b.cfg, childContainment()).field()
	return func(args json.RawMessage) (chat.GatedPreview, error) {
		summary, err := subagent.SpawnSummary(agents.snapshot(), args)
		if err != nil {
			return chat.GatedPreview{}, err
		}
		plan, err := subagent.SpawnPlan(agents.snapshot(), args)
		if err != nil {
			return chat.GatedPreview{}, err
		}
		// A writer that allows overlap and meets a running one that
		// allowed it too says whose claim it shares: that is the other
		// half of what a yes agrees to.
		if holder, claim := sup.SharedClaim(args); holder != "" {
			plan.Scope = strings.TrimSuffix(plan.Scope, " · overlap allowed") + " · shares " + claim + " with " + holder
		}
		// A child edits in its own worktree; nothing reaches the checkout
		// until its patch is approved on a card of its own.
		undo := "the child changes nothing on this checkout"
		if plan.Writer {
			undo = "its patch is a decision of its own before anything lands"
		}
		// The scope is a whole statement — a worktree and a claim, or the
		// phrase for a child that changes nothing — so the row carries no
		// second clause beside it: what reaches this checkout is the undo
		// row's answer and saying it twice was two sentences in one slot.
		fields := []chat.GatedField{
			{Label: chat.SpawnTouchesLabel, Value: plan.Scope, Open: plan.Writer},
			{Label: "undo", Value: "reviewed", Detail: undo},
			{Label: "budget", Value: plan.Budget, Detail: "counted in the session totals"},
		}
		// A child that reaches the web arrives with the hosts this
		// session has answered for and can never add to them, so what it
		// may reach without asking is part of this decision.
		if childReachesWeb(*session, agents, plan.Role) {
			fields = append(fields, chat.GatedField{
				Label: "reaches", Value: reach.value(),
				Detail: reach.detail(), Open: true,
			})
		}
		// What a writer's commands run under is decided before the
		// spawn, so the person reads it on the card they approve it on.
		if plan.Writer && agents.runsCommands(plan.Role) {
			fields = append(fields, writerCommands)
		}
		// A checkout's profile says what its commands are for in the
		// checkout's words, which the classifier is not handed; the
		// person reads them here instead, before the child exists.
		// See docs/capabilities/approvals-and-safety.md#a-profile-can-narrow-the-classifier-never-widen-it.
		if intent := agents.checkoutIntent(plan.Role); intent != "" {
			fields = append(fields, chat.GatedField{
				Label: "intent", Value: intent,
				Detail: "the checkout's profile says so · the classifier is not told",
			})
		}
		return chat.GatedPreview{
			Action: "spawn", Summary: summary, Fields: fields,
			// The act the card's row states, and the transcript's own
			// title for the call: which agent, and what it was asked to
			// do. A round asking for three of these draws one card with
			// three rows (docs/capabilities/subagents.md#spawning-is-a-decision).
			Title: "spawn " + spawnSubject(plan),
			Spawn: &components.SpawnRow{
				Role: string(plan.Role), Name: plan.Name, About: plan.About,
				Task: plan.Task, Touches: plan.Scope, Writer: plan.Writer,
			},
		}, nil
	}
}

// wireMCP is the servers: a call the user did not mark read-only goes through
// the queue the same way, and the card says where it goes and what leaves
// with it (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).
func (b *screenBuild) wireMCP(w *chat.Wiring, gatedPreviews map[string]chat.GatedPreviewFunc) {
	session := b.session
	mcpTools := session.mcpTools
	for _, name := range mcpTools.Gated() {
		name := name
		gatedPreviews[name] = func(args json.RawMessage) (chat.GatedPreview, error) {
			return mcpGatedPreview(mcpTools, name, args)
		}
	}
	// The servers were started without waiting, so each one's tools, the
	// block naming it and the toolbox reach the model at the first turn
	// boundary after it answers, and its row turns before that, when its
	// connect ends (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	var live func() []components.InspectorToolSource
	var join func(string) (chat.MCPJoin, bool)
	if session.mcpJoins {
		live = func() []components.InspectorToolSource { return mcpToolSources(mcpTools) }
		join = b.joiner.take
	}
	w.MCP = chat.MCP{
		Live:     live,
		Join:     join,
		Has:      mcpTools.Has,
		ReadOnly: mcpTools.ReadOnly,
		Manage:   mcpManager(mcpTools, session.mcpCatalog),
		Sources:  mcpToolSources(mcpTools),
		Prompts:  mcpTools.Prompts,
		Render:   mcpTools.Render,
		Refresh:  mcpTools.Refresh,
		Restate: func() ([]components.InspectorToolSource, []string) {
			return mcpToolSources(mcpTools), mcpDeathNotes(mcpTools.Deaths())
		},
		Abandon: mcpTools.AbandonCalls,
	}
}

// opening is what the terminal decides, applied to the built screen: a
// resumed conversation and its held turn, the update notice, and the first-run
// and changed-keys notices. done is a resume the person cancelled.
func (b *screenBuild) opening(model chat.Model, args []string) (_ chat.Model, done bool, _ error) {
	session, db, env := b.session, b.asm.db, b.env
	if session.wantsResume() {
		reopened, err := session.resumeChat(db)
		if err != nil {
			return model, true, err
		}
		if reopened.cancelled {
			return model, true, nil
		}
		if reopened.slot != "" {
			// Refresh the system prompt so shell/cwd context is current.
			if len(reopened.messages) > 0 && reopened.messages[0].Role == provider.RoleSystem {
				reopened.messages[0].Content = env.sysPrompt
			}
			// The conversation is also told what the checkout looks like now,
			// ahead of everything it remembers: the transcript describes the
			// tree as it was, and after a pull or a rebase that is not a
			// stale picture but a misleading one
			// (docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is).
			// A front-end with no model to hang it on asks chat.ResumeContext
			// for the same reading.
			model = model.WithResumedMessages(reopened.slot, reopened.messages)
			// A conversation the slot says was saved mid-turn comes back
			// mid-turn. Without this it would open idle with an unanswered
			// round in front of it, which is the shape a person reads as
			// "it finished" — and the round it is owed would never be asked
			// for (docs/capabilities/sessions-and-memory.md#a-held-turn-comes-back-held).
			if h, ok, err := db.ChatHold(reopened.slot); err == nil && ok {
				model = model.WithHeldTurn(h.Rounds, h.Granted)
			}
		}
	}
	if r := update.CheckCached(version); r != nil {
		model = model.WithUpdateNotice("update: " + r.Latest)
	}
	// One reading answers both, because the marker it reads is written by
	// it: a first run is owed the start screen's three keys and never the
	// notice about keys it did not have.
	switch launch := readKeymapLaunch(); {
	case launch.firstRun:
		model = model.WithFirstRun()
	case launch.noticeDue:
		model = model.WithKeysNotice(chat.KeysChangedNotice())
	}
	return model, false, nil
}

// run is the program: alternate scroll off, the wheel filter, the loop, the
// banner, and the stop seam.
func (b *screenBuild) run(model chat.Model, programOpts []tea.ProgramOption) error {
	// Ask the terminal to stop turning the wheel into arrow keys, which is what
	// alternate scroll does to a full-screen program on most terminals — and
	// what put hundreds of synthetic Up/Down presses into the draft
	// (chat/altscroll.go). The setting is saved and restored, so a terminal
	// that had it on keeps it after we exit.
	restoreScroll := chat.SuppressAlternateScroll(os.Stdout)
	defer restoreScroll()

	// Wheel floods are merged before they enter the update queue, so a key
	// pressed mid-fling never waits behind one frame per notch (chat/wheel.go).
	// The filter needs the program's own Send for its flush probe, which the
	// program cannot hand out before it exists — hence the two steps.
	wheel := chat.NewWheelFilter()
	programOpts = append(programOpts, tea.WithFilter(wheel.Filter))
	program := newProgram(model, programOpts...)
	wheel.SetSend(program.Send)
	final, err := program.Run()
	if err != nil {
		// os.Exit skips the deferred restore, so the terminal is put back
		// here rather than left reporting modified keys to the next program.
		restoreScroll()
		// And it skips the deferred close, which would leave the row saying
		// the session came out the way its last turn did. It came out as
		// this error.
		b.recorder.endWith(observe.SessionError)
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// The alt screen took the whole session with it on the way out (
	// docs/interface/surfaces.md#outside-the-tui). The banner is what the
	// terminal keeps: the slot the conversation is in, what the sitting cost,
	// and how to reopen it. The resume command is this command — `shhh chat` and
	// `shhh code` read the same autosave slot but not the same toolset, so the
	// one that comes back is the one that was running.
	if m, ok := final.(chat.Model); ok {
		printExitBanner(m.ExitBanner(b.resume))
	}
	// The last seam: the session stopping. It fires here rather than inside
	// the program because there is no screen left to hold it up, and because
	// every way out of a session — the quit chord, an error, the last turn —
	// has already come back through this one return.
	for _, note := range b.hooks.Stop(b.cmd.Context(), hook.Pos{}, "").Notes {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: "hooks: " + note})
	}
	return nil
}

// printExitBanner writes the exit banner on stderr, beside everything else
// this command says about itself, so a redirected stdout still carries only
// what the session produced. A session with nothing to resume renders empty
// and prints nothing at all.
func printExitBanner(b components.ExitBanner) {
	// stderr is what is being written to, so stderr is what is measured; a
	// redirected one has no width to give and takes the same 80 columns the
	// rest of the CLI falls back to.
	width, _, err := term.GetSize(os.Stderr.Fd())
	if err != nil || width <= 0 {
		width = 80
	}
	if view := b.View(width); view != "" {
		fprintStyled(os.Stderr, view)
	}
}
