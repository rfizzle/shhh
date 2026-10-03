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
		return m.systemNotice("not sent: " + reason)
	}
	// A draft in bang form is a command for the machine, not a message for
	// the model: `!cmd` rides the /run confirm, `!!cmd` the same with its
	// output kept local (bang.go). Checked before the steering branch so a
	// command typed mid-turn is refused the way /run is, never queued as a
	// sentence.
	if cmd, local, ok := bangCommand(text); ok {
		return m.runBang(cmd, local)
	}
	// A question the reader handed to the draft claims this sentence: they
	// answered it in their own words rather than changing the subject, so it
	// goes back as the call's result and joins the conversation as nothing
	// else (question.go). It is decided above the steering branch because a
	// message typed while the turn runs would otherwise join m.steering, and
	// an answer is not a steer.
	if m.questionAside() {
		return m.answerTyped(text)
	}
	if m.turnInFlight() {
		// Typed while the agent works: the message joins the conversation
		// before the next model request. A turn paused on a decision
		// is a turn in flight for this purpose — enter queues the sentence
		// for the next round rather than starting a turn the pending
		// decision would immediately interrupt.
		m.steering = append(m.steering, steeringItem{text: text, id: m.queue.next(), atts: m.takeAttachments()})
		// The queue above the box grows a row (msgqueue.go).
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

// runCommand dispatches one slash command from the orchestrator surface: the
// surface a register row opens (overlay.go), else the run the command table
// declares under the name, else answerCommand.
func (m Model) runCommand(text, name string) (tea.Model, tea.Cmd) {
	if m.working() {
		if reason, ok := idleOnlyReason(name); ok {
			note := name + " needs the turn to be finished — " + reason +
				". The agent is still working; nothing was queued. ctrl+c ends the turn"
			if active, _ := m.activeAgents(); active > 0 {
				note += " /agents steers what is running."
			}
			return m.surfaceNotice(note)
		}
	}
	if m.unavailableCommand(name) {
		return m.surfaceNotice(name + " is not part of this session")
	}
	parts := strings.Fields(text)
	// A command whose job is opening a surface is carried out by that
	// surface's register row (overlay.go). A bare one typed with words after
	// it goes on below and is answered as any form the session does not know.
	if c, ok := registeredCommand(name); ok && (!c.bare || len(parts) == 1) {
		return c.open(m, parts)
	}
	if c, ok := commands()[name]; ok && c.run != nil && (!c.exact || text == name) {
		if next, cmd, handled := c.run(m, parts); handled {
			return next, cmd
		}
	}
	return m.answerCommand(text, name, parts)
}

// answerCommand is the dispatch for a line no surface and no command's run
// took: a server's prompt, a skill under its own name, or the transcript row
// handleSlashCommand answers with — the unknown-command hint included.
func (m Model) answerCommand(text, name string, parts []string) (tea.Model, tea.Cmd) {
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
		return m.systemNotice("sub-agents are unavailable in this session")
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
		return m.surfaceNotice("no agent named " + name + ". /agents lists this session's agents")
	}
	if name == m.attachedTo {
		return m.surfaceNotice("already attached to " + name)
	}
	m.attach(name)
	return m, nil
}

// The slash-command table.
//
// A command used to be a case label in a string switch nearly three hundred
// lines long, with its aliases as extra labels on the same case — and then
// two of them, one for what the front end does and one for the row the
// transcript shows, so a command with both was declared twice. It is one row
// per command now holding both, and an alias is a name on the row.
//
// A command whose job is opening a surface is not here: its register row
// declares it (overlay.go). command_test.go holds both to the completion
// registry, so a name cannot be offered without being answered, or answered
// without being offered.

// command is one row of the table. A row is where a command's name sits
// beside what it does, so whatever else is said about a command — its menu
// row, its /help paragraph — has a row to be a field of.
type command struct {
	name    string
	aliases []string
	// exact is a run taken only when the line is the name alone, spelled as
	// typed; anything else goes on to answerCommand.
	exact bool
	// run is what the front end does with the line: open a picker, ask a
	// confirm, hand the terminal a write. handled false is a form it leaves
	// to the rest of the dispatch (answerCommand). nil is a command whose
	// whole answer is the transcript row.
	run commandRun
	// answer is the row the transcript shows, which handleSlashCommand gives
	// for the name. nil is a command the front end always carries out.
	answer slashHandler
}

