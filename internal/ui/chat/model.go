package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/caps"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// sessionNameLayout is how a session that was never named is called in the
// store: the moment it began, to the second. Every session gets a slot of its
// own — the autosave used to go to one shared slot, which meant each new
// session silently overwrote the last and `--resume` only ever had one thing
// to offer. `--continue` reopens whichever slot was written most recently.
const sessionNameLayout = "2006-01-02 15:04:05"

// newSessionName mints the slot a fresh conversation autosaves to. Two slots
// minted in the same second by one process (a /clear pressed twice) are told
// apart by a suffix, so neither can overwrite the other.
func newSessionName() string {
	sessionNameMu.Lock()
	defer sessionNameMu.Unlock()
	name := time.Now().Format(sessionNameLayout)
	if name == lastSessionStamp {
		sessionNameDup++
		return fmt.Sprintf("%s (%d)", name, sessionNameDup+1)
	}
	lastSessionStamp, sessionNameDup = name, 0
	return name
}

var (
	sessionNameMu    sync.Mutex
	lastSessionStamp string
	sessionNameDup   int
)

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
	// stateClassifying: the auto-mode permission classifier is
	// deciding whether the pending approval may run without a prompt.
	stateClassifying
	// statePlanApprove: a completed planning response is awaiting the user's
	// decision — execute, keep planning, or reject.
	statePlanApprove
	// stateFocus: focus mode (docs/interface/surfaces.md#reading-mode) —
	// j/k moves a selection cursor over expandable transcript rows, enter
	// expands/collapses in place, esc returns to the input.
	stateFocus
	// stateDiffFull: a diff is showing full screen (
	// docs/interface/surfaces.md#the-diff-view) — from a transcript edit
	// row, an approval's [d], or /diff.
	stateDiffFull
	// stateRewindPick: the interactive /rewind checkpoint picker is showing.
	stateRewindPick
	// statePick: a generic slash-command picker (/model, /permissions) is
	// showing.
	statePick
	// stateModelList: bare /model is querying the provider's /v1/models
	// endpoint before opening the picker; esc cancels back to input.
	stateModelList
	// stateReview: review mode (
	// docs/interface/surfaces.md#the-turns-close) — the file list and hunk pane
	// of what a turn changed, with staging per hunk. A takeover: full width, the
	// rail hidden, esc returns.
	stateReview
	// stateUndoConfirm: the inline confirm an undo asks through (inline
	// confirm) — what it would restore, what has drifted since, and esc to
	// decline. It borrows the bottom panel, not the transcript.
	stateUndoConfirm
	// stateKeyEntry: the masked key prompt an auth failure's [k] opens
	//. It borrows the bottom panel; esc keeps the old key.
	stateKeyEntry
	// statePressure: the context-pressure card is up — the
	// occupancy, where the window went, and the three answers. It borrows
	// the bottom panel; esc keeps going.
	statePressure
	// stateRetryWait: the turn is waiting out a bounded retry behind the
	// countdown meter. It is a stage of the turn, not a
	// surface — but nothing is streaming and the input is not live, so the
	// wait owns the keyboard for the two keys it offers.
	stateRetryWait
	// stateContext: the context surface is up — the window drawn as a
	// wrapped meter, the categories beside it, and the tool breakdowns
	// folded under both. A takeover: full width, the rail hidden, esc
	// returns. It reads the session and changes nothing in it.
	stateContext
	// statePreview: a staged attachment is showing full-pane — a
	// picture, or the text of a file or a paste. It is the one surface that
	// is opened by naming a file rather than by a key, because the chip it
	// belongs to has no key of its own.
	statePreview
)

const inputHeight = 3
const headerHeight = 1
const dividerHeight = 1
const statusBarHeight = 1
const horizontalPadding = 2

type tokenMsg struct {
	text string
	// think is reasoning text from the same batch. It rides the token message
	// rather than a message of its own because the two arrive interleaved on
	// one channel, and two messages would be two repaints of one frame.
	think string
	// final carries a terminal event (doneMsg, streamErrMsg, toolCallsMsg)
	// that arrived in the same batch, so it isn't lost when tokens are drained.
	final tea.Msg
}
type doneMsg struct{ usage *provider.Usage }

// streamErrMsg carries a failed stream back to the session. calls are the
// tool calls the model had *finished* writing before the wire broke, which is
// what makes continuing from a drop possible; it is empty for every
// failure that never got that far.
type streamErrMsg struct {
	err       error
	calls     []provider.ToolCall
	reasoning []provider.ReasoningBlock
}

// retryTickMsg is defined with the rest of the retry path in resume.go.
type streamStartedMsg struct {
	events <-chan provider.StreamEvent
	cancel context.CancelFunc
}
type toolCallsMsg struct {
	calls     []provider.ToolCall
	usage     *provider.Usage
	reasoning []provider.ReasoningBlock
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
// picker; err falls the session back to the curated catalog.
type modelListMsg struct {
	names []string
	err   error
}

// classifierDoneMsg carries the auto-mode classifier's verdict for the
// pending approval.
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
	// entryDiff: an applied edit/write rendered as a diff row.
	entryDiff
	// entryTurnClose: the rows a finished turn ends with.
	entryTurnClose
	// entryFailure: a classified provider failure rendered as a recovery row
	//. It is a row, not a modal, because it is part of the turn.
	entryFailure
	// entryStreamDrop: a reply that stopped halfway, rendered as the `stream`
	// recovery row and holding the partial it offers to continue from.
	entryStreamDrop
	// entryRoundPause: a turn that stopped at its tool-round ceiling,
	// rendered as the `rounds` recovery row. It stands in for
	// the turn's close block rather than sitting above one.
	entryRoundPause
	// entryFanout: the block a round that spawned two or more children
	// renders instead of their separate rows. It holds only the
	// batch number — the lanes are read off the supervisor every render.
	entryFanout
	// entryThink: the reasoning a round did, folded into one activity row
	// (think.go). It holds the readable text; the blocks the next
	// request replays are the agent's, not this row's.
	entryThink
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
	// activity row; zero hides it.
	duration time.Duration
	// expanded shows the full tool/command output instead of the truncated
	// block; toggled from focus mode.
	expanded bool
	// attached names what a user row's message carried — the names
	// and sizes, never the bytes. The transcript shows a screenshot as the
	// line "attached: shot.png (412 KB)" and nothing more.
	attached []string
	// diff is the entryDiff viewer; a pointer so focus-mode
	// expansion state survives re-renders.
	diff *components.DiffView
	// close is the entryTurnClose block: the raw counts a turn ended
	// with, so the rows re-render at any width like every other entry, and
	// turn is the turn it closed — what [v] and [u] act on.
	close *components.TurnClose
	turn  int64
	// fail is the classified provider failure behind an entryFailure row
	//. It is stored as the classification rather than as rendered
	// text, so the row re-renders at any width and the offered keys stay
	// derived from the class rather than parsed back out of a string.
	fail *provider.Failure
	// resume is what a dropped stream kept behind an entryStreamDrop row: the
	// partial text and the finished tool calls. It is a pointer so
	// that taking the offer marks this row spent wherever it is rendered from.
	resume *streamResume
	// pause is where a turn stopped at its round limit, behind an
	// entryRoundPause row. A pointer for the same reason: granting
	// the rounds spends the offer wherever the row is rendered from.
	pause *roundPause
	// fanout is the batch behind an entryFanout block. The lanes are
	// not stored: they are read off the supervisor at render time, which is
	// what keeps them live and what lets the block re-render at any width.
	fanout *fanoutBatch
	// deniedBy names who refused the call — decidedByYou for a decline at the
	// card, decidedByAuto for a rule — and renders the row as ⊘ rather than ✗
	// (docs/interface/principles.md#closed-vocabularies). Empty when
	// nothing was refused.
	deniedBy string
	// denyRule names the rule behind an auto denial, e.g. "plan mode".
	denyRule string
	// stepFold is your fold override for the step this entry titles (
	// docs/interface/surfaces.md#the-step); steps keep no layout state of their
	// own, so it lives on the raw entry and survives a resize.
	stepFold foldState
	// groupFold is the same override for the folded run of read-only calls
	// this entry heads.
	groupFold foldState
	// detailFold is the same override again for the detail bodies of the step
	// this entry titles — what ctrl+o opens and closes. It is a
	// third override rather than a level of the first two because it answers
	// a different question: stepFold and groupFold decide which rows are on
	// screen, this decides how much of each one is.
	detailFold foldState
	// thinkDepth is how much of an entryThink row's body is on screen — the
	// reader's own answer to [enter], recorded on the entry so the row
	// re-renders at any width like every other one.
	thinkDepth thinkDepth
	// thinkStreaming says the reasoning is still being written, which is what
	// spins the row. It is on the entry rather than on the Model for
	// the reason a pending tool result is: what a row is doing is part of the
	// row, and the render stays a function of the entry alone.
	thinkStreaming bool
	// planStep is the number of the approved plan's step this assistant
	// announcement carries out, offPlanStep when it carries out none of them,
	// and zero when no plan was running. It is stamped once, when the
	// entry is appended, so every reader of the outline stays a pure function
	// of the transcript.
	planStep int
}

