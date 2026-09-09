package chat

// Context management. Phase 1 trims: before each stream request, when
// the estimated context exceeds the trim threshold, the oldest tool results
// are replaced with a short placeholder while user/assistant text is kept.
// Phase 2 compacts: /compact asks the provider for a summary of the
// conversation and restarts the message list from it.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// DefaultContextWindow is the floor: the context size (in tokens) assumed for
// a model no endpoint, no table and no family could describe. That is a much
// smaller set than a name the table has not caught up with — the families
// cover the hosted generations and the ones a local runtime serves — so this
// is what is left for a private fine-tune under a name of its own.
const DefaultContextWindow = 32768

// Where this surface colours and acts on occupancy. The figures are the
// loop's, not this screen's: a session that trimmed at one share of its
// window and an unattended run that compacted at another would be one
// promise with two meanings.
const (
	// trimThresholdPercent is where trimming starts and where the status bar
	// ctx indicator turns alert-colored.
	trimThresholdPercent = agent.TrimThresholdPercent
	// warnThresholdPercent is where the ctx indicator turns warning-colored.
	warnThresholdPercent = agent.WarnThresholdPercent
	// trimLowWaterPercent is where a trim stops.
	trimLowWaterPercent = agent.TrimLowWaterPercent
)

// elidedResult replaces a trimmed tool result the evidence store could not
// take; one it could take carries the id that pages the original back.
const elidedResult = agent.ElidedResult

// compactSummaryEstimate is the allowance the card's recovery prediction
// makes for the summary that has not been written yet. It is the one term of
// the prediction nobody can know in advance, which is why the card says
// "about".
const compactSummaryEstimate = 1000

func estimateMessageTokens(msgs []provider.Message) int64 {
	return agent.EstimateMessageTokens(msgs)
}

// contextWindow is the model's context size: what the endpoint serving the
// model says, then the pricing table's figure, then the model family's
// published window, and DefaultContextWindow only for a model nothing
// recognises.
//
// The endpoint comes first because it is answering about the weights it
// loaded, under the id it loaded them as, which is a fact no public table can
// hold.
// See docs/capabilities/providers.md#model-data-is-fetched-and-a-snapshot-ships.
func (m Model) contextWindow() int64 {
	return m.windowFor(m.modelName)
}

// windowFor is that resolution for any model the session can name, which is
// how a rail scoped to a child agent asks about the model the child is
// running rather than the one this session is (frame.go).
func (m Model) windowFor(model string) int64 {
	if model == "" {
		return DefaultContextWindow
	}
	if m.endpointWindows != nil {
		if w, ok := m.endpointWindows(model); ok {
			return w
		}
	}
	if m.prices != nil {
		if w, ok := m.prices.ContextWindow(model); ok {
			return w
		}
	}
	if w, ok := provider.ContextWindowFor(model); ok {
		return w
	}
	return DefaultContextWindow
}

func (m Model) trimThreshold() int64 {
	return m.contextWindow() * trimThresholdPercent / 100
}

func (m Model) warnThreshold() int64 {
	return m.contextWindow() * warnThresholdPercent / 100
}

// trimLowWater is the estimate a trim runs down to once the threshold has
// been crossed.
func (m Model) trimLowWater() int64 {
	return m.contextWindow() * trimLowWaterPercent / 100
}

// contextSeverity classifies how close the context estimate is to the trim
// threshold: 0 normal, 1 approaching (warn), 2 at or over the threshold.
func (m Model) contextSeverity() int {
	tokens := m.estimatedContextTokens()
	switch {
	case tokens >= m.trimThreshold():
		return 2
	case tokens >= m.warnThreshold():
		return 1
	}
	return 0
}

// estimatedContextTokens is what the next request will carry: the provider's
// reported size when one has arrived, else the category accounting's own
// estimate. Every surface reads it through contextAccounting, so the
// rails, /stats and the trim thresholds cannot quote different numbers.
func (m Model) estimatedContextTokens() int64 {
	return m.contextAccounting().total()
}

// compactor is the shared recovery policy under this session's own figures:
// the same threshold, the same trim and the same kept tail an unattended run
// recovers by, so a session and a run nobody is watching cannot come to
// answer one question two ways.
//
// It is built where it is used rather than kept, because every figure it
// needs moves under the session — the window changes with /model and with an
// endpoint that answers late, the toolset arrives after the first frame, and
// the correction is re-learned on every response. The one thing it holds
// between calls is a bound on how often a summary may be asked for, and this
// surface already has that bound: the card and the automatic compaction are
// two answers to one crossing (pressure.go), so they share the crossing
// rather than counting two of them.
func (m Model) compactor() *agent.Compactor {
	c := &agent.Compactor{
		Window: m.contextWindow(),
		Model:  m.modelName,
		// The definitions ride the front of every request and are in no
		// message. The project context is not counted beside them: it lives
		// inside the system prompt, which the estimate walks.
		ToolTokens: m.toolDefTokens,
	}
	c.Calibrate(m.calibration)
	return c
}

