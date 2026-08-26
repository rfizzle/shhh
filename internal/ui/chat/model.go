package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// AutosaveName is the reserved chat-session slot that always mirrors the most
// recent conversation, used by `shhh chat --continue`.
const AutosaveName = "(last session)"

// DefaultMaxToolRounds bounds how many consecutive tool-call rounds one user
// turn may trigger before the loop pauses for fresh input
// (behavior.max_tool_rounds overrides it).
const DefaultMaxToolRounds = agent.DefaultMaxToolRounds

type StreamFunc = agent.StreamFunc
type ToolExecutor = agent.ToolExecutor

type state int

const (
	stateInput state = iota
	stateStreaming
	stateConfirmRun
	stateRunningCmd
	// stateClassifying: the auto-mode permission classifier (S-060) is
	// deciding whether the pending approval may run without a prompt.
	stateClassifying
	// statePlanApprove: a completed planning response is awaiting the user's
	// decision — execute, keep planning, or reject (S-061).
	statePlanApprove
	// stateFocus: focus mode (S-076, DESIGN-TUI.md §7) — j/k moves a
	// selection cursor over expandable transcript rows, enter
	// expands/collapses in place, esc returns to the input.
	stateFocus
	// stateDiffFull: a diff is showing full screen (S-074, DESIGN-TUI.md
	// §3c) — from a transcript edit row, an approval's [d], or /diff.
	stateDiffFull
	// stateRewindPick: the interactive /rewind checkpoint picker is showing
	// (S-069).
	stateRewindPick
	// statePick: a generic slash-command picker (/model, /mode) is showing
	// (S-078).
	statePick
	// stateModelList: bare /model is querying the provider's /v1/models
	// endpoint before opening the picker (S-083); esc cancels back to input.
	stateModelList
	// stateReview: review mode (S-099, DESIGN-TUI.md §16a) — the file list
	// and hunk pane of what a turn changed, with staging per hunk. A
	// takeover: full width, the rail hidden, esc returns.
	stateReview
	// stateUndoConfirm: the inline confirm an undo asks through (S-100,
	// §5) — what it would restore, what has drifted since, and esc to
	// decline. It borrows the bottom panel, not the transcript.
	stateUndoConfirm
	// stateKeyEntry: the masked key prompt an auth failure's [k] opens
	// (S-106, §17a). It borrows the bottom panel; esc keeps the old key.
	stateKeyEntry
	// stateRetryWait: the turn is waiting out a bounded retry behind the
	// countdown meter (S-107, §17a). It is a stage of the turn, not a
	// surface — but nothing is streaming and the input is not live, so the
	// wait owns the keyboard for the two keys it offers.
	stateRetryWait
)

const inputHeight = 3
const headerHeight = 1
const dividerHeight = 1
const statusBarHeight = 1
const chromeHeight = headerHeight + dividerHeight + dividerHeight + statusBarHeight
const horizontalPadding = 2

type tokenMsg struct {
	text string
	// final carries a terminal event (doneMsg, streamErrMsg, toolCallsMsg)
	// that arrived in the same batch, so it isn't lost when tokens are drained.
	final tea.Msg
}
type doneMsg struct{ usage *provider.Usage }

// streamErrMsg carries a failed stream back to the session. calls are the
// tool calls the model had *finished* writing before the wire broke, which is
// what makes continuing from a drop possible (S-107); it is empty for every
// failure that never got that far.
type streamErrMsg struct {
	err   error
	calls []provider.ToolCall
}

// retryTickMsg is defined with the rest of S-107 in resume.go.
type streamStartedMsg struct {
	events <-chan provider.StreamEvent
	cancel context.CancelFunc
}
type toolCallsMsg struct {
	calls []provider.ToolCall
	usage *provider.Usage
}
type toolResultsMsg struct {
	runID   int
	results []agent.ToolResult
}
type cmdDoneMsg struct {
	runID    int
	command  string
	output   string
	exitCode int
	duration time.Duration
}
type initialPromptMsg struct{}

// modelListMsg carries the provider's live model list back to the /model
// picker (S-083); err falls the session back to the curated catalog.
type modelListMsg struct {
	names []string
	err   error
}

// classifierDoneMsg carries the auto-mode classifier's verdict for the
// pending approval (S-060).
type classifierDoneMsg struct {
	runID   int
	verdict agent.ClassifierVerdict
}

type entryKind int

const (
	entryUser entryKind = iota
	entryAssistant
	entryTool
	entrySystem
	entryError
	entryCommand
	// entryDiff: an applied edit/write rendered as a diff row (S-074).
	entryDiff
	// entryTurnClose: the rows a finished turn ends with (S-098).
	entryTurnClose
	// entryFailure: a classified provider failure rendered as a recovery row
	// (S-106, §17a). It is a row, not a modal, because it is part of the turn.
	entryFailure
	// entryStreamDrop: a reply that stopped halfway, rendered as the `stream`
	// recovery row and holding the partial it offers to continue from
	// (S-107, §17a).
	entryStreamDrop
)

// entry is one transcript item, stored raw so the history can be re-rendered
// at any width (e.g. after a terminal resize).
type entry struct {
	kind       entryKind
	text       string
	toolName   string
	toolArgs   string
	toolResult string
	exitCode   int
	// duration is how long the tool call or command ran, shown on its
	// activity row (S-075); zero hides it.
	duration time.Duration
	// expanded shows the full tool/command output instead of the truncated
	// block; toggled from focus mode (S-076).
	expanded bool
	// diff is the entryDiff viewer (S-074); a pointer so focus-mode
	// expansion state survives re-renders.
	diff *components.DiffView
	// close is the entryTurnClose block (S-098): the raw counts a turn ended
	// with, so the rows re-render at any width like every other entry, and
	// turn is the turn it closed — what [v] and [u] act on.
	close *components.TurnClose
	turn  int64
	// fail is the classified provider failure behind an entryFailure row
	// (S-106). It is stored as the classification rather than as rendered
	// text, so the row re-renders at any width and the offered keys stay
	// derived from the class rather than parsed back out of a string.
	fail *provider.Failure
	// resume is what a dropped stream kept behind an entryStreamDrop row: the
	// partial text and the finished tool calls (S-107). It is a pointer so
	// that taking the offer marks this row spent wherever it is rendered from.
	resume *streamResume
	// deniedBy names who refused the call — decidedByYou for a decline at the
	// card, decidedByAuto for a rule — and renders the row as ⊘ rather than ✗
	// (S-089, DESIGN-TUI.md §6d). Empty when nothing was refused.
	deniedBy string
	// denyRule names the rule behind an auto denial, e.g. "plan mode".
	denyRule string
	// stepFold is your fold override for the step this entry titles (S-090,
	// DESIGN-TUI.md §13b); steps keep no layout state of their own, so it
	// lives on the raw entry and survives a resize.
	stepFold foldState
	// groupFold is the same override for the folded run of read-only calls
	// this entry heads (S-091, §13c).
	groupFold foldState
	// planStep is the number of the approved plan's step this assistant
	// announcement carries out, offPlanStep when it carries out none of them,
	// and zero when no plan was running (S-104). It is stamped once, when the
	// entry is appended, so every reader of the outline stays a pure function
	// of the transcript.
	planStep int
}