type Model struct {
	// agent owns the loop state (message list, stream requests, tool
	// dispatch, approval queue, iteration guard); the Model is one front-end
	// driving it.
	agent    *agent.Agent
	db       *storage.DB
	copyFn   func(string) clipboard.Result
	runFn    func(context.Context, string) (string, int)
	switchFn func(string)

	viewport viewport
	input    textarea.Model
	spinner  spinner.Model
	// spawnRow is 1 + the transcript index of the current round's first spawn
	// row, or 0 once the round has none left to convert — the row a second
	// child of the same round turns into the fan-out block.
	spawnRow int
	// thinkIdx is 1 + the transcript index of this round's think row, or 0
	// where the round has not thought yet (think.go). It is the round's row
	// rather than the block's, so reasoning that arrives in three pieces
	// around two tool calls still lands on one row.
	thinkIdx int
	// spinFrame counts spinner ticks for the passive surfaces that draw a
	// frame themselves rather than animating one (the inspector rail's agent
	// lanes). It is the session's one frame counter: every surface
	// that moves reads it, and it advances only with m.spinner, so the three
	// places the one tick source names cannot report three different frames.
	spinFrame int
	// spinning reports whether a tick chain is in flight. It is what makes
	// "one tick source, never three" a property rather than a habit:
	// spinCmd starts a chain only when this is false (spin.go).
	spinning bool
	// streamDirty reports whether a chunk has landed that the transcript has
	// not been repainted for. It rides the tick above rather than adding a
	// clock of its own — the session is allowed one, and the streaming render
	// spends it on
	// this as well: a repaint per chunk was re-rendering an answer that grows
	// as it arrives, once per token.
	streamDirty bool

	transcript []entry
	// Incremental render cache: the rendered lines of entries
	// [0, cached.count), always a whole number of step blocks, with
	// the live tail rebuilt after them each frame (lines.go).
	cached lineCache
	// streamMD is the arriving message's own cache, keyed on nothing the
	// caches above are: it holds a render of the part of that one message that
	// can no longer change, so a chunk re-renders the tail rather than the
	// answer (streammd.go).
	streamMD streamingMarkdown

	// Input recall: inputHistory holds previously submitted inputs;
	// historyIdx == len(inputHistory) means "not browsing".
	inputHistory []string
	historyIdx   int

	streaming string
	events    <-chan provider.StreamEvent
	cancel    context.CancelFunc
	// state is the current surface: the stage of the session's own turn, or
	// a transient view borrowing the screen. turnBack parks the turn's stage
	// while a surface has it, so the turn keeps running underneath (
	// turn.go).
	state      state
	turnBack   state
	pendingRun string
	runCancel  context.CancelFunc
	// pendingBlast is the approval card's blast-radius block for the decision
	// showing now, resolved once when the confirm is armed because it
	// reads the filesystem and git.
	pendingBlast blastRadius
	// pendingScope is what that decision reaches outside the working scope
	//, resolved with the blast radius and consumed when the decision
	// is answered: approving it grants the directories, refusing it grants
	// nothing.
	pendingScope scopeReach
	// The approval queue made visible: pendingQueue is the strip
	// above the card, pendingBatch the queued call IDs [A] would answer with
	// the current one, and batchApproved those an earlier [A] already
	// answered — they run when they reach the head instead of asking again.
	// approvalTotal is how many decisions this tool round queued, so the
	// card can say "2 of 5" once two have been answered.
	pendingQueue  components.QueueStrip
	pendingBatch  []string
	batchApproved map[string]bool
	approvalTotal int
	// Compact activity feed: verbosity is the feed's default density
	// (/ui verbosity); tailRunFn is the tail-capable command runner, and
	// runningCommand/runStart/runTail drive the live row while a command runs.
	verbosity      verbosity
	tailRunFn      TailFunc
	runningCommand string
	runStart       time.Time
	runTail        *commandTail
	// runningTools is the auto-run batch currently executing, so the frame's
	// status line can name the call it is running. It is read
	// only while agent.Executing() is true.
	runningTools []provider.ToolCall
	// Head of the agent's approval queue while its confirm prompt is showing,
	// with everything needed to preview and execute it.
	pendingApproval *approvalRequest
	gatedTools      map[string]GatedPreviewFunc
	// Session approval policy: the permission mode plus the
	// internals it builds on. commandAllowlist comes from config; everything
	// below it is what [a] and /permissions allow have granted this session. The
	// default is manual mode with nothing granted: everything prompts.
	mode             agent.Mode
	modeCycle        []agent.Mode
	commandAllowlist []string
	// The blanket grants: every edit, every command, until revoked. They are
	// what /permissions allow sets, and nothing else — [a] used to set them,
	// which made one keystroke on one `go test` the last time the session asked
	// about anything.
	allowAllEdits    bool
	allowAllCommands bool
	// The scoped grants [a] records instead: directories edits are allowed
	// under, and allowlist entries in agent.GrantPrefix's shape. /permissions
	// revoke clears all four, which is the way back that a session grant never
	// had.
	editDirGrants []string
	commandGrants []string
	// scope is the session's working scope: the directory it was
	// opened in plus whatever has been added to it since. It is a pointer
	// because the runner closures that wrap contained commands read it off
	// the UI goroutine, and because a grant made on a card has to be the same
	// grant the sandbox sees on the next command.
	scope *scope.Scope
	// Read-only inspection commands auto-run in every mode; config can extend
	// the built-in list or turn it off entirely.
	readOnlyExtra    []string
	readOnlyDisabled bool
	// Auto mode's LLM permission classifier: judges gated calls the
	// static policy would ask about; nil falls back to asking the user.
	classifier       *agent.Classifier
	classifierCancel context.CancelFunc
	// The session summary (summary.go): a cheap model's periodic read
	// of what the session is doing, drawn as the rail's SUMMARY block.
	summarizer    *agent.Summarizer
	summary       summaryState
	summaryCancel context.CancelFunc
	// summaryTarget is the instruction the current turn is serving, captured
	// when the turn starts and never re-derived. It is what a reading judges
	// drift against, and anchoring it here — rather than reading the tail of
	// a conversation that may itself have drifted — is what will make
	// auto-steering answerable.
	summaryTarget string
	// defaults are the persisted model defaults /model default writes.
	defaults Defaults
	// lastDenial is the most recent auto-mode denial, shown by /permissions why.
	lastDenial string
	// denialNotice mirrors lastDenial on the notice rail until the
	// next user turn clears it.
	denialNotice string
	// planChoice is the focused row of the plan-approval prompt.
	planChoice int
	// The armed plan: planDoc is the planning response parsed into
	// steps, planFacts and planDetail the radius line computed from it. All
	// three are resolved once, when the prompt opens, because pricing the
	// plan asks git about every file it names.
	planDoc    plan.Plan
	planFacts  []components.PlanFact
	planDetail string
	// planRun is the plan the user approved, for as long as it is being
	// carried out: it numbers the transcript's steps, fills the
	// rail's PLAN block and answers /plan. Nil when no plan is running.
	planRun *planRun
	// focusIdx is the transcript index of the row selected in focus mode
	//; -1 while the transcript is being read with nothing on it to
	// select.
	focusIdx int
	// readingKeyList is `[?]` in reading mode: the compact hint
	// bar swapped for the mode's whole key register, in place. It is
	// per-visit, not per-session — the mode closing closes the list too,
	// because it is a reading of this surface rather than a preference about
	// it, and the four supporting TUIs treat their own `[?]` the same way.
	readingKeyList bool
	// mouseOn turns terminal mouse reporting on (ctrl+x, /ui mouse). The
	// zero value is off, because reporting costs the terminal its own
	// click-drag selection and a transcript is text people copy out of. The
	// wheel is the side of the trade with a substitute — pgup/pgdn, ctrl+e,
	// j/k all read the transcript — so the wheel is the side you ask for.
	mouseOn bool
	// caps is what this terminal told shhh it can do — inline images,
	// desktop notifications, focus events. It is asked once,
	// when the program hands over its environment, and the replies land
	// wherever they land. `/ui terminal` reads it, and so does the desktop
	// notification the session raises when you are not there, which is the
	// OSC 99 answer being spent.
	caps caps.Terminal
	// notifyOn is whether shhh may raise a desktop notification when a turn
	// stops while the window is not the one in front (
	// appearance.notify). It is on by default, because unlike mouse
	// reporting it takes nothing away: the gate below means it can only fire
	// when the terminal has said the reader cannot see the screen.
	notifyOn bool
	// away is what the terminal last said about focus. Its zero value is
	// false, and that is deliberate: a window that is in front and a terminal
	// that has never mentioned focus are different facts with the same
	// answer to the only question asked of them — may shhh assume nobody is
	// looking? — and the answer to both is no.
	away bool
	// Application-owned transcript selection (select.go). sel is the
	// selection itself — anchor, endpoint, and whether the button is still
	// down — in rendered-transcript coordinates. selScrollDir and
	// selScrollSeq drive the edge auto-scroll: the direction a drag held at
	// the edge of the pane is asking for, and the fence that stops a tick
	// which outlived its drag. selNotice is the notice rail's line after a
	// successful copy.
	sel          selection
	selScrollDir int
	selScrollSeq int
	selNotice    string
	// press is the cell the primary button last went down in (
	// click.go). A click is a press and a release in the same cell, which is
	// what lets one button carry both the selection drag and the targets.
	press pointerPress
	// writeConfig persists one config key to the user's file. The CLI
	// installs it; a session without one cannot make a setting stick and
	// says so rather than pretending it did.
	writeConfig ConfigWriter
	// containment wraps assistant commands in OS-level process containment
	// when a mechanism is available.
	containment Containment
	// evidence reduces bulky tool results and keeps the originals
	// retrievable.
	evidence Evidence
	// mutationHook post-processes applied file-modification results before
	// reduction — e.g. appending language-server diagnostics.
	mutationHook MutationHook
	// gate backs the /gate quality-gate command.
	gate Gate
	// processes backs /ps and process-start approval gating.
	processes Processes
	// memory backs /memory and the remember-tool confirm flow;
	// memoryAsk is the open memory prompt while a proposal awaits the user.
	memory Memory
	// todos backs /todo and the TODO block; todoStore is the backlog as last
	// read from disk, reloaded on the events that can change it.
	todos     Todos
	todoStore *todo.Store
	memoryAsk *components.NoteSelect
	// secrets backs /secret and the scrub on the agent.
	secrets Secrets
	// skills is the session's skill catalog, behind /skills, /skill and
	// the /<skill-name> shortcut; nil when none loaded. skillsList renders
	// the catalog for /skills — the same text `shhh skills` prints.
	skills     *skill.Catalog
	skillsList func(*skill.Catalog) string
	// compacting marks an in-flight /compact request: the streamed
	// response is a summary handled by finishCompact, not conversation text.
	compacting bool
	// observer receives content-free session events for observability
	//; turnCount and toolDefTokens feed it and /stats.
	observer      Observer
	turnCount     int64
	toolDefTokens int64
	// subagents supervises spawned child agents; childAsks queues
	// their approval requests routed into this session's approval surface.
	subagents *subagent.Supervisor
	childAsks []*subagent.Ask
	// decisionHeld is whether the decision on screen holds the keyboard
	//. A card that arrives on top of a sentence never does:
	// until the handover chord it renders its keys as not-yet-live and every
	// letter goes into the draft. One that arrives on a draft nobody is
	// typing into does, because there is no sentence for the letters to
	// belong to.
	decisionHeld bool
	// heldOnArrival narrows that: the decision holds the keyboard because it
	// landed on an idle draft, not because the handover gave it to it. A
	// card in that state answers only what it was walked up to be asked and
	// hands the keyboard back for everything else (components/approval.go).
	heldOnArrival bool
	// lastKeypress is when the keyboard was last touched, whatever it was
	// pointed at. It is the second half of "nobody is typing into it": an
	// empty draft is not the same thing as an idle one, and a reader between
	// two words has an empty draft for as long as the backspace held.
	lastKeypress time.Time
	// Sub-agent management and steering: attachedTo focuses the chat
	// surface on a child ("" = orchestrator); childViews holds each child's
	// mirrored transcript and scroll state so attach/detach loses nothing;
	// agentList is the open agent manager, killConfirm/killTarget its armed
	// inline kill confirmation, and answerAgent the row whose approval is
	// being answered over the list rather than inside the child.
	attachedTo  string
	childViews  map[string]*childView
	parentView  viewState
	agentList   *components.AgentList
	killConfirm *components.Confirm
	killTarget  string
	answerAgent string
	// Session branching and rewind: checkpoints mark each user turn's
	// start; sessionName is the storage slot rewind branches hang off (set by
	// /save, /load, and branch switches); rewindSelect is the open /rewind
	// picker; gitSnapshot records the workspace git state per checkpoint when
	// wired.
	checkpoints  []checkpoint
	sessionName  string
	rewindSelect *components.Select
	gitSnapshot  func() GitSnapshot
	// Rich diff rendering: fullDiff is the viewer showing full
	// screen, diffReturn where esc goes back to.
	fullDiff   *components.DiffView
	diffReturn state
	// The staged attachment preview: preview is the card while it
	// has the pane. There is no return state beside it — the surface is
	// opened from the draft and from nowhere else, so leaveSurface's own
	// answer is always the right one.
	preview *components.AttachmentView
	// Review mode: review is the surface while it has the screen,
	// reviewTurnN the turn it is reviewing (0 for a review of something
	// else), and reviewReturn where esc goes back to.
	review       *components.ReviewView
	reviewTurnN  int64
	reviewReturn state
	// Undo: undoAsk is the confirm while it is up, undoPlan what it
	// would do to the workspace (read once, when the confirm was offered),
	// and undoReturn where declining hands the screen back to.
	undoAsk    *components.UndoConfirm
	undoPlan   changeset.UndoPlan
	undoReturn state
	// Per-turn changeset store: changes records every applied edit
	// with the content on both sides, keyed by turn, and is what /diff
	// renders; tracker answers whether git knew about a file when it was
	// edited, and is nil outside a repository.
	changes *changeset.Store
	tracker *changeset.Tracker
	// Slash-command completion: completions is the filtered candidate
	// list for the input value completeFor (a mismatch means stale → hidden),
	// completeIdx the focused row, and completeDismissedFor the input value
	// esc dismissed the menu for (typing anything else re-opens it).
	// Argument-level completion adds the token span being completed
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
	// Interactive slash-command pickers: picker is the open select
	// card, pickerApply consumes the chosen index and returns the transcript
	// note; modelOptions is the /model picker's model catalog.
	//
	// pickerAll is the list the picker opened over and pickerIndex maps the
	// rows it is showing back onto it, so a choice made through the filter row
	// still reaches an apply written against the whole list.
	picker       *components.Select
	pickerApply  func(*Model, int, bool) string
	pickerAll    []components.SelectOption
	pickerIndex  []int
	modelOptions []string
	// The command palette: the open palette's query and candidates,
	// which turn statePick into a filtered list rather than a fixed one.
	// recentFiles overrides the checkout walk behind its FILES group, which
	// is how the tests stop depending on the directory they run in.
	palette     *paletteState
	recentFiles func() []project.RecentFile
	// Live model discovery: modelLister queries the provider's
	// /v1/models endpoint for endpoints no curated catalog can cover, and the
	// result replaces modelOptions for the rest of the session.
	modelLister     func(context.Context) ([]string, error)
	modelListCancel context.CancelFunc
	modelListed     bool
	// steering holds messages typed while the agent is working; they
	// are injected as user messages before the next stream request.
	steering []string
	// attachments are the images and files staged for the next message
	// (attachments.go). They ride on whichever user message goes out
	// next — a fresh turn or the first queued steering line — and are never
	// rendered, only named.
	attachments []provider.Attachment
	// pasteLines and pasteColumns are the shape past which a paste is staged
	// as one of them rather than typed into the draft
	// (appearance.paste_lines / appearance.paste_columns). They hold the
	// defaults rather than zero, so a session built without
	// WithPasteThresholds still stages a log.
	pasteLines    int
	pasteColumns  int
	title         string
	width         int
	height        int
	ready         bool
	atBottom      bool
	quitting      bool
	initialPrompt string

	// TotalTokensIn and TotalTokensOut are the main agent's own spend — its
	// turns and nothing else. The session's spend, which includes the
	// classifier, the summary and every sub-agent, is the ledger's.
	TotalTokensIn  int64
	TotalTokensOut int64
	// ledger is the session-wide spend, filled by the provider gate rather
	// than by this model, so a feature added later counts without this file
	// changing.
	ledger *meter.Ledger
	// Current-turn accounting for the inspector rail's THIS TURN and SPEND
	// blocks: when the turn started, when it finished (zero while it
	// runs), and what it has spent.
	turnStarted time.Time
	turnEnded   time.Time
	// turnOpen marks a turn the user started and that has not yet closed, so
	// the close rows are appended once, for a real turn; turnOutcome
	// is how it ended.
	turnOpen      bool
	turnOutcome   components.TurnState
	turnTokensIn  int64
	turnTokensOut int64
	// contextTokens is what the provider last reported the request carrying;
	// zero means nothing has been reported about the current message list, so
	// the accounting estimates instead and says so.
	contextTokens int64
	// vitals is the session's per-turn usage history and the burn series
	// behind the rail's sparkline; projectTokens is the estimated
	// size of the project context inside the system prompt, which the
	// occupancy breakdown names separately.
	vitals        vitals
	projectTokens int64
	prices        *pricing.Table
	modelName     string
	updateNotice  string
	// Reasoning effort (reasoning.go): the level this session is on,
	// the hook that carries a change to the next request, and the persisted
	// default with whatever outranks it — the model's three, for the setting
	// that sits beside it on the rail.
	effort          provider.Effort
	effortFn        func(provider.Effort)
	effortDefault   string
	effortOutranked string
	// First contact: what the session already knew about the
	// checkout when it opened, which suggestion the pointer is on, and
	// whether the screen has been spent — a session that has said something
	// to the model is not new again just because /clear emptied it.
	start      *StartInfo
	startFocus int
	startSpent bool
	// Recovery from a provider failure: the provider the session
	// resolved to, the two hooks a failure row's keys need, and the masked
	// key prompt [k] opens. A hook left nil is a key the row does not offer,
	// which is why they are checked rather than assumed.
	providerName     string
	switchProviderFn func(string) error
	replaceKeyFn     func(string) error
	keyAsk           *components.SecretPrompt
	// retry is the bounded wait between a failed request and the next one
	//; retrySeq fences its timer, so a cancelled or superseded wait
	// is never advanced by a tick that outlived it.
	retry *retryWait
	// retryAttempt counts the automatic retries this stall has used, against
	// maxRetryAttempts. It outlives each individual wait, which is what makes
	// the bound a bound.
	retryAttempt int
	retrySeq     int
	// The context-pressure card: the card while it is up, and
	// whether this crossing of the alert threshold has already been
	// answered. The flag is cleared by falling back under the threshold, so
	// the card arrives once per crossing rather than once per turn.
	pressure      *components.PressureCard
	pressureShown bool
	// The context surface: the screen while it is up, and the tool
	// definitions it itemises the tool category into. The definitions are
	// the host's because which tools a session has depends on what the
	// machine turned out to have (prompt.Toolbox).
	context  *components.ContextScreen
	toolDefs []ToolTokens
	// contextOpen is which of the surface's folds the reader had open when
	// they last left it, by label. It outlives the screen because the screen
	// is rebuilt from the accounting on every opening.
	contextOpen map[string]bool
	// The round-limit pause: the offer standing on the last turn to
	// stop at its ceiling, the rounds [+50] has granted the turn in front of
	// it, and whether [!] has lifted this turn's ceiling altogether. All
	// three expire with the turn — resetRounds spends the offer and gives the
	// configured ceiling back.
	roundPause     *roundPause
	roundGrant     int
	roundsUncapped bool
}

