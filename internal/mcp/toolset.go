package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
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
	// StatusChanged: a project server whose definition changed since it was
	// trusted, so the trust no longer covers it.
	StatusChanged Status = "changed"
	// StatusMissingEnv: it references an environment variable that is unset.
	StatusMissingEnv Status = "missing-env"
	// StatusExcluded: the session's kind does not admit it — a conversation
	// takes only servers marked read-only.
	StatusExcluded Status = "excluded"
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
}

// ProjectTrust is the person's answer about the checkout a project server
// was defined in: whether what it declares may load at all, and whether an
// answer they gave was overtaken by an edit. It is a value the session reads
// from its own store before the first dial; nothing in a checkout can set
// it, and the zero value — nobody asked — starts nothing.
//
// A server is not trusted by name any more. Every kind of thing a checkout
// can name runs as whoever cloned it, so the question is asked once about
// the checkout rather than five times about five files
// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
type ProjectTrust struct {
	// Granted is trust recorded at the checkout as it stands now.
	Granted bool
	// Changed is trust recorded at a different state of it.
	Changed bool
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
	// Timeout overrides every definition's startup timeout when set.
	Timeout time.Duration
}

// Toolset is the session's connected servers and what they offer,
// addressed by the names the model calls and the commands the person types.
//
// Everything derived from a server's catalog is rebuilt together under one
// lock, because a list-changed notification can arrive at any moment and a
// table half rebuilt would answer Has for a tool Execute can no longer find.
type Toolset struct {
	Reports []Report

	mu        sync.Mutex
	servers   map[string]*Server
	tools     map[string]toolRef
	prompts   map[string]promptRef
	resources map[string]*Server
	defs      []provider.Tool
	// inflight counts the calls dispatched and not yet returned. A refresh
	// waits for it to reach zero, which is what makes the swap a round
	// boundary rather than something that happens under a round's own calls
	// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	inflight int
}

type toolRef struct {
	server *Server
	tool   Tool
}

type promptRef struct {
	server *Server
	prompt Prompt
}

// Connect tries every definition in the catalog at once and returns the
// toolset with a report per definition. Nothing here is an error: a server
// that did not connect is a report the listing shows and a tool the
// session does not have, the same way a language server that was not found
// is. Every server connects concurrently because the slow case — a cold
// `npx` cache — is per server and a session should not pay it in series.
func Connect(ctx context.Context, c *Catalog, opts Options) *Toolset {
	ts := &Toolset{servers: map[string]*Server{}}
	if c == nil {
		ts.index()
		return ts
	}
	ts.Reports = make([]Report, len(c.Servers))
	var wg sync.WaitGroup
	for i, def := range c.Servers {
		ts.Reports[i] = Report{Definition: def}
		if status, missing := admit(def, opts); status != "" {
			ts.Reports[i].Status = status
			ts.Reports[i].Missing = missing
			continue
		}
		wg.Add(1)
		go func(i int, def Definition) {
			defer wg.Done()
			ts.Reports[i] = connectOne(ctx, def, opts)
		}(i, def)
	}
	wg.Wait()
	for _, r := range ts.Reports {
		if r.Status == StatusConnected {
			ts.servers[r.Definition.Name] = r.Server
		}
	}
	ts.index()
	return ts
}

// index rebuilds every table the session reads from the servers' catalogs.
// It is one function and not four because the tables have to agree: a
// prompt row pointing at a server whose tool table was not rebuilt is a
// command that answers with a tool the model was never offered. Callers
// hold ts.mu, except Connect, where nothing else can see the toolset yet.
func (ts *Toolset) index() {
	ts.tools = map[string]toolRef{}
	ts.prompts = map[string]promptRef{}
	ts.resources = map[string]*Server{}
	ts.defs = nil
	taken := map[string]bool{}
	for _, s := range ts.sorted() {
		for _, t := range s.Tools {
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
// list and the prompt block naming the resources both went into the request
// when the session opened, and a tool or a uri the model was never told
// about is one it will not ask for. What moves here is what is read at the
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
	}
	if !moved {
		return false
	}
	ts.index()
	return true
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
		switch {
		case opts.Project.Changed:
			return StatusChanged, nil
		case !opts.Project.Granted:
			return StatusUntrusted, nil
		}
	}
	if _, missing := def.Expand(opts.Lookup); len(missing) > 0 {
		return StatusMissingEnv, missing
	}
	return "", nil
}