type Model struct {
	// agent owns the loop state (message list, stream requests, tool
	// dispatch, approval queue, iteration guard); the Model is one front-end
	// driving it (S-056).
	agent    *agent.Agent
	db       *storage.DB
	copyFn   func(string) clipboard.Result
	runFn    func(context.Context, string) (string, int)
	switchFn func(string)

	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model
	// spinFrame counts spinner ticks for the passive surfaces that draw a
	// frame themselves rather than animating one (the inspector rail's agent
	// lanes, S-094).
	spinFrame int

	transcript []entry
	// Incremental render cache: entries [0, cachedCount) rendered at
	// cachedWidth, always a whole number of step blocks (S-090). cachedSep is
	// the last cached unit's spacing entry, so the tail joins on the same
	// rhythm.
	cachedRender string
	cachedWidth  int
	cachedCount  int
	cachedSep    entry
	cachedHasSep bool

	// Input recall: inputHistory holds previously submitted inputs;
	// historyIdx == len(inputHistory) means "not browsing".
	inputHistory []string
	historyIdx   int

	streaming string
	events    <-chan provider.StreamEvent
	cancel    context.CancelFunc
	// state is the current surface: the stage of the session's own turn, or
	// a transient view borrowing the screen. turnBack parks the turn's stage
	// while a surface has it, so the turn keeps running underneath (S-087,
	// turn.go).
	state      state
	turnBack   state
	pendingRun string
	runCancel  context.CancelFunc
	// pendingBlast is the approval card's blast-radius block for the decision
	// showing now (S-101), resolved once when the confirm is armed because it
	// reads the filesystem and git.
	pendingBlast blastRadius
	// The approval queue made visible (S-102): pendingQueue is the strip
	// above the card, pendingBatch the queued call IDs [A] would answer with
	// the current one, and batchApproved those an earlier [A] already
	// answered — they run when they reach the head instead of asking again.
	// approvalTotal is how many decisions this tool round queued, so the
	// card can say "2 of 5" once two have been answered.
	pendingQueue  components.QueueStrip
	pendingBatch  []string
	batchApproved map[string]bool
	approvalTotal int
	// Compact activity feed (S-075): verbosity is the feed's default density
	// (/ui verbosity); tailRunFn is the tail-capable command runner, and
	// runningCommand/runStart/runTail drive the live row while a command runs.
	verbosity      verbosity
	tailRunFn      TailFunc
	runningCommand string
	runStart       time.Time
	runTail        *commandTail
	// Head of the agent's approval queue while its confirm prompt is showing,
	// with everything needed to preview and execute it.
	pendingApproval *approvalRequest
	gatedTools      map[string]GatedPreviewFunc
	// Session approval policy: the permission mode (S-059) plus the S-054
	// internals it builds on — [a] on a confirm prompt promotes its category
	// to auto-approval, commandAllowlist comes from config. The default is
	// manual mode: everything prompts.
	mode             agent.Mode
	modeCycle        []agent.Mode
	allowAllEdits    bool
	allowAllCommands bool
	commandAllowlist []string
	// Read-only inspection commands auto-run in every mode; config can extend
	// the built-in list or turn it off entirely.
	readOnlyExtra    []string
	readOnlyDisabled bool
	// Auto mode's LLM permission classifier (S-060): judges gated calls the
	// static policy would ask about; nil falls back to asking the user.
	classifier       *agent.Classifier
	classifierCancel context.CancelFunc
	// defaults are the persisted model defaults /model default writes.
	defaults Defaults
	// lastDenial is the most recent auto-mode denial, shown by /mode why.
	lastDenial string
	// denialNotice mirrors lastDenial on the notice rail (S-082) until the
	// next user turn clears it.
	denialNotice string
	// planChoice is the focused row of the plan-approval prompt (S-061).
	planChoice int
	// The armed plan (S-103): planDoc is the planning response parsed into
	// steps, planFacts and planDetail the radius line computed from it. All
	// three are resolved once, when the prompt opens, because pricing the
	// plan asks git about every file it names.
	planDoc    plan.Plan
	planFacts  []components.PlanFact
	planDetail string
	// planRun is the plan the user approved, for as long as it is being
	// carried out (S-104): it numbers the transcript's steps, fills the
	// rail's PLAN block and answers /plan. Nil when no plan is running.
	planRun *planRun
	// focusIdx is the transcript index of the row selected in focus mode
	// (S-076).
	focusIdx int
	// containment wraps assistant commands in OS-level process containment
	// when a mechanism is available (S-062).
	containment Containment
	// evidence reduces bulky tool results and keeps the originals
	// retrievable (S-064).
	evidence Evidence
	// mutationHook post-processes applied file-modification results before
	// reduction — e.g. appending language-server diagnostics (S-071).
	mutationHook MutationHook
	// gate backs the /gate quality-gate command (S-067).
	gate Gate
	// processes backs /ps and process-start approval gating (S-073).
	processes Processes
	// memory backs /memory and the remember-tool confirm flow (S-070);
	// memoryAsk is the open memory prompt while a proposal awaits the user.
	memory    Memory
	memoryAsk *components.NoteSelect
	// compacting marks an in-flight /compact request (S-055): the streamed
	// response is a summary handled by finishCompact, not conversation text.
	compacting bool
	// observer receives content-free session events for observability
	// (S-065); turnCount and toolDefTokens feed it and /stats.
	observer      Observer
	turnCount     int64
	toolDefTokens int64
	// subagents supervises spawned child agents (S-068); childAsks queues
	// their approval requests routed into this session's approval surface.
	subagents *subagent.Supervisor
	childAsks []*subagent.Ask
	// Sub-agent management and steering (S-077): attachedTo focuses the chat
	// surface on a child ("" = orchestrator); childViews holds each child's
	// mirrored transcript and scroll state so attach/detach loses nothing;
	// agentList is the open agent manager, killConfirm/killTarget its armed
	// inline kill confirmation.
	attachedTo  string
	childViews  map[string]*childView
	parentView  viewState
	agentList   *components.AgentList
	killConfirm *components.Confirm
	killTarget  string
	// Session branching and rewind (S-069): checkpoints mark each user turn's
	// start; sessionName is the storage slot rewind branches hang off (set by
	// /save, /load, and branch switches); rewindSelect is the open /rewind
	// picker; gitSnapshot records the workspace git state per checkpoint when
	// wired.
	checkpoints  []checkpoint
	sessionName  string
	rewindSelect *components.Select
	gitSnapshot  func() GitSnapshot
	// Rich diff rendering (S-074): fullDiff is the viewer showing full
	// screen, diffReturn where esc goes back to.
	fullDiff   *components.DiffView
	diffReturn state
	// Review mode (S-099): review is the surface while it has the screen,
	// reviewTurnN the turn it is reviewing (0 for a review of something
	// else), and reviewReturn where esc goes back to.
	review       *components.ReviewView
	reviewTurnN  int64
	reviewReturn state
	// Undo (S-100): undoAsk is the confirm while it is up, undoPlan what it
	// would do to the workspace (read once, when the confirm was offered),
	// and undoReturn where declining hands the screen back to.
	undoAsk    *components.UndoConfirm
	undoPlan   changeset.UndoPlan
	undoReturn state
	// Per-turn changeset store (S-097): changes records every applied edit
	// with the content on both sides, keyed by turn, and is what /diff
	// renders; tracker answers whether git knew about a file when it was
	// edited, and is nil outside a repository.
	changes *changeset.Store
	tracker *changeset.Tracker
	// Slash-command completion (S-078): completions is the filtered candidate
	// list for the input value completeFor (a mismatch means stale → hidden),
	// completeIdx the focused row, and completeDismissedFor the input value
	// esc dismissed the menu for (typing anything else re-opens it).
	// Argument-level completion (S-079) adds the token span being completed
	// (completeStart/completeEnd, rune offsets into the input), completeArg
	// to say the focused row is an argument value rather than a command
	// name, and argCache so a command's dynamic sources (branch names, saved
	// chats) are read once per menu rather than once per keystroke.
	completions          []completionItem
	completeFor          string
	completeIdx          int
	completeDismissedFor string
	completeStart        int
	completeEnd          int
	completeArg          bool
	argCache             map[int][]argOption
	argCacheFor          string
	// Interactive slash-command pickers (S-078): picker is the open select
	// card, pickerApply consumes the chosen index and returns the transcript
	// note; modelOptions is the /model picker's model catalog.
	picker       *components.Select
	pickerApply  func(*Model, int) string
	modelOptions []string
	// Live model discovery (S-083): modelLister queries the provider's
	// /v1/models endpoint for endpoints no curated catalog can cover, and the
	// result replaces modelOptions for the rest of the session.
	modelLister     func(context.Context) ([]string, error)
	modelListCancel context.CancelFunc
	modelListed     bool
	// steering holds messages typed while the agent is working (S-058); they
	// are injected as user messages before the next stream request.
	steering      []string
	title         string
	width         int
	height        int
	ready         bool
	atBottom      bool
	quitting      bool
	initialPrompt string

	TotalTokensIn  int64
	TotalTokensOut int64
	// Current-turn accounting for the inspector rail's THIS TURN and SPEND
	// blocks (S-092): when the turn started, when it finished (zero while it
	// runs), and what it has spent.
	turnStarted time.Time
	turnEnded   time.Time
	// turnOpen marks a turn the user started and that has not yet closed, so
	// the close rows are appended once, for a real turn (S-098); turnOutcome
	// is how it ended.
	turnOpen      bool
	turnOutcome   components.TurnState
	turnTokensIn  int64
	turnTokensOut int64
	// contextTokens is what the provider last reported the request carrying;
	// zero means nothing has been reported about the current message list, so
	// the accounting estimates instead and says so (S-093).
	contextTokens int64
	// vitals is the session's per-turn usage history and the burn series
	// behind the rail's sparkline (S-093); projectTokens is the estimated
	// size of the project context inside the system prompt, which the
	// occupancy breakdown names separately.
	vitals        vitals
	projectTokens int64
	prices        *pricing.Table
	modelName     string
	updateNotice  string
	// First contact (S-105): what the session already knew about the
	// checkout when it opened, which suggestion the pointer is on, and
	// whether the screen has been spent — a session that has said something
	// to the model is not new again just because /clear emptied it.
	start      *StartInfo
	startFocus int
	startSpent bool
	// Recovery from a provider failure (S-106): the provider the session
	// resolved to, the two hooks a failure row's keys need, and the masked
	// key prompt [k] opens. A hook left nil is a key the row does not offer,
	// which is why they are checked rather than assumed.
	providerName     string
	switchProviderFn func(string) error
	replaceKeyFn     func(string) error
	keyAsk           *components.SecretPrompt
	// retry is the bounded wait between a failed request and the next one
	// (S-107); retrySeq fences its timer, so a cancelled or superseded wait
	// is never advanced by a tick that outlived it.
	retry *retryWait
	// retryAttempt counts the automatic retries this stall has used, against
	// maxRetryAttempts. It outlives each individual wait, which is what makes
	// the bound a bound.
	retryAttempt int
	retrySeq     int
}