func New(initialMessages []provider.Message, stream StreamFunc) Model {
	ta := textarea.New()
	// No placeholder sentence and no per-line prompt: the command-center
	// frame's gutter glyph and bottom-rail hints carry that.
	ta.Placeholder = ""
	ta.Prompt = ""
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.ShowLineNumbers = false
	// Three keys insert a line break, one of which the user can find:
	// shift+enter is rewritten to alt+enter before the textarea sees it
	// (newline.go), and ctrl+j is the chord that works in a terminal too old
	// to report either.
	// The register's newline keys, less shift+enter, which the textarea
	// cannot see: terminals that report it are handled above, and the other
	// two are what the ones that cannot get instead.
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
		// On unless the config says otherwise (WithNotify): unlike mouse
		// reporting, a notification takes nothing away, and it cannot fire
		// while anyone is looking at the screen.
		notifyOn:     true,
		pasteLines:   attachment.DefaultPasteLines,
		pasteColumns: attachment.DefaultPasteColumns,
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
func (m Model) WithResumedMessages(name string, msgs []provider.Message) Model {
	m.loadConversation(msgs)
	if name != "" {
		m.sessionName = name
	}
	return m
}

// autosaveCmd persists the conversation to the session's own slot in the
// background. Returns nil when there is no DB or nothing beyond the system
// prompt to save. The slot is captured here, not when the command runs, so
// a save issued just before the session moves to a new slot still lands in
// the one it was describing.
func (m Model) autosaveCmd() tea.Cmd {
	if m.db == nil || len(m.agent.Messages()) <= 1 {
		return nil
	}
	db, name := m.db, m.sessionName
	msgs := m.agent.RequestMessages()
	return func() tea.Msg {
		_ = db.SaveChat(name, msgs)
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

// ExitBanner is what this session leaves on the terminal once the alt screen
// is gone: the slot the conversation ended up in, how big it got,
// what the sitting cost, and the command that reopens it. resume is that
// command, supplied by the front-end because the model does not know which of
// shhh's faces it is wearing — `shhh chat --continue`, `shhh code
// --continue`.
//
// The saved/not-saved split is autosaveCmd's condition and nothing else, and
// the slot named is the one autosaveCmd writes rather than the session's
// working name: what quitting wrote is what --continue will read back, so a
// banner offering a resume the autosave did not take cannot be built.
func (m Model) ExitBanner(resume string) components.ExitBanner {
	b := components.ExitBanner{
		Turns: m.conversationTurns(),
		Spend: m.spendLabel(m.TotalTokensIn, m.TotalTokensOut),
	}
	if m.db == nil {
		b.Unsaved = true
		return b
	}
	b.Session, b.Resume = m.sessionName, resume
	return b
}

// conversationTurns counts the exchanges the saved conversation holds, the
// way ListChats counts them — user messages, so a resumed session reports the
// whole thing rather than the part this sitting added. m.turnCount is the
// wrong number here: it counts what was dispatched, including the steering
// lines that joined a turn already running.
func (m Model) conversationTurns() int {
	n := 0
	for _, msg := range m.agent.Messages() {
		if msg.Role == provider.RoleUser {
			n++
		}
	}
	return n
}

func (m Model) Messages() []provider.Message { return m.agent.Messages() }

func (m Model) Init() tea.Cmd {
	// No spinner tick here: nothing is moving on an empty session, and a
	// chain started before there is anything to animate is a chain that dies
	// at its first tick. Update starts one the moment something does move
	// (spin.go).
	cmds := []tea.Cmd{textarea.Blink}
	if m.initialPrompt != "" {
		cmds = append(cmds, func() tea.Msg { return initialPromptMsg{} })
	}
	if m.subagents != nil {
		cmds = append(cmds, listenSubagents(m.subagents.Events()))
	}
	// Mouse reporting is not asked for here: it is a field on the View
	//, so every surface that runs this Model gets the same answer
	// from the same place and the toggle has one thing to flip.
	return tea.Batch(cmds...)
}

// Update routes the message, then makes the spinner's one rule true again
// : a tick chain runs exactly while something on screen is
// moving. Resuming the loop here rather than at each transition is what makes
// "reliably restarts" a property of the loop instead of something fifteen
// separate handoffs are each trusted to remember — three of them did not, and
// the frame froze on the first turn of every session.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	mm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	if tick := mm.spinCmd(); tick != nil {
		cmd = tea.Batch(cmd, tick)
	}
	// And the desktop notification is derived here for the same reason
	//: the moment worth notifying about is a transition — the
	// session stopped needing shhh and started needing the reader — and a
	// transition is a fact about the model before against the model after,
	// not a message any one of the dozen handlers that reach it could be
	// trusted to send.
	if call := mm.notifyCmd(m); call != nil {
		cmd = tea.Batch(cmd, call)
	}
	// The turn's closing summary is derived here too (summary.go), and
	// for the third time for the same reason: "the turn just ended" is a fact
	// about two models, and every path back to the input would otherwise have
	// to remember to ask for one.
	if read := mm.summaryCloseCmd(m); read != nil {
		cmd = tea.Batch(cmd, read)
	}
	return mm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A modified Enter is a line break, not a send. Every modifier means the
	// same thing, so they are rewritten here into the one key the textarea's
	// newline binding listens for — before any surface can mistake a
	// shift+enter for a send of its own (newline.go).
	if newlineKey(msg) {
		msg = altEnter
	}
	// What the terminal can do is folded in wherever the reply lands
	//. The answers come back as five unrelated message types,
	// over however long the terminal takes to send them, and none of them
	// is anything else on this switch's business — so they are read here,
	// before the routing, and go on to it unchanged.
	m.caps.Update(msg)
	switch msg := msg.(type) {
	case tea.EnvMsg:
		// The program's own environment, which over ssh is the client's
		// terminal rather than this machine's. It arrives once, at
		// startup, and asking is the only thing to do with it.
		return m, m.caps.Query(msg)
	case tea.FocusMsg:
		// The window came back to the front. Nothing on screen changes; what
		// changes is whether shhh may assume nobody is looking.
		m.away = false
		return m, nil

	case tea.BlurMsg:
		m.away = true
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncInputWidth()
		// The transcript wraps to its pane, which is narrower than the content
		// width while the inspector rail shows.
		paneWidth := m.transcriptWidth()
		vpHeight := m.viewportHeight()
		// Every rendered line reflows at a new width, so a selection's
		// coordinates stop naming the text they were taken over.
		m.resizeSelection(paneWidth)

		if !m.ready {
			m.viewport = newViewport(paneWidth, vpHeight)
			m.viewport.SetLines(m.renderHistoryLines())
			m.ready = true
		} else {
			m.viewport.SetWidth(paneWidth)
			m.viewport.SetHeight(vpHeight)
			m.viewport.SetLines(m.renderHistoryLines())
		}
		// A placement is cells at a size: a pane that changed
		// shape under a picture the terminal is holding is a picture that no
		// longer fits the hole left for it, so it is sent again at the new
		// one. Every other surface reflows from its own View.
		return m, m.placePicture()

	case tea.MouseMsg:
		// The wheel scrolls whatever is showing content — the transcript, or
		// the full-screen diff and review surfaces that take the screen from
		// it. It never reaches the textarea, which is what made a scroll
		// gesture over the conversation move the three-line prompt box
		//. Press, drag and release own the transcript's text
		// selection (select.go).
		if !m.mouseOn {
			return m, nil
		}
		return m.updateMouse(msg)

	case tea.PasteMsg:
		// A file dragged into the terminal arrives as a bracketed paste of
		// its path. When it points at an image or a document, attaching it
		// is the only thing the gesture can have meant; everything
		// else pastes as the text it is.
		//
		// In v2 a paste is a message of its own rather than a keystroke
		// wearing a Paste flag. What that flag bought was routing:
		// pasted text reached whichever surface had the keyboard, so a paste
		// into a card's filter row filtered. So the text is handed on
		// as the keystroke it used to be — one press carrying the whole run,
		// which is what v1 delivered — and every surface below sees exactly
		// what it saw before.
		if m.inputLive() && m.attachedTo == "" {
			if path, ok := pastedFileAttachment(msg.Content); ok {
				return m, attachFileCmd(path)
			}
			// A stack trace or a log is a file that happens to have arrived
			// through the clipboard, and typing it into a three-row box hides
			// the sentence it was meant to go with (attachments.go). The
			// line endings are settled first, because the count that decides
			// this is a count of newlines and a terminal is free to send
			// carriage returns.
			if pasted := attachment.NormalizeNewlines(msg.Content); m.pasteOverflows(pasted) {
				return m.stagePaste(pasted)
			}
		}
		if msg.Content == "" {
			return m, nil
		}
		return m.update(tea.KeyPressMsg{
			Code: []rune(msg.Content)[0],
			Text: msg.Content,
		})

	case selectionScrollMsg:
		// A drag held at the edge of the transcript pane. It is
		// answered whatever the surface, because the fence inside it is what
		// decides whether the tick is still wanted — a selection cancelled
		// between the tick being scheduled and arriving has already bumped
		// the sequence.
		return m.updateSelectionScroll(msg)

	case tea.KeyPressMsg:
		// Every key stamps the clock a decision's arrival reads (
		// interrupt.go). It is stamped here rather than on the draft's own
		// path because the question is whether the reader is at the keyboard,
		// not which surface they were talking to.
		m.lastKeypress = time.Now()
		// Mouse reporting is the one setting with a chord of its own (
		// reading mode), and the only key answered before the surfaces are: what it
		// costs — the terminal's own click-drag selection — is discovered at
		// the moment of wanting to copy something, with a mouse already in
		// hand and no appetite for a slash command. That moment arrives just
		// as often over the full-screen diff or a transcript being read as it
		// does over the draft, so the chord is answered above all of them.
		// Nothing else claims it, so nothing is taken away by that.
		if keys.Match(msg, keys.Draft.Mouse) {
			return m.toggleMouse()
		}
		if m.state == stateDiffFull {
			return m.updateDiffFull(msg)
		}
		if m.state == statePreview {
			return m.updatePreview(msg)
		}
		// The handover means one thing in both states a decision can be in:
		// give the card the whole keyboard. From ungated it is the mid-sentence
		// rule's transfer
		// — every letter belonged to the draft, and now none do. From a card
		// holding the keyboard by arrival it buys the keys that card left
		// alone on purpose ([a], [d], [A]).
		if m.interruptShowing() && keys.Match(msg, keys.Draft.Answer) && (m.decisionUngated() || m.heldOnArrival) {
			return m.gateDecision()
		}
		// A decision that arrived on top of a sentence is inert until it holds the
		// keyboard
		// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard):
		// ungated, the handover above is the only key that is its own, and every
		// letter belongs to the draft.
		if !m.decisionUngated() {
			if keys.Match(msg, keys.Draft.Clear) && m.escLeavesWaiting() {
				// Esc leaves the decision waiting rather than denying it; [n]
				// is how you say no.
				return m.ungateDecision()
			}
			if m.state == stateConfirmRun {
				return m.updateConfirmRun(msg)
			}
			if m.state == statePlanApprove {
				return m.updatePlanApprove(msg)
			}
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
		if m.state == statePressure {
			return m.updatePressure(msg)
		}
		if m.state == stateContext {
			return m.updateContext(msg)
		}
		// A draining retry countdown owns the keyboard the way the confirm
		// prompt does: nothing is streaming, the input is not live, and the
		// wait offers two keys that both end it.
		if m.state == stateRetryWait {
			return m.updateRetryWait(msg)
		}
		if m.state == stateFocus {
			return m.updateFocus(msg)
		}
		// The agent manager list takes over the bottom panel and keys.
		if m.agentList != nil {
			return m.updateAgentList(msg)
		}
		// A child agent's routed approval takes over the bottom panel;
		// it defers to the parent's own prompts above. Like every other
		// decision that arrives unbidden it is inert until the handover gives
		// it the keyboard, which is why the check is on decisionHeld.
		if ask := m.activeChildAsk(); ask != nil && m.decisionHeld {
			return m.updateChildAsk(msg, ask)
		}
		switch pressed := msg.String(); {
		case keys.Is(pressed, keys.Draft.Quit):
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
		case keys.Is(pressed, keys.Draft.Cancel):
			// While attached, Ctrl+C acts on the child: cancel its turn.
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
				m.releaseDecision()
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
				m.viewport.SetLines(m.renderHistoryLines())
				m.viewport.GotoBottom()
				return m, m.autosaveCmd()
			}
			if m.decisionUngated() {
				// Ctrl+C keeps the meaning the card has always given it: it
				// answers the decision no. No draft can produce the chord, so
				// leaving it live is what keeps a waiting decision endable
				// without first taking the keyboard.
				m.decisionHeld = true
				return m.routeDecision(msg)
			}
			if strings.TrimSpace(m.input.Value()) != "" {
				m.input.Reset()
				m.historyIdx = len(m.inputHistory)
				return m, nil
			}
			m.quitting = true
			return m, m.quitCmd()
		case keys.Is(pressed, keys.Draft.Mode):
			// Cycle the permission mode; attached, it cycles the
			// child's mode clamped to the orchestrator's ceiling.
			if m.attachedTo != "" {
				return m.cycleAttachedMode()
			}
			m.applyMode(agent.NextMode(m.modeCycle, m.mode))
			return m, nil
		case keys.Is(pressed, keys.Draft.Agents):
			// Agent manager; without a supervisor the key keeps its
			// textarea meaning (line start).
			if m.subagents != nil {
				return m.openAgentList()
			}
		case keys.Is(pressed, keys.Draft.Attach):
			// Ctrl+V used to be the textarea's own text paste. It reads the
			// clipboard properly now: a screenshot or a copied file is
			// staged as an attachment, and plain text still lands in the
			// draft (attachments.go). Attached to a child, the
			// orchestrator's staging area is not what the keyboard is
			// pointed at, so the key keeps its textarea meaning there.
			if m.inputLive() && m.attachedTo == "" {
				return m, readClipboardCmd()
			}
		case keys.Is(pressed, keys.Draft.Editor):
			// The draft goes out to the reader's own editor and comes back
			// (editor.go). It is the one key here that stops the world:
			// the program is suspended while the editor has the terminal, so
			// it is refused with a notice rather than queued whenever
			// something is still happening on this screen.
			//
			// It costs the textarea's own ctrl+g, which selected everything
			// in the box. Nothing in shhh offered that key or said it was
			// there, and what it selected could only be deleted or replaced
			// — which is what the editor is for.
			return m.openEditor()
		case keys.Is(pressed, keys.Draft.Reasoning):
			// Reasoning effort: the level the next request asks for.
			// It changes nothing about the conversation and nothing about the
			// turn in flight, so like the rest of the live surfaces it is
			// answered while one runs — and it is a chord, so the draft below
			// keeps every letter it has.
			if m.inputLive() && m.attachedTo == "" {
				next, note := m.cycleReasoning()
				m = next
				m.appendEntry(entry{kind: entrySystem, text: note})
				m.syncViewport()
				return m, nil
			}
		case keys.Is(pressed, keys.Draft.Palette):
			// The command palette: one prompt over the commands, the
			// saved chats and the files this session has touched. It reads
			// the session without touching the conversation, so it opens
			// over a running turn like the rest of the live surfaces.
			// Attached, the orchestrator's commands are not what the keyboard
			// is pointed at, so the key keeps its textarea meaning there.
			if m.inputLive() && m.attachedTo == "" {
				return m.openPalette()
			}
		case keys.Is(pressed, keys.Draft.Reading):
			// Focus mode: navigate and expand transcript rows; scoped
			// to whichever agent is focused. It reads the transcript
			// without touching the conversation, so it opens over a running
			// turn too — the turn keeps streaming underneath.
			if m.inputLive() {
				return m.enterFocusMode()
			}
		case keys.Is(pressed, keys.Draft.Detail):
			// Step detail: the step in flight opens its rows'
			// bodies without the keyboard leaving the draft. It reads the
			// transcript and changes nothing about the conversation, so it
			// is answered over a running turn like the rest of the live
			// surfaces — which is the case it exists for, since the step
			// worth asking about is usually the one still going.
			if m.inputLive() {
				return m.detailFromDraft()
			}
		case keys.Is(pressed, keys.Draft.PageUp, keys.Draft.PageDown):
			// The pager keys read the transcript and leave the keyboard in
			// the draft. Reading is not a decision — the wheel
			// has always said so — and the reader scrolling back to check a
			// path mid-sentence is not asking to stop writing the sentence.
			// Paging used to hand the keyboard over, which took the draft off
			// the screen to answer a question about the pane above it.
			//
			// No draft can produce these keys, which is what makes them safe
			// to spend here and why the letters bubbles binds to the same job
			// (j/k/u/d/f/b and the spacebar) are still not offered at all.
			if m.inputLive() {
				dir := -1
				if keys.Is(pressed, keys.Draft.PageDown) {
					dir = 1
				}
				m.scrollPage(dir)
				return m, nil
			}
		case keys.Is(pressed, keys.Draft.ScrollUp):
			// The same job by a line rather than a page. Both
			// chords are bound because terminals disagree about which they
			// report: the textarea underneath claims neither, and neither is
			// reachable by typing.
			if m.inputLive() {
				m.scrollLines(-keyScrollLines)
				return m, nil
			}
		case keys.Is(pressed, keys.Draft.ScrollDown):
			if m.inputLive() {
				m.scrollLines(keyScrollLines)
				return m, nil
			}
		case keys.Is(pressed, keys.Draft.Clear):
			// A visible selection is what esc cancels first.
			// It is the only thing on the surface esc could mean while one
			// is lit, and it says so without touching the draft — a reader
			// who selected the wrong six screens has not also abandoned the
			// sentence they were writing.
			if m.cancelSelection() {
				m.refreshTranscript()
				return m, nil
			}
			// With the completion menu open, esc only dismisses the menu; the
			// draft survives and further typing re-opens it.
			if m.completionActive() {
				m.dismissCompletions()
				m.syncViewport()
				return m, nil
			}
			// The input is live in every non-confirm state, so esc
			// clears the draft; attached with an empty draft it pops one
			// breadcrumb level.
			if m.attachedTo != "" && strings.TrimSpace(m.input.Value()) == "" {
				m.detachOne()
				return m, nil
			}
			m.input.Reset()
			m.historyIdx = len(m.inputHistory)
			return m, nil
		case keys.Is(pressed, keys.Draft.Complete):
			// Tab writes the focused completion into the input.
			if m.completionActive() {
				m.acceptCompletion()
				m.syncViewport()
				return m, nil
			}
		case keys.Is(pressed, keys.Draft.HistoryPrev):
			if m.completionActive() {
				if m.completeIdx > 0 {
					m.completeIdx--
				}
				return m, nil
			}
			// The start screen's suggestion list claims ↑↓ only while it is
			// live: an empty draft on a session that has not started yet,
			// which is also the only time the input history has nothing to
			// browse.
			if next, claimed := m.startKey("up"); claimed {
				return next, nil
			}
			// Recall is the draft's, wherever the draft has the keyboard
			//. It used to be the idle turn's: ↑ did nothing
			// while the agent streamed, ran a command or waited on the
			// classifier, and nothing under an approval card that had not
			// been given the keyboard — the four states steering and the
			// mid-sentence rule exist
			// to keep the sentence live in. A key that is the input's in
			// every state and a frame that is live but cannot recall
			// were two claims about the same three lines, and the code was
			// answering the older one.
			if m.inputLive() && len(m.inputHistory) > 0 &&
				(m.browsingHistory() || strings.TrimSpace(m.input.Value()) == "") {
				if m.historyIdx > 0 {
					m.historyIdx--
					m.input.SetValue(m.inputHistory[m.historyIdx])
				}
				return m, nil
			}
			// ↑ used to hand the keyboard to the transcript on an empty draft
			// with no history to recall. It is the input's key in every other
			// state, and a key that changes surface depending on how much
			// history a session happens to have is one nobody can learn
			//. Alternate scroll made it worse than unlearnable: on a
			// terminal that synthesises arrows for the wheel, a flick opened
			// reading mode (altscroll.go). Scrolling has its own keys now.
		case keys.Is(pressed, keys.Draft.HistoryNext):
			if m.completionActive() {
				if m.completeIdx < len(m.completions)-1 {
					m.completeIdx++
				}
				return m, nil
			}
			if next, claimed := m.startKey("down"); claimed {
				return next, nil
			}
			if m.inputLive() && m.browsingHistory() {
				m.historyIdx++
				if m.historyIdx >= len(m.inputHistory) {
					m.historyIdx = len(m.inputHistory)
					m.input.Reset()
				} else {
					m.input.SetValue(m.inputHistory[m.historyIdx])
				}
				return m, nil
			}
		case keys.Is(pressed, keys.Draft.Send):
			// While attached, Enter acts on the child: scoped commands and
			// mid-turn steering.
			if m.attachedTo != "" {
				return m.attachedSubmit()
			}
			// Enter on a live start screen types the focused suggestion and
			// submits it, so choosing an offer and typing it are the same
			// act down to the dispatch.
			if action := m.startAction(); action != "" {
				m.input.SetValue(action)
				return m.submitInput()
			}
			// One submit path for every state that keeps the input live
			// (command.go): commands run, plain text is a message when
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
		// A round is a request: the row the last one's reasoning landed on is
		// not the row this one's belongs to (think.go).
		m.settleThink()
		m.thinkIdx = 0
		return m, waitForEvent(m.events)

	case tokenMsg:
		// The provider is answering: whatever stall preceded this is over, and
		// the next one starts its own bounded count.
		m.clearRetryChain()
		m.appendThinking(msg.think)
		if msg.text != "" {
			// A model that has started writing has stopped thinking, so the
			// round's think row settles here rather than spinning under the
			// answer it already produced.
			m.settleThink()
		}
		m.streaming += msg.text
		// The repaint rides the spinner's tick rather than the chunk (the
		// streaming render). A chunk that arrives while the loop is running only
		// records that one is owed; one that arrives with nothing ticking — the
		// last of a stream, or a state that draws no spinner — repaints itself,
		// because nothing else is going to.
		if m.spinning && msg.final == nil {
			m.streamDirty = true
		} else {
			m.flushStream()
		}
		if msg.final != nil {
			return m.update(msg.final)
		}
		return m, waitForEvent(m.events)

	case doneMsg:
		m.clearRetryChain()
		m.accumulateUsage(msg.usage)
		// A response that ended in text asked for no tools, so its thinking
		// has nowhere to travel to and the latch is dropped rather than left
		// for a later round to pick up.
		m.agent.CarryReasoning(nil)
		if m.compacting {
			return m.finishCompact()
		}
		hadText := m.streaming != ""
		m.finishStreaming()
		// A steering message queued while the model was responding becomes the
		// next user turn immediately.
		if cmd := m.dispatchSteering(); cmd != nil {
			return m, cmd
		}
		// A completed planning response gets the plan-approval prompt.
		if m.mode == agent.ModePlan && hadText {
			m.setTurnState(statePlanApprove)
			m.armPlan()
			m.syncViewport()
		}
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd()

	case toolCallsMsg:
		m.clearRetryChain()
		m.accumulateUsage(msg.usage)
		// The thinking behind these calls has to travel with them into the
		// next request.
		m.agent.CarryReasoning(msg.reasoning)
		// And what is readable of it is the round's think row, for a provider
		// that hands its reasoning over whole at the end rather than as it is
		// written (think.go). The row goes in before the announcement, which
		// is where it happened.
		m.recordReasoning(msg.reasoning)
		if m.compacting {
			return m.abortCompact()
		}
		auto, gated := m.agent.BeginToolRound(m.streaming, msg.calls, m.requiresApproval)
		m.approvalTotal = len(gated)
		// A round is also where the session summary is scheduled:
		// the round counter has just moved, which is the clock the reading
		// interval is kept on. It is a no-op until one falls due.
		summary := m.summaryCmd()
		// A round is where a fan-out is measured: the children spawned in one
		// share a batch and render as one block.
		m.beginSpawnBatch()
		if m.streaming != "" {
			// This is the announcement a step is titled by, so it is where an
			// approved plan's step list joins the transcript.
			m.appendEntry(m.stampStep(entry{kind: entryAssistant, text: m.streaming}))
		}
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		if len(auto) > 0 {
			return m, tea.Batch(m.execToolsCmd(auto), summary)
		}
		next, cmd := m.advanceApprovalQueue()
		return next, tea.Batch(cmd, summary)

	case toolResultsMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateStreaming {
			return m, nil
		}
		m.agent.RecordAutoResults(msg.results)
		m.runningTools = nil
		for _, r := range msg.results {
			m.recordToolEvent(r.Call.Name, r.Duration, outcomeFromResult(r.Result))
			m.appendEntry(entry{kind: entryTool, toolName: r.Call.Name, toolArgs: r.Call.Arguments, toolResult: r.Result, duration: r.Duration})
		}
		m.viewport.SetLines(m.renderHistoryLines())
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
		// before both the transcript entry and the tool result, so
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
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
			return m.advanceApprovalQueue()
		}
		m.setTurnState(stateInput)
		m.agent.Append(provider.Message{
			Role:    provider.RoleUser,
			Content: commandContextMessage(msg.command, out, msg.exitCode),
		})
		// A message typed while the /run command executed is sent now, with
		// the command context already in the conversation.
		if cmd := m.dispatchSteering(); cmd != nil {
			return m, cmd
		}
		m.viewport.SetLines(m.renderHistoryLines())
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
		// An applied edit lands in the transcript as a collapsed diff row (
		// docs/interface/surfaces.md#the-diff-view); failures keep the plain tool
		// block so the error text stays visible.
		if req.kind == approvalDiff && len(req.hunks) > 0 && outcomeFromResult(msg.result) == outcomeOK {
			m.appendEntry(entry{kind: entryDiff, diff: &components.DiffView{
				Path:     req.path,
				Verb:     req.verb,
				Hunks:    req.hunks,
				Mode:     components.DiffCollapsed,
				MaxLines: maxDiffExpandedLines,
				Syntax:   diffSyntax(req.path),
			}})
		} else if req.call.Name == subagent.SpawnToolName && outcomeFromResult(msg.result) == outcomeOK {
			m.appendSpawnEntry(entry{kind: entryTool, toolName: req.call.Name, toolArgs: req.call.Arguments, toolResult: msg.result, duration: msg.duration})
		} else {
			m.appendEntry(entry{kind: entryTool, toolName: req.call.Name, toolArgs: req.call.Arguments, toolResult: msg.result, duration: msg.duration})
		}
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()

	case classifierDoneMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateClassifying || m.pendingApproval == nil {
			return m, nil
		}
		m.classifierCancel = nil
		return m.finishClassifierCheck(msg.verdict)

	case summaryDoneMsg:
		// A reading never routes anything: it changes what the rail draws and
		// nothing else, which is why it has no turn-state guard of its own
		//. finishSummary decides what to keep.
		m.summaryCancel = nil
		m.finishSummary(msg)
		return m, nil

	case modelListMsg:
		return m.finishModelList(msg)

	case subagentEventMsg:
		return m.handleSubagentEvent(msg.ev)

	case streamErrMsg:
		// Classified, never raw: the failure is a row on the
		// activity grid with the provider's own words in its detail body and
		// the keys for its class underneath. What happens after the row —
		// an offer to continue a partial, a bounded wait, or the end of the
		// turn — belongs to the retry path (resume.go).
		return m.handleStreamFailure(msg)

	case retryTickMsg:
		return m.retryTick(msg)

	case clipboardMsg:
		return m.handleClipboard(msg)

	case editorDoneMsg:
		return m.editorFinished(msg)

	case todoEditorDoneMsg:
		return m.todoEditorFinished(msg)

	case attachedFileMsg:
		return m.handleAttachedFile(msg)

	case spinner.TickMsg:
		// The one tick, advancing the one frame (spin.go). The guard
		// that used to stand here decided whether to answer at all, and a
		// tick it declined took the chain with it.
		return m.spinTick(msg)
	}

	var cmds []tea.Cmd
	// The input stays live while the agent streams or runs tools so the user
	// can type a steering message; only the confirm and plan-approval
	// prompts take over.
	if m.decisionUngated() || (m.state != stateConfirmRun && m.state != statePlanApprove && m.state != stateRetryWait) {
		// Any other keypress while browsing input history turns the recalled
		// text into a fresh draft.
		if _, ok := msg.(tea.KeyPressMsg); ok {
			m.historyIdx = len(m.inputHistory)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		// Keystrokes may have changed the input: refresh the slash-command
		// completion menu, and resize the viewport when it appears/disappears.
		if _, ok := msg.(tea.KeyPressMsg); ok {
			m.syncCompletions()
			m.syncViewport()
		}
	}

	// Nothing is forwarded to the transcript here. The pager bindings that
	// used to be — bubbles' defaults, j, k, u, d, f, b and the spacebar —
	// scrolled the history out from under any draft containing those letters,
	// so shhh's own pane reads no keys at all (viewport.go). While the
	// input owns the keyboard the transcript is moved by the wheel, by
	// pgup/pgdn and by focus mode, never by a character the sentence wanted.
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
	return m.sendUserMessageAs(text, text)
}

// sendUserMessageAs starts a turn on text while the transcript shows
// shown in its place — the command that produced a message, where the
// message itself is not what the user typed.
func (m Model) sendUserMessageAs(text, shown string) (tea.Model, tea.Cmd) {
	// A plan that has been through its list has answered "where are we", so
	// the next instruction retires it. One with steps left to go survives the
	// message, because that question is still open.
	if m.planRun != nil && m.planRun.complete() {
		m.planRun = nil
	}
	// The session has now said something of its own, so first contact is
	// over: /clear empties the transcript without making it new again.
	m.spendStartScreen()
	m.clearRetryChain()
	m.turnCount++
	m.turnStarted, m.turnEnded = time.Now(), time.Time{}
	m.turnOpen, m.turnOutcome = true, components.TurnDone
	m.turnTokensIn, m.turnTokensOut = 0, 0
	m.vitals.startTurn()
	// A fresh user turn clears the notice rail's denial alert;
	// lastDenial stays for /permissions why.
	m.denialNotice = ""
	// A new turn starts from the ceiling the session was configured with, and
	// the pause behind it can no longer be granted more rounds.
	m.resetRounds()
	// This message is the target every reading of this turn is judged
	// against, and it is captured once, here. A run that drifts must
	// not be able to drift its own yardstick with it — which is the whole
	// difference between a drift signal and a summary of wherever the
	// conversation happens to have ended up.
	m.summaryTarget = shown
	m.summary.startTurn()
	m.recordCheckpoint(shown)
	atts := m.takeAttachments()
	m.agent.StartTurnWith(text, atts)
	m.appendEntry(entry{kind: entryUser, text: shown, attached: attachment.Names(atts)})
	m.trimForRequest()
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.requestStream()
}

// View is the frame the terminal shows. In v2 that is a value rather than a
// string: the screen's content, and the terminal states the session
// is asking for while it is up. Two of those used to be commands — the alt
// screen a program option each host passed, mouse reporting a command the
// toggle had to remember to send — and both were the same bug waiting, a
// state the model believed in and the terminal had never been told about. A
// field cannot drift from what View draws, because it is what View draws.
func (m Model) View() tea.View {
	v := tea.NewView(m.screen())
	v.AltScreen = true
	// Focus reporting is asked for unconditionally, because it costs the
	// terminal nothing and it is the only thing that can say whether anyone
	// is looking. A terminal that does not know the mode says
	// nothing back, and saying nothing is an answer shhh can act on: no blur
	// ever arrives, so it never concludes the reader has gone.
	v.ReportFocus = true
	// Reporting is off for the session by default — the terminal keeps its
	// own click-drag selection, which is the one thing tracking costs and the
	// one thing nothing else here can do — and `/ui mouse on` buys the wheel
	// with it.
	if m.mouseOn {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// screen paints the whole terminal, drawing each block into the rectangle
// layout.go resolved for it. Nothing here measures anything: a
// block is handed a rectangle, it fills what it can, and ultraviolet clips
// the rest at the edge.
func (m Model) screen() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "Initializing…"
	}

	s := m.surface()
	scr := uv.NewScreenBuffer(max(m.width, 0), max(m.height, 0))
	draw := func(view string, area uv.Rectangle) { drawIn(scr, view, area) }

	draw(m.headerRow(), s.header)
	// The line under the header says which pane has the keyboard (reading
	// mode): a plain divider while the input does, the transcript's own rail
	// while focus mode does.
	draw(m.readingRail(s.rail.Dx()), s.rail)

	// The body renders into the transcript pane; the header, divider and the
	// prompt frame span both panes. Surfaces that take the pane
	// over get all of it — the scroll gutter's column is the transcript's own
	//, and they do their own scrolling.
	view := s.in(s.view, s.pane)
	draw(m.paneView(view), view)
	draw(m.liveTail(s.pane.Dx()), s.in(s.tail, s.pane))
	// Working sub-agents render as compact progress rows above the divider
	//; hidden while the agent list or an attached view covers them.
	draw(m.renderAgentRows(s.pane.Dx()), s.in(s.agents, s.pane))

	// Past 130 content columns the body shares its rows with the inspector
	// rail; the split is horizontal only, so the row budget the
	// vertical split handed out is unchanged.
	if rail := m.inspectorData().Lines(s.inspector.Dx(), s.body.Dy()); len(rail) > 0 {
		column := strings.TrimSuffix(strings.Repeat(sty.Pane.Divider.Render("│")+"\n", s.body.Dy()), "\n")
		draw(column, s.in(s.body, s.divider))
		draw(strings.Join(rail, "\n"), s.in(s.body, s.inspector))
	}

	m.drawBottomPanel(scr, s.bottom)

	return renderScreen(scr)
}

// headerRow is the title row: the header carries only the title —
// the static key hint moved into the frame's contextual bottom rail, the
// update notice onto the notice rail, and the attached breadcrumb onto the
// frame's top rail.
func (m Model) headerRow() string {
	title := m.title
	if title == "" {
		title = "shhh chat"
	}
	header := sty.Header.Render(" " + title)
	if m.attachedTo != "" && !m.frameShowing() {
		// A takeover surface while attached keeps the breadcrumb visible.
		header += sty.HeaderHint.Render("  " + m.breadcrumb())
	}
	return header
}

// paneView is what the transcript pane's rows hold: one of the three
// surfaces that take the pane over, or the transcript itself. A takeover
// surface draws no live tail under it, so the rectangle it is handed is the
// whole body and it does its own scrolling in it.
func (m Model) paneView(area uv.Rectangle) string {
	switch {
	case m.state == stateDiffFull && m.fullDiff != nil:
		// The full-screen diff takes over the viewport.
		m.fullDiff.Height = area.Dy()
		return m.fullDiff.View(area.Dx())
	case m.state == statePreview && m.preview != nil:
		// The staged attachment takes over the pane.
		m.preview.Height = area.Dy()
		return m.preview.View(area.Dx())
	case m.state == stateReview && m.review != nil:
		// Review mode takes over the whole surface.
		m.review.Height = area.Dy()
		return m.review.View(area.Dx())
	case m.state == stateContext && m.context != nil:
		// The context surface takes over the pane the same way.
		m.context.MaxLines = area.Dy()
		return m.context.View(area.Dx())
	}
	return m.transcriptBody()
}

// liveTail is the block the turn draws under the transcript while it works:
// the thinking spinner, the running command's own activity row, the retry
// countdown. It is the one part of the pane whose height is
// not fixed, so the layout asks it rather than assuming — the row it takes
// used to be spent without being budgeted for, which put the bottom of the
// frame one row past the bottom of the terminal.
//
// Attached, the child's session fills the pane and its liveness shows in the
// child-scoped status bar, not a parent spinner.
func (m Model) liveTail(width int) string {
	if m.attachedTo != "" {
		return ""
	}
	switch m.state {
	case stateStreaming:
		if m.streaming != "" {
			return ""
		}
		label := "Thinking…"
		switch {
		case m.compacting:
			label = "Compacting…"
		case m.agent.Executing():
			label = "Running tools…"
		}
		return m.spinner.View() + " " + label
	case stateRunningCmd:
		if m.pendingApproval != nil && m.pendingApproval.kind != approvalExec {
			return m.spinner.View() + " Applying changes…"
		}
		// The running command renders as a live activity row whose tail is
		// its last output line; spinner ticks keep it fresh.
		return m.runningCommandRow(width)
	case stateClassifying:
		return m.spinner.View() + " Checking permission…"
	case stateRetryWait:
		if m.retry == nil {
			return ""
		}
		// The failure row is already in the transcript; this is the part of
		// it that drains. A wait is a meter, never a spinner.
		return m.retryWaitBlock(width)
	case stateModelList:
		return m.spinner.View() + " Listing models…"
	}
	return ""
}

// liveTailHeight is what that block costs the transcript. It is measured
// rather than declared: a retry countdown is a meter and its two offers, and
// a constant saying otherwise was how the surface came to overrun the
// terminal by a row.
func (m Model) liveTailHeight() int {
	tail := m.liveTail(m.paneWidth())
	if tail == "" {
		return 0
	}
	return lipgloss.Height(tail)
}

// drawBottomPanel paints the surface's bottom rows: the command-center frame
// (docs/interface/surfaces.md#the-input-frame), or the divider +
// status-bar stack with whichever takeover surface replaced the input under
// it.
func (m Model) drawBottomPanel(scr uv.Screen, area uv.Rectangle) {
	if m.frameShowing() {
		// A decision that has not been given the keyboard rides above the
		// frame, with the rail that names the keyboard's owner between them.
		var head, frame uv.Rectangle
		layout.Vertical(layout.Len(m.interruptHeight()), layout.Fill(1)).
			Split(area).Assign(&head, &frame)
		drawIn(scr, m.renderInterrupt(area.Dx()), head)
		m.drawPromptFrame(scr, frame)
		return
	}
	drawIn(scr, m.takeoverPanel(area.Dx()), area)
}

// takeoverPanel is the bottom panel when the frame is not showing: the
// divider and status bar, and under them the input or the surface that
// replaced it.
func (m Model) takeoverPanel(width int) string {
	inputView := m.input.View()
	// The slash-command completion menu renders under the input;
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
	case statePreview:
		inputView = m.renderPreviewHint()
	case stateReview:
		inputView = m.renderReviewHint()
	case stateUndoConfirm:
		inputView = m.renderUndoConfirm()
	case stateKeyEntry:
		inputView = m.renderKeyEntry()
	case statePressure:
		inputView = m.renderPressure()
	case stateContext:
		inputView = m.renderContextHint()
	}
	// The agent manager list takes the bottom panel while open.
	if m.agentList != nil {
		inputView = m.renderAgentList()
	}
	// A child agent's routed approval takes over the bottom panel when the
	// parent's own prompts aren't using it.
	if ask := m.activeChildAsk(); ask != nil {
		inputView = m.renderChildAsk(ask)
	}
	return dividerStyle(width) + "\n" + m.renderStatusBar(width) + "\n" + inputView
}

// startRun resolves which code block from the last response to execute.
// It returns either a message for the transcript, or entersConfirm=true after
// switching to the confirmation state. Bare /run takes the first block: the
// several-blocks case is routed to the picker before it gets here.
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
	// /run is the user's own command: it never runs contained, so the working
	// scope has nothing to say about it.
	m.pendingScope = scopeReach{}
	m.pendingBlast = m.resolveRadius(nil)
	m.clearQueueStrip()
	m.setTurnState(stateConfirmRun)
	return "", true
}

