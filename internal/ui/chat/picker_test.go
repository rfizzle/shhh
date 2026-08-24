package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
)

func TestModelPick_BareModelOpensPicker(t *testing.T) {
	var switched string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2", "m3"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /model should open the picker")
	}
	if !strings.Contains(m.picker.Options[m.picker.Focus].Label, "m1") {
		t.Fatalf("the current model should be focused, got %q", m.picker.Options[m.picker.Focus].Label)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("selecting should return to input")
	}
	if switched != "m2" || m.modelName != "m2" {
		t.Fatalf("expected switch to m2, got switched=%q modelName=%q", switched, m.modelName)
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "Switched model to m2") {
		t.Fatalf("transcript should note the switch, got %q", last.text)
	}
}

func TestModelPick_EscCancels(t *testing.T) {
	var switched string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.state != stateInput || m.picker != nil {
		t.Fatal("esc should close the picker")
	}
	if switched != "" || m.modelName != "m1" {
		t.Fatal("esc should not switch the model")
	}
}

func TestModelPick_CurrentModelMergedIntoCatalog(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "custom-model").
		WithModelOptions([]string{"m1", "m2"})

	choices := m.modelPickChoices()
	if len(choices) != 3 || choices[0] != "custom-model" {
		t.Fatalf("the current model should be merged in first, got %v", choices)
	}
}

func TestModelPick_FallsBackWithoutCatalog(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "m1")

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("without a catalog bare /model should not open a picker")
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "Current model: m1") {
		t.Fatalf("expected the usage text fallback, got %q", last.text)
	}
}

func TestModePick_BareModeOpensPickerAndApplies(t *testing.T) {
	m := readyModel(t)

	m.input.SetValue("/mode")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /mode should open the picker")
	}
	if !strings.Contains(m.picker.Options[m.picker.Focus].Label, "manual") {
		t.Fatalf("the current mode should be focused, got %q", m.picker.Options[m.picker.Focus].Label)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.mode != agent.ModeAcceptEdits {
		t.Fatalf("expected accept-edits, got %v", m.mode)
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "Mode set to accept-edits") {
		t.Fatalf("transcript should note the mode change, got %q", last.text)
	}
}

func TestPick_CtrlDQuits(t *testing.T) {
	m := readyModel(t)
	m.input.SetValue("/mode")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)
	if !m.quitting || cmd == nil {
		t.Fatal("ctrl+d in a picker should quit")
	}
}

func TestPick_RendersInBottomPanel(t *testing.T) {
	m := readyModel(t)
	m.input.SetValue("/mode")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "Permission mode") {
		t.Fatal("the picker card should render in the view")
	}
}
