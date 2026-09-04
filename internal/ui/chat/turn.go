package chat

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The turn: what the session's own work is doing, and the messages it
// reports.
//
// Two different things used to share Model.state: what the session's own
// turn is doing (streaming, running a command, waiting on the classifier)
// and which transient view owns the screen (focus mode, the full-screen
// diff, a picker). Conflating them is why nothing could be opened or run
// mid-turn — a surface that overwrote the state made the turn's own messages
// unroutable, so every command was refused while the agent worked. With
// sub-agents that ruled out the whole management surface exactly when it
// matters: children only exist while the parent turn is in flight.
//
// The split keeps Model.state as the one field the renderer switches on and
// parks the turn's state in turnBack while a surface borrows the screen. The
// turn keeps running underneath: its messages route by turnState(), its
// transitions land through setTurnState(), and leaving the surface hands the
// screen back to whatever the turn became in the meantime.

// isSurface reports whether s is a transient view borrowing the screen
// rather than a stage of the session's own turn. It reads the register
// (overlay.go) rather than a list of its own: the list was one of six, and
// the one whose omissions were invisible — a mode missing from it parks the
// turn under itself and its own messages stop routing.
func (s state) isSurface() bool {
	o := overlayFor(s)
	return o != nil && o.borrows
}

// turnState is what the session's own turn is doing, ignoring any surface
// currently borrowing Model.state.
func (m Model) turnState() state {
	if m.state.isSurface() {
		return m.turnBack
	}
	return m.state
}

// setTurnState moves the turn to s, writing through a surface that has the
// screen instead of closing it: a turn that finishes (or asks for approval)
// while the user is reading a diff waits for them to come back.
func (m *Model) setTurnState(s state) {
	// A turn is not over while the checks it owes are still to run. The
	// verdict belongs on the close row, so the turn goes to the gate rather
	// than to the input and comes back here when the verdict is in
	// (gate.go). It is decided ahead of everything below because what
	// follows is the end of a turn, and this turn has not reached one.
	//
	// Only a turn that ran to its own end: a cancelled or broken one left
	// the work halfway, and a verdict about half an edit is a verdict about
	// nothing. A turn parked at its round ceiling has not ended either — its
	// checkpoint row is already on screen and the checks are owed to the
	// round that finishes it, the way the close row is.
	if s == stateInput && m.working() && !m.turnStarted.IsZero() && !m.heldAtBoundary() &&
		!m.pausedAtRoundLimit() && m.turnOutcome == components.TurnDone && m.closeGateOwed() {
		s = stateCloseGate
	}
	// Every arrival at a decision, and every departure from one, passes
	// through here — so this is where the keyboard is decided. A card can
	// never inherit the gate the last one was given, and one
	// arriving on a draft nobody is typing into holds the keyboard itself
	// rather than charging a handover for a sentence that is not there.
	m.armDecision(s)
	// Nor can a card inherit the last one's scroll: the offsets describe a
	// body that has just been replaced, and a stale pan would blank the new
	// card's rows outright. Reset on every arrival — /run, a !bang, the
	// classifier skip and the queue all pass through here
	// (docs/interface/surfaces.md#the-approval-card). A card's own
	// full-screen [d] round trip is surface mechanics and never re-arrives.
	if s == stateConfirmRun {
		m.cardScroll, m.cardPan = 0, 0
	}
	// A turn going idle stamps its end, so the inspector rail's elapsed time
	// freezes at what the turn took instead of counting on.
	//
	// A turn parked at a round boundary is the exception, and the only one:
	// it has not ended, its next round is what continues it, and everything
	// below would say otherwise — the close row, the record of how the turn
	// came out, the ring the spend sparkline is drawn from. All of it is
	// owed to the round that finishes the turn instead (hold.go).
	if s == stateInput && m.working() && !m.turnStarted.IsZero() && !m.heldAtBoundary() {
		m.turnEnded = time.Now()
		// A turn can have edited the backlog files; the rail reads the
		// store, so the store is re-read here rather than per frame.
		m.reloadTodos()
		// The turn's usage joins the session's history with its wall time
		//, which is why the ring is closed here and not on the
		// last usage report — a turn is more than its final request.
		m.vitals.endTurn(m.turnEnded.Sub(m.turnStarted))
		// And the turn closes with what it did. It happens here for
		// the same reason: every path back to the input passes through this
		// one transition, so no turn can end without a summary.
		m.appendTurnClose()
		// A hold the turn outran goes with it, and so does the hold on
		// every child. A turn that answered without asking for another
		// round never reaches the boundary the mark would have been acted
		// on at, and a fan-out left parked on a request its parent has
		// finished with has nothing that would ever let it go (hold.go).
		m.dropHold()
		// A turn that ends at the alert threshold ends with the decision
		// surface, not with a notice about a trim that already happened
		//. Every path back to the input passes through here, which
		// is what makes "once per crossing" a property rather than a habit.
		defer m.armPressureCard()
	}
	if m.state.isSurface() {
		m.turnBack = s
		return
	}
	m.state = s
}

