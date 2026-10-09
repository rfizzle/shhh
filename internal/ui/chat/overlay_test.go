package chat

// The overlay register against the surfaces it dispatches.
//
// The register replaced six lists, and the failure it exists to stop is a
// mode present in some of them and missing from others — a surface that
// draws and cannot be typed into, or one that answers keys and cannot be
// seen. None of that is a compile error, so it is these tests.

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// Every mode says where it draws, and the placement decides what the rest of
// the surface does with it: a pane overlay hides the rail and leaves a hint
// where the draft was, a panel overlay takes rows out of the transcript, and
// the two floating cards ride above the frame until the handover gives them
// the keyboard.
func TestOverlayPlacements(t *testing.T) {
	want := map[state]placement{
		stateConfirmRun:  placeFloating,
		statePlanApprove: placeFloating,
		stateQuestion:    placeFloating,

		statePick:           placePanel,
		stateTodoPropose:    placePanel,
		stateTodoDraft:      placePanel,
		stateTodoGroom:      placePanel,
		statePasteDrop:      placePanel,
		stateScaffold:       placePanel,
		stateSetup:          placePanel,
		stateToolchainDraft: placePanel,
		stateTodoPause:      placePanel,
		stateUndoConfirm:    placePanel,
		stateInboundHold:    placePanel,
		stateQueue:          placePanel,
		stateKeyPopup:       placePanel,
		stateQuitConfirm:    placePanel,
		stateKeyEntry:       placePanel,
		stateFocus:          placePanel,
		statePressure:       placePanel,
		stateCommitCard:     placePanel,
		stateCommitMessage:  placePanel,
		stateRewindScope:    placePanel,

		stateDiffFull:   placePane,
		stateOutputFull: placePane,
		stateKeyList:    placePane,
		statePreview:    placePane,
		statePasteView:  placePane,
		stateReview:     placePane,
		stateContext:    placePane,
		stateSources:    placePane,
		stateSteps:      placePane,
		stateReadings:   placePane,
		stateTurns:      placePane,
		stateAlerts:     placePane,
		stateTools:      placePane,
		stateSpend:      placePane,
		stateFlakes:     placePane,
		stateSafety:     placePane,
		stateNotes:      placePane,
		stateBacklog:    placePane,
		statePersona:    placePane,
		stateConfig:     placePane,
		stateEditor:     placePane,

		stateRetryWait: placeNone,
		stateModelList: placeNone,
	}
	for s, p := range want {
		o := overlayFor(s)
		if o == nil {
			t.Errorf("state %d has no row in the register", s)
			continue
		}
		if o.Placement() != p {
			t.Errorf("state %d places %d, want %d", s, o.Placement(), p)
		}
	}
	overlays()
	for s := range overlayTable {
		if _, ok := want[s]; !ok {
			t.Errorf("state %d is in the register and not in this test", s)
		}
	}
}

// A pane overlay leaves a line where the draft box was and a panel overlay
// does not: the pane one has taken the transcript, so the panel is all the
// room left to say how to get out of it.
func TestOverlayPaneModesLeaveAHint(t *testing.T) {
	overlays()
	for s, o := range overlayTable {
		if o.place == placePane && o.hint == nil {
			t.Errorf("state %d takes the pane and leaves no hint in the draft's place", s)
		}
		if o.place != placePane && o.hint != nil {
			t.Errorf("state %d leaves a draft hint without taking the pane", s)
		}
	}
}

// Every mode answers keys one way or the other — semantically, or by handing
// the session back itself — and every mode that draws into the panel is one
// isSurface knows about: the two lists that used to be written separately.
func TestOverlayRowsAreComplete(t *testing.T) {
	overlays()
	for s, o := range overlayTable {
		if (o.keys == nil) == (o.answer == nil) {
			t.Errorf("state %d must have exactly one of keys and answer", s)
		}
		if o.place != placeNone && o.lines == nil {
			t.Errorf("state %d draws somewhere and has no rows", s)
		}
		if o.borrows != s.isSurface() {
			t.Errorf("state %d borrows=%v but isSurface=%v", s, o.borrows, s.isSurface())
		}
	}
}

