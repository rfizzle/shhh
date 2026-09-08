package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/secret"
)

// The test binary doubles as a stdio MCP server: run with the environment
// variable set, it serves two tools, two prompts and a resource over its own
// stdin and stdout, which is exactly what a definition naming os.Args[0]
// spawns. No fixture binary to build, no network.
const serverEnv = "SHHH_MCP_TEST_SERVER"

// envDumpEnv names a file the binary writes its own environment to before
// exiting: a "server" that does nothing but say what it was started with.
// The dial fails, because nothing there speaks the protocol, and the
// process ran — which is the whole of what a question about a spawned
// process's environment asks. It is a mode of its own rather than one more
// tool on the echo server so that the catalog every other test counts
// stays the size those tests say it is.
const envDumpEnv = "SHHH_MCP_TEST_ENVDUMP"

// sleepEnv names a mode that starts, speaks nothing and exits: a process
// that is up while the dial waits for a handshake that will never come,
// which is what the connect's timeout arm is there for.
const sleepEnv = "SHHH_MCP_TEST_SLEEP"

// slowServer is the value of serverEnv that adds the tool which never
// answers. It is a mode rather than a fourth tool on the ordinary server
// because every other test counts this server's catalog.
const slowServer = "slow"

func TestMain(m *testing.M) {
	if path := os.Getenv(envDumpEnv); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(os.Environ(), "\n")), 0o600)
		return
	}
	if d := os.Getenv(sleepEnv); d != "" {
		wait, err := time.ParseDuration(d)
		if err != nil {
			os.Exit(1)
		}
		time.Sleep(wait)
		return
	}
	if os.Getenv(serverEnv) != "" {
		runTestServer()
		return
	}
	os.Exit(m.Run())
}

type echoIn struct {
	Text string `json:"text"`
}
type echoOut struct {
	Echo string `json:"echo"`
}

func runTestServer() {
	server := sdk.NewServer(&sdk.Implementation{Name: "echo-server", Version: "1.2.3"}, &sdk.ServerOptions{
		Instructions: "Call echo with anything.",
	})
	sdk.AddTool(server, &sdk.Tool{
		Name:        "echo",
		Description: "Echo the text back.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, echoOut, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo: " + in.Text}}}, echoOut{Echo: in.Text}, nil
	})
	sdk.AddTool(server, &sdk.Tool{
		Name:        "fail",
		Description: "Always fails.",
	}, func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "nope: " + in.Text}}}, nil, nil
	})
	// A tool that changes the server's own lists, so a test can make a real
	// list-changed notification arrive rather than simulating one.
	sdk.AddTool(server, &sdk.Tool{
		Name:        "grow",
		Description: "Publish one more prompt.",
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ echoIn) (*sdk.CallToolResult, any, error) {
		server.AddPrompt(&sdk.Prompt{Name: "later", Description: "Added after the session opened."},
			func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
				return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{
					{Role: "user", Content: &sdk.TextContent{Text: "later"}},
				}}, nil
			})
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "grown"}}}, nil, nil
	})
	server.AddPrompt(&sdk.Prompt{
		Name:        "review",
		Description: "Review a change.",
		Arguments:   []*sdk.PromptArgument{{Name: "ref", Description: "What to review.", Required: true}},
	}, func(_ context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{
			{Role: "user", Content: &sdk.TextContent{Text: "Review " + req.Params.Arguments["ref"] + "."}},
		}}, nil
	})
	server.AddPrompt(&sdk.Prompt{
		Name:        "brief",
		Description: "Two voices.",
	}, func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{
			{Role: "user", Content: &sdk.TextContent{Text: "what changed?"}},
			{Role: "assistant", Content: &sdk.TextContent{Text: "nothing yet"}},
		}}, nil
	})
	server.AddResource(&sdk.Resource{
		URI: "docs://guide", Name: "guide", Description: "The guide.", MIMEType: "text/markdown",
	}, func(_ context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{
			{URI: req.Params.URI, MIMEType: "text/markdown", Text: "the guide, at " + req.Params.URI},
		}}, nil
	})
	// A resource template, so a uri the listing never named still reaches
	// the server: that is what the scheme rule exists to make possible.
	server.AddResourceTemplate(&sdk.ResourceTemplate{
		URITemplate: "docs://{name}", Name: "page", MIMEType: "application/octet-stream",
	}, func(_ context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{
			{URI: req.Params.URI, MIMEType: "application/octet-stream", Blob: make([]byte, 2048)},
		}}, nil
	})
	if os.Getenv(serverEnv) == slowServer {
		sdk.AddTool(server, &sdk.Tool{
			Name:        "hang",
			Description: "Never answers.",
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ echoIn) (*sdk.CallToolResult, any, error) {
			<-ctx.Done()
			return nil, nil, ctx.Err()
		})
	}
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}

func testDefinition(t *testing.T) Definition {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Definition{
		Name: "echo", Scope: ScopeUser, Transport: TransportStdio,
		Command: exe, Env: map[string]string{serverEnv: "1"}, ReadOnly: true,
	}
}

// slowDefinition is the same server with the tool that never answers.
func slowDefinition(t *testing.T) Definition {
	t.Helper()
	d := testDefinition(t)
	d.Env[serverEnv] = slowServer
	return d
}

func TestDialListsAndCallsTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := Dial(ctx, testDefinition(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.Info.Name != "echo-server" || s.Info.Version != "1.2.3" {
		t.Errorf("server info = %+v", s.Info)
	}
	if s.Instructions != "Call echo with anything." {
		t.Errorf("instructions = %q", s.Instructions)
	}
	if len(s.Tools) != 3 {
		t.Fatalf("tools = %d, want 3", len(s.Tools))
	}
	echo := s.Tools[0]
	if echo.Name != "echo__echo" || echo.Remote != "echo" || !echo.ReadOnlyHint {
		t.Errorf("echo tool = %+v", echo)
	}
	if !strings.Contains(string(echo.InputSchema), `"text"`) {
		t.Errorf("schema = %s", echo.InputSchema)
	}

	out, err := s.Call(ctx, echo, json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo: hi" {
		t.Errorf("result = %q", out)
	}

	_, err = s.Call(ctx, s.Tools[1], json.RawMessage(`{"text":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "nope: x") {
		t.Errorf("tool error = %v, want the server's text", err)
	}
}

// A server offers three lists and the client reads all three: the prompts
// become commands of the session, the resources become uris the one resource
// tool reads.
func TestDialListsPromptsAndResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := Dial(ctx, testDefinition(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if len(s.Prompts) != 2 {
		t.Fatalf("prompts = %+v, want two", s.Prompts)
	}
	brief, review := s.Prompts[0], s.Prompts[1]
	if brief.Name != "echo:brief" || review.Name != "echo:review" || review.Server != "echo" {
		t.Fatalf("prompt names = %q %q", brief.Name, review.Name)
	}
	if len(review.Arguments) != 1 || review.Arguments[0].Name != "ref" || !review.Arguments[0].Required {
		t.Fatalf("review arguments = %+v", review.Arguments)
	}
	if len(s.Resources) != 1 || s.Resources[0].URI != "docs://guide" || s.Resources[0].MIMEType != "text/markdown" {
		t.Fatalf("resources = %+v", s.Resources)
	}

	// A prompt whose messages are all the person's is prose; one that
	// scripts both sides is labelled, because run together unmarked the two
	// halves read as one contradictory sentence.
	out, err := s.Render(ctx, review, map[string]string{"ref": "the patch"})
	if err != nil || out != "Review the patch." {
		t.Fatalf("render = %q, %v", out, err)
	}
	out, err = s.Render(ctx, brief, nil)
	if err != nil || out != "user: what changed?\n\nassistant: nothing yet" {
		t.Fatalf("two-voice render = %q, %v", out, err)
	}

	if out, err = s.Read(ctx, "docs://guide"); err != nil || out != "the guide, at docs://guide" {
		t.Fatalf("read = %q, %v", out, err)
	}
	// Bytes the model cannot read come back as the notice a binary gets
	// everywhere else here, never as a page of base64.
	out, err = s.Read(ctx, "docs://opaque")
	if err != nil || out != "[resource omitted: docs://opaque, application/octet-stream, 2.0 kB]" {
		t.Fatalf("binary read = %q, %v", out, err)
	}
}

func TestConnectBuildsTheToolsetAndReports(t *testing.T) {
	def := testDefinition(t)
	c := &Catalog{Servers: []Definition{
		def,
		{Name: "off", Scope: ScopeUser, Transport: TransportStdio, Command: "true", Disabled: true},
		{Name: "proj", Scope: ScopeProject, Transport: TransportStdio, Command: "true"},
		{Name: "keyed", Scope: ScopeUser, Transport: TransportHTTP, URL: "https://x/${SHHH_MCP_TEST_NOPE}"},
		{Name: "acts", Scope: ScopeUser, Transport: TransportStdio, Command: "true"},
	}}
	ts := Connect(context.Background(), c, Options{ReadOnlyOnly: true, Lookup: func(string) (string, bool) { return "", false }})
	defer ts.Close()

	want := map[string]Status{
		"echo": StatusConnected, "off": StatusDisabled, "proj": StatusExcluded,
		"keyed": StatusExcluded, "acts": StatusExcluded,
	}
	for _, r := range ts.Reports {
		if r.Status != want[r.Definition.Name] {
			t.Errorf("%s: status %s, want %s (%s)", r.Definition.Name, r.Status, want[r.Definition.Name], r.Error)
		}
	}
	// Three tools of the server's own, plus the one tool every server's
	// resources are read through.
	if ts.Len() != 4 || !ts.Has("echo__echo") || !ts.ReadOnly("echo__fail") {
		t.Errorf("toolset = %v", ts.Definitions())
	}
	if got := ts.Gated(); len(got) != 0 {
		t.Errorf("gated = %v on a read-only server", got)
	}
	out, err := ts.Execute("echo__echo", json.RawMessage(`{"text":"there"}`))
	if err != nil || out != "echo: there" {
		t.Errorf("execute = %q, %v", out, err)
	}
	next := func(name string, _ json.RawMessage) (string, error) { return "next:" + name, nil }
	if out, _ := ts.WrapExecutor(next)("read_file", nil); out != "next:read_file" {
		t.Errorf("chain passed through = %q", out)
	}
	if out, err := ts.WrapReadOnlyExecutor(next)("echo__echo", json.RawMessage(`{"text":"ro"}`)); err != nil || out != "echo: ro" {
		t.Errorf("read-only chain = %q, %v", out, err)
	}
	p, err := ts.Preview("echo__echo", json.RawMessage(`{"text":"a\nb"}`))
	if err != nil || p.Summary != "echo echo text=a b" || !p.ReadOnlyHint {
		t.Errorf("preview = %+v, %v", p, err)
	}
	block := PromptBlock(ts)
	for _, want := range []string{"# MCP servers", "- echo — 3 tools, read-only (echo-server 1.2.3)", "> Call echo with anything.",
		"resource docs://guide — The guide.", "`" + ResourceToolName + "` reads any resource listed below"} {
		if !strings.Contains(block, want) {
			t.Errorf("prompt block lacks %q:\n%s", want, block)
		}
	}
}

// A child handed only the read-only definitions must not be able to reach
// a gated server's tool by name.
func TestWrapReadOnlyExecutorRefusesGatedTools(t *testing.T) {
	def := testDefinition(t)
	def.ReadOnly = false
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{def}}, Options{})
	defer ts.Close()
	if ts.Len() != 4 || len(ts.ReadOnlyDefinitions()) != 0 {
		t.Fatalf("toolset = %d tools, %d read-only", ts.Len(), len(ts.ReadOnlyDefinitions()))
	}
	next := func(name string, _ json.RawMessage) (string, error) { return "next:" + name, nil }
	if _, err := ts.WrapReadOnlyExecutor(next)("echo__echo", json.RawMessage(`{"text":"x"}`)); err == nil || !strings.Contains(err.Error(), "not marked read-only") {
		t.Errorf("gated tool dispatched through the read-only chain: %v", err)
	}
	if out, _ := ts.WrapReadOnlyExecutor(next)("read_file", nil); out != "next:read_file" {
		t.Errorf("chain passed through = %q", out)
	}
}

func TestAdmitProjectServersByTrust(t *testing.T) {
	def := Definition{Name: "proj", Scope: ScopeProject, Transport: TransportStdio, Command: "true", Args: []string{"a"}}
	if s, _ := admit(def, Options{}); s != StatusUntrusted {
		t.Errorf("nobody asked: %s", s)
	}
	if s, _ := admit(def, Options{Project: ProjectTrust{Granted: true}}); s != "" {
		t.Errorf("trusted: %s", s)
	}
	// The checkout was answered for and edited since: that is a different
	// row and a different sentence, not a plain refusal.
	if s, _ := admit(def, Options{Project: ProjectTrust{Changed: true}}); s != StatusChanged {
		t.Errorf("changed: %s", s)
	}
	// The person's own definition needs no checkout's permission.
	mine := def
	mine.Scope = ScopeUser
	if s, _ := admit(mine, Options{}); s != "" {
		t.Errorf("a user server waited on a checkout: %s", s)
	}
	missing := Definition{Name: "m", Scope: ScopeUser, Transport: TransportStdio, Command: "x", Env: map[string]string{"T": "${NOPE_A} ${NOPE_B}"}}
	s, names := admit(missing, Options{Lookup: func(string) (string, bool) { return "", false }})
	if s != StatusMissingEnv || strings.Join(names, ",") != "NOPE_A,NOPE_B" {
		t.Errorf("missing env: %s %v", s, names)
	}
}

func TestConnectReportsAServerThatWillNotStart(t *testing.T) {
	c := &Catalog{Servers: []Definition{{Name: "bad", Scope: ScopeUser, Transport: TransportStdio, Command: "/nonexistent/mcp-server"}}}
	ts := Connect(context.Background(), c, Options{Timeout: 5 * time.Second})
	r := ts.Reports[0]
	if r.Status != StatusFailed || !strings.Contains(r.Error, "server bad") {
		t.Errorf("report = %+v", r)
	}
	if ts.Len() != 0 {
		t.Errorf("tools registered from a failed server: %v", ts.Definitions())
	}
}

func TestToolNames(t *testing.T) {
	taken := map[string]bool{}
	cases := []struct{ remote, want string }{
		{"get_issue", "gh__get_issue"},
		{"get issue", "gh__get_issue_2"},
		{"search/code", "gh__search_code"},
		{strings.Repeat("x", 70), "gh__" + strings.Repeat("x", 60)},
		{strings.Repeat("x", 80), "gh__" + strings.Repeat("x", 58) + "_2"},
		{strings.Repeat("x", 81), "gh__" + strings.Repeat("x", 58) + "_3"},
	}
	for _, c := range cases {
		if got := ToolName("gh", c.remote, taken); got != c.want {
			t.Errorf("ToolName(%q) = %q, want %q", c.remote, got, c.want)
		}
		if len(taken) > 0 {
			for n := range taken {
				if len(n) > MaxToolNameLength {
					t.Errorf("%q is longer than %d", n, MaxToolNameLength)
				}
			}
		}
	}
	if s, ok := SplitName("gh__get_issue"); !ok || s != "gh" {
		t.Errorf("SplitName = %q %v", s, ok)
	}
	if _, ok := SplitName("read_file"); ok {
		t.Error("read_file split as a server tool")
	}
}

func TestDefinitionValidateAndExpand(t *testing.T) {
	bad := []Definition{
		{Name: "", Transport: TransportStdio, Command: "x"},
		{Name: "Bad", Transport: TransportStdio, Command: "x"},
		{Name: "9lives", Transport: TransportStdio, Command: "x"},
		{Name: "a", Transport: TransportStdio},
		{Name: "a", Transport: TransportHTTP, URL: "ftp://x"},
		{Name: "a", Transport: TransportHTTP, URL: "https://x", Command: "y"},
		{Name: "a", Transport: "grpc", URL: "https://x"},
		{Name: strings.Repeat("a", 25), Transport: TransportStdio, Command: "x"},
	}
	for _, d := range bad {
		if err := d.Validate(); err == nil {
			t.Errorf("%+v validated", d)
		}
	}
	d := Definition{Name: "a", Transport: TransportHTTP, URL: "https://${HOST}/mcp", Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}}
	env := map[string]string{"HOST": "example.test", "TOKEN": "s3cret"}
	out, missing := d.Expand(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if len(missing) != 0 || out.URL != "https://example.test/mcp" || out.Headers["Authorization"] != "Bearer s3cret" {
		t.Errorf("expand = %+v %v", out, missing)
	}
	if d.Headers["Authorization"] != "Bearer ${TOKEN}" {
		t.Error("Expand mutated the definition")
	}
	if names := d.SecretNames(); strings.Join(names, ",") != "HOST,TOKEN" {
		t.Errorf("SecretNames = %v", names)
	}
}

func TestDiscoverReadsFilesAndShadows(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	userDir := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(userDir, JSONFileName), `{"mcpServers": {
		"docs": {"command": "npx", "args": ["-y", "docs-mcp"], "readOnly": true},
		"shared": {"url": "https://user.example/mcp"},
		"Bad Name": {"command": "x"}
	}}`)
	write(filepath.Join(root, ".mcp.json"), `{"mcpServers": {
		"shared": {"url": "https://project.example/sse", "type": "sse", "read_only": true},
		"broken": {}
	}}`)
	write(filepath.Join(root, ".shhh", JSONFileName), `not json`)
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	user := []Definition{{Name: "cfg", Transport: TransportStdio, Command: "cfg-server"}}
	c := Discover(sub, user, []string{userDir})

	names := make([]string, 0, len(c.Servers))
	for _, d := range c.Servers {
		names = append(names, d.Name+":"+string(d.Scope))
	}
	if got := strings.Join(names, " "); got != "bad-name:user cfg:user docs:user shared:project" {
		t.Errorf("servers = %s", got)
	}
	shared, _ := c.Find("shared")
	if shared.Transport != TransportSSE || shared.URL != "https://project.example/sse" || shared.ReadOnly {
		t.Errorf("project shadow = %+v", shared)
	}
	docs, _ := c.Find("docs")
	if !docs.ReadOnly || docs.Transport != TransportStdio || len(docs.Args) != 2 {
		t.Errorf("docs = %+v", docs)
	}
	joined := strings.Join(c.Diagnostics, "\n")
	for _, want := range []string{`"Bad Name" is loaded as "bad-name"`, "read-only is ignored in a project file", "neither a command nor a url", "not a valid catalog", "server shared shadows your own definition"} {
		if !strings.Contains(joined, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, joined)
		}
	}
	if ProjectRoot(t.TempDir()) != "" {
		t.Error("a directory with no repository has a project root")
	}
}

func TestFlattenResults(t *testing.T) {
	res := &sdk.CallToolResult{Content: []sdk.Content{
		&sdk.TextContent{Text: "one"},
		&sdk.ImageContent{MIMEType: "image/png", Data: make([]byte, 2048)},
		&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///x", Text: "two"}},
		&sdk.ResourceLink{URI: "https://x/y", Title: "Y"},
	}}
	got := Flatten(res)
	want := "one\n[image omitted: image/png, 2.0 kB]\ntwo\n[resource: https://x/y — Y]"
	if got != want {
		t.Errorf("Flatten =\n%s\nwant\n%s", got, want)
	}
	structured := &sdk.CallToolResult{StructuredContent: map[string]any{"a": 1}}
	if got := Flatten(structured); got != "{\n  \"a\": 1\n}" {
		t.Errorf("structured = %q", got)
	}
}

func TestCompactArgs(t *testing.T) {
	got := CompactArgs(json.RawMessage(`{"b": [1,2], "a": "` + strings.Repeat("x", 70) + `", "c": "l1\nl2"}`))
	want := "a=" + strings.Repeat("x", 57) + "... b=[1,2] c=l1 l2"
	if got != want {
		t.Errorf("CompactArgs = %q", got)
	}
	if CompactArgs(json.RawMessage(`{}`)) != "" || CompactArgs(nil) != "" {
		t.Error("empty args rendered")
	}
}

// A server that will not connect leaves a line behind. The session carries on
// without its tools, and a model that never had a tool does not report
// missing one, so nothing else says the server was ever meant to be there.
func TestDial_ATransportThatWillNotConnectReachesTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	logs.To(path)
	t.Cleanup(func() { logs.To("") })

	missing := filepath.Join(t.TempDir(), "no-such-server")
	def := Definition{Name: "ghost", Scope: ScopeUser, Transport: TransportStdio, Command: missing}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := Dial(ctx, def, nil); err == nil {
		t.Fatal("a server whose command does not exist must not dial")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("nothing was written to the log: %v", err)
	}
	written := string(body)
	for _, want := range []string{"mcp server would not connect", "server=ghost", "transport=stdio"} {
		if !strings.Contains(written, want) {
			t.Errorf("the line does not say %s:\n%s", want, written)
		}
	}
	// The transport's error is built from the command the definition names.
	// `shhh mcp` shows it whole to the person who asked; the file two
	// sessions share keeps no command lines.
	if strings.Contains(written, missing) {
		t.Errorf("the line names the command:\n%s", written)
	}
}

// A dial the session cancelled is not a server that would not connect. The
// context is the session's own and a caller that gave up waiting leaves this
// dial running against it, so quitting with a slow server still handshaking
// would otherwise accuse it of a failure that was the session ending.
func TestDial_ACancelledDialWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	logs.To(path)
	t.Cleanup(func() { logs.To("") })

	// A server that starts and then says nothing, which is what a handshake
	// outlasting the session looks like from here. The context is dead
	// before the dial, which is the state this goroutine finds itself in
	// when the session it belongs to has already gone.
	def := Definition{Name: "slow", Scope: ScopeUser, Transport: TransportStdio, Command: "sleep", Args: []string{"30"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Dial(ctx, def, nil); err == nil {
		t.Fatal("a cancelled dial must not report a connected server")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		body, _ := os.ReadFile(path)
		t.Errorf("a cancelled dial wrote a log line: %v, %s", err, body)
	}
}

// A resource read is a read whatever the server is. The person's read-only
// mark answers "may this server act without asking"; reading is not acting,
// so the resource tool is never in the gated set and never draws a card.
func TestResourcesAreReadsOnAServerNobodyMarkedReadOnly(t *testing.T) {
	def := testDefinition(t)
	def.ReadOnly = false
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{def}}, Options{})
	defer ts.Close()

	if !ts.Has(ResourceToolName) || !ts.ReadOnly(ResourceToolName) {
		t.Fatalf("the resource tool is gated on a server nobody marked read-only")
	}
	for _, name := range ts.Gated() {
		if name == ResourceToolName {
			t.Fatalf("the resource tool is in the gated set: %v", ts.Gated())
		}
	}
	if len(ts.Gated()) != 3 {
		t.Fatalf("gated = %v, want the server's own three tools", ts.Gated())
	}
	out, err := ts.Execute(ResourceToolName, json.RawMessage(`{"uri":"docs://guide"}`))
	if err != nil || out != "the guide, at docs://guide" {
		t.Fatalf("resource read = %q, %v", out, err)
	}
	// A child was handed the read-only servers and nothing else, so the same
	// uri through its chain is refused rather than read.
	next := func(name string, _ json.RawMessage) (string, error) { return "next:" + name, nil }
	_, err = ts.WrapReadOnlyExecutor(next)(ResourceToolName, json.RawMessage(`{"uri":"docs://guide"}`))
	if err == nil || !strings.Contains(err.Error(), "not marked read-only") {
		t.Fatalf("a child read a resource off a server it was not handed: %v", err)
	}
	if len(ts.ReadOnlyDefinitions()) != 0 {
		t.Fatalf("a child was offered %v", ts.ReadOnlyDefinitions())
	}
}

// A uri no server listed still reaches the server that established its
// scheme — which is what a resource template is for — and one that matches
// nothing is an error naming where the catalog is, not a silent empty read.
func TestResourceUriResolvesByListingThenByScheme(t *testing.T) {
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{testDefinition(t)}}, Options{})
	defer ts.Close()

	out, err := ts.Execute(ResourceToolName, json.RawMessage(`{"uri":"docs://opaque"}`))
	if err != nil || !strings.Contains(out, "[resource omitted: docs://opaque") {
		t.Fatalf("templated uri = %q, %v", out, err)
	}
	if _, err := ts.Execute(ResourceToolName, json.RawMessage(`{"uri":"tickets://7"}`)); err == nil ||
		!strings.Contains(err.Error(), "no connected server publishes") {
		t.Fatalf("an unknown scheme = %v", err)
	}
	if _, err := ts.Execute(ResourceToolName, json.RawMessage(`{"uri":"  "}`)); err == nil ||
		!strings.Contains(err.Error(), "needs a uri") {
		t.Fatalf("an empty uri = %v", err)
	}
}

// The model is told what it can read and not what the person can type: a
// prompt is a command with no way for the model to invoke it, so naming one
// in the prompt block would cost a round to find that out.
func TestPromptBlockNamesResourcesAndNotPrompts(t *testing.T) {
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{testDefinition(t)}}, Options{})
	defer ts.Close()
	block := PromptBlock(ts)
	if !strings.Contains(block, "docs://guide") {
		t.Fatalf("the block does not name the resources:\n%s", block)
	}
	for _, name := range []string{"echo:review", "echo:brief"} {
		if strings.Contains(block, name) {
			t.Fatalf("the block offers the model a command it cannot type:\n%s", block)
		}
	}
}

// The session reaches a prompt by the command name it answers to, and an
// unknown one is a refusal the person can read rather than an empty turn.
func TestToolsetRendersAPromptByItsCommandName(t *testing.T) {
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{testDefinition(t)}}, Options{})
	defer ts.Close()

	names := make([]string, 0, 2)
	for _, p := range ts.Prompts() {
		names = append(names, p.Name)
	}
	if strings.Join(names, " ") != "echo:brief echo:review" {
		t.Fatalf("prompts = %v", names)
	}
	out, err := ts.Render(context.Background(), "echo:review", map[string]string{"ref": "HEAD"})
	if err != nil || out != "Review HEAD." {
		t.Fatalf("render = %q, %v", out, err)
	}
	if _, err := ts.Render(context.Background(), "echo:nope", nil); err == nil ||
		!strings.Contains(err.Error(), "no prompt named") {
		t.Fatalf("unknown prompt = %v", err)
	}
}

// A server that says its lists changed is re-listed in the background, and
// the new catalog is taken at a round boundary. Taking it mid-round would
// move what a call's result is an answer to.
func TestListChangedIsTakenAtTheBoundaryAndNotMidRound(t *testing.T) {
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{testDefinition(t)}}, Options{})
	defer ts.Close()

	if _, err := ts.Execute("echo__grow", json.RawMessage(`{"text":"x"}`)); err != nil {
		t.Fatal(err)
	}
	server := ts.Servers()[0]
	deadline := time.Now().Add(10 * time.Second)
	for {
		server.mu.Lock()
		arrived := server.pending != nil
		server.mu.Unlock()
		if arrived {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server's list-changed notification never produced a re-listing")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A call is out: the catalog stays where it was, whatever has arrived.
	ts.mu.Lock()
	ts.inflight++
	ts.mu.Unlock()
	if ts.Refresh() {
		t.Fatal("the catalog moved under a round's own calls")
	}
	if len(ts.Prompts()) != 2 {
		t.Fatalf("prompts moved mid-round: %+v", ts.Prompts())
	}

	// The round is over: this is the boundary, and the swap happens here.
	ts.mu.Lock()
	ts.inflight--
	ts.mu.Unlock()
	if !ts.Refresh() {
		t.Fatal("the re-listing was never taken")
	}
	names := make([]string, 0, 3)
	for _, p := range ts.Prompts() {
		names = append(names, p.Name)
	}
	if strings.Join(names, " ") != "echo:brief echo:later echo:review" {
		t.Fatalf("prompts after the boundary = %v", names)
	}
	if ts.Refresh() {
		t.Fatal("a second refresh with nothing pending reported a change")
	}
}

// A prompt's command name obeys the tool name's length rule. The cap is the
// provider's, but one vocabulary is easier to be right about than two, and a
// command that does not fit a menu row is no more usable than a tool name a
// provider refuses.
func TestPromptNamesObeyTheToolNameRule(t *testing.T) {
	taken := map[string]bool{}
	cases := []struct{ remote, want string }{
		{"review", "gh:review"},
		{"review", "gh:review_2"},
		{"write a plan", "gh:write_a_plan"},
		{strings.Repeat("x", 70), "gh:" + strings.Repeat("x", 61)},
	}
	for _, c := range cases {
		got := PromptName("gh", c.remote, taken)
		if got != c.want {
			t.Errorf("PromptName(%q) = %q, want %q", c.remote, got, c.want)
		}
		if len(got) > MaxToolNameLength {
			t.Errorf("%q is longer than %d", got, MaxToolNameLength)
		}
	}
}

// Every session that defines no server, and every surface that asks a
// toolset a question before one exists, goes through these: a session with
// no MCP at all must not be a session that panics on the first keystroke.
func TestAToolsetWithNoServersAnswersEverything(t *testing.T) {
	ts := Connect(context.Background(), nil, Options{})
	if ts.Has(ResourceToolName) || ts.ReadOnly(ResourceToolName) || ts.Len() != 0 {
		t.Error("an empty toolset claims a resource tool")
	}
	if ts.Refresh() {
		t.Error("an empty toolset reported a change")
	}
	if _, err := ts.Execute(ResourceToolName, json.RawMessage(`{"uri":"x://y"}`)); err == nil {
		t.Error("an empty toolset read a resource")
	}
	if _, err := ts.Render(context.Background(), "a:b", nil); err == nil {
		t.Error("an empty toolset rendered a prompt")
	}
	if len(ts.Prompts()) != 0 || len(ts.ReadOnlyDefinitions()) != 0 || len(ts.Gated()) != 0 {
		t.Error("an empty toolset offers something")
	}

	var none *Toolset
	if none.Has("x") || none.ReadOnly("x") || none.Refresh() || len(none.Prompts()) != 0 {
		t.Error("a nil toolset offers something")
	}
	if _, err := none.Render(context.Background(), "a:b", nil); err == nil {
		t.Error("a nil toolset rendered a prompt")
	}
}

// envDumpDefinition is a stdio "server" that writes its environment to path
// and exits. The dial never connects; the file is the answer.
func envDumpDefinition(t *testing.T, path string) Definition {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Definition{
		Name: "dump", Scope: ScopeUser, Transport: TransportStdio,
		Command: exe, Env: map[string]string{envDumpEnv: path},
	}
}

// startedWith is the environment the dumped process actually had.
func startedWith(t *testing.T, def Definition, mask func(string) bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	expanded, missing := def.Expand(nil)
	if len(missing) > 0 {
		t.Fatalf("the definition references unset variables: %v", missing)
	}
	if s, err := Dial(ctx, expanded, mask); err == nil {
		s.Close()
		t.Fatal("a process that only dumps its environment must not connect")
	}
	body, err := os.ReadFile(expanded.Env[envDumpEnv])
	if err != nil {
		t.Fatalf("the process wrote no environment: %v", err)
	}
	return string(body)
}

// The one place shhh spawns a process for someone else's definition used to
// be the one place that process was handed more than the model's own
// commands get. A stdio server now starts from the masked set, and the
// `${NAME}` reference the design already asks for is what puts one back.
func TestDial_AStdioServerStartsFromTheMaskedEnvironment(t *testing.T) {
	t.Setenv("SHHH_FAKE_TOKEN", "sesame")
	t.Setenv("SHHH_FAKE_PLAIN", "kept")

	dir := t.TempDir()
	def := envDumpDefinition(t, filepath.Join(dir, "masked"))
	env := startedWith(t, def, secret.MaskedEnvName)
	if strings.Contains(env, "SHHH_FAKE_TOKEN") {
		t.Error("a server the person did not ask to lend a token to was handed one")
	}
	if !strings.Contains(env, "SHHH_FAKE_PLAIN=kept") {
		t.Error("the mask took a variable that holds no credential by convention")
	}
	if got := WithheldEnv(def, secret.MaskedEnvName); !slices.Contains(got, "SHHH_FAKE_TOKEN") {
		t.Errorf("withheld = %v, and the listing has nothing to show the person", got)
	}

	// The same server, saying which token it needs. An unset reference
	// already keeps it from starting; a set one now has to survive the mask
	// as well, or the reference would be decorative.
	declared := envDumpDefinition(t, filepath.Join(dir, "declared"))
	declared.Env["SHHH_FAKE_TOKEN"] = "${SHHH_FAKE_TOKEN}"
	env = startedWith(t, declared, secret.MaskedEnvName)
	if !strings.Contains(env, "SHHH_FAKE_TOKEN=sesame") {
		t.Error("a server that declared the token it needs was not given it")
	}
	if got := WithheldEnv(declared, secret.MaskedEnvName); slices.Contains(got, "SHHH_FAKE_TOKEN") {
		t.Errorf("withheld = %v, which names a variable the server was given", got)
	}

	// The escape hatch: no mask, the environment whole, and nothing to
	// report as withheld.
	off := envDumpDefinition(t, filepath.Join(dir, "off"))
	if env = startedWith(t, off, nil); !strings.Contains(env, "SHHH_FAKE_TOKEN=sesame") {
		t.Error("mcp.env_mask=false is meant to hand over the environment whole")
	}
	if got := WithheldEnv(off, nil); got != nil {
		t.Errorf("withheld = %v with no mask installed", got)
	}
	// A remote server is no process of shhh's, so nothing is withheld from it.
	remote := Definition{Name: "r", Scope: ScopeUser, Transport: TransportHTTP, URL: "https://example.test/mcp"}
	if got := WithheldEnv(remote, secret.MaskedEnvName); got != nil {
		t.Errorf("withheld = %v for a server shhh starts no process for", got)
	}
}

// The report carries the withheld names whether or not the server answered:
// the reader who needs them most is looking at one that would not start.
func TestConnect_AFailedServerStillReportsWhatWasWithheld(t *testing.T) {
	t.Setenv("SHHH_FAKE_TOKEN", "sesame")
	def := Definition{Name: "ghost", Scope: ScopeUser, Transport: TransportStdio,
		Command: filepath.Join(t.TempDir(), "no-such-server")}
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{def}}, Options{EnvMask: secret.MaskedEnvName})
	defer ts.Close()
	r := ts.Reports[0]
	if r.Status != StatusFailed {
		t.Fatalf("status = %s", r.Status)
	}
	if !slices.Contains(r.Withheld, "SHHH_FAKE_TOKEN") {
		t.Errorf("withheld = %v", r.Withheld)
	}
}

// A definition that names its tools registers those and leaves the rest of
// the server outside the session. The filter runs in one place, so a name
// left out is absent from every table at once: the model is not offered it,
// no card previews it, and a call naming it anyway is an unknown tool.
func TestOnlyTheNamedToolsOfAServerAreRegistered(t *testing.T) {
	def := testDefinition(t)
	def.ReadOnly = false
	def.Tools = []string{"echo"}
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{def}}, Options{})
	defer ts.Close()

	// One tool of the server's own, plus the one tool resources are read
	// through: the server publishes a resource whatever it registers.
	if ts.Len() != 2 || !ts.Has("echo__echo") || ts.Has("echo__fail") || ts.Has("echo__grow") {
		t.Fatalf("toolset = %v", ts.Definitions())
	}
	if got := ts.Gated(); len(got) != 1 || got[0] != "echo__echo" {
		t.Errorf("gated = %v", got)
	}
	if _, _, ok := ts.Lookup("echo__fail"); ok {
		t.Error("a tool the definition left out is still in the lookup table")
	}
	if _, err := ts.Preview("echo__fail", nil); err == nil {
		t.Error("a card previewed a call the session cannot make")
	}
	if _, err := ts.Execute("echo__fail", json.RawMessage(`{"text":"x"}`)); err == nil {
		t.Error("a tool the definition left out was dispatched")
	}
	// The server still holds its whole catalog: `shhh mcp show` is where a
	// person reads a large server to decide what to name.
	s := ts.Reports[0].Server
	if len(s.Tools) != 3 || len(s.RegisteredTools()) != 1 {
		t.Errorf("server holds %d tools, %d registered", len(s.Tools), len(s.RegisteredTools()))
	}
	if block := PromptBlock(ts); !strings.Contains(block, "- echo — 1 tool") {
		t.Errorf("the block counts tools the session does not have:\n%s", block)
	}
}

// The scale the selection exists for: twenty tools listed, two named, and
// only the two in anything the session reads.
func TestALargeServerIsRegisteredInPart(t *testing.T) {
	s := &Server{Definition: Definition{Name: "big", Tools: []string{"t03", "t11"}}}
	for i := range 20 {
		remote := fmt.Sprintf("t%02d", i)
		s.Tools = append(s.Tools, Tool{Name: "big__" + remote, Remote: remote, InputSchema: json.RawMessage(`{}`)})
	}
	ts := &Toolset{servers: map[string]*Server{"big": s}}
	ts.index()
	if ts.Len() != 2 || !ts.Has("big__t03") || !ts.Has("big__t11") {
		t.Fatalf("registered %v", ts.Definitions())
	}
	if got := len(ts.Gated()); got != 2 {
		t.Errorf("gated %d of 20", got)
	}
	s.Definition.Tools = nil
	ts.index()
	if ts.Len() != 20 {
		t.Errorf("a definition that names nothing registers %d of 20", ts.Len())
	}
}

// A server's own words and its resource list are third-party text at the
// most trusted position in the request, and they sit in the opening every
// round repeats. Both are bounded per server, and the cut reads nothing but
// that server's own catalog, so adding a second server does not move the
// first's lines and the cached opening does not churn.
func TestPromptBlockBoundsAServersWordsAndItsResources(t *testing.T) {
	big := &Server{
		Definition:   Definition{Name: "big"},
		Instructions: strings.Repeat("a line of the server's own prose\n", 500),
	}
	for i := range 10000 {
		big.Resources = append(big.Resources, Resource{URI: fmt.Sprintf("docs://p%05d", i)})
	}
	block := promptBlock([]*Server{big})
	if len(block) > 4000 {
		t.Errorf("the block is %d bytes for one server", len(block))
	}
	for _, want := range []string{
		"that server's own words about itself",
		fmt.Sprintf("…and %d more, ask `%s` by uri", 10000-MaxPromptResources, ResourceToolName),
		fmt.Sprintf("ran past %d bytes", MaxInstructionsBytes),
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block lacks %q:\n%s", want, block)
		}
	}
	if got := strings.Count(block, "resource docs://"); got != MaxPromptResources {
		t.Errorf("%d resources listed, want %d", got, MaxPromptResources)
	}
	other := &Server{Definition: Definition{Name: "other"}, Instructions: "short"}
	if two := promptBlock([]*Server{big, other}); !strings.HasPrefix(two, block) {
		t.Errorf("a second server re-cut the first's block:\n%s", two)
	}
	// A cut landing inside a multi-byte rune would put a replacement
	// character at the top of every request; the ellipsis is three bytes,
	// so the cap falls inside one.
	dense := &Server{Definition: Definition{Name: "dense"}, Instructions: strings.Repeat("…", MaxInstructionsBytes)}
	if got := promptBlock([]*Server{dense}); !utf8.ValidString(got) {
		t.Error("the instructions were cut through a rune")
	}
}

// A vendor's snippet is written for clients whose names are labels, and the
// promise is that it works as it is. The JSON catalog renames; nothing is
// silent about it.
func TestJSONCatalogNormalisesAServerName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Framelink Figma MCP", "framelink-figma-mcp"},
		{"github", "github"},
		{"@vendor/mcp_server", "vendor-mcp-server"},
		{"  spaced  out  ", "spaced-out"},
		{"2fa", "fa"},
		{"A very long vendor name for one server", "a-very-long-vendor-name"},
		// Nothing survives, so the name is left as written and the refusal
		// that follows quotes the spelling the file actually holds.
		{"!!!", "!!!"},
	} {
		if got := normaliseName(c.in, map[string]bool{}); got != c.want {
			t.Errorf("%q became %q, want %q", c.in, got, c.want)
		}
	}
	taken := map[string]bool{}
	if a, b := normaliseName("Docs", taken), normaliseName("docs", taken); a != "docs" || b != "docs-2" {
		t.Errorf("two names collided into %q and %q", a, b)
	}
}

// The rename is said in both spellings, the definition loads under the new
// one, and a definition that will not load is reported once: the catalog
// validates, the reader does not, and two validations reported the same
// broken server twice in the same words.
func TestReadJSONRenamesAndTheCatalogValidatesOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, JSONFileName), []byte(`{"mcpServers": {
		"Framelink Figma MCP": {"command": "npx", "args": ["-y", "figma-mcp"], "tools": ["get_file"]},
		"broken": {}
	}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Discover(t.TempDir(), nil, []string{dir})
	d, ok := c.Find("framelink-figma-mcp")
	if !ok || len(d.Tools) != 1 || d.Tools[0] != "get_file" {
		t.Fatalf("servers = %+v", c.Servers)
	}
	joined := strings.Join(c.Diagnostics, "\n")
	for _, want := range []string{`"Framelink Figma MCP" is loaded as "framelink-figma-mcp"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, joined)
		}
	}
	if n := strings.Count(joined, "neither a command nor a url"); n != 1 {
		t.Errorf("the same broken server is reported %d times:\n%s", n, joined)
	}
}

// A table header in the person's own config is a key they wrote and will
// read the refusal about; renaming it would leave them looking for a
// section that answers to something else.
func TestAConfigNameIsRefusedRatherThanRenamed(t *testing.T) {
	c := Discover(t.TempDir(), []Definition{{Name: "Framelink Figma MCP", Transport: TransportStdio, Command: "npx"}}, nil)
	if c.Len() != 0 {
		t.Fatalf("servers = %+v", c.Servers)
	}
	if joined := strings.Join(c.Diagnostics, "\n"); !strings.Contains(joined, "must start with a letter") {
		t.Errorf("diagnostics = %s", joined)
	}
}

// A server killed after a successful connect is noticed at the first call
// that meets it, and every call after that is answered without a round trip
// into a pipe with nothing at the far end. The tools stay registered: the
// model was told about them, and a name that vanished would come back as
// "unknown tool" instead of the sentence saying what happened.
func TestAServerKilledAfterConnectIsNoticedOnceAndAnswersFast(t *testing.T) {
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{testDefinition(t)}}, Options{})
	defer ts.Close()
	s := ts.Reports[0].Server
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		t.Fatal("the server did not start")
	}
	if err := s.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}

	_, err := ts.Execute("echo__echo", json.RawMessage(`{"text":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "no longer running") {
		t.Fatalf("the call that met the dead server = %v", err)
	}
	if s.Dead() == "" {
		t.Fatal("the server was not marked dead")
	}
	// The second call is the one the model makes after reading the first
	// result, and it must cost nothing: no session, no timeout, no wait.
	started := time.Now()
	_, again := ts.Execute("echo__echo", json.RawMessage(`{"text":"hi"}`))
	if again == nil || !strings.Contains(again.Error(), "no longer running") {
		t.Fatalf("the next call = %v", again)
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("the next call took %s; it should not have reached the transport", took)
	}
	if !ts.Has("echo__echo") {
		t.Error("a dead server's tools left the session; the model was told about them")
	}

	if !ts.Refresh() {
		t.Fatal("the death did not move the toolset at the boundary")
	}
	deaths := ts.Deaths()
	if len(deaths) != 1 || deaths[0].Name != "echo" || deaths[0].Reason == "" {
		t.Fatalf("deaths = %+v", deaths)
	}
	// Once. A note at every boundary for the rest of the session is
	// wallpaper, and a rail that keeps reporting news nobody can act on.
	if ts.Refresh() {
		t.Error("the same death moved the toolset a second time")
	}
	if got := ts.Deaths(); len(got) != 0 {
		t.Errorf("deaths were not drained: %+v", got)
	}
}

// A call to a server that answers nothing ends at the definition's own
// bound rather than sitting there for the default two minutes.
func TestAHungCallEndsAtTheConfiguredTimeout(t *testing.T) {
	def := slowDefinition(t)
	def.CallTimeout = 200 * time.Millisecond
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{def}}, Options{})
	defer ts.Close()
	if len(ts.Reports) != 1 || ts.Reports[0].Status != StatusConnected {
		t.Fatalf("connect = %+v", ts.Reports[0])
	}

	started := time.Now()
	_, err := ts.Execute("echo__hang", json.RawMessage(`{"text":"x"}`))
	took := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "did not answer within 200ms") {
		t.Fatalf("a hung call = %v", err)
	}
	if took > 5*time.Second {
		t.Fatalf("the bound was not honoured: the call took %s", took)
	}
	// A call the session gave up on says nothing about whether the server
	// is still there, so the next one is tried the ordinary way.
	if s := ts.Reports[0].Server; s.Dead() != "" {
		t.Errorf("a timed-out call marked the server dead: %s", s.Dead())
	}
}

// The interrupt reaches a call in flight. Without it a person who pressed
// it waits out the whole call timeout on a server that will never answer,
// which is the one wait in this surface nothing else can shorten.
func TestAbandonCallsReachesACallInFlight(t *testing.T) {
	def := slowDefinition(t)
	def.CallTimeout = time.Minute
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{def}}, Options{})
	defer ts.Close()

	done := make(chan error, 1)
	go func() {
		_, err := ts.Execute("echo__hang", json.RawMessage(`{"text":"x"}`))
		done <- err
	}()
	// Wait for the call to be on the register rather than for a duration:
	// a sleep long enough to be reliable is a slow test, and one short
	// enough to be fast is a flake.
	deadline := time.Now().Add(10 * time.Second)
	for {
		ts.mu.Lock()
		out := len(ts.calls)
		ts.mu.Unlock()
		if out == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the call never reached the register AbandonCalls reads")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ts.AbandonCalls()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "was cancelled") {
			t.Fatalf("the abandoned call = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the abandoned call never returned")
	}
	// The count has to come back down, or the toolset never takes another
	// re-listing for the rest of the session.
	ts.mu.Lock()
	inflight, registered := ts.inflight, len(ts.calls)
	ts.mu.Unlock()
	if inflight != 0 || registered != 0 {
		t.Fatalf("after the cancel: inflight=%d registered=%d", inflight, registered)
	}
	// Read the server directly and not through Refresh: a death makes
	// Refresh report movement, so a test that guards on its answer can
	// never fail for the regression it is named after.
	if why := ts.Reports[0].Server.Dead(); why != "" {
		t.Errorf("an abandoned call marked the server dead: %s", why)
	}
}

