package chat

// The settings surface inside a session: `/config`
// (docs/interface/surfaces.md#the-supporting-screens).
//
// `shhh config` has drawn every setting, where its value came from and what
// changing one costs since before this session existed, and the one thing it
// could not do was open while a session was running. So the four settings a
// person most wants to change mid-session — how often the harness checks in,
// what its steering says, which model summarises, which backlog profile the
// project is read under — were reachable only by quitting the session that
// made them want to change.
//
// This is that screen, not a second one. The component is the same
// (components.ConfigScreen) and so is every judgement about a setting: what
// it means, what its default is, which answers `[enter]` offers and when any
// of it reaches the file. All of that is the CLI's — the chat package owns no
// config semantics, the way it owns none for the writer beside it
// (defaults.go) — and it arrives here as a ConfigSession the host installs.
//
// What the session adds is where the screen draws: it is a pane overlay like
// the context reading, so the turn underneath it keeps running, and nothing
// it stages touches the file until `[ctrl+s]`.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// ConfigSession is one staged pass over the config file: the screen to draw,
// and what an answer to it means.
//
// The screen is a pointer because the host owns it — a staged edit is
// resolved to the host and the host hands the rows back, so a copy taken here
// would be showing the reader a file nothing is being written to.
type ConfigSession struct {
	// Screen is the surface, already carrying the rows the file stands at.
	Screen *components.ConfigScreen
	// Answer is called with everything the screen's Update returned: it
	// stages the edit a key made, and on the write the screen closes with it
	// puts the staged keys in the file. What it returns is the row the
	// session leaves in the transcript — empty for a screen that closed
	// having written nothing, which is most of them.
	Answer func(done bool, result components.ConfigResult) string
	// Take is handed the settings the session runs on now, in the shape it
	// was wired with, and moves onto it every value the last answer had the
	// session take: a key the session reads at a turn boundary, staged, or
	// put back to what the file holds by a discard. It reports whether it
	// moved anything. The session takes what moved at its next turn
	// boundary, which between turns is now
	// (docs/interface/surfaces.md#the-settings-screen). Nil takes nothing.
	Take func(w *Wiring) bool
}

// ConfigOpener starts one. It is called per opening rather than once per
// session because the screen states where every value came from, and a copy
// loaded when the session started would state it about a file that has been
// edited since — by `shhh config set` in another terminal, or by the reader.
//
// models is what the session's own model picker offers now — the catalog,
// or the endpoint's list once `/model` has asked for it — so the picker a
// flow's row opens is that one list rather than a second idea of which
// models there are.
type ConfigOpener func(models []string) (ConfigSession, error)

// openConfigScreen puts the surface up.
func (m Model) openConfigScreen() (tea.Model, tea.Cmd) {
	if m.wiring.ConfigScreen == nil {
		return m.systemNotice("this session cannot reach the config file. `shhh config` opens the same screen")
	}
	session, err := m.wiring.ConfigScreen(m.modelPickChoices())
	if err != nil {
		return m.systemNotice(failed("config", "could not read the config: "+err.Error()))
	}
	m.screens = m.screens.with(stateConfig, &session)
	m.enterSurface(stateConfig)
	return m, nil
}

// answerConfig routes one key while the surface is up. Every key goes to the
// host, finished or not: a change is staged as it is made, and the screen
// redraws from what the host made of it rather than from what it thinks it
// changed.
func (m *Model) answerConfig(msg tea.KeyPressMsg) (bool, overlayAction) {
	screen := m.screens.config()
	if screen == nil || screen.Screen == nil || screen.Answer == nil {
		return true, m.closeConfigScreen("")
	}
	done, result := screen.Screen.Update(msg)
	note := screen.Answer(done, result)
	m.takeStaged(screen.Take)
	if !done {
		// A write leaves its receipt in the transcript and the screen up,
		// carrying the same line on its foot row.
		return false, overlayAction{note: note}
	}
	return true, m.closeConfigScreen(note)
}

// closeConfigScreen hands the screen back to the turn, which may have moved
// on while the surface was up, and leaves what the answer had to say.
func (m *Model) closeConfigScreen(note string) overlayAction {
	m.screens = m.screens.without(stateConfig)
	return overlayAction{close: true, note: note}
}

// configScreenLines renders the surface into the pane it was given.
func (m Model) configScreenLines(width, height int) []string {
	screen := m.screens.config()
	if screen == nil || screen.Screen == nil {
		return nil
	}
	screen.Screen.SetSize(width, height)
	return strings.Split(screen.Screen.View(width), "\n")
}

// renderConfigHint is the one line the screen leaves where the draft box was.
// The screen holds the keyboard and states its own keys at its foot, so the
// panel says which surface has them and nothing else, the way the sources
// screen's does.
func (m Model) renderConfigHint() string {
	hint := segAs(keys.Screen.Quit, "back to the prompt")
	return sty.SystemMsg.Render("config · ") + hint.render()
}