// updateConfirmRun routes confirm-prompt keys through the approval card
// ; the card's y/n/esc semantics match the original prompt, and [a]
// is offered only where a session grant is allowed.
func (m Model) updateConfirmRun(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	// A memory proposal confirms through its own prompt, not the card.
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
		// [d] opens the pending edit full screen; esc returns here
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
		// would classify the same way. Membership was on the strip
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
		// Approve, and stop asking about this shape of call for the session
		//. The grant is scoped to what the card showed — this
		// command's leading words, this file's directory — because that is
		// what the reader read before pressing the key. The blanket grants
		// the key used to hand out are `/permissions allow` now: a session-wide
		// "never ask me again" is a decision worth typing, not a decision
		// worth pressing while a card is in front of you.
		//
		// Safety-flagged commands, generic gated tools, and /run keep asking
		// (the card offers [a] only where a grant is allowed).
		if req := m.pendingApproval; req != nil {
			switch req.kind {
			case approvalExec:
				m.recordDecision(decisionAllow, "user-always")
				if prefix := m.grantCommand(req.command); prefix != "" {
					m.noteGrant("Commands starting " + strconv.Quote(prefix) + " will run without asking. /permissions revoke takes it back.")
				}
				m.syncChildGrants()
				return m.executeRun()
			case approvalDiff:
				m.recordDecision(decisionAllow, "user-always")
				if dir := m.grantEditDir(req.path); dir != "" {
					m.noteGrant("Edits in " + displayDir(dir) + " will apply without asking. /permissions revoke takes it back.")
				}
				m.syncChildGrants()
				return m.executeApprovedTool()
			}
		}
	case components.ApprovalRelease:
		// The card had the keyboard by arrival and this key is not one of its
		// answers, so it is the first letter of a sentence. The
		// decision stays exactly where it was.
		return m.releaseToDraft(msg)
	case components.ApprovalDeny:
		if m.pendingApproval != nil {
			return m.declineApproval()
		}
		m.pendingRun = ""
		m.setTurnState(stateInput)
		m.syncViewport()
		m.appendEntry(entry{kind: entrySystem, text: "Run cancelled."})
		m.viewport.SetLines(m.renderHistoryLines())
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
	// An approved command that writes outside the working scope puts those
	// directories in it — otherwise containment would go on refusing
	// the write the user has just approved.
	m.applyScopeGrant()
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
	// Assistant commands run contained when a mechanism is available;
	// /run — the user's own command — stays on the plain runner.
	if m.pendingApproval != nil && m.containment.Run != nil {
		runFn = m.containment.Run
		tailFn = m.containment.TailRun
	}
	return m, func() tea.Msg {
		start := time.Now()
		var out string
		var code int
		// The tail-capable runner feeds the live row when wired.
		if tailFn != nil {
			out, code = tailFn(ctx, command, tail.Set)
		} else {
			out, code = runFn(ctx, command)
		}
		return cmdDoneMsg{runID: runID, command: command, output: out, exitCode: code, duration: time.Since(start)}
	}
}

