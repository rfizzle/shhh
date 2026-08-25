package chat

// Plan mode's in-session flow (S-061): while the session is in plan mode the
// stream request carries planning instructions and the mode policy in
// internal/agent refuses gated calls (waving through read-only inspection
// commands). When the model finishes a planning response, the plan-approval
// prompt takes over the input area: the user executes the plan in a chosen
// mode, keeps planning, or rejects it — all in the same session.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
)

// planApprovedMessage is the user-role message that turns an approved plan
// into the execution turn, with the plan already in context.
const planApprovedMessage = "The plan is approved. Leave planning and execute it now, using your tools."

// planApproveOptions are the plan-approval prompt rows, in order; selection
// is by index (see selectPlanOption).
var planApproveOptions = []string{
	"Execute plan (accept-edits mode)",
	"Execute plan (auto mode)",
	"Execute plan (manual approvals)",
	"Keep planning — tell me what to change",
	"Reject plan",
}

// updatePlanApprove handles keys while the plan-approval prompt is showing.
func (m Model) updatePlanApprove(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key := msg.String(); key {
	case "up", "k":
		if m.planChoice > 0 {
			m.planChoice--
		}
		return m, nil
	case "down", "j":
		if m.planChoice < len(planApproveOptions)-1 {
			m.planChoice++
		}
		return m, nil
	case "enter":
		return m.selectPlanOption(m.planChoice)
	case "1", "2", "3", "4", "5":
		return m.selectPlanOption(int(key[0] - '1'))
	case "esc", "ctrl+c":
		// Esc never destroys: dismissing the prompt keeps planning.
		return m.keepPlanning()
	case "ctrl+d":
		m.quitting = true
		return m, m.quitCmd()
	}
	return m, nil
}

func (m Model) selectPlanOption(idx int) (tea.Model, tea.Cmd) {
	switch idx {
	case 0:
		return m.approvePlan(agent.ModeAcceptEdits)
	case 1:
		return m.approvePlan(agent.ModeAuto)
	case 2:
		return m.approvePlan(agent.ModeManual)
	case 3:
		return m.keepPlanning()
	case 4:
		return m.rejectPlan()
	}
	return m, nil
}

// approvePlan continues the same session straight into execution: the mode
// switches to the chosen one and the approval message becomes the next user
// turn, with the plan already in context.
func (m Model) approvePlan(execMode agent.Mode) (tea.Model, tea.Cmd) {
	m.applyMode(execMode)
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("Plan approved — executing in %s mode.", execMode)})
	m.recordCheckpoint(planApprovedMessage)
	m.agent.StartTurn(planApprovedMessage)
	m.appendEntry(entry{kind: entryUser, text: planApprovedMessage})
	m.trimForRequest()
	m.syncViewport()
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, tea.Batch(m.spinner.Tick, m.requestStream(), m.autosaveCmd())
}

// keepPlanning dismisses the prompt so the user can send feedback; the
// session stays in plan mode.
func (m Model) keepPlanning() (tea.Model, tea.Cmd) {
	m.setTurnState(stateInput)
	m.syncViewport()
	m.appendEntry(entry{kind: entrySystem, text: "Keep planning — describe what to change and the agent will revise the plan."})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, nil
}

// rejectPlan discards the prompt; the session stays in plan mode and the plan
// remains in the transcript for reference.
func (m Model) rejectPlan() (tea.Model, tea.Cmd) {
	m.setTurnState(stateInput)
	m.syncViewport()
	m.appendEntry(entry{kind: entrySystem, text: "Plan rejected. Still in plan mode — give new directions, or switch modes with Shift+Tab or /mode."})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, nil
}

// planApproveLines builds the plan-approval prompt (DESIGN-TUI.md §4a), one
// rendered row per element.
func (m Model) planApproveLines() []string {
	lines := []string{userStyle.Render("Plan ready — how should I proceed?")}
	for i, opt := range planApproveOptions {
		row := fmt.Sprintf("  %d. %s", i+1, opt)
		if i == m.planChoice {
			row = planSelectedStyle.Render(fmt.Sprintf("❯ %d. %s", i+1, opt))
		}
		lines = append(lines, row)
	}
	lines = append(lines, systemMsgStyle.Render("↑↓/jk move · enter select · 1-5 jump · esc keep planning"))
	return lines
}

// renderPlanApprove renders the plan-approval prompt padded to the bottom
// panel height.
func (m Model) renderPlanApprove() string {
	lines := m.planApproveLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// savePlan writes the plan text to .shhh/plans/<name>.md (an empty name gets
// a timestamp) and returns the written path. Saving is optional — the
// approval flow never requires it.
func savePlan(plan, name string) (string, error) {
	name = sanitizePlanName(strings.TrimSuffix(name, ".md"))
	dir := filepath.Join(".shhh", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".md")
	if !strings.HasSuffix(plan, "\n") {
		plan += "\n"
	}
	if err := os.WriteFile(path, []byte(plan), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// sanitizePlanName keeps plan names filesystem-safe: letters, digits, dot,
// dash, underscore; anything else becomes a dash. Empty (or all-unsafe) names
// fall back to a timestamp.
func sanitizePlanName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.TrimLeft(b.String(), ".-")
	if out == "" {
		out = "plan-" + time.Now().Format("20060102-150405")
	}
	return out
}