// heldTake is what the settings screen had the session take while a turn
// ran: the settings as they stood when it was asked, and as the screen left
// them. Only what differs between the two is taken at the boundary, so a mode
// cycled or a model switched since is not put back.
type heldTake struct{ from, to Wiring }

// takeStaged asks the screen's host what the last answer had the session
// take, over what the session is on now, and takes it: at once between
// turns, and where the next turn opens while one is running, so a model or a
// round limit never moves under the turn that is using it. A second answer
// while the turn runs is asked over the first's, so neither is lost.
func (m *Model) takeStaged(take func(*Wiring) bool) {
	if take == nil {
		return
	}
	now := m.settingsNow()
	held := heldTake{from: now, to: now}
	if m.taken != nil {
		held = *m.taken
	}
	if !take(&held.to) {
		return
	}
	if m.turnOpen {
		m.taken = &held
		return
	}
	m.takeSettings(held.from, held.to)
}

// takeHeld is the turn boundary taking what the screen staged while the
// last turn ran.
func (m *Model) takeHeld() {
	if m.taken == nil {
		return
	}
	held := *m.taken
	m.taken = nil
	m.takeSettings(held.from, held.to)
}

// settingsNow is the settings the session runs on now, in the wiring's shape:
// what a command moved since the session opened, and what it was wired with
// for the rest.
func (m Model) settingsNow() Wiring {
	w := m.wiring
	w.Mode, w.ModelName, w.Effort = m.policy.mode, m.modelName, m.effort
	w.Suggestions, w.Verbosity = m.suggest.on, m.verbosity.String()
	w.MouseOff, w.NotifyOff, w.WindowTitleOff = !m.pointer.mouseOn, !m.notifyOn, !m.windowTitleOn
	w.PasteLines, w.PasteColumns, w.RailWidth = m.pasteLines, m.pasteColumns, m.railCols
	return w
}

// takeSettings moves the session onto to wherever it differs from from — what
// the screen moved, and nothing else — through the same paths the commands
// take: the mode the way /permissions sets it, the model the way /model
// switches it, the level the way ctrl+t moves it, so a value taken from the
// settings screen draws on the status line and turns the mode cycle exactly
// as theirs does. Nothing here writes the file: the screen's own write is the
// only one.
func (m *Model) takeSettings(from, to Wiring) {
	if to.Mode != from.Mode && !m.wiring.Conversation {
		m.applyMode(to.Mode)
	}
	if to.ModelName != from.ModelName && to.ModelName != "" && m.wiring.SwitchModel != nil {
		m.wiring.SwitchModel(to.ModelName)
		m.modelName = to.ModelName
	}
	if to.Effort != from.Effort {
		m.applyEffort(to.Effort)
	}
	// The loop's own settings go through the applier every surface's loop is
	// set with, and are kept as what the session was wired with, which is
	// what a turn's ceiling is read back from.
	if to.MaxToolRounds != from.MaxToolRounds || to.Steering != from.Steering ||
		to.ProgressCalls != from.ProgressCalls || to.ProgressElapsed != from.ProgressElapsed {
		m.wiring.MaxToolRounds, m.wiring.Steering = to.MaxToolRounds, to.Steering
		m.wiring.ProgressCalls, m.wiring.ProgressElapsed = to.ProgressCalls, to.ProgressElapsed
		ApplySettings(m.agent, m.wiring)
	}
	if to.Suggestions != from.Suggestions {
		m.suggest.on = to.Suggestions
	}
	if to.Verbosity != from.Verbosity {
		m.verbosity = verbosityNormal
		if v, err := parseVerbosity(to.Verbosity); err == nil {
			m.verbosity = v
		}
	}
	if to.MouseOff != from.MouseOff {
		m.pointer.mouseOn = !to.MouseOff
		// Reporting off hands the selection back to the terminal, as /ui
		// mouse does.
		if to.MouseOff {
			m.cancelSelection()
		}
	}
	if to.NotifyOff != from.NotifyOff {
		m.notifyOn = !to.NotifyOff
	}
	if to.WindowTitleOff != from.WindowTitleOff {
		m.windowTitleOn = !to.WindowTitleOff
	}
	if to.PasteLines != from.PasteLines {
		m.pasteLines = attachment.DefaultPasteLines
		if to.PasteLines != 0 {
			m.pasteLines = to.PasteLines
		}
	}
	if to.PasteColumns != from.PasteColumns {
		m.pasteColumns = attachment.DefaultPasteColumns
		if to.PasteColumns != 0 {
			m.pasteColumns = to.PasteColumns
		}
	}
	if to.RailWidth != from.RailWidth {
		m.railCols = to.RailWidth
	}
	// A theme, a density or a rail that moved is a transcript drawn again,
	// and a rail that moved is a viewport resized, as /ui does for each.
	m.invalidateRenderCache()
	m.syncViewport()
}
