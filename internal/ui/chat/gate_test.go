package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func TestGate_SlashCommand(t *testing.T) {
	var got []string
	m := gatedModel(t, nil, nil).WithGate(Gate{
		Manage: func(args []string) string {
			got = append(got, strings.Join(args, " "))
			return "gate says"
		},
	})
	handled, result := m.handleSlashCommand("/gate result")
	if !handled || result != "gate says" {
		t.Fatalf("/gate result = %v %q", handled, result)
	}
	m.handleSlashCommand("/gate run smoke")
	if len(got) != 2 || got[0] != "result" || got[1] != "run smoke" {
		t.Fatalf("manage args = %v", got)
	}
}

func TestGate_SlashCommandUnavailable(t *testing.T) {
	m := gatedModel(t, nil, nil)
	handled, result := m.handleSlashCommand("/gate run")
	if !handled || !strings.Contains(result, "unavailable") {
		t.Fatalf("/gate without a runner = %v %q", handled, result)
	}
}

func TestGate_HelpMentionsCommand(t *testing.T) {
	if !strings.Contains(helpText(), "/gate") {
		t.Fatal("/help must list /gate")
	}
}

// gateWorkspace is a workspace whose trusted config names an on-close suite.
func gateWorkspace(t *testing.T, body string) string {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".shhh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "quality.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

const onCloseConfig = `{"on_close": "fast", "suites": {
	"fast": {"checks": [{"name": "vet", "exe": "sh", "args": ["-c", "true"]}]}}}`

// scriptedGate answers each run with the next verdict, repeating the last.
func scriptedGate(t *testing.T, ws string, verdicts ...quality.Verdict) (Gate, *int) {
	t.Helper()
	runs := 0
	return Gate{
		Manage: func([]string) string { return "gate says" },
		Run: func(ctx context.Context, suite string) (*quality.Result, error) {
			v := verdicts[min(runs, len(verdicts)-1)]
			runs++
			exit := 0
			if v != quality.VerdictPass {
				exit = 1
			}
			return &quality.Result{
				Suite: suite, Verdict: v, Trusted: true,
				Reason:      "the check would not start",
				Checks:      []quality.CheckResult{{Name: "vet", Command: "go vet ./...", ExitCode: exit}},
				Fingerprint: quality.TakeFingerprint(ws),
			}, nil
		},
	}, &runs
}

// closeGateModel is a session that honours the workspace's on-close suite,
// sitting at the input with the scripted runner behind /gate.
func closeGateModel(t *testing.T, verdicts ...quality.Verdict) (Model, *int) {
	t.Helper()
	ws := gateWorkspace(t, onCloseConfig)
	gate, runs := scriptedGate(t, ws, verdicts...)
	m := turnModel(t).WithWorkspace(ws).WithGate(gate)
	m.closeGate.on = true
	return m, runs
}

// startEditedTurn opens a turn that changed a source file.
func startEditedTurn(t *testing.T, m Model) Model {
	t.Helper()
	m = sendText(t, m, "fix the bug")
	m.changes.Add(m.turnCount, changeset.Record{
		Path: "internal/a/a.go", Before: "one\n", After: "one\ntwo\n",
		BeforeExists: true, AfterExists: true, Agent: changeset.MainAgent,
	})
	return m
}

// drain runs a command and every command a batch of them holds, returning
// the messages that came back.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drain(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// closeTurnWithGate ends the turn and delivers the verdict the close run
// produced, returning the model after it landed.
func closeTurnWithGate(t *testing.T, m Model) Model {
	t.Helper()
	updated, cmd := m.Update(doneMsg{})
	m = updated.(Model)
	if m.turnState() != stateCloseGate {
		t.Fatalf("the turn closed without waiting for its checks: state %v", m.turnState())
	}
	for _, msg := range drain(cmd) {
		if cg, ok := msg.(closeGateMsg); ok {
			updated, _ := m.Update(cg)
			return updated.(Model)
		}
	}
	t.Fatal("the close started no gate run")
	return m
}

// lastGateRow is the newest quality-gate tool row in the transcript.
func lastGateRow(t *testing.T, m Model) entry {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryTool && m.transcript[i].toolName == quality.ToolName {
			return m.transcript[i]
		}
	}
	t.Fatal("no gate row in the transcript")
	return entry{}
}

