package chat

// The command amended at the card
// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// amendModel is a session holding a command card with the keyboard, over a
// workspace of its own so the blast radius is read in a tree this test owns.
func amendModel(t *testing.T, command string, ran *[]string) Model {
	t.Helper()
	m := gatedModel(t, nil, nil).
		WithWorkspace(t.TempDir()).
		WithRunner(func(_ context.Context, cmd string) (string, int) {
			*ran = append(*ran, cmd)
			return "ok", 0
		})
	return execApproval(t, m, command)
}

// offersAmend reports whether the card is advertising the amend key.
func offersAmend(card *components.ApprovalCard) bool {
	return slices.ContainsFunc(card.ExtraHints, func(o components.KeyOffer) bool {
		return o.Key == keys.Bracket(keys.Decision.Amend)
	})
}

// openAmend presses the key and types a whole line in place of the one the
// field was prefilled with.
func openAmend(t *testing.T, m Model, line string) Model {
	t.Helper()
	m = press(t, m, keys.Shown(keys.Decision.Amend))
	if m.commandEdit == nil {
		t.Fatal("the amend key should open the command in a field")
	}
	e := *m.commandEdit
	e.field.SetValue(line)
	m.commandEdit = &e
	return m
}

// The whole of the story: what runs is the reader's line, the model is told
// so in a sentence, and the row is the account of the line that ran.
func TestAmend_TheReadersLineRunsAndEverythingSaysSo(t *testing.T) {
	var ran []string
	var decisions [][2]string
	m := amendModel(t, "npm test", &ran).WithObserver(observe.Observer{
		Decision: func(_ observe.Pos, decision, reason string) {
			decisions = append(decisions, [2]string{decision, reason})
		},
	})
	if !offersAmend(m.approvalCard()) {
		t.Fatal("a command card offers to be amended")
	}
	m = openAmend(t, m, "npm test -- --runInBand")
	if m.pendingApproval == nil || m.state != stateConfirmRun {
		t.Fatal("opening the field must settle nothing")
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if len(ran) != 0 {
		t.Fatalf("the runner is reached by the returned command, not by the key: %v", ran)
	}
	done := driveCmdDone(t, cmd)
	if got := []string{"npm test -- --runInBand"}; !slices.Equal(ran, got) {
		t.Fatalf("the amended line should be the one that ran, got %v", ran)
	}
	updated, _ = m.Update(done)
	m = updated.(Model)

	// The model is told what ran in place of what it asked for, in one
	// sentence, ahead of the result — not as a bare success.
	told := lastToolMessage(t, m).Content
	for _, want := range []string{"npm test -- --runInBand", "npm test", "in place of"} {
		if !strings.Contains(told, want) {
			t.Fatalf("the result should name both lines, got %q", told)
		}
	}
	if strings.HasPrefix(told, "exit code:") {
		t.Fatalf("a bare success is what this key exists to prevent, got %q", told)
	}

	// The row records the line that ran, and says whose line it was.
	row := m.transcript[len(m.transcript)-1]
	if row.kind != entryCommand || row.text != "npm test -- --runInBand" {
		t.Fatalf("the row should record the line that ran, got %+v", row)
	}
	if row.amendedFrom != "npm test" {
		t.Fatalf("the row should keep the line the call carried, got %q", row.amendedFrom)
	}
	detail := m.activityRowDetail(row, false)
	if want := components.OutcomeBy(components.OutcomeAmended, decidedByYou); detail.Allowed != want {
		t.Fatalf("the row's outcome should name the decider, want %q got %q", want, detail.Allowed)
	}

	// And the record can tell an amendment from a plain allow.
	want := [2]string{observe.DecisionAllow, observe.ReasonUserAmended}
	if !slices.Contains(decisions, want) {
		t.Fatalf("the record should hold one %v, got %v", want, decisions)
	}
	if slices.Contains(decisions, [2]string{observe.DecisionAllow, observe.ReasonUser}) {
		t.Fatal("an amendment is not a plain allow, and the aggregate has to be able to tell")
	}
}

// The card's three questions are asked again of the line that will run: what
// the block says, and the offers the card derives from the command text.
func TestAmend_TheBlockIsReadAgainstTheLineThatRuns(t *testing.T) {
	var ran []string
	m := amendModel(t, "echo hi > one.txt", &ran)
	if text := cardText(m.approvalCard(), m); !strings.Contains(text, "one.txt") {
		t.Fatalf("the block should name what the original writes:\n%s", text)
	}
	if req := m.pendingApproval; req.dryCommand != "" {
		t.Fatalf("the fixture wants a command with no harmless form, got %q", req.dryCommand)
	}

	m = openAmend(t, m, "rsync --delete src/ dst/")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	driveCmdDone(t, cmd)

	// The block is the amended line's, resolved before it ran rather than
	// inherited from the card the key was pressed on.
	block := m.pendingBlast
	for _, f := range block.fields {
		if strings.Contains(f.Value+f.Detail, "one.txt") {
			t.Fatalf("the block still describes the line nobody ran: %+v", block.fields)
		}
	}
	if !strings.Contains(block.fields[0].Detail, "rsync") {
		t.Fatalf("the block should describe what the amended line touches: %+v", block)
	}
	// The dry-run offer is derived from the command text, so it is derived
	// again too: rsync has a harmless form and the original had none.
	if m.pendingApproval.dryCommand == "" {
		t.Fatal("the offers should follow the line that will run")
	}
}

// The repeat detector is filed under the line that ran, not the line the
// call carried. Keying the model's own line against the amendment's output
// would record an interaction that never happened — and would leave the line
// that really ran unrecorded, so running it twice would go unnoticed.
func TestAmend_TheRepeatDetectorIsFiledUnderTheLineThatRan(t *testing.T) {
	var ran []string
	m := amendModel(t, "npm test", &ran).WithRepeats(agent.NewRepeatDetector())
	m = openAmend(t, m, "npm test -- --runInBand")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)

	// The model now proposes the reader's own line. The detector has seen it
	// once, so this is the second identical interaction and reads as one.
	m = execApproval(t, m, "npm test -- --runInBand")
	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)

	if got := lastToolMessage(t, m).Content; !agent.IsRepeatNotice(got) {
		t.Fatalf("the amended line should have been filed under itself, got %q", got)
	}
}

