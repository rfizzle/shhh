package prompt

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/shell"
)

// The working list is a tool, and a base prompt names no tool: the session
// and every child role are told about it by its own definition and its
// toolbox line, where it was registered, and by nothing here — and none of
// them is still taught the text grammar the list used to be read out of.
// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.
func TestWorkingSteps_NoRoleIsTaughtTheListInItsBasePrompt(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}
	for name, got := range map[string]string{
		"session":      BuildAgent(info),
		"writer":       BuildWriter(info),
		"researcher":   BuildResearcher(info, WebTools{}),
		"reviewer":     BuildReviewer(info, ProfileSpec{}),
		"profile":      BuildProfile(info, ProfileSpec{Name: "auditor", Tools: []string{"read_file"}}),
		"conversation": BuildConversation(info),
	} {
		for _, said := range []string{"progress: <step number>", "reading steps:", "# Your steps", "working list"} {
			if strings.Contains(got, said) {
				t.Errorf("the %s prompt still carries %q", name, said)
			}
		}
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
