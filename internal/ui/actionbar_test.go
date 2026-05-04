package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateBar(m ActionBarModel, msg tea.Msg) (ActionBarModel, tea.Cmd) {
	model, cmd := m.Update(msg)
	return model.(ActionBarModel), cmd
}

func TestActionBar_InitialState(t *testing.T) {
	m := NewActionBarModel()
	if m.Selected() != ActionNone {
		t.Errorf("expected no selection initially, got %v", m.Selected())
	}
}

func TestActionBar_RightNavWraps(t *testing.T) {
	m := NewActionBarModel()
	// cursor starts at 0 (Run), advance through all items to wrap
	for range singleActions {
		m, _ = updateBar(m, tea.KeyMsg{Type: tea.KeyRight})
	}
	// Verify by pressing Enter — should select Run (index 0)
	_, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionRun {
		t.Errorf("expected ActionRun after wrapping, got %v", msg)
	}
}

func TestActionBar_LeftNavWraps(t *testing.T) {
	m := NewActionBarModel()
	// cursor starts at 0, left wraps to last (Cancel)
	m, _ = updateBar(m, tea.KeyMsg{Type: tea.KeyLeft})
	_, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionCancel {
		t.Errorf("expected ActionCancel after wrapping left, got %v", msg)
	}
}

func TestActionBar_TabNavigates(t *testing.T) {
	m := NewActionBarModel()
	m, _ = updateBar(m, tea.KeyMsg{Type: tea.KeyTab})
	_, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionCopy {
		t.Errorf("expected ActionCopy after Tab, got %v", msg)
	}
}

func TestActionBar_ShiftTabNavigates(t *testing.T) {
	m := NewActionBarModel()
	m, _ = updateBar(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	_, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionCancel {
		t.Errorf("expected ActionCancel after Shift+Tab, got %v", msg)
	}
}

func TestActionBar_ShortcutR(t *testing.T) {
	m := NewActionBarModel()
	m, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.Selected() != ActionRun {
		t.Errorf("expected ActionRun, got %v", m.Selected())
	}
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionRun {
		t.Errorf("expected ActionSelectedMsg{ActionRun}, got %v", msg)
	}
}

func TestActionBar_ShortcutC(t *testing.T) {
	m := NewActionBarModel()
	m, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.Selected() != ActionCopy {
		t.Errorf("expected ActionCopy, got %v", m.Selected())
	}
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionCopy {
		t.Errorf("expected ActionSelectedMsg{ActionCopy}, got %v", msg)
	}
}

func TestActionBar_ShortcutEsc(t *testing.T) {
	m := NewActionBarModel()
	m, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.Selected() != ActionCancel {
		t.Errorf("expected ActionCancel, got %v", m.Selected())
	}
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionCancel {
		t.Errorf("expected ActionSelectedMsg{ActionCancel}, got %v", msg)
	}
}

func TestActionBar_ShortcutQ(t *testing.T) {
	m := NewActionBarModel()
	m, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.Selected() != ActionCancel {
		t.Errorf("expected ActionCancel, got %v", m.Selected())
	}
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionCancel {
		t.Errorf("expected ActionSelectedMsg{ActionCancel}, got %v", msg)
	}
}

func TestActionBar_EnterSelectsCurrent(t *testing.T) {
	m := NewActionBarModel()
	// Default cursor is at Run (index 0)
	m, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Selected() != ActionRun {
		t.Errorf("expected ActionRun, got %v", m.Selected())
	}
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionRun {
		t.Errorf("expected ActionSelectedMsg{ActionRun}, got %v", msg)
	}
}

func TestActionBar_ViewShowsAllOptions(t *testing.T) {
	m := NewActionBarModel()
	view := m.View()
	if !strings.Contains(view, "Run") {
		t.Error("expected 'Run' in view")
	}
	if !strings.Contains(view, "Copy") {
		t.Error("expected 'Copy' in view")
	}
	if !strings.Contains(view, "Edit") {
		t.Error("expected 'Edit' in view")
	}
	if !strings.Contains(view, "Revise") {
		t.Error("expected 'Revise' in view")
	}
	if !strings.Contains(view, "Cancel") {
		t.Error("expected 'Cancel' in view")
	}
}

func TestActionBar_ShortcutE(t *testing.T) {
	m := NewActionBarModel()
	m, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if m.Selected() != ActionEdit {
		t.Errorf("expected ActionEdit, got %v", m.Selected())
	}
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionEdit {
		t.Errorf("expected ActionSelectedMsg{ActionEdit}, got %v", msg)
	}
}

func TestActionBar_ShortcutV(t *testing.T) {
	m := NewActionBarModel()
	m, cmd := updateBar(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if m.Selected() != ActionRevise {
		t.Errorf("expected ActionRevise, got %v", m.Selected())
	}
	msg := cmd()
	if sel, ok := msg.(ActionSelectedMsg); !ok || sel.Action != ActionRevise {
		t.Errorf("expected ActionSelectedMsg{ActionRevise}, got %v", msg)
	}
}

func TestActionBar_Reset(t *testing.T) {
	m := NewActionBarModel()
	m, _ = updateBar(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.Selected() != ActionRun {
		t.Fatal("precondition: expected ActionRun")
	}
	m = m.Reset()
	if m.Selected() != ActionNone {
		t.Errorf("expected ActionNone after reset, got %v", m.Selected())
	}
}

func TestActionBar_ViewShowsShortcuts(t *testing.T) {
	m := NewActionBarModel()
	view := m.View()
	if !strings.Contains(view, "(r)") {
		t.Error("expected '(r)' shortcut in view")
	}
	if !strings.Contains(view, "(c)") {
		t.Error("expected '(c)' shortcut in view")
	}
	if !strings.Contains(view, "(e)") {
		t.Error("expected '(e)' shortcut in view")
	}
	if !strings.Contains(view, "(v)") {
		t.Error("expected '(v)' shortcut in view")
	}
	if !strings.Contains(view, "(esc)") {
		t.Error("expected '(esc)' shortcut in view")
	}
}