// trimContext elides the oldest tool results, once the estimate has crossed
// the trim threshold, until it is back down to the low-water mark; it
// returns how many were elided. The message surgery itself lives with the
// agent's message list.
//
// The step is the shared one and it is handed no way to ask for a summary:
// what a summary costs is a request, and a request on this surface is a
// command the event loop answers rather than a wait a driver sits out. Where
// a trim cannot clear the line the round tail asks for one (recoverForRound).
func (m *Model) trimContext() int {
	before := m.estimatedContextTokens()
	// The bodies as the conversation carries them now. The surgery is in
	// place and reports only how many it did, so this is what says which:
	// a message whose content is no longer what it was here, and is a
	// placeholder, is one the trim has just taken.
	was := messageBodies(m.agent.Messages())
	n := m.compactor().RecoverFrom(m.agent, before, nil)
	if n.Elided > 0 {
		m.elideTranscript(was)
		m.signal(observe.SignalTrim, observe.TrimReason(n.Elided, n.BeforePct, n.AfterPct))
		// What the provider reported described the untrimmed conversation, so
		// it no longer describes anything: the accounting re-derives the size
		// from the messages that remain, and says it is estimating.
		m.contextTokens = 0
	}
	return n.Elided
}

// elidedRow is what a transcript row keeps once the trim has taken its body:
// everything the row read off that body, and the evidence id that pages the
// original back — empty where nothing kept it.
//
// The fields are kept rather than re-derived because the placeholder is not
// the result: re-reading it would turn a failed call into a clean one, drop
// the receipt a git write answered with, and have every row report that it
// read one line (activity.go).
type elidedRow struct {
	state    components.ActivityState
	outcome  string
	counts   string
	evidence string
}

// messageBodies is the conversation's contents, by message index.
func messageBodies(msgs []provider.Message) []string {
	bodies := make([]string, len(msgs))
	for i, msg := range msgs {
		bodies[i] = msg.Content
	}
	return bodies
}

// elideTranscript replaces the body of every transcript row whose result the
// trim has just taken out of the conversation with the placeholder the model
// was left with. was is the conversation as it stood before the trim.
//
// Without this a session holds two copies of everything it read: the trim
// shrinks the request and the transcript goes on holding the megabytes it
// elided for the life of the process, which is the half of a day-long
// session's memory nothing was watching. The row stays, because it is what
// happened, and so do its counts, which is the part of the body a reader
// scanning the feed was reading anyway. What goes is the text — and the row
// says so, and offers the original where the store took it
// (docs/capabilities/evidence.md#a-trim-makes-the-same-promise).
func (m *Model) elideTranscript(was []string) {
	// Placeholders in the order the trim wrote them, filed under the result
	// each one replaced. A result the conversation carries twice is elided
	// twice, and the transcript's copies of it are in the same order as the
	// messages', so the queue hands them out in that order too.
	byResult := map[string][]string{}
	for i, msg := range m.agent.Messages() {
		if i >= len(was) || msg.Content == was[i] {
			continue
		}
		if _, elided := agent.Elided(msg.Content); !elided {
			continue
		}
		byResult[was[i]] = append(byResult[was[i]], msg.Content)
	}
	if len(byResult) == 0 {
		return
	}
	for i := range m.transcript {
		e := &m.transcript[i]
		if e.elided != nil || (e.kind != entryTool && e.kind != entryCommand) {
			continue
		}
		queue := byResult[e.toolResult]
		if len(queue) == 0 {
			continue
		}
		byResult[e.toolResult] = queue[1:]
		id, _ := agent.Elided(queue[0])
		// What the row says about the call is read off the body while there
		// is still a body to read it off.
		was := m.activityRowDetail(*e, false)
		e.elided = &elidedRow{
			state:    was.State,
			outcome:  was.Outcome,
			counts:   activityCounts(e.toolName, e.toolResult),
			evidence: id,
		}
		e.toolResult = queue[0]
	}
	m.invalidateRenderCache()
}

// trimForRequest trims ahead of a stream request and notes it in the
// transcript when anything was elided.
func (m *Model) trimForRequest() {
	n := m.trimContext()
	if n == 0 {
		return
	}
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
		"Context trimmed: %d older tool result(s) elided.", n)})
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
}