func New(initialMessages []provider.Message, stream StreamFunc) Model {
	ta := textarea.New()
	// No placeholder sentence and no per-line prompt: the command-center
	// frame's gutter glyph and bottom-rail hints carry that (S-082).
	ta.Placeholder = ""
	ta.Prompt = ""
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("alt+enter")

	// One frame set, one cadence, one colour, shared with the one-shot UI
	// (S-094).
	s := components.NewSpinnerModel()

	return Model{
		agent:     agent.New(initialMessages, stream),
		input:     ta,
		spinner:   s,
		state:     stateInput,
		verbosity: verbosityNormal,
		atBottom:  true,
		copyFn:    clipboard.Copy,
		// Every session records what it changes; WithChangeset swaps in a
		// store with a different bound or a git tracker (S-097).
		changes:     changeset.New(changeset.DefaultMaxBytes),
		sessionName: AutosaveName,
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

// WithTitle overrides the header title (default "shhh chat"), so `shhh code`
// can reuse the TUI under its own name.
func (m Model) WithTitle(title string) Model {
	m.title = title
	return m
}

func (m Model) WithDB(db *storage.DB) Model {
	m.db = db
	return m
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

// WithProvider names the provider the session resolved to and wires the two
// things a provider failure can offer to do about it (S-106): replacing the
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

// WithClassifier enables auto mode's LLM permission classifier (S-060):
// gated calls the static policy would ask about are judged by it instead;
// its failures fall back to asking the user.
func (m Model) WithClassifier(c *agent.Classifier) Model {
	m.classifier = c
	return m
}

// WithMaxToolRounds overrides the per-turn tool-round cap; n <= 0 keeps
// DefaultMaxToolRounds.
func (m Model) WithMaxToolRounds(n int) Model {
	m.agent.SetMaxRounds(n)
	return m
}

func (m Model) effectiveMaxToolRounds() int {
	return m.agent.MaxRounds()
}

// WithResumedMessages replaces the conversation with a previously saved one
// and rebuilds the transcript from it.
func (m Model) WithResumedMessages(msgs []provider.Message) Model {
	m.loadConversation(msgs)
	return m
}

// autosaveCmd persists the conversation to the autosave slot in the
// background. Returns nil when there is no DB or nothing beyond the system
// prompt to save.
func (m Model) autosaveCmd() tea.Cmd {
	if m.db == nil || len(m.agent.Messages()) <= 1 {
		return nil
	}
	db := m.db
	msgs := m.agent.RequestMessages()
	return func() tea.Msg {
		_ = db.SaveChat(AutosaveName, msgs)
		return nil
	}
}

// quitCmd quits, autosaving first when possible.
func (m Model) quitCmd() tea.Cmd {
	if save := m.autosaveCmd(); save != nil {
		return tea.Sequence(save, tea.Quit)
	}
	return tea.Quit
}

func (m Model) Messages() []provider.Message { return m.agent.Messages() }

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.spinner.Tick}
	if m.initialPrompt != "" {
		cmds = append(cmds, func() tea.Msg { return initialPromptMsg{} })
	}
	if m.subagents != nil {
		cmds = append(cmds, listenSubagents(m.subagents.Events()))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncInputWidth()
		// The transcript wraps to its pane, which is narrower than the content
		// width while the inspector rail shows (S-092).
		paneWidth := m.transcriptWidth()
		vpHeight := m.viewportHeight()

		if !m.ready {
			m.viewport = viewport.New(paneWidth, vpHeight)
			m.viewport.MouseWheelEnabled = true
			m.viewport.SetContent(m.renderHistory())
			m.ready = true
		} else {
			m.viewport.Width = paneWidth
			m.viewport.Height = vpHeight
			m.viewport.SetContent(m.renderHistory())
		}
		return m, nil

	case tea.KeyMsg:
		if m.state == stateDiffFull {
			return m.updateDiffFull(msg)
		}
		if m.state == stateConfirmRun {
			return m.updateConfirmRun(msg)
		}
		if m.state == statePlanApprove {
			return m.updatePlanApprove(msg)
		}
		if m.state == stateRewindPick {
			return m.updateRewindPick(msg)
		}
		if m.state == statePick {
			return m.updatePick(msg)
		}
		if m.state == stateModelList {
			return m.updateModelList(msg)
		}
		if m.state == stateReview {
			return m.updateReview(msg)
		}
		if m.state == stateUndoConfirm {
			return m.updateUndoConfirm(msg)
		}
		if m.state == stateKeyEntry {
			return m.updateKeyEntry(msg)
		}
		// A draining retry countdown owns the keyboard the way the confirm
		// prompt does: nothing is streaming, the input is not live, and the
		// wait offers two keys that both end it (S-107).
		if m.state == stateRetryWait {
			return m.updateRetryWait(msg)
		}
		if m.state == stateFocus {
			return m.updateFocus(msg)
		}
		// The agent manager list (S-077) takes over the bottom panel and keys.
		if m.agentList != nil {
			return m.updateAgentList(msg)
		}
		// A child agent's routed approval takes over the bottom panel (S-068);
		// it defers to the parent's own prompts above.
		if ask := m.activeChildAsk(); ask != nil {
			return m.updateChildAsk(msg, ask)
		}
		switch msg.String() {
		case "ctrl+d":
			m.quitting = true
			m.cancelSubagents()
			if m.cancel != nil {
				m.cancel()
			}
			if m.runCancel != nil {
				m.runCancel()
			}
			if m.classifierCancel != nil {
				m.classifierCancel()
			}
			return m, m.quitCmd()
		case "ctrl+c":
			// While attached, Ctrl+C acts on the child: cancel its turn (S-077).
			if m.attachedTo != "" {
				return m.attachedCancel()
			}
			if m.state == stateClassifying {
				// Skip the classifier check and ask the user directly.
				if m.classifierCancel != nil {
					m.classifierCancel()
					m.classifierCancel = nil
				}
				m.state = stateConfirmRun
				m.syncViewport()
				return m, nil
			}
			if m.state == stateRunningCmd {
				if m.runCancel != nil {
					m.runCancel()
				}
				return m, nil
			}
			if m.state == stateStreaming {
				m.cancelStreaming()
				m.viewport.SetContent(m.renderHistory())
				m.viewport.GotoBottom()
				return m, m.autosaveCmd()
			}
			if strings.TrimSpace(m.input.Value()) != "" {
				m.input.Reset()
				m.historyIdx = len(m.inputHistory)
				return m, nil
			}
			m.quitting = true
			return m, m.quitCmd()
		case "shift+tab":
			// Cycle the permission mode (S-059); attached, it cycles the
			// child's mode clamped to the orchestrator's ceiling (S-077).
			if m.attachedTo != "" {
				return m.cycleAttachedMode()
			}
			m.applyMode(agent.NextMode(m.modeCycle, m.mode))
			return m, nil
		case "ctrl+a":
			// Agent manager (S-077); without a supervisor the key keeps its
			// textarea meaning (line start).
			if m.subagents != nil {
				return m.openAgentList()
			}
		case "ctrl+e":
			// Focus mode (S-076): navigate and expand transcript rows; scoped
			// to whichever agent is focused (S-077). It reads the transcript
			// without touching the conversation, so it opens over a running
			// turn too — the turn keeps streaming underneath (S-087).
			if m.inputLive() {
				return m.enterFocusMode()
			}
		case "esc":
			// With the completion menu open, esc only dismisses the menu; the
			// draft survives and further typing re-opens it (S-078).
			if m.completionActive() {
				m.dismissCompletions()
				m.syncViewport()
				return m, nil
			}
			// The input is live in every non-confirm state (S-058), so esc
			// clears the draft; attached with an empty draft it pops one
			// breadcrumb level (S-077).
			if m.attachedTo != "" && strings.TrimSpace(m.input.Value()) == "" {
				m.detachOne()
				return m, nil
			}
			m.input.Reset()
			m.historyIdx = len(m.inputHistory)
			return m, nil
		case "tab":
			// Tab writes the focused completion into the input (S-078).
			if m.completionActive() {
				m.acceptCompletion()
				m.syncViewport()
				return m, nil
			}
		case "up":
			if m.completionActive() {
				if m.completeIdx > 0 {
					m.completeIdx--
				}
				return m, nil
			}
			// The start screen's suggestion list claims ↑↓ only while it is
			// live: an empty draft on a session that has not started yet,
			// which is also the only time the input history has nothing to
			// browse (S-105).
			if next, claimed := m.startKey("up"); claimed {
				return next, nil
			}
			if m.state == stateInput && len(m.inputHistory) > 0 &&
				(m.browsingHistory() || strings.TrimSpace(m.input.Value()) == "") {
				if m.historyIdx > 0 {
					m.historyIdx--
					m.input.SetValue(m.inputHistory[m.historyIdx])
				}
				return m, nil
			}
		case "down":
			if m.completionActive() {
				if m.completeIdx < len(m.completions)-1 {
					m.completeIdx++
				}
				return m, nil
			}
			if next, claimed := m.startKey("down"); claimed {
				return next, nil
			}
			if m.state == stateInput && m.browsingHistory() {
				m.historyIdx++
				if m.historyIdx >= len(m.inputHistory) {
					m.historyIdx = len(m.inputHistory)
					m.input.Reset()
				} else {
					m.input.SetValue(m.inputHistory[m.historyIdx])
				}
				return m, nil
			}
		case "enter":
			// While attached, Enter acts on the child: scoped commands and
			// mid-turn steering (S-077).
			if m.attachedTo != "" {
				return m.attachedSubmit()
			}
			// Enter on a live start screen types the focused suggestion and
			// submits it, so choosing an offer and typing it are the same
			// act down to the dispatch (S-105).
			if action := m.startAction(); action != "" {
				m.input.SetValue(action)
				return m.submitInput()
			}
			// One submit path for every state that keeps the input live
			// (S-087, command.go): commands run, plain text is a message when
			// idle and steering while the agent works.
			if m.inputLive() {
				return m.submitInput()
			}
		}

	case initialPromptMsg:
		text := m.initialPrompt
		m.initialPrompt = ""
		return m.sendUserMessage(text)

	case streamStartedMsg:
		m.events = msg.events
		m.cancel = msg.cancel
		return m, waitForEvent(m.events)

	case tokenMsg:
		// The provider is answering: whatever stall preceded this is over, and
		// the next one starts its own bounded count (S-107).
		m.clearRetryChain()
		m.streaming += msg.text
		m.viewport.SetContent(m.renderHistory())
		if m.atBottom {
			m.viewport.GotoBottom()
		}
		if msg.final != nil {
			return m.Update(msg.final)
		}
		return m, waitForEvent(m.events)

	case doneMsg:
		m.clearRetryChain()
		m.accumulateUsage(msg.usage)
		if m.compacting {
			return m.finishCompact()
		}
		hadText := m.streaming != ""
		m.finishStreaming()
		// A steering message queued while the model was responding becomes the
		// next user turn immediately (S-058).
		if cmd := m.dispatchSteering(); cmd != nil {
			return m, cmd
		}
		// A completed planning response gets the plan-approval prompt (S-061).
		if m.mode == agent.ModePlan && hadText {
			m.setTurnState(statePlanApprove)
			m.armPlan()
			m.syncViewport()
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd()

	case toolCallsMsg:
		m.clearRetryChain()
		m.accumulateUsage(msg.usage)
		if m.compacting {
			return m.abortCompact()
		}
		auto, gated := m.agent.BeginToolRound(m.streaming, msg.calls, m.requiresApproval)
		m.approvalTotal = len(gated)
		if m.streaming != "" {
			// This is the announcement a step is titled by, so it is where an
			// approved plan's step list joins the transcript (S-104).
			m.appendEntry(m.stampStep(entry{kind: entryAssistant, text: m.streaming}))
		}
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if len(auto) > 0 {
			return m, m.execToolsCmd(auto)
		}
		return m.advanceApprovalQueue()

	case toolResultsMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateStreaming {
			return m, nil
		}
		m.agent.RecordAutoResults(msg.results)
		for _, r := range msg.results {
			m.recordToolEvent(r.Call.Name, r.Duration, outcomeFromResult(r.Result))
			m.appendEntry(entry{kind: entryTool, toolName: r.Call.Name, toolArgs: r.Call.Arguments, toolResult: r.Result, duration: r.Duration})
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if m.agent.QueuedApprovals() > 0 {
			return m.advanceApprovalQueue()
		}
		return m.resumeToolLoop()

	case cmdDoneMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateRunningCmd {
			return m, nil
		}
		m.runCancel = nil
		m.runningCommand = ""
		m.runTail = nil
		m.runStart = time.Time{}
		out := strings.TrimRight(msg.output, "\n")
		// Assistant command output goes through the reduction pipeline
		// (S-064) before both the transcript entry and the tool result, so
		// the user sees exactly what the model got. /run — the user's own
		// command — stays unreduced.
		if m.pendingApproval != nil {
			out = m.reduceResult(tools.ExecCommandName, out)
			outcome := outcomeOK
			if msg.exitCode != 0 {
				outcome = outcomeError
			}
			m.recordToolEvent(tools.ExecCommandName, msg.duration, outcome)
		}
		m.appendEntry(entry{kind: entryCommand, text: msg.command, toolResult: out, exitCode: msg.exitCode, duration: msg.duration})
		if m.pendingApproval != nil {
			m.pendingApproval = nil
			m.agent.ResolveApproval(execToolResult(out, msg.exitCode))
			m.viewport.SetContent(m.renderHistory())
			m.viewport.GotoBottom()
			return m.advanceApprovalQueue()
		}
		m.setTurnState(stateInput)
		m.agent.Append(provider.Message{
			Role:    provider.RoleUser,
			Content: commandContextMessage(msg.command, out, msg.exitCode),
		})
		// A message typed while the /run command executed is sent now, with
		// the command context already in the conversation (S-058).
		if cmd := m.dispatchSteering(); cmd != nil {
			return m, cmd
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd()

	case approvedToolDoneMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateRunningCmd || m.pendingApproval == nil {
			return m, nil
		}
		req := m.pendingApproval
		m.pendingApproval = nil
		m.agent.ResolveApproval(msg.result)
		m.recordToolEvent(req.call.Name, msg.duration, outcomeFromResult(msg.result))
		m.noteEvictedTurns(msg.evicted)
		// An applied edit lands in the transcript as a collapsed diff row
		// (S-074, DESIGN-TUI.md §3a); failures keep the plain tool block so
		// the error text stays visible.
		if req.kind == approvalDiff && len(req.hunks) > 0 && outcomeFromResult(msg.result) == outcomeOK {
			m.appendEntry(entry{kind: entryDiff, diff: &components.DiffView{
				Path:     req.path,
				Verb:     req.verb,
				Hunks:    req.hunks,
				Mode:     components.DiffCollapsed,
				MaxLines: maxDiffExpandedLines,
				Syntax:   diffSyntax(req.path),
			}})
		} else {
			m.appendEntry(entry{kind: entryTool, toolName: req.call.Name, toolArgs: req.call.Arguments, toolResult: msg.result, duration: msg.duration})
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()

	case classifierDoneMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateClassifying || m.pendingApproval == nil {
			return m, nil
		}
		m.classifierCancel = nil
		return m.finishClassifierCheck(msg.verdict)

	case modelListMsg:
		return m.finishModelList(msg)

	case subagentEventMsg:
		return m.handleSubagentEvent(msg.ev)

	case streamErrMsg:
		// Classified, never raw (S-106, §17a): the failure is a row on the
		// activity grid with the provider's own words in its detail body and
		// the keys for its class underneath. What happens after the row —
		// an offer to continue a partial, a bounded wait, or the end of the
		// turn — is S-107's (resume.go).
		return m.handleStreamFailure(msg)

	case retryTickMsg:
		return m.retryTick(msg)

	case spinner.TickMsg:
		// frameWorking keeps the top rail's WORKING spinner animated for the
		// whole turn (S-082), including while streamed text is rendering and
		// while an attached child works.
		if m.frameWorking() || (m.turnState() == stateStreaming && m.streaming == "") || m.turnState() == stateRunningCmd || m.turnState() == stateClassifying || m.state == stateModelList {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			m.spinFrame++
			return m, cmd
		}
	}

	var cmds []tea.Cmd
	// The input stays live while the agent streams or runs tools so the user
	// can type a steering message (S-058); only the confirm and plan-approval
	// prompts take over.
	if m.state != stateConfirmRun && m.state != statePlanApprove && m.state != stateRetryWait {
		// Any other keypress while browsing input history turns the recalled
		// text into a fresh draft.
		if _, ok := msg.(tea.KeyMsg); ok {
			m.historyIdx = len(m.inputHistory)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		// Keystrokes may have changed the input: refresh the slash-command
		// completion menu, and resize the viewport when it appears/disappears
		// (S-078).
		if _, ok := msg.(tea.KeyMsg); ok {
			m.syncCompletions()
			m.syncViewport()
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	m.atBottom = m.viewport.AtBottom()

	return m, tea.Batch(cmds...)
}

func (m Model) browsingHistory() bool {
	return m.historyIdx < len(m.inputHistory)
}

func (m *Model) recordInput(text string) {
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
	}
	m.historyIdx = len(m.inputHistory)
}

func (m Model) sendUserMessage(text string) (tea.Model, tea.Cmd) {
	// A plan that has been through its list has answered "where are we", so
	// the next instruction retires it. One with steps left to go survives the
	// message, because that question is still open (S-104).
	if m.planRun != nil && m.planRun.complete() {
		m.planRun = nil
	}
	// The session has now said something of its own, so first contact is
	// over: /clear empties the transcript without making it new again (S-105).
	m.spendStartScreen()
	m.clearRetryChain()
	m.turnCount++
	m.turnStarted, m.turnEnded = time.Now(), time.Time{}
	m.turnOpen, m.turnOutcome = true, components.TurnDone
	m.turnTokensIn, m.turnTokensOut = 0, 0
	m.vitals.startTurn()
	// A fresh user turn clears the notice rail's denial alert (S-082);
	// lastDenial stays for /mode why.
	m.denialNotice = ""
	m.recordCheckpoint(text)
	m.agent.StartTurn(text)
	m.appendEntry(entry{kind: entryUser, text: text})
	m.trimForRequest()
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, m.requestStream()
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "Initializing…"
	}

	contentWidth := m.width - horizontalPadding*2
	// The body renders into the transcript pane; the header, divider and the
	// prompt frame span both panes (S-092, §15).
	paneWidth := m.transcriptWidth()

	title := m.title
	if title == "" {
		title = "shhh chat"
	}
	// The header carries only the title (S-082): the static key hint moved
	// into the frame's contextual bottom rail, the update notice onto the
	// notice rail, and the attached breadcrumb onto the frame's top rail.
	header := headerStyle.Render(" " + title)
	if m.attachedTo != "" && !m.frameShowing() {
		// A takeover surface while attached keeps the breadcrumb visible.
		header += headerHintStyle.Render("  " + m.breadcrumb())
	}
	header += strings.Repeat(" ", max(0, contentWidth-lipgloss.Width(header)))

	topDivider := dividerStyle(contentWidth)

	var body string
	switch {
	case m.state == stateDiffFull && m.fullDiff != nil:
		// The full-screen diff takes over the viewport (S-074, §3c).
		m.fullDiff.Height = m.viewportHeight()
		body = m.fullDiff.View(paneWidth)
	case m.state == stateReview && m.review != nil:
		// Review mode takes over the whole surface (S-099, §16a).
		m.review.Height = m.viewportHeight()
		body = m.review.View(paneWidth)
	case m.attachedTo != "":
		// The attached child's session fills the surface; its liveness shows
		// in the child-scoped status bar, not a parent spinner.
		body = m.viewport.View()
	case m.state == stateStreaming && m.streaming == "":
		label := "Thinking…"
		switch {
		case m.compacting:
			label = "Compacting…"
		case m.agent.Executing():
			label = "Running tools…"
		}
		body = m.viewport.View() + "\n" + m.spinner.View() + " " + label
	case m.state == stateRunningCmd:
		if m.pendingApproval != nil && m.pendingApproval.kind != approvalExec {
			body = m.viewport.View() + "\n" + m.spinner.View() + " Applying changes…"
		} else {
			// The running command renders as a live activity row whose tail is
			// its last output line (S-075); spinner ticks keep it fresh.
			body = m.viewport.View() + "\n" + m.runningCommandRow(paneWidth)
		}
	case m.state == stateClassifying:
		body = m.viewport.View() + "\n" + m.spinner.View() + " Checking permission…"
	case m.state == stateRetryWait && m.retry != nil:
		// The failure row is already in the transcript; this is the part of
		// it that drains (S-107, §17a). A wait is a meter, never a spinner.
		body = m.viewport.View() + "\n" + m.retryWaitBlock(paneWidth)
	case m.state == stateModelList:
		body = m.viewport.View() + "\n" + m.spinner.View() + " Listing models…"
	default:
		body = m.viewport.View()
	}

	// Working sub-agents render as compact progress rows above the divider
	// (S-068); hidden while the agent list or an attached view covers them.
	if m.agentRowsHeight() > 0 {
		if rows := m.renderAgentRows(paneWidth); rows != "" {
			body += "\n" + rows
		}
	}

	// Past 130 content columns the body shares its rows with the inspector
	// rail (S-092, §15); the split is horizontal only, so the row budget the
	// chrome accounting handed out is unchanged.
	if m.twoPane() {
		body = m.splitPanes(body)
	}

	// The command-center frame is the default bottom panel (S-082,
	// DESIGN-TUI.md §12); takeover surfaces replace it wholesale and keep the
	// divider + status-bar stack, as does the sub-minFrameWidth plain layout.
	var bottom string
	if m.frameShowing() {
		bottom = m.renderPromptFrame()
	} else {
		inputView := m.input.View()
		// The slash-command completion menu renders under the input (S-078);
		// the takeover surfaces below replace it wholesale.
		if m.completionActive() && m.attachedTo == "" && m.agentList == nil && m.activeChildAsk() == nil {
			inputView += "\n" + strings.Join(m.completionMenuLines(), "\n")
		}
		switch m.state {
		case stateConfirmRun:
			inputView = m.renderConfirm()
		case statePlanApprove:
			inputView = m.renderPlanApprove()
		case stateRewindPick:
			inputView = m.renderRewindPick()
		case statePick:
			inputView = m.renderPick()
		case stateFocus:
			inputView = m.renderFocusHint()
		case stateDiffFull:
			inputView = m.renderDiffFullHint()
		case stateReview:
			inputView = m.renderReviewHint()
		case stateUndoConfirm:
			inputView = m.renderUndoConfirm()
		case stateKeyEntry:
			inputView = m.renderKeyEntry()
		}
		// The agent manager list takes the bottom panel while open (S-077).
		if m.agentList != nil {
			inputView = m.renderAgentList()
		}
		// A child agent's routed approval takes over the bottom panel when the
		// parent's own prompts aren't using it (S-068).
		if ask := m.activeChildAsk(); ask != nil {
			inputView = m.renderChildAsk(ask)
		}
		bottom = dividerStyle(contentWidth) + "\n" + m.renderStatusBar(contentWidth) + "\n" + inputView
	}

	content := header + "\n" + topDivider + "\n" + body + "\n" + bottom
	return lipgloss.NewStyle().Padding(0, horizontalPadding).Render(content)
}

// startRun resolves which code block from the last response to execute.
// It returns either a message for the transcript, or entersConfirm=true after
// switching to the confirmation state. Bare /run takes the first block: the
// several-blocks case is routed to the picker (S-081) before it gets here.
func (m *Model) startRun(parts []string) (result string, entersConfirm bool) {
	if m.runFn == nil {
		return "Command execution is not available in this session.", false
	}
	blocks := extractCodeBlocks(m.lastAssistantText())
	if len(blocks) == 0 {
		return "No code blocks in the last response to run.", false
	}
	idx := 0
	if len(parts) > 1 {
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 || n > len(blocks) {
			return fmt.Sprintf("Usage: /run [1-%d]", len(blocks)), false
		}
		idx = n - 1
	}
	m.pendingRun = blocks[idx]
	m.pendingBlast = m.resolveRadius(nil)
	m.clearQueueStrip()
	m.setTurnState(stateConfirmRun)
	return "", true
}

// updateConfirmRun routes confirm-prompt keys through the approval card
// (S-076); the card's y/n/esc semantics match the original prompt, and [a]
// (S-054) is offered only where a session grant is allowed.
func (m Model) updateConfirmRun(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+d" {
		m.quitting = true
		return m, m.quitCmd()
	}
	// A memory proposal (S-070) confirms through its own prompt, not the card.
	if m.memoryAsk != nil {
		return m.updateMemoryAsk(msg)
	}
	done, result := m.approvalCard().Update(msg)
	if !done {
		return m, nil
	}
	switch result {
	case components.ApprovalApprove:
		if m.pendingApproval != nil {
			m.recordDecision(decisionAllow, "user")
		}
		if m.pendingApproval != nil && m.pendingApproval.kind != approvalExec {
			return m.executeApprovedTool()
		}
		return m.executeRun()
	case components.ApprovalFullDiff:
		// [d] opens the pending edit full screen (S-074); esc returns here
		// with the approval still pending.
		if req := m.pendingApproval; req != nil && req.kind == approvalDiff {
			return m.openDiffFull(&components.DiffView{
				Path:   req.path,
				Verb:   req.verb,
				Hunks:  req.hunks,
				Syntax: diffSyntax(req.path),
			}, stateConfirmRun)
		}
	case components.ApprovalBatch:
		// [A] answers this decision and every queued decision the session
		// would classify the same way (S-102). Membership was on the strip
		// before the key applied it, and a flagged action was never in it.
		if req := m.pendingApproval; req != nil && len(m.pendingBatch) > 0 {
			m.approveBatch()
			m.recordDecision(decisionAllow, "user-batch")
			if req.kind == approvalExec {
				return m.executeRun()
			}
			return m.executeApprovedTool()
		}
	case components.ApprovalAlways:
		// Approve and auto-allow this category for the session (S-054).
		// Safety-flagged commands, generic gated tools, and /run keep asking
		// (the card offers [a] only where a grant is allowed).
		if req := m.pendingApproval; req != nil {
			switch req.kind {
			case approvalExec:
				m.recordDecision(decisionAllow, "user-always")
				m.allowAllCommands = true
				m.syncChildGrants()
				return m.executeRun()
			case approvalDiff:
				m.recordDecision(decisionAllow, "user-always")
				m.allowAllEdits = true
				m.syncChildGrants()
				return m.executeApprovedTool()
			}
		}
	case components.ApprovalDeny:
		if m.pendingApproval != nil {
			return m.declineApproval()
		}
		m.pendingRun = ""
		m.setTurnState(stateInput)
		m.syncViewport()
		m.appendEntry(entry{kind: entrySystem, text: "Run cancelled."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	}
	return m, nil
}

func execToolResult(output string, exitCode int) string {
	return tools.FormatExecResult(output, exitCode)
}

func (m Model) executeRun() (tea.Model, tea.Cmd) {
	command := m.pendingRun
	m.pendingRun = ""
	m.setTurnState(stateRunningCmd)
	m.runningCommand = command
	m.runStart = time.Now()
	tail := &commandTail{}
	m.runTail = tail
	m.syncViewport()
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	runID := m.agent.RunID()
	runFn := m.runFn
	tailFn := m.tailRunFn
	// Assistant commands run contained when a mechanism is available (S-062);
	// /run — the user's own command — stays on the plain runner.
	if m.pendingApproval != nil && m.containment.Run != nil {
		runFn = m.containment.Run
		tailFn = m.containment.TailRun
	}
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		start := time.Now()
		var out string
		var code int
		// The tail-capable runner feeds the live row (S-075) when wired.
		if tailFn != nil {
			out, code = tailFn(ctx, command, tail.Set)
		} else {
			out, code = runFn(ctx, command)
		}
		return cmdDoneMsg{runID: runID, command: command, output: out, exitCode: code, duration: time.Since(start)}
	})
}

// commandContextMessage is appended to the conversation (as the user) so the
// model can see what a /run produced, without triggering a response.
func commandContextMessage(command, output string, exitCode int) string {
	if cut, truncated := tools.TruncateOutput(output, tools.MaxExecOutputBytes); truncated {
		output = cut + "\n… (output truncated)"
	}
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return fmt.Sprintf("I ran this command:\n```\n%s\n```\nExit code: %d\nOutput:\n```\n%s\n```", command, exitCode, output)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// resumeToolLoop requests the next model response after a round of tool
// results — unless this turn has hit the tool-round cap, in which case the
// loop pauses and control returns to the user (a fresh message continues the
// conversation and resets the counter).
func (m Model) resumeToolLoop() (tea.Model, tea.Cmd) {
	// Steering messages queued mid-turn join the conversation here, between
	// tool rounds, so the model sees them on its next request (S-058). They
	// count as fresh user input, so they also lift a hit round cap.
	if m.injectSteering() {
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
	}
	if m.agent.CapReached() {
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
			"Paused after %d tool rounds this turn. Send a message (e.g. \"continue\") to keep going.",
			m.agent.Rounds())})
		m.setTurnState(stateInput)
		m.syncViewport()
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd()
	}
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.trimForRequest()
	m.syncViewport()
	return m, m.requestStream()
}

func (m Model) requestStream() tea.Cmd {
	msgs := m.agent.RequestMessages()
	// Plan mode injects planning instructions into the request's system
	// prompt (S-061); the stored conversation stays untouched, so leaving
	// plan mode stops the injection.
	if m.mode == agent.ModePlan && len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		msgs[0].Content += "\n\n" + prompt.PlanModeInstructions
	}
	return m.requestStreamFor(msgs)
}

// requestStreamFor starts a stream over an explicit message list (callers
// pass a copy so in-flight requests are immune to later mutation).
func (m Model) requestStreamFor(msgs []provider.Message) tea.Cmd {
	a := m.agent
	return func() tea.Msg {
		events, cancel, err := a.Stream(msgs)
		if err != nil {
			return streamErrMsg{err: err}
		}
		return streamStartedMsg{events: events, cancel: cancel}
	}
}

func (m Model) execToolsCmd(calls []provider.ToolCall) tea.Cmd {
	a := m.agent
	runID := a.RunID()
	return func() tea.Msg {
		return toolResultsMsg{runID: runID, results: a.ExecuteCalls(calls)}
	}
}

// waitForEvent reads the next stream event. If it is a token, any further
// tokens already buffered on the channel are drained into a single batch so
// the UI re-renders once per batch instead of once per token.
func waitForEvent(events <-chan provider.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return doneMsg{}
		}
		if final := terminalMsg(ev); final != nil {
			return final
		}
		var batch strings.Builder
		batch.WriteString(ev.Token)
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return tokenMsg{text: batch.String(), final: doneMsg{}}
				}
				if final := terminalMsg(ev); final != nil {
					return tokenMsg{text: batch.String(), final: final}
				}
				batch.WriteString(ev.Token)
			default:
				return tokenMsg{text: batch.String()}
			}
		}
	}
}

