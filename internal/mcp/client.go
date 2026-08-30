package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
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

// Server is one connected server: the definition it came from, what the
// handshake said, and the tools it listed.
type Server struct {
	Definition Definition
	// Info and Instructions are what the server said about itself. The
	// instructions are meant for the model and go into the prompt.
	Info         sdk.Implementation
	Instructions string
	Protocol     string
	Tools        []Tool

	session *sdk.ClientSession
	// stderr holds the tail of a stdio server's standard error, which is
	// where a server that would not start explains why.
	stderr *tailBuffer
	// cmd is the stdio process, kept so Close can wait for it.
	cmd *exec.Cmd
	mu  sync.Mutex
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
	client := sdk.NewClient(clientInfo, &sdk.ClientOptions{
		// A client that advertises no capabilities is what a tool-only
		// consumer is; roots, sampling and elicitation are all things shhh
		// does not answer, and advertising them invites requests it would
		// drop.
		Capabilities: &sdk.ClientCapabilities{},
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		// A handshake that failed may have left the process running — a
		// server that started and then never answered is the common
		// timeout — and nothing else will ever close it.
		s.kill()
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
	if err := s.listTools(ctx); err != nil {
		_ = session.Close()
		s.kill()
		return nil, s.wrapErr("list tools", err)
	}
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
			// The standalone notification stream is what a server uses to
			// say its tool list changed. shhh snapshots the list at connect
			// and does not act on the notification, so the stream would be a
			// held connection with nothing to deliver.
			DisableStandaloneSSE: true,
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

// listTools fetches every page of the server's tool list and names each
// tool for the session.
func (s *Server) listTools(ctx context.Context) error {
	var (
		tools  []Tool
		cursor string
		taken  = map[string]bool{}
	)
	for {
		res, err := s.session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return err
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
	s.Tools = tools
	return nil
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

// ToolName is the name the model calls a server's tool by: server, the
// separator, then the remote name with every character a provider would
// reject replaced, cut to the provider limit and made unique against taken.
// The server name goes first so that a transcript row and a tool list both
// group by where a tool came from.
func ToolName(server, remote string, taken map[string]bool) string {
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
		local = "tool"
	}
	prefix := server + Separator
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
	// The session is read under the lock Close nils it under: a call that
	// starts after the session ended is an error, not a nil dereference.
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
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
	var parts []string
	for _, c := range res.Content {
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
				parts = append(parts, fmt.Sprintf("[resource omitted: %s, %s, %s]", v.Resource.URI, v.Resource.MIMEType, byteCount(len(v.Resource.Blob))))
			}
		}
	}
	text := strings.TrimRight(strings.Join(parts, "\n"), "\n")
	if text == "" && res.StructuredContent != nil {
		if b, err := json.MarshalIndent(res.StructuredContent, "", "  "); err == nil {
			text = string(b)
		}
	}
	return text
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
