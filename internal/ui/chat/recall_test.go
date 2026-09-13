package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// resumedModel is a session that came back from storage: a conversation
// loaded before the first key is pressed, which is what --continue, --resume,
// /load and a branch switch all reduce to.
func resumedModel(t *testing.T, msgs []provider.Message) Model {
	t.Helper()
	m := New(msgs[:1], multiTokenStream("ok")).
		WithStartScreen(StartInfo{}).
		WithResumedMessages("", msgs)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return updated.(Model)
}

func pressUp(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	return updated.(Model)
}

func pressDown(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	return updated.(Model)
}

func TestResumedSession_RecallsWhatWasTyped(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first saved prompt"},
		{Role: provider.RoleAssistant, Content: "an answer"},
		{Role: provider.RoleUser, Content: "second saved prompt"},
		{Role: provider.RoleAssistant, Content: "another answer"},
	})

	m = pressUp(t, m)
	if m.input.Value() != "second saved prompt" {
		t.Fatalf("↑ on a resumed session should recall the newest prompt, got %q", m.input.Value())
	}
	m = pressUp(t, m)
	if m.input.Value() != "first saved prompt" {
		t.Fatalf("↑ again should walk back, got %q", m.input.Value())
	}
	m = pressDown(t, m)
	if m.input.Value() != "second saved prompt" {
		t.Fatalf("↓ should walk forward, got %q", m.input.Value())
	}
	m = pressDown(t, m)
	if m.input.Value() != "" {
		t.Fatalf("↓ past the newest entry should clear the draft, got %q", m.input.Value())
	}
}

// A prompt typed this sitting joins the recalled ones rather than replacing
// them: resuming does not start the history over.
func TestResumedSession_NewInputJoinsTheHistory(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "saved prompt"},
		{Role: provider.RoleAssistant, Content: "an answer"},
	})

	m = sendText(t, m, "typed this sitting")
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	m = pressUp(t, m)
	if m.input.Value() != "typed this sitting" {
		t.Fatalf("expected the newest prompt, got %q", m.input.Value())
	}
	m = pressUp(t, m)
	if m.input.Value() != "saved prompt" {
		t.Fatalf("expected the resumed prompt behind it, got %q", m.input.Value())
	}
}

// The user-role messages the session writes for itself are not lines anyone
// typed, so ↑ never offers them (recall.go). The steer is the one the old
// prefix list did not know about.
func TestResumedSession_SkipsWhatNobodyTyped(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: compactContextMessage("the session so far, summarised"), Machine: true},
		{Role: provider.RoleUser, Content: "the only thing anyone typed"},
		{Role: provider.RoleAssistant, Content: "an answer"},
		{Role: provider.RoleUser, Content: commandContextMessage("go test ./...", "ok", 0, nil), Machine: true},
		{Role: provider.RoleUser, Content: continuePrompt, Machine: true},
		{Role: provider.RoleUser, Content: "You are editing a file the task did not ask about.", Machine: true},
	})

	if got := len(m.inputHistory); got != 1 {
		t.Fatalf("expected one recallable prompt, got %d: %q", got, m.inputHistory)
	}
	m = pressUp(t, m)
	if m.input.Value() != "the only thing anyone typed" {
		t.Fatalf("↑ recalled a line nobody typed: %q", m.input.Value())
	}
	// Nothing older to reach: the draft stays on the one real prompt.
	m = pressUp(t, m)
	if m.input.Value() != "the only thing anyone typed" {
		t.Fatalf("↑ past the oldest entry should hold, got %q", m.input.Value())
	}
}

// Loading a second conversation replaces the ring rather than concatenating
// two histories: the ring belongs to the conversation on screen.
func TestLoadConversation_ReplacesTheHistory(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "a prompt from the first conversation"},
	})
	m.loadConversation([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "a prompt from the second"},
	})

	if len(m.inputHistory) != 1 || m.inputHistory[0] != "a prompt from the second" {
		t.Fatalf("expected only the loaded conversation's prompts, got %q", m.inputHistory)
	}
	if m.historyIdx != len(m.inputHistory) {
		t.Fatalf("a fresh load should not be mid-browse: idx %d of %d", m.historyIdx, len(m.inputHistory))
	}
}

