package chat

// Slash-command autocomplete (S-078). Typing "/" plus a prefix in the input
// opens a completion menu under the textarea: ↑↓ moves, tab completes into the
// input, enter runs the highlighted command, esc dismisses. The menu is
// derived from a single command registry filtered by what this session
// actually has wired (no /save without a DB, no /agents without a
// supervisor), so it never offers a command that would answer "unavailable".
//
// Completion continues past the command name (S-079): each registry row
// carries argument specs — static subcommand lists, or dynamic sources read
// once when the menu opens on that position — and the menu re-filters on the
// token under the cursor. See completeargs.go.

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
	// key is the binding that reaches the command without typing it, shown
	// beside the name in the palette (S-112). Empty means the command has
	// none.
	key string
	// argSpecs describes the command's positional arguments (S-079), one
	// spec per position; positions past the list are free-form and get no
	// menu.
	argSpecs []argSpec
	// idleOnly is why the command needs the session's own turn to be
	// finished — it rewrites or replaces the conversation the agent is
	// working in. Empty means it runs while the agent works (S-087), which
	// is the default: inspecting and steering a running session is the point
	// of having one. An idle-only command drops out of the menu for the
	// duration, the way an unwired one never appears at all.
	idleOnly string
}

// idleOnlyReason reports why a command cannot run mid-turn, if it cannot.
func idleOnlyReason(name string) (string, bool) {
	for _, c := range slashCommands {
		if c.idleOnly == "" {
			continue
		}
		if c.name == name {
			return c.idleOnly, true
		}
		for _, a := range c.aliases {
			if a == name {
				return c.idleOnly, true
			}
		}
	}
	return "", false
}

// completionItem is one menu row: either a command name (the args column
// holds its hint) or a value for the argument under the cursor. name is what
// tab writes into the input; space asks for a trailing space because more
// can follow.
type completionItem struct {
	name  string
	args  string
	desc  string
	space bool
}

