package reports

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Server serves stored reports on loopback. It is lazy: nothing listens
// until the first URL is asked for, so a session that never makes a report
// never opens a port. The port is ephemeral and the ids in the path are the
// unguessable part — there is no daemon and nothing binds beyond 127.0.0.1.
type Server struct {
	store *Store

	mu     sync.Mutex
	ln     net.Listener
	srv    *http.Server
	closed bool
}

// NewServer wraps a store for serving. Close when the session ends.
func NewServer(store *Store) *Server { return &Server{store: store} }

// URL returns the serving address for one report, starting the listener on
// first use.
func (s *Server) URL(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", fmt.Errorf("report server is closed")
	}
	if s.ln == nil {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", err
		}
		s.ln = ln
		s.srv = &http.Server{Handler: http.HandlerFunc(s.handle)}
		go func() { _ = s.srv.Serve(ln) }()
	}
	return fmt.Sprintf("http://%s/r/%s", s.ln.Addr(), id), nil
}

// Close stops the listener; idempotent.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.srv != nil {
		return s.srv.Close()
	}
	return nil
}

// handle serves GET /r/<id> and nothing else. Every serve re-renders from
// the store under the current template and tokens (Cache-Control: no-store),
// and a malformed id answers exactly like an unknown one — a guess learns
// nothing.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutPrefix(r.URL.Path, "/r/")
	if !ok || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	doc, meta, err := s.store.Load(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body, err := Render(doc, meta, id)
	if err != nil {
		http.Error(w, "report failed to render", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	// The page is self-contained by construction; the CSP makes that a
	// property of the browser too — nothing loads, nothing phones home
	// (docs/capabilities/reports.md#the-page-cannot-phone-home).
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// OpenBrowser asks the desktop to open a URL, best-effort: a browser that
// does not appear is a hint the user types the URL, never a failed report.
func OpenBrowser(url string) error {
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		return fmt.Errorf("refusing to open non-local url")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
