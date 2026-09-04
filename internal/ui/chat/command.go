package chat

// Submitting the input. Enter used to mean three unrelated things
// depending on the turn state, with the slash-command dispatch buried in the
// idle branch — so while the agent worked, every command bounced off one
// refusal ("commands can't run while the agent is working"). That was worst
// exactly when a command was most wanted: sub-agents only exist while the
// parent's turn is in flight, so the agent manager, and everything else that
// inspects a running session, was unreachable for its whole lifetime.
//
// The dispatch lives here now, shared by both states. A command that leaves
// the running conversation alone runs immediately; a command that would
// rewrite or replace it (the registry's idleOnly rows) says so and waits.
// Plain text is still a message when idle and steering while working.

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/storage"
)

// submitInput handles Enter on the orchestrator surface, in every state that
// keeps the input live.
func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	// With the completion menu open, enter runs the highlighted command
	// rather than the raw prefix; on an argument row it completes the
	// token first and runs the whole line. A file-mention row is inserted,
	// never run — the sentence is still being written (mention.go). A menu
	// the reader has neither filtered nor arrowed onto is showing what could
	// follow rather than a choice, and enter is the line's
	// (completionRunsInput).
	if m.completionActive() {
		switch {
		case m.complete.files:
			return m.insertMention()
		case m.completionRunsInput():
			// The line as typed, untouched by the row under the cursor.
		case m.complete.arg:
			m.acceptCompletion()
			text = strings.TrimSpace(m.input.Value())
		default:
			text = m.complete.items[m.complete.idx].name
		}
	}
	if text == "" {
		return m, nil
	}
	// A line carrying a secret's value is not recalled: the point of the
	// command is that the value is typed once and never shown again.
	if !secretInput(text) {
		m.recordInput(text)
	}
	m.input.Reset()
	// A submitted line is where a server's re-listing is taken: the reader
	// is between turns here, or steering one, and either way the toolset
	// applies nothing while a round's calls are out. What it moves is what
	// this surface reads live — the commands a server publishes; what the
	// model was told is the session's (mcp.go).
	m.refreshMCP()
	if name := commandName(text); name != "" {
		return m.runCommand(text, name)
	}
	if reason, held := m.todoRunHoldsInput(); held {
		return m.systemNotice("Not sent: " + reason + ".")
	}
	// A draft in bang form is a command for the machine, not a message for
	// the model: `!cmd` rides the /run confirm, `!!cmd` the same with its
	// output kept local (bang.go). Checked before the steering branch so a
	// command typed mid-turn is refused the way /run is, never queued as a
	// sentence.
	if cmd, local, ok := bangCommand(text); ok {
		return m.runBang(cmd, local)
	}
	if m.turnInFlight() {
		// Typed while the agent works: the message joins the conversation
		// before the next model request. A turn paused on a decision
		// is a turn in flight for this purpose — enter queues the sentence
		// for the next round rather than starting a turn the pending
		// decision would immediately interrupt.
		m.steering = append(m.steering, text)
		// The queued count surfaces on the notice rail.
		m.syncViewport()
		return m, nil
	}
	return m.sendUserMessage(text)
}

// commandName is the leading "/word" of a submitted line, or "" when the line
// is ordinary text. A lone /word is a command even when it is misspelled — a
// typo gets answered rather than sent to the model — while a path like
// /etc/hosts carries another slash and is text.
func commandName(text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return ""
	}
	if strings.Contains(parts[0][1:], "/") {
		return ""
	}
	return parts[0]
}

