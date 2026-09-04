package run

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

// The kinds are a closed set the code carries out, and a pipeline naming
// anything else is refused rather than reached.
func TestKinds_AreClosed(t *testing.T) {
	for _, k := range Kinds() {
		if !k.Known() {
			t.Errorf("%s is in the set and not known", k)
		}
	}
	if Kind("script").Known() {
		t.Error("a kind nothing carries out reads as known")
	}
	for _, f := range Finishes() {
		if !f.Known() {
			t.Errorf("%s is in the set and not known", f)
		}
	}
	if Finish("push").Known() {
		t.Error("a finish nothing carries out reads as known")
	}
	for _, r := range PauseRules() {
		if !r.Known() {
			t.Errorf("%s is in the set and not known", r)
		}
	}
	if PauseRule("sometimes").Known() {
		t.Error("a pause rule nothing carries out reads as known")
	}
}

// A step that changes the tree runs in auto, because there is nobody to
// approve each edit for it; one that only reads runs read-only, so nothing
// can change under a reading.
func TestAccess_DecidesTheMode(t *testing.T) {
	if Write.Mode() != ModeAuto || Read.Mode() != ModePlan {
		t.Fatalf("write = %q, read = %q", Write.Mode(), Read.Mode())
	}
}

// A finish that writes the work somewhere a later reader can find it is what
// makes a run want a repository; a finish that spends a turn is what makes a
// run ask for one more answer.
func TestFinish_WritesAndTurns(t *testing.T) {
	for _, c := range []struct {
		f             Finish
		writes, turns bool
	}{
		{FinishCommit, true, true},
		{FinishArchive, false, false},
		{FinishNote, false, true},
		{FinishCommand, false, false},
		{FinishHook, false, false},
	} {
		if c.f.Writes() != c.writes || c.f.Turns() != c.turns {
			t.Errorf("%s writes=%v turns=%v", c.f, c.f.Writes(), c.f.Turns())
		}
	}
}

// The answer shape is the kind's and the step's reads, never the wording's:
// a step asks for exactly the marker lines the code reads back, and one that
// reads nothing in particular still asks to be told it could not finish.
func TestShape_IsBuiltFromTheKindAndWhatTheStepReads(t *testing.T) {
	code := todo.BuiltinCode()
	research, _ := BuiltinCode().Step("research")
	got := research.Shape(code, false)
	for _, want := range []string{"## Plan:", "`size: S|M|L`", "questions: none", "blocked: <why>"} {
		if !strings.Contains(got, want) {
			t.Errorf("the reading step's shape lost %q:\n%s", want, got)
		}
	}
	// The grade line is the profile's field and the profile's words, so a
	// backlog graded another way is asked for its own scale.
	two := research.Shape(researchProfile(), false)
	if !strings.Contains(two, "`depth: quick|deep`") || strings.Contains(two, "size") {
		t.Errorf("the grade line is not the profile's:\n%s", two)
	}

	implement, _ := BuiltinCode().Step("implement")
	if got := implement.Shape(code, false); strings.Contains(got, "questions:") || !strings.Contains(got, "blocked: <why>") {
		t.Errorf("a turn that reads nothing in particular still reports a block:\n%s", got)
	}

	review, _ := BuiltinCode().Step("review")
	if got := review.Shape(code, false); !strings.Contains(got, "Answer with one line `verdict: clean`") {
		t.Errorf("the reading's shape = %q", got)
	}
	if got := review.Shape(code, true); !strings.Contains(got, "End your report with") {
		t.Errorf("a child writes a report and ends it with the verdict, got %q", got)
	}

	commit, _ := BuiltinCode().Step("commit")
	if got := commit.Shape(code, false); !strings.Contains(got, "COMMIT:") || !strings.Contains(got, "REPORT:") {
		t.Errorf("the commit finish asks for both blocks:\n%s", got)
	}
	note := PipelineStep{Name: "file", Kind: KindFinish, Finish: FinishNote}
	if got := note.Shape(code, false); strings.Contains(got, "COMMIT:") || !strings.Contains(got, "REPORT:") {
		t.Errorf("a note finish asks for the report alone:\n%s", got)
	}
	// A command runs no model, so there is no answer to shape.
	verify, _ := BuiltinCode().Step("verify")
	if got := verify.Shape(code, false); got != "" {
		t.Errorf("a command step asks the model for nothing, got %q", got)
	}
}
