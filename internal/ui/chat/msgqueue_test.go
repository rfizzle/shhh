package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

var (
	queueKey    = tea.KeyPressMsg{Code: tea.KeyF2}
	queueUp     = tea.KeyPressMsg{Code: tea.KeyUp}
	queueCancel = tea.KeyPressMsg{Code: 'x', Text: "x"}
	queueEnter  = tea.KeyPressMsg{Code: tea.KeyEnter}
)

// queuedModel is a session with a turn running and two messages typed at it:
// a steer that carries a picture staged for it, then a follow-up.
func queuedModel(t *testing.T, width int) Model {
	t.Helper()
	m := frameModel(t, width, 40)
	m.setTurnState(stateStreaming)
	m = stagePNG(t, m, "shot.png")
	m.input.InsertString("the header in this one is wrong too")
	m = pressKeys(t, m, queueEnter)
	m.input.SetValue("then run the whole suite")
	m = pressKeys(t, m, queueChord())
	if len(m.steering) != 1 || len(m.followUps) != 1 {
		t.Fatalf("the fixture should queue one steer and one follow-up: %+v / %+v", m.steering, m.followUps)
	}
	return m
}

// userTexts is every user message the conversation holds.
func userTexts(m Model) []string {
	var out []string
	for _, msg := range m.Messages() {
		if msg.Role == provider.RoleUser {
			out = append(out, msg.Content)
		}
	}
	return out
}

// A pulled-back message is the reader's draft again, with its picture back on
// the strip — and it is out of the queue, so the next boundary delivers
// nothing and sending it queues it afresh at the end.
func TestQueue_PullBackTakesTheMessageOutOfTheQueue(t *testing.T) {
	m := queuedModel(t, 100)
	m = pressKeys(t, m, queueKey)
	if m.state != stateQueue {
		t.Fatalf("the key should move the keyboard into the queue, state=%d", m.state)
	}
	m = pressKeys(t, m, queueUp, queueEnter)
	if m.state == stateQueue {
		t.Fatal("a pull-back should give the draft the keyboard back")
	}
	if !strings.Contains(m.input.Value(), "the header in this one is wrong too") {
		t.Fatalf("the steer should be back in the draft, got %q", m.input.Value())
	}
	if len(m.steering) != 0 || len(m.attachments) != 1 {
		t.Fatalf("the steer should leave the queue with its picture back on the strip: steering=%d staged=%d",
			len(m.steering), len(m.attachments))
	}
	before := len(userTexts(m))
	if m.injectSteering() {
		t.Fatal("the boundary should have nothing left to deliver")
	}
	if len(userTexts(m)) != before {
		t.Fatal("a pulled-back message reached the conversation")
	}

	// Sent again, it is a new message at the end, carrying the picture.
	m = pressKeys(t, m, queueEnter)
	// Steering goes at the next round, so it is listed ahead of the
	// follow-up whatever order the two were typed in.
	if rows := m.queuedRows(); len(rows) != 2 || rows[0].followUp || !rows[1].followUp {
		t.Fatalf("the edited steer should be queued again: %+v", rows)
	}
	if len(m.steering) != 1 || len(m.steering[0].atts) != 1 {
		t.Fatalf("the re-sent steer should carry its picture: %+v", m.steering)
	}
	if !m.injectSteering() {
		t.Fatal("the re-sent steer should be delivered")
	}
	sent := 0
	for _, text := range userTexts(m) {
		if strings.Contains(text, "the header in this one is wrong too") {
			sent++
		}
	}
	if sent != 1 {
		t.Fatalf("the steer should reach the conversation exactly once, got %d", sent)
	}
}

