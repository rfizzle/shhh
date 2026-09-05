package chat

// Building a session: the constructor and the wiring the caller hands it.
//
// Every dependency the surface cannot resolve for itself — the database, the
// ledger, the classifier, the tool executor, the switch that changes model —
// arrives through one of these rather than through a package-level default.
// It is what lets a test drive the whole surface with none of them and lets
// the CLI wire a real one without the surface importing it back
// (docs/architecture.md#one-agent-several-front-ends).

import (
	"context"
	"path/filepath"

	"charm.land/bubbles/v2/textarea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

func New(initialMessages []provider.Message, stream StreamFunc) Model {
	ta := textarea.New()
	// No placeholder sentence and no per-line prompt: the command-center
	// frame's gutter glyph and bottom-rail hints carry that.
	ta.Placeholder = ""
	ta.Prompt = ""
	ta.Focus()
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	// The draft's cursor is the terminal's own: it blinks at the reader's
	// rate, takes their shape, and is where an input method and a screen
	// reader look for it (docs/interface/surfaces.md#the-input-frame). What
	// this surface owes in return is a coordinate on every frame, which
	// drawPromptFrame reports from the rectangle it draws the draft into.
	ta.SetVirtualCursor(false)
	// The box grows and shrinks with what is in it, counted by the textarea
	// against the same wrap it draws with, so the box and its contents cannot
	// disagree about how many rows there are. The floor is the three rows the
	// box has always had; the ceiling moves with the terminal and is set on
	// every fit (fitDraft).
	ta.DynamicHeight = true
	ta.MinHeight = inputHeight
	ta.MaxHeight = maxDraftRows
	ta.SetHeight(inputHeight)
	// Two keys insert a line break, one of which the user can find:
	// shift+enter is rewritten to ctrl+j before the textarea sees it
	// (newline.go), and ctrl+j is the chord that works in a terminal too old
	// to report it. Alt+enter used to be a third, and went with the
	// follow-up chord it shared: Windows Terminal takes it for full screen
	// (docs/interface/reserved-keys.md).
	ta.KeyMap.InsertNewline.SetKeys(keys.Draft.Newline.Keys()[1:]...)

	// One frame set, one cadence, one colour, shared with the one-shot UI.
	s := components.NewSpinnerModel()

	return Model{
		agent:     agent.New(initialMessages, stream),
		input:     ta,
		spinner:   s,
		state:     stateInput,
		verbosity: verbosityNormal,
		atBottom:  true,
		copyFn:    clipboard.Copy,
		// On unless the config says otherwise (WithMouse).
		mouseOn: true,
		// On unless the config says otherwise (WithNotify): unlike mouse
		// reporting, a notification takes nothing away, and it cannot fire
		// while anyone is looking at the screen.
		notifyOn: true,
		// On unless the config says otherwise (WithWindowTitle), for the same
		// reason: naming the tab takes nothing away, and the reader with
		// eight of them cannot ask for it once they are lost among the
		// others (terminal.go).
		windowTitleOn: true,
		windowDir:     sessionDir(),
		pasteLines:    attachment.DefaultPasteLines,
		pasteColumns:  attachment.DefaultPasteColumns,
		// Every session records what it changes; WithChangeset swaps in a
		// store with a different bound or a git tracker.
		changes:     changeset.New(changeset.DefaultMaxBytes),
		sessionName: newSessionName(),
	}
}

func (m Model) WithToolExecutor(executor ToolExecutor) Model {
	m.agent.SetExecutor(executor)
	return m
}

// WithRunner enables /run with the given command executor.
func (m Model) WithRunner(run func(context.Context, string) (string, int)) Model {
	m.runFn = run
	return m
}

// WithModelSwitcher enables /model <name>; fn must make subsequent stream
// requests use the given model.
func (m Model) WithModelSwitcher(fn func(string)) Model {
	m.switchFn = fn
	return m
}

// WithNewSession wires the boundary. Without it /new still starts the
// conversation over, and the record goes on describing both halves as one
// session — which is why every host that has a record wires this.
func (m Model) WithNewSession(fn NewSession) Model {
	m.newSession = fn
	return m
}

// WithWorkspaceBlock wires the checkout reading a rebuilt conversation is
// given: fn answers with the workspace section of the system prompt as the
// tree stands when it is called. A compaction and a load replace the block
// the conversation was carrying with it (context.go).
func (m Model) WithWorkspaceBlock(fn func() string) Model {
	m.workspaceBlock = fn
	return m
}

// WithTitle overrides the header title (default "shhh chat"), so `shhh code`
// can reuse the TUI under its own name.
func (m Model) WithTitle(title string) Model {
	m.title = title
	return m
}

// WithWorkspace states the directory the session's relative paths are
// resolved against — where a saved plan lands, what a command's blast radius
// is measured from, where an attached file is read. A session that is not
// told stays on the process's working directory.
func (m Model) WithWorkspace(root string) Model {
	m.workspace = root
	return m
}

// inWorkspace resolves a path the session was handed against the directory it
// belongs to. An absolute path is already its own answer, and a session with
// no workspace leaves a relative one as it was — which is the process's
// directory, the same place it would have read before it was told.
func (m Model) inWorkspace(path string) string {
	if m.workspace == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(m.workspace, path)
}

// WithDB wires the store, which is also where the session's slot comes from:
// the name it was built with is a timestamp two processes started in the same
// second would both mint, so the store is asked to turn it into a slot of
// this session's own before anything is written to it.
func (m Model) WithDB(db *storage.DB) Model {
	m.db = db
	m.adoptSlot(m.claimSlot(m.sessionName))
	return m
}

// claimSlot asks the store for a slot under name and answers with the one it
// gave. A store that cannot say leaves the session on the name it had: the
// timestamp is still what the person sees, and the save it protects is one
// this session would otherwise not have made at all.
func (m Model) claimSlot(name string) string {
	if m.db == nil {
		return name
	}
	claimed, err := m.db.ClaimChatSlot(name)
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
	if m.db != nil && m.sessionName != name {
		_ = m.db.ReleaseChatSlot(m.sessionName)
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
	m.bindNotebook()
	m.changes.SetSlot(m.sessionName)
	if last := max(m.changes.LastTurn(), int64(m.conversationTurns())); last > m.turnCount {
		m.turnCount = last
	}
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

func (m Model) WithPricing(prices *pricing.Table, modelName string) Model {
	m.prices = prices
	m.modelName = modelName
	return m
}

// WithEndpointWindows wires the endpoint's own answer for a model's context
// length (provider.ModelWindower), which outranks the pricing table: a local
// runtime reports the window it loaded the weights with, under an id the
// public table has never seen. A nil lookup, or one that does not know the
// model, leaves the session on the table and the family floor.
func (m Model) WithEndpointWindows(fn func(string) (int64, bool)) Model {
	m.endpointWindows = fn
	return m
}

// WithLedger wires the session's spend ledger — what every request made
// through the provider gate cost, attributed to whatever made it. The session
// totals the rail and /stats report come from here rather than from the
// turn's own accounting, because the turn is only one of the things spending.
// A nil ledger leaves those surfaces on the main agent's own figures.
// See docs/architecture.md#spend-is-counted-at-the-provider.
func (m Model) WithLedger(l *meter.Ledger) Model {
	m.ledger = l
	return m
}

// WithProvider names the provider the session resolved to and wires the two
// things a provider failure can offer to do about it: replacing the
// key for this session, and switching to another registered provider. Either
// hook may be nil — the failure row then does not offer that key rather than
// offering one that does nothing.
func (m Model) WithProvider(name string, replaceKey func(string) error, switchProvider func(string) error) Model {
	m.providerName = name
	m.replaceKeyFn = replaceKey
	m.switchProviderFn = switchProvider
	return m
}

func (m Model) WithUpdateNotice(notice string) Model {
	m.updateNotice = notice
	return m
}

// WithClassifier enables auto mode's LLM permission classifier:
// gated calls the static policy would ask about are judged by it instead;
// its failures fall back to asking the user.
func (m Model) WithClassifier(c *agent.Classifier) Model {
	m.classifier = c
	return m
}

// WithMaxToolRounds overrides the per-turn tool-round cap; zero keeps
// DefaultMaxToolRounds and a negative n is agent.UnlimitedToolRounds, which
// starts the session with no checkpoint at all — what `shhh code
// --max-rounds 0` asks for, and the way to leave a session running unattended.
// The rail has a reading for it, so the TUI no longer has to refuse it.
func (m Model) WithMaxToolRounds(n int) Model {
	m.agent.SetMaxRounds(n)
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