// commandContextPrefix opens that message, as a constant for the reason
// compactContextPrefix is one: input recall reads it to tell a line the
// reader typed from one the session wrote (recall.go).
const commandContextPrefix = "I ran this command:"

// commandContextMessage is appended to the conversation (as the user) so the
// model can see what a /run produced, without triggering a response.
func commandContextMessage(command, output string, exitCode int) string {
	if cut, truncated := tools.TruncateOutput(output, tools.MaxExecOutputBytes); truncated {
		output = cut + "\n… (output truncated)"
	}
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return fmt.Sprintf(commandContextPrefix+"\n```\n%s\n```\nExit code: %d\nOutput:\n```\n%s\n```", command, exitCode, output)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// resumeToolLoop requests the next model response after a round of tool
// results — unless this turn has hit the tool-round cap, in which case it
// pauses on the checkpoint that says what it managed and offers the ways on
// (a fresh message still continues the conversation and resets the
// counter).
func (m Model) resumeToolLoop() (tea.Model, tea.Cmd) {
	// Steering messages queued mid-turn join the conversation here, between
	// tool rounds, so the model sees them on its next request. They
	// count as fresh user input, so they also lift a hit round cap.
	if m.injectSteering() {
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
	}
	// The ceiling is the session's, not the Agent's, because [+50] raises it
	// for this turn alone.
	if !m.roundsUnbounded() && m.agent.Rounds() >= m.effectiveMaxToolRounds() {
		return m.pauseAtRoundLimit()
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
	// prompt; the stored conversation stays untouched, so leaving
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

// execToolsCmd dispatches an auto-run batch off the UI goroutine, stamping
// what it dispatched so the frame's status line can name it.
func (m *Model) execToolsCmd(calls []provider.ToolCall) tea.Cmd {
	m.runningTools = calls
	a := m.agent
	runID := a.RunID()
	return func() tea.Msg {
		return toolResultsMsg{runID: runID, results: a.ExecuteCalls(calls)}
	}
}

// waitForEvent reads the next stream event. If it is a token, any further
// tokens already buffered on the channel are drained into a single batch so
// the UI re-renders once per batch instead of once per token. Reasoning text
// drains into the same batch on its own string: it is a different act with a
// row of its own (think.go), and the two never have to be told apart after
// the fact because they never share a field.
func waitForEvent(events <-chan provider.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return doneMsg{}
		}
		if final := terminalMsg(ev); final != nil {
			return final
		}
		var text, think strings.Builder
		text.WriteString(ev.Token)
		think.WriteString(ev.Thinking)
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return tokenMsg{text: text.String(), think: think.String(), final: doneMsg{}}
				}
				if final := terminalMsg(ev); final != nil {
					return tokenMsg{text: text.String(), think: think.String(), final: final}
				}
				text.WriteString(ev.Token)
				think.WriteString(ev.Thinking)
			default:
				return tokenMsg{text: text.String(), think: think.String()}
			}
		}
	}
}