// recoverForRound is the window recovery a round tail takes, and reports
// whether the caller should ask for a summary before its own request rather
// than send it. The trim runs here either way; the compaction it may go on to
// ask for is the caller's to start, because on this surface a request is a
// command.
//
// The trim alone was what a long turn had. A turn that crosses the line at
// its thirtieth round of a hundred and fifty has nothing left to elide long
// before it ends, and every request after that goes out oversize until the
// provider refuses one — at the worst moment there is, because everything the
// turn worked out is still only in the conversation it is about to lose. The
// card cannot help there: it is offered at a turn's end, and this turn does
// not have one yet.
// See docs/capabilities/coding-agent.md#the-window-recovers-where-nobody-is-watching.
func (m *Model) recoverForRound() bool {
	m.trimForRequest()
	// Read back off the session's own accounting rather than off what the
	// trim reported, because the trim has just made the provider's report
	// stale and this is the figure every other surface will be showing.
	if m.contextSeverity() < 2 {
		// Under the line: the next crossing is a new crossing, and gets its
		// own attempt at a summary. Both ways back under re-arm, the way the
		// card's bound does.
		m.autoCompacted = false
		return false
	}
	if m.autoCompacted || m.compacting || len(m.agent.Messages()) <= 1 {
		return false
	}
	// And the card's own guards, for the same reasons: a compaction empties
	// the transcript and reopens it on a summary, which is no smaller a
	// thing to do to a screen somebody else is using than opening a card
	// over it (pressure.go).
	if !m.screenIsFree() {
		return false
	}
	m.autoCompacted, m.compactResume = true, true
	return true
}

// screenIsFree reports whether the session is somewhere a recovery may take
// the screen: nothing borrowing it, no child lane attached or asking, and
// nothing typed at the turn waiting to go into it. It is one predicate rather
// than two lists because the card and the automatic compaction interrupt the
// same reader in the same way, and a guard added to one and not the other is
// how the two drift apart.
func (m Model) screenIsFree() bool {
	return !m.state.isSurface() && m.attachedTo == "" && m.agentList == nil &&
		m.activeChildAsk() == nil && len(m.steering) == 0
}

// compactingNotice is the line the transcript carries while the summary is
// being written. It is the one notice that comes back off the transcript: the
// receipt row answers it, and a session that kept both would be saying a
// compaction was running above a row saying it finished.
const compactingNotice = "Compacting conversation…"

// dropCompactingNotice takes that line back off, where it is still the last
// thing on the transcript. A compaction that failed leaves it: there the line
// is the account of an attempt, and the error under it says how the attempt
// went.
func (m *Model) dropCompactingNotice() {
	n := len(m.transcript)
	if n > 0 && m.transcript[n-1].kind == entrySystem && m.transcript[n-1].text == compactingNotice {
		m.transcript = m.transcript[:n-1]
	}
}

// startCompact asks the provider to summarize the conversation; the response
// is handled by finishCompact instead of joining the conversation.
func (m Model) startCompact() (tea.Model, tea.Cmd) {
	if len(m.agent.Messages()) <= 1 {
		m.appendEntry(entry{kind: entrySystem, text: "Nothing to compact yet."})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, nil
	}
	m.compacting = true
	// What the receipt will measure the act against, read here because none
	// of it survives the act: the occupancy it is about to change, and the
	// spend the summary's own request is about to add to.
	m.compactRun = &compactStart{
		at:    time.Now(),
		pct:   m.contextPercent(),
		spent: m.sessionSpend().Cost,
	}
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.appendEntry(entry{kind: entrySystem, text: compactingNotice})
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	// The request the shared step builds, under the choice it asks for: what
	// a compaction sends is one thing whichever surface asked for it.
	return m, m.requestStreamFor(m.agent.CompactRequest(), provider.ToolChoiceNone)
}

