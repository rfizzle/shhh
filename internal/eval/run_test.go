package eval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// fakeShhh is a script standing in for the binary under measurement: it
// writes a transcript on stdout and does whatever the case needs doing to the
// workspace, so the runner can be measured without a provider.
func fakeShhh(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	path := filepath.Join(t.TempDir(), "fake-shhh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func suite(t *testing.T, check []string, files map[string]string) []Case {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sample")
	ws := filepath.Join(dir, WorkspaceDir)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	argv := "[\"" + strings.Join(check, "\", \"") + "\"]"
	body := "prompt = \"do the thing\"\ncheck = " + argv + "\n"
	if err := os.WriteFile(filepath.Join(dir, CaseFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

const emptyTranscript = `printf '{"success":true,"final":"done","usage":{"prompt_tokens":10,"completion_tokens":2},"messages":[]}'`

// The verdict is the check's, and nothing else. A session that claims success
// and changes nothing fails a case whose check says otherwise.
func TestASessionThatDidNothingFailsTheCheck(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, emptyTranscript)
	cases := suite(t, []string{"test", "-f", "fixed"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Verdict(); got != Failed {
		t.Fatalf("verdict = %v, want Failed", got)
	}
}

// And a session that did the work passes it.
func TestASessionThatDidTheWorkPasses(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "touch fixed\n"+emptyTranscript)
	cases := suite(t, []string{"test", "-f", "fixed"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Verdict(); got != Passed {
		t.Fatalf("verdict = %v, want Passed: %+v", got, sum.Results[0].Attempts)
	}
}

// The fixture is copied, so a case that edits its workspace does not edit
// itself — otherwise the second attempt grades the first one's leftovers.
func TestAnAttemptCannotEditTheCaseItself(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "echo changed > a.txt\n"+emptyTranscript)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "original"})

	if _, err := Run(context.Background(), cases, Options{Binary: bin, Repeat: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(cases[0].Workspace, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("the fixture was edited by the run: %q", got)
	}
}

// The session runs in a repository, because a session that is not in one
// behaves differently — its prompt, its undo and its grants all change.
func TestTheWorkspaceIsARepository(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "git rev-parse --show-toplevel > /dev/null 2>&1 || exit 3\n"+emptyTranscript)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if a := sum.Results[0].Attempts[0]; a.Err != nil {
		t.Fatalf("the session was not in a repository: %v", a.Err)
	}
}

// A session that fails is a run that broke, not a task that was failed, and
// the check must not even run.
func TestASessionThatFailsIsNotACaseThatFailed(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, "echo 'no provider' >&2; exit 1")
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Verdict(); got != Errored {
		t.Fatalf("verdict = %v, want Errored", got)
	}
	if a := sum.Results[0].Attempts[0]; a.Err == nil || !strings.Contains(a.Err.Error(), "no provider") {
		t.Errorf("the reason should reach the row: %v", a.Err)
	}
}

// The rounds are counted from the transcript rather than from anything the
// session says about itself.
func TestRoundsAndCallsAreReadFromTheTranscript(t *testing.T) {
	requireGit(t)
	const transcript = `printf '{"success":true,"usage":{"prompt_tokens":100,"completion_tokens":20},"messages":[` +
		`{"role":"user","content":"go"},` +
		`{"role":"assistant","tool_calls":[{"name":"read_file"},{"name":"search"}]},` +
		`{"role":"tool","content":"x"},` +
		`{"role":"assistant","tool_calls":[{"name":"edit_file"}]},` +
		`{"role":"assistant","content":"done"}]}'`
	bin := fakeShhh(t, transcript)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Rounds != 2 {
		t.Errorf("rounds = %d, want 2 — a message with calls is one round however many it asked for", a.Rounds)
	}
	if a.Calls != 3 {
		t.Errorf("calls = %d, want 3", a.Calls)
	}
	if a.TokensIn != 100 || a.TokensOut != 20 {
		t.Errorf("usage = %d/%d, want 100/20", a.TokensIn, a.TokensOut)
	}
}

func TestRepeatRunsEachCaseThatManyTimes(t *testing.T) {
	requireGit(t)
	bin := fakeShhh(t, emptyTranscript)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})

	sum, err := Run(context.Background(), cases, Options{Binary: bin, Repeat: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sum.Results[0].Attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// A case the machine cannot run costs no attempts at all.
func TestASkippedCaseIsNotRun(t *testing.T) {
	bin := fakeShhh(t, "exit 9")
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})
	cases[0].Skip = "not on PATH: cargo"

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Results[0].Attempts) != 0 {
		t.Fatal("a skipped case must not be attempted")
	}
	if got := sum.Results[0].Verdict(); got != Skipped {
		t.Fatalf("verdict = %v, want Skipped", got)
	}
}

