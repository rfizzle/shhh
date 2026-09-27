package prompt

import (
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/shell"
)

// The session and every child role are shown the grammar their list is
// counted in, in the same words, and what they are shown is what the counter
// reads: the example filled in parses to its steps, its mark and its
// revision.
func TestWorkingSteps_EveryRoleIsAskedInTheGrammarItIsReadIn(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}
	for name, got := range map[string]string{
		"session":    BuildAgent(info),
		"writer":     BuildWriter(info),
		"researcher": BuildResearcher(info, WebTools{}),
		"reviewer":   BuildReviewer(info, ProfileSpec{}),
		"profile":    BuildProfile(info, ProfileSpec{Name: "auditor", Tools: []string{"read_file"}}),
	} {
		if !strings.Contains(got, workingSteps) {
			t.Errorf("the %s prompt should carry the working-steps paragraph", name)
		}
	}
	if !strings.Contains(BuildAgent(info), "next action, with the working list below where the task has several steps.") {
		t.Error("the public-progress sentence should point at the working list")
	}
	if strings.Contains(BuildConversation(info), workingSteps) {
		t.Error("a conversation asks for no working list")
	}

	filled := strings.NewReplacer("<what this step does>", "read the loop", "<step number>", "1").Replace(workingSteps)
	if p := plan.Parse(filled); len(p.Steps) != 2 {
		t.Errorf("the example should parse to its two steps, got %+v", p.Steps)
	}
	if marks := plan.Progress(filled); !slices.Equal(marks, []int{1}) {
		t.Errorf("the example's progress line should mark step 1, got %v", marks)
	}
	var l plan.Checklist
	l.Note(filled, true)
	l.Note("steps:\n1. patch the flag", true)
	if done, total, current := l.Tally(); done != 1 || total != 2 || current != "patch the flag" {
		t.Errorf("a revision in the words the paragraph gives = %d/%d %q, want 1/2 on the new step", done, total, current)
	}
	if !strings.Contains(workingSteps, "a line of its own reading steps: and under it") {
		t.Error("the paragraph should name the steps: marker a revision is read under")
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
