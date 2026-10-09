package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// handoffAnswer is the writing every test's provider hands back.
const handoffAnswer = `{"summary":"Retry backoff capped; the timer test still flakes","done":["capped the backoff at 30s"],"open":["the timer test flakes under load"],"decisions":["no jitter: the cap is enough"]}`

// handoffProvider answers every handoff request with handoffAnswer and keeps
// the evidence it was handed.
type handoffProvider struct{ asked []string }

func (p *handoffProvider) Name() string { return "handoffs" }

func (p *handoffProvider) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.asked = append(p.asked, msgs[len(msgs)-1].Content)
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "h1", Name: agent.HandoffToolName, Arguments: handoffAnswer}},
		Done:      true,
	}
	close(ch)
	return ch, nil
}

// handoffConversation is a conversation with work in it.
func handoffConversation() []provider.Message {
	return []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "cap the retry backoff"},
		{Role: provider.RoleAssistant, Content: "capped it at 30s; the timer test still flakes"},
	}
}

// handoffModel is a sized session over a store, holding a conversation that
// has been saved to its slot, with the handoff writer wired on p.
func handoffModel(t *testing.T, p provider.Provider) (Model, *storage.DB) {
	t.Helper()
	db := rewindTestDB(t)
	m := New(handoffConversation(), mockStream, Wiring{
		DB:      db,
		Handoff: agent.NewHandoffWriter(p, agent.HandoffConfig{Model: "fast"}),
	})
	if err := db.SaveChat(m.sessionName, handoffConversation()); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	return updated.(Model), db
}

// submit types a line and enters it, handing back what it started.
func submit(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	m.input.SetValue(text)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(Model), cmd
}

// writeHandoff runs /handoff and lands its writing, handing back the model
// with the card up.
func writeHandoff(t *testing.T, m Model, line string) Model {
	t.Helper()
	m, cmd := submit(t, m, line)
	for _, msg := range drain(cmd) {
		if d, ok := msg.(handoffDraftMsg); ok {
			updated, _ := m.Update(d)
			m = updated.(Model)
			if m.state != stateHandoff {
				t.Fatalf("the writing landed and no card opened (state %v)", m.state)
			}
			return m
		}
	}
	t.Fatalf("%s started no writing", line)
	return m
}

// The handoff is asked for with the person's note, shown on a card, and kept
// on the slot only on the yes: esc keeps nothing and leaves the draft
// waiting, [n] drops it, and the yes writes it whole with its summary first.
func TestHandoff_IsWrittenOnYesOnly(t *testing.T) {
	p := &handoffProvider{}
	m, db := handoffModel(t, p)

	m = writeHandoff(t, m, "/handoff pick up at the flake")
	if len(p.asked) != 1 || !strings.Contains(p.asked[0], "pick up at the flake") || !strings.Contains(p.asked[0], "cap the retry backoff") {
		t.Fatalf("the writing should carry the note and the conversation, got %q", p.asked)
	}
	card := ansi.Strip(strings.Join(m.handoffLines(), "\n"))
	for _, want := range []string{"Retry backoff capped", "pick up at the flake", "the timer test flakes under load", "no jitter", "[e] edit first"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card is missing %q:\n%s", want, card)
		}
	}

	m, _ = pressKey(t, m, escK)
	if m.state == stateHandoff {
		t.Fatal("esc should take the card down")
	}
	if got, _ := db.ChatHandoff(m.sessionName); got != "" {
		t.Fatalf("esc kept a handoff: %q", got)
	}
	// The draft waits: /handoff opens it again without asking again.
	m, _ = submit(t, m, "/handoff")
	if m.state != stateHandoff || len(p.asked) != 1 {
		t.Fatalf("/handoff after esc should reopen the waiting draft (state %v, asked %d)", m.state, len(p.asked))
	}
	m, _ = pressKey(t, m, keyPress('n'))
	if m.state == stateHandoff || m.handoff.writing != nil {
		t.Fatal("[n] should drop the draft")
	}
	if got, _ := db.ChatHandoff(m.sessionName); got != "" {
		t.Fatalf("[n] kept a handoff: %q", got)
	}

	m = writeHandoff(t, m, "/handoff")
	m, _ = pressKey(t, m, keyPress('y'))
	got, _ := db.ChatHandoff(m.sessionName)
	if agent.HandoffFirstLine(got) != "Retry backoff capped; the timer test still flakes" || !strings.Contains(got, "open: the timer test flakes under load") {
		t.Fatalf("the yes should keep the handoff on the slot, got %q", got)
	}
	if m.handoff.kept != got {
		t.Fatalf("the session should carry what it kept, got %q", m.handoff.kept)
	}
}

