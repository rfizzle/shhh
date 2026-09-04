package run

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

func item(size string) todo.Item {
	return todo.Item{Slug: "x", Title: "Do x", Fields: map[string]string{"size": size}, Profile: todo.BuiltinCode(), Priority: todo.PriorityMedium, Path: "/r/.shhh/todo/x.md", Body: "## Acceptance criteria\n- [ ] works"}
}

const planText = "## Plan: do x\n\n1. Read the thing\n   files: a.go\n   action: read\n2. Change it\n   files: a.go\n   action: edit\n\nsize: S\nquestions: none\n"

func TestRun_HappyPathSmall(t *testing.T) {
	it := item("M")
	s := Start(it, "sess", "manual", 3, Options{Repo: true})
	first := s.First(it, "")
	if first.Action != ActionPrompt || first.Mode != ModePlan || !strings.Contains(first.Prompt, "RESEARCH") || !strings.Contains(first.Prompt, "BACKLOG ITEM x") {
		t.Fatalf("first = %+v", first)
	}
	step := s.Observe(it, planText)
	if step.Action != ActionPrompt || step.Stage != StageImplement || step.Mode != ModeAuto || s.Grade != "S" || len(s.Steps) != 2 {
		t.Fatalf("after research = %+v, state %+v", step, s)
	}
	if !strings.Contains(step.Prompt, "APPROVED PLAN") || !strings.Contains(step.Prompt, "tick its checkbox") {
		t.Fatalf("implement prompt = %q", step.Prompt)
	}
	step = s.Observe(it, "Changed a.go.")
	if step.Action != ActionVerify {
		t.Fatalf("after implement = %+v", step)
	}
	step = s.VerifyResult(it, true, "")
	if step.Action != ActionPrompt || step.Stage != StageReview || step.Mode != ModePlan || !s.Verified {
		t.Fatalf("after verify = %+v", step)
	}
	step = s.Observe(it, "Looked at everything.\nblocked: is what the item said\nverdict: clean\n")
	if step.Action != ActionPrompt || step.Stage != StageCommit || step.Mode != ModePlan {
		t.Fatalf("after review = %+v", step)
	}
	step = s.Observe(it, "COMMIT:\nDo the x thing\n\nBecause.\n\nREPORT:\n## Report\nSummary: did x\n")
	if step.Action != ActionCommit || s.Message != "Do the x thing\n\nBecause." || !strings.HasPrefix(s.Report, "## Report") {
		t.Fatalf("after commit turn = %+v, msg %q", step, s.Message)
	}
	step = s.Committed([]string{"a.go"})
	if step.Action != ActionDone || !s.Over() || s.Files[0] != "a.go" {
		t.Fatalf("done = %+v", step)
	}
}

func TestRun_RemediationRoundsBySize(t *testing.T) {
	for _, c := range []struct {
		size   string
		rounds int
	}{{"S", 1}, {"M", 2}, {"L", 2}, {"", 2}} {
		it := item(c.size)
		s := Start(it, "", "", 0, Options{Repo: true})
		s.First(it, "")
		s.Observe(it, strings.Replace(planText, "size: S", "size: "+c.size, 1))
		s.Observe(it, "done")
		var step Step
		for i := 0; i <= c.rounds; i++ {
			step = s.VerifyResult(it, false, "FAIL a_test.go")
			if i < c.rounds {
				if step.Action != ActionPrompt || step.Stage != StageRemediate || !strings.Contains(step.Prompt, "FAIL a_test.go") || s.Round != i+1 {
					t.Fatalf("%s round %d: %+v", c.size, i, step)
				}
				if back := s.Observe(it, "fixed"); back.Action != ActionVerify {
					t.Fatalf("remediate should go back to verify, got %+v", back)
				}
			}
		}
		if step.Action != ActionBlocked || !strings.Contains(s.Blocked, "remediation rounds spent") {
			t.Fatalf("%s: after rounds spent = %+v", c.size, step)
		}
	}
}

func TestRun_ReviewFindingsThenClean(t *testing.T) {
	it := item("M")
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	s.Observe(it, strings.Replace(planText, "size: S", "size: M", 1))
	s.Observe(it, "done")
	s.VerifyResult(it, true, "")
	step := s.Observe(it, "verdict: findings\n1. a.go:3 off by one")
	if step.Stage != StageRemediate || !strings.Contains(step.Prompt, "off by one") {
		t.Fatalf("findings = %+v", step)
	}
	s.Observe(it, "fixed")
	s.VerifyResult(it, true, "")
	if step := s.Observe(it, "no verdict here"); step.Action != ActionBlocked {
		t.Fatalf("a review without a verdict should block, got %+v", step)
	}
}

func TestRun_ResearchGates(t *testing.T) {
	cases := map[string]string{
		"open question": "## Plan: x\n\n1. a\n\nsize: S\nquestions:\n- keep the old flag?\n",
		"no plan":       "I would change a.go.\nsize: S\nquestions: none",
		"blocked":       "blocked: the item asks for a file that was deleted",
		"empty":         "   ",
	}
	for name, text := range cases {
		it := item("S")
		s := Start(it, "", "", 0, Options{Repo: true})
		s.First(it, "")
		if step := s.Observe(it, text); step.Action != ActionBlocked || s.Blocked == "" {
			t.Errorf("%s: %+v", name, step)
		}
	}
	it := item("S")
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	s.Observe(it, "## Plan: x\n\n1. a\n\nsize: L\nquestions: none\n")
	if s.Grade != "L" || s.GradeBefore != "S" || !strings.Contains(s.Summary(), "size L (was S)") {
		t.Errorf("regrade not recorded: %s", s.Summary())
	}
}

