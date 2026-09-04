package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/caps"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// sessionNameLayout is how a session that was never named is called in the
// store: the moment it began, to the second. Every session gets a slot of its
// own — the autosave used to go to one shared slot, which meant each new
// session silently overwrote the last and `--resume` only ever had one thing
// to offer. `--continue` reopens whichever slot was written most recently.
const sessionNameLayout = "2006-01-02 15:04:05"

// newSessionName is what a fresh conversation is called before the store has
// been asked for it. It is the clock and nothing else: the timestamp is for
// the person reading a listing, and what makes it unique is the store's
// claim, which is the only party that can see the other sessions.
// See docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session.
func newSessionName() string {
	return time.Now().Format(sessionNameLayout)
}

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
	// stateOutputFull: a row's whole output is showing full screen (
	// docs/interface/surfaces.md#the-activity-row) — the depth past the
	// in-place body, from reading mode's [enter] or a command card's [d].
	stateOutputFull
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
	// stateTodoPropose: the proposals card from a bare /todo add is showing —
	// the session read into backlog items, checked ones written on enter.
	stateTodoPropose
	// statePersona: the profile drafter has the screen — the brief, the
	// drafter's questions, the wait, and the draft on its card
	// (docs/interface/surfaces.md#the-profile-drafter). It is a takeover
	// because every step of it is typed into, so the input it would borrow
	// is the input it has to own.
	statePersona
	// stateTodoPause: a backlog run is paused on the person — the plan, the
	// questions and the size are showing, with go ahead / re-plan / stop.
	stateTodoPause
	// stateQuitConfirm: the inline confirm quitting over a live turn asks
	// through (docs/interface/surfaces.md#the-inline-confirm) — a surface,
	// so the turn keeps running while the question is up.
	stateQuitConfirm
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
	// stateCloseGate: the turn's work is done and the repository's own
	// checks are running over it. It is a stage of the turn and not a
	// surface — the turn's accounting is still open, because the verdict
	// belongs on the close row and a failing one may still earn the turn
	// another round (gate.go).
	stateCloseGate
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
	// stateScaffold: the card offering to write this project's `.shhh`
	// context file is up (scaffold.go). It is a takeover because the reader
	// asked for it — from the start screen's third row or by typing /init.
	stateScaffold
	// statePasteDrop: the selector a bare `/paste drop` opens over the
	// staged chips — checked ones are dropped on enter, esc drops none. The
	// one-chip case asks through the inline confirm instead.
	statePasteDrop
)

const inputHeight = 3
const headerHeight = 1
const dividerHeight = 1
const statusBarHeight = 1

// resizeSettle is how long the terminal size must hold still before the whole
// history is re-wrapped at it. A drag delivers a size per column crossed, and
// the full re-render is the only expensive part of answering one; the frame
// and rectangles move immediately. A constant rather than a setting — it is a
// property of how terminals report drags, not a preference.
const resizeSettle = 120 * time.Millisecond

