package provider

// The provider failure taxonomy (
// docs/interface/surfaces.md#the-recovery-row).
//
// Every dialect used to map its own 401 and its own 429 and hand everything
// else to the transcript as a Go error string, so one failure read four
// different ways and most failures read as a stack trace. One classifier now
// stands between every dialect and the rest of shhh. It names what went wrong
// from a closed vocabulary, keeps the provider's own words for the detail
// body rather than promoting them to the headline, and carries what a
// recovery key needs to be offered honestly: which key was sent, and how long
// the provider asked us to wait.
//
// The classes are this package's; the keys are the UI's (internal/ui/chat,
// internal/ui). Nothing here knows what a keystroke does, and nothing here
// decides whether a failure is worth a row or a card.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/rfizzle/shhh/internal/logs"
	openai "github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

// Class is what went wrong, from a closed vocabulary. The value
// is the word the interface says, so a class the UI has no case for still
// reads as itself rather than as a number.
type Class string

const (
	// ClassAuth — the key was rejected: 401, 403, an invalid-key message.
	ClassAuth Class = "unauthorized"
	// ClassRateLimit — too many requests for now; the same request later works.
	ClassRateLimit Class = "rate limited"
	// ClassQuota — the account is out of credit or over its cap; waiting
	// does not help, which is why it is not a rate limit.
	ClassQuota Class = "quota exhausted"
	// ClassOverloaded — the provider is failing on its own side: 5xx, 529.
	ClassOverloaded Class = "overloaded"
	// ClassContextLength — the request did not fit the model's window.
	ClassContextLength Class = "context too long"
	// ClassNetwork — the request never reached the provider, or the
	// connection died on the way back.
	ClassNetwork Class = "network"
	// ClassMalformed — a response arrived that was not the shape promised.
	ClassMalformed Class = "malformed response"
	// ClassCancelled — the context was cancelled; usually you pressed Esc.
	ClassCancelled Class = "cancelled"
	// ClassUnclassified — a failure this table has no case for. It is a real
	// class, not an error path: the row still renders on the grid and the
	// provider's own message goes in the detail body.
	ClassUnclassified Class = "unclassified"
)

// The sentinels errors.Is matches a Failure against, one per class. Callers
// that only need "was this the key" ask for ErrAuth rather than unpacking the
// Failure.
var (
	ErrAuth          = errors.New(string(ClassAuth))
	ErrRateLimited   = errors.New(string(ClassRateLimit))
	ErrQuota         = errors.New(string(ClassQuota))
	ErrOverloaded    = errors.New(string(ClassOverloaded))
	ErrContextLength = errors.New(string(ClassContextLength))
	ErrNetwork       = errors.New(string(ClassNetwork))
	ErrMalformed     = errors.New(string(ClassMalformed))
	ErrCancelled     = errors.New(string(ClassCancelled))
	ErrUnclassified  = errors.New(string(ClassUnclassified))
)

var sentinels = map[Class]error{
	ClassAuth:          ErrAuth,
	ClassRateLimit:     ErrRateLimited,
	ClassQuota:         ErrQuota,
	ClassOverloaded:    ErrOverloaded,
	ClassContextLength: ErrContextLength,
	ClassNetwork:       ErrNetwork,
	ClassMalformed:     ErrMalformed,
	ClassCancelled:     ErrCancelled,
	ClassUnclassified:  ErrUnclassified,
}

// maxMessage bounds the provider's own words. The detail body is bounded on
// screen anyway; this keeps a provider that answers a 500 with an HTML error
// page out of the transcript's memory as well as off its rows.
const maxMessage = 400

// Failure is one classified provider call. It is the error every dialect
// returns, so the surfaces downstream never see a raw provider error again.
type Failure struct {
	// Class is what went wrong.
	Class Class
	// Provider names the dialect that produced it — a built-in name, or a
	// gateway profile's own name, so a failure behind a gateway says which
	// gateway.
	Provider string
	// Status is the HTTP status where the transport reported one, 0 otherwise.
	Status int
	// Message is the provider's own words, bounded. It belongs in the detail
	// body: it is evidence, not the headline.
	Message string
	// KeyEnv names the variables the key was looked for in, for the auth
	// row's offer to replace it.
	KeyEnv string
	// KeyTail is the last four characters of the key that was sent — enough
	// to tell two keys apart, never enough to be one.
	KeyTail string
	// RetryAfter is the wait the provider asked for, when it named one.
	RetryAfter time.Duration

	err error
}

