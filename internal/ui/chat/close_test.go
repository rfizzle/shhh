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
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/provider"
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
	if !strings.Contains(view, "∗ worked") || !strings.Contains(view, "s") {
		t.Fatalf("the row should state the outcome and the elapsed time, got %q", view)
	}
}

// A command is assumed to write, so a turn that ran one and changed no file
// answers that on its close rather than falling silent on it
// (docs/interface/surfaces.md#the-turns-close). A turn that only read has no
// such question, and a command that never ran raises none either.
func TestTurnClose_ACommandThatWroteNothingSaysSo(t *testing.T) {
	remote := MCP{
		Has:      func(name string) bool { return strings.HasPrefix(name, "docs__") },
		ReadOnly: func(name string) bool { return name == "docs__search" },
	}
	cases := []struct {
		name string
		row  entry
		want bool
	}{
		{"a command that ran", entry{kind: entryCommand, text: "go generate ./..."}, true},
		{"a server call nobody vouched for", entry{kind: entryTool, toolName: "docs__publish"}, true},
		{"a read", entry{kind: entryTool, toolName: "read_file"}, false},
		{"a read-only server's call", entry{kind: entryTool, toolName: "docs__search"}, false},
		{"a refused command", entry{kind: entryTool, toolName: "execute_command", deniedBy: decidedByYou}, false},
		{"a dry run the reader asked for", entry{kind: entryCommand, text: "rm -rf build", localRun: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sendText(t, readyModel(t).WithMCP(remote), "tidy the build")
			m.appendEntry(tc.row)
			m = finishTurn(t, m)

			c := lastClose(t, m)
			if c.Changes != nil {
				t.Fatalf("nothing was written, so there are no files to state: %+v", c.Changes)
			}
			if c.WroteNothing != tc.want {
				t.Fatalf("wrote nothing = %v, want %v", c.WroteNothing, tc.want)
			}
			view := plainView(c, 80)
			if got := strings.Contains(view, "changed no files"); got != tc.want {
				t.Fatalf("the close should say changed no files: %v, got:\n%s", tc.want, view)
			}
			// No offers: there is nothing to review, keep or take back.
			if strings.Contains(view, "[") {
				t.Fatalf("a close with no changeset offers nothing, got:\n%s", view)
			}
		})
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
	// Its offers are drawn once the row is selected (inertkeys.go).
	view := ansi.Strip(m.closeFor(*c, rowUnderCursor).View(100))
	for _, want := range []string{"1 file changed", "+1", "−0", "[enter] review turn", "[ctrl+space/ctrl+y] commit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the changeset row should state %q, got:\n%s", want, view)
		}
	}
	// The temp dir is not a repository, and unknown is not untracked; and
	// taking the turn back is a command, which the note names.
	if c.Changes.Note != "not a git repository" || c.Changes.Back != "/undo 1 takes it back" {
		t.Fatalf("outside a repository the tracking note should say so, and name /undo, got %q and %q",
			c.Changes.Note, c.Changes.Back)
	}
	if strings.Contains(view, "[u]") || strings.Contains(view, "[g]") {
		t.Fatalf("the row offers no letter for undo or commit any more, got:\n%s", view)
	}
}

// A turn that committed names the sha where the reader is already looking,
// says what undo does not reach, and stops naming /undo: undo puts files back
// out of the session's own records and never touches history, so naming it
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
	for _, want := range []string{receipt, components.CommitUndoNote, "[enter] review turn"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the close should state %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "takes it back") || strings.Contains(view, "] commit") {
		t.Fatalf("a committed changeset names no undo and offers no second commit, got:\n%s", view)
	}
}

