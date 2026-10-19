package cli

import (
	"strings"
	"testing"

	"github.com/rfizzle/agentcore/secret"
)

func TestSecretPromptBlock(t *testing.T) {
	if secretPromptBlock(nil, true) != "" || secretPromptBlock(secret.New(), true) != "" {
		t.Fatal("no secrets, no block")
	}
	v := secret.New()
	_ = v.Add("API_KEY", "value-value-value")
	block := secretPromptBlock(v, true)
	for _, want := range []string{"$API_KEY", "[secret:API_KEY]", "## Secrets"} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "value-value-value") {
		t.Fatal("the prompt block must never carry a value")
	}
}

// The mask's symptom is a variable that is simply unset, which is exactly
// what a machine nobody configured looks like — so the block says the mask
// is there even when no secret was declared, and says what the other
// placeholder means whatever else it says.
func TestSecretPromptBlock_SaysTheMaskAndTheRedactionExist(t *testing.T) {
	masked := secret.New()
	masked.SetEnvMask(true)
	block := secretPromptBlock(masked, true)
	for _, want := range []string{"## Secrets", "_TOKEN", secret.Redacted("kind")} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}

	v := secret.New()
	v.SetEnvMask(true)
	if err := v.Add("API_KEY", "value-value-value"); err != nil {
		t.Fatal(err)
	}
	block = secretPromptBlock(v, true)
	for _, want := range []string{"$API_KEY", secret.Placeholder("API_KEY"), "_SECRET", secret.Redacted("kind")} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
}

// A variable the mask took is asked for by name, never by value: the value
// typed into the conversation would reach the provider, and a run nobody
// can answer must not ask for a command it cannot receive.
// See docs/capabilities/secrets.md#a-masked-variable-is-asked-for-by-name.
func TestSecretPromptBlock_AsksForAMaskedVariableByName(t *testing.T) {
	v := secret.New()
	v.SetEnvMask(true)
	asked := secretPromptBlock(v, true)
	if !strings.Contains(asked, "/secret set NAME") || !strings.Contains(asked, "Never ask for the value") {
		t.Errorf("a session a person can answer should ask for /secret and never the value:\n%s", asked)
	}
	unattended := secretPromptBlock(v, false)
	if strings.Contains(unattended, "/secret") || !strings.Contains(unattended, "Never ask for the value") {
		t.Errorf("a run nobody can answer should name the variable and never ask for a command or the value:\n%s", unattended)
	}
}

// A hidden value is outside the prompt block: it is never named, and with
// nothing declared it adds nothing, so the block stays empty until there is
// something to tell.
func TestSecretPromptBlock_NeverMentionsAHiddenValue(t *testing.T) {
	const hidden = "rt_hidden_value_0123456789"
	v := secret.New()
	if err := v.Add("DECLARED", "declared-value-1234"); err != nil {
		t.Fatal(err)
	}
	v.Hide("chatgpt-token", hidden)
	block := secretPromptBlock(v, true)
	if strings.Contains(block, "chatgpt-token") || strings.Contains(block, hidden[:8]) {
		t.Errorf("the prompt block mentions the hidden value:\n%s", block)
	}
	only := secret.New()
	only.Hide("chatgpt-token", hidden)
	if block := secretPromptBlock(only, true); block != "" {
		t.Errorf("a vault with only a hidden value has a prompt block:\n%s", block)
	}
}
