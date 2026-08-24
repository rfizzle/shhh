package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLS is an in-process language server speaking framed JSON-RPC over
// pipes, substituted for a real binary via Options.connect.
type fakeLS struct {
	mu sync.Mutex
	w  io.Writer

	// ignoreShutdown makes the server never answer the shutdown request.
	ignoreShutdown bool
	// noPublish suppresses publishDiagnostics entirely.
	noPublish bool
	// diagsFor decides what didOpen/didChange publishes for a file's content.
	diagsFor func(content string) []Diagnostic
	// definitionResult / referencesResult are returned verbatim.
	definitionResult json.RawMessage
	referencesResult json.RawMessage

	connects int
	synced   []string // methods received for file sync, in order
}

func (f *fakeLS) connect(spec ServerSpec, root string) (*transport, error) {
	f.mu.Lock()
	f.connects++
	f.mu.Unlock()
	clientOutR, clientOutW := io.Pipe() // client → server
	serverOutR, serverOutW := io.Pipe() // server → client
	go f.serve(clientOutR, serverOutW)
	return &transport{
		in:  clientOutW,
		out: serverOutR,
		kill: func() {
			_ = clientOutR.Close()
			_ = serverOutW.Close()
		},
	}, nil
}

func (f *fakeLS) send(msg rpcMessage) {
	msg.JSONRPC = "2.0"
	body, _ := json.Marshal(msg)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.w == nil {
		return
	}
	fmt.Fprintf(f.w, "Content-Length: %d\r\n\r\n", len(body))
	_, _ = f.w.Write(body)
}

func (f *fakeLS) serve(r io.Reader, w io.WriteCloser) {
	f.mu.Lock()
	f.w = w
	f.mu.Unlock()
	br := bufio.NewReader(r)
	for {
		msg, err := readFrame(br)
		if err != nil {
			_ = w.Close()
			return
		}
		switch msg.Method {
		case "initialize":
			f.send(rpcMessage{ID: msg.ID, Result: json.RawMessage(`{"capabilities":{}}`)})
		case "initialized", "exit":
		case "textDocument/didOpen", "textDocument/didChange":
			f.mu.Lock()
			f.synced = append(f.synced, msg.Method)
			f.mu.Unlock()
			f.publishFor(msg)
		case "textDocument/definition":
			f.send(rpcMessage{ID: msg.ID, Result: f.definitionResult})
		case "textDocument/references":
			f.send(rpcMessage{ID: msg.ID, Result: f.referencesResult})
		case "shutdown":
			if !f.ignoreShutdown {
				f.send(rpcMessage{ID: msg.ID, Result: json.RawMessage("null")})
			}
		default:
			if msg.ID != nil {
				f.send(rpcMessage{ID: msg.ID, Result: json.RawMessage("null")})
			}
		}
	}
}

func (f *fakeLS) publishFor(msg rpcMessage) {
	if f.noPublish {
		return
	}
	var p struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	content := p.TextDocument.Text
	if len(p.ContentChanges) > 0 {
		content = p.ContentChanges[0].Text
	}
	var diags []Diagnostic
	if f.diagsFor != nil {
		diags = f.diagsFor(content)
	}
	if diags == nil {
		diags = []Diagnostic{}
	}
	body, _ := json.Marshal(map[string]any{
		"uri":         p.TextDocument.URI,
		"diagnostics": diags,
	})
	f.send(rpcMessage{Method: "textDocument/publishDiagnostics", Params: body})
}

// testManager builds a manager over a temp workspace with the fake server
// owning .go files.
func testManager(t *testing.T, fake *fakeLS, opts Options) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	opts.connect = fake.connect
	if opts.RequestTimeout == 0 {
		opts.RequestTimeout = 5 * time.Second
	}
	if opts.DiagnosticsTimeout == 0 {
		opts.DiagnosticsTimeout = 5 * time.Second
	}
	m := NewManager(root, []ServerSpec{{Name: "gopls", Command: "gopls", Extensions: []string{".go"}}}, opts)
	t.Cleanup(m.Shutdown)
	return m, root
}

func writeWorkspaceFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiagnosticsAfterChange_ErrorsFirstAndBounded(t *testing.T) {
	fake := &fakeLS{diagsFor: func(content string) []Diagnostic {
		if !strings.Contains(content, "BROKEN") {
			return nil
		}
		// Deliberately warning-first: formatting must reorder errors first.
		return []Diagnostic{
			{Range: Range{Start: Position{Line: 1, Character: 2}}, Severity: 2, Message: "unused variable", Source: "gopls"},
			{Range: Range{Start: Position{Line: 4, Character: 0}}, Severity: 1, Message: "undefined:  broken\nsymbol", Source: "gopls"},
			{Range: Range{Start: Position{Line: 9, Character: 1}}, Severity: 4, Message: "hint here"},
		}
	}}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "main.go", "package main // BROKEN\n")

	out := m.DiagnosticsAfterChange(path)
	if !strings.Contains(out, "Diagnostics (gopls) for main.go:") {
		t.Fatalf("missing header: %q", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected header + 3 diagnostics, got %q", out)
	}
	// Errors first, 1-based positions, whitespace-collapsed message.
	if !strings.Contains(lines[1], "main.go:5:1 error: undefined: broken symbol (gopls)") {
		t.Fatalf("error should be first with 1-based position: %q", lines[1])
	}
	if !strings.Contains(lines[2], "main.go:2:3 warning: unused variable") {
		t.Fatalf("warning should follow: %q", lines[2])
	}
	if !strings.Contains(lines[3], "hint: hint here") {
		t.Fatalf("hint should be last: %q", lines[3])
	}
}

func TestDiagnosticsAfterChange_CleanFileIsSilent(t *testing.T) {
	fake := &fakeLS{diagsFor: func(string) []Diagnostic { return nil }}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "ok.go", "package main\n")
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("clean file should produce no diagnostics block, got %q", out)
	}
}

func TestDiagnosticsAfterChange_TruncatesAtCap(t *testing.T) {
	fake := &fakeLS{diagsFor: func(string) []Diagnostic {
		items := make([]Diagnostic, MaxDiagnostics+5)
		for i := range items {
			items[i] = Diagnostic{Range: Range{Start: Position{Line: i}}, Severity: 1, Message: fmt.Sprintf("e%d", i)}
		}
		return items
	}}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "big.go", "package main\n")
	out := m.DiagnosticsAfterChange(path)
	if !strings.Contains(out, "… and 5 more") {
		t.Fatalf("expected elision note, got %q", out)
	}
	if got := strings.Count(out, "\n"); got != MaxDiagnostics+1 {
		t.Fatalf("expected %d lines after header, got %d", MaxDiagnostics+1, got)
	}
}

func TestDiagnosticsAfterChange_NoServerForExtensionIsNoOp(t *testing.T) {
	fake := &fakeLS{}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "notes.txt", "hello\n")
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("uncovered extension should be a no-op, got %q", out)
	}
	if fake.connects != 0 {
		t.Fatal("no server should have started for an uncovered extension")
	}
}

func TestDiagnosticsAfterChange_SilentServerTimesOutQuietly(t *testing.T) {
	fake := &fakeLS{noPublish: true}
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 100 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")
	start := time.Now()
	out := m.DiagnosticsAfterChange(path)
	if out != "" {
		t.Fatalf("expected quiet timeout, got %q", out)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

func TestManager_ServerStartsLazilyAndOnce(t *testing.T) {
	fake := &fakeLS{}
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 50 * time.Millisecond})
	if fake.connects != 0 {
		t.Fatal("no server should start before first use")
	}
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")
	m.DiagnosticsAfterChange(path)
	m.DiagnosticsAfterChange(path)
	if fake.connects != 1 {
		t.Fatalf("server should start exactly once, started %d times", fake.connects)
	}
	// First sync opens, second changes.
	fake.mu.Lock()
	synced := append([]string{}, fake.synced...)
	fake.mu.Unlock()
	if len(synced) != 2 || synced[0] != "textDocument/didOpen" || synced[1] != "textDocument/didChange" {
		t.Fatalf("expected didOpen then didChange, got %v", synced)
	}
}