// [e]'s edit comes back onto the card, and an edit that empties the file
// leaves the draft as it was.
func TestHandoff_AnEditComesBackOntoTheCard(t *testing.T) {
	m, db := handoffModel(t, &handoffProvider{})
	m = writeHandoff(t, m, "/handoff")
	m.leaveSurface()

	path := filepath.Join(t.TempDir(), "handoff.txt")
	if err := os.WriteFile(path, []byte("My own words\nopen: the flake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(handoffEditorDoneMsg{path: path})
	m = updated.(Model)
	if m.state != stateHandoff || m.handoff.writing.draft != "My own words\nopen: the flake" {
		t.Fatalf("the edit should be the draft, back on the card (state %v, draft %q)", m.state, m.handoff.writing.draft)
	}
	if got, _ := db.ChatHandoff(m.sessionName); got != "" {
		t.Fatal("an edit kept the handoff before the yes")
	}

	m.leaveSurface()
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, _ = m.Update(handoffEditorDoneMsg{path: empty})
	if got := updated.(Model).handoff.writing.draft; got != "My own words\nopen: the flake" {
		t.Fatalf("an emptied file should leave the draft as it was, got %q", got)
	}
}

// uncommittedTurn puts a closed turn whose changes nobody committed on the
// transcript.
func uncommittedTurn(m Model) Model {
	m.appendEntry(entry{kind: entryTurnClose, turn: 1, close: &components.TurnClose{
		Changes: &components.TurnChanges{Files: 2, Added: 14, Removed: 3}}})
	return m
}

// A quit over a turn's uncommitted work, with no handoff kept, offers one in
// a sentence on the quit's confirm; declining it quits.
func TestHandoff_TheQuitOffersOneAndNoQuits(t *testing.T) {
	m, _ := handoffModel(t, &handoffProvider{})
	m = uncommittedTurn(m)

	m, _ = submit(t, m, "/exit")
	if m.state != stateQuitConfirm || m.quitAsk == nil || !strings.Contains(m.quitAsk.Prompt, "handoff") {
		t.Fatalf("the quit should offer a handoff on its confirm (state %v)", m.state)
	}
	if m.quitting {
		t.Fatal("the offer quit before it was answered")
	}
	m, _ = pressKey(t, m, keyPress('n'))
	if !m.quitting {
		t.Fatal("declining the offer should quit")
	}

	// Twice the quit chord reaches the same offer.
	m, _ = handoffModel(t, &handoffProvider{})
	m = uncommittedTurn(m)
	m, _ = pressKey(t, m, ctrlC)
	m, _ = pressKey(t, m, ctrlC)
	if m.state != stateQuitConfirm || m.quitting {
		t.Fatalf("the second press should offer the handoff first (state %v, quitting %v)", m.state, m.quitting)
	}
}

// The offer's yes writes the handoff, and the card's yes keeps it and quits.
func TestHandoff_TheOffersYesKeepsItThenQuits(t *testing.T) {
	m, db := handoffModel(t, &handoffProvider{})
	m = uncommittedTurn(m)
	m, _ = submit(t, m, "/exit")
	m, cmd := pressKey(t, m, keyPress('y'))
	if m.quitting {
		t.Fatal("the yes should write the handoff before quitting")
	}
	for _, msg := range drain(cmd) {
		if d, ok := msg.(handoffDraftMsg); ok {
			updated, _ := m.Update(d)
			m = updated.(Model)
		}
	}
	if m.state != stateHandoff {
		t.Fatalf("the writing should land on the card (state %v)", m.state)
	}
	m, _ = pressKey(t, m, keyPress('y'))
	if !m.quitting {
		t.Fatal("the card's yes after the offer should quit")
	}
	if got, _ := db.ChatHandoff(m.sessionName); got == "" {
		t.Fatal("the handoff was not kept before the quit")
	}
}

// Nothing is offered where nothing is owed: a session that kept a handoff,
// and one with no uncommitted turn, quit as they always did.
func TestHandoff_NoOfferWhereNothingIsOwed(t *testing.T) {
	m, _ := handoffModel(t, &handoffProvider{})
	m, _ = submit(t, m, "/exit")
	if !m.quitting {
		t.Fatal("a session with no uncommitted turn should quit straight away")
	}
	m, _ = handoffModel(t, &handoffProvider{})
	m = uncommittedTurn(m)
	m.handoff.kept = "kept already"
	m, _ = submit(t, m, "/exit")
	if !m.quitting {
		t.Fatal("a session that kept a handoff should quit straight away")
	}
}

// A handoff reaches every copy of its conversation: a rewind's branch takes
// the one the slot holds, and so does a save moved off a taken slot, whether
// the handoff was kept this sitting or on the last one.
func TestHandoff_FollowsABranchAndAMovedSlot(t *testing.T) {
	// The branch.
	m := newRewindModel(t)
	db := rewindTestDB(t)
	m.wiring.DB = db
	m.bindStores()
	m = completeExchange(t, m, "first question", "answer one")
	m = completeExchange(t, m, "second question", "answer two")
	root := m.sessionName
	if err := db.SaveChat(root, m.Messages()); err != nil {
		t.Fatal(err)
	}
	const carried = "Left last sitting\nopen: the flake"
	if err := db.SetChatHandoff(root, carried); err != nil {
		t.Fatal(err)
	}
	if m.handoff.kept != "" {
		t.Fatal("setup: this sitting should have kept nothing")
	}
	m = sendText(t, m, "/rewind 1")
	branches, err := db.ListChatBranches(root)
	if err != nil || len(branches) != 2 {
		t.Fatalf("setup: want root and a branch, got %d (%v)", len(branches), err)
	}
	if got, _ := db.ChatHandoff(branches[1].Name); got != carried {
		t.Fatalf("the branch should hold the slot's handoff, got %q", got)
	}

	// The moved slot: another session takes the slot, and the autosave
	// writes the conversation, with the slot's handoff, somewhere else.
	path := t.TempDir() + "/test.db"
	mdb, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer mdb.Close()
	other, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	sys := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	mm := sendText(t, New(sys, mockStream, Wiring{DB: mdb}), "first question")
	updated, _ := mm.Update(tokenMsg{text: "an answer"})
	updated, save := updated.(Model).Update(doneMsg{})
	save()
	mm = updated.(Model)
	taken := mm.sessionName
	if err := mdb.SetChatHandoff(taken, carried); err != nil {
		t.Fatal(err)
	}
	if _, err := other.LoadChat(taken); err != nil {
		t.Fatal(err)
	}
	theirs := append(handoffConversation(), provider.Message{Role: provider.RoleUser, Content: "and more"})
	if err := other.SaveChat(taken, theirs); err != nil {
		t.Fatal(err)
	}
	move, ok := mm.autosaveCmd()().(autosaveMovedMsg)
	if !ok {
		t.Fatalf("the autosave should have moved the conversation")
	}
	if got, _ := mdb.ChatHandoff(move.to); got != carried {
		t.Fatalf("the moved slot should hold the handoff, got %q", got)
	}

	// And one kept this sitting rides the move without a read of the slot.
	mm.handoff.kept = "kept now"
	if err := other.SaveChat(taken, append(theirs, provider.Message{Role: provider.RoleUser, Content: "again"})); err != nil {
		t.Fatal(err)
	}
	mv, ok := mm.autosaveCmd()().(autosaveMovedMsg)
	if !ok {
		t.Fatal("the autosave should have moved the conversation again")
	}
	if got, _ := mdb.ChatHandoff(mv.to); got != "kept now" {
		t.Fatalf("the moved slot should hold what this sitting kept, got %q", got)
	}
}

// A sitting that resumed on a handoff and then did new work is owed a fresh
// one like any other, and the yes replaces the old.
func TestHandoff_ANewCloseReopensTheOffer(t *testing.T) {
	db := rewindTestDB(t)
	saved := handoffConversation()
	if err := db.SaveChat("yesterday", saved); err != nil {
		t.Fatal(err)
	}
	if err := db.SetChatHandoff("yesterday", "the old one\nopen: the flake"); err != nil {
		t.Fatal(err)
	}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{
		DB: db, Workspace: t.TempDir(),
		Handoff: agent.NewHandoffWriter(&handoffProvider{}, agent.HandoffConfig{Model: "fast"}),
	}).WithResumedMessages("yesterday", saved)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = updated.(Model)

	if m.handoffOwed() {
		t.Fatal("a resume with no new work owes nothing")
	}
	m = uncommittedTurn(m)
	if !m.handoffOwed() {
		t.Fatal("new work after a resume should owe a handoff, the old one notwithstanding")
	}
	m, _ = submit(t, m, "/exit")
	if m.state != stateQuitConfirm || !m.handoff.offered {
		t.Fatalf("the quit should offer a fresh handoff (state %v)", m.state)
	}
	m, cmd := pressKey(t, m, keyPress('y'))
	for _, msg := range drain(cmd) {
		if d, ok := msg.(handoffDraftMsg); ok {
			updated, _ := m.Update(d)
			m = updated.(Model)
		}
	}
	m, _ = pressKey(t, m, keyPress('y'))
	got, _ := db.ChatHandoff("yesterday")
	if got == "" || strings.Contains(got, "the old one") {
		t.Fatalf("the yes should replace the old handoff, got %q", got)
	}
}

// The quit chord pressed a second time over a card is offered the same
// handoff an idle quit is, rather than leaving with the work unwritten.
func TestHandoff_TheOfferReachesAQuitOverACard(t *testing.T) {
	m, _ := handoffModel(t, &handoffProvider{})
	m = uncommittedTurn(m)
	m, _ = pressKey(t, m, ctrlC)
	next, _ := m.openKeyList(keys.OnInput, nil)
	m = next.(Model)
	if m.state != stateKeyList || !m.armed.openOn(armQuit, quitChord()) {
		t.Fatalf("setup: want the key list over an open window (state %v)", m.state)
	}
	m, _ = pressKey(t, m, ctrlC)
	if m.quitting || m.state != stateQuitConfirm || !m.handoff.offered {
		t.Fatalf("the second press over a card should offer the handoff (state %v, quitting %v)", m.state, m.quitting)
	}
}

// An autosave that could not write the handoff says so, naming the slot,
// instead of leaving a next sitting that opens on nothing.
func TestHandoff_AFailedSaveIsSaid(t *testing.T) {
	m, db := handoffModel(t, &handoffProvider{})
	m.handoff.kept = "a handoff"
	slot := m.sessionName
	// The slot goes away between the conversation's write and the handoff's.
	msg := m.saveCmd(func() string {
		if err := db.DeleteChat(slot); err != nil {
			t.Fatal(err)
		}
		return ""
	})()
	failed, ok := msg.(autosaveHandoffFailedMsg)
	if !ok || failed.slot != slot {
		t.Fatalf("the autosave should report the handoff, got %#v", msg)
	}
	next, _ := m.Update(failed)
	m = next.(Model)
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entrySystem || !strings.Contains(last.text, slot) || !strings.Contains(last.text, "handoff could not be written") {
		t.Fatalf("a row should say the handoff was not written and name the slot, got %q", last.text)
	}
}
