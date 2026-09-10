// Package testhttp supplies in-memory HTTP endpoints for tests that exercise
// a client without needing the host to permit listening on a TCP port.
package testhttp

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
)

// Registry owns a set of in-memory origins and is an http.RoundTripper for
// them. Handlers receive ordinary requests and write ordinary responses, so
// a client test keeps HTTP's request and response semantics without a socket.
type Registry struct {
	mu       sync.RWMutex
	next     int
	handlers map[string]http.Handler
}

// Server is one address in a Registry.
type Server struct {
	URL     string
	r       *Registry
	host    string
	aliases []string
}

// NewServer registers h at a fresh stable test origin.
func (r *Registry) NewServer(h http.Handler) *Server {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handlers == nil {
		r.handlers = make(map[string]http.Handler)
	}
	r.next++
	host := fmt.Sprintf("test-%d.invalid", r.next)
	r.handlers[host] = h
	return &Server{URL: "http://" + host, r: r, host: host}
}

// Close removes the server from its registry. It is safe to call repeatedly.
func (s *Server) Close() {
	if s == nil || s.r == nil {
		return
	}
	s.r.mu.Lock()
	delete(s.r.handlers, s.host)
	for _, host := range s.aliases {
		delete(s.r.handlers, host)
	}
	s.r.mu.Unlock()
}

// Alias makes h reachable under another origin and returns that origin. It
// lets a client test exercise host-based behavior without a second listener.
func (s *Server) Alias(host string) string {
	s.r.mu.Lock()
	s.r.handlers[host] = s.r.handlers[s.host]
	s.aliases = append(s.aliases, host)
	s.r.mu.Unlock()
	return "http://" + host
}

// Client returns an HTTP client whose requests are dispatched by this
// registry. Its transport rejects every origin the registry does not own.
func (r *Registry) Client() *http.Client { return &http.Client{Transport: r} }

// RoundTrip implements http.RoundTripper.
func (r *Registry) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.RLock()
	h := r.handlers[req.URL.Host]
	r.mu.RUnlock()
	if h == nil {
		return nil, fmt.Errorf("testhttp: no handler for %s", req.URL.Host)
	}
	rec := newResponseRecorder()
	go func() {
		h.ServeHTTP(rec, req)
		rec.finish()
	}()
	select {
	case <-rec.ready:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	rec.mu.Lock()
	status := rec.status
	header := rec.header.Clone()
	rec.mu.Unlock()
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       rec.pr,
		Request:    req,
	}, nil
}

// URL joins path to the server's origin. It is handy where a test needs a
// named endpoint rather than the root.
func (s *Server) URLFor(path string) string {
	u, _ := url.Parse(s.URL)
	u.Path = path
	return u.String()
}

type httptestResponseRecorder struct {
	mu     sync.Mutex
	header http.Header
	status int
	ready  chan struct{}
	once   sync.Once
	pr     *io.PipeReader
	pw     *io.PipeWriter
	abort  error
}

func newResponseRecorder() *httptestResponseRecorder {
	pr, pw := io.Pipe()
	return &httptestResponseRecorder{
		header: make(http.Header),
		ready:  make(chan struct{}),
		pr:     pr,
		pw:     pw,
	}
}

func (r *httptestResponseRecorder) Header() http.Header { return r.header }
func (r *httptestResponseRecorder) WriteHeader(status int) {
	r.mu.Lock()
	if r.status == 0 {
		r.status = status
	}
	r.mu.Unlock()
	r.signal()
}
func (r *httptestResponseRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.pw.Write(p)
}

// Flush satisfies handlers that mark an event-stream chunk as available. The
// in-memory transport returns its buffered response after the handler ends,
// which preserves the stream's wire format without a network connection.
func (r *httptestResponseRecorder) Flush() { r.WriteHeader(http.StatusOK) }

func (r *httptestResponseRecorder) signal() { r.once.Do(func() { close(r.ready) }) }

func (r *httptestResponseRecorder) finish() {
	r.WriteHeader(http.StatusOK)
	r.mu.Lock()
	err := r.abort
	r.mu.Unlock()
	_ = r.pw.CloseWithError(err)
}

// Abort ends a fixture response with an unexpected EOF after the handler's
// bytes. It lets a streaming parser test a broken wire without opening one.
func Abort(w http.ResponseWriter) {
	r, ok := w.(*httptestResponseRecorder)
	if !ok {
		return
	}
	r.mu.Lock()
	r.abort = io.ErrUnexpectedEOF
	r.mu.Unlock()
}