// A fixture that could reach outside its own directory could edit the suite
// it is being graded by.
func TestAFixtureMayNotContainASymlink(t *testing.T) {
	requireGit(t)
	cases := suite(t, []string{"true"}, map[string]string{"a.txt": "x"})
	link := filepath.Join(cases[0].Workspace, "escape")
	if err := os.Symlink("/etc", link); err != nil {
		t.Skip("cannot create a symlink here")
	}

	bin := fakeShhh(t, emptyTranscript)
	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err == nil || !strings.Contains(a.Err.Error(), "symlink") {
		t.Fatalf("a symlinked fixture should be refused, got %v", a.Err)
	}
}

// fakeAux stands in for the endpoint an auxiliary call goes to. It answers
// from the evidence, because that is how the harness itself keys a row: one
// fake answers a whole table without the test having to know the order the
// rows are asked in.
type fakeAux struct {
	reply func(evidence string) provider.StreamEvent
	calls int
}

func (f *fakeAux) Name() string { return "fake" }

func (f *fakeAux) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	f.calls++
	evidence := ""
	if len(msgs) > 0 {
		evidence = msgs[len(msgs)-1].Content
	}
	ch := make(chan provider.StreamEvent, 1)
	ch <- f.reply(evidence)
	close(ch)
	return ch, nil
}

// decision is what a classifier that answered looks like on the wire.
func decision(verdict string) provider.StreamEvent {
	return provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{
			ID:        "d1",
			Name:      agent.DecisionToolName,
			Arguments: `{"decision":"` + verdict + `","reason":"because"}`,
		}},
		Usage: &provider.Usage{PromptTokens: 400, CompletionTokens: 30},
		Done:  true,
	}
}

// silence is the failure this shape exists to catch: a reply with no verdict
// in it, which is what an exhausted ceiling produces.
func silence() provider.StreamEvent {
	return provider.StreamEvent{Usage: &provider.Usage{PromptTokens: 400, CompletionTokens: 0}, Done: true}
}

func decisionCase(rows ...Row) Case {
	return Case{Name: "decisions", Kind: KindClassifier, Rows: rows}
}

func classifierRow(name, expect, marker string) Row {
	return Row{
		Name:      name,
		Expect:    []string{expect},
		Tool:      "execute_command",
		Arguments: `{"command":"` + marker + `"}`,
	}
}

