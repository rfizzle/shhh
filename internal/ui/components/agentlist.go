package components

// The sub-agent manager (docs/interface/surfaces.md#the-agent-manager). S-077
// made it a live list you could attach to, cancel and kill from; S-111 makes
// it the place a blocked child is answered. Opening the manager *because*
// something needs you and then being sent into that child's session just to
// say yes is a detour the list can spare you, so the approval card renders
// over the list and hands the list back.
//
// A row's progress is a fan-out lane's progress in list form: both read the
// same AgentProgress (§9g), so what the transcript says about a child and
// what the manager says about it cannot drift apart.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// AgentState is one agent row's lifecycle state
// (docs/interface/surfaces.md#the-agent-manager).
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
	// window is the shared sliding window (listwindow.go). A fan-out wide
	// enough to overflow this card is itself the problem the screen should be
	// showing, which is why the manager went unwindowed until S-124 — but a
	// list the pointer can walk off the bottom of is worse than a wide
	// fan-out, so it scrolls now, on the same code every other list uses.
	// Blocked children never scroll: they are pinned above the window, so
	// opening the manager because something needs you always shows you the
	// thing that does.
	window listWindow
}

// split divides the rows into the ones pinned above the window and the ones
// it scrolls. The pinned run is the head of the list while it is the current
// agent or a blocked child — which, given the sort the host owes this list
// (blocked children to the top, below the orchestrator, §9a), is exactly the
// orchestrator and everyone waiting on an answer. It is the leading run
// rather than every blocked row anywhere, because the component does not sort
// its own rows: a sort that happens in here is a sort nobody can check
// against the transcript, and a blocked child that ended up below the fold is
// a host that did not sort rather than a row this one should move.
func (l *AgentList) split() (pinned, scrolling []int) {
	i := 0
	for ; i < len(l.Rows); i++ {
		if s := l.Rows[i].State; s != AgentCurrent && s != AgentBlocked {
			break
		}
		pinned = append(pinned, i)
	}
	for ; i < len(l.Rows); i++ {
		scrolling = append(scrolling, i)
	}
	return pinned, scrolling
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
func (l *AgentList) Update(msg tea.KeyPressMsg) (done bool, result any) {
	switch pressed := msg.String(); {
	case pressed == "up", pressed == "k":
		if l.Focus > 0 {
			l.Focus--
		}
	case pressed == "down", pressed == "j":
		if l.Focus < len(l.Rows)-1 {
			l.Focus++
		}
	case keys.Is(pressed, keys.Agent.Attach):
		return true, AgentListResult{Action: AgentAttach, Index: l.Focus}
	case keys.Is(pressed, keys.Agent.Answer):
		if l.focused().Answerable {
			return false, AgentListResult{Action: AgentAnswer, Index: l.Focus}
		}
	case keys.Is(pressed, keys.Agent.Retry):
		if l.focused().Retryable {
			return false, AgentListResult{Action: AgentRetry, Index: l.Focus}
		}
	case keys.Is(pressed, keys.Agent.Cancel):
		return false, AgentListResult{Action: AgentCancel, Index: l.Focus}
	case keys.Is(pressed, keys.Agent.Kill):
		return false, AgentListResult{Action: AgentKill, Index: l.Focus}
	case keys.Is(pressed, keys.Agent.Back):
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
		return sty.Headline.Render("●")
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
		status = sty.Err.Render("⚠ " + status)
	} else {
		status = sty.Dim.Render(status)
	}
	if r.Spend != "" {
		status += "  " + sty.Status.Render(r.Spend)
	}
	return status
}

// render lays one row out across the card's inner width, with its note (if
// any) indented underneath.
func (r AgentRow) render(inner int, focused bool) []string {
	right := r.rightField()
	left := r.stateGlyph() + " " + r.Name
	if r.Task != "" {
		left += "  " + sty.Dimmer.Render(clip(r.Task, max(inner/3, 8)))
	}
	gap := inner - 2 - lipgloss.Width(left) - lipgloss.Width(right)
	row := left
	if gap >= 2 {
		row += strings.Repeat(" ", gap) + right
	} else {
		row = clip(left, max(inner-2-lipgloss.Width(right)-2, 0)) + "  " + right
	}
	if focused {
		row = sty.FocusRow.Render("❯") + " " + row
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
	segments := []string{offer(keys.Agent.Attach)}
	if focus.Answerable {
		segments = append(segments, offer(keys.Agent.Answer))
	}
	if focus.Retryable {
		segments = append(segments, offer(keys.Agent.Retry))
	}
	return append(segments, offer(keys.Agent.Cancel), offer(keys.Agent.Kill), offer(keys.Agent.Back))
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

// visibleRows renders the scrolling half of the list windowed to a body
// budget, with the markers the window makes necessary. An agent is one row
// plus the line under it that says what it is waiting for or why it failed,
// and every agent is a row the pointer can land on, so the markers count
// agents rather than lines.
func (l *AgentList) visibleRows(width, budget int, scrolling []int) []string {
	inner := width - cardFrameWidth
	n := len(scrolling)
	focus := -1
	for pos, i := range scrolling {
		if i == l.Focus {
			focus = pos
		}
	}
	g := listGeometry{
		n:     n,
		focus: focus,
		height: func(pos int) int {
			if l.Rows[scrolling[pos]].Note != "" {
				return 2
			}
			return 1
		},
		counts: func(int) bool { return true },
	}
	lo, hi := l.window.rangeFor(g, budget)
	var rows []string
	if lo > 0 {
		rows = append(rows, listOverflowRow("↑", lo, "", width))
	}
	for pos := lo; pos < hi; pos++ {
		i := scrolling[pos]
		rows = append(rows, l.Rows[i].render(inner, i == l.Focus)...)
	}
	if hi < n {
		rows = append(rows, listOverflowRow("↓", n-hi, "", width))
	}
	return rows
}

func (l *AgentList) View(width int) string {
	inner := width - cardFrameWidth
	// The key hints and the pinned blocked children come off the budget
	// before the window is drawn: the list scrolls under them, and the window
	// may never buy itself a row (§4a).
	hints := hintRows(l.hints(), width)
	pinned, scrolling := l.split()
	var rows []string
	for _, i := range pinned {
		rows = append(rows, l.Rows[i].render(inner, i == l.Focus)...)
	}
	rows = append(rows, l.visibleRows(width, bodyBudget(l.MaxLines, len(hints)+len(rows)), scrolling)...)
	rows = append(rows, hints...)
	rows = boundRows(rows, l.MaxLines)
	chrome := cardChrome{title: "Agents"}
	if tally := l.tally(); tally != "" {
		chrome.chips = []string{tally}
	}
	return renderChromeCard(chrome, rows, width)
}