// turnChangesRowFor is the changed-files row a test states directly, so the
// assertion above is about the row's shape rather than about a changeset
// store it had to fill first.
func turnChangesRowFor(files, added, removed int, committed bool) *components.TurnChanges {
	// Enter's own act leads, as it does on the selected row.
	offers := []components.TurnKey{reviewTurnOffer()}
	if !committed {
		offers = append(offers, commitOffer())
	}
	return &components.TurnChanges{Files: files, Added: added, Removed: removed, Keys: offers}
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
	if !strings.Contains(plainView(c, 100), "cancelled") {
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
	if !strings.Contains(plainView(c, 80), "failed") {
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
	if c.Label != "quality gate default" || !strings.Contains(c.Counts, "4 of 4 checks") {
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
			toolResult: "Quality gate \"default\": FAIL — 1/2 checks passed (1s)"},
	}, false)
	if c == nil || !c.Failed {
		t.Fatalf("one failure among three makes the verdict failing, got %+v", c)
	}
	if c.Counts != "1 of 3 passing" {
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
	c.Note = "round 3 of 25"

	wide := plainView(c, 110)
	if !strings.Contains(wide, "round 3 of 25") || !strings.Contains(wide, "not a git repository") {
		t.Fatalf("a wide terminal keeps the notes, got:\n%s", wide)
	}
	const narrowWidth = 30
	narrow := plainView(c, narrowWidth)
	if strings.Contains(narrow, "round 3 of 25") || strings.Contains(narrow, "not a git repository") {
		t.Fatalf("the notes drop before the statement does, got:\n%s", narrow)
	}
	if !strings.Contains(narrow, "worked") || !strings.Contains(narrow, "1 file changed") {
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
	if !strings.Contains(ansi.Strip(m.panelView()), "[enter] review turn") {
		t.Fatalf("the hint should say what enter does on the row, got %q", ansi.Strip(m.panelView()))
	}

	// Enter opens review mode over the turn's changeset; the surface
	// names the turn it is reviewing.
	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	review := updated.(Model)
	if review.state != stateReview || review.review == nil {
		t.Fatalf("enter should open what the turn changed, got state %v", review.state)
	}
	if review.review.Title != "turn 1" {
		t.Fatalf("the surface should name the turn it is reviewing, got %q", review.review.Title)
	}

	// [u] on the close is reading mode's half page and nothing more: taking
	// the turn back is /undo, and the row offers no letter for it.
	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if undo := updated.(Model); undo.state == stateUndoConfirm || undo.undoAsk != nil {
		t.Fatalf("[u] on a close should not arm an undo any more, got state %v", undo.state)
	}
}

func TestTurnClose_IsAnOrdinaryTranscriptEntry(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	m = finishTurn(t, m)

	wide := ansi.Strip(m.renderHistory())
	if !strings.Contains(wide, "∗ worked") || !strings.Contains(wide, "1 file changed") {
		t.Fatalf("the close rows render in the feed:\n%s", wide)
	}

	// A resize re-renders them from the stored counts like every other entry.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 46, Height: 30})
	m = updated.(Model)
	narrow := ansi.Strip(m.renderHistory())
	if !strings.Contains(narrow, "worked") || !strings.Contains(narrow, "1 file changed") {
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
	// And what is still waiting on the notes screen rides beside the count,
	// because the notebook has not been opened in this session: turn 4's
	// three are unread as well as this one (notes.go).
	_, _, _ = m.notebook.Write("writer-1", "The patch is in loop.go", "one hunk")
	if got := m.turnNotesClause(); got != "1 note from writer-1 · 4 unread" {
		t.Fatalf("the close said %q", got)
	}

	// The row is a reading, not an act: no rail, and nothing in the glyph
	// column either.
	view := plainView(&components.TurnClose{
		State: components.TurnDone, Notes: "2 notes from reviewer-1",
	}, 80)
	line := strings.Split(view, "\n")[1]
	// The pointer column, then the rail and glyph columns blank
	// (docs/interface/surfaces.md#the-leading-columns).
	if !strings.HasPrefix(line, "    2 notes from reviewer-1") {
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

// The summary fold beside a turn's close is a fold, not the turn: selected
// and opened it expands in place, and only the row that states what the turn
// changed opens the turn's review. The two sat side by side each offering a
// key, and which of them a key reached was a question the screen did not
// answer.
func TestTurnClose_ASummaryFoldBesideItExpandsRatherThanReviews(t *testing.T) {
	m, _ := undoModel(t)
	m.appendEntry(summaryRowEntry(agent.SummaryVerdict{
		Text: "Capped the rounds and nothing else.", State: agent.SummaryOnTarget, Round: 3}, ""))
	at := len(m.transcript) - 1
	m.pointer, m.focusIdx = true, at
	m.refreshCursorView()

	if _, ok := m.reviewableRow(at); ok {
		t.Fatal("a summary fold names no turn to review")
	}
	updated, _ := m.openCursorRow(m.state)
	opened := updated.(Model)
	if opened.state == stateReview {
		t.Fatal("enter on the summary fold opened a review")
	}
	if !opened.transcript[at].expanded {
		t.Fatal("enter on the summary fold should expand it")
	}

	// And the close beside it is what opens the review.
	closeAt := indexOfKind(t, m, entryTurnClose)
	m.focusIdx = closeAt
	updated, _ = m.openCursorRow(m.state)
	if r := updated.(Model); r.state != stateReview || r.reviewTurnN != m.transcript[closeAt].turn {
		t.Fatalf("enter on the close should review its own turn, got state %v turn %d",
			r.state, r.reviewTurnN)
	}
}

// A running turn draws no total line: the frame's status and the cockpit
// already count it. The turn's last line appears once it ends, as the
// close's first row, with the files it changed on the line under it
// (docs/interface/surfaces.md#the-turns-close).
func TestClose_NoTotalWhileTheTurnRuns(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")

	for _, tc := range []struct {
		name string
		st   state
		cmd  bool
	}{
		{"streaming", stateStreaming, false},
		{"classifying", stateClassifying, false},
		{"running a command", stateRunningCmd, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := m
			r.setTurnState(tc.st)
			if tc.cmd {
				r.approval.request = &approvalRequest{kind: approvalExec}
				r.runningCommand = "go test ./..."
				r.runStart = clock()
				r.runTail = &commandTail{}
				r.runTail.Set("ok  ./internal/ui")
			}
			r.invalidateRenderCache()
			screen := ansi.Strip(r.renderHistory() + "\n" + r.resolveLiveTail(r.paneWidth()))
			if strings.Contains(screen, "working") || strings.Contains(screen, "1 tool") {
				t.Fatalf("a running turn draws a total line:\n%s", screen)
			}
			// The live tail ends on the running card: the card holds the
			// command's last line, and nothing is drawn under it.
			if tc.cmd {
				if tail := r.resolveLiveTail(r.paneWidth()); tail != "" {
					t.Fatalf("something is drawn under the running card:\n%s", ansi.Strip(tail))
				}
				if !strings.Contains(screen, "ok  ./internal/ui") {
					t.Fatalf("the running card does not hold the command's last line:\n%s", screen)
				}
			}
		})
	}

	m = finishTurn(t, m)
	if live := m.resolveLiveTail(m.paneWidth()); strings.Contains(ansi.Strip(live), "working") {
		t.Fatalf("a finished turn draws no running total: %q", live)
	}
	rows := strings.Split(plainView(lastClose(t, m), 100), "\n")
	if !strings.HasPrefix(rows[0], "  ∗ worked ") || !strings.Contains(rows[0], "1 tool") || !strings.Contains(rows[0], " · done ") {
		t.Fatalf("the close leads with the total, done and when, got %q", rows[0])
	}
	if len(rows) < 2 || !strings.HasPrefix(rows[1], " ▎✎ 1 file changed") || !strings.Contains(rows[1], "/undo") {
		t.Fatalf("the changed-files line is the second line, its way back in its words:\n%s", strings.Join(rows, "\n"))
	}
	if strings.Contains(rows[1], "[enter]") {
		t.Fatalf("nothing selects the close, so it offers nothing:\n%s", strings.Join(rows, "\n"))
	}
}

// A turn that broke says so on its total, with what it got through and that
// it left the files as they were, and says the last of those once.
func TestClose_AFailedTurnSaysNoFilesChanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		ran  bool
	}{
		{"a turn that only asked", false},
		{"a turn that ran a command first", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sendText(t, readyModel(t), "do it")
			if tc.ran {
				m.appendEntry(entry{kind: entryCommand, text: "touch notes.txt", toolResult: "ok"})
			}
			updated, _ := m.Update(streamErrMsg{err: errors.New("upstream refused the request")})
			m = updated.(Model)
			view := plainView(lastClose(t, m), 100)
			if !strings.HasPrefix(view, "  ✗ failed · ") || !strings.Contains(view, "no files changed") {
				t.Fatalf("a failed turn's total says so and what it left, got:\n%s", view)
			}
			if strings.Contains(view, "changed no files") || strings.Contains(view, "\n") {
				t.Fatalf("the total already says no file changed, so nothing under it says it again:\n%s", view)
			}
		})
	}
}

// A retry is a turn of its own: its total counts the calls, the time and
// the spend since the retry began, and the attempt that failed keeps its own
// total above the retry's line (docs/interface/surfaces.md#the-turns-close).
func TestClose_ARetriedTurnCountsOnlyTheRetry(t *testing.T) {
	now := time.Date(2026, 9, 4, 13, 37, 0, 0, time.UTC)
	was := clock
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = was })
	calls := func(m Model, n int) Model {
		for range n {
			m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"loop.go"}`, toolResult: "package agent"})
		}
		return m
	}

	m := sendText(t, readyModel(t), "raise the round cap")
	m = calls(m, 13)
	m.vitals.record("gpt-4o", provider.Usage{PromptTokens: 41000, CompletionTokens: 1200}, 1.15, true)
	now = now.Add(98 * time.Second)
	updated, _ := m.Update(streamErrMsg{err: &provider.Failure{Class: provider.ClassUnclassified, Provider: "openai", Message: "stream reset"}})
	m = updated.(Model)
	failed := lastClose(t, m)

	now = now.Add(22 * time.Second)
	next, _ := m.retryTurn()
	m = calls(next.(Model), 9)
	m.vitals.record("gpt-4o", provider.Usage{PromptTokens: 30000, CompletionTokens: 900}, 0.71, true)
	now = now.Add(2*time.Minute + 4*time.Second)
	m = finishTurn(t, m)
	retried := lastClose(t, m)

	var rows []string
	for _, e := range m.transcript {
		switch e.kind {
		case entryTurnClose:
			rows = append(rows, strings.TrimSpace(plainView(e.close, 110)))
		case entryRetry:
			rows = append(rows, strings.TrimSpace(ansi.Strip(components.RetryLine{NewModel: e.failModel}.View(110))))
		}
	}
	want := []string{
		"✗ failed · 13 tools · 1m 38s · $1.15 · no files changed",
		"↻ try again · same prompt, same model",
		"∗ worked 2m 04s · 9 tools · $0.71 · done 1:41 PM",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the turn's lines read\n%s\nwant\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	if failed.Tools != 13 || retried.Tools != 9 {
		t.Errorf("the failed attempt counted %d tools and the retry %d, want 13 and 9", failed.Tools, retried.Tools)
	}
}

// The rail's THIS TURN block counts a retried turn the way its total does:
// the calls since the retry began, while the retry runs and once it is done
// (docs/interface/surfaces.md#the-inspector-rail).
func TestRail_ThisTurnCountsOnlyTheRetry(t *testing.T) {
	now := time.Date(2026, 9, 4, 13, 37, 0, 0, time.UTC)
	was := clock
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = was })
	calls := func(m Model, n int) Model {
		for range n {
			m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"loop.go"}`, toolResult: "package agent"})
		}
		return m
	}
	block := func(m Model) string {
		rail := components.InspectorRail{Turn: m.inspectorTurn(nil)}
		return strings.TrimSpace(ansi.Strip(rail.View(components.InspectorWidth, 0)))
	}

	m := calls(sendText(t, readyModel(t), "raise the round cap"), 13)
	now = now.Add(98 * time.Second)
	updated, _ := m.Update(streamErrMsg{err: &provider.Failure{Class: provider.ClassUnclassified, Provider: "openai", Message: "stream reset"}})
	m = updated.(Model)
	now = now.Add(22 * time.Second)
	next, _ := m.retryTurn()
	m = calls(next.(Model), 9)
	running := block(m)
	now = now.Add(2*time.Minute + 4*time.Second)
	done := block(finishTurn(t, m))

	tests := []struct{ name, got string }{{"running", running}, {"done", done}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(tt.got, "\n")
			if len(lines) != 2 || strings.TrimSpace(lines[0]) != "THIS TURN" ||
				strings.TrimSpace(lines[1]) != "0 files this turn · 9 tools" {
				t.Fatalf("THIS TURN reads\n%s\nwant the retry's own 9 tools", tt.got)
			}
		})
	}
}
