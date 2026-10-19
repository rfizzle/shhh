package chat

// The rail's doors (docs/interface/surfaces.md#the-inspector-rail): a block's
// heading and its fold marker open the surface that holds the whole block,
// and the cell that opened it closes it again.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/sidejobs"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// railDoorModel is railClickModel with CHANGES and AGENTS both past their
// presets, so each draws a fold marker under its rows.
func railDoorModel(t *testing.T) Model {
	t.Helper()
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv(), MaxConcurrent: 5})
	t.Cleanup(sup.Close)
	m := inspectorModel(t, 160, 60)
	m.wiring.Subagents = sup
	m.adoptChildren()
	m.pointer.mouseOn = true
	for i := range 10 {
		m.changes.Add(1, changeset.Record{
			Path: fmt.Sprintf("pkg/f%02d.go", i), AfterExists: true, After: "package pkg\n",
		})
	}
	for i := range 5 {
		spawnChild(t, sup, subagent.RoleResearcher, fmt.Sprintf("researcher-%d", i+1))
	}
	m.viewport.SetLines(m.renderHistoryLines())
	return m
}

// doorCell is the first column of the rail row that is the named block's
// door: its heading, or with marker set its fold marker.
func doorCell(t *testing.T, m Model, block string, marker bool) (x, y int) {
	t.Helper()
	area := m.railArea()
	want := components.RailTarget{Kind: components.RailTargetBlock, Name: block}
	for i, row := range m.inspectorData().Rows(area.Dx(), area.Dy()) {
		if row.Target != want {
			continue
		}
		if strings.Contains(stripANSI(row.Text), block) != marker {
			return area.Min.X, area.Min.Y + i
		}
	}
	t.Fatalf("no %s door (marker %v) on the rail:\n%s", block, marker, stripANSI(m.View().Content))
	return 0, 0
}

// Each door opens the surface its command opens, and a second click on the
// same cell closes it — the surface covers the rail, so the row is not there
// for the second click, but the cell is. Nothing a door opens takes the draft.
func TestRailDoors_AHeadingOrAMarkerOpensItsSurfaceAndTheCellClosesIt(t *testing.T) {
	for _, c := range []struct {
		block   string
		marker  bool
		model   func(*testing.T) Model
		command string
	}{
		{components.RailChanges, false, railDoorModel, "/diff"},
		{components.RailChanges, true, railDoorModel, "/diff"},
		{components.RailAgents, false, railDoorModel, "/agents"},
		{components.RailAgents, true, railDoorModel, "/agents"},
		{components.RailContext, false, railDoorModel, "/context"},
		{components.RailSteps, false, railStepsModel, "/steps"},
		{components.RailPlan, false, railPlanModel, "/plan"},
		{components.RailSummary, false, railSummaryModel, "/readings"},
		{components.RailTurn, false, railTurnModel, "/turns"},
		{components.RailAlerts, false, railAlertsModel, "/alerts"},
		{components.RailAlerts, true, railAlertsModel, "/alerts"},
		{components.RailTodo, false, railTodoModel, "/todo"},
		{components.RailTodo, true, railTodoModel, "/todo"},
		{components.RailSpend, false, railDoorModel, "/stats"},
		{components.RailTools, false, railToolsModel, "/mcp"},
		{components.RailTools, true, railToolsModel, "/mcp"},
		{components.RailProcesses, false, railProcessesModel, "/ps"},
		{components.RailCalls, false, railProcessesModel, "/ps"},
	} {
		name := c.block
		if c.marker {
			name += " marker"
		}
		t.Run(name, func(t *testing.T) {
			m := c.model(t)
			m.input.SetValue("half a sentence")
			door := railDoors()[c.block]
			x, y := doorCell(t, m, c.block, c.marker)

			opened := click(t, m, x, y)
			if door.surface(opened) == nil {
				t.Fatalf("a click on the %s door should open what %s opens, got state %d", name, c.command, opened.state)
			}
			if !opened.inspectorHidden() {
				t.Fatalf("the surface %s opens should stand over the rail", c.command)
			}
			if got := opened.input.Value(); got != "half a sentence" {
				t.Fatalf("a door must not take the draft, got %q", got)
			}
			// The key twin opens the same surface.
			typed, _ := m.runCommand(c.command, c.command)
			if door.surface(typed.(Model)) == nil {
				t.Fatalf("%s should open the surface the %s door opens", c.command, name)
			}

			closed := click(t, opened, x, y)
			if door.surface(closed) != nil {
				t.Fatalf("the cell that opened %s should close it, got state %d", c.command, closed.state)
			}
			if closed.pointer.railOpened.live {
				t.Fatal("closing the surface should leave nothing holding the cell")
			}
			if closed.state != m.state || closed.inspectorHidden() {
				t.Fatalf("the second click should put the screen back, got state %d", closed.state)
			}
		})
	}
}

// railProcessesModel is a session with processes started and a round of
// calls in flight, so the rail draws PROCESSES and CALLS.
func railProcessesModel(t *testing.T) Model {
	t.Helper()
	m := withProcesses(inspectorModel(t, 160, 60), newProcessHost())
	m.pointer.mouseOn = true
	m.calls = callFixture(t)
	return m
}

// railStepsModel is railDoorModel with the session's own working list
// declared, so the rail draws a STEPS block.
func railStepsModel(t *testing.T) Model {
	t.Helper()
	m := railDoorModel(t)
	m.workSteps = stepsCalled(t, `{"steps":[{"title":"Read the loop"},{"title":"Patch the limit"}]}`)
	return m
}

