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
	"strings"

	tea "charm.land/bubbletea/v2"
)

// submitInput handles Enter on the orchestrator surface, in every state that
// keeps the input live.
func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	// With the completion menu open, enter runs the highlighted command
	// rather than the raw prefix; on an argument row it completes the
	// token first and runs the whole line. A file-mention row is inserted,
	// never run — the sentence is still being written (mention.go).
	if m.completionActive() {
		if m.completeFiles {
			return m.insertMention()
		}
		if m.completeArg {
			m.acceptCompletion()
			text = strings.TrimSpace(m.input.Value())
		} else {
			text = m.completions[m.completeIdx].name
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
	if name := commandName(text); name != "" {
		return m.runCommand(text, name)
	}
	// A profile being drafted owns the next line: the brief, or the
	// answers to what the drafter asked.
	if m.personaHoldsInput() {
		return m.answerPersona(text)
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
	if m.working() || m.decisionUngated() {
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

	case text == "/compact":
		return m.startCompact()

	case text == "/rewind":
		// Bare /rewind opens the checkpoint picker; the numbered form
		// goes through handleSlashCommand.
		return m.openRewindPick()

	case text == "/diff":
		// The cumulative session diff, full screen.
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

	case text == scaffoldCommandName:
		// The scaffolding card: what it would write, before it writes it.
		return m.scaffoldCommand()

	case name == "/todo":
		// Bare /todo opens the backlog picker; the subcommands are textual,
		// and edit hands the item file to the editor.
		return m.todoCommand(parts)

	case text == "/branches":
		// Bare /branches opens the branch picker; a session with no
		// branch family falls through.
		if picked, cmd, ok := m.openBranchPick(); ok {
			return picked, cmd
		}
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
