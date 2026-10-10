package cli

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The cancel chord on a supporting screen is the quit, and the quit is two
// presses of it in a row: the first answers nothing and the screen never sees
// it, the second ends the program, and any other key between them starts the
// count again. It is not the screen's way back — that is esc, which the
// screen answers itself (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestScreenHost_TheCancelChordQuitsOnTheSecondPress(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	answered := 0
	var m tea.Model = newScreenModel(&components.MetricsScreen{}, 80, func(done bool, _ components.MetricsResult) tea.Cmd {
		answered++
		return nil
	})
	press := func(k tea.KeyPressMsg) tea.Cmd {
		var cmd tea.Cmd
		m, cmd = m.Update(k)
		return cmd
	}
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}

	if quits(press(ctrlC)) {
		t.Fatal("one ctrl+c must not quit")
	}
	if answered != 0 {
		t.Fatal("the chord is the host's: the screen must not be handed it")
	}
	press(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if quits(press(ctrlC)) {
		t.Fatal("a key between the two presses starts the count again")
	}
	if !quits(press(ctrlC)) {
		t.Fatal("the second ctrl+c in a row must quit")
	}
}

// A first press the reader walked away from costs nothing: past the window
// the next press arms again rather than quitting.
func TestScreenHost_TheCancelChordsWindowExpires(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	m := newScreenModel(&components.MetricsScreen{}, 80, func(bool, components.MetricsResult) tea.Cmd { return nil })
	next, _ := m.Update(ctrlC)
	m = next.(screenModel[components.MetricsResult])
	m.armed = m.armed.Add(-2 * pressAgain)
	next, cmd := m.Update(ctrlC)
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("a press past the window must arm again, not quit")
		}
	}
	if next.(screenModel[components.MetricsResult]).armed.IsZero() {
		t.Fatal("the late press should arm a fresh window")
	}
}
