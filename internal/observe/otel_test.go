package observe

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/rfizzle/shhh/internal/logs"
)

// collector is a test OTLP receiver: it speaks the one thing the exporter
// says, which is a protobuf POST of resource spans. The request body is
// decoded as TracesData rather than as the collector service's own request
// type, because the two are the same field on the wire and the service type
// drags a gRPC gateway in behind it for no reading this test takes.
type collector struct {
	*httptest.Server
	mu        sync.Mutex
	spans     []*tracepb.Span
	resources []*commonpb.KeyValue
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var data tracepb.TracesData
		if err := proto.Unmarshal(body, &data); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		for _, rs := range data.GetResourceSpans() {
			c.resources = append(c.resources, rs.GetResource().GetAttributes()...)
			for _, ss := range rs.GetScopeSpans() {
				c.spans = append(c.spans, ss.GetSpans()...)
			}
		}
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *collector) received() []*tracepb.Span {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.spans)
}

func (c *collector) resource() []*commonpb.KeyValue {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.resources)
}

// aWholeSession reports one of everything a surface can report, so a test
// that asks what the exporter is capable of saying has seen all of it.
func aWholeSession(s *SessionSpan) {
	s.Usage(2, 1200, 340, 0.0125)
	s.ToolCall(Pos{Turn: 1, Round: 2}, "search", 12*time.Millisecond, OutcomeError, ClassBadArgs)
	s.Decision(Pos{Turn: 1, Round: 2}, DecisionAsk, ReasonSafety)
	s.Signal(Pos{Turn: 1, Round: 3}, SignalCompact, CompactPressure)
	s.Gate("unit", GatePass)
	s.Turn(1, 3, 900*time.Millisecond, TurnDone)
	s.End(SessionCompleted)
}

// aWholeExport is one of everything the exporter can send: a session, and the
// child a fan-out hangs under it. Two spans, because what a child adds to the
// export is the trace it shares with its parent, and a single session can
// never say anything about that.
func aWholeExport(exp *Exporter) {
	parent := exp.Session("code", "openai", "gpt-test")
	child := exp.Child(parent, "researcher", "openai", "gpt-test-small")
	aWholeSession(child)
	aWholeSession(parent)
}

func attrs(kvs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.GetKey()] = kv.GetValue().String()
	}
	return out
}

// A session is one span, and every row the record would keep is one event on
// it. The shape is the whole promise: a reader that groups by the span has
// the session, and a reader that reads the events has the timeline.
func TestExporter_ASessionIsOneSpanAndAnEventPerRow(t *testing.T) {
	c := newCollector(t)
	exp, err := NewExporter(context.Background(), c.URL, "0.0.0-test")
	if err != nil {
		t.Fatalf("build the exporter: %v", err)
	}
	aWholeSession(exp.Session("code", "openai", "gpt-test"))

	spans := c.received()
	if len(spans) != 1 {
		t.Fatalf("a session is one span, got %d", len(spans))
	}
	span := spans[0]
	if span.GetName() != SpanSession {
		t.Errorf("the span is named %q, not %q", span.GetName(), SpanSession)
	}
	// Five rows were reported and five events arrived: the usage report is
	// not among them, because the record keeps it on the session and not in
	// its events.
	var names []string
	for _, e := range span.GetEvents() {
		names = append(names, e.GetName())
	}
	want := []string{EventToolResult, EventDecision, EventSignal, EventSignal, EventClose}
	if !slices.Equal(names, want) {
		t.Fatalf("the events are %v, want %v", names, want)
	}

	on := attrs(span.GetAttributes())
	for key, want := range map[string]string{
		AttrKind: "code", AttrProvider: "openai", AttrModel: "gpt-test",
		AttrOutcome: SessionCompleted,
	} {
		if got := on[key]; !strings.Contains(got, want) {
			t.Errorf("the span's %s is %q, want %q", key, got, want)
		}
	}
	for _, spent := range []string{AttrTurns, AttrTokensIn, AttrTokensOut, AttrCost} {
		if _, ok := on[spent]; !ok {
			t.Errorf("the span does not carry %s", spent)
		}
	}

	// The tool call keeps the two words the record reads off a result, and
	// the position it happened at.
	tool := attrs(span.GetEvents()[0].GetAttributes())
	for key, want := range map[string]string{
		AttrTool: "search", AttrEventCode: OutcomeError, AttrReason: ClassBadArgs,
		AttrTurn: "1", AttrRound: "2",
	} {
		if got := tool[key]; !strings.Contains(got, want) {
			t.Errorf("the tool event's %s is %q, want %q", key, got, want)
		}
	}
	// The gate's suite rides in the tool field, the way the stored row keeps
	// it, and it carries no position at all.
	gate := attrs(span.GetEvents()[3].GetAttributes())
	for key, want := range map[string]string{
		AttrTool: "unit", AttrEventCode: SignalGate, AttrReason: GatePass,
		AttrTurn: "0", AttrRound: "0",
	} {
		if got := gate[key]; !strings.Contains(got, want) {
			t.Errorf("the gate event's %s is %q, want %q", key, got, want)
		}
	}
}

