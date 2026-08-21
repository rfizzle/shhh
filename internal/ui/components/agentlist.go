package components

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AgentState is one agent row's lifecycle state (DESIGN-TUI.md §9a).
type AgentState int

const (
	AgentCurrent AgentState = iota // ● the agent whose surface is shown
	AgentRunning                   // ◇ working
	AgentBlocked                   // ◇ ⚠ waiting on the user
	AgentDone                      // ✓ finished
	AgentFailed                    // ✗ failed
)

// AgentRow is one agent in the list: identity, task label, live status, and
// spend.
type AgentRow struct {
	State  AgentState
	Name   string
	Task   string
	Status string
	Spend  string
}

// AgentAction is what the user asked to do with the focused row.
type AgentAction int

const (
	AgentAttach AgentAction = iota // enter — attach to the agent's surface
	AgentCancel                    // x — cancel its current turn
	AgentKill                      // X — kill the agent
	AgentBack                      // esc — dismiss the list
)

// AgentListResult is the agent-list Update result.
type AgentListResult struct {
	Action AgentAction
	Index  int
}

// AgentList is the sub-agent manager list (§9a), following the selector
// visual language. The host keeps Rows current while the list is open — it is
// a live view.
type AgentList struct {
	Rows     []AgentRow
	Focus    int
	MaxLines int
}

// Update handles list keys. Cancel and kill resolve with done=false so the
// list stays open over the live view (the host performs the action); attach
// and esc dismiss it.
func (l *AgentList) Update(msg tea.KeyMsg) (done bool, result any) {
	switch msg.String() {
	case "up", "k":
		if l.Focus > 0 {
			l.Focus--
		}
	case "down", "j":
		if l.Focus < len(l.Rows)-1 {
			l.Focus++
		}
	case "enter":
		return true, AgentListResult{Action: AgentAttach, Index: l.Focus}
	case "x":
		return false, AgentListResult{Action: AgentCancel, Index: l.Focus}
	case "X":
		return false, AgentListResult{Action: AgentKill, Index: l.Focus}
	case "esc", "ctrl+c":
		return true, AgentListResult{Action: AgentBack, Index: -1}
	}
	return false, nil
}

// stateGlyph pairs every state with a glyph so monochrome terminals stay
// usable.
func (r AgentRow) stateGlyph() string {
	switch r.State {
	case AgentCurrent:
		return headlineStyle.Render("●")
	case AgentDone:
		return addStyle.Render("✓")
	case AgentFailed:
		return errStyle.Render("✗")
	default:
		return infoStyle.Render("◇")
	}
}

func (l *AgentList) View(width int) string {
	inner := width - cardFrameWidth
	var rows []string
	for i, r := range l.Rows {
		status := r.Status
		if r.State == AgentBlocked {
			status = errStyle.Render("⚠ " + status)
		} else {
			status = dimStyle.Render(status)
		}
		right := status
		if r.Spend != "" {
			right += "  " + statusStyle.Render(r.Spend)
		}
		left := r.stateGlyph() + " " + r.Name
		if r.Task != "" {
			left += "  " + dimmerStyle.Render(clip(r.Task, max(inner/3, 8)))
		}
		gap := inner - 2 - lipgloss.Width(left) - lipgloss.Width(right)
		row := left
		if gap >= 2 {
			row += strings.Repeat(" ", gap) + right
		} else {
			row = clip(left, max(inner-2-lipgloss.Width(right)-2, 0)) + "  " + right
		}
		if i == l.Focus {
			rows = append(rows, focusRowStyle.Render("❯")+" "+row)
		} else {
			rows = append(rows, "  "+row)
		}
	}
	rows = append(rows, hintRows([]string{"enter attach · x cancel · X kill · esc back"}, width)...)
	rows = boundRows(rows, l.MaxLines)
	return renderCard("Agents", rows, width)
}