// finishCompact restarts the message list from the streamed summary: system
// prompt plus one user message carrying the summary. An empty summary leaves
// the conversation unchanged.
func (m Model) finishCompact() (tea.Model, tea.Cmd) {
	summary := strings.TrimSpace(m.streaming)
	// Read out here rather than in the builder below, which takes the model
	// by value: what the receipt is drawn from is spent by drawing it, and a
	// record left behind would be the next compaction's figures.
	started := m.compactRun
	m.compacting, m.compactRun = false, nil
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.releaseAfterCompact()
	if summary == "" {
		m.appendEntry(entry{kind: entryError, text: "compaction produced no summary; conversation unchanged"})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m.resumeAfterCompact(nil)
	}
	// The handoff a compaction writes is what a later opening of this
	// conversation is given, so it is kept beside the conversation and put on
	// the slot by the save at the end of this function (reopen.go). It is
	// stored rather than written again later: this is the summary the model
	// was actually asked for, and asking for a second one at quit would be a
	// request nobody made.
	m.compactSummary = summary
	// What survives is decided before the conversation is replaced: the turns
	// kept verbatim, and the plan's checklist, which is read off a transcript
	// that is about to be discarded.
	kept := m.compactKeep()
	run, carried := m.planRun, m.planChecklist()
	// What the turns the summary replaces were holding, measured while they
	// are still in the conversation. The system prompt is not among them: it
	// survives a compaction, so counting it here would put the one thing the
	// act cannot recover into the figure for what it did.
	dropped := m.droppedTokens(kept)
	// And the rows those turns left on screen, split from the rows the kept
	// turns left, before the transcript is torn down. The line saying a
	// compaction was running goes first: the receipt is its answer.
	m.dropCompactingNotice()
	folded, remaining, turns := m.compactSplit(kept)

	m.agent.Compact(summary, kept)
	// A compaction keeps the system prompt and replaces everything under it,
	// so the workspace block is the one thing left describing the checkout as
	// it was when the session opened rather than as it is now.
	m.regenerateWorkspace()
	// The counter the shared step leaves alone. Here the compaction is the
	// user's own request, and a request typed by the person in front of the
	// session is exactly what a fresh round budget is for. A compaction the
	// round tail asked for is not: a turn handed a fresh budget for having
	// filled its window would have no ceiling at all, which is the reason
	// agent.Agent.Compact leaves the counter where it is.
	if !m.compactResume {
		m.resetRounds()
	}
	m.signal(observe.SignalCompact, observe.CompactAsked)
	// Nothing has been reported about the rebuilt conversation yet.
	m.contextTokens = 0
	// The burn series described the conversation that was just discarded.
	m.vitals.clearBurn()
	m.resetTranscript()
	// Pre-compaction checkpoints point into the discarded conversation;
	// rebuild them from what remains.
	m.checkpoints = checkpointsFromMessages(m.agent.Messages())
	// The act on the grid, in the columns every other act is stated in: what
	// it folded, how much window that gave back, what it cost and how long it
	// took (docs/interface/principles.md#one-grid). Under it the summary the
	// model wrote in place of the turns — quoted rather than given the
	// Assistant heading a turn gets, because nobody asked for it and the
	// reply the next turn opens with is a different thing (compactBlock).
	m.appendEntry(entry{kind: entryCompactSummary, text: summary, expanded: true,
		compact: m.compactReceiptFor(started, turns, dropped)})
	// And the turns themselves, out of the window but not out of the record:
	// a compaction is about what the model remembers, and a transcript that
	// dropped the rows would be answering a question nobody asked it
	// (docs/interface/principles.md#fold-never-hide).
	m.appendEntries(folded)
	// The turns the model kept are the turns the screen keeps: a transcript
	// that lost them would say the conversation starts at the summary, and
	// the request that follows would say otherwise.
	//
	// The rows they already have, rather than rows rebuilt from their
	// messages. A turn redrawn from the conversation is prose and nothing
	// else — the calls it made, what they cost and what they found are the
	// transcript's, not the message list's — and putting every row back
	// exactly once is also what makes the split incapable of losing one,
	// whatever the boundary. A session handed a conversation it has no
	// record of gets them rebuilt, which is what every session got before.
	if len(remaining) > 0 {
		m.appendEntries(remaining)
	} else {
		m.appendMessageEntries(kept)
	}
	// The plan outlives the conversation it was being carried out in. Its
	// checklist is frozen onto the run before the transcript goes, and the
	// run is rebased on the transcript that replaces it.
	if run != nil {
		run.carryOver(carried, len(m.transcript))
		m.planRun = run
	}
	// The window is empty again, so the next alert is a new crossing.
	m.pressureShown = false
	// The compaction's own bound is deliberately not cleared here. Being
	// back under the line is what re-arms it (recoverForRound), and a
	// compaction is not proof of that: a conversation whose system prompt
	// and tool definitions are most of a small window is still over the
	// threshold with nothing left in it, and a bound cleared by the thing it
	// bounds would ask for a summary again on the very next round, and again
	// after that.
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m.resumeAfterCompact(m.autosaveCmd())
}

// releaseAfterCompact ends the turn a compaction was asked inside of — unless
// the round tail is what asked for it, in which case the turn has not ended
// and everything an arrival at the input draws is owed to the round that
// finishes it instead: the close row, the reading of how the turn came out,
// the ring the spend sparkline is drawn from, the checks a close gate owes,
// and the card offered at the threshold.
func (m *Model) releaseAfterCompact() {
	if m.compactResume {
		return
	}
	m.setTurnState(stateInput)
}

