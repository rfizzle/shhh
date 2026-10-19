package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/failure"
)

// wireRecorder stands in for the network under http.DefaultTransport and keeps
// the last request it was asked to send. It answers with a stream that ends at
// once, so a provider's turn completes.
type wireRecorder struct {
	url    string
	header http.Header
	body   string
	reply  string
}

func (w *wireRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	w.url, w.header = req.URL.String(), req.Header.Clone()
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		w.body = string(b)
	}
	h := http.Header{"Content-Type": []string{"text/event-stream"}}
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(w.reply)), Request: req}, nil
}

// onTheWire routes every provider built from here on through a recorder.
func onTheWire(t *testing.T, reply string) *wireRecorder {
	t.Helper()
	w := &wireRecorder{reply: reply}
	prev := http.DefaultTransport
	http.DefaultTransport = w
	t.Cleanup(func() { http.DefaultTransport = prev })
	return w
}

func turn(t *testing.T, p llm.Provider, messages []llm.Message, tools []llm.Tool) {
	t.Helper()
	ch, err := p.StreamCompletion(context.Background(), messages, llm.CompletionOpts{Model: "gpt-5.6", Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
}

const (
	chatDone      = "data: [DONE]\n\n"
	responsesDone = `data: {"type":"response.completed","response":{"status":"completed","output":[]}}` + "\n\n"
)

// TestShhhIdentityIsByteIdentical pins every string the provider layer writes
// for shhh: the variables a key and a base URL come from, the cache and
// routing keys, OpenRouter's headers, the plan route's tool group and the
// signed-out sentence. The expected values were captured before the provider
// layer left this repository, by running the same calls against the code that
// had them written in (the two cache keys for model "gpt-5.6": only that
// family is sent a key, so the wire cannot carry the old fixture's model "m"),
// so a change here is a change in what a person's cache,
// headers or environment see. It reaches them through llm's exported entry
// points and the requests they send.
func TestShhhIdentityIsByteIdentical(t *testing.T) {
	llm.SetApp(shhhApp)
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
		if _, err := llm.Resolve(name, llm.ResolveOpts{}); err == nil || err.Error() != want {
			t.Errorf("Resolve(%s) = %v, want %q", name, err, want)
		}
	}

	t.Run("environment names", func(t *testing.T) {
		t.Setenv("SHHH_API_KEY", "envkey")
		t.Setenv("SHHH_BASE_URL", "http://env.invalid/v1")
		user := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
		for _, name := range []string{"openai-compatible", "openai-responses"} {
			w := onTheWire(t, chatDone)
			if name == "openai-responses" {
				w.reply = responsesDone
			}
			p, err := llm.Resolve(name, llm.ResolveOpts{})
			if err != nil {
				t.Fatalf("Resolve(%s): %v", name, err)
			}
			turn(t, p, user, nil)
			if !strings.HasPrefix(w.url, "http://env.invalid/v1/") || w.header.Get("Authorization") != "Bearer envkey" {
				t.Errorf("%s sent %s with %q", name, w.url, w.header.Get("Authorization"))
			}
		}
	})

	t.Run("openrouter", func(t *testing.T) {
		t.Setenv("SHHH_API_KEY", "envkey")
		t.Setenv("SHHH_BASE_URL", "http://env.invalid/v1")
		for _, c := range []struct {
			name     string
			messages []llm.Message
			session  string
		}{
			{"with messages", []llm.Message{{Role: llm.RoleSystem, Content: "sys"}, {Role: llm.RoleUser, Content: "hi"}}, "shhh-19f1009488c3fa197540aa1346cdf402"},
			{"no messages", nil, "shhh-827b8604f8e45721866e86a49deb8a95"},
		} {
			w := onTheWire(t, chatDone)
			p, err := llm.Resolve("openrouter", llm.ResolveOpts{})
			if err != nil {
				t.Fatal(err)
			}
			turn(t, p, c.messages, nil)
			if got := w.header.Get("HTTP-Referer"); got != "https://github.com/rfizzle/shhh" {
				t.Errorf("%s: HTTP-Referer = %q", c.name, got)
			}
			if got := w.header.Get("X-Title"); got != "shhh" {
				t.Errorf("%s: X-Title = %q", c.name, got)
			}
			if got := w.header.Get("X-Session-Id"); got != c.session {
				t.Errorf("%s: session = %q, want %q", c.name, got, c.session)
			}
		}
	})

	t.Run("cache keys", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")
		w := onTheWire(t, chatDone)
		p, err := llm.Resolve("openai", llm.ResolveOpts{})
		if err != nil {
			t.Fatal(err)
		}
		turn(t, p, []llm.Message{{Role: llm.RoleSystem, Content: "s"}}, nil)
		if !strings.Contains(w.body, `"prompt_cache_key":"shhh-ddb8d3dcb3eefe5fad44292107f8abc71dd46bbf369c1b13a344586b46ed9729"`) {
			t.Errorf("chat cache key: %s", w.body)
		}

		w = onTheWire(t, responsesDone)
		p, err = llm.Resolve("openai-responses", llm.ResolveOpts{})
		if err != nil {
			t.Fatal(err)
		}
		turn(t, p, []llm.Message{{Role: llm.RoleSystem, Content: "ins"}}, []llm.Tool{{Name: "t", Description: "d", Parameters: []byte(`{}`)}})
		if !strings.Contains(w.body, `"prompt_cache_key":"shhh-7466b097652200aa4c08824d85ce9cee"`) {
			t.Errorf("responses cache key: %s", w.body)
		}
	})

	t.Run("the plan route", func(t *testing.T) {
		const signedOut = "not signed in to ChatGPT; run `shhh providers login chatgpt`"
		_, err := llm.Resolve("chatgpt", llm.ResolveOpts{})
		if !errors.Is(err, llm.ErrChatGPTSignedOut) || err == nil || err.Error() != signedOut {
			t.Errorf("Resolve(chatgpt) = %v", err)
		}
		if got := llm.ErrChatGPTSignedOut.Error(); got != signedOut {
			t.Errorf("signed-out error = %q", got)
		}

		w := &wireRecorder{reply: responsesDone}
		p, err := llm.Resolve("chatgpt", llm.ResolveOpts{TokenSource: staticToken("tok"), HTTPClient: &http.Client{Transport: w}})
		if err != nil {
			t.Fatal(err)
		}
		turn(t, p, []llm.Message{
			{Role: llm.RoleUser, Content: "go"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read_file", Arguments: "{}"}}},
		}, []llm.Tool{{Name: "read_file", Parameters: []byte(`{}`)}})
		if !strings.Contains(w.body, `"namespace":"shhh"`) || !strings.Contains(w.body, `{"type":"namespace","name":"shhh","description":"The tools this session can call.","tools":[`) {
			t.Errorf("plan request: %s", w.body)
		}

		// A refused token reads as the sign-in the person is to run.
		p, err = llm.Resolve("chatgpt", llm.ResolveOpts{TokenSource: &flakyToken{}, HTTPClient: &http.Client{Transport: w}})
		if err != nil {
			t.Fatal(err)
		}
		ch, err := p.StreamCompletion(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "go"}}, llm.CompletionOpts{})
		var f *failure.Failure
		if err == nil {
			for ev := range ch {
				if ev.Err != nil {
					err = ev.Err
				}
			}
		}
		if !errors.As(err, &f) || f.KeyEnv != "shhh providers login chatgpt" {
			t.Errorf("a refused token = %v", err)
		}
	})
}

type staticToken string

func (s staticToken) Token(context.Context, string) (string, error) { return string(s), nil }

// flakyToken answers once, so the provider builds, and then says the sign-in
// has expired.
type flakyToken struct{ n int }

func (f *flakyToken) Token(context.Context, string) (string, error) {
	f.n++
	if f.n == 1 {
		return "tok", nil
	}
	return "", errors.New("the ChatGPT sign-in has expired")
}
