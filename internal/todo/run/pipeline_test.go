package run

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

// notesProfile is a vocabulary with no grade at all, which is the shape a
// pipeline has to work without: nothing to rank, so nothing to spend
// differently on.
func notesProfile() todo.Profile {
	return todo.Profile{
		Name: "notes", Noun: "note",
		Fields: []todo.Field{todo.PriorityField()},
	}
}

// notesPipeline is a run with no verify, no commit and no lanes: read the
// item, have it read back, file it. It is the shape the whole refactor is
// for — a backlog whose work is not built, tested and committed.
func notesPipeline() Pipeline {
	return Pipeline{Name: "notes", Steps: []PipelineStep{
		{Name: "gather", Kind: KindTurn, Access: Read,
			Builtin: "GATHER. Read the sources.", Blocks: []string{PlaceholderItem}},
		{Name: "read", Kind: KindAgent, Access: Read,
			Builtin: "READ what was gathered.", TaskBuiltin: "READ this, which you did not gather.",
			Blocks: []string{PlaceholderItem}},
		{Name: "file", Kind: KindFinish, Access: Read, Finish: FinishArchive},
	}}
}

func note() todo.Item {
	return todo.Item{Slug: "n", Title: "Read the papers", Profile: notesProfile(),
		Priority: todo.PriorityMedium, Path: "/r/.shhh/todo/n.md",
		Body: "## Acceptance criteria\n- [ ] read"}
}

// A pipeline of three steps runs an item to done with no changeset, no
// supervisor and no repository, and it draws three stages. Nothing in the
// machine is about building, testing or committing; those are steps the code
// pipeline has and this one does not.
func TestPipeline_AShortRunFinishesWithoutARepository(t *testing.T) {
	p := notesPipeline()
	if err := p.Validate(); err != nil {
		t.Fatalf("the pipeline does not validate: %v", err)
	}
	it := note()
	s := Start(it, "sess", "manual", 1, Options{Pipeline: p})
	if strip := s.Shape().Strip(); len(strip) != 3 {
		t.Fatalf("the row draws %v, want three stages", strip)
	}
	if !s.NoCommit {
		t.Error("a pipeline with no commit is a run that makes none")
	}
	first := s.First(it, "")
	if first.Action != ActionPrompt || first.Stage != "gather" || first.Mode != ModePlan {
		t.Fatalf("first = %+v", first)
	}
	if !strings.Contains(first.Prompt, "GATHER") || !strings.Contains(first.Prompt, "BACKLOG ITEM n") {
		t.Fatalf("the gather prompt = %q", first.Prompt)
	}
	if strings.Contains(first.Prompt, "questions:") || strings.Contains(first.Prompt, "## Plan:") {
		t.Fatalf("a step that reads neither must not ask for either:\n%s", first.Prompt)
	}
	step := s.Observe(it, "I read three papers.")
	if step.Action != ActionReview || step.Stage != "read" {
		t.Fatalf("after gather = %+v", step)
	}
	// No supervisor: the reading is taken in the session's own turn, which
	// is the fallback every agent step has.
	step = s.SelfReview(it)
	if step.Action != ActionPrompt || !strings.Contains(step.Prompt, "verdict: clean") {
		t.Fatalf("self reading = %+v", step)
	}
	step = s.Observe(it, "verdict: clean")
	if step.Action != ActionDone || step.Stage != StageDone || !s.Over() {
		t.Fatalf("a clean reading should file the item: %+v", step)
	}
	if !strings.Contains(s.Report, "## Report") {
		t.Errorf("the archive should carry a report:\n%s", s.Report)
	}
}

// The validator refuses the shapes a free list makes possible and a fixed
// skeleton would have prevented.
func TestPipeline_ValidateRefusesTheShapesARunCannotTake(t *testing.T) {
	for _, c := range []struct {
		name string
		p    Pipeline
		says string
	}{{
		"no finish",
		Pipeline{Name: "a", Steps: []PipelineStep{{Name: "think", Kind: KindTurn, Access: Read}}},
		"no finish step",
	}, {
		"a gate after a write",
		Pipeline{Name: "b", Steps: []PipelineStep{
			{Name: "build", Kind: KindTurn, Access: Write},
			{Name: "ask", Kind: KindGate},
			{Name: "file", Kind: KindFinish, Finish: FinishArchive},
		}},
		"cannot come after it was",
	}, {
		"a fan-out with nothing to integrate it",
		Pipeline{Name: "c", Steps: []PipelineStep{
			{Name: "divide", Kind: KindFanOut, Access: Write},
			{Name: "file", Kind: KindFinish, Finish: FinishArchive},
		}},
		"no turn after it integrates them",
	}, {
		"a commit in a run that never writes",
		Pipeline{Name: "d", Steps: []PipelineStep{
			{Name: "read", Kind: KindTurn, Access: Read},
			{Name: "land", Kind: KindFinish, Finish: FinishCommit},
		}},
		"no step in it changes the tree",
	}} {
		err := c.p.Validate()
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: err = %v, want one saying %q", c.name, err, c.says)
		}
	}
	if err := BuiltinCode().Validate(); err != nil {
		t.Fatalf("the built-in pipeline must validate: %v", err)
	}
	if err := notesPipeline().Validate(); err != nil {
		t.Fatalf("a short pipeline must validate: %v", err)
	}
}

