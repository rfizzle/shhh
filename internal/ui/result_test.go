package ui

// The result surface: the default explanation, the containment line,
// the safe default that moves with the risk, the dry run, and the revise
// ladder.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// armed streams command to completion and hands back the model sitting on the
// result surface. A nil explain func means this test is not about explaining.
func armed(t *testing.T, command string, explain ExplainStreamFunc) GenerateModel {
	t.Helper()
	m := NewGenerateModel(makeEvents(command), noopCancel, nil, nil, explain, "")
	if explain == nil {
		m = m.WithExplain(ExplainNone)
	}
	m = drainStream(m, 2)
	return m
}

func press(t *testing.T, m GenerateModel, key string) GenerateModel {
	t.Helper()
	model, cmd := m.Update(keyMsg(key))
	return settle(model.(GenerateModel), cmd)
}

// A response carrying no sentence of its own is what falls back to asking for
// one on a second request: a model that cannot produce the section, or a
// reply cut short before it reached it.
func TestResult_ExplainsBrieflyByDefault(t *testing.T) {
	var asked int
	var askedLong bool
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		asked++
		askedLong = long
		return makeEvents("lists the directory in long form"), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents("ls -la"), noopCancel, nil, nil, explain, "")
	m = drainStream(m, 2)

	if asked != 1 {
		t.Fatalf("the default asked for %d explanations, want 1", asked)
	}
	if askedLong {
		t.Error("the default asked for the long form; the flag is what buys that")
	}
	// The keys are live while the one line arrives — nothing to wait for.
	if m.Phase() != phaseAction {
		t.Errorf("the brief explanation took the screen: phase %v", m.Phase())
	}
	m = drainExplainStream(m, 2)
	view := m.View().Content
	if !strings.Contains(view, "lists the directory in long form") {
		t.Errorf("the explanation is not under the command:\n%s", view)
	}
	if strings.Contains(view, "Explanation:") {
		t.Errorf("the one-liner rendered as the long form's block:\n%s", view)
	}
}

func TestResult_SilentModeSuppressesTheExplanation(t *testing.T) {
	asked := 0
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		asked++
		return makeEvents("lists files"), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents("ls -la"), noopCancel, nil, nil, explain, "").
		WithExplain(ExplainNone)
	m = drainStream(m, 2)
	if asked != 0 {
		t.Errorf("silent mode asked for %d explanations", asked)
	}
	if strings.Contains(m.View().Content, "lists files") {
		t.Errorf("silent mode explained the command anyway:\n%s", m.View().Content)
	}
}

func TestResult_FlagBuysTheLongForm(t *testing.T) {
	var askedLong bool
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		askedLong = long
		return makeEvents("the long form"), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents("ls -la"), noopCancel, nil, nil, explain, "").
		WithExplain(ExplainLong)
	m = drainStream(m, 2)
	if !askedLong {
		t.Error("-e did not ask for the long form")
	}
	if m.Phase() != phaseExplain {
		t.Errorf("the long form did not take the screen: phase %v", m.Phase())
	}
}

func TestResult_ContainmentLineStatesTheReach(t *testing.T) {
	m := armed(t, "grep -r listen .", nil)
	if !strings.Contains(m.View().Content, "⛨ read-only · no network · no sudo") {
		t.Errorf("no containment line on the result surface:\n%s", m.View().Content)
	}
}

func TestResult_ContainmentLineComesFromTheResolver(t *testing.T) {
	m := armed(t, "curl https://example.com", nil)
	if got := m.Reach().Reach(); !strings.Contains(m.View().Content, "⛨ "+got) {
		t.Errorf("the line and the resolver disagree: the view has no %q", got)
	}
	if !strings.Contains(m.View().Content, "network") {
		t.Error("a command that leaves the machine did not say so")
	}
}

func TestResult_RiskIsStatedAboveTheContainmentLine(t *testing.T) {
	view := armed(t, "rm -rf build", nil).View().Content
	risk := strings.Index(view, "⚠ ")
	reach := strings.Index(view, "⛨ ")
	if risk < 0 {
		t.Fatalf("no risk line for a safety-flagged command:\n%s", view)
	}
	if reach < 0 || risk > reach {
		t.Errorf("the risk line is not above the containment line:\n%s", view)
	}
}

func TestResult_OrdinaryCommandRunsOnEnter(t *testing.T) {
	m := press(t, armed(t, "ls -la", nil), "enter")
	if m.Result().Action != ActionRun {
		t.Errorf("enter on an ordinary command did %v, want ActionRun", m.Result().Action)
	}
	if m.Result().Confirmed {
		t.Error("an ordinary command was reported as already confirmed")
	}
}

