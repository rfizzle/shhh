package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default request and diagnostics-wait bounds. Requests cover initialize and
// navigation calls; the diagnostics wait is how long an applied edit waits
// for the server to re-publish before giving up quietly.
const (
	DefaultRequestTimeout     = 15 * time.Second
	DefaultDiagnosticsTimeout = 3 * time.Second
)

// shutdownTimeout bounds the polite shutdown exchange per server (a var so
// tests can shorten it).
var shutdownTimeout = 2 * time.Second

// Bounds on what reaches the model: diagnostics per file, reference results
// per query, symbols per search or outline, and lines of hover text.
//
// MaxSymbols is larger than MaxReferences because an outline's value is being
// complete — a truncated one sends the reader back to the file it was meant to
// replace — and a hundred declarations is about where reading the file becomes
// the cheaper answer anyway.
//
// MaxHoverLines is a signature and the paragraphs after it, which is the part
// of a doc comment that answers what something is. Past that a hover is the
// whole doc comment, and a whole doc comment is what reading the file gives.
const (
	MaxDiagnostics = 20
	MaxReferences  = 50
	MaxSymbols     = 100
	MaxHoverLines  = 25
)

// Options tunes a Manager. Zero values take the defaults; connect is a test
// seam for substituting an in-process fake server.
type Options struct {
	RequestTimeout     time.Duration
	DiagnosticsTimeout time.Duration

	connect func(ServerSpec, string) (*transport, error)
}

// managedServer is one spec's lazily-started instance. The sync.Once makes
// the start race-free; a failed start is remembered so a broken server is a
// no-op for the rest of the session instead of a retry storm.
type managedServer struct {
	spec ServerSpec
	once sync.Once
	srv  *server
	err  error
}

// Manager owns the session's language servers: lazy start on first use, one
// instance per server, bounded requests, and shutdown with the session.
type Manager struct {
	root string
	opts Options

	mu      sync.Mutex
	servers map[string]*managedServer // spec name → instance
	byExt   map[string]*managedServer
}

// NewManager builds a manager over the detected specs, rooted at the
// workspace directory. No server starts until a file it owns is touched.
func NewManager(root string, specs []ServerSpec, opts Options) *Manager {
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = DefaultRequestTimeout
	}
	if opts.DiagnosticsTimeout <= 0 {
		opts.DiagnosticsTimeout = DefaultDiagnosticsTimeout
	}
	if opts.connect == nil {
		opts.connect = spawnTransport
	}
	m := &Manager{
		root:    root,
		opts:    opts,
		servers: make(map[string]*managedServer),
		byExt:   make(map[string]*managedServer),
	}
	for _, spec := range specs {
		ms := &managedServer{spec: spec}
		m.servers[spec.Name] = ms
		for _, ext := range spec.Extensions {
			m.byExt[strings.ToLower(ext)] = ms
		}
	}
	return m
}

