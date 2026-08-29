package chat

// Turn summary and changeset row: a turn ends with what it did, what
// it changed, and whether the checks still pass.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// turnModel is a model sitting at the input, able to apply an approved write.
func turnModel(t *testing.T) Model {
	t.Helper()
	m := gatedModel(t, nil, nil)
	m.state = stateInput
	return m
}

// finishTurn ends the in-flight response, which is what closes the turn.
func finishTurn(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(doneMsg{})
	return updated.(Model)
}

// lastClose returns the close block of the most recently finished turn.
func lastClose(t *testing.T, m Model) *components.TurnClose {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryTurnClose {
			if m.transcript[i].close == nil {
				t.Fatal("a close entry with no data renders nothing")
			}
			return m.transcript[i].close
		}
	}
	t.Fatal("the finished turn appended no close rows")
	return nil
}

func plainView(c *components.TurnClose, width int) string {
	return ansi.Strip(c.View(width))
}

func TestTurnClose_ATurnThatChangedNothingGetsTheSummaryRowOnly(t *testing.T) {
	m := finishTurn(t, sendText(t, readyModel(t), "explain the loop"))

	c := lastClose(t, m)
	if c.State != components.TurnDone {
		t.Fatalf("a turn that ran to completion is done, got %v", c.State)
	}
	if c.Changes != nil {
		t.Fatalf("nothing was written, so there is no changeset row: %+v", c.Changes)
	}
	if c.Checks != nil {
		t.Fatalf("nothing was checked, so there is no verdict row: %+v", c.Checks)
	}
	view := plainView(c, 80)
	if strings.Count(view, "\n") != 0 {
		t.Fatalf("the summary row should stand alone, got:\n%s", view)
	}
	if !strings.Contains(view, "✓ Done") || !strings.Contains(view, "s") {
		t.Fatalf("the row should state the outcome and the elapsed time, got %q", view)
	}
}

func TestTurnClose_TheChangesRowStatesTheFilesAndOffersTheKeys(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	m = finishTurn(t, m)

	c := lastClose(t, m)
	if c.Changes == nil {
		t.Fatal("a turn that wrote a file gets a changeset row")
	}
	if c.Changes.Files != 1 || c.Changes.Added != 1 || c.Changes.Removed != 0 {
		t.Fatalf("expected 1 file +1 −0, got %+v", c.Changes)
	}
	view := plainView(c, 100)
	for _, want := range []string{"1 file changed", "+1", "−0", "[v] review", "[u] undo turn"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the changeset row should state %q, got:\n%s", want, view)
		}
	}
	// The temp dir is not a repository, and unknown is not untracked.
	if c.Changes.Note != "no git here" {
		t.Fatalf("outside a repository the tracking note should say so, got %q", c.Changes.Note)
	}
}

func TestTurnClose_ACancelledTurnSaysSoAndStillReportsWhatItChanged(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	m.setTurnState(stateStreaming)
	m.cancelStreaming()

	c := lastClose(t, m)
	if c.State != components.TurnCancelled {
		t.Fatalf("ctrl+c ends the turn as cancelled, got %v", c.State)
	}
	if !strings.Contains(plainView(c, 100), "Cancelled") {
		t.Fatalf("the row says so in words, not only in colour: %q", plainView(c, 100))
	}
	if c.Changes == nil || c.Changes.Files != 1 {
		t.Fatalf("a cancelled turn still reports what it changed before stopping, got %+v", c.Changes)
	}
}

func TestTurnClose_AFailedTurnSaysSo(t *testing.T) {
	m := sendText(t, readyModel(t), "do it")
	updated, _ := m.Update(streamErrMsg{err: errors.New("upstream refused the request")})
	m = updated.(Model)

	c := lastClose(t, m)
	if c.State != components.TurnFailed {
		t.Fatalf("a turn whose stream broke is failed, got %v", c.State)
	}
	if !strings.Contains(plainView(c, 80), "Failed") {
		t.Fatalf("the row says so in words: %q", plainView(c, 80))
	}
}

