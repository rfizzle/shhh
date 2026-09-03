package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// transport is a started server process's stdio plus lifecycle hooks; tests
// substitute an in-process fake speaking the same framing.
type transport struct {
	in   io.WriteCloser
	out  io.Reader
	kill func()
}

// spawnTransport launches the server binary with the workspace as its working
// directory.
func spawnTransport(spec ServerSpec, root string) (*transport, error) {
	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Reap the process when it exits so a shut-down server never lingers as a
	// zombie.
	go func() { _ = cmd.Wait() }()
	return &transport{
		in:   stdin,
		out:  stdout,
		kill: func() { _ = cmd.Process.Kill() },
	}, nil
}

// Position and Location are the LSP wire shapes (0-based line/character).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Diagnostic is one published diagnostic; severity 1=error 2=warning 3=info
// 4=hint (0 counts as error per the spec's "client to interpret").
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

// publishedDiags is the latest publishDiagnostics for one file, stamped with
// the server-wide publish sequence so waiters can tell fresh from stale.
type publishedDiags struct {
	seq   int64
	items []Diagnostic
}

// serverCapabilities is the part of the initialize result the tools gate on.
// Definition and references are answered by every server the registry knows
// and are asked unconditionally; these three are not, and a request a server
// never advertised is answered by nothing at all — the call waits out the
// request timeout and the model is handed a broken-looking tool where it
// should have been handed a "no".
// See docs/capabilities/coding-agent.md#five-questions-for-the-language-server.
type serverCapabilities struct {
	workspaceSymbol bool
	documentSymbol  bool
	hover           bool
}

// server is one running language server owned by the session.
type server struct {
	spec       ServerSpec
	root       string
	conn       *conn
	tr         *transport
	reqTimeout time.Duration
	// caps is written once during the handshake, before the manager hands
	// this server to anything, and only read afterwards.
	caps serverCapabilities

	mu       sync.Mutex
	versions map[string]int // open file → didOpen/didChange version
	diags    map[string]publishedDiags
	pubSeq   int64
	// changed is closed and replaced on every publishDiagnostics, waking
	// diagnostics waiters (a broadcast).
	changed chan struct{}
}

// startServer launches the transport and runs the initialize handshake; a
// server that cannot initialize within the request timeout is killed.
func startServer(spec ServerSpec, root string, connect func(ServerSpec, string) (*transport, error), reqTimeout time.Duration) (*server, error) {
	tr, err := connect(spec, root)
	if err != nil {
		return nil, fmt.Errorf("cannot start %s: %w", spec.Name, err)
	}
	s := &server{
		spec:       spec,
		root:       root,
		tr:         tr,
		reqTimeout: reqTimeout,
		versions:   make(map[string]int),
		diags:      make(map[string]publishedDiags),
		changed:    make(chan struct{}),
	}
	s.conn = newConn(tr.in, tr.out, s.handleNotification)

	initParams := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToURI(root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": false},
				"publishDiagnostics": map[string]any{},
				"definition":         map[string]any{},
				"references":         map[string]any{},
				// Ask for the nested outline; a server that only has the flat
				// SymbolInformation shape answers in that one and both are
				// parsed, so this is a preference rather than a requirement.
				"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true},
				// Plaintext first: hover is flattened either way, and a server
				// that can skip generating the markdown does less work.
				"hover": map[string]any{"contentFormat": []string{"plaintext", "markdown"}},
			},
			"workspace": map[string]any{"symbol": map[string]any{}},
		},
		"workspaceFolders": []map[string]any{
			{"uri": pathToURI(root), "name": filepath.Base(root)},
		},
	}
	result, err := s.conn.call("initialize", initParams, s.reqTimeout)
	if err != nil {
		tr.kill()
		return nil, fmt.Errorf("%s initialize failed: %w", spec.Name, err)
	}
	s.caps = parseCapabilities(result)
	if err := s.conn.notify("initialized", map[string]any{}); err != nil {
		tr.kill()
		return nil, fmt.Errorf("%s initialized notification failed: %w", spec.Name, err)
	}
	return s, nil
}

