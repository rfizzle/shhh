package chat

// The stream: asking for a reply, reading it as it arrives, and closing it.
//
// A request is a command; the events come back on a channel and are drained
// into the messages the turn route answers (turn.go). Both go through the
// backend (backend.go). Cancelling is the same
// path with the context cut, which is why what a cancelled turn keeps is
// decided here and not at the key that cancelled it.

import (
	"context"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// resumeToolLoop requests the next model response after a round of tool
// results, through the round tail every driver of the loop shares
// (agent.RoundTail) — unless the turn has hit the tool-round cap, in which
// case it pauses on the checkpoint that says what it managed and offers the
// ways on (a fresh message still continues the conversation and resets the
// counter), or a hold asked mid-turn parks it, or the window wants a summary
// first.
func (m Model) resumeToolLoop() (tea.Model, tea.Cmd) {
	switch step, _ := m.roundTail().Next(); step {
	case agent.StepHeld:
		return m.holdTurn()
	case agent.StepCapped:
		return m.pauseAtRoundLimit()
	case agent.StepCompact:
		// Where eliding could not clear the line the summary goes first and
		// this request follows it (context.go).
		return m.startCompact()
	}
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.syncViewport()
	return m, m.requestStream()
}

// parked is the release a screen's hold hands the round tail. The screen
// parks by returning to its own loop rather than by waiting, so nothing
// waits on it and nothing closes it: the release is the key (hold.go).
var parked = make(chan struct{})

// roundTail is the round tail over the screen's seams. It is built where it
// is used, on the copy of the Model the caller is about to return, because
// the seams write that copy: built once and stored, a steer's turn number
// would land on a Model nobody keeps.
func (m *Model) roundTail() agent.RoundTail {
	return agent.RoundTail{
		Agent: m.agent,
		// A hold asked mid-turn is honoured here: this is the one moment the
		// turn is between rounds, with the round's results already in the
		// conversation and nothing yet asked of the model, so parking costs
		// nothing and owes nothing (hold.go). A turn at its ceiling is
		// stopped by the checkpoint instead, and that stop answers the hold
		// (rounds.go).
		Hold: func() <-chan struct{} {
			if !m.holdAsked || m.agent.CapReached() {
				return nil
			}
			return parked
		},
		// Steering messages queued mid-turn join the conversation here,
		// between tool rounds, so the model sees them on its next request.
		// They count as fresh user input, so they also lift a hit round cap.
		Steer: func() bool {
			queued, fresh := m.injectSteering()
			if queued {
				m.viewport.SetLines(m.renderHistoryLines())
				m.viewport.GotoBottom()
			}
			return fresh
		},
		Target:  func() string { return m.summaryTarget },
		Recover: m.recoverForRound,
		// An overlay belongs to the reader, so the status request waits for
		// a boundary with nothing on the screen rather than going out
		// unseen behind a card or a supporting screen (progress.go).
		HoldProgress: func() bool { return m.state.isSurface() },
		OnTree:       m.showTreeNotice,
		OnIntervene:  m.showIntervention,
	}
}

func (m Model) requestStream() tea.Cmd {
	msgs := m.agent.RequestMessages()
	// The read-only modes inject their instructions into the request's system
	// prompt; the stored conversation stays untouched, so leaving the mode
	// stops the injection. The two blocks differ because the modes do: plan
	// mode asks for a plan, and read-only mode says only what the bound is,
	// which is the whole of the difference between them.
	if block := agent.ModeInstructions(m.policy.mode, m.policy.readOnlyExtra, m.toolNames()); block != "" && len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		msgs[0].Content += "\n\n" + block
	}
	return m.requestStreamFor(msgs, provider.ToolChoiceAuto)
}

// toolNames is the registered toolset by name, which the mode paragraph names
// a reader from only where the session holds it.
func (m Model) toolNames() []string {
	names := make([]string, 0, len(m.toolDefs))
	for _, d := range m.toolDefs {
		names = append(names, d.Name)
	}
	return names
}

// requestStreamFor starts a stream over an explicit message list (callers
// pass a copy so in-flight requests are immune to later mutation) under an
// explicit tool choice. It asks the backend rather than the loop: the stream
// is the first thing the screen reaches through it (backend.go).
func (m Model) requestStreamFor(msgs []provider.Message, choice string) tea.Cmd {
	return m.backend.Request(msgs, choice)
}

// waitForNext reads the open stream's next batch through the backend that
// opened it.
func (m Model) waitForNext() tea.Cmd { return m.backend.Next(m.events) }

