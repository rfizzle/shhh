package chat

// ctrl+c is the one quit, and it escalates one step per press
// (docs/interface/surfaces.md#the-input-frame): while a turn works the first
// press stops the run; idle with a draft it clears the draft; idle with an
// empty draft the first press arms a short window and a second press of the
// same chord inside it quits. Over a card or a picker the first press is the
// surface's own cancel (back, deny) and it opens that window too: cancel
// first, then quit. Stopping a run is reversible work already kept
// and autosaved, so it takes one press; leaving the session is the act that
// takes two, and a stop never becomes a quit by itself — a press that stops
// the run opens no window, so the next one is a new first press.
//
// The window expires silently: a press that was a reflex costs nothing, which
// is the same judgement the esc invariant makes about exploration
// (docs/interface/principles.md#esc-is-always-the-safe-answer). It is keyed
// by the chord that opened it, so no other chord completes a quit.
//
// ctrl+d quits nowhere: it is end of input in a shell and a tmux chord, and
// is reserved (docs/interface/reserved-keys.md). Typing /quit over a live
// turn is a real question, the inline confirm
// (docs/interface/surfaces.md#the-inline-confirm), because the second press
// there would destroy work the reader may not have noticed running.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// armKind is what an open window would do on its second press.
type armKind int

const (
	armNone armKind = iota
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
	return a.kind == kind && clock().Before(a.deadline)
}

// openOn is open for the chord that armed it: a window completes only on a
// press of the key that opened it.
func (a armedPress) openOn(kind armKind, key string) bool {
	return a.open(kind) && a.key == key
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
	m.armed = armedPress{kind: kind, key: key, deadline: clock().Add(window), seq: seq}
	return tea.Tick(window, func(time.Time) tea.Msg { return armExpiredMsg{seq: seq} })
}

// disarm shuts the window, keeping the sequence so a pending expiry for the
// window just shut stays recognisable as stale.
func (m *Model) disarm() { m.armed = armedPress{kind: armNone, seq: m.armed.seq} }

// armedHint is the offer the rails print while a window is open. The quit is
// stated only while nothing works, so a window the turn outran (a turn began
// between presses, and the next press stops it) says nothing rather than
// promising a quit the press will not carry out.
//
// The key is named, and named in the brackets every other offer on the rail
// wears (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) armedHint() (hintSeg, bool) {
	if m.armed.open(armQuit) && !m.working() {
		return hintSeg{key: m.armed.key, label: "again quits"}, true
	}
	return hintSeg{}, false
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

// quitNow carries the quit out: every live cancellation, then the drain that
// makes the command cancellations stick, then the autosaving quit. The
// cancels are all nil-safe, so the idle path shares it.
func (m *Model) quitNow() tea.Cmd {
	m.quitting = true
	m.cancelSubagents()
	if m.wiring.AbandonFetchWaits != nil {
		m.wiring.AbandonFetchWaits()
	}
	m.abandonMCPCalls()
	if m.cancel != nil {
		m.cancel()
	}
	// A suite is a subprocess and minutes of one; a session that is leaving
	// must not go on spending a build on the way out (gate.go).
	m.cancelCloseGate()
	m.stopSideJobs()
	// Cancelling a command only asks it to stop; the kill that would follow
	// is a timer inside this process, and quitting takes the process with it.
	// So the stop is finished here and now rather than left to a timer that
	// will never run — otherwise a command which ignores the interrupt
	// outlives the session that started it and goes on holding the port or
	// the lock the next attempt needs, which is the orphan the group
	// mechanism exists to stop, reappearing on the one path where nobody is
	// left to notice. The drain is bounded, so a quit with something to stop
	// is still a quit
	// (docs/capabilities/containment.md#a-cancelled-command-takes-its-children-with-it).
	runner.StopCaptured()
	return m.quitCmd()
}

// quitChord is the one quit, as the register spells it.
func quitChord() string { return keys.Shown(keys.Draft.Cancel) }

// quitPress is the quit's two presses: the first arms the window, a second
// press of the same chord inside it carries the quit out. prior is the window
// as it stood before this key, which updateKey consumed on the way in.
func (m Model) quitPress(prior armedPress) (tea.Model, tea.Cmd) {
	if m.editorUnsaved() {
		return m, nil
	}
	if prior.openOn(armQuit, quitChord()) {
		// A session leaving work uncommitted and nothing written about it
		// asks once, in a sentence; a third press is that question's no and
		// quits (handoff.go).
		if m.handoffOwed() {
			return m.openHandoffOffer()
		}
		cmd := m.quitNow()
		return m, cmd
	}
	cmd := m.armPress(armQuit, quitChord())
	return m, cmd
}

