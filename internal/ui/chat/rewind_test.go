package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// completeExchange runs one full user turn: send text, stream reply, done.
func completeExchange(t *testing.T, m Model, userText, reply string) Model {
	t.Helper()
	m = sendText(t, m, userText)
	updated, _ := m.Update(tokenMsg{text: reply})
	m = updated.(Model)
	updated, _ = m.Update(doneMsg{})
	return updated.(Model)
}

func newRewindModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, multiTokenStream("ok"))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return updated.(Model)
}

func rewindTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCheckpoints_RecordedPerUserTurn(t *testing.T) {
	m := newRewindModel(t)
	m = completeExchange(t, m, "first question", "answer one")
	m = completeExchange(t, m, "second question", "answer two")

	if len(m.checkpoints) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(m.checkpoints))
	}
	if m.checkpoints[0].index != 1 || m.checkpoints[1].index != 3 {
		t.Fatalf("unexpected checkpoint indices: %d, %d", m.checkpoints[0].index, m.checkpoints[1].index)
	}
	if m.checkpoints[1].preview != "second question" {
		t.Fatalf("unexpected preview: %q", m.checkpoints[1].preview)
	}
	if m.checkpoints[0].hasGit {
		t.Fatal("no snapshot function wired → checkpoints should not claim git state")
	}
}

func TestCheckpoints_GitSnapshotRecorded(t *testing.T) {
	m := newRewindModel(t).WithGitSnapshots(func() GitSnapshot {
		return GitSnapshot{Repo: true, Head: "abc123def456789", StatusHash: "h", DirtyPaths: 2}
	})
	m = completeExchange(t, m, "hello", "hi")

	cp := m.checkpoints[0]
	if !cp.hasGit || !cp.git.Repo || cp.git.Head != "abc123def456789" || cp.git.DirtyPaths != 2 {
		t.Fatalf("git state not recorded on checkpoint: %+v", cp)
	}
}