// The resource says what produced the spans and nothing about the machine
// that did. The SDK's own default resource, and the host and process
// detectors beside it, put the hostname and the command line on every span —
// which is a path and a command on the one connection that has neither, and
// a leak no assertion over span attributes would ever see.
func TestExporter_TheResourceNamesTheProductAndNotTheMachine(t *testing.T) {
	c := newCollector(t)
	exp, err := NewExporter(context.Background(), c.URL, "0.0.0-test")
	if err != nil {
		t.Fatalf("build the exporter: %v", err)
	}
	aWholeSession(exp.Session("code", "openai", "gpt-test"))

	on := attrs(c.resource())
	if len(on) != 2 {
		t.Fatalf("the resource carries %d attributes, want the two that name the product: %v", len(on), on)
	}
	for _, key := range []string{"service.name", "service.version"} {
		if _, ok := on[key]; !ok {
			t.Errorf("the resource does not carry %s: %v", key, on)
		}
	}
}

// Nothing reaches a collector under a key the package has not declared, and
// no declared key is dead. The second half is what stops the list from
// becoming a comment: a key that nothing writes any more would let the set
// read as wider than the exporter really is.
func TestExporter_TheAttributeSetIsClosed(t *testing.T) {
	c := newCollector(t)
	exp, err := NewExporter(context.Background(), c.URL, "0.0.0-test")
	if err != nil {
		t.Fatalf("build the exporter: %v", err)
	}
	aWholeExport(exp)

	seen := map[string]bool{}
	for _, span := range c.received() {
		for _, kv := range span.GetAttributes() {
			seen[kv.GetKey()] = true
		}
		for _, e := range span.GetEvents() {
			for _, kv := range e.GetAttributes() {
				seen[kv.GetKey()] = true
			}
		}
	}
	for key := range seen {
		if !slices.Contains(exportAttrs, key) {
			t.Errorf("the collector was sent %q, which the closed set does not name", key)
		}
	}
	for _, key := range exportAttrs {
		if !seen[key] {
			t.Errorf("the closed set names %q and a whole session never writes it", key)
		}
	}
}

