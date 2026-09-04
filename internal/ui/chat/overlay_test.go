package chat

// The overlay register against the surfaces it dispatches.
//
// The register replaced six lists, and the failure it exists to stop is a
// mode present in some of them and missing from others — a surface that
// draws and cannot be typed into, or one that answers keys and cannot be
// seen. None of that is a compile error, so it is these tests.

import (
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

		statePick:        placePanel,
		stateTodoPropose: placePanel,
		stateTodoDraft:   placePanel,
		stateTodoGroom:   placePanel,
		statePasteDrop:   placePanel,
		stateScaffold:    placePanel,
		stateTodoPause:   placePanel,
		stateUndoConfirm: placePanel,
		stateQuitConfirm: placePanel,
		stateKeyEntry:    placePanel,
		stateFocus:       placePanel,
		statePressure:    placePanel,

		stateDiffFull:   placePane,
		stateOutputFull: placePane,
		statePreview:    placePane,
		stateReview:     placePane,
		stateContext:    placePane,
		stateBacklog:    placePane,
		statePersona:    placePane,

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
	overlayOnce.Do(func() { overlayTable = buildOverlays() })
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
	overlayOnce.Do(func() { overlayTable = buildOverlays() })
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
	overlayOnce.Do(func() { overlayTable = buildOverlays() })
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
	overlayOnce.Do(func() { overlayTable = buildOverlays() })
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