// ServerNames lists the detected (not necessarily started) servers.
func (m *Manager) ServerNames() []string {
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// serverFor lazily starts and returns the server owning path's extension;
// (nil, "") when no detected server covers it or its start failed.
func (m *Manager) serverFor(path string) (*server, string) {
	m.mu.Lock()
	ms := m.byExt[strings.ToLower(filepath.Ext(path))]
	m.mu.Unlock()
	if ms == nil {
		return nil, ""
	}
	if srv := m.start(ms); srv != nil {
		return srv, ms.spec.Name
	}
	return nil, ""
}

// start brings ms up exactly once and returns nil if it never came up.
func (m *Manager) start(ms *managedServer) *server {
	ms.once.Do(func() {
		ms.srv, ms.err = startServer(ms.spec, m.root, m.opts.connect, m.opts.RequestTimeout)
	})
	if ms.err != nil {
		return nil
	}
	return ms.srv
}

// startedServer pairs a running server with the spec name errors are reported
// under.
type startedServer struct {
	name string
	srv  *server
}

// symbolServers starts every detected server and keeps the ones that
// advertised workspace symbol search, ordered by name so a merged result is
// stable. This is the one question with no file to route on, and a checkout
// with two languages keeps half the answer in each server — so it reaches
// past the extension map, at the cost of starting a server this session had
// not otherwise needed, which is the same lazy start any file touch pays.
func (m *Manager) symbolServers() []startedServer {
	m.mu.Lock()
	pending := make([]*managedServer, 0, len(m.servers))
	for _, ms := range m.servers {
		pending = append(pending, ms)
	}
	m.mu.Unlock()
	sort.Slice(pending, func(i, j int) bool { return pending[i].spec.Name < pending[j].spec.Name })

	var out []startedServer
	for _, ms := range pending {
		srv := m.start(ms)
		if srv == nil || !srv.caps.workspaceSymbol {
			continue
		}
		out = append(out, startedServer{name: ms.spec.Name, srv: srv})
	}
	return out
}

// abs resolves path against the workspace root.
func (m *Manager) abs(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(m.root, path)
}

// rel renders an absolute path workspace-relative when possible, for compact
// file:line references.
func (m *Manager) rel(path string) string {
	if r, err := filepath.Rel(m.root, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

// DiagnosticsAfterChange syncs an applied file change to its server and
// returns the fresh diagnostics as a bounded, errors-first block — or "" when
// no server covers the file, the server is broken, or it does not publish
// within the diagnostics timeout. It never blocks longer than the configured
// bounds, so a hung server costs one bounded wait, not a wedged loop.
func (m *Manager) DiagnosticsAfterChange(path string) string {
	path = m.abs(path)
	srv, name := m.serverFor(path)
	if srv == nil {
		return ""
	}
	before := srv.publishSeq()
	if err := srv.syncFile(path); err != nil {
		return ""
	}
	items, ok := srv.waitDiagnostics(path, before, m.opts.DiagnosticsTimeout)
	if !ok || len(items) == 0 {
		return ""
	}
	return m.formatDiagnostics(name, path, items)
}

// formatDiagnostics renders diagnostics errors-first, capped at
// MaxDiagnostics with an elision note.
func (m *Manager) formatDiagnostics(serverName, path string, items []Diagnostic) string {
	sorted := make([]Diagnostic, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		return severityRank(sorted[i].Severity) < severityRank(sorted[j].Severity)
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "Diagnostics (%s) for %s:", serverName, m.rel(path))
	shown := sorted
	if len(shown) > MaxDiagnostics {
		shown = shown[:MaxDiagnostics]
	}
	for _, d := range shown {
		msg := strings.Join(strings.Fields(d.Message), " ")
		fmt.Fprintf(&sb, "\n%s:%d:%d %s: %s", m.rel(path), d.Range.Start.Line+1, d.Range.Start.Character+1, severityLabel(d.Severity), msg)
		if d.Source != "" {
			fmt.Fprintf(&sb, " (%s)", d.Source)
		}
	}
	if len(sorted) > len(shown) {
		fmt.Fprintf(&sb, "\n… and %d more", len(sorted)-len(shown))
	}
	return sb.String()
}

// severityRank orders errors before warnings before the rest; an absent
// severity counts as an error, per the spec's guidance.
func severityRank(severity int) int {
	if severity <= 0 {
		return 1
	}
	return severity
}

func severityLabel(severity int) string {
	switch severity {
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "error"
	}
}

// resolvePosition locates symbol on the 1-based line of path and returns the
// LSP position of its first occurrence.
func (m *Manager) resolvePosition(path string, line int, symbol string) (Position, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Position{}, fmt.Errorf("cannot read file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if line < 1 || line > len(lines) {
		return Position{}, fmt.Errorf("line %d is out of range (file has %d lines)", line, len(lines))
	}
	text := lines[line-1]
	idx := strings.Index(text, symbol)
	if idx < 0 {
		return Position{}, fmt.Errorf("symbol %q not found on line %d", symbol, line)
	}
	return Position{Line: line - 1, Character: utf16Column(text, idx)}, nil
}

// Definition resolves symbol at the 1-based line of path to its definition
// locations, formatted as file:line references.
func (m *Manager) Definition(path string, line int, symbol string) (string, error) {
	return m.navigate(path, line, symbol, "definition")
}

// References resolves symbol at the 1-based line of path to its reference
// locations (declaration included), bounded at MaxReferences.
func (m *Manager) References(path string, line int, symbol string) (string, error) {
	return m.navigate(path, line, symbol, "references")
}

func (m *Manager) navigate(path string, line int, symbol string, kind string) (string, error) {
	path = m.abs(path)
	srv, name := m.serverFor(path)
	if srv == nil {
		return "", fmt.Errorf("no language server available for %s", filepath.Ext(path))
	}
	pos, err := m.resolvePosition(path, line, symbol)
	if err != nil {
		return "", err
	}
	var locs []Location
	if kind == "definition" {
		locs, err = srv.definition(path, pos)
	} else {
		locs, err = srv.references(path, pos)
	}
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w", name, kind, err)
	}
	if len(locs) == 0 {
		return fmt.Sprintf("No %s found for %q at %s:%d.", kind, symbol, m.rel(path), line), nil
	}

	truncated := 0
	if len(locs) > MaxReferences {
		truncated = len(locs) - MaxReferences
		locs = locs[:MaxReferences]
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d %s for %q:", len(locs)+truncated, kind, symbol)
	for _, loc := range locs {
		fmt.Fprintf(&sb, "\n%s:%d", m.rel(uriToPath(loc.URI)), loc.Range.Start.Line+1)
	}
	if truncated > 0 {
		fmt.Fprintf(&sb, "\n… and %d more (truncated at %d)", truncated, MaxReferences)
	}
	return sb.String(), nil
}

// symbolKinds names the kind numbers a symbol result carries. An unknown
// number renders as "symbol": a kind this client has not heard of comes from a
// newer spec, and a bare integer in an outline reads as a line number.
var symbolKinds = [...]string{
	1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class",
	6: "method", 7: "property", 8: "field", 9: "constructor", 10: "enum",
	11: "interface", 12: "function", 13: "variable", 14: "constant",
	15: "string", 16: "number", 17: "boolean", 18: "array", 19: "object",
	20: "key", 21: "null", 22: "enum member", 23: "struct", 24: "event",
	25: "operator", 26: "type parameter",
}

// symbolNoun agrees a count with its noun, so a single hit does not read as
// "1 symbols".
func symbolNoun(n int) string {
	if n == 1 {
		return "symbol"
	}
	return "symbols"
}

func symbolKind(kind int) string {
	if kind > 0 && kind < len(symbolKinds) && symbolKinds[kind] != "" {
		return symbolKinds[kind]
	}
	return "symbol"
}

// WorkspaceSymbol searches the workspace's symbol index for query and returns
// the matches as bounded file:line references. It asks every started server
// that indexes symbols and merges what they say, since a match may live in any
// of the languages the checkout contains.
func (m *Manager) WorkspaceSymbol(query string) (string, error) {
	servers := m.symbolServers()
	if len(servers) == 0 {
		return "", fmt.Errorf("no language server here answers %s", WorkspaceSymbolToolName)
	}
	var found []symbol
	var failures []string
	for _, entry := range servers {
		syms, err := entry.srv.workspaceSymbol(query)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", entry.name, err))
			continue
		}
		found = append(found, syms...)
	}
	if len(found) == 0 {
		// Every server failing is a broken tool; some failing while others
		// answered nothing is a query that matched nothing.
		if len(failures) == len(servers) {
			return "", fmt.Errorf("%s failed (%s)", WorkspaceSymbolToolName, strings.Join(failures, "; "))
		}
		return fmt.Sprintf("No symbols matching %q.", query), nil
	}

	total := len(found)
	truncated := 0
	if len(found) > MaxSymbols {
		truncated = total - MaxSymbols
		found = found[:MaxSymbols]
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d %s matching %q:", total, symbolNoun(total), query)
	for _, sym := range found {
		fmt.Fprintf(&sb, "\n%s:%d %s %s", m.rel(sym.Path), sym.Line, symbolKind(sym.Kind), sym.display())
	}
	if truncated > 0 {
		fmt.Fprintf(&sb, "\n… and %d more (truncated at %d)", truncated, MaxSymbols)
	}
	return sb.String(), nil
}

// DocumentSymbol returns the file's outline: every declaration in it with its
// kind and line, nested as the server nests it.
func (m *Manager) DocumentSymbol(path string) (string, error) {
	path = m.abs(path)
	srv, name := m.serverFor(path)
	if srv == nil {
		return "", fmt.Errorf("no language server available for %s", filepath.Ext(path))
	}
	if !srv.caps.documentSymbol {
		return "", fmt.Errorf("%s does not answer %s", name, DocumentSymbolToolName)
	}
	syms, err := srv.documentSymbol(path)
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w", name, DocumentSymbolToolName, err)
	}
	if len(syms) == 0 {
		return fmt.Sprintf("No symbols in %s.", m.rel(path)), nil
	}

	total := len(syms)
	truncated := 0
	if len(syms) > MaxSymbols {
		truncated = total - MaxSymbols
		syms = syms[:MaxSymbols]
	}
	// Every line is in the file the header names, so the lines carry the line
	// number alone: repeating the path on each one costs most of what an
	// outline saves over reading the file.
	var sb strings.Builder
	fmt.Fprintf(&sb, "Outline of %s (%d %s):", m.rel(path), total, symbolNoun(total))
	for _, sym := range syms {
		fmt.Fprintf(&sb, "\n%d %s%s %s", sym.Line, strings.Repeat("  ", sym.Depth), symbolKind(sym.Kind), sym.display())
	}
	if truncated > 0 {
		fmt.Fprintf(&sb, "\n… and %d more (truncated at %d)", truncated, MaxSymbols)
	}
	return sb.String(), nil
}

// Hover returns the type, signature and documentation of symbol at the 1-based
// line of path, flattened out of the server's markdown and bounded by lines.
func (m *Manager) Hover(path string, line int, name string) (string, error) {
	path = m.abs(path)
	srv, serverName := m.serverFor(path)
	if srv == nil {
		return "", fmt.Errorf("no language server available for %s", filepath.Ext(path))
	}
	if !srv.caps.hover {
		return "", fmt.Errorf("%s does not answer %s", serverName, HoverToolName)
	}
	pos, err := m.resolvePosition(path, line, name)
	if err != nil {
		return "", err
	}
	text, err := srv.hover(path, pos)
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w", serverName, HoverToolName, err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Sprintf("No hover information for %q at %s:%d.", name, m.rel(path), line), nil
	}

	lines := strings.Split(text, "\n")
	truncated := 0
	if len(lines) > MaxHoverLines {
		truncated = len(lines) - MaxHoverLines
		lines = lines[:MaxHoverLines]
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s:%d %s\n%s", m.rel(path), line, name, strings.Join(lines, "\n"))
	if truncated > 0 {
		fmt.Fprintf(&sb, "\n… and %d more lines (truncated at %d)", truncated, MaxHoverLines)
	}
	return sb.String(), nil
}

// Shutdown stops every started server, each bounded by shutdownTimeout; it is
// safe to call more than once.
func (m *Manager) Shutdown() {
	var wg sync.WaitGroup
	m.mu.Lock()
	servers := make([]*managedServer, 0, len(m.servers))
	for _, ms := range m.servers {
		servers = append(servers, ms)
	}
	m.mu.Unlock()
	for _, ms := range servers {
		// Mark never-started specs as done so nothing starts after Shutdown.
		ms.once.Do(func() { ms.err = fmt.Errorf("lsp manager shut down") })
		if ms.srv == nil {
			continue
		}
		wg.Add(1)
		go func(s *server) {
			defer wg.Done()
			s.shutdown(shutdownTimeout)
		}(ms.srv)
	}
	wg.Wait()
}
