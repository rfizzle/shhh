package chat

// The rail's click targets (
// docs/interface/surfaces.md#the-inspector-rail). The rail knows every file
// this session has changed and every session it has started, by name, and
// until now that was all it could do with them. A row that names a thing and
// cannot be gone to is a lookup the reader still has to type out.
//
// Two kinds of row are targets, and they pass the same test the transcript's
// two do: the pointer names exactly one thing, and the thing it names already
// has a key.
//
//   - A CHANGES row. It names one path, and that path's diff is what /diff
//     opens by name.
//   - An AGENTS row. It names one session, and the chord that walks the map
//     and the manager's [enter] both attach to it already.
//
// Everything else on the rail fails it. A heading names a block rather than a
// thing in it; SUMMARY is a sentence; PLAN, TODO and TOOLS are readings whose
// rows have no act of their own; CONTEXT and SPEND are meters, and a meter
// has nothing to open. Each of those does have a surface behind it — /plan,
// /todo, /tools, /context, /stats — but a row that opened a whole surface
// would be a target the same click could not leave, which is the rule the
// transcript's targets are chosen by too (click.go).
//
// The rail never takes the keyboard. Attaching is a focus switch and not a
// handover: the draft holds every character it had, reading mode is not
// entered, and nothing about the pointer's arrival decides anything.

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// railArea is the rectangle the rail is drawn into, empty when the surface
// has not split or something is covering it. It is the same intersection the
// draw takes (model.go), so a cell is tested against what was drawn rather
// than against an arrangement worked out a second way.
func (m Model) railArea() uv.Rectangle {
	s := m.surface()
	return s.in(s.body, s.inspector)
}

// railTargetAt resolves a screen cell to what the rail's row there points at.
// A cell past the rail's last row is blank rather than absent — the rail is
// shorter than the pane beside it whenever the session has little to report —
// so it resolves to nothing rather than to no answer.
//
// The rail is drawn from the top of its rectangle downwards, one row per
// line, so the row is the offset and there is nothing to measure: the rows
// are asked for at the same width and height the draw asks for them at, and
// each one carries its own target.
func (m Model) railTargetAt(area uv.Rectangle, x, y int) components.RailTarget {
	rows := m.inspectorData().Rows(area.Dx(), area.Dy())
	i := y - area.Min.Y
	if i < 0 || i >= len(rows) {
		return components.RailTarget{}
	}
	return rows[i].Target
}

// clickRail answers a click on the rail, and reports whether the click was
// the rail's at all. A cell inside the rail's rectangle is always the rail's,
// target or not: an inert row is inert, and letting the click fall through to
// the surfaces underneath would make the rail's quiet rows do whatever
// happened to be behind them.
func (m Model) clickRail(x, y int) (tea.Model, tea.Cmd, bool) {
	if m.railDiff.live && m.railDiff.x == x && m.railDiff.y == y {
		// The full-screen diff this cell opened is covering the rail, so the
		// row is not there to be found — but the cell is, and a click that
		// opened a thing closes it again (click.go). Nothing else on this
		// screen answers a click, so the cell means what it meant.
		next, cmd := m.closeDiffFull()
		return next, cmd, true
	}
	area := m.railArea()
	if area.Empty() || !uv.Pos(x, y).In(area) {
		return m, nil, false
	}
	switch target := m.railTargetAt(area, x, y); target.Kind {
	case components.RailTargetFile:
		// The cell is remembered before the diff opens, so that the same
		// cell closes it: the diff takes the whole surface, the rail
		// included, so by the time a second click lands there is no row left
		// under it to resolve. A path with nothing to show hands it back.
		m.railDiff = pointerPress{x: x, y: y, live: true}
		next, cmd := m.openFileDiff(target.Name)
		return next, cmd, true
	case components.RailTargetSession:
		return m.clickSession(target.Name), nil, true
	}
	return m, nil, true
}

// clickSession is what a click on a row of the map does: attach to that
// session, or — on the row already marked — go back to the orchestrator,
// because a click that opened a thing closes it. A click on the
// orchestrator's own row while the keyboard is already there changes nothing
// and says nothing, which is what "already here" looks like.
func (m Model) clickSession(name string) tea.Model {
	if name == m.attachedTo {
		if name == "" {
			return m
		}
		name = ""
	}
	m.attach(name)
	if m.state == stateFocus {
		// Reading mode's cursor is an index into the transcript it was opened
		// over, and the transcript on screen is now another session's. So it
		// is re-seated the way reading mode seats it when it opens — on the
		// newest row there is something to open on — rather than left
		// pointing into rows that have gone.
		if idxs := m.expandableIndices(); len(idxs) > 0 {
			m.focusIdx = idxs[len(idxs)-1]
		} else {
			m.focusIdx = -1
		}
		m.refreshFocusView()
	}
	return m
}