// A line that reaches a directory the card never named is a card the reader
// has not seen, whether or not it is otherwise heavier: approving one adds
// that directory to the working scope for the session, and a grant made by a
// keystroke aimed at a card that never mentioned it is exactly what nothing
// here may inherit.
func TestAmend_ALineThatLeavesTheScopeIsPutBackToTheReader(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	sc, problems := scope.New(root)
	if sc == nil {
		t.Fatalf("scope.New(%q): %v", root, problems)
	}
	var ran []string
	m := gatedModel(t, nil, nil).WithWorkspace(root).WithScope(sc).
		WithRunner(func(_ context.Context, cmd string) (string, int) {
			ran = append(ran, cmd)
			return "ok", 0
		})
	m = execApproval(t, m, "echo hi > "+filepath.Join(root, "one.txt"))
	if m.pendingScope.any() {
		t.Fatalf("the fixture wants a line inside the scope, got %v", m.pendingScope.dirs)
	}

	m = openAmend(t, m, "echo hi > "+filepath.Join(outside, "two.txt"))
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if len(ran) != 0 || m.state != stateConfirmRun {
		t.Fatalf("a line that leaves the scope is asked about, got %v state=%d", ran, m.state)
	}
	if !m.pendingScope.any() {
		t.Fatal("the working scope should have been read against the amended line")
	}
	if sc.Contains(filepath.Join(outside, "three.txt")) {
		t.Fatal("nothing was approved, so nothing was granted")
	}
	// And the card the reader is put back to names what it now reaches, so
	// the directory is read before it is granted rather than after.
	if !slices.ContainsFunc(m.pendingBlast.fields, func(f components.CardField) bool {
		return strings.Contains(f.Label, "scope")
	}) {
		t.Fatalf("the redrawn card should carry the scope row, got %+v", m.pendingBlast.fields)
	}
}