// editorUnsaved reports a typed buffer in the editor pane that is not on
// disk. The chord that escalates to a quit never reaches it: over such a
// buffer the first press is the pane's own cancel, which asks, and a press
// that asks opens no window, because a quit needs the buffer saved or
// discarded on purpose first (docs/interface/surfaces.md#the-editor-pane).
// It reads the held screen rather than the state, so a key list or a card
// drawn over the pane does not hide the buffer.
func (m Model) editorUnsaved() bool {
	s := m.screens.editPane()
	return s != nil && s.pane.Modified()
}

// surfaceKey routes a key to the surface holding the keyboard, answering the
// quit chord around it. The chord escalates here as it does everywhere:
// cancel first, then quit. The first press does what the surface has always
// let it do — back out of a picker, a preview or the key list, deny on an
// approval card — and opens the quit window; a second press of the same
// chord inside the window leaves the session. A single press never quits
// over a card. Over a modified editor buffer the cancel is the surface's and
// nothing more: no window opens, so no run of presses quits. It is answered once here rather than at the top of each
// surface's own key handler, where the copies drifted into cancelling
// different halves of what was still running. Two branches of the ladder are
// not routed through here: the quit confirm, because that surface is the
// question the chord asks, and the context screen, which has never answered
// it.
func (m Model) surfaceKey(msg tea.KeyPressMsg, to func(tea.KeyPressMsg) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	if !keys.Match(msg, keys.Draft.Cancel) {
		return to(msg)
	}
	if m.editorUnsaved() {
		return to(msg)
	}
	if m.pressed.openOn(armQuit, quitChord()) {
		// Over a card the chord asks the same once as it does idle; the card
		// that is already the handoff is its answer (handoff.go).
		if m.state != stateHandoff && m.handoffOwed() {
			return m.openHandoffOffer()
		}
		cmd := m.quitNow()
		return m, cmd
	}
	next, cmd := to(msg)
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	arm := nm.armPress(armQuit, quitChord())
	return nm, tea.Batch(cmd, arm)
}

// openQuitConfirm asks before quitting over a live turn: what the quit
// cancels and what the autosave keeps, default No
// (docs/interface/surfaces.md#the-inline-confirm). It is a surface, so the
// turn keeps running underneath while the question is up.
func (m Model) openQuitConfirm() (tea.Model, tea.Cmd) {
	return m.openEndConfirm("Quit? ", (*Model).quitNow)
}

// openNewSessionConfirm is the same question for the session boundary, which
// is quitting without the exit: the turn is cancelled and the conversation
// autosaved either way, and the only difference is whether the terminal comes
// back. Two openers over one surface rather than two surfaces, because a
// second confirm would be this one drawn twice and free to disagree with it
// about what a yes costs.
func (m Model) openNewSessionConfirm() (tea.Model, tea.Cmd) {
	return m.openEndConfirm("Start a new session? ", (*Model).newSessionNow)
}

// openEndConfirm puts the question up: what ending the turn here costs, what
// the autosave keeps, default No, and what a yes carries out.
func (m Model) openEndConfirm(prompt string, yes func(*Model) tea.Cmd) (tea.Model, tea.Cmd) {
	lost := "The running turn is cancelled"
	if active, _ := m.activeAgents(); active > 0 {
		lost = fmt.Sprintf("The running turn and %s are cancelled", plural(active, "agent"))
	}
	kept := "nothing is saved"
	// The saved/not-saved split is autosaveCmd's condition, so the confirm
	// cannot promise a save the act will not take.
	if m.wiring.DB != nil && len(m.agent.Messages()) > 1 {
		kept = "the conversation is autosaved to " + m.sessionName
	}
	m.quitAsk = &components.Confirm{Prompt: prompt + lost + "; " + kept + ".", KeyList: true}
	m.quitAskYes = yes
	m.enterSurface(stateQuitConfirm)
	m.syncViewport()
	return m, nil
}

// newSessionNow crosses the boundary — the confirm's act. The turn is
// cancelled the way the cancel chord cancels one, so what streamed so far is
// in the conversation the autosave writes: ending a session never discards
// work that was already done.
func (m *Model) newSessionNow() tea.Cmd {
	m.cancelStreaming()
	if m.runCancel != nil {
		m.runCancel()
	}
	notes, save := m.startNewSession()
	m.appendEntries(notes)
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return save
}

// updateQuitConfirm routes keys while the quit confirm is up. Declining
// changes nothing: the turn underneath never stopped.
func (m Model) updateQuitConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.quitAsk == nil {
		m.leaveSurface()
		return m, nil
	}
	done, yes := m.quitAsk.Update(msg)
	if !done {
		return m, nil
	}
	act := m.quitAskYes
	offered := m.handoff.offered
	m.quitAsk, m.quitAskYes, m.handoff.offered = nil, nil, false
	m.leaveSurface()
	m.syncViewport()
	if yes && act != nil {
		return m, act(&m)
	}
	// The offer of a handoff is asked on the way out, so declining it is
	// the quit that was asked for (handoff.go).
	if offered {
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