func TestResult_DestructiveCommandSpendsEnterOnTheRadius(t *testing.T) {
	m := armed(t, "rm -rf build", nil)
	m = press(t, m, "enter")
	if m.Result().Action == ActionRun {
		t.Fatal("enter ran a destructive command")
	}
	if m.Phase() != phaseAction {
		t.Fatalf("expected to stay on the result surface, got phase %v", m.Phase())
	}
	if !strings.Contains(m.View().Content, "would affect") {
		t.Errorf("enter did not say what would be affected:\n%s", m.View().Content)
	}

	m = press(t, m, "y")
	if m.Result().Action != ActionRun {
		t.Errorf("[y] did %v, want ActionRun", m.Result().Action)
	}
	if !m.Result().Confirmed {
		t.Error("the deliberate key was not reported as a confirmation")
	}
}

func TestResult_AffectedNamesThePathsAndDescribesThem(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "build")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.o", "b.o"} {
		if err := os.WriteFile(filepath.Join(target, name), []byte("xx"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	view := press(t, armed(t, "rm -rf "+target, nil), "enter").View().Content
	if !strings.Contains(view, target) {
		t.Errorf("the affected block did not name the path:\n%s", view)
	}
	if !strings.Contains(view, "2 files") {
		t.Errorf("the affected block did not describe what is there:\n%s", view)
	}
}

func TestResult_AffectedSaysSoWhenItResolvedNothing(t *testing.T) {
	view := press(t, armed(t, "rm -rf $TARGET", nil), "enter").View().Content
	if !strings.Contains(view, "would affect") {
		t.Fatalf("no affected block:\n%s", view)
	}
	if !strings.Contains(view, "expands") && !strings.Contains(view, "could not resolve") {
		t.Errorf("an unresolved command showed an empty list rather than saying so:\n%s", view)
	}
}

func TestResult_DryRunRunsTheDerivedForm(t *testing.T) {
	var ran string
	m := NewGenerateModel(makeEvents("find . -name '*.tmp' -delete"), noopCancel, nil, nil, nil, "").
		WithExplain(ExplainNone).
		WithDryRun(func(command string) (string, int) {
			ran = command
			return "./a.tmp\n./b.tmp", 0
		})
	m = drainStream(m, 2)

	if !strings.Contains(m.View().Content, "[d] dry run") {
		t.Fatalf("a command with a dry run was not offered one:\n%s", m.View().Content)
	}
	m = press(t, m, "d")
	if m.Phase() != phaseDryRun {
		t.Fatalf("expected phaseDryRun, got %v", m.Phase())
	}
	m = step(m, m.dryRunCmd()())

	if ran != "find . -name '*.tmp' -print" {
		t.Errorf("the dry run executed %q, not the derived no-op form", ran)
	}
	if m.Phase() != phaseAction {
		t.Errorf("the surface did not come back: phase %v", m.Phase())
	}
	view := m.View().Content
	if !strings.Contains(view, "./a.tmp") || !strings.Contains(view, "./b.tmp") {
		t.Errorf("the dry run's output is not on the surface:\n%s", view)
	}
}

func TestResult_DryRunNotOfferedWithoutOne(t *testing.T) {
	m := armed(t, "rm -rf build", nil)
	if strings.Contains(m.View().Content, "dry run") {
		t.Errorf("rm was offered a dry run it does not have:\n%s", m.View().Content)
	}
	m = press(t, m, "d")
	if m.Phase() != phaseAction {
		t.Errorf("[d] did something on a command with no dry run: phase %v", m.Phase())
	}
}

func TestResult_ReviseKeepsThePreviousCommandAndCountsRevisions(t *testing.T) {
	view := reviseOnce(t).View().Content
	if !strings.Contains(view, "$ ls") {
		t.Errorf("the previous command is not on the surface:\n%s", view)
	}
	if !strings.Contains(view, "❯ add -la") {
		t.Errorf("the feedback that replaced it is not on the surface:\n%s", view)
	}
	if !strings.Contains(view, "revision 1") {
		t.Errorf("no revision counter:\n%s", view)
	}
	if !strings.Contains(view, "[u] back") {
		t.Errorf("no way back:\n%s", view)
	}
}

func TestResult_BackStepsToThePreviousCommand(t *testing.T) {
	m := press(t, reviseOnce(t), "u")
	if got := m.stream.Output(); got != "ls" {
		t.Errorf("[u] left %q on screen, want the command from before the revise", got)
	}
	view := m.View().Content
	if strings.Contains(view, "revision 1") {
		t.Errorf("the counter did not come back down:\n%s", view)
	}
	if strings.Contains(view, "[u] back") {
		t.Error("[u] is still offered with nothing left to step back to")
	}
	if len(m.Messages()) != 1 {
		t.Errorf("stepping back left %d messages, want the one the first answer added", len(m.Messages()))
	}
}

// reviseOnce takes a model through one full revise: `ls` becomes `ls -la`.
func reviseOnce(t *testing.T) GenerateModel {
	t.Helper()
	newStream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return makeEvents("ls -la"), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents("ls"), noopCancel, nil, newStream, nil, "").
		WithExplain(ExplainNone)
	m = drainStream(m, 2)

	m = press(t, m, "r")
	if m.Phase() != phaseRevise {
		t.Fatalf("expected phaseRevise, got %v", m.Phase())
	}
	for _, r := range "add -la" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")
	m = drainStream(m, 2)
	if m.Phase() != phaseAction {
		t.Fatalf("expected the result surface after the revise, got phase %v", m.Phase())
	}
	return m
}