func TestCloseGate_APassingRunLandsOneRowAheadOfTheClose(t *testing.T) {
	m, runs := closeGateModel(t, quality.VerdictPass)
	m = closeTurnWithGate(t, startEditedTurn(t, m))

	if *runs != 1 {
		t.Fatalf("the suite ran %d times, want once", *runs)
	}
	row, closed := -1, -1
	for i, e := range m.transcript {
		switch {
		case e.kind == entryTool && e.toolName == quality.ToolName:
			row = i
		case e.kind == entryTurnClose:
			closed = i
		}
	}
	if row < 0 || closed < 0 || row > closed {
		t.Fatalf("the verdict row must precede the close row (row %d, close %d)", row, closed)
	}
	c := lastClose(t, m)
	if c.Checks == nil || c.Checks.Failed || !strings.Contains(c.Checks.Label, "quality gate fast") {
		t.Fatalf("the close row does not carry the passing verdict: %+v", c.Checks)
	}
	// The row's text is the runner's own, so /gate result and this row can
	// never come to disagree about what happened.
	if sum, ok := quality.Summarize(lastGateRow(t, m).toolResult); !ok || sum.Verdict != quality.VerdictPass {
		t.Fatalf("the row does not hold a formatted result: %q", lastGateRow(t, m).toolResult)
	}
}

func TestCloseGate_AFailingVerdictBuysOneRoundAndThenCloses(t *testing.T) {
	m, runs := closeGateModel(t, quality.VerdictFail, quality.VerdictFail)
	m = startEditedTurn(t, m)
	rounds := m.agent.Rounds()

	// The first failure hands the verdict back and the turn goes on.
	updated, cmd := m.Update(doneMsg{})
	m = updated.(Model)
	for _, msg := range drain(cmd) {
		if cg, ok := msg.(closeGateMsg); ok {
			updated, _ = m.Update(cg)
			m = updated.(Model)
		}
	}
	if m.turnState() != stateStreaming {
		t.Fatalf("a failing verdict must continue the turn, state %v", m.turnState())
	}
	if m.agent.Rounds() != rounds {
		t.Errorf("the hand-back spent %d rounds, want none", m.agent.Rounds()-rounds)
	}
	msgs := m.agent.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleUser || !strings.Contains(last.Content, "FAIL") {
		t.Fatalf("the verdict never reached the request: %+v", last)
	}
	if sum, ok := quality.Summarize(last.Content); !ok || sum.Verdict != quality.VerdictFail {
		t.Fatalf("the model was told something a gate call would not have said: %q", last.Content)
	}

	// The second failure closes the turn showing it.
	m = closeTurnWithGate(t, m)
	if *runs != 2 {
		t.Fatalf("the suite ran %d times, want twice", *runs)
	}
	c := lastClose(t, m)
	if c.Checks == nil || !c.Checks.Failed {
		t.Fatalf("a turn never closes with a hidden failure: %+v", c.Checks)
	}
}

func TestCloseGate_RunsNothingForATurnWithNoWorkInIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{"an empty changeset", ""},
		{"writes under the state directory only", filepath.Join(".shhh", "todo", "x.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, runs := closeGateModel(t, quality.VerdictFail)
			m = sendText(t, m, "have a look")
			if tc.path != "" {
				m.changes.Add(m.turnCount, changeset.Record{
					Path: tc.path, After: "x\n", AfterExists: true, Agent: changeset.MainAgent,
				})
			}
			updated, _ := m.Update(doneMsg{})
			m = updated.(Model)
			if m.turnState() == stateCloseGate {
				t.Fatal("the turn waited on checks it does not owe")
			}
			if *runs != 0 {
				t.Fatalf("the suite ran %d times, want none", *runs)
			}
			if c := lastClose(t, m); c.Checks != nil {
				t.Fatalf("a row was drawn for a run that never happened: %+v", c.Checks)
			}
		})
	}
}

func TestCloseGate_AnInteractiveSessionRunsNothingUntilItIsTurnedOn(t *testing.T) {
	m, runs := closeGateModel(t, quality.VerdictFail)
	m.closeGate.on = false
	m = startEditedTurn(t, m)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.turnState() == stateCloseGate || *runs != 0 {
		t.Fatalf("the default must be off: state %v, %d runs", m.turnState(), *runs)
	}
	if c := lastClose(t, m); c.Checks != nil {
		t.Fatalf("a row was drawn with the toggle off: %+v", c.Checks)
	}
}

