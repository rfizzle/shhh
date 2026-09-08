package chat

// Slash-command autocomplete. Typing "/" plus a prefix in the input opens a
// completion menu under the textarea: ↑↓ moves, tab completes into the input,
// enter runs the highlighted command, esc dismisses. The menu is derived from
// a single command registry filtered by what this session actually has wired
// (no /save without a DB, no /agents without a supervisor), so it never
// offers a command that would answer "unavailable".
//
// Completion continues past the command name: each registry row
// carries argument specs — static subcommand lists, or dynamic sources read
// once when the menu opens on that position — and the menu re-filters on the
// token under the cursor. See completeargs.go.

import (
	"fmt"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	// beside the name in the palette. Empty means the command has
	// none.
	key string
	// argSpecs describes the command's positional arguments, one
	// spec per position; positions past the list are free-form and get no
	// menu.
	argSpecs []argSpec
	// idleOnly is why the command needs the session's own turn to be
	// finished — it rewrites or replaces the conversation the agent is
	// working in. Empty means it runs while the agent works, which
	// is the default: inspecting and steering a running session is the point
	// of having one. An idle-only command stays in the menu for the duration,
	// greyed behind ⊘ with idleOnlyMeta beside it; an unwired one never
	// appears at all, because it is not a command this session has.
	idleOnly string
}

// idleOnlyMeta is the short field a menu puts at the end of a row it cannot
// offer yet. The registry's own reason is a sentence naming what the command
// would disturb, and a sentence does not fit a right-aligned column beside
// twenty other rows — the sentence is what the notice says when the row is
// taken anyway, so the two are the same fact at two lengths.
const idleOnlyMeta = "idle only"