// commandRun is what one command does in the front end. parts is the whole
// line split on whitespace, the command name included.
type commandRun func(m Model, parts []string) (next tea.Model, cmd tea.Cmd, handled bool)

// slashHandler is what one command does with the words it was typed with.
// parts is the whole line split on whitespace, the command name included:
// two commands read theirs as a list of subcommands and pass it on whole. The
// answer is the row the transcript shows.
type slashHandler func(m *Model, parts []string) string

// always is a run that handles every form of its command.
func always(run func(m Model, parts []string) (tea.Model, tea.Cmd)) commandRun {
	return func(m Model, parts []string) (tea.Model, tea.Cmd, bool) {
		next, cmd := run(m, parts)
		return next, cmd, true
	}
}

// bareRun is a run that reads no words, for a command declared exact.
func bareRun(run func(m Model) (tea.Model, tea.Cmd)) commandRun {
	return func(m Model, _ []string) (tea.Model, tea.Cmd, bool) {
		next, cmd := run(m)
		return next, cmd, true
	}
}

// picks is a run that opens a picker when there is something to pick, and
// otherwise leaves the line to the command's answer, which says why not.
func picks(open func(m Model) (tea.Model, tea.Cmd, bool)) commandRun {
	return func(m Model, _ []string) (tea.Model, tea.Cmd, bool) { return open(m) }
}

var (
	commandOnce    sync.Once
	commandByName  map[string]*command
	slashHandlerAt map[string]slashHandler
)

// commands is the table by every name a row answers to. slashHandlers is the
// same table as handleSlashCommand reads it: the names whose row has an
// answer.
//
// Both are built on first use rather than at initialisation for the reason
// the overlay register is (overlay.go): a command reads the session, and
// reading the session eventually asks which mode has the screen — a loop the
// compiler reads as an initialisation cycle in a package-level table.
func commands() map[string]*command {
	commandOnce.Do(indexCommands)
	return commandByName
}

func slashHandlers() map[string]slashHandler {
	commandOnce.Do(indexCommands)
	return slashHandlerAt
}

func indexCommands() {
	commandByName = map[string]*command{}
	slashHandlerAt = map[string]slashHandler{}
	for _, c := range buildCommands() {
		for _, n := range append([]string{c.name}, c.aliases...) {
			commandByName[n] = c
			if c.answer != nil {
				slashHandlerAt[n] = c.answer
			}
		}
	}
}

