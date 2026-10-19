package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/catalog"
	"github.com/rfizzle/llm/chatgpt"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/testhttp"
	"github.com/rfizzle/shhh/internal/ui"
)

// The tokens of a signed-in test record. The refresh token has no shape the
// pattern table knows, which is the case the vault has to answer by value.
var testChatGPT = chatgpt.Credential{
	Email: "person@example.com", Issuer: chatgpt.Issuer, Subject: "user-abc", ClientID: "oaiapp_testclient",
	IDToken: "idtok_9f8e7d6c5b4a39281706", AccessToken: "acctok_1a2b3c4d5e6f7a8b9c0d",
	RefreshToken: "rftok_Zq8Wm3Np7Rx2Kv5Ts9Ly", TokenType: "Bearer",
	Scopes: strings.Fields(chatgpt.Scopes),
}

// resetChatGPT gives the test its own data directory, a fresh account memo
// and a fresh relay, and puts the process's back afterwards.
func resetChatGPT(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	chatGPT.mu.Lock()
	saved := chatGPT.account
	chatGPT.account = nil
	chatGPT.mu.Unlock()
	relay := chatGPTRelay
	chatGPTRelay = &tokenRelay{}
	t.Cleanup(func() {
		chatGPT.mu.Lock()
		chatGPT.account = saved
		chatGPT.mu.Unlock()
		chatGPTRelay = relay
	})
	return filepath.Join(data, "shhh", "chatgpt")
}

// signInRecord puts a record on this host the way a copied-in sign-in
// arrives.
func signInRecord(t *testing.T, c chatgpt.Credential) {
	t.Helper()
	c.ExpiresAt = time.Now().Add(time.Hour)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := chatGPTAccount()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Import(src); err != nil {
		t.Fatal(err)
	}
}

// mustNotCarry fails when text holds any of the three tokens whole, in
// base64, or as any eight-byte run.
func mustNotCarry(t *testing.T, what, text string) {
	t.Helper()
	for _, tok := range []string{testChatGPT.AccessToken, testChatGPT.RefreshToken, testChatGPT.IDToken} {
		forms := []string{tok, base64.StdEncoding.EncodeToString([]byte(tok))}
		for i := 0; i+8 <= len(tok); i++ {
			forms = append(forms, tok[i:i+8])
		}
		for _, f := range forms {
			if strings.Contains(text, f) {
				t.Errorf("%s carries %q:\n%s", what, f, text)
				return
			}
		}
	}
}

