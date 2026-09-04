package chat

// The stream: asking for a reply, reading it as it arrives, and closing it.
//
// A request is a command; the events come back on a channel and are drained
// into the messages the turn route answers (turn.go). Cancelling is the same
// path with the context cut, which is why what a cancelled turn keeps is
// decided here and not at the key that cancelled it.

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

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
	// And a hold asked mid-turn is honoured beside it, for the same reason
	// the ceiling is checked here: this is the one moment the turn is between
	// rounds, with the round's results already in the conversation and
	// nothing yet asked of the model, so parking costs nothing and owes
	// nothing (hold.go).
	if m.holdAsked {
		return m.holdTurn()
	}
	// A long turn is asked what it has got, long before the cap — by a drift
	// verdict where there is one, by the clock otherwise (steer.go). Steering
	// injected above answers the same question and resets the counter both
	// are measured against, so a turn the reader has just steered is not
	// asked it twice.
	// The tree first, then the question: a check-in asked against a tree the
	// turn has not been told about is answered against the wrong one.
	m.injectTreeNotice(false)
	m.injectInterventions()
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
	if m.policy.mode == agent.ModePlan && len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		msgs[0].Content += "\n\n" + prompt.PlanModeInstructions
	}
	return m.requestStreamFor(msgs, provider.ToolChoiceAuto)
}

// requestStreamFor starts a stream over an explicit message list (callers
// pass a copy so in-flight requests are immune to later mutation) under an
// explicit tool choice.
func (m Model) requestStreamFor(msgs []provider.Message, choice string) tea.Cmd {
	a := m.agent
	return func() tea.Msg {
		events, cancel, err := a.StreamWithChoice(msgs, choice)
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
	if ev.ToolCallDelta != nil {
		// A fragment of a tool call's arguments. It ends the token batch
		// rather than draining into it: what it feeds is a row of its own,
		// and the two never have to be told apart after the fact because
		// they never share a field (activity.go).
		//
		// It is read before the terminal signals below because every parser
		// sends a fragment on an event of its own and puts nothing else on
		// it. A dialect that ever rode a fragment on the event that ended
		// its round would lose the end of the round here, not the fragment.
		return toolDeltaMsg{delta: *ev.ToolCallDelta}
	}
	if ev.Err != nil {
		// The completed tool calls ride the failure: a stream that
		// broke after the model finished writing a call kept that call.
		return streamErrMsg{err: ev.Err, calls: ev.ToolCalls, reasoning: ev.Reasoning}
	}
	if len(ev.ToolCalls) > 0 {
		return toolCallsMsg{calls: ev.ToolCalls, usage: ev.Usage, reasoning: ev.Reasoning, stop: ev.Stop}
	}
	if ev.Done {
		return doneMsg{usage: ev.Usage, stop: ev.Stop}
	}
	return nil
}

// accumulateUsage folds one request's usage into the session vitals and
// reads the running totals back out, so the rail, the cockpit and /stats all
// quote the same numbers from one place.
func (m *Model) accumulateUsage(u *provider.Usage) {
	if u == nil {
		return
	}
	// The estimator is measured here because here is the one moment the two
	// figures describe the same messages: the response this usage belongs to
	// has not joined the conversation yet, so what the accounting counts is
	// the list the request was built from. What a request puts on top of that
	// list — the compaction instruction, the plan-mode preamble — is a few
	// dozen tokens against a whole conversation, and no single round can move
	// the factor far in any case.
	m.calibration.Observe(m.modelName, int64(u.PromptTokens), m.contextEstimate().total())
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
	// Before anything else, because this is what stops the close from being
	// drawn while a suite is still running: the row it leaves is what the
	// close block reads its verdict off (gate.go).
	m.cancelCloseGate()
	if m.todoRunner.state != nil && !m.todoRunner.state.Over() {
		m.todoRunner.cancelled = true
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
	// Follow-ups survive the cancel but are not sent after one: the queue
	// was written against work that was just abandoned, so the rail marks
	// it held and the reader decides what still applies (followup.go).
	m.holdFollowUps()
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
	m.signal(observe.SignalSteer, strconv.Itoa(len(m.steering)))
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
	// A turn that went to its checks rather than to the input is not over,
	// and what was typed at it belongs after the verdict rather than to a
	// turn of its own: sending it here would take the turn away from the run
	// it is waiting on, and the checks that turn owes would never happen.
	// settleCloseGate asks again once the close has been drawn, and
	// resumeToolLoop injects it where a hand-back continues the turn
	// (gate.go).
	if m.turnState() == stateCloseGate {
		return nil
	}
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
