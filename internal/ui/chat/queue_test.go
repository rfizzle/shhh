package chat

// The approval queue strip and the list that answers it. What these hold to
// is that the queue key never answers more than the strip said it would: the
// membership on screen, the rows the list opens with and the calls that run
// without a second prompt are the same set, and a safety-flagged action is in
// none of them. What the list adds is the other half — a row the reader
// unchecked is denied, one at a time, by the path a single card's no takes.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// keyA presses the batch key.
func keyA() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'A', Text: "A"} }

// keyN presses the decline key.
func keyN() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'n', Text: "n"} }

// openQueue presses the queue key and asserts the list came up.
func openQueue(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(keyA())
	m = updated.(Model)
	if m.queueList == nil {
		t.Fatal("the queue key should have opened the queue as a list")
	}
	return m
}

// answerQueue opens the list and confirms it exactly as it opened — every row
// checked, which is the answer the key used to give on its own.
func answerQueue(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := openQueue(t, m).Update(keyEnter)
	return updated.(Model), cmd
}

// execCall is one queued shell command.
func execCall(id, command string) provider.ToolCall {
	return provider.ToolCall{
		ID: id, Name: "execute_command",
		Arguments: fmt.Sprintf(`{"command":%q}`, command),
	}
}

// writeCall is one queued file write.
func writeCall(id, path, content string) provider.ToolCall {
	return provider.ToolCall{
		ID: id, Name: "write_file",
		Arguments: fmt.Sprintf(`{"path":%q,"content":%q}`, path, content),
	}
}

func TestQueueStrip_ShowsPositionAndOrder(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo first"),
		execCall("c2", "echo second"),
		execCall("c3", "echo third"),
	}})
	m = updated.(Model)
	m = handover(t, m)

	lines := m.confirmLines()
	if len(lines) < 4 {
		t.Fatalf("expected a strip above the card, got %d lines", len(lines))
	}
	header := lines[0]
	if !strings.Contains(header, "3 pending") {
		t.Fatalf("strip header should count the queue, got %q", header)
	}
	// The items keep the order they will be asked in, and the current one is
	// the only one marked with the pointer.
	for i, want := range []string{"1 echo first", "2 echo second", "3 echo third"} {
		if !strings.Contains(lines[i+1], want) {
			t.Fatalf("strip row %d should read %q, got %q", i+1, want, lines[i+1])
		}
	}
	if !strings.Contains(lines[1], "▸") {
		t.Fatal("the current item should carry the pointer")
	}
	if strings.Contains(lines[2], "▸") || strings.Contains(lines[3], "▸") {
		t.Fatal("only the current item should carry the pointer")
	}
	// The card says where in the round this decision sits, which the dots —
	// drawn over what is left — cannot.
	if !strings.Contains(strings.Join(lines, "\n"), "(1 of 3)") {
		t.Fatal("the card title should carry the queue position")
	}

	// One decision is not a queue: the strip disappears once the rest drain.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if !strings.Contains(strings.Join(m.confirmLines(), "\n"), "(2 of 3)") {
		t.Fatal("the second decision should be 2 of 3")
	}
}

func TestQueueStrip_SingleDecisionHasNoStrip(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo only"),
	}})
	m = updated.(Model)
	m = handover(t, m)

	view := strings.Join(m.confirmLines(), "\n")
	if strings.Contains(view, "pending") {
		t.Fatal("a single decision should not draw a queue strip")
	}
	if strings.Contains(view, "[y/n/a/A]") {
		t.Fatal("a single decision should not offer the batch key")
	}
	if strings.Contains(view, "(1 of 1)") {
		t.Fatal("a single decision should not carry a queue position")
	}
}