func connectOne(ctx context.Context, def Definition, opts Options) Report {
	r := Report{Definition: def}
	expanded, _ := def.Expand(opts.Lookup)
	timeout := def.StartupTimeout()
	if opts.Timeout > 0 {
		timeout = opts.Timeout
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
		s, err := Dial(ctx, expanded)
		done <- dialed{s, err}
	}()
	var (
		s   *Server
		err error
	)
	select {
	case d := <-done:
		s, err = d.s, d.err
	case <-time.After(timeout):
		err = fmt.Errorf("server %s: no answer within %s", def.Name, timeout)
		go func() {
			if d := <-done; d.s != nil {
				d.s.Close()
			}
		}()
	}
	r.Took = time.Since(started)
	if err != nil {
		r.Status = StatusFailed
		r.Error = err.Error()
		if ctx.Err() != nil {
			// The session went away mid-dial: that is not the server's
			// fault, and the row should not say it was.
			r.Error = fmt.Sprintf("server %s: the session ended before it answered", def.Name)
		}
		return r
	}
	// The unexpanded definition is what the report shows: the listing must
	// never print a token that an environment reference stood in for.
	s.Definition = def
	r.Status = StatusConnected
	r.Server = s
	return r
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
	if ts == nil {
		return nil
	}
	var out []provider.Tool
	for _, d := range ts.Definitions() {
		if d.Name == ResourceToolName {
			if ts.hasReadOnlyResource() {
				out = append(out, d)
			}
			continue
		}
		if ts.ReadOnly(d.Name) {
			out = append(out, d)
		}
	}
	return out
}

func (ts *Toolset) hasReadOnlyResource() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
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

// Execute runs one registered tool. Unknown names are an error rather than
// a pass-through: the executor chain asks Has first.
func (ts *Toolset) Execute(name string, args json.RawMessage) (string, error) {
	if name == ResourceToolName {
		return ts.readResource(args, false)
	}
	ref, ok := ts.begin(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	defer ts.end()
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCallTimeout)
	defer cancel()
	out, err := ref.server.Call(ctx, ref.tool, args)
	if err != nil {
		return "", err
	}
	return bound(out), nil
}

// begin takes the reference behind a name and marks a call in flight, so a
// refresh cannot move the catalog out from under it.
func (ts *Toolset) begin(name string) (toolRef, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ref, ok := ts.tools[name]
	if ok {
		ts.inflight++
	}
	return ref, ok
}

func (ts *Toolset) end() {
	ts.mu.Lock()
	ts.inflight--
	ts.mu.Unlock()
}

// readResource answers the resource tool. readOnlyServers is the child
// agent's chain: it was handed the read-only servers and nothing else, so a
// URI on any other server is refused there rather than read.
func (ts *Toolset) readResource(args json.RawMessage, readOnlyServers bool) (string, error) {
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
	defer ts.end()
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCallTimeout)
	defer cancel()
	out, err := server.Read(ctx, uri)
	if err != nil {
		return "", err
	}
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
// dispatched here, everything else passes to next.
func (ts *Toolset) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if ts.Has(name) {
			return ts.Execute(name, args)
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
func (ts *Toolset) WrapReadOnlyExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if ts.Has(name) {
			if name == ResourceToolName {
				return ts.readResource(args, true)
			}
			if !ts.ReadOnly(name) {
				return "", fmt.Errorf("%s is not available to this agent: its server is not marked read-only", name)
			}
			return ts.Execute(name, args)
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

// Close ends every session.
func (ts *Toolset) Close() {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, s := range ts.servers {
		s.Close()
	}
}