func TestManager_StartFailureBecomesNoOp(t *testing.T) {
	connects := 0
	m := NewManager(t.TempDir(), []ServerSpec{{Name: "gopls", Command: "gopls", Extensions: []string{".go"}}}, Options{
		RequestTimeout:     time.Second,
		DiagnosticsTimeout: time.Second,
		connect: func(ServerSpec, string) (*transport, error) {
			connects++
			return nil, fmt.Errorf("binary vanished")
		},
	})
	path := writeWorkspaceFile(t, m.root, "main.go", "package main\n")
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("failed server should be silent, got %q", out)
	}
	if _, err := m.Definition(path, 1, "main"); err == nil {
		t.Fatal("navigation against a failed server should error")
	}
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatal("failed server should stay silent")
	}
	if connects != 1 {
		t.Fatalf("failed start must not retry, connected %d times", connects)
	}
}

func TestToolset_DefinitionFormatsFileLine(t *testing.T) {
	fake := &fakeLS{}
	m, root := testManager(t, fake, Options{})
	writeWorkspaceFile(t, root, "other.go", "package main\nfunc target() {}\n")
	path := writeWorkspaceFile(t, root, "main.go", "package main\nfunc main() { target() }\n")
	fake.definitionResult, _ = json.Marshal(Location{
		URI:   pathToURI(filepath.Join(root, "other.go")),
		Range: Range{Start: Position{Line: 1, Character: 5}},
	})

	ts := NewToolset(m)
	args, _ := json.Marshal(map[string]any{"path": path, "line": 2, "symbol": "target"})
	out, err := ts.Execute(DefinitionToolName, args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "other.go:2") {
		t.Fatalf("definition should be a 1-based file:line reference, got %q", out)
	}
}

func TestToolset_ReferencesBounded(t *testing.T) {
	fake := &fakeLS{}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "main.go", "package main\nvar count int\n")
	locs := make([]Location, MaxReferences+10)
	for i := range locs {
		locs[i] = Location{URI: pathToURI(path), Range: Range{Start: Position{Line: i}}}
	}
	fake.referencesResult, _ = json.Marshal(locs)

	ts := NewToolset(m)
	args, _ := json.Marshal(map[string]any{"path": path, "line": 2, "symbol": "count"})
	out, err := ts.Execute(ReferencesToolName, args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, fmt.Sprintf("… and 10 more (truncated at %d)", MaxReferences)) {
		t.Fatalf("references should be bounded with a note, got tail %q", out[len(out)-80:])
	}
	if !strings.Contains(out, fmt.Sprintf("%d references for %q:", MaxReferences+10, "count")) {
		t.Fatalf("references header should carry the total, got %q", strings.SplitN(out, "\n", 2)[0])
	}
}

func TestToolset_NoResultsMessage(t *testing.T) {
	fake := &fakeLS{definitionResult: json.RawMessage("null")}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")
	ts := NewToolset(m)
	args, _ := json.Marshal(map[string]any{"path": path, "line": 1, "symbol": "main"})
	out, err := ts.Execute(DefinitionToolName, args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No definition found") {
		t.Fatalf("empty result should say so, got %q", out)
	}
}

func TestToolset_ArgumentValidation(t *testing.T) {
	ts := NewToolset(NewManager(t.TempDir(), nil, Options{}))
	cases := []string{
		`{"line":1,"symbol":"x"}`,
		`{"path":"a.go","symbol":"x"}`,
		`{"path":"a.go","line":0,"symbol":"x"}`,
		`{"path":"a.go","line":1,"symbol":" "}`,
	}
	for _, args := range cases {
		if _, err := ts.Execute(DefinitionToolName, json.RawMessage(args)); err == nil {
			t.Fatalf("args %s should be rejected", args)
		}
	}
}

func TestManager_SymbolNotOnLine(t *testing.T) {
	fake := &fakeLS{}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "main.go", "package main\nfunc main() {}\n")
	if _, err := m.Definition(path, 1, "nowhere"); err == nil || !strings.Contains(err.Error(), "not found on line") {
		t.Fatalf("expected symbol-not-found error, got %v", err)
	}
	if _, err := m.Definition(path, 99, "main"); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("expected out-of-range error, got %v", err)
	}
}

func TestManager_ShutdownWithHungServerTimesOut(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 100 * time.Millisecond
	defer func() { shutdownTimeout = old }()

	fake := &fakeLS{ignoreShutdown: true, noPublish: true}
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 50 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")
	m.DiagnosticsAfterChange(path) // start the server

	done := make(chan struct{})
	go func() {
		m.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown wedged on a server that ignores the shutdown request")
	}
}

