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

// Bounds on what reaches the model: diagnostics per file and reference
// results per query.
const (
	MaxDiagnostics = 20
	MaxReferences  = 50
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
	ms.once.Do(func() {
		ms.srv, ms.err = startServer(ms.spec, m.root, m.opts.connect, m.opts.RequestTimeout)
	})
	if ms.err != nil {
		return nil, ""
	}
	return ms.srv, ms.spec.Name
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
