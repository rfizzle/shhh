package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/resolve"
)

// A plan reads no key, so the wizard never asks for one for it and nothing
// pasted is ever saved against it: the pick is the sign-in.
// See docs/capabilities/chatgpt-plan.md#in-a-session.
func TestProviderSetup_AChatGPTPickIsTheSignInAndNeverAKeyPrompt(t *testing.T) {
	var m tea.Model = NewProviderSetup(resolve.Survey{Provider: "anthropic"}, []string{"anthropic", "chatgpt"})
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter}, // the wizard
		{Code: tea.KeyDown},  // onto chatgpt
		{Code: tea.KeyEnter}, // pick it
	} {
		m, _ = m.Update(k)
	}
	setup := m.(ProviderSetup)
	if setup.step != stepSave {
		t.Fatalf("step = %v, want the save question and no key prompt", setup.step)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	choice := m.(ProviderSetup).Choice()
	if choice.Provider != "chatgpt" || !choice.Chosen || choice.APIKey != "" || !choice.Save {
		t.Fatalf("choice = %+v", choice)
	}
}

// The first-run card's [t] is the same choice.
func TestProviderSetup_TheCardOffersContinueWithChatGPT(t *testing.T) {
	var m tea.Model = NewProviderSetup(resolve.Survey{Provider: "anthropic"}, []string{"anthropic", "chatgpt"})
	m, _ = m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	if got := m.(ProviderSetup); got.step != stepSave || got.choice.Provider != "chatgpt" {
		t.Fatalf("step %v choice %+v", got.step, got.choice)
	}
}
