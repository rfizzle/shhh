package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/llm/failure"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The in-session ChatGPT sign-in, the plan's mark, its failures and its
// sign-out. Nothing here reaches OpenAI: the sign-in is a fake behind the
// wiring seam.
// See docs/capabilities/chatgpt-plan.md#in-a-session.

// fakeSignIn is a sign-in that says what it is told to, then waits to be
// released or cancelled.
type fakeSignIn struct {
	say     []string
	release chan ChatGPTSignIn
	err     error
}

func (f *fakeSignIn) run(ctx context.Context, say func(string)) (ChatGPTSignIn, error) {
	for _, s := range f.say {
		say(s)
	}
	select {
	case res := <-f.release:
		return res, f.err
	case <-ctx.Done():
		return ChatGPTSignIn{}, ctx.Err()
	}
}

// drain runs a command until it yields a message that is not nil, and
// delivers it. A batch is run member by member.
func deliver(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = deliver(t, m, c)
		}
		return m
	}
	if msg == nil {
		return m
	}
	next, more := m.Update(msg)
	return deliver(t, next.(Model), more)
}

func planSession(t *testing.T) (Model, *fakeSignIn) {
	t.Helper()
	m := failureModel(t)
	f := &fakeSignIn{release: make(chan ChatGPTSignIn, 1)}
	m.wiring.SignInChatGPT = f.run
	return m, f
}

func TestPlan_TheAuthFailureOffersTheSignInBesideTheKey(t *testing.T) {
	m, _ := planSession(t)
	offers := m.failureKeys(&failure.Failure{Class: failure.ClassAuth, Provider: "openai"})
	var words []string
	for _, o := range offers {
		words = append(words, o.Label)
	}
	got := strings.Join(words, "|")
	if got != "enter a new key|continue with ChatGPT|switch provider" {
		t.Fatalf("offers = %q", got)
	}
}

func TestPlan_OnThePlanTheSignInReplacesThePastePrompt(t *testing.T) {
	m, _ := planSession(t)
	m.providerName = "chatgpt"
	for _, o := range m.failureKeys(&failure.Failure{Class: failure.ClassAuth, Provider: "chatgpt"}) {
		if o.Label == "enter a new key" {
			t.Fatal("a plan session has no key to paste")
		}
	}
	m.wiring.SignInChatGPT = nil
	for _, o := range m.failureKeys(&failure.Failure{Class: failure.ClassAuth, Provider: "openai"}) {
		if o.Label == "continue with ChatGPT" {
			t.Fatal("a session that cannot sign in must not offer it")
		}
	}
}

func TestPlan_ThePanelSaysWhatItIsWaitingOnAndWhatEscDoes(t *testing.T) {
	m, f := planSession(t)
	f.say = []string{"Opened your browser to sign in to ChatGPT. Waiting on 127.0.0.1:41234 for up to 5m0s; esc cancels.\n"}
	next, cmd := m.startPlanSignIn()
	m = next.(Model)
	if m.state != stateChatGPT {
		t.Fatalf("state = %v, want the panel", m.state)
	}
	// Run the sign-in and read what it said into the panel.
	msgs := cmd().(tea.BatchMsg)
	msgs[0]() // starts the sign-in
	next, _ = m.Update(msgs[1]())
	m = next.(Model)
	view := strings.Join(m.planLines(), "\n")
	for _, want := range []string{"Waiting on 127.0.0.1:41234", "Esc cancels the sign-in, closes the listener and changes nothing"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the panel lacks %q:\n%s", want, view)
		}
	}
	// What the sign-in said is the panel's, never a transcript row.
	for _, e := range m.transcript {
		if strings.Contains(e.text, "127.0.0.1") {
			t.Fatalf("the sign-in's words reached the transcript: %q", e.text)
		}
	}
}

