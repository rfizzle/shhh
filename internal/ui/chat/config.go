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
// it stages touches the file until `[w]`.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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
}

// ConfigOpener starts one. It is called per opening rather than once per
// session because the screen states where every value came from, and a copy
// loaded when the session started would state it about a file that has been
// edited since — by `shhh config set` in another terminal, or by the reader.
type ConfigOpener func() (ConfigSession, error)

// WithConfigScreen installs what `/config` opens. A session without one still
// runs; it says the settings cannot be reached from here rather than drawing
// a screen with nothing behind it.
func (m Model) WithConfigScreen(open ConfigOpener) Model {
	m.openConfig = open
	return m
}

// openConfigScreen puts the surface up.
func (m Model) openConfigScreen() (tea.Model, tea.Cmd) {
	if m.openConfig == nil {
		return m.systemNotice("This session cannot reach the config file. `shhh config` opens the same screen.")
	}
	session, err := m.openConfig()
	if err != nil {
		return m.systemNotice("Error: could not read the config: " + err.Error())
	}
	m.configScreen = &session
	m.enterSurface(stateConfig)
	return m, nil
}

// answerConfig routes one key while the surface is up. Every key goes to the
// host, finished or not: a change is staged as it is made, and the screen
// redraws from what the host made of it rather than from what it thinks it
// changed.
func (m *Model) answerConfig(msg tea.KeyPressMsg) (bool, overlayAction) {
	if m.configScreen == nil || m.configScreen.Screen == nil || m.configScreen.Answer == nil {
		return true, m.closeConfigScreen("")
	}
	done, result := m.configScreen.Screen.Update(msg)
	note := m.configScreen.Answer(done, result)
	if !done {
		return false, overlayAction{}
	}
	return true, m.closeConfigScreen(note)
}

// closeConfigScreen hands the screen back to the turn, which may have moved
// on while the surface was up, and leaves what the write had to say.
//
// A write that landed says so and says what it did not do: the running
// session goes on with the settings it started with, and a row that reported
// only the file would be letting the reader believe this turn had changed
// under them.
func (m *Model) closeConfigScreen(note string) overlayAction {
	m.configScreen = nil
	if note != "" {
		note += "\nThis session keeps the settings it started on; the next one starts on these."
	}
	return overlayAction{close: true, note: note}
}

// configScreenLines renders the surface into the pane it was given.
func (m Model) configScreenLines(width, height int) []string {
	if m.configScreen == nil || m.configScreen.Screen == nil {
		return nil
	}
	m.configScreen.Screen.SetSize(width, height)
	return strings.Split(m.configScreen.Screen.View(width), "\n")
}

// renderConfigHint is the one line the screen leaves where the draft box was.
// The screen holds the keyboard and states its own keys at its foot, so the
// panel says which surface has them and nothing else, the way the sources
// screen's does.
func (m Model) renderConfigHint() string {
	hint := keys.Bracket(keys.Screen.Quit) + " back to the prompt"
	return sty.SystemMsg.Render("config · "+hint) + strings.Repeat("\n", inputHeight-1)
}
