package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rfizzle/shhh/internal/logs"
)

// The test binary doubles as a stdio MCP server: run with the environment
// variable set, it serves two tools over its own stdin and stdout, which is
// exactly what a definition naming os.Args[0] spawns. No fixture binary to
// build, no network.
const serverEnv = "SHHH_MCP_TEST_SERVER"

func TestMain(m *testing.M) {
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

func TestDialListsAndCallsTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := Dial(ctx, testDefinition(t))
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
	if len(s.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(s.Tools))
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
	if ts.Len() != 2 || !ts.Has("echo__echo") || !ts.ReadOnly("echo__fail") {
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
	for _, want := range []string{"# MCP servers", "- echo — 2 tools, read-only (echo-server 1.2.3)", "> Call echo with anything."} {
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
	if ts.Len() != 2 || len(ts.ReadOnlyDefinitions()) != 0 {
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
		{strings.Repeat("x", 80), "gh__" + strings.Repeat("x", 60)},
		{strings.Repeat("x", 81), "gh__" + strings.Repeat("x", 58) + "_2"},
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
	if got := strings.Join(names, " "); got != "cfg:user docs:user shared:project" {
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
	for _, want := range []string{"Bad Name", "read-only is ignored in a project file", "neither a command nor a url", "not a valid catalog", "server shared shadows your own definition"} {
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
	if _, err := Dial(ctx, def); err == nil {
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
	if _, err := Dial(ctx, def); err == nil {
		t.Fatal("a cancelled dial must not report a connected server")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		body, _ := os.ReadFile(path)
		t.Errorf("a cancelled dial wrote a log line: %v, %s", err, body)
	}
}