func TestPlan_EscCancelsTheSignInAndChangesNothing(t *testing.T) {
	m, f := planSession(t)
	switched := 0
	m.wiring.SwitchProvider = func(string) error { switched++; return nil }
	next, _ := m.startPlanSignIn()
	m = next.(Model)
	flow := m.signIn()
	updated, _ := m.routeOverlay(overlayFor(stateChatGPT), tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state == stateChatGPT || m.signIn() != nil {
		t.Fatal("esc should close the panel")
	}
	select {
	case <-flow.done:
	default:
		t.Fatal("esc should cancel the sign-in's context, which closes the listener")
	}
	if m.providerName != "openai" || switched != 0 {
		t.Fatalf("a cancelled sign-in changed the provider to %q (%d switches)", m.providerName, switched)
	}
	_ = f
}

func TestPlan_ASignInWithPlanUseMovesTheSessionAndShowsTheWelcomeOnce(t *testing.T) {
	m, f := planSession(t)
	welcomed := false
	marks := 0
	m.wiring.PlanWelcomed = func() bool { return welcomed }
	m.wiring.MarkPlanWelcomed = func() { welcomed = true; marks++ }
	f.release <- ChatGPTSignIn{Email: "a@b.c", PlanGranted: true}
	next, cmd := m.startPlanSignIn()
	m = deliver(t, next.(Model), cmd)
	if m.providerName != "chatgpt" {
		t.Fatalf("provider = %q, want chatgpt", m.providerName)
	}
	if m.state != stateChatGPT || m.signIn().phase != planWelcome {
		t.Fatal("the first plan sign-in on a host shows the one-time panel")
	}
	view := strings.Join(m.planLines(), "\n")
	for _, want := range []string{"You're using your ChatGPT plan", "https://chatgpt.com/settings/usage", "[enter] got it"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the panel lacks %q:\n%s", want, view)
		}
	}
	updated, _ := m.routeOverlay(overlayFor(stateChatGPT), tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state == stateChatGPT {
		t.Fatal("enter should close the panel")
	}

	// A second sign-in on the same host goes straight on.
	f.release <- ChatGPTSignIn{Email: "a@b.c", PlanGranted: true}
	next, cmd = m.startPlanSignIn()
	m = deliver(t, next.(Model), cmd)
	if m.state == stateChatGPT || marks != 1 {
		t.Fatalf("the panel showed again (state %v, marked %d times)", m.state, marks)
	}
}

func TestPlan_ASignInWithoutPlanUseSaysSoAndNamesConsent(t *testing.T) {
	m, f := planSession(t)
	f.release <- ChatGPTSignIn{Email: "a@b.c", PlanGranted: false}
	next, cmd := m.startPlanSignIn()
	m = deliver(t, next.(Model), cmd)
	if m.providerName != "openai" {
		t.Fatalf("a sign-in without plan use moved the session to %q", m.providerName)
	}
	view := strings.Join(m.planLines(), "\n")
	if !strings.Contains(view, "plan use was not granted") || !strings.Contains(view, "shhh providers login chatgpt --consent") {
		t.Fatalf("the panel should name --consent:\n%s", view)
	}
}

func TestPlan_AFailedSignInLeavesTheSessionWhereItWas(t *testing.T) {
	m, f := planSession(t)
	f.err = errors.New("the sign-in was not finished within 5m0s; nothing was saved")
	f.release <- ChatGPTSignIn{}
	next, cmd := m.startPlanSignIn()
	m = deliver(t, next.(Model), cmd)
	if m.state == stateChatGPT || m.providerName != "openai" {
		t.Fatalf("state %v provider %q", m.state, m.providerName)
	}
	last := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(last, "did not finish") {
		t.Fatalf("the failure should be said: %q", last)
	}
}

func TestPlan_TheSwitchToChatGPTRunsAsACommand(t *testing.T) {
	m := failureModel(t)
	var called bool
	m.wiring.SwitchProvider = func(name string) error { called = name == "chatgpt"; return nil }
	cmd := m.switchProviderCmd("chatgpt")
	if called {
		t.Fatal("the switch ran before the command did, which is Update waiting on a token")
	}
	m = deliver(t, m, cmd)
	if !called || m.providerName != "chatgpt" {
		t.Fatalf("called=%v provider=%q", called, m.providerName)
	}
}

func TestPlan_AUsageLimitLeadsWithManageUsage(t *testing.T) {
	m := failureModel(t)
	m.providerName = "chatgpt"
	f := &failure.Failure{Class: failure.ClassUsageLimit, Provider: "chatgpt"}
	offers := m.failureKeys(f)
	if len(offers) != 2 || offers[0].Label != "manage usage" || offers[1].Label != "switch provider" {
		t.Fatalf("offers = %+v", offers)
	}
	s := failureSentence(f)
	for _, want := range []string{"plan's or shhh's own weekly cap", "https://chatgpt.com/settings/usage"} {
		if !strings.Contains(s, want) {
			t.Fatalf("sentence lacks %q: %s", want, s)
		}
	}
	// [m] says where; it opens nothing.
	next, _ := m.usageNotice()
	last := next.(Model).transcript
	if !strings.Contains(last[len(last)-1].text, "https://chatgpt.com/settings/usage") {
		t.Fatal("manage usage should say the address")
	}
}

func TestPlan_NotEligibleOffersTheSwitchAndNoRetry(t *testing.T) {
	m := failureModel(t)
	for _, o := range m.failureKeys(&failure.Failure{Class: failure.ClassNotEligible}) {
		if o.Label != "switch provider" {
			t.Fatalf("unexpected offer %q", o.Label)
		}
	}
	if len(m.failureKeys(&failure.Failure{Class: failure.ClassNotEligible})) != 1 {
		t.Fatal("want the switch alone")
	}
}

func TestPlan_UnsupportedNamesTheParam(t *testing.T) {
	f := &failure.Failure{Class: failure.ClassUnsupported, Param: "input_audio"}
	if !strings.Contains(failureSentence(f), "input_audio") || !strings.Contains(failureOutcome(f), "input_audio") {
		t.Fatal("the card should name error.param")
	}
}

func TestPlan_TheHeaderCarriesTheWordWhilePlanPays(t *testing.T) {
	m := failureModel(t)
	m.modelName = "gpt-5.6-terra"
	if strings.Contains(m.headerRow(100), planWord) {
		t.Fatal("the word belongs to a plan session")
	}
	m.providerName = "chatgpt"
	if !strings.Contains(m.headerRow(100), planWord) {
		t.Fatal("the header should say ChatGPT plan")
	}
}

func TestPlan_LogoutSaysWhenRevocationWasNotConfirmed(t *testing.T) {
	m := failureModel(t)
	m.providerName = "chatgpt"
	m.wiring.SignOutChatGPT = func(context.Context) (bool, error) { return false, nil }
	next, cmd := m.logoutCommand([]string{"/logout", "chatgpt"})
	m = deliver(t, next.(Model), cmd)
	last := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(last, "Revocation was not confirmed") || !strings.Contains(last, "ChatGPT Settings") {
		t.Fatalf("note = %q", last)
	}
	if m.providerName != "chatgpt" {
		t.Fatal("the session stays on a provider with no credential, for the auth failure to offer the way back")
	}
}

func TestPlan_ASpentPlanTurnShowsTokensAndNoDollar(t *testing.T) {
	m := failureModel(t)
	m.providerName = "chatgpt"
	total := meter.Totals{In: 1200, Out: 300, Requests: 2}
	if got := m.totalsLabel(total); strings.Contains(got, "$") {
		t.Fatalf("a plan session's total is %q", got)
	}
}

func TestPlan_APlanTurnIsNeverPricedAfterTheSessionLeavesThePlan(t *testing.T) {
	m := failureModel(t)
	m.providerName = "openai"
	m.modelName = "gpt-5.6-terra"
	total := meter.Totals{In: 1200, Out: 300, Requests: 2, Plan: 1500}
	if got := m.totalsLabel(total); strings.Contains(got, "$") || !strings.Contains(got, "tok") {
		t.Fatalf("a session that ran on the plan then switched shows %q", got)
	}
}

func TestPlan_APickerLabelsEachSlugWithItsDisplayName(t *testing.T) {
	m := failureModel(t)
	m.modelName = "gpt-5.6-terra"
	m.wiring.ModelLabels = func() map[string]string { return map[string]string{"gpt-5.6-terra": "GPT-5.6 Terra"} }
	pick := m.modelPickFor(modelTarget{})
	found := false
	for _, o := range pick.opts {
		if strings.HasPrefix(o.Label, "gpt-5.6-terra") && o.Desc == "GPT-5.6 Terra" {
			found = true
		}
	}
	if !found {
		t.Fatalf("options = %+v", pick.opts)
	}
	_ = components.SelectOption{}
}