// recordInput's rules are the loaded conversation's rules, because it is the
// one that does the appending: a prompt repeated back to back is one entry.
func TestResumedSession_CollapsesRepeatedPrompts(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "try again"},
		{Role: provider.RoleAssistant, Content: "an answer"},
		{Role: provider.RoleUser, Content: "try again"},
	})

	if got := len(m.inputHistory); got != 1 {
		t.Fatalf("expected consecutive repeats to collapse, got %d: %q", got, m.inputHistory)
	}
}

// Recall belongs to the draft wherever the draft has the keyboard (
// A turn in flight keeps the input live, so it keeps recall.
func TestRecall_WorksWhileTheTurnRuns(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, multiTokenStream("ok"))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	m = sendText(t, m, "the prompt this turn is answering")
	if m.state != stateStreaming {
		t.Fatalf("the turn should be in flight, got state %d", m.state)
	}
	m = pressUp(t, m)
	if m.input.Value() != "the prompt this turn is answering" {
		t.Fatalf("↑ mid-turn should recall, got %q", m.input.Value())
	}
	m = pressDown(t, m)
	if m.input.Value() != "" {
		t.Fatalf("↓ mid-turn should walk back out of the history, got %q", m.input.Value())
	}
	if m.state != stateStreaming {
		t.Fatalf("recall must not disturb the turn, state is now %d", m.state)
	}
}

// A decision that arrived on top of a sentence has not taken the keyboard, so
// the draft keeps every key it offers — recall included. The sentence
// itself is how the draft gets empty enough to recall into: enter queues it
// for the next round and leaves the card waiting, and ↑ brings it back.
func TestRecall_WorksWhileADecisionWaitsForTheKeyboard(t *testing.T) {
	m := interruptedModel(t, "queue this")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.decisionUngated() {
		t.Fatal("queueing a line must not hand the card the keyboard")
	}
	if m.input.Value() != "" {
		t.Fatalf("a queued sentence leaves the draft, got %q", m.input.Value())
	}

	m = pressUp(t, m)
	if m.input.Value() != "queue this" {
		t.Fatalf("↑ under an ungated card should recall, got %q", m.input.Value())
	}
	if m.state != stateConfirmRun {
		t.Fatalf("recall must not answer or dismiss the card, state is now %d", m.state)
	}
}

// Once the card has the keyboard, it has ↑ too: the handover moves every key,
// and the draft is not what is being typed into any more.
func TestRecall_StopsAtTheHandover(t *testing.T) {
	m := interruptedModel(t, "queue this")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m = handover(t, m)

	m = pressUp(t, m)
	if m.input.Value() != "" {
		t.Fatalf("↑ belongs to the card once it holds the keyboard, got %q", m.input.Value())
	}
}

// A surface that took the screen took ↑ with it: reading mode moves its own
// cursor, and the draft underneath is not recalling anything.
func TestRecall_StaysOutOfASurfaceThatHasTheKeyboard(t *testing.T) {
	m := focusModel(t)
	m.recordInput("an earlier prompt")

	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+o should enter reading mode, got state %d", m.state)
	}
	m = pressUp(t, m)
	if m.input.Value() != "" {
		t.Fatalf("↑ in reading mode must not reach the draft, got %q", m.input.Value())
	}
}

// An empty conversation is an empty ring, and ↑ leaves the draft alone.
func TestResumedSession_NothingToRecall(t *testing.T) {
	m := resumedModel(t, []provider.Message{{Role: provider.RoleSystem, Content: "sys"}})

	m.input.SetValue("")
	m = pressUp(t, m)
	if m.input.Value() != "" {
		t.Fatalf("↑ with nothing to recall should leave the draft empty, got %q", m.input.Value())
	}
}

