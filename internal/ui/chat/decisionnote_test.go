package chat

// The two answers that carry a sentence
// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// notedCardModel is a session holding a gated edit whose card has the
// keyboard: an arrival takes it, the handover buys the rest of the run.
func notedCardModel(t *testing.T) Model {
	t.Helper()
	executor := func(name string, args json.RawMessage) (string, error) {
		t.Fatalf("no tool may run without an answer, but %s did", name)
		return "", nil
	}
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	})
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"line one\nline two\n"}`},
	}})
	return handover(t, updated.(Model))
}

func lastToolMessage(t *testing.T, m Model) provider.Message {
	t.Helper()
	msgs := m.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleTool {
			return msgs[i]
		}
	}
	t.Fatal("no tool result in the conversation")
	return provider.Message{}
}

// The whole of the story: the model is told what the reader was thinking
// instead of being told only no, and the transcript keeps the same sentence
// where the reader can read it back.
func TestDenyNoted_TheSentenceIsWhatTheModelIsToldAndWhatTheRowKeeps(t *testing.T) {
	const why = "not that file — the generated one is written by make"

	var decisions [][2]string
	m := notedCardModel(t).WithObserver(observe.Observer{
		Decision: func(_ observe.Pos, decision, reason string) {
			decisions = append(decisions, [2]string{decision, reason})
		},
	})
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'N', Text: "N"})
	m = updated.(Model)
	if m.decisionNote == nil || m.decisionNote.allow {
		t.Fatal("the shifted deny should open the field on the deny side")
	}
	if m.pendingApproval == nil {
		t.Fatal("opening the field must settle nothing")
	}
	m = typeInto(t, m, why)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if got := lastToolMessage(t, m).Content; got != "error: "+why {
		t.Fatalf("the model should be told the reader's sentence and nothing else, got %q", got)
	}
	for _, boilerplate := range []string{"declined this tool call", "declined to run this command"} {
		if strings.Contains(lastToolMessage(t, m).Content, boilerplate) {
			t.Fatalf("a noted denial must not also carry %q", boilerplate)
		}
	}

	// It is still the reader's denial, drawn as one, with the sentence folded
	// under the row rather than clipped into the outcome column.
	row := m.transcript[len(m.transcript)-1]
	if row.kind != entryTool || row.deniedBy != decidedByYou || row.denyRule != "" {
		t.Fatalf("a noted denial is still yours and not a rule's, got %+v", row)
	}
	// And the record says so too: a sentence does not turn the reader's
	// denial into a rule's (approvals-and-safety.md#denials-are-two-different-facts).
	if want := [2]string{observe.DecisionDeny, observe.ReasonUser}; len(decisions) != 1 || decisions[0] != want {
		t.Fatalf("the record should hold one %v, got %v", want, decisions)
	}
	if row.denyNote != why {
		t.Fatalf("the row should keep the sentence verbatim, got %q", row.denyNote)
	}
	detail := m.activityRowDetail(row, false)
	if strings.Contains(detail.Outcome, why) {
		t.Fatalf("the sentence belongs under the row, not in its outcome: %q", detail.Outcome)
	}
	if len(detail.Detail) != 1 || detail.Detail[0] != why {
		t.Fatalf("the row's expansion should carry the sentence verbatim, got %q", detail.Detail)
	}

	row.expanded = true
	if view := m.activityRowDetail(row, false).View(m.contentWidth()); !strings.Contains(ansi.Strip(view), why) {
		t.Fatalf("the expanded row should show the sentence:\n%s", view)
	}
}

// The reflex path: a reader who has been pressing the shifted letter for a
// year presses it and presses enter, and the model reads exactly what it read
// before this existed. Byte for byte, for each of the sentences a decline can
// carry.
func TestDenyNoted_AnEmptyNoteIsTodaysDenialByteForByte(t *testing.T) {
	edit := func(t *testing.T) Model {
		t.Helper()
		return notedCardModel(t)
	}
	command := func(t *testing.T) Model {
		t.Helper()
		var bare, contained []string
		return runExecApproval(t, containedModel(t, &bare, &contained, "contained: bwrap"))
	}
	for _, tc := range []struct {
		name string
		open func(*testing.T) Model
		want string
	}{
		{"a tool call", edit, "error: the user declined this tool call"},
		{"a command", command, "error: the user declined to run this command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plain := press(t, tc.open(t), "n")
			if got := lastToolMessage(t, plain).Content; got != tc.want {
				t.Fatalf("the plain denial changed: %q", got)
			}
			noted := press(t, press(t, tc.open(t), "N"), "enter")
			if got := lastToolMessage(t, noted).Content; got != tc.want {
				t.Fatalf("an empty note should be the plain denial, got %q", got)
			}
			row := noted.transcript[len(noted.transcript)-1]
			if row.denyNote != "" {
				t.Fatalf("an empty note is not a note, got %q", row.denyNote)
			}
			// And a sentence replaces the fixed one rather than joining it,
			// on this variant as on the other: the command card is the one
			// that would otherwise say no a second way.
			told := press(t, typeInto(t, press(t, tc.open(t), "N"), "wrong directory"), "enter")
			if got := lastToolMessage(t, told).Content; got != "error: wrong directory" {
				t.Fatalf("the sentence should be the whole of it, got %q", got)
			}
		})
	}

	// The third sentence belongs to a proposal answered on its own surface,
	// which offers no note at all — so it is the one variant this story can
	// only leave alone, and the test says so rather than leaving it untested.
	m, _ := memoryModel(t, agent.ModeManual)
	updated, _ := m.Update(rememberCall())
	m = press(t, handover(t, updated.(Model)), "esc")
	const want = "error: the user declined to save this memory; do not re-propose it this session"
	if got := lastToolMessage(t, m).Content; got != want {
		t.Fatalf("the memory decline changed: %q", got)
	}
}

// The allow side reaches the same round the reader had the thought in, as
// their own words rather than the session's.
func TestAllowNoted_TheSentenceGoesOutAsYourOwnSteer(t *testing.T) {
	const next = "use the staging bucket for the next one"
	var ran []string
	executor := func(name string, args json.RawMessage) (string, error) {
		ran = append(ran, name)
		return "wrote 2 lines", nil
	}
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	})
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"line one\nline two\n"}`},
	}})
	m = handover(t, updated.(Model))

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	m = updated.(Model)
	if m.decisionNote == nil || !m.decisionNote.allow {
		t.Fatal("the shifted allow should open the field on the allow side")
	}
	if m.state != stateConfirmRun {
		t.Fatalf("opening the field must not run the act, got state %d", m.state)
	}
	m = typeInto(t, m, next)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		c()
	}

	if len(ran) != 1 {
		t.Fatalf("the act should have run, got %v", ran)
	}
	if len(m.steering) != 1 {
		t.Fatalf("the sentence should be one steer, got %d", len(m.steering))
	}
	if m.steering[0].text != next {
		t.Fatalf("the steer should be the sentence verbatim, got %q", m.steering[0].text)
	}
	if m.steering[0].machine {
		t.Fatal("the reader wrote it, so it is not a machine-authored steer")
	}

	// An allow with nothing written is the plain allow: no steer, and the
	// next round is not handed an empty sentence.
	ran = nil
	m2 := notedCardModel(t)
	m2 = press(t, press(t, m2, "Y"), "enter")
	if len(m2.steering) != 0 {
		t.Fatalf("an empty note is not a steer, got %v", m2.steering)
	}
}

