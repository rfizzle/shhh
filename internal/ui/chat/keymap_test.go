package chat

// The realigned keymap: the readline chords reach the textarea, the palette
// answers the chord Crush and OpenCode taught, esc esc opens rewind, ctrl+r
// searches the ring, and `?` on an empty draft prints the keys.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
)

var (
	ctrlA = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	ctrlE = tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}
	ctrlK = tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	ctrlU = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	ctrlP = tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	ctrlR = tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

func TestDraft_ReadlineChordsReachTheTextarea(t *testing.T) {
	m := typeChars(t, readyModel(t), "hello world")

	m, _ = pressKey(t, m, ctrlA)
	m = typeChars(t, m, "x")
	if got := m.input.Value(); got != "xhello world" {
		t.Errorf("ctrl+a did not move to line start: %q", got)
	}

	m, _ = pressKey(t, m, ctrlA)
	m, _ = pressKey(t, m, ctrlK)
	if got := m.input.Value(); got != "" {
		t.Errorf("ctrl+a then ctrl+k did not kill to end of line: %q", got)
	}

	m = typeChars(t, m, "back again")
	m, _ = pressKey(t, m, ctrlA)
	m, _ = pressKey(t, m, ctrlE)
	m = typeChars(t, m, "!")
	if got := m.input.Value(); got != "back again!" {
		t.Errorf("ctrl+e did not move to line end: %q", got)
	}

	m, _ = pressKey(t, m, ctrlU)
	if got := m.input.Value(); got != "" {
		t.Errorf("ctrl+u did not kill to start of line: %q", got)
	}

	m = typeChars(t, m, "two words")
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if got := m.input.Value(); got != "two " {
		t.Errorf("ctrl+w did not delete the previous word: %q", got)
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt})
	m = typeChars(t, m, "x")
	if got := m.input.Value(); got != "xtwo " {
		t.Errorf("alt+b did not move back a word: %q", got)
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt})
	m = typeChars(t, m, "!")
	if got := m.input.Value(); got != "xtwo! " {
		t.Errorf("alt+f did not move forward a word: %q", got)
	}
}

func TestPalette_OpensOnItsChordAndTheOldOneIsTheTextareas(t *testing.T) {
	m, _ := pressKey(t, readyModel(t), ctrlP)
	if m.palette == nil {
		t.Fatal("ctrl+p did not open the palette")
	}

	m2, _ := pressKey(t, readyModel(t), ctrlK)
	if m2.palette != nil {
		t.Fatal("ctrl+k still opens the palette; it belongs to the textarea now")
	}
}

func TestRewind_DoubleEscOnAnEmptyIdleDraftOpensThePicker(t *testing.T) {
	m := readyModel(t)
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}

	m, _ = pressKey(t, m, escK)
	if m.state != stateInput {
		t.Fatal("a single esc opened a surface")
	}
	m, _ = pressKey(t, m, escK)
	if m.state != stateRewindPick {
		t.Fatal("esc esc on an empty idle draft did not open the rewind picker")
	}

	before := len(m.transcript)
	m, _ = pressKey(t, m, escK)
	if m.state != stateInput {
		t.Fatal("esc did not close the picker")
	}
	if len(m.transcript) != before {
		t.Fatal("closing the picker changed the history")
	}
}

func TestRewind_TheGestureWindowExpires(t *testing.T) {
	m := readyModel(t)
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}

	m, _ = pressKey(t, m, escK)
	m.armed.deadline = time.Now().Add(-time.Millisecond)
	m, _ = pressKey(t, m, escK)
	if m.state == stateRewindPick {
		t.Fatal("a second esc after the window shut still opened the picker")
	}
}

func TestRewind_EscWithADraftClearsItFirst(t *testing.T) {
	m := typeChars(t, readyModel(t), "half a thought")
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}

	m, _ = pressKey(t, m, escK)
	if m.input.Value() != "" || m.state != stateInput {
		t.Fatal("esc with a draft must clear it and nothing else")
	}
	m, _ = pressKey(t, m, escK)
	m, _ = pressKey(t, m, escK)
	if m.state != stateRewindPick {
		t.Fatal("the two presses after the clearing one did not open the picker")
	}
}

func TestRewind_AttachedEscDetachesAndNeverOpensThePicker(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.checkpoints = []checkpoint{{index: 1, preview: "make it fast"}}
	spawnBlockedChild(t, sup)
	m.attach("researcher-1")

	m, _ = pressKey(t, m, escK)
	if m.attachedTo != "" {
		t.Fatal("esc while attached must detach a level")
	}
	m, _ = pressKey(t, m, escK)
	if m.state == stateRewindPick {
		t.Fatal("the esc that detached counted toward the rewind gesture")
	}
}

func TestRewind_NeverWhileATurnStreams(t *testing.T) {
	m := streamingCancelModel(t)
	m, _ = pressKey(t, m, escK)
	m, _ = pressKey(t, m, escK)
	if m.state == stateRewindPick {
		t.Fatal("esc esc while streaming opened rewind; it is the interrupt there")
	}
}

func searchModel(t *testing.T, history ...string) Model {
	t.Helper()
	m := readyModel(t)
	m.inputHistory = append(m.inputHistory, history...)
	m.historyIdx = len(m.inputHistory)
	return m
}