// runCommand dispatches one slash command from the orchestrator surface.
func (m Model) runCommand(text, name string) (tea.Model, tea.Cmd) {
	if m.working() {
		if reason, ok := idleOnlyReason(name); ok {
			note := name + " needs the turn to be finished — " + reason +
				". The agent is still working; nothing was queued. Ctrl+C ends the turn."
			if active, _ := m.activeAgents(); active > 0 {
				note += " /agents steers what is running."
			}
			return m.surfaceNotice(note)
		}
	}
	if m.unavailableCommand(name) {
		return m.surfaceNotice(name + " is not part of this session.")
	}
	parts := strings.Fields(text)
	switch {
	case name == "/notes":
		return m.surfaceNotice(m.notesCommand(parts[1:]))

	case name == "/paste":
		// Attachments. Not idleOnly: staging bytes for the next
		// message touches nothing the running turn is using.
		return m.runPaste(parts)

	case name == "/agents":
		if len(parts) > 1 && parts[1] == "new" {
			return m.startPersona(strings.Join(parts[2:], " "))
		}
		return m.openAgentList()

	case name == "/attach":
		return m.attachCommand(parts)

	case name == "/secret" || name == "/secrets":
		// Not idleOnly: adding a secret mid-turn is exactly when the
		// command is wanted — the model just asked for a key it lacks.
		return m.secretCommand(parts[1:])

	case name == "/skill":
		// Explicit activation. Not idleOnly: while the agent works the
		// content queues as steering, like any typed text.
		if len(parts) < 2 {
			return m.surfaceNotice("Usage: /skill <name> [task]. /skills lists what can be activated.")
		}
		return m.activateSkill(parts[1], strings.Join(parts[2:], " "))

	case name == "/detach":
		if m.attachedTo == "" {
			return m.surfaceNotice("Not attached to an agent. /attach <name> or /agents to pick one.")
		}
		m.detachOne()
		return m, nil

	case name == "/clear" || name == "/new":
		// The session boundary (model.go). Over a turn that is not over it
		// asks first, the way quitting does and for the same reason: what a
		// yes costs is the work the reader may not have noticed running, and
		// a boundary is never crossed mid-turn.
		if m.turnInFlight() {
			return m.openNewSessionConfirm()
		}
		note, save := m.startNewSession()
		next, cmd := m.systemNotice(note)
		return next, tea.Batch(cmd, save)

	case name == "/exit" || name == "/quit" || name == "/q":
		// A typed command is deliberate, so an idle quit goes straight
		// out; over a live turn even it confirms, because what it costs is
		// the turn's work, not the reader's time.
		if m.working() {
			return m.openQuitConfirm()
		}
		return m, m.quitNow()

	case name == "/run":
		// Bare /run with several code blocks opens the picker; one
		// block, /run <n>, and every no-op case go straight to startRun.
		if len(parts) == 1 {
			if picked, cmd, ok := m.openRunPick(); ok {
				return picked, cmd
			}
		}
		result, entersConfirm := m.startRun(parts)
		if !entersConfirm {
			m.appendEntry(entry{kind: entrySystem, text: result})
		}
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, nil

	case text == "/status":
		// The rail's SUMMARY block in words, for the terminals
		// below 130 columns that have no rail to draw it in — the same answer
		// the rail's rules give for PLAN. It takes a fresh reading on the way out:
		// asking for the summary is a reason to have a current one.
		note, read := m.statusCommand()
		next, cmd := m.systemNotice(note)
		return next, tea.Batch(cmd, read)

	case name == "/copy":
		// The last response onto the clipboard. Answered here rather than
		// in the handler table below because the copy may be a write the
		// terminal takes, and a row of that table has nowhere to put one.
		return m.copyCommand(parts)

	case text == "/compact":
		return m.startCompact()

	case text == "/rewind":
		// Bare /rewind opens the checkpoint picker; the numbered form
		// goes through handleSlashCommand.
		return m.openRewindPick()

	case name == "/diff":
		// Bare, the cumulative session diff; with a path, that one file's,
		// which is the keyboard's way to the door a click on a CHANGES row
		// opens (railclick.go). The argument is a path and not a turn
		// number, because the rail's rows are paths and the two surfaces
		// answer the same question.
		if len(parts) > 1 {
			return m.openFileDiff(strings.Join(parts[1:], " "))
		}
		return m.openSessionDiff()

	case text == "/step":
		// The in-flight step's detail, from the draft — the chord that
		// answered this went to reading mode, and the question it answered
		// is still asked mid-turn with a half-written sentence in the box
		// (detail.go).
		return m.detailFromDraft()

	case text == "/context":
		// The occupancy surface, full screen. It reads the conversation
		// and changes nothing in it, so it is not idleOnly: a window filling
		// up mid-turn is exactly when the question gets asked.
		return m.openContext()

	case name == "/review":
		// Review mode over a turn's changeset; bare takes the
		// most recent turn that changed anything.
		return m.reviewCommand(parts)

	case name == "/undo":
		// Put a turn's edits back from the session's own records;
		// bare takes the most recent turn that changed anything.
		return m.undoCommand(parts)

	case text == "/model" && m.canPickModel():
		// Bare /model opens the model picker; the named form and
		// sessions with nothing to pick go through handleSlashCommand. A
		// provider that can enumerate its endpoint is queried first.
		return m.startModelPick()

	case text == "/permissions" || text == "/perms" || text == "/mode":
		// Bare /permissions opens the mode picker.
		return m.openModePick()

	case text == "/load" || text == "/chats":
		// Bare /load and /chats open the saved-chat picker; with
		// nothing saved they fall through to the listing below.
		if picked, cmd, ok := m.openChatPick(); ok {
			return picked, cmd
		}

	case name == "/ui":
		// /ui mouse flips the terminal's own reporting. That is
		// a field on the View rather than a command back to the program, so
		// this setting takes the same path as every other /ui setting: change
		// the model, say so in the transcript.
		return m.systemNotice(m.uiCommand(parts))

	case name == "/trust":
		// The one answer that decides whether the checkout's skills, agent
		// profiles, quality suites and servers load at all. Like trusting a
		// server, it lands in the next session: the prompt naming the skills
		// and the toolset holding the gate were built when this one started.
		return m.systemNotice(m.trustCommand(parts[1:]))

	case text == scaffoldCommandName:
		// The scaffolding card: what it would write, before it writes it.
		return m.scaffoldCommand()

	case name == "/todo":
		// Bare /todo opens the backlog picker; the subcommands are textual,
		// and edit hands the item file to the editor.
		return m.todoCommand(parts)

	case name == "/memory" && len(parts) > 1 && parts[1] == "edit":
		// /memory edit hands the entry's text to the editor; every other
		// /memory subcommand is textual and goes through handleSlashCommand.
		if len(parts) != 3 {
			return m.systemNotice("Usage: /memory edit <id>")
		}
		return m.openMemoryEditor(parts[2])

	case text == "/branches":
		// Bare /branches opens the branch picker; a session with no
		// branch family falls through.
		if picked, cmd, ok := m.openBranchPick(); ok {
			return picked, cmd
		}
	}
	// A connected server's prompts are commands of this session
	// (mcp.go). The name is namespaced by the server it came from, so
	// nothing in the registry can collide with one, and the lookup is
	// before the skills' because a prompt name is exact where a skill's is
	// a bare word.
	if p, ok := m.mcpPrompt(name); ok {
		return m.runMCPPrompt(p, parts[1:])
	}
	// A skill's own name works as a command — /documentation — the way
	// the other harnesses spell it, but only where no real command has the
	// name: the registry wins a collision, so a skill called "help" is
	// reached through /skill help.
	if _, ok := m.skills.Find(name[1:]); ok {
		if _, taken := lookupCommand(&m, name); !taken {
			return m.activateSkill(name[1:], strings.Join(parts[1:], " "))
		}
	}
	// commandName accepts exactly what handleSlashCommand answers — down to
	// the unknown-command hint for a misspelling — so this always handles.
	_, result := m.handleSlashCommand(text)
	return m.systemNotice(result)
}