// terminalMsg converts a non-token stream event into its message, or returns
// nil for a plain token event.
func terminalMsg(ev provider.StreamEvent) tea.Msg {
	if ev.Err != nil {
		// The completed tool calls ride the failure (S-107): a stream that
		// broke after the model finished writing a call kept that call.
		return streamErrMsg{err: ev.Err, calls: ev.ToolCalls}
	}
	if len(ev.ToolCalls) > 0 {
		return toolCallsMsg{calls: ev.ToolCalls, usage: ev.Usage}
	}
	if ev.Done {
		return doneMsg{usage: ev.Usage}
	}
	return nil
}

// maxToolResultLines bounds an activity row's detail view when it isn't
// explicitly expanded (failed-row auto-expansion, high verbosity).
const maxToolResultLines = 8

func formatToolArgs(raw string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	var parts []string
	for k, v := range m {
		switch val := v.(type) {
		case string:
			parts = append(parts, k+"="+val)
		default:
			b, _ := json.Marshal(val)
			parts = append(parts, k+"="+string(b))
		}
	}
	return strings.Join(parts, " ")
}

// accumulateUsage folds one request's usage into the session vitals and
// reads the running totals back out, so the rail, the cockpit and /stats all
// quote the same numbers from one place (S-093).
func (m *Model) accumulateUsage(u *provider.Usage) {
	if u == nil {
		return
	}
	cost, priced := m.usageCost(*u)
	m.vitals.record(m.modelName, *u, cost, priced)
	m.TotalTokensIn, m.TotalTokensOut = m.vitals.totalIn, m.vitals.totalOut
	m.turnTokensIn, m.turnTokensOut = m.vitals.current.In, m.vitals.current.Out
	m.contextTokens = m.vitals.lastContext
	m.notifyUsage()
}