// resizeSettledMsg is the settle window closing. The sequence number keeps a
// settle scheduled during the drag from rendering at a width the drag has
// already left.
type resizeSettledMsg struct{ seq int }

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
type doneMsg struct {
	usage *provider.Usage
	// stop is why the model stopped writing. It matters here for one value:
	// a reply cut off at the output ceiling is half an answer, and the
	// session offers to have it finished rather than filing it as the whole
	// one (resume.go).
	stop provider.StopReason
}

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
	// stop is why the model stopped writing. A round of calls that ended at
	// the output ceiling has lost whatever the model had not finished
	// writing, and the round runs what survived under a notice saying so.
	stop provider.StopReason
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
	// local: a `!!` run — the output lands in the transcript and never in
	// the conversation (bang.go).
	local bool
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
	// recovery row and holding the partial it offers to continue from. Both
	// ways a reply stops halfway draw it — a wire that broke and a model that
	// filled its output budget — because what is on offer is the same offer
	// (resume.go).
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
	// entrySummary: a session reading, folded into one activity row
	// (summary.go). The rail draws the latest reading bounded to three lines;
	// this is where a longer one can be read whole, and where the readings
	// before it are still on record.
	entrySummary
	// entryTodoRun: the backlog run, one row for the whole of it, updated in
	// place as the run moves through its stages (todorun.go). It holds a
	// handle on the run's own state rather than a copy of it, the way the
	// fan-out block holds a batch number.
	entryTodoRun
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
	// localRun marks a command row whose output stayed out of the
	// conversation — a `!!` run — which its outcome says (bang.go).
	localRun bool
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
	// todorun is the run behind an entryTodoRun row. A pointer for the same
	// reason: the row draws the machine's own state at render time, so it
	// moves as the run moves and re-renders at any width.
	todorun *todoRunRow
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
	// this entry titles — what /step opens and closes. It is a
	// third override rather than a level of the first two because it answers
	// a different question: stepFold and groupFold decide which rows are on
	// screen, this decides how much of each one is.
	detailFold foldState
	// thinkDepth is how much of an entryThink row's body is on screen — the
	// reader's own answer to [enter], recorded on the entry so the row
	// re-renders at any width like every other one.
	thinkDepth thinkDepth
	// reading is the session summary behind an entrySummary row (summary.go):
	// the verdict as it landed, plus the target it was judged against. Both
	// are stored rather than read back off the model, because the target is
	// anchored per turn and an old row must keep saying what it was actually
	// read against once the next instruction has moved it.
	reading *summaryReading
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
	// newSession is the half of a session boundary that lives outside this
	// model: the record closed and reopened, and the system prompt built
	// again. Nil in a host that has neither, which is every front-end but
	// the two commands — the conversation still starts over, on the prompt
	// it already had.
	newSession NewSession
	// workspaceBlock is the checkout read again, as the prompt section that
	// states it. Nil in a host that cannot survey one, which leaves a
	// rebuilt conversation on the reading it already carried.
	workspaceBlock func() string

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
	// composed is how many bytes of tool-call arguments the round in flight
	// has written. It is a count and not a buffer: what the round asked for
	// arrives whole on the terminal event, and the half-written JSON on the
	// way there is worth a number and nothing else (activity.go).
	composed int
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
	// turnUp and turnDown carry the turn's token counts, and sessionUp and
	// sessionDown the session's, from the figure on screen to the figure the
	// session has measured — a step per frame of the counter above. The
	// session's are separate counters rather than the turn's plus what came
	// before, for the reason the rail's own accounting gives (vitals.go).
	turnUp, turnDown       components.Odometer
	sessionUp, sessionDown components.Odometer
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
	// histSearch is the open reverse search over the ring, or nil
	// (historysearch.go).
	histSearch *historySearch

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
	// pendingRunLocal marks a `!!` command (bang.go): it runs like any
	// /run, and its output stays out of the conversation.
	pendingRunLocal bool
	runCancel       context.CancelFunc
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
	// What the session has stopped asking about, and the mode that frames it
	// (policy.go).
	policy policyState
	// scope is the session's working scope: the directory it was
	// opened in plus whatever has been added to it since. It is a pointer
	// because the runner closures that wrap contained commands read it off
	// the UI goroutine, and because a grant made on a card has to be the same
	// grant the sandbox sees on the next command.
	scope *scope.Scope
	// Auto mode's LLM permission classifier: judges gated calls the
	// static policy would ask about; nil falls back to asking the user.
	classifier       *agent.Classifier
	classifierCancel context.CancelFunc
	// The session summary (summary.go): a cheap model's periodic read
	// of what the session is doing, drawn as the rail's SUMMARY block.
	summarizer    *agent.Summarizer
	summary       summaryState
	summaryCancel context.CancelFunc
	// The session titler and what it has written — title.go.
	titler      *agent.Titler
	titles      titleState
	titleCancel context.CancelFunc
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
	// mouseOn turns terminal mouse reporting on (ctrl+x, /ui mouse). It is
	// on by default so the wheel scrolls the transcript, click-drag selects
	// text, and clicks open rows or answer cards. Turning it off hands
	// selection back to the terminal's native selector.
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
	// railDiff is the rail cell a file's diff was opened from (railclick.go).
	railDiff pointerPress
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
	// closeGate is the run a turn makes as it closes: whether this session
	// honours the workspace's on_close suite, and where the current turn's
	// run has got to (gate.go).
	closeGate closeGateRun
	// processes backs /ps and process-start approval gating.
	processes Processes
	// memory backs /memory and the remember-tool confirm flow;
	// memoryAsk is the open memory prompt while a proposal awaits the user.
	memory Memory
	// todos backs /todo and the TODO block; todoStore is the backlog as last
	// read from disk, reloaded on the events that can change it.
	todos     Todos
	todoStore *todo.Store
	// todoPropose is the open proposals card and todoProposals what it is
	// showing; todoExtractRun numbers readings so a late one is dropped.
	// The backlog run in progress, if any (todorun.go).
	todoRunner        todoRunState
	todoPropose       *components.MultiSelect
	todoProposals     []todo.Proposal
	todoSprintPlan    []string
	todoExtracting    bool
	todoExtractRun    int
	todoExtractCancel context.CancelFunc
	memoryAsk         *components.NoteSelect
	// secrets backs /secret and the scrub on the agent.
	secrets Secrets
	// skills is the session's skill catalog, behind /skills, /skill and
	// the /<skill-name> shortcut; nil when none loaded. skillsList renders
	// the catalog for /skills — the same text `shhh skills` prints.
	skills     *skill.Catalog
	skillsList func(*skill.Catalog) string
	// mcp is the session's MCP servers: which tools are theirs, which run
	// as reads, and the /mcp listing.
	mcp MCP
	// compacting marks an in-flight /compact request: the streamed
	// response is a summary handled by finishCompact, not conversation text.
	compacting bool
	// compactSummary is the last summary a compaction produced, carried so
	// every save can put it on the slot for the next opening (reopen.go).
	// Empty is a conversation that never compacted, and nothing here ever
	// writes one that a compaction did not.
	compactSummary string
	// observer receives the session's content-free events; turnCount and
	// toolDefTokens feed it and /stats.
	observer      observe.Observer
	turnCount     int64
	toolDefTokens int64
	// subagents supervises spawned child agents; childAsks queues
	// their approval requests routed into this session's approval surface.
	subagents *subagent.Supervisor
	childAsks []*subagent.Ask
	// decisionHeld is whether the decision on screen holds the keyboard
	//. A card that arrives on top of a sentence never does:
	// until the handover chord it renders its keys as not-yet-live and every
	// letter goes into the draft. One that arrives on an empty draft does,
	// because there is no sentence for the letters to belong to — with the
	// grace window covering the keys a warm keyboard could still have in
	// flight (interrupt.go).
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
	// graceFrom is when the decision now holding the keyboard by arrival
	// landed on a keyboard still warm — the open grace window (interrupt.go).
	// Zero when no window is open; graceSeq names the window's current end,
	// so a repaint tick scheduled for an end a key moved is stale.
	graceFrom time.Time
	graceSeq  int
	// lastDecisionLeft is when a decision last left the screen, which is how
	// a card replacing another (the queue advancing) is told apart from a
	// card landing on fresh typing.
	lastDecisionLeft time.Time
	// resizeSeq names the latest resize, so the settle scheduled for an
	// abandoned width recognises itself as stale (resizeSettledMsg).
	resizeSeq int
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
	// /save, /load, and branch switches); gitSnapshot records the workspace
	// git state per checkpoint when wired. The picker the gesture opens is
	// the selector family's (rewind.go).
	checkpoints []checkpoint
	sessionName string
	gitSnapshot func() GitSnapshot
	// Rich diff rendering: fullDiff is the viewer showing full
	// screen, diffReturn where esc goes back to.
	fullDiff   *components.DiffView
	diffReturn state
	// The full-screen output view (outputview.go): fullOutput is the viewer
	// while it has the pane, outputIdx the transcript entry it came from
	// (noOutputEntry for the command card's full view), outputReturn where
	// esc goes back to.
	fullOutput   *components.OutputView
	outputIdx    int
	outputReturn state
	// The approval card's scroll (docs/interface/surfaces.md#the-approval-card):
	// the card is rebuilt every frame, so its offsets live here and are reset
	// whenever the card changes (armConfirm).
	cardScroll int
	cardPan    int
	// readingCopied is the reading rail's note about the last [y]: what was
	// copied and how far it ran. It stands until the next key in the mode,
	// which is the moment the reader has moved on from the copy it captions.
	readingCopied string
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
	// The two-press windows and the question asked before a turn that is
	// not over is ended (cancel.go): armed is the open window between a
	// first press and its second, quitAsk the confirm while the question is
	// up, and quitAskYes what a yes carries out — quitting, or crossing the
	// session boundary. The act is set with the confirm rather than read off
	// a flag beside it, because a flag can say one thing while the words on
	// screen say another.
	armed      armedPress
	quitAsk    *components.Confirm
	quitAskYes func(*Model) tea.Cmd
	// workspace is the directory the session's relative paths belong to. It
	// is stated once, for the reason sessionDir is (terminal.go), and it is
	// also what lets a surface be exercised against a scratch directory
	// without moving the process — a test that chdirs records the target as
	// one of its package's cache inputs and makes the package uncacheable.
	// Empty means the process's own working directory.
	workspace string
	// Per-turn changeset store: changes records every applied edit
	// with the content on both sides, keyed by turn, and is what /diff
	// renders; tracker answers whether git knew about a file when it was
	// edited, and is nil outside a repository.
	changes *changeset.Store
	tracker *changeset.Tracker
	// The open completion menu — slash commands, their arguments, and the @
	// file mention (complete.go).
	complete completionState
	// Interactive slash-command pickers: picker is the open select
	// card, pickerApply consumes the chosen index and returns the transcript
	// note; modelOptions is the /model picker's model catalog.
	//
	// pickerAll is the list the picker opened over and pickerIndex maps the
	// rows it is showing back onto it, so a choice made through the filter row
	// still reaches an apply written against the whole list.
	picker       *components.Select
	pickerApply  func(*Model, int, bool) (string, tea.Cmd)
	pickerAll    []components.SelectOption
	pickerIndex  []int
	modelOptions []string
	// chats is the saved-chat picker's housekeeping — chats.go.
	chats chatOps
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
	// followUps are drafts queued with alt+enter while a turn was live,
	// sent one per turn end once the session is idle (followup.go). held
	// stops the automatic send after a cancel: the queue survives, the rail
	// says so, and the reader decides what still applies.
	followUps     []string
	followUpsHeld bool
	// pasteDrop is the open `/paste drop` selector and pasteDropConfirm the
	// inline confirm the one-chip case asks through (attachments.go).
	pasteDrop        *components.MultiSelect
	pasteDropConfirm *components.Confirm
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
	pasteLines   int
	pasteColumns int
	// railCols is the inspector rail's column count when the session was
	// given one (appearance.rail_width, /ui rail), and zero when it is the
	// width ladder's to decide. Both go through railWidth, which holds a
	// given number to the same limits it holds the ladder to (layout.go).
	railCols int
	title    string
	// The terminal's own window: whether the tab is named after this session
	// (appearance.window_title), the directory that name carries, and the
	// brief red the tab wears after a turn breaks with the sequence that
	// clears it (terminal.go).
	windowTitleOn  bool
	windowDir      string
	progressFailed bool
	progressSeq    int
	width          int
	height         int
	ready          bool
	atBottom       bool
	quitting       bool
	initialPrompt  string

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
	// calibration is what this session has learned about its own estimator
	// from the reports that have arrived, and it scales every estimate the
	// accounting makes once a report has been compared against one. It is the
	// session's, not the conversation's: /clear and a compaction throw away
	// the messages, and neither changes how the model counts them.
	calibration agent.Calibration
	// vitals is the session's per-turn usage history and the burn series
	// behind the rail's sparkline; projectTokens is the estimated
	// size of the project context inside the system prompt, which the
	// occupancy breakdown names separately.
	vitals        vitals
	projectTokens int64
	prices        *pricing.Table
	// endpointWindows answers what the endpoint serving the session's model
	// says its context length is, for the runtimes that report one. Nil for
	// every provider whose models the public table already describes.
	endpointWindows func(string) (int64, bool)
	modelName       string
	updateNotice    string
	keysNotice      string
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
	start *StartInfo
	// conversation marks `shhh chat`; notebook is its shared notebook.
	conversation bool
	notebook     *notebook.Store
	// personas is the profile-drafting flow's wiring; persona the one in
	// progress, personaScreen the surface it runs on.
	personas      Personas
	persona       *personaFlow
	personaScreen *components.ProfileScreen
	startFocus    int
	startSpent    bool
	// scaffold is the project-scaffolding offer and the write behind it
	// (scaffold.go).
	scaffold Scaffold
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
	// backoff is the shared bound and schedule this stall is being retried
	// on (internal/agent). It outlives each individual wait, which is what
	// makes the bound a bound.
	backoff  agent.Backoff
	retrySeq int
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
	// The hold: the turn parked at a round boundary, and whether one has
	// been asked for and not yet reached (hold.go). Like the pause above
	// them both belong to the turn — a turn the session has moved past
	// cannot be let go of, and dropHold is what says so.
	hold      *turnHold
	holdAsked bool
	// framed is the frame being painted, and is non-nil for exactly the
	// length of one paint (layout.go). Everything a paint measures — the
	// column split, the bottom panel, the live tail — is drawn from it as
	// well as measured from it, so each is resolved once instead of once per
	// reader. It is set on paint's own copy of the model and on nothing that
	// outlives the paint; a nil frame is every other caller, which resolves
	// its own answer as before.
	framed *frame
}

