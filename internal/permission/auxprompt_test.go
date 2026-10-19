package permission

import "testing"

// The classifier's instruction is the built-in literal
// until a file replaces them.
func TestAuxiliaryPromptOverrides(t *testing.T) {
	if got := (ClassifierConfig{}).prompt(); got != classifierPrompt {
		t.Error("an unset classifier instruction must be the built-in one")
	}
	if got := (ClassifierConfig{Prompt: "decide"}).prompt(); got != "decide" {
		t.Errorf("classifier instruction override: %q", got)
	}
}