// resumeAfterCompact hands the screen back to whatever asked for the
// compaction: the input, or — where the round tail asked for it — the round
// that was waiting to send its request.
//
// A summary that did not arrive resumes the turn all the same. The turn is
// not abandoned for the recovery having failed: the request goes out against
// the conversation as it stands, and what it meets is the failure row, which
// offers the compaction again as a key somebody can press.
func (m Model) resumeAfterCompact(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if !m.compactResume {
		return m, cmd
	}
	m.compactResume = false
	next, resume := m.resumeToolLoop()
	return next, tea.Batch(cmd, resume)
}

// regenerateWorkspace puts the checkout as it stands now into the system
// prompt, replacing the workspace block the conversation was carrying.
//
// It runs where a conversation is rebuilt out of a stored message — a
// compaction, a load — and nowhere else. Those are the two moments the block
// outlives the reading it was taken from: everything else in the prompt was
// built for the session that is running, while the branch and the dirty count
// were true of a minute that may be hours gone. A conversation continuing on
// them names a branch nobody is on and disowns changes that are its own.
//
// A host that cannot survey the checkout leaves the prompt alone, which is
// what every front-end without one did before there was anything to ask.
func (m *Model) regenerateWorkspace() {
	if m.workspaceBlock == nil {
		return
	}
	msgs := m.agent.Messages()
	if len(msgs) == 0 || msgs[0].Role != provider.RoleSystem {
		return
	}
	rebuilt := project.ReplaceBlock(msgs[0].Content, m.workspaceBlock())
	if rebuilt == msgs[0].Content {
		return
	}
	// Copied rather than written through the slice the agent handed back:
	// the conversation is the agent's to hold, and a caller reaching into it
	// is a write nothing in the agent can see.
	updated := append([]provider.Message(nil), msgs...)
	updated[0].Content = rebuilt
	m.agent.SetMessages(updated)
}

// compactStart is what a compaction in flight remembers about the
// conversation it is about to replace (Model.compactRun): when the request
// went out, how full the window was, and what the session had spent. All
// three are gone by the time the summary lands.
type compactStart struct {
	at    time.Time
	pct   int
	spent float64
}

// compactReceipt is the account behind the receipt block: what a compaction
// folded out of the window, what that gave back, and what the act cost.
//
// It is stored rather than rendered, so the block re-wraps at any width like
// every other entry — which is also why the summary's own line count is not
// here: how many lines a paragraph is depends on the pane it is drawn in.
type compactReceipt struct {
	// first and last are the turns the compaction folded out of the window.
	// Both zero on a compaction that folded none, which is the floor case.
	first, last int64
	// was and now are the window's occupancy either side of the act.
	was, now int
	// tokens is what the folded turns were holding.
	tokens int64
	// cost is what the summary cost, already in the session's dollar format.
	// Empty where the model has no price, because a receipt is not the place
	// to invent one.
	cost string
	// duration is how long the summary took to arrive.
	duration time.Duration
	// floor is the row's whole target on a compaction that could fold
	// nothing: what it freed, and what is left that no summary can stand in
	// for. Empty on a compaction that folded something, which is nearly all
	// of them.
	floor string
}

// turns is how many turns the receipt folded away.
func (r compactReceipt) turns() int {
	if r.first == 0 {
		return 0
	}
	return int(r.last - r.first + 1)
}

// account is the receipt row's right-aligned field: where the window stood
// before the act and where it stands after, and what the summary cost.
func (r compactReceipt) account() string {
	acct := fmt.Sprintf("ctx %d%% → %d%%", r.was, r.now)
	if r.cost == "" {
		return acct
	}
	return acct + " · " + r.cost
}

// contextPercent is how full the window is, as the share every occupancy
// surface states it in. Zero for a session with no window to measure
// against, which is the one case where the figure would be invented.
func (m Model) contextPercent() int {
	window := m.contextWindow()
	if window <= 0 {
		return 0
	}
	return int(min(m.estimatedContextTokens()*100/window, 100))
}

// droppedTokens is what the conversation loses to a compaction: everything
// under the system prompt, less the tail kept verbatim. It is measured
// against the messages rather than against the window, because the window
// also carries the tool definitions and the project context and a compaction
// touches neither — counting them would put what the act cannot recover into
// the figure for what it did.
func (m Model) droppedTokens(kept []provider.Message) int64 {
	msgs := m.agent.Messages()
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		msgs = msgs[1:]
	}
	return max(estimateMessageTokens(msgs)-estimateMessageTokens(kept), 0)
}

