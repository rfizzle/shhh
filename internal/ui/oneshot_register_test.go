package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The bar answers seven keys and no others. The explanation, the other
// commands, the dry run, stepping through several, going back a revise and the
// q and ctrl+c a bar once answered or never did are not keys of it: they are
// rows of the view, or the draft's chord answered around the bar.
func TestOneShot_TheRegisterIsSevenKeys(t *testing.T) {
	want := []struct {
		key  string
		do   Action
		word string
	}{
		{"enter", ActionShow, "[enter] show what it would affect"},
		{"y", ActionRun, "[y] run it"},
		{"e", ActionEdit, "[e] edit"},
		{"r", ActionRevise, "[r] retry with a note"},
		{"c", ActionCopy, "[c] copy"},
		{"ctrl+s", ActionSave, "[ctrl+s] write"},
		{"esc", ActionCancel, "[esc] back"},
	}
	m := NewActionBarModel().SetRevision(1)
	view := ansi.Strip(m.View(barWidth))
	if n := strings.Count(view, "["); n != len(want) {
		t.Errorf("the bar draws %d keys, want %d:\n%s", n, len(want), view)
	}
	for _, w := range want {
		if _, got := pressBar(t, m, w.key); got != w.do {
			t.Errorf("%q selected %v, want %v", w.key, got, w.do)
		}
		if !strings.Contains(view, w.word) {
			t.Errorf("the bar does not print %q:\n%s", w.word, view)
		}
	}
	for _, k := range []string{"x", "a", "d", "p", "t", "u", "q", "s"} {
		if _, got := pressBar(t, m.SetMulti(true).SetDanger(true), k); got != ActionNone {
			t.Errorf("%q selected %v; it is not a key of the bar", k, got)
		}
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Error("the bar answered ctrl+c; it is the draft's chord, answered around the bar")
	}
}

// ctrl+c is not the bar's: the first press stops what the surface is running
// — the dry run — and a press with nothing running opens the window the quit
// takes, the same two presses as every other surface. A second press inside
// the window leaves; one after it starts again; any other key shuts it.
func TestOneShot_CtrlCStopsTheDryRunThenQuitsOnTheSecondPress(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	now := time.Now()
	oneShotNow = func() time.Time { return now }
	t.Cleanup(func() { oneShotNow = time.Now })

	stopped := false
	m := NewGenerateModel(makeEvents("find . -name '*.tmp' -delete"), noopCancel, nil, nil, nil, "")
	m = drainStream(m, 2)
	m = press(t, m, "enter")
	m = press(t, m, "enter")
	if m.Phase() != phaseDryRun {
		t.Fatalf("the dry run row did not start a dry run: phase %v", m.Phase())
	}
	m.stopDry = func() { stopped = true }
	m = step(m, ctrlC)
	if !stopped || m.Phase() != phaseView || !strings.Contains(m.View().Content, "stopped") {
		t.Fatalf("ctrl+c did not stop the dry run: stopped=%v phase %v", stopped, m.Phase())
	}

	// Nothing is running: the first press arms and says so, and does not leave.
	m = step(m, ctrlC)
	if m.Phase() != phaseView || !strings.Contains(m.View().Content, "[ctrl+c] again quits") {
		t.Fatalf("the first idle ctrl+c did not arm the quit: phase %v\n%s", m.Phase(), m.View().Content)
	}
	// The window expiring takes the foot away.
	m = step(m, disarmMsg{at: now})
	if strings.Contains(m.View().Content, "again quits") {
		t.Errorf("the window expired and the foot stayed:\n%s", m.View().Content)
	}
	// A second press after the window is a first press again.
	m = step(m, ctrlC)
	now = now.Add(pressAgain + time.Millisecond)
	m = step(m, ctrlC)
	if m.Phase() == phaseDone {
		t.Fatal("a second ctrl+c after the window quit")
	}
	// Another key shuts the window.
	m = step(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(m, ctrlC)
	if m.Phase() == phaseDone {
		t.Fatal("ctrl+c after another key quit; the window should have shut")
	}
	// Two in a row inside the window quit.
	m = step(m, ctrlC)
	if m.Phase() != phaseDone || m.Result().Action != ActionCancel {
		t.Errorf("the second ctrl+c did not leave: phase %v", m.Phase())
	}
}
