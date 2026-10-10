package chat

// The approval card's register: two answers, the field, the list, the try,
// the full view, the way out and the key list, and nothing else
// (docs/interface/surfaces.md#the-approval-card). And the note each answer
// carries, which is the draft
// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// draftedCardModel is a session whose gated edit landed on a sentence in the
// draft, handed the keyboard: the reader was writing, the card arrived, and
// they gave it the keyboard to answer it. An empty draft is the card that
// took the keyboard by arriving.
func draftedCardModel(t *testing.T, executor ToolExecutor, draft string) Model {
	t.Helper()
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	})
	m.input.SetValue(draft)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"line one\nline two\n"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("the gated call should have raised a decision, got %d", m.state)
	}
	if draft == "" {
		return m
	}
	return handover(t, m)
}

// noRun is the executor of a card that must not run.
func noRun(t *testing.T) ToolExecutor {
	return func(name string, _ json.RawMessage) (string, error) {
		t.Fatalf("no tool may run without an answer, but %s did", name)
		return "", nil
	}
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

// The register is the nine: y, n, e, a, t, enter, esc, ? and the four
// scroll chords. The card's row of the register holds the keys the card
// answers itself — esc is the decision's way back to the draft and is
// answered before the card sees it — and g, which only a child's routed
// card offers, to go to the agent that asked. None of the old spellings is
// live: Y and N, the shifted queue, the full diff's v and V, the explain x.
func TestApproval_TheRegisterIsNineKeys(t *testing.T) {
	var got []string
	for _, b := range keys.OnApprovalCard.Surface().Bindings {
		got = append(got, b.Keys()...)
	}
	slices.Sort(got)
	want := []string{"?", "a", "e", "enter", "g", "n", "shift+down", "shift+left", "shift+right", "shift+up", "t", "y"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the card's register is %v, want %v", got, want)
	}
	// The card draws the way out with the rest, so the ninth key is on it.
	m := draftedCardModel(t, noRun(t), "")
	m = handover(t, m)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "[esc]") || !strings.Contains(view, "[enter] full diff") {
		t.Fatalf("the gated card should offer esc and the full view:\n%s", view)
	}
	// Every retired spelling is inert on a live card: nothing answered,
	// nothing opened, no field.
	for _, k := range []string{"Y", "N", "A", "v", "V", "x"} {
		live := draftedCardModel(t, noRun(t), "")
		live = handover(t, live)
		after := press(t, live, k)
		if after.state != stateConfirmRun || after.approval.request == nil ||
			after.approval.grant != nil || after.approval.list != nil || after.approval.edit != nil {
			t.Errorf("%q should do nothing on the card, got state %d", k, after.state)
		}
	}
	// Enter opens the full view and answers nothing.
	opened := press(t, handover(t, draftedCardModel(t, noRun(t), "")), "enter")
	if opened.state != stateDiffFull || opened.approval.request == nil {
		t.Fatalf("enter should open the full diff with the decision waiting, got state %d", opened.state)
	}
}

