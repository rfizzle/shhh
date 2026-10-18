package cli

// The ChatGPT plan sign-in, as the rest of the CLI reaches it: one account
// per process, a relay that hides every token it learns from the model, and
// the sentence the setup report says about it.
// See docs/capabilities/chatgpt-plan.md.

import (
	"context"
	"slices"
	"sync"

	"github.com/rfizzle/shhh/internal/chatgpt"
	"github.com/rfizzle/shhh/internal/secret"
)

// chatGPT is the process's one account, opened on first need. It is one per
// process so that every request, every refresh and every vault go through
// the same lock and the same relay.
var chatGPT struct {
	mu      sync.Mutex
	account *chatgpt.Account
}

// chatGPTOptions is what the account is opened with; a test points it at a
// temporary directory and an in-memory server.
var chatGPTOptions = func() chatgpt.Options { return chatgpt.Options{} }

// chatGPTTokens hides every token the account learns in every session vault.
var chatGPTTokens = &tokenRelay{}

// chatGPTAccount is the process's account. A failure to open it is not
// remembered: the directory may be creatable on the next try.
func chatGPTAccount() (*chatgpt.Account, error) {
	chatGPT.mu.Lock()
	defer chatGPT.mu.Unlock()
	if chatGPT.account != nil {
		return chatGPT.account, nil
	}
	o := chatGPTOptions()
	relay := chatGPTTokens
	o.OnTokens = func(access, refresh, id string) { relay.learn(access, refresh, id) }
	a, err := chatgpt.Open(o)
	if err != nil {
		return nil, err
	}
	chatGPT.account = a
	return a, nil
}

// listChatGPTModels lists the models the plan serves the signed-in account,
// so a sign-in can say what it unlocked. It belongs to the plan's provider,
// which reads the plan's own model list; it is nil until that provider is
// registered and sets it, and a sign-in without it lists nothing.
var listChatGPTModels func(ctx context.Context, a *chatgpt.Account) ([]string, error)

// chatGPTDefaultModel picks the model a sign-in saved with --save writes to
// provider.model, out of the ones the plan listed. It is the plan provider's
// choice to make, which replaces this one when it is wired; until then the
// first listed model is the plan's own suggestion.
var chatGPTDefaultModel = func(listed []string) string {
	if len(listed) == 0 {
		return ""
	}
	return listed[0]
}

// tokenRelay hands the tokens the account learns to the session vaults, in
// either order: a vault attached after a refresh is told what was learned
// before it, and one attached first hears every later set. That is what lets
// the provider and the vault be built in whichever order a command builds
// them.
//
// The refresh token is the reason this exists. The access and ID tokens are
// JWTs, which the shape table already redacts; the refresh token is opaque,
// and nothing but its value can find it.
// See docs/capabilities/chatgpt-plan.md#the-tokens-never-reach-the-model.
type tokenRelay struct {
	mu     sync.Mutex
	seen   []string
	vaults []*secret.Vault
}

// learn records a set and hides it in every attached vault. It is the
// account's OnTokens, called with the account's lock held, and touches
// nothing but the vaults.
func (r *tokenRelay) learn(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if v == "" || slices.Contains(r.seen, v) {
			continue
		}
		r.seen = append(r.seen, v)
		for _, vault := range r.vaults {
			vault.Hide(chatgpt.TokenKind, v)
		}
	}
}

// attach adds a vault and hides everything learned so far in it.
func (r *tokenRelay) attach(v *secret.Vault) {
	if v == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vaults = append(r.vaults, v)
	for _, s := range r.seen {
		v.Hide(chatgpt.TokenKind, s)
	}
}

// hideChatGPTTokens attaches a session vault to the relay and, where this
// host holds a sign-in, hides its tokens at once: a session that never
// talks to ChatGPT can still read the record through an uncontained
// command, and the vault is what keeps the refresh token out of what the
// model reads. A host that never signed in is left without a directory.
func hideChatGPTTokens(v *secret.Vault) {
	chatGPTTokens.attach(v)
	if !chatgpt.HasRecord(chatGPTOptions().Dir) {
		return
	}
	a, err := chatGPTAccount()
	if err != nil {
		return
	}
	// A record that will not read has no tokens to hide here, and the
	// session that needs one reports the failure where it is used.
	cred, err := a.Credential()
	if err != nil {
		return
	}
	chatGPTTokens.learn(cred.AccessToken, cred.RefreshToken, cred.IDToken)
}

// The sentences the setup report says about the sign-in.
const (
	chatGPTNotSignedIn = "not signed in to ChatGPT — run `shhh providers login chatgpt`"
	chatGPTNoPlan      = "signed in to ChatGPT, but plan use was not granted — run `shhh providers login chatgpt --consent`"
)

// chatGPTSurveyLine is what the provider report names as the way in when
// the session resolved to chatgpt, or empty for any other provider and for a
// sign-in that is in order (the failure was something else, and the report
// says so on its own). It reads the record and nothing more: an unattended
// run that has no sign-in opens no browser and no listener, it says how to
// sign in and exits.
// See docs/capabilities/chatgpt-plan.md#a-run-nobody-is-watching-never-signs-in.
func chatGPTSurveyLine(providerName string) string {
	if providerName != "chatgpt" {
		return ""
	}
	if !chatgpt.HasRecord(chatGPTOptions().Dir) {
		return chatGPTNotSignedIn
	}
	a, err := chatGPTAccount()
	if err != nil {
		return chatGPTNotSignedIn
	}
	cred, err := a.Credential()
	switch {
	case err != nil || !cred.SignedIn():
		return chatGPTNotSignedIn
	case !cred.PlanGranted():
		return chatGPTNoPlan
	}
	return ""
}
