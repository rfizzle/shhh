package chat

// Building a session: the constructor, and the value the caller hands it.
//
// Every dependency the surface cannot resolve for itself — the database, the
// ledger, the classifier, the tool executor, the switch that changes model —
// arrives in the wiring rather than through a package-level default. It is
// what lets a test drive the whole surface with none of them and lets the CLI
// wire a real one without the surface importing it back
// (docs/architecture.md#one-agent-several-front-ends).
//
// What the terminal decides — a resumed conversation and its held turn, the
// first prompt, the inbox, the notices — is not known until the terminal is,
// so it stays a handful of With methods applied to the built screen.
// See docs/architecture.md#the-screen-is-handed-its-wiring-as-one-value.

import (
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/nudge"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// New builds the screen from the conversation, the stream that answers it,
// and everything else it is given. The work the wiring needs done happens
// here in one fixed order, each step after the ones whose results it reads.
func New(initialMessages []provider.Message, stream StreamFunc, w Wiring) Model {
	ta := components.NewTextArea()
	// No placeholder sentence and no per-line prompt: the command-center
	// frame's gutter glyph and bottom-rail hints carry that.
	ta.Placeholder = ""
	ta.Prompt = ""
	ta.Focus()
	// The draft's cursor is the terminal's own: it blinks at the reader's
	// rate, takes their shape, and is where an input method and a screen
	// reader look for it (docs/interface/surfaces.md#the-input-frame). What
	// this surface owes in return is a coordinate on every frame, which
	// drawPromptFrame reports from the rectangle it draws the draft into.
	ta.SetVirtualCursor(false)
	// The box grows and shrinks with what is in it, counted by the textarea
	// against the same wrap it draws with, so the box and its contents cannot
	// disagree about how many rows there are. The floor is the one row the
	// sentence is typed on (minDraftRows); the ceiling moves with the
	// terminal and is set on every fit (fitDraft).
	ta.DynamicHeight = true
	ta.MinHeight = minDraftRows
	ta.MaxHeight = maxDraftRows
	ta.SetHeight(minDraftRows)

	// One frame set, one cadence, one colour, shared with the one-shot UI.
	s := components.NewSpinnerModel()

	m := Model{
		agent:     agent.New(initialMessages, stream),
		wiring:    w,
		input:     ta,
		spinner:   s,
		state:     stateInput,
		verbosity: verbosityNormal,
		atBottom:  true,
		copyFn:    clipboard.Copy,
		// On unless the config says otherwise: unlike mouse reporting, a
		// notification takes nothing away, and it cannot fire while anyone is
		// looking at the screen.
		notifyOn: !w.NotifyOff,
		// On unless the config says otherwise, for the same reason: naming
		// the tab takes nothing away, and the reader with eight of them cannot
		// ask for it once they are lost among the others (terminal.go).
		windowTitleOn: !w.WindowTitleOff,
		pointer:       pointerState{mouseOn: !w.MouseOff},
		windowDir:     sessionDir(),
		pasteLines:    attachment.DefaultPasteLines,
		pasteColumns:  attachment.DefaultPasteColumns,
		// Every session records what it changes; the wiring may swap in a
		// store with a different bound, or one persisted into the local store.
		changes:     changeset.New(changeset.DefaultMaxBytes),
		sessionName: newSessionName(),
		searchMemo:  &searchMemo{},
		alertMemo:   &alertMemo{},
		nudges:      &nudge.Turn{},
	}
	// The stream is reached through the backend, which today is the loop
	// above in this process (backend.go).
	m.backend = inProcessBackend{agent: m.agent}
	m.seed()
	m.bindStores()
	m.applyLoop()
	m.adoptChildren()
	m.loadTodos()
	// Last, because the tool seams ask the screen which calls it gates, and
	// everything above is part of that answer: the wrap captures the screen
	// as it stands when it is built (hooks.go).
	if w.Hooks != nil && w.Executor != nil {
		m.agent.SetExecutor(m.hookExecutor(w.Executor))
	}
	return m
}

// seed copies into the screen's own state what the update loop goes on to
// write: the wiring keeps what the session was given, and these move from
// there.
func (m *Model) seed() {
	w := m.wiring
	// A word the ladder does not have starts the session on normal rather
	// than refusing it: the settings writer has already judged the word, so
	// one that reaches here is a file edited by hand, and a session that will
	// not start over a density is a worse answer than the default.
	if v, err := parseVerbosity(strings.TrimSpace(w.Verbosity)); err == nil {
		m.verbosity = v
	}
	if w.PasteLines != 0 {
		m.pasteLines = w.PasteLines
	}
	if w.PasteColumns != 0 {
		m.pasteColumns = w.PasteColumns
	}
	m.railCols = w.RailWidth
	m.seedPolicy()
	if w.Changeset != nil {
		m.changes = w.Changeset
	}
	m.containment = w.Containment
	m.defaults = w.Defaults
	m.providerName = w.ProviderName
	m.modelName = w.ModelName
	m.effort, m.effortDefault = w.Effort, w.EffortDefault
	m.projectTokens = w.ProjectContextTokens
	m.setToolDefinitions(w.ToolDefinitions)
	m.gatedTools = w.GatedTools
	m.approval.checks = w.GatedChecks
	m.classifier.judge = w.Classifier
	m.summary.writer = w.Summarizer
	m.titles.writer, m.titles.on = w.Titler, w.Titles
	m.account.writer, m.account.every = w.Accountant, w.AccountEvery
	m.suggest.writer, m.suggest.on = w.Suggester, w.Suggestions
	m.startOffers.writer, m.startOffers.gather = w.StartOfferer, w.StartOffersGather
	m.patterns.cfg, m.patterns.wording = w.Patterns, -1
	m.picker.models.options, m.picker.models.lister = w.ModelOptions, w.ModelLister
	m.timing.idle, m.timing.firstPaint = w.StreamIdle, w.FirstPaint
	m.scaffold = w.Scaffold
	m.mcp = w.MCP
	if m.mcp.ReadOnly == nil {
		m.mcp.ReadOnly = func(string) bool { return false }
	}
	m.todo.wiring = w.Todos
}

// seedPolicy is the session's policy as it starts. A conversation has no
// start screen — the empty session is a prompt, not a survey of the checkout
// (docs/capabilities/chat.md#it-starts-where-you-are-not-with-what-you-have)
// — and no modes: the toolset is the bound, so the one policy it runs in is
// fixed here, manual underneath, because what still reaches a decision — a
// spawn, a memory — is a question for the person, and a fetch is answered by
// the conversation's own rule ahead of the mode.
// See docs/capabilities/chat.md#a-conversation-has-one-mode.
func (m *Model) seedPolicy() {
	w := m.wiring
	p := &m.policy
	p.allowlist, p.denylist = w.CommandAllowlist, w.CommandDenylist
	p.allowHosts, p.denyHosts = w.AllowHosts, w.DenyHosts
	p.timeout = w.CommandTimeout
	p.readOnlyExtra, p.readOnlyDisabled = w.ReadOnlyCommands, w.ReadOnlyOff
	p.secretIgnore = w.CommitSecretIgnore
	if len(w.Cycle) > 0 {
		p.cycle = w.Cycle
	}
	if w.Conversation {
		p.mode = agent.ModeManual
		return
	}
	p.mode = w.Mode
	if w.Start != nil {
		// A copy, so the screen's facts are its own: a first run is marked on
		// them later without writing through the caller's value.
		info := *w.Start
		m.start = &info
	}
}

// bindStores claims the session's slot and binds what is kept per slot to
// it. The name the screen was built with is a timestamp two processes
// started in the same second would both mint, so a session with a store asks
// it for a slot of its own before anything is written; the changeset, the
// notebook and the sources ledger are then pointed at that slot, the
// changeset's written records becoming this sitting's before anything draws.
// A session with neither a store nor a changeset of its own keeps the name it
// was built with, and binds only the two records it was handed.
func (m *Model) bindStores() {
	w := m.wiring
	if w.DB != nil || w.Changeset != nil {
		m.adoptSlot(m.claimSlot(m.sessionName))
		return
	}
	m.bindNotebook()
	m.bindSources()
}

// ApplyLoop writes the loop part of the wiring onto a loop: the executor, the
// round cap, the steering, the progress clocks, the scrub, which results are
// kept whole and where a trim's elisions go. Each is zero-safe, so a value
// that leaves one unset leaves the loop's own default. The screen's
// constructor applies it, and so do the two headless tails, which build a
// value holding only this part
// (docs/architecture.md#the-screen-is-handed-its-wiring-as-one-value).
func ApplyLoop(a *agent.Agent, w Wiring) {
	a.SetExecutor(w.Executor)
	a.SetMaxRounds(w.MaxToolRounds)
	a.SetSteering(w.Steering)
	a.SetProgressIntervals(w.ProgressCalls, w.ProgressElapsed)
	if w.Secrets.Scrub != nil {
		a.SetScrub(w.Secrets.Scrub)
	}
	a.StoreElided(w.Evidence.Keep)
	if w.Skills != nil {
		// Activated skill content is exempted from context trimming: the
		// instructions are guidance for every later turn, and a trimmed skill
		// fails silently — the model just stops following it.
		a.KeepResults(skill.IsContent)
	}
}

// applyLoop is that, and then what only the screen has: the retry bound and
// the tree check.
func (m *Model) applyLoop() {
	w, a := m.wiring, m.agent
	ApplyLoop(a, w)
	m.backoff.SetLimit(w.RetryLimit)
	if w.TreeCheck != nil {
		cfg := *w.TreeCheck
		if cfg.Own == nil {
			store := m.changes
			cfg.Own = func() []string { return writtenPaths(store) }
		}
		if cfg.Instructions == nil {
			cfg.Instructions = instructionFiles(cfg.Dir)
		}
		a.SetTreeCheck(cfg)
	}
}

// adoptChildren hands the supervisor the parent's mode, its live grants and
// the conversation's policy, after all three are settled. This session is the
// surface with a person behind its cards, so a child's classifier no comes
// here rather than being refused
// (docs/capabilities/subagents.md#a-child-answers-to-the-session).
func (m *Model) adoptChildren() {
	sup := m.wiring.Subagents
	if sup == nil {
		return
	}
	m.childViews = map[string]*childView{}
	sup.SetParentMode(m.policy.mode)
	sup.SetParentGrants(m.liveGrants())
	sup.SetAttended()
	if m.wiring.Conversation {
		sup.SetConversationPolicy()
	}
}

// inWorkspace resolves a path the session was handed against the directory it
// belongs to. An absolute path is already its own answer, and a session with
// no workspace leaves a relative one as it was — which is the process's
// directory, the same place it would have read before it was told.
func (m Model) inWorkspace(path string) string {
	if m.wiring.Workspace == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(m.wiring.Workspace, path)
}

// claimSlot asks the store for a slot under name and answers with the one it
// gave. A store that cannot say leaves the session on the name it had: the
// timestamp is still what the person sees, and the save it protects is one
// this session would otherwise not have made at all.
func (m Model) claimSlot(name string) string {
	if m.wiring.DB == nil {
		return name
	}
	claimed, err := m.wiring.DB.ClaimChatSlot(name)
	if err != nil {
		return name
	}
	return claimed
}

// adoptSlot moves the session's autosave to another slot and gives back the
// one it is leaving when nothing was ever written there — a session that
// resumed an older conversation claimed a slot on the way in and never used
// it, and a listing full of those is a listing of nothing.
func (m *Model) adoptSlot(name string) {
	if m.wiring.DB != nil && m.sessionName != name {
		_ = m.wiring.DB.ReleaseChatSlot(m.sessionName)
	}
	m.sessionName = name
	m.bindSlot()
}

// bindSlot points the session's per-slot state at the slot it is now in: the
// notebook it writes notes into, and the changeset records it keeps past this
// sitting. The turn counter is carried past what the slot has already been
// through, because a turn number is how a person addresses one — a resumed
// conversation that started counting from one again would hand its first turn
// a number an earlier turn already has.
//
// Two lower bounds, and the higher of them wins. The records say where the
// numbering got to, but a turn that changed no files leaves no record, so a
// sitting that ended on one would be undercounted; the conversation's own
// user messages are the other reading and cover exactly that case.
// Overshooting only skips a number, which costs nothing.
func (m *Model) bindSlot() {
	m.changes.SetSlot(m.sessionName)
	// The written records become this sitting's live changeset before
	// anything draws: the rail, review, undo and commit all read memory,
	// and a resume that left them on disk would treat still-owned files
	// as somebody else's.
	m.changes.Restore()
	if last := max(m.changes.LastTurn(), int64(m.conversationTurns())); last > m.turnCount {
		m.turnCount = last
		m.agent.SetTurn(m.turnCount)
	}
	// After the counter has caught up, not before: the bind is also what
	// tells the notebook and the sources ledger which turn is open.
	m.bindNotebook()
	m.bindSources()
}

// mintSlot moves the session to a slot of its own, claimed now, giving back
// the slot it leaves when nothing was ever written there.
func (m *Model) mintSlot() {
	m.adoptSlot(m.claimSlot(newSessionName()))
}

// mintSlotKeeping is mintSlot for a session with a save already on its way to
// the slot it is leaving. The claim is not given back: releasing it deletes a
// row nothing has written to yet, and the row it deletes is the one that save
// is about to write into — which the store then has to put back under a claim
// this process no longer holds, with the collision check that rides the claim
// gone for the length of the write. quitCmd draws the same line from the
// other side, releasing only when there is no save to make.
func (m *Model) mintSlotKeeping() {
	m.sessionName = m.claimSlot(newSessionName())
	m.bindSlot()
}

func (m Model) WithInitialPrompt(prompt string) Model {
	m.initialPrompt = prompt
	return m
}

func (m Model) WithUpdateNotice(notice string) Model {
	m.updateNotice = notice
	return m
}

// effectiveMaxToolRounds is this turn's tool-round ceiling: the configured
// cap plus whatever [+50] has granted the turn in front of it. The
// grant lives here rather than on the Agent so that it expires with the turn
// — a new one starts from the ceiling the session was configured with.
// Callers that render or enforce a ceiling must ask roundsUnbounded first:
// like agent.MaxRounds, this keeps answering with a number when there is no
// bound, because no number honestly means "none".
func (m Model) effectiveMaxToolRounds() int {
	return m.agent.MaxRounds() + m.roundGrant
}

// roundsUnbounded reports that this turn will not stop at a ceiling: either
// [!] lifted it for the turn, or the session was started without one.
func (m Model) roundsUnbounded() bool {
	return m.roundsUncapped || m.agent.Uncapped()
}

// WithResumedMessages replaces the conversation with a previously saved one
// and rebuilds the transcript from it. name is the slot it came from, which
// is the slot the session keeps autosaving to: a resumed conversation grows
// in place rather than forking into a second copy. An empty name keeps the
// fresh slot the model was built with.
//
// The conversation is told what the checkout looks like now on its way back,
// which is the one thing a restored transcript cannot say for itself
// (reopen.go).
func (m Model) WithResumedMessages(name string, msgs []provider.Message) Model {
	m.resumeConversation(name, msgs)
	return m
}

// WithHeldTurn reopens a resumed conversation held rather than idle: the turn
// in it was parked at a round boundary and the round it was about to ask for
// is still owed. Without it the conversation would come back with an
// unanswered round in front of it and an idle prompt, which is the shape a
// person reads as "it finished" (hold.go).
func (m Model) WithHeldTurn(rounds, granted int) Model {
	m.hold = &turnHold{turn: m.turnCount, rounds: rounds, granted: granted}
	m.roundGrant = granted
	m.appendEntry(entry{kind: entrySystem, text: m.heldNotice()})
	m.syncViewport()
	return m
}

// loadTodos reads the backlog the screen was wired with from disk. A session
// opened beside a parallel sprint follows it from its first frame, the way
// the session that started it does: Init starts the re-read this marks as
// armed.
func (m *Model) loadTodos() {
	m.reloadTodos()
	m.todo.runner.following = m.lanesLive()
}