func buildCommands() []*command {
	return []*command{
		// Attachments. Not idleOnly: staging bytes for the next
		// message touches nothing the running turn is using.
		{name: "/paste", run: always(Model.runPaste)},
		{name: "/attach", run: always(Model.attachCommand)},
		{name: "/secret", aliases: []string{"/secrets"},
			// Not idleOnly: adding a secret mid-turn is exactly when the
			// command is wanted — the model just asked for a key it lacks.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
				return m.secretCommand(parts[1:])
			})},
		{name: "/skill",
			// Explicit activation. Not idleOnly: while the agent works the
			// content queues as steering, like any typed text.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
				if len(parts) < 2 {
					return m.surfaceNotice("usage: /skill <name> [task]. /skills lists what can be activated")
				}
				return m.activateSkill(parts[1], strings.Join(parts[2:], " "))
			})},
		{name: "/plan",
			// The approved plan's checklist, read whole on the steps screen —
			// the list the rail's PLAN block draws and whose heading opens the
			// same screen. Not idleOnly: mid-turn is when somebody asks where
			// the plan has got to, and below 130 columns there is no rail.
			run: func(m Model, parts []string) (tea.Model, tea.Cmd, bool) {
				if len(parts) != 1 || m.planRun == nil {
					return m, nil, false
				}
				next, cmd := m.openSteps()
				return next, cmd, true
			},
			answer: slashPlan},
		{name: "/detach", run: always(func(m Model, _ []string) (tea.Model, tea.Cmd) {
			if m.attachedTo == "" {
				return m.surfaceNotice("not attached to an agent. /attach <name> or /agents to pick one")
			}
			m.detachOne()
			return m, nil
		})},
		{name: "/clear", aliases: []string{"/new"},
			// The session boundary (model.go). Over a turn that is not over it
			// asks first, the way quitting does and for the same reason: what a
			// yes costs is the work the reader may not have noticed running, and
			// a boundary is never crossed mid-turn.
			run: always(func(m Model, _ []string) (tea.Model, tea.Cmd) {
				if m.turnInFlight() {
					return m.openNewSessionConfirm()
				}
				notes, save := m.startNewSession()
				next, cmd := m.systemEntries(notes)
				return next, tea.Batch(cmd, save)
			})},
		{name: "/exit", aliases: []string{"/quit", "/q"},
			// A typed command is deliberate, so an idle quit goes straight
			// out; over a live turn even it confirms, because what it costs is
			// the turn's work, not the reader's time.
			run: always(func(m Model, _ []string) (tea.Model, tea.Cmd) {
				if m.working() {
					return m.openQuitConfirm()
				}
				return m, m.quitNow()
			})},
		{name: "/run",
			// Bare /run with several code blocks opens the picker; one
			// block, /run <n>, and every no-op case go straight to startRun.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
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
			})},
		{name: "/status", exact: true,
			// The rail's SUMMARY block in words, for the terminals
			// below 130 columns that have no rail to draw it in — the same answer
			// the rail's rules give for PLAN. It takes a fresh reading on the way out:
			// asking for the summary is a reason to have a current one.
			run: bareRun(func(m Model) (tea.Model, tea.Cmd) {
				note, read := m.statusCommand()
				next, cmd := m.systemNotice(note)
				return next, tea.Batch(cmd, read)
			})},
		// The last response onto the clipboard. A run rather than an answer
		// because the copy may be a write the terminal takes, and an answer
		// has nowhere to put one.
		{name: "/copy", run: always(Model.copyCommand)},
		{name: "/compact", exact: true, run: bareRun(Model.startCompact)},
		// Bare /rewind opens the checkpoint picker; the numbered form is
		// the answer.
		{name: "/rewind", exact: true, run: bareRun(Model.openRewindPick), answer: slashRewind},
		// The in-flight step's detail, from the draft — the chord that
		// answered this went to reading mode, and the question it answered
		// is still asked mid-turn with a half-written sentence in the box
		// (detail.go).
		{name: "/step", exact: true, run: bareRun(Model.detailFromDraft)},
		// Put a turn's edits back from the session's own records;
		// bare takes the most recent turn that changed anything.
		{name: "/undo", run: always(Model.undoCommand)},
		{name: "/model", exact: true,
			// Bare /model opens the model picker; the named form and
			// sessions with nothing to pick get the answer. A provider that
			// can enumerate its endpoint is queried first.
			run: picks(func(m Model) (tea.Model, tea.Cmd, bool) {
				if !m.canPickModel() {
					return m, nil, false
				}
				next, cmd := m.startModelPick()
				return next, cmd, true
			}),
			answer: slashModel},
		// Bare /permissions opens the mode picker.
		{name: "/permissions", aliases: []string{"/perms", "/mode"}, exact: true,
			run: bareRun(Model.openModePick), answer: slashPermissions},
		// Bare /load and /chats open the saved-chat picker; with nothing
		// saved they go on to the listing their answer gives.
		{name: "/load", exact: true, run: picks(Model.openChatPick), answer: slashLoad},
		{name: "/chats", exact: true, run: picks(Model.openChatPick), answer: slashChats},
		{name: "/help",
			// Bare /help is the key list's register row (keypopup.go). With words
			// after it, it writes the whole help — a sheet laid out at the pane's
			// width rather than a sentence (helpsheet.go), so it is appended as one.
			run: always(func(m Model, _ []string) (tea.Model, tea.Cmd) {
				return m.helpNotice(m.helpSheet())
			}),
			answer: slashHelp},
		{name: "/ui",
			// /ui mouse flips the terminal's own reporting. That is
			// a field on the View rather than a command back to the program, so
			// this setting takes the same path as every other /ui setting: change
			// the model, say so in the transcript.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
				return m.systemNotice(m.uiCommand(parts))
			}),
			answer: slashUI},
		{name: "/trust",
			// The one answer that decides whether the checkout's skills, agent
			// profiles, quality suites and servers load at all. Like trusting a
			// server, it lands in the next session: the prompt naming the skills
			// and the toolset holding the gate were built when this one started.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
				return m.systemNotice(m.trustCommand(parts[1:]))
			})},
		// The scaffolding card: what it would write, before it writes it.
		{name: scaffoldCommandName, exact: true, run: bareRun(Model.scaffoldCommand)},
		{name: "/memory",
			// /memory edit hands the entry's text to the editor; every other
			// /memory subcommand is textual and gets the answer.
			run: func(m Model, parts []string) (tea.Model, tea.Cmd, bool) {
				if len(parts) < 2 || parts[1] != "edit" {
					return m, nil, false
				}
				if len(parts) != 3 {
					next, cmd := m.systemNotice("usage: /memory edit <id>")
					return next, cmd, true
				}
				next, cmd := m.openMemoryEditor(parts[2])
				return next, cmd, true
			},
			answer: slashMemory},
		// Bare /branches opens the branch picker; a session with no
		// branch family gets the answer.
		{name: "/branches", exact: true, run: picks(Model.openBranchPick), answer: slashBranches},
		{name: "/reasoning", aliases: []string{"/think"}, answer: slashReasoning},
		{name: "/add-dir", aliases: []string{"/adddir"}, answer: slashAddDir},
		{name: "/sandbox", answer: slashSandbox},
		{name: "/evidence", answer: slashEvidence},
		{name: "/gate", answer: slashGate},
		{name: "/ps", answer: slashProcesses},
		{name: "/mcp", answer: slashMCP},
		{name: "/skills", answer: slashSkills},
		{name: "/save", answer: slashSave},
		{name: "/sessions", answer: slashSessions},
	}
}

