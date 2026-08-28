package profile

// The transport applies a profile's rules to live traffic. It sits under
// whichever SDK the dialect uses, so the rules are written against the JSON
// on the wire rather than against any Go struct: a gateway quirk in a field
// shhh's types don't model is still reachable.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// sseDataPrefix is the SSE field the OpenAI and Anthropic streams carry
// their JSON in.
const sseDataPrefix = "data:"

// Transport injects a profile's headers and applies its rewrite rules to the
// request body and, when any response rules exist, to the response — whole
// JSON bodies and each event of a stream alike.
type Transport struct {
	Base    http.RoundTripper
	Headers map[string]string
	Rules   []Rule
}

// NewTransport builds the round tripper for one endpoint, or returns base
// unchanged when that endpoint asks for no header or body changes. The
// endpoint arrives with inheritance already applied, so its headers and rules
// are the profile's plus its own.
func NewTransport(e Endpoint, base http.RoundTripper) http.RoundTripper {
	if len(e.Headers) == 0 && len(e.Rewrite) == 0 {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{Base: base, Headers: e.Headers, Rules: e.Rewrite}
}

func (t *Transport) hasRules(direction string) bool {
	for _, r := range t.Rules {
		if r.Direction == direction {
			return true
		}
	}
	return false
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	// The model is read from the outgoing body and reused to match response
	// rules, whose bodies may not name it.
	model := ""
	if t.hasRules(DirectionRequest) || t.hasRules(DirectionResponse) {
		var err error
		model, err = t.rewriteRequest(req)
		if err != nil {
			return nil, err
		}
	}

	resp, err := t.Base.RoundTrip(req)
	if err != nil || resp == nil || !t.hasRules(DirectionResponse) {
		return resp, err
	}
	return t.rewriteResponse(resp, model)
}

// rewriteRequest applies the request rules in place and returns the model the
// request names, if any.
func (t *Transport) rewriteRequest(req *http.Request) (string, error) {
	if req.Body == nil || !isJSON(req.Header.Get("Content-Type")) {
		return "", nil
	}
	raw, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return "", err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		// Not an object we can reason about — send it through untouched.
		restoreBody(req, raw)
		return "", nil
	}
	model, _ := body["model"].(string)
	if Apply(t.Rules, DirectionRequest, model, body) == 0 {
		restoreBody(req, raw)
		return model, nil
	}
	edited, err := json.Marshal(body)
	if err != nil {
		restoreBody(req, raw)
		return model, nil
	}
	restoreBody(req, edited)
	return model, nil
}

func restoreBody(req *http.Request, raw []byte) {
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(raw)), nil
	}
}

// rewriteResponse applies the response rules to a JSON body or to each event
// of a stream.
func (t *Transport) rewriteResponse(resp *http.Response, model string) (*http.Response, error) {
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(strings.ToLower(ct), "text/event-stream"):
		resp.Body = t.rewriteStream(resp.Body, model)
		return resp, nil
	case isJSON(ct):
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		edited := t.rewriteJSON(raw, model)
		resp.Body = io.NopCloser(bytes.NewReader(edited))
		resp.ContentLength = int64(len(edited))
		resp.Header.Set("Content-Length", strconv.Itoa(len(edited)))
		return resp, nil
	}
	return resp, nil
}

// rewriteJSON applies the response rules to one JSON object, returning the
// original bytes when it isn't one or nothing changed.
func (t *Transport) rewriteJSON(raw []byte, model string) []byte {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return raw
	}
	if model == "" {
		model, _ = body["model"].(string)
	}
	if Apply(t.Rules, DirectionResponse, model, body) == 0 {
		return raw
	}
	edited, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return edited
}

// rewriteStream rewrites each `data:` event as it streams, leaving every
// other line — comments, event names, blank separators, [DONE] — untouched,
// and forwarding bytes as they arrive so the TUI keeps rendering live.
func (t *Transport) rewriteStream(body io.ReadCloser, model string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer body.Close()
		scanner := bufio.NewScanner(body)
		// SSE events can carry a whole tool-call payload; the default 64KB
		// token limit is too small for that.
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			out := t.rewriteEvent(line, model)
			if _, err := pw.Write(append(out, '\n')); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.Close()
	}()
	return pr
}

// rewriteEvent rewrites one SSE line when it carries a JSON payload.
func (t *Transport) rewriteEvent(line []byte, model string) []byte {
	if !bytes.HasPrefix(line, []byte(sseDataPrefix)) {
		return line
	}
	payload := bytes.TrimSpace(line[len(sseDataPrefix):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return line
	}
	edited := t.rewriteJSON(payload, model)
	if bytes.Equal(edited, payload) {
		return line
	}
	return append([]byte(sseDataPrefix+" "), edited...)
}

func isJSON(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}