// idleOnlyReason reports why a command cannot run mid-turn, if it cannot.
func idleOnlyReason(name string) (string, bool) {
	for _, c := range slashCommands() {
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
	// off is why the row cannot be run right now, in the words the row shows
	// (idleOnlyMeta). Empty means it can. The row is still completed and still
	// run: running it is how the session says the longer reason out loud.
	off string
}

var (
	slashOnce  sync.Once
	slashTable []slashCommand
)

// slashCommands is the completion registry, in menu order. Descriptions are
// deliberately shorter than /help's — they share a row with the name.
//
// It is built on first use rather than at initialisation because a row's
// argument list is resolved against the session — the width it is drawn at,
// the attachments it offers — and the session asks the overlay register which
// mode owns the screen (overlay.go), which reads a table built the same way.
// A package-level registry closes that loop into an initialisation cycle.
func slashCommands() []slashCommand {
	slashOnce.Do(func() { slashTable = buildSlashCommands() })
	return slashTable
}

func buildSlashCommands() []slashCommand {
	return []slashCommand{
		{name: "/help", desc: "Show commands, keys, and the approval policy"},
		// Not idleOnly, though it replaces the conversation: a turn that is not
		// over is exactly when ending the session is worth asking about, so the
		// command stays offered mid-turn and answers with the confirm quitting
		// draws (cancel.go).
		{name: "/clear", aliases: []string{"/new"}, desc: "Start a new session"},
		{name: "/paste", args: "[path|show <name>|drop [name]|clear]", desc: "Attach the clipboard, or a file, to your next message",
			key: keys.Shown(keys.Draft.Attach),
			argSpecs: []argSpec{
				{options: []argOption{
					{"show", "Look at a staged image"},
					{"drop", "Take one attachment back out"},
					{"clear", "Drop what is staged"},
				}},
				{after: []string{"drop"}, dynamic: attachmentDropArgs},
				{after: []string{"show"}, dynamic: attachmentShowArgs},
			}},
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
			key:     keys.Shown(keys.Draft.Mode),
			argSpecs: []argSpec{
				{dynamic: modeArgs},
				{after: []string{"allow"}, options: []argOption{
					{"commands", "Every command runs without asking"},
					{"edits", "Every edit applies without asking"},
				}},
				{after: []string{"revoke"}, options: []argOption{
					{"edits", "Only the edit grants"},
					{"commands", "Only the command grants"},
					{"hosts", "Only the fetch host grants"},
				}},
			}},
		{name: "/reasoning", args: "[off|low|medium|high|xhigh|max|default]", desc: "How much the model thinks before it answers",
			aliases: []string{"/think"},
			key:     keys.Shown(keys.Draft.Reasoning),
			argSpecs: []argSpec{
				{dynamic: reasoningArgs},
				{after: []string{"default"}, options: reasoningLevelArgs()},
			}},
		{name: "/context", desc: "The window as a meter, itemised down to the tool"},
		{name: "/stats", desc: "Context occupancy and session spend"},
		{name: "/step", desc: "Open the in-flight step's detail (again closes it)"},
		{name: "/status", desc: "Where the session is, and whether it is still on target"},
		{name: "/trust", desc: "Let this checkout's skills, agent profiles and quality suites load (\"off\" withdraws it)"},
		{name: "/ui", args: "verbosity <low|normal|high> | mono <on|off>", desc: "Activity feed density and monochrome mode",
			argSpecs: []argSpec{
				{options: []argOption{
					{"verbosity", "Activity feed density"},
					{"theme", "Which colour table every surface draws with"},
					{"ground", "Paint the screen with the theme's own background"},
					{"mono", "Strip every surface to two greys"},
					{"mouse", "Whether shhh or the terminal owns the mouse"},
					{"notify", "Say so when a turn stops and you are elsewhere"},
					{"title", "Name an unnamed session after its first turn"},
					{"window", "Name the terminal's own tab after this session"},
					{"rail", "How many columns the inspector rail takes"},
					{"terminal", "What this terminal can do"},
				}},
				{after: []string{"verbosity"}, options: []argOption{
					{"low", "Step headers only"},
					{"normal", "Read-only calls folded"},
					{"high", "Every row expanded"},
				}},
				{after: []string{"theme"}, options: []argOption{
					{components.ThemeAuto, "The table chosen for the background this terminal reports"},
					{components.ThemeDark, "The product's own colours, on a dark ground"},
					{components.ThemeLight, "The same fifteen jobs, on a light ground"},
					{components.ThemeCharm, "The same fifteen jobs in CharmTone"},
				}},
				{after: []string{"ground"}, options: []argOption{
					{"on", "Paint the background the theme was drawn against"},
					{"off", "Leave the terminal's own background"},
				}},
				{after: []string{"mono"}, options: []argOption{
					{"on", "Two greys — glyphs and words carry every state"},
					{"off", "The full palette"},
				}},
				{after: []string{"mouse"}, options: []argOption{
					{"on", "The wheel scrolls, click-drag selects, a click opens a row"},
					{"off", "The terminal keeps its own click-drag selection"},
				}},
				{after: []string{"notify"}, options: []argOption{
					{"on", "One notification when a turn stops and the window is not in front"},
					{"off", "A turn that stops while you are elsewhere waits silently"},
				}},
				{after: []string{"title"}, options: []argOption{
					{"on", "The summary model names the session after its first turn"},
					{"off", "Sessions keep the timestamp they were opened at"},
				}},
				{after: []string{"window"}, options: []argOption{
					{"on", "The tab says the command, the directory, and ⏸ while a decision waits"},
					{"off", "The tab keeps whatever your terminal puts there"},
				}},
				{after: []string{"rail"}, dynamic: railArgs},
			}},
		// The whole settings file, where /ui is the handful of its keys a
		// session flips often enough to have a word for. Not idleOnly: it
		// stages edits to your own config file and writes none of them until
		// [w] (config.go).
		{name: "/config", desc: "Every setting, where its value came from, and what changing it costs",
			enabled: func(m *Model) bool { return m.openConfig != nil }},
		{name: "/add-dir", args: "[<path>|drop <path>]", desc: "The directories this session may work in",
			enabled: func(m *Model) bool { return m.scope != nil },
			argSpecs: []argSpec{
				{options: []argOption{{"drop", "Take a directory back out of the scope"}}},
				{after: []string{"drop"}, dynamic: scopeDropArgs},
			}},
		{name: "/sandbox", args: "[doctor|scope|list|status|destroy|prune]", desc: "Containment status and container sandboxes",
			enabled: func(m *Model) bool { return m.codingSurfaces() },
			argSpecs: staticArgs(
				argOption{"doctor", "Report containment support"},
				argOption{"scope", "The directories commands may write to"},
				argOption{"list", "List container sandboxes"},
				argOption{"status", "This session's sandbox"},
				argOption{"destroy", "Destroy a sandbox by id"},
				argOption{"prune", "Remove stopped sandboxes"},
			)},
		{name: "/sources", desc: "What this session read: every fetch and search, by host",
			enabled: func(m *Model) bool { return m.sourceLedger != nil }},
		{name: "/evidence", args: "[purge]", desc: "Tool-output evidence store",
			enabled:  func(m *Model) bool { return m.evidence.Manage != nil },
			argSpecs: staticArgs(argOption{"purge", "Delete stored tool output"})},
		{name: "/gate", args: "[run|result|on|off]", desc: "Run the project's quality gate",
			enabled: func(m *Model) bool { return m.gate.Manage != nil },
			argSpecs: staticArgs(
				argOption{"run", "Run the gate suites"},
				argOption{"result", "Show the last result"},
				argOption{"on", "Run the suite as a turn that changed files closes"},
				argOption{"off", "Stop running it at a turn's close"},
			)},
		{name: "/ps", desc: "List session-owned long-running processes",
			enabled: func(m *Model) bool { return m.processes.Manage != nil }},
		{name: scaffoldCommandName, desc: "Scaffold this project's .shhh/ context file (asks first)",
			enabled:  func(m *Model) bool { return m.scaffold.Write != nil },
			idleOnly: "it writes a file into the checkout"},
		{name: "/skills", desc: "The skills this session loaded, and why any did not"},
		{name: "/mcp", args: "[trust <name>|distrust <name>]", desc: "The MCP servers this session connected, and why any did not",
			argSpecs: staticArgs(
				argOption{"trust", "Let a project server start from the next session on"},
				argOption{"distrust", "Withdraw that"},
			)},
		{name: "/skill", args: "<name> [task]", desc: "Activate a skill now, with your task after it",
			enabled:  func(m *Model) bool { return m.skills.Len() > 0 },
			argSpecs: []argSpec{{dynamic: skillArgs}}},
		{name: "/secret", args: "[list|set|forget]", desc: "Values commands can use and the model never sees",
			enabled: func(m *Model) bool { return m.secrets.Manage != nil },
			argSpecs: staticArgs(
				argOption{"list", "Name the session's secrets"},
				argOption{"set", "Declare one: NAME from the environment, or NAME=value"},
				argOption{"forget", "Drop one by name"},
			)},
		{name: "/notes", args: "[drop <n>|clear]", desc: "The session's shared notebook: what the agents wrote for each other",
			enabled: func(m *Model) bool { return m.notebook != nil },
			argSpecs: staticArgs(
				argOption{"drop", "Remove one note by number"},
				argOption{"clear", "Empty the notebook"},
			)},
		{name: "/memory", args: "[list|add|edit|forget]", desc: "Durable memories",
			enabled: func(m *Model) bool { return m.memory.Manage != nil },
			argSpecs: staticArgs(
				argOption{"list", "Show stored memories"},
				argOption{"add", "Remember something"},
				argOption{"edit", "Reword a memory by id, in your editor"},
				argOption{"forget", "Drop a memory by id"},
			)},
		{name: "/agents", args: "[new [brief]]", desc: "Agent manager; new drafts a profile from a sentence",
			key: keys.Shown(keys.Draft.Agents),
			// The manager opens on a session that can spawn agents or draft a
			// profile for one. Drafting alone is enough: the list is where the
			// offer to draft lives (attach.go).
			enabled:  func(m *Model) bool { return m.subagents != nil || m.personas.Enabled },
			argSpecs: staticArgs(argOption{"new", "Draft an agent profile with the model's help"})},
		{name: "/attach", args: "[name]", desc: "Attach to an agent's session and steer it",
			enabled:  func(m *Model) bool { return m.subagents != nil },
			argSpecs: []argSpec{{dynamic: agentArgs, fuzzy: true}}},
		{name: "/detach", desc: "Back to the orchestrator (also esc)",
			enabled: func(m *Model) bool { return m.subagents != nil && m.attachedTo != "" }},
		{name: "/todo", args: "[show|edit|new|add|groom|block|open|done|drop|run|sprint|status|stop]", desc: "The project's backlog (bare /todo opens the screen)",
			enabled: func(m *Model) bool { return m.todosEnabled() },
			argSpecs: []argSpec{
				{options: []argOption{
					{"show", "Print an item"},
					{"edit", "Open an item in your editor"},
					{"add", "Read this session into items, or add one from a sentence"},
					{"groom", "Read an item against the tree and propose the corrections"},
					{"block", "Mark an item blocked, with why"},
					{"open", "Reopen a blocked item"},
					{"done", "Archive an item"},
					{"drop", "Delete an item outright"},
					{"run", "Work an item through to a commit (bare run takes the next ready one)"},
					{"sprint", "The set being worked: bare shows it, plan proposes one"},
					{"status", "Where the run is"},
					{"stop", "Abandon the run; the item goes back to open"},
				}},
				{after: []string{"show", "edit", "groom", "block", "open", "done", "drop", "run"}, dynamic: todoSlugArgs, fuzzy: true},
			}},
		{name: "/plan", args: "[save|drop]", desc: "The approved plan as a checklist, with anything that has departed from it",
			enabled: func(m *Model) bool { return m.codingSurfaces() },
			argSpecs: staticArgs(
				argOption{"save", "Write the last plan/response to .shhh/plans/"},
				argOption{"drop", "Forget the approved plan; steps go back to inferred"},
			)},
		{name: "/diff", args: "[path]", desc: "Cumulative session diff, full screen — bare, or one file's",
			enabled:  func(m *Model) bool { return m.changes != nil && m.codingSurfaces() },
			argSpecs: []argSpec{{dynamic: sessionFileArgs, fuzzy: true}}},
		{name: "/review", args: "[turn]", desc: "Review what a turn changed — files, hunks, staging",
			enabled:  func(m *Model) bool { return m.changes != nil && m.codingSurfaces() },
			argSpecs: []argSpec{{dynamic: reviewTurnArgs}}},
		{name: "/undo", args: "[turn]", desc: "Put back what a turn changed (asks first)",
			enabled:  func(m *Model) bool { return m.changes != nil && m.codingSurfaces() },
			argSpecs: []argSpec{{dynamic: reviewTurnArgs}},
			idleOnly: "it writes files the running turn may be editing"},
		{name: "/compact", desc: "Continue from a summary plus the most recent turns",
			idleOnly: "it rewrites the conversation into a summary"},
		{name: "/rewind", args: "[n]", desc: "Rewind to before a user turn — the conversation, the files, or both",
			argSpecs: []argSpec{{dynamic: checkpointArgs}},
			idleOnly: "it rewinds the conversation and can write files back"},
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
		{name: "/chats", desc: "Saved chats — enter loads, x deletes, r renames",
			enabled:  func(m *Model) bool { return m.db != nil },
			idleOnly: "it opens the picker that replaces the conversation"},
		{name: "/exit", aliases: []string{"/quit", "/q"}, desc: "Quit (also /quit, /q)", key: keys.Shown(keys.Draft.Quit)},
	}
}

