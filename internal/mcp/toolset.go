package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	calldescribe "github.com/rfizzle/shhh/internal/receipt/describe"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// Status is what became of one definition when the session tried to use it.
type Status string

const (
	// StatusConnected: the server answered and its tools are registered.
	StatusConnected Status = "connected"
	// StatusFailed: it did not start, or did not answer in time.
	StatusFailed Status = "failed"
	// StatusDisabled: the definition says not to start it.
	StatusDisabled Status = "disabled"
	// StatusUntrusted: a project server the person has not trusted yet.
	StatusUntrusted Status = "untrusted"
	// StatusMissingEnv: it references an environment variable that is unset.
	StatusMissingEnv Status = "missing-env"
	// StatusExcluded: the session's kind does not admit it — a conversation
	// takes only servers marked read-only.
	StatusExcluded Status = "excluded"
	// StatusStarting: its connect is still running. A session that does not
	// wait for its servers opens with every admitted one in this state, and
	// each turns connected or failed the moment its own connect ends
	// (docs/capabilities/mcp.md#a-server-that-did-not-answer-is-a-row).
	StatusStarting Status = "starting"
)

// Report is one definition's outcome, for listings.
type Report struct {
	Definition Definition
	Status     Status
	// Server is set when Status is StatusConnected.
	Server *Server
	// Error is the failure, Missing the unset variables, Took the time to
	// connect and list.
	Error   string
	Missing []string
	Took    time.Duration
	// TimedOut is a failure that was the startup wait running out rather
	// than the server refusing, which the record files under a word of its
	// own because the two are fixed differently.
	TimedOut bool
	// Withheld names the inherited variables the mask kept out of a stdio
	// server's environment. It is on the report rather than on the server
	// because the reader who needs it most is looking at one that would not
	// start (docs/capabilities/mcp.md#a-server-sees-the-masked-environment).
	Withheld []string
	// Began is when the connect started, which is what a row still starting
	// counts its seconds from; Bound is how long it has to answer, and
	// BoundBySession says the session's own startup timeout set it rather
	// than the definition, which decides which key a reader is pointed at.
	Began          time.Time
	Bound          time.Duration
	BoundBySession bool
}

// ProjectTrust is the person's answer about the checkout a project server
// was defined in: whether what it declares may load at all. An edit to the
// definition does not take the answer away — trust is about the checkout,
// and the session says once what changed
// (docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs).
// It is a value the session reads from its own store before the first dial;
// nothing in a checkout can set it, and the zero value — nobody asked —
// starts nothing.
//
// A server is not trusted by name any more. Every kind of thing a checkout
// can name runs as whoever cloned it, so the question is asked once about
// the checkout rather than five times about five files
// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
type ProjectTrust struct {
	// Granted is the person's answer for the checkout.
	Granted bool
}

// Options shape a connect.
type Options struct {
	// Project decides project-scope servers; the zero value admits none.
	Project ProjectTrust
	// ReadOnlyOnly admits only servers the person marked read-only — the
	// conversation's rule, since a chat has nothing to ask with
	// (docs/capabilities/mcp.md#what-a-conversation-may-reach).
	ReadOnlyOnly bool
	// Lookup resolves environment references; nil means the process
	// environment.
	Lookup func(string) (string, bool)
	// EnvMask is the test an inherited variable's name is put to before a
	// stdio server is started with it: true withholds it. nil hands the
	// process environment over whole. The session resolves it from its
	// configuration and this package is told, the way the runner is
	// (docs/capabilities/mcp.md#a-server-sees-the-masked-environment).
	EnvMask func(name string) bool
	// Timeout overrides every definition's startup timeout when set.
	Timeout time.Duration
	// CallTimeout is the session's own bound on one request to a server,
	// filled in where a definition did not name its own. It fills in rather
	// than overriding, unlike Timeout: a call timeout written against one
	// server is a person saying that server is slow, and a session-wide
	// number is what the rest of them get.
	CallTimeout time.Duration
	// Observe is handed each definition's report as its connect settles —
	// one that connected, failed or timed out, and one that was never
	// started — from the goroutine that settled it, so it must be safe for
	// concurrent use. It is how the time a connect took reaches the
	// session's record (docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed).
	// nil observes nothing.
	Observe func(Report)
	// Dial reaches one server; nil is Dial. It is the seam a test holds a
	// connect open on, so a server that has not answered yet is a state a
	// test stands in rather than a race it has to win.
	Dial func(ctx context.Context, def Definition, mask func(name string) bool) (*Server, error)
}