func TestBatch_MembershipSpansOnlyTheSameCategory(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	dir := t.TempDir()

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		writeCall("w1", filepath.Join(dir, "a.txt"), "a\n"),
		execCall("c2", "echo two"),
	}})
	m = updated.(Model)
	m = handover(t, m)

	// The current decision is a command, so the batch is the queued commands
	// and not the queued edit.
	if got := m.pendingBatch; len(got) != 1 || got[0] != "c2" {
		t.Fatalf("batch should hold only the other command, got %v", got)
	}
	view := strings.Join(m.confirmLines(), "\n")
	if !strings.Contains(ansi.Strip(view), "[A] answer 2 like this as a list") {
		t.Fatalf("the key should state how many it answers, got:\n%s", view)
	}
	if !strings.Contains(view, "[A] lists the 2 marked") {
		t.Fatalf("the strip should state the batch before it applies, got:\n%s", view)
	}
	lines := m.confirmLines()
	if !strings.Contains(lines[1], "[A]") || !strings.Contains(lines[3], "[A]") {
		t.Fatal("both commands should be marked as batch members")
	}
	if strings.Contains(lines[2], "[A]") {
		t.Fatalf("the queued edit should not be marked, got %q", lines[2])
	}
}

func TestBatch_ExcludesFlaggedActions(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "git reset --hard"),
		execCall("c3", "echo three"),
	}})
	m = updated.(Model)
	m = handover(t, m)

	if got := m.pendingBatch; len(got) != 1 || got[0] != "c3" {
		t.Fatalf("a safety-flagged command must be left out of the batch, got %v", got)
	}
	lines := m.confirmLines()
	if strings.Contains(lines[2], "[A]") {
		t.Fatalf("the flagged command should carry no batch mark, got %q", lines[2])
	}
	if !strings.Contains(lines[2], "HIGH") {
		t.Fatalf("the flagged command should be rated on the strip, got %q", lines[2])
	}

	// The list runs the two unflagged commands; the flagged one still asks.
	m, cmd := answerQueue(t, m)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("the flagged command must still prompt, got state %d", m.state)
	}
	if !strings.Contains(strings.Join(m.confirmLines(), "\n"), "git reset --hard") {
		t.Fatal("the prompt should be the flagged command")
	}
	// Decline it, and the last batch member runs without asking again.
	m = handover(t, m)
	updated, _ = m.Update(keyN())
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("the remaining batch member should run without a prompt, got state %d", m.state)
	}
	if len(ran) != 1 || ran[0] != "echo one" {
		t.Fatalf("only the first command should have finished so far, got %v", ran)
	}
}

func TestBatch_ApprovesEveryMemberWithoutAskingAgain(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "echo two"),
		execCall("c3", "echo three"),
	}})
	m = updated.(Model)
	m = handover(t, m)

	m, cmd := answerQueue(t, m)
	// The list is not a session grant: it answers these three and nothing
	// the model asks for later.
	if m.policy.allCommands {
		t.Fatal("[A] must not promote the category to a session grant")
	}
	for i := 0; i < 3; i++ {
		if m.state != stateRunningCmd {
			t.Fatalf("command %d should run without a prompt, got state %d", i+1, m.state)
		}
		updated, cmd = m.Update(driveCmdDone(t, cmd))
		m = updated.(Model)
	}
	if len(ran) != 3 || ran[0] != "echo one" || ran[2] != "echo three" {
		t.Fatalf("all three commands should have run in order, got %v", ran)
	}
	if m.state != stateStreaming || cmd == nil {
		t.Fatal("the stream should resume once the batch drains")
	}
	if len(m.batchAnswered) != 0 {
		t.Fatalf("the list's answers should be consumed as they are used, got %v", m.batchAnswered)
	}
}

