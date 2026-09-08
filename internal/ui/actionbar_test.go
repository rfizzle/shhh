package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	if strings.Contains(NewActionBarModel().View(barWidth), "dry run") {
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

// barWidth is a terminal wide enough for the whole row, so a test about which
// keys are offered is not also a test about where the row breaks.
const barWidth = 200

func TestActionBar_ViewIsOneRowOfBracketedKeys(t *testing.T) {
	view := NewActionBarModel().View(barWidth)
	for _, want := range []string{
		"[↵] run", "[e] edit", "[r] revise", "[x] explain",
		"[c] copy", "[s] save", "[esc] quit",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("bar is missing %q:\n%s", want, view)
		}
	}
	if rows := len(strings.Split(view, "\n")); rows != 1 {
		t.Errorf("the bar is %d rows in a %d-column terminal, want 1:\n%s", rows, barWidth, view)
	}
}

// A narrow terminal is the one that used to lose the end of the row: the
// renderer drops what is past the last column, so the keys that went missing
// were `[s] save` and the `[esc]` that says how to leave.
func TestActionBar_NarrowRowBreaksBetweenKeysAndKeepsThemAll(t *testing.T) {
	const width = 60
	view := NewActionBarModel().SetDanger(true).SetDryRun(true).View(width)
	for _, want := range []string{
		"[↵] show what it would affect", "[y] run it", "[d] dry run",
		"[e] edit", "[r] revise", "[x] explain", "[c] copy", "[s] save",
		"[esc] quit",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the narrow bar dropped %q:\n%s", want, view)
		}
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("row %d is %d columns wide in a %d-column terminal:\n%s", i+1, w, width, line)
		}
		// A row that begins anywhere but at a key is a row that was broken
		// through the middle of one.
		if !strings.HasPrefix(ansi.Strip(line), "[") {
			t.Errorf("row %d does not start with a key:\n%s", i+1, line)
		}
	}
}

// The counter says which command the keys belong to, so it stays on the row
// the keys start on rather than taking one of its own.
func TestActionBar_NarrowRowKeepsTheRevisionCountBesideTheFirstKey(t *testing.T) {
	view := NewActionBarModel().SetRevision(2).View(40)
	first := ansi.Strip(strings.Split(view, "\n")[0])
	if !strings.HasPrefix(first, "revision 2") {
		t.Errorf("the revision count is not leading the row:\n%s", view)
	}
	if !strings.Contains(first, "[↵] run") {
		t.Errorf("the count took a row of its own:\n%s", view)
	}
}

func TestActionBar_DangerViewNamesBothHalves(t *testing.T) {
	view := NewActionBarModel().SetDanger(true).SetDryRun(true).View(barWidth)
	for _, want := range []string{"[↵] show what it would affect", "[y] run it", "[d] dry run"} {
		if !strings.Contains(view, want) {
			t.Errorf("destructive bar is missing %q:\n%s", want, view)
		}
	}
}

func TestActionBar_ViewStatesTheRevisionCount(t *testing.T) {
	view := NewActionBarModel().SetRevision(2).View(barWidth)
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
