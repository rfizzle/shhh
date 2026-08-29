package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func updateBar(m ActionBarModel, msg tea.Msg) (ActionBarModel, tea.Cmd) {
	return m.Update(msg)
}

// pressBar sends one key and reports the action it selected, or ActionNone.
func pressBar(t *testing.T, m ActionBarModel, key string) (ActionBarModel, Action) {
	t.Helper()
	m, cmd := updateBar(m, keyMsg(key))
	if cmd == nil {
		return m, ActionNone
	}
	sel, ok := cmd().(ActionSelectedMsg)
	if !ok {
		return m, ActionNone
	}
	return m, sel.Action
}

// keyMsg builds the tea.KeyPressMsg whose String() is key.
func keyMsg(key string) tea.KeyPressMsg {
	switch key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	return tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
}

func TestActionBar_InitialState(t *testing.T) {
	m := NewActionBarModel()
	if m.Selected() != ActionNone {
		t.Errorf("expected no selection initially, got %v", m.Selected())
	}
}

func TestActionBar_KeysAreDirect(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want Action
	}{
		{"enter", ActionRun},
		{"e", ActionEdit},
		{"r", ActionRevise},
		{"x", ActionExplain},
		{"c", ActionCopy},
		{"s", ActionSave},
		{"esc", ActionCancel},
		{"q", ActionCancel},
	} {
		if _, got := pressBar(t, NewActionBarModel(), tc.key); got != tc.want {
			t.Errorf("%q selected %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestActionBar_NoNavigationLeft(t *testing.T) {
	// The row is not a menu: arrows and tab move nothing and select nothing.
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyLeft},
		tea.KeyPressMsg{Code: tea.KeyRight},
		tea.KeyPressMsg{Code: tea.KeyTab},
		tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift},
	} {
		m, cmd := updateBar(NewActionBarModel(), msg)
		if cmd != nil {
			t.Errorf("%v produced a command; the bar has no cursor to move", msg)
		}
		if m.Selected() != ActionNone {
			t.Errorf("%v selected %v", msg, m.Selected())
		}
	}
	// And enter after them still runs, because nothing moved.
	m := NewActionBarModel()
	m, _ = updateBar(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if _, got := pressBar(t, m, "enter"); got != ActionRun {
		t.Errorf("enter after an arrow selected %v, want ActionRun", got)
	}
}

func TestActionBar_MultiRunsAll(t *testing.T) {
	m := NewActionBarModel().SetMulti(true)
	if _, got := pressBar(t, m, "enter"); got != ActionRunAll {
		t.Errorf("enter on a multi-command bar selected %v, want ActionRunAll", got)
	}
	if _, got := pressBar(t, m, "t"); got != ActionRunStep {
		t.Errorf("[t] selected %v, want ActionRunStep", got)
	}
}

func TestActionBar_DangerMovesTheDefault(t *testing.T) {
	m := NewActionBarModel().SetDanger(true)
	if _, got := pressBar(t, m, "enter"); got != ActionAffected {
		t.Errorf("enter on a destructive command selected %v, want ActionAffected", got)
	}
	if _, got := pressBar(t, m, "y"); got != ActionRun {
		t.Errorf("[y] selected %v, want ActionRun", got)
	}
}

func TestActionBar_OrdinaryCommandIgnoresY(t *testing.T) {
	if _, got := pressBar(t, NewActionBarModel(), "y"); got != ActionNone {
		t.Errorf("[y] on an ordinary command selected %v; it is not an offer there", got)
	}
}

func TestActionBar_EnterIsSpentOnceTheRadiusIsShowing(t *testing.T) {
	m := NewActionBarModel().SetDanger(true).SetAffected(true)
	if _, got := pressBar(t, m, "enter"); got != ActionNone {
		t.Errorf("enter selected %v after the radius was already shown; only y runs", got)
	}
	if _, got := pressBar(t, m, "y"); got != ActionRun {
		t.Errorf("[y] selected %v, want ActionRun", got)
	}
}

func TestActionBar_DryRunOfferedOnlyWhenItExists(t *testing.T) {
	if _, got := pressBar(t, NewActionBarModel(), "d"); got != ActionNone {
		t.Errorf("[d] selected %v with no dry run available", got)
	}
	m := NewActionBarModel().SetDryRun(true)
	if _, got := pressBar(t, m, "d"); got != ActionDryRun {
		t.Errorf("[d] selected %v, want ActionDryRun", got)
	}
	if strings.Contains(NewActionBarModel().View(), "dry run") {
		t.Error("the bar offered a dry run it cannot perform")
	}
}

func TestActionBar_BackOfferedOnlyAfterARevise(t *testing.T) {
	if _, got := pressBar(t, NewActionBarModel(), "u"); got != ActionNone {
		t.Errorf("[u] selected %v with nothing to step back to", got)
	}
	m := NewActionBarModel().SetRevision(1)
	if _, got := pressBar(t, m, "u"); got != ActionBack {
		t.Errorf("[u] selected %v, want ActionBack", got)
	}
}

func TestActionBar_ViewIsOneRowOfBracketedKeys(t *testing.T) {
	view := NewActionBarModel().View()
	for _, want := range []string{
		"[↵] run", "[e] edit", "[r] revise", "[x] explain",
		"[c] copy", "[s] save", "[esc] quit",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("bar is missing %q:\n%s", want, view)
		}
	}
	// One row of keys, under the blank line the bar's own margin puts above it.
	rows := 0
	for _, line := range strings.Split(view, "\n") {
		if strings.TrimSpace(line) != "" {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("the bar is %d rows, want 1:\n%s", rows, view)
	}
}

func TestActionBar_DangerViewNamesBothHalves(t *testing.T) {
	view := NewActionBarModel().SetDanger(true).SetDryRun(true).View()
	for _, want := range []string{"[↵] show what it would affect", "[y] run it", "[d] dry run"} {
		if !strings.Contains(view, want) {
			t.Errorf("destructive bar is missing %q:\n%s", want, view)
		}
	}
}

func TestActionBar_ViewStatesTheRevisionCount(t *testing.T) {
	view := NewActionBarModel().SetRevision(2).View()
	if !strings.Contains(view, "revision 2") {
		t.Errorf("bar did not state the revision count:\n%s", view)
	}
	if !strings.Contains(view, "[u] back") {
		t.Errorf("bar did not offer [u]:\n%s", view)
	}
}

func TestActionBar_Reset(t *testing.T) {
	m, _ := pressBar(t, NewActionBarModel(), "c")
	if m.Selected() != ActionCopy {
		t.Fatalf("expected ActionCopy before reset, got %v", m.Selected())
	}
	if m.Reset().Selected() != ActionNone {
		t.Errorf("expected ActionNone after reset, got %v", m.Reset().Selected())
	}
}