// Toolset is the session's connected servers and what they offer,
// addressed by the names the model calls and the commands the person types.
//
// Everything derived from a server's catalog is rebuilt together under one
// lock, because a list-changed notification can arrive at any moment and a
// table half rebuilt would answer Has for a tool Execute can no longer find.
//
// The reports are under the same lock, because a connect started in the
// background writes its report from its own goroutine while the rail and
// the listings read them: Reports hands a reader a copy.
type Toolset struct {
	mu sync.Mutex
	// reports is one per definition, in catalog order. A starting one is
	// rewritten the moment its connect ends, so every surface that reads it
	// says up or error at once; what the model was told waits for Join.
	reports []Report
	// settled are the reports whose connect ended and that no boundary has
	// taken yet, by index. Join drains them: a server's tools reach the
	// tables below only there, so a request is never built from a list a
	// connect is halfway through joining.
	settled []int
	// connects is the connects still running, for Wait.
	connects sync.WaitGroup
	// closed says the session has ended: a connect that lands after it
	// closes its own server rather than leaving a process nobody owns.
	closed bool

	servers   map[string]*Server
	tools     map[string]toolRef
	prompts   map[string]promptRef
	resources map[string]*Server
	defs      []provider.Tool
	// offered is defs as the last Join left it (Offered).
	offered []provider.Tool
	// inflight counts the calls dispatched and not yet returned. A refresh
	// waits for it to reach zero, which is what makes the swap a round
	// boundary rather than something that happens under a round's own calls
	// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	inflight int
	// calls are the cancels of the requests in flight, keyed by a counter
	// so a call that has returned takes its own entry out and never a
	// later call's. It is what AbandonCalls reaches: a request is made off
	// the session's context, since a tool executor is handed a name and
	// arguments and no context at all, so the interrupt has nothing else to
	// pull (docs/capabilities/mcp.md#a-call-that-hangs-can-be-given-up).
	calls    map[int]context.CancelFunc
	nextCall int
	// deaths are the servers that stopped answering, taken at a refresh and
	// drained by the surface that says so.
	deaths []Death
	// ledger is the session's sources ledger, installed by UseLedger; nil is
	// a session that keeps none.
	ledger *web.Ledger
}

// UseLedger points the toolset at the session's sources ledger, so a page a
// server's tool read is a row of its own kind beside the fetcher's. The row
// is filed from the result the server returned and never from anything the
// model said about it (docs/capabilities/chat.md#what-was-read).
func (ts *Toolset) UseLedger(l *web.Ledger) {
	ts.mu.Lock()
	ts.ledger = l
	ts.mu.Unlock()
}

type toolRef struct {
	server *Server
	tool   Tool
}

type promptRef struct {
	server *Server
	prompt Prompt
}

// Connect tries every definition in the catalog at once, waits for every
// connect to end, and returns the toolset with a report per definition and
// every server that answered joined. Nothing here is an error: a server
// that did not connect is a report the listing shows and a tool the
// session does not have, the same way a language server that was not found
// is. It is Start for a surface nobody watches, where a first round without
// the tools is a worse answer nobody can see was worse.
func Connect(ctx context.Context, c *Catalog, opts Options) *Toolset {
	ts := Start(ctx, c, opts)
	ts.Wait()
	ts.Join()
	return ts
}

// Start begins a connect for every definition the session admits and
// returns at once, with each of them starting. Every server connects
// concurrently because the slow case — a cold `npx` cache — is per server
// and a session should not pay it in series; and none of them is waited
// on, because a session that opened only when its slowest server answered
// made every prompt wait on somebody else's uptime. A connect that ends
// turns its report then and there, and its tools wait in the queue Join
// takes at a turn boundary
// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
func Start(ctx context.Context, c *Catalog, opts Options) *Toolset {
	ts := &Toolset{servers: map[string]*Server{}}
	ts.index()
	if c == nil {
		return ts
	}
	ts.reports = make([]Report, len(c.Servers))
	var starting []int
	began := time.Now()
	for i, def := range c.Servers {
		ts.reports[i] = Report{Definition: def}
		if status, missing := admit(def, opts); status != "" {
			ts.reports[i].Status = status
			ts.reports[i].Missing = missing
			opts.observe(ts.reports[i])
			continue
		}
		bound, bySession := startupBound(def, opts)
		ts.reports[i] = Report{Definition: def, Status: StatusStarting,
			Began: began, Bound: bound, BoundBySession: bySession}
		starting = append(starting, i)
	}
	// Every report is written before the first connect is let go, so a
	// connect that ends at once never races the loop that set them up.
	ts.connects.Add(len(starting))
	for _, i := range starting {
		go func(i int, def Definition) {
			defer ts.connects.Done()
			ts.settle(i, connectOne(ctx, def, opts))
		}(i, c.Servers[i])
	}
	return ts
}

