package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/profile"
)

// clientInfo is what shhh says it is in the initialize handshake.
var clientInfo = &sdk.Implementation{Name: "shhh", Version: "dev"}

// SetVersion stamps the version the handshake reports; the CLI knows it and
// this package does not.
func SetVersion(v string) {
	if v != "" {
		clientInfo = &sdk.Implementation{Name: "shhh", Version: v}
	}
}

// Tool is one tool a connected server offers, as the session sees it.
type Tool struct {
	// Name is the name the model calls: the server's name, two underscores,
	// the tool's own name made provider-safe. Remote is the name the server
	// knows the tool by.
	Name   string
	Remote string
	// Title is the human-readable name, falling back to Remote.
	Title       string
	Description string
	InputSchema json.RawMessage
	// ReadOnlyHint is the server's own claim that the tool changes nothing.
	// It is shown, and it grants nothing
	// (docs/capabilities/mcp.md#a-server-cannot-vouch-for-itself).
	ReadOnlyHint bool
	// Destructive is the server's own claim that the tool may destroy
	// something; the default in the protocol is true, so the flag is only
	// meaningful when a server bothered to say otherwise.
	Destructive bool
}

// PromptArgument is one value a prompt is rendered with. The protocol
// describes an argument by name and prose rather than by a JSON schema, so
// this is the whole of what a server says about one.
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

// Prompt is one prompt a connected server offers, as the session sees it: a
// command of its own rather than a tool, because a prompt is text for the
// person to send and not a call for the model to make.
type Prompt struct {
	// Name is the command the session answers to, without its slash: the
	// server's name, the prompt separator, then the prompt's own name made
	// safe. Remote is the name the server knows it by.
	Name   string
	Remote string
	// Server is the definition's name, so a listing can group by where a
	// prompt came from without splitting the name again.
	Server string
	// Title is the human-readable name, falling back to Remote.
	Title       string
	Description string
	Arguments   []PromptArgument
}

// Usage is the prompt's argument list as one line, in the form a person
// types it: `name=` for one the server requires, `[name=]` for one it does
// not, and empty for a prompt that takes nothing. It lives here rather than
// on each surface because the menu hint, the listing row and the refusal
// that quotes it back all have to agree — a usage line that says one thing
// in the menu and another in the error is worse than none.
func (p Prompt) Usage() string {
	if len(p.Arguments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p.Arguments))
	for _, a := range p.Arguments {
		if a.Required {
			parts = append(parts, a.Name+"=")
			continue
		}
		parts = append(parts, "["+a.Name+"=]")
	}
	return strings.Join(parts, " ")
}

// Resource is one resource a connected server publishes. Reading one is a
// read whatever the server is: it returns what the server holds and changes
// nothing (docs/capabilities/mcp.md#a-resource-is-a-read).
type Resource struct {
	URI  string
	Name string
	// Title is the human-readable name, falling back to Name.
	Title       string
	Description string
	MIMEType    string
	// Size is what the server said the raw content weighs, or zero when it
	// did not say.
	Size int64
}

// catalog is everything one server offers, as one value. It is a value
// rather than three fields so a re-listing can be prepared off to the side
// and swapped in whole: a catalog half replaced would offer a prompt whose
// server no longer lists it.
type catalog struct {
	tools     []Tool
	prompts   []Prompt
	resources []Resource
}

// Server is one connected server: the definition it came from, what the
// handshake said, and what it listed.
type Server struct {
	Definition Definition
	// Info and Instructions are what the server said about itself. The
	// instructions are meant for the model and go into the prompt.
	Info         sdk.Implementation
	Instructions string
	Protocol     string
	Tools        []Tool
	Prompts      []Prompt
	Resources    []Resource

	session *sdk.ClientSession
	// stderr holds the tail of a stdio server's standard error, which is
	// where a server that would not start explains why.
	stderr *tailBuffer
	// cmd is the stdio process, kept so Close can wait for it.
	cmd *exec.Cmd
	mu  sync.Mutex
	// pending is a re-listing a list-changed notification asked for, waiting
	// for a round boundary to be taken. The notification arrives on the
	// transport's own goroutine, in the middle of whatever the session is
	// doing, and a catalog swapped in there would move under the round's
	// own calls (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	pending *catalog
	// listing says a re-listing is already in flight, so a server that
	// announces three changes in a second costs one round trip rather than
	// three.
	listing bool
}

