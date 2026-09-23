package rpc

// The socket every terminal session listens on.
//
// A session running on this machine can be handed a line by another one — a
// sibling told the branch moved under it, most often. What it listens on is
// deliberately not the protocol's server: that surface starts sessions, runs
// turns and answers approvals, and a socket any process of the same user can
// open must be able to do none of it. So the inbox speaks the protocol's
// framing and takes exactly one method, and a connection gets one message and
// one answer. A line carries no authority: it is text for the turn, never an
// approval, a command or a setting, and what the receiving session does with
// it is its own person's to decide.
// See docs/capabilities/sessions-and-memory.md#a-session-can-hand-another-a-line.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// MethodSessionSend hands a session one line. It is the only method the
// inbox answers.
const MethodSessionSend = "session/send"

// CodeRefused: the session takes no lines from other sessions.
const CodeRefused = -32007

// MaxLineBytes bounds what one message may carry. A line is a sentence for a
// colleague, and a socket that read without limit would let any process on
// the machine make a session hold whatever it was sent.
const MaxLineBytes = 16 << 10

// inboxDeadline bounds a connection: one message in and one answer out. A
// client that opens the socket and says nothing would otherwise hold a
// goroutine for as long as the session runs. It covers the take function's
// own wait as well, so a take must answer well inside it: an answer written
// after the deadline is lost, and the sender is told the line failed when the
// session in fact took it.
const inboxDeadline = 15 * time.Second

// SendParams is one line. From is the sending session's slot, empty where no
// session sent it; To is the slot the sender named, which a process holding
// one session need not read.
type SendParams struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Text string `json:"text"`
}

// SendResult says what became of the line: Taken is the receiving session's
// own word for it, which is how the sender learns it waits on a person.
type SendResult struct {
	Taken string `json:"taken"`
}

// The two words a line can be taken as.
const (
	// TakenDelivered: the line joins the turn, now or at its next boundary.
	TakenDelivered = "delivered"
	// TakenHeld: the line waits on a card for the person to pass on or drop.
	TakenHeld = "held"
)

// ErrRefused is what a take function returns to refuse a line outright.
var ErrRefused = errors.New("this session takes no lines from other sessions")

// Inbox answers session/send and nothing else.
type Inbox struct {
	take func(SendParams) (string, error)
}

// NewInbox builds the listener's half. take is handed every well-formed line
// and answers with how it was taken, ErrRefused, or another error.
func NewInbox(take func(SendParams) (string, error)) *Inbox {
	return &Inbox{take: take}
}

// Serve accepts connections until the context is cancelled or the listener
// fails, answering one message on each.
func (in *Inbox) Serve(ctx context.Context, l net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		nc, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer nc.Close()
			_ = nc.SetDeadline(time.Now().Add(inboxDeadline))
			in.ServeConn(nc, nc)
		}()
	}
}

// ServeConn reads one message from r and writes its answer to w. Whatever
// else the client sends on the same connection is not read.
func (in *Inbox) ServeConn(r io.Reader, w io.Writer) {
	id := json.RawMessage("null")
	answer := func(result any, rerr *Error) {
		_ = json.NewEncoder(w).Encode(response{JSONRPC: Version, ID: id, Result: result, Error: rerr})
	}
	// The frame is bounded as well as the text: the text is what the limit
	// is about, and the envelope around it is a few names.
	line, tooLong, err := readBoundedLine(bufio.NewReader(r), MaxLineBytes+4096)
	switch {
	case tooLong:
		answer(nil, errorf(CodeInvalidParams, "a line is at most %d bytes", MaxLineBytes))
		return
	case err != nil && len(line) == 0:
		return
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		answer(nil, errorf(CodeParse, "not a JSON-RPC message: %v", err))
		return
	}
	if len(req.ID) > 0 {
		id = req.ID
	}
	if req.JSONRPC != Version {
		answer(nil, errorf(CodeInvalidRequest, "jsonrpc must be %q", Version))
		return
	}
	if req.Method != MethodSessionSend {
		answer(nil, errorf(CodeMethodNotFound, "this socket takes %s and nothing else", MethodSessionSend))
		return
	}
	p, rerr := decode[SendParams](req.Params)
	if rerr != nil {
		answer(nil, rerr)
		return
	}
	if strings.TrimSpace(p.Text) == "" {
		answer(nil, errorf(CodeInvalidParams, "a line needs text"))
		return
	}
	if len(p.Text) > MaxLineBytes {
		answer(nil, errorf(CodeInvalidParams, "a line is at most %d bytes", MaxLineBytes))
		return
	}
	taken, err := in.take(p)
	switch {
	case errors.Is(err, ErrRefused):
		answer(nil, errorf(CodeRefused, "%s", ErrRefused.Error()))
	case err != nil:
		answer(nil, errorf(CodeInternal, "%s", err.Error()))
	default:
		answer(SendResult{Taken: taken}, nil)
	}
}

// readBoundedLine reads up to the first newline, or reports that the line ran
// past limit before one arrived.
func readBoundedLine(br *bufio.Reader, limit int) ([]byte, bool, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, true, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return []byte(strings.TrimSpace(string(buf))), false, err
	}
}

// Send writes one line to a session's inbox over conn and reads the answer.
// It is the whole of the client: one message, one answer.
func Send(conn io.ReadWriter, p SendParams) (SendResult, error) {
	params, err := json.Marshal(p)
	if err != nil {
		return SendResult{}, err
	}
	req := request{JSONRPC: Version, ID: json.RawMessage("1"), Method: MethodSessionSend, Params: params}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return SendResult{}, err
	}
	var resp struct {
		Result *SendResult `json:"result"`
		Error  *Error      `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return SendResult{}, fmt.Errorf("the session did not answer: %w", err)
	}
	if resp.Error != nil {
		return SendResult{}, resp.Error
	}
	if resp.Result == nil {
		return SendResult{}, errors.New("the session answered with nothing")
	}
	return *resp.Result, nil
}