func TestBatch_EditsBatchTogether(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		writeCall("w1", first, "one\n"),
		writeCall("w2", second, "two\n"),
		execCall("c1", "echo after"),
	}})
	m = updated.(Model)
	m = handover(t, m)

	if got := m.pendingBatch; len(got) != 1 || got[0] != "w2" {
		t.Fatalf("the batch should hold the other edit only, got %v", got)
	}
	// The strip states an edit by its diff, so two writes are not one row
	// twice over.
	if !strings.Contains(m.confirmLines()[1], "+1 −0") {
		t.Fatalf("an edit row should carry its diff stats, got %q", m.confirmLines()[1])
	}

	m, cmd := answerQueue(t, m)
	for i := 0; i < 2; i++ {
		msg := driveApprovedTool(t, cmd)
		updated, cmd = m.Update(msg)
		m = updated.(Model)
	}
	// Both edits applied; the queued command still asks, because it was never
	// in the batch.
	if m.state != stateConfirmRun {
		t.Fatalf("the queued command should still prompt, got state %d", m.state)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s should have been written: %v", path, err)
		}
	}
}

func TestBatch_DenyStillDeniesOnlyTheCurrentItem(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "echo two"),
	}})
	m = updated.(Model)
	m = handover(t, m)

	updated, _ = m.Update(keyN())
	m = updated.(Model)
	m = handover(t, m)
	if m.state != stateConfirmRun {
		t.Fatalf("declining one item should move to the next, got state %d", m.state)
	}
	if !strings.Contains(strings.Join(m.confirmLines(), "\n"), "echo two") {
		t.Fatal("the second command should be the one now asked")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.ToolCallID != "c1" || !strings.HasPrefix(last.Content, "error:") {
		t.Fatalf("the declined call should have its own error result, got %+v", last)
	}
}

func TestBatch_KeyIsAbsentWithoutAQueue(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "git reset --hard"),
	}})
	m = updated.(Model)
	m = handover(t, m)

	// The only other item is flagged, so there is no batch and no key for one
	// — but [A] keeps its old meaning as the shifted spelling of [a].
	if len(m.pendingBatch) != 0 {
		t.Fatalf("no batch should be offered, got %v", m.pendingBatch)
	}
	if strings.Contains(strings.Join(m.confirmLines(), "\n"), "like this") {
		t.Fatal("no batch key should be offered when nothing would join it")
	}
	updated, _ = m.Update(keyA())
	m = updated.(Model)
	if m.queueList != nil {
		t.Fatal("[A] with nothing to list should not open a list")
	}
	// It keeps its old meaning as the shifted spelling of [a], which is now
	// the grants the card can make rather than one of them (grant.go).
	if m.grantChoice == nil {
		t.Fatal("[A] without a queue should still reach the grant list")
	}
}

func TestBatch_CancelledTurnDropsItsGrants(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "echo two"),
		execCall("c3", "echo three"),
	}})
	m = updated.(Model)
	m = handover(t, m)
	m, _ = answerQueue(t, m)
	if len(m.batchAnswered) != 2 {
		t.Fatalf("the list should hold answers for the two queued commands, got %v", m.batchAnswered)
	}

	// Cancelling the turn drops the queue the answers named, so the answers
	// go with it: a later round's calls could reuse an id and must not
	// inherit an answer given about a queue that no longer exists.
	m.cancelStreaming()
	if len(m.batchAnswered) != 0 {
		t.Fatalf("a cancelled turn should drop the list's answers, got %v", m.batchAnswered)
	}
	if m.pendingQueue.Rows() != 0 {
		t.Fatal("a cancelled turn should drop its queue strip")
	}
}

// --- the queue answered as a list ---

