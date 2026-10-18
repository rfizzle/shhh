package chat

// The ChatGPT plan inside the session (docs/capabilities/chatgpt-plan.md#in-a-session):
// the sign-in the auth failure offers beside the key, the panel that waits on
// the browser, the one-time confirmation that the plan is paying, the switch
// to the plan, and /logout.
//
// Everything that can wait runs as a command. The sign-in waits on a person
// and a browser, and building the plan's provider takes a token under a
// bound of a minute, so neither runs in Update: a screen that froze for a
// minute with esc dead would be the one place a person could not stop
// shhh doing something. The words the sign-in says, which include the
// address when the browser did not open, go to the panel and nowhere else:
// they are never a transcript row, so they are never a message the model
// reads.

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// planName is the provider that spends a plan, by the name the registry
	// knows it under.
	planName = "chatgpt"
	// planUsageURL is where a plan's allowance is shown and managed. It is
	// said in text: a link needs a terminal that can follow one.
	planUsageURL = "https://chatgpt.com/settings/usage"
	// planWord is what the header says beside the model while the plan pays.
	planWord = "ChatGPT plan"
)

// planPhase is which of the panel's three faces is showing.
type planPhase int

const (
	// planWaiting waits on the browser.
	planWaiting planPhase = iota
	// planNoPlan is a sign-in that came back without plan use.
	planNoPlan
	// planWelcome is the one-time confirmation.
	planWelcome
)

// planFlow is a sign-in in flight, and the panel over it.
type planFlow struct {
	phase  planPhase
	cancel context.CancelFunc
	done   <-chan struct{}
	events chan planEvent
	// said is what the sign-in has said, line by line.
	said  []string
	email string
	// note is the transcript row the panel leaves when it closes.
	note string
}

// planEvent is one thing the sign-in did: said something, or finished.
type planEvent struct {
	said string
	done bool
	res  ChatGPTSignIn
	err  error
}

// planEventMsg carries an event to the model; the flow it names fences it to
// the sign-in that sent it.
type planEventMsg struct {
	flow *planFlow
	ev   planEvent
}

// providerSwitchedMsg is the end of a switch that ran as a command.
type providerSwitchedMsg struct {
	name string
	err  error
}

// logoutDoneMsg is the end of /logout chatgpt.
type logoutDoneMsg struct {
	revoked bool
	err     error
}

// onPlan reports that the plan is paying for this session.
func (m Model) onPlan() bool { return m.providerName == planName }

// signIn is the sign-in in flight, or nil while none is.
func (m Model) signIn() *planFlow { return m.plan }

// canSignInChatGPT reports whether the sign-in is an offer this session makes.
func (m Model) canSignInChatGPT() bool { return m.wiring.SignInChatGPT != nil }

// startPlanSignIn opens the waiting panel and starts the sign-in behind it.
func (m Model) startPlanSignIn() (tea.Model, tea.Cmd) {
	if !m.canSignInChatGPT() {
		return m.systemNotice("this session cannot sign in to ChatGPT")
	}
	if m.signIn() != nil {
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &planFlow{cancel: cancel, done: ctx.Done(), events: make(chan planEvent, 64)}
	m.plan = f
	m.enterSurface(stateChatGPT)
	m.syncViewport()
	sign := m.wiring.SignInChatGPT
	run := func() tea.Msg {
		go func() {
			send := func(ev planEvent) {
				select {
				case f.events <- ev:
				case <-ctx.Done():
				}
			}
			res, err := sign(ctx, func(s string) { send(planEvent{said: s}) })
			send(planEvent{done: true, res: res, err: err})
		}()
		return nil
	}
	return m, tea.Batch(run, waitPlan(f))
}

// waitPlan reads the flow's next event. It ends with the flow: a cancelled
// sign-in has nobody to hear what it says.
func waitPlan(f *planFlow) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-f.events:
			return planEventMsg{flow: f, ev: ev}
		case <-f.done:
			return nil
		}
	}
}