// The bounds and refusals that had no test. Each is a line the model reads
// when something did not fit or could not be resolved, and each is cheap to
// get wrong in a way nothing else notices.

func TestBoundCapsWhatOneCallFeedsTheModel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"short", "hello", "hello"},
		{"exactly the cap", strings.Repeat("x", MaxResultBytes), strings.Repeat("x", MaxResultBytes)},
		{"one byte over", strings.Repeat("x", MaxResultBytes+1),
			strings.Repeat("x", MaxResultBytes) + "\n… (truncated: the result was longer than 65536 bytes)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bound(c.in); got != c.want {
				t.Errorf("bound(%d bytes) = %d bytes, ending %q", len(c.in), len(got), lastRunes(got, 60))
			}
		})
	}
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// The connect's timeout arm: a process that starts, says nothing and is
// still there when the wait runs out. The session goes on without it.
func TestConnectGivesUpOnAServerThatNeverAnswers(t *testing.T) {
	def := testDefinition(t)
	def.Env = map[string]string{sleepEnv: "3s"}
	def.Timeout = 150 * time.Millisecond
	started := time.Now()
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{def}}, Options{})
	defer ts.Close()
	took := time.Since(started)
	r := ts.Reports[0]
	if r.Status != StatusFailed || !strings.Contains(r.Error, "no answer within 150ms") {
		t.Fatalf("report = %s / %q", r.Status, r.Error)
	}
	if took > 3*time.Second {
		t.Fatalf("the connect waited %s for a server it had given up on", took)
	}
	if ts.Len() != 0 {
		t.Errorf("a server that never answered registered %d tools", ts.Len())
	}
}