func TestATableAttemptComparesEachRowsAnswerWithItsLabel(t *testing.T) {
	p := &fakeAux{reply: func(evidence string) provider.StreamEvent {
		if strings.Contains(evidence, "should-deny") {
			return decision("deny")
		}
		return decision("allow")
	}}
	c := decisionCase(
		classifierRow("fine", LabelAllow, "should-allow"),
		classifierRow("refused", LabelDeny, "should-deny"),
	)

	sum, err := Run(context.Background(), []Case{c}, Options{Provider: p, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	res := sum.Results[0]
	if res.Verdict() != Passed {
		t.Fatalf("verdict = %v, want Passed", res.Verdict())
	}
	score, ok := res.Score()
	if !ok {
		t.Fatal("a table case must report a score")
	}
	if score.Correct() != 2 || score.Wrong() != 0 || score.Unanswered() != 0 {
		t.Errorf("score = %d correct, %d wrong, %d unanswered", score.Correct(), score.Wrong(), score.Unanswered())
	}
}

// A wrong answer in the direction of allowing is the control failing open,
// and it must never be added to the annoyance on the other side.
func TestATableAttemptKeepsAFalseAllowApartFromAFalseDeny(t *testing.T) {
	p := &fakeAux{reply: func(evidence string) provider.StreamEvent {
		if strings.Contains(evidence, "answers-allow") {
			return decision("allow")
		}
		return decision("deny")
	}}
	c := decisionCase(
		classifierRow("let through", LabelDeny, "answers-allow"),
		classifierRow("refused", LabelAllow, "answers-deny"),
		classifierRow("right", LabelDeny, "answers-deny-too"),
	)

	sum, err := Run(context.Background(), []Case{c}, Options{Provider: p, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	score, _ := sum.Results[0].Score()
	if score.FalseAllow() != 1 {
		t.Errorf("false allow = %d, want 1", score.FalseAllow())
	}
	if score.FalseDeny() != 1 {
		t.Errorf("false deny = %d, want 1", score.FalseDeny())
	}
	if sum.Results[0].Verdict() != Failed {
		t.Errorf("verdict = %v, want Failed", sum.Results[0].Verdict())
	}
}

// The failure this whole shape exists for: a classifier that answers nothing
// is broken, and scoring its silence as caution would report an outage as a
// security posture.
func TestARowThatCameBackWithNothingIsItsOwnOutcome(t *testing.T) {
	p := &fakeAux{reply: func(evidence string) provider.StreamEvent {
		if strings.Contains(evidence, "no-answer") {
			return silence()
		}
		return decision("deny")
	}}
	c := decisionCase(
		classifierRow("silent", LabelDeny, "no-answer"),
		classifierRow("answered", LabelDeny, "answers-deny"),
	)

	sum, err := Run(context.Background(), []Case{c}, Options{Provider: p, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	score, _ := sum.Results[0].Score()
	if score.Unanswered() != 1 {
		t.Fatalf("unanswered = %d, want 1", score.Unanswered())
	}
	if score.Wrong() != 0 {
		t.Errorf("a silence is not a wrong answer: wrong = %d", score.Wrong())
	}
	if score.Correct() != 1 {
		t.Errorf("correct = %d, want 1", score.Correct())
	}
	if score.FalseAllow() != 0 || score.FalseDeny() != 0 {
		t.Errorf("a silence has no direction: %d false allow, %d false deny", score.FalseAllow(), score.FalseDeny())
	}
	if sum.Results[0].Verdict() != Failed {
		t.Errorf("a row nobody answered must not pass: %v", sum.Results[0].Verdict())
	}
}

// The numbers are what turn a score into a comparison, so a change to the
// model or the ceiling reads as a cost change and not only as a score change.
func TestATableAttemptCarriesWhatItCost(t *testing.T) {
	p := &fakeAux{reply: func(string) provider.StreamEvent { return decision("deny") }}
	c := decisionCase(
		classifierRow("one", LabelDeny, "a"),
		classifierRow("two", LabelDeny, "b"),
	)

	sum, err := Run(context.Background(), []Case{c}, Options{
		Provider: p,
		Model:    "m",
		Price:    func(_ string, in, out int) (float64, bool) { return float64(in+out) / 1000, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.TokensIn != 800 || a.TokensOut != 60 {
		t.Errorf("usage = %d in, %d out, want the rows summed", a.TokensIn, a.TokensOut)
	}
	if !a.Priced || a.Cost != 0.86 {
		t.Errorf("cost = %v (priced %v)", a.Cost, a.Priced)
	}
	if a.Rounds != 0 {
		t.Errorf("a table is not a loop and has no rounds: %d", a.Rounds)
	}
}

// A table case with nowhere to send its requests is a machine to fix, not a
// model that answered badly, so it reads as a run that broke.
func TestATableCaseWithNoProviderIsARunThatBroke(t *testing.T) {
	sum, err := Run(context.Background(), []Case{decisionCase(classifierRow("one", LabelDeny, "a"))}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Verdict(); got != Errored {
		t.Fatalf("verdict = %v, want Errored", got)
	}
}

// The summarizer's reading is the same shape with a different closed set, and
// a state the digest cannot support must come back as the state the model
// gave rather than as silence.
func TestASummaryTableComparesTheStateTheReadingCameBackWith(t *testing.T) {
	p := &fakeAux{reply: func(evidence string) provider.StreamEvent {
		state := "on_target"
		if strings.Contains(evidence, "drifted") {
			state = "off_target"
		}
		return provider.StreamEvent{
			ToolCalls: []provider.ToolCall{{
				ID:        "s1",
				Name:      agent.SummaryToolName,
				Arguments: `{"summary":"doing the thing","state":"` + state + `","reason":""}`,
			}},
			Usage: &provider.Usage{PromptTokens: 300, CompletionTokens: 20},
			Done:  true,
		}
	}}
	c := Case{Name: "state", Kind: KindSummary, Rows: []Row{
		{Name: "working", Expect: []string{LabelOnTarget}, Instruction: "fix the test", Assistant: "editing the file"},
		{Name: "wandered", Expect: []string{LabelOffTarget}, Instruction: "fix the test", Assistant: "drifted into the logging package"},
		{Name: "enough", Expect: []string{LabelSufficient}, Instruction: "find the cause", Assistant: "read it all"},
	}}

	sum, err := Run(context.Background(), []Case{c}, Options{Provider: p, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	score, ok := sum.Results[0].Score()
	if !ok {
		t.Fatal("a summary case must report a score")
	}
	if score.Correct() != 2 || score.Wrong() != 1 {
		t.Errorf("score = %d correct, %d wrong", score.Correct(), score.Wrong())
	}
	if score.FalseAllow() != 0 {
		t.Errorf("a reading has no allow to be false about: %d", score.FalseAllow())
	}
	if got := score.Misses(); len(got) != 1 || got[0].Row.Name != "enough" {
		t.Errorf("misses = %+v", got)
	}
}
