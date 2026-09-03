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
	// publishDelay holds each publishDiagnostics back, standing in for a
	// server whose first load outlasts the wait an edit gives it. A delayed
	// publication waits on its own goroutine so the serve loop keeps reading:
	// the pipes are unbuffered, and a loop asleep mid-publish would block the
	// client's next sync rather than merely delay the answer to it.
	publishDelay time.Duration
	// diagsFor decides what didOpen/didChange publishes for a file's content.
	diagsFor func(content string) []Diagnostic
	// definitionResult / referencesResult are returned verbatim.
	definitionResult json.RawMessage
	referencesResult json.RawMessage
	// The three capability-gated results, also returned verbatim.
	workspaceSymbolResult json.RawMessage
	documentSymbolResult  json.RawMessage
	hoverResult           json.RawMessage
	// caps is the capabilities object of the initialize result; the zero
	// value advertises nothing, which is what a server that answers only
	// definition and references looks like on the wire.
	caps string

	connects int
	synced   []string // methods received for file sync, in order
	asked    []string // request methods received, in order
}

// allCapabilities advertises every gated provider, in the three spellings the
// spec allows for "yes".
const allCapabilities = `{"workspaceSymbolProvider":true,"documentSymbolProvider":{"label":"gopls"},"hoverProvider":{"workDoneProgress":true}}`

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