func TestRewindNumbered_TruncatesAndBranches(t *testing.T) {
	db := rewindTestDB(t)
	m := newRewindModel(t).WithDB(db)
	m = completeExchange(t, m, "first question", "answer one")
	m = completeExchange(t, m, "second question", "answer two")

	m = sendText(t, m, "/rewind 2")

	if got := len(m.Messages()); got != 3 {
		t.Fatalf("expected 3 messages after rewind (sys+turn1), got %d", got)
	}
	if len(m.checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint left, got %d", len(m.checkpoints))
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entrySystem || !strings.Contains(last.text, "files on disk were not restored") {
		t.Fatalf("rewind message must state files are untouched, got %q", last.text)
	}
	if !strings.Contains(last.text, "kept as branch") {
		t.Fatalf("rewind message should name the branch, got %q", last.text)
	}

	branches, err := db.ListChatBranches(AutosaveName)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 {
		t.Fatalf("expected root + tail branch, got %d", len(branches))
	}
	tail, err := db.LoadChat(branches[1].Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 5 {
		t.Fatalf("branch should hold the full abandoned conversation, got %d messages", len(tail))
	}
	// The truncated transcript re-renders correctly (the rewind notice quotes
	// the removed turn's preview, so assert on the answers).
	history := stripANSI(m.renderHistory())
	if !strings.Contains(history, "answer one") || strings.Contains(history, "answer two") {
		t.Fatal("transcript should contain turn 1 only after rewind")
	}
}

func TestRewind_WithoutDB_SaysTailDiscarded(t *testing.T) {
	m := newRewindModel(t)
	m = completeExchange(t, m, "only turn", "reply")

	note := m.rewindToTurn(1)
	if !strings.Contains(note, "discarded") {
		t.Fatalf("no-DB rewind must say the tail was not preserved, got %q", note)
	}
	if got := len(m.Messages()); got != 1 {
		t.Fatalf("expected system prompt only, got %d messages", got)
	}
}

func TestRewind_OutOfRange(t *testing.T) {
	m := newRewindModel(t)
	m = completeExchange(t, m, "hi", "yo")

	if note := m.rewindToTurn(5); !strings.Contains(note, "Usage: /rewind") {
		t.Fatalf("out-of-range turn should show usage, got %q", note)
	}
	if len(m.Messages()) != 3 {
		t.Fatal("out-of-range rewind must not change the conversation")
	}
}

func TestRewind_BarePicker_EscKeepsConversation(t *testing.T) {
	m := newRewindModel(t)
	m = completeExchange(t, m, "first", "one")
	m = completeExchange(t, m, "second", "two")

	m = sendText(t, m, "/rewind")
	if m.state != stateRewindPick || m.rewindSelect == nil {
		t.Fatal("bare /rewind should open the picker")
	}
	if len(m.rewindSelect.Options) != 2 {
		t.Fatalf("picker should list every checkpoint, got %d", len(m.rewindSelect.Options))
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput || m.rewindSelect != nil {
		t.Fatal("esc should dismiss the picker")
	}
	if len(m.Messages()) != 5 {
		t.Fatal("cancelled picker must not change the conversation")
	}
}

func TestRewind_BarePicker_SelectRewinds(t *testing.T) {
	db := rewindTestDB(t)
	m := newRewindModel(t).WithDB(db)
	m = completeExchange(t, m, "first", "one")
	m = completeExchange(t, m, "second", "two")

	m = sendText(t, m, "/rewind")
	// The focused row is the latest turn (options are latest-first).
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("picker should close after selecting")
	}
	if got := len(m.Messages()); got != 3 {
		t.Fatalf("selecting the latest turn should drop it, got %d messages", got)
	}
	if branches, _ := db.ListChatBranches(AutosaveName); len(branches) != 2 {
		t.Fatalf("picker rewind should preserve the tail as a branch, got %d family members", len(branches))
	}
}

func TestRewind_NoCheckpoints(t *testing.T) {
	m := newRewindModel(t)
	m = sendText(t, m, "/rewind")

	if m.state != stateInput {
		t.Fatal("no checkpoints → no picker")
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "No checkpoints") {
		t.Fatalf("expected no-checkpoints notice, got %q", last.text)
	}
}

func TestRewind_GitDivergenceReported(t *testing.T) {
	heads := []string{"aaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbb"}
	call := 0
	m := newRewindModel(t).WithGitSnapshots(func() GitSnapshot {
		h := heads[min(call, len(heads)-1)]
		call++
		return GitSnapshot{Repo: true, Head: h, StatusHash: "s", DirtyPaths: 0}
	})
	m = completeExchange(t, m, "change stuff", "done")

	note := m.rewindToTurn(1)
	if !strings.Contains(note, "HEAD has moved") || !strings.Contains(note, "aaaaaaaaaaaa → bbbbbbbbbbbb") {
		t.Fatalf("expected HEAD divergence in the rewind message, got %q", note)
	}
}

func TestBranches_ListAndSwitch(t *testing.T) {
	db := rewindTestDB(t)
	m := newRewindModel(t).WithDB(db)
	m = completeExchange(t, m, "first question", "answer one")
	m = completeExchange(t, m, "second question", "answer two")
	m = sendText(t, m, "/rewind 2")

	handled, listing := m.handleSlashCommand("/branches")
	if !handled {
		t.Fatal("/branches should be handled")
	}
	if !strings.Contains(listing, "* 1. "+AutosaveName) {
		t.Fatalf("listing should mark the current session, got %q", listing)
	}
	if !strings.Contains(listing, "(branch of") {
		t.Fatalf("listing should show the parent relationship, got %q", listing)
	}

	handled, result := m.handleSlashCommand("/branches 2")
	if !handled || !strings.Contains(result, "Switched to branch") {
		t.Fatalf("expected a branch switch, got %q", result)
	}
	if got := len(m.Messages()); got != 5 {
		t.Fatalf("switching to the tail branch should restore all 5 messages, got %d", got)
	}
	if m.sessionName == AutosaveName {
		t.Fatal("sessionName should track the switched-to branch")
	}
	if len(m.checkpoints) != 2 {
		t.Fatalf("checkpoints should rebuild for the loaded branch, got %d", len(m.checkpoints))
	}

	// The pre-switch working conversation was saved, not lost.
	kept, err := db.LoadChat(AutosaveName)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 3 {
		t.Fatalf("current branch should be saved before switching, got %d messages", len(kept))
	}

	// /save then /load work on the branch.
	if handled, res := m.handleSlashCommand("/save named-branch"); !handled || !strings.Contains(res, "saved") {
		t.Fatalf("/save on a branch failed: %q", res)
	}
	if m.sessionName != "named-branch" {
		t.Fatal("/save should move the session to the new name")
	}
	if handled, res := m.handleSlashCommand("/load " + AutosaveName); !handled || !strings.Contains(res, "Loaded chat") {
		t.Fatalf("/load on a branch failed: %q", res)
	}
	if len(m.Messages()) != 3 {
		t.Fatal("/load should replace the conversation with the loaded branch")
	}
}

func TestBranches_NoDB(t *testing.T) {
	m := newRewindModel(t)
	if _, result := m.handleSlashCommand("/branches"); !strings.Contains(result, "unavailable") {
		t.Fatalf("expected persistence-unavailable notice, got %q", result)
	}
}

func TestBranches_NoneYet(t *testing.T) {
	db := rewindTestDB(t)
	m := newRewindModel(t).WithDB(db)
	if _, result := m.handleSlashCommand("/branches"); !strings.Contains(result, "no branches yet") {
		t.Fatalf("expected no-branches notice, got %q", result)
	}
}

func TestLoadConversation_RebuildsCheckpoints(t *testing.T) {
	saved := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "old question"},
		{Role: provider.RoleAssistant, Content: "old answer"},
		{Role: provider.RoleUser, Content: "follow-up"},
		{Role: provider.RoleAssistant, Content: "more"},
	}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithResumedMessages(saved)

	if len(m.checkpoints) != 2 {
		t.Fatalf("resumed sessions should have rewind checkpoints, got %d", len(m.checkpoints))
	}
	if m.checkpoints[1].index != 3 || m.checkpoints[1].preview != "follow-up" {
		t.Fatalf("unexpected rebuilt checkpoint: %+v", m.checkpoints[1])
	}
}

func TestSteering_RecordsCheckpoint(t *testing.T) {
	m := newRewindModel(t)
	m = completeExchange(t, m, "first", "one")
	m.state = stateStreaming
	m.steering = []string{"actually do this instead"}
	if !m.injectSteering() {
		t.Fatal("steering should inject")
	}
	if len(m.checkpoints) != 2 {
		t.Fatalf("steering messages are user turns and should checkpoint, got %d", len(m.checkpoints))
	}
}