func slashHelp(m *Model, _ []string) string {
	return helpText(m)
}

func slashModel(m *Model, parts []string) string {
	if len(parts) < 2 {
		if m.modelName != "" {
			return fmt.Sprintf("current model: %s%s\n%s", m.modelName, m.modelChosenBy(), modelUsage)
		}
		return modelUsage
	}
	// /model default [name] and /model agents [name] persist a default to
	// the config file instead of switching this session only.
	if parts[1] == "default" || parts[1] == "agents" {
		return m.setModelDefault(parts[1], parts[2:])
	}
	if m.switchFn == nil {
		return "model switching is not available in this session"
	}
	if len(parts) > 2 {
		return "model names cannot contain spaces. " + modelUsage
	}
	name := parts[1]
	if name == m.modelName {
		return fmt.Sprintf("already using %s", name)
	}
	m.switchFn(name)
	m.modelName = name
	return fmt.Sprintf("switched model to %s. (/model default %s makes it the default for new sessions.)", name, name)
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
		return "usage: /permissions [manual|accept-edits|auto|read-only|plan|why|grants|allow|revoke]"
	}
	if parts[1] == "why" {
		if m.lastDenial == "" {
			return "no auto-mode denials this session"
		}
		return "last auto-mode denial:\n  " + m.lastDenial
	}
	mode, err := agent.ParseMode(parts[1])
	if err != nil {
		return failed("permissions", err.Error())
	}
	if m.conversation {
		return conversationModeNote
	}
	m.applyMode(mode)
	return fmt.Sprintf("mode set to %s — %s", mode, mode.Describe())
}

func slashReasoning(m *Model, parts []string) string {
	return m.reasoningCommand(parts[1:])
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
			return "command containment is not configured in this session"
		}
		return m.containment.Report
	}
	return "container sandbox management is unavailable in this session"
}

func slashEvidence(m *Model, parts []string) string {
	if m.evidence.Manage == nil {
		return "the evidence store is unavailable in this session"
	}
	return m.evidence.Manage(parts[1:])
}

func slashGate(m *Model, parts []string) string {
	if m.gate.Manage == nil {
		return "the quality gate is unavailable in this session"
	}
	if handled, note := m.gateToggle(parts[1:]); handled {
		return note
	}
	return m.gate.Manage(parts[1:])
}

func slashProcesses(m *Model, parts []string) string {
	if m.processes.Manage == nil {
		return "the process supervisor is unavailable in this session"
	}
	return m.processes.Manage(parts[1:])
}

func slashMemory(m *Model, parts []string) string {
	if m.memory.Manage == nil {
		return "durable memory is unavailable in this session"
	}
	return m.memory.Manage(parts[1:])
}