// Dial starts or reaches the server the definition names, runs the
// handshake, and lists its tools. The context is the session's, not a
// deadline: the SDK's SSE transport keeps its event stream on the context
// it was dialled with, so a context cancelled after the handshake would
// close the server behind a listing that says it connected. Callers bound
// the handshake by waiting, not by cancelling (see connectOne).
func Dial(ctx context.Context, def Definition) (*Server, error) {
	s := &Server{Definition: def}
	transport, err := s.transport(ctx)
	if err != nil {
		return nil, err
	}
	// The handshake says what shhh will do with what the server sends. It
	// takes tools, prompts and resources, so it registers a handler for each
	// list-changed notification and the server learns it is worth sending
	// them. It still advertises no capabilities of its own: roots, sampling
	// and elicitation are requests *to* the client, shhh answers none of
	// them, and advertising one invites a request it would drop.
	client := sdk.NewClient(clientInfo, &sdk.ClientOptions{
		Capabilities:               &sdk.ClientCapabilities{},
		ToolListChangedHandler:     func(context.Context, *sdk.ToolListChangedRequest) { s.relist(ctx) },
		PromptListChangedHandler:   func(context.Context, *sdk.PromptListChangedRequest) { s.relist(ctx) },
		ResourceListChangedHandler: func(context.Context, *sdk.ResourceListChangedRequest) { s.relist(ctx) },
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		// A handshake that failed may have left the process running — a
		// server that started and then never answered is the common
		// timeout — and nothing else will ever close it.
		s.kill()
		// A server that will not connect is written down because the
		// session goes on without it: its tools are simply absent, and a
		// model that never had a tool does not report missing one
		// (docs/capabilities/configuration.md#a-failure-is-written-down).
		// The transport's own error is left out — it is built from the
		// command line or the endpoint the definition names, which is the
		// one thing this file should not accumulate; `shhh mcp` shows it
		// whole, to the person who asked.
		//
		// A cancelled dial is not a server that would not connect. The
		// context here is the session's own, and a caller that has given up
		// waiting leaves this goroutine running against it — so quitting
		// with a slow server still dialling would write a line accusing it
		// of a failure that was the session ending.
		if !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			logs.Logger().Warn("mcp server would not connect",
				"server", def.Name, "transport", string(def.Transport))
		}
		return nil, s.wrapErr("connect", err)
	}
	s.session = session
	if init := session.InitializeResult(); init != nil {
		if init.ServerInfo != nil {
			s.Info = *init.ServerInfo
		}
		s.Instructions = strings.TrimSpace(init.Instructions)
		s.Protocol = init.ProtocolVersion
	}
	cat, err := s.listAll(ctx, session)
	if err != nil {
		_ = session.Close()
		s.kill()
		return nil, s.wrapErr("list tools", err)
	}
	s.Tools, s.Prompts, s.Resources = cat.tools, cat.prompts, cat.resources
	return s, nil
}

// transport builds the transport for the definition. Environment references
// have already been expanded by the caller.
func (s *Server) transport(ctx context.Context) (sdk.Transport, error) {
	def := s.Definition
	switch def.Transport {
	case TransportStdio:
		cmd := exec.CommandContext(context.Background(), def.Command, def.Args...)
		cmd.Env = mergeEnv(os.Environ(), def.Env)
		s.stderr = &tailBuffer{max: 4096}
		cmd.Stderr = s.stderr
		s.cmd = cmd
		return &sdk.CommandTransport{Command: cmd}, nil
	case TransportHTTP:
		return &sdk.StreamableClientTransport{
			Endpoint:   def.URL,
			HTTPClient: httpClient(def.Headers),
			// The standalone notification stream is what a remote server
			// says its lists changed over. shhh acts on that now, so the
			// stream is what carries a prompt or a resource the server added
			// after the session opened; without it the catalog is whatever
			// the server held at connect, for the life of the session.
		}, nil
	case TransportSSE:
		return &sdk.SSEClientTransport{Endpoint: def.URL, HTTPClient: httpClient(def.Headers)}, nil
	}
	return nil, fmt.Errorf("server %s: unknown transport %q", def.Name, def.Transport)
}