// Error renders the failure as a one-line Go error: the class, then the
// provider's own words where it had any. It never calls the wrapped error's
// own Error — some SDK error types panic on a synthetic value — so the string
// is composed from what was extracted rather than from what was wrapped.
func (f *Failure) Error() string {
	if f.Message == "" {
		return string(f.Class)
	}
	return string(f.Class) + ": " + f.Message
}

// Unwrap exposes the provider's error for callers that want the original.
func (f *Failure) Unwrap() error { return f.err }

// Is matches the class sentinel, so errors.Is(err, ErrAuth) works on any
// dialect's failure.
func (f *Failure) Is(target error) bool { return sentinels[f.Class] == target }

// Headline is the row's target field: the status where there was one,
// then the class. `401 unauthorized`, `overloaded`.
func (f *Failure) Headline() string {
	if f.Status > 0 {
		return strconv.Itoa(f.Status) + " " + string(f.Class)
	}
	return string(f.Class)
}

// Recoverable reports whether the failure is a stall the session can come
// back from — ⚠ in the row's glyph — rather than a call that is over. The
// distinction is the whole reason both glyphs exist: a rate limit
// resumes, a rejected key does not until you replace it.
func (f *Failure) Recoverable() bool {
	switch f.Class {
	case ClassRateLimit, ClassOverloaded, ClassNetwork:
		return true
	}
	return false
}

