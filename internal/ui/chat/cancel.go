package chat

// Abandoning work takes a second press (
// docs/interface/surfaces.md#the-input-frame).
//
// A turn in flight is minutes of work, and the keys that end it — the
// interrupt, the quit chord — are exactly the keys a reflex produces. So the
// first press of one opens a short window and says so on the rails, and only
// a second press inside the window carries the act out. The window expires
// silently: a press that was a reflex costs nothing, which is the same
// judgement the esc invariant makes about exploration
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
//
// There is one window, not one per key, and the interrupt's is fed by the
// cancel chord alone. Esc used to arm it too, which put the one key that
// leaves every surface in the product — a diff, a menu, a selection — in
// charge of abandoning a turn whenever the draft happened to be empty. So
// the key that arms is always the same one now, and the notice always names
// it.
//
// Quitting arms the same machine under its own kind — and quitting over a
// live turn is not a window at all but a real question, the inline confirm
// (docs/interface/surfaces.md#the-inline-confirm), because there the second
// press would destroy the very work the reader may not have noticed running.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// armKind is what an open window would do on its second press.
type armKind int

const (
	armNone armKind = iota
	// armCancel: the next interrupt press cancels the streaming turn.
	armCancel
	// armQuit: the next quit press ends the session.
	armQuit
	// armRewind: the next esc on the empty idle draft opens the rewind
	// picker.
	armRewind
)

// pressAgain is how long the first press waits for its second. A constant
// rather than a setting: it is a reflex filter, not a preference.
const pressAgain = 2 * time.Second

// rewindPressWindow is the double-esc gesture's own, much shorter, window:
// the two presses are one gesture rather than a press and a decision, and a
// second esc arriving late should cost nothing rather than open a surface
// the reader had stopped asking for.
const rewindPressWindow = 500 * time.Millisecond

// armedPress is the open window: what the second press would do, the spelling
// of the key that armed it (the hint prints it back), when the window shuts,
// and a sequence number so an expiry scheduled for an old window cannot shut
// a new one.
type armedPress struct {
	kind     armKind
	key      string
	deadline time.Time
	seq      int
}

// open reports whether the window is armed for kind right now.
func (a armedPress) open(kind armKind) bool {
	return a.kind == kind && time.Now().Before(a.deadline)
}

// armExpiredMsg is the window shutting on its own. The handler repaints, so
// the hint reverts without waiting for the next keystroke.
type armExpiredMsg struct{ seq int }

// armPress opens the window and schedules its silent expiry.
func (m *Model) armPress(kind armKind, key string) tea.Cmd {
	return m.armPressFor(kind, key, pressAgain)
}

// armPressFor is armPress with the window named, for the one kind whose
// window is a gesture's rather than a reflex filter's.
func (m *Model) armPressFor(kind armKind, key string, window time.Duration) tea.Cmd {
	seq := m.armed.seq + 1
	m.armed = armedPress{kind: kind, key: key, deadline: time.Now().Add(window), seq: seq}
	return tea.Tick(window, func(time.Time) tea.Msg { return armExpiredMsg{seq: seq} })
}

// disarm shuts the window, keeping the sequence so a pending expiry for the
// window just shut stays recognisable as stale.
func (m *Model) disarm() { m.armed = armedPress{kind: armNone, seq: m.armed.seq} }

// armedNotice is the phrase the rails print while a window is open, or empty.
// Each kind is stated only in the state its second press would act in, so a
// window the turn outran (the stream ended between presses) says nothing
// rather than promising a cancel with nothing to cancel.
func (m Model) armedNotice() string {
	switch {
	case m.armed.open(armCancel) && (m.turnState() == stateStreaming || m.heldAtBoundary()):
		return m.armed.key + " again cancels the turn"
	case m.armed.open(armQuit) && !m.working():
		return "press again to quit"
	}
	return ""
}

// cancelTurnNow abandons the streaming turn — the second press's act. What
// streamed so far is kept and autosaved; interrupting never discards work
// already done.
func (m Model) cancelTurnNow() (tea.Model, tea.Cmd) {
	m.cancelStreaming()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.autosaveCmd()
}

// quitNow carries the quit out: every live cancellation, then the autosaving
// quit. The cancels are all nil-safe, so the idle path shares it.
func (m *Model) quitNow() tea.Cmd {
	m.quitting = true
	m.cancelSubagents()
	if m.cancel != nil {
		m.cancel()
	}
	if m.runCancel != nil {
		m.runCancel()
	}
	if m.classifierCancel != nil {
		m.classifierCancel()
	}
	return m.quitCmd()
}

// openQuitConfirm asks before quitting over a live turn: what the quit
// cancels and what the autosave keeps, default No
// (docs/interface/surfaces.md#the-inline-confirm). It is a surface, so the
// turn keeps running underneath while the question is up.
func (m Model) openQuitConfirm() (tea.Model, tea.Cmd) {
	lost := "The running turn is cancelled"
	if active, _ := m.activeAgents(); active > 0 {
		lost = fmt.Sprintf("The running turn and %s are cancelled", plural(active, "agent"))
	}
	kept := "nothing is saved"
	// The saved/not-saved split is autosaveCmd's condition, so the confirm
	// cannot promise a save the quit will not take.
	if m.db != nil && len(m.agent.Messages()) > 1 {
		kept = "the conversation is autosaved to " + m.sessionName
	}
	m.quitAsk = &components.Confirm{Prompt: "Quit? " + lost + "; " + kept + "."}
	m.enterSurface(stateQuitConfirm)
	m.syncViewport()
	return m, nil
}

// updateQuitConfirm routes keys while the quit confirm is up. Declining
// changes nothing: the turn underneath never stopped.
func (m Model) updateQuitConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.quitAsk == nil {
		m.leaveSurface()
		return m, nil
	}
	done, result := m.quitAsk.Update(msg)
	if !done {
		return m, nil
	}
	m.quitAsk = nil
	m.leaveSurface()
	m.syncViewport()
	if yes, _ := result.(bool); yes {
		return m, m.quitNow()
	}
	return m, nil
}

// quitConfirmLines renders the confirm, one row per line.
func (m Model) quitConfirmLines() []string {
	if m.quitAsk == nil {
		return nil
	}
	return strings.Split(m.quitAsk.View(m.contentWidth()), "\n")
}

// renderQuitConfirm pads the confirm to the bottom panel's height.
func (m Model) renderQuitConfirm() string {
	lines := m.quitConfirmLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}
