package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
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

// A conversation that came back from the store is rebuilt from its messages,
// and the ones the session wrote for itself are the session's: they come back
// as system rows and they are not turns. Counting one would number the check-
// in among the reader's turns, and "turn 3" would then cut the conversation
// in the middle of the turn the check-in interrupted.
func TestCheckpoints_RebuiltSkipTheMessagesTheSessionWrote(t *testing.T) {
	steer := "You are editing a file the task did not ask about. Say why or go back."
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "add the retry"},
		{Role: provider.RoleAssistant, Content: "editing the config"},
		{Role: provider.RoleUser, Content: steer, Machine: true},
		{Role: provider.RoleAssistant, Content: "going back"},
		{Role: provider.RoleUser, Content: "now run the tests"},
	})

	if len(m.checkpoints) != 2 {
		t.Fatalf("expected the two typed turns as checkpoints, got %d: %+v", len(m.checkpoints), m.checkpoints)
	}
	loaded := m.Messages()
	for _, cp := range m.checkpoints {
		if loaded[cp.index].Machine {
			t.Fatalf("checkpoint %d points at a message the session wrote: %q",
				cp.index, loaded[cp.index].Content)
		}
	}
	if m.checkpoints[1].preview != "now run the tests" {
		t.Fatalf("the newest checkpoint previews %q, want the last typed turn",
			m.checkpoints[1].preview)
	}
	var kind entryKind
	for _, e := range m.transcript {
		if strings.Contains(e.text, "did not ask about") {
			kind = e.kind
		}
	}
	if kind != entrySystem {
		t.Fatalf("the steer came back as entry kind %v, want the session's own row", kind)
	}
}