func TestCloseGate_AMissingOrBrokenConfigIsACleanNoOp(t *testing.T) {
	for _, body := range []string{"", `{"suites": {`, `{"suites": {"fast": {"checks": [{"name": "c", "exe": "sh"}]}}}`} {
		ws := t.TempDir()
		if body != "" {
			ws = gateWorkspace(t, body)
		}
		gate, runs := scriptedGate(t, ws, quality.VerdictFail)
		m := turnModel(t).WithWorkspace(ws).WithGate(gate)
		m.closeGate.on = true
		m = startEditedTurn(t, m)
		updated, _ := m.Update(doneMsg{})
		m = updated.(Model)
		if m.turnState() == stateCloseGate || *runs != 0 {
			t.Fatalf("config %q: state %v, %d runs", body, m.turnState(), *runs)
		}
	}
}

func TestCloseGate_CancellingARunLeavesCancelledAndNeverAPass(t *testing.T) {
	m, _ := closeGateModel(t, quality.VerdictPass)
	m = startEditedTurn(t, m)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.turnState() != stateCloseGate {
		t.Fatalf("state = %v, want the close waiting on its checks", m.turnState())
	}
	// The command is never run: the cancel arrives while the suite is still
	// going, which is the case the row has to describe.
	m.closeGate.running = true
	m.cancelStreaming()

	c := lastClose(t, m)
	if c.Checks == nil {
		t.Fatal("a cancelled run still leaves a verdict row")
	}
	if !c.Checks.Failed {
		t.Errorf("cancelled reads as passing: %+v", c.Checks)
	}
	if sum, _ := quality.Summarize(lastGateRow(t, m).toolResult); sum.Verdict != quality.VerdictCancelled {
		t.Errorf("verdict = %q, want cancelled", sum.Verdict)
	}
}

func TestCloseGate_ToggleAnswersOnAndOff(t *testing.T) {
	m, _ := closeGateModel(t, quality.VerdictPass)
	m.closeGate.on = false

	handled, note := m.handleSlashCommand("/gate on")
	if !handled || !strings.Contains(note, `"fast"`) {
		t.Fatalf("/gate on = %v %q", handled, note)
	}
	if !m.closeGate.on {
		t.Fatal("/gate on left the session off")
	}
	handled, note = m.handleSlashCommand("/gate off")
	if !handled || m.closeGate.on {
		t.Fatalf("/gate off = %v %q, on = %v", handled, note, m.closeGate.on)
	}
	// Anything else is still the runner's to answer.
	if _, note := m.handleSlashCommand("/gate result"); note != "gate says" {
		t.Fatalf("/gate result = %q", note)
	}
}

func TestCloseGate_ToggleSaysSoWhenTheWorkspaceNamesNoSuite(t *testing.T) {
	ws := gateWorkspace(t, `{"suites": {"fast": {"checks": [{"name": "c", "exe": "sh", "args": ["-c", "true"]}]}}}`)
	gate, _ := scriptedGate(t, ws, quality.VerdictPass)
	m := turnModel(t).WithWorkspace(ws).WithGate(gate)
	_, note := m.handleSlashCommand("/gate on")
	if !strings.Contains(note, quality.ConfigRelPath) {
		t.Fatalf("/gate on with no suite named = %q", note)
	}
}

func TestCloseGate_UsageAndCompletionOfferTheToggle(t *testing.T) {
	for _, want := range []string{"on", "off"} {
		found := false
		for _, c := range slashCommands {
			if c.name != "/gate" {
				continue
			}
			for _, spec := range c.argSpecs {
				for _, o := range spec.options {
					if o.value == want {
						found = true
					}
				}
			}
		}
		if !found {
			t.Errorf("/gate completion does not offer %q", want)
		}
	}
	if !strings.Contains(helpText(), "on|off") {
		t.Error("/help does not name the toggle")
	}
}

