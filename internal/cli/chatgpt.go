package cli

// The ChatGPT plan sign-in, as the rest of the CLI reaches it: one account
// per process, a relay that hides every token it learns from the model, and
// the sentence the setup report says about it.
// See docs/capabilities/chatgpt-plan.md.

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/rfizzle/agentcore/secret"
	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/catalog"
	"github.com/rfizzle/llm/chatgpt"
	"github.com/rfizzle/llm/openai/responses"
	"github.com/rfizzle/shhh/internal/storage/datadir"
)

// chatGPT is the process's one account, opened on first need. It is one per
// process so that every request, every refresh and every vault go through
// the same lock and the same relay.
var chatGPT struct {
	mu      sync.Mutex
	account *chatgpt.Account
}

// chatGPTStore is where the sign-in lives: <datadir>/chatgpt, beside the
// store and never in the config directory. A data directory that cannot be
// found is an empty store that holds no record; opening an account says why.
func chatGPTStore() chatgpt.FileStore {
	store, _ := chatGPTStoreOrErr()
	return store
}

// chatGPTStoreOrErr is chatGPTStore with the reason a data directory was not
// found, which is what a person needs to hear when the account will not open.
func chatGPTStoreOrErr() (chatgpt.FileStore, error) {
	data, err := datadir.Dir()
	if err != nil {
		return chatgpt.FileStore{}, err
	}
	return chatgpt.FileStore{Dir: filepath.Join(data, "chatgpt")}, nil
}

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
	store, err := chatGPTStoreOrErr()
	if err != nil {
		return nil, err
	}
	o := chatgpt.Options{App: shhhApp, Store: store}
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
	p, err := llm.Resolve("chatgpt", llm.ResolveOpts{TokenSource: chatGPTTokens()})
	if err != nil {
		return nil, err
	}
	lister, ok := p.(catalog.Lister)
	if !ok {
		return nil, nil
	}
	return lister.ListModels(ctx)
}

// chatGPTDefaultModel picks the model a sign-in saved with --save writes to
// provider.model, out of the ones the plan listed.
var chatGPTDefaultModel = responses.ChatGPTDefaultModel

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
	// stamp is the version of the record this relay last read, so a scrub
	// costs a stat and reads the record only when another process has
	// replaced it.
	stamp chatgpt.RecordStamp
}

// refresh hides the tokens of a record another process has written since this
// relay last looked. The shell is the reason it exists: a command can read
// the record whatever the containment setting, and its output is scrubbed
// by a vault that knows only what this session has learned, so each scrub
// first asks whether the record changed. It is a stat when nothing did, and a
// read of the record, never logged, when something did. A record that cannot
// be read at all is tried again at the next scrub; one that reads but does not
// parse is remembered as the version it is.
// See docs/capabilities/chatgpt-plan.md#what-keeps-the-tokens-from-the-model.
func (r *tokenRelay) refresh() {
	store := chatGPTStore()
	now := chatgpt.StatRecord(store)
	r.mu.Lock()
	same := now.Same(r.stamp)
	r.mu.Unlock()
	if same {
		return
	}
	cred, err := chatgpt.ReadRecord(store)
	if err != nil && !chatgpt.IsMalformed(err) {
		return
	}
	// A record that parsed badly is a version too: it is not read and
	// parsed again at every line of output, and the next write replaces it.
	r.mu.Lock()
	r.stamp = now
	r.mu.Unlock()
	if err != nil {
		return
	}
	r.learn(cred.AccessToken, cred.RefreshToken, cred.IDToken)
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
// signature alone. The vault scrubs every eight-byte run of what it hides
// before the shape pass runs, and headers and claims are shared: every RS256
// token starts with the same header, and a payload opening `{"iss":"https://`
// encodes the same way whoever issued it. Hiding either would cut a run out
// of an unrelated JWT in a command's output, what was left would no longer
// look like a JWT, and it would reach the model whole. The signature is
// random, so it shares a run with nothing, and a JWT without its signature
// is not a credential: the header and payload are claims.
func hiddenPart(token string) string {
	if strings.HasPrefix(token, "eyJ") && strings.Count(token, ".") == 2 {
		return token[strings.LastIndexByte(token, '.')+1:]
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
	v.BeforeScrub(r.refresh)
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
	if !chatgpt.HasRecord(chatGPTStore()) {
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

// chatGPTNoPlan is the sentence the setup report says about a sign-in that
// was not granted plan use. The signed-out sentence is the plan route's own
// error (provider.ErrChatGPTSignedOut), read where it is said, so a run that
// fails to resolve and the report that explains it say the same words.
var chatGPTNoPlan = "signed in to ChatGPT, but plan use was not granted — run `shhh providers login chatgpt --consent`"

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
	if !chatgpt.HasRecord(chatGPTStore()) {
		return llm.ErrChatGPTSignedOut.Error()
	}
	a, err := chatGPTAccount()
	if err != nil {
		return llm.ErrChatGPTSignedOut.Error()
	}
	cred, err := a.Credential()
	switch {
	case err != nil || !cred.SignedIn():
		return llm.ErrChatGPTSignedOut.Error()
	case !cred.PlanGranted():
		return chatGPTNoPlan
	}
	return ""
}

// setupSignIn is the sign-in the first-run card's "continue with ChatGPT"
// runs: the browser opens through the account's own opener, the address is
// said on the terminal if it does not, and a sign-in that comes back without
// plan use is an error that names the way to ask for it, since a session on
// it would be refused on its first request. A test replaces it.
var setupSignIn = func(ctx context.Context, say io.Writer) error {
	a, err := chatGPTAccount()
	if err != nil {
		return err
	}
	res, err := a.Login(ctx, chatgpt.LoginOptions{Say: say})
	if err != nil {
		return err
	}
	if !res.PlanGranted {
		return errors.New(chatGPTNoPlan)
	}
	return nil
}