func TestHistorySearch_TypingFiltersAndEscRestores(t *testing.T) {
	m := typeChars(t, searchModel(t, "go test", "go build"), "draft words")

	m, _ = pressKey(t, m, ctrlR)
	if !m.historySearching() {
		t.Fatal("ctrl+r did not open the history search")
	}
	m = typeChars(t, m, "bu")
	if got := m.input.Value(); got != "go build" {
		t.Fatalf("the draft does not show the match: %q", got)
	}

	m, _ = pressKey(t, m, escK)
	if m.historySearching() {
		t.Fatal("esc did not close the search")
	}
	if got := m.input.Value(); got != "draft words" {
		t.Fatalf("esc did not put the draft back: %q", got)
	}
}

func TestHistorySearch_TheChordStepsOlderAndEnterKeeps(t *testing.T) {
	m := searchModel(t, "go build one", "go test", "go build two")

	m, _ = pressKey(t, m, ctrlR)
	m = typeChars(t, m, "build")
	if got := m.input.Value(); got != "go build two" {
		t.Fatalf("the newest match goes first: %q", got)
	}
	m, _ = pressKey(t, m, ctrlR)
	if got := m.input.Value(); got != "go build one" {
		t.Fatalf("ctrl+r again did not step to the older match: %q", got)
	}
	m, _ = pressKey(t, m, ctrlR)
	if got := m.input.Value(); got != "go build one" {
		t.Fatalf("stepping past the oldest match moved somewhere: %q", got)
	}

	m, _ = pressKey(t, m, enter)
	if m.historySearching() {
		t.Fatal("enter did not close the search")
	}
	if got := m.input.Value(); got != "go build one" {
		t.Fatalf("enter did not keep the match in the draft: %q", got)
	}
}

func TestHistorySearch_BackspaceEditsTheQuery(t *testing.T) {
	m := searchModel(t, "go test", "go build")
	m, _ = pressKey(t, m, ctrlR)
	m = typeChars(t, m, "bux")
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := m.input.Value(); got != "go build" {
		t.Fatalf("deleting the typo did not bring the match back: %q", got)
	}
}

func TestHistorySearch_AKeyItDoesNotAnswerHandsTheKeyboardBack(t *testing.T) {
	m := searchModel(t, "go build")
	m, _ = pressKey(t, m, ctrlR)
	m = typeChars(t, m, "bui")
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.historySearching() {
		t.Fatal("a key the search has no meaning for did not close it")
	}
	if got := m.input.Value(); got != "go build" {
		t.Fatalf("leaving the search dropped the match: %q", got)
	}
}

func TestHistorySearch_ClosesWhenADecisionTakesTheKeyboard(t *testing.T) {
	m := gatedModel(t, func(string, json.RawMessage) (string, error) { return "", nil },
		map[string]GatedPreviewFunc{"write_file": writeFilePreview("old\n")})
	m.inputHistory = []string{"go build"}
	m.historyIdx = len(m.inputHistory)
	m, _ = pressKey(t, m, ctrlR)
	m = typeChars(t, m, "zzz") // no match, so the box stays empty
	if !m.historySearching() {
		t.Fatal("fixture: the search did not open")
	}

	// The keyboard has been quiet and the box is empty, so the arriving
	// card holds the keyboard (interrupt.go) — and the search must not sit
	// invisibly on top of it, filtering the card's answer keys.
	m.lastKeypress = time.Now().Add(-2 * draftQuiet)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "c1", Name: "write_file", Arguments: `{"path":"a.go","content":"new\n"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("fixture: the gated call did not open the card, state %d", m.state)
	}

	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.historySearching() {
		t.Fatal("the search stayed open under a card that holds the keyboard")
	}
	if m.state == stateConfirmRun && m.pendingApproval != nil {
		t.Fatal("the card's answer key filtered an invisible query instead of answering")
	}
}

func TestDraft_FreedChordsStayTextKeysUnderALiveSupervisor(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m = typeChars(t, m, "ab")
	m, _ = pressKey(t, m, ctrlA)
	m = typeChars(t, m, "x")
	if got := m.input.Value(); got != "xab" {
		t.Errorf("with a supervisor wired, ctrl+a stopped being line start: %q", got)
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.agentList == nil {
		t.Error("ctrl+b did not open the agent manager")
	}
}

func TestHistorySearch_AnEmptyRingSaysSo(t *testing.T) {
	m, _ := pressKey(t, readyModel(t), ctrlR)
	if m.historySearching() {
		t.Fatal("the search opened over nothing to search")
	}
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "No input history") {
		t.Errorf("the refusal did not say why:\n%s", view)
	}
}

func TestKeyList_QuestionMarkOnAnEmptyDraftPrintsTheKeys(t *testing.T) {
	m, _ := pressKey(t, readyModel(t), tea.KeyPressMsg{Code: '?', Text: "?"})
	if got := m.input.Value(); got != "" {
		t.Fatalf("? on an empty draft landed in the draft: %q", got)
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "Keys:") || !strings.Contains(view, "reading mode") {
		t.Errorf("? did not print the key section as a system row:\n%s", view)
	}
}

func TestKeysNotice_RidesTheNoticeRailWhenDue(t *testing.T) {
	m := frameModel(t, 130, 40).WithKeysNotice(KeysChangedNotice())
	line := stripANSI(m.noticeLine())
	if !strings.Contains(line, "keys changed:") {
		t.Errorf("the rebind notice is not on the rail: %q", line)
	}
	for _, want := range []string{"reading", "palette", "agents", "history search"} {
		if !strings.Contains(line, want) {
			t.Errorf("the notice does not name the %s rebind: %q", want, line)
		}
	}

	if line := stripANSI(frameModel(t, 130, 40).noticeLine()); strings.Contains(line, "keys changed:") {
		t.Errorf("the notice shows without being due: %q", line)
	}
}