func TestCloseGate_ATurnThatDidNotReachItsOwnEndRunsNothing(t *testing.T) {
	// A cancelled turn left the work halfway; a verdict about half an edit
	// is a verdict about nothing.
	m, runs := closeGateModel(t, quality.VerdictPass)
	m = startEditedTurn(t, m)
	m.setTurnState(stateStreaming)
	m.cancelStreaming()
	if m.turnState() == stateCloseGate || *runs != 0 {
		t.Fatalf("a cancelled turn ran its checks: state %v, %d runs", m.turnState(), *runs)
	}
	if c := lastClose(t, m); c.State != components.TurnCancelled {
		t.Errorf("close state = %v, want cancelled", c.State)
	}

	// A turn parked at its ceiling has not ended either: the checks are
	// owed to the round that finishes it.
	m, runs = closeGateModel(t, quality.VerdictPass)
	m = startEditedTurn(t, m)
	updated, _ := m.pauseAtRoundLimit()
	m = updated.(Model)
	if m.turnState() == stateCloseGate || *runs != 0 {
		t.Fatalf("a paused turn ran its checks: state %v, %d runs", m.turnState(), *runs)
	}
}

func TestCloseGate_SteeringTypedAtTheCloseWaitsForTheVerdict(t *testing.T) {
	m, runs := closeGateModel(t, quality.VerdictPass)
	m = startEditedTurn(t, m)
	m = sendText(t, m, "also update the docs")
	if len(m.steering) != 1 {
		t.Fatalf("the message was not queued as steering: %v", m.steering)
	}
	m = closeTurnWithGate(t, m)

	if *runs != 1 {
		t.Fatalf("the suite ran %d times, want once — typing must not take the turn away from it", *runs)
	}
	if c := lastClose(t, m); c.Checks == nil {
		t.Fatal("the close row lost its verdict to the queued message")
	}
	if len(m.steering) != 0 {
		t.Errorf("the steering was left queued after the close: %v", m.steering)
	}
	last := m.agent.Messages()[len(m.agent.Messages())-1]
	if last.Role != provider.RoleUser || last.Content != "also update the docs" {
		t.Errorf("the steering never became the next turn: %+v", last)
	}
}

func TestCloseGate_SteeringJoinsTheRoundAFailingVerdictBuys(t *testing.T) {
	m, _ := closeGateModel(t, quality.VerdictFail)
	m = startEditedTurn(t, m)
	m = sendText(t, m, "and mind the goldens")

	updated, cmd := m.Update(doneMsg{})
	m = updated.(Model)
	for _, msg := range drain(cmd) {
		if cg, ok := msg.(closeGateMsg); ok {
			updated, _ = m.Update(cg)
			m = updated.(Model)
		}
	}
	if m.turnState() != stateStreaming {
		t.Fatalf("state = %v, want the turn continuing", m.turnState())
	}
	if len(m.steering) != 0 {
		t.Fatalf("the steering was stranded: %v", m.steering)
	}
	var steered, verdict bool
	for _, msg := range m.agent.Messages() {
		if msg.Role != provider.RoleUser {
			continue
		}
		if msg.Content == "and mind the goldens" {
			steered = true
		}
		if strings.Contains(msg.Content, "FAIL") {
			verdict = true
		}
	}
	if !steered || !verdict {
		t.Errorf("the resumed round carries steering=%v verdict=%v, want both", steered, verdict)
	}
}

func TestCloseGate_ARaceWithAnotherRunStillLeavesTheTurnAVerdict(t *testing.T) {
	ws := gateWorkspace(t, onCloseConfig)
	m := turnModel(t).WithWorkspace(ws).WithGate(Gate{
		Manage: func([]string) string { return "" },
		Run: func(context.Context, string) (*quality.Result, error) {
			return nil, errors.New(`a gate run (suite "default") is already in progress`)
		},
	})
	m.closeGate.on = true
	m = closeTurnWithGate(t, startEditedTurn(t, m))

	c := lastClose(t, m)
	if c.Checks == nil || !c.Checks.Failed {
		t.Fatalf("a turn that could not check itself closed saying nothing: %+v", c.Checks)
	}
	if sum, _ := quality.Summarize(lastGateRow(t, m).toolResult); sum.Verdict != quality.VerdictBlocked {
		t.Errorf("verdict = %q, want blocked", sum.Verdict)
	}
}