// surfaceNotice writes a front-end note to whichever transcript is on screen
// — the attached child's, or the orchestrator's — so an answer never lands
// where the user cannot see it.
func (m Model) surfaceNotice(text string) (tea.Model, tea.Cmd) {
	if m.attachedTo != "" && m.subagents != nil {
		m.noteChild(m.attachedTo, text)
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		m.atBottom = true
		return m, nil
	}
	return m.systemNotice(text)
}

// activeAgents is how many children are working and how many of those are
// blocked on the user, or zeroes without a supervisor.
func (m Model) activeAgents() (active, blocked int) {
	if m.subagents == nil {
		return 0, 0
	}
	return m.subagents.ActiveCounts()
}

// attachCommand is /attach: bare, it opens the agent list to pick from;
// named, it jumps straight into that agent's session.
func (m Model) attachCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.subagents == nil {
		return m.systemNotice("Sub-agents are unavailable in this session.")
	}
	if len(parts) < 2 {
		return m.openAgentList()
	}
	name := parts[1]
	if name == "orchestrator" {
		m.attach("")
		return m, nil
	}
	if _, ok := m.subagents.Get(name); !ok {
		return m.surfaceNotice("No agent named " + name + ". /agents lists this session's agents.")
	}
	if name == m.attachedTo {
		return m.surfaceNotice("Already attached to " + name + ".")
	}
	m.attach(name)
	return m, nil
}