// TestQueueList_CountsTheDecisionsItCouldNotTake holds the fold: the rows are
// the ones the strip marked, and the queued decisions that are nobody's
// business but their own card's are counted on the list rather than dropped
// from it (docs/interface/principles.md#fold-never-hide).
func TestQueueList_CountsTheDecisionsItCouldNotTake(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "echo two"),
		execCall("c3", "git reset --hard"),
		execCall("c4", "echo three"),
		execCall("c5", "git reset --hard HEAD~2"),
		execCall("c6", "echo four"),
	}})
	m = handover(t, updated.(Model))
	m = openQueue(t, m)

	if got := m.queueList.ids; len(got) != 4 {
		t.Fatalf("the list should hold the head and the three marked, got %v", got)
	}
	view := strings.Join(m.confirmLines(), "\n")
	if !strings.Contains(view, "2 asked on their own") {
		t.Fatalf("the list should count what it could not take:\n%s", view)
	}
	if strings.Contains(view, "git reset") {
		t.Fatalf("a flagged command must not be a row of the list:\n%s", view)
	}
	// It opens as the answer the key used to give on its own, which is what
	// makes enter the old act rather than a new one.
	for i, checked := range m.queueList.sel.Checked[:4] {
		if !checked {
			t.Fatalf("row %d should open checked", i)
		}
	}
	// And the strip it replaced is not drawn under it: the list is that strip
	// opened, so the queue is on the screen once.
	if strings.Contains(view, "pending") {
		t.Fatalf("the strip should not be drawn beneath the list:\n%s", view)
	}
}

// TestQueueList_AllowsTheCheckedAndDeniesTheRest is the whole point of the
// list: allowing two and denying one costs one pass, and the denial is the
// reader's own — the same row, the same result and the same reason code a
// card's [n] produces.
func TestQueueList_AllowsTheCheckedAndDeniesTheRest(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "echo two"),
		execCall("c3", "echo three"),
	}})
	m = handover(t, updated.(Model))
	m = openQueue(t, m)

	// Down one row and untick it: the second command is the one the reader
	// does not want.
	m = pressKeys(t, m, keyDown, keySpace)
	updated, cmd := m.Update(keyEnter)
	m = updated.(Model)

	// The head runs now; the rest are answered as they reach the head.
	for i := 0; i < 2 && cmd != nil; i++ {
		if m.state != stateRunningCmd {
			t.Fatalf("an allowed command should run without a prompt, got state %d", m.state)
		}
		updated, cmd = m.Update(driveCmdDone(t, cmd))
		m = updated.(Model)
	}
	if len(ran) != 2 || ran[0] != "echo one" || ran[1] != "echo three" {
		t.Fatalf("the checked commands should have run and nothing else, got %v", ran)
	}
	var denied *provider.Message
	for i, msg := range m.Messages() {
		if msg.ToolCallID == "c2" {
			denied = &m.Messages()[i]
		}
	}
	if denied == nil || !strings.HasPrefix(denied.Content, "error:") {
		t.Fatalf("the unchecked command should have been refused, got %+v", denied)
	}
	// A denial the reader gave is drawn as theirs, not as a rule's
	// (docs/capabilities/approvals-and-safety.md#denials-are-two-different-facts).
	found := false
	for _, e := range m.transcript {
		if e.toolName == "execute_command" && e.deniedBy != "" {
			found = true
			if e.deniedBy != decidedByYou {
				t.Fatalf("the list's denial should be the reader's, got %q", e.deniedBy)
			}
			if e.denyRule != "" {
				t.Fatalf("the list's denial should name no rule, got %q", e.denyRule)
			}
		}
	}
	if !found {
		t.Fatal("the denied command should have left a row")
	}
}

// TestQueueList_ARuleStillRefusesARowItAllowed is why the list marks rather
// than executes: the answer is given before the calls ahead of it have run,
// and every one of them is put to the deny list, the mode and the working
// scope again at the head. A decision does not outrank a rule.
func TestQueueList_ARuleStillRefusesARowItAllowed(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithCommandDenylist([]string{"echo two"})

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "echo two"),
	}})
	m = handover(t, updated.(Model))

	m, cmd := answerQueue(t, m)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)

	if len(ran) != 1 || ran[0] != "echo one" {
		t.Fatalf("the deny-listed command must not run, got %v", ran)
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.ToolCallID != "c2" || !strings.Contains(last.Content, "deny list") {
		t.Fatalf("the deny list should have refused it in its own words, got %+v", last)
	}
	for _, e := range m.transcript {
		if e.deniedBy != "" && e.deniedBy != decidedByAuto {
			t.Fatalf("a rule's denial should not be drawn as the reader's, got %q", e.deniedBy)
		}
	}
}

