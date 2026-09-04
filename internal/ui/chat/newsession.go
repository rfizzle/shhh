package chat

// Crossing the session boundary, and loading a conversation into one.
//
// Everything quitting and launching again does, without the exit: the
// conversation left whole in its own slot, the record of it closed and
// another opened, and every reading a launched session takes taken again.
// See docs/capabilities/sessions-and-memory.md#a-new-conversation-is-a-new-session.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// startNewSession crosses the session boundary: everything quitting and
// launching again does, without the exit. The conversation is left whole in
// its own slot, the record of it is closed and another opened, and every
// reading a launched session takes is taken again — so a new conversation is
// a new session wherever the old one was counted, rather than the same
// session wearing an empty transcript.
// See docs/capabilities/sessions-and-memory.md#a-new-conversation-is-a-new-session.
//
// It answers with the row the new session opens on and the save of the one
// left behind, which the caller runs: the boundary is a keystroke away from
// the work of a whole sitting, and a row naming the slot is what makes that
// recoverable rather than merely reversible.
func (m *Model) startNewSession() (note string, save tea.Cmd) {
	// The autosave is built first and in quitting's own sequence: it reads
	// the conversation as it stands and names the slot to the record that is
	// about to be closed, so the row left behind describes the conversation
	// it is the record of.
	save = m.autosaveCmd()
	// The slot only counts as left behind if something is going into it: the
	// row must not name a slot this conversation was never written to.
	left := ""
	if save != nil {
		left = m.sessionName
	}

	// Live work belongs to the session that started it. A child left running
	// would spend against a closed record and report into a transcript that
	// no longer exists, which is why quitting kills them and why this does
	// too (cancel.go).
	m.cancelSubagents()
	if m.classifierCancel != nil {
		m.classifierCancel()
	}
	m.dropTodoExtract()
	m.dropTodoDraft()
	m.dropPersona()
	// A run in progress keeps its checkpoint and the new session is told how
	// to pick it up. The checkpoint was written to survive exactly this: the
	// stages already done are in the tree, and putting the item back to open
	// would throw that work away for the price of one sentence.
	kept := ""
	if m.todoRunner.state != nil && !m.todoRunner.state.Over() {
		kept = m.keepTodoRun("this session ended")
	}
	if m.attachedTo != "" {
		// The keyboard was in a child that has just been killed.
		m.attach("")
	}
	// The mirrored child transcripts go with them. Emptied rather than
	// dropped: the map is made when the supervisor is wired and written to
	// whenever a child reports, so a nil one here would panic on the first
	// child of the new session.
	clear(m.childViews)

	// The host's half: the record ends here and another begins, and the
	// system prompt is built again from the checkout as it stands rather than
	// as it stood when the process started.
	var start SessionStart
	if m.newSession != nil {
		start = m.newSession()
	}
	m.setSystemPrompt(start.Prompt)
	if start.Prompt != "" {
		m.projectTokens = start.ProjectTokens
	}
	m.resetTranscript()
	m.checkpoints = nil
	// Follow-ups were written against the conversation being dropped.
	m.followUps = nil
	m.followUpsHeld = false
	m.contextTokens = 0
	m.vitals.reset()
	// The session's spend starts over with its accounting, or the rail would
	// keep quoting a bill for a conversation that no longer exists.
	m.ledger.Reset()
	m.TotalTokensIn, m.TotalTokensOut = 0, 0
	// And the counters are put back to their zero value rather than aimed at
	// it: a climb is measured movement, and there is nothing here for a
	// figure to travel across (turnstatus.go).
	m.turnUp, m.turnDown = components.Odometer{}, components.Odometer{}
	m.sessionUp, m.sessionDown = components.Odometer{}, components.Odometer{}
	m.resetRounds()
	// The turn's accounting started over, so there is no longer a turn to
	// close with a summary either.
	m.turnOpen = false
	// Turns are numbered from one again, which is what makes the record's
	// turn column mean the same thing in both rows.
	m.turnCount = 0
	// A backlog run's bookkeeping is counted in those turns and indexed into
	// that transcript, and a cancel mark left standing would end the first
	// stage turn of the next run before it was read.
	m.todoRunner.turn, m.todoRunner.mark, m.todoRunner.cancelled = 0, 0, false
	m.resetSummary()
	// The handoff belongs to the conversation that was compacted, and this is
	// a different one: carried across, the new slot would open on a summary of
	// work its transcript never mentions (reopen.go).
	m.compactSummary = ""
	// The edits belong to the conversation that made them: with the turns
	// renumbered, a review or an undo would otherwise reach into the
	// conversation before this one.
	m.changes.Reset()
	// What the model has been shown is nothing, because this model has been
	// shown nothing (tools/seen.go).
	tools.ForgetAll()
	// The tree reading compares against a baseline, and the changeset that
	// subtracts this session's own edits from it has just been emptied — so
	// the baseline is taken again here, or the first reading of the new
	// session would report the last one's work as a stranger's.
	m.agent.RestartTreeCheck()
	// A new conversation is a new session with a slot of its own; the one
	// just left stays in the store as it was, for --resume to find — and is
	// only given back when this session is leaving it empty.
	if left == "" {
		m.mintSlot()
	} else {
		m.mintSlotKeeping()
	}
	m.resetTitle()
	// The new row is linked to the new slot now rather than at the first
	// save, so the two halves of the boundary — a record closed and a
	// conversation started — are one act in the store as well.
	if m.observer.Session != nil {
		m.observer.Session(m.sessionName)
	}

	return newSessionRow(left, start.Resume, kept), save
}