// slowDown makes every publication from here on arrive after d.
func (f *fakeLS) slowDown(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishDelay = d
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
		if msg.ID != nil {
			f.mu.Lock()
			f.asked = append(f.asked, msg.Method)
			f.mu.Unlock()
		}
		switch msg.Method {
		case "initialize":
			caps := f.caps
			if caps == "" {
				caps = "{}"
			}
			f.send(rpcMessage{ID: msg.ID, Result: json.RawMessage(`{"capabilities":` + caps + `}`)})
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
		case "workspace/symbol":
			f.send(rpcMessage{ID: msg.ID, Result: f.workspaceSymbolResult})
		case "textDocument/documentSymbol":
			f.send(rpcMessage{ID: msg.ID, Result: f.documentSymbolResult})
		case "textDocument/hover":
			f.send(rpcMessage{ID: msg.ID, Result: f.hoverResult})
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
	f.mu.Lock()
	delay := f.publishDelay
	f.mu.Unlock()
	notice := rpcMessage{Method: "textDocument/publishDiagnostics", Params: body}
	if delay <= 0 {
		f.send(notice)
		return
	}
	go func() {
		time.Sleep(delay)
		f.send(notice)
	}()
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

// waitForPublishes blocks until the client has taken in at least n
// publications for path. Waiting on what the server sent would order a test
// against the wrong side of the pipe: the notification is on its way, and the
// sequence a held question is measured against has not moved yet.
func waitForPublishes(t *testing.T, m *Manager, path string, n int64) {
	t.Helper()
	srv, _ := m.serverFor(m.abs(path))
	if srv == nil {
		t.Fatalf("no server covers %s", path)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if srv.publishSeq() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("client never saw %d publications (saw %d)", n, srv.publishSeq())
}

// waitForHeld polls until a late answer has been collected, and returns it.
func waitForHeld(t *testing.T, m *Manager) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if held := m.TakeHeldDiagnostics(); held != "" {
			return held
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("a late answer never reached the held set")
	return ""
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

// symbolJSON renders a flat SymbolInformation entry, the shape both symbol
// requests are allowed to answer in.
func symbolJSON(name, container string, kind int, uri string, line int) string {
	return fmt.Sprintf(`{"name":%q,"containerName":%q,"kind":%d,"location":{"uri":%q,"range":{"start":{"line":%d,"character":0},"end":{"line":%d,"character":0}}}}`,
		name, container, kind, uri, line, line)
}

func TestToolset_WorkspaceSymbolFormatsMatches(t *testing.T) {
	fake := &fakeLS{caps: allCapabilities}
	m, root := testManager(t, fake, Options{})
	target := pathToURI(filepath.Join(root, "target.go"))
	fake.workspaceSymbolResult = json.RawMessage("[" +
		symbolJSON("Toolset", "", 23, target, 19) + "," +
		symbolJSON("Execute", "Toolset", 6, target, 41) + "]")

	ts := NewToolset(m)
	args, _ := json.Marshal(map[string]any{"query": "Toolset"})
	out, err := ts.Execute(WorkspaceSymbolToolName, args)
	if err != nil {
		t.Fatal(err)
	}
	want := "2 symbols matching \"Toolset\":\ntarget.go:20 struct Toolset\ntarget.go:42 method Toolset.Execute"
	if out != want {
		t.Fatalf("workspace symbols should be kinded file:line references:\ngot  %q\nwant %q", out, want)
	}
}

func TestManager_WorkspaceSymbolMergesEveryIndexingServer(t *testing.T) {
	root := t.TempDir()
	goServer := &fakeLS{caps: allCapabilities}
	// The Python server indexes nothing: it must be asked for nothing and
	// must not take the whole search down with it.
	pyServer := &fakeLS{caps: `{"hoverProvider":true}`}
	goServer.workspaceSymbolResult = json.RawMessage("[" + symbolJSON("Handle", "", 12, pathToURI(filepath.Join(root, "a.go")), 4) + "]")
	pyServer.workspaceSymbolResult = json.RawMessage("[" + symbolJSON("handle", "", 12, pathToURI(filepath.Join(root, "b.py")), 8) + "]")

	fakes := map[string]*fakeLS{"gopls": goServer, "pyright": pyServer}
	m := NewManager(root, []ServerSpec{
		{Name: "gopls", Command: "gopls", Extensions: []string{".go"}},
		{Name: "pyright", Command: "pyright", Extensions: []string{".py"}},
	}, Options{
		RequestTimeout:     5 * time.Second,
		DiagnosticsTimeout: 5 * time.Second,
		connect: func(spec ServerSpec, root string) (*transport, error) {
			return fakes[spec.Name].connect(spec, root)
		},
	})
	t.Cleanup(m.Shutdown)

	out, err := m.WorkspaceSymbol("handle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 symbol matching") || !strings.Contains(out, "a.go:5 function Handle") {
		t.Fatalf("the indexing server's hit should be the whole answer, got %q", out)
	}
	if strings.Contains(out, "b.py") {
		t.Fatalf("a server that does not advertise the search must not be asked, got %q", out)
	}
	pyServer.mu.Lock()
	asked := append([]string{}, pyServer.asked...)
	pyServer.mu.Unlock()
	for _, method := range asked {
		if method == "workspace/symbol" {
			t.Fatal("an unadvertised request was sent anyway")
		}
	}
}

func TestToolset_DocumentSymbolOutlinesHierarchy(t *testing.T) {
	fake := &fakeLS{caps: allCapabilities}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "outline.go", "package main\n\ntype Toolset struct{}\n")
	fake.documentSymbolResult = json.RawMessage(`[
		{"name":"Toolset","kind":23,
		 "range":{"start":{"line":2,"character":0},"end":{"line":4,"character":1}},
		 "selectionRange":{"start":{"line":2,"character":5},"end":{"line":2,"character":12}},
		 "children":[
			{"name":"Manager","kind":8,
			 "range":{"start":{"line":3,"character":1},"end":{"line":3,"character":18}},
			 "selectionRange":{"start":{"line":3,"character":1},"end":{"line":3,"character":8}}}
		 ]},
		{"name":"NewToolset","kind":12,
		 "range":{"start":{"line":6,"character":0},"end":{"line":8,"character":1}},
		 "selectionRange":{"start":{"line":6,"character":5},"end":{"line":6,"character":15}}}
	]`)

	ts := NewToolset(m)
	args, _ := json.Marshal(map[string]any{"path": path})
	out, err := ts.Execute(DocumentSymbolToolName, args)
	if err != nil {
		t.Fatal(err)
	}
	want := "Outline of outline.go (3 symbols):\n3 struct Toolset\n4   field Manager\n7 function NewToolset"
	if out != want {
		t.Fatalf("outline should nest and carry kinds:\ngot  %q\nwant %q", out, want)
	}
	// The outline is asked about what is on disk, so the file is synced first.
	fake.mu.Lock()
	synced := len(fake.synced)
	fake.mu.Unlock()
	if synced == 0 {
		t.Fatal("a document request should sync the file first")
	}
}

func TestToolset_HoverFlattensMarkdown(t *testing.T) {
	fake := &fakeLS{caps: allCapabilities}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "main.go", "package main\nfunc target() {}\n")
	fake.hoverResult = json.RawMessage(`{"contents":{"kind":"markdown","value":"` +
		"```go\\nfunc target()\\n```\\n\\n---\\n\\n### Target\\n\\ntarget calls `run` twice.\\n\\n\\nSecond paragraph." +
		`"}}`)

	ts := NewToolset(m)
	args, _ := json.Marshal(map[string]any{"path": path, "line": 2, "symbol": "target"})
	out, err := ts.Execute(HoverToolName, args)
	if err != nil {
		t.Fatal(err)
	}
	want := "main.go:2 target\nfunc target()\n\nTarget\n\ntarget calls run twice.\n\nSecond paragraph."
	if out != want {
		t.Fatalf("hover should reach the model as prose:\ngot  %q\nwant %q", out, want)
	}
}

func TestToolset_HoverBoundedByLines(t *testing.T) {
	fake := &fakeLS{caps: allCapabilities}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "main.go", "package main\nfunc target() {}\n")
	lines := make([]string, MaxHoverLines+4)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	body, _ := json.Marshal(map[string]any{"contents": strings.Join(lines, "\n")})
	fake.hoverResult = body

	ts := NewToolset(m)
	args, _ := json.Marshal(map[string]any{"path": path, "line": 2, "symbol": "target"})
	out, err := ts.Execute(HoverToolName, args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, fmt.Sprintf("… and 4 more lines (truncated at %d)", MaxHoverLines)) {
		t.Fatalf("hover should be bounded with a notice, got %q", out)
	}
}

func TestToolset_SymbolResultsBounded(t *testing.T) {
	fake := &fakeLS{caps: allCapabilities}
	m, root := testManager(t, fake, Options{})
	uri := pathToURI(filepath.Join(root, "big.go"))
	entries := make([]string, MaxSymbols+7)
	for i := range entries {
		entries[i] = symbolJSON(fmt.Sprintf("Sym%d", i), "", 12, uri, i)
	}
	fake.workspaceSymbolResult = json.RawMessage("[" + strings.Join(entries, ",") + "]")

	out, err := m.WorkspaceSymbol("Sym")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, fmt.Sprintf("%d symbols matching", MaxSymbols+7)) {
		t.Fatalf("the header should carry the total, got %q", strings.SplitN(out, "\n", 2)[0])
	}
	if !strings.Contains(out, fmt.Sprintf("… and 7 more (truncated at %d)", MaxSymbols)) {
		t.Fatalf("symbols should be bounded with a notice, got tail %q", out[len(out)-60:])
	}
}

