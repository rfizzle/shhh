package chat

// The session's standing account
// (docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write).
//
// A title says what a conversation is about; it does not say how far it got.
// So every few turns a cheap model revises two sentences — what the session
// was doing, and where it left off — and the slot keeps them in the column a
// compaction's handoff is kept in. Every listing of saved chats draws them
// under the title, and a conversation opened again is told them first
// (reopen.go). The rules:
//
//   - It is asked at a turn's close, no more often than every
//     summary.resume_interval_turns turns, as a background command like the
//     title's: nothing waits for it and a failed reading changes nothing.
//   - It is asked once more where the session is left — at the session
//     boundary and at quit — when a turn has closed since the last one, and
//     that reading rides the save that leaves the slot behind.
//   - Each reading revises the one before it, and whichever of an account
//     and a compaction's handoff was written last is the one the slot keeps:
//     a reading that lands after a compaction it did not see is dropped.

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/storage"
)

// accountState is what the session knows about its standing account. The
// account itself is compactSummary, which is what every save puts on the
// slot, so the two writers of that column share one value and the later
// write is the one that stands.
type accountState struct {
	writer *agent.Accountant
	// every is how many closed turns pass between two readings; zero is
	// off.
	every int
	// turns counts the turns closed since the last reading was asked for.
	turns int
	// inFlight marks a reading still out, readFor the slot it was asked
	// for, and cancel what stops it.
	inFlight bool
	readFor  string
	cancel   context.CancelFunc
}

// accountDoneMsg carries a finished reading back, with the slot it was read
// for and the account it revised: either may have moved on by the time it
// lands.
type accountDoneMsg struct {
	name, basis string
	verdict     agent.AccountVerdict
}

// WithAccountant wires the writer of the standing account and how many turns
// pass between two readings. A nil writer, or every at zero, asks nothing.
func (m Model) WithAccountant(a *agent.Accountant, every int) Model {
	m.account.writer = a
	m.account.every = every
	return m
}

// accountEnabled reports whether readings are taken at all.
func (m Model) accountEnabled() bool {
	return m.account.every > 0 && m.account.writer.Enabled() && m.db != nil
}

// noteAccountTurn counts a closed turn toward the next reading. It is called
// from the one place a turn's accounting is closed (appendTurnClose).
func (m *Model) noteAccountTurn() {
	m.account.turns++
}

// accountRequest is the evidence a reading is taken from: the standing
// account, and the conversation as the slot keeps it.
func (m Model) accountRequest() agent.AccountRequest {
	return agent.AccountRequestFrom(m.compactSummary, agent.StripResumeContext(m.agent.Messages()))
}

// accountCloseCmd asks for a reading once enough turns have closed since the
// last one. It rides the Update tail beside the title's, which is where a
// command can leave from.
func (m *Model) accountCloseCmd() tea.Cmd {
	if !m.accountEnabled() || m.account.inFlight || m.account.turns < m.account.every {
		return nil
	}
	req := m.accountRequest()
	if req.Empty() {
		return nil
	}
	m.account.turns = 0
	m.account.inFlight = true
	m.account.readFor = m.sessionName
	writer, name, basis := m.account.writer, m.sessionName, m.compactSummary
	ctx, cancel := context.WithCancel(context.Background())
	m.account.cancel = cancel
	return func() tea.Msg {
		defer cancel()
		return accountDoneMsg{name: name, basis: basis, verdict: writer.Account(ctx, req)}
	}
}

// finishAccount applies a reading: kept on the model, so every save after it
// carries it, and written to the slot now, so a session that never saves
// again still leaves it there.
func (m *Model) finishAccount(msg accountDoneMsg) tea.Cmd {
	if msg.name == m.account.readFor {
		m.account.inFlight = false
		m.account.cancel = nil
	}
	if msg.verdict.Failed || msg.verdict.Account == "" {
		return nil
	}
	// A reading for a slot the session has left is about another
	// conversation, and one that revised an account something has since
	// replaced — a compaction's handoff, a conversation loaded over this one
	// — would overwrite the newer writing with an older view.
	if msg.name != m.sessionName || msg.basis != m.compactSummary {
		return nil
	}
	m.compactSummary = msg.verdict.Account
	if m.db == nil {
		return nil
	}
	db, slot, dir := m.db, m.sessionName, m.workspace
	r := storage.ChatResume{Summary: m.compactSummary, Steps: m.workSteps.Encode()}
	return func() tea.Msg {
		// The resume columns are one write (storage.SetChatResume), so the
		// commit is read here beside the account, the way the autosave
		// reads it.
		r.Head, r.Root = project.Head(dir), project.Root(dir)
		_ = db.SetChatResume(slot, r)
		return nil
	}
}

// closingAccount is the reading a session owes the slot it is leaving — at
// the boundary and at quit — where a turn has closed since the last reading
// or a reading is still out and will not land. It answers with the work, for
// the save that leaves the slot behind to run before it writes the resume
// columns (saveCmd): the reading has to be in the save that carries it, and
// nothing after the save is left to write it. Nil is nothing owed.
func (m Model) closingAccount() func() string {
	if !m.accountEnabled() || (m.account.turns == 0 && !m.account.inFlight) {
		return nil
	}
	req := m.accountRequest()
	if req.Empty() {
		return nil
	}
	writer, basis := m.account.writer, m.compactSummary
	return func() string {
		v := writer.Account(context.Background(), req)
		if v.Failed || v.Account == "" {
			return basis
		}
		return v.Account
	}
}

// resetAccount forgets the count and any reading still out: a new
// conversation has had no turns, and a reading about the old one is not
// about it.
func (m *Model) resetAccount() {
	if m.account.cancel != nil {
		m.account.cancel()
	}
	m.account = accountState{writer: m.account.writer, every: m.account.every}
}
