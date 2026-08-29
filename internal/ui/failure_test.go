package ui

// The one-shot's half of the taxonomy: the same classification as the
// session, rendered on the same grid, with the way out stated as a command
// because there is no program left listening for a key.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
)

func TestFailureReport_ClassifiesAndOffersACommand(t *testing.T) {
	report, ok := FailureReport(&provider.Failure{
		Class: provider.ClassAuth, Status: 401, Provider: "openai",
		Message: "Incorrect API key provided", KeyEnv: "SHHH_API_KEY or OPENAI_API_KEY", KeyTail: "4f9c",
	}, "gpt-4o")
	if !ok {
		t.Fatal("a classified failure should report")
	}
	got := ansi.Strip(report)
	for _, want := range []string{
		"model", "gpt-4o", "401 unauthorized", "key ···4f9c rejected",
		"Incorrect API key provided", "shhh config set provider.api_key",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report should say %q, got:\n%s", want, got)
		}
	}
	// The one-shot has already exited: a bracketed key here would be an
	// offer nothing can honour.
	if strings.Contains(got, "[k]") || strings.Contains(got, "[r]") {
		t.Errorf("the one-shot must not offer keys, got:\n%s", got)
	}
}

func TestFailureReport_EveryClassSaysSomethingUseful(t *testing.T) {
	for _, class := range []provider.Class{
		provider.ClassAuth, provider.ClassRateLimit, provider.ClassQuota,
		provider.ClassOverloaded, provider.ClassContextLength, provider.ClassNetwork,
		provider.ClassMalformed, provider.ClassCancelled, provider.ClassUnclassified,
	} {
		t.Run(string(class), func(t *testing.T) {
			report, ok := FailureReport(&provider.Failure{Class: class, Message: "because"}, "gpt-4o")
			if !ok {
				t.Fatal("every class should report")
			}
			got := ansi.Strip(report)
			if !strings.Contains(got, string(class)) {
				t.Errorf("the row should name the class, got:\n%s", got)
			}
			if strings.TrimSpace(strings.SplitN(got, "\n", 3)[2]) == "" {
				t.Errorf("the row owes the reader a way out, got:\n%s", got)
			}
		})
	}
}

func TestFailureReport_NamesTheWaitAProviderAskedFor(t *testing.T) {
	report, _ := FailureReport(&provider.Failure{
		Class: provider.ClassRateLimit, Status: 429, RetryAfter: 38 * time.Second,
	}, "gpt-4o")
	if got := ansi.Strip(report); !strings.Contains(got, "retry in 38s") {
		t.Errorf("the wait should reach the outcome, got:\n%s", got)
	}
}

func TestFailureReport_DeclinesWhatIsNotAProviderFailure(t *testing.T) {
	if _, ok := FailureReport(errors.New("reading stdin: broken pipe"), "gpt-4o"); ok {
		t.Error("only provider failures belong on this row")
	}
	if _, ok := FailureLine(errors.New("reading stdin: broken pipe")); ok {
		t.Error("only provider failures belong on this line")
	}
}

func TestFailureLine_IsOneLineWithNoChrome(t *testing.T) {
	line, ok := FailureLine(&provider.Failure{
		Class: provider.ClassOverloaded, Status: 503,
		Message: "Service Unavailable\nupstream timed out",
	})
	if !ok {
		t.Fatal("a classified failure should have a line")
	}
	if strings.Contains(line, "\n") {
		t.Errorf("a pipe gets one line, got %q", line)
	}
	if !strings.Contains(line, "503 overloaded") || !strings.Contains(line, "Service Unavailable") {
		t.Errorf("the line should carry the class and the message, got %q", line)
	}
	if strings.Contains(line, "upstream timed out") {
		t.Errorf("only the first line of the message belongs there, got %q", line)
	}
}

func TestPlainProviderReport_NamesEveryPlace(t *testing.T) {
	got := PlainProviderReport(resolve.Survey{
		Provider: "openai",
		Places: []resolve.Place{
			{Kind: resolve.PlaceEnv, Detail: "SHHH_API_KEY, OPENAI_API_KEY — unset"},
			{Kind: resolve.PlaceConfig, Detail: "~/.config/shhh/config.toml — no such file"},
			{Kind: resolve.PlaceProfiles, Detail: "no .toml in ~/.config/shhh/providers"},
			{Kind: resolve.PlaceLocal, Finding: "localhost:11434", Detail: "llama3.3", Found: true},
		},
		Likely: "the local runtime is already answering",
	})
	for _, want := range []string{
		"looked in 4 places", "env", "config", "profiles",
		"✓ local", "llama3.3", "the local runtime is already answering", "shhh config set",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plain report should say %q, got:\n%s", want, got)
		}
	}
}