// Adding a mode is one row. This adds one and asserts the whole surface
// dispatches it — the keyboard, the panel's rows, its bound and the turn
// parked under it — without a second list anywhere naming it.
func TestOverlayAddingAModeIsOneRow(t *testing.T) {
	const testState = state(1 << 20)
	answered := 0
	overlays()
	overlayTable[testState] = &mode{
		place:   placePanel,
		borrows: true,
		lines:   panelRows(func(Model) []string { return []string{"a mode nothing else knows about"} }),
		bound:   func(Model) int { return 7 },
		keys: func(m Model, _ tea.KeyPressMsg) (tea.Model, tea.Cmd) {
			answered++
			return m, nil
		},
	}
	t.Cleanup(func() { delete(overlayTable, testState) })

	if !testState.isSurface() {
		t.Fatal("a row that borrows the screen is not a surface")
	}
	m := readyModel(t)
	m.enterSurface(testState)
	if got := m.panel().lines; len(got) != 1 || got[0] != "a mode nothing else knows about" {
		t.Fatalf("the panel did not draw the row: %v", got)
	}
	if got := overlayFor(testState).Bound(m); got != 7 {
		t.Fatalf("bound = %d, want the row's 7", got)
	}
	if _, _, handled := m.updateKey(tea.KeyPressMsg{Code: 'x'}); !handled || answered != 1 {
		t.Fatalf("the key ladder did not route to the row (handled=%v answered=%d)", handled, answered)
	}
}

// The register row is where a surface declares everything the rest of the
// session reads about it, and this is the check that the readers agree with
// the rows: a surface's command is a command table row that opens it, named
// by that one row, on the menu once and with a paragraph in /help; a pane
// surface stands over the rail, and every rail door is a row's.
func TestRegisterDeclaresEachSurfaceOnce(t *testing.T) {
	rows := map[string]*mode{"the agent manager": agentListMode()}
	for s, o := range overlays() {
		rows[fmt.Sprintf("state %d", s)] = o
	}
	listed := map[string]int{}
	for _, c := range slashCommands() {
		listed[c.name]++
	}
	doors := map[string]string{}
	opener := map[string]string{}
	for name, o := range rows {
		if command := o.command; command != "" {
			if other, dup := opener[command]; dup {
				t.Errorf("%s and %s are both opened by %s", name, other, command)
			}
			opener[command] = name
			if c, ok := commands()[command]; !ok || c.name != command || c.open == nil {
				t.Errorf("%s names %s, which is no command table row that opens a surface", name, command)
			}
			if listed[command] != 1 {
				t.Errorf("%s names %s, which the completion registry lists %d times", name, command, listed[command])
			}
			if strings.TrimSpace(commandHelp(declaredSlash(command))) == "" {
				t.Errorf("%s names %s, which has no /help paragraph", name, command)
			}
		}
		if len(o.doorAlso) > 0 && o.door == nil {
			t.Errorf("%s names further doors and has no door for them to share", name)
		}
		if d := o.door; d != nil {
			for _, block := range append([]string{d.block}, o.doorAlso...) {
				if other, dup := doors[block]; dup {
					t.Errorf("%s and %s both declare the %s door", name, other, block)
				}
				doors[block] = name
			}
			if d.open == nil || d.surface == nil || d.close == nil {
				t.Errorf("%s declares the %s door without an open, a showing and a close", name, d.block)
			}
		}
	}
	for name, c := range commands() {
		if c.open != nil && name == c.name && opener[name] == "" {
			t.Errorf("%s opens a surface no register row names it for", name)
		}
	}
	for block := range railDoors() {
		if _, ok := doors[block]; !ok {
			t.Errorf("the rail has a %s door no register row declares", block)
		}
	}
	for block := range doors {
		if !railDoorSet()[block] {
			t.Errorf("the %s door is declared and the rail is not told it has one", block)
		}
	}
	// The two staged-attachment viewers are opened from the draft's own strip
	// and have always left the rail standing; every other pane covers it.
	keepsRail := map[state]bool{statePreview: true, statePasteView: true}
	for s, o := range overlays() {
		if o.place == placePane && !o.hidesRail && !keepsRail[s] {
			t.Errorf("state %d takes the pane and leaves the rail beside it", s)
		}
	}
}