// TestQueueList_EscAnswersNothing: the way out of a list of decisions leaves
// every one of them exactly where it was
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestQueueList_EscAnswersNothing(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		execCall("c1", "echo one"),
		execCall("c2", "echo two"),
	}})
	m = handover(t, updated.(Model))
	before := strings.Join(m.confirmLines(), "\n")

	m = openQueue(t, m)
	updated, _ = m.Update(keyEsc)
	m = updated.(Model)

	if m.queueList != nil {
		t.Fatal("esc should have closed the list")
	}
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("the card should still be waiting, got state %d", m.state)
	}
	if len(m.batchAnswered) != 0 {
		t.Fatalf("esc should have answered nothing, got %v", m.batchAnswered)
	}
	if len(ran) != 0 {
		t.Fatalf("esc should have run nothing, got %v", ran)
	}
	if got := strings.Join(m.confirmLines(), "\n"); got != before {
		t.Fatalf("esc should give back the card it borrowed the screen from:\n%s\nwant:\n%s", got, before)
	}
}

// TestQueueList_WindowsALongQueue: twenty decisions are scrollable rather
// than clipped, and the marker counts what it is holding back — including how
// many of those are ticked, which is the reader's own answer scrolled out of
// sight.
func TestQueueList_WindowsALongQueue(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	calls := make([]provider.ToolCall, 0, 20)
	for i := 0; i < 20; i++ {
		calls = append(calls, execCall(fmt.Sprintf("c%d", i+1), fmt.Sprintf("echo %d", i+1)))
	}
	updated, _ := m.Update(toolCallsMsg{calls: calls})
	m = handover(t, updated.(Model))
	m = openQueue(t, m)

	if got := len(m.queueList.ids); got != 20 {
		t.Fatalf("every command should be a row, got %d", got)
	}
	lines := m.confirmLines()
	if len(lines) > m.maxConfirmPanelHeight() {
		t.Fatalf("the list should stay inside the panel's %d rows, got %d",
			m.maxConfirmPanelHeight(), len(lines))
	}
	view := strings.Join(lines, "\n")
	if !strings.Contains(view, "more") || !strings.Contains(view, "checked") {
		t.Fatalf("the window should count what it is holding back:\n%s", view)
	}
	if strings.Contains(view, "echo 20") {
		t.Fatalf("a windowed list should not be drawing its last row:\n%s", view)
	}
}

// TestQueueList_RatesEveryRow: the row carries the severity the strip gave
// it, in the same words, because they are the same fact about the same call.
func TestQueueList_RatesEveryRow(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	dir := t.TempDir()

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		writeCall("w1", filepath.Join(dir, "a.txt"), "one\n"),
		writeCall("w2", filepath.Join(dir, "b.txt"), "two\n"),
	}})
	m = handover(t, updated.(Model))
	m = openQueue(t, m)

	view := stripANSI(strings.Join(m.confirmLines(), "\n"))
	if strings.Count(view, "medium") != 2 {
		t.Fatalf("both edits should carry their rating as a word:\n%s", view)
	}
	if !strings.Contains(view, "+1 \u22120") {
		t.Fatalf("a row should keep the short field the strip gives it:\n%s", view)
	}
}

// driveApprovedTool extracts the approvedToolDoneMsg produced by an approval.
func driveApprovedTool(t *testing.T, cmd tea.Cmd) approvedToolDoneMsg {
	t.Helper()
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			return msg
		}
	}
	t.Fatal("expected approvedToolDoneMsg from the approval cmd")
	return approvedToolDoneMsg{}
}

