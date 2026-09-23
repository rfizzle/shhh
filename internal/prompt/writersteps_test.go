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
