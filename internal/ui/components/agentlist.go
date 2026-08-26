package components

// The sub-agent manager (DESIGN-TUI.md §9a). S-077 made it a live list you
// could attach to, cancel and kill from; S-111 makes it the place a blocked
// child is answered. Opening the manager *because* something needs you and
// then being sent into that child's session just to say yes is a detour the
// list can spare you, so the approval card renders over the list and hands
// the list back.
//
// A row's progress is a fan-out lane's progress in list form: both read the
// same AgentProgress (§9g), so what the transcript says about a child and
// what the manager says about it cannot drift apart.

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
	AgentBlocked                   // ⚠ waiting on the user
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
	// Progress is the child's live progress, rendered by the fan-out lane's
	// renderer (§9g). Nil for a row with no child progress to draw — the
	// orchestrator, which is not a child — and those rows fall back to
	// Status and Spend.
	Progress *AgentProgress
	// Note is the line under the row: what a blocked child is waiting for,
	// why a failed one failed. `⚠ needs you` without saying what for sends
	// the reader looking, and so does `failed`.
	Note string
	// Answerable marks a blocked row whose pending approval can be answered
	// here; Retryable marks a failed row that can be run again on its
	// original task. Each gates a key, because a key offered where it does
	// nothing is not an offer.
	Answerable bool
	Retryable  bool
}

// AgentAction is what the user asked to do with the focused row.
type AgentAction int

const (
	AgentAttach AgentAction = iota // enter — attach to the agent's surface
	AgentCancel                    // x — cancel its current turn
	AgentKill                      // X — kill the agent
	AgentAnswer                    // a — answer its pending approval in place
	AgentRetry                     // r — run a failed agent again on its task
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

// focused is the row the keys act on.
func (l *AgentList) focused() AgentRow {
	if l.Focus < 0 || l.Focus >= len(l.Rows) {
		return AgentRow{}
	}
	return l.Rows[l.Focus]
}

// Update handles list keys. Cancel, kill, answer and retry resolve with
// done=false so the list stays open over the live view (the host performs the
// action and comes back); attach and esc dismiss it. [a] and [r] are silent
// on a row that does not offer them rather than reporting a failure the row
// already predicted.
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
	case "a":
		if l.focused().Answerable {
			return false, AgentListResult{Action: AgentAnswer, Index: l.Focus}
		}
	case "r":
		if l.focused().Retryable {
			return false, AgentListResult{Action: AgentRetry, Index: l.Focus}
		}
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
// usable. A child's glyph is the lane's, so the manager and the transcript
// mark the same child the same way; only the orchestrator's `●` is the
// list's own.
func (r AgentRow) stateGlyph() string {
	switch r.State {
	case AgentCurrent:
		return headlineStyle.Render("●")
	case AgentBlocked:
		return AgentProgress{State: FanoutBlocked}.glyph()
	case AgentDone:
		return AgentProgress{State: FanoutDone}.glyph()
	case AgentFailed:
		return AgentProgress{State: FanoutFailed}.glyph()
	default:
		return AgentProgress{State: FanoutRunning}.glyph()
	}
}

// rightField is what the row reports: the lane renderer's outcome field for a
// child, and the plain status and spend for a row that has no child progress.
func (r AgentRow) rightField() string {
	if r.Progress != nil {
		return r.Progress.outcomeField()
	}
	status := r.Status
	if r.State == AgentBlocked {
		status = errStyle.Render("⚠ " + status)
	} else {
		status = dimStyle.Render(status)
	}
	if r.Spend != "" {
		status += "  " + statusStyle.Render(r.Spend)
	}
	return status
}

// render lays one row out across the card's inner width, with its note (if
// any) indented underneath.
func (r AgentRow) render(inner int, focused bool) []string {
	right := r.rightField()
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
	if focused {
		row = focusRowStyle.Render("❯") + " " + row
	} else {
		row = "  " + row
	}
	rows := []string{row}
	if r.Note != "" {
		rows = append(rows, indented(r.Note, detailIndent, inner))
	}
	return rows
}

// hints are the keys the focused row offers. [a] and [r] appear only where
// the row can act on them, so the run states what this row can do rather than
// what the list can do in general.
func (l *AgentList) hints() []string {
	focus := l.focused()
	segments := []string{"enter attach"}
	if focus.Answerable {
		segments = append(segments, "a answer")
	}
	if focus.Retryable {
		segments = append(segments, "r retry")
	}
	return append(segments, "x cancel", "X kill", "esc back")
}

// tally is the manager's title-rail summary: the same sentence the fan-out
// header states, about the children this list holds. The orchestrator is not
// a child and is left out of it.
func (l *AgentList) tally() string {
	var states []FanoutState
	for _, r := range l.Rows {
		if r.Progress != nil {
			states = append(states, r.Progress.State)
		}
	}
	if len(states) == 0 {
		return ""
	}
	return stateTally(states)
}

func (l *AgentList) View(width int) string {
	inner := width - cardFrameWidth
	var rows []string
	for i, r := range l.Rows {
		rows = append(rows, r.render(inner, i == l.Focus)...)
	}
	rows = append(rows, hintRows(l.hints(), width)...)
	rows = boundRows(rows, l.MaxLines)
	chrome := cardChrome{title: "Agents"}
	if tally := l.tally(); tally != "" {
		chrome.chips = []string{tally}
	}
	return renderChromeCard(chrome, rows, width)
}
