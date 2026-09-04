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

// trimContext elides the oldest tool results, once the estimate has crossed
// the trim threshold, until it is back down to the low-water mark; it
// returns how many were elided. The message surgery itself lives with the
// agent's message list.
func (m *Model) trimContext() int {
	before := m.estimatedContextTokens()
	elided, after := m.agent.TrimOldToolResults(before, m.trimThreshold(), m.trimLowWater(), m.calibration)
	if elided > 0 {
		window := m.contextWindow()
		m.signal(observe.SignalTrim, observe.TrimReason(elided,
			percentOf(before, window), percentOf(after, window)))
		// What the provider reported described the untrimmed conversation, so
		// it no longer describes anything: the accounting re-derives the size
		// from the messages that remain, and says it is estimating.
		m.contextTokens = 0
	}
	return elided
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
	m.setTurnState(stateInput)
	if summary == "" {
		m.appendEntry(entry{kind: entryError, text: "compaction produced no summary; conversation unchanged"})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, nil
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
	// session is exactly what a fresh round budget is for.
	m.resetRounds()
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
	m.appendEntry(entry{kind: entryAssistant, text: summary})
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
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.autosaveCmd()
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
	m.setTurnState(stateInput)
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
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
