package chat

// The rail's click targets (
// docs/interface/surfaces.md#the-inspector-rail). The rail knows every file
// this session has changed and every session it has started, by name, and
// until now that was all it could do with them. A row that names a thing and
// cannot be gone to is a lookup the reader still has to type out.
//
// Three kinds of row are targets, and they pass the same test the
// transcript's do: the pointer names exactly one thing, and the thing it
// names already has a key.
//
//   - A CHANGES row. It names one path, and that path's diff is what /diff
//     opens by name.
//   - An AGENTS row. It names one session, and the chord that walks the map
//     and the manager's [enter] both attach to it already.
//   - A block's heading, and its fold marker. Each names the block, and where
//     the block has a surface holding the whole of what it bounds, that
//     surface is what a command already opens: CHANGES is /diff, AGENTS is
//     /agents, STEPS is /steps, TODO is /todo, CONTEXT is /context
//     (railDoors).
//
// A heading or a marker opens that surface, and the remembered cell closes
// it: the surface takes the rail's columns, so the row is not there for a
// second click to land on, but the cell is — the way a file row's diff has
// always worked. That is what makes a whole surface a target the same click
// can leave, and the surface's own esc leaves it too
// (docs/interface/surfaces.md#the-inspector-rail). A block with no surface
// behind it — SUMMARY is a sentence, THIS TURN and ALERTS are this turn's,
// PLAN and SPEND answer in a transcript row rather than a surface, TOOLS has
// no command — keeps its heading inert, and so does a meter's row.
//
// The rail never takes the keyboard. Attaching is a focus switch and not a
// handover: the draft holds every character it had, reading mode is not
// entered, and nothing about the pointer's arrival decides anything. A door
// has no key on the rail for the same reason — its command is its key.