// The note is the draft. What the reader was writing when they answered goes
// out with the answer and leaves the box: in place of the fixed refusal on a
// no, as their own steer on a yes. An empty draft is the plain answer, byte
// for byte.
func TestApproval_TheDraftIsTheNote(t *testing.T) {
	const why = "not that file — the generated one is written by make"

	t.Run("a no says why", func(t *testing.T) {
		var decisions [][2]string
		m := draftedCardModel(t, noRun(t), why)
		m.wiring.Observer = observe.Observer{
			Decision: func(_ observe.Pos, decision, reason string) {
				decisions = append(decisions, [2]string{decision, reason})
			},
		}
		m = press(t, m, "n")
		if got := lastToolMessage(t, m).Content; got != "error: "+why {
			t.Fatalf("the model should be told the reader's sentence and nothing else, got %q", got)
		}
		if m.input.Value() != "" {
			t.Fatalf("the sentence went with the answer and should leave the draft, got %q", m.input.Value())
		}
		row := m.transcript[len(m.transcript)-1]
		if row.kind != entryTool || row.deniedBy != decidedByYou || row.denyRule != "" || row.denyNote != why {
			t.Fatalf("a noted denial is still yours, with the sentence under it, got %+v", row)
		}
		if want := [2]string{observe.DecisionDeny, observe.ReasonUser}; len(decisions) != 1 || decisions[0] != want {
			t.Fatalf("the record should hold one %v, got %v", want, decisions)
		}
		detail := m.activityRowDetail(row, false, m.contentWidth())
		if strings.Contains(detail.Outcome, why) || len(detail.Detail) != 1 || detail.Detail[0] != why {
			t.Fatalf("the sentence belongs under the row, verbatim: %+v", detail)
		}
	})

	t.Run("a yes says what next", func(t *testing.T) {
		const next = "use the staging bucket for the next one"
		var ran []string
		executor := func(name string, _ json.RawMessage) (string, error) {
			ran = append(ran, name)
			return "wrote 2 lines", nil
		}
		m := draftedCardModel(t, executor, next)
		updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
		m = updated.(Model)
		for _, c := range unwrapBatch(cmd) {
			c()
		}
		if len(ran) != 1 {
			t.Fatalf("the act should have run, got %v", ran)
		}
		if len(m.steering) != 1 || m.steering[0].text != next || m.steering[0].machine ||
			m.steering[0].kind != queuedNote || m.steering[0].id == 0 {
			t.Fatalf("the sentence should be one steer of the reader's own, got %+v", m.steering)
		}
		if m.input.Value() != "" {
			t.Fatalf("the sentence went with the answer and should leave the draft, got %q", m.input.Value())
		}
	})

	t.Run("an empty draft is the plain answer", func(t *testing.T) {
		m := press(t, draftedCardModel(t, noRun(t), ""), "n")
		if got := lastToolMessage(t, m).Content; got != "error: the user declined this tool call" {
			t.Fatalf("the plain denial changed: %q", got)
		}
		var bare, contained []string
		c := press(t, runExecApproval(t, containedModel(t, &bare, &contained, "contained: bwrap")), "n")
		if got := lastToolMessage(t, c).Content; got != "error: the user declined to run this command" {
			t.Fatalf("the plain command denial changed: %q", got)
		}
		if row := c.transcript[len(c.transcript)-1]; row.denyNote != "" {
			t.Fatalf("an empty draft is not a note, got %q", row.denyNote)
		}
	})

	// Attached to a child, what is typed is that child's steer, so the
	// session's answer leaves it where it is.
	t.Run("an attached draft is the child's", func(t *testing.T) {
		m := draftedCardModel(t, noRun(t), why)
		m.attachedTo = "reader-1"
		if note := m.takeDraftNote(); note != "" || m.input.Value() != why {
			t.Fatalf("an attached draft should stay the child's, took %q and left %q", note, m.input.Value())
		}
	})

	// A memory proposal answers on its own surface and carries no note, so
	// its refusal is the one this rule leaves alone.
	t.Run("a memory proposal keeps its sentence", func(t *testing.T) {
		m, _ := memoryModel(t, agent.ModeManual)
		updated, _ := m.Update(rememberCall())
		m = press(t, handover(t, updated.(Model)), "esc")
		const want = "error: the user declined to save this memory; do not re-propose it this session"
		if got := lastToolMessage(t, m).Content; got != want {
			t.Fatalf("the memory decline changed: %q", got)
		}
	})
}

// Esc is never a denial. On a card handed the keyboard it gives the keyboard
// back to the draft, the sentence still in it, and the request waits — no
// tool result, no record of an answer, no row.
func TestApproval_EscLeavesTheRequestWaiting(t *testing.T) {
	for _, draft := range []string{"", "half a thought"} {
		var decisions []string
		m := draftedCardModel(t, noRun(t), draft)
		m.wiring.Observer = observe.Observer{
			Decision: func(_ observe.Pos, decision, _ string) { decisions = append(decisions, decision) },
		}
		rows := len(m.transcript)
		m = press(t, m, "esc")
		if m.state != stateConfirmRun || m.approval.request == nil {
			t.Fatalf("draft %q: esc should leave the request waiting, got state %d", draft, m.state)
		}
		if !m.decisionUngated() {
			t.Fatalf("draft %q: esc should hand the keyboard back to the draft", draft)
		}
		if m.input.Value() != draft {
			t.Fatalf("draft %q: esc should leave the sentence where it was, got %q", draft, m.input.Value())
		}
		for _, msg := range m.Messages() {
			if msg.Role == provider.RoleTool {
				t.Fatalf("draft %q: esc must not answer the call: %+v", draft, msg)
			}
		}
		if len(decisions) != 0 || len(m.transcript) != rows {
			t.Fatalf("draft %q: esc should record nothing, got %v and %d new rows", draft, decisions, len(m.transcript)-rows)
		}
	}
}