// A server that advertises two of the three refuses the third by name and
// keeps answering the two — the tool stays registered either way, because
// which questions a server takes is not known until it has started.
func TestManager_UnadvertisedQuestionIsRefusedByName(t *testing.T) {
	fake := &fakeLS{caps: `{"documentSymbolProvider":true,"hoverProvider":true}`}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "main.go", "package main\nfunc target() {}\n")
	fake.documentSymbolResult = json.RawMessage("[" + symbolJSON("target", "", 12, pathToURI(path), 1) + "]")
	fake.hoverResult = json.RawMessage(`{"contents":"func target()"}`)
	ts := NewToolset(m)

	query, _ := json.Marshal(map[string]any{"query": "target"})
	_, err := ts.Execute(WorkspaceSymbolToolName, query)
	if err == nil || !strings.Contains(err.Error(), WorkspaceSymbolToolName) {
		t.Fatalf("an unsupported question should be refused by name, got %v", err)
	}

	outline, _ := json.Marshal(map[string]any{"path": path})
	if _, err := ts.Execute(DocumentSymbolToolName, outline); err != nil {
		t.Fatalf("the supported questions must still be answered: %v", err)
	}
	position, _ := json.Marshal(map[string]any{"path": path, "line": 2, "symbol": "target"})
	if _, err := ts.Execute(HoverToolName, position); err != nil {
		t.Fatalf("the supported questions must still be answered: %v", err)
	}

	fake.mu.Lock()
	asked := append([]string{}, fake.asked...)
	fake.mu.Unlock()
	for _, method := range asked {
		if method == "workspace/symbol" {
			t.Fatal("a refused question must never reach the wire")
		}
	}
}

