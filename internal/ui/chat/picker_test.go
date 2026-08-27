package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

// A catalog longer than the bottom panel scrolls under the pointer rather
// than leaving it below the card (S-116). This is the /model picker, but the
// window belongs to components.Select, so /mode, /load, /chats, /branches and
// /run all get it from the same place.
func TestPick_LongCatalogScrollsWithTheFocus(t *testing.T) {
	names := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		names = append(names, fmt.Sprintf("model-%02d", i))
	}
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "model-01").
		WithModelOptions(names)
	// A short terminal, so the panel cannot hold twenty rows however it tries.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m = updated.(Model)
	m = sendText(t, m, "/model")

	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /model should open the picker")
	}
	for i := 0; i < len(names); i++ {
		panel := m.renderPick()
		want := fmt.Sprintf("%d. model-%02d", i+1, i+1)
		if !strings.Contains(ansi.Strip(panel), "❯ "+want) {
			t.Fatalf("at option %d the pointer should be on the card:\n%s", i+1, panel)
		}
		if got, budget := strings.Count(panel, "\n")+1, m.bottomPanelHeight(); got != budget {
			t.Fatalf("the panel should stay exactly %d rows, got %d", budget, got)
		}
		if i < len(names)-1 {
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
			m = updated.(Model)
		}
	}
	// The last option is reachable, which is what the old fixed slice made
	// impossible.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.modelName != "model-20" {
		t.Fatalf("the bottom of the catalog should be selectable, got %q", m.modelName)
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

// --- run picker (S-081) ---------------------------------------------------

const twoBlockResponse = "First:\n```bash\necho one\n```\nThen:\n```python\nprint(\"a\")\nprint(\"b\")\n```"

func TestRunPick_MultipleBlocksOpensPicker(t *testing.T) {
	m := runCapableModel(twoBlockResponse)
	m = sendText(t, m, "/run")

	if m.state != statePick || m.picker == nil {
		t.Fatalf("bare /run with several blocks should open the picker, got state=%d", m.state)
	}
	if len(m.picker.Options) != 2 {
		t.Fatalf("expected a row per block, got %d", len(m.picker.Options))
	}
	if m.picker.Focus != 0 {
		t.Fatalf("the first block should be focused, got %d", m.picker.Focus)
	}
	first := m.picker.Options[0].Label
	if !strings.HasPrefix(first, "echo one") || !strings.Contains(first, "bash") || !strings.Contains(first, "1 line") {
		t.Fatalf("row should carry first line, language, and line count, got %q", first)
	}
	if m.picker.Options[0].Desc != "" {
		t.Fatalf("a one-line block's preview repeats its label, so it gets no description, got %q", m.picker.Options[0].Desc)
	}
	second := m.picker.Options[1]
	if !strings.Contains(second.Label, "python") || !strings.Contains(second.Label, "2 lines") {
		t.Fatalf("second row should be a 2-line python block, got %q", second.Label)
	}
	if !strings.Contains(second.Desc, `print("a") ⏎ print("b")`) {
		t.Fatalf("description should preview the block, got %q", second.Desc)
	}
}

func TestRunPick_SelectingEntersConfirm(t *testing.T) {
	m := runCapableModel(twoBlockResponse)
	m = sendText(t, m, "/run")
	m = focusPick(t, m, 1)

	if m.state != stateConfirmRun {
		t.Fatalf("selecting a block should enter the confirm flow, got state=%d", m.state)
	}
	if m.pendingRun != "print(\"a\")\nprint(\"b\")" {
		t.Fatalf("expected the second block pending, got %q", m.pendingRun)
	}
	if m.picker != nil {
		t.Fatal("the picker should be dismissed once a block is chosen")
	}
	for _, e := range m.transcript {
		if e.kind == entrySystem && e.text == "" {
			t.Fatal("handing off to the confirm prompt should not append an empty note")
		}
	}
	if !strings.Contains(m.View(), "Approve command") {
		t.Fatal("the confirm card should render after selecting")
	}
}

