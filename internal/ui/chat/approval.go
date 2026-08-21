package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/tools"
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
)

// approvalRequest is the head of the approval queue: one tool call awaiting
// the user's decision, with everything needed to preview and execute it.
type approvalRequest struct {
	call    provider.ToolCall
	kind    approvalKind
	command string     // approvalExec: the command handed to the runner
	title   string     // action headline, e.g. "edit main.go"
	diff    []diffLine // approvalDiff: the change to show
	summary string     // one-line description for transcript entries
}

// approvedToolDoneMsg carries the executor result of an approved non-exec
// tool call.
type approvedToolDoneMsg struct {
	runID  int
	result string
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
func (m Model) requiresApproval(name string) bool {
	if name == tools.ExecCommandName && m.runFn != nil {
		return true
	}
	if tools.IsMutating(name) {
		return true
	}
	_, ok := m.gatedTools[name]
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
			diff:    unifiedDiff(mut.OldText, mut.NewText),
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
			diff:    unifiedDiff(p.OldText, p.NewText),
			summary: title,
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
	}, nil
}

// advanceApprovalQueue shows the confirm prompt for the next queued
// approval-gated tool call, or resumes the model stream once the queue is
// empty.
func (m Model) advanceApprovalQueue() (tea.Model, tea.Cmd) {
	if len(m.approvalQueue) == 0 {
		m.pendingCalls = nil
		return m.resumeToolLoop()
	}
	tc := m.approvalQueue[0]
	req, err := m.buildApprovalRequest(tc)
	if err != nil {
		m.approvalQueue = m.approvalQueue[1:]
		m.pendingCalls = m.approvalQueue
		m.messages = append(m.messages, provider.Message{
			Role:       provider.RoleTool,
			Content:    "error: " + err.Error(),
			ToolCallID: tc.ID,
		})
		m.appendEntry(entry{kind: entrySystem, text: "Skipped a tool call with invalid arguments."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	m.pendingApproval = req
	if req.kind == approvalExec {
		m.pendingRun = req.command
	}
	// Session policy (S-054): an always-allowed category or an allowlisted
	// command skips the prompt; safety-flagged commands never do.
	if reason, ok := m.policyAllows(req); ok {
		m.appendEntry(entry{kind: entrySystem, text: "Auto-approved (" + reason + "): " + req.summary})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		if req.kind == approvalExec {
			return m.executeRun()
		}
		return m.executeApprovedTool()
	}
	m.state = stateConfirmRun
	m.syncViewportHeight()
	return m, nil
}

// declineApproval records an error tool result for the pending call and moves
// on to the next queued approval.
func (m Model) declineApproval() (tea.Model, tea.Cmd) {
	req := m.pendingApproval
	m.pendingApproval = nil
	m.pendingRun = ""
	content := "error: the user declined this tool call"
	if req.kind == approvalExec {
		content = "error: the user declined to run this command"
	}
	m.messages = append(m.messages, provider.Message{
		Role:       provider.RoleTool,
		Content:    content,
		ToolCallID: req.call.ID,
	})
	m.approvalQueue = m.approvalQueue[1:]
	m.pendingCalls = m.approvalQueue
	m.appendEntry(entry{kind: entrySystem, text: "Declined: " + req.summary})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m.advanceApprovalQueue()
}

// executeApprovedTool runs an approved non-exec tool call through the tool
// executor in the background; the result arrives as approvedToolDoneMsg.
func (m Model) executeApprovedTool() (tea.Model, tea.Cmd) {
	m.state = stateRunningCmd
	m.syncViewportHeight()
	runID := m.runID
	call := m.pendingApproval.call
	// Built-in mutating tools run through their own dispatcher; the session
	// executor (the auto-run read-only path) never learns them. A registered
	// gated tool keeps the session executor.
	executor := m.executor
	if _, registered := m.gatedTools[call.Name]; !registered && tools.IsMutating(call.Name) {
		executor = tools.ExecuteMutating
	}
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		var result string
		if executor != nil {
			out, err := executor(call.Name, json.RawMessage(call.Arguments))
			if err != nil {
				result = "error: " + err.Error()
			} else {
				result = out
			}
		} else {
			result = "error: no tool executor configured"
		}
		return approvedToolDoneMsg{runID: runID, result: result}
	})
}

// confirmLines builds the confirm prompt for the pending approval (or a /run
// confirmation), one rendered row per element.
func (m Model) confirmLines() []string {
	if m.pendingApproval != nil && m.pendingApproval.kind != approvalExec {
		return m.approvalPreviewLines()
	}
	label := "Run: "
	if m.pendingApproval != nil {
		label = "Assistant wants to run: "
	}
	lines := []string{userStyle.Render(label) + firstLine(m.pendingRun)}
	warnings := safety.Check(m.pendingRun)
	if len(warnings) > 0 {
		var risks []string
		for _, w := range warnings {
			risks = append(risks, w.Risk)
		}
		lines = append(lines, errorStyle.Render("⚠ "+strings.Join(risks, "; ")))
	}
	// [a] is offered only for assistant commands without safety warnings:
	// flagged actions can never be blanket-approved, and /run stays manual.
	prompt := "Run this command? [y/N]"
	if m.pendingApproval != nil && len(warnings) == 0 {
		prompt = "Run this command? [y/n/a]  (a: always allow commands this session)"
	}
	lines = append(lines, systemMsgStyle.Render(prompt))
	return lines
}

// approvalPreviewLines renders the diff or generic preview for the pending
// non-exec approval.
func (m Model) approvalPreviewLines() []string {
	req := m.pendingApproval
	lines := []string{userStyle.Render("Assistant wants to " + req.title)}
	switch req.kind {
	case approvalDiff:
		maxDiff := m.maxConfirmPanelHeight() - 2 // title + prompt rows
		lines = append(lines, renderDiffLines(req.diff, m.contentWidth(), maxDiff)...)
		lines = append(lines, systemMsgStyle.Render("Apply this change? [y/n/a]  (a: always allow edits this session)"))
	default:
		if req.summary != "" && req.summary != req.title {
			lines = append(lines, toolArgsStyle.Render(clipLine(firstLine(req.summary), m.contentWidth())))
		}
		lines = append(lines, systemMsgStyle.Render("Allow this? [y/N]"))
	}
	return lines
}

// renderConfirm renders the confirm prompt padded to the bottom panel height.
func (m Model) renderConfirm() string {
	lines := m.confirmLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// bottomPanelHeight is how many rows the bottom panel currently occupies; the
// confirm prompt may grow beyond the input's fixed height for diff previews.
func (m Model) bottomPanelHeight() int {
	if m.state == stateConfirmRun {
		if n := len(m.confirmLines()); n > inputHeight {
			return min(n, m.maxConfirmPanelHeight())
		}
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
func (m *Model) syncViewportHeight() {
	if !m.ready {
		return
	}
	if h := m.viewportHeight(); h != m.viewport.Height {
		m.viewport.Height = h
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
	}
}