// maxCompletionRows caps how many commands the menu shows at once; longer
// match lists scroll to keep the focused row visible.
const maxCompletionRows = 6

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
	if m.complete.dismissedFor != "" && val != m.complete.dismissedFor {
		m.complete.dismissedFor = ""
	}
	if !m.inputLive() || m.attachedTo != "" || m.agentList != nil || m.activeChildAsk() != nil ||
		strings.ContainsAny(val, "\t\n") || val == m.complete.dismissedFor {
		m.clearCompletions()
		m.complete.mentionCache = nil
		return
	}

	var prev string
	if m.complete.idx < len(m.complete.items) {
		prev = m.complete.items[m.complete.idx].name
	}

	prior, token, start, end := tokenAtCursor(val, m.inputCursor())
	var matches []completionItem
	files := false
	switch {
	case strings.HasPrefix(val, "/"):
		// A slash line is a command being typed, and the menu is the
		// registry's.
		if len(prior) == 0 {
			matches = m.commandMatches(token)
		} else {
			matches = m.argumentMatches(prior, token)
		}
	case strings.HasPrefix(token, "@"):
		// An @ token — at the start of the draft or after whitespace,
		// because tokens are whitespace-split — is a file mention
		// (mention.go). A space or a cursor moved off the token closes
		// the menu the same way it always has: the token under the
		// cursor is no longer this one.
		matches = m.mentionMatches(strings.TrimPrefix(token, "@"))
		files = true
	default:
		m.clearCompletions()
		m.complete.mentionCache = nil
		return
	}
	// The mention cache outlives an empty match list on purpose: the walk
	// behind it runs once per @ draft, and a token that matches nothing is
	// still that draft mid-edit. It is dropped above, where the draft stops
	// being one — never per keystroke.
	if !files {
		m.complete.mentionCache = nil
	}
	if len(matches) == 0 {
		m.clearCompletions()
		return
	}

	m.complete.items = matches
	m.complete.forInput = val
	m.complete.arg = len(prior) > 0 && !files
	m.complete.files = files
	m.complete.start = start
	m.complete.end = end
	m.complete.idx = 0
	m.complete.token = token
	// A keystroke is a new menu, whatever the last one was pointed at: what
	// ↑↓ said about a list the reader has since retyped is not an answer
	// about this one.
	m.complete.moved = false
	// Keep the arrowed-to row focused across keystrokes — unless the typed
	// text now names a candidate exactly, which always wins the focus.
	if !exactlyNamed(m, token) {
		for i, c := range matches {
			if c.name == prev {
				m.complete.idx = i
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
	for _, c := range slashCommands() {
		if c.enabled != nil && !c.enabled(m) {
			continue
		}
		if !c.matches(token) {
			continue
		}
		item := completionItem{name: c.name, args: c.args, desc: c.desc, space: c.args != ""}
		// A command that needs an idle session is unavailable, not gone: it
		// shows greyed with the reason beside it and comes back when the turn
		// ends. Dropping it left the reader who typed /comp mid-turn with an
		// empty menu and no way to tell a command that is waiting from one
		// this build does not have
		// (docs/interface/principles.md#fold-never-hide).
		if c.idleOnly != "" && m.working() {
			item.off = idleOnlyMeta
		}
		if c.namesExactly(token) {
			matches = append([]completionItem{item}, matches...)
		} else {
			matches = append(matches, item)
		}
	}
	// A connected server's prompts are commands too (mcp.go), and they
	// come last: what the session promises to answer outranks what a
	// server happens to publish today.
	return append(matches, m.mcpCommandMatches(token)...)
}

// argumentMatches are the candidates for the argument under the cursor:
// prior[0] names the command, and the remaining prior tokens fix the
// position. Free-form positions (a chat name to save, a memory body) have no
// spec and so no menu.
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
	m.complete.items = nil
	m.complete.forInput = ""
	m.complete.arg = false
	m.complete.files = false
	m.complete.token = ""
	m.complete.moved = false
	m.complete.argCache = nil
	m.complete.argCacheFor = ""
}

// completionRunsInput reports whether enter belongs to the line rather than
// to the focused row. An argument menu that opened on an empty token and has
// not been arrowed onto is a list of what could follow, not a choice already
// made: "/mo" then tab leaves "/model " with the whole catalog under it, and
// the reader who presses enter there means bare /model — the picker — not
// whichever model happens to sort first. Type a prefix or point at a row and
// the menu is a choice again, which enter takes.
//
// See docs/interface/surfaces.md#the-completion-menu.
func (m Model) completionRunsInput() bool {
	return m.complete.arg && !m.complete.files && m.complete.token == "" && !m.complete.moved
}

// completionActive reports whether the menu applies to the input right now; a
// stale menu (the input changed through a path that skipped syncCompletions,
// e.g. a reset) deactivates itself because completeFor no longer matches.
func (m Model) completionActive() bool {
	return m.inputLive() && len(m.complete.items) > 0 &&
		m.complete.forInput != "" && m.complete.forInput == m.input.Value()
}

// dismissCompletions hides the menu until the input text changes again (esc).
func (m *Model) dismissCompletions() {
	dismissed := m.complete.forInput
	m.clearCompletions()
	m.complete.dismissedFor = dismissed
}

// acceptCompletion writes the focused candidate into the input (tab),
// replacing only the token under the cursor. Candidates that can be
// followed by more text get a trailing space so the user can keep typing.
func (m *Model) acceptCompletion() {
	c := m.complete.items[m.complete.idx]
	r := []rune(m.input.Value())
	start, end := m.complete.start, min(m.complete.end, len(r))
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
	m.input.SetCursorColumn(cursor)
	m.syncCompletions()
}

// completionMenuLines renders the menu rows plus the hint line, bounded so
// the bottom panel (input + menu) stays within the confirm-panel height cap.
func (m Model) completionMenuLines() []string {
	if !m.completionActive() {
		return nil
	}
	width := m.completionMenuWidth()
	// The input (inputHeight rows) plus the menu must fit the confirm-panel
	// cap; on very short terminals the hint line goes first, then rows.
	budget := max(m.maxConfirmPanelHeight()-inputHeight, 1)
	showHint := budget >= 2
	if showHint {
		budget--
	}
	visible := min(len(m.complete.items), maxCompletionRows, budget)
	start := 0
	if m.complete.idx >= visible {
		start = m.complete.idx - visible + 1
	}

	nameW := 0
	for _, c := range m.complete.items[start : start+visible] {
		if w := lipgloss.Width(plainCommandLabel(c)); w > nameW {
			nameW = w
		}
	}

	lines := make([]string, 0, visible+1)
	for i := start; i < start+visible; i++ {
		lines = append(lines, completionRow(m.complete.items[i], i == m.complete.idx, nameW, width))
	}

	if !showHint {
		return lines
	}
	segments := m.completionHint()
	if len(m.complete.items) > visible {
		segments = append([]string{fmt.Sprintf("%d/%d", m.complete.idx+1, len(m.complete.items))}, segments...)
	}
	// The row sheds a whole segment rather than being cut where the pane ends:
	// half an offer is worse than no offer, because a reader cannot tell it
	// from a key that is spelled that way
	// (docs/interface/principles.md#fold-never-hide).
	return append(lines, sty.Complete.Hint.Render(components.FitSegments(segments, width)))
}

// completionHint is the menu's key row, in the order it may be given up.
// Every spelling is the draft binding the menu answers underneath it — the
// menu is the draft's own surface and borrows nothing from the selector
// family — so a keymap file that moves one moves the offer with it
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Tab leads because completing is what the menu is for; running is what
// happens once there is nothing left to complete.
func (m Model) completionHint() []string {
	complete := keys.Bracket(keys.Draft.Complete) + " complete"
	move := keys.BracketPair(keys.Draft.HistoryPrev, keys.Draft.HistoryNext) + " move"
	dismiss := keys.Bracket(keys.Draft.Clear) + " dismiss"
	switch {
	case m.complete.files:
		// A file row is inserted, never run: the sentence goes on.
		return []string{
			keys.Bracket(keys.Draft.Complete) + "/" + keys.Bracket(keys.Draft.Send) + " insert",
			move, dismiss,
		}
	case m.completionRunsInput():
		// Enter runs the line as it stands here, so the row says which line
		// that is: a reader who tab-completed "/model" is about to get the
		// picker, not the first row under the cursor.
		return []string{
			complete,
			keys.Bracket(keys.Draft.Send) + " run " + strings.TrimSpace(m.complete.forInput),
			keys.BracketPair(keys.Draft.HistoryPrev, keys.Draft.HistoryNext) + " pick",
			dismiss,
		}
	}
	return []string{complete, keys.Bracket(keys.Draft.Send) + " run", move, dismiss}
}

// completionMenuWidth is the columns a menu row is drawn in. The frame's box
// spends four of the content width on its borders and their padding, and the
// menu lands inside it beside the draft; a row that measured itself against
// the content width would push its right-aligned field into the border, where
// the frame cuts it and the reason reads as a shorter word. Below the frame's
// narrowest rung the menu is written straight into the content and gets all
// of it.
func (m Model) completionMenuWidth() int {
	if !m.frameShowing() {
		return m.contentWidth()
	}
	return m.inputInnerWidth() + lipgloss.Width(m.promptGutter())
}

// completionRow lays one menu row: the name and its argument hint in a column
// measured over the window, the description after it, and — on a row the
// running turn has put out of reach — the ⊘ in front of the name and the
// reason right-aligned at the end.
//
// The reason takes its columns before the description does, which is the drop
// order every other list on the screen keeps: the description is what the
// command does, and a reader who cannot have it yet is asking why rather than
// what. It goes whole or not at all — half a clause reads as a different
// clause (docs/interface/principles.md#fold-never-hide).
func completionRow(c completionItem, focused bool, nameW, width int) string {
	plain := plainCommandLabel(c)
	pad := strings.Repeat(" ", max(nameW-lipgloss.Width(plain), 0))
	// The menu is a list like every other, so its pointer is the same mark in
	// the same column, outside whatever the row is painted with
	// (components.LitOption).
	head := components.PointerColumn()
	body := plain + pad + "  "
	lead := head + body

	avail := max(width-lipgloss.Width(lead), 0)
	tail := ""
	if c.off != "" && avail >= lipgloss.Width(c.off)+2 {
		tail = c.off
		avail -= lipgloss.Width(tail) + 2
	}
	desc := clipRow(c.desc, avail)
	gap := ""
	if tail != "" {
		gap = strings.Repeat(" ", max(width-lipgloss.Width(lead+desc+tail), 0))
	}

	switch {
	case focused:
		// Painted whole: the row is already bold on the focus ground, and the
		// reason rides inside that rather than beside it.
		return components.LitOption(body+desc+gap+tail, width)
	case c.off != "":
		// One run across the whole row. Split into three it would say the row
		// is three things, when what it says is that none of it can be had yet.
		return clipRow(sty.Complete.Off.Render(lead+desc+gap+tail), width)
	}
	label := sty.Complete.Name.Render(c.name)
	if c.args != "" {
		label += " " + sty.Complete.Args.Render(c.args)
	}
	return clipRow(head+label+pad+"  "+sty.Complete.Desc.Render(desc), width)
}

// plainCommandLabel is the unstyled name+args column used for alignment,
// behind the ⊘ that marks a row the session cannot run right now. The glyph
// is part of the column rather than a prefix outside it, so a menu holding
// both kinds still lines its descriptions up — the same choice the selector's
// own rows make.
func plainCommandLabel(c completionItem) string {
	label := c.name
	if c.args != "" {
		label += " " + c.args
	}
	if c.off != "" {
		return "⊘ " + label
	}
	return label
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

// completionState is the open completion menu. It is one struct rather than
// thirteen fields on the session because every one of them is only ever read
// with the others: a candidate list means nothing without the input value it
// was built for, and a focused row means nothing without the span it would be
// written into.
type completionState struct {
	// items is the filtered candidate list for the input value forInput. A
	// mismatch means the list is stale, so the menu is hidden rather than
	// drawn against a draft it no longer describes.
	items    []completionItem
	forInput string
	// idx is the focused row, and dismissedFor is the input value esc
	// dismissed the menu for — typing anything else re-opens it.
	idx          int
	dismissedFor string
	// start and end are the span of the token being completed, as rune
	// offsets into the input, and arg says the focused row is an argument
	// value rather than a command name.
	start int
	end   int
	arg   bool
	// token is what has been typed of the token being completed, and moved
	// says ↑↓ has pointed at a row. Together they are how enter tells a menu
	// that is a list of what could follow from one that is a choice already
	// narrowed — see completionRunsInput.
	token string
	moved bool
	// files says the menu is the @ file mention's (mention.go): enter
	// inserts the focused path rather than running anything, and
	// mentionCache holds the file walk for the life of the @ draft — it
	// survives a token that matches nothing, so the walk never runs per
	// keystroke.
	files        bool
	mentionCache []paletteEntry
	// argCache holds a command's dynamic argument sources (branch names,
	// saved chats) so they are read once per menu rather than per keystroke.
	argCache    map[int][]argOption
	argCacheFor string
}