func TestRunPick_SelectedBlockKeepsSafetyWarnings(t *testing.T) {
	m := runCapableModel("Safe:\n```bash\necho hi\n```\nNot:\n```bash\nrm -rf /\n```")
	m = sendText(t, m, "/run")
	m = focusPick(t, m, 1)

	if m.state != stateConfirmRun {
		t.Fatalf("expected confirm state, got %d", m.state)
	}
	if !strings.Contains(m.View(), "⚠") {
		t.Fatal("a dangerous picked block should still show its safety warning")
	}
}

func TestRunPick_EscReturnsToInputWithoutRunning(t *testing.T) {
	m := runCapableModel(twoBlockResponse)
	m = sendText(t, m, "/run")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.state != stateInput || m.picker != nil {
		t.Fatalf("esc should dismiss the picker, got state=%d", m.state)
	}
	if m.pendingRun != "" {
		t.Fatalf("esc should not stage a command, got %q", m.pendingRun)
	}
}

func TestRunPick_SingleBlockGoesStraightToConfirm(t *testing.T) {
	m := runCapableModel("Do this:\n```bash\necho hi\n```")
	m = sendText(t, m, "/run")

	if m.state != stateConfirmRun || m.picker != nil {
		t.Fatalf("one block should skip the picker, got state=%d", m.state)
	}
	if m.pendingRun != "echo hi" {
		t.Fatalf("expected 'echo hi' pending, got %q", m.pendingRun)
	}
}

func TestRunPick_NumberedFormSkipsPicker(t *testing.T) {
	m := runCapableModel(twoBlockResponse)
	m = sendText(t, m, "/run 1")

	if m.state != stateConfirmRun || m.picker != nil {
		t.Fatalf("/run <n> should skip the picker, got state=%d", m.state)
	}
	if m.pendingRun != "echo one" {
		t.Fatalf("expected 'echo one' pending, got %q", m.pendingRun)
	}
}

func TestRunPick_NoRunnerKeepsTextMessage(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleAssistant, Content: twoBlockResponse},
	}
	updated, _ := New(msgs, mockStream).Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m := updated.(Model)

	m = sendText(t, m, "/run")
	if m.state != stateInput || m.picker != nil {
		t.Fatalf("no runner should keep the text path, got state=%d", m.state)
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "not available") {
		t.Fatalf("expected the 'not available' message, got %q", last.text)
	}
}

func TestRunPick_UntaggedFenceAndBlankBlock(t *testing.T) {
	m := runCapableModel("```\nls\n```\nand\n```\n\n```")
	m = sendText(t, m, "/run")

	if m.picker == nil {
		t.Fatal("expected the picker to open")
	}
	if strings.Contains(m.picker.Options[0].Label, "·  ·") {
		t.Fatalf("an untagged fence should not leave an empty language slot, got %q", m.picker.Options[0].Label)
	}
	if !strings.HasPrefix(m.picker.Options[1].Label, "(empty block)") {
		t.Fatalf("an empty block needs a placeholder label, got %q", m.picker.Options[1].Label)
	}
}

func TestRunPickPreview_CapsLongBlocks(t *testing.T) {
	preview := runPickPreview(strings.Repeat("x", runPreviewMax*2))
	if r := []rune(preview); len(r) > runPreviewMax+2 {
		t.Fatalf("preview should be capped, got %d runes", len(r))
	}
	if !strings.HasSuffix(preview, "…") {
		t.Fatalf("a capped preview should end in an ellipsis, got %q", preview)
	}
}

// --- live model discovery (S-083) -----------------------------------------

// listerModel is a session whose provider can enumerate its endpoint, with no
// curated catalog — the openai-compatible case the picker could not serve.
func listerModel(t *testing.T, fn func(context.Context) ([]string, error)) Model {
	t.Helper()
	return readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "llama3").
		WithModelLister(fn)
}

// runBatch executes a command, flattening one level of tea.Batch, and returns
// the messages it produced.
func runBatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		if c != nil {
			out = append(out, c())
		}
	}
	return out
}