// While the field holds the keyboard every letter is text. The keys in the
// table are the ones that answer this card when it does not: the digits the
// queue answers, and the three letters the run and its qualifiers print.
func TestDecisionNote_EveryLetterIsTextWhileTheFieldIsOpen(t *testing.T) {
	for _, key := range []string{"1", "2", "9", "d", "t", "a", "y", "n", "A"} {
		t.Run(key, func(t *testing.T) {
			m := press(t, notedCardModel(t), "N")
			before := m.state
			m = press(t, m, key)
			if m.decisionNote == nil {
				t.Fatalf("%q closed the field", key)
			}
			if m.state != before {
				t.Fatalf("%q moved the session from state %d to %d", key, before, m.state)
			}
			if m.pendingApproval == nil {
				t.Fatalf("%q answered the decision", key)
			}
			if got := m.decisionNote.field.Value(); got != key {
				t.Fatalf("%q should be text in the field, got %q", key, got)
			}

			// Esc closes the field, and the decision is exactly where it was
			// — so the letter is a key again.
			m = press(t, m, "esc")
			if m.decisionNote != nil {
				t.Fatalf("esc should close the field")
			}
			if m.pendingApproval == nil || m.state != before {
				t.Fatal("esc must leave the decision waiting")
			}
			live := press(t, m, key)
			// The card's other surfaces count as movement too: [a] opens the
			// grants it can make and [A] the queue, and neither settles the
			// decision or moves the scroll (grant.go, queue.go).
			if live.state == before && live.pendingApproval != nil && live.decisionNote == nil &&
				live.grantChoice == nil && live.queueList == nil &&
				live.cardScroll == m.cardScroll {
				// Nothing moved: the key is inert now, which is only right
				// for the keys this card does not offer.
				if card := m.approvalCard(); offeredKey(card, key) {
					t.Fatalf("%q is on the card and did nothing once the field closed", key)
				}
			}
		})
	}
}