// compactSplit divides the transcript at the first of the turns the
// compaction keeps: the rows going out of the window, and the rows staying
// in it.
//
// The boundary is counted in the reader's own units — a turn starts at the
// row carrying what they typed — because the kept tail arrives as a list of
// messages and the transcript is the only place those turns have rows. A
// transcript holding fewer of them than the tail claims folds nothing, which
// is the honest answer rather than a boundary guessed at.
//
// Rows a previous compaction already took out of the window are above the
// boundary too, and they are not folded again: a session that compacts twice
// over the same turns has recovered nothing the second time, and that is what
// the floor case is.
func (m Model) compactSplit(kept []provider.Message) (folded, remaining []entry, turns compactTurns) {
	left, end := keptReaderTurns(kept), len(m.transcript)
	for end > 0 && left > 0 {
		end--
		if m.transcript[end].kind == entryUser {
			left--
		}
	}
	if left > 0 {
		end = 0
	}
	folded = make([]entry, end)
	copy(folded, m.transcript[:end])
	// Numbered before the marking, because what says a turn went out of the
	// window earlier is the mark the loop below is about to write over
	// everything.
	was, above := turnsIn(folded)
	_, all := turnsIn(m.transcript)
	turns = compactTurns{keptFirst: above + 1, keptLast: all}
	if above > was {
		turns.first, turns.last = was+1, above
	}
	for i := range folded {
		folded[i].outOfWindow = true
	}
	// Both halves are copies. The transcript they came from is torn down
	// between this call and the appends that put them back, and a slice still
	// pointing into it would be reading an array the rebuild is writing over.
	remaining = append(remaining, m.transcript[end:]...)
	return folded, remaining, turns
}

// compactTurns is how a compaction numbers the conversation it acted on: the
// turns this one took out of the window, and the turns still in it. A
// compaction whose whole foldable half had already been folded takes none,
// and first and last stay zero — the floor case.
type compactTurns struct {
	first, last         int64
	keptFirst, keptLast int64
}

// keptReaderTurns counts the turns in a kept tail that the transcript has a
// user row for: the messages the reader typed, and only those.
//
// It is not CompactKeptTurns, which counts every user-role message. A message
// the session wrote for itself — a check-in, a steer, the context a `!!` run
// hands the model — is user-role on the wire and a notice on the screen
// (newsession.go), so counting it here would walk the boundary one real turn
// too far back. The rows between the two boundaries belong to a turn the
// summary has replaced and the kept tail does not carry, which is a turn the
// split would hold on neither side of itself
// (docs/interface/principles.md#fold-never-hide).
func keptReaderTurns(kept []provider.Message) int {
	n := 0
	for _, msg := range kept {
		if msg.Role == provider.RoleUser && !msg.Machine {
			n++
		}
	}
	return n
}

// turnsIn counts the turns a run of entries holds — one per row carrying what
// the reader typed — and how many of those a compaction has already folded.
//
// Turns are counted off the rows rather than read off the session's own
// counter, because the counter belongs to the session and the numbering the
// receipt states belongs to the transcript in front of the reader: a
// conversation loaded from a record has rows the counter never saw.
func turnsIn(es []entry) (folded, total int64) {
	for _, e := range es {
		if e.kind != entryUser {
			continue
		}
		total++
		if e.outOfWindow {
			folded++
		}
	}
	return folded, total
}

// turnsPhrase names a range of turns the way the receipt row and the fold
// line under it both name it, so the two cannot disagree about which turns
// they are talking about.
func turnsPhrase(first, last int64) string {
	if first == last {
		return fmt.Sprintf("turn %d", first)
	}
	return fmt.Sprintf("turns %d–%d", first, last)
}

// compactReceiptFor is the account the receipt block is drawn from: what the
// conversation was when the request went out (started), what it is now, the
// turns that went in between, and what the request cost.
func (m Model) compactReceiptFor(started *compactStart, turns compactTurns, dropped int64) *compactReceipt {
	r := &compactReceipt{now: m.contextPercent(), tokens: dropped, first: turns.first, last: turns.last}
	if started != nil {
		r.was, r.duration = started.pct, time.Since(started.at)
		if spent := m.sessionSpend().Cost - started.spent; spent > 0 {
			r.cost = formatCost(spent)
		}
	}
	if r.first == 0 {
		r.floor = m.compactFloor(turns, r.was, r.now)
	}
	return r
}

// compactFloor is what the row says when the summary replaced nothing: how
// much window that freed, and what is still in it that no summary can stand
// in for. It is stated as a break rather than as a quiet success — the act
// was asked to recover a window and did not — and it names the reader's own
// things, because those are what they can act on.
func (m Model) compactFloor(turns compactTurns, was, now int) string {
	freed := fmt.Sprintf("freed %d%%", max(was-now, 0))
	var parts []string
	if m.planRun != nil {
		parts = append(parts, "the plan")
	}
	if files, _, _ := m.changes.Totals(); files > 0 {
		parts = append(parts, "the changeset")
	}
	if turns.keptLast >= turns.keptFirst {
		parts = append(parts, turnsPhrase(turns.keptFirst, turns.keptLast))
	}
	if len(parts) == 0 {
		return freed + " · there was nothing left to fold"
	}
	return freed + " · what remains is " + joinClauses(parts) +
		" — none of it foldable"
}