// The slash-command table.
//
// A command used to be a case label in a string switch nearly three hundred
// lines long, with its aliases as extra labels on the same case — so nothing
// could ask which names a session answers, or answer one, without reading the
// whole switch. It is a map from the name to what the name does now, and an
// alias is a row pointing at the same function.

// slashHandler is what one command does with the words it was typed with.
// parts is the whole line split on whitespace, the command name included:
// two commands read theirs as a list of subcommands and pass it on whole. The
// answer is the row the transcript shows.
type slashHandler func(m *Model, parts []string) string

var (
	slashHandlerOnce  sync.Once
	slashHandlerTable map[string]slashHandler
)

// slashHandlers is the table. Every name a session answers is a row of it,
// which is what lets the completion registry be checked against what the
// session will actually do with a name it offers.
//
// It is built on first use rather than at initialisation for the reason the
// overlay register is (overlay.go): a command reads the session, and reading
// the session eventually asks which mode has the screen — a loop the compiler
// reads as an initialisation cycle in a package-level table.
func slashHandlers() map[string]slashHandler {
	slashHandlerOnce.Do(func() { slashHandlerTable = buildSlashHandlers() })
	return slashHandlerTable
}

func buildSlashHandlers() map[string]slashHandler {
	return map[string]slashHandler{
		"/help":        slashHelp,
		"/model":       slashModel,
		"/permissions": slashPermissions,
		"/perms":       slashPermissions,
		"/mode":        slashPermissions,
		"/reasoning":   slashReasoning,
		"/think":       slashReasoning,
		"/stats":       slashStats,
		"/ui":          slashUI,
		"/add-dir":     slashAddDir,
		"/adddir":      slashAddDir,
		"/sandbox":     slashSandbox,
		"/evidence":    slashEvidence,
		"/gate":        slashGate,
		"/ps":          slashProcesses,
		"/memory":      slashMemory,
		"/mcp":         slashMCP,
		"/skills":      slashSkills,
		"/plan":        slashPlan,
		"/rewind":      slashRewind,
		"/branches":    slashBranches,
		"/save":        slashSave,
		"/load":        slashLoad,
		"/chats":       slashChats,
	}
}

func slashHelp(m *Model, _ []string) string {
	return helpText(m) + "\n\n" + m.policyHelp()
}

func slashModel(m *Model, parts []string) string {
	if len(parts) < 2 {
		if m.modelName != "" {
			return fmt.Sprintf("Current model: %s\n%s", m.modelName, modelUsage)
		}
		return modelUsage
	}
	// /model default [name] and /model agents [name] persist a default to
	// the config file instead of switching this session only.
	if parts[1] == "default" || parts[1] == "agents" {
		return m.setModelDefault(parts[1], parts[2:])
	}
	if m.switchFn == nil {
		return "Model switching is not available in this session."
	}
	if len(parts) > 2 {
		return "Model names cannot contain spaces. " + modelUsage
	}
	name := parts[1]
	if name == m.modelName {
		return fmt.Sprintf("Already using %s.", name)
	}
	m.switchFn(name)
	m.modelName = name
	return fmt.Sprintf("Switched model to %s. (/model default %s makes it the default for new sessions.)", name, name)
}