// slashCommands is the completion registry, in menu order. Descriptions are
// deliberately shorter than /help's — they share a row with the name.
var slashCommands = []slashCommand{
	{name: "/help", desc: "Show commands, keys, and the approval policy"},
	{name: "/clear", aliases: []string{"/new"}, desc: "Start a new conversation",
		idleOnly: "it starts a new conversation"},
	{name: "/paste", args: "[path|clear]", desc: "Attach the clipboard, or a file, to your next message",
		key:      "ctrl+v",
		argSpecs: staticArgs(argOption{"clear", "Drop what is staged"})},
	{name: "/copy", args: "[code]", desc: "Copy the last response (or just its code blocks)",
		argSpecs: staticArgs(argOption{"code", "Only the code blocks"})},
	{name: "/run", args: "[n]", desc: "Run a code block from the last response",
		enabled:  func(m *Model) bool { return m.runFn != nil },
		idleOnly: "it runs a command in this session"},
	{name: "/model", args: "[name]", desc: "Switch the model (bare /model opens a picker)",
		argSpecs: []argSpec{{dynamic: modelArgs, fuzzy: true}},
		idleOnly: "it switches the model the running turn is using"},
	{name: "/permissions", args: "[name|grants|allow|revoke|why]", desc: "What runs without asking, and the mode that frames it",
		aliases: []string{"/perms", "/mode"},
		key:     "shift+tab",
		argSpecs: []argSpec{
			{dynamic: modeArgs},
			{after: []string{"allow"}, options: []argOption{
				{"commands", "Every command runs without asking"},
				{"edits", "Every edit applies without asking"},
			}},
			{after: []string{"revoke"}, options: []argOption{
				{"edits", "Only the edit grants"},
				{"commands", "Only the command grants"},
			}},
		}},
	{name: "/reasoning", args: "[off|low|medium|high|default]", desc: "How much the model thinks before it answers (Ctrl+R cycles)",
		aliases: []string{"/think"},
		key:     reasoningKey,
		argSpecs: []argSpec{
			{dynamic: reasoningArgs},
			{after: []string{"default"}, options: reasoningLevelArgs()},
		}},
	{name: "/stats", desc: "Context occupancy and session spend"},
	{name: "/ui", args: "verbosity <low|normal|high> | mono <on|off>", desc: "Activity feed density and monochrome mode",
		argSpecs: []argSpec{
			{options: []argOption{
				{"verbosity", "Activity feed density"},
				{"mono", "Strip every surface to two greys"},
			}},
			{after: []string{"verbosity"}, options: []argOption{
				{"low", "Step headers only"},
				{"normal", "Read-only calls folded"},
				{"high", "Every row expanded"},
			}},
			{after: []string{"mono"}, options: []argOption{
				{"on", "Two greys — glyphs and words carry every state"},
				{"off", "The full palette"},
			}},
		}},
	{name: "/sandbox", args: "[doctor|list|status|destroy|prune]", desc: "Containment status and container sandboxes",
		argSpecs: staticArgs(
			argOption{"doctor", "Report containment support"},
			argOption{"list", "List container sandboxes"},
			argOption{"status", "This session's sandbox"},
			argOption{"destroy", "Destroy a sandbox by id"},
			argOption{"prune", "Remove stopped sandboxes"},
		)},
	{name: "/evidence", args: "[purge]", desc: "Tool-output evidence store",
		enabled:  func(m *Model) bool { return m.evidence.Manage != nil },
		argSpecs: staticArgs(argOption{"purge", "Delete stored tool output"})},
	{name: "/gate", args: "[run|result]", desc: "Run the project's quality gate",
		enabled: func(m *Model) bool { return m.gate.Manage != nil },
		argSpecs: staticArgs(
			argOption{"run", "Run the gate suites"},
			argOption{"result", "Show the last result"},
		)},
	{name: "/ps", desc: "List session-owned long-running processes",
		enabled: func(m *Model) bool { return m.processes.Manage != nil }},
	{name: "/memory", args: "[list|add|forget]", desc: "Durable memories",
		enabled: func(m *Model) bool { return m.memory.Manage != nil },
		argSpecs: staticArgs(
			argOption{"list", "Show stored memories"},
			argOption{"add", "Remember something"},
			argOption{"forget", "Drop a memory by id"},
		)},
	{name: "/agents", desc: "Agent manager: attach, steer, cancel, kill (Ctrl+A)",
		key:     "ctrl+a",
		enabled: func(m *Model) bool { return m.subagents != nil }},
	{name: "/attach", args: "[name]", desc: "Attach to an agent's session and steer it",
		enabled:  func(m *Model) bool { return m.subagents != nil },
		argSpecs: []argSpec{{dynamic: agentArgs, fuzzy: true}}},
	{name: "/detach", desc: "Back to the orchestrator (also esc)",
		enabled: func(m *Model) bool { return m.subagents != nil && m.attachedTo != "" }},
	{name: "/plan", args: "[save|drop]", desc: "The approved plan as a checklist, with anything that has departed from it",
		argSpecs: staticArgs(
			argOption{"save", "Write the last plan/response to .shhh/plans/"},
			argOption{"drop", "Forget the approved plan; steps go back to inferred"},
		)},
	{name: "/diff", desc: "Cumulative session diff, full screen",
		enabled: func(m *Model) bool { return m.changes != nil }},
	{name: "/review", args: "[turn]", desc: "Review what a turn changed — files, hunks, staging",
		enabled:  func(m *Model) bool { return m.changes != nil },
		argSpecs: []argSpec{{dynamic: reviewTurnArgs}}},
	{name: "/undo", args: "[turn]", desc: "Put back what a turn changed (asks first)",
		enabled:  func(m *Model) bool { return m.changes != nil },
		argSpecs: []argSpec{{dynamic: reviewTurnArgs}},
		idleOnly: "it writes files the running turn may be editing"},
	{name: "/compact", desc: "Continue from a summary plus the most recent turns",
		idleOnly: "it rewrites the conversation into a summary"},
	{name: "/rewind", args: "[n]", desc: "Rewind to before a user turn (bare /rewind picks)",
		argSpecs: []argSpec{{dynamic: checkpointArgs}},
		idleOnly: "it rewinds the conversation"},
	{name: "/branches", args: "[n|name]", desc: "Switch this session's branches (bare /branches picks)",
		enabled:  func(m *Model) bool { return m.db != nil },
		argSpecs: []argSpec{{dynamic: branchArgs, fuzzy: true}},
		idleOnly: "it switches the conversation to another branch"},
	{name: "/save", args: "[name]", desc: "Save this chat",
		enabled: func(m *Model) bool { return m.db != nil }},
	{name: "/load", args: "[name]", desc: "Load a saved chat (bare /load picks)",
		enabled:  func(m *Model) bool { return m.db != nil },
		argSpecs: []argSpec{{dynamic: chatArgs, fuzzy: true}},
		idleOnly: "it replaces the conversation"},
	{name: "/chats", desc: "Saved chats — enter to load",
		enabled:  func(m *Model) bool { return m.db != nil },
		idleOnly: "it opens the picker that replaces the conversation"},
	{name: "/exit", aliases: []string{"/quit", "/q"}, desc: "Quit (also /quit, /q)", key: "ctrl+d"},
}

// maxCompletionRows caps how many commands the menu shows at once; longer
// match lists scroll to keep the focused row visible.
const maxCompletionRows = 6

var (
	completeFocusStyle lipgloss.Style
	completeArgsStyle  lipgloss.Style
	completeDescStyle  lipgloss.Style
	completeHintStyle  lipgloss.Style
)

// applyCompleteStyles rebuilds the menu's styles from the palette (S-095).
func applyCompleteStyles(p components.ColorTokens) {
	completeFocusStyle = lipgloss.NewStyle().Bold(true).Background(p.FocusBg)
	completeArgsStyle = lipgloss.NewStyle().Foreground(p.Dim)
	completeDescStyle = lipgloss.NewStyle().Foreground(p.Dim)
	completeHintStyle = lipgloss.NewStyle().Foreground(p.Dim).Italic(true)
}

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