func (s *server) handleNotification(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string       `json:"uri"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	path := uriToPath(p.URI)
	s.mu.Lock()
	s.pubSeq++
	s.diags[path] = publishedDiags{seq: s.pubSeq, items: p.Diagnostics}
	ch := s.changed
	s.changed = make(chan struct{})
	s.mu.Unlock()
	close(ch)
}

// publishSeq is the current publish sequence; a waiter snapshots it before a
// sync to recognize diagnostics published after that point.
func (s *server) publishSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pubSeq
}

// syncFile pushes the file's on-disk content to the server: didOpen on first
// contact, full-content didChange afterwards.
func (s *server) syncFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	version, open := s.versions[path]
	version++
	s.versions[path] = version
	s.mu.Unlock()

	uri := pathToURI(path)
	if !open {
		return s.conn.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": languageID(path),
				"version":    version,
				"text":       string(data),
			},
		})
	}
	return s.conn.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []map[string]any{{"text": string(data)}},
	})
}

// waitDiagnostics blocks until the server publishes diagnostics for path
// after afterSeq, or the timeout passes. ok is false on timeout — the caller
// treats that as "no diagnostics available", never as a clean bill.
func (s *server) waitDiagnostics(path string, afterSeq int64, timeout time.Duration) (items []Diagnostic, ok bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		d, have := s.diags[path]
		ch := s.changed
		s.mu.Unlock()
		if have && d.seq > afterSeq {
			return d.items, true
		}
		select {
		case <-ch:
		case <-s.conn.done:
			return nil, false
		case <-deadline.C:
			return nil, false
		}
	}
}

// locationRequest runs a definition/references-shaped request at a position.
func (s *server) locationRequest(method string, path string, pos Position, extra map[string]any) ([]Location, error) {
	if err := s.syncFile(path); err != nil {
		return nil, err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"position":     pos,
	}
	for k, v := range extra {
		params[k] = v
	}
	result, err := s.conn.call(method, params, s.reqTimeout)
	if err != nil {
		return nil, err
	}
	return parseLocations(result), nil
}

func (s *server) definition(path string, pos Position) ([]Location, error) {
	return s.locationRequest("textDocument/definition", path, pos, nil)
}

func (s *server) references(path string, pos Position) ([]Location, error) {
	return s.locationRequest("textDocument/references", path, pos, map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	})
}

// parseLocations accepts every shape the spec allows: null, one Location, a
// Location array, or a LocationLink array.
func parseLocations(raw json.RawMessage) []Location {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var one Location
	if err := json.Unmarshal(raw, &one); err == nil && one.URI != "" {
		return []Location{one}
	}
	var many []Location
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 && many[0].URI != "" {
		return many
	}
	var links []struct {
		TargetURI   string `json:"targetUri"`
		TargetRange Range  `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(raw, &links); err == nil {
		locs := make([]Location, 0, len(links))
		for _, l := range links {
			if l.TargetURI != "" {
				locs = append(locs, Location{URI: l.TargetURI, Range: l.TargetRange})
			}
		}
		return locs
	}
	return nil
}