func TestTurnClose_OneBlockPerTurn(t *testing.T) {
	m := finishTurn(t, sendText(t, readyModel(t), "first"))
	m = finishTurn(t, sendText(t, m, "second"))

	var closes []int
	for i, e := range m.transcript {
		if e.kind == entryTurnClose {
			closes = append(closes, i)
		}
	}
	if len(closes) != 2 {
		t.Fatalf("two turns close twice, got %d", len(closes))
	}
	if got := m.transcript[closes[0]].turn; got != 1 {
		t.Fatalf("the first close belongs to turn 1, got %d", got)
	}
	if got := m.transcript[closes[1]].turn; got != 2 {
		t.Fatalf("the second close belongs to turn 2, got %d", got)
	}
}

func TestTurnClose_ARunFinishingIsNotATurnEnding(t *testing.T) {
	m := runCapableModel("```bash\necho hi\n```")
	m = sendText(t, m, "/run")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	updated, _ = m.Update(cmdDoneMsg{runID: m.agent.RunID(), command: "echo hi", output: "hi"})
	m = updated.(Model)

	for _, e := range m.transcript {
		if e.kind == entryTurnClose {
			t.Fatal("a /run the user typed is not a turn, so it closes nothing")
		}
	}
}

func TestTurnChecksRow_ReadsTheQualityGateVerdict(t *testing.T) {
	pass := "Quality gate \"default\": PASS — 4/4 checks passed (12.8s)\nTree: clean"
	c := turnChecksRow([]entry{{kind: entryTool, toolName: quality.ToolName, toolResult: pass}})
	if c == nil || c.Failed {
		t.Fatalf("a clean gate run is a passing verdict, got %+v", c)
	}
	if c.Label != "quality gate default" || !strings.Contains(c.Counts, "4/4 checks") {
		t.Fatalf("the row should name the suite and its tally, got %+v", c)
	}

	stale := pass + "\nSTALE: the tree has changed since this run"
	c = turnChecksRow([]entry{{kind: entryTool, toolName: quality.ToolName, toolResult: stale}})
	if c == nil || !c.Failed || !strings.Contains(c.Counts, "stale") {
		t.Fatalf("a stale pass is not a pass, got %+v", c)
	}
}

func TestTurnChecksRow_ReadsATestCommandsExitCode(t *testing.T) {
	c := turnChecksRow([]entry{{kind: entryCommand, text: "go test ./internal/agent/...",
		exitCode: 1, duration: 12800 * time.Millisecond}})
	if c == nil || !c.Failed {
		t.Fatalf("a test command that exited non-zero is a failing verdict, got %+v", c)
	}
	if !strings.Contains(c.Counts, "exit 1") {
		t.Fatalf("the row should carry the exit code, got %+v", c)
	}

	if got := turnChecksRow([]entry{{kind: entryCommand, text: "ls -la"}}); got != nil {
		t.Fatalf("an ordinary command is not a verdict about the code, got %+v", got)
	}
}

func TestTurnChecksRow_SeveralRunsCollapseToOneTally(t *testing.T) {
	c := turnChecksRow([]entry{
		{kind: entryCommand, text: "go test ./internal/agent/..."},
		{kind: entryCommand, text: "go test ./internal/ui/...", exitCode: 1},
		{kind: entryTool, toolName: quality.ToolName,
			toolResult: "Quality gate \"default\": PASS — 2/2 checks passed (1s)"},
	})
	if c == nil || !c.Failed {
		t.Fatalf("one failure among three makes the verdict failing, got %+v", c)
	}
	if c.Counts != "2 of 3 passing" {
		t.Fatalf("the row answers with a tally, got %q", c.Counts)
	}
}

func TestTurnClose_RowsReRenderAtAnyWidth(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	m = finishTurn(t, m)
	c := lastClose(t, m)
	c.Note = "round 3/25"

	wide := plainView(c, 110)
	if !strings.Contains(wide, "round 3/25") || !strings.Contains(wide, "no git here") {
		t.Fatalf("a wide terminal keeps the notes, got:\n%s", wide)
	}
	const narrowWidth = 30
	narrow := plainView(c, narrowWidth)
	if strings.Contains(narrow, "round 3/25") || strings.Contains(narrow, "no git here") {
		t.Fatalf("the notes drop before the statement does, got:\n%s", narrow)
	}
	if !strings.Contains(narrow, "Done") || !strings.Contains(narrow, "1 file changed") {
		t.Fatalf("what the rows state survives the squeeze, got:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if len([]rune(line)) > narrowWidth {
			t.Fatalf("a close row must not overflow its width: %q", line)
		}
	}
}