func TestParsers(t *testing.T) {
	if qs := questionLines("questions:\n- a?\n* b?\nnot a bullet\n- c?"); strings.Join(qs, "|") != "a?|b?" {
		t.Errorf("questions = %v", qs)
	}
	if qs := questionLines("Questions: Is it on?\n\n- later"); strings.Join(qs, "|") != "Is it on?" {
		t.Errorf("inline question = %v", qs)
	}
	if v, f := verdictLine("Verdict: FINDINGS\n1. x"); v != "findings" || f != "1. x" {
		t.Errorf("verdict = %q %q", v, f)
	}
	if _, _, ok := commitParts("REPORT: r\nCOMMIT: c"); ok {
		t.Error("markers out of order accepted")
	}
	if m, r, ok := commitParts("COMMIT:\n```text\nSubject\n\nBody\n```\nREPORT:\n## Report\nx"); !ok || m != "Subject\n\nBody" || r != "## Report\nx" {
		t.Errorf("fenced commit = %q %q %v", m, r, ok)
	}
}

func TestTestCommands_SnapshotAtStart(t *testing.T) {
	it := item("S")
	it.Body = "## Tests\n- `go test ./a`\n- go vet ./...\n-\n\n## Notes\n- not a test"
	s := Start(it, "", "", 0, Options{Repo: true})
	if got := strings.Join(s.Tests, "|"); got != "go test ./a|go vet ./..." {
		t.Errorf("tests = %q", got)
	}
}

func TestCheckpoint_RoundTrips(t *testing.T) {
	root := t.TempDir()
	s := Start(item("S"), "sess", "manual", 2, Options{Repo: true})
	s.Stage = StageVerify
	s.Round = 1
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	back, err := Load(root, "x")
	if err != nil || back.Stage != StageVerify || back.Round != 1 || back.PrevMode != "manual" || back.Turn != 2 {
		t.Fatalf("loaded = %+v, %v", back, err)
	}
	Discard(root, "x")
	if _, err := Load(root, "x"); err == nil {
		t.Fatal("checkpoint survived Discard")
	}
}

func TestRun_PauseGatesBySize(t *testing.T) {
	plan := func(size string, questions string) string {
		return "## Plan: x\n\n1. a\n\nsize: " + size + "\nquestions: " + questions + "\n"
	}
	cases := []struct {
		name          string
		before        string
		text          string
		action        Action
		pausedContain string
	}{
		{"S clean", "S", plan("S", "none"), ActionPrompt, ""},
		{"S question blocks", "S", plan("S", "which flag?"), ActionBlocked, ""},
		{"M clean", "M", plan("M", "none"), ActionPrompt, ""},
		{"M question pauses", "M", plan("M", "which flag?"), ActionPause, "questions"},
		{"M upgraded from S pauses", "S", plan("M", "none"), ActionPause, "up from S"},
		{"M downgraded to S continues", "M", plan("S", "none"), ActionPrompt, ""},
		{"L always pauses", "L", plan("L", "none"), ActionPause, "large"},
		{"ungraded to M continues", "", plan("M", "none"), ActionPrompt, ""},
	}
	for _, c := range cases {
		it := item(c.before)
		s := Start(it, "", "", 0, Options{Repo: true})
		s.First(it, "")
		step := s.Observe(it, c.text)
		if step.Action != c.action || !strings.Contains(s.Paused, c.pausedContain) {
			t.Errorf("%s: action=%v paused=%q", c.name, step.Action, s.Paused)
		}
		if step.Action == ActionPrompt && step.Stage != StageImplement {
			t.Errorf("%s: should go to implement", c.name)
		}
	}
}

func TestRun_ResumeAndReplan(t *testing.T) {
	it := item("L")
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	large := strings.Replace(planText, "size: S", "size: L", 1)
	if step := s.Observe(it, large); step.Action != ActionPause {
		t.Fatalf("L should pause: %+v", step)
	}
	step := s.Replan(it, "keep the old flag")
	if step.Action != ActionPrompt || step.Stage != StageResearch || step.Mode != ModePlan || !strings.Contains(step.Prompt, "ANSWERS AND STEERING") || !strings.Contains(step.Prompt, "keep the old flag") || s.Paused != "" {
		t.Fatalf("replan = %+v", step)
	}
	s.Observe(it, large)
	step = s.Resume(it)
	// A large item is divided before it is built, in the read-only mode,
	// with the person's steering in front of the split.
	if step.Action != ActionPrompt || step.Stage != StageSplit || step.Mode != ModePlan || s.Paused != "" || !strings.Contains(step.Prompt, "keep the old flag") {
		t.Fatalf("resume = %+v", step)
	}
	if step := s.Resume(it); step.Action != ActionBlocked {
		t.Fatal("resume without a pause should block")
	}
}

func TestRun_ReviewBySize(t *testing.T) {
	it := item("M")
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	s.Observe(it, strings.Replace(planText, "size: S", "size: M", 1))
	s.Observe(it, "done")
	step := s.VerifyResult(it, true, "")
	if step.Action != ActionReview || s.Reviewer != "todo-review-x-1" || !strings.Contains(s.ReviewTask(it, ""), "Review this change") {
		t.Fatalf("M review = %+v reviewer=%q", step, s.Reviewer)
	}
	if task := s.ReviewTask(it, "+++ b/a.go"); !strings.Contains(task, "```diff\n+++ b/a.go") {
		t.Fatalf("task lacks the diff: %q", task)
	}
	if step := s.SelfReview(it); step.Action != ActionPrompt || s.Reviewer != "" || !strings.Contains(step.Shown, "no reviewer agent") {
		t.Fatalf("self review = %+v", step)
	}
	if step := s.ReviewResult(it, ""); step.Action != ActionBlocked {
		t.Fatal("an empty report should block")
	}
	s.Stage = StageReview
	s.Blocked = ""
	if step := s.ReviewResult(it, "looked\nverdict: findings\n1. bad"); step.Action != ActionPrompt || step.Stage != StageRemediate {
		t.Fatalf("findings from the child = %+v", step)
	}
	s.Observe(it, "fixed")
	step = s.VerifyResult(it, true, "")
	if s.Reviewer != "todo-review-x-2" {
		t.Fatalf("the second review is a new child: %q", s.Reviewer)
	}
	small := item("S")
	ss := Start(small, "", "", 0, Options{Repo: true})
	ss.First(small, "")
	ss.Observe(small, planText)
	ss.Observe(small, "done")
	if step := ss.VerifyResult(small, true, ""); step.Action != ActionPrompt || ss.Reviewer != "" {
		t.Fatalf("S reviews itself: %+v", step)
	}
}