func (m *Model) finishStreaming() {
	if m.compacting {
		// A cancelled compaction discards the partial summary and keeps the
		// conversation unchanged (the success path goes through finishCompact).
		m.compacting = false
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.appendEntry(entry{kind: entrySystem, text: "Compaction cancelled; conversation unchanged."})
		m.setTurnState(stateInput)
		return
	}
	if m.streaming != "" {
		m.agent.Append(provider.Message{
			Role:    provider.RoleAssistant,
			Content: m.streaming,
		})
		m.appendEntry(entry{kind: entryAssistant, text: m.streaming})
	}
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.setTurnState(stateInput)
}

// cancelStreaming aborts an in-flight response or tool run. Any pending tool
// calls get synthetic error results so the conversation stays well-formed for
// the next request.
func (m *Model) cancelStreaming() {
	if m.cancel != nil {
		m.cancel()
	}
	// Ctrl+C cancels the whole child tree with the turn (S-068).
	m.cancelSubagents()
	for _, tc := range m.agent.CancelTurn() {
		m.appendEntry(entry{kind: entryTool, toolName: tc.Name, toolArgs: tc.Arguments, toolResult: cancelledToolResult})
	}
	m.pendingApproval = nil
	m.memoryAsk = nil
	// The queue the strip described is gone with the turn, and so is every
	// batch grant made against it (S-102).
	m.clearQueueStrip()
	m.batchApproved, m.approvalTotal = nil, 0
	// Ctrl+C is a cancellation, and the close rows say so (S-098).
	m.turnOutcome = components.TurnCancelled
	m.finishStreaming()
	m.restoreSteering()
	// Restored steering empties the queue: the notice rail may shrink (S-082).
	m.syncViewport()
}