// namesExactly reports whether the token is this command's own name or one of
// its aliases written in full.
func (c slashCommand) namesExactly(token string) bool {
	if c.name == token {
		return true
	}
	for _, a := range c.aliases {
		if a == token {
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
	if !m.inputLive() || m.attachedTo != "" || m.agentList != nil || m.activeChildAsk() != nil ||
		!strings.HasPrefix(val, "/") || strings.ContainsAny(val, "\t\n") || val == m.completeDismissedFor {
		m.clearCompletions()
		return
	}

	var prev string
	if m.completeIdx < len(m.completions) {
		prev = m.completions[m.completeIdx].name
	}

	prior, token, start, end := tokenAtCursor(val, m.inputCursor())
	var matches []completionItem
	if len(prior) == 0 {
		matches = m.commandMatches(token)
	} else {
		matches = m.argumentMatches(prior, token)
	}
	if len(matches) == 0 {
		m.clearCompletions()
		return
	}

	m.completions = matches
	m.completeFor = val
	m.completeArg = len(prior) > 0
	m.completeStart = start
	m.completeEnd = end
	m.completeIdx = 0
	// Keep the arrowed-to row focused across keystrokes — unless the typed
	// text now names a candidate exactly, which always wins the focus.
	if !exactlyNamed(m, token) {
		for i, c := range matches {
			if c.name == prev {
				m.completeIdx = i
				break
			}
		}
	}
}

// exactlyNamed reports whether the typed token is some available command's
// name or alias in full — the case that always wins the menu's focus.
func exactlyNamed(m *Model, token string) bool {
	c, ok := lookupCommand(m, token)
	return ok && c.namesExactly(token)
}

// commandMatches are the available commands whose name or an alias starts
// with the typed token. An exact match ranks first so typing "/permissions"
// in full never leaves "/model" (or any longer sibling) highlighted — and an
// alias typed in full counts, because a reader who typed "/mode" from muscle
// memory has named a command exactly, whatever the menu calls it.
func (m *Model) commandMatches(token string) []completionItem {
	var matches []completionItem
	for _, c := range slashCommands {
		if c.enabled != nil && !c.enabled(m) {
			continue
		}
		// A command that needs an idle session is unavailable, not hidden
		// forever: it comes back when the turn ends (S-087).
		if c.idleOnly != "" && m.working() {
			continue
		}
		if !c.matches(token) {
			continue
		}
		item := completionItem{name: c.name, args: c.args, desc: c.desc, space: c.args != ""}
		if c.namesExactly(token) {
			matches = append([]completionItem{item}, matches...)
		} else {
			matches = append(matches, item)
		}
	}
	return matches
}

// argumentMatches are the candidates for the argument under the cursor:
// prior[0] names the command, and the remaining prior tokens fix the
// position. Free-form positions (a chat name to save, a memory body) have no
// spec and so no menu (S-079).
func (m *Model) argumentMatches(prior []string, token string) []completionItem {
	c, ok := lookupCommand(m, prior[0])
	if !ok {
		return nil
	}
	spec, ok := argSpecFor(c, len(prior)-1, prior)
	if !ok {
		return nil
	}
	opts := filterArgs(m.argCandidates(c.name, len(prior)-1, spec), token, spec.fuzzy)
	// A trailing space only helps when another argument can follow — counted
	// in positions, since gated alternatives share one.
	more := argPositions(c) > len(prior)
	items := make([]completionItem, len(opts))
	for i, o := range opts {
		items[i] = completionItem{name: o.value, desc: o.desc, space: more}
	}
	return items
}

// clearCompletions hides the menu and drops the dynamic-source cache, so the
// next menu re-reads branch and chat names rather than showing stale ones.
func (m *Model) clearCompletions() {
	m.completions = nil
	m.completeFor = ""
	m.completeArg = false
	m.argCache = nil
	m.argCacheFor = ""
}

// completionActive reports whether the menu applies to the input right now; a
// stale menu (the input changed through a path that skipped syncCompletions,
// e.g. a reset) deactivates itself because completeFor no longer matches.
func (m Model) completionActive() bool {
	return m.inputLive() && len(m.completions) > 0 &&
		m.completeFor != "" && m.completeFor == m.input.Value()
}

// dismissCompletions hides the menu until the input text changes again (esc).
func (m *Model) dismissCompletions() {
	dismissed := m.completeFor
	m.clearCompletions()
	m.completeDismissedFor = dismissed
}

// acceptCompletion writes the focused candidate into the input (tab),
// replacing only the token under the cursor (S-079). Candidates that can be
// followed by more text get a trailing space so the user can keep typing.
func (m *Model) acceptCompletion() {
	c := m.completions[m.completeIdx]
	r := []rune(m.input.Value())
	start, end := m.completeStart, min(m.completeEnd, len(r))
	text := c.name
	cursor := start + len([]rune(text))
	if c.space {
		if end < len(r) && r[end] == ' ' {
			// Step over the space that is already there rather than doubling it.
			cursor++
		} else {
			text += " "
			cursor = start + len([]rune(text))
		}
	}
	m.input.SetValue(string(r[:start]) + text + string(r[end:]))
	m.input.SetCursor(cursor)
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
func plainCommandLabel(c completionItem) string {
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