func slashMCP(m *Model, parts []string) string {
	if m.mcp.Manage == nil {
		return "no MCP servers in this session. Define one under [mcp.servers] in your config, or in mcp.json beside it; `shhh mcp` lists what a session here would connect"
	}
	return m.mcp.Manage(parts[1:])
}

func slashSessions(m *Model, _ []string) string {
	if m.sessions == nil {
		return "the sessions on this machine are not readable from here; `shhh sessions` lists them"
	}
	return m.sessions()
}

func slashSkills(m *Model, _ []string) string {
	if m.skills == nil {
		return "no skills loaded in this session. A skill is a directory holding a SKILL.md under .shhh/skills, .agents/skills or .claude/skills, in the project or your home directory"
	}
	return m.skillsList(m.skills)
}

func slashPlan(m *Model, parts []string) string {
	// Bare /plan with an approved plan running opens the steps screen over
	// it (runCommand); what reaches here bare is a session with no plan to
	// show, which is told so.
	if len(parts) == 1 {
		return m.planStatus()
	}
	switch parts[1] {
	case "save":
		planText := m.lastAssistantText()
		if strings.TrimSpace(planText) == "" {
			return "no plan to save yet — there is no assistant response"
		}
		path, err := savePlan(m.workspace, planText, strings.Join(parts[2:], "-"))
		if err != nil {
			return failed("plan", "could not save it: "+err.Error())
		}
		return "plan saved to " + path
	case "drop":
		if m.planRun == nil {
			return "no approved plan is running"
		}
		m.planRun = nil
		m.invalidateRenderCache()
		return "dropped the approved plan — the outline goes back to inferring its steps"
	}
	return planUsage
}

func slashRewind(m *Model, parts []string) string {
	// Only the numbered form arrives here; bare /rewind opens the picker
	// from the enter handler.
	if len(m.checkpoints) == 0 {
		return "no checkpoints to rewind to yet"
	}
	if len(parts) != 2 {
		return fmt.Sprintf("usage: /rewind [<turn 0-%d>] — bare /rewind opens the picker", len(m.checkpoints))
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return m.rewindUsage()
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
// `code`, one of the code blocks in it or all of them (copyCode).
//
// The terminal is offered the text before any tool on this machine
// (copyText), which is what carries the copy back over ssh to the reader
// instead of leaving it on the server they are connected to. So the answer
// is a clipboard write as well as a line for the transcript, which is more
// than a command's answer can hand back: /copy is a run.
func (m Model) copyCommand(parts []string) (tea.Model, tea.Cmd) {
	text := m.lastAssistantText()
	if text == "" {
		return m.systemNotice("nothing to copy yet")
	}
	if len(parts) > 1 && parts[1] == "code" {
		return m.copyCode(text, parts[2:])
	}
	return m.copyWhole(text, "response")
}

func slashSave(m *Model, parts []string) string {
	if m.db == nil {
		return "chat persistence is unavailable"
	}
	name := "unnamed"
	if len(parts) > 1 {
		name = strings.Join(parts[1:], " ")
	}
	if err := m.db.SaveChat(name, stripResumeContext(m.agent.Messages())); err != nil {
		return failed("save", err.Error())
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
		Summary: m.compactSummary, Head: project.Head(m.workspace), Root: project.Root(m.workspace),
		Steps: m.workSteps.Encode()})
	// Future rewind branches hang off the named session.
	m.adoptSlot(name)
	return fmt.Sprintf("chat saved as %q", name)
}

func slashLoad(m *Model, parts []string) string {
	if m.db == nil {
		return "chat persistence is unavailable"
	}
	if len(parts) < 2 {
		// Only reached when there is nothing to pick; otherwise bare
		// /load opens the picker from the enter handler.
		_, listing := m.handleSlashCommand("/chats")
		return listing + "\n\nusage: /load <name>"
	}
	return m.loadChatByName(strings.Join(parts[1:], " "))
}

func slashChats(m *Model, _ []string) string {
	if m.db == nil {
		return "chat persistence is unavailable"
	}
	entries, err := m.db.ListChats()
	if err != nil {
		return failed("chats", err.Error())
	}
	if len(entries) == 0 {
		return "no saved chats"
	}
	var sb strings.Builder
	sb.WriteString("saved chats:\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "  %s  (%s)\n", e.Name, chatDesc(e))
	}
	return strings.TrimRight(sb.String(), "\n")
}
