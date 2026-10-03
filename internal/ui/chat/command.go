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
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	// slash is the command's completion row and /help paragraph: what the
	// menu, the palette and /help offer under the name (complete.go). Its
	// name is this row's; its aliases are the ones the menu matches while
	// filtering, which leave out a spelling answered only so a slip of habit
	// is not an unknown command. nil is a command whose row is declared on
	// the register row of the surface it opens (overlay.go).
	slash *slashCommand
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
		{name: "/paste",
			slash: &slashCommand{args: "[path|show <handle>|drop [handle]|clear]", desc: "attach the clipboard, or a file, to your next message",
				key: keys.Shown(keys.Draft.Attach),
				argSpecs: []argSpec{
					{options: []argOption{
						{"show", "look at a staged image"},
						{"drop", "take one attachment back out"},
						{"clear", "drop what is staged"},
					}},
					{after: []string{"drop"}, dynamic: attachmentDropArgs},
					{after: []string{"show"}, dynamic: attachmentShowArgs},
				},
				help: `attach the clipboard — a screenshot, or files copied in a file manager — to your next message; /paste <path> attaches a file by name, /paste show <handle> opens a staged image or paste full-pane, /paste drop <handle> takes one back out — the handle is the one a chip leads with, Image#1, and its name works too — and /paste clear drops what is staged (ctrl+v). With a sentence half typed, reading mode (ctrl+o) keeps it and reaches the strip of chips as its last row: ←→ picks a chip, enter opens it, x drops it, esc goes back to the sentence; a click on a chip opens it too`},
			run: always(Model.runPaste)},
		{name: "/attach",
			slash: &slashCommand{args: "[name]", desc: "attach to an agent's session and steer it",
				enabled:  func(m *Model) bool { return m.subagents != nil },
				argSpecs: []argSpec{{dynamic: agentArgs, fuzzy: true}},
				help:     `attach to an agent's session and steer it (bare /attach lists)`},
			run: always(Model.attachCommand)},
		{name: "/secret",
			slash: &slashCommand{args: "[list|set|forget]", desc: "values commands can use and the model never sees",
				enabled: func(m *Model) bool { return m.secrets.Manage != nil },
				argSpecs: staticArgs(
					argOption{"list", "name the session's secrets"},
					argOption{"set", "declare one: NAME from the environment, or NAME=value"},
					argOption{"forget", "drop one by name"},
				),
				help: `values a command may use and the model never sees: list names them, set NAME takes one from your environment (or NAME=value declares it outright), forget NAME drops it. What a command prints is scrubbed of them before it reaches the transcript`},
			aliases: []string{"/secrets"},
			// Not idleOnly: adding a secret mid-turn is exactly when the
			// command is wanted — the model just asked for a key it lacks.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
				return m.secretCommand(parts[1:])
			})},
		{name: "/skill",
			slash: &slashCommand{args: "<name> [task]", desc: "activate a skill now, with your task after it",
				enabled:  func(m *Model) bool { return m.skills.Len() > 0 },
				argSpecs: []argSpec{{dynamic: skillArgs}},
				help:     `activate a skill now: /skill <name> [task] sends its instructions to the model with your task, as the model would load them itself. /<name> does the same for a skill whose name is not a command`},
			// Explicit activation. Not idleOnly: while the agent works the
			// content queues as steering, like any typed text.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
				if len(parts) < 2 {
					return m.surfaceNotice("usage: /skill <name> [task]. /skills lists what can be activated")
				}
				return m.activateSkill(parts[1], strings.Join(parts[2:], " "))
			})},
		{name: "/plan",
			slash: &slashCommand{args: "[save|drop]", desc: "the approved plan as a checklist, with anything that has departed from it",
				enabled: func(m *Model) bool { return m.codingSurfaces() },
				argSpecs: staticArgs(
					argOption{"save", "write the last plan/response to .shhh/plans/"},
					argOption{"drop", "forget the approved plan; steps go back to inferred"},
				),
				help: `the approved plan as a checklist on the steps screen — each step, the paths it named, its state and the calls that carried it out — with anything that has departed from it · save [name] writes the last plan/response to .shhh/plans/ · drop forgets an approved plan`},
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
		{name: "/detach",
			slash: &slashCommand{desc: "back to the orchestrator (also esc)",
				enabled: func(m *Model) bool { return m.subagents != nil && m.attachedTo != "" },
				help:    `back to your own session (also esc while attached)`},
			run: always(func(m Model, _ []string) (tea.Model, tea.Cmd) {
				if m.attachedTo == "" {
					return m.surfaceNotice("not attached to an agent. /attach <name> or /agents to pick one")
				}
				m.detachOne()
				return m, nil
			})},
		{name: "/clear",
			// Not idleOnly, though it replaces the conversation: a turn that is not
			// over is exactly when ending the session is worth asking about, so the
			// command stays offered mid-turn and answers with the confirm quitting
			// draws (cancel.go).
			slash: &slashCommand{aliases: []string{"/new"}, desc: "start a new session",
				help: `end this session and start another (also /new)`},
			aliases: []string{"/new"},
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
		{name: "/exit",
			slash: &slashCommand{aliases: []string{"/quit", "/q"}, desc: "quit (also /quit, /q)", key: keys.Shown(keys.Draft.Quit),
				help: `quit (also /quit, /q)`},
			aliases: []string{"/quit", "/q"},
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
			slash: &slashCommand{args: "[n]", desc: "run a code block from the last response",
				enabled:  func(m *Model) bool { return m.runFn != nil },
				idleOnly: "it runs a command in this session",
				help:     `run a code block from the last response (with confirmation)`},
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
		{name: "/status",
			slash: &slashCommand{desc: "where the session is, and whether it is still on target",
				help: `where this session is: what it is working on, what it has spent, and whether the last few turns are still on the target you set it`},
			exact: true,
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
		{name: "/copy",
			slash: &slashCommand{args: "[code]", desc: "copy the last response (or just its code blocks)",
				argSpecs: []argSpec{
					{options: []argOption{{"code", "only the code blocks"}}},
					{after: []string{"code"}, options: []argOption{{"all", "every block, joined"}}},
				},
				help: `copy the last response (or just its code blocks)`},
			run: always(Model.copyCommand)},
		{name: "/compact",
			slash: &slashCommand{desc: "continue from a summary plus the most recent turns",
				idleOnly: "it rewrites the conversation into a summary",
				help:     `continue from a summary plus the most recent turns`},
			exact: true, run: bareRun(Model.startCompact)},
		// Bare /rewind opens the checkpoint picker; the numbered form is
		// the answer.
		{name: "/rewind",
			slash: &slashCommand{args: "[n]", desc: "rewind to the end of a turn — the conversation, the files, or both",
				argSpecs: []argSpec{{dynamic: checkpointArgs}},
				idleOnly: "it rewinds the conversation and can write files back",
				help:     `rewind to the end of turn [n], 0 being the start (bare /rewind picks interactively); the abandoned tail is kept as a branch, and a card asks whether the files come back too`},
			exact: true, run: bareRun(Model.openRewindPick), answer: slashRewind},
		// The in-flight step's detail, from the draft — the chord that
		// answered this went to reading mode, and the question it answered
		// is still asked mid-turn with a half-written sentence in the box
		// (detail.go).
		{name: "/step",
			slash: &slashCommand{desc: "open the in-flight step's card onto its calls (again closes it)",
				help: `open the in-flight step's card onto its calls, every call with its output body, bounded; run it again to close (/ui verbosity high is the same thing for every card at once)`},
			exact: true, run: bareRun(Model.detailFromDraft)},
		// Put a turn's edits back from the session's own records;
		// bare takes the most recent turn that changed anything.
		{name: "/undo",
			slash: &slashCommand{args: "[turn]", desc: "put back what a turn changed (asks first)",
				enabled:  func(m *Model) bool { return m.changes != nil && m.codingSurfaces() },
				argSpecs: []argSpec{{dynamic: reviewTurnArgs}},
				idleOnly: "it writes files the running turn may be editing",
				help:     `put back what a turn changed, from the session's own records (not git). Asks first, names anything that changed since, and is itself recorded as a turn`},
			run: always(Model.undoCommand)},
		{name: "/model",
			slash: &slashCommand{args: "[name]", desc: "switch the model (bare /model opens a picker)",
				argSpecs: []argSpec{{dynamic: modelArgs, fuzzy: true}},
				idleOnly: "it switches the model the running turn is using",
				help: `switch the model (bare /model opens an interactive picker)
default [name]   show or persist the default model for new sessions
agents [name]    show or persist the model sub-agents run on ("inherit" follows the session model)`},
			exact: true,
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
		{name: "/permissions",
			slash: &slashCommand{args: "[name|grants|allow|revoke|why]", desc: "what runs without asking, and the mode that frames it",
				aliases: []string{"/perms", "/mode"},
				key:     keys.Shown(keys.Draft.Mode),
				argSpecs: []argSpec{
					{dynamic: modeArgs},
					{after: []string{"allow"}, options: []argOption{
						{"commands", "every command runs without asking"},
						{"edits", "every edit applies without asking"},
					}},
					{after: []string{"revoke"}, options: []argOption{
						{"edits", "only the edit grants"},
						{"commands", "only the command grants"},
						{"hosts", "only the fetch host grants"},
					}},
				},
				help: `what runs without asking, and the permission mode that frames it (also /perms; was /mode)
[name]   bare opens a picker over the five modes:
         manual asks about every consequential call
         accept-edits applies the edits and asks for the rest
         auto adds the allowlist and lets the classifier judge
         read-only writes nothing — a write is refused, not asked
         plan is read-only, ending on a plan you can approve here or carry into a fresh session
why      the latest auto-mode denial's reason
grants   what this session has stopped asking about
allow <commands|edits>   grant a whole category
revoke [commands|edits|hosts|agents]   take the grants back`},
			aliases: []string{"/perms", "/mode"}, exact: true,
			run: bareRun(Model.openModePick), answer: slashPermissions},
		// Bare /load and /chats open the saved-chat picker; with nothing
		// saved they go on to the listing their answer gives.
		{name: "/load",
			slash: &slashCommand{args: "[name]", desc: "load a saved chat (bare /load picks)",
				enabled:  func(m *Model) bool { return m.db != nil },
				argSpecs: []argSpec{{dynamic: chatArgs, fuzzy: true}},
				idleOnly: "it replaces the conversation",
				help:     `load a saved chat (bare /load opens a picker)`},
			exact: true, run: picks(Model.openChatPick), answer: slashLoad},
		{name: "/chats",
			slash: &slashCommand{desc: "saved chats — enter loads, x deletes, r renames",
				enabled:  func(m *Model) bool { return m.db != nil },
				idleOnly: "it opens the picker that replaces the conversation",
				help:     `saved chats — opens the same picker; enter loads, [x] deletes (asks first), [r] renames`},
			exact: true, run: picks(Model.openChatPick), answer: slashChats},
		{name: "/help",
			// Bare /help is the key list's register row (keypopup.go). With words
			// after it, it writes the whole help — a sheet laid out at the pane's
			// width rather than a sentence (helpsheet.go), so it is appended as one.
			run: always(func(m Model, _ []string) (tea.Model, tea.Cmd) {
				return m.helpNotice(m.helpSheet())
			}),
			answer: slashHelp},
		{name: "/ui",
			slash: &slashCommand{args: "verbosity <low|normal|high> | mono <on|off>", desc: "screen density and monochrome mode",
				argSpecs: []argSpec{
					{options: []argOption{
						{"verbosity", "how much the screen explains"},
						{"theme", "which colour table every surface draws with"},
						{"ground", "paint the screen with the theme's own background"},
						{"mono", "strip every surface to two greys"},
						{"mouse", "whether shhh or the terminal owns the mouse"},
						{"notify", "say so when a turn stops and you are elsewhere"},
						{"title", "name an unnamed session after its first turn"},
						{"suggest", "offer a next step in the empty draft after each turn"},
						{"window", "name the terminal's own tab after this session"},
						{"rail", "how many columns the inspector rail takes"},
						{"terminal", "what this terminal can do"},
					}},
					{after: []string{"verbosity"}, options: []argOption{
						{"low", "each card its header alone, thinking left out"},
						{"normal", "each card its header, sentence and evidence"},
						{"high", "every card open on its calls"},
					}},
					{after: []string{"theme"}, options: []argOption{
						{components.ThemeAuto, "The table chosen for the background this terminal reports"},
						{components.ThemeDark, "The product's own colours, on a dark ground"},
						{components.ThemeLight, fmt.Sprintf("The same %d jobs, on a light ground", components.PaletteSize)},
						{components.ThemeCharm, fmt.Sprintf("The same %d jobs in CharmTone", components.PaletteSize)},
					}},
					{after: []string{"ground"}, options: []argOption{
						{"on", "paint the background the theme was drawn against"},
						{"off", "leave the terminal's own background"},
					}},
					{after: []string{"mono"}, options: []argOption{
						{"on", "two greys — glyphs and words carry every state"},
						{"off", "the full palette"},
					}},
					{after: []string{"mouse"}, options: []argOption{
						{"on", "the wheel scrolls, click-drag selects, a click opens a row"},
						{"off", "the terminal keeps its own click-drag selection"},
					}},
					{after: []string{"notify"}, options: []argOption{
						{"on", "one notification when a turn stops and the window is not in front"},
						{"off", "a turn that stops while you are elsewhere waits silently"},
					}},
					{after: []string{"title"}, options: []argOption{
						{"on", "the summary model names the session after its first turn"},
						{"off", "sessions keep the timestamp they were opened at"},
					}},
					{after: []string{"suggest"}, options: []argOption{
						{"on", "a cheap model offers the obvious next message; → takes it"},
						{"off", "the empty draft stays empty and nothing is asked"},
					}},
					{after: []string{"window"}, options: []argOption{
						{"on", "the tab says the command, the directory, and ⏸ while a decision waits"},
						{"off", "the tab keeps whatever your terminal puts there"},
					}},
					{after: []string{"rail"}, dynamic: railArgs},
				},
				help: `screen density, pane layout, monochrome and mouse: /ui verbosity <low|normal|high> · /ui mono <on|off> · /ui mouse <on|off>
low draws each step's card as its header alone and leaves thinking out, normal draws the header, the sentence and the evidence, high opens every card onto its calls; a card you opened or closed stays as you left it. The mouse is on by default so the wheel scrolls the transcript, click-drag selects it, and clicks open rows, open or close a card by its header, or answer keys; off hands selection back to the terminal, and ctrl+x flips it and saves it
terminal   what this terminal answered when shhh asked what it can do: inline images, desktop notifications, focus events, cell size`},
			// /ui mouse flips the terminal's own reporting. That is
			// a field on the View rather than a command back to the program, so
			// this setting takes the same path as every other /ui setting: change
			// the model, say so in the transcript.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
				return m.systemNotice(m.uiCommand(parts))
			}),
			answer: slashUI},
		{name: "/trust",
			slash: &slashCommand{desc: "let this checkout's skills, agent profiles and quality suites load (\"off\" withdraws it)",
				help: `let this checkout's own skills, agent profiles, wordings and quality suites load. A clone can carry instructions, so nothing of a checkout's runs until you say so; "off" withdraws it and the next session starts without them`},
			// The one answer that decides whether the checkout's skills, agent
			// profiles, quality suites and servers load at all. Like trusting a
			// server, it lands in the next session: the prompt naming the skills
			// and the toolset holding the gate were built when this one started.
			run: always(func(m Model, parts []string) (tea.Model, tea.Cmd) {
				return m.systemNotice(m.trustCommand(parts[1:]))
			})},
		// The scaffolding card: what it would write, before it writes it.
		{name: scaffoldCommandName,
			slash: &slashCommand{desc: "scaffold this project's .shhh/ context file (asks first)",
				enabled:  func(m *Model) bool { return m.scaffold.Write != nil },
				idleOnly: "it writes a file into the checkout",
				help:     `scaffold this project's .shhh/ context file — the card lists what it would write, and nothing is written until you say so. The start screen offers it in a checkout that has no .shhh`},
			exact: true, run: bareRun(Model.scaffoldCommand)},
		{name: "/memory",
			slash: &slashCommand{args: "[list|add|edit|forget]", desc: "durable memories",
				enabled: func(m *Model) bool { return m.memory.Manage != nil },
				argSpecs: staticArgs(
					argOption{"list", "show stored memories"},
					argOption{"add", "remember something"},
					argOption{"edit", "reword a memory by id, in your editor"},
					argOption{"forget", "drop a memory by id"},
				),
				help: `durable memories: list (default) · add [global] [kind] <text> · edit <id> (opens the entry in your editor) · forget <id>`},
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
		{name: "/branches",
			slash: &slashCommand{args: "[n|name]", desc: "switch this session's branches (bare /branches picks)",
				enabled:  func(m *Model) bool { return m.db != nil },
				argSpecs: []argSpec{{dynamic: branchArgs, fuzzy: true}},
				idleOnly: "it switches the conversation to another branch",
				help:     `switch this session's branches: [n] by number, [name] by name, bare opens a picker`},
			exact: true, run: picks(Model.openBranchPick), answer: slashBranches},
		{name: "/reasoning",
			slash: &slashCommand{args: "[off|low|medium|high|xhigh|max|default]", desc: "how much the model thinks before it answers",
				aliases: []string{"/think"},
				key:     keys.Shown(keys.Draft.Reasoning),
				argSpecs: []argSpec{
					{dynamic: reasoningArgs},
					{after: []string{"default"}, options: reasoningLevelArgs()},
				},
				help: `how much thinking the model does before it answers: off (the default), low, medium, high, xhigh or max — ctrl+t cycles them
[level]           set it for this session (also /think)
default [level]   show or persist the level new sessions start on (provider.reasoning)`},
			aliases: []string{"/think"}, answer: slashReasoning},
		{name: "/add-dir",
			slash: &slashCommand{args: "[<path>|drop <path>]", desc: "the directories this session may work in",
				enabled: func(m *Model) bool { return m.scope != nil },
				argSpecs: []argSpec{
					{options: []argOption{{"drop", "take a directory back out of the scope"}}},
					{after: []string{"drop"}, dynamic: scopeDropArgs},
				},
				help: `the working scope: which directories this session may write to. Bare lists it; <path> adds one (contained commands can write there, and edits there stop asking about leaving the scope); drop <path> takes it back`},
			aliases: []string{"/adddir"}, answer: slashAddDir},
		{name: "/sandbox",
			slash: &slashCommand{args: "[doctor|scope|list|status|destroy|prune]", desc: "containment status and container sandboxes",
				enabled: func(m *Model) bool { return m.codingSurfaces() },
				argSpecs: staticArgs(
					argOption{"doctor", "report containment support"},
					argOption{"scope", "the directories commands may write to"},
					argOption{"list", "list container sandboxes"},
					argOption{"status", "this session's sandbox"},
					argOption{"destroy", "destroy a sandbox by id"},
					argOption{"prune", "remove stopped sandboxes"},
				),
				help: `containment status and container sandboxes (doctor|scope|list|status|destroy <id>|prune)`},
			answer: slashSandbox},
		{name: "/evidence",
			slash: &slashCommand{args: "[purge]", desc: "tool-output evidence store",
				enabled:  func(m *Model) bool { return m.evidence.Manage != nil },
				argSpecs: staticArgs(argOption{"purge", "delete stored tool output"}),
				help:     `tool-output evidence store: reduction stats and size (purge to clear)`},
			answer: slashEvidence},
		{name: "/gate",
			slash: &slashCommand{args: "[run|result|on|off]", desc: "run the project's quality gate",
				enabled: func(m *Model) bool { return m.gate.Manage != nil },
				argSpecs: staticArgs(
					argOption{"run", "run the gate suites"},
					argOption{"result", "show the last result"},
					argOption{"on", "run the suite as a turn that changed files closes"},
					argOption{"off", "stop running it at a turn's close"},
				),
				help: `quality gate: run [suite] starts the project's checks in the background, result shows the verdict, on|off runs them as a turn closes`},
			answer: slashGate},
		{name: "/ps",
			slash: &slashCommand{desc: "list session-owned long-running processes",
				enabled: func(m *Model) bool { return m.processes.Manage != nil },
				help:    `list the long-running processes this session owns (process tool)`},
			answer: slashProcesses},
		{name: "/mcp", answer: slashMCP},
		{name: "/skills",
			slash: &slashCommand{desc: "the skills this session loaded, and why any did not",
				help: `the skills this session loaded (SKILL.md directories), and why any did not`},
			answer: slashSkills},
		{name: "/save",
			slash: &slashCommand{args: "[name]", desc: "save this chat",
				enabled: func(m *Model) bool { return m.db != nil },
				help:    `save this chat`},
			answer: slashSave},
		{name: "/sessions",
			slash: &slashCommand{desc: "the sessions running on this machine, and where each one is",
				help: `the sessions running on this machine: the conversation each saves to, its checkout and branch, and whether it is working`},
			answer: slashSessions},
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