// compactVerb is the receipt row's verb, out of the same closed vocabulary
// every other row's verb comes from.
const compactVerb = "compact"

// compactRowFor is the receipt as an activity row: the act the session took
// on its own conversation, in the seven fields every other act is stated in
// (docs/interface/surfaces.md#the-activity-row). It carries no kind glyph,
// because a compaction is not a call — the column says how it came out
// instead — and no mutation rail, because nothing on the machine was touched.
func compactRowFor(r compactReceipt) components.ActivityRow {
	row := components.ActivityRow{
		Kind:     components.ActivityCompaction,
		Verb:     compactVerb,
		Duration: activityDuration(r.duration),
	}
	if r.floor != "" {
		// The whole of what happened goes in the one field that clips: the
		// figure first, because that is the part a narrow pane has to keep,
		// and the explanation behind it.
		row.State, row.Target = components.ActivityFailed, r.floor
		return row
	}
	row.Target = "folded " + turnsPhrase(r.first, r.last)
	row.Allowed = r.account()
	return row
}

// compactBlock draws the receipt: the act as a row, the fold line that counts
// what it holds, and — while the fold is open — the summary the model wrote
// in place of the turns.
//
// One block rather than three entries, because it is one act and reading mode
// puts one cursor on it: [enter] on the row folds the summary away and gives
// it back, the way [enter] folds every other body on the transcript.
func (m Model) compactBlock(e entry, width int) string {
	r := e.compact
	if r == nil {
		// A summary with no receipt behind it came out of a record written
		// before receipts were rows. It keeps the quoted paragraph it always
		// had rather than a row invented for it.
		return m.compactSummaryBlock(e, width)
	}
	lines := []string{compactRowFor(*r).View(width)}
	if r.floor != "" {
		// Nothing was folded, so there is nothing to fold back: the row is
		// the whole of the receipt.
		return strings.Join(lines, "\n")
	}
	body := m.compactSummaryLines(e.text, width)
	lines = append(lines, m.compactFoldLine(*r, len(body), e.expanded, width))
	if !e.expanded {
		return strings.Join(lines, "\n")
	}
	indent := strings.Repeat(" ", components.GridDetailIndent)
	shown := body
	if len(shown) > maxToolResultLines {
		shown = shown[:maxToolResultLines]
	}
	for _, l := range shown {
		lines = append(lines, indent+sty.CompactSummary.Render(l))
	}
	if tail := compactSummaryTail(len(body)-len(shown), r.turns()); tail != "" {
		// Wrapped rather than clipped, for the reason the summary above it is:
		// it is a sentence, and half a sentence about where the turns went is
		// worse than a foot that costs two lines.
		for _, l := range strings.Split(m.wordWrap(tail, max(width-components.GridDetailIndent, 1)), "\n") {
			lines = append(lines, indent+sty.SystemMsg.Render(l))
		}
	}
	return strings.Join(lines, "\n")
}

// compactSummaryLines is the summary wrapped to the detail body's width. It
// is what the fold line counts, so the number on that line is the number of
// lines opening the fold costs.
func (m Model) compactSummaryLines(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	inner := max(width-components.GridDetailIndent, 1)
	return strings.Split(m.wordWrap(text, inner), "\n")
}

// compactFoldLine is the line under the receipt: which turns are behind it,
// what they were holding, what stands in for them now, and the key that
// closes it again. It sits on the grid a field short, the way the folded run
// of read-only calls does — the fold mark takes the glyph column and what was
// swallowed starts in the verb column — so the two fold rows line up
// (docs/interface/principles.md#fold-never-hide).
func (m Model) compactFoldLine(r compactReceipt, summaryLines int, open bool, width int) string {
	mark, label := "▸", "read the summary"
	if open {
		mark, label = "▾", "fold it back up"
	}
	// Which turns, and that they were compacted: the fold's own identity, and
	// the one part of the line that is never given up.
	const sep = " · "
	held := mark + " " + turnsPhrase(r.first, r.last) + sep + "compacted"
	// What they were holding and what stands in for it. This is the clause
	// that goes when the pane is tight — the turn range above already says
	// what the fold swallowed, and a size cut down to `a 7-line su…` says
	// less than no size at all (guidelines/layout-breakpoints: the word goes
	// rather than being cut down).
	var size string
	if r.tokens > 0 && summaryLines > 0 {
		size = sep + formatWindowSize(r.tokens) + " tokens → " + summaryLength(summaryLines)
	}
	key := keys.Bracket(keys.Reading.Expand) + " " + label
	lead := strings.Repeat(" ", components.GridVerbColumn-2)
	room := width - lipgloss.Width(lead)
	// Widest first: everything, then without the size, then without the offer
	// as well. The size is what goes, because the turn range in front of it
	// is already what the fold swallowed and this is only how big it was; the
	// offer stays, because a fold row that does not say how to open it is a
	// row the reader has to guess at.
	for _, dim := range []string{held + size, held} {
		if lipgloss.Width(dim)+lipgloss.Width(sep+key) <= room {
			return lead + sty.SystemMsg.Render(dim+sep) + sty.Hint.Key.Render(key)
		}
	}
	return components.Clip(lead+sty.SystemMsg.Render(held), width)
}