func TestNewProviderSetup_OffersLocalOnlyWhenSomethingAnswered(t *testing.T) {
	without := NewProviderSetup(resolve.Survey{Provider: "openai"}, []string{"openai"})
	if got := ansi.Strip(without.View().Content); strings.Contains(got, "[o]") {
		t.Errorf("with nothing local, the card must not offer it, got:\n%s", got)
	}
	with := NewProviderSetup(resolve.Survey{
		Provider: "openai", LocalBaseURL: "http://localhost:11434/v1", LocalModel: "llama3.3",
	}, []string{"openai"})
	if got := ansi.Strip(with.View().Content); !strings.Contains(got, "[o] use llama3.3 locally") {
		t.Errorf("an answering endpoint should be offered by name, got:\n%s", got)
	}
}

// setupKey drives the program the way bubbletea would.
func setupKey(m ProviderSetup, key tea.KeyPressMsg) ProviderSetup {
	next, _ := m.Update(key)
	return next.(ProviderSetup)
}

func pressRune(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func TestProviderSetup_PasteAKeyForTheProviderThatFailed(t *testing.T) {
	m := NewProviderSetup(resolve.Survey{Provider: "anthropic"}, []string{"anthropic", "openai"})
	m = setupKey(m, pressRune('p'))
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "Paste a key for anthropic") {
		t.Fatalf("[p] should prompt for the resolved provider's key, got:\n%s", got)
	}
	for _, r := range "sk-ant-1234" {
		m = setupKey(m, pressRune(r))
	}
	if got := ansi.Strip(m.View().Content); strings.Contains(got, "sk-ant") {
		t.Errorf("the key must never be echoed, got:\n%s", got)
	}
	m = setupKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "Save this to the config file") {
		t.Fatalf("a pasted key should ask whether to keep it, got:\n%s", got)
	}
	m = setupKey(m, pressRune('n'))
	choice := m.Choice()
	if !choice.Chosen || choice.Provider != "anthropic" || choice.APIKey != "sk-ant-1234" {
		t.Errorf("choice = %+v, want the pasted key for anthropic", choice)
	}
	if choice.Save {
		t.Error("declining the save must not write it")
	}
}

func TestProviderSetup_WizardPicksAProviderFirst(t *testing.T) {
	m := NewProviderSetup(resolve.Survey{Provider: "openai"}, []string{"anthropic", "openai"})
	m = setupKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "Which provider") {
		t.Fatalf("enter should open the provider list, got:\n%s", got)
	}
	m = setupKey(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = setupKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "sk-9999" {
		m = setupKey(m, pressRune(r))
	}
	m = setupKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = setupKey(m, pressRune('y'))
	choice := m.Choice()
	if choice.Provider != "openai" || choice.APIKey != "sk-9999" || !choice.Save {
		t.Errorf("choice = %+v, want openai with the pasted key, saved", choice)
	}
}

func TestProviderSetup_LocalNeedsNoKey(t *testing.T) {
	m := NewProviderSetup(resolve.Survey{
		Provider: "openai", LocalBaseURL: "http://localhost:11434/v1", LocalModel: "llama3.3",
	}, []string{"openai"})
	m = setupKey(m, pressRune('o'))
	m = setupKey(m, pressRune('n'))
	choice := m.Choice()
	if choice.Provider != "openai-compatible" || choice.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("choice = %+v, want the local endpoint", choice)
	}
	if choice.Model != "llama3.3" {
		t.Errorf("the offer named a model, so the choice should carry it, got %q", choice.Model)
	}
	if choice.APIKey != "" {
		t.Error("a local runtime needs no key")
	}
}

func TestProviderSetup_EscChoosesNothing(t *testing.T) {
	m := NewProviderSetup(resolve.Survey{Provider: "openai"}, []string{"openai"})
	m = setupKey(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.Choice().Chosen {
		t.Error("esc dismisses; it must not choose a provider")
	}
}

func TestProviderSetup_AnEmptyKeyMeansDifferentThingsInTheTwoPaths(t *testing.T) {
	// Pasting nothing for the provider that just failed is a decline: it
	// returns to the card rather than starting a session on an empty key.
	declined := NewProviderSetup(resolve.Survey{Provider: "openai"}, []string{"openai"})
	declined = setupKey(declined, pressRune('p'))
	declined = setupKey(declined, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := ansi.Strip(declined.View().Content); !strings.Contains(got, "No model provider configured") {
		t.Errorf("an empty paste should return to the card, got:\n%s", got)
	}
	if declined.Choice().Chosen {
		t.Error("an empty paste must not choose a provider")
	}

	// The wizard's empty key is meaningful: a gateway or a local runtime may
	// need none, and the prompt says so.
	wizard := NewProviderSetup(resolve.Survey{Provider: "openai"}, []string{"openai-compatible"})
	wizard = setupKey(wizard, tea.KeyPressMsg{Code: tea.KeyEnter})
	wizard = setupKey(wizard, tea.KeyPressMsg{Code: tea.KeyEnter})
	wizard = setupKey(wizard, tea.KeyPressMsg{Code: tea.KeyEnter})
	wizard = setupKey(wizard, pressRune('n'))
	choice := wizard.Choice()
	if !choice.Chosen || choice.Provider != "openai-compatible" || choice.APIKey != "" {
		t.Errorf("choice = %+v, want the picked provider with no key", choice)
	}
}
