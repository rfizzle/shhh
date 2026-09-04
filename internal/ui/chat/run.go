package chat

// Running a code block, and the confirm that stands in front of it.
//
// /run and a !bang both land on the same card and the same execution, which
// is the point: what a command costs does not depend on which door asked for
// it (docs/interface/surfaces.md#the-approval-card).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

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
	// A memory proposal confirms through its own prompt, not the card. It is
	// a row of the register like every other mode (overlay.go) rather than
	// the hand check it was in each of the five places a state is read.
	if o := m.askOverlay(); o != nil {
		next, act := o.Update(m, msg)
		return next, act.run
	}
	// The card's own scroll, answered before the decision keys so a held
	// card cannot read a chord as the start of a sentence. The chords reach
	// here only while the card holds the keyboard; ungated they still
	// scroll the transcript, exactly as before.
	if keys.Match(msg, keys.Decision.ScrollUp, keys.Decision.ScrollDown,
		keys.Decision.PanLeft, keys.Decision.PanRight) {
		return m.scrollCard(msg)
	}
	done, result := m.approvalCard().Update(msg)
	if !done {
		return m, nil
	}
	switch result {
	case components.ApprovalApprove:
		if m.pendingApproval != nil {
			m.recordDecision(observe.DecisionAllow, observe.ReasonUser)
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
		// A command card's [d] opens its own facts the same way: the whole
		// command, the warnings and the blast radius, unclipped, with the
		// decision still pending behind it.
		if req := m.pendingApproval; req == nil || req.kind == approvalExec {
			return m.openOutputFull(m.commandCardView(), noOutputEntry, stateConfirmRun)
		}
	case components.ApprovalBatch:
		// [A] answers this decision and every queued decision the session
		// would classify the same way. Membership was on the strip
		// before the key applied it, and a flagged action was never in it.
		if req := m.pendingApproval; req != nil && len(m.pendingBatch) > 0 {
			m.approveBatch()
			m.recordDecision(observe.DecisionAllow, observe.ReasonUserBatch)
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
				m.recordDecision(observe.DecisionAllow, observe.ReasonUserAlways)
				if prefix := m.grantCommand(req.command); prefix != "" {
					m.noteGrant("Commands starting " + strconv.Quote(prefix) + " will run without asking. /permissions revoke takes it back.")
				}
				m.syncChildGrants()
				return m.executeRun()
			case approvalDiff:
				m.recordDecision(observe.DecisionAllow, observe.ReasonUserAlways)
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
		m.pendingRunLocal = false
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
	local := m.pendingRunLocal
	m.pendingRun = ""
	m.pendingRunLocal = false
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
	// An assistant command gets a ceiling; a command the reader typed does
	// not, because they are here and the cancel key is their ceiling.
	assistant := m.pendingApproval != nil
	var ctx context.Context
	var cancel context.CancelFunc
	if assistant && m.policy.timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), m.policy.timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
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
	timeout := m.policy.timeout
	if !assistant {
		timeout = 0
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
		// A killed command reports what it managed to print and an exit code
		// that says nothing about why it stopped. The model has to be told
		// which of the two it was, or it reads a timeout as a failing command
		// and starts debugging the command.
		out = noteTimeout(out, ctx.Err(), timeout)
		return cmdDoneMsg{runID: runID, command: command, output: out, exitCode: code, duration: time.Since(start), local: local}
	}
}

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
