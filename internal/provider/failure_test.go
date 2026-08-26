package provider

// The taxonomy's own tests (S-106). Each dialect hands back a different error
// shape for the same failure — a typed *openai.APIError, an Anthropic error
// envelope, a Gemini string with the status written into the prose — and the
// point of the classifier is that all three arrive as the same class. So the
// table below is organised by shape rather than by class: it is the mapping
// the story asked to be covered.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	openai "github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

// anthropicError builds the Messages API's error the way the SDK does, so the
// envelope the classifier reads is a real one rather than a struct literal
// with the fields it happens to want.
func anthropicError(t *testing.T, status int, body string) *anthropic.Error {
	t.Helper()
	err := &anthropic.Error{}
	if uerr := json.Unmarshal([]byte(body), err); uerr != nil {
		t.Fatalf("building the fixture error: %v", uerr)
	}
	err.StatusCode = status
	return err
}

func TestClassify_ProviderShapes(t *testing.T) {
	classify := newClassifier("openai", "OPENAI_API_KEY", "sk-live-abcd4f9c")

	cases := []struct {
		name string
		err  error
		want Class
	}{
		// openai chat + compat + openrouter + the gateway profiles over them
		{"openai 401", &openai.APIError{HTTPStatusCode: 401, Message: "Incorrect API key provided"}, ClassAuth},
		{"openai 403", &openai.APIError{HTTPStatusCode: 403, Message: "Country not supported"}, ClassAuth},
		{"openai 429 rate", &openai.APIError{HTTPStatusCode: 429, Message: "Rate limit reached for gpt-4o"}, ClassRateLimit},
		{"openai 429 quota", &openai.APIError{HTTPStatusCode: 429, Message: "You exceeded your current quota, please check your plan and billing details"}, ClassQuota},
		{"openai 400 context", &openai.APIError{HTTPStatusCode: 400, Message: "This model's maximum context length is 128000 tokens"}, ClassContextLength},
		{"openai 400 otherwise", &openai.APIError{HTTPStatusCode: 400, Message: "Unknown parameter: 'reasoning'"}, ClassUnclassified},
		{"openai 500", &openai.APIError{HTTPStatusCode: 500, Message: "The server had an error"}, ClassOverloaded},
		{"openai 503", &openai.APIError{HTTPStatusCode: 503, Message: "Service Unavailable"}, ClassOverloaded},
		{"openai transport 401", &openai.RequestError{HTTPStatusCode: 401, Body: []byte("invalid api key")}, ClassAuth},
		{"openai transport 429", &openai.RequestError{HTTPStatusCode: 429, Body: []byte("slow down")}, ClassRateLimit},

		// anthropic messages, and any gateway profile speaking it
		{"anthropic 401", anthropicError(t, 401, `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`), ClassAuth},
		{"anthropic 429", anthropicError(t, 429, `{"error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`), ClassRateLimit},
		{"anthropic 529", anthropicError(t, 529, `{"error":{"type":"overloaded_error","message":"Overloaded"}}`), ClassOverloaded},
		{"anthropic credit", anthropicError(t, 400, `{"error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the API"}}`), ClassQuota},
		{"anthropic context", anthropicError(t, 400, `{"error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000"}}`), ClassContextLength},

		// gemini, which reports its status in prose
		{"gemini typed 429", genai.APIError{Code: 429, Message: "Resource has been exhausted"}, ClassRateLimit},
		{"gemini 401 in prose", errors.New("googleapi: Error 401: API key not valid"), ClassAuth},
		{"gemini 403 in prose", errors.New("googleapi: Error 403: forbidden"), ClassAuth},
		{"gemini 429 in prose", errors.New("googleapi: Error 429: rate limit"), ClassRateLimit},
		{"gemini 503 in prose", errors.New("googleapi: Error 503: The model is overloaded"), ClassOverloaded},

		// the transport, which belongs to no dialect
		{"cancelled", context.Canceled, ClassCancelled},
		{"wrapped cancellation", &url.Error{Op: "Post", URL: "https://api.openai.com", Err: context.Canceled}, ClassCancelled},
		{"refused", &url.Error{Op: "Post", URL: "https://api.openai.com", Err: syscall.ECONNREFUSED}, ClassNetwork},
		{"dns", &url.Error{Op: "Post", URL: "https://api.example", Err: &net.DNSError{Err: "no such host"}}, ClassNetwork},
		{"deadline", context.DeadlineExceeded, ClassNetwork},
		{"dropped mid-stream", errors.New("unexpected EOF"), ClassNetwork},
		{"malformed body", errors.New("invalid character 'h' looking for beginning of value"), ClassMalformed},

		// and the one that has no name
		{"unrecognised", errors.New("something went sideways"), ClassUnclassified},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := AsFailure(classify(tc.err))
			if !ok {
				t.Fatalf("classify returned an unclassified error: %v", tc.err)
			}
			if f.Class != tc.want {
				t.Errorf("class = %q, want %q (message %q)", f.Class, tc.want, f.Message)
			}
		})
	}
}

func TestClassify_SentinelsMatchTheClass(t *testing.T) {
	classify := newClassifier("openai", "OPENAI_API_KEY", "")
	for class, sentinel := range sentinels {
		f := &Failure{Class: class}
		if !errors.Is(f, sentinel) {
			t.Errorf("errors.Is on %q did not match its sentinel", class)
		}
	}
	err := classify(&openai.APIError{HTTPStatusCode: 401, Message: "nope"})
	if !errors.Is(err, ErrAuth) {
		t.Errorf("a 401 should match ErrAuth, got %v", err)
	}
	if errors.Is(err, ErrRateLimited) {
		t.Errorf("a 401 should not match ErrRateLimited")
	}
}