func TestResult_AStaleStreamMessageIsIgnored(t *testing.T) {
	// The explanation and the next command are two streams answering to the
	// same message types. A cancelled explanation's last message can land
	// after a revise has started the next command, and it must not be read as
	// that command's own.
	explainEvents := make(chan provider.StreamEvent)
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return explainEvents, noopCancel, nil
	}
	newStream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return make(chan provider.StreamEvent), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents("ls"), noopCancel, nil, newStream, explain, "")
	m = drainStream(m, 2)
	staleID := m.explainStream.id
	if staleID == 0 {
		t.Fatal("no explanation stream was started")
	}

	m = press(t, m, "r")
	m = press(t, m, "x")
	m = press(t, m, "enter")
	if m.Phase() != phaseStreaming {
		t.Fatalf("expected phaseStreaming after the revise, got %v", m.Phase())
	}

	m = step(m, doneMsg{id: staleID})
	if m.Phase() != phaseStreaming {
		t.Errorf("a stale message from the explanation ended the command stream: phase %v", m.Phase())
	}
	if m.stream.Done() {
		t.Error("the command stream was marked done by another stream's message")
	}
}

func TestResult_StepByStepIsNotAConfirmation(t *testing.T) {
	// `[t]` asked nothing, so the caller's own per-step prompt and its safety
	// warning both still stand.
	m := NewGenerateModel(makeEvents("rm -rf a\nrm -rf b"), noopCancel, nil, nil, nil, "").
		WithExplain(ExplainNone)
	m = drainStream(m, 2)
	m = press(t, m, "t")
	if m.Result().Action != ActionRunStep {
		t.Fatalf("[t] did %v, want ActionRunStep", m.Result().Action)
	}
	if m.Result().Confirmed {
		t.Error("step-by-step was reported as a deliberate confirmation")
	}
}

// The defect this guards against: opening the explanation's stream is an HTTP
// request that does not return until the model starts answering, and it used
// to be made inline in Update. The event loop cannot paint while it is out,
// so the command sat alone on screen for the whole round trip and the action
// bar arrived when the request did — seconds, on a real provider.
func TestResult_TheKeysAreOnScreenBeforeTheExplanationIsAskedFor(t *testing.T) {
	var asked int
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		asked++
		return makeEvents("lists files"), noopCancel, nil
	}

	m := NewGenerateModel(makeEvents("ls -la"), noopCancel, nil, nil, explain, "")
	m = drainStreamPending(m, 2)

	if asked != 0 {
		t.Error("the request was made inline, which is what stops the loop painting")
	}
	if m.Phase() != phaseAction {
		t.Fatalf("the surface is not on the action phase: %v", m.Phase())
	}
	if !m.opening {
		t.Error("the surface does not know it is waiting on a stream")
	}
	view := m.View().Content
	if !strings.Contains(view, "[↵] run") {
		t.Errorf("the keys are not on screen while the explanation is being asked for:\n%s", view)
	}
	if !strings.Contains(view, "ls -la") {
		t.Errorf("the command is not on screen with them:\n%s", view)
	}
}

