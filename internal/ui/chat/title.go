package chat

// Session titles (docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write).
//
// A session that was never named is called by the moment it began, and a
// list of those is a list of timestamps. So after a session's first
// completed turn a cheap model reads the exchange and writes a title of a
// few words, which every listing draws beside the slot's name. The rules:
//
//   - A name the user typed always wins. A session saved as `/save name`
//     or renamed from the picker is never asked for a title, and the title
//     it may already carry is only ever shown beside that name.
//   - A failed reading leaves the row untitled and is retried once, after
//     the next turn, and never again. A provider that refuses twice is not
//     asked a third time.
//   - The reading is a background command like the summary's: nothing on
//     screen waits for it, and the turn under it is untouched either way.
//   - It is off unless a summary model is configured, because on the
//     session model the cheapest question is still not cheap; `/ui title`
//     flips it for the session and `summary.title` in the config for good.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// titleAttempts is how many readings a session gets: the first, and one
// retry after the next turn.
const titleAttempts = 2

// titleState is what the session knows about its title: the words on the
// row, whether readings are wanted, and how many have been asked for.
type titleState struct {
	// on is the session's switch (/ui title, summary.title).
	on bool
	// title is the session's generated title as the store has it — empty
	// until a reading lands, and reloaded when a saved chat is opened.
	title string
	// attempts counts readings asked for on this slot; inFlight marks one
	// still out, and readFor the slot it was asked for.
	attempts int
	inFlight bool
	readFor  string
}

// titleDoneMsg carries a finished reading back to the model, with the slot
// it was read for: the session may have moved on by the time it lands.
type titleDoneMsg struct {
	name    string
	verdict agent.TitleVerdict
}

// WithTitler wires the session titler and whether the session starts with
// titles on. A nil titler leaves every row untitled and no requests made.
func (m Model) WithTitler(t *agent.Titler, on bool) Model {
	m.titler = t
	m.titles.on = on
	return m
}

// titleEnabled reports whether readings are taken at all.
func (m Model) titleEnabled() bool {
	return m.titles.on && m.titler.Enabled() && m.db != nil
}

// isAutosaveSlot reports whether a session name is one the session was given
// rather than one the user typed: the moment it began, with the counted
// suffix the store's claim adds when that name is taken. Only such a slot is
// titled.
func isAutosaveSlot(name string) bool {
	if len(name) < len(sessionNameLayout) {
		return false
	}
	if _, err := time.Parse(sessionNameLayout, name[:len(sessionNameLayout)]); err != nil {
		return false
	}
	rest := name[len(sessionNameLayout):]
	if rest == "" {
		return true
	}
	var n int
	_, err := fmt.Sscanf(rest, " (%d)", &n)
	return err == nil && strings.HasSuffix(rest, ")")
}

// titleCloseCmd asks for a title when a turn ends on a slot that wants one,
// derived from the model before against the model after — the same way the
// summary's close reading is, and for the same reason.
func (m *Model) titleCloseCmd(prev Model) tea.Cmd {
	if !prev.working() || m.working() {
		return nil
	}
	if !m.titleEnabled() || m.titles.inFlight || m.titles.title != "" {
		return nil
	}
	if !isAutosaveSlot(m.sessionName) || m.titles.attempts >= titleAttempts {
		return nil
	}
	req := m.titleRequest()
	if req.User == "" {
		return nil
	}
	m.titles.attempts++
	m.titles.inFlight = true
	m.titles.readFor = m.sessionName
	titler, name := m.titler, m.sessionName
	ctx, cancel := context.WithCancel(context.Background())
	m.titleCancel = cancel
	return func() tea.Msg {
		defer cancel()
		return titleDoneMsg{name: name, verdict: titler.Title(ctx, req)}
	}
}

// titleRequest is the exchange the title is read from: the first thing the
// user asked and the last thing the assistant said. Tool results are not
// in it, so nothing the agent read can name the session.
func (m Model) titleRequest() agent.TitleRequest {
	var req agent.TitleRequest
	for _, msg := range m.agent.Messages() {
		if msg.Role == provider.RoleUser && strings.TrimSpace(msg.Content) != "" {
			req.User = msg.Content
			break
		}
	}
	req.Assistant = m.lastAssistantText()
	return req
}

// finishTitle applies a reading. A failed one changes nothing; a good one is
// kept on the model and written to the slot it was read for. The write is a
// command, like the autosave, and the autosave carries the title too, so a
// slot the store has not seen yet still ends up titled.
func (m *Model) finishTitle(msg titleDoneMsg) tea.Cmd {
	if msg.name != m.titles.readFor {
		// A reading for a slot the session has since left behind: /clear
		// or a load reset the state. Its words are about another
		// conversation.
		return nil
	}
	m.titles.inFlight = false
	m.titleCancel = nil
	if msg.verdict.Failed || msg.verdict.Title == "" {
		return nil
	}
	// The reading is this conversation's whether it is still in the slot
	// it was read for or /save has since moved it to a named one.
	m.titles.title = msg.verdict.Title
	if m.db == nil {
		return nil
	}
	db, title := m.db, msg.verdict.Title
	slots := []string{msg.name}
	if m.sessionName != msg.name {
		slots = append(slots, m.sessionName)
	}
	return func() tea.Msg {
		for _, name := range slots {
			_ = db.SetChatTitle(name, title)
		}
		return nil
	}
}

// resetTitle forgets the slot's title state: a new slot has no title and
// every reading to come.
func (m *Model) resetTitle() {
	if m.titleCancel != nil {
		// A reading out for the old slot is about a conversation this
		// session no longer holds.
		m.titleCancel()
		m.titleCancel = nil
	}
	m.titles = titleState{on: m.titles.on}
}

// loadTitle reads the stored title of the slot the session just moved to.
func (m *Model) loadTitle() {
	m.resetTitle()
	if m.db == nil {
		return
	}
	m.titles.title, _ = m.db.ChatTitle(m.sessionName)
}

// titleStatus is the /ui readout's word for the titler: on and with what,
// or off and why.
func (m Model) titleStatus() string {
	switch {
	case !m.titles.on && !m.titler.Enabled():
		return "off — no summary model is configured (summary.model)"
	case !m.titles.on:
		return "off"
	case !m.titler.Enabled():
		return "on, but no summary model is configured (summary.model) — nothing is asked"
	}
	return "on (" + m.titler.Model() + ")"
}

// titleCommand handles /ui title [on|off].
func (m *Model) titleCommand(parts []string) string {
	if len(parts) == 2 {
		return "Session titles: " + m.titleStatus() +
			".\nUsage: /ui title <on|off> — on, a cheap model names an unnamed session after its first turn; a name you give it always wins."
	}
	if len(parts) != 3 {
		return "Usage: /ui title <on|off>"
	}
	switch parts[2] {
	case "on", "true", "yes":
		m.titles.on = true
	case "off", "false", "no":
		m.titles.on = false
	default:
		return fmt.Sprintf("Error: unknown title setting %q (on, off)", parts[2])
	}
	return "Session titles: " + m.titleStatus() + "."
}