func TestContinue_ReentersEachStage(t *testing.T) {
	it := item("M")
	for stage, want := range map[Stage]Action{StageResearch: ActionPrompt, StageImplement: ActionPrompt, StageRemediate: ActionPrompt, StageVerify: ActionVerify, StageReview: ActionReview, StageCommit: ActionPrompt, StageDone: ActionBlocked, StageBlocked: ActionBlocked} {
		s := Start(it, "", "", 0, Options{Repo: true})
		s.Stage, s.Plan, s.Grade, s.Message = stage, "## Plan: x\n\n1. a", "M", "stale"
		step := s.Continue(it)
		if step.Action != want {
			t.Errorf("%s: action %v, want %v", stage, step.Action, want)
		}
		if stage == StageCommit && s.Message != "" {
			t.Error("a continued commit stage forgets the stale message")
		}
	}
}

func TestContinue_AtAPauseReshowsTheCard(t *testing.T) {
	it := item("L")
	s := Start(it, "", "", 0, Options{Repo: true})
	s.Stage, s.Paused, s.Plan = StageResearch, "a large item pauses", "## Plan: x\n\n1. a"
	if step := s.Continue(it); step.Action != ActionPause || s.Paused == "" {
		t.Fatalf("continue at a pause = %+v", step)
	}
}

func TestReview_NamesAreNeverReused(t *testing.T) {
	it := item("M")
	s := Start(it, "", "", 0, Options{Repo: true})
	s.Grade = "M"
	first := s.reading(it).Shown
	s.Reviewer = ""
	second := s.reading(it).Shown
	if first == second || !strings.HasSuffix(first, "-1") || !strings.HasSuffix(second, "-2") {
		t.Fatalf("names = %q %q", first, second)
	}
}

// A run asked for without a commit ends at the review: it archives with a
// report that names the paths the work is in and says it was not committed,
// rather than going on to a commit turn with nothing to commit into.
func TestRun_WithoutACommitEndsAfterTheReview(t *testing.T) {
	it := item("M")
	s := Start(it, "sess", "manual", 1, Options{NoCommit: true})
	s.First(it, "")
	s.Observe(it, planText)
	s.Observe(it, "Changed a.go.")
	s.VerifyResult(it, true, "")
	s.Paths = []string{"a.go", "a_test.go"}

	step := s.Observe(it, "verdict: clean")
	if step.Action != ActionDone || step.Stage != StageDone || !s.Over() {
		t.Fatalf("a clean review without a commit should archive: %+v", step)
	}
	if !strings.Contains(step.Shown, "not committed") {
		t.Errorf("the run row should say so: %q", step.Shown)
	}
	if strings.Join(s.Files, ",") != "a.go,a_test.go" {
		t.Errorf("the archived files = %v, want the run's paths", s.Files)
	}
	for _, want := range []string{"## Report", "not committed", "a.go", "a_test.go"} {
		if !strings.Contains(s.Report, want) {
			t.Errorf("the report does not carry %q:\n%s", want, s.Report)
		}
	}
	if !strings.Contains(s.Summary(), "not committed") {
		t.Errorf("the summary should say a run makes no commit: %q", s.Summary())
	}
}

// The default is unchanged: nothing asked for means a commit turn follows a
// clean review, as it always did.
func TestRun_ACommitIsStillTheDefaultEnd(t *testing.T) {
	it := item("M")
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	s.Observe(it, planText)
	s.Observe(it, "Changed a.go.")
	s.VerifyResult(it, true, "")
	if step := s.Observe(it, "verdict: clean"); step.Stage != StageCommit || step.Action != ActionPrompt {
		t.Fatalf("a clean review should ask for a commit message: %+v", step)
	}
}

// Every stage prompt that names a git command reads one fact, so outside a
// repository none of them tells the model to run something that will fail.
func TestPrompts_GitStepsAreConditionalOnARepository(t *testing.T) {
	it := item("M")
	for _, c := range []struct {
		name  string
		build func(repo bool) string
		want  string
	}{
		{"review", func(repo bool) string {
			return promptAt("review", it, promptArgs{plan: planText, repo: repo}, Wordings{})
		}, "`git diff`"},
		{"commit", func(repo bool) string { return promptAt("commit", it, promptArgs{repo: repo}, Wordings{}) }, "git log -10"},
	} {
		if got := c.build(true); !strings.Contains(got, c.want) {
			t.Errorf("%s in a repository should still ask for %s:\n%s", c.name, c.want, got)
		}
		got := c.build(false)
		if strings.Contains(got, c.want) {
			t.Errorf("%s outside a repository asks for %s anyway:\n%s", c.name, c.want, got)
		}
		if !strings.Contains(got, "not a git repository") && !strings.Contains(got, "no history here") {
			t.Errorf("%s outside a repository does not say why:\n%s", c.name, got)
		}
	}
}