func TestRewindNumbered_TruncatesAndBranches(t *testing.T) {
	db := rewindTestDB(t)
	m := newRewindModel(t).WithDB(db)
	m = completeExchange(t, m, "first question", "answer one")
	m = completeExchange(t, m, "second question", "answer two")
	root := m.sessionName

	m = sendText(t, m, "/rewind 1")

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

	branches, err := db.ListChatBranches(root)
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

	note := m.rewindToTurn(0)
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

// A rewind to turn N returns to where turn N ended, so the latest turn is
// where the session already stands and turn 0 is the start of it
// (docs/interface/surfaces.md#the-rewind).
func TestRewind_TurnNIsWhereTurnNEnded(t *testing.T) {
	m := newRewindModel(t)
	m = completeExchange(t, m, "first", "one")
	m = completeExchange(t, m, "second", "two")

	if note := m.rewindToTurn(2); !strings.Contains(note, "already stands at the end of turn 2") {
		t.Fatalf("the latest turn has nothing after it to take back, got %q", note)
	}
	if len(m.Messages()) != 5 {
		t.Fatal("a rewind to where the session stands must not change the conversation")
	}
	if note := m.rewindToTurn(1); !strings.Contains(note, `Rewound to the end of turn 1 ("first")`) {
		t.Fatalf("the message should name the turn the session now stands at, got %q", note)
	}
	if got := m.frameActivity(40); !strings.Contains(got, "at turn 1") {
		t.Fatalf("the frame should name the same turn the command did, got %q", got)
	}
	if note := m.rewindToTurn(0); !strings.Contains(note, "Rewound to the start of the session") {
		t.Fatalf("turn 0 is the start of the session, got %q", note)
	}
	if len(m.Messages()) != 1 {
		t.Fatalf("the start of the session is the system prompt alone, got %d messages", len(m.Messages()))
	}
	if got := m.frameActivity(40); !strings.Contains(got, "at the start") {
		t.Fatalf("a rewind to turn 0 still says where the session stands, got %q", got)
	}
}

func TestRewind_BarePicker_EscKeepsConversation(t *testing.T) {
	m := newRewindModel(t)
	m = completeExchange(t, m, "first", "one")
	m = completeExchange(t, m, "second", "two")

	m = sendText(t, m, "/rewind")
	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /rewind should open the picker")
	}
	if len(m.picker.Options) != 2 {
		t.Fatalf("picker should list every checkpoint, got %d", len(m.picker.Options))
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput || m.picker != nil {
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
	root := m.sessionName

	m = sendText(t, m, "/rewind")
	// The rows are latest-first, so the second is turn 1: returning to where
	// it ended takes turn 2 back.
	m = press(t, m, "down")
	m = press(t, m, "enter")

	if m.state != stateInput {
		t.Fatal("picker should close after selecting")
	}
	if got := len(m.Messages()); got != 3 {
		t.Fatalf("returning to the end of turn 1 should drop turn 2, got %d messages", got)
	}
	if branches, _ := db.ListChatBranches(root); len(branches) != 2 {
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

	note := m.rewindToTurn(0)
	if !strings.Contains(note, "HEAD has moved") || !strings.Contains(note, "aaaaaaaaaaaa → bbbbbbbbbbbb") {
		t.Fatalf("expected HEAD divergence in the rewind message, got %q", note)
	}
}

func TestBranches_ListAndSwitch(t *testing.T) {
	db := rewindTestDB(t)
	m := newRewindModel(t).WithDB(db)
	m = completeExchange(t, m, "first question", "answer one")
	m = completeExchange(t, m, "second question", "answer two")
	root := m.sessionName
	m = sendText(t, m, "/rewind 1")

	// Bare /branches is the picker's; the text path never answers with a
	// list of rows to read a number off.
	handled, bare := m.handleSlashCommand("/branches")
	if !handled {
		t.Fatal("/branches should be handled")
	}
	if !strings.Contains(bare, "opens the picker") {
		t.Fatalf("bare /branches should name the picker, got %q", bare)
	}
	if strings.Contains(bare, root) || strings.Contains(bare, "1. ") {
		t.Fatalf("bare /branches should not list the family, got %q", bare)
	}

	handled, result := m.handleSlashCommand("/branches 2")
	if !handled || !strings.Contains(result, "Switched to branch") {
		t.Fatalf("expected a branch switch, got %q", result)
	}
	if got := len(m.Messages()); got != 5 {
		t.Fatalf("switching to the tail branch should restore all 5 messages, got %d", got)
	}
	if m.sessionName == root {
		t.Fatal("sessionName should track the switched-to branch")
	}
	if len(m.checkpoints) != 2 {
		t.Fatalf("checkpoints should rebuild for the loaded branch, got %d", len(m.checkpoints))
	}

	// The pre-switch working conversation was saved, not lost.
	kept, err := db.LoadChat(root)
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
	if handled, res := m.handleSlashCommand("/load " + root); !handled || !strings.Contains(res, "Loaded chat") {
		t.Fatalf("/load on a branch failed: %q", res)
	}
	// The branch's three messages, plus the reading /load puts in front of
	// any conversation it opens.
	if len(m.Messages()) != 4 {
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
		WithResumedMessages("", saved)

	if len(m.checkpoints) != 2 {
		t.Fatalf("resumed sessions should have rewind checkpoints, got %d", len(m.checkpoints))
	}
	// Index 3 in the saved conversation, one further along in the one that
	// was restored: the reading of the checkout went in ahead of it, and a
	// checkpoint that did not move with it would rewind to the wrong turn.
	if m.checkpoints[1].index != 4 || m.checkpoints[1].preview != "follow-up" {
		t.Fatalf("unexpected rebuilt checkpoint: %+v", m.checkpoints[1])
	}
}

func TestSteering_RecordsCheckpoint(t *testing.T) {
	m := newRewindModel(t)
	m = completeExchange(t, m, "first", "one")
	m.state = stateStreaming
	m.steering = []steeringItem{{text: "actually do this instead"}}
	if !m.injectSteering() {
		t.Fatal("steering should inject")
	}
	if len(m.checkpoints) != 2 {
		t.Fatalf("steering messages are user turns and should checkpoint, got %d", len(m.checkpoints))
	}
}

// rewindChangeModel is a rewind model wired to a store and a changeset that
// persists into it, which is what a coding session has. The slot is named
// rather than minted: two models in one test share one handle to the store,
// and the timestamp every session is named after has a second's resolution —
// so two of them would claim one name and the second would give back the
// first's row, which two real processes never do because neither has the
// other's writes in its own map.
func rewindChangeModel(t *testing.T, db *storage.DB, store *changeset.Store, name string) Model {
	t.Helper()
	store.Persist(db)
	m := newRewindModel(t)
	m.sessionName = name
	return m.WithDB(db).WithChangeset(store, nil)
}

// recordEdit writes content and records the edit against the turn in flight,
// the way an approved write does.
func recordEdit(t *testing.T, m Model, path, before, after string) {
	t.Helper()
	if after == "" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}
	m.changes.Add(m.turnCount, changeset.Record{
		Path: path, Before: before, After: after,
		BeforeExists: before != "", AfterExists: after != "",
	})
}

// rewindOfferModel: two turns, each editing one file, ready for /rewind.
func rewindOfferModel(t *testing.T) (Model, string, string) {
	t.Helper()
	db := rewindTestDB(t)
	m := rewindChangeModel(t, db, changeset.New(0), "offer")
	dir := t.TempDir()
	kept, added := filepath.Join(dir, "kept.go"), filepath.Join(dir, "added.go")
	if err := os.WriteFile(kept, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = sendText(t, m, "first")
	recordEdit(t, m, kept, "one\n", "two\n")
	m = completeReply(t, m, "did it")

	m = sendText(t, m, "second")
	recordEdit(t, m, kept, "two\n", "three\n")
	recordEdit(t, m, added, "", "new\n")
	m = completeReply(t, m, "did it again")
	return m, kept, added
}

// completeReply streams a reply and closes the turn.
func completeReply(t *testing.T, m Model, reply string) Model {
	t.Helper()
	updated, _ := m.Update(tokenMsg{text: reply})
	m = updated.(Model)
	updated, _ = m.Update(doneMsg{})
	return updated.(Model)
}

// The scope card is the whole point: going back to before a turn and leaving
// what it wrote on disk is one answer of three, not the only one there is.
func TestRewind_OffersConversationFilesOrBoth(t *testing.T) {
	m, _, _ := rewindOfferModel(t)

	m = sendText(t, m, "/rewind 1")
	if m.state != stateRewindScope || m.rewindScope == nil {
		t.Fatalf("a rewind with records after it should ask what to put back, got state %v", m.state)
	}
	card := m.rewindScope.card
	if card.Title != "Rewind to turn 1" {
		t.Fatalf("the card should name the point it would return to, got %q", card.Title)
	}
	if !strings.Contains(card.Code.Value, "2 files") {
		t.Fatalf("the code field should say what would come back, got %q", card.Code.Value)
	}
	// The hole in the offer is named on the field that would write.
	if !strings.Contains(card.Code.Detail, "command") {
		t.Fatalf("the card should say a command's changes are not recorded, got %q", card.Code.Detail)
	}
	if !strings.Contains(card.Talk.Value, "turn 2 leaves the window") || !strings.Contains(card.Talk.Detail, "ctx ") {
		t.Fatalf("the talk field should say what leaves and what it costs, got %q / %q",
			card.Talk.Value, card.Talk.Detail)
	}
	if card.Undo.Value != "yes" {
		t.Fatalf("a rewind is a turn and can be taken back, got %q", card.Undo.Value)
	}
	// Nothing was written by opening the card.
	if len(m.Messages()) != 5 {
		t.Fatalf("the offer must not rewind anything on its own, got %d messages", len(m.Messages()))
	}
}

// A session with nothing on record after the checkpoint has one answer, so it
// is given rather than asked for.
func TestRewind_WithNothingRecordedGoesStraightBack(t *testing.T) {
	db := rewindTestDB(t)
	m := rewindChangeModel(t, db, changeset.New(0), "nothing recorded")
	m = completeExchange(t, m, "only turn", "reply")

	m = sendText(t, m, "/rewind 0")
	if m.state != stateInput {
		t.Fatalf("no records means no question to ask, got state %v", m.state)
	}
	if !strings.Contains(lastSystem(t, m), "files on disk were not restored") {
		t.Fatalf("the message should still say the files were left, got %q", lastSystem(t, m))
	}
}

// The files answer: every turn from the checkpoint on, folded into one net
// change per file, put back through the undo confirm — with a file that has
// changed since left alone and named.
func TestRewind_FilesRestoresTheRunAndLeavesDrift(t *testing.T) {
	m, kept, added := rewindOfferModel(t)
	// Somebody edited the file the second turn created, after the turn.
	if err := os.WriteFile(added, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = sendText(t, m, "/rewind 1")
	m = press(t, m, keys.Shown(keys.Rewind.Code))

	if m.state != stateUndoConfirm || m.undoAsk == nil {
		t.Fatalf("the files answer should ask before it writes, got state %v", m.state)
	}
	if got := m.undoAsk.Drifted; len(got) != 1 || got[0] != added {
		t.Fatalf("the file changed since should be the drifted one, got %v", got)
	}
	if m.undoAsk.Restores != 1 {
		t.Fatalf("expected the one file [y] would write back, got %d", m.undoAsk.Restores)
	}

	m = press(t, m, "y")
	content, err := os.ReadFile(kept)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "two\n" {
		t.Fatalf("the file should be where turn 1 left it, got %q", content)
	}
	if drifted, err := os.ReadFile(added); err != nil || string(drifted) != "mine\n" {
		t.Fatalf("a file changed since must be left alone, got %q (%v)", drifted, err)
	}
	notice := lastSystem(t, m)
	if !strings.Contains(notice, added) || !strings.Contains(notice, "/rewind 1 again") {
		t.Fatalf("the notice should name the file it left and how to force it, got %q", notice)
	}
	// The conversation was not touched: this answer was about the files.
	if len(m.Messages()) < 5 {
		t.Fatalf("the files answer must leave the conversation alone, got %d messages", len(m.Messages()))
	}
	// And the restore is a turn of its own, so it can be undone in turn.
	if note := lastClose(t, m).Note; !strings.Contains(note, "rewind to turn 1") {
		t.Fatalf("the restore should close with a row naming what it took back, got %q", note)
	}
}

// Both: the conversation goes back and the confirm for the files comes up
// behind it.
func TestRewind_BothRewindsAndThenAsksAboutTheFiles(t *testing.T) {
	m, kept, _ := rewindOfferModel(t)

	m = sendText(t, m, "/rewind 1")
	m = press(t, m, keys.Shown(keys.Rewind.Both))

	if got := len(m.Messages()); got != 3 {
		t.Fatalf("both should have rewound the conversation to the end of turn 1, got %d messages", got)
	}
	if m.state != stateUndoConfirm {
		t.Fatalf("both should then ask about the files, got state %v", m.state)
	}
	m = press(t, m, "y")
	if content, err := os.ReadFile(kept); err != nil || string(content) != "two\n" {
		t.Fatalf("both should have put the files back, got %q (%v)", content, err)
	}
}

// Talk only: the conversation goes back, and the message says the files were
// left where the turns left them.
func TestRewind_ConversationLeavesTheFiles(t *testing.T) {
	m, kept, _ := rewindOfferModel(t)

	m = sendText(t, m, "/rewind 1")
	m = press(t, m, keys.Shown(keys.Rewind.Talk))

	if m.state == stateUndoConfirm {
		t.Fatal("the conversation answer writes no files and asks nothing")
	}
	if content, err := os.ReadFile(kept); err != nil || string(content) != "three\n" {
		t.Fatalf("the files should be exactly as the turns left them, got %q (%v)", content, err)
	}
	if !strings.Contains(lastSystem(t, m), "left as they are") {
		t.Fatalf("the message should say the files were kept, got %q", lastSystem(t, m))
	}
}

// A conversation rebuilt from a saved transcript knows where its turns began
// and not what they were numbered, so it offers the conversation alone rather
// than guessing whose edits to put back.
func TestRewind_ARebuiltCheckpointOffersNoFiles(t *testing.T) {
	db := rewindTestDB(t)
	m := rewindChangeModel(t, db, changeset.New(0), "rebuilt")
	m = completeExchange(t, m, "first", "one")
	m.loadConversation(m.Messages())

	m = sendText(t, m, "/rewind 0")
	if m.state != stateInput {
		t.Fatalf("a rebuilt checkpoint has no turn to restore, got state %v", m.state)
	}
}

// unhashableSnapshots wires the checkpoint capture to a real reading of ws,
// which is what the session does — the flag saying whether the content was
// digested has to survive the copy or the divergence line has nothing to
// check.
func unhashableSnapshots(ws string) func() GitSnapshot {
	return func() GitSnapshot {
		fp := quality.TakeFingerprint(ws)
		return GitSnapshot{
			Repo: fp.Repo, Head: fp.Head, StatusHash: fp.StatusHash,
			DirtyPaths: fp.DirtyPaths, Unhashed: fp.Unhashed,
		}
	}
}

// Past the bound the digest stands for the dirty paths' names and only as
// much of their content as was reached, so a run of edits confined to files
// that were already dirty leaves two readings identical. The sentence saying
// the tree still matches is the one a restore gets decided on, so it is the
// one withheld.
func TestRewind_ATreePastTheBoundIsNotReadAsUnchanged(t *testing.T) {
	cases := []struct {
		name string
		fill func(t *testing.T, ws string)
	}{
		// Both fixtures are sized comfortably past their bound rather than
		// to it: the numbers themselves live in the quality package, and a
		// copy of one here would be a second statement of it to go stale.
		{"more paths than are digested", func(t *testing.T, ws string) {
			t.Helper()
			for i := 0; i < 1000; i++ {
				name := filepath.Join(ws, fmt.Sprintf("f%04d.txt", i))
				if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"more bytes than are digested", func(t *testing.T, ws string) {
			t.Helper()
			// Sparse: the budget is checked against the reported size before
			// anything is read, so this crosses the bound at no disk cost.
			for i := 0; i < 4; i++ {
				f, err := os.Create(filepath.Join(ws, fmt.Sprintf("big%d.bin", i)))
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate(16 << 20); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := treeRepo(t)
			tc.fill(t, ws)
			m := newRewindModel(t).WithGitSnapshots(unhashableSnapshots(ws))
			m = completeExchange(t, m, "change stuff", "done")

			if !m.checkpoints[0].git.Unhashed {
				t.Fatalf("the fixture should be past the bound: %+v", m.checkpoints[0].git)
			}
			note := m.gitDivergence(m.checkpoints[0])
			if !strings.Contains(note, "cannot be read") {
				t.Fatalf("expected the unreadable-tree line, got %q", note)
			}
			if !strings.Contains(note, quality.ContentBound()) {
				t.Fatalf("the line should name the bound, got %q", note)
			}
			if strings.Contains(note, "match this checkpoint") {
				t.Fatalf("a tree nobody digested must not read as unchanged, got %q", note)
			}
		})
	}
}

// Within the bound the reading is the one it always was: an untouched tree
// matches, and an edit to a file that was already dirty is a change, because
// the content is digested and the still path list hides nothing.
func TestRewind_WithinTheBoundTheReadingIsUnchanged(t *testing.T) {
	ws := treeRepo(t)
	dirty := filepath.Join(ws, "dirty.txt")
	if err := os.WriteFile(dirty, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newRewindModel(t).WithGitSnapshots(unhashableSnapshots(ws))
	m = completeExchange(t, m, "change stuff", "done")

	if m.checkpoints[0].git.Unhashed {
		t.Fatalf("one dirty path is well inside the bound: %+v", m.checkpoints[0].git)
	}
	if note := m.gitDivergence(m.checkpoints[0]); !strings.Contains(note, "match this checkpoint") {
		t.Fatalf("an untouched tree should still read as matching, got %q", note)
	}
	if err := os.WriteFile(dirty, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if note := m.gitDivergence(m.checkpoints[0]); !strings.Contains(note, "has changed since this checkpoint") {
		t.Fatalf("an edit inside an already-dirty file is a change, got %q", note)
	}
}

// The reading is a warning and not a refusal. What a restore puts back comes
// from the session's own records, which are exact whatever the tree's size,
// so a tree the fingerprint could not read is still offered one.
func TestRewind_ATreePastTheBoundStillOffersTheRestore(t *testing.T) {
	db := rewindTestDB(t)
	m := rewindChangeModel(t, db, changeset.New(0), "past the bound").
		WithGitSnapshots(func() GitSnapshot {
			return GitSnapshot{Repo: true, Head: "abc123def4567", StatusHash: "s", DirtyPaths: 900, Unhashed: true}
		})
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept.go")
	if err := os.WriteFile(kept, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = sendText(t, m, "first")
	recordEdit(t, m, kept, "one\n", "two\n")
	m = completeReply(t, m, "did it")

	m = sendText(t, m, "/rewind 0")
	if m.state != stateRewindScope || m.rewindScope == nil {
		t.Fatalf("an unreadable tree must still be offered all three answers, got state %v", m.state)
	}
	m = press(t, m, keys.Shown(keys.Rewind.Both))

	if m.state != stateUndoConfirm {
		t.Fatalf("the files half should still reach the confirm, got state %v", m.state)
	}
	if note := lastSystem(t, m); !strings.Contains(note, "cannot be read") {
		t.Fatalf("the message should warn what it could not check, got %q", note)
	}
}

// --- the timeline -------------------------------------------------

// rewindPickerModel is four turns of one session: one returning to whose end
// would take back a turn whose records were dropped — which is the boundary a
// restore cannot cross — that dropped turn, one that read only, and one that
// wrote. The ages are fixed so the rows say the same thing on every run.
func rewindPickerModel(t *testing.T, width int) Model {
	t.Helper()
	db := rewindTestDB(t)
	// The bound holds the last turn's records and not the second's, so the
	// last turn's write is what drops the second.
	m := rewindChangeModel(t, db, changeset.New(1024), "timeline")
	m.width, m.height = width, 40
	m.syncInputWidth()
	dir := t.TempDir()
	loop, rounds := filepath.Join(dir, "loop.go"), filepath.Join(dir, "rounds.go")
	for _, p := range []string{loop, rounds} {
		if err := os.WriteFile(p, []byte(strings.Repeat("one\n", 6)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m = sendText(t, m, "clear the tmp dir")
	m = completeReply(t, m, "cleared")

	fixtures := filepath.Join(dir, "fixtures.txt")
	m = sendText(t, m, "regenerate the fixtures")
	recordEdit(t, m, fixtures, "", strings.Repeat("fixture\n", 200))
	m = completeReply(t, m, "regenerated")

	m = sendText(t, m, "find where rounds are counted")
	m = completeReply(t, m, "in the loop")

	m = sendText(t, m, "cap rounds instead of erroring")
	recordEdit(t, m, loop, strings.Repeat("one\n", 6), strings.Repeat("two\n", 8))
	recordEdit(t, m, rounds, strings.Repeat("one\n", 6), strings.Repeat("two\n", 10))
	m = completeReply(t, m, "done")

	if got := m.changes.Evicted(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("the fixture wants turn 2's records dropped, got %v", got)
	}
	for i := range m.checkpoints {
		m.checkpoints[i].at = time.Now().Add(-time.Duration(30-9*i) * time.Minute)
	}
	return m
}

// Each row is a turn: its words behind its own number, what it changed, and
// how long ago (docs/interface/surfaces.md#the-rewind).
func TestRewindPicker_RowsCarryTheTurnAndWhatItChanged(t *testing.T) {
	m := rewindPickerModel(t, 120)
	m = sendText(t, m, "/rewind")
	if m.state != statePick || m.picker == nil {
		t.Fatalf("bare /rewind should open the timeline, got state %v", m.state)
	}
	if m.picker.Rail != rewindRailLabel {
		t.Fatalf("the picker should name itself on the rail, got %q", m.picker.Rail)
	}
	if !strings.HasPrefix(strings.Join(m.pickerLines(), "\n"), "\x1b[38;5;") &&
		!strings.Contains(m.pickerLines()[0], "REWIND") {
		t.Fatalf("the rail should be the first line drawn, got %q", m.pickerLines()[0])
	}
	opts := m.picker.Options
	if len(opts) != 4 {
		t.Fatalf("four turns, four rows, got %d", len(opts))
	}
	// Newest first, and the number on the row is the turn's own.
	if opts[0].Number != 4 || opts[3].Number != 1 {
		t.Fatalf("rows should be numbered by turn, newest first: %d … %d", opts[0].Number, opts[3].Number)
	}
	if detail := detailPlain(opts[0]); !strings.HasPrefix(detail, mutationMark) ||
		!strings.Contains(detail, "2 files") || !strings.Contains(detail, "+18") {
		t.Fatalf("a turn that wrote should carry its mark and diffstat, got %q", detail)
	}
	if detail := detailPlain(opts[1]); detail != readsOnlyPhrase {
		t.Fatalf("a turn that changed nothing should say so, got %q", detail)
	}
	if opts[1].Meta != "12m" {
		t.Fatalf("the age is the short field at the end of the row, got %q", opts[1].Meta)
	}
}

// A turn past which the files cannot come back stays in the list, inert, with
// the reason where the diffstat would be — and talk only still works past it.
func TestRewindPicker_AnUnreachableTurnStatesWhyAndStillRewindsTheTalk(t *testing.T) {
	m := rewindPickerModel(t, 120)
	m = sendText(t, m, "/rewind")
	oldest := m.picker.Options[3]
	if !oldest.Dim {
		t.Fatalf("a turn no restore can cross should be drawn as unavailable: %+v", oldest)
	}
	if !strings.HasPrefix(detailPlain(oldest), "code can't be restored past this — ") {
		t.Fatalf("the row should say why on the row, got %q", detailPlain(oldest))
	}
	// Taking it is how the surface says why, and the conversation still goes
	// back: what the records never held never was.
	m = press(t, m, "down")
	m = press(t, m, "down")
	m = press(t, m, "down")
	m = press(t, m, "enter")
	if m.state == stateRewindScope {
		t.Fatal("there is no file half to ask about past the boundary")
	}
	if note := lastSystem(t, m); !strings.Contains(note, "Only the conversation was rewound") {
		t.Fatalf("talk only should still have run, got %q", note)
	}
}

// The latest row of a conversation rebuilt from the store has no turn after
// it to be blocked by, and no number of its own to ask the records about, so
// it reports nothing rather than some other turn's change.
func TestRewindPicker_ARebuiltTurnReportsNoChange(t *testing.T) {
	m := rewindPickerModel(t, 120)
	m.checkpoints[len(m.checkpoints)-1].turn = 0
	m = sendText(t, m, "/rewind")
	if latest := m.picker.Options[0]; len(latest.Detail) != 0 || latest.Dim {
		t.Fatalf("a rebuilt turn's row should say nothing about what it changed: %+v", latest)
	}
}

// detailPlain is a row's detail as the cells it says, which is what a test
// asserts against and what the lit row is painted from.
func detailPlain(opt components.SelectOption) string {
	var b strings.Builder
	for _, s := range opt.Detail {
		b.WriteString(s.Text)
	}
	return b.String()
}

// --- the return ---------------------------------------------------

// A rewind lands as an act on the mutation rail, and the frame's top rail
// says where the reader now stands until the next turn
// (docs/interface/surfaces.md#the-rewind).
func TestRewind_LandsAsARowAndTheFrameSaysWhereYouStand(t *testing.T) {
	m, _, _ := rewindOfferModel(t)
	m.width, m.height = 120, 40
	m.syncInputWidth()

	m = sendText(t, m, "/rewind 1")
	m = press(t, m, keys.Shown(keys.Rewind.Talk))

	row := lastRewindRow(t, m)
	if row.Kind != components.ActivityCompaction {
		t.Fatalf("a rewind that moved no files carries no mutation rail, got kind %v", row.Kind)
	}
	if row.Verb != rewindVerb {
		t.Fatalf("the row's verb is the act, got %q", row.Verb)
	}
	if !strings.Contains(row.Target, "out of the window") || !strings.Contains(row.Target, "ctx ") {
		t.Fatalf("the row should say what left and what the window costs, got %q", row.Target)
	}
	if got := m.frameActivity(40); !strings.Contains(got, "at turn 1") {
		t.Fatalf("the frame should say where the session stands, got %q", got)
	}
	// The next turn makes the default reading true again.
	m = sendText(t, m, "carry on")
	if got := m.frameActivity(40); strings.Contains(got, "at turn") {
		t.Fatalf("a new turn should retire the rewind's label, got %q", got)
	}
}

// The card's ctx pair is the row's. The provider's report counted the turns
// the cut takes out, so the rewind drops it, and the window afterwards is the
// corrected estimate of what is kept: a card that subtracted an estimate of
// the tail from the report would predict one figure and the row land on
// another (docs/interface/surfaces.md#the-rewind).
func TestRewind_TheCardsContextPairIsTheRows(t *testing.T) {
	for _, answer := range []keys.Binding{keys.Rewind.Talk, keys.Rewind.Both} {
		m, _, _ := rewindOfferModel(t)
		m.width, m.height = 120, 40
		m.syncInputWidth()
		// A small report over a conversation whose estimate is a fifth of the
		// window, the shape a scripted endpoint gives: the report puts the
		// window near empty and the estimate the cut falls back on does not.
		m.toolDefTokens = m.contextWindow() / 5
		m.contextTokens, m.contextReportedAt = 10, len(m.Messages())

		m = sendText(t, m, "/rewind 1")
		if m.rewindScope == nil {
			t.Fatal("the rewind should have opened the scope card")
		}
		detail := m.rewindScope.card.Talk.Detail
		pair := detail[:strings.Index(detail, " · ")]
		if pair != "ctx 0% → 20%" {
			t.Fatalf("the card should predict the corrected estimate of the kept turns, got %q", pair)
		}
		m = press(t, m, keys.Shown(answer))
		if m.state == stateUndoConfirm {
			m = press(t, m, "y")
		}
		if row := lastRewindRow(t, m); !strings.Contains(row.Target, pair) {
			t.Fatalf("the row should land on the card's %q, got %q", pair, row.Target)
		}
	}
}

// Both halves of one act land as one row: the file restore is answered at the
// confirm, and the row waits for that answer.
func TestRewind_BothLandsOneRowCountingWhatCameBack(t *testing.T) {
	m, _, _ := rewindOfferModel(t)
	m.width, m.height = 120, 40
	m.syncInputWidth()

	m = sendText(t, m, "/rewind 1")
	m = press(t, m, keys.Shown(keys.Rewind.Both))
	if m.state != stateUndoConfirm {
		t.Fatalf("both should ask about the files, got state %v", m.state)
	}
	if rewindRows(m) != 0 {
		t.Fatal("the row must wait for the file half rather than reporting half an act")
	}
	m = press(t, m, "y")

	if n := rewindRows(m); n != 1 {
		t.Fatalf("one act, one row, got %d", n)
	}
	row := lastRewindRow(t, m)
	if row.Kind != components.ActivityEdit {
		t.Fatalf("a rewind that put files back carries the mutation rail, got kind %v", row.Kind)
	}
	if !strings.Contains(row.Counts, "file") {
		t.Fatalf("the row should count what came back, got %q", row.Counts)
	}
	if !strings.Contains(row.Target, "files back to turn 1") ||
		!strings.Contains(row.Target, "out of the window") {
		t.Fatalf("the row should state both halves, got %q", row.Target)
	}
}

// rewindRows counts the rewind rows in the transcript.
func rewindRows(m Model) int {
	n := 0
	for _, e := range m.transcript {
		if e.notice != nil && e.notice.Act != nil && e.notice.Act.Verb == rewindVerb {
			n++
		}
	}
	return n
}

// lastRewindRow is the most recent rewind row, or a fatal.
func lastRewindRow(t *testing.T, m Model) components.ActivityRow {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if e := m.transcript[i]; e.notice != nil && e.notice.Act != nil && e.notice.Act.Verb == rewindVerb {
			return *e.notice.Act
		}
	}
	t.Fatal("no rewind row in the transcript")
	return components.ActivityRow{}
}

// --- goldens --------------------------------------------------------

// TestGolden_RewindPicker captures the timeline: the rail that names it, a
// turn that wrote, a turn that read only, and the turn no restore can cross.
func TestGolden_RewindPicker(t *testing.T) {
	captureGolden(t, "rewind-picker", "the rewind picker", goldenWidths,
		func(width int) []golden.Panel {
			m := rewindPickerModel(t, width)
			m = sendText(t, m, "/rewind")
			m.syncViewport()
			// The card is held by pointer, so the whole list is rendered
			// before anything is typed into it: a copy of the model shares
			// the card the query would edit.
			whole := strings.Join(m.pickerLines(), "\n")
			for _, r := range "rounds" {
				m = press(t, m, string(r))
			}
			m.syncViewport()
			// The query row closed, which is where the picker's own key is
			// live: what a rewind to the row would take back.
			c := rewindPickerModel(t, width)
			c = sendText(t, c, "/rewind")
			updated, _ := c.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
			c = updated.(Model)
			c.syncViewport()
			return []golden.Panel{
				{Label: "the timeline · newest first, one row per turn", View: whole},
				{Label: "typed into · the run the query named is bold",
					View: strings.Join(m.pickerLines(), "\n")},
				{Label: "the query row closed · [d] reads what a rewind here takes back",
					View: strings.Join(c.pickerLines(), "\n")},
			}
		})
}

// TestGolden_RewindRow captures the row a rewind lands as, in both weights:
// the one that put files back and carries the mutation rail, and the one that
// only moved the window and carries neither.
func TestGolden_RewindRow(t *testing.T) {
	captureGolden(t, "rewind-row", "the rewind's return row", goldenWidths,
		func(width int) []golden.Panel {
			ret := rewindReturn{turn: 5, first: 6, last: 7, was: 62, now: 41, at: time.Now()}
			rec := func(path string, before, after int) changeset.Record {
				return changeset.Record{
					Path:         path,
					Before:       strings.Repeat("was\n", before),
					After:        strings.Repeat("now\n", after),
					BeforeExists: true, AfterExists: true,
				}
			}
			restored := changeset.Fold([]changeset.Turn{{Records: []changeset.Record{
				rec("internal/agent/loop.go", 24, 2),
				rec("internal/agent/rounds.go", 4, 1),
				rec("internal/cli/root.go", 2, 1),
			}}})
			row := func(r rewindReturn, folded changeset.Turn) string {
				m := newRewindModel(t)
				m.width, m.height = width, 40
				m.syncInputWidth()
				m.appendRewindRow(r, folded)
				return m.renderEntry(m.transcript[len(m.transcript)-1], width)
			}
			// As it lands after a real rewind: the fold the turns went into,
			// and the row under it.
			landed := func() string {
				m := newRewindModel(t)
				m.width, m.height = width, 40
				m.syncInputWidth()
				for _, turn := range []string{"find where rounds are counted", "cap rounds at the limit", "raise the cap"} {
					m = completeExchange(t, m, turn, "done")
				}
				m = sendText(t, m, "/rewind 1")
				var out []string
				for _, e := range m.transcript {
					if e.kind == entryRewound || (e.notice != nil && e.notice.Act != nil && e.notice.Act.Verb == rewindVerb) {
						out = append(out, strings.TrimRight(m.renderEntry(e, width), "\n"))
					}
				}
				return strings.Join(out, "\n")
			}
			return []golden.Panel{
				{Label: "both · the files came back and the window moved", View: row(ret, restored)},
				{Label: "talk only · nothing on the machine was touched",
					View: row(ret, changeset.Turn{})},
				{Label: "as it lands · the turns folded above the row", View: landed()},
			}
		})
}