// injectSteering appends queued steering messages to the conversation and
// transcript as user messages, reporting whether any were queued. Steering is
// fresh user input, so it resets the tool-round counter (S-053 semantics).
func (m *Model) injectSteering() bool {
	if len(m.steering) == 0 {
		return false
	}
	for _, text := range m.steering {
		m.recordCheckpoint(text)
		m.agent.Append(provider.Message{Role: provider.RoleUser, Content: text})
		m.appendEntry(entry{kind: entryUser, text: text})
	}
	m.turnCount += int64(len(m.steering))
	m.steering = nil
	m.denialNotice = ""
	m.agent.ResetRounds()
	m.syncViewport()
	return true
}

// dispatchSteering turns queued steering messages into a fresh user turn once
// the current turn has ended: it injects them and opens the next stream.
// Returns nil when nothing was queued.
func (m *Model) dispatchSteering() tea.Cmd {
	if !m.injectSteering() {
		return nil
	}
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.trimForRequest()
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return tea.Batch(m.spinner.Tick, m.requestStream(), m.autosaveCmd())
}

// restoreSteering returns queued-but-uninjected steering messages to the input
// when a turn ends abnormally (cancel, stream error), so nothing typed is
// silently lost.
func (m *Model) restoreSteering() {
	if len(m.steering) == 0 {
		return
	}
	parts := m.steering
	if cur := m.input.Value(); strings.TrimSpace(cur) != "" {
		parts = append(parts, cur)
	}
	m.input.SetValue(strings.Join(parts, "\n"))
	m.steering = nil
}

func (m *Model) appendEntry(e entry) {
	m.transcript = append(m.transcript, e)
}

func (m *Model) resetTranscript() {
	m.transcript = nil
	// The checklist is read off the transcript, so a transcript that is gone
	// takes the approved plan with it rather than pointing at entries that no
	// longer exist (S-104).
	m.planRun = nil
	m.invalidateRenderCache()
}

// invalidateRenderCache forces the next renderHistory to re-render every
// entry (used when an entry's rendering changes in place, e.g. focus-mode
// expansion).
func (m *Model) invalidateRenderCache() {
	m.cachedRender = ""
	m.cachedCount = 0
	m.cachedSep, m.cachedHasSep = entry{}, false
}

// renderEntry renders one entry's own lines, always ending in exactly one
// newline and never in a trailing blank line. Spacing between entries is not
// an entry's business — separatorBefore owns it, so every caller that
// concatenates entries gets the same rhythm.
func (m Model) renderEntry(e entry, width int) string {
	switch e.kind {
	case entryUser:
		return userStyle.Render("You") + "\n" + m.wordWrap(e.text, width) + "\n"
	case entryAssistant:
		return assistantStyle.Render("Assistant") + "\n" + renderMarkdown(e.text, width) + "\n"
	case entryTool, entryCommand:
		// Compact one-row activity rendering (S-075); focus mode expands it.
		return m.activityRowFor(e).View(width) + "\n"
	case entryTurnClose:
		if e.close == nil {
			return ""
		}
		return e.close.View(width) + "\n"
	case entryFailure:
		return m.failureRow(e).View(width) + "\n"
	case entryStreamDrop:
		return m.dropRow(e).View(width) + "\n"
	case entryDiff:
		if e.diff == nil {
			return ""
		}
		// While its full-screen view is showing, the transcript behind keeps
		// the bounded expanded form.
		if e.diff.Mode == components.DiffFull {
			return strings.Join(e.diff.ExpandedLines(width), "\n") + "\n"
		}
		return e.diff.View(width) + "\n"
	case entrySystem:
		return systemMsgStyle.Render(e.text) + "\n"
	case entryError:
		return errorStyle.Render("Error: "+e.text) + "\n"
	}
	return ""
}

// entryIsBlock reports whether an entry reads as a standalone block — a
// conversational turn, a diff, or a notice long enough to wrap onto its own
// lines — rather than as a row in the compact activity feed (§6).
func entryIsBlock(e entry) bool {
	switch e.kind {
	case entryUser, entryAssistant, entryDiff, entryTurnClose:
		return true
	case entrySystem, entryError:
		return strings.Contains(strings.TrimSpace(e.text), "\n")
	}
	return false
}

// separatorBefore returns the spacing between two adjacent entries: one blank
// line whenever either side is a block, and nothing between feed rows, so
// activity rows and one-line notices pack tight while turns keep their air.
func separatorBefore(prev, cur entry) string {
	if entryIsBlock(prev) || entryIsBlock(cur) {
		return "\n"
	}
	return ""
}