// Detail is the bounded detail body: the provider's own words, split on the
// lines it wrote them in and stripped of blank ones. An unclassified failure
// is the reason this exists — it is where the message that could not be
// named still gets said.
func (f *Failure) Detail() []string {
	var out []string
	for _, line := range strings.Split(f.Message, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// AsFailure unwraps a classified provider failure, reporting whether the
// error was one. Every surface that renders an error from a provider call
// goes through it.
func AsFailure(err error) (*Failure, bool) {
	var f *Failure
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}

// classifier is one dialect's binding of the shared taxonomy: the provider
// name and the key it sends, which are the only things the classification
// needs that the error itself cannot say.
type classifier struct {
	name   string
	keyEnv string
	key    string
}

// newClassifier returns the func every dialect wraps its errors in. keyEnv is
// the variables the key was looked for in, for the auth row's offer; key is
// the key that was actually sent, of which only the last four characters are
// ever kept.
func newClassifier(name, keyEnv, key string) func(error) error {
	c := classifier{name: name, keyEnv: keyEnv, key: key}
	return c.classify
}

// classify names one error. A failure that is already classified passes
// through untouched, so a dialect that wraps another dialect (a gateway
// profile over the openai client) does not re-answer the question.
func (c classifier) classify(err error) error {
	if err == nil {
		return nil
	}
	if f, ok := AsFailure(err); ok {
		return f
	}
	status, message := errorShape(err)
	f := &Failure{
		Provider: c.name,
		Status:   status,
		Message:  bound(message),
		KeyEnv:   c.keyEnv,
		KeyTail:  keyTail(c.key),
		err:      err,
	}
	f.Class = classOf(err, status, message)
	f.RetryAfter = retryAfter(message)
	record(f)
	return f
}

// record writes one failure to the diagnostic log. Every refusal is written
// from here and from nowhere else, because this is the only place a provider
// failure is named: a dialect that classified its own error would be logged
// twice, and one that did not would be missed entirely. A refusal that is
// then waited out is written down a second time by whatever puts the wait
// on, which is a different event — how long, and how many more — and not a
// second account of this one.
//
// A cancellation is left out. It is the reader pressing Esc, and a log whose
// commonest line is something the reader did on purpose is one they stop
// reading. Everything else is a request that failed, which is what somebody
// tailing this file came for.
// See docs/capabilities/configuration.md#a-failure-is-written-down.
func record(f *Failure) {
	if f.Class == ClassCancelled {
		return
	}
	// The message is the provider's own, already bounded, and already what
	// the recovery row on the screen shows — writing it down exposes nothing
	// the session did not.
	logs.Logger().Error("provider request refused",
		"provider", f.Provider, "class", string(f.Class),
		"status", f.Status, "detail", f.Message)
}

// classOf is the mapping itself, and the only place it lives. Cancellation
// comes first because a cancelled request often surfaces as a transport
// error that would otherwise read as a network failure; the status is asked
// next because a provider that answered at all is more authoritative than
// its prose; the prose is last, for the dialects that hand back nothing else.
func classOf(err error, status int, message string) Class {
	if errors.Is(err, context.Canceled) {
		return ClassCancelled
	}
	if status > 0 {
		if class, ok := classFromStatus(status, message); ok {
			return class
		}
	}
	if class, ok := classFromMessage(message); ok {
		return class
	}
	if isNetworkError(err) {
		return ClassNetwork
	}
	return ClassUnclassified
}

// classFromStatus maps an HTTP status to a class, letting the message settle
// the two statuses that carry more than one meaning: a 429 is a rate limit
// unless the account is out of credit, and a 400 is nothing in particular
// unless it says the request did not fit.
func classFromStatus(status int, message string) (Class, bool) {
	switch status {
	case 401, 403:
		return ClassAuth, true
	case 402:
		return ClassQuota, true
	case 408:
		return ClassNetwork, true
	case 413:
		return ClassContextLength, true
	case 429:
		if hasAny(message, quotaPhrases) {
			return ClassQuota, true
		}
		return ClassRateLimit, true
	case 400, 422:
		if hasAny(message, contextPhrases) {
			return ClassContextLength, true
		}
		if hasAny(message, quotaPhrases) {
			return ClassQuota, true
		}
		// Everything else a 400 can mean is a bug in the request, and there
		// is no honest name for it here — the message goes in the body and
		// the row says unclassified.
		return "", false
	case 500, 502, 503, 504, 529:
		return ClassOverloaded, true
	}
	return "", false
}

// The phrase tables. They are matched against the provider's own message
// lowercased, and are the fallback for the dialects that report a status only
// in prose (Gemini) and for transport errors that carry no status at all.
var (
	contextPhrases = []string{
		"context length", "context_length_exceeded", "maximum context",
		"too many tokens", "prompt is too long", "input is too long",
		"reduce the length", "exceeds the maximum", "max_tokens",
	}
	quotaPhrases = []string{
		"insufficient_quota", "quota", "billing", "credit balance",
		"payment required", "spending limit", "out of credits",
	}
	ratePhrases = []string{
		"rate limit", "rate_limit", "too many requests",
	}
	overloadPhrases = []string{
		"overloaded", "service unavailable", "temporarily unavailable",
		"server had an error", "internal server error", "at capacity",
		"try again later",
	}
	authPhrases = []string{
		"invalid api key", "incorrect api key", "invalid_api_key",
		"api key not valid", "unauthorized", "authentication",
		"permission denied", "api_key_invalid", "forbidden",
	}
	malformedPhrases = []string{
		"unexpected end of json", "invalid character", "cannot unmarshal",
		"invalid json", "unrecognized catalog shape", "unmarshal",
	}
	networkPhrases = []string{
		"connection refused", "no such host", "dial tcp", "i/o timeout",
		"deadline exceeded", "connection reset", "unexpected eof",
		"network is unreachable", "tls handshake", "eof",
	}
)

// classFromMessage reads the class out of the provider's prose. The order is
// the disambiguation: quota before rate limit because an out-of-credit 429
// says both, malformed before network because "unexpected EOF" is a dropped
// connection and "unexpected end of JSON input" is not.
func classFromMessage(message string) (Class, bool) {
	msg := strings.ToLower(message)
	// A status the dialect only wrote into its prose still outranks the
	// prose itself — Gemini reports `googleapi: Error 429: ...` and nothing
	// typed.
	if status := statusFromMessage(msg); status > 0 {
		if class, ok := classFromStatus(status, msg); ok {
			return class, true
		}
	}
	for _, table := range []struct {
		phrases []string
		class   Class
	}{
		{contextPhrases, ClassContextLength},
		{quotaPhrases, ClassQuota},
		{ratePhrases, ClassRateLimit},
		{overloadPhrases, ClassOverloaded},
		{authPhrases, ClassAuth},
		{malformedPhrases, ClassMalformed},
		{networkPhrases, ClassNetwork},
	} {
		if hasAny(msg, table.phrases) {
			return table.class, true
		}
	}
	return "", false
}

// statusPattern finds a three-digit HTTP status written into a message, in
// the shapes the SDKs write it: `Error 429:`, `status 503`, `HTTP 401`.
var statusPattern = regexp.MustCompile(`(?i)\b(?:error|status|status code|http)[: ]+(\d{3})\b`)

func statusFromMessage(message string) int {
	m := statusPattern.FindStringSubmatch(message)
	if m == nil {
		return 0
	}
	status, err := strconv.Atoi(m[1])
	if err != nil || status < 400 || status > 599 {
		return 0
	}
	return status
}

// retryPattern finds the wait a provider asked for, in the shapes they write
// it: `try again in 38s`, `retry after 20 seconds`, `Please retry in 1.5s`.
var retryPattern = regexp.MustCompile(`(?i)(?:try again|retry|wait)(?:\s+after|\s+in)?\s+([0-9]+(?:\.[0-9]+)?)\s*(ms|s|sec|secs|second|seconds|m|min|mins|minute|minutes)\b`)

// retryAfter reads the provider's own wait out of its message. Zero means it
// did not name one, which is different from "retry now".
func retryAfter(message string) time.Duration {
	m := retryPattern.FindStringSubmatch(message)
	if m == nil {
		return 0
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil || value <= 0 {
		return 0
	}
	unit := time.Second
	switch strings.ToLower(m[2]) {
	case "ms":
		unit = time.Millisecond
	case "m", "min", "mins", "minute", "minutes":
		unit = time.Minute
	}
	return time.Duration(value * float64(unit))
}

// errorShape pulls the status and the provider's own message out of the typed
// errors each SDK returns. It never calls the SDK error's own Error method:
// the Anthropic one dereferences the request it was built from, which a
// synthetic error in a test does not have.
func errorShape(err error) (int, string) {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatusCode, apiErr.Message
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		message := strings.TrimSpace(string(reqErr.Body))
		if message == "" && reqErr.Err != nil {
			message = reqErr.Err.Error()
		}
		return reqErr.HTTPStatusCode, message
	}
	var anthErr *anthropic.Error
	if errors.As(err, &anthErr) {
		return anthErr.StatusCode, anthropicMessage(anthErr)
	}
	var genErr genai.APIError
	if errors.As(err, &genErr) {
		return genErr.Code, genErr.Message
	}
	return 0, err.Error()
}

// anthropicMessage reads the message out of the Messages API's error
// envelope, falling back to the error type it names — `overloaded_error` is a
// worse sentence than the message but a better one than nothing.
func anthropicMessage(err *anthropic.Error) string {
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if raw := err.RawJSON(); raw != "" {
		if json.Unmarshal([]byte(raw), &envelope) == nil && envelope.Error.Message != "" {
			return envelope.Error.Message
		}
	}
	if t := string(err.Type()); t != "" {
		return t
	}
	return fmt.Sprintf("HTTP %d", err.StatusCode)
}

// isNetworkError is the last question asked of an error that named nothing:
// did it ever reach the provider. net.Error covers the timeouts and the
// dials; the phrase table above covers what the SDKs wrap them in.
func isNetworkError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// hasAny reports whether the lowercased message contains any of the phrases.
func hasAny(message string, phrases []string) bool {
	msg := strings.ToLower(message)
	for _, p := range phrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// keyTail is the last four characters of the key that was sent — enough to
// tell the key in the keychain from the one in the environment, never enough
// to be either. A key too short to have four is reported as what it has,
// because a two-character key is itself the finding.
func keyTail(key string) string {
	runes := []rune(strings.TrimSpace(key))
	if len(runes) == 0 {
		return ""
	}
	if len(runes) > 4 {
		runes = runes[len(runes)-4:]
	}
	return string(runes)
}

// bound trims a provider's message to something a transcript can hold.
func bound(message string) string {
	message = strings.TrimSpace(message)
	runes := []rune(message)
	if len(runes) <= maxMessage {
		return message
	}
	return string(runes[:maxMessage]) + "…"
}