// A cancel takes the message and what rode with it, says so in one row, and
// leaves the rest of the queue where it was.
func TestQueue_CancelDropsTheMessageAndItsAttachments(t *testing.T) {
	m := queuedModel(t, 100)
	m = pressKeys(t, m, queueKey, queueUp, queueCancel)
	if len(m.steering) != 0 || len(m.attachments) != 0 {
		t.Fatalf("the steer and its picture should be gone: steering=%d staged=%d", len(m.steering), len(m.attachments))
	}
	if len(m.followUps) != 1 || m.state != stateQueue {
		t.Fatalf("the follow-up should stay queued with the keyboard still in the queue: %d, state=%d", len(m.followUps), m.state)
	}
	last := m.transcript[len(m.transcript)-1]
	if last.notice == nil || last.notice.Outcome != "cancelled" ||
		!strings.Contains(last.notice.Subject, "the header in this one is wrong too") ||
		!strings.Contains(last.notice.Subject, "Image#1") {
		t.Fatalf("the cancel should leave one row naming the message and its picture: %+v", last.notice)
	}
	if m.injectSteering() {
		t.Fatal("a cancelled message was delivered")
	}
	// Cancelling the last one gives the keyboard back.
	m = pressKeys(t, m, queueCancel)
	if len(m.followUps) != 0 || m.state == stateQueue {
		t.Fatalf("an empty queue should hand the keyboard back: followUps=%d state=%d", len(m.followUps), m.state)
	}
}

// The turn keeps running under the queue, so a message can be delivered while
// the pointer is on it. The key that lost the race says so and does nothing:
// the message is not put in the draft to be sent a second time.
func TestQueue_AMessageDeliveredFirstIsAlreadySent(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{queueEnter, queueCancel} {
		m := queuedModel(t, 100)
		m = pressKeys(t, m, queueKey, queueUp)
		m.input.SetValue("")
		if !m.injectSteering() {
			t.Fatal("the boundary should deliver the steer")
		}
		m = pressKeys(t, m, key)
		if m.state == stateQueue {
			t.Fatalf("%s on a sent message should hand the keyboard back", key)
		}
		if m.input.Value() != "" {
			t.Fatalf("%s on a sent message put %q in the draft", key, m.input.Value())
		}
		last := m.transcript[len(m.transcript)-1]
		if !strings.HasPrefix(last.text, "already sent") || !strings.Contains(last.text, "/rewind") {
			t.Fatalf("%s should say the message was already sent: %q", key, last.text)
		}
		if len(m.followUps) != 1 {
			t.Fatalf("%s should leave the follow-up alone, got %d", key, len(m.followUps))
		}
	}
}

// A turn that breaks gives its steering back to the draft, and a key aimed at
// a message it gave back says so rather than claiming it was sent.
func TestQueue_AMessageTheTurnGaveBackIsNotSent(t *testing.T) {
	m := queuedModel(t, 100)
	m = pressKeys(t, m, queueKey, queueUp)
	m.restoreSteering()
	m = pressKeys(t, m, queueCancel)
	last := m.transcript[len(m.transcript)-1]
	if !strings.HasPrefix(last.text, "not sent") || !strings.Contains(m.input.Value(), "the header in this one is wrong too") {
		t.Fatalf("the key should say the message is back in the draft: %q, draft %q", last.text, m.input.Value())
	}
}

// Esc leaves everything as it was: the draft, the queue and the strip.
func TestQueue_EscGoesBackToTheDraftUntouched(t *testing.T) {
	m := queuedModel(t, 100)
	m.input.SetValue("half a sentence")
	m = pressKeys(t, m, queueKey, queueUp, escK)
	if m.state == stateQueue || m.input.Value() != "half a sentence" {
		t.Fatalf("esc should give the draft back as it was: state=%d draft=%q", m.state, m.input.Value())
	}
	if len(m.queuedRows()) != 2 {
		t.Fatalf("esc should leave the queue alone, got %d", len(m.queuedRows()))
	}
}

