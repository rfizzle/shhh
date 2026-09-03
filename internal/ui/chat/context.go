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
	"github.com/rfizzle/shhh/internal/provider"
)

// DefaultContextWindow is the floor: the context size (in tokens) assumed for
// a model no endpoint, no table and no family could describe. That is a much
// smaller set than a name the table has not caught up with — the families
// cover the hosted generations and the ones a local runtime serves — so this
// is what is left for a private fine-tune under a name of its own.
const DefaultContextWindow = 32768

const (
	// trimThresholdPercent of the context window is where trimming starts
	// and where the status bar ctx indicator turns alert-colored.
	trimThresholdPercent = 80
	// warnThresholdPercent is where the ctx indicator turns warning-colored.
	warnThresholdPercent = 60
	// trimLowWaterPercent is where a trim stops. It is the warn line rather
	// than a figure of its own because that is already the place this
	// interface calls "filling up but not yet a problem", and a trim has no
	// reason to invent a second one.
	//
	// It is far below the trigger on purpose. A trim that stopped as soon as
	// it was under the threshold would clear the line by a few hundred
	// tokens, cross it again a round later, and pay the price of a trim
	// again — and that price is the whole prompt prefix the provider was
	// caching, because eliding a message invalidates the cache from that
	// message on. Stopping here spends one invalidation on a fifth of the
	// window of headroom.
	// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
	trimLowWaterPercent = warnThresholdPercent
)

// elidedResult replaces trimmed tool results in the conversation.
const elidedResult = agent.ElidedResult

// What a compaction keeps besides the summary. The pressure card
// promises the most recent turns survive it, so they have to: a summary is a
// description of a conversation, and the turn you are in the middle of is the
// one place a description is not good enough.
const (
	// compactKeepTurns is how many of the most recent user turns are carried
	// through verbatim.
	compactKeepTurns = 2
	// compactKeepPercent bounds what those turns may occupy of the window. A
	// single turn that read half the repository is not a tail, and keeping it
	// would compact the conversation into the same corner it started in.
	compactKeepPercent = 15
	// compactSummaryEstimate is the allowance the recovery prediction makes
	// for the summary that has not been written yet. It is the one term of
	// the prediction nobody can know in advance, which is why the card says
	// "about".
	compactSummaryEstimate = 1000
)

// compactInstruction is sent as the final user message of a /compact request.
const compactInstruction = "Summarize this conversation so it can be continued from the summary alone. " +
	"Capture the user's goals, key facts and decisions, work completed, current state, and open tasks. " +
	"Reply with only the summary text."

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
	elided, after := m.agent.TrimOldToolResults(before, m.trimThreshold(), m.trimLowWater())
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
	msgs := append(m.agent.RequestMessages(), provider.Message{Role: provider.RoleUser, Content: compactInstruction})
	// A summary is prose, and the request says so rather than asking nicely
	// and hoping: the instruction just appended sits under a whole session's
	// worth of tool results, and a model reading it as one more turn answers
	// with the tool call the turn was about to make.
	//
	// The tools stay on the request. They are the head of the prefix the
	// provider caches — in front of the system prompt, in the one dialect
	// that is told about the prefix at all — so a request that dropped them
	// to prevent a tool call would rebuild the whole head to save a retry,
	// and the marks that name that head would have nothing to point at.
	// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
	return m, m.requestStreamFor(msgs, provider.ToolChoiceNone)
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

	rebuilt := make([]provider.Message, 0, 2+len(kept))
	if msgs := m.agent.Messages(); len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		rebuilt = append(rebuilt, msgs[0])
	}
	rebuilt = append(rebuilt, provider.Message{Role: provider.RoleUser, Content: compactContextMessage(summary)})
	rebuilt = append(rebuilt, kept...)
	m.agent.SetMessages(rebuilt)
	m.resetRounds()
	// Nothing has been reported about the rebuilt conversation yet.
	m.contextTokens = 0
	// The burn series described the conversation that was just discarded.
	m.vitals.clearBurn()
	m.resetTranscript()
	// Pre-compaction checkpoints point into the discarded conversation;
	// rebuild them from what remains.
	m.checkpoints = checkpointsFromMessages(rebuilt)
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

// compactKeep is the tail of the conversation a compaction carries through
// verbatim: whole turns, most recent first, bounded by compactKeepTurns and
// by compactKeepPercent of the window.
//
// The boundary is always a user message, which is what keeps the result
// well-formed — a tail that started inside a tool round would open with
// results for calls the model could no longer see it had made. A tail that
// would be the whole conversation is no tail at all: there would be nothing
// left for the summary to have summarized.
func (m Model) compactKeep() []provider.Message {
	msgs := m.agent.Messages()
	first := 0
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		first = 1
	}
	var starts []int
	for i := first; i < len(msgs); i++ {
		if msgs[i].Role == provider.RoleUser {
			starts = append(starts, i)
		}
	}
	budget := m.contextWindow() * compactKeepPercent / 100
	at := -1
	for n := 1; n <= compactKeepTurns && n <= len(starts); n++ {
		start := starts[len(starts)-n]
		if start <= first {
			break
		}
		if estimateMessageTokens(msgs[start:]) > budget {
			break
		}
		at = start
	}
	if at < 0 {
		return nil
	}
	return append([]provider.Message(nil), msgs[at:]...)
}

// keptTurnCount counts the user messages in a kept tail — the turns it is.
func (m Model) keptTurnCount(kept []provider.Message) int {
	n := 0
	for _, msg := range kept {
		if msg.Role == provider.RoleUser {
			n++
		}
	}
	return n
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

// compactContextPrefix opens that message. It is a constant because input
// recall reads it: a resumed session seeds its history from the user-role
// messages it loads, and this is one of the three that nobody typed
// (recall.go).
const compactContextPrefix = "Summary of the conversation so far (earlier messages were compacted):"

// compactContextMessage is the user-role message that carries the summary
// into the restarted conversation.
func compactContextMessage(summary string) string {
	return compactContextPrefix + "\n\n" + summary
}
