package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
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

// --- session pickers (S-080) ----------------------------------------------

// pickIndex is the picker row whose label starts with want.
func pickIndex(t *testing.T, m Model, want string) int {
	t.Helper()
	for i, o := range m.picker.Options {
		if strings.HasPrefix(o.Label, want) {
			return i
		}
	}
	t.Fatalf("no picker row for %q, got %v", want, m.picker.Options)
	return -1
}

// focusPick arrows from the focused row to target and selects it.
func focusPick(t *testing.T, m Model, target int) Model {
	t.Helper()
	for m.picker.Focus < target {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	for m.picker.Focus > target {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(Model)
}

func chatPickModel(t *testing.T, names ...string) Model {
	t.Helper()
	db := rewindTestDB(t)
	for _, name := range names {
		msgs := []provider.Message{
			{Role: provider.RoleUser, Content: "q for " + name},
			{Role: provider.RoleAssistant, Content: "a for " + name},
		}
		if err := db.SaveChat(name, msgs); err != nil {
			t.Fatal(err)
		}
	}
	return readyModel(t).WithDB(db)
}

func TestChatPick_BareLoadOpensPickerAndLoads(t *testing.T) {
	m := sendText(t, chatPickModel(t, "alpha", "beta"), "/load")

	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /load should open the saved-chat picker")
	}
	if m.picker.Title != "Load a saved chat" {
		t.Fatalf("unexpected picker title %q", m.picker.Title)
	}
	if len(m.picker.Options) != 2 {
		t.Fatalf("expected both saved chats, got %v", m.picker.Options)
	}
	idx := pickIndex(t, m, "alpha")
	if !strings.HasPrefix(m.picker.Options[idx].Desc, "1 turns, ") {
		t.Fatalf("description should carry turn count and time, got %q", m.picker.Options[idx].Desc)
	}

	m = focusPick(t, m, idx)
	if m.state != stateInput || m.picker != nil {
		t.Fatal("selecting should return to input")
	}
	if m.sessionName != "alpha" {
		t.Fatalf("selecting should load alpha, got session %q", m.sessionName)
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, `Loaded chat "alpha"`) {
		t.Fatalf("transcript should note the load, got %q", last.text)
	}
	if got := m.Messages()[len(m.Messages())-1].Content; got != "a for alpha" {
		t.Fatalf("the conversation should be alpha's, got %q", got)
	}
}

func TestChatPick_BareChatsOpensTheSamePicker(t *testing.T) {
	m := sendText(t, chatPickModel(t, "alpha"), "/chats")

	if m.state != statePick || m.picker == nil || m.picker.Title != "Load a saved chat" {
		t.Fatal("bare /chats should open the saved-chat picker")
	}
}

func TestChatPick_EscDoesNotLoad(t *testing.T) {
	m := sendText(t, chatPickModel(t, "alpha", "beta"), "/load")
	before := m.sessionName

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.state != stateInput || m.picker != nil {
		t.Fatal("esc should close the picker")
	}
	if m.sessionName != before {
		t.Fatalf("esc should not load anything, session moved to %q", m.sessionName)
	}
}

func TestChatPick_CurrentSessionMarkedAndFocused(t *testing.T) {
	m := chatPickModel(t, "alpha", "beta")
	m.sessionName = "beta"
	m = sendText(t, m, "/load")

	focused := m.picker.Options[m.picker.Focus]
	if !strings.HasPrefix(focused.Label, "beta") || !strings.Contains(focused.Label, "(current)") {
		t.Fatalf("the current chat should be marked and focused, got %q", focused.Label)
	}
}

func TestChatPick_NoSavedChatsKeepsTextMessage(t *testing.T) {
	for _, cmd := range []string{"/load", "/chats"} {
		m := sendText(t, chatPickModel(t), cmd)
		if m.picker != nil {
			t.Fatalf("%s should not open an empty picker", cmd)
		}
		if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "No saved chats.") {
			t.Fatalf("%s with nothing saved should keep the text message, got %q", cmd, last.text)
		}
	}
}

func TestChatPick_NoDBKeepsTextMessage(t *testing.T) {
	m := sendText(t, readyModel(t), "/load")
	if m.picker != nil {
		t.Fatal("no database → no picker")
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "unavailable") {
		t.Fatalf("expected the persistence-unavailable notice, got %q", last.text)
	}
}

// branchPickModel is a session with one rewind branch hanging off it.
func branchPickModel(t *testing.T) Model {
	t.Helper()
	m := newRewindModel(t).WithDB(rewindTestDB(t))
	m = completeExchange(t, m, "first question", "answer one")
	m = completeExchange(t, m, "second question", "answer two")
	return sendText(t, m, "/rewind 2")
}

func TestBranchPick_BareBranchesOpensPickerAndSwitches(t *testing.T) {
	m := sendText(t, branchPickModel(t), "/branches")

	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /branches should open the branch picker")
	}
	focused := m.picker.Options[m.picker.Focus]
	if !strings.HasPrefix(focused.Label, AutosaveName) || !strings.Contains(focused.Label, "(current)") {
		t.Fatalf("the current branch should be marked and focused, got %q", focused.Label)
	}
	target := 1 - m.picker.Focus
	if !strings.Contains(m.picker.Options[target].Desc, "branch of") {
		t.Fatalf("the branch row should name its parent, got %q", m.picker.Options[target].Desc)
	}
	name := m.picker.Options[target].Label

	m = focusPick(t, m, target)
	if m.sessionName != name {
		t.Fatalf("expected a switch to %q, got session %q", name, m.sessionName)
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "Switched to branch") {
		t.Fatalf("transcript should note the switch, got %q", last.text)
	}
	if got := len(m.Messages()); got != 5 {
		t.Fatalf("switching to the tail branch should restore all 5 messages, got %d", got)
	}
	// The pre-switch working conversation was saved, not lost.
	kept, err := m.db.LoadChat(AutosaveName)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 3 {
		t.Fatalf("the current branch should be saved before switching, got %d messages", len(kept))
	}
}

func TestBranchPick_SelectingCurrentBranchIsANoOp(t *testing.T) {
	m := sendText(t, branchPickModel(t), "/branches")
	before := m.sessionName

	m = focusPick(t, m, m.picker.Focus)
	if m.sessionName != before {
		t.Fatalf("picking the current branch should stay put, got %q", m.sessionName)
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "Already on") {
		t.Fatalf("expected the already-on notice, got %q", last.text)
	}
}

func TestBranchPick_NoBranchFamilyKeepsTextMessage(t *testing.T) {
	m := sendText(t, newRewindModel(t).WithDB(rewindTestDB(t)), "/branches")
	if m.picker != nil {
		t.Fatal("a session with no branches should not open a picker")
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "no branches yet") {
		t.Fatalf("expected the no-branches notice, got %q", last.text)
	}
}

func TestBranchPick_NoDBKeepsTextMessage(t *testing.T) {
	m := sendText(t, newRewindModel(t), "/branches")
	if m.picker != nil {
		t.Fatal("no database → no picker")
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "unavailable") {
		t.Fatalf("expected the persistence-unavailable notice, got %q", last.text)
	}
}