// The long form is the one case that does hold the screen, and it says so
// with a spinner rather than with nothing.
func TestResult_TheLongFormSpinsWhileItsStreamOpens(t *testing.T) {
	var asked int
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		asked++
		return makeEvents("lists files in detail"), noopCancel, nil
	}

	m := NewGenerateModel(makeEvents("ls -la"), noopCancel, nil, nil, explain, "").
		WithExplain(ExplainLong)
	m = drainStreamPending(m, 2)

	if asked != 0 {
		t.Error("the request was made inline")
	}
	if m.Phase() != phaseExplain {
		t.Fatalf("the long form did not take the screen: %v", m.Phase())
	}
	if !strings.Contains(m.View().Content, "Explanation:") {
		t.Errorf("the wait says nothing about what it is waiting for:\n%s", m.View().Content)
	}
	if m.explainStream.spinner.View() == "" {
		t.Error("nothing is turning while the request is out")
	}
}

// A revise opens its stream the same way, and the surface says it is thinking
// rather than leaving the old command on screen looking live.
func TestResult_AReviseSpinsWhileItsStreamOpens(t *testing.T) {
	var asked int
	newStream := func(messages []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		asked++
		return makeEvents("ls -la"), noopCancel, nil
	}

	m := NewGenerateModel(makeEvents("ls"), noopCancel, nil, newStream, nil, "")
	m = drainStream(m, 2)
	m = press(t, m, "r")
	m = typeKeys(m, "add -la")
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = model.(GenerateModel)

	if asked != 0 {
		t.Error("the request was made inline")
	}
	if m.Phase() != phaseStreaming {
		t.Fatalf("the revise did not go back to streaming: %v", m.Phase())
	}
	if !m.opening {
		t.Error("the surface does not know it is waiting on a stream")
	}
	if !strings.Contains(m.View().Content, "Thinking") {
		t.Errorf("the wait for the new stream says nothing:\n%s", m.View().Content)
	}

	// And it does arrive.
	m = settle(m, cmd)
	if asked != 1 {
		t.Errorf("the stream was opened %d times, want 1", asked)
	}
	m = drainStream(m, 2)
	if m.stream.Output() != "ls -la" {
		t.Errorf("the revised command came back as %q", m.stream.Output())
	}
}

// An explanation still being asked for when the reader moves on is an answer
// about a command nobody is looking at.
func TestResult_AnExplanationForALastCommandIsDropped(t *testing.T) {
	m := NewGenerateModel(makeEvents("ls -la"), noopCancel, nil, nil,
		mockExplainStream("lists files"), "")
	m = drainStream(m, 2)

	cancelled := false
	stale := explainReadyMsg{
		gen:    m.gen - 1,
		events: makeEvents("about something else"),
		cancel: func() { cancelled = true },
	}
	model, _ := m.Update(stale)
	m = model.(GenerateModel)

	if !cancelled {
		t.Error("the request behind a dropped answer was left running")
	}
	if strings.Contains(m.View().Content, "about something else") {
		t.Errorf("an explanation of a command that is gone reached the screen:\n%s", m.View().Content)
	}
}

// Checking a command spawns a shell and walks PATH, and the reader has no
// reason to sit through either: the command is written, so it is on screen
// with its keys live while the check runs beside it.
func TestResult_TheCommandIsUsableWhileItIsChecked(t *testing.T) {
	m := NewGenerateModel(makeEvents("ls -la"), noopCancel, nil,
		mockNewStream("ls"), nil, "bash")
	m = drainStreamPending(m, 2)

	if !m.checking {
		t.Fatal("the surface is not waiting on a check it asked for")
	}
	if m.Phase() != phaseAction {
		t.Errorf("the check held the screen: phase %v", m.Phase())
	}
	view := m.View().Content
	if !strings.Contains(view, "ls -la") {
		t.Errorf("the command is not on screen while it is being checked:\n%s", view)
	}
	if !strings.Contains(view, "[↵] run") {
		t.Errorf("the keys are not live while the command is being checked:\n%s", view)
	}
}

// A check that is out must not be asked for again by every message that
// arrives while it runs.
func TestResult_AnOutstandingCheckIsAskedForOnce(t *testing.T) {
	m := NewGenerateModel(makeEvents("ls -la"), noopCancel, nil,
		mockNewStream("ls"), nil, "bash")
	m = drainStreamPending(m, 2)

	gen := m.gen
	for i := 0; i < 3; i++ {
		model, _ := m.Update(spinner.TickMsg{})
		m = model.(GenerateModel)
	}
	if m.gen != gen {
		t.Errorf("the check was asked for again %d times while it was out", m.gen-gen)
	}
}