func TestClassify_KeepsTheProvidersOwnWords(t *testing.T) {
	classify := newClassifier("openai", "OPENAI_API_KEY", "")
	f, _ := AsFailure(classify(&openai.APIError{HTTPStatusCode: 400, Message: "Unknown parameter: 'reasoning'"}))
	if f.Class != ClassUnclassified {
		t.Fatalf("class = %q, want unclassified", f.Class)
	}
	detail := f.Detail()
	if len(detail) != 1 || detail[0] != "Unknown parameter: 'reasoning'" {
		t.Errorf("the message should reach the detail body, got %q", detail)
	}
}

func TestClassify_BoundsTheMessage(t *testing.T) {
	long := make([]byte, maxMessage*3)
	for i := range long {
		long[i] = 'x'
	}
	classify := newClassifier("openai", "OPENAI_API_KEY", "")
	f, _ := AsFailure(classify(&openai.APIError{HTTPStatusCode: 500, Message: string(long)}))
	if got := len([]rune(f.Message)); got != maxMessage+1 {
		t.Errorf("message length = %d, want %d plus the ellipsis", got, maxMessage)
	}
}

func TestClassify_PassesAClassifiedFailureThrough(t *testing.T) {
	first := newClassifier("openai", "OPENAI_API_KEY", "sk-abcd")
	second := newClassifier("litellm", "LITELLM_KEY", "sk-zzzz")
	once := first(&openai.APIError{HTTPStatusCode: 401, Message: "nope"})
	twice := second(once)
	f, ok := AsFailure(twice)
	if !ok {
		t.Fatal("expected a classified failure")
	}
	if f.Provider != "openai" {
		t.Errorf("provider = %q, want the dialect that first named the failure", f.Provider)
	}
}

func TestClassify_CarriesTheKeyItSent(t *testing.T) {
	classify := newClassifier("openai", "SHHH_API_KEY or OPENAI_API_KEY", "sk-live-abcd4f9c")
	f, _ := AsFailure(classify(&openai.APIError{HTTPStatusCode: 401, Message: "nope"}))
	if f.KeyTail != "4f9c" {
		t.Errorf("KeyTail = %q, want the last four characters", f.KeyTail)
	}
	if f.KeyEnv != "SHHH_API_KEY or OPENAI_API_KEY" {
		t.Errorf("KeyEnv = %q", f.KeyEnv)
	}
	if f.Provider != "openai" {
		t.Errorf("Provider = %q", f.Provider)
	}
}

func TestKeyTail(t *testing.T) {
	for _, tc := range []struct{ key, want string }{
		{"", ""},
		{"ab", "ab"},
		{"abcd", "abcd"},
		{"sk-proj-longer-4f9c", "4f9c"},
		{"  sk-padded-1234  ", "1234"},
	} {
		if got := keyTail(tc.key); got != tc.want {
			t.Errorf("keyTail(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    time.Duration
	}{
		{"Rate limit reached. Please try again in 20s.", 20 * time.Second},
		{"retry after 38 seconds", 38 * time.Second},
		{"Please try again in 1.5s", 1500 * time.Millisecond},
		{"try again in 2 minutes", 2 * time.Minute},
		{"Please try again in 480ms", 480 * time.Millisecond},
		{"rate limited", 0},
	} {
		if got := retryAfter(tc.message); got != tc.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tc.message, got, tc.want)
		}
	}
}

func TestFailure_HeadlineAndRecoverable(t *testing.T) {
	withStatus := &Failure{Class: ClassAuth, Status: 401}
	if got := withStatus.Headline(); got != "401 unauthorized" {
		t.Errorf("Headline() = %q", got)
	}
	if withStatus.Recoverable() {
		t.Error("a rejected key is not a stall the session comes back from on its own")
	}
	bare := &Failure{Class: ClassNetwork}
	if got := bare.Headline(); got != "network" {
		t.Errorf("Headline() without a status = %q", got)
	}
	if !bare.Recoverable() {
		t.Error("a network failure is recoverable")
	}
	for _, class := range []Class{ClassRateLimit, ClassOverloaded, ClassNetwork} {
		if !(&Failure{Class: class}).Recoverable() {
			t.Errorf("%q should be recoverable", class)
		}
	}
	for _, class := range []Class{ClassAuth, ClassQuota, ClassContextLength, ClassMalformed, ClassCancelled, ClassUnclassified} {
		if (&Failure{Class: class}).Recoverable() {
			t.Errorf("%q should not be recoverable", class)
		}
	}
}

func TestFailure_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("inner")
	f := &Failure{Class: ClassOverloaded, Message: "Overloaded", err: inner}
	if got := f.Error(); got != "overloaded: Overloaded" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(f, inner) {
		t.Error("the provider's own error should stay reachable")
	}
	if got := (&Failure{Class: ClassCancelled}).Error(); got != "cancelled" {
		t.Errorf("Error() without a message = %q", got)
	}
}

func TestClassify_NilStaysNil(t *testing.T) {
	if err := newClassifier("openai", "", "")(nil); err != nil {
		t.Errorf("classifying nil returned %v", err)
	}
}