// execToolsCmd dispatches an auto-run batch off the UI goroutine. What the
// batch is running is not stamped anywhere for the frame to read: the calls
// are rows in the feed while they run, and the rail says only that the turn
// is in its running phase (turnstatus.go).
//
// The batch runs under a context of its own whose cancel the model holds, so
// a stop reaches a call that is still running — a search over a large tree,
// a query, a page slow to answer — rather than leaving it to run out
// (docs/capabilities/coding-agent.md#a-tool-stops-with-the-turn).
func (m *Model) execToolsCmd(calls []provider.ToolCall) tea.Cmd {
	a := m.agent
	runID := a.RunID()
	m.endToolRound()
	ctx, cancel := context.WithCancel(context.Background())
	m.toolCancel = cancel
	return func() tea.Msg {
		return toolResultsMsg{runID: runID, results: a.ExecuteCalls(ctx, calls)}
	}
}

// endToolRound stops the round of auto-run calls, if one is running, and
// lets go of its cancel. A round that finished is ended the same way, which
// releases what its context holds.
func (m *Model) endToolRound() {
	if m.toolCancel != nil {
		m.toolCancel()
		m.toolCancel = nil
	}
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
	reported, estimated := int64(u.PromptTokens), m.contextEstimate().total()
	m.calibration.Observe(m.modelName, reported, estimated)
	cost, priced := m.usageCost(*u)
	m.vitals.record(m.modelName, *u, cost, priced)
	m.noteCacheExpiries()
	m.TotalTokensIn, m.TotalTokensOut = m.vitals.totalIn, m.vitals.totalOut
	m.turnTokensIn, m.turnTokensOut = m.vitals.current.In, m.vitals.current.Out
	// The report is anchored to the messages it described. Those are the
	// messages the request was built from: the round's answer and its tool
	// results have not joined the list yet, so the list length goes down with
	// the count and the accounting estimates whatever arrives after it. The
	// prompt alone is the anchor, not prompt plus completion — the completion
	// becomes a message a moment later, and the estimate counts it then.
	//
	// A count too small to be these messages is billed and not anchored: the
	// occupancy stays on the estimate, or on the report before it, rather
	// than falling from half full to empty between two frames of one turn.
	// See docs/capabilities/providers.md#how-full-the-window-is-corrected-by-what-it-cost.
	if !agent.ReportUndercounts(reported, estimated) {
		m.contextTokens, m.contextReportedAt = m.vitals.lastIn, len(m.agent.Messages())
	}
	m.notifyUsage()
}

