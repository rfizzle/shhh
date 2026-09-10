// Package testhttp supplies in-memory HTTP endpoints for tests that exercise
// a client without needing the host to permit listening on a TCP port.
package testhttp

import (
	"bytes"
	"fmt"
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
	finished := make(chan *httptestResponseRecorder, 1)
	go func() {
		rec := &httptestResponseRecorder{header: make(http.Header)}
		h.ServeHTTP(rec, req)
		finished <- rec
	}()
	var rec *httptestResponseRecorder
	select {
	case rec = <-finished:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     rec.header.Clone(),
		Body:       ioNopCloser{Reader: bytes.NewReader(rec.body.Bytes())},
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
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *httptestResponseRecorder) Header() http.Header { return r.header }
func (r *httptestResponseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *httptestResponseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}

type ioNopCloser struct{ *bytes.Reader }

func (ioNopCloser) Close() error { return nil }
