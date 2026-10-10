package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var (
	keyBTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	keyTab  = tea.KeyPressMsg{Code: tea.KeyTab}
)

// flowPickModel is a session with a model picker that can aim at two flows,
// recording what the session is told to hold.
func flowPickModel(t *testing.T) (Model, *[]string) {
	t.Helper()
	m := readyModel(t)
	var held []string
	m.wiring.SwitchModel = func(string) {}
	m.wiring.ConfigWriter = func(string, string) error { return nil }
	m.wiring.ModelFlows = func() []FlowTarget {
		return []FlowTarget{
			{Key: "behavior.classifier_model", Name: "classifier", Model: "scripted-small"},
			{Key: "behavior.explainer_model", Name: "explanation", Model: "scripted-fast"},
		}
	}
	m.wiring.HoldFlow = func(key, model string) { held = append(held, key+"="+model) }
	m.picker.models.options = []string{"scripted-fast", "scripted-small", "scripted-big"}
	m.modelName = "scripted-big"
	opened, _ := m.openModelPick()
	return opened.(Model), &held
}

// Shift+tab walks the targets forward from the session and tab walks them
// back, round; the title and the current row follow the target.
func TestModelPicker_ShiftTabCyclesTheTarget(t *testing.T) {
	m, _ := flowPickModel(t)
	card := func() (string, string) {
		c := m.picker.card
		return c.Title, c.Options[c.Focus].Label
	}
	if title, row := card(); title != "Switch model · for the session" || row != "scripted-big  (current)" {
		t.Fatalf("the session target reads %q on %q", title, row)
	}
	if !strings.Contains(strings.Join(m.pickerLines(), "\n"), "[shift+tab] for a flow") {
		t.Errorf("the key row does not offer [shift+tab] for a flow:\n%s", strings.Join(m.pickerLines(), "\n"))
	}
	m = pressKeys(t, m, keyBTab)
	if title, row := card(); title != "Switch model · for the classifier" || row != "scripted-small  (current)" {
		t.Fatalf("the classifier target reads %q on %q", title, row)
	}
	if !strings.Contains(strings.Join(m.pickerLines(), "\n"), "[shift+tab] next flow") {
		t.Errorf("the key row does not offer [shift+tab] next flow")
	}
	m = pressKeys(t, m, keyBTab)
	if title, _ := card(); title != "Switch model · for the explanation" {
		t.Fatalf("second flow reads %q", title)
	}
	m = pressKeys(t, m, keyBTab)
	if title, _ := card(); title != "Switch model · for the session" {
		t.Fatalf("the cycle should wrap to the session, got %q", title)
	}
	m = pressKeys(t, m, keyTab)
	if title, _ := card(); title != "Switch model · for the explanation" {
		t.Fatalf("tab should walk back, got %q", title)
	}
}

// The picker opens on the session every time, whichever target the last one
// was left on.
func TestModelPicker_OpensOnTheSession(t *testing.T) {
	m, _ := flowPickModel(t)
	m = pressKeys(t, m, keyBTab, keyEsc, keyEsc)
	if m.picker.card != nil {
		t.Fatal("two escapes should close the picker")
	}
	opened, _ := m.openModelPick()
	if title := opened.(Model).picker.card.Title; title != "Switch model · for the session" {
		t.Fatalf("a new picker opens on %q", title)
	}
}

// Enter on a flow holds that flow's model and leaves the session's alone.
func TestModelPicker_AFlowChoiceIsHeldForTheSession(t *testing.T) {
	m, held := flowPickModel(t)
	switched := ""
	m.wiring.SwitchModel = func(n string) { switched = n }
	m = pressKeys(t, m, keyBTab, keyEsc, keyDown, keyEnter)
	if len(*held) != 1 || (*held)[0] != "behavior.classifier_model=scripted-big" {
		t.Fatalf("held %v", *held)
	}
	if switched != "" || m.modelName != "scripted-big" {
		t.Errorf("a flow choice moved the session to %q / %q", switched, m.modelName)
	}
	last := m.transcript[len(m.transcript)-1].text
	if last != "classifier now on scripted-big · this session" {
		t.Errorf("receipt %q", last)
	}
}

// The default key is the session target's; a flow target does not offer or
// answer it.
func TestModelPicker_NoDefaultOnAFlow(t *testing.T) {
	m, held := flowPickModel(t)
	if m.picker.card.AltKey == "" {
		t.Fatal("the session target should offer the default key")
	}
	m = pressKeys(t, m, keyBTab)
	if m.picker.card.AltKey != "" {
		t.Errorf("a flow target offers %q", m.picker.card.AltKey)
	}
	m = pressKeys(t, m, keyEsc, keyPress('m'))
	if len(*held) != 0 || m.picker.card == nil {
		t.Errorf("m acted on a flow target: held %v", *held)
	}
}
