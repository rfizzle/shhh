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

// An integration writer is told what its copy holds, what reads as not
// reconciled, and how to stop with both patches kept — in a paragraph that
// names no tool, since it rides whatever profile the conflicting writer ran.
func TestIntegrationWriter_SaysHowToReconcileAndHowToStop(t *testing.T) {
	for _, said := range []string{
		"every other file of that writer's patch already merged into it",
		"Leave no conflict markers",
		"leave the conflicting files as you found them and say so in your report",
	} {
		if !strings.Contains(IntegrationWriter, said) {
			t.Errorf("the integration paragraph should say %q", said)
		}
	}
	for _, tool := range []string{"write_file", "edit_file", "execute_command", "read_file", "quality_gate"} {
		if strings.Contains(IntegrationWriter, tool) {
			t.Errorf("the integration paragraph should name no tool, found %q", tool)
		}
	}
	if strings.Contains(BuildWriter(shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}), "# Reconciling two changes") {
		t.Fatal("an ordinary writer's prompt should not carry the integration paragraph")
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

// A writer is told its check may wait for a slot and that the wait is not a
// failure, under the verify step it would otherwise read the pause against;
// the session's own prompt, which shares no slots with a sibling, is not.
func TestBuildWriter_SaysACheckMayWaitForASlot(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}
	got := BuildWriter(info)
	if !strings.Contains(got, "when one is available.\n"+writerCheckSlots+"\n") {
		t.Fatalf("the writer's prompt should say a check may wait for a slot, under its verify step:\n%s", got)
	}
	if strings.Contains(BuildAgent(info), "check slot") {
		t.Fatal("the base prompt should not carry the writer's paragraph")
	}
}
