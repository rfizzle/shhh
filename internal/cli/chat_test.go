package cli

import "testing"

// The flags a conversation behind --print is driven by. They are asserted as
// a set because the refusals the shared print path answers with name them:
// a sentence offering `--resume=<name>` to somebody whose command has no such
// spelling sends them to an error, and the whole point of an unattended
// contract is that what it says can be acted on.
func TestChatCmd_TheFlagsAnUnattendedRunIsDrivenBy(t *testing.T) {
	cmd := newChatCmd()
	for _, name := range []string{"print", "output", "json", "yes", "continue", "resume"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("`shhh chat` has no --%s", name)
		}
	}
	// Nothing here edits or runs a command, so there is nothing for the
	// coding agent's command flags to be about.
	for _, name := range []string{"allow", "sandbox", "require-sandbox"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("`shhh chat` offers --%s and has no command to run", name)
		}
	}
	resume := cmd.Flags().Lookup("resume")
	if resume.NoOptDefVal != resumeFromPicker {
		t.Fatalf("--resume on its own resolves to %q, want the picker's stand-in", resume.NoOptDefVal)
	}
	if err := cmd.Flags().Parse([]string{"--resume=yesterday"}); err != nil {
		t.Fatalf("--resume=<name>: %v", err)
	}
	if got := resume.Value.String(); got != "yesterday" {
		t.Fatalf("--resume=yesterday named %q", got)
	}
}
