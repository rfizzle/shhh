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
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/structural"
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

// A turn that committed names the sha where the reader is already looking,
// says what undo does not reach, and stops offering [u]: undo puts files back
// out of the session's own records and never touches history, so offering it
// beside a commit would read as an offer to take the commit back.
func TestTurnClose_ACommittedTurnNamesTheShaAndDropsTheUndoOffer(t *testing.T) {
	receipt := "committed 3 files as a41f2c9 on master"
	commit := entry{kind: entryTool, toolName: structural.GitWriteToolName,
		toolArgs: `{"verb":"commit","message":"feat(agent): cap rounds"}`, toolResult: receipt}

	c := turnCommitRow([]entry{commit})
	if c == nil || c.Receipt != receipt {
		t.Fatalf("the commit row should carry the receipt, got %+v", c)
	}
	// The last commit of a turn is the one HEAD is standing on.
	second := commit
	second.toolResult = "committed 1 file as b52d3e0 on master"
	if c := turnCommitRow([]entry{commit, second}); c == nil || c.Receipt != second.toolResult {
		t.Fatalf("the close should name the turn's last commit, got %+v", c)
	}
	// A staging is not a commit, and a refused commit did not happen.
	for _, e := range []entry{
		{kind: entryTool, toolName: structural.GitWriteToolName,
			toolArgs: `{"verb":"add","paths":["a.go"]}`, toolResult: "staged 1 file"},
		{kind: entryTool, toolName: structural.GitWriteToolName,
			toolArgs: `{"verb":"commit","message":"x"}`, toolResult: "error: nothing is staged"},
	} {
		if c := turnCommitRow([]entry{e}); c != nil {
			t.Fatalf("%s should leave no commit row, got %+v", e.toolResult, c)
		}
	}

	view := plainView(&components.TurnClose{
		State:   components.TurnDone,
		Changes: turnChangesRowFor(3, 30, 4, true),
		Commit:  &components.TurnCommit{Receipt: receipt},
	}, 110)
	for _, want := range []string{receipt, components.CommitUndoNote, "[v] review"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the close should state %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "undo turn") {
		t.Fatalf("a committed changeset offers no undo key, got:\n%s", view)
	}
}

// turnChangesRowFor is the changed-files row a test states directly, so the
// assertion above is about the row's shape rather than about a changeset
// store it had to fill first.
func turnChangesRowFor(files, added, removed int, committed bool) *components.TurnChanges {
	keys := []components.TurnKey{{Key: "[v]", Label: "review"}}
	if !committed {
		keys = append(keys, components.TurnKey{Key: "[u]", Label: "undo turn"})
	}
	return &components.TurnChanges{Files: files, Added: added, Removed: removed, Keys: keys}
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
	c := turnChecksRow([]entry{{kind: entryTool, toolName: quality.ToolName, toolResult: pass}}, false)
	if c == nil || c.Failed {
		t.Fatalf("a clean gate run is a passing verdict, got %+v", c)
	}
	if c.Label != "quality gate default" || !strings.Contains(c.Counts, "4/4 checks") {
		t.Fatalf("the row should name the suite and its tally, got %+v", c)
	}

	stale := pass + "\nSTALE: the tree has changed since this run"
	c = turnChecksRow([]entry{{kind: entryTool, toolName: quality.ToolName, toolResult: stale}}, false)
	if c == nil || !c.Failed || !strings.Contains(c.Counts, "stale") {
		t.Fatalf("a stale pass is not a pass, got %+v", c)
	}
}

func TestTurnChecksRow_ReadsATestCommandsExitCode(t *testing.T) {
	c := turnChecksRow([]entry{{kind: entryCommand, text: "go test ./internal/agent/...",
		exitCode: 1, duration: 12800 * time.Millisecond}}, false)
	if c == nil || !c.Failed {
		t.Fatalf("a test command that exited non-zero is a failing verdict, got %+v", c)
	}
	if !strings.Contains(c.Counts, "exit 1") {
		t.Fatalf("the row should carry the exit code, got %+v", c)
	}

	if got := turnChecksRow([]entry{{kind: entryCommand, text: "ls -la"}}, false); got != nil {
		t.Fatalf("an ordinary command is not a verdict about the code, got %+v", got)
	}
}