// A response the parsers do not recognise is an empty answer, not an error:
// the server said something, it just said nothing useful.
func TestToolset_UnrecognisedResponseShapesAreEmptyAnswers(t *testing.T) {
	unknown := json.RawMessage(`{"unexpected":true}`)
	fake := &fakeLS{
		caps:                  allCapabilities,
		workspaceSymbolResult: unknown,
		documentSymbolResult:  unknown,
		hoverResult:           unknown,
	}
	m, root := testManager(t, fake, Options{})
	path := writeWorkspaceFile(t, root, "main.go", "package main\nfunc target() {}\n")
	ts := NewToolset(m)

	for _, tc := range []struct {
		tool, args, want string
	}{
		{WorkspaceSymbolToolName, `{"query":"target"}`, "No symbols matching"},
		{DocumentSymbolToolName, fmt.Sprintf(`{"path":%q}`, path), "No symbols in"},
		{HoverToolName, fmt.Sprintf(`{"path":%q,"line":2,"symbol":"target"}`, path), "No hover information"},
	} {
		out, err := ts.Execute(tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Fatalf("%s: %v", tc.tool, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s should say it found nothing, got %q", tc.tool, out)
		}
	}
}

func TestToolset_DefinitionsMatchWhatItDispatches(t *testing.T) {
	ts := NewToolset(NewManager(t.TempDir(), nil, Options{}))
	defs := ts.Definitions()
	if len(defs) != 6 {
		t.Fatalf("expected six language-server tools, got %d", len(defs))
	}
	for _, def := range defs {
		if !ts.Has(def.Name) {
			t.Fatalf("%s is offered but not dispatched", def.Name)
		}
		if !json.Valid(def.Parameters) {
			t.Fatalf("%s has an invalid schema", def.Name)
		}
	}
	if ts.Has("read_file") {
		t.Fatal("the toolset should claim only its own tools")
	}
}

// Nothing is registered when no server was detected, so this is the manager
// the toolset would be built over if that no-op ever slipped: every tool has
// to refuse cleanly rather than hang or panic.
func TestToolset_NoServerDetectedRefusesEveryTool(t *testing.T) {
	m := NewManager(t.TempDir(), nil, Options{})
	t.Cleanup(m.Shutdown)
	path := filepath.Join(m.root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := NewToolset(m)
	for _, tc := range []struct{ tool, args string }{
		{DefinitionToolName, fmt.Sprintf(`{"path":%q,"line":1,"symbol":"main"}`, path)},
		{ReferencesToolName, fmt.Sprintf(`{"path":%q,"line":1,"symbol":"main"}`, path)},
		{HoverToolName, fmt.Sprintf(`{"path":%q,"line":1,"symbol":"main"}`, path)},
		{DocumentSymbolToolName, fmt.Sprintf(`{"path":%q}`, path)},
		{WorkspaceSymbolToolName, `{"query":"main"}`},
		{DiagnosticsToolName, fmt.Sprintf(`{"path":%q}`, path)},
		{DiagnosticsToolName, `{}`},
	} {
		if _, err := ts.Execute(tc.tool, json.RawMessage(tc.args)); err == nil {
			t.Fatalf("%s should refuse when no server was detected", tc.tool)
		}
	}
}

func TestToolset_NewToolArgumentValidation(t *testing.T) {
	ts := NewToolset(NewManager(t.TempDir(), nil, Options{}))
	for _, tc := range []struct{ tool, args string }{
		{WorkspaceSymbolToolName, `{}`},
		{WorkspaceSymbolToolName, `{"query":"  "}`},
		{DocumentSymbolToolName, `{}`},
		{HoverToolName, `{"path":"a.go","line":0,"symbol":"x"}`},
		{HoverToolName, `{"path":"a.go","line":1,"symbol":" "}`},
		{DiagnosticsToolName, `{"path":42}`},
		{"symbols", `{"query":"x"}`},
	} {
		if _, err := ts.Execute(tc.tool, json.RawMessage(tc.args)); err == nil {
			t.Fatalf("%s with %s should be rejected", tc.tool, tc.args)
		}
	}
}

func TestParseCapabilities_EverySpellingOfYesAndNo(t *testing.T) {
	caps := parseCapabilities(json.RawMessage(`{"capabilities":{"workspaceSymbolProvider":true,"documentSymbolProvider":{"label":"x"},"hoverProvider":false}}`))
	if !caps.workspaceSymbol || !caps.documentSymbol || caps.hover {
		t.Fatalf("true and an options object are yes, false is no: %+v", caps)
	}
	none := parseCapabilities(json.RawMessage(`{"capabilities":{}}`))
	if none.workspaceSymbol || none.documentSymbol || none.hover {
		t.Fatalf("an absent provider is not advertised: %+v", none)
	}
	if garbage := parseCapabilities(json.RawMessage(`nonsense`)); garbage.hover {
		t.Fatal("an unreadable initialize result advertises nothing")
	}
}

func TestParseSymbols_BothShapes(t *testing.T) {
	flat := parseSymbols(json.RawMessage("["+symbolJSON("Run", "Server", 6, "file:///a.go", 7)+"]"), "")
	if len(flat) != 1 || flat[0].Path != filepath.FromSlash("/a.go") || flat[0].Line != 8 || flat[0].display() != "Server.Run" {
		t.Fatalf("flat symbol: %+v", flat)
	}
	nested := parseSymbols(json.RawMessage(`[{"name":"A","kind":5,"range":{"start":{"line":0,"character":0},"end":{"line":9,"character":0}},"children":[{"name":"b","kind":6,"selectionRange":{"start":{"line":3,"character":1},"end":{"line":3,"character":2}}}]}]`), "/x.go")
	if len(nested) != 2 || nested[1].Depth != 1 || nested[1].Line != 4 || nested[0].Path != "/x.go" {
		t.Fatalf("nested symbols: %+v", nested)
	}
	if got := parseSymbols(json.RawMessage("null"), ""); got != nil {
		t.Fatalf("null is no symbols, got %+v", got)
	}
}

func TestParseHover_AllShapes(t *testing.T) {
	for name, raw := range map[string]string{
		"plain string":    `{"contents":"func run()"}`,
		"marked string":   `{"contents":{"language":"go","value":"func run()"}}`,
		"markup content":  `{"contents":{"kind":"markdown","value":"` + "```go\\nfunc run()\\n```" + `"}}`,
		"array of marked": `{"contents":[{"language":"go","value":"func run()"},"ignored"]}`,
	} {
		if got := parseHover(json.RawMessage(raw)); !strings.HasPrefix(got, "func run()") {
			t.Fatalf("%s should flatten to the signature, got %q", name, got)
		}
	}
	if got := parseHover(json.RawMessage("null")); got != "" {
		t.Fatalf("a null hover is empty, got %q", got)
	}
	if got := parseHover(json.RawMessage(`{"contents":[]}`)); got != "" {
		t.Fatalf("an empty contents array is empty, got %q", got)
	}
}

// What a server puts inside a fence is code, and the flattening must not read
// it as markup: a comment is not a heading, a divider is not a rule, and a
// backtick is part of a raw string. Getting this wrong hands the model an
// altered signature, which is worse than any markup left in.
func TestFlattenMarkup_LeavesFencedCodeExactlyAsItIs(t *testing.T) {
	code := "#define MAX 10\nvar q = `SELECT *`\n-----"
	got := flattenMarkup("```c\n" + code + "\n```\n\n---\n\n## Notes\n\n**Deprecated**: use `next` instead.")
	want := code + "\n\nNotes\n\nDeprecated: use next instead."
	if got != want {
		t.Fatalf("fenced code should survive verbatim and prose should lose its marks:\ngot  %q\nwant %q", got, want)
	}
}

// lateDiagnostics is a server whose checks always outlast the wait an edit
// gives them, reporting one error naming whatever marker the content carries.
func lateDiagnostics(delay time.Duration) *fakeLS {
	return &fakeLS{
		publishDelay: delay,
		diagsFor: func(content string) []Diagnostic {
			marker := strings.TrimSpace(strings.TrimPrefix(content, "package main //"))
			return []Diagnostic{{
				Range:    Range{Start: Position{Line: 0, Character: 0}},
				Severity: 1,
				Message:  "undefined: " + marker,
				Source:   "gopls",
			}}
		},
	}
}

// The wait is a deadline for the edit's own result and not for the question:
// what the server says afterwards is held, tallied, and handed over once.
func TestHeldDiagnostics_LateAnswerReachesTheNextResult(t *testing.T) {
	fake := &fakeLS{
		publishDelay: 100 * time.Millisecond,
		diagsFor: func(string) []Diagnostic {
			return []Diagnostic{
				{Range: Range{Start: Position{Line: 1, Character: 2}}, Severity: 2, Message: "unused variable", Source: "gopls"},
				{Range: Range{Start: Position{Line: 4, Character: 0}}, Severity: 1, Message: "undefined:  broken\nsymbol", Source: "gopls"},
				{Range: Range{Start: Position{Line: 6, Character: 0}}, Severity: 1, Message: "missing return", Source: "gopls"},
			}
		},
	}
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 10 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")

	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("the edit's own result must still end quietly, got %q", out)
	}

	held := waitForHeld(t, m)
	wantHeader := "[diagnostics: main.go — 2 errors, 1 warning]"
	if !strings.HasPrefix(held, wantHeader) {
		t.Fatalf("held block should open with %q, got %q", wantHeader, held)
	}
	lines := strings.Split(held, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected a header and three diagnostics, got %q", held)
	}
	if !strings.Contains(lines[1], "error") || !strings.Contains(lines[2], "error") || !strings.Contains(lines[3], "warning") {
		t.Fatalf("errors must come first, got %q", held)
	}
	if !strings.Contains(lines[1], "main.go:5:1 error: undefined: broken symbol (gopls)") {
		t.Fatalf("a wrapped message should collapse onto one line, got %q", lines[1])
	}
	if again := m.TakeHeldDiagnostics(); again != "" {
		t.Fatalf("a held answer is handed over once, got %q", again)
	}
}

// The block rides in front of whatever result the model reads next, because
// the round that made the edit is over and nothing else is going its way.
func TestHeldDiagnostics_RideInFrontOfTheNextToolResult(t *testing.T) {
	fake := lateDiagnostics(50 * time.Millisecond)
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 10 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main // broken\n")
	m.DiagnosticsAfterChange(path)
	waitForPublishes(t, m, path, 1)

	exec := NewToolset(m).WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "3 matches", nil
	})
	out, err := exec("search", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "[diagnostics: main.go — 1 error]") {
		t.Fatalf("the held block should open the result, got %q", out)
	}
	if !strings.HasSuffix(out, "3 matches") {
		t.Fatalf("the result itself must survive intact, got %q", out)
	}
	again, err := exec("search", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if again != "3 matches" {
		t.Fatalf("the next result should carry nothing more, got %q", again)
	}
}