// summaryLength is what the fold line says stands in for the turns: a
// paragraph the reader can price in lines before they open it.
func summaryLength(n int) string {
	if n == 1 {
		return "a 1-line summary"
	}
	return fmt.Sprintf("a %d-line summary", n)
}

// compactSummaryTail is the foot under a bounded summary: what the cap
// swallowed, and where the turns themselves went. A fold states what it holds
// (docs/interface/principles.md#fold-never-hide), and a reader who cannot see
// the turns has to be told they are still on the transcript rather than left
// to assume a compaction threw them away.
func compactSummaryTail(more, turns int) string {
	var parts []string
	switch {
	case more == 1:
		parts = append(parts, "1 more line")
	case more > 1:
		parts = append(parts, fmt.Sprintf("%d more lines", more))
	}
	switch {
	case turns == 1:
		parts = append(parts, "the turn itself is below, out of the window but still in the transcript")
	case turns > 1:
		parts = append(parts, fmt.Sprintf(
			"the %d turns themselves are below, out of the window but still in the transcript", turns))
	}
	if len(parts) == 0 {
		return ""
	}
	return "· " + strings.Join(parts, " · ") + " ·"
}

// compactSummaryBlock draws a summary with no receipt behind it — one out of
// a record written before receipts were rows — wrapped onto the detail indent
// every other body under a row uses, and rendered in Dimmer italic.
//
// The slant is the point, and it is the only one on the screen. A reader
// scanning back past a compaction needs to know that the paragraph they are
// reading is the model's account of a conversation rather than the
// conversation, and every other way of saying so — a heading, a colour, a
// glyph — is already spent on something else. So italic means quoted model
// output here and nowhere else in the product's own chrome.
//
// Wrapped rather than clipped, for the reason a notice's body is: this is
// prose, and a summary cut off at the right margin is a summary the reader
// has to go somewhere else to finish.
func (m Model) compactSummaryBlock(e entry, width int) string {
	text := strings.TrimSpace(e.text)
	if text == "" {
		return ""
	}
	indent := strings.Repeat(" ", components.GridDetailIndent)
	inner := max(width-components.GridDetailIndent, 1)
	lines := strings.Split(m.wordWrap(text, inner), "\n")
	for i, l := range lines {
		lines[i] = indent + sty.CompactSummary.Render(l)
	}
	return strings.Join(lines, "\n")
}

// compactKeep is the tail a compaction carries through verbatim, under this
// session's window and its own correction factor. The card reads it too, to
// promise what compacting would keep before anything has been compacted.
func (m Model) compactKeep() []provider.Message {
	return m.agent.CompactKeep(m.contextWindow()*agent.CompactKeepPercent/100, m.calibration)
}

// keptTurnCount counts the user messages in a kept tail — the turns it is.
func (m Model) keptTurnCount(kept []provider.Message) int {
	return agent.CompactKeptTurns(kept)
}

// abortCompact abandons a compaction that answered with tool calls, leaving
// the conversation unchanged.
//
// It is a backstop, not the way a compaction usually ends: the request
// forbids a tool call outright (startCompact), so reaching here means a
// provider that did not honour that, and the wording says so rather than
// describing a model doing something reasonable.
func (m Model) abortCompact() (tea.Model, tea.Cmd) {
	m.compacting, m.compactRun = false, nil
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.appendEntry(entry{kind: entryError, text: "compaction failed: the model called a tool on a request that forbade one; conversation unchanged"})
	m.releaseAfterCompact()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m.resumeAfterCompact(nil)
}

// compactContextPrefix opens that message. Input recall reads it: a resumed
// session seeds its history from the user-role messages it loads, and this is
// one of the three that nobody typed (recall.go).
const compactContextPrefix = agent.CompactSummaryPrefix

// compactContextMessage is the user-role message that carries the summary
// into the restarted conversation.
func compactContextMessage(summary string) string {
	return agent.CompactSummaryMessage(summary)
}