// terminalMsg converts a non-token stream event into its message, or returns
// nil for a plain token event.
func terminalMsg(ev provider.StreamEvent) tea.Msg {
	if ev.Err != nil {
		// The completed tool calls ride the failure: a stream that
		// broke after the model finished writing a call kept that call.
		return streamErrMsg{err: ev.Err, calls: ev.ToolCalls, reasoning: ev.Reasoning}
	}
	if len(ev.ToolCalls) > 0 {
		return toolCallsMsg{calls: ev.ToolCalls, usage: ev.Usage, reasoning: ev.Reasoning}
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
// quote the same numbers from one place.
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
	// Whatever repaint the arriving message still owed, it does not owe it any
	// more: the message is about to be an entry like every other.
	m.streamDirty = false
	// The round's think row stops spinning here however the stream ended —
	// finished, cancelled, or abandoned (think.go).
	m.settleThink()
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
	// Ctrl+C cancels the whole child tree with the turn.
	m.cancelSubagents()
	for _, tc := range m.agent.CancelTurn() {
		m.appendEntry(entry{kind: entryTool, toolName: tc.Name, toolArgs: tc.Arguments, toolResult: cancelledToolResult})
	}
	m.pendingApproval = nil
	m.memoryAsk = nil
	// The queue the strip described is gone with the turn, and so is every
	// batch grant made against it.
	m.clearQueueStrip()
	m.batchApproved, m.approvalTotal = nil, 0
	// Ctrl+C is a cancellation, and the close rows say so.
	m.turnOutcome = components.TurnCancelled
	m.finishStreaming()
	m.restoreSteering()
	// Restored steering empties the queue: the notice rail may shrink.
	m.syncViewport()
}

// injectSteering appends queued steering messages to the conversation and
// transcript as user messages, reporting whether any were queued. Steering is
// fresh user input, so it resets the tool-round counter.
func (m *Model) injectSteering() bool {
	if len(m.steering) == 0 {
		return false
	}
	// Whatever was staged goes with the first line of the batch: they are
	// all injected into the same round, so which one carries them is only a
	// question of where the transcript names them.
	atts := m.takeAttachments()
	for _, text := range m.steering {
		m.recordCheckpoint(text)
		m.agent.Append(provider.Message{Role: provider.RoleUser, Content: text, Attachments: atts})
		m.appendEntry(entry{kind: entryUser, text: text, attached: attachment.Names(atts)})
		atts = nil
	}
	m.turnCount += int64(len(m.steering))
	m.steering = nil
	m.denialNotice = ""
	m.resetRounds()
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
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return tea.Batch(m.requestStream(), m.autosaveCmd())
}

// restoreSteering returns queued-but-uninjected steering messages to the
// input when a turn ends abnormally (cancel, stream error), so nothing typed
// is silently lost.
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
	// Every entry knows the turn it belongs to, so a row that outlives its
	// turn can still name it — the rail's alerts do. An entry
	// that already carries one (a close block, a round-limit pause) keeps it.
	if e.turn == 0 {
		e.turn = m.turnCount
	}
	m.transcript = append(m.transcript, e)
}