// The reviewer child cannot run commands, so outside a repository the diff
// in its task is the whole of what changed and the task says so — a
// reviewer that believes there is history behind it reports a file as
// unchanged when what it found was no repository to ask.
func TestReviewTask_WithoutARepositorySaysTheDiffIsAllThereIs(t *testing.T) {
	it := item("M")
	const patch = "diff --git a/a.go b/a.go\n+++ b/a.go\n+x\n"

	s := Start(it, "", "", 0, Options{NoCommit: true})
	task := s.ReviewTask(it, patch)
	if !strings.Contains(task, patch) {
		t.Errorf("the task should carry the changeset diff:\n%s", task)
	}
	if !strings.Contains(task, "not a git repository") {
		t.Errorf("the task should say there is no history behind it:\n%s", task)
	}

	inRepo := Start(it, "", "", 0, Options{Repo: true})
	if got := inRepo.ReviewTask(it, patch); strings.Contains(got, "not a git repository") {
		t.Errorf("a task inside a repository should not carry the note:\n%s", got)
	}
}

// A checkpoint parked at the commit stage, continued after the run was
// asked for without a commit, archives instead of taking the commit turn.
// Re-sending the prompt there would make exactly the commit the person had
// just said not to make.
func TestRun_ContinueAtCommitHonoursANoCommitRun(t *testing.T) {
	it := item("M")
	s := Start(it, "sess", "manual", 1, Options{Repo: true})
	s.Stage = StageCommit
	s.Paths = []string{"a.go"}

	if step := s.Continue(it); step.Action != ActionPrompt || step.Stage != StageCommit {
		t.Fatalf("a committing run should still ask for its message: %+v", step)
	}

	// A run asked for without a commit is a run whose finish is the archive,
	// which is what the flag and the setting both amount to.
	s = Start(it, "sess", "manual", 1, Options{Repo: true, NoCommit: true})
	s.Stage = StageCommit
	s.Paths = []string{"a.go"}
	step := s.Continue(it)
	if step.Action != ActionDone || step.Stage != StageDone || !s.Over() {
		t.Fatalf("a continued run without a commit should archive: %+v", step)
	}
	if !strings.Contains(s.Report, "not committed") || !strings.Contains(s.Report, "a.go") {
		t.Errorf("the report should say so and name the path:\n%s", s.Report)
	}
}

// TestRun_ResearchCarriesTheSprintGoal pins what an item is told about the
// set it belongs to: the goal in the research stage, in the continued and
// re-planned research too, and nothing at all without a sprint.
func TestRun_ResearchCarriesTheSprintGoal(t *testing.T) {
	const goal = "Make the provider cache trustworthy end to end."
	it := item("M")
	s := Start(it, "sess", "manual", 1, Options{Repo: true, Sprint: goal})
	for _, step := range []Step{s.First(it, ""), s.Continue(it), s.Replan(it, "answer")} {
		if !strings.Contains(step.Prompt, "SPRINT") || !strings.Contains(step.Prompt, goal) {
			t.Fatalf("research prompt lacks the sprint goal:\n%s", step.Prompt)
		}
	}
	// The later stages do not repeat it: what the set is for scopes the
	// research and nothing after it.
	s.First(it, "")
	if implement := s.Observe(it, planText); strings.Contains(implement.Prompt, "SPRINT") {
		t.Fatalf("the implement prompt repeats the sprint goal:\n%s", implement.Prompt)
	}

	bare := Start(it, "sess", "manual", 1, Options{Repo: true})
	if step := bare.First(it, ""); strings.Contains(step.Prompt, "SPRINT") {
		t.Fatalf("a session with no sprint sent a sprint heading:\n%s", step.Prompt)
	}
}

func TestClosesWithGate_IsTheImplementStageAndNoOther(t *testing.T) {
	it := item("M")
	s := Start(it, "", "", 0, Options{Repo: true, CloseGate: true})
	if s.ClosesWithGate() {
		t.Error("research closes with the gate")
	}
	s.First(it, "")
	if step := s.Observe(it, planText); step.Stage != StageImplement {
		t.Fatalf("stage = %s, want implement", step.Stage)
	}
	if !s.ClosesWithGate() {
		t.Error("implement does not close with the gate")
	}
	s.Observe(it, "Changed a.go.")
	if s.Stage != StageVerify || s.ClosesWithGate() {
		t.Errorf("verify closes with the gate (stage %s)", s.Stage)
	}
	var none *State
	if none.ClosesWithGate() {
		t.Error("no run at all closes with the gate")
	}
}

func TestClosesWithGate_IsOffWhereTheWorkspaceNamesNoSuite(t *testing.T) {
	it := item("M")
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	s.Observe(it, planText)
	if s.ClosesWithGate() {
		t.Error("implement closes with a gate the workspace never asked for")
	}
}

func TestChecks_CarriesOnlyAPassAndIsSpentByTheVerify(t *testing.T) {
	it := item("M")
	s := Start(it, "", "", 0, Options{Repo: true, CloseGate: true})
	s.Checks(false)
	if s.Checked {
		t.Error("a failing verdict was carried to the verify stage")
	}
	s.Checks(true)
	if !s.Checked {
		t.Fatal("a passing verdict was not carried")
	}
	s.VerifyResult(it, true, "")
	if s.Checked {
		t.Error("the verdict outlived the verify it was for")
	}
}

func TestContinue_DropsAVerdictReachedInAnotherSitting(t *testing.T) {
	it := item("M")
	s := Start(it, "", "", 0, Options{Repo: true, CloseGate: true})
	s.Stage, s.Checked, s.Plan = StageVerify, true, planText
	if step := s.Continue(it); step.Action != ActionVerify {
		t.Fatalf("continue at verify = %+v", step)
	}
	if s.Checked {
		t.Error("a continued run took a verdict from the session before it")
	}
}

