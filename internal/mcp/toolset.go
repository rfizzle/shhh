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

// Trust answers whether a project server may start. It is a store the
// session owns; nothing in a checkout can answer it.
type Trust interface {
	// Trusted returns the fingerprint the server was trusted at, if any.
	Trusted(root, name string) (fingerprint string, ok bool)
}

// Options shape a connect.
type Options struct {
	// Root is the repository root project servers are trusted under.
	Root string
	// Trust decides project servers; nil trusts none.
	Trust Trust
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

// Toolset is the session's connected servers and the tools they offer,
// addressed by the names the model calls.
type Toolset struct {
	Reports []Report

	mu      sync.Mutex
	servers map[string]*Server
	tools   map[string]toolRef
	defs    []provider.Tool
}

type toolRef struct {
	server *Server
	tool   Tool
}

// Connect tries every definition in the catalog at once and returns the
// toolset with a report per definition. Nothing here is an error: a server
// that did not connect is a report the listing shows and a tool the
// session does not have, the same way a language server that was not found
// is. Every server connects concurrently because the slow case — a cold
// `npx` cache — is per server and a session should not pay it in series.
func Connect(ctx context.Context, c *Catalog, opts Options) *Toolset {
	ts := &Toolset{servers: map[string]*Server{}, tools: map[string]toolRef{}}
	if c == nil {
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
	taken := map[string]bool{}
	for _, r := range ts.Reports {
		if r.Status != StatusConnected {
			continue
		}
		ts.servers[r.Definition.Name] = r.Server
		for _, t := range r.Server.Tools {
			if taken[t.Name] {
				continue
			}
			taken[t.Name] = true
			ts.tools[t.Name] = toolRef{server: r.Server, tool: t}
			ts.defs = append(ts.defs, provider.Tool{Name: t.Name, Description: describe(r.Server, t), Parameters: t.InputSchema})
		}
	}
	sort.Slice(ts.defs, func(i, j int) bool { return ts.defs[i].Name < ts.defs[j].Name })
	return ts
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
		if opts.Trust == nil {
			return StatusUntrusted, nil
		}
		fp, ok := opts.Trust.Trusted(opts.Root, def.Name)
		switch {
		case !ok:
			return StatusUntrusted, nil
		case fp != def.Fingerprint():
			return StatusChanged, nil
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
	return append([]provider.Tool(nil), ts.defs...)
}

// Len is how many tools are registered.
func (ts *Toolset) Len() int {
	if ts == nil {
		return 0
	}
	return len(ts.defs)
}

// Has reports whether name is one of this toolset's tools.
func (ts *Toolset) Has(name string) bool {
	if ts == nil {
		return false
	}
	_, ok := ts.tools[name]
	return ok
}

// ReadOnly reports whether name belongs to a server the person marked
// read-only, which is the one thing that lets it run without an answer.
func (ts *Toolset) ReadOnly(name string) bool {
	if ts == nil {
		return false
	}
	ref, ok := ts.tools[name]
	return ok && ref.server.Definition.ReadOnly
}

// Gated is every registered tool that needs an answer before it runs:
// the tools of every server not marked read-only.
func (ts *Toolset) Gated() []string {
	if ts == nil {
		return nil
	}
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
// a child agent, which has no card to ask on, is handed.
func (ts *Toolset) ReadOnlyDefinitions() []provider.Tool {
	if ts == nil {
		return nil
	}
	var out []provider.Tool
	for _, d := range ts.defs {
		if ts.ReadOnly(d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// Lookup returns the server and tool behind a session name.
func (ts *Toolset) Lookup(name string) (*Server, Tool, bool) {
	if ts == nil {
		return nil, Tool{}, false
	}
	ref, ok := ts.tools[name]
	if !ok {
		return nil, Tool{}, false
	}
	return ref.server, ref.tool, true
}

// Servers are the connected servers, by name.
func (ts *Toolset) Servers() []*Server {
	if ts == nil {
		return nil
	}
	out := make([]*Server, 0, len(ts.servers))
	for _, s := range ts.servers {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Definition.Name < out[j].Definition.Name })
	return out
}

// MaxResultBytes caps what one call may feed the model. It is the file
// read's cap: a remote result is a read like any other, and the evidence
// store reduces it further on the same terms.
const MaxResultBytes = tools.MaxReadFileBytes

// Execute runs one registered tool. Unknown names are an error rather than
// a pass-through: the executor chain asks Has first.
func (ts *Toolset) Execute(name string, args json.RawMessage) (string, error) {
	ref, ok := ts.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCallTimeout)
	defer cancel()
	out, err := ref.server.Call(ctx, ref.tool, args)
	if err != nil {
		return "", err
	}
	if cut, wasCut := tools.TruncateOutput(out, MaxResultBytes); wasCut {
		out = cut + fmt.Sprintf("\n… (truncated: the result was longer than %d bytes)", MaxResultBytes)
	}
	return out, nil
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
	ref, ok := ts.tools[name]
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