func (m *Model) finishStreaming() {
	// Whatever repaint the arriving message still owed, it does not owe it any
	// more: the message is about to be an entry like every other.
	m.streamDirty = false
	if m.compacting {
		// A cancelled compaction discards the partial summary and keeps the
		// conversation unchanged (the success path goes through
		// finishCompact). The turn it was recovering the window for is
		// cancelled with it: what the reader stopped is the turn, and
		// resuming the round they interrupted would be answering a key with
		// the opposite of what it says.
		m.compacting, m.compactResume, m.compactRun = false, false, nil
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.appendEntry(entry{kind: entrySystem, text: "compaction cancelled; conversation unchanged"})
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
	// And the calls the round is running, which answer as stopped: the
	// rows below read so, and so does what the model is told.
	m.endToolRound()
	// Before anything else, because this is what stops the close from being
	// drawn while a suite is still running: the row it leaves is what the
	// close block reads its verdict off (gate.go).
	m.cancelCloseGate()
	if m.todo.runner.state != nil && !m.todo.runner.state.Over() {
		m.todo.runner.cancelled = true
	}
	// Ctrl+C cancels the whole child tree with the turn.
	m.cancelSubagents()
	// And the one part of a fetch that is not a request: a wait a host asked
	// for is time nobody is now spending it for (activity.go).
	if m.wiring.AbandonFetchWaits != nil {
		m.wiring.AbandonFetchWaits()
	}
	// And a server call, for the same reason and one step further out: a
	// server request runs under the toolset's own context rather than the
	// call's, so without this the cancel leaves a request to a hung server
	// running until its own timeout (mcp.go).
	m.abandonMCPCalls()
	for _, tc := range m.agent.CancelTurn() {
		if _, ok := m.approval.classifierAllowed[tc.ID]; ok {
			delete(m.approval.classifierAllowed, tc.ID)
			m.recordDecision(observe.DecisionDeny, observe.ReasonUserStop)
		}
		m.appendCallRow(tc.ID, entry{kind: entryTool, toolName: tc.Name, toolArgs: tc.Arguments, toolResult: agent.CancelledResult})
	}
	m.approval.request = nil
	m.memoryAsk = nil
	// And the question, which the cancel answered with the turn's synthetic
	// result. A card left behind here is not merely a stale pointer: the
	// grace window reads it to decide which keys a burst may not answer
	// (interrupt.go), so the next decision to arrive would be protected by
	// the wrong card's keys. A question the reader had handed to the draft
	// goes with it too — it is the request above and this card together, so
	// clearing both is what makes the next message an ordinary message again
	// (question.go).
	m.question = nil
	// The queue the strip described is gone with the turn, and so is every
	// answer the list gave against it.
	m.clearQueueStrip()
	m.approval.batchAnswered, m.approval.total = nil, 0
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

// deliverSteering injects queued steering and, where a person's words (or
// another session's) joined, tells the loop the turn was steered, reporting
// whether anything was queued. It is the whole of a steer for a caller that
// is not the round tail; the tail tells the loop itself (agent.RoundTail).
func (m *Model) deliverSteering() bool {
	queued, fresh := m.injectSteering()
	if fresh {
		m.agent.Steered()
	}
	return queued
}

// injectSteering appends queued steering messages to the conversation and
// transcript, reporting whether any were queued and whether any of them were
// fresh input — a person's, or another session's. Fresh input extends the
// summary target and records checkpoints, and the loop's half of it, the
// round counter and the verdict judged before it, is the caller's
// (agent.Agent.Steered); machine-authored steering (announcements the session
// wrote for itself) joins as machine messages without disturbing those
// metrics.
func (m *Model) injectSteering() (queued, fresh bool) {
	if len(m.steering) == 0 {
		return false, false
	}
	// Each line carries what was staged when it was queued: those belong to
	// it, and what is staged now belongs to the sentence still being typed.
	humanSteers, sentSteers := 0, 0
	for _, item := range m.steering {
		if item.sent {
			// A line another session sent: a steer, in the session's own
			// voice rather than the reader's, so it is not a turn of theirs
			// and leaves no checkpoint for a rewind to offer (inbound.go).
			m.injectSent(item, false)
			sentSteers++
			continue
		}
		if item.machine {
			m.agent.AppendMachine(item.text)
			m.appendEntry(entry{kind: entrySystem, text: item.text})
			continue
		}
		humanSteers++
		// The steer is a turn of its own, and the number moves before the
		// message is written rather than after the batch, because everything
		// stamped with it — the checkpoint, the conversation's own record of
		// where this message was written, and every event the rest of the
		// turn files — has to agree on which turn that is. Counted after,
		// the words went in under the turn before them while the events went
		// in under this one, and the two could no longer be read against
		// each other
		// (docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back).
		m.turnCount++
		m.agent.SetTurn(m.turnCount)
		m.recordCheckpoint(item.text)
		m.agent.Append(provider.Message{Role: provider.RoleUser, Content: item.text, Attachments: item.atts})
		m.appendSent(item.text, item.atts)
		// What the reader has just asked for is part of what this turn is
		// serving, so it is part of what the readings judge it against
		// (agent.ExtendTarget). The anchor is there to stop the run moving
		// its own yardstick; the person is not the run.
		m.summaryTarget = agent.ExtendTarget(m.summaryTarget, item.text)
	}
	if humanSteers+sentSteers > 0 {
		// And what was judged against the shorter instruction is retired, before
		// the boundary below can deliver it (summary.go).
		m.summarySteered()
		if humanSteers > 0 {
			m.signal(observe.SignalSteer, strconv.Itoa(humanSteers))
		}
		m.movedPastTurn()
	}
	m.steering = nil
	m.denialNotice = ""
	m.syncViewport()
	return true, humanSteers+sentSteers > 0
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
	if !m.deliverSteering() {
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
// is silently lost. Machine-authored announcements are not restored.
func (m *Model) restoreSteering() {
	if len(m.steering) == 0 {
		return
	}
	var parts []string
	for _, item := range m.steering {
		switch {
		case item.sent:
			// Another session's words are not the reader's draft: they go
			// back to their card, for the reader to pass on to the next turn
			// or drop (inbound.go).
			m.inbound.held = append(m.inbound.held, InboundLine{From: item.from, Text: item.text})
			if item.id != 0 && item.id == m.queue.sel {
				m.queue.returned = queueToHeldCard
			}
		case !item.machine:
			parts = append(parts, item.text)
			if item.id != 0 && item.id == m.queue.sel {
				m.queue.returned = queueToDraft
			}
			// What rode with it goes back on the strip, where the fold its
			// sentence holds can find it again.
			m.attachments = append(m.attachments, item.atts...)
		}
	}
	if cur := m.input.Value(); strings.TrimSpace(cur) != "" {
		parts = append(parts, cur)
	}
	if len(parts) > 0 {
		m.input.SetValue(strings.Join(parts, "\n"))
	}
	m.steering = nil
}
