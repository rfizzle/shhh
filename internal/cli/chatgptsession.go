package cli

// The ChatGPT plan inside a running session: the sign-in the failure card
// and /logout reach through the chat's wiring. The sign-in is the same
// account and the same opener as `shhh providers login chatgpt`; what differs
// is that its words go to a panel. The screen, not this file, moves the
// session onto the plan.
// See docs/capabilities/chatgpt-plan.md#in-a-session.

import (
	"context"

	"github.com/rfizzle/llm/chatgpt"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// lineSayer is an io.Writer that hands what it is written to a function.
type lineSayer func(string)

func (f lineSayer) Write(p []byte) (int, error) {
	f(string(p))
	return len(p), nil
}

// signInChatGPT signs in from inside the session. The address, when the
// browser did not open, reaches only say, which is the panel: it is never
// logged and never a message the model reads.
func (env *sessionEnv) signInChatGPT(ctx context.Context, say func(string)) (chat.ChatGPTSignIn, error) {
	a, err := chatGPTAccount()
	if err != nil {
		return chat.ChatGPTSignIn{}, err
	}
	res, err := a.Login(ctx, chatgpt.LoginOptions{
		Say:       lineSayer(say),
		StopWords: "esc cancels",
	})
	if err != nil {
		return chat.ChatGPTSignIn{}, err
	}
	// The session is not moved here. The screen moves it, and only while the
	// panel is still open: an esc that lands between the browser's return and
	// a switch made here would leave the stream on the plan under a header
	// that still named the old provider.
	return chat.ChatGPTSignIn{Email: res.Email, PlanGranted: res.PlanGranted}, nil
}

// signOutChatGPT is `shhh providers logout chatgpt` for the session: the
// server is told first and the tokens are cleared whether it heard or not.
func signOutChatGPT(ctx context.Context) (bool, error) {
	a, err := chatGPTAccount()
	if err != nil {
		return false, err
	}
	return a.Logout(ctx)
}

// planWelcomed reports whether this host has been told the plan is paying.
func planWelcomed() bool {
	a, err := chatGPTAccount()
	if err != nil {
		return true
	}
	c, err := a.Credential()
	return err != nil || !c.WelcomedAt.IsZero()
}

// markPlanWelcomed records that the one-time panel was shown. A record that
// cannot be written means the panel shows again, which is the cheaper error.
func markPlanWelcomed() {
	if a, err := chatGPTAccount(); err == nil {
		_ = a.MarkWelcomed()
	}
}

// modelLabels is the display name of each slug the active provider has one
// for, where it keeps any (the plan route does, from its catalog).
func (env *sessionEnv) modelLabels() map[string]string {
	if env.activeProvider == nil {
		return nil
	}
	l, ok := env.activeProvider().(interface{ ModelLabels() map[string]string })
	if !ok {
		return nil
	}
	return l.ModelLabels()
}