func TestModelList_BareModelQueriesTheProvider(t *testing.T) {
	called := 0
	m := listerModel(t, func(context.Context) ([]string, error) {
		called++
		return []string{"llama3", "qwen3:8b"}, nil
	})

	m.input.SetValue("/model")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateModelList {
		t.Fatalf("bare /model should query the provider first, got state %v", m.state)
	}

	var listed tea.Msg
	for _, msg := range runBatch(cmd) {
		if _, ok := msg.(modelListMsg); ok {
			listed = msg
		}
	}
	if listed == nil {
		t.Fatal("the query should produce a modelListMsg")
	}
	if called != 1 {
		t.Fatalf("expected one query, got %d", called)
	}

	updated, _ = m.Update(listed)
	m = updated.(Model)
	if m.state != statePick || m.picker == nil {
		t.Fatalf("the discovered models should open the picker, got state %v", m.state)
	}
	if len(m.picker.Options) != 2 {
		t.Fatalf("expected both discovered models, got %d", len(m.picker.Options))
	}
}

func TestModelList_QueriedOncePerSession(t *testing.T) {
	called := 0
	m := listerModel(t, func(context.Context) ([]string, error) {
		called++
		return []string{"llama3", "qwen3:8b"}, nil
	})

	m.input.SetValue("/model")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	runBatch(cmd)
	updated, _ = m.Update(modelListMsg{names: []string{"llama3", "qwen3:8b"}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	m.input.SetValue("/model")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.state != statePick {
		t.Fatalf("the cached list should open the picker directly, got state %v", m.state)
	}
	if called != 1 {
		t.Fatalf("the endpoint should be queried once per session, got %d", called)
	}
}

func TestModelList_ErrorFallsBackToUsageText(t *testing.T) {
	m := listerModel(t, func(context.Context) ([]string, error) {
		return nil, errors.New("connection refused")
	})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(modelListMsg{err: errors.New("connection refused")})
	m = updated.(Model)

	if m.state != stateInput || m.picker != nil {
		t.Fatalf("a failed query should return to the input, got state %v", m.state)
	}
	texts := []string{
		m.transcript[len(m.transcript)-2].text,
		m.transcript[len(m.transcript)-1].text,
	}
	if !strings.Contains(texts[0], "Could not list models: connection refused") {
		t.Fatalf("the failure should be reported, got %q", texts[0])
	}
	if !strings.Contains(texts[1], "Current model: llama3") {
		t.Fatalf("expected the usage text fallback, got %q", texts[1])
	}
}

func TestModelList_ErrorKeepsCuratedCatalog(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "gpt-4o").
		WithModelOptions([]string{"gpt-4o", "o3"}).
		WithModelLister(func(context.Context) ([]string, error) {
			return nil, errors.New("timeout")
		})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(modelListMsg{err: errors.New("timeout")})
	m = updated.(Model)

	if m.state != statePick || m.picker == nil {
		t.Fatalf("the curated catalog should still open the picker, got state %v", m.state)
	}
	if len(m.picker.Options) != 2 {
		t.Fatalf("expected the curated entries, got %d", len(m.picker.Options))
	}
}

func TestModelList_EmptyEndpointReportsIt(t *testing.T) {
	m := listerModel(t, func(context.Context) ([]string, error) { return nil, nil })

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(modelListMsg{})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatalf("an empty list has nothing to pick, got state %v", m.state)
	}
	if !strings.Contains(m.transcript[len(m.transcript)-2].text, "no models") {
		t.Fatalf("the empty result should be reported, got %q", m.transcript[len(m.transcript)-2].text)
	}
}

func TestModelList_EscCancelsTheQuery(t *testing.T) {
	m := listerModel(t, func(ctx context.Context) ([]string, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatalf("esc should abandon the query, got state %v", m.state)
	}
	// A late result from the abandoned query must not open a picker.
	updated, _ = m.Update(modelListMsg{names: []string{"a", "b"}})
	m = updated.(Model)
	if m.state != stateInput || m.picker != nil {
		t.Fatal("a late result should be ignored")
	}
}

func TestModelList_WithoutASwitcherStaysOnText(t *testing.T) {
	m := readyModel(t).
		WithPricing(nil, "llama3").
		WithModelLister(func(context.Context) ([]string, error) {
			t.Fatal("a session that cannot switch models should not query")
			return nil, nil
		})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("expected the text path, got state %v", m.state)
	}
}

func TestModelList_RendersSpinnerWhileQuerying(t *testing.T) {
	m := listerModel(t, func(ctx context.Context) ([]string, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(m.View(), "Listing models…") {
		t.Fatal("the query should show its own status line")
	}
}