import (
	"sync"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// railDoor is one surface a rail click can open: how it opens, which surface
// it is while it is up, and how it closes. surface answers the surface's own
// value — a pointer — or nil when it is not showing, so a remembered cell is
// honoured only while the very surface it opened is the one on screen.
type railDoor struct {
	open    func(Model) (tea.Model, tea.Cmd)
	surface func(Model) any
	close   func(Model) (tea.Model, tea.Cmd)
}

// railOpening is the rail cell a surface was opened from, the door it went
// through and the surface that door opened. It is the memo every rail door
// shares, so a heading, a marker and a file row are closed by their cell the
// same way.
type railOpening struct {
	pointerPress
	door    railDoor
	surface any
}

// showing reports that the surface this cell opened is still the one up. A
// surface closed by its own esc, or replaced by another opening of the same
// kind, is not: a second click on the cell then means what the row under it
// means.
func (o railOpening) showing(m Model) bool {
	return o.live && o.surface != nil && o.door.surface(m) == o.surface
}

// railDoors is every block whose heading and fold marker open a surface,
// keyed by the block's name: one row per door, the command each one's key
// twin is. It is built on first use for the reason the overlay register is
// (overlay.go): a row names the session's own methods.
func railDoors() map[string]railDoor {
	railDoorOnce.Do(func() {
		railDoorTable = map[string]railDoor{
			components.RailChanges: {Model.openSessionDiff, reviewShowing, Model.closeReview},     // /diff
			components.RailAgents:  {Model.openAgentList, agentListShowing, Model.closeAgentList}, // /agents
			components.RailSteps:   {Model.openSteps, stepsShowing, Model.closeStepsScreen},       // /steps
			components.RailTodo:    {Model.openTodoDoor, backlogShowing, Model.closeTodoScreen},   // /todo
			components.RailContext: {Model.openContext, contextShowing, Model.closeContextScreen}, // /context
		}
		railDoorNames = map[string]bool{}
		for name := range railDoorTable {
			railDoorNames[name] = true
		}
	})
	return railDoorTable
}

var (
	railDoorOnce  sync.Once
	railDoorTable map[string]railDoor
	railDoorNames map[string]bool
)

// railDoorSet is the names of the blocks with a door, which the rail is
// handed so that exactly those headings and markers carry a target.
func railDoorSet() map[string]bool {
	railDoors()
	return railDoorNames
}

// fileDoor is a file row's door. It opens by path, so it is not a row of the
// table, but it closes by its cell like every other.
func fileDoor() railDoor { return railDoor{surface: diffShowing, close: Model.closeDiffFull} }

func diffShowing(m Model) any {
	if m.state != stateDiffFull || m.fullDiff == nil {
		return nil
	}
	return m.fullDiff
}

func reviewShowing(m Model) any {
	if m.state != stateReview || m.review == nil {
		return nil
	}
	return m.review
}

func agentListShowing(m Model) any {
	if m.agentList == nil {
		return nil
	}
	return m.agentList
}

func stepsShowing(m Model) any {
	if m.state != stateSteps || m.stepsScreen == nil {
		return nil
	}
	return m.stepsScreen
}

func backlogShowing(m Model) any {
	if m.state != stateBacklog || m.backlog == nil {
		return nil
	}
	return m.backlog
}

func contextShowing(m Model) any {
	if m.state != stateContext || m.context == nil {
		return nil
	}
	return m.context
}

// openTodoDoor is bare /todo, the backlog screen, through the command's own
// path so a click says whatever the command would have said first.
func (m Model) openTodoDoor() (tea.Model, tea.Cmd) {
	return m.todoCommand([]string{"/todo"})
}

// closeAgentList takes the manager down the way its own esc does.
func (m Model) closeAgentList() (tea.Model, tea.Cmd) {
	m.agentList = nil
	m.answerAgent = ""
	m.syncViewport()
	return m, nil
}

// closeTodoScreen takes the backlog screen down the way its own esc does.
func (m Model) closeTodoScreen() (tea.Model, tea.Cmd) {
	m.shutTodoScreen()
	return m, nil
}

// closeContextScreen takes the context screen down the way its own esc does.
func (m Model) closeContextScreen() (tea.Model, tea.Cmd) {
	m.closeContext()
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// throughDoor opens a door from a rail cell and remembers the cell only
// where something opened: a door with nothing to show answers in a
// transcript row instead — a session that changed nothing, an unavailable
// manager — and leaves no cell holding a surface that is not there.
func (m Model) throughDoor(door railDoor, open func(Model) (tea.Model, tea.Cmd), x, y int) (tea.Model, tea.Cmd) {
	next, cmd := open(m)
	if nm, ok := next.(Model); ok {
		nm.railOpened = railOpening{}
		if s := door.surface(nm); s != nil {
			nm.railOpened = railOpening{pointerPress: pointerPress{x: x, y: y, live: true}, door: door, surface: s}
		}
		return nm, cmd
	}
	return next, cmd
}

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
	if o := m.railOpened; o.x == x && o.y == y && o.showing(m) {
		// The surface this cell opened is covering the rail, so the row is
		// not there to be found — but the cell is, and a click that opened a
		// thing closes it again (click.go). The cell means what it meant.
		m.railOpened = railOpening{}
		next, cmd := o.door.close(m)
		return next, cmd, true
	}
	area := m.railArea()
	if area.Empty() || !uv.Pos(x, y).In(area) {
		return m, nil, false
	}
	switch target := m.railTargetAt(area, x, y); target.Kind {
	case components.RailTargetFile:
		// The cell is remembered with the diff it opened, so that the same
		// cell closes it: the diff takes the whole surface, the rail
		// included, so by the time a second click lands there is no row left
		// under it to resolve.
		next, cmd := m.throughDoor(fileDoor(), func(m Model) (tea.Model, tea.Cmd) { return m.openFileDiff(target.Name) }, x, y)
		return next, cmd, true
	case components.RailTargetSession:
		return m.clickSession(target.Name), nil, true
	case components.RailTargetBlock:
		if door, ok := railDoors()[target.Name]; ok {
			next, cmd := m.throughDoor(door, door.open, x, y)
			return next, cmd, true
		}
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