// A uri nobody listed resolves by scheme when exactly one server publishes
// under it, and is refused when more than one does: the wrong server is a
// request sent somewhere the reader did not intend.
func TestResolveResourceRefusesAnAmbiguousScheme(t *testing.T) {
	a := &Server{Definition: Definition{Name: "alpha", ReadOnly: true}}
	b := &Server{Definition: Definition{Name: "beta"}}
	ts := &Toolset{
		servers:   map[string]*Server{"alpha": a, "beta": b},
		resources: map[string]*Server{"docs://a": a, "docs://b": b, "wiki://w": b},
	}
	cases := []struct {
		name       string
		uri        string
		readOnly   bool
		wantServer *Server
		wantErr    string
	}{
		{name: "a uri one of them listed", uri: "docs://a", wantServer: a},
		{name: "a scheme only one publishes", uri: "wiki://elsewhere", wantServer: b},
		{name: "a scheme two publish", uri: "docs://elsewhere", wantErr: "could be on any of alpha, beta"},
		{name: "a scheme nobody publishes", uri: "tickets://7", wantErr: "no connected server publishes"},
		{name: "the ambiguity resolves when only one server is admitted",
			uri: "docs://elsewhere", readOnly: true, wantServer: a},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ts.resolveResource(c.uri, c.readOnly)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				if ts.inflight != 0 {
					t.Fatalf("a refused resolve left %d calls in flight", ts.inflight)
				}
				return
			}
			if err != nil || got != c.wantServer {
				t.Fatalf("resolve = %v, %v", got, err)
			}
			ts.mu.Lock()
			ts.inflight--
			ts.mu.Unlock()
		})
	}
}