// newSessionRow is the row the new session opens on. It says where the exit
// banner would have said it: the slot the conversation was left in and the
// command that reopens it, because nothing is leaving the alt screen here and
// a conversation nobody can name is one nobody gets back to. An empty slot is
// a session that was not written down — there was nothing to save, or nowhere
// to save it — and the row promises nothing rather than naming a slot that
// holds something else. kept is the backlog run the boundary let go of, on a
// line of its own: it is an offer to act, not part of the account.
func newSessionRow(slot, resume, kept string) string {
	note := "Started a new session."
	switch {
	case slot != "" && resume != "":
		note += fmt.Sprintf(" The conversation so far is saved as %q; `%s` reopens it.", slot, resume)
	case slot != "":
		note += fmt.Sprintf(" The conversation so far is saved as %q.", slot)
	}
	if kept != "" {
		note += "\n\n" + kept
	}
	return note
}

// setSystemPrompt puts the conversation back to one message: the prompt the
// host built for this session, or the one the conversation already carried
// where the host had none to give. A session that never had one starts with
// no messages at all, which is what a front-end wired without a prompt has
// always had.
func (m *Model) setSystemPrompt(text string) {
	if text == "" {
		if msgs := m.agent.Messages(); len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
			text = msgs[0].Content
		}
	}
	if text == "" {
		m.agent.SetMessages(nil)
		return
	}
	m.agent.SetMessages([]provider.Message{{Role: provider.RoleSystem, Content: text}})
}

// loadConversation replaces the current conversation and rebuilds the
// transcript from the stored messages.
func (m *Model) loadConversation(msgs []provider.Message) {
	// A loaded conversation is a session with a past; the start screen does
	// not come back after it is cleared.
	m.spendStartScreen()
	m.agent.SetMessages(msgs)
	m.resetTranscript()
	// Follow-ups were written against the conversation being replaced, so
	// none of them may fire into this one.
	m.followUps = nil
	m.followUpsHeld = false
	m.checkpoints = checkpointsFromMessages(msgs)
	m.appendMessageEntries(msgs)
	// The prompts that conversation was made of are what ↑ recalls in it
	// (recall.go). They are seeded here rather than by each of the four
	// callers, for the same reason the transcript is: every path back to a
	// stored conversation passes through this one function.
	m.recallFromMessages(msgs)
}

// appendMessageEntries renders a run of messages into the transcript: the
// user turns, the assistant text, and one tool entry per call paired with the
// result that followed it. It is shared by the session load and by the tail a
// compaction carries through, so a conversation put back on screen
// looks the same however it got there.
func (m *Model) appendMessageEntries(msgs []provider.Message) {
	for i, msg := range msgs {
		switch msg.Role {
		case provider.RoleUser:
			// A resumed turn keeps the names of what it attached:
			// the bytes were saved with it, so the row that said "attached:
			// shot.png" says it again.
			m.appendEntry(entry{kind: entryUser, text: msg.Content,
				attached: attachment.Names(msg.Attachments)})
		case provider.RoleAssistant:
			// The thinking that led to the turn comes back with it, above it,
			// where it happened (think.go). A conversation that is still
			// replaying its reasoning to the model and no longer showing it
			// would be the transcript quietly disagreeing with the request.
			if think := reasoningText(msg.Reasoning); think != "" {
				m.appendEntry(entry{kind: entryThink, text: think})
			}
			if msg.Content != "" {
				m.appendEntry(entry{kind: entryAssistant, text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				var result string
				if i+1 < len(msgs) {
					for _, next := range msgs[i+1:] {
						if next.Role == provider.RoleTool && next.ToolCallID == tc.ID {
							result = next.Content
							break
						}
						if next.Role != provider.RoleTool {
							break
						}
					}
				}
				m.appendEntry(entry{kind: entryTool, toolName: tc.Name, toolArgs: tc.Arguments, toolResult: result})
			}
		}
	}
}