// railPlanModel is railDoorModel executing an approved plan with its first
// step carried out, so the rail draws a PLAN block in STEPS' place.
func railPlanModel(t *testing.T) Model {
	t.Helper()
	m := railDoorModel(t)
	m.planRun = newPlanRun(plan.Parse(planFixture), len(m.transcript))
	announce(t, &m, "Locate the round accounting", time.Second, false)
	m.viewport.SetLines(m.renderHistoryLines())
	return m
}

// railSummaryModel is railDoorModel with a reading landed, so the rail draws
// a SUMMARY block.
func railSummaryModel(t *testing.T) Model {
	t.Helper()
	m := railDoorModel(t)
	m.summary.writer = sidejobs.NewSummarizer(&readingProvider{text: "Reading."},
		sidejobs.SummaryConfig{Model: "fast", IntervalRounds: 10, MinGap: -1})
	landReading(&m, sidejobs.SummaryVerdict{Text: "Reading the loop.", State: sidejobs.SummaryOnTarget, Round: 3})
	return m
}

// railTurnModel is railDoorModel with its files counted as the current
// turn's, so the rail draws a THIS TURN block.
func railTurnModel(t *testing.T) Model {
	t.Helper()
	m := railDoorModel(t)
	m.turnCount = 1
	return m
}

// railAlertsModel is railDoorModel with a formatter that broke and came back
// clean beside its standing test failure, so the ALERTS block draws its
// `… 1 superseded` marker under the row.
func railAlertsModel(t *testing.T) Model {
	t.Helper()
	m := railDoorModel(t)
	m.transcript = append(m.transcript,
		entry{kind: entryCommand, text: "gofmt -l .", exitCode: 1, turn: 1},
		entry{kind: entryCommand, text: "gofmt -l .", exitCode: 0, turn: 1})
	m.viewport.SetLines(m.renderHistoryLines())
	return m
}

// railTodoModel is a two-pane session with a backlog longer than the TODO
// block draws, so the block carries its own `… N more`.
func railTodoModel(t *testing.T) Model {
	t.Helper()
	m := todoModel(t, todoTestRoot(t))
	m.pointer.mouseOn = true
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	return updated.(Model)
}

// A surface a door opened is left by its own esc too, and then the cell is a
// row again: the next click on it opens the surface rather than closing
// something that is no longer there.
func TestRailDoors_EscLeavesAndTheCellIsARowAgain(t *testing.T) {
	m := railDoorModel(t)
	x, y := doorCell(t, m, components.RailChanges, false)
	opened := click(t, m, x, y)
	if opened.state != stateReview {
		t.Fatalf("the CHANGES heading should open the session's review, got state %d", opened.state)
	}
	next, _ := opened.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	left := next.(Model)
	if left.state == stateReview {
		t.Fatal("esc should leave the review a door opened")
	}
	again := click(t, left, x, y)
	if again.state != stateReview {
		t.Fatalf("after esc the cell is the heading again and opens the review, got state %d", again.state)
	}
}

// A surface opened some other way is not the door's to close: the memo
// answers only for the very surface its cell opened.
func TestRailDoors_TheCellClosesOnlyWhatItOpened(t *testing.T) {
	m := railDoorModel(t)
	x, y := doorCell(t, m, components.RailChanges, false)
	opened := click(t, m, x, y)
	next, _ := opened.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	reopened, _ := next.(Model).runCommand("/diff", "/diff")
	typed := reopened.(Model)
	if typed.state != stateReview {
		t.Fatalf("/diff should open the review, got state %d", typed.state)
	}
	if typed.pointer.railOpened.showing(typed) {
		t.Fatal("a review /diff opened is not the one the cell remembered")
	}
}

// TestRailClick_FootLineOpensTheRail: on a terminal too short for its blocks,
// the rail ends on a line counting what it dropped, and that line is a door —
// a click opens the rail in full over the pane, the same cell closes it, its
// own esc leaves it, and nothing about it takes the draft.
func TestRailClick_FootLineOpensTheRail(t *testing.T) {
	m := railDoorModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
	m = next.(Model)
	m.input.SetValue("half a sentence")
	area := m.railArea()
	rows := m.inspectorData().Rows(area.Dx(), area.Dy())
	if len(rows) == 0 {
		t.Fatalf("no rail at 160x20:\n%s", stripANSI(m.View().Content))
	}
	foot := rows[len(rows)-1]
	if foot.Target != (components.RailTarget{Kind: components.RailTargetBlock, Name: components.RailWhole}) ||
		!strings.Contains(stripANSI(foot.Text), "↓ ") {
		t.Fatalf("a short rail should end on the foot line's door, got %q %+v:\n%s",
			stripANSI(foot.Text), foot.Target, stripANSI(m.View().Content))
	}
	x, y := area.Min.X, area.Min.Y+len(rows)-1

	opened := click(t, m, x, y)
	if opened.state != stateRail || opened.screens.rail() == nil {
		t.Fatalf("a click on the foot line should open the rail in full, got state %d", opened.state)
	}
	if !opened.inspectorHidden() {
		t.Fatal("the rail in full stands over the rail")
	}
	if got := opened.input.Value(); got != "half a sentence" {
		t.Fatalf("the door must not take the draft, got %q", got)
	}
	view := stripANSI(opened.View().Content)
	for _, block := range []string{"/rail", "the rail in full", "THIS TURN"} {
		if !strings.Contains(view, block) {
			t.Fatalf("the rail in full should hold %s:\n%s", block, view)
		}
	}

	closed := click(t, opened, x, y)
	if closed.state == stateRail || closed.inspectorHidden() || closed.pointer.railOpened.live {
		t.Fatalf("the cell that opened the rail should close it, got state %d", closed.state)
	}

	left, _ := opened.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if left.(Model).state == stateRail {
		t.Fatal("esc should leave the rail in full")
	}
}