// The words a run's record is keyed on and the words a surface draws it with
// are one vocabulary. Every stage a run passes through is a word some step
// can be named, and every step names itself with either its stage or its
// action and nothing else — so a row drawn from one and a record written from
// the other cannot describe the same transition differently.
func TestStepNameIsTheStageForATurnAndTheActionOtherwise(t *testing.T) {
	for _, c := range []struct {
		step Step
		want string
	}{
		{Step{Action: ActionPrompt, Stage: StageResearch}, "research"},
		{Step{Action: ActionPrompt, Stage: StageImplement}, "implement"},
		{Step{Action: ActionPrompt, Stage: StageRemediate}, "remediate"},
		{Step{Action: ActionPrompt, Stage: StageCommit}, "commit"},
		{Step{Action: ActionVerify, Stage: StageVerify}, "verify"},
		{Step{Action: ActionReview, Stage: StageReview}, "review"},
		{Step{Action: ActionFanOut, Stage: StageFanOut}, "fan-out"},
		{Step{Action: ActionCommit, Stage: StageCommit}, "commit"},
		{Step{Action: ActionPause, Stage: StageResearch}, "pause"},
		{Step{Action: ActionWait, Stage: StageFanOut}, "wait"},
		{Step{Action: ActionDone, Stage: StageDone}, "done"},
		{Step{Action: ActionBlocked, Stage: StageBlocked}, "blocked"},
	} {
		if got := c.step.Name(); got != c.want {
			t.Errorf("Step{%v, %s}.Name() = %q, want %q", c.step.Action, c.step.Stage, got, c.want)
		}
	}
}

// The strip is the stages in the order a run takes them, each of them a word
// a step can be named, and each of them placed. A stage the strip drew that
// no step could name would be a word only the row knows.
func TestStripStagesArePlacedAndNameable(t *testing.T) {
	strip := BuiltinCode().Strip()
	if len(strip) != 5 {
		t.Fatalf("the strip is the five stages a run passes through, got %v", strip)
	}
	for i, stage := range strip {
		if got := BuiltinCode().Place(stage); got != i {
			t.Errorf("Place(%s) = %d, want %d", stage, got, i)
		}
		if got := (Step{Action: ActionPrompt, Stage: stage}).Name(); got != string(stage) {
			t.Errorf("no step names %s: got %q", stage, got)
		}
	}
	// The stages a run only sometimes takes report the strip stage they
	// belong to, and the ends of a run sit nowhere in it.
	for stage, want := range map[Stage]int{
		StageSplit: 1, StageFanOut: 1, StageRemediate: 1,
		StageDone: -1, StageBlocked: -1, Stage("nonsense"): -1,
	} {
		if got := BuiltinCode().Place(stage); got != want {
			t.Errorf("Place(%s) = %d, want %d", stage, got, want)
		}
	}
}

// A review's verdict is on the checkpoint, because it is the review's whole
// answer and a run picked up in another session has no other way to know it.
func TestReviewVerdictIsRecorded(t *testing.T) {
	it := todo.Item{Slug: "x", Fields: map[string]string{"size": "S"}, Profile: todo.BuiltinCode()}
	s := Start(it, "sess", "manual", 1, Options{})
	s.Plan = "## Plan\n\n1. a\n"
	s.reading(it)
	if step := s.ReviewResult(it, "verdict: findings\n- the flag is never read"); step.Stage != StageRemediate {
		t.Fatalf("findings should remediate, got %s", step.Stage)
	}
	if s.Verdict != "findings" {
		t.Errorf("verdict = %q, want findings", s.Verdict)
	}
	s.reading(it)
	if step := s.ReviewResult(it, "verdict: clean"); step.Stage != StageCommit {
		t.Fatalf("clean should commit, got %s", step.Stage)
	}
	if s.Verdict != "clean" {
		t.Errorf("verdict = %q, want clean", s.Verdict)
	}
}

// A checkpoint that is not over is what makes an item held, and the surfaces
// that would change it under a run read it here. Both files are checked
// because a sprint records the item it has taken before that item has a
// checkpoint of its own.
func TestHeldBy(t *testing.T) {
	root := t.TempDir()
	if _, held := HeldBy(root, "x"); held {
		t.Error("an item with no checkpoint reads as held")
	}

	s := Start(item("S"), "sess-a", "", 0, Options{})
	s.Stage = StageImplement
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	h, held := HeldBy(root, "x")
	if !held || h.Session != "sess-a" || h.Stage != StageImplement || h.Sprint {
		t.Fatalf("hold = %+v, held = %v", h, held)
	}

	// A run that ended holds nothing: its item is archived or blocked, and
	// what happens to it next is the person's to decide.
	s.Stage = StageDone
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, held := HeldBy(root, "x"); held {
		t.Error("a finished run still holds its item")
	}

	Discard(root, "x")
	sp := StartSprint("sess-b", "", 0, false)
	sp.Current = "x"
	if err := sp.Save(root); err != nil {
		t.Fatal(err)
	}
	if h, held := HeldBy(root, "x"); !held || h.Session != "sess-b" || !h.Sprint {
		t.Fatalf("sprint hold = %+v, held = %v", h, held)
	}
	if _, held := HeldBy(root, "y"); held {
		t.Error("the sprint holds an item it is not on")
	}
}

// The grooming prompt names the item, offers the closed verdict set and no
// other word, and asks for the shape the reader parses.
func TestRun_TheGroomingPromptOffersTheClosedSet(t *testing.T) {
	p := GroomPrompt(BuiltinCode(), item("M"))
	if !strings.Contains(p, "GROOMING") || !strings.Contains(p, "BACKLOG ITEM x") {
		t.Fatalf("prompt = %q", p)
	}
	for _, v := range todo.Verdicts() {
		if !strings.Contains(p, string(v)) {
			t.Errorf("prompt does not offer %q", v)
		}
	}
	for _, marker := range []string{"claim:", "verdict:", "now:", "evidence:"} {
		if !strings.Contains(p, marker) {
			t.Errorf("prompt does not name the %q line", marker)
		}
	}
}

