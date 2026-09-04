package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
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
	root := m.sessionName

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
	// The focused row is the latest turn (options are latest-first).
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.state != stateInput {
		t.Fatal("picker should close after selecting")
	}
	if got := len(m.Messages()); got != 3 {
		t.Fatalf("selecting the latest turn should drop it, got %d messages", got)
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
	root := m.sessionName
	m = sendText(t, m, "/rewind 2")

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
	m.steering = []string{"actually do this instead"}
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

// The offer is the whole point: going back to before a turn and leaving what
// it wrote on disk is one answer of three, not the only one there is.
func TestRewind_OffersConversationFilesOrBoth(t *testing.T) {
	m, _, _ := rewindOfferModel(t)

	m = sendText(t, m, "/rewind 1")
	if m.state != statePick || m.picker == nil {
		t.Fatalf("a rewind with records after it should ask what to put back, got state %v", m.state)
	}
	if len(m.picker.Options) != 3 {
		t.Fatalf("expected three readings of a rewind, got %d", len(m.picker.Options))
	}
	labels := []string{m.picker.Options[0].Label, m.picker.Options[1].Label, m.picker.Options[2].Label}
	if labels[0] != "conversation" || labels[1] != "files" || labels[2] != "both" {
		t.Fatalf("unexpected offer: %v", labels)
	}
	if !strings.Contains(m.picker.Options[1].Desc, "2 files") ||
		!strings.Contains(m.picker.Options[1].Desc, "2 turns") {
		t.Fatalf("the files row should say what it would put back, got %q", m.picker.Options[1].Desc)
	}
	// The hole in the offer is named on the row that would write.
	if !strings.Contains(m.picker.Options[1].Desc, "command") {
		t.Fatalf("the card should say a command's changes are not recorded, got %q", m.picker.Options[1].Desc)
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

	m = sendText(t, m, "/rewind 1")
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
	m = press(t, m, "j") // conversation → files
	m = press(t, m, "enter")

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
	if string(content) != "one\n" {
		t.Fatalf("both turns should have been put back, got %q", content)
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
	if note := lastClose(t, m).Note; !strings.Contains(note, "rewind to before turn 1") {
		t.Fatalf("the restore should close with a row naming what it took back, got %q", note)
	}
}

// Both: the conversation goes back and the confirm for the files comes up
// behind it.
func TestRewind_BothRewindsAndThenAsksAboutTheFiles(t *testing.T) {
	m, kept, _ := rewindOfferModel(t)

	m = sendText(t, m, "/rewind 1")
	m = press(t, m, "j")
	m = press(t, m, "j") // conversation → files → both
	m = press(t, m, "enter")

	if got := len(m.Messages()); got != 1 {
		t.Fatalf("both should have rewound the conversation, got %d messages", got)
	}
	if m.state != stateUndoConfirm {
		t.Fatalf("both should then ask about the files, got state %v", m.state)
	}
	m = press(t, m, "y")
	if content, err := os.ReadFile(kept); err != nil || string(content) != "one\n" {
		t.Fatalf("both should have put the files back, got %q (%v)", content, err)
	}
}

// Conversation: today's behaviour, and the message says the files were left.
func TestRewind_ConversationLeavesTheFiles(t *testing.T) {
	m, kept, _ := rewindOfferModel(t)

	m = sendText(t, m, "/rewind 1")
	m = press(t, m, "enter")

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

	m = sendText(t, m, "/rewind 1")
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

	m = sendText(t, m, "/rewind 1")
	if m.state != statePick || m.picker == nil || len(m.picker.Options) != 3 {
		t.Fatalf("an unreadable tree must still be offered all three answers, got state %v", m.state)
	}
	m = press(t, m, "j")
	m = press(t, m, "j") // conversation → files → both
	m = press(t, m, "enter")

	if m.state != stateUndoConfirm {
		t.Fatalf("the files half should still reach the confirm, got state %v", m.state)
	}
	if note := lastSystem(t, m); !strings.Contains(note, "cannot be read") {
		t.Fatalf("the message should warn what it could not check, got %q", note)
	}
}