func TestManager_ShutdownPreventsLaterStarts(t *testing.T) {
	fake := &fakeLS{}
	m, root := testManager(t, fake, Options{})
	m.Shutdown()
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("no server should start after shutdown, got %q", out)
	}
	if fake.connects != 0 {
		t.Fatal("shutdown must prevent later lazy starts")
	}
}

func TestDetectServers_OnlyFindsPresent(t *testing.T) {
	specs := detectServers(func(name string) (string, error) {
		if name == "gopls" {
			return "/usr/bin/gopls", nil
		}
		return "", fmt.Errorf("not found")
	})
	if len(specs) != 1 || specs[0].Name != "gopls" {
		t.Fatalf("expected only gopls, got %+v", specs)
	}
	none := detectServers(func(string) (string, error) { return "", fmt.Errorf("not found") })
	if len(none) != 0 {
		t.Fatalf("expected no servers, got %+v", none)
	}
}

func TestConn_CallTimesOutAgainstSilentPeer(t *testing.T) {
	clientOutR, clientOutW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()
	defer serverOutW.Close()
	// Drain the client's writes; the peer just never answers.
	go func() { _, _ = io.Copy(io.Discard, clientOutR) }()
	c := newConn(clientOutW, serverOutR, nil)
	start := time.Now()
	_, err := c.call("initialize", nil, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout fired too late")
	}
}

func TestConn_AnswersServerConfigurationRequest(t *testing.T) {
	clientOutR, clientOutW := io.Pipe() // client → peer
	serverOutR, serverOutW := io.Pipe() // peer → client
	newConn(clientOutW, serverOutR, nil)

	// Peer sends a workspace/configuration request with two items…
	go func() {
		body := `{"jsonrpc":"2.0","id":7,"method":"workspace/configuration","params":{"items":[{},{}]}}`
		fmt.Fprintf(serverOutW, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}()

	// …and must get two nulls back.
	br := bufio.NewReader(clientOutR)
	type frame struct {
		msg rpcMessage
		err error
	}
	got := make(chan frame, 1)
	go func() {
		msg, err := readFrame(br)
		got <- frame{msg, err}
	}()
	select {
	case f := <-got:
		if f.err != nil {
			t.Fatal(f.err)
		}
		if string(f.msg.ID) != "7" || string(f.msg.Result) != "[null,null]" {
			t.Fatalf("expected [null,null] reply to id 7, got id=%s result=%s", f.msg.ID, f.msg.Result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply to server request")
	}
}

func TestUTF16Column(t *testing.T) {
	line := "π := 𝜋2" // "π := " is 5 UTF-16 units, 𝜋 is a surrogate pair (2)
	idx := strings.Index(line, "2")
	if got := utf16Column(line, idx); got != 7 {
		t.Fatalf("expected UTF-16 column 7, got %d", got)
	}
	if got := utf16Column("abc", 99); got != 3 {
		t.Fatalf("out-of-range offset should clamp, got %d", got)
	}
}

func TestLanguageID(t *testing.T) {
	cases := map[string]string{
		"a.go": "go", "b.rs": "rust", "c.ts": "typescript", "d.jsx": "javascript",
		"e.py": "python", "f.txt": "plaintext",
	}
	for path, want := range cases {
		if got := languageID(path); got != want {
			t.Fatalf("languageID(%s) = %s, want %s", path, got, want)
		}
	}
}

func TestParseLocations_AllShapes(t *testing.T) {
	if locs := parseLocations(json.RawMessage("null")); locs != nil {
		t.Fatal("null should parse to no locations")
	}
	one := parseLocations(json.RawMessage(`{"uri":"file:///a.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}}}`))
	if len(one) != 1 || one[0].URI != "file:///a.go" {
		t.Fatalf("single location: %+v", one)
	}
	many := parseLocations(json.RawMessage(`[{"uri":"file:///a.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}}}]`))
	if len(many) != 1 {
		t.Fatalf("location array: %+v", many)
	}
	links := parseLocations(json.RawMessage(`[{"targetUri":"file:///b.go","targetSelectionRange":{"start":{"line":3,"character":0},"end":{"line":3,"character":0}}}]`))
	if len(links) != 1 || links[0].URI != "file:///b.go" || links[0].Range.Start.Line != 3 {
		t.Fatalf("location links: %+v", links)
	}
}
