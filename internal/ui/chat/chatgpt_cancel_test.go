package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The callback lands and the record is saved, then esc is pressed before the
// session has moved: the session stays on its provider, the header names it,
// and the note says where the session is. Nothing switches behind the
// header's back.
func TestPlan_EscAfterTheBrowserReturnedNeverMovesTheSession(t *testing.T) {
	m, f := planSession(t)
	switched := 0
	m.wiring.SwitchProvider = func(string) error { switched++; return nil }
	m.modelName = "gpt-4o"
	f.release <- ChatGPTSignIn{Email: "a@b.c", PlanGranted: true}

	next, cmd := m.startPlanSignIn()
	m = next.(Model)
	batch := cmd().(tea.BatchMsg)
	batch[0]()         // the sign-in runs and finishes
	done := batch[1]() // its done event is waiting, not yet delivered

	updated, _ := m.routeOverlay(overlayFor(stateChatGPT), tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	next, more := m.Update(done)
	m = deliver(t, next.(Model), more)

	if switched != 0 || m.providerName != "openai" {
		t.Fatalf("esc did not stop the switch: %d switches, provider %q", switched, m.providerName)
	}
	if strings.Contains(m.headerRow(100), planWord) {
		t.Fatal("the header names the plan for a session that is not on it")
	}
	last := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(last, "stays on openai") {
		t.Fatalf("note = %q", last)
	}
}