// The relay hides what it learned before a vault was attached and what it
// learns after, and a session vault opened on a signed-in host hides the
// record's tokens before anything is learned at all.
func TestSecrets_HideRelayReplays(t *testing.T) {
	resetChatGPT(t)
	chatGPTRelay.learn("early_token_value_123", "", "")
	v := secret.New()
	chatGPTRelay.attach(v)
	chatGPTRelay.learn("later_token_value_456")
	want := secret.Redacted(chatgpt.TokenKind)
	if got := v.Scrub("a early_token_value_123 b later_token_value_456"); got != "a "+want+" b "+want {
		t.Errorf("Scrub = %q", got)
	}

	signInRecord(t, testChatGPT)
	chatGPTRelay = &tokenRelay{}
	vault, err := loadSecrets(config.Config{}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	line := "cat credentials.json: " + testChatGPT.RefreshToken + " " +
		base64.StdEncoding.EncodeToString([]byte(testChatGPT.AccessToken)) + " " + testChatGPT.IDToken[3:15]
	mustNotCarry(t, "the scrubbed record", vault.Scrub(line))
	if len(vault.Names()) != 0 || vault.Len() != 0 {
		t.Errorf("the tokens were declared as secrets: %v", vault.Names())
	}
	mustNotCarry(t, "the command environment", strings.Join(vault.Environ(), "\n"))
	mustNotCarry(t, "the prompt block", secret.PromptBlock(vault, true))
}

// The plan's JWTs are hidden by their signature alone. Headers and claims
// are shared with other services' tokens — every RS256 header, every payload
// opening {"iss":"https:// — so an unrelated JWT a command prints after a
// sign-in still scrubs whole by its shape, while nothing of the plan's own
// signatures survives.
func TestSecrets_AHiddenJWTLeavesOtherJWTsWhole(t *testing.T) {
	resetChatGPT(t)
	header := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9"
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	random := func() string {
		b := make([]byte, 256)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	access := header + "." + seg(`{"iss":"https://auth.openai.com","aud":"https://api.openai.com/v1","scp":"resource.invoke"}`) + "." + random()
	id := header + "." + seg(`{"iss":"https://auth.openai.com","email":"person@example.com","nonce":"n"}`) + "." + random()
	v := secret.New()
	chatGPTRelay.attach(v)
	chatGPTRelay.learn(access, testChatGPT.RefreshToken, id)

	// Another service's tokens: the jwt.io example, and one whose header and
	// first claim are the plan token's own.
	jwtIO := header + ".eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiYWRtaW4iOnRydWUsImlhdCI6MTUxNjIzOTAyMn0." +
		"NHVaYe26MbtOYhSKkoKYdFVomg4i8ZJd8_-RU8VNbftc4TSMb4bXP3l3YlNWACwyXPGffz5aXHc6lxZu7UjdZRaaT0NlZbS1xLf0kmMdW1c1KJ8T7b4Q"
	sameIssuer := header + "." + seg(`{"iss":"https://auth.openai.com","sub":"someone-else","aud":"another-app"}`) + "." + random()
	for name, tok := range map[string]string{"jwt.io": jwtIO, "same first claim": sameIssuer} {
		if got := v.Scrub("token: " + tok); got != "token: "+secret.Redacted("jwt") {
			t.Errorf("%s: an unrelated JWT scrubbed to %q", name, got)
		}
	}

	for _, tok := range []string{access, id} {
		sig := tok[strings.LastIndexByte(tok, '.')+1:]
		// The whole token never leaves with its signature intact.
		if got := v.Scrub("out: " + tok); strings.Contains(got, sig) {
			t.Errorf("the plan's token survived with its signature: %q", got)
		}
		// Nor does any run of the signature, raw or in base64.
		for _, text := range []string{sig[17:61], base64.StdEncoding.EncodeToString([]byte(sig))} {
			got := v.Scrub("out: " + text)
			for i := 0; i+8 <= len(sig); i++ {
				if strings.Contains(got, sig[i:i+8]) {
					t.Errorf("an eight-byte run of the plan's signature survived: %q in %q", sig[i:i+8], got)
					break
				}
			}
			if strings.Contains(got, base64.StdEncoding.EncodeToString([]byte(sig))) {
				t.Errorf("the signature survived in base64: %q", got)
			}
		}
	}
	mustNotCarry(t, "the refresh token", v.Scrub(testChatGPT.RefreshToken))
}

// `chatgpt` resolves from the signed-in account, and the session's first
// request goes under that account's access token — through the one door
// that turns the account into a token source.
func TestChatGPT_ResolvesFromTheSignedInAccount(t *testing.T) {
	resetChatGPT(t)
	if providerTokens("chatgpt") == nil {
		t.Fatal("chatgpt has no token source")
	}
	signInRecord(t, testChatGPT)
	var bearer atomic.Value
	reg := &testhttp.Registry{}
	api := reg.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-plan-a","display_name":"Plan A","visibility":"list"}]}`))
	}))
	api.Alias("api.openai.com")
	t.Cleanup(api.Close)
	p, err := llm.Resolve("chatgpt", llm.ResolveOpts{TokenSource: providerTokens("chatgpt"), HTTPClient: reg.Client()})
	if err != nil {
		t.Fatalf("chatgpt did not resolve from a signed-in record: %v", err)
	}
	models, err := p.(catalog.Lister).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := bearer.Load().(string); got != "Bearer "+testChatGPT.AccessToken {
		t.Errorf("the first request went under %q", got)
	}
	if len(models) != 1 || models[0] != "gpt-plan-a" || chatGPTDefaultModel(models) != "gpt-plan-a" {
		t.Errorf("models = %v, default %q", models, chatGPTDefaultModel(models))
	}

	// Signed out, the same name fails to resolve with the sign-in error.
	a, _ := chatGPTAccount()
	if err := a.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := llm.Resolve("chatgpt", llm.ResolveOpts{TokenSource: providerTokens("chatgpt"), HTTPClient: reg.Client()}); !errors.Is(err, llm.ErrChatGPTSignedOut) {
		t.Errorf("a signed-out chatgpt resolved to %v", err)
	}
}