// bundled is a response answered the way the generator is asked to answer:
// the sentence about the command comes back with it, so the surface has
// everything it shows the moment the stream ends.
const bundled = `ls -la
--- explanation
Lists every entry in the working directory, hidden ones included, one per line.`

const bundledSentence = "Lists every entry in the working directory, hidden ones included, one per line."

// refuseExplain answers a request for an explanation that should never be
// made, and counts the ones that are.
func refuseExplain(asked *int) ExplainStreamFunc {
	return func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		*asked++
		return makeEvents("asked for on its own"), noopCancel, nil
	}
}

func TestResult_TheBundledExplanationNeedsNoSecondRequest(t *testing.T) {
	asked := 0
	m := NewGenerateModel(makeEvents(bundled), noopCancel, nil, nil, refuseExplain(&asked), "")
	m = drainStream(m, 2)

	if asked != 0 {
		t.Errorf("the sentence came with the command and was asked for %d times anyway", asked)
	}
	if m.Phase() != phaseAction {
		t.Errorf("the surface is waiting for something: phase %v", m.Phase())
	}
	view := m.View().Content
	if !strings.Contains(view, bundledSentence) {
		t.Errorf("the sentence that came with the command is not under it:\n%s", view)
	}
	// It is read, not shown: the command is the command.
	if m.stream.Output() != "ls -la" {
		t.Errorf("the section leaked into the command: %q", m.stream.Output())
	}
	if strings.Contains(view, "--- explanation") {
		t.Errorf("the sentinel reached the screen:\n%s", view)
	}
	if strings.Contains(view, "Explanation:") {
		t.Errorf("the one-liner rendered as the long form's block:\n%s", view)
	}
	if !strings.Contains(view, "[↵] run") {
		t.Errorf("the keys are not on screen with it:\n%s", view)
	}
}

func TestResult_TheLongFormIsStillARequestOfItsOwn(t *testing.T) {
	asked, askedLong := 0, false
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		asked++
		askedLong = long
		return makeEvents("lists the directory, breaking down each flag"), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents(bundled), noopCancel, nil, nil, explain, "").
		WithExplain(ExplainLong)
	m = drainStream(m, 2)

	if asked != 1 || !askedLong {
		t.Errorf("the flag asked for %d explanations, long=%v", asked, askedLong)
	}
	if m.Phase() != phaseExplain {
		t.Errorf("the long form did not take the screen: phase %v", m.Phase())
	}
}

func TestResult_TheLongFormOnDemandStillOpensAStream(t *testing.T) {
	asked, askedLong := 0, false
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		asked++
		askedLong = long
		return makeEvents("lists the directory, breaking down each flag"), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents(bundled), noopCancel, nil, nil, explain, "")
	m = drainStream(m, 2)
	m = press(t, m, "x")

	if asked != 1 || !askedLong {
		t.Errorf("`x` asked for %d explanations, long=%v", asked, askedLong)
	}
	if m.Phase() != phaseExplain {
		t.Errorf("`x` did not open the long form: phase %v", m.Phase())
	}
}

func TestResult_SilentModeIgnoresASentenceItDidNotAskFor(t *testing.T) {
	m := NewGenerateModel(makeEvents(bundled), noopCancel, nil, nil, nil, "").
		WithExplain(ExplainNone)
	m = drainStream(m, 2)

	if strings.Contains(m.View().Content, bundledSentence) {
		t.Errorf("silent mode showed the sentence anyway:\n%s", m.View().Content)
	}
	if got := press(t, m, "enter").Result().Explanation; got != "" {
		t.Errorf("silent mode handed the caller %q", got)
	}
}

func TestResult_TheStreamNeverShowsTheSentence(t *testing.T) {
	// The response arrives token by token and the sentence is in the middle
	// of it, ahead of the alternatives — the first thing that could be shown
	// as something to run.
	m := NewGenerateModel(
		makeEvents("ls -la", "\n--- expla", "nation\nLists the directory."),
		noopCancel, nil, nil, nil, "")
	for i := 0; i < 3; i++ {
		m = drainStream(m, 1)
		view := m.View().Content
		if strings.Contains(view, "expla") || strings.Contains(view, "Lists the directory") {
			t.Errorf("the explanation rendered as command text mid-stream:\n%s", view)
		}
	}

	m = drainStream(m, 1)
	if !strings.Contains(m.View().Content, "Lists the directory.") {
		t.Errorf("the sentence that never showed did not become the explanation:\n%s", m.View().Content)
	}
}