// parseCapabilities reads the gated providers off an initialize result. The
// spec spells support three ways — true, an options object, or a registration
// object — and absence as false, null, or a missing key, so anything that is
// not one of the latter counts as yes.
func parseCapabilities(raw json.RawMessage) serverCapabilities {
	var res struct {
		Capabilities struct {
			WorkspaceSymbol json.RawMessage `json:"workspaceSymbolProvider"`
			DocumentSymbol  json.RawMessage `json:"documentSymbolProvider"`
			Hover           json.RawMessage `json:"hoverProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return serverCapabilities{}
	}
	return serverCapabilities{
		workspaceSymbol: advertised(res.Capabilities.WorkspaceSymbol),
		documentSymbol:  advertised(res.Capabilities.DocumentSymbol),
		hover:           advertised(res.Capabilities.Hover),
	}
}

func advertised(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "false":
		return false
	}
	return true
}

// symbol is a workspace hit or an outline entry reduced to what the rendering
// needs. Depth is the nesting in a file's outline and 0 for a flat result.
type symbol struct {
	Name      string
	Container string
	Kind      int
	Path      string
	Line      int // 1-based
	Depth     int
}

// display qualifies a name with the container the server named, when it named
// one: two methods called Execute are told apart by their types, not by their
// lines.
func (s symbol) display() string {
	if s.Container == "" {
		return s.Name
	}
	return s.Container + "." + s.Name
}

// rawSymbol accepts both shapes a symbol result is allowed to take. The flat
// one carries a location of its own; the nested one carries ranges into the
// file that was asked about and its children. They share enough fields to
// parse as one type, and which shape arrived is decided per entry rather than
// per response — a server is free to mix them across requests.
type rawSymbol struct {
	Name           string      `json:"name"`
	ContainerName  string      `json:"containerName"`
	Kind           int         `json:"kind"`
	Location       *Location   `json:"location"`
	Range          Range       `json:"range"`
	SelectionRange Range       `json:"selectionRange"`
	Children       []rawSymbol `json:"children"`
}

// parseSymbols flattens a symbol response, depth-first so an outline stays in
// declaration order. path is where a nested entry lives, since that shape
// names no file of its own; an unrecognised shape yields no symbols rather
// than an error, which the caller reports as "nothing found".
func parseSymbols(raw json.RawMessage, path string) []symbol {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var items []rawSymbol
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	var out []symbol
	var walk func([]rawSymbol, int)
	walk = func(list []rawSymbol, depth int) {
		for _, item := range list {
			if item.Name == "" {
				continue
			}
			sym := symbol{
				Name:      item.Name,
				Container: item.ContainerName,
				Kind:      item.Kind,
				Path:      path,
				Depth:     depth,
			}
			switch {
			case item.Location != nil && item.Location.URI != "":
				sym.Path = uriToPath(item.Location.URI)
				sym.Line = item.Location.Range.Start.Line + 1
			default:
				// selectionRange points at the name, range at the whole
				// declaration; the name is the more useful line to jump to.
				r := item.SelectionRange
				if r == (Range{}) {
					r = item.Range
				}
				sym.Line = r.Start.Line + 1
			}
			out = append(out, sym)
			walk(item.Children, depth+1)
		}
	}
	walk(items, 0)
	return out
}

// workspaceSymbol asks the server's index for declarations matching query.
// There is no document to sync: the question is about the whole workspace.
func (s *server) workspaceSymbol(query string) ([]symbol, error) {
	result, err := s.conn.call("workspace/symbol", map[string]any{"query": query}, s.reqTimeout)
	if err != nil {
		return nil, err
	}
	return parseSymbols(result, ""), nil
}

// documentSymbol returns the file's outline, syncing it first so the server
// answers about what is on disk rather than what it last saw.
func (s *server) documentSymbol(path string) ([]symbol, error) {
	if err := s.syncFile(path); err != nil {
		return nil, err
	}
	result, err := s.conn.call("textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
	}, s.reqTimeout)
	if err != nil {
		return nil, err
	}
	return parseSymbols(result, path), nil
}

// hover returns the symbol's type, signature and documentation at pos, already
// flattened to plain text.
func (s *server) hover(path string, pos Position) (string, error) {
	if err := s.syncFile(path); err != nil {
		return "", err
	}
	result, err := s.conn.call("textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"position":     pos,
	}, s.reqTimeout)
	if err != nil {
		return "", err
	}
	return parseHover(result), nil
}

// parseHover flattens a hover result to plain text. Its contents field is a
// string, a language/value pair, a kind/value pair, or an array mixing them.
func parseHover(raw json.RawMessage) string {
	var res struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return ""
	}
	return flattenMarkup(strings.Join(markupParts(res.Contents), "\n\n"))
}

// markupParts collects the text out of every shape hover contents can take.
func markupParts(raw json.RawMessage) []string {
	switch strings.TrimSpace(string(raw)) {
	case "", "null":
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []string{text}
	}
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Value == "" {
			return nil
		}
		return []string{obj.Value}
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil {
		var out []string
		for _, item := range list {
			out = append(out, markupParts(item)...)
		}
		return out
	}
	return nil
}

// flattenMarkup renders hover markdown as prose: fence delimiters, inline code
// marks, bold marks, heading marks and rules removed, runs of blank lines
// collapsed to one. The signature a server puts in the first fence is the most
// useful line in the result, so it is kept as a line rather than dropped with
// its fence — and what reaches the model is text it cannot mistake for markup
// of its own.
//
// What is inside a fence is code, and code is passed through untouched: a
// leading # is a comment there and not a heading, a row of dashes is a divider
// and not a rule, and a backtick is part of a raw string. Stripping those would
// hand the model an altered signature, which is worse than any markup left in.
// Single * and _ are left alone everywhere for the same reason — they are a
// pointer type and half the identifiers in Python — and a link keeps its target,
// which is information rather than decoration.
func flattenMarkup(s string) string {
	var out []string
	gap := false
	inFence := false
	for _, raw := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		line := raw
		if !inFence {
			if isRule(trimmed) {
				continue
			}
			if strings.HasPrefix(trimmed, "#") {
				line = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			}
			line = strings.ReplaceAll(line, "**", "")
			line = strings.ReplaceAll(line, "`", "")
		}
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			gap = len(out) > 0
			continue
		}
		if gap {
			out = append(out, "")
			gap = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isRule reports whether a prose line is a horizontal rule, which servers use
// to separate a signature from its documentation and which reads as noise once
// the fences around it are gone.
func isRule(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	return strings.Trim(trimmed, "-") == "" || strings.Trim(trimmed, "=") == "" || strings.Trim(trimmed, "_") == ""
}

// shutdown runs the polite shutdown/exit sequence bounded by timeout, then
// kills the process either way.
func (s *server) shutdown(timeout time.Duration) {
	_, _ = s.conn.call("shutdown", nil, timeout)
	_ = s.conn.notify("exit", nil)
	s.conn.close(fmt.Errorf("client shut down"))
	if s.tr.kill != nil {
		s.tr.kill()
	}
}

// pathToURI renders an absolute path as a file:// URI.
func pathToURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

// uriToPath converts a file:// URI back to a local path; a non-file URI is
// returned as-is so it still displays.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	return filepath.FromSlash(u.Path)
}

// utf16Column converts a byte offset in line to the UTF-16 code-unit column
// LSP positions use.
func utf16Column(line string, byteOffset int) int {
	if byteOffset > len(line) {
		byteOffset = len(line)
	}
	return len(utf16.Encode([]rune(line[:byteOffset])))
}