// startupBound is how long a definition has to answer, and whether the
// session's own timeout is what set it.
func startupBound(def Definition, opts Options) (time.Duration, bool) {
	if opts.Timeout > 0 {
		return opts.Timeout, true
	}
	return def.StartupTimeout(), false
}

// settle turns a starting report into what its connect came to and queues
// it for the next boundary. The row reads the new state from this moment;
// the tools do not, until Join.
func (ts *Toolset) settle(i int, r Report) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	was := ts.reports[i]
	r.Began, r.Bound, r.BoundBySession = was.Began, was.Bound, was.BoundBySession
	ts.reports[i] = r
	if ts.closed {
		// The session ended while this one was still connecting: nobody
		// will join it, so it is closed where it landed.
		r.Server.Close()
		return
	}
	ts.settled = append(ts.settled, i)
}

// Wait blocks until every connect Start began has ended. It is what a
// surface that cannot show a row starting does before its first round.
func (ts *Toolset) Wait() {
	if ts == nil {
		return
	}
	ts.connects.Wait()
}

// Join takes every connect that ended since the last boundary: a server
// that answered has its tools indexed — the provider's list, the prompt
// block, the commands and the resources all move together here — and one
// that did not is handed back so the session can say so. It returns the
// reports it took, in catalog order, and nothing while a call is in flight,
// for Refresh's reason: a table rebuilt under a round's own calls would
// change what a result answers. The caller decides the boundary, which is a
// turn's and never a round's
// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
func (ts *Toolset) Join() []Report {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.inflight > 0 || len(ts.settled) == 0 {
		return nil
	}
	sort.Ints(ts.settled)
	out := make([]Report, 0, len(ts.settled))
	joined := false
	for _, i := range ts.settled {
		r := ts.reports[i]
		if why := r.Server.Dead(); r.Status == StatusConnected && why != "" {
			// The server answered and stopped answering before any boundary
			// took it. Refresh looks at joined servers only, so nothing else
			// would ever say so: it is a server that did not start, and its
			// tools are never offered
			// (docs/capabilities/mcp.md#a-server-that-dies-is-noticed).
			r.Server.Close()
			r.Status, r.Error, r.Server = StatusFailed, why, nil
			ts.reports[i] = r
		}
		if r.Status == StatusConnected {
			ts.servers[r.Definition.Name] = r.Server
			joined = true
		}
		out = append(out, r)
	}
	ts.settled = nil
	if joined {
		ts.index()
		ts.offered = append([]provider.Tool(nil), ts.defs...)
	}
	return out
}

// Offered is the tool list as the last join left it: what a request carries
// in a session whose servers join at a boundary. It is not Definitions,
// which a list-changed Refresh rebuilds at any round boundary — a server's
// re-listing moves what is read when it is used, never what the model was
// offered (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
func (ts *Toolset) Offered() []provider.Tool {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]provider.Tool(nil), ts.offered...)
}

// FromReports is a toolset over reports that have already settled, every
// connected server among them joined: a reading of connects made somewhere
// else, which is what a test of a listing holds.
func FromReports(reports []Report) *Toolset {
	ts := &Toolset{servers: map[string]*Server{}, reports: append([]Report(nil), reports...)}
	for _, r := range reports {
		if r.Status == StatusConnected && r.Server != nil {
			ts.servers[r.Definition.Name] = r.Server
		}
	}
	ts.index()
	ts.offered = append([]provider.Tool(nil), ts.defs...)
	return ts
}

// Reports is one report per definition, in catalog order, as the connects
// stand now. It is a copy: a connect still running rewrites its own report
// when it ends, and a reader holding the toolset's slice would be reading
// it while that happened.
func (ts *Toolset) Reports() []Report {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]Report(nil), ts.reports...)
}

// index rebuilds every table the session reads from the servers' catalogs.
// It is one function and not four because the tables have to agree: a
// prompt row pointing at a server whose tool table was not rebuilt is a
// command that answers with a tool the model was never offered. Callers
// hold ts.mu, except Start, where nothing else can see the toolset yet.
func (ts *Toolset) index() {
	ts.tools = map[string]toolRef{}
	ts.prompts = map[string]promptRef{}
	ts.resources = map[string]*Server{}
	ts.defs = nil
	taken := map[string]bool{}
	for _, s := range ts.sorted() {
		// A definition that named its tools is filtered here and nowhere
		// else, so every table built below holds the same set: Gated, the
		// provider's definitions, a call's dispatch and an approval card's
		// preview all read what this loop wrote, and a tool left out of the
		// session cannot be reached by name from any of them
		// (docs/capabilities/mcp.md#a-large-server-is-taken-in-part).
		for _, t := range s.RegisteredTools() {
			if taken[t.Name] {
				continue
			}
			taken[t.Name] = true
			ts.tools[t.Name] = toolRef{server: s, tool: t}
			ts.defs = append(ts.defs, provider.Tool{Name: t.Name, Description: describe(s, t), Parameters: t.InputSchema})
		}
		for _, p := range s.Prompts {
			if _, dup := ts.prompts[p.Name]; dup {
				continue
			}
			ts.prompts[p.Name] = promptRef{server: s, prompt: p}
		}
		for _, r := range s.Resources {
			if _, dup := ts.resources[r.URI]; dup {
				continue
			}
			ts.resources[r.URI] = s
		}
	}
	sort.Slice(ts.defs, func(i, j int) bool { return ts.defs[i].Name < ts.defs[j].Name })
	// The one tool every server's resources are read through joins last, so
	// it sorts among the server tools rather than ahead of the first of
	// them, and only when something published a resource: a tool whose whole
	// catalog is empty is a round the model spends finding that out.
	if len(ts.resources) > 0 {
		ts.defs = append(ts.defs, ResourceDefinition())
	}
}

