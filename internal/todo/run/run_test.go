package run

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

func item(size todo.Size) todo.Item {
	return todo.Item{Slug: "x", Title: "Do x", Size: size, Priority: todo.PriorityMedium, Path: "/r/.shhh/todo/x.md", Body: "## Acceptance criteria\n- [ ] works"}
}

const planText = "## Plan: do x\n\n1. Read the thing\n   files: a.go\n   action: read\n2. Change it\n   files: a.go\n   action: edit\n\nsize: S\nquestions: none\n"

func TestRun_HappyPathSmall(t *testing.T) {
	it := item(todo.SizeM)
	s := Start(it, "sess", "manual", 3, Options{Repo: true})
	first := s.First(it, "")
	if first.Action != ActionPrompt || first.Mode != ModePlan || !strings.Contains(first.Prompt, "RESEARCH") || !strings.Contains(first.Prompt, "BACKLOG ITEM x") {
		t.Fatalf("first = %+v", first)
	}
	step := s.Observe(it, planText)
	if step.Action != ActionPrompt || step.Stage != StageImplement || step.Mode != ModeAuto || s.Size != todo.SizeS || len(s.Steps) != 2 {
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
		size   todo.Size
		rounds int
	}{{todo.SizeS, 1}, {todo.SizeM, 2}, {todo.SizeL, 2}, {"", 2}} {
		it := item(c.size)
		s := Start(it, "", "", 0, Options{Repo: true})
		s.First(it, "")
		s.Observe(it, strings.Replace(planText, "size: S", "size: "+string(c.size), 1))
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
	it := item(todo.SizeM)
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
		it := item(todo.SizeS)
		s := Start(it, "", "", 0, Options{Repo: true})
		s.First(it, "")
		if step := s.Observe(it, text); step.Action != ActionBlocked || s.Blocked == "" {
			t.Errorf("%s: %+v", name, step)
		}
	}
	it := item(todo.SizeS)
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	s.Observe(it, "## Plan: x\n\n1. a\n\nsize: L\nquestions: none\n")
	if s.Size != todo.SizeL || s.SizeBefore != todo.SizeS || !strings.Contains(s.Summary(), "size L (was S)") {
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
	it := item(todo.SizeS)
	it.Body = "## Tests\n- `go test ./a`\n- go vet ./...\n-\n\n## Notes\n- not a test"
	s := Start(it, "", "", 0, Options{Repo: true})
	if got := strings.Join(s.Tests, "|"); got != "go test ./a|go vet ./..." {
		t.Errorf("tests = %q", got)
	}
}

func TestCheckpoint_RoundTrips(t *testing.T) {
	root := t.TempDir()
	s := Start(item(todo.SizeS), "sess", "manual", 2, Options{Repo: true})
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
		before        todo.Size
		text          string
		action        Action
		pausedContain string
	}{
		{"S clean", todo.SizeS, plan("S", "none"), ActionPrompt, ""},
		{"S question blocks", todo.SizeS, plan("S", "which flag?"), ActionBlocked, ""},
		{"M clean", todo.SizeM, plan("M", "none"), ActionPrompt, ""},
		{"M question pauses", todo.SizeM, plan("M", "which flag?"), ActionPause, "questions"},
		{"M upgraded from S pauses", todo.SizeS, plan("M", "none"), ActionPause, "up from S"},
		{"M downgraded to S continues", todo.SizeM, plan("S", "none"), ActionPrompt, ""},
		{"L always pauses", todo.SizeL, plan("L", "none"), ActionPause, "large"},
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
	it := item(todo.SizeL)
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
	it := item(todo.SizeM)
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
	small := item(todo.SizeS)
	ss := Start(small, "", "", 0, Options{Repo: true})
	ss.First(small, "")
	ss.Observe(small, planText)
	ss.Observe(small, "done")
	if step := ss.VerifyResult(small, true, ""); step.Action != ActionPrompt || ss.Reviewer != "" {
		t.Fatalf("S reviews itself: %+v", step)
	}
}

func TestContinue_ReentersEachStage(t *testing.T) {
	it := item(todo.SizeM)
	for stage, want := range map[Stage]Action{StageResearch: ActionPrompt, StageImplement: ActionPrompt, StageRemediate: ActionPrompt, StageVerify: ActionVerify, StageReview: ActionReview, StageCommit: ActionPrompt, StageDone: ActionBlocked, StageBlocked: ActionBlocked} {
		s := Start(it, "", "", 0, Options{Repo: true})
		s.Stage, s.Plan, s.Size, s.Message = stage, "## Plan: x\n\n1. a", todo.SizeM, "stale"
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
	it := item(todo.SizeL)
	s := Start(it, "", "", 0, Options{Repo: true})
	s.Stage, s.Paused, s.Plan = StageResearch, "a large item pauses", "## Plan: x\n\n1. a"
	if step := s.Continue(it); step.Action != ActionPause || s.Paused == "" {
		t.Fatalf("continue at a pause = %+v", step)
	}
}

func TestReview_NamesAreNeverReused(t *testing.T) {
	it := item(todo.SizeM)
	s := Start(it, "", "", 0, Options{Repo: true})
	s.Size = todo.SizeM
	first := s.review(it).Shown
	s.Reviewer = ""
	second := s.review(it).Shown
	if first == second || !strings.HasSuffix(first, "-1") || !strings.HasSuffix(second, "-2") {
		t.Fatalf("names = %q %q", first, second)
	}
}

// A run asked for without a commit ends at the review: it archives with a
// report that names the paths the work is in and says it was not committed,
// rather than going on to a commit turn with nothing to commit into.
func TestRun_WithoutACommitEndsAfterTheReview(t *testing.T) {
	it := item(todo.SizeM)
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
	it := item(todo.SizeM)
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
	it := item(todo.SizeM)
	for _, c := range []struct {
		name  string
		build func(repo bool) string
		want  string
	}{
		{"review", func(repo bool) string { return reviewPrompt(it, planText, repo) }, "`git diff`"},
		{"commit", func(repo bool) string { return commitPrompt(it, planText, repo) }, "git log -10"},
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
	it := item(todo.SizeM)
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
	it := item(todo.SizeM)
	s := Start(it, "sess", "manual", 1, Options{Repo: true})
	s.Stage = StageCommit
	s.Paths = []string{"a.go"}

	if step := s.Continue(it); step.Action != ActionPrompt || step.Stage != StageCommit {
		t.Fatalf("a committing run should still ask for its message: %+v", step)
	}

	s.Stage = StageCommit
	s.NoCommit = true
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
	it := item(todo.SizeM)
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
	it := item(todo.SizeM)
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
	it := item(todo.SizeM)
	s := Start(it, "", "", 0, Options{Repo: true})
	s.First(it, "")
	s.Observe(it, planText)
	if s.ClosesWithGate() {
		t.Error("implement closes with a gate the workspace never asked for")
	}
}

func TestChecks_CarriesOnlyAPassAndIsSpentByTheVerify(t *testing.T) {
	it := item(todo.SizeM)
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
	it := item(todo.SizeM)
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
	strip := Strip()
	if len(strip) != 5 {
		t.Fatalf("the strip is the five stages a run passes through, got %v", strip)
	}
	for i, stage := range strip {
		if got := Place(stage); got != i {
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
		if got := Place(stage); got != want {
			t.Errorf("Place(%s) = %d, want %d", stage, got, want)
		}
	}
}

// A review's verdict is on the checkpoint, because it is the review's whole
// answer and a run picked up in another session has no other way to know it.
func TestReviewVerdictIsRecorded(t *testing.T) {
	it := todo.Item{Slug: "x", Size: todo.SizeS}
	s := Start(it, "sess", "manual", 1, Options{})
	s.Plan = "## Plan\n\n1. a\n"
	s.review(it)
	if step := s.ReviewResult(it, "verdict: findings\n- the flag is never read"); step.Stage != StageRemediate {
		t.Fatalf("findings should remediate, got %s", step.Stage)
	}
	if s.Verdict != "findings" {
		t.Errorf("verdict = %q, want findings", s.Verdict)
	}
	s.review(it)
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

	s := Start(item(todo.SizeS), "sess-a", "", 0, Options{})
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