func (m *Model) resetTranscript() {
	m.transcript = nil
	// The index a fan-out would have converted points into a transcript that
	// no longer exists, and so does the round's think row.
	m.spawnRow = 0
	m.thinkIdx = 0
	// The checklist is read off the transcript, so a transcript that is gone
	// takes the approved plan with it rather than pointing at entries that no
	// longer exist.
	m.planRun = nil
	// A selection is a pair of coordinates into a render of this transcript;
	// with the transcript gone they name nothing.
	m.clearSelection()
	m.invalidateRenderCache()
}

// flushStream repaints the transcript with as much of the arriving message as
// has landed, and forgets that a repaint was owed.
func (m *Model) flushStream() {
	m.streamDirty = false
	m.viewport.SetLines(m.renderHistoryLines())
	if m.atBottom {
		m.viewport.GotoBottom()
	}
}

// invalidateRenderCache forces the next renderHistory to re-render every
// entry (used when an entry's rendering changes in place, e.g. focus-mode
// expansion).
func (m *Model) invalidateRenderCache() {
	m.cached.reset()
}

// renderEntry renders one entry's own lines, always ending in exactly one
// newline and never in a trailing blank line. Spacing between entries is not
// an entry's business — separatorBefore owns it, so every caller that
// concatenates entries gets the same rhythm.
func (m Model) renderEntry(e entry, width int) string {
	return m.renderEntryKeys(e, width, false)
}

// renderEntryKeys is the same, told whether the row's own keys are live —
// which they are only while reading mode's cursor is standing on this row
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// Everywhere else the row is beside a live draft, `v` is a letter, and the
// row says so: its keys go grey and the key that hands the keyboard over is
// offered in the live treatment beside them.
func (m Model) renderEntryKeys(e entry, width int, keysLive bool) string {
	return m.renderEntryDetail(e, width, keysLive, false)
}

// renderEntryDetail is the same again, told whether the step this row belongs
// to has its detail open. Only the activity rows can answer to
// it; every other kind of entry renders the same inside an opened step as
// outside one, because a step opens the bodies of its calls and nothing else.
func (m Model) renderEntryDetail(e entry, width int, keysLive, stepDetail bool) string {
	switch e.kind {
	case entryUser:
		row := sty.User.Render("You") + "\n" + m.wordWrap(e.text, width) + "\n"
		if len(e.attached) > 0 {
			row += sty.SystemMsg.Render(clipRow("attached: "+strings.Join(e.attached, ", "), width)) + "\n"
		}
		return row
	case entryAssistant:
		return sty.Assistant.Render("Assistant") + "\n" + renderMarkdown(e.text, width) + "\n"
	case entryTool, entryCommand:
		// Compact one-row activity rendering; focus mode expands it,
		// and so does the step around it.
		return m.activityRowDetail(e, stepDetail).View(width) + "\n"
	case entryThink:
		// The round's reasoning, folded (think.go). Low verbosity draws no
		// row at all, and an entry that renders to nothing is not a unit, so
		// nothing downstream — spacing, line mapping, the reading cursor —
		// has to know it was skipped.
		if !m.showThink() {
			return ""
		}
		return m.thinkRowFor(e, width).View(width) + "\n"
	case entryTurnClose:
		if e.close == nil {
			return ""
		}
		c := *e.close
		c.KeysWaiting, c.Handover = !keysLive, m.rowHandover(keysLive)
		return c.View(width) + "\n"
	case entryFailure:
		return m.gateRow(m.failureRow(e), keysLive).View(width) + "\n"
	case entryStreamDrop:
		return m.gateRow(m.dropRow(e), keysLive).View(width) + "\n"
	case entryRoundPause:
		return m.gateRow(m.roundPauseRow(e), keysLive).View(width) + "\n"
	case entryFanout:
		block := m.fanoutBlockFor(e)
		if len(block.Lanes) == 0 {
			return ""
		}
		return block.View(width) + "\n"
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
		return sty.SystemMsg.Render(e.text) + "\n"
	case entryError:
		return sty.Error.Render("Error: "+e.text) + "\n"
	}
	return ""
}