// What a reading is told to check is the profile's and the only part that
// is: a checkout of code is asked about its tree, a reading list about its
// sources, and both answer in the same verdicts to the same markers.
func TestRun_TheGroomingPromptChecksWhatTheProfileNames(t *testing.T) {
	code := GroomPrompt(BuiltinCode(), item("M"))
	if !strings.Contains(code, "every `path:line`") {
		t.Fatalf("the code profile's own questions are not in its prompt:\n%s", code)
	}
	it := item("M")
	it.Profile.Name = "research"
	it.Profile.Groom = "Check every source the question names is still there.\n\n{{item}}"
	other := GroomPrompt(BuiltinCode(), it)
	switch {
	case !strings.Contains(other, "Check every source the question names"):
		t.Errorf("the profile's own questions are not asked:\n%s", other)
	case strings.Contains(other, "every `path:line`"):
		t.Errorf("another profile's questions were asked:\n%s", other)
	case !strings.Contains(other, "BACKLOG ITEM x") || !strings.Contains(other, "verdict:"):
		t.Errorf("the item and the answer shape are the runner's:\n%s", other)
	}
}

// A reading the person accepted rides in the research stage and nowhere
// else: repeating it at every stage would spend tokens restating what only
// the plan is built from.
func TestRun_ResearchCarriesTheAcceptedGrooming(t *testing.T) {
	it := item("M")
	const block = "GROOMING — this item was read against the tree on 2026-09-04"
	s := Start(it, "sess", "manual", 1, Options{Repo: true, Groomed: block})
	if first := s.First(it, ""); !strings.Contains(first.Prompt, block) {
		t.Fatalf("research prompt = %q", first.Prompt)
	}
	step := s.Observe(it, planText)
	if strings.Contains(step.Prompt, block) {
		t.Errorf("the implement prompt restated the reading: %q", step.Prompt)
	}
	// A run continued from a checkpoint taken at the research stage states
	// the reading it was started with, which is the reason the block is in
	// the checkpoint at all.
	kept := Start(it, "sess", "manual", 1, Options{Repo: true, Groomed: block})
	if again := kept.Continue(it); !strings.Contains(again.Prompt, block) {
		t.Errorf("continued research = %q", again.Prompt)
	}
}

// A run with no accepted reading says nothing about one.
func TestRun_ResearchSaysNothingWithoutAGrooming(t *testing.T) {
	it := item("M")
	s := Start(it, "sess", "manual", 1, Options{Repo: true})
	if first := s.First(it, ""); strings.Contains(first.Prompt, "GROOMING") {
		t.Fatalf("research prompt = %q", first.Prompt)
	}
}

// Every stage takes its wording from the set the run was started with, and a
// stage nothing replaced keeps the built-in words.
func TestWordings_EachStageTakesItsOwnAndOnlyItsOwn(t *testing.T) {
	it := item("M")
	w := Wordings{
		"research":    "MY RESEARCH",
		"implement":   "MY IMPLEMENT",
		"review":      "MY REVIEW",
		"review_task": "MY REVIEW TASK",
		"remediate":   "MY REMEDIATE",
		"commit":      "MY COMMIT",
		"standards":   "MY STANDARDS",
	}
	for _, tc := range []struct {
		name    string
		built   func(w Wordings) string
		mine    string
		builtin string
	}{
		{"research", func(w Wordings) string { return promptAt("research", it, promptArgs{}, w) }, "MY RESEARCH", "RESEARCH stage"},
		{"implement", func(w Wordings) string { return promptAt("implement", it, promptArgs{plan: planText}, w) }, "MY IMPLEMENT", "IMPLEMENT stage"},
		{"review", func(w Wordings) string { return promptAt("review", it, promptArgs{plan: planText, repo: true}, w) }, "MY REVIEW", "REVIEW stage"},
		{"review task", func(w Wordings) string {
			return promptAt("review", it, promptArgs{plan: planText, diff: "diff", repo: true, task: true}, w)
		}, "MY REVIEW TASK", "Review this change against"},
		{"remediate", func(w Wordings) string { return promptAt("remediate", it, promptArgs{findings: "a finding"}, w) }, "MY REMEDIATE", "REMEDIATE stage"},
		{"commit", func(w Wordings) string { return promptAt("commit", it, promptArgs{repo: true}, w) }, "MY COMMIT", "COMMIT stage"},
	} {
		got := tc.built(w)
		if !strings.Contains(got, tc.mine) || strings.Contains(got, tc.builtin) {
			t.Errorf("%s did not take its wording:\n%s", tc.name, got)
		}
		if !strings.Contains(got, "BACKLOG ITEM x") {
			t.Errorf("%s dropped the item block:\n%s", tc.name, got)
		}
		if unset := tc.built(Wordings{}); !strings.Contains(unset, tc.builtin) || strings.Contains(unset, tc.mine) {
			t.Errorf("%s with nothing set did not keep the built-in words:\n%s", tc.name, unset)
		}
	}
}

// The standards sentence is one wording shared by the stages that change the
// tree, and the stages that only read do not carry it either way.
func TestWordings_StandardsIsSharedByTheStagesThatChangeTheTree(t *testing.T) {
	it := item("M")
	w := Wordings{"standards": "MY STANDARDS"}
	for _, got := range []string{
		promptAt("research", it, promptArgs{}, w),
		promptAt("implement", it, promptArgs{plan: planText}, w),
		promptAt("remediate", it, promptArgs{findings: "a finding"}, w),
		laneTask(it, planText, Lane{Name: "one", Paths: []string{"a.go"}, Task: "build a"}, "", w, BuiltinCode()),
		integratePrompt(it, planText, []Lane{{Name: "one", Paths: []string{"a.go"}}}, "", w, BuiltinCode()),
	} {
		if !strings.Contains(got, "MY STANDARDS") || strings.Contains(got, "Read AGENTS.md") {
			t.Errorf("a stage kept the built-in standards sentence:\n%s", got)
		}
	}
	// The grooming pass is not a stage of a run, so a file that replaced a
	// stage's wording does not reach it: its instruction and its standards
	// sentence are the profile's, and this run's replacements are the run's.
	if groom := GroomPrompt(BuiltinCode(), it); strings.Contains(groom, "MY STANDARDS") {
		t.Errorf("the grooming pass took a run's wording:\n%s", groom)
	}
}

