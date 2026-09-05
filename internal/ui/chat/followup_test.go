package chat

// The follow-up queue: alt+enter with a turn live queues the draft for
// after it; steering stays its own queue; a cancel holds rather than sends.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// queueChord is the follow-up queue's one chord (keys.Draft.Queue).
func queueChord() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
}

func TestFollowUp_QueuesWhileTurnIsLive(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.input.SetValue("and then update the docs")

	updated, _ := m.Update(queueChord())
	next := updated.(Model)

	if len(next.followUps) != 1 || next.followUps[0] != "and then update the docs" {
		t.Fatalf("expected one queued follow-up, got %v", next.followUps)
	}
	if len(next.steering) != 0 {
		t.Fatalf("a follow-up is not steering, got %v", next.steering)
	}
	if next.input.Value() != "" {
		t.Fatal("queueing should clear the draft")
	}
	if !strings.Contains(stripANSI(next.noticeLine()), "1 follow-up") {
		t.Fatalf("the notice rail should count the follow-up: %q", stripANSI(next.noticeLine()))
	}
}

func TestFollowUp_SentWhenTheTurnEnds(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.input.SetValue("and then update the docs")
	updated, _ := m.Update(queueChord())
	m = updated.(Model)

	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	if len(m.followUps) != 0 {
		t.Fatalf("the follow-up should have gone out, got %v", m.followUps)
	}
	if m.state != stateStreaming {
		t.Fatalf("the follow-up should have started the next turn, got state %d", m.state)
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleUser || last.Content != "and then update the docs" {
		t.Fatalf("expected the follow-up as the next user message, got %+v", last)
	}
}

func TestFollowUp_SteeringStillWinsTheTurnEnd(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.input.SetValue("later: docs")
	updated, _ := m.Update(queueChord())
	m = updated.(Model)
	m = sendText(t, m, "now: fix the test")

	updated, _ = m.Update(doneMsg{})
	m = updated.(Model)

	if len(m.followUps) != 1 {
		t.Fatalf("steering goes first; the follow-up should still be queued, got %v", m.followUps)
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Content != "now: fix the test" {
		t.Fatalf("expected the steering line to be injected, got %+v", last)
	}
}

func TestFollowUp_SurvivesCancelHeld(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.input.SetValue("and then update the docs")
	updated, _ := m.Update(queueChord())
	m = updated.(Model)

	m.cancelStreaming()

	if len(m.followUps) != 1 {
		t.Fatalf("a cancel must keep the follow-up, got %v", m.followUps)
	}
	if !m.followUpsHeld {
		t.Fatal("a cancel must hold the queue rather than send it")
	}
	notice := stripANSI(m.noticeLine())
	if !strings.Contains(notice, "follow-up held") {
		t.Fatalf("the rail should say the queue is held: %q", notice)
	}

	// The next turn ending must offer, not send.
	m2 := sendText(t, m, "try something else")
	updated, _ = m2.Update(doneMsg{})
	m2 = updated.(Model)
	if len(m2.followUps) != 1 {
		t.Fatalf("a held follow-up must not send itself, got %v", m2.followUps)
	}
}

func TestFollowUp_TheChordOnAnEmptyDraftPullsNewestBack(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.input.SetValue("first")
	updated, _ := m.Update(queueChord())
	m = updated.(Model)
	m.input.SetValue("second")
	updated, _ = m.Update(queueChord())
	m = updated.(Model)

	// The same chord on the empty draft is the pull.
	updated, _ = m.Update(queueChord())
	m = updated.(Model)

	if m.input.Value() != "second" {
		t.Fatalf("expected the newest follow-up back in the draft, got %q", m.input.Value())
	}
	if len(m.followUps) != 1 || m.followUps[0] != "first" {
		t.Fatalf("expected the older follow-up still queued, got %v", m.followUps)
	}
}

func TestFollowUp_BrokenTurnHoldsTheQueue(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.input.SetValue("deploy it")
	updated, _ := m.Update(queueChord())
	m = updated.(Model)

	updated, _ = m.endBrokenTurn()
	m = updated.(Model)
	if len(m.followUps) != 1 || !m.followUpsHeld {
		t.Fatalf("a broken turn must hold the queue, got %v held=%v", m.followUps, m.followUpsHeld)
	}
}

func TestFollowUp_LoadedConversationDropsTheQueue(t *testing.T) {
	m := steeringModel(t, mockStream)
	m.input.SetValue("deploy it")
	updated, _ := m.Update(queueChord())
	m = updated.(Model)
	m.cancelStreaming()

	m.loadConversation([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
	})
	if len(m.followUps) != 0 || m.followUpsHeld {
		t.Fatalf("a loaded conversation must drop the queue, got %v held=%v", m.followUps, m.followUpsHeld)
	}
}

func TestFollowUp_RefusesACommandDraft(t *testing.T) {
	m := steeringModel(t, mockStream)
	for _, draft := range []string{"!make test", "/compact"} {
		m.input.SetValue(draft)
		updated, _ := m.Update(queueChord())
		next := updated.(Model)
		if len(next.followUps) != 0 {
			t.Fatalf("%q must not queue as a message, got %v", draft, next.followUps)
		}
		if !strings.Contains(lastSystemText(next), "Not queued") {
			t.Fatalf("%q should be refused out loud, got %q", draft, lastSystemText(next))
		}
	}
}

func TestFollowUp_SentAfterARunCommand(t *testing.T) {
	m := runCapableModel("no blocks here")
	m = sendText(t, m, "!true")
	m = handover(t, m)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)

	// Queued while the command runs.
	m.input.SetValue("then commit it")
	updated, _ = m.Update(queueChord())
	m = updated.(Model)
	if len(m.followUps) != 1 {
		t.Fatalf("expected the follow-up queued during the run, got %v", m.followUps)
	}

	updated, _ = m.Update(cmdDoneMsg{runID: m.agent.RunID(), command: "true", output: "", exitCode: 0})
	m = updated.(Model)
	if len(m.followUps) != 0 || m.state != stateStreaming {
		t.Fatalf("the run's end should dispatch the follow-up, got %v state=%d", m.followUps, m.state)
	}
}
