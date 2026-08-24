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

// server is one running language server owned by the session.
type server struct {
	spec       ServerSpec
	root       string
	conn       *conn
	tr         *transport
	reqTimeout time.Duration

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
			},
			"workspace": map[string]any{},
		},
		"workspaceFolders": []map[string]any{
			{"uri": pathToURI(root), "name": filepath.Base(root)},
		},
	}
	if _, err := s.conn.call("initialize", initParams, s.reqTimeout); err != nil {
		tr.kill()
		return nil, fmt.Errorf("%s initialize failed: %w", spec.Name, err)
	}
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