// Every table derived from the register opens on a session nothing has
// painted yet. Each is built in the register's own first use, so the check
// starts from a register nobody has built: a derived table read on some
// other path would answer from a nil map here rather than from the table a
// paint happened to build first.
func TestRegisterDerivedTablesOpenFromAZeroModel(t *testing.T) {
	overlayOnce, overlayTable = sync.Once{}, nil
	registerDoors, registerDoorNames = nil, nil
	commandOnce, commandByName, slashHandlerAt = sync.Once{}, nil, nil
	slashOnce, slashTable = sync.Once{}, nil

	var m Model
	if c, ok := commands()["/context"]; !ok || c.open == nil {
		t.Fatal("the command dispatch has no /context before a paint")
	}
	if len(slashCommands()) == 0 {
		t.Fatal("the completion registry is empty before a paint")
	}
	if len(railDoors()) == 0 || len(railDoorSet()) != len(railDoors()) {
		t.Fatal("the rail doors are missing before a paint")
	}
	if strings.TrimSpace(commandHelp(declaredSlash("/steps"))) == "" {
		t.Fatal("/help has no paragraph for a surface's command before a paint")
	}
	m.state = stateSteps
	if !m.inspectorHidden() {
		t.Fatal("the rail should stand hidden under a pane surface before a paint")
	}
	m.pointer.mouseOn, m.ready = true, true
	if m.selectableSurface() {
		t.Fatal("the transcript should not be selectable under a pane surface before a paint")
	}
}

// The held screens that take the pane are built by one constructor, and each
// still answers as its own row: the same command and rail door it always
// had, the pane's flags, and nothing drawn while no screen is held.
func TestOverlayPaneScreensKeepTheirRows(t *testing.T) {
	want := []struct {
		s       state
		command string
		door    string
	}{
		{stateContext, "/context", components.RailContext},
		{stateSources, "/sources", ""},
		{stateSteps, "/steps", components.RailSteps},
		{stateReadings, "/readings", components.RailSummary},
		{stateTurns, "/turns", components.RailTurn},
		{stateAlerts, "/alerts", components.RailAlerts},
		{stateSpend, "/stats", components.RailSpend},
		{stateFlakes, "/gate", ""},
		{stateNotes, "/notes", ""},
		{stateTools, "/mcp", components.RailTools},
		{stateSafety, "/safety", ""},
		{stateBacklog, "/todo", components.RailTodo},
		{stateConfig, "/config", ""},
		{stateEditor, "/edit", ""},
	}
	overlays()
	if len(overlayTable) != 46 {
		t.Errorf("the register has %d rows, want 46", len(overlayTable))
	}
	for _, w := range want {
		o := overlayFor(w.s)
		if o == nil {
			t.Errorf("state %d has no row in the register", w.s)
			continue
		}
		if o.place != placePane || !o.holds || !o.borrows || !o.hidesRail || !o.noSelection {
			t.Errorf("state %d lost a pane screen's flags: %+v", w.s, o)
		}
		if o.command != w.command {
			t.Errorf("state %d's command is not %s", w.s, w.command)
		}
		door := ""
		if o.door != nil {
			door = o.door.block
		}
		if door != w.door {
			t.Errorf("state %d's rail door is %q, want %q", w.s, door, w.door)
		}
		if lines := o.Lines(Model{}, 80, 20); lines != nil {
			t.Errorf("state %d drew %q with no screen held", w.s, lines)
		}
	}
	if o := overlayFor(stateContext); !o.ownsQuit || o.answer == nil {
		t.Error("the context screen no longer answers its own keys and the quit chord")
	}
	if o := overlayFor(stateSteps); len(o.doorAlso) != 1 || o.doorAlso[0] != components.RailPlan {
		t.Errorf("the steps screen's PLAN door is %v", o.doorAlso)
	}
}
