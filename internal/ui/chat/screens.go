package chat

// The screens a size offers: one table the size passes in.
//
// A pane screen used to be a row written into the chat register by hand,
// which meant a second size with other screens would have had to edit that
// register. A screen is now a screenSpec — the state it takes, the command
// that opens it, the key surface that lists its keys, where it draws and what
// draws it — and a size hands the register its table. chatScreens is the chat
// size's. The cards, selectors and viewers stay hand-written rows of the
// register (overlay.go): they are stages of a turn, not screens a size
// chooses to offer. See
// docs/architecture.md#a-busy-screen-gives-each-of-its-modes-one-owner.

import (
	"strings"

	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// screenSpec is one screen a size offers.
type screenSpec struct {
	// state is the state the screen takes while it is up.
	state state
	// command is the slash command whose job is opening it, "" for a screen
	// no command of its own opens. commandArgs are the words after it that
	// open this screen where its other forms do other things.
	command     string
	commandArgs []string
	// surface is the register row of the keyed surfaces
	// (internal/ui/keys) that lists its keys, keys.NoSurface where the
	// screen has none of its own.
	surface keys.SurfaceID
	// place is where the grammar draws it.
	place placement
	// draw renders the screen into the rectangle it was given; nil draws
	// nothing.
	draw func(m Model, width, height int) []string
	// held reports that the session keeps the screen in heldScreens while it
	// is up, and that it borrows the turn, stands over the rail and keeps a
	// drag from selecting, as every held screen does.
	held bool
	// row is what is the screen's own: its hint, its keys, its rail door.
	row mode
}

// mode is the register row the spec declares.
func (s screenSpec) mode() *mode {
	row := s.row
	row.place = s.place
	row.lines = s.draw
	row.command = s.command
	row.commandArgs = s.commandArgs
	if s.held {
		row.holds = true
		row.borrows = true
		row.hidesRail = true
		row.noSelection = true
	}
	return &row
}

// chatScreens is the chat size's table: the screens `shhh chat` and `shhh
// code` offer. It is a function and not a variable because the rows name the
// session's own methods, which ask the register back (overlays).
func chatScreens() []screenSpec {
	return []screenSpec{
		{state: stateReview, command: "/review", surface: keys.OnReview, place: placePane,
			draw: func(m Model, width, height int) []string {
				if m.review == nil {
					return nil
				}
				m.review.SetSize(width, height)
				return strings.Split(m.review.View(width), "\n")
			},
			row: mode{
				borrows:     true,
				hidesRail:   true,
				noSelection: true,
				hint:        (Model).renderReviewHint,
				keys:        (Model).updateReview,
				// The CHANGES door is /diff's bare form: the session's whole
				// changeset, read in review mode.
				doorCommand: "/diff",
				door:        &surfaceDoor{components.RailChanges, railDoor{Model.openSessionDiff, reviewShowing, Model.closeReview}},
			}},
		// The screens the session holds while they take the pane. Each is the
		// screen's accessor and what is its own; sizedPane supplies the draw.
		{state: stateContext, command: "/context", surface: keys.OnContext, place: placePane, held: true,
			draw: sizedPane(heldScreens.contextScreen), row: contextScreenRow()},
		{state: stateSources, command: "/sources", surface: keys.OnSources, place: placePane, held: true,
			draw: sizedPane(heldScreens.sources), row: sourcesScreenRow()},
		{state: stateSteps, command: "/steps", surface: keys.NoSurface, place: placePane, held: true,
			draw: sizedPane(heldScreens.steps), row: stepsScreenRow()},
		{state: stateReadings, command: "/readings", surface: keys.NoSurface, place: placePane, held: true,
			draw: sizedPane(heldScreens.readings), row: readingsScreenRow()},
		{state: stateTurns, command: "/turns", surface: keys.NoSurface, place: placePane, held: true,
			draw: sizedPane(heldScreens.turns), row: turnsScreenRow()},
		{state: stateAlerts, command: "/alerts", surface: keys.NoSurface, place: placePane, held: true,
			draw: sizedPane(heldScreens.alerts), row: alertsScreenRow()},
		{state: stateSpend, command: "/stats", surface: keys.NoSurface, place: placePane, held: true,
			draw: sizedPane(heldScreens.spend), row: spendScreenRow()},
		{state: stateFlakes, command: "/gate", commandArgs: []string{"flakes"}, surface: keys.NoSurface, place: placePane, held: true,
			draw: sizedPane(heldScreens.flakes), row: flakesScreenRow()},
		// What repeats, each as a proposal. The screen writes nothing; enter
		// opens the proposal's card, which is where a yes is given
		// (patterns.go).
		{state: stateProposals, command: patternsCommandName, surface: keys.NoSurface, place: placePane, held: true,
			draw: sizedPane(heldScreens.patterns),
			row: mode{
				hint: (Model).renderPatternsHint,
				keys: (Model).updatePatterns,
			}},
		{state: stateTools, command: "/mcp", surface: keys.NoSurface, place: placePane, held: true,
			draw: (Model).toolsLines,
			row: mode{
				hint: (Model).renderToolsHint,
				keys: (Model).updateTools,
				door: &surfaceDoor{components.RailTools, railDoor{Model.openTools, toolsShowing, Model.closeToolsScreen}},
			}},
		{state: stateSafety, command: "/safety", surface: keys.NoSurface, place: placePane, held: true,
			draw: (Model).safetyLines,
			row: mode{
				hint: (Model).renderSafetyHint,
				keys: (Model).updateSafety,
			}},
		{state: stateNotes, command: "/notes", surface: keys.OnNotes, place: placePane, held: true,
			draw: sizedPane(heldScreens.notes), row: notesScreenRow()},
		{state: stateBacklog, command: "/todo", surface: keys.OnBacklog, place: placePane, held: true,
			draw: func(m Model, width, height int) []string {
				if m.screens.backlog() == nil {
					return nil
				}
				return strings.Split(m.backlogPane(width, height), "\n")
			},
			row: mode{
				hint: (Model).renderTodoScreenHint,
				keys: (Model).updateTodoScreen,
				door: &surfaceDoor{components.RailTodo, railDoor{Model.openTodoDoor, backlogShowing, Model.closeTodoScreen}},
			}},
		// The one pane screen that can write a file. It writes on `[ctrl+s]`
		// alone and asks before it walks away from anything staged, which is
		// the screen's own rule rather than the register's (config.go).
		{state: stateConfig, command: "/config", surface: keys.NoSurface, place: placePane, held: true,
			draw: (Model).configScreenLines,
			row: mode{
				hint:   (Model).renderConfigHint,
				answer: (*Model).answerConfig,
			}},
		// The editor pane, the second surface that is typed into. Every
		// letter is the file's while it is up, so it reads the keys ahead of
		// the handover and the grace window the way the viewers do: a
		// decision arriving mid-word must not take the next keystroke out
		// of the file (edit.go).
		{state: stateEditor, command: "/edit", surface: keys.OnEditor, place: placePane, held: true,
			draw: (Model).editPaneLines,
			row: mode{
				aboveDecision: true,
				hint:          (Model).renderEditPaneHint,
				answer:        (*Model).answerEditPane,
			}},
		// The profile drafter is a flow rather than a reading and needs the
		// room for the same reason the readings do: the draft it ends on is a
		// whole file. The manager's own row opens it, not a command.
		{state: statePersona, surface: keys.OnProfileDrafter, place: placePane,
			draw: func(m Model, width, height int) []string {
				if m.personaScreen == nil {
					return nil
				}
				return strings.Split(m.personaPane(width, height), "\n")
			},
			row: mode{
				borrows:   true,
				hidesRail: true,
				hint:      (Model).renderPersonaHint,
				keys:      (Model).updatePersona,
			}},
	}
}

// sizedPane is the drawer of a screen the session holds while it takes the
// pane, drawn the plain way: it sizes the held screen to the pane on every
// paint and draws nothing while none is held. screen is the accessor it is
// held under.
func sizedPane[T any, P interface {
	*T
	SetSize(width, height int)
	View(width int) string
}](screen func(heldScreens) P) func(m Model, width, height int) []string {
	return func(m Model, width, height int) []string {
		held := screen(m.screens)
		if held == nil {
			return nil
		}
		held.SetSize(width, height)
		return strings.Split(held.View(width), "\n")
	}
}

func contextScreenRow() mode {
	return mode{
		ownsQuit: true,
		hint:     (Model).renderContextHint,
		answer:   (*Model).answerContext,
		door:     &surfaceDoor{components.RailContext, railDoor{Model.openContext, contextShowing, Model.closeContextScreen}},
	}
}

func sourcesScreenRow() mode {
	return mode{
		hint: (Model).renderSourcesHint,
		keys: (Model).updateSources,
	}
}

func stepsScreenRow() mode {
	return mode{
		hint: (Model).renderStepsHint,
		keys: (Model).updateSteps,
		door: &surfaceDoor{components.RailSteps, railDoor{Model.openSteps, stepsShowing, Model.closeStepsScreen}},
		// PLAN stands where STEPS would while an approved plan is being
		// executed, and the screen is then the plan's (worksteps.go).
		doorAlso: []string{components.RailPlan},
	}
}

func readingsScreenRow() mode {
	return mode{
		hint: (Model).renderReadingsHint,
		keys: (Model).updateReadings,
		door: &surfaceDoor{components.RailSummary, railDoor{Model.openReadings, readingsShowing, Model.closeReadingsScreen}},
	}
}

func turnsScreenRow() mode {
	return mode{
		hint: (Model).renderTurnsHint,
		keys: (Model).updateTurns,
		door: &surfaceDoor{components.RailTurn, railDoor{Model.openTurns, turnsShowing, Model.closeTurnsScreen}},
	}
}

func alertsScreenRow() mode {
	return mode{
		hint: (Model).renderAlertsHint,
		keys: (Model).updateAlerts,
		door: &surfaceDoor{components.RailAlerts, railDoor{Model.openAlerts, alertsShowing, Model.closeAlertsScreen}},
	}
}

func spendScreenRow() mode {
	return mode{
		hint: (Model).renderStatsHint,
		keys: (Model).updateStats,
		door: &surfaceDoor{components.RailSpend, railDoor{Model.openStats, statsShowing, Model.closeStatsScreen}},
	}
}

func flakesScreenRow() mode {
	return mode{
		hint: (Model).renderFlakesHint,
		keys: (Model).updateFlakes,
	}
}

func notesScreenRow() mode {
	return mode{
		hint: (Model).renderNotesHint,
		keys: (Model).updateNotes,
	}
}
