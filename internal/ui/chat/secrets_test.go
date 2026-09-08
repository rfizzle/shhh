package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestSecret_MidTurnAnnouncementIsMachineSteering(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).WithSecrets(Secrets{
		Manage: func(args []string) (string, string) {
			return "stored FOO", "Secret FOO is now available for use in commands."
		},
	})
	m.state = stateStreaming
	m.summaryTarget = "initial task"

	// Executing /secret mid-turn queues an announcement for the model.
	updated, _ := m.secretCommand([]string{"set", "FOO=bar"})
	m = updated.(Model)

	if len(m.steering) != 1 {
		t.Fatalf("expected 1 queued steering item, got %d", len(m.steering))
	}
	if !m.steering[0].machine {
		t.Fatal("secret announcement must be marked as machine-authored")
	}
	announcement := m.steering[0].text

	// Queue a human steering message alongside it.
	m.steering = append(m.steering, steeringItem{text: "focus on unit tests", machine: false})

	turnBefore := m.turnCount
	if !m.injectSteering() {
		t.Fatal("steering injection failed")
	}

	// 1. Machine message should have Machine: true, human message Machine: false.
	agentMsgs := m.agent.Messages()
	var foundMachine, foundHuman bool
	for _, msg := range agentMsgs {
		if strings.Contains(msg.Content, announcement) {
			foundMachine = true
			if !msg.Machine {
				t.Errorf("announcement message should be flagged Machine: true, got %+v", msg)
			}
		}
		if msg.Content == "focus on unit tests" {
			foundHuman = true
			if msg.Machine {
				t.Errorf("human message should not be flagged Machine, got %+v", msg)
			}
		}
	}
	if !foundMachine || !foundHuman {
		t.Fatalf("expected both messages in agent context, foundMachine=%v, foundHuman=%v", foundMachine, foundHuman)
	}

	// 2. Machine message must not generate a checkpoint.
	for _, cp := range m.checkpoints {
		if strings.Contains(cp.preview, announcement) {
			t.Errorf("secret announcement should not record a checkpoint, got preview: %s", cp.preview)
		}
	}

	// 3. Machine message must be entrySystem, human message entryUser.
	var seenSys, seenUser bool
	for _, e := range m.transcript {
		if strings.Contains(e.text, announcement) {
			seenSys = true
			if e.kind != entrySystem {
				t.Errorf("announcement transcript row should be entrySystem, got %v", e.kind)
			}
		}
		if e.text == "focus on unit tests" {
			seenUser = true
			if e.kind != entryUser {
				t.Errorf("human steering transcript row should be entryUser, got %v", e.kind)
			}
		}
	}
	if !seenSys || !seenUser {
		t.Fatalf("transcript entries missing: seenSys=%v, seenUser=%v", seenSys, seenUser)
	}

	// 4. Target must not include the secret announcement.
	if strings.Contains(m.summaryTarget, announcement) {
		t.Fatalf("machine announcement must not extend the summary target: %s", m.summaryTarget)
	}
	if !strings.Contains(m.summaryTarget, "focus on unit tests") {
		t.Fatalf("human steering should extend the summary target: %s", m.summaryTarget)
	}

	// 5. Turn count only increments for human steering.
	if m.turnCount != turnBefore+1 {
		t.Errorf("turnCount = %d, want %d", m.turnCount, turnBefore+1)
	}
}

func TestSecret_RestoreSteeringExcludesMachineAnnouncements(t *testing.T) {
	m := New(nil, mockStream)
	m.steering = []steeringItem{
		{text: "Secret TOKEN is now available.", machine: true},
		{text: "run the linter", machine: false},
	}

	m.restoreSteering()

	got := m.input.Value()
	if strings.Contains(got, "Secret TOKEN") {
		t.Fatalf("restoreSteering should discard machine announcements, got %q", got)
	}
	if got != "run the linter" {
		t.Fatalf("restoreSteering should restore human text, got %q", got)
	}
	if len(m.steering) != 0 {
		t.Fatalf("steering queue should be cleared, got %+v", m.steering)
	}
}

func TestSecret_MidTurnAnnouncementRendersAsSystemRowAfterSaveAndLoad(t *testing.T) {
	db := rewindTestDB(t)
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).WithDB(db).WithSecrets(Secrets{
		Manage: func(args []string) (string, string) {
			return "stored KEY", "Secret KEY is now available."
		},
	})
	m.state = stateStreaming

	// Queue the announcement mid-turn
	updated, _ := m.secretCommand([]string{"set", "KEY=val"})
	m = updated.(Model)

	if !m.injectSteering() {
		t.Fatal("injectSteering failed")
	}

	// Save to DB
	slot := m.sessionName
	if err := db.SaveChat(slot, m.Messages()); err != nil {
		t.Fatalf("SaveChat failed: %v", err)
	}

	// Load session back from DB
	loaded, err := db.LoadChat(slot)
	if err != nil {
		t.Fatalf("LoadChat failed: %v", err)
	}

	reloaded := New(loaded[:1], mockStream).
		WithDB(db).
		WithResumedMessages(slot, loaded)

	var foundAnnouncement bool
	for _, e := range reloaded.transcript {
		if strings.Contains(e.text, "Secret KEY is now available.") {
			foundAnnouncement = true
			if e.kind != entrySystem {
				t.Fatalf("reloaded transcript row for secret announcement should be entrySystem, got %v", e.kind)
			}
		}
	}
	if !foundAnnouncement {
		t.Fatal("announcement not found in reloaded transcript")
	}
}
