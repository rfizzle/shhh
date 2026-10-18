package chat

// The rail in full (docs/interface/surfaces.md#the-inspector-rail): `/rail`,
// and the door on the rail's foot line. A rail too short for its blocks drops
// the static and standing ones to one line counting them, and that line opens
// this — every block, nothing shed, in the pane — rather than taking the
// keyboard itself: nothing on the rail takes the keyboard, and a surface with
// its own esc answers "show me what you left out" without one. The same cell
// closes it, as every rail door's does.

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// openRail puts the rail up in full. A session with nothing on its rail yet
// answers in a row instead, the way every door with nothing behind it does.
func (m Model) openRail() (tea.Model, tea.Cmd) {
	rail := m.inspectorData()
	if rail.Empty() {
		return m.systemNotice("the rail has nothing to show yet")
	}
	m.screens = m.screens.with(stateRail, &components.RailScreen{Rail: rail})
	m.enterSurface(stateRail)
	return m, nil
}

// updateRail routes keys while the screen is up. Nothing it answers changes
// the session, so the only answer that reaches the host is leaving.
func (m Model) updateRail(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.rail()
	if screen == nil || screen.Update(msg) {
		return m.closeRailScreen()
	}
	return m, nil
}

// closeRailScreen hands the screen back to the turn, the way its own esc does
// and the way the foot line's cell does.
func (m Model) closeRailScreen() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateRail)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// railShowing is the screen while it is the one up.
func railShowing(m Model) any {
	if m.state != stateRail || m.screens.rail() == nil {
		return nil
	}
	return m.screens.rail()
}

// railLines renders the screen over the rail as the session reads it at this
// paint, so a child that blocks while it is up is on it at once.
func (m Model) railLines(width, height int) []string {
	screen := m.screens.rail()
	if screen == nil {
		return nil
	}
	screen.Rail = m.inspectorData()
	screen.SetSize(width, height)
	return strings.Split(screen.View(width), "\n")
}

// renderRailHint is the one line the screen leaves where the draft box was:
// the way out and nothing else, the way the readings screen's does.
func (m Model) renderRailHint() []hintSeg {
	return paneHint("rail")
}
