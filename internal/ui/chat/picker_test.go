package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

func TestModelPick_BareModelOpensPicker(t *testing.T) {
	var switched string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2", "m3"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /model should open the picker")
	}
	if !strings.Contains(m.picker.Options[m.picker.Focus].Label, "m1") {
		t.Fatalf("the current model should be focused, got %q", m.picker.Options[m.picker.Focus].Label)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("selecting should return to input")
	}
	if switched != "m2" || m.modelName != "m2" {
		t.Fatalf("expected switch to m2, got switched=%q modelName=%q", switched, m.modelName)
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "Switched to m2 for this session") {
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /mode should open the picker")
	}
	if !strings.Contains(m.picker.Options[m.picker.Focus].Label, "manual") {
		t.Fatalf("the current mode should be focused, got %q", m.picker.Options[m.picker.Focus].Label)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = updated.(Model)
	if !m.quitting || cmd == nil {
		t.Fatal("ctrl+d in a picker should quit")
	}
}

func TestPick_RendersInBottomPanel(t *testing.T) {
	m := readyModel(t)
	m.input.SetValue("/mode")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	view := m.View().Content
	if !strings.Contains(view, "Permission mode") {
		t.Fatal("the picker card should render in the view")
	}
}

// A catalog longer than the bottom panel scrolls under the pointer rather
// than leaving it below the card. This is the /model picker, but the
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
		// The numbering column is right-aligned, so option 9's
		// label starts where option 10's does.
		want := fmt.Sprintf("%2d. model-%02d", i+1, i+1)
		if !strings.Contains(ansi.Strip(panel), "❯ "+want) {
			t.Fatalf("at option %d the pointer should be on the card:\n%s", i+1, panel)
		}
		if got, budget := strings.Count(panel, "\n")+1, m.bottomPanelHeight(); got != budget {
			t.Fatalf("the panel should stay exactly %d rows, got %d", budget, got)
		}
		if i < len(names)-1 {
			updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			m = updated.(Model)
		}
	}
	// The last option is reachable, which is what the old fixed slice made
	// impossible.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.modelName != "model-20" {
		t.Fatalf("the bottom of the catalog should be selectable, got %q", m.modelName)
	}
}