// A log pasted into a sentence and sent comes back with the sentence: ↑
// stages the paste again, so the fold in the draft opens, prices and rides
// out the way it did before the send (recall.go).
func TestRecall_RestagesTheSentPaste(t *testing.T) {
	log := strings.Repeat("loop_test.go:44: round 26 reached, still running\n", 11)
	updated, _ := frameModel(t, 100, 40).Update(tea.PasteMsg{Content: log})
	m := updated.(Model)
	m.input.InsertString(" fix the exit condition")
	sentence := m.input.Value()

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if len(m.attachments) != 0 {
		t.Fatalf("the send takes the staging area with it, %d left", len(m.attachments))
	}

	m = pressUp(t, m)
	if got := m.input.Value(); got != sentence {
		t.Fatalf("↑ should bring the sentence back as it was sent, got %q, want %q", got, sentence)
	}
	if len(m.attachments) != 1 || m.attachments[0].Name != "paste-1.txt" {
		t.Fatalf("the recalled fold should stage its paste again, got %v",
			attachment.Names(m.attachments))
	}
	if got := string(m.attachments[0].Data); got != log {
		t.Fatalf("the restaged paste is not the log that was sent: %q", got)
	}
	if m.pasteCost() == "" {
		t.Fatal("a recalled paste is priced on the vitals rail like any other")
	}

	// ↓ back out to the empty draft takes it with it: a paste whose fold has
	// left the draft is bytes the next message would carry and never mention.
	m = pressDown(t, m)
	if m.input.Value() != "" || len(m.attachments) != 0 {
		t.Fatalf("walking out of the history should drop what it staged: %q, %d staged",
			m.input.Value(), len(m.attachments))
	}
}

// A conversation loaded from storage keeps the names of what rode rather than
// the bytes, so there is nothing to stage from and the fold loses its quotes:
// a count in plain words, not a mark with no key behind it.
func TestRecall_StripsAFoldWithNoPasteBehindIt(t *testing.T) {
	token := components.PasteToken("paste 1", 214)
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "this log says the loop never stops — " + token + " — fix it",
			Attachments: []provider.Attachment{{
				Kind: provider.AttachmentText, Name: "paste-1.txt", Data: []byte("the log\n")}}},
	})

	m = pressUp(t, m)
	want := "this log says the loop never stops — paste 1 · 214 lines — fix it"
	if got := m.input.Value(); got != want {
		t.Fatalf("a fold with nothing behind it should read as its count, got %q, want %q", got, want)
	}
	if len(m.attachments) != 0 {
		t.Fatalf("nothing was there to stage, %d staged", len(m.attachments))
	}
}

// The name a paste had can be taken by the time its sentence comes back — a
// draft cleared out from under a staged paste leaves one behind. The bytes are
// staged under the next free name and the sentence is renumbered to match, so
// the fold and the chip never disagree about which paste is which.
func TestRecall_RenumbersAFoldWhoseNameIsTaken(t *testing.T) {
	log := strings.Repeat("a line of the log\n", 11)
	m := frameModel(t, 100, 40)
	m.attachments = []provider.Attachment{{
		Kind: provider.AttachmentText, Name: "paste-1.txt", Data: []byte("some other paste\n")}}
	m.transcript = []entry{{kind: entryUser, text: "sent", pastes: []pasteFold{{
		label: "paste 1", lines: 11,
		body: strings.Split(strings.TrimSuffix(log, "\n"), "\n"),
	}}}}

	got := m.restageRecalled(components.PasteToken("paste 1", 11))
	if want := components.PasteToken("paste 2", 11); got != want {
		t.Fatalf("the recalled fold should be renumbered to %q, got %q", want, got)
	}
	if len(m.attachments) != 2 || m.attachments[1].Name != "paste-2.txt" {
		t.Fatalf("the paste should be staged under the free name, got %v",
			attachment.Names(m.attachments))
	}
	if got := string(m.attachments[1].Data); got != log {
		t.Fatalf("the restaged paste is not the log the row kept: %q", got)
	}
}

// Attached, the keyboard is pointed at a child and the staging area behind the
// draft is the orchestrator's, which is why every other key that reads it
// stands down (keyroute.go). Recall stages nothing there, and the fold in the
// sentence it brought back reads as its count.
func TestRecall_StagesNothingWhileAttached(t *testing.T) {
	log := strings.Repeat("loop_test.go:44: round 26 reached, still running\n", 11)
	updated, _ := frameModel(t, 100, 40).Update(tea.PasteMsg{Content: log})
	m := updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m.attachedTo = "writer-1"

	m = pressUp(t, m)
	if len(m.attachments) != 0 {
		t.Fatalf("a steer carries nothing, %d staged", len(m.attachments))
	}
	if want := "paste 1 · 11 lines"; m.input.Value() != want {
		t.Fatalf("a fold in a steer should read as its count, got %q, want %q",
			m.input.Value(), want)
	}
}