// A line that would draw a heavier card draws that card: the reader has not
// read this one, and a card that ran on a keystroke aimed at the lighter one
// would be lying about what it was answered for.
func TestAmend_AHeavierLineDrawsTheHeavierCard(t *testing.T) {
	var ran []string
	m := amendModel(t, "echo hi", &ran)
	was := m.approvalCard()
	if !was.AllowAlways || len(was.Warnings) != 0 {
		t.Fatalf("the fixture wants a clean card that offers a grant: %+v", was)
	}

	m = openAmend(t, m, "rm -rf ./build")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if len(ran) != 0 || m.state != stateConfirmRun {
		t.Fatalf("a heavier amendment is a card, not a run: %v state=%d", ran, m.state)
	}
	if m.commandEdit != nil {
		t.Fatal("the field closes: the card behind it is the thing to read")
	}
	if m.pendingApproval == nil || m.pendingRun != "rm -rf ./build" {
		t.Fatalf("the card is about the amended line now, got %q", m.pendingRun)
	}
	now := m.approvalCard()
	text := cardText(now, m)
	if !now.Amended || !strings.Contains(text, "was: echo hi") {
		t.Fatalf("the card should say the line is the reader's:\n%s", text)
	}
	if now.Severity <= was.Severity || len(now.Warnings) == 0 {
		t.Fatalf("the card should be the heavier one, %v then %v", was.Severity, now.Severity)
	}
	// And it lands on the rule a flagged card has always been under: a
	// warning is never blanket-approved, inherited clean card or not.
	if now.AllowAlways {
		t.Fatalf("a flagged card offers no grant:\n%s", text)
	}
	// The reader answers the card they are actually being asked.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	driveCmdDone(t, cmd)
	if got := []string{"rm -rf ./build"}; !slices.Equal(ran, got) {
		t.Fatalf("the amended line runs when the heavier card is answered, got %v", ran)
	}
}

// A batch mark answered the shape of the line the model proposed. The reader
// has just made this a different shape, so the marks are taken again from the
// line that will run.
func TestAmend_TheBatchMarkIsTakenAgainstTheLineTheReaderWrote(t *testing.T) {
	var ran []string
	m := gatedModel(t, nil, nil).WithWorkspace(t.TempDir()).
		WithRunner(func(_ context.Context, cmd string) (string, int) {
			ran = append(ran, cmd)
			return "ok", 0
		})
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_a", Name: tools.ExecCommandName, Arguments: `{"command":"echo one"}`},
		{ID: "call_b", Name: tools.ExecCommandName, Arguments: `{"command":"echo two"}`},
	}})
	m = handover(t, updated.(Model))
	if !slices.Equal(m.pendingBatch, []string{"call_b"}) {
		t.Fatalf("the fixture wants a queue the card's [A] would answer, got %v", m.pendingBatch)
	}

	// A line no [A] may ever cover takes the marks with it: a safety-flagged
	// command is never blanket-approved, whichever card it arrived on.
	m = openAmend(t, m, "rm -rf ./build")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if len(ran) != 0 || m.state != stateConfirmRun {
		t.Fatalf("the fixture wants the heavier card drawn, got %v state=%d", ran, m.state)
	}
	if len(m.pendingBatch) != 0 {
		t.Fatalf("the mark was taken against the model's line, got %v", m.pendingBatch)
	}
	if card := m.approvalCard(); card.Batch {
		t.Fatal("a card that answers a batch must have a batch to answer")
	}
}

// A line a rule refuses is refused as the amended line's own refusal, with
// the rule named — and the reader is left holding the decision they were
// holding, on the card, with their line still in the field.
func TestAmend_ARefusedLineNamesTheRuleAndLeavesTheDecisionWaiting(t *testing.T) {
	var ran []string
	m := amendModel(t, "echo hi", &ran)
	m.policy.denylist = []string{"git push"}
	m = openAmend(t, m, "git push --force")

	before := len(m.transcript)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if len(ran) != 0 {
		t.Fatalf("a deny-listed line must not run: %v", ran)
	}
	if m.pendingApproval == nil || m.pendingRun != "echo hi" || m.state != stateConfirmRun {
		t.Fatal("the original decision is still waiting")
	}
	if len(m.transcript) != before {
		t.Fatal("nothing was decided about the call, so nothing is filed against it")
	}
	if m.commandEdit == nil {
		t.Fatal("the reader is returned to the card, not to the draft")
	}
	if !strings.Contains(m.commandEdit.refused, "deny list") {
		t.Fatalf("the refusal should name the rule, got %q", m.commandEdit.refused)
	}
	view := m.View().Content
	if !strings.Contains(view, "command_denylist") {
		t.Fatalf("the card should print the rule that refused:\n%s", view)
	}
	// And the refusal stands only until the reader answers it.
	m = press(t, m, "x")
	if m.commandEdit == nil || m.commandEdit.refused != "" {
		t.Fatalf("the next keystroke settles the refusal, got %+v", m.commandEdit)
	}
}