// The exporter names no path, prompt or command — not in a key, and not in
// an attribute it could reach for. The source is read rather than the wire,
// because the wire only shows what one session happened to do and the
// question is what the code is able to say at all.
func TestExporter_NamesNothingButItsOwnConstants(t *testing.T) {
	for _, key := range exportAttrs {
		for _, never := range []string{"path", "file", "dir", "prompt", "command", "arg", "text", "content", "message"} {
			if strings.Contains(key, never) {
				t.Errorf("the attribute key %q names %q, which the record has none of", key, never)
			}
		}
	}

	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", "otel.go"), nil, 0)
	if err != nil {
		t.Fatalf("read the exporter: %v", err)
	}
	built := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "attribute" {
			return true
		}
		// Every attribute the exporter can build takes its key from a
		// constant of this package. A literal here would be a key nobody
		// declared, and the test above could never see it unless the one
		// session it runs happened to reach that line.
		built++
		name, ok := call.Args[0].(*ast.Ident)
		if !ok || !strings.HasPrefix(name.Name, "Attr") {
			t.Errorf("attribute.%s is built from something other than an Attr constant", sel.Sel.Name)
		}
		// And a string value is a bare name — an argument the Observer
		// contract handed in, or a code declared in this package. This is
		// the half that keeps the record content-free, because a closed set
		// of keys says nothing about what is written under them:
		// attribute.String(AttrReason, err.Error()) uses a declared key and
		// would put a message, and usually a path, on the wire. Composing a
		// string is what is refused, rather than each of the expressions
		// somebody might compose. Numbers are exempt because a count, a
		// duration and a price have nothing in them to leak.
		if sel.Sel.Name == "String" && len(call.Args) > 1 {
			if _, ok := call.Args[1].(*ast.Ident); !ok {
				t.Error("a string attribute is given a value that is composed rather than passed in")
			}
		}
		return true
	})
	// A walk that matched nothing would pass this test for the wrong reason,
	// which is the failure a source-reading test is most prone to.
	if built < len(exportAttrs) {
		t.Errorf("the walk found %d attributes built and the set names %d", built, len(exportAttrs))
	}
}

// A collector that refuses the connection costs one line and then nothing.
// The session it happened in still ran: every call after the failure returns,
// and the next session's is not a second line in the log.
func TestExporter_ADeadEndpointCostsOneLogRecord(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	path := filepath.Join(t.TempDir(), "shhh.log")
	logs.To(path)
	t.Cleanup(func() { logs.To("") })

	exp, err := NewExporter(context.Background(), url, "0.0.0-test")
	if err != nil {
		t.Fatalf("build the exporter: %v", err)
	}
	aWholeSession(exp.Session("code", "openai", "gpt-test"))
	if exp.exporting() {
		t.Fatal("a refused connection should have switched export off")
	}
	aWholeSession(exp.Session("chat", "openai", "gpt-test"))

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	if n := strings.Count(string(written), "session record not exported"); n != 1 {
		t.Errorf("a dead endpoint wrote %d records, want 1:\n%s", n, written)
	}
}

// A session with no collector behind it reports through the same calls and
// nothing happens. It is what every session on every machine that has not
// set an endpoint does, so it is the path that must not panic.
func TestExporter_NoEndpointIsANoOp(t *testing.T) {
	var none *Exporter
	aWholeExport(none)
	if err := none.Shutdown(context.Background()); err != nil {
		t.Errorf("shutting down nothing: %v", err)
	}
}