// renderStatusBar renders the cockpit rail (S-075, DESIGN-TUI.md §8): the
// active mode, tool-round counter, context occupancy meter (colored at the
// S-055 thresholds), usage and spend, queued steering, policy grants, and the
// sub-agent badge, with the model name right-aligned and dropped first when
// narrow.
func (m Model) renderStatusBar(width int) string {
	// Attached, the status bar scopes to the focused child (S-077).
	if m.attachedTo != "" && m.subagents != nil {
		return m.renderChildStatusBar(width)
	}
	return m.cockpitData(true).View(width)
}

// cockpitData assembles the cockpit segments (§8). The frame's vitals rail
// (S-082) omits the queued-steering extra — the notice rail carries it — so
// includeQueued is false there.
func (m Model) cockpitData(includeQueued bool) components.Cockpit {
	c := components.Cockpit{
		CtxPct:   -1,
		WarnPct:  warnThresholdPercent,
		AlertPct: trimThresholdPercent,
		Model:    m.modelName,
	}
	if m.turnState() == stateClassifying {
		c.Mode, c.ModeKind = "checking", components.CockpitChecking
	} else {
		c.Mode = strings.ReplaceAll(m.mode.String(), "-", " ")
		switch m.mode {
		case agent.ModeAcceptEdits, agent.ModeAuto:
			c.ModeKind = components.CockpitPermissive
		default:
			c.ModeKind = components.CockpitGated
		}
	}
	// Round counter shows only mid-turn, so long tool loops are visible.
	if m.agent.Rounds() > 0 && m.turnState() != stateInput {
		c.Round = fmt.Sprintf("round %d/%d", m.agent.Rounds(), m.effectiveMaxToolRounds())
	}
	if m.TotalTokensIn != 0 || m.TotalTokensOut != 0 {
		c.Tokens = fmt.Sprintf("↑%s ↓%s", formatTokenCount(m.TotalTokensIn), formatTokenCount(m.TotalTokensOut))
		if label := m.spendLabel(m.TotalTokensIn, m.TotalTokensOut); strings.HasPrefix(label, "$") {
			c.Spend = label
		}
		if tokens := m.estimatedContextTokens(); tokens > 0 {
			c.CtxPct = int(tokens * 100 / m.contextWindow())
		}
	}
	// Steering messages waiting to be injected (S-058).
	if n := len(m.steering); n > 0 && includeQueued {
		c.Extra = append(c.Extra, fmt.Sprintf("queued %d", n))
	}
	// Active approval policy (S-054); absent in the default ask-everything state.
	if p := m.policyLabel(); p != "" {
		c.Extra = append(c.Extra, p)
	}
	// Working sub-agents, with blocked-on-approval count (S-068).
	if m.subagents != nil {
		c.Agents, c.AgentsBlocked = m.subagents.ActiveCounts()
	}
	return c
}

func formatTokenCount(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func (m *Model) renderHistory() string {
	if m.state == stateFocus {
		// Focus mode renders fresh with the selection gutter, bypassing the
		// incremental cache; it scopes to whichever agent is focused (S-077).
		content, _, _ := m.renderFocusHistory()
		return content
	}
	// Attached view (S-077): the focused child's session, rendered fresh from
	// the supervisor's live transcript (the parent's cache is untouched).
	if m.attachedTo != "" && m.subagents != nil {
		return m.renderAttachedHistory()
	}
	if len(m.transcript) == 0 && m.turnState() != stateStreaming {
		// First contact (S-105): the empty session states what it already
		// knows about the project and offers work. Hosts without a survey —
		// the attached child view, a bare test model — keep the plain line.
		if m.startScreenShowing() {
			return m.renderStartScreen(m.transcriptWidth())
		}
		return welcomeStyle.Render("Type a message to start chatting.")
	}
	w := m.transcriptWidth()
	if w != m.cachedWidth {
		m.cachedWidth = w
		m.invalidateRenderCache()
	}
	// History renders as step blocks (S-090, §13). Every block but the last
	// is frozen — the grouping scan is left to right, so a block that already
	// has a successor can never change — and only the last one re-renders
	// each frame, because a running step's header restates its count and
	// duration as rows land.
	blocks := m.blocksOf(m.transcript)
	// Freeze everything before the last block rows can still land in. With an
	// approved plan that is not the last block: its declared-but-not-started
	// steps trail it, and they change as the run reaches them (S-104).
	for bi := 0; bi < lastLiveBlock(blocks); bi++ {
		blk := blocks[bi]
		if blk.end <= m.cachedCount {
			continue
		}
		block, prev, have := joinUnits(m.blockUnits(blk, m.transcript, w, false, -1), m.cachedSep, m.cachedHasSep)
		m.cachedRender += block
		m.cachedSep, m.cachedHasSep = prev, have
		m.cachedCount = blk.end
	}
	s := m.cachedRender
	prev, havePrev := m.cachedSep, m.cachedHasSep
	for _, blk := range blocks {
		if blk.end <= m.cachedCount {
			continue
		}
		var block string
		block, prev, havePrev = joinUnits(m.blockUnits(blk, m.transcript, w, false, -1), prev, havePrev)
		s += block
	}
	if m.turnState() == stateStreaming && m.streaming != "" {
		if havePrev {
			s += separatorBefore(prev, entry{kind: entryAssistant})
		}
		s += assistantStyle.Render("Assistant") + "\n"
		s += renderMarkdown(m.streaming, w)
	}
	return s
}

func (m Model) contentWidth() int {
	return m.width - horizontalPadding*2
}

func (m Model) viewportHeight() int {
	h := m.height - m.bottomPanelHeight() - chromeHeight - m.agentRowsHeight() - m.frameExtraHeight() - m.retryWaitHeight()
	if h < 1 {
		return 1
	}
	return h
}

func (m Model) wordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if lipgloss.Width(line) <= width {
			result.WriteString(line)
			result.WriteByte('\n')
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			result.WriteByte('\n')
			continue
		}
		lineLen := 0
		for i, word := range words {
			wLen := lipgloss.Width(word)
			if i > 0 && lineLen+1+wLen > width {
				result.WriteByte('\n')
				lineLen = 0
			} else if i > 0 {
				result.WriteByte(' ')
				lineLen++
			}
			result.WriteString(word)
			lineLen += wLen
		}
		result.WriteByte('\n')
	}
	return strings.TrimRight(result.String(), "\n")
}

func dividerStyle(width int) string {
	return lipgloss.NewStyle().
		Foreground(components.Palette.Dim).
		Render(strings.Repeat("─", width))
}

