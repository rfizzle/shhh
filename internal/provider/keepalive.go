package provider

import (
	"io"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go/option"
)

// A gateway holds a quiet stream open by writing an SSE comment (`: …`) or a
// ping event, and the Anthropic SDK reads both without handing them back: the
// stream the loop sees has no such event in it. For that dialect the keepalive
// is read off the body on its way to the SDK, so that a ping reaches the loop
// as the thing it is — something arrived that draws nothing
// (StreamEvent.Keepalive).

// pingSniffer wraps a response body and reports each keepalive line that
// passes through it. It passes every byte on unchanged and keeps only the
// first few bytes of the line it is in.
type pingSniffer struct {
	io.ReadCloser
	onPing func()
	head   [len("event: ping")]byte
	n      int
	long   bool
}

// Read reports the pings in what it read after the bytes are in hand, on the
// goroutine that is reading the stream.
func (p *pingSniffer) Read(b []byte) (int, error) {
	n, err := p.ReadCloser.Read(b)
	pings := 0
	for _, c := range b[:n] {
		switch c {
		case '\r':
		case '\n':
			if p.isPing() {
				pings++
			}
			p.n, p.long = 0, false
		default:
			if p.n < len(p.head) {
				p.head[p.n] = c
				p.n++
			} else {
				p.long = true
			}
		}
	}
	for range pings {
		p.onPing()
	}
	return n, err
}

// isPing says the line just ended is a comment, or exactly `event: ping`.
func (p *pingSniffer) isPing() bool {
	if p.n > 0 && p.head[0] == ':' {
		return true
	}
	return !p.long && string(p.head[:p.n]) == "event: ping"
}

// sniffPings is the request option that puts a pingSniffer on the response.
func sniffPings(onPing func()) option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err == nil && resp != nil && resp.Body != nil {
			resp.Body = &pingSniffer{ReadCloser: resp.Body, onPing: onPing}
		}
		return resp, err
	})
}