// A sentence longer than the row it is typed on scrolls inside the field, and
// the caret stays inside the card: a cursor drawn past the frame is a cursor
// in the transcript, pointing at a row nobody is typing into.
func TestDecisionNote_TheCaretStaysInsideTheCard(t *testing.T) {
	// The second width is under the card's own frame threshold, where the
	// rows are drawn bare and the field has almost no room: that is where a
	// field sized to a floor rather than to the room it has would put the
	// caret outside the card altogether.
	for _, width := range []int{80, 14} {
		m := notedCardModel(t)
		m.width, m.height = width, 40
		m.syncInputWidth()
		m = typeInto(t, press(t, m, "N"), strings.Repeat("no. ", 60))
		cur := m.confirmCursor(m.contentWidth())
		if cur == nil {
			t.Fatalf("w%d: an open field places the cursor", width)
		}
		rows := m.confirmPanelLines()
		if cur.Y < 0 || cur.Y >= len(rows) {
			t.Fatalf("w%d: the caret is on row %d of a %d-row panel", width, cur.Y, len(rows))
		}
		if got := ansi.StringWidth(ansi.Strip(rows[cur.Y])); cur.X >= got {
			t.Fatalf("w%d: the caret is at column %d of a %d-column row", width, cur.X, got)
		}
	}
}

// offeredKey reports whether the card advertises this keystroke in its run.
func offeredKey(card *components.ApprovalCard, key string) bool {
	for _, k := range card.KeyRun() {
		if k.Key == key {
			return true
		}
	}
	return false
}

// The grace window swallows the shifted letters alongside the plain ones: a
// capital from the tail of a buffered burst is the reflex the window is for,
// and a field it opened would be a mode nobody asked for.
func TestGrace_TheNotedAnswersAreDiscardedToo(t *testing.T) {
	for _, key := range []string{"y", "n", "Y", "N"} {
		if !(Model{}).graceDiscards(key) {
			t.Errorf("%q should be discarded while the grace window is open", key)
		}
	}
	for _, key := range []string{"esc", "ctrl+c"} {
		if (Model{}).graceDiscards(key) {
			t.Errorf("%q must stay live: the safe answer has to stay reachable", key)
		}
	}
}

// The card divides the run it printed among the keys it answers, with nothing
// left over, so a click on a wider run still lands on the key under it.
func TestNotedRun_ClicksLandOnTheKeyUnderThem(t *testing.T) {
	m := notedCardModel(t)
	card := m.approvalCard()
	row := ""
	for _, line := range strings.Split(card.View(m.contentWidth()), "\n") {
		if strings.Contains(ansi.Strip(line), "["+strings.Join(shownRun(card), "/")+"]") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("the card should print its run:\n%s", card.View(m.contentWidth()))
	}
	seen := map[string]bool{}
	for col := range ansi.StringWidth(ansi.Strip(row)) {
		if key, ok := card.KeyAt(row, col); ok {
			seen[key] = true
		}
	}
	for _, k := range card.KeyRun() {
		if !seen[k.Key] {
			t.Errorf("no cell of the printed run resolves to %q", k.Key)
		}
	}
}

func shownRun(card *components.ApprovalCard) []string {
	run := card.KeyRun()
	out := make([]string, len(run))
	for i, k := range run {
		out[i] = k.Shown
	}
	return out
}
