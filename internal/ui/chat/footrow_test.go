package chat

import (
	"fmt"
	"strings"
	"testing"
)

// The foot row is a ladder, floor first, and offers drop from the right as
// the width runs out (docs/interface/surfaces.md#the-input-frame). One case
// per width and per idle and working, read off the Frame board's windows 19
// to 26: the row the board draws is the row the frame draws, except that the
// board's 110 and 130 boxes are the full column count and the frame's are the
// terminal less its padding, so each rung that depends on the last four
// columns arrives four columns later (quit idle, commands working).
func TestFrame_TheFootRowLadder(t *testing.T) {
	idle := "[enter] send · [shift+tab] mode · [ctrl+/] commands · [ctrl+]] keys · [ctrl+n] queue · [ctrl+c] ×2 quit"
	working := "[ctrl+p] hold · [ctrl+c] stop the run · [enter] add to this turn · [shift+tab] mode · [ctrl+/] commands · [ctrl+]] keys · [ctrl+n] queue"
	cases := []struct {
		width   int
		working bool
		want    string
	}{
		{60, false, "[enter] send · [shift+tab] mode · [ctrl+]] keys"},
		{80, false, "[enter] send · [shift+tab] mode · [ctrl+/] commands · [ctrl+]] keys"},
		{110, false, strings.TrimSuffix(idle, " · [ctrl+c] ×2 quit")},
		{130, false, idle},
		{60, true, "[ctrl+p] hold · [ctrl+c] stop the run"},
		{80, true, "[ctrl+p] hold · [ctrl+c] stop the run · [enter] add to this turn"},
		{110, true, "[ctrl+p] hold · [ctrl+c] stop the run · [enter] add to this turn · [shift+tab] mode"},
		{130, true, strings.TrimSuffix(working, " · [ctrl+n] queue")},
	}
	for _, c := range cases {
		name := fmt.Sprintf("%d idle", c.width)
		m := frameModel(t, c.width, 40)
		if c.working {
			name = fmt.Sprintf("%d working", c.width)
			m.state = stateStreaming
		}
		if got := footRow(stripANSI(m.renderPromptFrame())); got != c.want {
			t.Errorf("%s: foot row\n got  %q\n want %q", name, got, c.want)
		}
	}
}

// A narrow terminal is not a keyless one: the floor is what each state needs
// at once, and it is on the row at sixty and eighty columns.
func TestFrame_NarrowRowsAreNeverHintless(t *testing.T) {
	for _, width := range []int{60, 80} {
		idle := footRow(stripANSI(frameModel(t, width, 40).renderPromptFrame()))
		for _, want := range []string{"[enter] send", "[ctrl+]] keys"} {
			if !strings.Contains(idle, want) {
				t.Errorf("at %d columns the idle row lost %q: %q", width, want, idle)
			}
		}
		m := frameModel(t, width, 40)
		m.state = stateStreaming
		working := footRow(stripANSI(m.renderPromptFrame()))
		for _, want := range []string{"[ctrl+p] hold", "[ctrl+c] stop the run"} {
			if !strings.Contains(working, want) {
				t.Errorf("at %d columns the working row lost %q: %q", width, want, working)
			}
		}
	}
}

// The editor and the attach chord are not rungs: a rarity does not earn the
// row, and the attach chord is a contextual helper above the prompt.
func TestFrame_TheEditorAndTheAttachChordLeaveTheRow(t *testing.T) {
	for _, width := range []int{60, 80, 110, 130, 200} {
		row := footRow(stripANSI(frameModel(t, width, 40).renderPromptFrame()))
		for _, gone := range []string{"ctrl+g", "ctrl+v", "editor", "attach", "ctrl+d"} {
			if strings.Contains(row, gone) {
				t.Errorf("at %d columns the row still carries %q: %q", width, gone, row)
			}
		}
	}
}
