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
	// On the span of a session another one started: what kind of session
	// that parent was. The trace already carries the parenthood — a child's
	// span hangs under its parent's — and this is the half a dashboard
	// groups by without walking a trace to find out whose child a row is.
	// The parent's kind and not its identity: an id would be a row number
	// on one machine's database, which means nothing on the other side of
	// the network.
	AttrParent = "shhh.session.parent"
	// On an event: the row's own four columns and its position.
	AttrTool       = "shhh.tool"
	AttrEventCode  = "shhh.outcome"
	AttrReason     = "shhh.reason"
	AttrDurationMs = "shhh.duration_ms"
	AttrTurn       = "shhh.turn"
	AttrRound      = "shhh.round"
)

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

// exportPause is how long one export failure switches export off for.
//
// It is a pause and not a latch. A collector is restarted, a gateway is
// redeployed, a laptop closes its lid on a train: every one of those is a
// failure that mends itself, and a process that switched export off for good
// would go on for the rest of its life — a coding session lasts hours —
// sending nothing to a collector that came back a minute later, with one
// line in a log nobody is reading to say why the dashboard has a hole in it.
// The number is long enough that a collector that is properly down costs a
// handful of attempts an hour rather than one per session boundary, and
// short enough that the hole is a gap and not the rest of the day.
const exportPause = 5 * time.Minute

// Exporter is the process's connection to a collector: it opens a span per
// session and sends each one when it closes.
//
// A nil Exporter is the answer to "no endpoint is configured", and every
// method below tolerates one, so a caller wires this the way it wires the
// recorder itself — unconditionally, with no branch of its own.
//
// Nothing is queued: a span is sent on the goroutine that ends it, so at any
// moment every span that exists has either arrived or failed. Shutdown is
// still called on the way out of the process, because what is left after the
// last span is a connection, and a client that walks away from one leaves the
// far end waiting out a stream it can only time out.
type Exporter struct {
	tracer   trace.Tracer
	provider *sdktrace.TracerProvider
	sink     *spanSink
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
			AttributeValueLengthLimit: -1,
			AttributeCountLimit:       -1,
			EventCountLimit:           exportEventLimit,
			// A link limit of zero is not "the default" and not "nothing
			// links to anything here": it is the SDK dropping every link a
			// span is given, silently, which is how a relationship added to
			// this file one afternoon would arrive at the collector as a
			// span with nothing attached to it. Unlimited, like the
			// attributes, because links are added by this package and a
			// session has a handful at most.
			LinkCountLimit:              -1,
			AttributePerEventCountLimit: -1,
			AttributePerLinkCountLimit:  -1,
		}),
		// The resource is written out rather than detected. resource.Default
		// and the host and process detectors beside it put the machine's
		// hostname and the command line that started the process on every
		// span — which is a path and a command, on the one connection this
		// package exists to keep free of both.
		sdktrace.WithResource(resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName(ServiceName), semconv.ServiceVersion(version))),
	)
	return &Exporter{tracer: provider.Tracer(ServiceName), provider: provider, sink: sink}, nil
}

// Shutdown flushes what is held and closes the connection. Nothing is held —
// every span was sent as it ended — so this is the close, and the flush is
// asked for anyway because it is the provider's own way of being told the
// process is over: a build that queued spans one day would otherwise lose
// the last of them with nothing failing.
//
// A nil Exporter and a second call are both no-ops, which is what lets the
// caller be a plain defer on the way out.
func (e *Exporter) Shutdown(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if err := e.provider.ForceFlush(ctx); err != nil {
		return err
	}
	return e.provider.Shutdown(ctx)
}

// spanSink is the processor a finished span goes through. It is written here
// rather than taken from the SDK because the SDK's own simple processor
// hands an export failure to a global error handler, and the answer this
// wants is local: pause export and write one line, so a collector that has
// gone away costs one log record per outage and not one per session boundary
// for the rest of the day.
type spanSink struct {
	exp *otlptrace.Exporter
	// pausedUntil is when export resumes, in Unix nanoseconds, or zero while
	// nothing has failed. A moment rather than a flag because a collector
	// that has gone away usually comes back (exportPause).
	pausedUntil atomic.Int64
}

// paused reports whether the sink is sitting out a failure at now.
func (s *spanSink) paused(now time.Time) bool {
	return now.UnixNano() < s.pausedUntil.Load()
}

func (s *spanSink) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (s *spanSink) OnEnd(span sdktrace.ReadOnlySpan) {
	now := time.Now()
	if s.paused(now) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	if err := s.exp.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span}); err != nil {
		// Swapped before the line is written, so two sessions closing at
		// once still leave one record rather than two — and so a collector
		// that stays down writes one line per outage rather than one per
		// attempt: a swap that found a pause already running found a line
		// already written for it.
		if was := s.pausedUntil.Swap(now.Add(exportPause).UnixNano()); was <= now.UnixNano() {
			logs.Logger().Warn("session record not exported", "error", err, "retry_in", exportPause.String())
		}
	}
}

func (s *spanSink) Shutdown(ctx context.Context) error { return s.exp.Shutdown(ctx) }

// ForceFlush has nothing to flush: a span is sent on the goroutine that
// ended it, so by the time anyone could ask, every span that exists has
// either arrived or failed.
func (s *spanSink) ForceFlush(context.Context) error { return nil }

// Session opens the span one session's record hangs off. The span is not
// sent until End, because it is the session — a span that closed at the
// first event would be an event, and the thing a dashboard wants to group
// by is the run.
func (e *Exporter) Session(kind, provider, model string) *SessionSpan {
	if e == nil {
		return nil
	}
	return e.open(context.Background(), kind, provider, model)
}

// Child opens the span a sub-agent's record hangs off, inside its parent's
// trace: the same span every other session gets, started under the parent's
// span context so the two arrive at the collector as one trace rather than as
// unrelated sessions that happen to overlap.
//
// That is the whole of the argument for it. A fan-out is the composition the
// export is least able to describe otherwise — six children, each a span of
// its own, and nothing on the wire to say they were one piece of work — so a
// team reading the export would see less than the same team reading `shhh
// observe`, which has the parent link in the row.
// See docs/capabilities/sessions-and-memory.md#the-record-can-leave-this-machine.
func (e *Exporter) Child(parent *SessionSpan, kind, provider, model string) *SessionSpan {
	if e == nil {
		return nil
	}
	if parent == nil {
		// A child whose parent was not recording is still a session, and it
		// is a root one: there is no span for it to hang under, and hanging
		// it under nothing would be the same span with a broken parent id.
		return e.Session(kind, provider, model)
	}
	parentKind := parent.kind
	child := e.open(trace.ContextWithSpan(context.Background(), parent.span), kind, provider, model)
	child.span.SetAttributes(attribute.String(AttrParent, parentKind))
	return child
}

// open is the one place a session span is started, so a child and a root
// carry the same three attributes and cannot come to differ in what a session
// says about itself.
func (e *Exporter) open(ctx context.Context, kind, provider, model string) *SessionSpan {
	_, span := e.tracer.Start(ctx, SpanSession,
		trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(
		attribute.String(AttrKind, kind),
		attribute.String(AttrProvider, provider),
		attribute.String(AttrModel, model),
	)
	return &SessionSpan{span: span, kind: kind}
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
	// kind is what this session was, kept so a child opened under it can say
	// whose child it is without the caller being asked for the parent's kind
	// a second time and being free to answer differently.
	kind string
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