// A wording that says nothing about the answer still produces a prompt that
// asks for it, and the answer that shape asks for is one Observe reads.
func TestWordings_TheAnswerShapeIsAppendedWhateverTheWordingSays(t *testing.T) {
	it := item("M")
	w := Wordings{
		"research":  "Read the code. Say nothing else.",
		"implement": "Write the code.",
		"review":    "Read the change.",
		"remediate": "Fix it.",
		"commit":    "Say what you did.",
	}
	s := Start(it, "sess", "manual", 1, Options{Repo: true, Wordings: w})
	first := s.First(it, "")
	for _, want := range []string{"size: S|M|L", "questions: none", "blocked: <why>"} {
		if !strings.Contains(first.Prompt, want) {
			t.Fatalf("the research shape lost %q:\n%s", want, first.Prompt)
		}
	}
	step := s.Observe(it, planText)
	if step.Stage != StageImplement || s.Grade != "S" {
		t.Fatalf("the answer to a replaced wording was not read: %+v", step)
	}
	if !strings.Contains(step.Prompt, "Do not commit; the runner commits") {
		t.Fatalf("the implement shape was edited out:\n%s", step.Prompt)
	}
	s.Observe(it, "Changed a.go.")
	review := s.VerifyResult(it, true, "")
	if review.Stage != StageReview || !strings.Contains(review.Prompt, "verdict: clean") {
		t.Fatalf("the review shape was edited out: %+v", review)
	}
	if commit := s.Observe(it, "verdict: clean"); commit.Stage != StageCommit || !strings.Contains(commit.Prompt, "COMMIT:") {
		t.Fatalf("the commit shape was edited out: %+v", commit)
	}
}

// A wording that names a block places it; one that does not takes it after
// the instruction, in the order the built-in has them.
func TestWordings_PlaceholdersArePlacedWhereNamedAndAppendedWhereNot(t *testing.T) {
	it := item("M")
	named := promptAt("implement", it, promptArgs{plan: planText, answers: "ANSWERS HERE"}, Wordings{
		"implement": "before\n\n" + PlaceholderPlan + "\n\nbetween\n\n" + PlaceholderItem + "\n\nafter",
	})
	if at := strings.Index(named, "APPROVED PLAN"); at < 0 || at > strings.Index(named, "BACKLOG ITEM x") {
		t.Fatalf("a named block was not placed where the wording put it:\n%s", named)
	}
	if !strings.Contains(named, "between") || !strings.Contains(named, "ANSWERS HERE") {
		t.Fatalf("an unnamed block was dropped:\n%s", named)
	}
	unnamed := promptAt("implement", it, promptArgs{plan: planText, answers: "ANSWERS HERE"}, Wordings{"implement": "just the instruction"})
	item, plan := strings.Index(unnamed, "BACKLOG ITEM x"), strings.Index(unnamed, "APPROVED PLAN")
	if item < 0 || plan < item || strings.Index(unnamed, "ANSWERS HERE") < plan {
		t.Fatalf("the unnamed blocks are not in the built-in order:\n%s", unnamed)
	}
	if strings.Contains(unnamed, PlaceholderItem) {
		t.Fatalf("a substitution reached the model as text:\n%s", unnamed)
	}
	// A stage with nothing for a block sends no empty space where it would
	// have been, named or not.
	empty := promptAt("remediate", it, promptArgs{}, Wordings{"remediate": "fix it\n\n" + PlaceholderFindings + "\n\nnow"})
	if strings.Contains(empty, "\n\n\n") {
		t.Fatalf("an empty block left a hole:\n%q", empty)
	}
}

// The digest is what a record says the wordings by, and it moves with an
// edit to any one of them.
func TestWordings_DigestMovesWithAnEdit(t *testing.T) {
	if (Wordings{}).Digest() != "" {
		t.Fatal("a run that replaced nothing must digest to nothing")
	}
	one := Wordings{"research": "as written"}
	if one.Digest() != (Wordings{"research": "as written"}).Digest() {
		t.Fatal("the same set must digest the same")
	}
	if one.Digest() == (Wordings{"research": "as edited"}).Digest() {
		t.Fatal("an edit must move the digest")
	}
	// Two wordings that swap texts are two different sets, not one.
	if (Wordings{"review": "as written"}).Digest() == one.Digest() {
		t.Fatal("which wording holds the text is part of the set")
	}
}

// A run picked up after the files moved says so on the row, because a run
// whose stages were asked different things is not one run's worth of work.
func TestWordings_AContinuedRunSaysTheWordsMoved(t *testing.T) {
	it := item("M")
	s := Start(it, "sess", "manual", 1, Options{Repo: true, Wordings: Wordings{"research": "as written"}})
	if step := s.Continue(it); strings.Contains(step.Shown, "wordings changed") {
		t.Fatalf("an unedited set must say nothing: %q", step.Shown)
	}
	s.Wordings = Wordings{"research": "as edited"}
	step := s.Continue(it)
	if !strings.Contains(step.Shown, "wordings changed") {
		t.Fatalf("a continued run must say the words moved: %q", step.Shown)
	}
	if !strings.Contains(step.Prompt, "as edited") {
		t.Fatalf("a continued run must send the words as they now are:\n%s", step.Prompt)
	}
	if again := s.Continue(it); strings.Contains(again.Shown, "wordings changed") {
		t.Fatalf("the run says it once, not at every stage: %q", again.Shown)
	}
}