// The strip is the steps a run always passes through; the ones it only
// sometimes takes are said under the step they belong to.
func TestPipeline_StripIsTheStepsWithAPlaceOfTheirOwn(t *testing.T) {
	p := BuiltinCode()
	strip := p.Strip()
	want := []Stage{StageResearch, StageImplement, StageVerify, StageReview, StageCommit}
	if len(strip) != len(want) {
		t.Fatalf("strip = %v, want %v", strip, want)
	}
	for i, stage := range want {
		if strip[i] != stage || p.Place(stage) != i {
			t.Errorf("%s is at %d on a strip of %v", stage, p.Place(stage), strip)
		}
	}
	for stage, at := range map[Stage]int{
		"pause": 0, StageSplit: 1, StageFanOut: 1, StageRemediate: 1,
		StageDone: -1, StageBlocked: -1, Stage("nonsense"): -1,
	} {
		if got := p.Place(stage); got != at {
			t.Errorf("Place(%s) = %d, want %d", stage, got, at)
		}
	}
}

// What a session must be able to do is what the pipeline's steps ask for,
// step by step. A run that never writes wants no changeset, and one that
// never commits wants no repository.
func TestPipeline_RefusesPerStepAndNotPerSession(t *testing.T) {
	code := BuiltinCode()
	ref, refused := code.Refuse(Can{Changeset: false, Supervisor: true, Runner: true, Repo: true})
	if !refused || ref.Need != NeedChangeset || !strings.Contains(ref.Why, "does not track changes") {
		t.Fatalf("a writing run in a session with no changeset = %+v %v", ref, refused)
	}
	if ref.Step != "implement" {
		t.Errorf("the refusal names %q, want the first step that writes", ref.Step)
	}
	ref, refused = code.Refuse(Can{Changeset: true, Supervisor: true, Runner: true, Repo: false})
	if !refused || ref.Need != NeedRepo || ref.Step != "commit" {
		t.Fatalf("a committing run outside a repository = %+v %v", ref, refused)
	}
	// The division into lanes happens at one grade and falls back where it
	// cannot happen at all, so it asks for no supervisor up front.
	if _, refused := code.Refuse(Can{Changeset: true, Supervisor: false, Runner: true, Repo: true}); refused {
		t.Error("a run that only sometimes fans out must not want a supervisor before it starts")
	}
	// A run without a commit is a run whose finish is the archive, and it
	// wants no repository.
	without := Options{NoCommit: true}.Steps()
	if _, refused := without.Refuse(Can{Changeset: true, Supervisor: false, Runner: true, Repo: false}); refused {
		t.Error("a run asked for without a commit must not want a repository")
	}
	// A pipeline that never writes, never commits and runs no command asks
	// a read-only session for nothing at all.
	if _, refused := notesPipeline().Refuse(Can{}); refused {
		t.Error("a run of reading steps must be allowed in a session that can do none of that")
	}
}

// A run picked up after the profile's steps changed is refused rather than
// carried on: the work already done was planned for a shape that is gone.
func TestPipeline_AContinuedRunRefusesADifferentShape(t *testing.T) {
	it := item("M")
	s := Start(it, "sess", "manual", 1, Options{Repo: true})
	s.Stage = StageImplement
	if step := s.Continue(it); step.Action != ActionPrompt {
		t.Fatalf("an unchanged pipeline must continue: %+v", step)
	}
	s.Stage = StageImplement
	s.Pipeline = notesPipeline()
	step := s.Continue(it)
	if step.Action != ActionBlocked || !strings.Contains(s.Blocked, "steps have changed") {
		t.Fatalf("a changed pipeline must refuse: %+v %q", step, s.Blocked)
	}
}

// A rule stated by grade is read from both ends of whatever scale the
// profile has, so one pipeline gates a two-grade backlog exactly as it gates
// a three-grade one.
func TestPipeline_RulesAreReadFromBothEndsOfTheScale(t *testing.T) {
	gate, _ := BuiltinCode().Step("pause")
	fix, _ := BuiltinCode().Step("remediate")
	for _, c := range []struct {
		name   string
		rank   int
		grades int
		pause  PauseRule
		rounds int
	}{
		{"three-grade smallest", 1, 3, PauseNever, 1},
		{"three-grade middle", 2, 3, PauseQuestionsOrUpgraded, 2},
		{"three-grade largest", 3, 3, PauseAlways, 2},
		{"two-grade smallest", 1, 2, PauseNever, 1},
		{"two-grade largest", 2, 2, PauseAlways, 2},
		{"ungraded", 0, 3, PauseQuestionsOrUpgraded, 2},
	} {
		if got := gate.pauseAt(c.rank, c.grades); got != c.pause {
			t.Errorf("%s pauses %q, want %q", c.name, got, c.pause)
		}
		if got := fix.roundsAt(c.rank, c.grades); got != c.rounds {
			t.Errorf("%s spends %d rounds, want %d", c.name, got, c.rounds)
		}
	}
}