// A refusal a person can act on and one they cannot are two rows, not one.
// The stale row names the file, shortens it against the workspace, and keeps
// the model's own sentence as the body opening the row shows.
func TestSkippedCallEntryTellsStalenessFromBadArguments(t *testing.T) {
	root := t.TempDir()
	m := New(nil, mockStream).WithWorkspace(root)

	stale := m.skippedCallEntry("write_file", fmt.Errorf("invalid arguments: %w",
		tools.StaleError{Path: filepath.Join(root, "internal", "agent", "loop.go")}))
	if stale.text != "internal/agent/loop.go" || stale.skipped != tools.StaleReason {
		t.Errorf("stale row:\n got subject %q reason %q\nwant %q, %q",
			stale.text, stale.skipped, "internal/agent/loop.go", tools.StaleReason)
	}
	if !strings.Contains(stale.toolResult, "read_file it again") {
		t.Errorf("the model's sentence should be the row's expansion, got %q", stale.toolResult)
	}
	if stale.kind != entryTool {
		t.Errorf("a refused call is the call's own row, got kind %d", stale.kind)
	}
	// The row says it in the outcome column, in the grid's own words.
	if row := m.activityRowFor(stale); row.Outcome != components.OutcomeSkipped ||
		row.Allowed != tools.StaleReason || row.Duration != components.NoDuration {
		t.Errorf("the row states the refusal on the grid, got %+v", row)
	}
	// The expansion has to be reachable, or the sentence is hidden rather
	// than folded.
	if len(outputLines(stale)) == 0 {
		t.Error("the row must offer its body to the reader")
	}

	given := fmt.Errorf("invalid arguments: %w", errors.New("path is required"))
	bad := m.skippedCallEntry("write_file", given)
	if bad.text != "write_file" || bad.skipped != skippedArgsReason {
		t.Errorf("malformed row:\n got subject %q reason %q\nwant %q, %q",
			bad.text, bad.skipped, "write_file", skippedArgsReason)
	}
	// The model is handed this sentence and the reader must be handed the
	// same one, or three of these rows in a session cannot be told apart.
	if bad.toolResult != given.Error() {
		t.Errorf("expansion should be the model's sentence verbatim:\n got %q\nwant %q",
			bad.toolResult, given.Error())
	}
	if len(outputLines(bad)) == 0 {
		t.Error("the malformed row must offer its body to the reader")
	}
}

// A call that arrives without a tool name has nothing to name, so the row's
// subject is empty and the outcome carries the whole of what is known.
func TestSkippedCallEntryFallsBackWhenTheToolIsUnnamed(t *testing.T) {
	m := New(nil, mockStream)
	e := m.skippedCallEntry("", errors.New("invalid arguments: path is required"))
	if e.text != "" || e.skipped != skippedArgsReason {
		t.Errorf("unnamed row:\n got subject %q reason %q\nwant %q, %q",
			e.text, e.skipped, "", skippedArgsReason)
	}
	if row := m.activityRowFor(e); row.Verb != "" || row.Target != "" ||
		row.Outcome != components.OutcomeSkipped {
		t.Errorf("nothing but the refusal is known, got %+v", row)
	}
	if e.toolResult == "" {
		t.Error("an unnamed call still folds the sentence the model was given")
	}
}

// A file outside the workspace keeps its absolute path: that it is somewhere
// else is the fact worth seeing.
func TestSkippedCallEntryKeepsAPathOutsideTheWorkspace(t *testing.T) {
	m := New(nil, mockStream).WithWorkspace(filepath.Join(t.TempDir(), "checkout"))
	outside := filepath.Join(t.TempDir(), "elsewhere", "notes.md")
	e := m.skippedCallEntry("write_file", tools.StaleError{Path: outside})
	if !strings.Contains(e.text, outside) {
		t.Errorf("row should keep the absolute path, got %q", e.text)
	}
}
