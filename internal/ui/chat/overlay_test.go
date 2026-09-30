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

		statePick:          placePanel,
		stateTodoPropose:   placePanel,
		stateTodoDraft:     placePanel,
		stateTodoGroom:     placePanel,
		statePasteDrop:     placePanel,
		stateScaffold:      placePanel,
		stateTodoPause:     placePanel,
		stateUndoConfirm:   placePanel,
		stateInboundHold:   placePanel,
		stateQueue:         placePanel,
		stateQuitConfirm:   placePanel,
		stateKeyEntry:      placePanel,
		stateFocus:         placePanel,
		statePressure:      placePanel,
		stateCommitCard:    placePanel,
		stateCommitMessage: placePanel,
		stateRewindScope:   placePanel,

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
		stateSafety:     placePane,
		stateNotes:      placePane,
		stateBacklog:    placePane,
		statePersona:    placePane,
		stateConfig:     placePane,

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
// the rows: a surface's command is on the menu once and has a paragraph in
// /help, a pane surface stands over the rail, and every rail door is a row's.
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
	for name, o := range rows {
		if c := o.command; c != nil {
			if listed[c.name] != 1 {
				t.Errorf("%s declares %s, which the completion registry lists %d times", name, c.name, listed[c.name])
			}
			if strings.TrimSpace(c.help) == "" {
				t.Errorf("%s declares %s with no /help paragraph", name, c.name)
			}
			if c.open == nil {
				t.Errorf("%s declares %s and nothing to do when it is typed", name, c.name)
			}
		}
		if d := o.door; d != nil {
			if other, dup := doors[d.block]; dup {
				t.Errorf("%s and %s both declare the %s door", name, other, d.block)
			}
			doors[d.block] = name
			if d.open == nil || d.surface == nil || d.close == nil {
				t.Errorf("%s declares the %s door without an open, a showing and a close", name, d.block)
			}
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
	registerCommands, registerDoors, registerDoorNames = nil, nil, nil
	slashOnce, slashTable = sync.Once{}, nil

	var m Model
	if c, ok := registeredCommand("/context"); !ok || c.open == nil {
		t.Fatal("the command dispatch has no /context before a paint")
	}
	if len(slashCommands()) == 0 {
		t.Fatal("the completion registry is empty before a paint")
	}
	if len(railDoors()) == 0 || len(railDoorSet()) != len(railDoors()) {
		t.Fatal("the rail doors are missing before a paint")
	}
	if strings.TrimSpace(commandHelp(registeredSlash("/steps"))) == "" {
		t.Fatal("/help has no paragraph for a register command before a paint")
	}
	m.state = stateSteps
	if !m.inspectorHidden() {
		t.Fatal("the rail should stand hidden under a pane surface before a paint")
	}
	m.mouseOn, m.ready = true, true
	if m.selectableSurface() {
		t.Fatal("the transcript should not be selectable under a pane surface before a paint")
	}
}
