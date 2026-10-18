package chat

// The ChatGPT plan's offer, reached through the real program: a provider
// that refuses the key draws a failure row, and the row offers the sign-in
// beside the key. The scene `chatgpt-offer` is the same route in a terminal.

import (
	"testing"

	"github.com/rfizzle/llm"
)

func TestProgram_AnAuthFailureOffersTheSignInBesideTheKey(t *testing.T) {
	p := refusingProvider{fail: &llm.Failure{
		Class: llm.ClassAuth, Status: 401, Provider: "openai",
		Message: "Incorrect API key provided",
	}}
	w := Wiring{
		ReplaceKey:    func(string) error { return nil },
		SignInChatGPT: (&fakeSignIn{}).run,
	}
	tm := runProgramAt(t, New([]llm.Message{{Role: llm.RoleSystem, Content: "sys"}}, streamOf(p), w), 110, 40)

	tm.Type("why does the loop stop")
	tm.Send(programEnter)
	waitForText(t, tm, "[t] continue with ChatGPT")
}
