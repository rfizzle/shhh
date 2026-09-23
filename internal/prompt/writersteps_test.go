package prompt

import (
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/shell"
)

// The writer is shown the grammar its lane is counted in, and what it is
// shown is what the counter reads: the example filled in parses to its steps
// and its mark.
func TestBuildWriter_AsksForThePlanInTheGrammarItIsReadIn(t *testing.T) {
	got := BuildWriter(shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"})
	if !strings.Contains(got, "# Your steps\n"+writerSteps) {
		t.Fatalf("the writer's prompt should carry the steps paragraph:\n%s", got)
	}
	filled := strings.NewReplacer("<what this step does>", "read the loop", "<step number>", "1").Replace(writerSteps)
	if p := plan.Parse(filled); len(p.Steps) != 2 {
		t.Errorf("the example should parse to its two steps, got %+v", p.Steps)
	}
	if marks := plan.Progress(filled); !slices.Equal(marks, []int{1}) {
		t.Errorf("the example's progress line should mark step 1, got %v", marks)
	}
}

// A writer is told its generated files are regenerated at landing, so it
// changes the source rather than hand-merging one; the session's own prompt,
// which lands nothing, is not.
func TestBuildWriter_SaysAGeneratedFileIsRegeneratedNotMerged(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}
	const said = "never hand-merge a generated file on a collision"
	if got := BuildWriter(info); !strings.Contains(got, said) {
		t.Fatalf("the writer's prompt should say generated files are regenerated:\n%s", got)
	}
	if got := BuildAgent(info); strings.Contains(got, said) {
		t.Fatal("the base prompt should not carry the writer's paragraph")
	}
}
