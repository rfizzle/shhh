package chat

// Submitting the input (S-087). Enter used to mean three unrelated things
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
	// rather than the raw prefix (S-078); on an argument row it completes the
	// token first and runs the whole line (S-079).
	if m.completionActive() {
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
	m.recordInput(text)
	m.input.Reset()
	if name := commandName(text); name != "" {
		return m.runCommand(text, name)
	}
	if m.working() || m.decisionUngated() {
		// Typed while the agent works: the message joins the conversation
		// before the next model request (S-058). A turn paused on a decision
		// is a turn in flight for this purpose — enter queues the sentence
		// for the next round rather than starting a turn the pending
		// decision would immediately interrupt (S-117).
		m.steering = append(m.steering, text)
		// The queued count surfaces on the notice rail (S-082).
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
	parts := strings.Fields(text)
	switch {
	case name == "/paste":
		// Attachments (S-134). Not idleOnly: staging bytes for the next
		// message touches nothing the running turn is using.
		return m.runPaste(parts)

	case name == "/agents":
		return m.openAgentList()

	case name == "/attach":
		return m.attachCommand(parts)

	case name == "/detach":
		if m.attachedTo == "" {
			return m.surfaceNotice("Not attached to an agent. /attach <name> or /agents to pick one.")
		}
		m.detachOne()
		return m, nil

	case name == "/exit" || name == "/quit" || name == "/q":
		m.quitting = true
		m.cancelSubagents()
		if m.cancel != nil {
			m.cancel()
		}
		if m.runCancel != nil {
			m.runCancel()
		}
		return m, m.quitCmd()

	case name == "/run":
		// Bare /run with several code blocks opens the picker (S-081); one
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
		// The rail's SUMMARY block in words (S-163), for the terminals
		// below 130 columns that have no rail to draw it in — the same answer
		// the rail's rules give for PLAN. It takes a fresh reading on the way out:
		// asking for the summary is a reason to have a current one.
		note, read := m.statusCommand()
		next, cmd := m.systemNotice(note)
		return next, tea.Batch(cmd, read)

	case text == "/compact":
		return m.startCompact()

	case text == "/rewind":
		// Bare /rewind opens the checkpoint picker (S-069); the numbered form
		// goes through handleSlashCommand.
		return m.openRewindPick()

	case text == "/diff":
		// The cumulative session diff, full screen (S-074).
		return m.openSessionDiff()

	case name == "/review":
		// Review mode over a turn's changeset (S-099); bare takes the
		// most recent turn that changed anything.
		return m.reviewCommand(parts)

	case name == "/undo":
		// Put a turn's edits back from the session's own records (S-100);
		// bare takes the most recent turn that changed anything.
		return m.undoCommand(parts)

	case text == "/model" && m.canPickModel():
		// Bare /model opens the model picker (S-078); the named form and
		// sessions with nothing to pick go through handleSlashCommand. A
		// provider that can enumerate its endpoint is queried first (S-083).
		return m.startModelPick()

	case text == "/permissions" || text == "/perms" || text == "/mode":
		// Bare /permissions opens the mode picker (S-078).
		return m.openModePick()

	case text == "/load" || text == "/chats":
		// Bare /load and /chats open the saved-chat picker (S-080); with
		// nothing saved they fall through to the listing below.
		if picked, cmd, ok := m.openChatPick(); ok {
			return picked, cmd
		}

	case name == "/ui":
		// /ui mouse flips the terminal's own reporting. Since S-155 that is
		// a field on the View rather than a command back to the program, so
		// this setting takes the same path as every other /ui setting: change
		// the model, say so in the transcript.
		return m.systemNotice(m.uiCommand(parts))

	case text == "/branches":
		// Bare /branches opens the branch picker (S-080); a session with no
		// branch family falls through.
		if picked, cmd, ok := m.openBranchPick(); ok {
			return picked, cmd
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
// named, it jumps straight into that agent's session (S-087).
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