// mergeEnv overlays extra on base, replacing a variable already set.
func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if _, replaced := extra[name]; !replaced {
			out = append(out, kv)
		}
	}
	names := make([]string, 0, len(extra))
	for k := range extra {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		out = append(out, k+"="+extra[k])
	}
	return out
}

// httpClient sends the definition's headers on every request, through the
// same round tripper a gateway profile uses. It is how a token reaches a
// remote server: shhh sends what it was told to send and runs no
// authorisation flow of its own
// (docs/capabilities/mcp.md#shhh-speaks-the-protocol-and-nothing-else).
func httpClient(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return http.DefaultClient
	}
	return &http.Client{Transport: profile.NewTransport(profile.Endpoint{Headers: headers}, nil)}
}

// listAll fetches the three lists a server can hold. Only the tools decide
// whether the dial succeeded: a server that declares prompts or resources
// and then will not list them still has tools worth having, and a listing
// that failed is not a server that did not connect. Each list is asked for
// only when the handshake said the server has it, because a server that
// never declared prompts answers prompts/list with a protocol error.
func (s *Server) listAll(ctx context.Context, session *sdk.ClientSession) (catalog, error) {
	var (
		cat  catalog
		caps *sdk.ServerCapabilities
	)
	if init := session.InitializeResult(); init != nil {
		caps = init.Capabilities
	}
	tools, err := s.listTools(ctx, session)
	if err != nil {
		return catalog{}, err
	}
	cat.tools = tools
	if caps != nil && caps.Prompts != nil {
		cat.prompts, _ = s.listPrompts(ctx, session)
	}
	if caps != nil && caps.Resources != nil {
		cat.resources, _ = s.listResources(ctx, session)
	}
	return cat, nil
}

// listTools fetches every page of the server's tool list and names each
// tool for the session.
func (s *Server) listTools(ctx context.Context, session *sdk.ClientSession) ([]Tool, error) {
	var (
		tools  []Tool
		cursor string
		taken  = map[string]bool{}
	)
	for {
		res, err := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, t := range res.Tools {
			if t == nil || t.Name == "" {
				continue
			}
			tools = append(tools, s.toolFrom(t, taken))
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Remote < tools[j].Remote })
	return tools, nil
}