// The wording keys are the pipeline's step names, so a profile with a step
// nobody thought of still has a file its words can be tuned with — and the
// built-in run's seven keys are exactly the ones a settings file has always
// named.
func TestPipeline_WordingKeysAreTheStepNames(t *testing.T) {
	got := strings.Join(BuiltinCode().WordingKeys(), " ")
	want := "standards research implement review review_task remediate commit"
	if got != want {
		t.Fatalf("keys = %q, want %q", got, want)
	}
	if keys := notesPipeline().WordingKeys(); strings.Join(keys, " ") != "standards gather read read_task" {
		t.Fatalf("a profile's own steps name their own keys, got %v", keys)
	}
	// Every key the pipeline names has built-in words behind it, or a
	// scaffold of the set would write an empty file.
	builtins := BuiltinCode().Builtins()
	for _, key := range BuiltinCode().WordingKeys() {
		if strings.TrimSpace(builtins[key]) == "" {
			t.Errorf("%s has no built-in wording", key)
		}
	}
}

// The digest is what a checkpoint says the run's shape by, and it moves with
// any change to a step the machine reads.
func TestPipeline_DigestMovesWithTheShape(t *testing.T) {
	if (Pipeline{}).Digest() != "" {
		t.Fatal("a pipeline with no steps must digest to nothing")
	}
	code := BuiltinCode()
	if code.Digest() != BuiltinCode().Digest() {
		t.Fatal("the same pipeline must digest the same")
	}
	if code.Archiving().Digest() == code.Digest() {
		t.Fatal("a run that archives instead of committing is a different shape")
	}
	if notesPipeline().Digest() == code.Digest() {
		t.Fatal("two pipelines must not digest alike")
	}
}

// Every way a run can end is one the machine carries out. A commit spends a
// turn and hands the message to the driver; a note spends a turn for the
// report alone; a command runs and the exit status decides; an archive and a
// hook end it where they stand.
func TestFinish_EveryEndingIsCarriedOut(t *testing.T) {
	it := note()
	end := func(f Finish, command string) (*State, Step) {
		p := Pipeline{Name: "x", Steps: []PipelineStep{
			{Name: "do", Kind: KindTurn, Access: Write, Builtin: "DO."},
			{Name: "end", Kind: KindFinish, Access: Read, Finish: f, Command: command,
				Builtin: "END.", Blocks: []string{PlaceholderItem}},
		}}
		if err := p.Validate(); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		s := Start(it, "sess", "manual", 1, Options{Repo: true, Pipeline: p})
		s.First(it, "")
		s.Paths = []string{"a.md"}
		return s, s.Observe(it, "did it")
	}

	s, step := end(FinishCommit, "")
	if step.Action != ActionPrompt || !strings.Contains(step.Prompt, "COMMIT:") {
		t.Fatalf("a commit finish asks for the message: %+v", step)
	}
	if step := s.Observe(it, "COMMIT:\nsubject\n\nbody\n\nREPORT:\n## Report\nSummary: did it\n"); step.Action != ActionCommit {
		t.Fatalf("after the commit turn = %+v", step)
	}

	s, step = end(FinishNote, "")
	if step.Action != ActionPrompt || strings.Contains(step.Prompt, "COMMIT:") || !strings.Contains(step.Prompt, "REPORT:") {
		t.Fatalf("a note finish asks for the report alone: %+v", step)
	}
	if step := s.Observe(it, "REPORT:\n## Report\nSummary: wrote it up\n"); step.Action != ActionDone ||
		!strings.Contains(s.Report, "wrote it up") {
		t.Fatalf("a note finish archives what the turn wrote: %+v %q", step, s.Report)
	}
	if step := s.Observe(it, "nothing in the asked shape"); step.Action != ActionBlocked {
		t.Fatal("a note finish with no report must block")
	}

	s, step = end(FinishCommand, "make ship")
	if step.Action != ActionVerify || step.Command != "make ship" {
		t.Fatalf("a command finish runs its command: %+v", step)
	}
	if step := s.VerifyResult(it, true, "ok"); step.Action != ActionDone || !s.Over() {
		t.Fatalf("a command finish that passed ends the run: %+v", step)
	}

	for _, f := range []Finish{FinishArchive, FinishHook} {
		s, step := end(f, "")
		if step.Action != ActionDone || !s.Over() || !strings.Contains(s.Report, "## Report") {
			t.Fatalf("%s = %+v", f, step)
		}
	}
}
