package chat

// Slash-command autocomplete (S-078). Typing "/" plus a prefix in the input
// opens a completion menu under the textarea: ↑↓ moves, tab completes into the
// input, enter runs the highlighted command, esc dismisses. The menu is
// derived from a single command registry filtered by what this session
// actually has wired (no /save without a DB, no /agents without a
// supervisor), so it never offers a command that would answer "unavailable".
// Argument-level completion (subcommand names, branch/chat names) is S-079.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// slashCommand is one registry row: the primary name shown in the menu, an
// argument hint, a one-line description, alternate names that also match
// while filtering, and an optional availability predicate.
type slashCommand struct {
	name    string
	args    string
	desc    string
	aliases []string
	enabled func(*Model) bool
}

// slashCommands is the completion registry, in menu order. Descriptions are
// deliberately shorter than /help's — they share a row with the name.
var slashCommands = []slashCommand{
	{name: "/help", desc: "Show commands, keys, and the approval policy"},
	{name: "/clear", aliases: []string{"/new"}, desc: "Start a new conversation"},
	{name: "/copy", args: "[code]", desc: "Copy the last response (or just its code blocks)"},
	{name: "/run", args: "[n]", desc: "Run a code block from the last response",
		enabled: func(m *Model) bool { return m.runFn != nil }},
	{name: "/model", args: "[name]", desc: "Switch the model (bare /model opens a picker)"},
	{name: "/mode", args: "[name|why]", desc: "Set the permission mode (bare /mode opens a picker)"},
	{name: "/stats", desc: "Context occupancy and session spend"},
	{name: "/ui", args: "verbosity <low|med|high>", desc: "Activity feed density"},
	{name: "/sandbox", args: "[doctor|list|status|destroy|prune]", desc: "Containment status and container sandboxes"},
	{name: "/evidence", args: "[purge]", desc: "Tool-output evidence store",
		enabled: func(m *Model) bool { return m.evidence.Manage != nil }},
	{name: "/gate", args: "[run|result]", desc: "Run the project's quality gate",
		enabled: func(m *Model) bool { return m.gate.Manage != nil }},
	{name: "/ps", desc: "List session-owned long-running processes",
		enabled: func(m *Model) bool { return m.processes.Manage != nil }},
	{name: "/memory", args: "[list|add|forget]", desc: "Durable memories",
		enabled: func(m *Model) bool { return m.memory.Manage != nil }},
	{name: "/agents", desc: "Agent manager: attach, steer, cancel, kill (Ctrl+A)",
		enabled: func(m *Model) bool { return m.subagents != nil }},
	{name: "/plan", args: "save [name]", desc: "Save the last plan/response to .shhh/plans/"},
	{name: "/diff", desc: "Cumulative session diff, full screen",
		enabled: func(m *Model) bool { return m.sessionDiff != nil }},
	{name: "/compact", desc: "Summarize the conversation and continue from the summary"},
	{name: "/rewind", args: "[n]", desc: "Rewind to before a user turn (bare /rewind picks)"},
	{name: "/branches", args: "[n|name]", desc: "List or switch this session's branches",
		enabled: func(m *Model) bool { return m.db != nil }},
	{name: "/save", args: "[name]", desc: "Save this chat",
		enabled: func(m *Model) bool { return m.db != nil }},
	{name: "/load", args: "<name>", desc: "Load a saved chat",
		enabled: func(m *Model) bool { return m.db != nil }},
	{name: "/chats", desc: "List saved chats",
		enabled: func(m *Model) bool { return m.db != nil }},
	{name: "/exit", aliases: []string{"/quit", "/q"}, desc: "Quit (also /quit, /q)"},
}

// maxCompletionRows caps how many commands the menu shows at once; longer
// match lists scroll to keep the focused row visible.
const maxCompletionRows = 6

var (
	completeFocusStyle = lipgloss.NewStyle().Bold(true).Background(components.Palette.FocusBg)
	completeArgsStyle  = lipgloss.NewStyle().Foreground(components.Palette.Dim)
	completeDescStyle  = lipgloss.NewStyle().Foreground(components.Palette.Dim)
	completeHintStyle  = lipgloss.NewStyle().Foreground(components.Palette.Dim).Italic(true)
)

// matchesCommand reports whether the typed token ("/mo") is a prefix of the
// command's name or one of its aliases.
func (c slashCommand) matches(token string) bool {
	if strings.HasPrefix(c.name, token) {
		return true
	}
	for _, a := range c.aliases {
		if strings.HasPrefix(a, token) {
			return true
		}
	}
	return false
}