// sorted is the connected servers by name, without the lock Servers takes.
func (ts *Toolset) sorted() []*Server {
	out := make([]*Server, 0, len(ts.servers))
	for _, s := range ts.servers {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Definition.Name < out[j].Definition.Name })
	return out
}

// Refresh takes whatever re-listing the servers have prepared since the last
// one and rebuilds the session's tables from it, reporting whether anything
// moved. It does nothing while a call is in flight: a catalog swapped under
// a round would change what a result belongs to halfway through it, so the
// notification waits for the boundary the caller decides
// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
//
// What it does not change is anything the model was already told: the tool
// list and the prompt block naming the resources are said for a server once,
// when it joins, and a tool or a uri the model was never told about is one
// it will not ask for. What moves here is what is read at the
// moment it is used — the commands the person can type, the listings, and
// the table a uri is resolved against.
func (ts *Toolset) Refresh() bool {
	if ts == nil {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.inflight > 0 {
		return false
	}
	moved := false
	for _, s := range ts.servers {
		if s.takePending() {
			moved = true
		}
		// A server that stopped answering moves the toolset as surely as a
		// re-listing does: the rail draws it as a failure from here on, and
		// the session owes the reader a line saying its tools have gone. Its
		// tools stay registered — the model was told about them, and a name
		// that vanished would fall off the executor chain and come back as
		// "unknown tool" instead of the sentence that says what happened
		// (docs/capabilities/mcp.md#a-server-that-dies-is-noticed).
		if d, died := s.takeDeath(); died {
			ts.deaths = append(ts.deaths, d)
			moved = true
		}
	}
	if !moved {
		return false
	}
	ts.index()
	return true
}

// Deaths drains the servers that stopped answering since the last call,
// which Refresh took at the boundary before this one. Draining is what
// makes the note appear once: a death is news at the boundary after it
// happened and wallpaper at every boundary after that.
func (ts *Toolset) Deaths() []Death {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	out := ts.deaths
	ts.deaths = nil
	return out
}

// AbandonCalls cancels every request in flight. It is the turn's cancel
// reaching the one part of a server call the turn's own context cannot: a
// tool executor is handed a name and arguments, so a call runs on a context
// this package made, and without this a person who pressed the interrupt
// waits out the whole call timeout on a server that will never answer
// (docs/capabilities/mcp.md#a-call-that-hangs-can-be-given-up).
//
// It does not mark anything dead. A call the session gave up on says
// nothing about whether the server is still there, and the next one finds
// out the ordinary way.
func (ts *Toolset) AbandonCalls() {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(ts.calls))
	for _, cancel := range ts.calls {
		cancels = append(cancels, cancel)
	}
	ts.mu.Unlock()
	// Outside the lock: each cancel wakes the call's own goroutine, which
	// takes this lock on its way out through end.
	for _, cancel := range cancels {
		cancel()
	}
}

// admit decides whether a definition is tried at all, and with what
// status it is left out.
func admit(def Definition, opts Options) (Status, []string) {
	if def.Disabled {
		return StatusDisabled, nil
	}
	if opts.ReadOnlyOnly && !def.ReadOnly {
		return StatusExcluded, nil
	}
	if def.Scope == ScopeProject {
		if !opts.Project.Granted {
			return StatusUntrusted, nil
		}
	}
	if _, missing := def.Expand(opts.Lookup); len(missing) > 0 {
		return StatusMissingEnv, missing
	}
	return "", nil
}

