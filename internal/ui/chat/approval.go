package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// GatedPreview describes what an approval-gated tool call is about to do, for
// the confirm prompt. When Path is set, OldText/NewText are shown as a colored
// unified diff; otherwise Summary is shown as a generic preview.
type GatedPreview struct {
	Action  string // short verb for the title, e.g. "write", "edit"
	Path    string
	OldText string
	NewText string
	Summary string
	// Fields is the tool's own blast-radius block (S-101) — a fetch states
	// its domain and what it sends, a spawn states the scope it claims. shhh
	// cannot resolve these from the arguments the way it resolves a shell
	// command's paths, so the tool that owns them supplies them.
	Fields []GatedField
}

// GatedField is one row of a tool's blast-radius block.
type GatedField struct {
	Label  string
	Value  string
	Detail string
	// Open marks a value that leaves something open — an outbound request, a
	// writable scope — so the card can colour it and rate the card by it.
	Open bool
}

// GatedPreviewFunc builds the confirm-prompt preview for one tool call's
// arguments. An error skips the call with an error tool result, mirroring how
// invalid execute_command arguments are handled.
type GatedPreviewFunc func(args json.RawMessage) (GatedPreview, error)

// approvalKind selects which preview the confirm prompt renders for a queued
// approval-gated tool call.
type approvalKind int

const (
	approvalExec    approvalKind = iota // one-line command + safety warnings
	approvalDiff                        // colored unified diff of a file write/edit
	approvalGeneric                     // one-line summary of the tool call
	approvalMemory                      // memory proposal (S-070): scope selector with optional note
)

// approvalRequest is the head of the approval queue: one tool call awaiting
// the user's decision, with everything needed to preview and execute it.
type approvalRequest struct {
	call    provider.ToolCall
	kind    approvalKind
	command string      // approvalExec: the command handed to the runner; also set for a process start (S-073) so mode policy treats it as a command
	title   string      // action headline, e.g. "edit main.go"
	verb    string      // approvalDiff: the action verb, e.g. "edit"
	path    string      // approvalDiff: the file being modified
	hunks   []diff.Hunk // approvalDiff: the change to show
	summary string      // one-line description for transcript entries
	// fields is a gated tool's own blast-radius block (S-101), from its
	// GatedPreview.
	fields []GatedField
	// auto marks a call the session approved on the user's behalf — mode
	// policy, a session grant, or the auto-mode classifier. It is what the
	// changeset record's origin says afterwards (S-097).
	auto bool
	// memoryDraft is the proposed entry for approvalMemory (S-070).
	memoryDraft memory.Draft
}

// approvedToolDoneMsg carries the executor result of an approved non-exec
// tool call.
type approvedToolDoneMsg struct {
	runID    int
	result   string
	duration time.Duration
	// evicted names the turns the changeset write dropped to stay inside its
	// bound, so the session can say so rather than losing them silently
	// (S-097).
	evicted []int64
}

// MutationHook post-processes an applied file-modification tool result before
// it is reduced and recorded — the LSP integration (S-071) uses it to append
// fresh diagnostics for the touched file so the model can self-correct in the
// same round. It runs off the UI goroutine and must return promptly (its own
// waits are bounded).
type MutationHook func(name string, args json.RawMessage, result string) string

// WithMutationHook installs the post-mutation result hook.
func (m Model) WithMutationHook(hook MutationHook) Model {
	m.mutationHook = hook
	return m
}

// WithGatedTools registers tools that must be approved by the user before
// they run through the tool executor; each entry builds the confirm-prompt
// preview for its tool. Gated tools never run via the auto-run path.
func (m Model) WithGatedTools(previews map[string]GatedPreviewFunc) Model {
	m.gatedTools = previews
	return m
}

// requiresApproval reports whether a tool call must go through the approval
// queue instead of the auto-run executor path. File-modification tools are
// always gated, mirroring how execute_command is intercepted.
func (m Model) requiresApproval(tc provider.ToolCall) bool {
	if tc.Name == tools.ExecCommandName && m.runFn != nil {
		return true
	}
	if tools.IsMutating(tc.Name) {
		return true
	}
	// remember (S-070) is always gated: agent-proposed memories persist only
	// after explicit user confirmation.
	if tc.Name == memory.RememberToolName {
		return true
	}
	// The process tool (S-073) gates on its arguments: start launches a
	// command and needs approval; status/read/input/stop auto-run.
	if m.processes.Manage != nil && tc.Name == process.ToolName {
		return process.NeedsApproval(json.RawMessage(tc.Arguments))
	}
	_, ok := m.gatedTools[tc.Name]
	return ok
}