func TestResult_TheSentenceReachesTheCaller(t *testing.T) {
	m := drainStream(NewGenerateModel(makeEvents(bundled), noopCancel, nil, nil, nil, ""), 2)
	if got := press(t, m, "enter").Result().Explanation; got != bundledSentence {
		t.Errorf("the result carries %q", got)
	}

	// One that was asked for separately is the same sentence and reaches the
	// caller the same way.
	streamed := drainExplainStream(armed(t, "ls -la", mockExplainStream("lists the directory")), 2)
	if got := press(t, streamed, "enter").Result().Explanation; got != "lists the directory" {
		t.Errorf("a streamed sentence reached the caller as %q", got)
	}

	// The long form is a block and not a sentence, so nothing is handed on.
	long := NewGenerateModel(makeEvents(bundled), noopCancel, nil, nil,
		mockExplainStream("lists the directory, breaking down each flag"), "").WithExplain(ExplainLong)
	long = drainExplainStream(drainStream(long, 2), 2)
	if got := press(t, long, "enter").Result().Explanation; got != "" {
		t.Errorf("the long form was handed on as a sentence: %q", got)
	}
}

// Naming a command to keep it is where the sentence is worth most: the
// caller files the snippet under it, and the alternative is a summarising
// request the save would have to stand still for.
func TestResult_SavingCarriesTheSentenceAlreadyOnScreen(t *testing.T) {
	asked := 0
	m := drainStream(NewGenerateModel(makeEvents(bundled), noopCancel, nil, nil, refuseExplain(&asked), ""), 2)

	m = press(t, m, "s")
	if m.Phase() != phaseSave {
		t.Fatalf("[s] did not ask for a name: phase %v", m.Phase())
	}
	for _, r := range "listing" {
		m = press(t, m, string(r))
	}

	res := press(t, m, "enter").Result()
	if res.Action != ActionSave || res.SaveName != "listing" {
		t.Fatalf("the save came back as %v named %q", res.Action, res.SaveName)
	}
	if res.Explanation != bundledSentence {
		t.Errorf("the save carries %q", res.Explanation)
	}
	if asked != 0 {
		t.Errorf("saving asked for an explanation %d times", asked)
	}
}

func TestResult_ChoosingAnAlternativeDropsTheSentenceAboutTheOther(t *testing.T) {
	asked := 0
	explain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		asked++
		return makeEvents("counts the entries instead"), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents(bundled+"\n--- alternatives\nls -la | wc -l"),
		noopCancel, nil, nil, explain, "")
	m = drainStream(m, 2)
	m = press(t, press(t, press(t, m, "a"), "down"), "enter")

	if strings.Contains(m.View().Content, bundledSentence) {
		t.Errorf("the sentence about the command it replaced is still on screen:\n%s", m.View().Content)
	}
	if asked != 1 {
		t.Fatalf("the chosen command was explained %d times", asked)
	}
	m = drainExplainStream(m, 2)
	if !strings.Contains(m.View().Content, "counts the entries instead") {
		t.Errorf("the chosen command did not get its own sentence:\n%s", m.View().Content)
	}
}

func TestResult_AnEditDropsTheSentenceAboutWhatItReplaced(t *testing.T) {
	m := drainStream(NewGenerateModel(makeEvents(bundled), noopCancel, nil, nil, nil, ""), 2)
	m = press(t, m, "e")
	m.editInput.SetValue("ls -lah")
	m = press(t, m, "enter")

	if strings.Contains(m.View().Content, bundledSentence) {
		t.Errorf("a sentence said about the command before the edit survived it:\n%s", m.View().Content)
	}
}

func TestResult_SteppingBackRestoresTheSentenceWithTheCommand(t *testing.T) {
	newStream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return makeEvents("ls\n--- explanation\nLists names only."), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents(bundled), noopCancel, nil, newStream, nil, "")
	m = drainStream(m, 2)
	m = press(t, m, "r")
	for _, r := range "shorter" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")
	m = drainStream(m, 2)

	if !strings.Contains(m.View().Content, "Lists names only.") {
		t.Fatalf("the revised command did not bring its own sentence:\n%s", m.View().Content)
	}
	m = press(t, m, "u")
	if !strings.Contains(m.View().Content, bundledSentence) {
		t.Errorf("stepping back did not bring the sentence back with the command:\n%s", m.View().Content)
	}
}