// The scheme is never guessed: it decides whether the record crosses the
// network in the clear, and a default would be choosing that for someone.
func TestParseEndpoint(t *testing.T) {
	for _, ok := range []string{"http://localhost:4318", "https://otel.example:4318/v1/traces"} {
		if _, err := ParseEndpoint(ok); err != nil {
			t.Errorf("ParseEndpoint(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "   ", "localhost:4318", "grpc://localhost:4317", "http://", "://x"} {
		if got, err := ParseEndpoint(bad); err == nil {
			t.Errorf("ParseEndpoint(%q) should be refused, got %q", bad, got)
		}
	}
}

// A fan-out arrives as one trace: the children hang under the session that
// spawned them, so a dashboard that opens the parent has the whole piece of
// work. Without this a run with six children is seven unrelated sessions that
// happen to overlap, which is less than the same run says in `shhh observe`,
// where the row carries the parent link.
func TestExporter_AFanOutIsOneTrace(t *testing.T) {
	c := newCollector(t)
	exp, err := NewExporter(context.Background(), c.URL, "0.0.0-test")
	if err != nil {
		t.Fatalf("build the exporter: %v", err)
	}
	parent := exp.Session("code", "openai", "gpt-test")
	child := exp.Child(parent, "researcher", "openai", "gpt-test-small")
	child.End(SessionCompleted)
	parent.End(SessionCompleted)

	spans := c.received()
	if len(spans) != 2 {
		t.Fatalf("a parent and a child are two spans, got %d", len(spans))
	}
	var got, under *tracepb.Span
	for _, span := range spans {
		if attrs(span.GetAttributes())[AttrKind] == `string_value:"researcher"` {
			got = span
		} else {
			under = span
		}
	}
	if got == nil || under == nil {
		t.Fatalf("the two spans are not the parent and the child: %v", spans)
	}
	if !slices.Equal(got.GetTraceId(), under.GetTraceId()) {
		t.Errorf("the child is in trace %x and its parent in %x", got.GetTraceId(), under.GetTraceId())
	}
	if !slices.Equal(got.GetParentSpanId(), under.GetSpanId()) {
		t.Errorf("the child's parent is %x, want the parent span %x", got.GetParentSpanId(), under.GetSpanId())
	}
	// And it says whose child it is without anyone having to walk the trace
	// to find out.
	if on := attrs(got.GetAttributes())[AttrParent]; !strings.Contains(on, "code") {
		t.Errorf("the child's %s is %q, want the parent's kind", AttrParent, on)
	}
	if _, ok := attrs(under.GetAttributes())[AttrParent]; ok {
		t.Errorf("a session nothing spawned carries %s", AttrParent)
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Errorf("shut the exporter down: %v", err)
	}
}

// A child whose parent was never recording is still exported, as a session of
// its own. The alternative is a span with a parent id pointing at nothing,
// which is a broken trace rather than a root one.
func TestExporter_AChildOfNothingIsARootSpan(t *testing.T) {
	c := newCollector(t)
	exp, err := NewExporter(context.Background(), c.URL, "0.0.0-test")
	if err != nil {
		t.Fatalf("build the exporter: %v", err)
	}
	exp.Child(nil, "researcher", "openai", "gpt-test-small").End(SessionCompleted)

	spans := c.received()
	if len(spans) != 1 {
		t.Fatalf("one child, one span, got %d", len(spans))
	}
	if id := spans[0].GetParentSpanId(); len(id) != 0 {
		t.Errorf("the span hangs under %x, and there was nothing to hang it under", id)
	}
}

// A failure is a pause and not a latch. A collector is restarted and a laptop
// closes its lid; a process that had switched export off for good would send
// nothing for the rest of a session that lasts hours, with one line in a log
// nobody is reading to say why the dashboard has a hole in it.
func TestExporter_AFailureIsAPauseAndNotALatch(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	path := filepath.Join(t.TempDir(), "shhh.log")
	logs.To(path)
	t.Cleanup(func() { logs.To("") })

	exp, err := NewExporter(context.Background(), url, "0.0.0-test")
	if err != nil {
		t.Fatalf("build the exporter: %v", err)
	}
	aWholeSession(exp.Session("code", "openai", "gpt-test"))
	if exp.exporting() {
		t.Fatal("a refused connection should have switched export off")
	}
	// The pause runs out, which is the whole of what re-arms it: nothing
	// probes the collector, and the next session that ends is the probe.
	exp.sink.pausedUntil.Store(time.Now().Add(-time.Second).UnixNano())
	if !exp.exporting() {
		t.Fatal("the pause ran out and export is still off")
	}
	aWholeSession(exp.Session("chat", "openai", "gpt-test"))
	if exp.exporting() {
		t.Fatal("the second failure should have paused export again")
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	// One line per outage: the second session failed after the first pause
	// had run out, so it is a second outage and says so.
	if n := strings.Count(string(written), "session record not exported"); n != 2 {
		t.Errorf("two outages wrote %d records, want 2:\n%s", n, written)
	}
}