// Esc closes the field with the original back on the card and the decision
// exactly where it was: the field never runs on esc
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestAmend_EscRestoresTheOriginalAndSettlesNothing(t *testing.T) {
	var ran []string
	m := amendModel(t, "npm test", &ran)
	m = press(t, openAmend(t, m, "rm -rf ./build"), "esc")

	if m.commandEdit != nil {
		t.Fatal("esc should close the field")
	}
	if len(ran) != 0 {
		t.Fatalf("esc never runs anything: %v", ran)
	}
	if m.pendingRun != "npm test" || m.pendingApproval == nil || m.pendingApproval.command != "npm test" {
		t.Fatalf("the original should be back on the card, got %q", m.pendingRun)
	}
	if m.approvalCard().Amended {
		t.Fatal("nothing was amended, so nothing says it was")
	}
	if m.state != stateConfirmRun {
		t.Fatalf("the decision is still waiting, got state %d", m.state)
	}
}

// While the field holds the keyboard every letter is text. The keys in the
// table are the ones that answer this card when it does not.
func TestAmend_EveryLetterIsTextWhileTheFieldIsOpen(t *testing.T) {
	for _, key := range []string{"1", "y", "n", "Y", "N", "a", "A", "d", "t", "x", "e"} {
		t.Run(key, func(t *testing.T) {
			var ran []string
			m := press(t, amendModel(t, "npm test", &ran), keys.Shown(keys.Decision.Amend))
			if m.commandEdit == nil {
				t.Fatal("the amend key should open the field")
			}
			before := m.state
			m = press(t, m, key)
			if m.commandEdit == nil {
				t.Fatalf("%q closed the field", key)
			}
			if m.state != before || m.pendingApproval == nil {
				t.Fatalf("%q answered the decision", key)
			}
			if len(ran) != 0 {
				t.Fatalf("%q ran something: %v", key, ran)
			}
			if got := m.commandEdit.field.Value(); got != "npm test"+key {
				t.Fatalf("%q should be text in the field, got %q", key, got)
			}
		})
	}
}

// A command of more than one line is not offered the key, because the field
// is one line: it joins what it is handed with spaces rather than refusing
// it, and a heredoc that came back as one line would run something nobody
// wrote.
func TestAmend_AMultiLineCommandIsNotOffered(t *testing.T) {
	var ran []string
	m := amendModel(t, "cat <<'EOF' > f\nhello\nEOF", &ran)
	if offersAmend(m.approvalCard()) {
		t.Fatal("a field that cannot hold the command is not an offer")
	}
	// And the letter is not a key there either, so it stays the reader's.
	m = press(t, m, keys.Shown(keys.Decision.Amend))
	if m.commandEdit != nil {
		t.Fatal("the key must not open a field the card did not offer")
	}
}

// The field is prefilled with the line the card is about, so the ordinary
// amendment is one flag typed onto the end rather than a line retyped.
func TestAmend_TheFieldOpensOnTheCommandItself(t *testing.T) {
	var ran []string
	m := press(t, amendModel(t, "npm test", &ran), keys.Shown(keys.Decision.Amend))
	if m.commandEdit == nil || m.commandEdit.field.Value() != "npm test" {
		t.Fatalf("the field should open prefilled, got %+v", m.commandEdit)
	}
	m = typeInto(t, m, " -- --runInBand")
	if got := m.commandEdit.field.Value(); got != "npm test -- --runInBand" {
		t.Fatalf("the caret should start past the line, got %q", got)
	}
	// And confirming the line unchanged is the plain allow the reader could
	// have pressed instead, not an amendment.
	m2 := press(t, press(t, amendModel(t, "npm test", &ran), keys.Shown(keys.Decision.Amend)), "enter")
	if m2.pendingApproval != nil && m2.pendingApproval.amendedFrom != "" {
		t.Fatal("a line nobody changed is not an amendment")
	}
}

// cardText is the card as it is drawn, with the colour taken off.
func cardText(card *components.ApprovalCard, m Model) string {
	return ansi.Strip(card.View(m.contentWidth()))
}