// --- session pickers ----------------------------------------------

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
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = updated.(Model)
	}
	for m.picker.Focus > target {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	if focused.Label != "beta" || !focused.Dim || focused.Meta != protectedPhrase {
		t.Fatalf("the current chat should be the unavailable row and focused, got %+v", focused)
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
	root := m.sessionName
	focused := m.picker.Options[m.picker.Focus]
	if focused.Label != root {
		t.Fatalf("the label column should be the branch name alone, got %q", focused.Label)
	}
	if focused.Meta != currentBranchPhrase {
		t.Fatalf("the current branch should be marked in the row's short field, got %q", focused.Meta)
	}
	target := 1 - m.picker.Focus
	if !strings.Contains(m.picker.Options[target].Desc, "branch of") {
		t.Fatalf("the branch row should name its parent, got %q", m.picker.Options[target].Desc)
	}
	// The row shows the part of the name its parent does not, and acts on
	// the whole of it.
	label := m.picker.Options[target].Label
	if !strings.HasPrefix(label, "…@turn") {
		t.Fatalf("a cut branch should show the cut, not the ancestry, got %q", label)
	}

	m = focusPick(t, m, target)
	if name := root + strings.TrimPrefix(label, "…"); m.sessionName != name {
		t.Fatalf("expected a switch to %q, got session %q", name, m.sessionName)
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "Switched to branch") {
		t.Fatalf("transcript should note the switch, got %q", last.text)
	}
	if got := len(m.Messages()); got != 5 {
		t.Fatalf("switching to the tail branch should restore all 5 messages, got %d", got)
	}
	// The pre-switch working conversation was saved, not lost.
	kept, err := m.db.LoadChat(root)
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

func TestBranchPick_EscDoesNotSwitch(t *testing.T) {
	m := sendText(t, branchPickModel(t), "/branches")
	before := m.sessionName
	messages := len(m.Messages())

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	if m.state != stateInput || m.picker != nil {
		t.Fatal("esc should close the picker")
	}
	if m.sessionName != before {
		t.Fatalf("esc should not switch, session moved to %q", m.sessionName)
	}
	if got := len(m.Messages()); got != messages {
		t.Fatalf("esc should leave the conversation alone, %d messages became %d", messages, got)
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

// A family's names all start with the session they were cut from, so the row
// shows the cut and lets the shared run stand as the card's elision mark.
// A branch saved under a name of its own is not built on its parent's, and
// keeps all of it.
func TestBranchPick_LabelShowsTheCutNotTheAncestry(t *testing.T) {
	const root = "2026-08-31 09:04:11"
	cut := root + "@turn3-20260831-141002.418"
	cases := []struct{ name, parent, want string }{
		{root, "", root},
		{cut, root, "…@turn3-20260831-141002.418"},
		{cut + "@turn2-20260831-152245.907", cut, "…@turn2-20260831-152245.907"},
		{"release notes", root, "release notes"},
	}
	for _, c := range cases {
		if got := branchLabel(c.name, c.parent); got != c.want {
			t.Errorf("branchLabel(%q, %q) = %q, want %q", c.name, c.parent, got, c.want)
		}
	}
}

// TestGolden_BranchPicker captures the branch picker: one row per branch of
// the family, the row the session is on marked in the short field, and the
// filter row a long generated name is narrowed with.
func TestGolden_BranchPicker(t *testing.T) {
	captureGolden(t, "branch-picker", "the branch picker over a session's family", goldenWidths, func(width int) []golden.Panel {
		const (
			root = "2026-08-31 09:04:11"
			cut  = root + "@turn3-20260831-141002.418"
			tail = cut + "@turn2-20260831-152245.907"
		)
		db := rewindTestDB(t)
		msgs := []provider.Message{
			{Role: provider.RoleUser, Content: "q"},
			{Role: provider.RoleAssistant, Content: "a"},
		}
		if err := db.SaveChat(root, msgs); err != nil {
			t.Fatal(err)
		}
		for _, b := range [][2]string{{root, cut}, {cut, tail}} {
			if err := db.SaveChatBranch(b[0], b[1], msgs); err != nil {
				t.Fatal(err)
			}
		}
		m := frameModel(t, width, 40).WithDB(db)
		m.sessionName = cut

		// A branch is named for the moment it was cut, and the family's turn
		// counts come out of the rewind that made it; the rows are pinned so
		// the capture is the same every run.
		at := time.Date(2026, 8, 31, 9, 4, 0, 0, time.Local)
		fixed := []storage.ChatBranch{
			{Name: root, Turns: 4, UpdatedAt: at},
			{Name: cut, Parent: root, Turns: 6, UpdatedAt: at},
			{Name: tail, Parent: cut, Turns: 2, UpdatedAt: at},
		}
		opened := sendText(t, m, "/branches")
		opts, focus := opened.branchPickOptions(fixed)
		opened.pickerAll, opened.picker.Options, opened.picker.Total = opts, opts, len(opts)
		opened.picker.Focus = focus
		opened.pickerIndex = identityIndex(len(opts))

		// The card is a pointer on the model, so the filtered panel takes a
		// copy of it before typing.
		card := *opened.picker
		filtered := opened
		filtered.picker = &card
		filtered = press(t, filtered, "/")
		for _, r := range "turn2" {
			filtered = press(t, filtered, string(r))
		}

		return []golden.Panel{
			{Label: "the family, focused on the branch the session is on", View: strings.Join(opened.pickerLines(), "\n")},
			{Label: "[/] narrowed to the tail branch", View: strings.Join(filtered.pickerLines(), "\n")},
		}
	})
}

// --- run picker ---------------------------------------------------

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
	if !strings.Contains(m.View().Content, "Approve command") {
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
	if !strings.Contains(m.View().Content, "⚠") {
		t.Fatal("a dangerous picked block should still show its safety warning")
	}
}

func TestRunPick_EscReturnsToInputWithoutRunning(t *testing.T) {
	m := runCapableModel(twoBlockResponse)
	m = sendText(t, m, "/run")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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

// --- live model discovery -----------------------------------------

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
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	runBatch(cmd)
	updated, _ = m.Update(modelListMsg{names: []string{"llama3", "qwen3:8b"}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	m.input.SetValue("/model")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(m.View().Content, "Listing models…") {
		t.Fatal("the query should show its own status line")
	}
}

// --- the filter row over the picker (
// docs/interface/surfaces.md#selectors) -------------

// runes feeds a query into an open picker one keystroke at a time, which is
// how a host learns the query changed at all.
func runes(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(Model)
	}
	return m
}

// / opens the query line, typing narrows the list, and the choice still
// reaches the apply that was written against the whole catalog — a filtered
// index is mapped back before it is spent.
func TestModelPick_FilterNarrowsAndStillSwitchesTheRightModel(t *testing.T) {
	var switched string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithPricing(nil, "gpt-5.2").
		WithModelOptions([]string{"gpt-5.2", "claude-opus-4.6", "claude-sonnet-4.6", "gemini-3-pro"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.picker.Filterable {
		t.Fatal("a picker over a catalog should offer the filter row")
	}

	m = runes(t, m, "/")
	if !m.picker.Filtering {
		t.Fatal("/ should open the query line")
	}
	m = runes(t, m, "sonnet")
	if got := len(m.picker.Options); got != 1 {
		t.Fatalf("one model matches \"sonnet\", the card is showing %d", got)
	}
	if m.picker.Total != 4 {
		t.Fatalf("the row should still name the catalog it filtered, got %d", m.picker.Total)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if switched != "claude-sonnet-4.6" {
		t.Fatalf("the filtered choice should reach the apply intact, got %q", switched)
	}
}

// A digit typed into an open query line is a digit: the model whose name
// carries a 5 must not be switched to halfway through being typed.
func TestModelPick_DigitsAreTextWhileTheQueryLineIsOpen(t *testing.T) {
	var switched string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithPricing(nil, "gpt-5.2").
		WithModelOptions([]string{"gpt-5.2", "gpt-5.1", "o4-mini"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m = runes(t, m, "/gpt-5.1")

	if switched != "" {
		t.Fatalf("nothing should have been chosen while typing, got %q", switched)
	}
	if m.state != statePick {
		t.Fatal("the picker should still be open")
	}
	if m.picker.Query != "gpt-5.1" {
		t.Fatalf("every key should have landed in the query, got %q", m.picker.Query)
	}
}

// ctrl+u puts the whole catalog back, and esc leaves without changing
// anything at all.
func TestModelPick_ClearAndEscape(t *testing.T) {
	var switched string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithPricing(nil, "gpt-5.2").
		WithModelOptions([]string{"gpt-5.2", "claude-opus-4.6", "gemini-3-pro"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m = runes(t, m, "/gemini")
	if len(m.picker.Options) != 1 {
		t.Fatalf("the filter should have narrowed the list, got %d", len(m.picker.Options))
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = updated.(Model)
	if len(m.picker.Options) != 3 || m.picker.Query != "" {
		t.Fatalf("ctrl+u should put the whole catalog back, got %d options and query %q",
			len(m.picker.Options), m.picker.Query)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput || switched != "" || m.picker != nil {
		t.Fatalf("esc should leave the picker changing nothing, state=%v switched=%q", m.state, switched)
	}
}

// A query nothing matched is a card that says so and names the nearest thing
// that does exist, and enter on it does nothing.
func TestModelPick_NoMatchNamesTheClosestModel(t *testing.T) {
	var switched string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithPricing(nil, "gpt-5.2").
		WithModelOptions([]string{"gpt-5.2", "claude-sonnet-4.6", "gemini-3-pro"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m = runes(t, m, "/sonnet-5")

	if len(m.picker.Options) != 0 {
		t.Fatalf("nothing matches \"sonnet-5\", got %d options", len(m.picker.Options))
	}
	if m.picker.Closest != "claude-sonnet-4.6" {
		t.Fatalf("the card should name the closest model there is, got %q", m.picker.Closest)
	}
	view := ansi.Strip(m.picker.View(70))
	for _, want := range []string{`no match for "sonnet-5"`, "closest is claude-sonnet-4.6", "0 of 3 match"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q on the card:\n%s", want, view)
		}
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != statePick || switched != "" {
		t.Fatalf("enter on a card that matched nothing should do nothing, state=%v switched=%q", m.state, switched)
	}
}

// closestOption is the same substring test as the match rule, run on shorter
// and shorter prefixes; a query with nothing in common names nothing rather
// than guessing.
func TestClosestOption(t *testing.T) {
	all := []components.SelectOption{
		{Label: "COMMANDS", Header: true},
		{Label: "claude-sonnet-4.6"},
		{Label: "gpt-5.2"},
	}
	if got := closestOption(all, "sonnet-5"); got != "claude-sonnet-4.6" {
		t.Fatalf("expected the model that shares \"sonnet-\", got %q", got)
	}
	if got := closestOption(all, "zzzz"); got != "" {
		t.Fatalf("nothing is close to %q, so nothing should be named, got %q", "zzzz", got)
	}
}

// The picker is where a model is chosen, so it is where the choice can be
// made to stick: [d] switches the session and writes provider.model, so the
// name just read off a list does not have to be typed back.
func TestModelPick_MakeDefaultSwitchesAndPersists(t *testing.T) {
	var switched string
	var wrote [][2]string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithConfigWriter(func(k, v string) error {
			wrote = append(wrote, [2]string{k, v})
			return nil
		}).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.picker == nil {
		t.Fatal("bare /model should open the picker")
	}
	// Both readings are on the card, and enter's is named once d's is.
	hint := ansi.Strip(m.picker.View(110))
	for _, want := range []string{"enter this session", "d and make it default"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the card should offer %q:\n%s", want, hint)
		}
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	updated, _ = updated.(Model).Update(tea.KeyPressMsg{Code: []rune(keys.Shown(keys.Select.Alt))[0], Text: keys.Shown(keys.Select.Alt)})
	next := updated.(Model)

	if switched != "m2" || next.modelName != "m2" {
		t.Fatalf("[d] switches the session too, got switched=%q modelName=%q", switched, next.modelName)
	}
	if len(wrote) != 1 || wrote[0] != [2]string{"provider.model", "m2"} {
		t.Fatalf("persisted %v, want provider.model=m2", wrote)
	}
	last := next.transcript[len(next.transcript)-1]
	if !strings.Contains(last.text, "m2") || !strings.Contains(last.text, "Default model set") {
		t.Fatalf("the note should say both things it did, got %q", last.text)
	}
}

// A key that cannot be honoured is not offered: a session with nowhere to
// write has no default to set.
func TestModelPick_NoWriterNoDefaultOffer(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2"})

	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.picker.AltKey != "" {
		t.Errorf("no writer means no offer, got %q", m.picker.AltKey)
	}
	if hint := ansi.Strip(m.picker.View(110)); !strings.Contains(hint, "enter select") {
		t.Errorf("enter goes back to its plain label when it is the only one:\n%s", hint)
	}
}

// Writing a default that something else overrules is the one way this can
// succeed and still not work, so the note has to say so.
func TestModelDefault_NamesWhatOutranksIt(t *testing.T) {
	m := New(nil, mockStream).
		WithConfigWriter(func(string, string) error { return nil }).
		WithDefaults(Defaults{Outranked: "SHHH_MODEL is set to gpt-4o"})
	m.modelName = "gpt-4o"

	_, out := m.handleSlashCommand("/model default o3")
	if !strings.Contains(out, "SHHH_MODEL") || !strings.Contains(out, "outranks") {
		t.Fatalf("the note should name what overrules it, got %q", out)
	}
	// And reading the setting back is the same claim, told more quietly.
	_, out = m.handleSlashCommand("/model default")
	if !strings.Contains(out, "Overruled") {
		t.Fatalf("reporting the setting should say it too, got %q", out)
	}
}