// Ctrl+C with a card up stops the run, as it does everywhere: the turn ends
// and the call goes with it, abandoned by the stop rather than denied — the
// row reads stopped, the model is told the turn was cancelled, and the record
// holds no denial.
func TestApproval_CtrlCStopsTheRunNotTheRequest(t *testing.T) {
	for _, draft := range []string{"", "half a thought"} {
		var decisions []string
		m := draftedCardModel(t, noRun(t), draft)
		stopped := false
		m.cancel = func() { stopped = true }
		m.wiring.Observer = observe.Observer{
			Decision: func(_ observe.Pos, decision, _ string) { decisions = append(decisions, decision) },
		}
		m, _ = pressKey(t, m, ctrlC)
		if !stopped {
			t.Fatalf("draft %q: ctrl+c over a card should stop the turn", draft)
		}
		if m.state == stateConfirmRun || m.approval.request != nil {
			t.Fatalf("draft %q: the stop should take the card with the turn, got state %d", draft, m.state)
		}
		if got := lastToolMessage(t, m).Content; got != agent.CancelledResult {
			t.Fatalf("draft %q: the model should be told the call was cancelled, got %q", draft, got)
		}
		if slices.Contains(decisions, observe.DecisionDeny) {
			t.Fatalf("draft %q: a stop is not a denial, but the record says %v", draft, decisions)
		}
		var row *entry
		for i := range m.transcript {
			if e := &m.transcript[i]; e.kind == entryTool && e.toolName == "write_file" {
				row = e
			}
		}
		if row == nil || row.toolResult != cancelledToolResult || row.deniedBy != "" {
			t.Fatalf("draft %q: the call should be abandoned by the stop, got %+v", draft, row)
		}
		detail := m.activityRowDetail(*row, false, m.contentWidth())
		if detail.Outcome != components.OutcomeStopped {
			t.Fatalf("draft %q: the row should read stopped, got %q", draft, detail.Outcome)
		}
		if m.pressed.openOn(armQuit, quitChord()) || m.armed.openOn(armQuit, quitChord()) {
			t.Fatalf("draft %q: the stop arms nothing", draft)
		}
	}
}

// The grace window swallows the two answers and nothing else the run prints.
func TestGrace_TheAnswersAreDiscarded(t *testing.T) {
	for _, key := range []string{"y", "n"} {
		if !(Model{}).graceDiscards(key) {
			t.Errorf("%q should be discarded while the grace window is open", key)
		}
	}
	for _, key := range []string{"esc", "ctrl+c", "Y", "N"} {
		if (Model{}).graceDiscards(key) {
			t.Errorf("%q must not be discarded", key)
		}
	}
}

// Every key the card printed resolves to itself, on whichever row the run
// wrapped it onto: the geometry is read out of the render, so a run too wide
// for the panel is clickable where it actually landed.
func TestApprovalRun_ClicksLandOnTheKeyUnderThem(t *testing.T) {
	m := handover(t, draftedCardModel(t, noRun(t), ""))
	card := m.approvalCard()
	seen := map[string]bool{}
	for _, row := range strings.Split(card.View(m.contentWidth()), "\n") {
		for col := range ansi.StringWidth(ansi.Strip(row)) {
			if key, ok := card.KeyAt(row, col); ok {
				seen[key] = true
			}
		}
	}
	for _, k := range card.KeyRun() {
		if !seen[k.Key] {
			t.Errorf("no cell of the printed run resolves to %q:\n%s",
				k.Key, ansi.Strip(card.View(m.contentWidth())))
		}
	}
}
