package observe

// The record, said out loud. `shhh observe` reads the same rows out of the
// local store; this is the half that hands them to a collector, so a team
// that already has a dashboard can read the session record on it instead of
// on a terminal in one person's checkout.
//
// Nothing new is invented here. A session is a span, a row of the record is
// an event on that span, and every string either side carries is the string
// the store would have held — a fixed identifier, or a code from one of the
// closed sets this package declares. That is the whole argument for why it
// is safe to switch on: the record is content-free by construction, so the
// exporter has nothing to filter, and a filter is exactly the mechanism that
// would be wrong one day without anything failing.
// See docs/capabilities/sessions-and-memory.md#the-record-can-leave-this-machine.

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/rfizzle/shhh/internal/logs"
)

// ServiceName is what the exported spans say produced them, and SpanSession
// is the name every one of them carries. Both are fixed: a span named after
// the session would be a name per session, which is a cardinality explosion
// in every backend that groups by it, and what tells two sessions apart is
// the attributes below.
const (
	ServiceName = "shhh"
	SpanSession = "shhh.session"
)

// The attribute keys. They are the record's own columns under a prefix, and
// they are constants for the same reason the codes are: a key spelled
// two ways is two columns nothing can add up, and the reader doing the
// adding is on the other side of the network where nothing here can fix it.
const (
	// On the span: what the session was.
	AttrKind     = "shhh.session.kind"
	AttrProvider = "shhh.session.provider"
	AttrModel    = "shhh.session.model"
	AttrOutcome  = "shhh.session.outcome"
	// On the span: what it spent, from the last usage report to arrive.
	AttrTurns     = "shhh.session.turns"
	AttrTokensIn  = "shhh.tokens.in"
	AttrTokensOut = "shhh.tokens.out"
	AttrCost      = "shhh.cost.usd"
	// On an event: the row's own four columns and its position.
	AttrTool       = "shhh.tool"
	AttrEventCode  = "shhh.outcome"
	AttrReason     = "shhh.reason"
	AttrDurationMs = "shhh.duration_ms"
	AttrTurn       = "shhh.turn"
	AttrRound      = "shhh.round"
)

// exportAttrs is every key the exporter can write, in one place so the set
// is a thing a test can hold rather than a claim a comment makes. A key that
// is added to the code and not to this list fails the test beside it, which
// is the point: the set is closed on purpose, and the way to widen it is to
// argue for the new key rather than to reach for attribute.String.
var exportAttrs = []string{
	AttrKind, AttrProvider, AttrModel, AttrOutcome,
	AttrTurns, AttrTokensIn, AttrTokensOut, AttrCost,
	AttrTool, AttrEventCode, AttrReason, AttrDurationMs, AttrTurn, AttrRound,
}

// exportTimeout bounds the one round trip a session's span costs, and it is
// the whole of what a slow collector can take from a session: the span is
// sent when the row closes, and a caller that closes a row somewhere a wait
// would be felt hands the send to a goroutine instead
// (internal/cli/observe.go). A refused connection returns long before this;
// a collector that answers with an error costs it once and then nothing,
// because the failure switches export off. The number is what a person would
// not notice as a program exits, and further than a collector on the same
// machine ever comes.
const exportTimeout = 2 * time.Second

// exportEventLimit is how many events one session's span may carry. The span
// is held in memory until the session ends and exported in one piece, so the
// limit is a memory ceiling and not a policy: at a few hundred bytes an
// event this is single-digit megabytes at the very top, and a session that
// runs past it is one that has already spent thousands of tool rounds. Past
// the limit the oldest events are dropped and the count of them rides on the
// exported span, so a truncated session reads as truncated rather than as
// short.
const exportEventLimit = 8192

// Exporter is the process's connection to a collector: it opens a span per
// session and sends each one when it closes.
//
// A nil Exporter is the answer to "no endpoint is configured", and every
// method below tolerates one, so a caller wires this the way it wires the
// recorder itself — unconditionally, with no branch of its own.
//
// There is deliberately nothing to shut down. A span is sent on the
// goroutine that ends it rather than queued, so at any moment every span
// that exists has either arrived or failed, and a process that stops holds
// nothing anyone would want flushed.
type Exporter struct {
	tracer trace.Tracer
	sink   *spanSink
}