// entryIsBlock reports whether an entry reads as a standalone block — a
// conversational turn, a diff, or a notice long enough to wrap onto its own
// lines — rather than as a row in the compact activity feed.
func entryIsBlock(e entry) bool {
	switch e.kind {
	case entryUser, entryAssistant, entryDiff, entryTurnClose, entryFanout:
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

// renderStatusBar renders the cockpit rail (
// docs/interface/surfaces.md#the-input-frame): the active mode, tool-round
// counter, context occupancy meter (coloured at the trim thresholds), usage
// and spend, queued steering, policy grants, and the sub-agent badge, with
// the model name right-aligned and dropped first when narrow.
func (m Model) renderStatusBar(width int) string {
	// Attached, the status bar scopes to the focused child.
	if m.attachedTo != "" && m.subagents != nil {
		return m.renderChildStatusBar(width)
	}
	return m.cockpitData(true).View(width)
}

// cockpitData assembles the cockpit segments. The frame's vitals rail
// omits the queued-steering extra — the notice rail carries it — so
// includeQueued is false there.
func (m Model) cockpitData(includeQueued bool) components.Cockpit {
	c := components.Cockpit{
		CtxPct:    -1,
		WarnPct:   warnThresholdPercent,
		AlertPct:  trimThresholdPercent,
		Reasoning: m.reasoningSegment(),
		Model:     m.modelName,
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
	// Round counter shows only mid-turn, so long tool loops are visible — and
	// through a round-limit pause, where the ceiling is the thing being
	// decided. The grant on offer is stated beside it, so the counter says
	// both what the bound is and what taking the offer would make it.
	if m.agent.Rounds() > 0 && (m.turnState() != stateInput || m.pausedAtRoundLimit()) {
		c.Round = m.roundCounter()
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
	// Steering messages waiting to be injected.
	if n := len(m.steering); n > 0 && includeQueued {
		c.Extra = append(c.Extra, fmt.Sprintf("queued %d", n))
	}
	// Active approval policy; absent in the default ask-everything
	// state.
	if p := m.policyLabel(); p != "" {
		c.Extra = append(c.Extra, p)
	}
	// Working sub-agents, with blocked-on-approval count.
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

// renderHistoryLines is the transcript the pane shows: the history, with any
// application-owned selection lit over it (select.go). The highlight
// is the last thing applied and the first thing dropped — the raw render is
// what the clipboard extraction reads, so no selection styling can reach it.
//
// Lines rather than one string is the currency the pane takes,
// so nothing between the block cache and the screen splits the session into
// lines again.
func (m *Model) renderHistoryLines() []string {
	lines := m.renderHistoryRawLines()
	if !m.selectableSurface() {
		return lines
	}
	return m.applySelectionHighlight(lines)
}

// renderHistory and renderHistoryRaw are the same two renders as one string.
// Nothing on the drawing path uses them: they are what the goldens capture
// and what the tests read, joined back up from the lines above.
func (m *Model) renderHistory() string {
	return strings.Join(m.renderHistoryLines(), "\n")
}

func (m *Model) renderHistoryRaw() string {
	return strings.Join(m.renderHistoryRawLines(), "\n")
}

func (m *Model) renderHistoryRawLines() []string {
	if m.state == stateFocus {
		// Focus mode renders fresh with the selection gutter, bypassing the
		// incremental cache; it scopes to whichever agent is focused.
		content, _, _ := m.renderFocusHistory()
		return strings.Split(content, "\n")
	}
	// Attached view: the focused child's session, rendered fresh from
	// the supervisor's live transcript (the parent's cache is untouched).
	if m.attachedTo != "" && m.subagents != nil {
		return strings.Split(m.renderAttachedHistory(), "\n")
	}
	if len(m.transcript) == 0 && m.turnState() != stateStreaming {
		// First contact: the empty session states what it already
		// knows about the project and offers work. Hosts without a survey —
		// the attached child view, a bare test model — keep the plain line.
		if m.startScreenShowing() {
			return strings.Split(m.renderStartScreen(m.transcriptWidth()), "\n")
		}
		return strings.Split(sty.Welcome.Render("Type a message to start chatting."), "\n")
	}
	w := m.transcriptWidth()
	if w != m.cached.width {
		m.cached.width = w
		m.invalidateRenderCache()
	}
	// History renders as step blocks. Every block but the last
	// is frozen — the grouping scan is left to right, so a block that already
	// has a successor can never change — and only the last one re-renders
	// each frame, because a running step's header restates its count and
	// duration as rows land.
	blocks := m.blocksOf(m.transcript)
	// Freeze everything before the last block rows can still land in. With an
	// approved plan that is not the last block: its declared-but-not-started
	// steps trail it, and they change as the run reaches them.
	// A live fan-out is the one entry that keeps changing without a row
	// landing in it, so its block cannot be frozen either.
	freeze := min(lastLiveBlock(blocks), m.liveFanoutBlock(blocks))
	// Back to the settled lines and no further: what the frozen blocks wrote
	// stays written, and only the tail after them is built again.
	m.cached.rewind()
	for bi := 0; bi < freeze; bi++ {
		blk := blocks[bi]
		if blk.end <= m.cached.count {
			continue
		}
		block, prev, have := joinUnits(m.blockUnits(blk, m.transcript, w, false, -1), m.cached.sep, m.cached.hasSep)
		m.cached.write(block)
		m.cached.freeze()
		m.cached.sep, m.cached.hasSep = prev, have
		m.cached.count = blk.end
	}
	prev, havePrev := m.cached.sep, m.cached.hasSep
	for _, blk := range blocks {
		if blk.end <= m.cached.count {
			continue
		}
		var block string
		block, prev, havePrev = joinUnits(m.blockUnits(blk, m.transcript, w, false, -1), prev, havePrev)
		m.cached.write(block)
	}
	if m.turnState() == stateStreaming && m.streaming != "" {
		if havePrev {
			m.cached.write(separatorBefore(prev, entry{kind: entryAssistant}))
		}
		m.cached.write(sty.Assistant.Render("Assistant") + "\n")
		// The one thing in the transcript that is not frozen, and the only
		// place the stable-prefix cache is used: everything else here is
		// either cached whole or rendered once (streammd.go).
		m.cached.write(m.streamMD.Render(m.streaming, w))
	}
	return m.cached.lines
}

// contentWidth is the surface inside the horizontal padding.
func (m Model) contentWidth() int {
	return m.columns().content.Dx()
}

// viewportHeight is the transcript's own rows, read off the vertical split
// rather than counted down from the terminal. The floor is a
// floor and not a layout: a terminal with no room left still has to hand the
// viewport a height it can render at.
func (m Model) viewportHeight() int {
	return max(m.surface().view.Dy(), 1)
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
		Foreground(components.Palette.Dim.Color()).
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
		// the config file instead of switching this session only.
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

	// /permissions was /mode until the name was the problem: one letter from
	// /model, on a menu that shows both, for a command whose whole job is
	// deciding what runs without asking. The old spelling still answers —
	// muscle memory is not a typo — but it is an alias now, and every line
	// the product prints says /permissions.
	case "/permissions", "/perms", "/mode":
		if len(parts) < 2 {
			return true, m.modeStatus()
		}
		// The grants are the mode's own subject — what the session has
		// stopped asking about — so they answer here rather than under a
		// command of their own.
		switch parts[1] {
		case "grants":
			return true, m.grantStatus()
		case "allow":
			return true, m.allowCommand(parts[2:])
		case "revoke":
			return true, m.revokeCommand(parts[2:])
		}
		if len(parts) > 2 {
			return true, "Usage: /permissions [manual|accept-edits|auto|plan|why|grants|allow|revoke]"
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

	case "/reasoning", "/think":
		return true, m.reasoningCommand(parts[1:])

	case "/stats":
		return true, m.statsReport()

	case "/ui":
		return true, m.uiCommand(parts)

	case "/add-dir", "/adddir":
		// The working scope: the grant made in front of no particular
		// decision. It lives beside /permissions rather than under it because
		// it answers a different question — not "what may run without
		// asking", but "where is the work".
		return true, m.scopeCommand(parts)

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

	case "/skills":
		if m.skills == nil {
			return true, "No skills loaded in this session. A skill is a directory holding a SKILL.md under .shhh/skills, .agents/skills or .claude/skills, in the project or your home directory."
		}
		return true, m.skillsList(m.skills)

	case "/plan":
		// Bare /plan reopens the approved plan mid-turn, which is how the
		// checklist stays reachable below 130 columns, where there is no rail
		// to hold it.
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
		// from the enter handler.
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
		// Future rewind branches hang off the named session.
		m.sessionName = name
		return true, fmt.Sprintf("Chat saved as %q", name)

	case "/load":
		if m.db == nil {
			return true, "Chat persistence is unavailable."
		}
		if len(parts) < 2 {
			// Only reached when there is nothing to pick; otherwise bare
			// /load opens the picker from the enter handler.
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
// /load <name> and the /load picker come through here.
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
  /paste [path]  Attach the clipboard — a screenshot, or files copied in a
                 file manager — to your next message; /paste <path> attaches
                 a file by name, /paste show <name> opens a staged image or
                 paste full-pane, /paste drop <name> takes one back out and
                 /paste clear drops what is staged (Ctrl+V)
  /copy [code]   Copy the last response (or just its code blocks)
  /run [n]       Run a code block from the last response (with confirmation)
  /model [name]  Switch the model (bare /model opens an interactive picker)
  /model default [name]   Show or persist the default model for new sessions
  /model agents [name]    Show or persist the model sub-agents run on
                 ("inherit" follows the session model)
  /permissions   What runs without asking, and the permission mode that
                 frames it (also /perms; was /mode)
                 [name]   manual, accept-edits, auto or plan; bare opens a picker
                 why      the latest auto-mode denial's reason
                 grants   what this session has stopped asking about
                 allow <commands|edits>   grant a whole category
                 revoke [commands|edits]  take the grants back
  /reasoning     How much thinking the model does before it answers:
                 off (the default), low, medium or high — Ctrl+R cycles them
                 [level]           set it for this session (also /think)
                 default [level]   show or persist the level new sessions
                                   start on (provider.reasoning)
  /context       The window as a meter, by category, with the tools itemised
  /stats         Context occupancy breakdown and cumulative session spend
  /ui            Activity feed density, pane layout, monochrome and mouse:
                 /ui verbosity <low|normal|high> · /ui mono <on|off> · /ui mouse <on|off>
                 (low hides counts, med collapses rows, high expands rows;
                  mouse is off by default so the terminal keeps click-drag
                  selection — on, shhh selects the transcript itself, the drag
                  scrolls past the pane, and a click opens the row or answers
                  the card key under it. Ctrl+X flips it and saves it)
                 terminal  what this terminal answered when shhh asked what
                           it can do: inline images, desktop notifications,
                           focus events, cell size
  /add-dir       The working scope: which directories this session may write
                 to. Bare lists it; <path> adds one (contained commands can
                 write there, and edits there stop asking about leaving the
                 scope); drop <path> takes it back
  /sandbox       Containment status and container sandboxes (doctor|scope|list|status|destroy <id>|prune)
  /evidence      Tool-output evidence store: reduction stats and size (purge to clear)
  /gate          Quality gate: run [suite] starts the project's checks in the background, result shows the verdict
  /ps            List the long-running processes this session owns (process tool)
  /memory        Durable memories: list (default) · add [global] [kind] <text> · forget <id>
  /todo          The project's backlog (.shhh/todo): bare opens a picker · show|edit <slug> ·
                 add <text> · block <slug> [why] · open|done|drop <slug>
  /skills        The skills this session loaded (SKILL.md directories), and
                 why any did not
  /skill <name> [task]
                 Activate a skill now: its instructions go to the model with
                 your task, as the model would load them itself. /<name>
                 does the same for a skill whose name is not a command
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
  /compact       Continue from a summary plus the most recent turns
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
  Enter          Send message        Shift+Enter  Insert newline
                 (Alt+Enter and Ctrl+J do the same, for terminals that cannot
                  report Shift+Enter)
  Ctrl+V         Attach the clipboard: a copied screenshot or file is staged
                 for your next message, ordinary text still pastes into the
                 draft. Dragging an image into the terminal attaches it the
                 same way. What is staged shows as chips above the input
  Pasting        Text taller than 10 lines or wider than 1000 columns is
                 staged as paste-1.txt rather than typed into the draft — both
                 through Ctrl+V and through your terminal's own paste — so a
                 stack trace does not bury the sentence it came with. Those
                 two numbers are the defaults for appearance.paste_lines and
                 appearance.paste_columns; shhh config shows this machine's,
                 and a negative turns one of them off. /paste show
                 paste-1.txt reads it back before you send it, and a paste
                 over 256 KB is refused rather than staged — it would ride in
                 the prompt itself
  Tab            Complete a slash command (typing / opens the menu;
                 ↑↓ move, Enter runs the highlighted command, Esc dismisses)
  Ctrl+K         Command palette: one prompt over commands, saved chats and
                 the files this session touched — type to filter, Enter runs,
                 Tab writes it into the input, Esc dismisses
  Ctrl+R         Cycle the reasoning level: off → low → medium → high. It
                 changes the next model request, not the one in flight, and
                 the level is stated on the vitals rail beside the model
  Shift+Tab      Cycle the permission mode
                 (while the agent is working, Enter queues a steering message
                  that joins the conversation before the next model request)
  Up/Down        Recall previous inputs (when the input is empty)
  Shift+Up       Scroll the transcript a line, without leaving the prompt —
  Shift+Down     the draft keeps the keyboard and every letter it has
                 (Ctrl+Up and Ctrl+Down do the same, for terminals that
                  report them)
  Ctrl+E         Reading mode: select tool/command/diff rows (j/k), expand/collapse (Enter),
                 pgup/pgdn page, ? lists every key the mode has, Esc or typing
                 returns to the prompt
                 (Enter on an edit row cycles collapsed → expanded → full-screen diff;
                  opens over a running turn, which keeps streaming underneath;
                  a transcript with nothing expandable opens as a plain pager)
  Ctrl+O         Open one step's detail: every row in it shows its output body,
                 bounded. From the prompt it opens the step in flight and the
                 draft keeps the keyboard; in reading mode it opens the step the
                 cursor is standing in. Press it again to close it
                 (/ui verbosity high is the same thing for every step at once)
  Ctrl+A         Agent manager: enter attaches to an agent's session, x cancels
                 its turn, X kills it; attached, typing steers the agent,
                 Shift+Tab sets its mode (clamped), Esc detaches
  Ctrl+G         Open the draft in your editor: $EDITOR (then $VISUAL, then
                 vi) opens a file holding what you have typed, at the line and
                 column the cursor was on, and whatever is in the file when
                 the editor exits becomes the draft. An empty file leaves the
                 draft alone. Not while a turn is running or a decision is
                 waiting — the editor takes the terminal with it
  Ctrl+Space     Hand the keyboard to a decision waiting on screen. An
                 approval that lands while you are typing does not take your
                 keys with it: its y, n and a are not live until this chord
                 gives them the keyboard, and until then every letter goes
                 into the draft. Esc leaves the decision waiting; n is how
                 you say no
  Esc            Clear the input
  Ctrl+C         Cancel response / clear input / quit
  Ctrl+D         Quit
  PgUp/PgDn      Page the transcript, leaving the keyboard in the prompt.
                 Scrolling away pauses the follow while a turn streams; the
                 notice rail counts what is below and PgDn walks back to it
  Wheel          Scroll the transcript (or the full-screen diff / review),
                 leaving the draft and the keyboard where they are
                 (needs Ctrl+X — off by default, so the terminal keeps its
                  own click-drag selection)
  Click-drag     With the mouse on (Ctrl+X), select transcript text: the drag
                 scrolls the pane when it reaches an edge, so a selection can
                 run past the screen; releasing copies it, Esc cancels
  Click          A press and release in the same cell opens the activity row
                 under it, the way Enter does in reading mode, or answers the
                 key it lands on in an approval card's [y/n/a]. It never takes
                 the keyboard: the draft keeps every character
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
	// The session's spend starts over with its accounting, or the rail would
	// keep quoting a bill for a conversation that no longer exists.
	m.ledger.Reset()
	m.TotalTokensIn, m.TotalTokensOut = 0, 0
	m.resetRounds()
	// The turn's accounting started over, so there is no longer a turn to
	// close with a summary either.
	m.turnOpen = false
	// A new conversation is a new session with a slot of its own; the one
	// just left stays in the store as it was, for --resume to find.
	m.sessionName = newSessionName()
}

// loadConversation replaces the current conversation and rebuilds the
// transcript from the stored messages.
func (m *Model) loadConversation(msgs []provider.Message) {
	// A loaded conversation is a session with a past; the start screen does
	// not come back after it is cleared.
	m.spendStartScreen()
	m.agent.SetMessages(msgs)
	m.resetTranscript()
	m.checkpoints = checkpointsFromMessages(msgs)
	m.appendMessageEntries(msgs)
	// The prompts that conversation was made of are what ↑ recalls in it
	// (recall.go). They are seeded here rather than by each of the four
	// callers, for the same reason the transcript is: every path back to a
	// stored conversation passes through this one function.
	m.recallFromMessages(msgs)
}

// appendMessageEntries renders a run of messages into the transcript: the
// user turns, the assistant text, and one tool entry per call paired with the
// result that followed it. It is shared by the session load and by the tail a
// compaction carries through, so a conversation put back on screen
// looks the same however it got there.
func (m *Model) appendMessageEntries(msgs []provider.Message) {
	for i, msg := range msgs {
		switch msg.Role {
		case provider.RoleUser:
			// A resumed turn keeps the names of what it attached:
			// the bytes were saved with it, so the row that said "attached:
			// shot.png" says it again.
			m.appendEntry(entry{kind: entryUser, text: msg.Content,
				attached: attachment.Names(msg.Attachments)})
		case provider.RoleAssistant:
			// The thinking that led to the turn comes back with it, above it,
			// where it happened (think.go). A conversation that is still
			// replaying its reasoning to the model and no longer showing it
			// would be the transcript quietly disagreeing with the request.
			if think := reasoningText(msg.Reasoning); think != "" {
				m.appendEntry(entry{kind: entryThink, text: think})
			}
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