// A close row offers [v] and [u] and nothing else. The round-limit pause's
// keys are dispatched in the same branch, so they reach this row too — and a
// key a row does not offer has to fall through to the draft, not land on
// whichever of the row's own offers happened to be checked last.
func TestTurnClose_TheRoundPauseKeysAreInertOnACloseRow(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "main.go"), "package main\n", "y")
	m = finishTurn(t, m)

	updated, _ := m.enterFocusMode()
	m = updated.(Model)
	if _, ok := m.focusedClose(); !ok {
		t.Fatalf("focus should land on the close rows, got kind %v", m.transcript[m.focusIdx].kind)
	}
	for _, key := range []string{keys.Shown(keys.Row.Rounds), keys.Shown(keys.Row.Uncap)} {
		next, _ := m.updateFocus(tea.KeyPressMsg{Code: []rune(key)[0], Text: key})
		got := next.(Model)
		if got.state == stateUndoConfirm {
			t.Errorf("%q is not an offer on a close row and must not arm the undo", key)
		}
		if !strings.Contains(got.input.Value(), key) {
			t.Errorf("%q should be a character like any other here, draft = %q", key, got.input.Value())
		}
	}
}

func TestTurnClose_ReachableFromFocusMode(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	m = finishTurn(t, m)

	updated, _ := m.enterFocusMode()
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+e should enter focus mode, got %v", m.state)
	}
	if _, ok := m.focusedClose(); !ok {
		t.Fatalf("focus should land on the close rows, but idx %d is kind %v",
			m.focusIdx, m.transcript[m.focusIdx].kind)
	}
	if !strings.Contains(ansi.Strip(m.renderFocusHint()), "[v] review") {
		t.Fatalf("the hint should offer what the row offers, got %q", ansi.Strip(m.renderFocusHint()))
	}

	// [v] opens review mode over the turn's changeset; the surface
	// names the turn it is reviewing.
	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: []rune(keys.Shown(keys.Row.Review))[0], Text: keys.Shown(keys.Row.Review)})
	review := updated.(Model)
	if review.state != stateReview || review.review == nil {
		t.Fatalf("[v] should open what the turn changed, got state %v", review.state)
	}
	if review.review.Title != "turn 1" {
		t.Fatalf("the surface should name the turn it is reviewing, got %q", review.review.Title)
	}

	// [u] arms the undo confirm over the row that offered it: the
	// prompt borrows the bottom panel and nothing is written until it is
	// answered.
	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: []rune(keys.Shown(keys.Row.Undo))[0], Text: keys.Shown(keys.Row.Undo)})
	undo := updated.(Model)
	if undo.state != stateUndoConfirm || undo.undoAsk == nil {
		t.Fatalf("[u] should ask before it writes, got state %v", undo.state)
	}
	if undo.undoReturn != stateFocus {
		t.Fatalf("esc should come back to the row that offered it, got %v", undo.undoReturn)
	}
	if prompt := ansi.Strip(undo.renderUndoConfirm()); !strings.Contains(prompt, "Undo turn 1?") {
		t.Fatalf("the confirm should name the turn, got %q", prompt)
	}
}

func TestTurnClose_IsAnOrdinaryTranscriptEntry(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	m = finishTurn(t, m)

	wide := ansi.Strip(m.renderHistory())
	if !strings.Contains(wide, "✓ Done") || !strings.Contains(wide, "1 file changed") {
		t.Fatalf("the close rows render in the feed:\n%s", wide)
	}

	// A resize re-renders them from the stored counts like every other entry.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 46, Height: 30})
	m = updated.(Model)
	narrow := ansi.Strip(m.renderHistory())
	if !strings.Contains(narrow, "Done") || !strings.Contains(narrow, "1 file changed") {
		t.Fatalf("the rows should survive a resize:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if len([]rune(line)) > 46 {
			t.Fatalf("a re-rendered row must fit the new width: %q", line)
		}
	}
}