// ParseEndpoint reads the configured endpoint into the URL the exporter
// sends to, or says why it cannot. A host with no scheme is refused rather
// than guessed at: http and https are a plaintext and an encrypted
// connection to somebody else's machine, and picking one on the user's
// behalf is picking whether the record crosses the network in the clear.
//
// The path is left as written, because a collector behind a gateway is often
// mounted somewhere other than the root; an endpoint with no path at all
// gets the OTLP default when the exporter is built.
func ParseEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("no endpoint")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("the endpoint is not a URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("the endpoint needs an http:// or https:// scheme")
	}
	if u.Host == "" {
		return "", errors.New("the endpoint names no host")
	}
	return u.String(), nil
}

// NewExporter builds the exporter for one endpoint. It reaches nothing: the
// first connection is made by the first session that closes, so a collector
// that is down costs a session nothing at the point it starts.
func NewExporter(ctx context.Context, endpoint, version string) (*Exporter, error) {
	target, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	client, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(target),
		otlptracehttp.WithTimeout(exportTimeout),
		// Retry off, and this is the load-bearing option. The default waits
		// out a failing collector for a minute with backoff, on the
		// goroutine that ended the span — which here is the one closing the
		// session, so a collector nobody is watching would hold a person's
		// terminal shut. One attempt, then the failure switches export off.
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{}),
	)
	if err != nil {
		return nil, err
	}
	sink := &spanSink{exp: client}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sink),
		// Stated rather than defaulted, both of them: the sampler and the
		// limits are otherwise read from OTEL_ environment variables, and a
		// record that is on because a config key says so should not be
		// silently sampled away by a variable exported for something else.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithRawSpanLimits(sdktrace.SpanLimits{
			AttributeValueLengthLimit:   -1,
			AttributeCountLimit:         -1,
			EventCountLimit:             exportEventLimit,
			LinkCountLimit:              0,
			AttributePerEventCountLimit: -1,
			AttributePerLinkCountLimit:  0,
		}),
		// The resource is written out rather than detected. resource.Default
		// and the host and process detectors beside it put the machine's
		// hostname and the command line that started the process on every
		// span — which is a path and a command, on the one connection this
		// package exists to keep free of both.
		sdktrace.WithResource(resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName(ServiceName), semconv.ServiceVersion(version))),
	)
	return &Exporter{tracer: provider.Tracer(ServiceName), sink: sink}, nil
}

// spanSink is the processor a finished span goes through. It is written here
// rather than taken from the SDK because the SDK's own simple processor
// hands an export failure to a global error handler, and the answer this
// wants is local: switch export off for the rest of the process and write
// one line, so a collector that has gone away costs one log record and not
// one per session boundary for the rest of the day.
type spanSink struct {
	exp *otlptrace.Exporter
	off atomic.Bool
}

func (s *spanSink) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (s *spanSink) OnEnd(span sdktrace.ReadOnlySpan) {
	if s.off.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	if err := s.exp.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span}); err != nil {
		// Swapped before the line is written, so two sessions closing at
		// once still leave one record rather than two.
		if !s.off.Swap(true) {
			logs.Logger().Warn("session record not exported", "error", err)
		}
	}
}

func (s *spanSink) Shutdown(ctx context.Context) error { return s.exp.Shutdown(ctx) }

// ForceFlush has nothing to flush: a span is sent on the goroutine that
// ended it, so by the time anyone could ask, every span that exists has
// either arrived or failed.
func (s *spanSink) ForceFlush(context.Context) error { return nil }

// exporting reports whether anything would still be sent, which is what a
// test asserts on and what says a failure has switched this off.
func (e *Exporter) exporting() bool { return e != nil && !e.sink.off.Load() }