// /permissions was /mode until the name was the problem: one letter from
// /model, on a menu that shows both, for a command whose whole job is
// deciding what runs without asking. The old spelling still answers —
// muscle memory is not a typo — but it is an alias now, and every line
// the product prints says /permissions.
func slashPermissions(m *Model, parts []string) string {
	if len(parts) < 2 {
		return m.modeStatus()
	}
	// The grants are the mode's own subject — what the session has
	// stopped asking about — so they answer here rather than under a
	// command of their own.
	switch parts[1] {
	case "grants":
		return m.grantStatus()
	case "allow":
		return m.allowCommand(parts[2:])
	case "revoke":
		return m.revokeCommand(parts[2:])
	}
	if len(parts) > 2 {
		return "Usage: /permissions [manual|accept-edits|auto|plan|why|grants|allow|revoke]"
	}
	if parts[1] == "why" {
		if m.lastDenial == "" {
			return "No auto-mode denials this session."
		}
		return "Last auto-mode denial:\n  " + m.lastDenial
	}
	mode, err := agent.ParseMode(parts[1])
	if err != nil {
		return "Error: " + err.Error()
	}
	m.applyMode(mode)
	return fmt.Sprintf("Mode set to %s — %s.", mode, mode.Describe())
}

func slashReasoning(m *Model, parts []string) string {
	return m.reasoningCommand(parts[1:])
}

func slashStats(m *Model, _ []string) string {
	return m.statsReport()
}

func slashUI(m *Model, parts []string) string {
	return m.uiCommand(parts)
}

func slashAddDir(m *Model, parts []string) string {
	// The working scope: the grant made in front of no particular
	// decision. It lives beside /permissions rather than under it because
	// it answers a different question — not "what may run without
	// asking", but "where is the work".
	return m.scopeCommand(parts)
}

func slashSandbox(m *Model, parts []string) string {
	args := parts[1:]
	if len(args) == 0 {
		args = []string{"doctor"}
	}
	if m.containment.Manage != nil {
		return m.containment.Manage(args)
	}
	// No manager wired (older sessions/tests): doctor falls back to the
	// static report; everything else is unavailable.
	if len(args) == 1 && args[0] == "doctor" {
		if m.containment.Report == "" {
			return "Command containment is not configured in this session."
		}
		return m.containment.Report
	}
	return "Container sandbox management is unavailable in this session."
}

func slashEvidence(m *Model, parts []string) string {
	if m.evidence.Manage == nil {
		return "The evidence store is unavailable in this session."
	}
	return m.evidence.Manage(parts[1:])
}

func slashGate(m *Model, parts []string) string {
	if m.gate.Manage == nil {
		return "The quality gate is unavailable in this session."
	}
	if handled, note := m.gateToggle(parts[1:]); handled {
		return note
	}
	return m.gate.Manage(parts[1:])
}

func slashProcesses(m *Model, parts []string) string {
	if m.processes.Manage == nil {
		return "The process supervisor is unavailable in this session."
	}
	return m.processes.Manage(parts[1:])
}

func slashMemory(m *Model, parts []string) string {
	if m.memory.Manage == nil {
		return "Durable memory is unavailable in this session."
	}
	return m.memory.Manage(parts[1:])
}

func slashMCP(m *Model, parts []string) string {
	if m.mcp.Manage == nil {
		return "No MCP servers in this session. Define one under [mcp.servers] in your config, or in mcp.json beside it; `shhh mcp` lists what a session here would connect."
	}
	return m.mcp.Manage(parts[1:])
}

func slashSkills(m *Model, _ []string) string {
	if m.skills == nil {
		return "No skills loaded in this session. A skill is a directory holding a SKILL.md under .shhh/skills, .agents/skills or .claude/skills, in the project or your home directory."
	}
	return m.skillsList(m.skills)
}

func slashPlan(m *Model, parts []string) string {
	// Bare /plan reopens the approved plan mid-turn, which is how the
	// checklist stays reachable below 130 columns, where there is no rail
	// to hold it.
	if len(parts) == 1 {
		return m.planStatus()
	}
	switch parts[1] {
	case "save":
		planText := m.lastAssistantText()
		if strings.TrimSpace(planText) == "" {
			return "No plan to save yet — there is no assistant response."
		}
		path, err := savePlan(m.workspace, planText, strings.Join(parts[2:], "-"))
		if err != nil {
			return "Error saving plan: " + err.Error()
		}
		return "Plan saved to " + path
	case "drop":
		if m.planRun == nil {
			return "No approved plan is running."
		}
		m.planRun = nil
		m.invalidateRenderCache()
		return "Dropped the approved plan — the outline goes back to inferring its steps."
	}
	return planUsage
}