func (m *Model) handleSlashCommand(text string) (handled bool, result string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false, ""
	}

	switch parts[0] {
	case "/help":
		return true, helpText() + "\n\n" + m.policyHelp()

	case "/clear", "/new":
		m.clearConversation()
		return true, "Started a new conversation."

	case "/model":
		if len(parts) < 2 {
			if m.modelName != "" {
				return true, fmt.Sprintf("Current model: %s\n%s", m.modelName, modelUsage)
			}
			return true, modelUsage
		}
		// /model default [name] and /model agents [name] persist a default to
		// the config file instead of switching this session only (S-086).
		if parts[1] == "default" || parts[1] == "agents" {
			return true, m.setModelDefault(parts[1], parts[2:])
		}
		if m.switchFn == nil {
			return true, "Model switching is not available in this session."
		}
		if len(parts) > 2 {
			return true, "Model names cannot contain spaces. " + modelUsage
		}
		name := parts[1]
		if name == m.modelName {
			return true, fmt.Sprintf("Already using %s.", name)
		}
		m.switchFn(name)
		m.modelName = name
		return true, fmt.Sprintf("Switched model to %s. (/model default %s makes it the default for new sessions.)", name, name)

	case "/mode":
		if len(parts) < 2 {
			return true, m.modeStatus()
		}
		if len(parts) > 2 {
			return true, "Usage: /mode [manual|accept-edits|auto|plan|why]"
		}
		if parts[1] == "why" {
			if m.lastDenial == "" {
				return true, "No auto-mode denials this session."
			}
			return true, "Last auto-mode denial:\n  " + m.lastDenial
		}
		mode, err := agent.ParseMode(parts[1])
		if err != nil {
			return true, "Error: " + err.Error()
		}
		m.applyMode(mode)
		return true, fmt.Sprintf("Mode set to %s — %s.", mode, mode.Describe())

	case "/stats":
		return true, m.statsReport()

	case "/ui":
		return true, m.uiCommand(parts)

	case "/sandbox":
		args := parts[1:]
		if len(args) == 0 {
			args = []string{"doctor"}
		}
		if m.containment.Manage != nil {
			return true, m.containment.Manage(args)
		}
		// No manager wired (older sessions/tests): doctor falls back to the
		// static report; everything else is unavailable.
		if len(args) == 1 && args[0] == "doctor" {
			if m.containment.Report == "" {
				return true, "Command containment is not configured in this session."
			}
			return true, m.containment.Report
		}
		return true, "Container sandbox management is unavailable in this session."

	case "/evidence":
		if m.evidence.Manage == nil {
			return true, "The evidence store is unavailable in this session."
		}
		return true, m.evidence.Manage(parts[1:])

	case "/gate":
		if m.gate.Manage == nil {
			return true, "The quality gate is unavailable in this session."
		}
		return true, m.gate.Manage(parts[1:])

	case "/ps":
		if m.processes.Manage == nil {
			return true, "The process supervisor is unavailable in this session."
		}
		return true, m.processes.Manage(parts[1:])

	case "/memory":
		if m.memory.Manage == nil {
			return true, "Durable memory is unavailable in this session."
		}
		return true, m.memory.Manage(parts[1:])

	case "/plan":
		// Bare /plan reopens the approved plan mid-turn, which is how the
		// checklist stays reachable below 130 columns, where there is no rail
		// to hold it (S-104).
		if len(parts) == 1 {
			return true, m.planStatus()
		}
		switch parts[1] {
		case "save":
			planText := m.lastAssistantText()
			if strings.TrimSpace(planText) == "" {
				return true, "No plan to save yet — there is no assistant response."
			}
			path, err := savePlan(planText, strings.Join(parts[2:], "-"))
			if err != nil {
				return true, "Error saving plan: " + err.Error()
			}
			return true, "Plan saved to " + path
		case "drop":
			if m.planRun == nil {
				return true, "No approved plan is running."
			}
			m.planRun = nil
			m.invalidateRenderCache()
			return true, "Dropped the approved plan — the outline goes back to inferring its steps."
		}
		return true, planUsage

	case "/rewind":
		// Only the numbered form arrives here; bare /rewind opens the picker
		// from the enter handler (S-069).
		if len(m.checkpoints) == 0 {
			return true, "No checkpoints to rewind to yet."
		}
		if len(parts) != 2 {
			return true, fmt.Sprintf("Usage: /rewind [<turn 1-%d>] — bare /rewind opens the picker", len(m.checkpoints))
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return true, fmt.Sprintf("Usage: /rewind [<turn 1-%d>]", len(m.checkpoints))
		}
		return true, m.rewindToTurn(n)

	case "/branches":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		branches, err := m.db.ListChatBranches(m.sessionName)
		if err != nil {
			return true, "Error: " + err.Error()
		}
		if len(parts) == 1 {
			return true, m.listBranches(branches)
		}
		return true, m.switchBranch(branches, strings.Join(parts[1:], " "))

	case "/copy":
		text := m.lastAssistantText()
		if text == "" {
			return true, "Nothing to copy yet."
		}
		what := "response"
		if len(parts) > 1 && parts[1] == "code" {
			blocks := extractCodeBlocks(text)
			if len(blocks) == 0 {
				return true, "No code blocks in the last response."
			}
			text = strings.Join(blocks, "\n")
			what = "code"
		}
		cr := m.copyFn(text)
		if cr.Warning != "" {
			return true, cr.Warning
		}
		return true, "Copied last " + what + " to clipboard."

	case "/save":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		name := "unnamed"
		if len(parts) > 1 {
			name = strings.Join(parts[1:], " ")
		}
		if err := m.db.SaveChat(name, m.agent.Messages()); err != nil {
			return true, "Error saving: " + err.Error()
		}
		// Future rewind branches hang off the named session (S-069).
		m.sessionName = name
		return true, fmt.Sprintf("Chat saved as %q", name)

	case "/load":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		if len(parts) < 2 {
			// Only reached when there is nothing to pick; otherwise bare
			// /load opens the picker from the enter handler (S-080).
			_, listing := m.handleSlashCommand("/chats")
			return true, listing + "\n\nUsage: /load <name>"
		}
		return true, m.loadChatByName(strings.Join(parts[1:], " "))

	case "/chats":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		entries, err := m.db.ListChats()
		if err != nil {
			return true, "Error: " + err.Error()
		}
		if len(entries) == 0 {
			return true, "No saved chats."
		}
		var sb strings.Builder
		sb.WriteString("Saved chats:\n")
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("  %s  (%s)\n", e.Name, sessionDesc(e.Turns, e.UpdatedAt)))
		}
		return true, strings.TrimRight(sb.String(), "\n")

	default:
		// A lone "/word" is almost certainly a mistyped command; a path like
		// /etc/hosts contains another slash and falls through to the LLM.
		if strings.HasPrefix(parts[0], "/") && !strings.Contains(parts[0][1:], "/") {
			return true, fmt.Sprintf("Unknown command %s. Type /help for available commands.", parts[0])
		}
		return false, ""
	}
}

// loadChatByName replaces the working conversation with a saved chat. Both
// /load <name> and the /load picker (S-080) come through here.
func (m *Model) loadChatByName(name string) string {
	msgs, err := m.db.LoadChat(name)
	if err != nil {
		return "Error: " + err.Error()
	}
	m.loadConversation(msgs)
	m.sessionName = name
	return fmt.Sprintf("Loaded chat %q (%d messages)", name, len(msgs))
}

// lastAssistantText returns the content of the most recent assistant message
// that has any text.
func (m Model) lastAssistantText() string {
	msgs := m.agent.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleAssistant && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}

func helpText() string {
	return strings.TrimSpace(`Commands:
  /help          Show this help
  /clear         Start a new conversation (also /new)
  /copy [code]   Copy the last response (or just its code blocks)
  /run [n]       Run a code block from the last response (with confirmation)
  /model [name]  Switch the model (bare /model opens an interactive picker)
  /model default [name]   Show or persist the default model for new sessions
  /model agents [name]    Show or persist the model sub-agents run on
                 ("inherit" follows the session model)
  /mode [name]   Set the permission mode (manual, accept-edits, auto, plan);
                 bare /mode opens an interactive picker
  /mode why      Show the latest auto-mode denial's reason
  /stats         Context occupancy breakdown and cumulative session spend
  /ui            Activity feed density and pane layout: /ui verbosity <low|normal|high>
                 (low hides counts, med collapses rows, high expands rows)
  /sandbox       Containment status and container sandboxes (doctor|list|status|destroy <id>|prune)
  /evidence      Tool-output evidence store: reduction stats and size (purge to clear)
  /gate          Quality gate: run [suite] starts the project's checks in the background, result shows the verdict
  /ps            List the long-running processes this session owns (process tool)
  /memory        Durable memories: list (default) · add [global] [kind] <text> · forget <id>
  /agents        Agent manager: attach, steer, cancel, kill sub-agents (also Ctrl+A)
  /attach [name] Attach to an agent's session and steer it (bare /attach lists)
  /detach        Back to your own session (also Esc while attached)
  /plan          The approved plan as a checklist, with anything that has
                 departed from it · save [name] writes the last plan/response
                 to .shhh/plans/ · drop forgets an approved plan
  /diff          Show what this session changed, full screen — read from the
                 session's own changeset, so it works outside a git repository
  /review [turn] Review what a turn changed: file list, hunks, staging per
                 hunk (bare reviews the last turn that changed anything).
                 Also [v] on a turn's changeset row. Nothing is applied.
  /undo [turn]   Put back what a turn changed, from the session's own records
                 (not git). Asks first, names anything that changed since,
                 and is itself recorded as a turn. Also [u] on the row.
  /compact       Summarize the conversation and continue from the summary
  /rewind [n]    Rewind to before a user turn (bare /rewind picks interactively);
                 the abandoned tail is kept as a branch. Conversation only —
                 files on disk are not restored.
  /branches [n]  Switch this session's branches (bare /branches opens a picker)
  /save [name]   Save this chat
  /load [name]   Load a saved chat (bare /load opens a picker)
  /chats         Saved chats — opens the same picker; enter loads
  /exit          Quit (also /quit, /q)

Commands run while the agent is working — including while sub-agents are in
flight, which is the only time they exist. The exceptions are the ones that
rewrite or replace the running conversation (/clear, /compact, /rewind,
/branches, /load, /chats, /model, /run); they say so and wait for the turn.

Keys:
  Enter          Send message        Alt+Enter    Insert newline
  Tab            Complete a slash command (typing / opens the menu;
                 ↑↓ move, Enter runs the highlighted command, Esc dismisses)
  Shift+Tab      Cycle the permission mode
                 (while the agent is working, Enter queues a steering message
                  that joins the conversation before the next model request)
  Up/Down        Recall previous inputs (when the input is empty)
  Ctrl+E         Focus mode: select tool/command/diff rows (j/k), expand/collapse (Enter), Esc back
                 (Enter on an edit row cycles collapsed → expanded → full-screen diff;
                  opens over a running turn, which keeps streaming underneath)
  Ctrl+A         Agent manager: enter attaches to an agent's session, x cancels
                 its turn, X kills it; attached, typing steers the agent,
                 Shift+Tab sets its mode (clamped), Esc detaches
  Esc            Clear the input
  Ctrl+C         Cancel response / clear input / quit
  Ctrl+D         Quit
  PgUp/PgDn      Scroll history
  y/n/a          Approval prompts: allow / deny / always allow this session`)
}

// clearConversation drops everything except the system prompt.
func (m *Model) clearConversation() {
	msgs := m.agent.Messages()
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		m.agent.SetMessages(msgs[:1:1])
	} else {
		m.agent.SetMessages(nil)
	}
	m.resetTranscript()
	m.checkpoints = nil
	m.contextTokens = 0
	m.vitals.reset()
	m.agent.ResetRounds()
	// The turn's accounting started over, so there is no longer a turn to
	// close with a summary either (S-098).
	m.turnOpen = false
}

// loadConversation replaces the current conversation and rebuilds the
// transcript from the stored messages.
func (m *Model) loadConversation(msgs []provider.Message) {
	// A loaded conversation is a session with a past; the start screen does
	// not come back after it is cleared (S-105).
	m.spendStartScreen()
	m.agent.SetMessages(msgs)
	m.resetTranscript()
	m.checkpoints = checkpointsFromMessages(msgs)
	for i, msg := range msgs {
		switch msg.Role {
		case provider.RoleUser:
			m.appendEntry(entry{kind: entryUser, text: msg.Content})
		case provider.RoleAssistant:
			if msg.Content != "" {
				m.appendEntry(entry{kind: entryAssistant, text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				var result string
				if i+1 < len(msgs) {
					for _, next := range msgs[i+1:] {
						if next.Role == provider.RoleTool && next.ToolCallID == tc.ID {
							result = next.Content
							break
						}
						if next.Role != provider.RoleTool {
							break
						}
					}
				}
				m.appendEntry(entry{kind: entryTool, toolName: tc.Name, toolArgs: tc.Arguments, toolResult: result})
			}
		}
	}
}