// A host that never signed in gets no directory from a session opening its
// vault or from the provider report.
func TestSecrets_NoSignInLeavesNoDirectory(t *testing.T) {
	dir := resetChatGPT(t)
	if _, err := loadSecrets(config.Config{}, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	_ = chatGPTRow()
	_ = chatGPTSurveyLine("chatgpt")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%s was created (%v)", dir, err)
	}
}

// The providers listing names the account and never a token, and the
// provider choice written for chatgpt is the provider's name and nothing
// else: it has no key to write.
func TestProviders_ChatGPTRowCarriesNoToken(t *testing.T) {
	resetChatGPT(t)
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("HOME", cfgDir)
	signInRecord(t, testChatGPT)

	listing := providersReport([]string{"chatgpt"}, nil, nil, nil, "", nil).String()
	if !strings.Contains(listing, "signed in as person@example.com") {
		t.Errorf("the row does not say who is signed in:\n%s", listing)
	}
	mustNotCarry(t, "shhh providers", listing)

	done := captureStderr(t)
	saveProviderChoice(config.Project{}, providerRequest{Provider: "chatgpt"})
	_ = done()
	written := readConfigFile(t)
	if !strings.Contains(written, `default = "chatgpt"`) || strings.Contains(written, "api_key") {
		t.Errorf("the saved choice:\n%s", written)
	}
	mustNotCarry(t, "the config file", written)

	noPlan := testChatGPT
	noPlan.Scopes = []string{"openid", "offline_access"}
	signInRecord(t, noPlan)
	if row := chatGPTRow(); !strings.Contains(row.Detail, "--consent") {
		t.Errorf("a sign-in without plan use reads %+v", row)
	}
}

// A sign-in without plan use says so and how to ask again.
func TestLogin_SaysPlanOffWhenScopeMissing(t *testing.T) {
	var out bytes.Buffer
	run := chatGPTLogin{
		login: func(context.Context, chatgpt.LoginOptions) (chatgpt.Result, error) {
			return chatgpt.Result{Email: "person@example.com"}, nil
		},
		configured: true,
		models: func(context.Context) ([]string, error) {
			t.Error("a sign-in without plan use listed the plan's models")
			return nil, nil
		},
	}
	if err := run.run(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"person@example.com", "plan use was not granted", "shhh providers login chatgpt --consent"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report lacks %q:\n%s", want, text)
		}
	}
}