// The queue chord on an empty draft is the pull-back aimed at the newest: the
// same removal, so what rode with it comes back too, and a line the session
// queued for itself is never what it takes.
func TestQueue_TheChordPullsTheNewestTheSameWay(t *testing.T) {
	m := queuedModel(t, 100)
	m = pressKeys(t, m, queueChord())
	if m.input.Value() != "then run the whole suite" || len(m.followUps) != 0 {
		t.Fatalf("the chord should pull the follow-up: draft=%q followUps=%d", m.input.Value(), len(m.followUps))
	}
	m.input.SetValue("")
	m.steering = append(m.steering, steeringItem{text: "an announcement", machine: true})
	m = pressKeys(t, m, queueChord())
	if !strings.Contains(m.input.Value(), "the header in this one is wrong too") || len(m.attachments) != 1 {
		t.Fatalf("the chord should pull the steer with its picture: draft=%q staged=%d", m.input.Value(), len(m.attachments))
	}
	if len(m.steering) != 1 || !m.steering[0].machine {
		t.Fatalf("the session's own line should stay queued: %+v", m.steering)
	}
}

// A cancelled turn gives its queued steering back to the draft, and what rode
// with it back to the strip, so nothing staged is lost with the turn.
func TestQueue_ACancelledTurnRestoresTheAttachments(t *testing.T) {
	m := queuedModel(t, 100)
	m.restoreSteering()
	if len(m.attachments) != 1 || !strings.Contains(m.input.Value(), "the header in this one is wrong too") {
		t.Fatalf("the steer and its picture should come back: draft=%q staged=%d", m.input.Value(), len(m.attachments))
	}
}

// TestGolden_Queue captures the queue in each state it is read in: drawn
// above the box while a turn runs, with the keyboard in it, with one message
// pulled back into the draft, and after a cancel.
func TestGolden_Queue(t *testing.T) {
	captureBoundedGolden(t, "queue", "the message queue", goldenWidths, func(width int) []golden.Panel {
		shown := queuedModel(t, width)
		selected := pressKeys(t, queuedModel(t, width), queueKey, queueUp)
		edited := pressKeys(t, queuedModel(t, width), queueKey, queueUp, queueEnter)
		cancelled := pressKeys(t, queuedModel(t, width), queueKey, queueCancel)
		cancelled.transcript = cancelled.transcript[len(cancelled.transcript)-1:]
		cancelled.invalidateRenderCache()
		return []golden.Panel{
			{Label: "queued · above the box while the turn runs", View: promptSurface(shown)},
			{Label: "the keyboard in the queue · the steer selected", View: strings.Join(selected.queueLines(), "\n")},
			{Label: "the steer pulled back into the draft · its picture on the strip", View: promptSurface(edited)},
			{Label: "the follow-up cancelled · its row, and the queue still open", View: cancelled.renderHistory() + "\n" + strings.Join(cancelled.queueLines(), "\n")},
		}
	})
}

// The route through the real program: two lines typed at a running turn,
// the keyboard moved into the queue, one cancelled and the other pulled back
// — and when the turn's answer lands, neither has reached the model.
func TestProgram_AQueuedLineIsCancelledOrPulledBackBeforeItIsSent(t *testing.T) {
	hold := make(chan struct{})
	p := &programProvider{turns: []programTurn{{text: "the scripted answer", hold: hold}}}
	tm := runProgram(t, New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p)))

	tm.Type("start the work")
	tm.Send(programEnter)
	tm.Type("fix the header too")
	tm.Send(programEnter)
	waitForText(t, tm, "steering   fix the header too")
	tm.Type("and the footer")
	tm.Send(programEnter)
	waitForText(t, tm, "steering   and the footer")

	tm.Send(queueKey)
	waitForText(t, tm, "queued · 2 messages")
	tm.Send(queueCancel)
	waitForText(t, tm, "cancelled")
	tm.Send(queueEnter)
	waitForText(t, tm, "▸ fix the header too")

	close(hold)
	waitForText(t, tm, "the scripted answer")
	finalFrame(t, tm)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, asked := range p.asked {
		if strings.Contains(asked.Content, "footer") || strings.Contains(asked.Content, "header") {
			t.Fatalf("a message taken out of the queue reached the model: %+v", asked)
		}
	}
	if len(p.asked) != 1 {
		t.Fatalf("the turn should have made one request, made %d", len(p.asked))
	}
}