// A failed call is left alone: its result is the error the model is about to
// read, and a block in front of it reads as part of the failure.
func TestHeldDiagnostics_LeaveAFailedCallAlone(t *testing.T) {
	fake := lateDiagnostics(50 * time.Millisecond)
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 10 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main // broken\n")
	m.DiagnosticsAfterChange(path)
	waitForPublishes(t, m, path, 1)

	exec := NewToolset(m).WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "", fmt.Errorf("no such file")
	})
	if _, err := exec("read_file", json.RawMessage(`{}`)); err == nil {
		t.Fatal("the wrapped error must be returned")
	}
	if held := m.TakeHeldDiagnostics(); held == "" {
		t.Fatal("a failed call must not consume the held answer")
	}
}

// A file edited again replaces its own open question, so the model is never
// handed two blocks about the same lines — nor the older of the two answers.
func TestHeldDiagnostics_ReEditReplacesTheOpenQuestion(t *testing.T) {
	fake := lateDiagnostics(50 * time.Millisecond)
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 10 * time.Millisecond})

	writeWorkspaceFile(t, root, "main.go", "package main // first\n")
	path := filepath.Join(root, "main.go")
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("expected a quiet timeout, got %q", out)
	}
	// The first answer is on the server before the second edit, so only the
	// second edit's answer can satisfy the question it leaves behind.
	waitForPublishes(t, m, path, 1)

	writeWorkspaceFile(t, root, "main.go", "package main // second\n")
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("expected a quiet timeout, got %q", out)
	}

	held := waitForHeld(t, m)
	if strings.Count(held, "[diagnostics:") != 1 {
		t.Fatalf("one block per file, got %q", held)
	}
	if !strings.Contains(held, "undefined: second") || strings.Contains(held, "undefined: first") {
		t.Fatalf("the held answer should be about the file as it is now, got %q", held)
	}
}