// handlePlanEvent folds one event into the panel.
func (m Model) handlePlanEvent(msg planEventMsg) (tea.Model, tea.Cmd) {
	f := m.signIn()
	if f == nil || msg.flow != f {
		return m, nil
	}
	if !msg.ev.done {
		for _, line := range strings.Split(msg.ev.said, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				f.said = append(f.said, line)
			}
		}
		m.syncViewport()
		return m, waitPlan(f)
	}
	f.cancel()
	res := msg.ev.res
	switch {
	case msg.ev.err != nil:
		if errors.Is(msg.ev.err, context.Canceled) {
			return m.closePlan(m.cancelNote())
		}
		return m.closePlan("the ChatGPT sign-in did not finish: " + msg.ev.err.Error() + ". Nothing changed")
	case !res.PlanGranted:
		f.phase, f.email = planNoPlan, res.Email
		m.syncViewport()
		return m, nil
	}
	// The switch runs as a command and only now, while the panel is open: a
	// sign-in cancelled with esc never reaches here, so it cannot move the
	// session. handleProviderSwitched says the result.
	var move tea.Cmd
	if m.wiring.SwitchProvider != nil {
		move = m.switchProviderCmd(planName)
	}
	f.note = "signed in as " + res.Email
	if move == nil {
		f.note += "; this session cannot switch to " + planName
	}
	if m.wiring.PlanWelcomed != nil && !m.wiring.PlanWelcomed() {
		// Recorded as it is shown, so it is never shown twice, whichever key
		// closes it.
		if m.wiring.MarkPlanWelcomed != nil {
			m.wiring.MarkPlanWelcomed()
		}
		f.phase = planWelcome
		m.syncViewport()
		return m, move
	}
	closed, cmd := m.closePlan(f.note)
	return closed, tea.Batch(cmd, move)
}

// adoptProvider moves the header and the failure rows to a provider the
// wiring has already switched to, and to the model it starts on where the
// switch moved the session to another provider.
func (m *Model) adoptProvider(name string) {
	was := m.providerName
	m.providerName = name
	if was != name {
		if model := provider.Defaults(name).Model; model != "" {
			m.modelName = model
		}
	}
}

// closePlan hands the screen back, leaving the note as a row.
func (m Model) closePlan(note string) (tea.Model, tea.Cmd) {
	m.plan = nil
	m.leaveSurface()
	m.syncViewport()
	return m.systemNotice(note)
}

// answerPlan routes keys while the panel is up. Esc while it waits stops the
// sign-in: the listener closes and nothing is written.
func (m *Model) answerPlan(msg tea.KeyPressMsg) (bool, overlayAction) {
	f := m.signIn()
	if f == nil {
		return true, overlayAction{close: true}
	}
	pressed := msg.String()
	switch f.phase {
	case planWaiting:
		if keys.Is(pressed, keys.Select.Cancel) {
			f.cancel()
			m.plan = nil
			return true, overlayAction{close: true, note: m.cancelNote()}
		}
	case planNoPlan:
		if keys.Is(pressed, keys.Wait.GotIt, keys.Select.Cancel) {
			m.plan = nil
			return true, overlayAction{close: true, note: "signed in as " + f.email + " without plan use; the session is unchanged"}
		}
	case planWelcome:
		if keys.Is(pressed, keys.Wait.GotIt, keys.Select.Cancel) {
			m.plan = nil
			return true, overlayAction{close: true, note: f.note}
		}
	}
	return false, overlayAction{}
}

// cancelNote is what a cancelled sign-in leaves. The record may have been
// saved if the browser had already returned; the session is not moved either
// way, and that is the part worth saying.
func (m Model) cancelNote() string {
	if m.providerName == "" {
		return "sign-in cancelled; the session is unchanged"
	}
	return "sign-in cancelled; the session stays on " + m.providerName
}

// planLines draws the panel.
func (m Model) planLines() []string {
	f := m.signIn()
	if f == nil {
		return nil
	}
	return strings.Split(planPanel(*f).View(m.contentWidth()), "\n")
}

