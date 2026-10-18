package chat

import (
	"testing"

	"github.com/rfizzle/llm"
)

// A ChatGPT plan session that runs out of allowance, or is refused by the
// plan, stays on the plan: nothing moves it to another provider or a key on
// the person's behalf. The failure card's own switch is the only way across,
// and it is a key the person presses.
// See docs/capabilities/chatgpt-plan.md#what-a-plan-session-cannot-do.
func TestStreamError_APlanRefusalNeverSwitchesTheProvider(t *testing.T) {
	for _, class := range []llm.Class{llm.ClassUsageLimit, llm.ClassNotEligible} {
		t.Run(string(class), func(t *testing.T) {
			m := failureModel(t)
			m.providerName = "chatgpt"
			switched, replaced := 0, 0
			m.wiring.SwitchProvider = func(string) error { switched++; return nil }
			m.wiring.ReplaceKey = func(string) error { replaced++; return nil }

			updated, cmd := m.Update(streamErrMsg{err: &llm.Failure{
				Class: class, Status: 429, Provider: "chatgpt",
				Code: "subscription_sharing_usage_limit_exceeded",
			}})
			next := updated.(Model)
			if cmd != nil {
				// Whatever the failure schedules is run, so a switch hidden
				// behind a command would still be caught.
				if msg := cmd(); msg != nil {
					updated, _ = next.Update(msg)
					next = updated.(Model)
				}
			}
			if next.providerName != "chatgpt" {
				t.Fatalf("the session moved to %q on its own", next.providerName)
			}
			if switched != 0 || replaced != 0 {
				t.Fatalf("a plan refusal switched the provider %d times and the key %d times", switched, replaced)
			}
			failed := false
			for _, e := range next.transcript {
				if e.kind == entryFailure && e.fail != nil && e.fail.Class == class {
					failed = true
				}
			}
			if !failed {
				t.Fatal("the refusal should land as a failure row, where the switch is the person's to press")
			}
		})
	}
}
