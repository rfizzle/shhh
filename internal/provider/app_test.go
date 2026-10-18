package provider

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

var shhhApp = App{
	Name:       "shhh",
	EnvPrefix:  "SHHH",
	SignInHint: "shhh providers login chatgpt",
	URL:        "https://github.com/rfizzle/shhh",
}

// TestMain sets the app the way shhh does, because the rest of this package's
// tests were written against SHHH_API_KEY and the shhh- keys.
func TestMain(m *testing.M) {
	SetApp(shhhApp)
	os.Exit(m.Run())
}

// withApp sets a for one test and puts shhh's back after.
func withApp(t *testing.T, a App) {
	t.Helper()
	SetApp(a)
	t.Cleanup(func() { SetApp(shhhApp) })
}

type headerRecorder struct{ h http.Header }

func (r *headerRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.h = req.Header.Clone()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

// TestShhhIdentityIsByteIdentical pins every string the backends write for
// the caller. The expected values were captured on the commit before the app
// became a setting, by running the same calls against the code that had them
// written in, so a change here is a change in what a person's cache, headers
// or environment see.
func TestShhhIdentityIsByteIdentical(t *testing.T) {
	for _, k := range []string{"SHHH_API_KEY", "SHHH_BASE_URL", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(k, "")
	}
	for name, want := range map[string]string{
		"anthropic":        "SHHH_API_KEY or ANTHROPIC_API_KEY is not set",
		"gemini":           "SHHH_API_KEY or GEMINI_API_KEY is not set",
		"openai":           "SHHH_API_KEY or OPENAI_API_KEY is not set",
		"openai-responses": "SHHH_API_KEY or OPENAI_API_KEY is not set",
		"openrouter":       "SHHH_API_KEY or OPENROUTER_API_KEY is not set",
	} {
		if _, err := Resolve(name, ResolveOpts{}); err == nil || err.Error() != want {
			t.Errorf("Resolve(%s) = %v, want %q", name, err, want)
		}
	}

	t.Setenv("SHHH_API_KEY", "envkey")
	t.Setenv("SHHH_BASE_URL", "http://env.invalid/v1")
	c, err := NewOpenAICompat(ResolveOpts{})
	if err != nil || c.apiKey != "envkey" || c.baseURL != "http://env.invalid/v1" {
		t.Errorf("compat read %q %q (%v)", c.apiKey, c.baseURL, err)
	}
	r, err := NewOpenAIResponses(ResolveOpts{})
	if err != nil || r.apiKey != "envkey" || r.baseURL != "http://env.invalid/v1" {
		t.Errorf("responses read %q %q (%v)", r.apiKey, r.baseURL, err)
	}

	rec := &headerRecorder{}
	req, _ := http.NewRequest(http.MethodPost, "http://x.invalid", nil)
	if _, err := (&openRouterTransport{base: rec}).RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if got := rec.h.Get("HTTP-Referer"); got != "https://github.com/rfizzle/shhh" {
		t.Errorf("HTTP-Referer = %q", got)
	}
	if got := rec.h.Get("X-Title"); got != "shhh" {
		t.Errorf("X-Title = %q", got)
	}

	messages := []Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "hi"}}
	for _, c := range []struct{ name, got, want string }{
		{"openrouter session", openRouterSessionID(messages), "shhh-19f1009488c3fa197540aa1346cdf402"},
		{"openrouter session, no messages", openRouterSessionID(nil), "shhh-827b8604f8e45721866e86a49deb8a95"},
		{"chat cache key", chatPromptCacheKey("m", json.RawMessage(`[{"role":"system","content":"s"}]`), json.RawMessage(`[]`)), "shhh-5520eb3a07d223c41253f016483d2424934f753c5ed41f9cffdc7c4b84cbd7ef"},
		{"responses cache key", responseCacheKey("m", "ins", []Tool{{Name: "t", Description: "d", Parameters: json.RawMessage(`{}`)}}), "shhh-6edd4baac2de7dee38778661cb677e8d"},
		{"sign-in hint", chatGPTKeyEnv(), "shhh providers login chatgpt"},
		{"signed-out error", ErrChatGPTSignedOut.Error(), "not signed in to ChatGPT; run `shhh providers login chatgpt`"},
		{"tool group", planToolNamespace(), "shhh"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	plan := &responsesRequest{Tools: []responsesTool{{Name: "read_file"}}, Input: []responseItem{{Type: "function_call"}}}
	rename := shapeForPlan(plan)
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	const wantPlan = `{"model":"","input":[{"type":"function_call","namespace":"shhh"}],"stream":true,"store":false,"tools":[{"type":"namespace","name":"shhh","description":"The tools this session can call.","tools":[{"type":"","name":"read_file"}]}]}`
	if string(body) != wantPlan {
		t.Errorf("plan request = %s, want %s", body, wantPlan)
	}
	if got := rename("", "shhh.read_file"); got != "read_file" {
		t.Errorf("a group-qualified name mapped to %q", got)
	}

	_, err = Resolve("chatgpt", ResolveOpts{})
	if !errors.Is(err, ErrChatGPTSignedOut) || err.Error() != "not signed in to ChatGPT; run `shhh providers login chatgpt`" {
		t.Errorf("Resolve(chatgpt) = %v", err)
	}
}

// TestTheNeutralAppNamesNoApp: an app that sets nothing reads LLM_ variables,
// keys its caches under llm, sends no referer and gives a sign-in sentence
// that names no command; and the signed-out error still matches by identity.
func TestTheNeutralAppNamesNoApp(t *testing.T) {
	withApp(t, App{})
	for _, k := range []string{"LLM_API_KEY", "SHHH_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(k, "")
	}
	_, err := Resolve("anthropic", ResolveOpts{})
	if err == nil || err.Error() != "LLM_API_KEY or ANTHROPIC_API_KEY is not set" {
		t.Errorf("Resolve(anthropic) = %v", err)
	}
	t.Setenv("LLM_API_KEY", "k")
	if _, err := Resolve("anthropic", ResolveOpts{}); err != nil {
		t.Errorf("LLM_API_KEY was not read: %v", err)
	}

	rec := &headerRecorder{}
	req, _ := http.NewRequest(http.MethodPost, "http://x.invalid", nil)
	_, _ = (&openRouterTransport{base: rec}).RoundTrip(req)
	if _, ok := rec.h["Http-Referer"]; ok || rec.h.Get("X-Title") != "llm" {
		t.Errorf("headers = %v", rec.h)
	}
	if got := openRouterSessionID(nil); !strings.HasPrefix(got, "llm-") {
		t.Errorf("session id %q", got)
	}
	if got := ErrChatGPTSignedOut.Error(); got != "not signed in to ChatGPT" {
		t.Errorf("signed-out = %q", got)
	}
	if !errors.Is(ErrChatGPTSignedOut, ErrChatGPTSignedOut) {
		t.Error("the sentinel does not match itself")
	}
}