// Every kind of block the model cannot read comes back as one line saying
// what was left out and how big it was, whether it arrived in a tool result
// or a resource read: a base64 blob in the context costs more than the
// notice and says less.
func TestBinaryBlocksBecomeANotice(t *testing.T) {
	t.Run("a tool result", func(t *testing.T) {
		cases := []struct {
			name string
			in   []sdk.Content
			want string
		}{
			{"audio", []sdk.Content{&sdk.AudioContent{MIMEType: "audio/wav", Data: make([]byte, 3072)}},
				"[audio omitted: audio/wav, 3.0 kB]"},
			{"audio beside text", []sdk.Content{
				&sdk.TextContent{Text: "here it is"},
				&sdk.AudioContent{MIMEType: "audio/mpeg", Data: make([]byte, 1<<21)},
			}, "here it is\n[audio omitted: audio/mpeg, 2.0 MB]"},
			{"an embedded blob", []sdk.Content{
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{
					URI: "file:///a.bin", MIMEType: "application/octet-stream", Blob: make([]byte, 512),
				}},
			}, "[resource omitted: file:///a.bin, application/octet-stream, 512 B]"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if got := Flatten(&sdk.CallToolResult{Content: c.in}); got != c.want {
					t.Errorf("Flatten = %q, want %q", got, c.want)
				}
			})
		}
	})

	t.Run("a resource read", func(t *testing.T) {
		cases := []struct {
			name string
			in   []*sdk.ResourceContents
			want string
		}{
			{"a blob", []*sdk.ResourceContents{
				{URI: "docs://cover", MIMEType: "image/png", Blob: make([]byte, 4096)},
			}, "[resource omitted: docs://cover, image/png, 4.0 kB]"},
			{"text beside a blob", []*sdk.ResourceContents{
				{URI: "docs://guide", MIMEType: "text/markdown", Text: "the guide"},
				{URI: "docs://cover", MIMEType: "image/png", Blob: make([]byte, 1024)},
			}, "the guide\n[resource omitted: docs://cover, image/png, 1.0 kB]"},
			{"nothing at all", []*sdk.ResourceContents{nil, {URI: "docs://empty"}}, ""},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if got := FlattenResource(&sdk.ReadResourceResult{Contents: c.in}); got != c.want {
					t.Errorf("FlattenResource = %q, want %q", got, c.want)
				}
			})
		}
	})
}