// The sign-in sets provider.default only on a machine with nothing set up;
// anywhere else it says how and changes nothing. The plan's models are
// listed once when there is a lister.
func TestLogin_SetsTheDefaultOnlyWhenNothingIsConfigured(t *testing.T) {
	granted := func(context.Context, chatgpt.LoginOptions) (chatgpt.Result, error) {
		return chatgpt.Result{Email: "person@example.com", PlanGranted: true}, nil
	}
	for _, configured := range []bool{false, true} {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", dir)
		t.Setenv("HOME", dir)
		var out bytes.Buffer
		run := chatGPTLogin{login: granted, configured: configured,
			models: func(context.Context) ([]string, error) { return []string{"gpt-a", "gpt-b"}, nil }}
		if err := run.run(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(config.WritePath())
		switch {
		case !configured && (err != nil || !strings.Contains(string(raw), `default = "chatgpt"`)):
			t.Errorf("unconfigured: the default was not set (%v):\n%s", err, raw)
		case configured && err == nil:
			t.Errorf("configured: the config was written:\n%s", raw)
		case configured && !strings.Contains(strings.Join(strings.Fields(out.String()), " "), "shhh config set provider.default chatgpt"):
			t.Errorf("configured: no hint:\n%s", out.String())
		}
		if !strings.Contains(out.String(), "gpt-a, gpt-b") {
			t.Errorf("the models were not listed:\n%s", out.String())
		}
		if err == nil && strings.Contains(string(raw), "model") {
			t.Errorf("provider.model was written without --save:\n%s", raw)
		}
	}

	// --save writes the plan's default model, and on a configured machine
	// that is the only thing it writes.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	run := chatGPTLogin{login: granted, configured: true, save: true,
		models: func(context.Context) ([]string, error) { return []string{"gpt-a", "gpt-b"}, nil }}
	if err := run.run(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw := readConfigFile(t)
	if !strings.Contains(raw, `model = "gpt-a"`) || strings.Contains(raw, "chatgpt") {
		t.Errorf("--save on a configured machine wrote:\n%s", raw)
	}
}

// Whether a machine counts as configured: a default, a key in the file or
// the environment.
func TestProviderConfigured(t *testing.T) {
	for _, v := range []string{"SHHH_PROVIDER", "SHHH_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(v, "")
	}
	if len(profile.Loaded()) == 0 && providerConfigured(config.Config{}) {
		t.Error("an empty machine counts as configured")
	}
	var withDefault config.Config
	withDefault.Provider.Default = "anthropic"
	if !providerConfigured(withDefault) {
		t.Error("a default provider does not count")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-x")
	if !providerConfigured(config.Config{}) {
		t.Error("a key in the environment does not count")
	}
}

// A sign-out the server did not confirm says so and names where to finish
// it; one it did confirm says that.
func TestLogout_SaysWhenRevocationWasNotConfirmed(t *testing.T) {
	for _, revoked := range []bool{true, false} {
		var out bytes.Buffer
		err := chatGPTLogout(context.Background(), &out, func(context.Context) (bool, error) { return revoked, nil })
		if err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if revoked != strings.Contains(text, "revoked at OpenAI") || revoked == strings.Contains(text, "ChatGPT Settings") {
			t.Errorf("revoked=%v:\n%s", revoked, text)
		}
	}
	var out bytes.Buffer
	if err := chatGPTLogout(context.Background(), &out, func(context.Context) (bool, error) {
		return false, chatgpt.ErrNotSignedIn
	}); err != nil || !strings.Contains(out.String(), "not signed in") {
		t.Errorf("not signed in: %v\n%s", err, out.String())
	}
}

// noNetwork answers every request with a failure, for the survey's probe of
// a local runtime.
type noNetwork struct{}

func (noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in this test")
}

// A run that resolved chatgpt with no sign-in is told how to sign in, in
// the plain report an unattended entry prints before it exits 1 — and
// reading that touches nothing but the record.
func TestChatGPTSurveyLine_NamesTheSignIn(t *testing.T) {
	resetChatGPT(t)
	if got := chatGPTSurveyLine("openai"); got != "" {
		t.Errorf("another provider got %q", got)
	}
	line := chatGPTSurveyLine("chatgpt")
	if line != llm.ErrChatGPTSignedOut.Error() {
		t.Fatalf("line = %q", line)
	}
	s := resolve.SurveyPlaces(context.Background(), resolve.SurveyOpts{Provider: "chatgpt", ChatGPT: line,
		LocalBaseURL: "http://local.invalid/v1", HTTPClient: &http.Client{Transport: noNetwork{}}})
	if report := ui.PlainProviderReport(s); !strings.Contains(report, line) {
		t.Errorf("the plain report does not name the sign-in:\n%s", report)
	}
	signInRecord(t, testChatGPT)
	a, _ := chatGPTAccount()
	if err := a.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := chatGPTSurveyLine("chatgpt"); got != line {
		t.Errorf("a cleared sign-in reads %q", got)
	}
}