// listPrompts fetches every page of the server's prompt list. Each prompt
// becomes a command the session answers to.
func (s *Server) listPrompts(ctx context.Context, session *sdk.ClientSession) ([]Prompt, error) {
	var (
		prompts []Prompt
		cursor  string
		taken   = map[string]bool{}
	)
	for {
		res, err := session.ListPrompts(ctx, &sdk.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, p := range res.Prompts {
			if p == nil || p.Name == "" {
				continue
			}
			prompts = append(prompts, s.promptFrom(p, taken))
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	sort.Slice(prompts, func(i, j int) bool { return prompts[i].Remote < prompts[j].Remote })
	return prompts, nil
}

// listResources fetches every page of the server's resource list. Templates
// are deliberately not read: a template is a URI shape with a hole in it,
// and shhh has nothing to fill the hole with — what it offers is the
// resources the server named, plus whatever URI the model asks for under a
// scheme one of them established.
func (s *Server) listResources(ctx context.Context, session *sdk.ClientSession) ([]Resource, error) {
	var (
		resources []Resource
		cursor    string
	)
	for {
		res, err := session.ListResources(ctx, &sdk.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, r := range res.Resources {
			if r == nil || r.URI == "" {
				continue
			}
			title := r.Title
			if title == "" {
				title = r.Name
			}
			resources = append(resources, Resource{
				URI: r.URI, Name: r.Name, Title: title,
				Description: strings.TrimSpace(r.Description),
				MIMEType:    r.MIMEType, Size: r.Size,
			})
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].URI < resources[j].URI })
	return resources, nil
}

func (s *Server) promptFrom(p *sdk.Prompt, taken map[string]bool) Prompt {
	out := Prompt{
		Name:        PromptName(s.Definition.Name, p.Name, taken),
		Remote:      p.Name,
		Server:      s.Definition.Name,
		Title:       p.Title,
		Description: strings.TrimSpace(p.Description),
	}
	if out.Title == "" {
		out.Title = p.Name
	}
	for _, a := range p.Arguments {
		if a == nil || a.Name == "" {
			continue
		}
		out.Arguments = append(out.Arguments, PromptArgument{
			Name: a.Name, Description: strings.TrimSpace(a.Description), Required: a.Required,
		})
	}
	return out
}

// relist starts a re-listing after a server said one of its lists changed,
// and leaves the result for the next round boundary. It runs off the
// notification's own goroutine because the notification arrives on the
// transport's read loop, which the request it would make also needs.
func (s *Server) relist(ctx context.Context) {
	s.mu.Lock()
	session := s.session
	if session == nil || s.listing {
		s.mu.Unlock()
		return
	}
	s.listing = true
	s.mu.Unlock()
	go func() {
		// Bounded by the same deadline the connect-time listing runs under.
		// Without one, a server that announces a change and then hangs on
		// the listing leaves this goroutine parked on the session's own
		// context for the rest of the session — and, because the guard
		// above lets one re-listing run at a time, silently swallows every
		// later notification it sends.
		ctx, cancel := context.WithTimeout(ctx, s.Definition.StartupTimeout())
		defer cancel()
		cat, err := s.listAll(ctx, session)
		s.mu.Lock()
		s.listing = false
		if err == nil {
			s.pending = &cat
		}
		s.mu.Unlock()
	}()
}

// takePending swaps in a re-listing that has arrived and reports whether
// there was one. The caller decides when: this is the round boundary, and
// nothing else in this file is allowed to move the catalog.
func (s *Server) takePending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return false
	}
	s.Tools, s.Prompts, s.Resources = s.pending.tools, s.pending.prompts, s.pending.resources
	s.pending = nil
	return true
}

// liveSession is the session under the lock Close nils it under: a request
// that starts after the session ended is an error, not a nil dereference.
func (s *Server) liveSession() *sdk.ClientSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

func (s *Server) toolFrom(t *sdk.Tool, taken map[string]bool) Tool {
	schema, err := json.Marshal(t.InputSchema)
	if err != nil || len(schema) == 0 || string(schema) == "null" {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	out := Tool{
		Name:        ToolName(s.Definition.Name, t.Name, taken),
		Remote:      t.Name,
		Title:       t.Title,
		Description: strings.TrimSpace(t.Description),
		InputSchema: schema,
		Destructive: true,
	}
	if t.Annotations != nil {
		out.ReadOnlyHint = t.Annotations.ReadOnlyHint
		if t.Annotations.DestructiveHint != nil {
			out.Destructive = *t.Annotations.DestructiveHint
		}
		if out.Title == "" {
			out.Title = t.Annotations.Title
		}
	}
	if out.Title == "" {
		out.Title = t.Name
	}
	return out
}

// MaxToolNameLength is the shortest provider limit on a tool name.
const MaxToolNameLength = 64

// Separator joins a server name to a tool name. No tool of shhh's own
// contains it, which is what lets a name be recognised as a server's
// without a registry lookup.
const Separator = "__"

// PromptSeparator joins a server name to a prompt name. It is a colon and
// not the tool separator because the two are different vocabularies reached
// different ways: a tool name is written by a model into a request, a prompt
// name is typed by a person after a slash, and the colon is what every other
// harness spells a namespaced command with.
const PromptSeparator = ":"

// ToolName is the name the model calls a server's tool by: server, the
// separator, then the remote name with every character a provider would
// reject replaced, cut to the provider limit and made unique against taken.
// The server name goes first so that a transcript row and a tool list both
// group by where a tool came from.
func ToolName(server, remote string, taken map[string]bool) string {
	return joinName(server, Separator, remote, "tool", taken)
}

// PromptName is the command one of a server's prompts answers to, without
// its slash. It obeys the same length rule as a tool name: the cap is the
// provider's, but a command that does not fit a menu row is no more usable
// than a tool name a provider refuses, and one vocabulary is easier to be
// right about than two.
func PromptName(server, remote string, taken map[string]bool) string {
	return joinName(server, PromptSeparator, remote, "prompt", taken)
}

// joinName builds `<server><sep><local>`: the remote name with every
// character a provider would reject replaced, the whole cut to
// MaxToolNameLength, and made unique against taken.
func joinName(server, sep, remote, fallback string, taken map[string]bool) string {
	var b strings.Builder
	for _, r := range remote {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	local := b.String()
	if local == "" {
		local = fallback
	}
	prefix := server + sep
	if room := MaxToolNameLength - len(prefix); len(local) > room {
		local = local[:room]
	}
	name := prefix + local
	for i := 2; taken[name]; i++ {
		suffix := fmt.Sprintf("_%d", i)
		name = prefix + local[:min(len(local), MaxToolNameLength-len(prefix)-len(suffix))] + suffix
	}
	taken[name] = true
	return name
}

// SplitName returns the server part of a session tool name, and whether
// the name is a server's at all.
func SplitName(name string) (server string, ok bool) {
	server, _, ok = strings.Cut(name, Separator)
	if !ok || server == "" {
		return "", false
	}
	return server, true
}

// Call invokes a tool by its session name and flattens the result to the
// text the model reads. A tool error is returned as an error so the agent
// reports it the way it reports every other failed tool: a model that
// cannot see that a call failed cannot correct it.
func (s *Server) Call(ctx context.Context, tool Tool, args json.RawMessage) (string, error) {
	var arguments map[string]any
	if len(bytes.TrimSpace(args)) > 0 {
		if err := json.Unmarshal(args, &arguments); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	session := s.liveSession()
	if session == nil {
		return "", fmt.Errorf("server %s: closed", s.Definition.Name)
	}
	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: tool.Remote, Arguments: arguments})
	if err != nil {
		return "", s.wrapErr("call "+tool.Remote, err)
	}
	text := Flatten(res)
	if res.IsError {
		if text == "" {
			text = "the tool reported an error"
		}
		return "", fmt.Errorf("%s: %s", tool.Remote, text)
	}
	return text, nil
}

// Render asks the server for one prompt's messages, filled in with args,
// and returns them as the text the person is about to send. A prompt is a
// person's turn and not a model's call, which is why nothing here is gated:
// they typed the command, and what comes back becomes the message that
// command stands for (docs/capabilities/mcp.md#a-prompt-is-a-command).
func (s *Server) Render(ctx context.Context, p Prompt, args map[string]string) (string, error) {
	session := s.liveSession()
	if session == nil {
		return "", fmt.Errorf("server %s: closed", s.Definition.Name)
	}
	res, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: p.Remote, Arguments: args})
	if err != nil {
		return "", s.wrapErr("get prompt "+p.Remote, err)
	}
	return FlattenPrompt(res), nil
}

