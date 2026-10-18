package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// TestGolden_PlanPanels captures the three faces of the ChatGPT panel in the
// bottom panel: waiting on the browser (opened, and with the address to open
// by hand), a sign-in that came back without plan use, and the one-time
// confirmation that the plan is paying
// (docs/capabilities/chatgpt-plan.md#in-a-session).
func TestGolden_PlanPanels(t *testing.T) {
	captureGolden(t, "plan-panels", "the ChatGPT plan's panels", goldenWidths, func(width int) []golden.Panel {
		panel := func(f planFlow) string {
			m := frameModel(t, width, 40)
			m.plan = &f
			return strings.Join(m.planLines(), "\n")
		}
		return []golden.Panel{
			{Label: "waiting · nothing said yet", View: panel(planFlow{})},
			{Label: "waiting · the browser opened, and the port it will come back to", View: panel(planFlow{
				said: []string{"Opened your browser to sign in to ChatGPT. Waiting on 127.0.0.1:41234 for up to 5m0s; esc cancels."},
			})},
			{Label: "waiting · no browser, so the address is the panel's to show", View: panel(planFlow{
				said: []string{
					"Open this address in a browser on a machine whose browser can reach this host's 127.0.0.1:41234:",
					"https://auth.openai.com/api/accounts/authorize?response_type=code&client_id=dynamic_agent_client&redirect_uri=http%3A%2F%2F127.0.0.1%3A41234%2Fauth%2Fcallback&state=example",
					"Waiting for up to 5m0s; esc cancels.",
				},
			})},
			{Label: "signed in without plan use · the way to ask for it again", View: panel(planFlow{
				phase: planNoPlan, email: "person@example.com",
			})},
			{Label: "the first plan sign-in on a host · said once", View: panel(planFlow{phase: planWelcome})},
		}
	})
}

// TestGolden_PlanFailures captures the failure cards the plan route earns:
// a spent allowance leading with Manage usage, an account it will not serve,
// a request it does not serve, and a sign-in that expired, which offers the
// sign-in and no paste prompt.
func TestGolden_PlanFailures(t *testing.T) {
	captureGolden(t, "plan-failures", "the plan's failure cards", goldenWidths, func(width int) []golden.Panel {
		build := func(f *provider.Failure) string {
			m := frameModel(t, width, 40)
			m.modelName = "gpt-5.6-terra"
			m.providerName = "chatgpt"
			m.wiring.ReplaceKey = func(string) error { return nil }
			m.wiring.SwitchProvider = func(string) error { return nil }
			m.wiring.SignInChatGPT = (&fakeSignIn{}).run
			m.turnOutcome = components.TurnFailed
			m.transcript = []entry{
				{kind: entryUser, text: "rename the round-limit sentinel"},
				{kind: entryFailure, fail: f, duration: 340 * time.Millisecond},
			}
			m.invalidateRenderCache()
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "usage limit · Manage usage leads, the switch is second", View: build(&provider.Failure{
				Class: provider.ClassUsageLimit, Status: 429, Provider: "chatgpt",
				Code: "subscription_sharing_usage_limit_exceeded", Message: "You have reached your usage limit.",
			})},
			{Label: "not eligible · the switch alone, no retry", View: build(&provider.Failure{
				Class: provider.ClassNotEligible, Status: 403, Provider: "chatgpt",
				Code: "subscription_sharing_user_not_eligible", Message: "This account cannot use plan sharing.",
			})},
			{Label: "unsupported · the part the route refused is named", View: build(&provider.Failure{
				Class: provider.ClassUnsupported, Status: 400, Provider: "chatgpt", Param: "input_audio",
				Code: "subscription_sharing_unsupported_capability", Message: "Audio input is not supported.",
			})},
			{Label: "auth on the plan · the sign-in stands where the paste prompt was", View: build(&provider.Failure{
				Class: provider.ClassAuth, Status: 401, Provider: "chatgpt",
				Message: "the ChatGPT sign-in has expired",
			})},
		}
	})
}

// TestGolden_PlanHeader captures the header while the plan pays: the words
// ChatGPT plan beside the model, a word and not a colour.
func TestGolden_PlanHeader(t *testing.T) {
	captureGolden(t, "plan-header", "the header while the plan pays", goldenWidths, func(width int) []golden.Panel {
		header := func(name string) string {
			m := frameModel(t, width, 40)
			m.wiring.Prices, m.modelName, m.providerName = nil, "gpt-5.6-terra", name
			m.start = new(startFixture())
			return m.headerRow(width - 2*horizontalPadding)
		}
		return []golden.Panel{
			{Label: "a session on an API key · no mark", View: header("openai")},
			{Label: "a session on the plan · the mark follows the model", View: header("chatgpt")},
		}
	})
}