func TestTurnChecksRow_SeveralRunsCollapseToOneTally(t *testing.T) {
	c := turnChecksRow([]entry{
		{kind: entryCommand, text: "go test ./internal/agent/..."},
		{kind: entryCommand, text: "go test ./internal/ui/...", exitCode: 1},
		{kind: entryTool, toolName: quality.ToolName,
			toolResult: "Quality gate \"default\": PASS — 2/2 checks passed (1s)"},
	}, false)
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
		t.Fatalf("ctrl+o should enter focus mode, got %v", m.state)
	}
	if _, ok := m.focusedClose(); !ok {
		t.Fatalf("focus should land on the close rows, but idx %d is kind %v",
			m.focusIdx, m.transcript[m.focusIdx].kind)
	}
	if !strings.Contains(ansi.Strip(m.panelView()), "[v] review") {
		t.Fatalf("the hint should offer what the row offers, got %q", ansi.Strip(m.panelView()))
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
	if prompt := ansi.Strip(undo.panelView()); !strings.Contains(prompt, "Undo turn 1?") {
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

// A child's report reaches the model and never the screen. Its notes are
// the other half — what a sibling will need — and without a line at the
// turn's close the person would not know anything had been written until
// they typed /notes.
func TestTurnClose_TheCloseNamesWhatTheChildrenWroteDown(t *testing.T) {
	m := turnModel(t)
	m = m.WithNotebook(notebook.New(nil))
	m.turnCount = 4
	m.notebook.SetTurn(4)

	if got := m.turnNotesClause(); got != "" {
		t.Fatalf("a turn nobody wrote in reports %q", got)
	}
	// The session's own notes are not a fan-out's findings: the rows that
	// wrote them are already in front of the person.
	_, _, _ = m.notebook.Write(notebook.Orchestrator, "Mine", "what I worked out")
	if got := m.turnNotesClause(); got != "" {
		t.Fatalf("the orchestrator's own note was counted as a child's: %q", got)
	}

	_, _, _ = m.notebook.Write("reviewer-1", "The gate reads the deny list", "policy.Decide")
	_, _, _ = m.notebook.Write("reviewer-1", "And the write tier", "mode.go")
	if got := m.turnNotesClause(); got != "2 notes from reviewer-1" {
		t.Fatalf("the close said %q", got)
	}
	_, _, _ = m.notebook.Write("researcher-1", "Where the goldens live", "testdata/golden")
	if got := m.turnNotesClause(); got != "3 notes from reviewer-1, researcher-1" {
		t.Fatalf("the close said %q", got)
	}

	// A later turn reports its own fan-out, not the one before it.
	m.turnCount = 5
	m.notebook.SetTurn(5)
	if got := m.turnNotesClause(); got != "" {
		t.Fatalf("turn 5 claimed turn 4's notes: %q", got)
	}
	_, _, _ = m.notebook.Write("writer-1", "The patch is in loop.go", "one hunk")
	if got := m.turnNotesClause(); got != "1 note from writer-1" {
		t.Fatalf("the close said %q", got)
	}

	// The row is a reading, not an act: no rail, and nothing in the glyph
	// column either.
	view := plainView(&components.TurnClose{
		State: components.TurnDone, Notes: "2 notes from reviewer-1",
	}, 80)
	line := strings.Split(view, "\n")[1]
	if !strings.HasPrefix(line, "   2 notes from reviewer-1") {
		t.Fatalf("the notes row carries a rail or a glyph: %q", line)
	}
	// And the notification, which has no glyphs at all, says it too.
	if s := (&components.TurnClose{State: components.TurnDone, Notes: "2 notes from reviewer-1"}).Summary(); !strings.Contains(s, "2 notes from reviewer-1") {
		t.Fatalf("the summary dropped the notes: %q", s)
	}
}

// The turn a note carries is the turn the surface was on when it was
// written, and the notebook is told at the same moment the counter moves.
func TestTurnClose_TheNotebookIsToldWhichTurnIsOpen(t *testing.T) {
	m := sendText(t, readyModel(t).WithNotebook(notebook.New(nil)), "have a look")
	n, _, err := m.notebook.Write("researcher-1", "Found it", "in loop.go")
	if err != nil {
		t.Fatal(err)
	}
	if n.Turn != m.turnCount {
		t.Fatalf("a note written during turn %d was stamped %d", m.turnCount, n.Turn)
	}
}