// NewSession is the host's half of a session boundary, called once each time
// the front-end crosses one. It ends the session in the record and opens
// another, and answers with what a launched session would have started from.
// Everything the boundary resets inside the conversation is this model's own
// work; the record and the prompt are the host's, because the recorder and
// the prompt builder live where the session was assembled.
// See docs/capabilities/sessions-and-memory.md#a-new-conversation-is-a-new-session.
type NewSession func() SessionStart

// SessionStart is what the host hands back across the boundary.
type SessionStart struct {
	// Prompt is the system prompt built again from the checkout as it stands
	// now, so the new conversation opens on the tree it is actually in. Empty
	// leaves the conversation on the prompt it had, which is the honest
	// answer from a host that cannot rebuild one.
	Prompt string
	// Resume is the command that reopens the slot the conversation was left
	// in — the same one the exit banner names, since crossing the boundary
	// is quitting without the exit and leaves the same thing behind.
	Resume string
	// ProjectTokens is what the instruction files inside that prompt cost,
	// which the occupancy breakdown names as its own category. It travels
	// with the prompt because it is a measurement of it: a figure left over
	// from the last build would describe a prompt the session is no longer
	// sending the moment an instruction file is edited.
	ProjectTokens int64
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
	db, name, title := m.db, m.sessionName, m.titles.title
	// What the next opening of this conversation starts from: the summary its
	// last compaction wrote, and the commit the checkout is on now (reopen.go).
	// The commit is asked for in the command rather than here, because this
	// runs on the way to a frame and that one does not.
	summary, dir := m.compactSummary, m.workspace
	// And the mark saying the conversation is mid-turn, which is what makes
	// quitting while held and starting again the same place (hold.go). It
	// rides the save itself rather than a write beside it: the conversation
	// and what the slot says about it are one fact, and two autosaves
	// overlapping could otherwise land their halves in either order.
	hold := m.holdMarker()
	// The slot's name is what joins this session's metrics to its
	// transcript, so the recorder learns it here, where the slot is decided.
	if m.observer.Session != nil {
		m.observer.Session(name)
	}
	// The reading this opening put in front of the conversation is left out
	// of what the slot keeps: it is rebuilt from the checkout every time the
	// conversation is opened (reopen.go).
	msgs := stripResumeContext(m.agent.RequestMessages())
	return func() tea.Msg {
		// A slot another session has taken over is not written to; the
		// store puts the conversation in one of this session's own and
		// says where, which is the slot every save after this one goes to.
		slot, err := db.AutosaveChat(name, newSessionName(), msgs, hold)
		if err != nil {
			return nil
		}
		// The title rides every save, so a slot written after the reading
		// landed carries it and /save name takes it along.
		if title != "" {
			_ = db.SetChatTitle(slot, title)
		}
		// And so does what the conversation is opened again on. The commit is
		// read here, at the save, so the slot says where the tree was when
		// this conversation was last written down rather than where it was
		// when the process started.
		_ = db.SetChatResume(slot, storage.ChatResume{Summary: summary, Head: project.Head(dir)})
		if slot != name {
			return autosaveMovedMsg{from: name, to: slot}
		}
		return nil
	}
}