// Read fetches one resource and flattens it to the text the model reads.
// The result is not the server's word about anything: a resource read
// returns what the server holds and changes nothing, whatever the server
// says about itself (docs/capabilities/mcp.md#a-resource-is-a-read).
func (s *Server) Read(ctx context.Context, uri string) (string, error) {
	session := s.liveSession()
	if session == nil {
		return "", fmt.Errorf("server %s: closed", s.Definition.Name)
	}
	res, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		return "", s.wrapErr("read "+uri, err)
	}
	return FlattenResource(res), nil
}

// FlattenPrompt renders a prompt's messages as the one turn they become.
// A message the server marked as the assistant's is labelled, because a
// prompt that scripts both sides of an exchange reads as nonsense when the
// two halves are run together unmarked; a prompt that is all the person's
// words — which is nearly all of them — is left as prose.
func FlattenPrompt(res *sdk.GetPromptResult) string {
	if res == nil {
		return ""
	}
	labelled := false
	for _, m := range res.Messages {
		if m != nil && m.Role != "" && m.Role != "user" {
			labelled = true
		}
	}
	var parts []string
	for _, m := range res.Messages {
		if m == nil {
			continue
		}
		text := flattenContent([]sdk.Content{m.Content})
		if text == "" {
			continue
		}
		if labelled {
			text = string(m.Role) + ": " + text
		}
		parts = append(parts, text)
	}
	return strings.TrimRight(strings.Join(parts, "\n\n"), "\n")
}

