package chat

// The approval queue strip and batch approval (S-102). What these hold to is
// that [A] never answers more than the strip said it would: the membership on
// screen and the calls that actually run without a second prompt are the same
// set, and a safety-flagged action is in neither.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
)

// keyA presses the batch key.
func keyA() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}} }

// keyN presses the decline key.
func keyN() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}} }

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
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
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

	// The current decision is a command, so the batch is the queued commands
	// and not the queued edit.
	if got := m.pendingBatch; len(got) != 1 || got[0] != "c2" {
		t.Fatalf("batch should hold only the other command, got %v", got)
	}
	view := strings.Join(m.confirmLines(), "\n")
	if !strings.Contains(view, "A: approve 2 like this") {
		t.Fatalf("the key should state how many it answers, got:\n%s", view)
	}
	if !strings.Contains(view, "[A] answers the 2 marked") {
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

	// [A] runs the two unflagged commands; the flagged one still asks.
	updated, cmd := m.Update(keyA())
	m = updated.(Model)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("the flagged command must still prompt, got state %d", m.state)
	}
	if !strings.Contains(strings.Join(m.confirmLines(), "\n"), "git reset --hard") {
		t.Fatal("the prompt should be the flagged command")
	}
	// Decline it, and the last batch member runs without asking again.
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

	updated, cmd := m.Update(keyA())
	m = updated.(Model)
	// The batch is not a session grant: it answers these three and nothing
	// the model asks for later.
	if m.allowAllCommands {
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
	if len(m.batchApproved) != 0 {
		t.Fatalf("batch grants should be consumed as they are used, got %v", m.batchApproved)
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

	if got := m.pendingBatch; len(got) != 1 || got[0] != "w2" {
		t.Fatalf("the batch should hold the other edit only, got %v", got)
	}
	// The strip states an edit by its diff, so two writes are not one row
	// twice over.
	if !strings.Contains(m.confirmLines()[1], "+1 −0") {
		t.Fatalf("an edit row should carry its diff stats, got %q", m.confirmLines()[1])
	}

	updated, cmd := m.Update(keyA())
	m = updated.(Model)
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

	updated, _ = m.Update(keyN())
	m = updated.(Model)
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
	if !m.allowAllCommands {
		t.Fatal("[A] without a batch should still take the session grant")
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
	updated, _ = m.Update(keyA())
	m = updated.(Model)
	if len(m.batchApproved) != 2 {
		t.Fatalf("[A] should hold grants for the two queued commands, got %v", m.batchApproved)
	}

	// Cancelling the turn drops the queue the grants named, so the grants go
	// with it: a later round's calls could reuse an id and must not inherit
	// an answer given about a queue that no longer exists.
	m.cancelStreaming()
	if len(m.batchApproved) != 0 {
		t.Fatalf("a cancelled turn should drop its batch grants, got %v", m.batchApproved)
	}
	if m.pendingQueue.Rows() != 0 {
		t.Fatal("a cancelled turn should drop its queue strip")
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
