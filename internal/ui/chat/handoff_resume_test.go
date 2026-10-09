package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// A handoff belongs to its conversation: /load leaves what this sitting kept
// behind rather than carrying it onto the slot it opens, and /save takes it
// with the copy.
func TestHandoff_StaysWithItsConversation(t *testing.T) {
	m, db := handoffModel(t, &handoffProvider{})
	m = writeHandoff(t, m, "/handoff")
	m, _ = pressKey(t, m, keyPress('y'))
	kept := m.handoff.kept
	if kept == "" {
		t.Fatal("the yes kept nothing")
	}

	m, _ = submit(t, m, "/save named copy")
	if got, _ := db.ChatHandoff("named copy"); got != kept {
		t.Fatalf("/save should take the handoff with the copy, got %q", got)
	}

	if err := db.SaveChat("other", handoffConversation()); err != nil {
		t.Fatal(err)
	}
	if err := db.SetChatHandoff("other", "the other conversation's own"); err != nil {
		t.Fatal(err)
	}
	m, _ = submit(t, m, "/load other")
	if m.sessionName != "other" || m.handoff.kept != "" {
		t.Fatalf("/load should leave the kept handoff behind (slot %q, kept %q)", m.sessionName, m.handoff.kept)
	}
	if save := m.autosaveCmd(); save != nil {
		save()
	}
	if got, _ := db.ChatHandoff("other"); got != "the other conversation's own" {
		t.Fatalf("a save after /load wrote over the loaded slot's handoff: %q", got)
	}
}

// A conversation reopened on a slot holding a handoff draws it as the
// sitting's first row, ahead of the resumed row, and the model is given it
// as the first message in front of the reading, labelled as the person's on
// its first line. The save leaves it out of the slot's conversation.
func TestResume_TheHandoffIsTheFirstRow(t *testing.T) {
	db := rewindTestDB(t)
	saved := handoffConversation()
	if err := db.SaveChat("yesterday", saved); err != nil {
		t.Fatal(err)
	}
	const handoff = "Retry backoff capped; the timer test still flakes\nopen: the timer test flakes under load"
	if err := db.SetChatHandoff("yesterday", handoff); err != nil {
		t.Fatal(err)
	}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream,
		Wiring{DB: db, Workspace: t.TempDir()}).WithResumedMessages("yesterday", saved)

	msgs := m.Messages()
	first, _, _ := strings.Cut(msgs[1].Content, "\n")
	if msgs[1].Role != provider.RoleUser || first != agent.ResumeHandoffPrefix || !strings.Contains(msgs[1].Content, handoff) {
		t.Fatalf("the handoff should be the first user message, labelled, got %q", msgs[1].Content)
	}
	if !strings.HasPrefix(msgs[2].Content, resumeMessagePrefix) {
		t.Fatalf("the reading follows the handoff, got %q", msgs[2].Content)
	}
	if kept := agent.StripResumeContext(msgs); kept[1].Content != "cap the retry backoff" {
		t.Fatalf("the save should leave the handoff out with the reading, got %q", kept[1].Content)
	}

	var handoffRow, resumedRow = -1, -1
	for i, e := range m.transcript {
		switch {
		case e.kind == entrySystem && strings.HasPrefix(e.text, handoffRowLead):
			handoffRow = i
		case e.notice != nil && e.notice.Verb == resumeVerb:
			resumedRow = i
		}
	}
	if handoffRow < 0 || resumedRow != handoffRow+1 {
		t.Fatalf("the handoff should be the sitting's first row, ahead of the resumed row (handoff %d, resumed %d)", handoffRow, resumedRow)
	}
	for _, e := range m.transcript[:handoffRow] {
		if e.kind == entrySystem {
			t.Fatalf("a row of this sitting stands ahead of the handoff: %+v", e)
		}
	}
	if row := m.transcript[handoffRow]; row.toolResult != handoff || !row.expanded {
		t.Fatalf("the row should carry the handoff whole and open, got %+v", m.transcript[handoffRow])
	}
}

// TestGolden_Handoff captures the handoff's four surfaces: the start screen's
// resume offer naming it, the card a writing lands on, the sentence the quit
// offers one in, and the sitting a resume opens with it as the first row.
func TestGolden_Handoff(t *testing.T) {
	const text = "Retry backoff capped; the timer test still flakes\n" +
		"note: pick up at the flake\n" +
		"done: capped the backoff at 30s, read from retry.max_wait and honoured on every provider\n" +
		"open: the timer test flakes under load\n" +
		"decided: no jitter: the cap is enough\n" +
		"files: internal/agent/retry.go, internal/agent/retry_test.go\n" +
		"item: retry-cap — Cap the retry backoff"
	captureGolden(t, "handoff", "the handoff: the resume offer, the card, the quit's offer and the resumed sitting", goldenWidths, func(width int) []golden.Panel {
		info := startFixture()
		info.Recent.Name = "retry-backoff"
		info.Recent.Handoff = agent.HandoffFirstLine(text)
		start := frameModelWith(t, width, 40, Wiring{Start: new(info)})

		db := rewindTestDB(t)
		wired := Wiring{DB: db, Workspace: t.TempDir(), Handoff: agent.NewHandoffWriter(&handoffProvider{}, agent.HandoffConfig{Model: "fast"})}
		card := frameModelWith(t, width, 40, wired)
		card.sessionName = "retry-backoff"
		card.handoff.writing = &handoffWriting{draft: text}
		card.enterSurface(stateHandoff)

		offer := uncommittedTurn(frameModelWith(t, width, 40, wired))
		next, _ := offer.openHandoffOffer()

		if err := db.SaveChat("retry-backoff", handoffConversation()); err != nil {
			t.Fatal(err)
		}
		if err := db.SetChatHandoff("retry-backoff", text); err != nil {
			t.Fatal(err)
		}
		resumed := frameModelWith(t, width, 40, wired).WithResumedMessages("retry-backoff", handoffConversation())
		return []golden.Panel{
			{Label: "the start screen · the resume offer names the handoff", View: start.renderHistory()},
			{Label: "the card · the session's draft, around the note", View: card.panelView()},
			{Label: "the quit · uncommitted work and no handoff", View: next.(Model).panelView()},
			{Label: "the resumed sitting · the handoff is the first row", View: resumed.renderHistory()},
		}
	})
}

// The start screen's resume offer carries the handoff's first line as its
// detail.
func TestStart_TheResumeOfferNamesTheHandoff(t *testing.T) {
	info := startFixture()
	info.Recent.Handoff = "Retry backoff capped; the timer test still flakes"
	rows, _ := startSuggestions(info, false, false, "", nil)
	if rows[0].Title != "pick up (last session)" || rows[0].Detail != info.Recent.Handoff {
		t.Fatalf("the resume offer should carry the handoff's first line, got %+v", rows[0])
	}
	info.Recent.Held = true
	rows, _ = startSuggestions(info, false, false, "", nil)
	if rows[0].Detail != info.Recent.Handoff+" · elsewhere" {
		t.Fatalf("a held offer still says why it is this one, got %q", rows[0].Detail)
	}
}
