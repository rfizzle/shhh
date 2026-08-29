// Package lsp is a minimal Language Server Protocol client: it
// manages auto-detected language servers over stdio JSON-RPC, feeds file
// changes to them, and surfaces diagnostics, definitions, and references to
// the agent. Every request is bounded by a timeout so a hung server can never
// wedge the agent loop; a missing or broken server degrades to a clean no-op.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rpcMessage is one JSON-RPC 2.0 message: a request (id+method), a
// notification (method only), or a response (id+result/error).
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("server error %d: %s", e.Code, e.Message)
}

// conn speaks Content-Length-framed JSON-RPC over a server's stdio. Calls are
// matched to responses by id; notifications from the server are handed to
// onNotify, and requests from the server are answered generically so servers
// that expect workspace/configuration or progress-token answers never stall.
type conn struct {
	writeMu sync.Mutex
	w       io.Writer

	onNotify func(method string, params json.RawMessage)

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage
	closed  bool
	readErr error
	done    chan struct{}
}

func newConn(w io.Writer, r io.Reader, onNotify func(method string, params json.RawMessage)) *conn {
	c := &conn{
		w:        w,
		onNotify: onNotify,
		pending:  make(map[int64]chan rpcMessage),
		done:     make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

// call sends a request and waits for its response, giving up after timeout
// (the request stays outstanding server-side; the reply is discarded).
func (c *conn) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		err := c.readErr
		c.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("connection closed")
		}
		return nil, err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: marshalParams(params)}); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-c.done:
		return nil, fmt.Errorf("connection closed during %s", method)
	case <-timer.C:
		return nil, fmt.Errorf("%s timed out after %s", method, timeout)
	}
}

// notify sends a notification (no response expected).
func (c *conn) notify(method string, params any) error {
	return c.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: marshalParams(params)})
}

func marshalParams(params any) json.RawMessage {
	if params == nil {
		return nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	return b
}

func (c *conn) write(msg rpcMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

func (c *conn) readLoop(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		msg, err := readFrame(br)
		if err != nil {
			c.close(err)
			return
		}
		switch {
		case msg.Method == "" && msg.ID != nil:
			// Response to one of our calls.
			id, err := strconv.ParseInt(strings.TrimSpace(string(msg.ID)), 10, 64)
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		case msg.ID != nil:
			// Request from the server: answer generically so it never stalls
			// waiting on capabilities we don't implement.
			c.answerServerRequest(msg)
		default:
			if c.onNotify != nil && msg.Method != "" {
				c.onNotify(msg.Method, msg.Params)
			}
		}
	}
}

// answerServerRequest replies to a server-to-client request with the most
// neutral legal answer, so servers that ask for configuration, capability
// registration, or progress tokens keep working without real support.
func (c *conn) answerServerRequest(msg rpcMessage) {
	resp := rpcMessage{JSONRPC: "2.0", ID: msg.ID}
	switch msg.Method {
	case "workspace/configuration":
		// One null per requested item.
		var params struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		nulls := make([]json.RawMessage, len(params.Items))
		for i := range nulls {
			nulls[i] = json.RawMessage("null")
		}
		b, _ := json.Marshal(nulls)
		resp.Result = b
	case "workspace/applyEdit":
		resp.Result = json.RawMessage(`{"applied":false}`)
	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create", "window/showMessageRequest",
		"workspace/semanticTokens/refresh", "workspace/codeLens/refresh",
		"workspace/inlayHint/refresh", "workspace/diagnostic/refresh":
		resp.Result = json.RawMessage("null")
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not supported: " + msg.Method}
	}
	_ = c.write(resp)
}

// close tears the connection down, failing every outstanding call.
func (c *conn) close(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.readErr = err
	close(c.done)
}

// readFrame reads one Content-Length-framed JSON-RPC message.
func readFrame(br *bufio.Reader) (rpcMessage, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return rpcMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return rpcMessage{}, fmt.Errorf("bad Content-Length: %w", err)
			}
			length = n
		}
	}
	if length < 0 {
		return rpcMessage{}, fmt.Errorf("frame missing Content-Length header")
	}
	if length > maxFrameBytes {
		return rpcMessage{}, fmt.Errorf("frame of %d bytes exceeds the %d-byte ceiling", length, maxFrameBytes)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(br, body); err != nil {
		return rpcMessage{}, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return rpcMessage{}, fmt.Errorf("bad JSON-RPC frame: %w", err)
	}
	return msg, nil
}

// maxFrameBytes bounds one incoming JSON-RPC frame so a misbehaving server
// cannot make the client allocate unboundedly.
const maxFrameBytes = 64 << 20