func connectOne(ctx context.Context, def Definition, opts Options) Report {
	r := Report{Definition: def, Withheld: WithheldEnv(def, opts.EnvMask)}
	expanded, _ := def.Expand(opts.Lookup)
	timeout := def.StartupTimeout()
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}
	dial := opts.Dial
	if dial == nil {
		dial = Dial
	}
	started := time.Now()
	// The dial runs on a context that outlives this call — the session's,
	// shorn of its cancellation would be wrong too, since a session that
	// ends must end its servers — and the deadline is a wait beside it. A
	// dial that finishes after the wait gave up is closed where it lands.
	type dialed struct {
		s   *Server
		err error
	}
	done := make(chan dialed, 1)
	go func() {
		s, err := dial(ctx, expanded, opts.EnvMask)
		done <- dialed{s, err}
	}()
	var (
		s        *Server
		err      error
		timedOut bool
	)
	select {
	case d := <-done:
		s, err = d.s, d.err
	case <-time.After(timeout):
		timedOut = true
		err = fmt.Errorf("server %s: no answer within %s", def.Name, timeout)
		go func() {
			if d := <-done; d.s != nil {
				d.s.Close()
			}
		}()
	}
	r.Took = time.Since(started)
	// Whatever came of it, the figure goes to the record as well as to the
	// listing: a slow server is time before the first paint, and without
	// this it was time no row anywhere held.
	defer func() { opts.observe(r) }()
	if err != nil {
		r.Status = StatusFailed
		r.Error = err.Error()
		r.TimedOut = timedOut
		if ctx.Err() != nil {
			// The session went away mid-dial: that is not the server's
			// fault, and the row should not say it was.
			r.Error = fmt.Sprintf("server %s: the session ended before it answered", def.Name)
			r.TimedOut = false
		}
		return r
	}
	// The session's bound fills in for a definition that named none, here
	// rather than at each dispatch: the definition on the server is what
	// every later call reads, and a resolution done twice is two places to
	// disagree about how long a server is allowed.
	if def.CallTimeout == 0 {
		def.CallTimeout = opts.CallTimeout
	}
	// The unexpanded definition is what the report shows: the listing must
	// never print a token that an environment reference stood in for.
	s.Definition = def
	r.Status = StatusConnected
	r.Server = s
	return r
}

// observe hands a settled report to the session's observer, if it has one.
func (o Options) observe(r Report) {
	if o.Observe != nil {
		o.Observe(r)
	}
}

// describe is the tool description the model reads: the server's own, led
// by where the tool comes from, so a model choosing between a local search
// and a remote one knows which is which.
func describe(s *Server, t Tool) string {
	head := "[" + s.Definition.Name + "]"
	if t.Title != "" && t.Title != t.Remote {
		head += " " + t.Title + "."
	}
	if t.Description == "" {
		return head
	}
	return head + " " + t.Description
}

// Definitions are the registered tools, for the provider.
func (ts *Toolset) Definitions() []provider.Tool {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]provider.Tool(nil), ts.defs...)
}

// Len is how many tools are registered.
func (ts *Toolset) Len() int {
	if ts == nil {
		return 0
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.defs)
}