// buildApprovalRequest turns a queued tool call into its confirm prompt.
func (m Model) buildApprovalRequest(tc provider.ToolCall) (*approvalRequest, error) {
	if tc.Name == tools.ExecCommandName {
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || strings.TrimSpace(args.Command) == "" {
			return nil, fmt.Errorf("invalid command arguments")
		}
		return &approvalRequest{
			call:    tc,
			kind:    approvalExec,
			command: args.Command,
			summary: firstLine(args.Command),
		}, nil
	}

	// Memory proposals get the scope-selector prompt, never a generic card
	// (S-070).
	if tc.Name == memory.RememberToolName {
		return m.buildMemoryApproval(tc)
	}

	// A process start (S-073) is approved like a command: the card shows the
	// command text, and mode policy treats it as one (allowlist, safety).
	if m.processes.Manage != nil && tc.Name == process.ToolName {
		name, command, err := process.StartSummary(json.RawMessage(tc.Arguments))
		if err != nil {
			return nil, err
		}
		title := "start process " + name
		return &approvalRequest{
			call:    tc,
			kind:    approvalGeneric,
			title:   title,
			command: command,
			summary: title + ": " + firstLine(command),
		}, nil
	}

	// A registered preview overrides the built-in mutating-tool handling.
	preview, ok := m.gatedTools[tc.Name]
	if !ok {
		if !tools.IsMutating(tc.Name) {
			return nil, fmt.Errorf("tool %s cannot be approved in this session", tc.Name)
		}
		mut, err := tools.PreviewMutation(tc.Name, json.RawMessage(tc.Arguments))
		if err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		title := mut.Action + " " + mut.Path
		return &approvalRequest{
			call:    tc,
			kind:    approvalDiff,
			title:   title,
			verb:    mut.Action,
			path:    mut.Path,
			hunks:   diff.Compute(mut.OldText, mut.NewText),
			summary: title,
		}, nil
	}
	p, err := preview(json.RawMessage(tc.Arguments))
	if err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Path != "" {
		action := p.Action
		if action == "" {
			action = "modify"
		}
		title := action + " " + p.Path
		return &approvalRequest{
			call:    tc,
			kind:    approvalDiff,
			title:   title,
			verb:    action,
			path:    p.Path,
			hunks:   diff.Compute(p.OldText, p.NewText),
			summary: title,
			fields:  p.Fields,
		}, nil
	}
	summary := p.Summary
	if summary == "" {
		summary = formatToolArgs(tc.Arguments)
	}
	return &approvalRequest{
		call:    tc,
		kind:    approvalGeneric,
		title:   "use " + tc.Name,
		summary: summary,
		fields:  p.Fields,
	}, nil
}