// planPanel is the card for the flow's phase.
func planPanel(f planFlow) components.PlanPanel {
	got := []components.KeyOffer{components.Offer(keys.Wait.GotIt)}
	switch f.phase {
	case planNoPlan:
		return components.PlanPanel{
			Title: "Signed in to ChatGPT",
			Body: []string{
				"Signed in as " + f.email + ", but plan use was not granted, so this session stays where it was.",
				"Run `shhh providers login chatgpt --consent` to sign in again and allow it.",
			},
			Offers: got,
		}
	case planWelcome:
		return components.PlanPanel{
			Title: "You're using your ChatGPT plan",
			Body: []string{
				"This session spends your ChatGPT plan's allowance, not API credit. A Plus plan shares one five-hour allowance across every app that uses it, ChatGPT included.",
				"Usage is shown and managed at " + planUsageURL + ".",
				"If the allowance runs out shhh stops and says so. It never moves to an API key on its own.",
			},
			Offers: got,
		}
	}
	body := append([]string(nil), f.said...)
	if len(body) == 0 {
		body = append(body, "Starting the sign-in…")
	}
	body = append(body, "", "Esc cancels the sign-in, closes the listener and changes nothing.")
	return components.PlanPanel{
		Title:  "Signing in with ChatGPT",
		Body:   body,
		Offers: []components.KeyOffer{components.OfferAs(keys.Select.Cancel, "cancel the sign-in")},
	}
}

// switchProviderCmd is a provider switch run as a command: for the plan the
// rebuild takes a token under a bound of a minute, and Update does not wait.
func (m Model) switchProviderCmd(name string) tea.Cmd {
	switchTo := m.wiring.SwitchProvider
	return func() tea.Msg { return providerSwitchedMsg{name: name, err: switchTo(name)} }
}

// handleProviderSwitched finishes a switch that ran as a command.
func (m Model) handleProviderSwitched(msg providerSwitchedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.systemNotice("Could not switch to " + msg.name + ": " + msg.err.Error())
	}
	m.adoptProvider(msg.name)
	return m.systemNotice("switched to " + msg.name + " on " + m.modelName + ". Ask again to use it")
}

// logoutCommand is /logout chatgpt: it signs out through the wiring, says
// when the revocation was not confirmed, and leaves the session on a
// provider with no credential, so its next request meets the auth failure's
// offers.
func (m Model) logoutCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) != 2 || parts[1] != planName {
		return m.systemNotice("usage: /logout chatgpt — signs out of the ChatGPT plan and revokes the sign-in")
	}
	if m.wiring.SignOutChatGPT == nil {
		return m.systemNotice("this session cannot sign out of ChatGPT")
	}
	out := m.wiring.SignOutChatGPT
	return m, func() tea.Msg {
		revoked, err := out(context.Background())
		return logoutDoneMsg{revoked: revoked, err: err}
	}
}

// handleLogout says how the sign-out went.
func (m Model) handleLogout(msg logoutDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.systemNotice("Could not sign out of ChatGPT: " + msg.err.Error())
	}
	note := "signed out of ChatGPT; its tokens are cleared from this machine"
	if !msg.revoked {
		note += ". Revocation was not confirmed: OpenAI did not answer, so shhh may still be connected to your account. Disconnect it in ChatGPT Settings to be sure"
	}
	if m.onPlan() {
		note += ". This session has no credential now; the next request offers the sign-in"
	}
	return m.systemNotice(note)
}

// planUsageLine is /ui's line about the plan while it pays: where the
// allowance is managed, in text, since a link needs a terminal that can
// follow one.
func (m Model) planUsageLine() string {
	if !m.onPlan() {
		return ""
	}
	return "plan: " + planWord + " — Manage usage at " + planUsageURL + "\n"
}

// planSpend reports that the ledger holds requests the plan answered, and so
// has tokens and no dollars to show for them.
func (m Model) planSpend() bool {
	if m.wiring.Ledger == nil {
		return m.onPlan()
	}
	for _, e := range m.wiring.Ledger.Entries() {
		if e.Provider == planName {
			return true
		}
	}
	return false
}

// usageNotice is what [m] on a spent-allowance card does: says where the
// allowance is managed. It opens nothing; the address is the person's to open.
func (m Model) usageNotice() (tea.Model, tea.Cmd) {
	return m.systemNotice("Manage usage at " + planUsageURL)
}
