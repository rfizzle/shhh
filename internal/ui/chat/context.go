package chat

// Context management. Phase 1 trims: before each stream request, when
// the estimated context exceeds the trim threshold, the oldest tool results
// are replaced with a short placeholder while user/assistant text is kept.
// Phase 2 compacts: /compact asks the provider for a summary of the
// conversation and restarts the message list from it.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
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
	if m.modelName == "" {
		return DefaultContextWindow
	}
	if m.endpointWindows != nil {
		if w, ok := m.endpointWindows(m.modelName); ok {
			return w
		}
	}
	if m.prices != nil {
		if w, ok := m.prices.ContextWindow(m.modelName); ok {
			return w
		}
	}
	if w, ok := provider.ContextWindowFor(m.modelName); ok {
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
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.appendEntry(entry{kind: entrySystem, text: "Compacting conversation…"})
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
	m.compacting = false
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
	m.appendEntry(entry{kind: entrySystem, text: compactedNotice(len(kept) > 0, m.keptTurnCount(kept))})
	// Quoted under that receipt rather than given the Assistant heading a
	// turn gets. The model wrote this, but it is not a turn in the
	// conversation: nothing was asked, and the reply the next turn opens with
	// is a different thing. So it reads as what it is — the model's words,
	// quoted (compactSummaryBlock).
	m.appendEntry(entry{kind: entryCompactSummary, text: summary})
	// The turns the model kept are the turns the screen keeps: a transcript
	// that lost them would say the conversation starts at the summary, and
	// the request that follows would say otherwise.
	m.appendMessageEntries(kept)
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

// compactedNotice is the line that opens the rebuilt conversation. It names
// the kept turns because the transcript below it is otherwise indisting-
// uishable from a session that started at the summary.
func compactedNotice(kept bool, turns int) string {
	if !kept || turns <= 0 {
		return "Conversation compacted; continuing from this summary:"
	}
	return fmt.Sprintf("Conversation compacted; continuing from this summary and the last %s:",
		plural(turns, "turn"))
}

// compactSummaryBlock draws the summary under the receipt row that announced
// it: wrapped onto the detail indent every other body under a row uses, and
// rendered in Dimmer italic.
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
	m.compacting = false
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