// enterSurface gives the screen to a transient view, remembering the turn
// state it borrows. Opening one surface from another (the full-screen diff
// from focus mode) keeps the same parked turn.
func (m *Model) enterSurface(s state) {
	if !m.state.isSurface() {
		m.turnBack = m.state
	}
	// A surface that borrows the screen takes the transcript's selection with
	// it (select.go): the full-screen viewers replace the pane, and
	// reading mode re-renders the history through a cursor gutter and can
	// expand a row under coordinates that were taken before it did.
	m.cancelSelection()
	m.state = s
}

// leaveSurface hands the screen back to the turn, which may have moved on
// while the surface was up.
func (m *Model) leaveSurface() {
	if m.state.isSurface() {
		m.state = m.turnBack
	}
	m.turnBack = stateInput
	// A decision the turn reached while the surface had the screen is only
	// arriving now, so it is armed now, the way every arrival is: holding
	// the keyboard over an empty draft, with the grace window absorbing the
	// keystroke that closed the surface and whatever followed it
	// (interrupt.go). One that was already holding the keyboard keeps it:
	// the reader took it on purpose, and the surface they just closed was
	// most likely the card's own full-screen diff.
	if m.arrivalGates(m.state) && !m.decisionHeld {
		// (arrivesHeld answers the summoned case too, so a /run confirm
		// picked from the block picker lands holding the keyboard.)
		m.decisionHeld = m.arrivesHeld()
		m.heldOnArrival = m.decisionHeld
		m.armGrace()
	}
}

// answerIsArriving reports whether the round has prose on the way that has
// not become an entry yet — the one thing the transcript draws whose last
// line is still open, which is what anything drawn under it has to know.
func (m Model) answerIsArriving() bool {
	return m.turnState() == stateStreaming && m.streaming != ""
}

// working reports whether the session's own turn is in flight — streaming,
// running a command, or waiting on the permission classifier. The input is
// live in all three, and so are the commands that leave the running
// conversation alone.
func (m Model) working() bool {
	switch m.turnState() {
	case stateStreaming, stateRunningCmd, stateClassifying, stateRetryWait, stateCloseGate:
		// A turn waiting out a retry is still a turn in flight: it
		// has not closed, its accounting is open, and the next thing it does
		// is the request it was already making.
		return true
	}
	return false
}

// inputLive reports whether the textarea owns the keyboard: the turn states
// that keep it (idle and working), with no surface or prompt over it.
func (m Model) inputLive() bool {
	if m.state.isSurface() {
		return false
	}
	// A decision on screen that has not been given the keyboard has not taken
	// it from the draft either: the frame is live, and so is
	// everything the input offers.
	if m.decisionUngated() {
		return true
	}
	switch m.state {
	case stateInput, stateStreaming, stateRunningCmd, stateClassifying, stateCloseGate:
		return true
	}
	return false
}