// autosaveMovedMsg says an autosave found its slot taken and wrote the
// conversation somewhere else.
type autosaveMovedMsg struct{ from, to string }

// noteSlotMove follows the store to the slot it put the conversation in and
// tells the reader, who is the only one who can see that they have two
// sessions open. Nothing is saved here: the store already wrote the
// conversation down, and this is the session catching up with where.
// See docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session.
func (m *Model) noteSlotMove(msg autosaveMovedMsg) {
	if msg.from != m.sessionName {
		// A second save was in flight when the first moved; both landed in
		// the same slot and the session is already in it.
		return
	}
	m.adoptSlot(msg.to)
	// The slot is what joins this session's metrics to its transcript, and
	// the one it was reported under now holds somebody else's conversation.
	if m.observer.Session != nil {
		m.observer.Session(msg.to)
	}
	// A title read for the slot that was lost must not be written to it
	// either: finishTitle stamps the slot the reading was taken for, which
	// is that session's row now. The reading starts over here.
	m.resetTitle()
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
		"Another session has written to %q, so this conversation moved to %q. Nothing there was overwritten.",
		msg.from, msg.to)})
	m.syncViewport()
}

// quitCmd quits, autosaving first when possible. A session with nothing to
// save gives its slot back on the way out: the claim was made at the start
// so that no other session could take the name, and a row nothing will ever
// be written to is one every later rename has to work around.
func (m Model) quitCmd() tea.Cmd {
	if save := m.autosaveCmd(); save != nil {
		return tea.Sequence(save, tea.Quit)
	}
	if m.db != nil {
		_ = m.db.ReleaseChatSlot(m.sessionName)
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
	b.Session, b.Title, b.Resume = m.sessionName, m.titles.title, resume
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
	// The draft box's height follows its content (frame.go). It is derived
	// here for the spinner's reason: a keystroke, a paste, a /clear and a
	// resize can all re-wrap the draft, and every one of those paths would
	// otherwise have to remember to re-measure it.
	mm.syncInputHeight()
	// The grace window's repaint rides the tail for the same reason: the
	// window is opened by arrivals and moved by discarded keys, and each of
	// those is a transition between the model before and the model after.
	if tick := mm.graceTickCmd(m); tick != nil {
		cmd = tea.Batch(cmd, tick)
	}
	// The counters are eased here for the spinner's reason as well: a usage
	// report, a chunk of prose and a turn opening or closing all move them,
	// and every one of those is a transition rather than a message one
	// handler could be trusted to own (turnstatus.go). It runs before the
	// spinner's rule because a climb still running is one of the things that
	// keeps the chain alive.
	mm.easeCounts()
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
	// And the tab's progress light, for the same reason again: a turn
	// breaking is a transition, and the paths that break one are the same
	// dozen (terminal.go).
	if tick := mm.progressCmd(m); tick != nil {
		cmd = tea.Batch(cmd, tick)
	}
	// The turn's closing summary is derived here too (summary.go), and
	// for the third time for the same reason: "the turn just ended" is a fact
	// about two models, and every path back to the input would otherwise have
	// to remember to ask for one.
	if read := mm.summaryCloseCmd(m); read != nil {
		cmd = tea.Batch(cmd, read)
	}
	// And the session's title (title.go), read once the first turn is over.
	if read := mm.titleCloseCmd(m); read != nil {
		cmd = tea.Batch(cmd, read)
	}
	// And the repository's own checks over what the turn wrote (gate.go).
	// The turn has reached its close and is waiting on the verdict, which
	// is a state the transition above put it in and nothing else will start
	// the run from.
	if run := mm.closeGateCmd(); run != nil {
		cmd = tea.Batch(cmd, run)
	}
	// And the backlog runner's next stage (todorun.go), for the same
	// reason again: a stage's turn ending is a transition.
	// The hook hands back the model whether or not it acted — a pause acts
	// and returns no command — so the model is always taken.
	after, step := mm.todoRunAfter(m)
	mm = after
	if step != nil {
		cmd = tea.Batch(cmd, step)
	}
	return mm, cmd
}

// answered marks a message one of the three routes took. It exists so a
// handler whose whole answer is another handler's answer stays one line:
// those hand back the session and a command, and the route adds only that
// the message is spent.
func answered(next tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd, bool) {
	return next, cmd, true
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A modified Enter is a line break, not a send. The modifiers mean the
	// same thing, so they are rewritten here into the one key the textarea's
	// newline binding listens for — before any surface can mistake a
	// shift+enter for a send of its own. Alt+enter alone is not rewritten:
	// it queues a follow-up while a turn is live (newline.go, followup.go).
	if newlineKey(msg) {
		msg = ctrlJ
	}
	// What the terminal can do is folded in wherever the reply lands
	//. The answers come back as unrelated message types, one
	// per question, over however long the terminal takes to send them, and
	// none of them is anything else on this switch's business — so they are
	// read here, before the routing, and go on to it unchanged.
	m.caps.Update(msg)
	// The terminal's own background is the one capability that changes what
	// is already on screen: under the auto theme it decides which table the
	// surfaces draw with, so a reply that arrives mid-session leaves every
	// cached row painted in the table the session opened with
	// (docs/interface/principles.md#a-colour-is-three-values-and-a-ground).
	if bg, ok := msg.(tea.BackgroundColorMsg); ok && components.SetGround(bg.IsDark()) {
		m.invalidateRenderCache()
	}
	// Three routes, and every message takes exactly one of them: what the
	// terminal and the window did (system.go), what the reader pressed
	// (keyroute.go), and what the session's own turn reported (turn.go).
	// They were one switch of a thousand lines, which is long enough that the
	// three had started to borrow one another's locals, and long enough that
	// nothing said which of the three a new case belonged in.
	//
	// Only a key falls out of its route: one no surface and no chord claimed
	// is the start of a sentence and belongs to the draft on the tail below,
	// so the key route hands the session back stamped whether or not it
	// answered.
	if next, cmd, handled := m.updateSystem(msg); handled {
		return next, cmd
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		next, cmd, handled := m.updateKey(key)
		if handled {
			return next, cmd
		}
		m = next.(Model)
	} else if next, cmd, handled := m.updateTurn(msg); handled {
		return next, cmd
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
	// What moved since the last boundary is said before the message it
	// would otherwise be read against — and before the checkpoint, so the
	// checkpoint still points at the person's own words.
	m.injectTreeNotice(true)
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

// commandContextPrefix opens that message, as a constant for the reason
// compactContextPrefix is one: input recall reads it to tell a line the
// reader typed from one the session wrote (recall.go).
const commandContextPrefix = "I ran this command:"

func firstLine(s string) string { return digest.FirstLine(s) }

// maxToolResultLines bounds an activity row's detail view when it isn't
// explicitly expanded (failed-row auto-expansion, high verbosity).
const maxToolResultLines = 8

// maxExpandedResultLines bounds the row a reader opened by name: the middle
// of the three depths, wide enough to read a failure whole and bounded so an
// opened row cannot cost the transcript the screen — the whole output is the
// full-screen depth past it (docs/interface/surfaces.md#the-activity-row).
const maxExpandedResultLines = maxToolResultLines * 4

// testHookRenderHistory, when non-nil, observes every full-history render.
// It exists for the resize tests: "the burst collapsed to one render" is the
// property the settle window buys, and nothing else on the surface can see a
// render that produced the same lines.
var testHookRenderHistory func()

// handleSlashCommand answers a line that starts with a command name. handled
// is false when the line is not one — an ordinary sentence, or a path that
// happens to start with a slash — and the line goes to the model instead.
func (m *Model) handleSlashCommand(text string) (handled bool, result string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false, ""
	}
	if run, ok := slashHandlers()[parts[0]]; ok {
		return true, run(m, parts)
	}
	// A lone "/word" is almost certainly a mistyped command; a path like
	// /etc/hosts contains another slash and falls through to the LLM.
	if strings.HasPrefix(parts[0], "/") && !strings.Contains(parts[0][1:], "/") {
		return true, fmt.Sprintf("Unknown command %s. Type /help for available commands.", parts[0])
	}
	return false, ""
}

// loadChatByName replaces the working conversation with a saved chat. Both
// /load <name> and the /load picker come through here.
func (m *Model) loadChatByName(name string) string {
	msgs, err := m.db.LoadChat(name)
	if err != nil {
		return "Error: " + err.Error()
	}
	// What the model was shown belongs to the conversation it was shown in.
	// Another conversation read other files, so its record would let a full
	// overwrite through on a reading this one never made (tools/seen.go).
	// Loading the slot this session is already writing is not that: it is the
	// same conversation, and it read exactly what the record says.
	if name != m.sessionName {
		tools.ForgetAll()
	}
	m.resumeConversation(name, msgs)
	// The loaded conversation brought its own system prompt, written in a
	// sitting that may be days old; the checkout in front of it is this one
	// (context.go).
	m.regenerateWorkspace()
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