func slashRewind(m *Model, parts []string) string {
	// Only the numbered form arrives here; bare /rewind opens the picker
	// from the enter handler.
	if len(m.checkpoints) == 0 {
		return "No checkpoints to rewind to yet."
	}
	if len(parts) != 2 {
		return fmt.Sprintf("Usage: /rewind [<turn 1-%d>] — bare /rewind opens the picker", len(m.checkpoints))
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Sprintf("Usage: /rewind [<turn 1-%d>]", len(m.checkpoints))
	}
	return m.rewindToTurn(n)
}

func slashBranches(m *Model, parts []string) string {
	branches, why := m.branchFamily()
	if len(parts) == 1 {
		// Only reached when there is nothing to pick; otherwise bare
		// /branches opens the picker from the enter handler. What is
		// left to say is why it did not open — never the family itself,
		// because a list whose rows are read and retyped is the thing
		// the picker replaced.
		if why == "" {
			why = fmt.Sprintf("This session has %d branches — /branches opens the picker over them.", len(branches))
		}
		return why
	}
	if why != "" {
		return why
	}
	return m.switchBranch(branches, strings.Join(parts[1:], " "))
}

// copyCommand is /copy: the last response onto the clipboard, or with
// `code`, only the code blocks in it.
//
// The terminal is offered the text before any tool on this machine
// (copyText), which is what carries the copy back over ssh to the reader
// instead of leaving it on the server they are connected to. So the answer
// is a clipboard write as well as a line for the transcript, which is more
// than a row of the handler table can hand back.
func (m Model) copyCommand(parts []string) (tea.Model, tea.Cmd) {
	text := m.lastAssistantText()
	if text == "" {
		return m.systemNotice("Nothing to copy yet.")
	}
	what := "response"
	if len(parts) > 1 && parts[1] == "code" {
		blocks := extractCodeBlocks(text)
		if len(blocks) == 0 {
			return m.systemNotice("No code blocks in the last response.")
		}
		text = strings.Join(blocks, "\n")
		what = "code"
	}
	res, write := m.copyText(text)
	if note := copyFailure(res); note != "" {
		return m.systemNotice(note)
	}
	next, cmd := m.systemNotice("Copied last " + what + " to clipboard.")
	return next, tea.Batch(cmd, write)
}

func slashSave(m *Model, parts []string) string {
	if m.db == nil {
		return "Chat persistence is unavailable."
	}
	name := "unnamed"
	if len(parts) > 1 {
		name = strings.Join(parts[1:], " ")
	}
	if err := m.db.SaveChat(name, stripResumeContext(m.agent.Messages())); err != nil {
		return "Error saving: " + err.Error()
	}
	// The generated title goes with the conversation into its named
	// slot; the name is what the listing leads with from now on.
	if m.titles.title != "" {
		_ = m.db.SetChatTitle(name, m.titles.title)
	}
	// So does what the conversation is opened again on, for the same
	// reason: a copy under a name of the person's choosing is the
	// conversation, and one that came back unable to say which commit it
	// was written on would be the one copy that could not (reopen.go).
	_ = m.db.SetChatResume(name, storage.ChatResume{
		Summary: m.compactSummary, Head: project.Head(m.workspace)})
	// Future rewind branches hang off the named session.
	m.adoptSlot(name)
	return fmt.Sprintf("Chat saved as %q", name)
}

func slashLoad(m *Model, parts []string) string {
	if m.db == nil {
		return "Chat persistence is unavailable."
	}
	if len(parts) < 2 {
		// Only reached when there is nothing to pick; otherwise bare
		// /load opens the picker from the enter handler.
		_, listing := m.handleSlashCommand("/chats")
		return listing + "\n\nUsage: /load <name>"
	}
	return m.loadChatByName(strings.Join(parts[1:], " "))
}

func slashChats(m *Model, _ []string) string {
	if m.db == nil {
		return "Chat persistence is unavailable."
	}
	entries, err := m.db.ListChats()
	if err != nil {
		return "Error: " + err.Error()
	}
	if len(entries) == 0 {
		return "No saved chats."
	}
	var sb strings.Builder
	sb.WriteString("Saved chats:\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "  %s  (%s)\n", e.Name, chatDesc(e))
	}
	return strings.TrimRight(sb.String(), "\n")
}