// Has reports whether name is one of this toolset's tools. The resource
// tool counts wherever a server published one: it is a server's tool, drawn
// and counted as one, even though no single server owns it.
func (ts *Toolset) Has(name string) bool {
	if ts == nil {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if name == ResourceToolName {
		return len(ts.resources) > 0
	}
	_, ok := ts.tools[name]
	return ok
}

// ReadOnly reports whether name runs without an answer: a tool of a server
// the person marked read-only, or a resource read.
//
// A resource read is a read whatever the server is. It returns what the
// server holds and changes nothing, so it is tiered the way a file read is
// — and no annotation of the server's can promote it, because nothing a
// server says about itself decides anything here
// (docs/capabilities/mcp.md#a-resource-is-a-read).
func (ts *Toolset) ReadOnly(name string) bool {
	if ts == nil {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if name == ResourceToolName {
		return len(ts.resources) > 0
	}
	ref, ok := ts.tools[name]
	return ok && ref.server.Definition.ReadOnly
}

// Gated is every registered tool that needs an answer before it runs:
// the tools of every server not marked read-only. The resource tool is
// never one of them.
func (ts *Toolset) Gated() []string {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var out []string
	for name, ref := range ts.tools {
		if !ref.server.Definition.ReadOnly {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ReadOnlyDefinitions are the tools of the servers marked read-only — what
// a child agent, which has no card to ask on, is handed. The resource tool
// joins them when a read-only server published a resource; the chain below
// keeps it to those servers, because what a child was handed is the
// read-only servers and nothing else
// (docs/capabilities/mcp.md#what-a-conversation-may-reach).
func (ts *Toolset) ReadOnlyDefinitions() []provider.Tool {
	defs, _ := ts.ReadOnlyView()
	return defs
}

// ReadOnlyView is the read-only servers' tools and the prompt block naming
// those servers, read in one hold of the lock: a server that joined between
// two reads would be named to a child without its tools, or handed over
// unnamed. A child reads the servers that have joined when it is spawned and
// keeps that set (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
func (ts *Toolset) ReadOnlyView() ([]provider.Tool, string) {
	if ts == nil {
		return nil, ""
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var out []provider.Tool
	for _, d := range ts.defs {
		if d.Name == ResourceToolName {
			if ts.hasReadOnlyResource() {
				out = append(out, d)
			}
			continue
		}
		if ref, ok := ts.tools[d.Name]; ok && ref.server.Definition.ReadOnly {
			out = append(out, d)
		}
	}
	var servers []*Server
	for _, s := range ts.sorted() {
		if s.Definition.ReadOnly {
			servers = append(servers, s)
		}
	}
	return out, promptBlock(servers)
}

// hasReadOnlyResource reports whether a read-only server published a
// resource. Callers hold ts.mu.
func (ts *Toolset) hasReadOnlyResource() bool {
	for _, s := range ts.resources {
		if s.Definition.ReadOnly {
			return true
		}
	}
	return false
}

// Lookup returns the server and tool behind a session name.
func (ts *Toolset) Lookup(name string) (*Server, Tool, bool) {
	if ts == nil {
		return nil, Tool{}, false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ref, ok := ts.tools[name]
	if !ok {
		return nil, Tool{}, false
	}
	return ref.server, ref.tool, true
}

// Prompts are the commands the connected servers publish, in catalog order.
// They are read live rather than snapshotted because a server may add one
// mid-session and the menu that offers them is drawn per keystroke.
func (ts *Toolset) Prompts() []Prompt {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	out := make([]Prompt, 0, len(ts.prompts))
	for _, s := range ts.sorted() {
		for _, p := range s.Prompts {
			if ref, ok := ts.prompts[p.Name]; ok && ref.server == s {
				out = append(out, p)
			}
		}
	}
	return out
}

// Render fetches one prompt's messages, filled in with args, as the text a
// user turn is started on. An unknown name is an error and not an empty
// turn: the command was typed, so the person is owed the reason.
func (ts *Toolset) Render(ctx context.Context, name string, args map[string]string) (string, error) {
	if ts == nil {
		return "", fmt.Errorf("no MCP servers in this session")
	}
	ts.mu.Lock()
	ref, ok := ts.prompts[name]
	ts.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no prompt named %s; `shhh mcp` lists what the servers offer", name)
	}
	return ref.server.Render(ctx, ref.prompt, args)
}

// Servers are the connected servers, by name.
func (ts *Toolset) Servers() []*Server {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.sorted()
}

// MaxResultBytes caps what one call may feed the model. It is the file
// read's cap: a remote result is a read like any other, and the evidence
// store reduces it further on the same terms.
const MaxResultBytes = tools.MaxReadFileBytes

// ResourceToolName is the one tool every server's resources are read
// through. There is one rather than one per server because a resource is
// addressed by URI and a URI already says where it lives, and because the
// tool has to be a read whatever the server is: a tool per server would put
// the read behind whichever tier that server's tools sit in.
const ResourceToolName = "mcp_resource"

// resourceSchema is the tool's whole argument list. The catalog is not in
// it: a schema describes the shape of a call and a uri is the data it is
// made with. What this session can read is in the MCP block of the prompt.
const resourceSchema = `{"type":"object","properties":{"uri":{"type":"string",` +
	`"description":"The resource URI, as the MCP servers block lists it."}},"required":["uri"]}`

// ResourceDefinition is the resource tool as the model is offered it.
func ResourceDefinition() provider.Tool {
	return provider.Tool{
		Name: ResourceToolName,
		Description: "Read one resource an MCP server publishes, by URI. Resources are the documents and " +
			"records a server holds; reading one changes nothing and costs no approval. The MCP servers " +
			"section of your instructions lists what each server publishes.",
		Parameters: json.RawMessage(resourceSchema),
	}
}

// Describers is how a call to the resource tool reads, by tool name. It has
// no word in the closed vocabulary yet, so it reads as its own name.
func Describers() map[string]calldescribe.Describer {
	return map[string]calldescribe.Describer{ResourceToolName: calldescribe.Unworded}
}

// ServerReceipt is how a call to a server's own tool reads. Every server's
// tools share it, because their names and schemas are the server's and
// nothing here knows them: a read-only server's call is a read and draws as
// one; every other server's call is an act shhh cannot see the far side of
// (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).
func ServerReceipt(readOnly bool) calldescribe.Describer {
	if readOnly {
		return calldescribe.Describer{Kind: calldescribe.KindRead, Verb: "mcp"}
	}
	return calldescribe.Describer{Kind: calldescribe.KindRemote, Verb: "mcp"}
}

// Execute runs one registered tool. Unknown names are an error rather than
// a pass-through: the executor chain asks Has first.
func (ts *Toolset) Execute(name string, args json.RawMessage) (string, error) {
	return ts.execute(web.Orchestrator, name, args)
}

// execute runs one registered tool for the named agent, which is who a page
// the result carried is filed under in the ledger.
func (ts *Toolset) execute(agent, name string, args json.RawMessage) (string, error) {
	if name == ResourceToolName {
		return ts.readResource(agent, args, false)
	}
	ref, ok := ts.begin(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	ctx, timeout, end := ts.dispatch(ref.server.Definition)
	defer end()
	out, pages, err := ref.server.call(ctx, ref.tool, args)
	if err != nil {
		return "", givenUp(ref.server.Definition, ref.tool.Remote, timeout, err)
	}
	ts.recordPages(agent, pages)
	return bound(out), nil
}

// recordPages files each page a server's result carried as a row of the
// server kind, which the sources screen draws as a call nobody vouched for:
// the page came through a boundary shhh did not fetch across, so it has no
// status of its own and is not one of the fetcher's reads.
func (ts *Toolset) recordPages(agent string, pages []pageRead) {
	ts.mu.Lock()
	l := ts.ledger
	ts.mu.Unlock()
	for _, p := range pages {
		l.Record(agent, web.Source{Kind: web.KindServer, Requested: p.URI, FinalURL: p.URI, Bytes: p.Bytes})
	}
}

// begin takes the reference behind a name and marks a call in flight, so a
// refresh cannot move the catalog out from under it. Every path that
// returns true must reach the end dispatch hands back, or the toolset never
// takes another re-listing for the rest of the session.
func (ts *Toolset) begin(name string) (toolRef, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ref, ok := ts.tools[name]
	if ok {
		ts.inflight++
	}
	return ref, ok
}

// dispatch opens the context one request runs on, bounded by what the
// definition allows a call, and puts its cancel where AbandonCalls can
// reach it. end undoes all three — off the register, the inflight count
// down, the context released — and runs on the abandoned path as much as on
// the ordinary one, which is what keeps a cancelled call from leaving the
// toolset permanently mid-round.
func (ts *Toolset) dispatch(def Definition) (context.Context, time.Duration, func()) {
	timeout := def.ToolCallTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ts.mu.Lock()
	if ts.calls == nil {
		ts.calls = map[int]context.CancelFunc{}
	}
	id := ts.nextCall
	ts.nextCall++
	ts.calls[id] = cancel
	ts.mu.Unlock()
	return ctx, timeout, func() {
		ts.mu.Lock()
		delete(ts.calls, id)
		ts.inflight--
		ts.mu.Unlock()
		cancel()
	}
}

// givenUp names an ending the session itself decided on. The SDK reports
// both as the context package's own words, which say nothing about which
// server, which tool or how long — a model told "context canceled" has no
// way to tell a two-minute timeout from a person pressing the interrupt,
// and neither has the person reading the transcript afterwards.
func givenUp(def Definition, tool string, timeout time.Duration, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("server %s: %s did not answer within %s; the call was given up. "+
			"Raise mcp.call_timeout_seconds if this tool is meant to take that long", def.Name, tool, timeout)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("server %s: %s was cancelled", def.Name, tool)
	}
	return err
}

// readResource answers the resource tool. readOnlyServers is the child
// agent's chain: it was handed the read-only servers and nothing else, so a
// URI on any other server is refused there rather than read. A read at an
// http or https address is the server handing back that page, the same as a
// tool result that embeds one, so it is filed under agent through the same
// recordPages a tool call's pages take (docs/capabilities/chat.md#what-was-read).
func (ts *Toolset) readResource(agent string, args json.RawMessage, readOnlyServers bool) (string, error) {
	var a struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	uri := strings.TrimSpace(a.URI)
	if uri == "" {
		return "", fmt.Errorf("%s needs a uri", ResourceToolName)
	}
	server, err := ts.resolveResource(uri, readOnlyServers)
	if err != nil {
		return "", err
	}
	ctx, timeout, end := ts.dispatch(server.Definition)
	defer end()
	out, pages, err := server.read(ctx, uri)
	if err != nil {
		return "", givenUp(server.Definition, ResourceToolName, timeout, err)
	}
	ts.recordPages(agent, pages)
	return bound(out), nil
}

// resolveResource finds the server a URI belongs to and marks a call in
// flight. A URI a server listed resolves to that server. One nobody listed
// resolves by scheme when exactly one server published resources under it,
// which is what makes a server's own addressing space reachable past the
// handful of URIs it chose to enumerate; an ambiguous scheme is refused
// rather than guessed, because the wrong server is a request sent somewhere
// the reader did not intend.
func (ts *Toolset) resolveResource(uri string, readOnlyServers bool) (*Server, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	admits := func(s *Server) bool { return !readOnlyServers || s.Definition.ReadOnly }
	if s, ok := ts.resources[uri]; ok && admits(s) {
		ts.inflight++
		return s, nil
	}
	scheme, _, hasScheme := strings.Cut(uri, ":")
	var candidates []*Server
	if hasScheme {
		for known, s := range ts.resources {
			if !admits(s) || !strings.HasPrefix(known, scheme+":") {
				continue
			}
			if !containsServer(candidates, s) {
				candidates = append(candidates, s)
			}
		}
	}
	switch len(candidates) {
	case 1:
		ts.inflight++
		return candidates[0], nil
	case 0:
		if readOnlyServers && ts.resources[uri] != nil {
			return nil, fmt.Errorf("%s is not available to this agent: its server is not marked read-only", uri)
		}
		return nil, fmt.Errorf("no connected server publishes %s; the MCP servers section of your instructions lists what they do publish", uri)
	}
	names := make([]string, 0, len(candidates))
	for _, s := range candidates {
		names = append(names, s.Definition.Name)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("%s could be on any of %s; name a uri one of them listed", uri, strings.Join(names, ", "))
}

func containsServer(list []*Server, s *Server) bool {
	for _, c := range list {
		if c == s {
			return true
		}
	}
	return false
}

// bound caps what one call feeds the model and says so where it cut.
func bound(out string) string {
	if cut, wasCut := tools.TruncateOutput(out, MaxResultBytes); wasCut {
		return cut + fmt.Sprintf("\n… (truncated: the result was longer than %d bytes)", MaxResultBytes)
	}
	return out
}

// WrapExecutor puts the toolset on an executor chain: its own tools are
// dispatched here, everything else passes to next. agent is who a page the
// call read is filed under in the ledger.
func (ts *Toolset) WrapExecutor(agent string, next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if ts.Has(name) {
			return ts.execute(agent, name, args)
		}
		return next(name, args)
	}
}

// WrapReadOnlyExecutor is the chain link for a caller that was handed only
// ReadOnlyDefinitions — a child agent. A gated tool's name reaching it is
// not dispatched: the child was never offered the tool, has no card to ask
// on, and a name it learned from its task text must not be a way around
// the card the parent would have shown
// (docs/capabilities/mcp.md#what-a-conversation-may-reach).
func (ts *Toolset) WrapReadOnlyExecutor(agent string, next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if ts.Has(name) {
			if name == ResourceToolName {
				return ts.readResource(agent, args, true)
			}
			if !ts.ReadOnly(name) {
				return "", fmt.Errorf("%s is not available to this agent: its server is not marked read-only", name)
			}
			return ts.execute(agent, name, args)
		}
		return next(name, args)
	}
}

// Preview is what an approval card says about a call before it runs.
type Preview struct {
	// Server and Tool name the act; Summary is the one-line form for the
	// card's headline, Args the arguments as the model gave them.
	Server, Tool, Summary, Args string
	// Transport says where the call goes: a process on this machine, or a
	// host the request leaves for.
	Transport Transport
	Target    string
	// ReadOnlyHint is the server's own claim, quoted as a claim.
	ReadOnlyHint bool
}

// Preview describes a call for its approval card.
func (ts *Toolset) Preview(name string, args json.RawMessage) (Preview, error) {
	ts.mu.Lock()
	ref, ok := ts.tools[name]
	ts.mu.Unlock()
	if !ok {
		return Preview{}, fmt.Errorf("unknown tool: %s", name)
	}
	def := ref.server.Definition
	p := Preview{
		Server: def.Name, Tool: ref.tool.Remote,
		Transport: def.Transport, Target: def.Target(),
		ReadOnlyHint: ref.tool.ReadOnlyHint,
		Args:         CompactArgs(args),
	}
	p.Summary = def.Name + " " + ref.tool.Remote
	if p.Args != "" {
		p.Summary += " " + p.Args
	}
	return p, nil
}

// CompactArgs renders arguments as `key=value` pairs on one line, values
// clipped, keys sorted — enough to recognise a call, never a page of it.
func CompactArgs(raw json.RawMessage) string {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var v string
		switch x := args[k].(type) {
		case string:
			v = x
		default:
			b, _ := json.Marshal(x)
			v = string(b)
		}
		v = strings.ReplaceAll(v, "\n", " ")
		if r := []rune(v); len(r) > 60 {
			v = string(r[:57]) + "..."
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

// Close ends every session: the servers that joined, the ones that answered
// and were waiting for a boundary, and — through settle — any that answer
// after this.
func (ts *Toolset) Close() {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.closed = true
	for _, s := range ts.servers {
		s.Close()
	}
	for _, r := range ts.reports {
		r.Server.Close()
	}
}