// advanceApprovalQueue shows the confirm prompt for the next queued
// approval-gated tool call, or resumes the model stream once the queue is
// empty.
func (m Model) advanceApprovalQueue() (tea.Model, tea.Cmd) {
	tc, ok := m.agent.NextApproval()
	if !ok {
		return m.resumeToolLoop()
	}
	req, err := m.buildApprovalRequest(tc)
	if err != nil {
		m.agent.ResolveApproval("error: " + err.Error())
		m.appendEntry(entry{kind: entrySystem, text: "Skipped a tool call with invalid arguments."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	m.pendingApproval = req
	if req.kind == approvalExec {
		m.pendingRun = req.command
	}
	// The blast radius is resolved once here, not inside View: it stats the
	// filesystem and asks git about the paths it found (S-101).
	m.pendingBlast = m.resolveRadius(req)
	// Agent-proposed memories (S-070) always require explicit user
	// confirmation: no mode, session grant, or classifier can wave one
	// through. Plan mode falls through to the policy below, which refuses the
	// write like any other.
	if req.kind == approvalMemory && m.mode != agent.ModePlan {
		m.recordDecision(decisionAsk, "memory")
		m.openMemoryAsk(req)
		m.armConfirm(req)
		return m, nil
	}
	// A decision an earlier [A] already answered (S-102) runs when its turn
	// comes, without asking again.
	if m.takeBatchApproval(req) {
		m.recordDecision(decisionAllow, "user-batch")
		req.auto = true
		m.appendEntry(entry{kind: entrySystem, text: "Approved with the batch: " + req.summary})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if req.kind == approvalExec {
			return m.executeRun()
		}
		return m.executeApprovedTool()
	}
	// Mode policy (S-059, absorbing S-054): the permissive modes and session
	// grants skip the prompt, plan mode refuses the call outright, and
	// safety-flagged commands always prompt.
	switch decision, reason := m.policyDecision(req); decision {
	case agent.Allow:
		m.recordDecision(decisionAllow, reasonCode(reason))
		req.auto = true
		m.appendEntry(entry{kind: entrySystem, text: "Auto-approved (" + reason + "): " + req.summary})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if req.kind == approvalExec {
			return m.executeRun()
		}
		return m.executeApprovedTool()
	case agent.Deny:
		m.recordDecision(decisionDeny, reasonCode(reason))
		m.pendingApproval = nil
		m.pendingRun = ""
		m.agent.ResolveApproval(agent.PlanModeResult)
		m.appendEntry(deniedEntry(req, decidedByAuto, reason, 0))
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	// In auto mode the classifier (S-060) judges what the static policy would
	// ask about — except safety-flagged actions, which always prompt the human.
	if m.mode == agent.ModeAuto && m.classifier != nil && !approvalAction(req).SafetyFlagged {
		return m.startClassifierCheck(req)
	}
	m.recordDecision(decisionAsk, askReason(approvalAction(req)))
	m.armConfirm(req)
	return m, nil
}

// startClassifierCheck sends the pending approval to the auto-mode permission
// classifier in the background (S-060); the verdict arrives as
// classifierDoneMsg.
func (m Model) startClassifierCheck(req *approvalRequest) (tea.Model, tea.Cmd) {
	m.setTurnState(stateClassifying)
	m.syncViewport()
	ctx, cancel := context.WithCancel(context.Background())
	m.classifierCancel = cancel
	classifier := m.classifier
	runID := m.agent.RunID()
	cwd, _ := os.Getwd()
	creq := agent.ClassifierRequest{
		Tool:      req.call.Name,
		Arguments: req.call.Arguments,
		CWD:       cwd,
		Recent:    m.agent.RequestMessages(),
	}
	return m, func() tea.Msg {
		return classifierDoneMsg{runID: runID, verdict: classifier.Judge(ctx, creq)}
	}
}

// finishClassifierCheck applies the classifier's verdict to the pending
// approval: allow executes it, deny refuses it with the reason as the tool
// result, and a failed check falls back to asking the user (fail closed).
func (m Model) finishClassifierCheck(v agent.ClassifierVerdict) (tea.Model, tea.Cmd) {
	// Classifier spend counts toward the session totals.
	m.TotalTokensIn += int64(v.Usage.PromptTokens)
	m.TotalTokensOut += int64(v.Usage.CompletionTokens)
	m.notifyUsage()

	req := m.pendingApproval
	elapsed := fmt.Sprintf("%.1fs", v.Elapsed.Seconds())
	switch decision, reason := agent.ResolveAuto(approvalAction(req), v); decision {
	case agent.Allow:
		m.recordDecision(decisionAllow, "classifier")
		req.auto = true
		m.appendEntry(entry{kind: entrySystem, text: "Auto-approved (classifier, " + elapsed + "): " + req.summary})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if req.kind == approvalExec {
			return m.executeRun()
		}
		return m.executeApprovedTool()
	case agent.Deny:
		m.recordDecision(decisionDeny, "classifier")
		m.lastDenial = req.summary + " — " + reason
		// Surfaces on the notice rail until the next user turn (S-082).
		m.denialNotice = req.summary
		m.pendingApproval = nil
		m.pendingRun = ""
		m.agent.ResolveApproval("error: auto mode denied this tool call: " + reason)
		m.appendEntry(deniedEntry(req, decidedByAuto, reason, v.Elapsed))
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	// Ask: the classifier failed closed or the safety backstop fired — the
	// user decides, never a silent allow.
	if v.Failed {
		m.recordDecision(decisionAsk, "classifier-failed")
		m.appendEntry(entry{kind: entrySystem, text: "Classifier unavailable (" + v.Reason + "); asking you instead."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
	} else {
		m.recordDecision(decisionAsk, "safety")
	}
	m.armConfirm(req)
	return m, nil
}

// declineApproval records an error tool result for the pending call and moves
// on to the next queued approval.
func (m Model) declineApproval() (tea.Model, tea.Cmd) {
	m.recordDecision(decisionDeny, "user")
	req := m.pendingApproval
	m.pendingApproval = nil
	m.pendingRun = ""
	content := "error: the user declined this tool call"
	switch req.kind {
	case approvalExec:
		content = "error: the user declined to run this command"
	case approvalMemory:
		content = "error: the user declined to save this memory; do not re-propose it this session"
	}
	m.agent.ResolveApproval(content)
	m.appendEntry(deniedEntry(req, decidedByYou, "", 0))
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m.advanceApprovalQueue()
}

// deniedEntry is the transcript row for a refused call (S-089,
// DESIGN-TUI.md §6d): the same activity row every other call gets, with ⊘,
// the decider's name and a duration field saying it never ran. A denial is a
// moment that mattered, so the row keeps its mutation rail (§14) — which is
// why it is a row and not a system notice.
func deniedEntry(req *approvalRequest, decider, rule string, elapsed time.Duration) entry {
	return entry{
		kind:     entryTool,
		toolName: req.call.Name,
		toolArgs: req.call.Arguments,
		deniedBy: decider,
		denyRule: rule,
		duration: elapsed,
	}
}

// executeApprovedTool runs an approved non-exec tool call through the tool
// executor in the background; the result arrives as approvedToolDoneMsg.
func (m Model) executeApprovedTool() (tea.Model, tea.Cmd) {
	m.setTurnState(stateRunningCmd)
	m.syncViewport()
	a := m.agent
	runID := a.RunID()
	call := m.pendingApproval.call
	// Built-in mutating tools run through their own dispatcher; the session
	// executor (the auto-run read-only path) never learns them. A registered
	// gated tool keeps the session executor. The session executor is already
	// wrapped by the reduction pipeline (S-064), so only the direct mutating
	// dispatch reduces here.
	_, registered := m.gatedTools[call.Name]
	mutating := !registered && tools.IsMutating(call.Name)
	reduce := m.evidence.Reduce
	hook := m.mutationHook
	// The changeset record is taken around the call, on this goroutine: the
	// file as it is now, then the file the call leaves behind (S-097). Both
	// reads happen next to the write, so a file that changed underneath the
	// approval preview is recorded as it really was, not as it was previewed.
	record := m.changeRecorder()
	return m, func() tea.Msg {
		var result string
		before := record.before()
		start := time.Now()
		if mutating {
			result = agent.ExecuteWith(tools.ExecuteMutating, call)
			if hook != nil {
				result = hook(call.Name, json.RawMessage(call.Arguments), result)
			}
			if reduce != nil {
				result = reduce(call.Name, result)
			}
		} else {
			result = a.ExecuteCall(call)
		}
		evicted := record.after(before)
		return approvedToolDoneMsg{runID: runID, result: result, duration: time.Since(start), evicted: evicted}
	}
}

// changeRecording captures one approved file modification for the changeset
// store. It is built on the UI goroutine and used on the executor's, which is
// why it carries plain values and a store that locks.
type changeRecording struct {
	store   *changeset.Store
	tracker *changeset.Tracker
	turn    int64
	path    string
	origin  changeset.Origin
}

// changeRecorder describes what this approval would record. A call with no
// file behind it (a command, a memory, a generic tool) records nothing.
func (m Model) changeRecorder() changeRecording {
	req := m.pendingApproval
	if req == nil || req.kind != approvalDiff || req.path == "" {
		return changeRecording{}
	}
	origin := changeset.Approved
	if req.auto {
		origin = changeset.AutoApproved
	}
	return changeRecording{
		store:   m.changes,
		tracker: m.tracker,
		turn:    m.turnCount,
		path:    req.path,
		origin:  origin,
	}
}

// fileState is a file as it was at one moment: content, and whether it was
// there at all.
type fileState struct {
	text   string
	exists bool
	track  changeset.Tracking
}

// before reads the file the approved call is about to modify, along with
// whether git knew about it — the input to the reversibility line elsewhere
// in the UI.
func (c changeRecording) before() fileState {
	if c.path == "" {
		return fileState{}
	}
	st := readFileState(c.path)
	st.track = c.tracker.Track(c.path)
	return st
}

// after records what the call actually left behind and returns the turns
// evicted by the write. The gate is the content, not the tool's own account
// of itself: a call that changed nothing records nothing, and a call that
// changed a file records it whatever it went on to report. Undo depends on
// the store knowing everything the workspace lost.
func (c changeRecording) after(before fileState) []int64 {
	if c.path == "" {
		return nil
	}
	now := readFileState(c.path)
	return c.store.Add(c.turn, changeset.Record{
		Path:         c.path,
		Before:       before.text,
		After:        now.text,
		BeforeExists: before.exists,
		AfterExists:  now.exists,
		Agent:        changeset.MainAgent,
		Origin:       c.origin,
		Track:        before.track,
	})
}

// readFileState reads a file, reporting a missing one as absent rather than
// as an error: a write that creates a file has no before-content, and that is
// the fact the record needs.
func readFileState(path string) fileState {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileState{}
	}
	return fileState{text: string(data), exists: true}
}

// approvalCard assembles the components.ApprovalCard (DESIGN-TUI.md §2) for
// the pending approval or /run confirmation. Both the confirm prompt's
// rendering and its key handling flow through this one card. While the user
// is attached to a child, the orchestrator's own card is labeled so it is
// never mistaken for the focused agent's (S-077).
func (m Model) approvalCard() *components.ApprovalCard {
	card := m.buildApprovalCard()
	if m.attachedTo != "" {
		card.Title = "orchestrator ▸ " + card.Title
	}
	// Whether the card's keys are live at all is not the card's to decide
	// (invariant 5, S-117): it depends on which surface holds the keyboard.
	m.applyNotYetLive(card)
	return card
}

func (m Model) buildApprovalCard() *components.ApprovalCard {
	card := &components.ApprovalCard{MaxLines: m.maxConfirmPanelHeight()}
	req := m.pendingApproval
	// Where this decision sits in the round, and the key that answers the
	// rest of its category along with it (S-102).
	card.QueuePos = m.queuePosition()
	if card.Batch = len(m.pendingBatch) > 0; card.Batch {
		card.BatchHint = fmt.Sprintf("A: approve %d like this", len(m.pendingBatch)+1)
	}
	// The blast-radius block, resolved when the decision was armed (S-101).
	// It also carries the safety risks, so the card states severity and
	// warnings from one source rather than two.
	m.pendingBlast.applyTo(card)

	if req == nil || req.kind == approvalExec {
		card.Variant = components.ApprovalCommand
		card.Title = "Approve command"
		card.Question = "Run this command?"
		if req != nil {
			card.Headline = "Assistant wants to run: " + firstLine(m.pendingRun)
		} else {
			card.Headline = "Run: " + firstLine(m.pendingRun)
		}
		// [a] is offered only for assistant commands without safety warnings:
		// flagged actions can never be pre-approved, and /run stays manual.
		// The card says why it is missing rather than omitting it silently
		// (S-101).
		//
		// What it grants is named on the card, because a key whose scope is
		// not stated is a key pressed on a guess: [a] records the command's
		// leading words, so the reader sees `go test` before they widen
		// anything, not `commands` after they have.
		if req != nil && len(card.Warnings) == 0 {
			if prefix := agent.GrantPrefix(req.command); prefix != "" {
				card.AllowAlways = true
				card.AlwaysHint = "a: always allow " + strconv.Quote(prefix)
			}
		}
		return card
	}

	card.Headline = "Assistant wants to " + req.title
	switch req.kind {
	case approvalDiff:
		card.Variant = components.ApprovalEdit
		card.Title = "Approve edit"
		card.Hunks = req.hunks
		card.Syntax = diffSyntax(req.path)
		card.FullDiff = len(req.hunks) > 0
		card.Question = "Apply this change?"
		card.AllowAlways = true
		card.AlwaysHint = "a: always allow edits in " + displayDir(filepath.Dir(req.path))
	default:
		card.Variant = components.ApprovalGeneric
		card.Title = "Approve tool"
		card.Question = "Allow this?"
		if req.summary != req.title {
			card.Summary = firstLine(req.summary)
		}
	}
	return card
}

// applyTo puts the resolved block onto the card: the severity word and the
// border that reinforces it, the risks, the fields, the containment chip, and
// the two lines that explain what the keys do not.
func (b blastRadius) applyTo(card *components.ApprovalCard) {
	card.Severity = b.severity
	card.Fields = b.fields
	card.Chip, card.Uncontained = b.chip, b.uncontained
	card.SafeDefault, card.Footnote = b.safe, b.footnote
	card.Reversibility = b.reversibility
	if len(b.risks) > 0 {
		card.Warnings = []string{strings.Join(b.risks, "; ")}
	}
}

// confirmLines renders the approval card — or the memory prompt (S-070) when
// one is showing — one row per element.
func (m Model) confirmLines() []string {
	width := m.contentWidth()
	// The queue strip (S-102) sits above whichever surface is asking.
	strip := m.pendingQueue.View(width)
	if m.memoryAsk != nil {
		return append(strip, m.memoryAskLines()...)
	}
	return append(strip, strings.Split(m.approvalCard().View(width), "\n")...)
}

// confirmPanelLines is the whole bottom panel a gated confirm occupies: the
// card, the rail that names the keyboard's owner, and the draft it is holding
// while it does (S-117, §7b). confirmLines stays the card alone, because that
// is what the rest of the surface asks it for.
func (m Model) confirmPanelLines() []string {
	return m.dressDecision(m.confirmLines(), m.contentWidth())
}

// renderConfirm renders the confirm prompt padded to the bottom panel height.
func (m Model) renderConfirm() string {
	lines := m.confirmPanelLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// bottomPanelHeight is how many rows the bottom panel currently occupies; the
// confirm and plan-approval prompts may grow beyond the input's fixed height.
func (m Model) bottomPanelHeight() int {
	var lines []string
	bound := m.maxConfirmPanelHeight()
	st := m.state
	if m.decisionUngated() && m.frameShowing() {
		// The card rides above the frame rather than filling the panel
		// (§7b), so the panel is the input's and interruptHeight is what
		// pays for the card.
		st = stateInput
	}
	switch st {
	case stateConfirmRun:
		lines, bound = m.confirmPanelLines(), m.confirmPanelBound()
	case statePlanApprove:
		lines, bound = m.planPanelLines(), m.planPanelBound()+m.gatedExtraRows()
	case stateRewindPick:
		lines = m.rewindPickLines()
	case statePick:
		lines = m.pickerLines()
	case stateUndoConfirm:
		lines = m.undoConfirmLines()
	case stateKeyEntry:
		lines = m.keyEntryLines()
	case statePressure:
		// The card is a decision, and a decision whose action bar was cut off
		// by the panel bound is not one: it gets the plan card's headroom.
		lines, bound = m.pressureLines(), m.planPanelBound()
	default:
		if m.agentList != nil {
			lines = m.agentListLines()
		} else if ask := m.activeChildAsk(); ask != nil && !m.decisionUngated() {
			lines = m.childAskPanelLines(ask)
		} else if m.completionActive() && m.attachedTo == "" {
			// The completion menu extends the input area (S-078).
			return min(inputHeight+len(m.completionMenuLines()), m.maxConfirmPanelHeight())
		}
	}
	if n := len(lines); n > inputHeight {
		return min(n, bound)
	}
	return inputHeight
}

// maxConfirmPanelHeight bounds how far the confirm panel may grow into the
// viewport (DESIGN-TUI.md §1: at most 40% of terminal height).
func (m Model) maxConfirmPanelHeight() int {
	return max(m.height*2/5, inputHeight)
}

// syncViewportHeight resizes the viewport when the bottom panel grows or
// shrinks (e.g. a diff preview replacing the input area).
func (m *Model) syncViewport() {
	if !m.ready {
		return
	}
	// Both dimensions can move without a resize: the chrome takes rows as
	// surfaces open and close, and the inspector rail takes columns whenever
	// the pane split toggles (S-092).
	h, w := m.viewportHeight(), m.transcriptWidth()
	if h == m.viewport.Height && w == m.viewport.Width {
		return
	}
	m.viewport.Height, m.viewport.Width = h, w
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
}