// updateTurn answers what the session's own turn reported: the stream's
// tokens and its end, the tool calls and their results, and the background
// work a turn starts and comes back from. handled is false for a message
// this route does not own.
func (m Model) updateTurn(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case initialPromptMsg:
		text := m.initialPrompt
		m.initialPrompt = ""
		return answered(m.sendUserMessage(text))

	case streamStartedMsg:
		m.events = msg.events
		m.cancel = msg.cancel
		// A round is a request: the row the last one's reasoning landed on is
		// not the row this one's belongs to (think.go), and the call the last
		// one was writing is not this one's either (activity.go).
		m.settleThink()
		m.thinkIdx = 0
		m.composed = 0
		return m, waitForEvent(m.events), true

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
		// A batch that ended on an argument fragment waits for the tick like
		// a plain one does (activity.go).
		if m.spinning && repaintsOnTick(msg.final) {
			m.streamDirty = true
		} else {
			m.flushStream()
		}
		if msg.final != nil {
			return answered(m.update(msg.final))
		}
		return m, waitForEvent(m.events), true

	case toolDeltaMsg:
		m.appendCompose(msg.delta)
		// A round writing a call sends fragments with nothing between them,
		// so most arrive as a message of their own rather than at the end of
		// a token batch. The repaint rule is the batch's either way: ride the
		// tick where one is running, and take the repaint here where none is,
		// because nothing else is going to.
		if m.spinning {
			m.streamDirty = true
		} else {
			m.flushStream()
		}
		return m, waitForEvent(m.events), true

	case doneMsg:
		m.clearRetryChain()
		m.accumulateUsage(msg.usage)
		// A response that ended in text asked for no tools, so its thinking
		// has nowhere to travel to and the latch is dropped rather than left
		// for a later round to pick up.
		m.agent.CarryReasoning(nil)
		if m.compacting {
			return answered(m.finishCompact())
		}
		hadText := m.streaming != ""
		text := m.streaming
		m.finishStreaming()
		// A reply that stopped at the output ceiling is not the model's whole
		// answer, and nothing that follows a finished turn should treat it as
		// one: no plan to approve, no queued follow-up sent against half an
		// answer. What it gets instead is the offer to have it finished
		// (resume.go).
		if msg.stop == provider.StopLength {
			if hadText {
				return answered(m.truncatedReply(text))
			}
			// The budget went entirely on a call the ceiling then cut, so
			// there is no sentence to offer to finish — only the fact that
			// the turn is ending on nothing rather than on an answer.
			m.truncatedRound()
		}
		// A steering message queued while the model was responding becomes the
		// next user turn immediately.
		if cmd := m.dispatchSteering(); cmd != nil {
			return m, cmd, true
		}
		// A completed planning response gets the plan-approval prompt —
		// unless a backlog run is what asked for the plan, in which case
		// the runner reads it and there is nothing to approve by hand. A
		// grooming reading is the same: it is a plan-mode turn whose answer
		// is verdicts, and what is put to the reader is its card.
		if m.policy.mode == agent.ModePlan && hadText && m.todoRunner.state == nil && !m.todoGroomer.going() {
			m.setTurnState(statePlanApprove)
			m.armPlan()
			m.syncViewport()
		}
		// The turn is truly over and nothing took the keyboard: the oldest
		// queued follow-up goes out as the next user message (followup.go).
		if m.turnState() == stateInput {
			if next, cmd, sent := m.dispatchFollowUp(); sent {
				return next, cmd, true
			}
		}
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd(), true

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
			return answered(m.abortCompact())
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
		if msg.stop == provider.StopLength {
			// The round asked for tools and ran out of budget while it was
			// still writing them. The calls that were finished are whole and
			// run as usual; the one the ceiling landed inside of never
			// reaches here (internal/provider/partial.go), and this is the
			// line that says so rather than letting a round quietly lose one.
			// It goes under the announcement, which is where it happened.
			m.truncatedRound()
		}
		m.streaming = ""
		m.events = nil
		m.cancel = nil
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		if len(auto) > 0 {
			return m, tea.Batch(m.execToolsCmd(auto), summary), true
		}
		next, cmd := m.advanceApprovalQueue()
		return next, tea.Batch(cmd, summary), true

	case toolResultsMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateStreaming {
			return m, nil, true
		}
		m.agent.RecordAutoResults(msg.results)
		m.runningTools = nil
		for _, r := range msg.results {
			m.recordToolResult(r.Call.Name, r.Duration, r.Result)
			if agent.IsRepeatNotice(r.Result) {
				m.signal(observe.SignalRepeat, r.Call.Name)
			}
			m.appendEntry(entry{kind: entryTool, toolName: r.Call.Name, toolArgs: r.Call.Arguments, toolResult: r.Result, duration: r.Duration})
		}
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		if m.agent.QueuedApprovals() > 0 {
			return answered(m.advanceApprovalQueue())
		}
		return answered(m.resumeToolLoop())

	case cmdDoneMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateRunningCmd {
			return m, nil, true
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
			outcome, class := observe.OutcomeOK, ""
			if msg.exitCode != 0 {
				outcome, class = observe.OutcomeError, observe.ClassExitStatus
			}
			m.recordToolEvent(tools.ExecCommandName, msg.duration, outcome, class)
		}
		m.appendEntry(entry{kind: entryCommand, text: msg.command, toolResult: out, exitCode: msg.exitCode, localRun: msg.local, duration: msg.duration})
		if m.pendingApproval != nil {
			m.pendingApproval = nil
			m.agent.ResolveApproval(execToolResult(out, msg.exitCode))
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
			return answered(m.advanceApprovalQueue())
		}
		m.setTurnState(stateInput)
		// A local run's output stays out of the conversation: that is the
		// whole difference `!!` buys, and the row's outcome says so (bang.go).
		if !msg.local {
			m.agent.Append(provider.Message{
				Role:    provider.RoleUser,
				Content: commandContextMessage(msg.command, out, msg.exitCode),
			})
		}
		// A message typed while the /run command executed is sent now, with
		// the command context already in the conversation.
		if cmd := m.dispatchSteering(); cmd != nil {
			return m, cmd, true
		}
		// And a follow-up queued while it ran goes out the same way: the
		// session is idle, which is all "after the turn" ever meant.
		if next, cmd, sent := m.dispatchFollowUp(); sent {
			return next, cmd, true
		}
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, m.autosaveCmd(), true

	case approvedToolDoneMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateRunningCmd || m.pendingApproval == nil {
			return m, nil, true
		}
		req := m.pendingApproval
		m.pendingApproval = nil
		m.agent.ResolveApproval(msg.result)
		m.recordToolResult(req.call.Name, msg.duration, msg.result)
		m.noteEvictedTurns(msg.evicted)
		// An applied edit lands in the transcript as a collapsed diff row (
		// docs/interface/surfaces.md#the-diff-view); failures keep the plain tool
		// block so the error text stays visible.
		if req.kind == approvalDiff && len(req.hunks) > 0 && digest.Outcome(msg.result) == digest.OutcomeOK {
			m.appendEntry(entry{kind: entryDiff, diff: &components.DiffView{
				Path:     req.path,
				Verb:     req.verb,
				Hunks:    req.hunks,
				Mode:     components.DiffCollapsed,
				MaxLines: maxDiffExpandedLines,
				Syntax:   diffSyntax(req.path),
			}})
		} else if req.call.Name == subagent.SpawnToolName && digest.Outcome(msg.result) == digest.OutcomeOK {
			m.appendSpawnEntry(entry{kind: entryTool, toolName: req.call.Name, toolArgs: req.call.Arguments, toolResult: msg.result, duration: msg.duration})
		} else {
			m.appendEntry(entry{kind: entryTool, toolName: req.call.Name, toolArgs: req.call.Arguments, toolResult: msg.result, duration: msg.duration})
		}
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return answered(m.advanceApprovalQueue())

	case preToolHookMsg:
		// The hooks in front of a gated call have answered; the queue picks
		// up where it left off (approval.go).
		if msg.runID != m.agent.RunID() || msg.req == nil {
			return m, nil, true
		}
		return answered(m.finishPreToolHook(msg))

	case classifierDoneMsg:
		if msg.runID != m.agent.RunID() || m.turnState() != stateClassifying || m.pendingApproval == nil {
			return m, nil, true
		}
		m.classifierCancel = nil
		return answered(m.finishClassifierCheck(msg.verdict))

	case titleDoneMsg:
		return m, m.finishTitle(msg), true

	case autosaveMovedMsg:
		m.noteSlotMove(msg)
		return m, nil, true

	case closeGateMsg:
		// The turn has been waiting on this: its close row is not drawn yet,
		// and a failing verdict may still earn it another round (gate.go).
		return answered(m.finishCloseGate(msg))

	case summaryDoneMsg:
		// A reading never routes anything: it changes what the rail draws and
		// what the transcript holds, and nothing else, which is why it has no
		// turn-state guard of its own. finishSummary decides what to keep.
		m.summaryCancel = nil
		if m.finishSummary(msg) {
			// The reading landed a row, and it landed out of band: no stream
			// owed this frame a repaint. Following to the bottom only if the
			// reader was already there is the same courtesy the stream shows
			// — a background row must not yank a scrollback anybody is
			// reading.
			m.flushStream()
		}
		return m, nil, true

	case modelListMsg:
		return answered(m.finishModelList(msg))

	case subagentEventMsg:
		return answered(m.handleSubagentEvent(msg.ev))

	case streamErrMsg:
		// Classified, never raw: the failure is a row on the
		// activity grid with the provider's own words in its detail body and
		// the keys for its class underneath. What happens after the row —
		// an offer to continue a partial, a bounded wait, or the end of the
		// turn — belongs to the retry path (resume.go).
		return answered(m.handleStreamFailure(msg))

	case retryTickMsg:
		return answered(m.retryTick(msg))

	case clipboardMsg:
		return answered(m.handleClipboard(msg))

	case editorDoneMsg:
		return answered(m.editorFinished(msg))

	case todoEditorDoneMsg:
		return answered(m.todoEditorFinished(msg))

	case memoryEditorDoneMsg:
		return answered(m.memoryEditorFinished(msg))

	case mcpPromptMsg:
		return answered(m.applyMCPPrompt(msg))

	case personaDraftMsg:
		return answered(m.finishPersonaDraft(msg))

	case todoProposalsMsg:
		return answered(m.finishTodoExtract(msg))

	case todoDraftMsg:
		return answered(m.finishTodoDraft(msg))

	case todoDraftEditorDoneMsg:
		return answered(m.todoDraftEditorFinished(msg))

	case todoVerifyMsg:
		return answered(m.finishTodoVerify(msg))

	case todoCommitMsg:
		return answered(m.finishTodoCommit(msg))

	case attachedFileMsg:
		return answered(m.handleAttachedFile(msg))

	case spinner.TickMsg:
		// The one tick, advancing the one frame (spin.go). The guard
		// that used to stand here decided whether to answer at all, and a
		// tick it declined took the chain with it.
		return answered(m.spinTick(msg))
	}
	return m, nil, false
}
