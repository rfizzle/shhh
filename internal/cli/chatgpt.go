package cli

// The ChatGPT plan sign-in, as the rest of the CLI reaches it: one account
// per process, a relay that hides every token it learns from the model, and
// the sentence the setup report says about it.
// See docs/capabilities/chatgpt-plan.md.

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/chatgpt"
	"github.com/rfizzle/shhh/internal/provider"
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

// chatGPTRelay hides every token the account learns in every session vault.
var chatGPTRelay = &tokenRelay{}

// chatGPTAccount is the process's account. A failure to open it is not
// remembered: the directory may be creatable on the next try.
func chatGPTAccount() (*chatgpt.Account, error) {
	chatGPT.mu.Lock()
	defer chatGPT.mu.Unlock()
	if chatGPT.account != nil {
		return chatGPT.account, nil
	}
	o := chatGPTOptions()
	relay := chatGPTRelay
	o.OnTokens = func(access, refresh, id string) { relay.learn(access, refresh, id) }
	a, err := chatgpt.Open(o)
	if err != nil {
		return nil, err
	}
	chatGPT.account = a
	return a, nil
}

// listChatGPTModels lists the models the plan serves the signed-in account,
// so a sign-in can say what it unlocked. It is the plan provider's own list,
// built on the process's account; a test replaces it.
var listChatGPTModels = func(ctx context.Context) ([]string, error) {
	p, err := provider.Resolve("chatgpt", provider.ResolveOpts{TokenSource: chatGPTTokens()})
	if err != nil {
		return nil, err
	}
	lister, ok := p.(provider.ModelLister)
	if !ok {
		return nil, nil
	}
	return lister.ListModels(ctx)
}

// chatGPTDefaultModel picks the model a sign-in saved with --save writes to
// provider.model, out of the ones the plan listed.
var chatGPTDefaultModel = provider.ChatGPTDefaultModel

// tokenRelay hands the tokens the account learns to the session vaults, in
// either order: a vault attached after a refresh is told what was learned
// before it, and one attached first hears every later set. That is what lets
// the provider and the vault be built in whichever order a command builds
// them.
//
// The refresh token is the reason this exists. The access and ID tokens are
// JWTs, which the shape table already redacts; the refresh token is opaque,
// and nothing but its value can find it.
// See docs/capabilities/chatgpt-plan.md#what-keeps-the-tokens-from-the-model.
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
		v = hiddenPart(v)
		if v == "" || slices.Contains(r.seen, v) {
			continue
		}
		r.seen = append(r.seen, v)
		for _, vault := range r.vaults {
			vault.Hide(chatgpt.TokenKind, v)
		}
	}
}

// hiddenPart is what of a token the vault is given. A JWT is hidden by its
// payload and signature and not its header: every RS256 token anyone issues
// starts with the same header, and the vault scrubs every eight-byte run of
// what it hides before the shape pass runs, so a hidden header would cut the
// front off an unrelated JWT in a command's output — and what was left would
// no longer look like a JWT, and would reach the model whole. The header is
// the algorithm's name and a key ID, not the secret.
func hiddenPart(token string) string {
	if strings.HasPrefix(token, "eyJ") && strings.Count(token, ".") == 2 {
		return token[strings.IndexByte(token, '.')+1:]
	}
	return token
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
	chatGPTRelay.attach(v)
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
	chatGPTRelay.learn(cred.AccessToken, cred.RefreshToken, cred.IDToken)
}

// The sentences the setup report says about the sign-in. The signed-out one
// is the plan route's own error, so a run that fails to resolve and the
// report that explains it say the same words.
var (
	chatGPTNotSignedIn = provider.ErrChatGPTSignedOut.Error()
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