// An answer that turns out to be clean is dropped rather than announced.
func TestHeldDiagnostics_CleanLateAnswerSaysNothing(t *testing.T) {
	fake := &fakeLS{publishDelay: 50 * time.Millisecond}
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 10 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")
	m.DiagnosticsAfterChange(path)
	waitForPublishes(t, m, path, 1)
	if held := m.TakeHeldDiagnostics(); held != "" {
		t.Fatalf("a clean late answer should say nothing, got %q", held)
	}
}

// A server that never publishes still produces nothing, held or otherwise.
func TestHeldDiagnostics_SilentServerHoldsNothing(t *testing.T) {
	fake := &fakeLS{noPublish: true}
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 20 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("expected a quiet timeout, got %q", out)
	}
	time.Sleep(50 * time.Millisecond)
	if held := m.TakeHeldDiagnostics(); held != "" {
		t.Fatalf("a silent server has nothing to hold, got %q", held)
	}
}

// The set can be asked for outright, per file and for the workspace, and
// asking settles the question an edit left open.
func TestToolset_DiagnosticsReportsFileAndWorkspace(t *testing.T) {
	fake := lateDiagnostics(50 * time.Millisecond)
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 10 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main // broken\n")
	m.DiagnosticsAfterChange(path)
	waitForPublishes(t, m, path, 1)
	ts := NewToolset(m)

	out, err := ts.Execute(DiagnosticsToolName, json.RawMessage(fmt.Sprintf(`{"path":%q}`, path)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Diagnostics (gopls) for main.go:") || !strings.Contains(out, "undefined: broken") {
		t.Fatalf("the tool should report the set the server has published, got %q", out)
	}
	if held := m.TakeHeldDiagnostics(); held != "" {
		t.Fatalf("asking outright settles the open question, got %q", held)
	}

	// No path is the workspace: every file this session has had checked.
	out, err = ts.Execute(DiagnosticsToolName, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "1 diagnostic across 1 file:") || !strings.Contains(out, "main.go:1:1 error:") {
		t.Fatalf("the workspace answer should tally the files it has, got %q", out)
	}
	if held := m.TakeHeldDiagnostics(); held != "" {
		t.Fatalf("the workspace answer settles the questions it reports, got %q", held)
	}
}

// The reader's question runs across files: an error three files down matters
// more than a warning in the first, whatever the filenames sort like.
func TestToolset_WorkspaceDiagnosticsPutErrorsFirstAcrossFiles(t *testing.T) {
	fake := &fakeLS{diagsFor: func(content string) []Diagnostic {
		if strings.Contains(content, "WARN") {
			return []Diagnostic{{Range: Range{Start: Position{Line: 2}}, Severity: 2, Message: "unused variable", Source: "gopls"}}
		}
		return []Diagnostic{{Range: Range{Start: Position{Line: 5}}, Severity: 1, Message: "undefined: boom", Source: "gopls"}}
	}}
	m, root := testManager(t, fake, Options{})
	m.DiagnosticsAfterChange(writeWorkspaceFile(t, root, "aaa.go", "package main // WARN\n"))
	m.DiagnosticsAfterChange(writeWorkspaceFile(t, root, "zzz.go", "package main\n"))

	out, err := NewToolset(m).Execute(DiagnosticsToolName, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "2 diagnostics across 2 files:") {
		t.Fatalf("expected a two-file tally, got %q", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "zzz.go:6:1 error") || !strings.HasPrefix(lines[2], "aaa.go:3:1 warning") {
		t.Fatalf("the error should come first whatever the filenames, got %q", out)
	}
}

// A file the servers have never looked at is reported as unchecked rather
// than as clean, which are opposite things that read the same.
func TestToolset_DiagnosticsSeparatesUncheckedFromClean(t *testing.T) {
	fake := &fakeLS{noPublish: true}
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 10 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main\n")
	ts := NewToolset(m)

	out, err := ts.Execute(DiagnosticsToolName, json.RawMessage(fmt.Sprintf(`{"path":%q}`, path)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "has not checked main.go yet") {
		t.Fatalf("an unchecked file should say so, got %q", out)
	}
	out, err = ts.Execute(DiagnosticsToolName, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No diagnostics in the files this session has had checked.") {
		t.Fatalf("a workspace with nothing to report should say so, got %q", out)
	}
}

// The workspace answer and the held block are two doors onto the same
// diagnostics, so reporting a file through one has to close the other.
func TestToolset_WorkspaceDiagnosticsSettleAHeldQuestion(t *testing.T) {
	fake := lateDiagnostics(50 * time.Millisecond)
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 10 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main // broken\n")
	m.DiagnosticsAfterChange(path)
	waitForPublishes(t, m, path, 1)

	out, err := NewToolset(m).Execute(DiagnosticsToolName, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "undefined: broken") {
		t.Fatalf("the workspace answer should carry the late set, got %q", out)
	}
	if held := m.TakeHeldDiagnostics(); held != "" {
		t.Fatalf("what the workspace answer reported must not arrive again, got %q", held)
	}
}

// Answering from the last thing the server said settles nothing about an edit
// it predates, so that question has to stay open — otherwise the answer still
// on its way is dropped and the edit is checked by nothing after all.
func TestToolset_DiagnosticsKeepsAQuestionItCannotAnswerOpen(t *testing.T) {
	fake := lateDiagnostics(0)
	m, root := testManager(t, fake, Options{DiagnosticsTimeout: 50 * time.Millisecond})
	path := writeWorkspaceFile(t, root, "main.go", "package main // first\n")
	if out := m.DiagnosticsAfterChange(path); !strings.Contains(out, "undefined: first") {
		t.Fatalf("the first check should answer inside its wait, got %q", out)
	}

	// From here the server is slower than two waits, so the second edit's
	// answer is still outstanding when the tool's own wait for it runs out.
	fake.slowDown(300 * time.Millisecond)
	writeWorkspaceFile(t, root, "main.go", "package main // second\n")
	if out := m.DiagnosticsAfterChange(path); out != "" {
		t.Fatalf("expected a quiet timeout, got %q", out)
	}

	out, err := NewToolset(m).Execute(DiagnosticsToolName, json.RawMessage(fmt.Sprintf(`{"path":%q}`, path)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "undefined: first") {
		t.Fatalf("the tool answers with the last thing the server said, got %q", out)
	}

	held := waitForHeld(t, m)
	if !strings.Contains(held, "undefined: second") {
		t.Fatalf("the outstanding answer must still arrive, got %q", held)
	}
}