// A substitution written mid-sentence stays mid-sentence. The spacing around
// it is the file's, and a builder that broke the sentence into paragraphs
// around the block would be editing the wording rather than filling it in.
func TestWordings_AnInlinePlaceholderKeepsItsSentence(t *testing.T) {
	it := item("M")
	got := promptAt("remediate", it, promptArgs{findings: "off by one"}, Wordings{
		"remediate": "Please fix " + PlaceholderFindings + " carefully.",
	})
	if !strings.HasPrefix(got, "Please fix off by one carefully.") {
		t.Fatalf("an inline substitution was reflowed:\n%q", got)
	}
	// One a wording named twice is placed twice, because a file that wrote
	// it twice meant it twice.
	twice := promptAt("remediate", it, promptArgs{findings: "off by one"}, Wordings{
		"remediate": PlaceholderFindings + " — and again: " + PlaceholderFindings,
	})
	if strings.Count(twice, "off by one") != 2 {
		t.Fatalf("a repeated substitution was placed once:\n%q", twice)
	}
}

// The built-in wordings place their own blocks, so every stage shows the
// model what it is about to be told about before it is told about it. A
// project's file that names no substitution gets its blocks after the
// instruction instead, which is the only order a builder can choose for
// prose it did not write.
func TestWordings_TheBuiltInStagesShowTheBlocksBeforeTheInstructions(t *testing.T) {
	it := item("M")
	for _, tc := range []struct {
		name         string
		built        string
		block, after string
	}{
		{"research", promptAt("research", it, promptArgs{}, Wordings{}), "BACKLOG ITEM x", "Work out exactly how"},
		{"implement", promptAt("implement", it, promptArgs{plan: planText}, Wordings{}), "APPROVED PLAN", "Touch only what the plan names"},
		{"review", promptAt("review", it, promptArgs{plan: planText, repo: true}, Wordings{}), "APPROVED PLAN", "Check, in this order"},
		{"review task", promptAt("review", it, promptArgs{plan: planText, diff: "@@ diff @@", repo: true, task: true}, Wordings{}), "THE CHANGE", "Read every file the diff touches"},
		{"remediate", promptAt("remediate", it, promptArgs{findings: "off by one"}, Wordings{}), "off by one", "Do not commit."},
		{"commit", promptAt("commit", it, promptArgs{repo: true}, Wordings{}), "BACKLOG ITEM x", "git log -10"},
	} {
		block, after := strings.Index(tc.built, tc.block), strings.Index(tc.built, tc.after)
		if block < 0 || after < 0 || block > after {
			t.Errorf("%s puts %q at %d and %q at %d:\n%s", tc.name, tc.block, block, tc.after, after, tc.built)
		}
	}
}

// researchProfile is a vocabulary with two grades rather than three, which
// is the shape the runner has to work without knowing either word: the
// first grade reviews itself and spends one round, the last is divided into
// lanes and gated before anything is built.
func researchProfile() todo.Profile {
	return todo.Profile{
		Noun: "question",
		Fields: []todo.Field{
			todo.PriorityField(),
			{Name: "depth", Values: []todo.Value{{Name: "quick"}, {Name: "deep"}}},
		},
		Grade: "depth",
	}
}

func graded(p todo.Profile, grade string) todo.Item {
	return todo.Item{Slug: "x", Title: "Do x", Profile: p, Priority: todo.PriorityMedium,
		Fields: map[string]string{p.Grade: grade}, Path: "/r/.shhh/todo/x.md",
		Body: "## Acceptance criteria\n- [ ] works"}
}

// The gates read the grade's rank, not its word: on a two-grade scale the
// first grade is the one the three-grade scale calls S and the last is the
// one it calls L, with nothing in between.
func TestRun_GatesReadTheRankAndNotTheWord(t *testing.T) {
	p := researchProfile()
	quick := graded(p, "quick")
	s := Start(quick, "", "", 0, Options{Repo: true})
	if s.Rounds() != 1 {
		t.Errorf("the smallest grade should spend one round, got %d", s.Rounds())
	}
	s.First(quick, "")
	if step := s.Observe(quick, "## Plan: x\n\n1. a\n\ndepth: quick\nquestions: none\n"); step.Stage != StageImplement {
		t.Fatalf("a quick item should go straight to implement: %+v", step)
	}
	s.Observe(quick, "done")
	if step := s.VerifyResult(quick, true, ""); step.Action != ActionPrompt || s.Reviewer != "" {
		t.Errorf("the smallest grade reviews itself: %+v", step)
	}

	deep := graded(p, "deep")
	d := Start(deep, "", "", 0, Options{Repo: true})
	if d.Rounds() != 2 {
		t.Errorf("a grade above the smallest should spend two rounds, got %d", d.Rounds())
	}
	d.First(deep, "")
	if step := d.Observe(deep, "## Plan: x\n\n1. a\n\ndepth: deep\nquestions: none\n"); step.Action != ActionPause {
		t.Fatalf("the largest grade pauses before anything is built: %+v", step)
	}
	if step := d.Resume(deep); step.Stage != StageSplit {
		t.Fatalf("the largest grade is divided into lanes: %+v", step)
	}
}

// The grade line the research stage answers with is the profile's own field
// and its own words: a line naming something else is not a grade.
func TestRun_GradeLineIsTheProfilesField(t *testing.T) {
	p := researchProfile()
	if got, ok := gradeLine(p, "size: L\ndepth: DEEP\n"); !ok || got != "deep" {
		t.Errorf("grade = %q %v", got, ok)
	}
	if _, ok := gradeLine(p, "depth: shallow\n"); ok {
		t.Error("a word off the scale is not a grade")
	}
}

// promptAt is one step of the built-in pipeline as a prompt, for the tests
// that build a step's words without a run around them.
func promptAt(name string, it todo.Item, a promptArgs, w Wordings) string {
	a.step, _ = BuiltinCode().Step(name)
	a.item = it
	return BuiltinCode().prompt(a, w, it.Profile)
}

// reading enters the pipeline's reading step directly, for the tests that
// are about what the step does rather than about how a run reaches it.
func (s *State) reading(it todo.Item) Step {
	ps, ok := s.readingStep()
	if !ok {
		return s.block("the pipeline has no reading step")
	}
	return s.readBy(it, ps)
}
