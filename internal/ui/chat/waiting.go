package chat

// The session waiting on a person
// (docs/interface/surfaces.md#when-you-are-not-there).
//
// A card that landed with an empty draft used to take the keyboard and
// appear at the bottom of the screen, and nothing else on the screen
// changed: the header kept its tone, the transcript kept its colours, the
// vitals rail went on looking like a rail under a working turn. The only
// cues that the session had stopped needing shhh and started needing a
// person pointed out of the window — the tab's glyph, and a desktop
// notification that fires only when the terminal says it is not in front.
// Inside a focused window, waiting looked exactly like working.
//
// So the wait is one fact the session keeps — when it opened — and four
// surfaces read it: the held call's own row in the transcript, the frame's
// top rail, the tab, and the bell. Each says the same count and the same
// clock, because they are one value rendered four times and never four
// counters; a title that said two over a rail that said three would be a
// bug by definition.
//
// The wait is a span, not a card. It opens when a decision first lands and
// closes when nothing is left to answer, so a queue of three is one wait:
// the bell rings once at its opening, the clock runs across the cards, and
// the second card arriving on the first's answer is the queue advancing
// rather than a new summons. That is why the stamp is read off the model
// before against the model after in Update's tail, the way the notification
// is (notify.go), rather than set by any one of the handlers that raise a
// card — three of them are cancellations, and none of them knows whether a
// wait is already open.

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// decisionWaiting reports whether the session is stopped on a decision of
// the reader's: an approval, a plan, the model's own question — on the card
// or handed to the draft — or a child's routed ask. It reads the turn's
// state rather than the screen's, because a card the reader has opened the
// full view over is still what the session is waiting on.
func (m Model) decisionWaiting() bool {
	switch m.turnState() {
	case stateConfirmRun, statePlanApprove, stateQuestion:
		return true
	}
	return len(m.childAsks) > 0
}

// waitOpen reports whether the wait is still going: a decision on screen,
// or one queued behind the answer the reader just gave. The queue is what
// makes three cards one wait — between the first's answer and the second's
// arrival the head of the queue runs, nothing is on screen, and the wait
// has not ended.
func (m Model) waitOpen() bool {
	if m.decisionWaiting() {
		return true
	}
	return m.agent != nil && len(m.agent.PendingApprovals()) > 0
}

// trackWait keeps the stamp true after a message: opened on the first
// decision to land, held across the queue, cleared once nothing is left.
func (m *Model) trackWait() {
	switch {
	case m.decisionWaiting():
		if m.waitingSince.IsZero() {
			m.waitingSince = time.Now()
		}
	case !m.waitOpen():
		m.waitingSince = time.Time{}
	}
}

// waitedFor is how long the session has stood on the reader's answer; zero
// when it is not.
func (m Model) waitedFor() time.Duration {
	if m.waitingSince.IsZero() {
		return 0
	}
	return time.Since(m.waitingSince)
}

// waitLabel is the wait as every surface prints it. It is coarser than a
// call's clock on purpose: a call is measured in tenths because the tenths
// are what a slow test costs, and a wait is measured in the units a reader
// coming back from a meeting thinks in. Under a second it is nothing, so a
// card that just landed says only that it is waiting.
func waitLabel(d time.Duration) string {
	switch {
	case d < time.Second:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// waitingChip is the frame's word for the wait: how many are waiting under
// the glyph every gated state wears, and how long the session has stood
// idle once that is a second or more. The wait is not a fifth phase — a
// phase is something the turn is doing and this is the turn doing nothing —
// so it takes the phase's slot rather than a word in the phase vocabulary
// (docs/interface/surfaces.md#the-input-frame).
func (m Model) waitingChip() string {
	chip := fmt.Sprintf("⏸ %d waiting", m.waitingCount())
	if label := waitLabel(m.waitedFor()); label != "" {
		chip += " · idle " + label
	}
	return chip
}

// bellCmd is the bell this transition earns, or nil: one ring at the
// opening of a wait. prev is the model the message arrived at; the receiver
// is the model it produced, with the stamp already tracked.
//
// It rides the notification switch and not a switch of its own, the way the
// tab's progress light does: the same promise in a different channel, and a
// reader who turned the summons off did not mean "but ring". It does not
// ride the focus gate — the bell is the summons for a window that is in
// front, which is the one the notification cannot reach. A turn finishing
// opens no wait, so it rings nothing; you asked shhh to work, not to talk.
func (m Model) bellCmd(prev Model) tea.Cmd {
	if !m.notifyOn || m.quitting {
		return nil
	}
	if !prev.waitingSince.IsZero() || m.waitingSince.IsZero() {
		return nil
	}
	return m.caps.Bell()
}

// waitingRow is the held call's own row, drawn under the transcript while
// the decision about it waits: the last row above the rule that names the
// card, so the card sits under its cause and the eye crosses nothing to get
// from one to the other. The outcome says who it is waiting for and the
// duration counts the wait rather than any act — nothing is running, and a
// clock that implied otherwise would be the one lie on the screen
// (docs/interface/surfaces.md#the-activity-row).
//
// It is drawn live rather than landed as an entry, because the row the
// answer leaves goes at the call's place in its round (turn.go) and carries
// the account of the answer; a row landed now would have to be taken back.
// Empty where the decision is not a call — a plan, a memory proposal — or
// where the call is a child's and its lane already says so.
func (m Model) waitingRow(width int) string {
	req := m.pendingApproval
	if req == nil || m.memoryAsk != nil {
		return ""
	}
	switch m.turnState() {
	case stateConfirmRun, stateQuestion:
	default:
		return ""
	}
	row := components.ActivityRow{
		State:    components.ActivityWaiting,
		Outcome:  components.OutcomeWaiting,
		Duration: waitLabel(m.waitedFor()),
		Frame:    m.spinFrame,
	}
	if req.kind == approvalExec {
		row.Kind = components.ActivityCommand
		row.Verb = "run"
		row.Target = firstLine(req.command)
	} else {
		row.Kind = m.activityKind(req.call.Name)
		row.Verb = activityVerbFor(req.call.Name, req.call.Arguments)
		row.Target = digest.Arg(req.call.Name, req.call.Arguments)
	}
	return row.View(width)
}