// syncCompletions recomputes the completion menu from the current input.
// Call it after every keypress that may have changed the input; every other
// path invalidates the menu implicitly because completeFor no longer matches
// the input value.
func (m *Model) syncCompletions() {
	val := m.input.Value()
	if m.completeDismissedFor != "" && val != m.completeDismissedFor {
		m.completeDismissedFor = ""
	}
	if m.state != stateInput || m.attachedTo != "" || m.agentList != nil || m.activeChildAsk() != nil ||
		!strings.HasPrefix(val, "/") || strings.ContainsAny(val, " \t\n") || val == m.completeDismissedFor {
		m.completions = nil
		m.completeFor = ""
		return
	}

	var prev string
	if m.completeIdx < len(m.completions) {
		prev = m.completions[m.completeIdx].name
	}

	// Exact name match ranks first so typing "/mode" in full never leaves
	// "/model" (or any longer sibling) highlighted.
	var matches []slashCommand
	for _, c := range slashCommands {
		if c.enabled != nil && !c.enabled(m) {
			continue
		}
		if !c.matches(val) {
			continue
		}
		if c.name == val {
			matches = append([]slashCommand{c}, matches...)
		} else {
			matches = append(matches, c)
		}
	}

	m.completions = matches
	m.completeFor = val
	m.completeIdx = 0
	// Keep the arrowed-to row focused across keystrokes — unless the typed
	// text now names a command exactly, which always wins the focus.
	if len(matches) == 0 || matches[0].name != val {
		for i, c := range matches {
			if c.name == prev {
				m.completeIdx = i
				break
			}
		}
	}
}

// completionActive reports whether the menu applies to the input right now; a
// stale menu (the input changed through a path that skipped syncCompletions,
// e.g. a reset) deactivates itself because completeFor no longer matches.
func (m Model) completionActive() bool {
	return m.state == stateInput && len(m.completions) > 0 &&
		m.completeFor != "" && m.completeFor == m.input.Value()
}

// dismissCompletions hides the menu until the input text changes again (esc).
func (m *Model) dismissCompletions() {
	m.completeDismissedFor = m.completeFor
	m.completions = nil
	m.completeFor = ""
}

// acceptCompletion writes the focused command into the input (tab); commands
// that take arguments get a trailing space so the user can keep typing.
func (m *Model) acceptCompletion() {
	c := m.completions[m.completeIdx]
	text := c.name
	if c.args != "" {
		text += " "
	}
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.syncCompletions()
}

// completionMenuLines renders the menu rows plus the hint line, bounded so
// the bottom panel (input + menu) stays within the confirm-panel height cap.
func (m Model) completionMenuLines() []string {
	if !m.completionActive() {
		return nil
	}
	width := m.contentWidth()
	// The input (inputHeight rows) plus the menu must fit the confirm-panel
	// cap; on very short terminals the hint line goes first, then rows.
	budget := max(m.maxConfirmPanelHeight()-inputHeight, 1)
	showHint := budget >= 2
	if showHint {
		budget--
	}
	visible := min(len(m.completions), maxCompletionRows, budget)
	start := 0
	if m.completeIdx >= visible {
		start = m.completeIdx - visible + 1
	}

	nameW := 0
	for _, c := range m.completions[start : start+visible] {
		if w := lipgloss.Width(plainCommandLabel(c)); w > nameW {
			nameW = w
		}
	}

	lines := make([]string, 0, visible+1)
	for i := start; i < start+visible; i++ {
		c := m.completions[i]
		plain := plainCommandLabel(c)
		pad := strings.Repeat(" ", max(nameW-lipgloss.Width(plain), 0))
		var row string
		if i == m.completeIdx {
			row = completeFocusStyle.Render(clipRow("❯ "+plain+pad+"  "+c.desc, width))
		} else {
			label := c.name
			if c.args != "" {
				label += " " + completeArgsStyle.Render(c.args)
			}
			row = clipRow("  "+label+pad+"  "+completeDescStyle.Render(c.desc), width)
		}
		lines = append(lines, row)
	}

	if !showHint {
		return lines
	}
	hint := "tab complete · enter run · ↑↓ move · esc dismiss"
	if len(m.completions) > visible {
		hint = fmt.Sprintf("%d/%d · %s", m.completeIdx+1, len(m.completions), hint)
	}
	return append(lines, completeHintStyle.Render(clipRow(hint, width)))
}

// plainCommandLabel is the unstyled name+args column used for alignment.
func plainCommandLabel(c slashCommand) string {
	if c.args == "" {
		return c.name
	}
	return c.name + " " + c.args
}

// clipRow truncates a possibly-styled row to the given display width.
func clipRow(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}