// FlattenResource renders a resource read as text: every text part as it
// is, and every blob as the one-line notice a binary gets everywhere else
// here — the model cannot read bytes through a text tool, and a base64 blob
// in its context is a page of noise that costs more than the notice.
func FlattenResource(res *sdk.ReadResourceResult) string {
	if res == nil {
		return ""
	}
	var parts []string
	for _, c := range res.Contents {
		if c == nil {
			continue
		}
		switch {
		case c.Text != "":
			parts = append(parts, c.Text)
		case len(c.Blob) > 0:
			parts = append(parts, binaryNotice(c.URI, c.MIMEType, len(c.Blob)))
		}
	}
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

// binaryNotice is what stands in for bytes the model cannot read, wherever
// they arrive from: a tool result's embedded resource and a resource read
// get the same line, because they are the same thing to the reader of the
// transcript.
func binaryNotice(uri, mediaType string, n int) string {
	return fmt.Sprintf("[resource omitted: %s, %s, %s]", uri, mediaType, byteCount(n))
}

// Flatten renders a result as text: text blocks as they are, embedded text
// resources as their text, and every binary block as a one-line notice
// saying what was left out and how big it was — the model cannot read an
// image through a text tool, and a base64 blob in its context is a page of
// noise that costs more than the notice. Structured content is the
// fallback when a result carried no text at all.
func Flatten(res *sdk.CallToolResult) string {
	if res == nil {
		return ""
	}
	text := flattenContent(res.Content)
	if text == "" && res.StructuredContent != nil {
		if b, err := json.MarshalIndent(res.StructuredContent, "", "  "); err == nil {
			text = string(b)
		}
	}
	return text
}

// flattenContent is the one reading of a content list, shared by a tool
// result and a prompt message so the two never describe the same block two
// different ways.
func flattenContent(content []sdk.Content) string {
	var parts []string
	for _, c := range content {
		switch v := c.(type) {
		case *sdk.TextContent:
			parts = append(parts, v.Text)
		case *sdk.ImageContent:
			parts = append(parts, fmt.Sprintf("[image omitted: %s, %s]", v.MIMEType, byteCount(len(v.Data))))
		case *sdk.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio omitted: %s, %s]", v.MIMEType, byteCount(len(v.Data))))
		case *sdk.ResourceLink:
			line := "[resource: " + v.URI
			if v.Title != "" {
				line += " — " + v.Title
			} else if v.Name != "" {
				line += " — " + v.Name
			}
			parts = append(parts, line+"]")
		case *sdk.EmbeddedResource:
			if v.Resource == nil {
				continue
			}
			switch {
			case v.Resource.Text != "":
				parts = append(parts, v.Resource.Text)
			case len(v.Resource.Blob) > 0:
				parts = append(parts, binaryNotice(v.Resource.URI, v.Resource.MIMEType, len(v.Resource.Blob)))
			}
		}
	}
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

func byteCount(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// wrapErr names the server and the step, and appends what a stdio server
// wrote to stderr — for a server that would not start, that is usually the
// whole answer.
func (s *Server) wrapErr(step string, err error) error {
	msg := fmt.Sprintf("server %s: %s: %v", s.Definition.Name, step, err)
	if s.stderr != nil {
		if tail := s.stderr.String(); tail != "" {
			msg += "\n" + tail
		}
	}
	return fmt.Errorf("%s", msg)
}

// kill ends a stdio server's process if it was started. Close is the
// orderly way; this is for the paths where there is no session to close.
func (s *Server) kill() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// Stderr is the tail of what a stdio server wrote to standard error, or "".
func (s *Server) Stderr() string {
	if s == nil || s.stderr == nil {
		return ""
	}
	return s.stderr.String()
}

// Close ends the session; a stdio server gets stdin closed, then a
// SIGTERM, then a SIGKILL, on the SDK's schedule.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		_ = s.session.Close()
		s.session = nil
	}
}

// tailBuffer keeps the last max bytes written to it. A server's stderr is
// bounded so a chatty one cannot grow the session's memory, and the tail
// rather than the head because the reason for a failure is at the end.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func secondsToDuration(n int) time.Duration { return time.Duration(n) * time.Second }
