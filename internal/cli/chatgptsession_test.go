package cli

import (
	"testing"

	"github.com/rfizzle/llm/chatgpt"
)

// The one-time panel is recorded in the credential record: shown once, and
// not again on this host, sign-out included.
func TestPlanWelcomeIsRecordedOnceInTheRecord(t *testing.T) {
	resetChatGPT(t)
	signInRecord(t, chatgpt.Credential{Email: "a@b.c", RefreshToken: "r", AccessToken: "a", IDToken: "i", ClientID: "oaiapp_x",
		Scopes: []string{chatgpt.PlanScope}})
	if planWelcomed() {
		t.Fatal("a fresh sign-in has not been welcomed")
	}
	markPlanWelcomed()
	if !planWelcomed() {
		t.Fatal("the welcome should be recorded")
	}
	a, err := chatGPTAccount()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Clear(); err != nil {
		t.Fatal(err)
	}
	if !planWelcomed() {
		t.Fatal("sign-out must not bring the panel back")
	}
}

// The chat screen says the usage address in text, from a copy of its own.
func TestPlanUsageAddressIsTheDocumentedOne(t *testing.T) {
	if chatgpt.UsageURL != "https://chatgpt.com/settings/usage" {
		t.Fatalf("UsageURL = %q; the chat screen says this address in text", chatgpt.UsageURL)
	}
}
