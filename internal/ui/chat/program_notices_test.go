package chat

// The session's own voice, walked through the real program: a notice, the
// key list and a provider's refusal, each reached by the keys a reader
// presses. The scene `notices` is the same route in a terminal.

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// refusingProvider answers every request with the same classified refusal.
type refusingProvider struct{ fail *provider.Failure }

func (p refusingProvider) Name() string { return "scripted" }

func (p refusingProvider) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	return nil, p.fail
}

// A notice is lower case with no full stop, the key list is laid out on the
// grid and a refused request is a failure row: none of them says `Error:`.
func TestProgram_NoticesSpeakInTheReadmesVoice(t *testing.T) {
	p := refusingProvider{fail: &provider.Failure{
		Class: provider.ClassAuth, Status: 401, Provider: "openai",
		Message: "Incorrect API key provided",
	}}
	tm := runProgramAt(t, New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p)), 110, 40)

	tm.Type("/copy")
	tm.Send(programEnter)
	waitForText(t, tm, "nothing to copy yet")

	tm.Send(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	waitForText(t, tm, "print this key list")

	tm.Type("why does the loop stop")
	tm.Send(programEnter)
	waitForText(t, tm, "401 unauthorized")

	frame := finalFrame(t, tm)
	for _, gone := range []string{"Error:", "Nothing to copy yet."} {
		if strings.Contains(frame, gone) {
			t.Errorf("the frame still says %q:\n%s", gone, frame)
		}
	}
}