// Session opens the span one session's record hangs off. The span is not
// sent until End, because it is the session — a span that closed at the
// first event would be an event, and the thing a dashboard wants to group
// by is the run.
func (e *Exporter) Session(kind, provider, model string) *SessionSpan {
	if e == nil {
		return nil
	}
	_, span := e.tracer.Start(context.Background(), SpanSession,
		trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(
		attribute.String(AttrKind, kind),
		attribute.String(AttrProvider, provider),
		attribute.String(AttrModel, model),
	)
	return &SessionSpan{span: span}
}

// SessionSpan is one session's span, with a method per callback the Observer
// contract has. The methods are the contract's own shapes on purpose: a
// recorder that persists a row and exports it should be reporting the same
// arguments to both, and a signature that differed would be a place for the
// two records to drift apart.
//
// A nil SessionSpan is what a session with no collector holds, and every
// method is a no-op on one.
type SessionSpan struct {
	span trace.Span
}

// ToolCall exports one executed tool call.
func (s *SessionSpan) ToolCall(at Pos, tool string, duration time.Duration, outcome, class string) {
	s.event(EventToolResult, at,
		attribute.String(AttrTool, tool),
		attribute.String(AttrEventCode, outcome),
		attribute.String(AttrReason, class),
		attribute.Int64(AttrDurationMs, duration.Milliseconds()),
	)
}

// Decision exports one mode-policy verdict.
func (s *SessionSpan) Decision(at Pos, decision, reason string) {
	s.event(EventDecision, at,
		attribute.String(AttrEventCode, decision),
		attribute.String(AttrReason, reason),
	)
}

// Turn exports a turn closing. The rounds it took ride in the position's
// round, which is where the stored row keeps them too.
func (s *SessionSpan) Turn(turn, rounds int64, duration time.Duration, outcome string) {
	s.event(EventClose, Pos{Turn: turn, Round: rounds},
		attribute.String(AttrEventCode, outcome),
		attribute.Int64(AttrDurationMs, duration.Milliseconds()),
	)
}

// Signal exports one of the loop's own safeguards firing.
func (s *SessionSpan) Signal(at Pos, code, reason string) {
	s.event(EventSignal, at,
		attribute.String(AttrEventCode, code),
		attribute.String(AttrReason, reason),
	)
}

// Gate exports one quality-gate run, in the shape the stored row takes: the
// suite in the tool field, because it is what the verdict is a verdict of,
// and no position, because a gate run has none.
func (s *SessionSpan) Gate(suite, verdict string) {
	s.event(EventSignal, Pos{},
		attribute.String(AttrTool, suite),
		attribute.String(AttrEventCode, SignalGate),
		attribute.String(AttrReason, verdict),
	)
}

// Usage sets what the session has spent so far. It is attributes on the span
// and not an event, because the record keeps it on the session row and not
// in its events: a usage report is a running total restated, and one event
// per restatement would be a timeline of the same number.
func (s *SessionSpan) Usage(turns, tokensIn, tokensOut int64, cost float64) {
	if s == nil {
		return
	}
	s.span.SetAttributes(
		attribute.Int64(AttrTurns, turns),
		attribute.Int64(AttrTokensIn, tokensIn),
		attribute.Int64(AttrTokensOut, tokensOut),
		attribute.Float64(AttrCost, cost),
	)
}

// End closes the span and sends it. The outcome is the session's, from the
// closed set the record keeps, and it is also the span's status: a dashboard
// that colours a failed trace red should agree with the row that says the
// session errored.
func (s *SessionSpan) End(outcome string) {
	if s == nil {
		return
	}
	s.span.SetAttributes(attribute.String(AttrOutcome, outcome))
	// No description beside the code. It is the one field of a span that
	// takes prose, and prose is the thing this record does not have.
	if outcome == SessionError {
		s.span.SetStatus(codes.Error, "")
	} else {
		s.span.SetStatus(codes.Ok, "")
	}
	s.span.End()
}

// event is the one place an event is added, so the position lands in the
// same two attributes every time and there is one function to read when the
// question is what the exporter can say.
func (s *SessionSpan) event(name string, at Pos, attrs ...attribute.KeyValue) {
	if s == nil {
		return
	}
	s.span.AddEvent(name, trace.WithAttributes(append(attrs,
		attribute.Int64(AttrTurn, at.Turn),
		attribute.Int64(AttrRound, at.Round),
	)...))
}
