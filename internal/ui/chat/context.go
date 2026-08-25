package chat

// Context management (S-055). Phase 1 trims: before each stream request, when
// the estimated context exceeds the trim threshold, the oldest tool results
// are replaced with a short placeholder while user/assistant text is kept.
// Phase 2 compacts: /compact asks the provider for a summary of the
// conversation and restarts the message list from it.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// DefaultContextWindow is the conservative context size (in tokens) assumed
// when the pricing table doesn't know the model's real window.
const DefaultContextWindow = 32768

const (
	// trimThresholdPercent of the context window is where trimming starts
	// and where the status bar ctx indicator turns alert-colored.
	trimThresholdPercent = 80
	// warnThresholdPercent is where the ctx indicator turns warning-colored.
	warnThresholdPercent = 60
)

// elidedResult replaces trimmed tool results in the conversation.
const elidedResult = agent.ElidedResult

// compactInstruction is sent as the final user message of a /compact request.
const compactInstruction = "Summarize this conversation so it can be continued from the summary alone. " +
	"Capture the user's goals, key facts and decisions, work completed, current state, and open tasks. " +
	"Reply with only the summary text."

func estimateMessageTokens(msgs []provider.Message) int64 {
	return agent.EstimateMessageTokens(msgs)
}

// contextWindow is the model's context size from the pricing table, or
// DefaultContextWindow when unknown.
func (m Model) contextWindow() int64 {
	if m.prices != nil && m.modelName != "" {
		if w, ok := m.prices.ContextWindow(m.modelName); ok {
			return w
		}
	}
	return DefaultContextWindow
}

func (m Model) trimThreshold() int64 {
	return m.contextWindow() * trimThresholdPercent / 100
}

func (m Model) warnThreshold() int64 {
	return m.contextWindow() * warnThresholdPercent / 100
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
// estimate (S-093). Every surface reads it through contextAccounting, so the
// rails, /stats and the trim thresholds cannot quote different numbers.
func (m Model) estimatedContextTokens() int64 {
	return m.contextAccounting().total()
}

// trimContext elides the oldest tool results until the context estimate is
// back under the trim threshold, returning how many were elided. The message
// surgery itself lives with the agent's message list.
func (m *Model) trimContext() int {
	elided, _ := m.agent.TrimOldToolResults(m.estimatedContextTokens(), m.trimThreshold())
	if elided > 0 {
		// What the provider reported described the untrimmed conversation, so
		// it no longer describes anything: the accounting re-derives the size
		// from the messages that remain, and says it is estimating (S-093).
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
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
}

// startCompact asks the provider to summarize the conversation; the response
// is handled by finishCompact instead of joining the conversation.
func (m Model) startCompact() (tea.Model, tea.Cmd) {
	if len(m.agent.Messages()) <= 1 {
		m.appendEntry(entry{kind: entrySystem, text: "Nothing to compact yet."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	}
	m.compacting = true
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.appendEntry(entry{kind: entrySystem, text: "Compacting conversation…"})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	msgs := append(m.agent.RequestMessages(), provider.Message{Role: provider.RoleUser, Content: compactInstruction})
	return m, tea.Batch(m.spinner.Tick, m.requestStreamFor(msgs))
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
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	}
	rebuilt := make([]provider.Message, 0, 2)
	if msgs := m.agent.Messages(); len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		rebuilt = append(rebuilt, msgs[0])
	}
	rebuilt = append(rebuilt, provider.Message{Role: provider.RoleUser, Content: compactContextMessage(summary)})
	m.agent.SetMessages(rebuilt)
	m.agent.ResetRounds()
	// Nothing has been reported about the rebuilt conversation yet.
	m.contextTokens = 0
	// The burn series described the conversation that was just discarded.
	m.vitals.clearBurn()
	m.resetTranscript()
	// Pre-compaction checkpoints point into the discarded conversation;
	// rebuild them from what remains (S-069).
	m.checkpoints = checkpointsFromMessages(rebuilt)
	m.appendEntry(entry{kind: entrySystem, text: "Conversation compacted; continuing from this summary:"})
	m.appendEntry(entry{kind: entryAssistant, text: summary})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, m.autosaveCmd()
}

// abortCompact abandons a compaction that didn't produce a plain text
// summary (the model answered with tool calls), leaving the conversation
// unchanged.
func (m Model) abortCompact() (tea.Model, tea.Cmd) {
	m.compacting = false
	m.streaming = ""
	m.events = nil
	m.cancel = nil
	m.setTurnState(stateInput)
	m.appendEntry(entry{kind: entryError, text: "compaction failed: the model responded with tool calls; conversation unchanged"})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, nil
}

// compactContextMessage is the user-role message that carries the summary
// into the restarted conversation.
func compactContextMessage(summary string) string {
	return "Summary of the conversation so far (earlier messages were compacted):\n\n" + summary
}
