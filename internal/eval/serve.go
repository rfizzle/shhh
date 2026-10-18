package eval

// A conversation with somebody on the other side.
//
// A case with a persona is served: the binary under measurement runs as
// `serve --stdio` in the copied workspace, started exactly as a printed
// attempt starts it, and this file is its only client. It opens one session,
// puts the case's prompt as the first turn, answers every question and every
// gated call the session puts to it from the persona, and when a turn closes
// asks the persona whether there is anything more to say.
//
// The printed run cannot stand in for this. The question tool is registered
// only where somebody can answer it, so "the agent never asked" is only a
// finding where it could have; and an approval can only be pushed back on
// call by call where a client is asked for each one. A served session is
// assembled as an unattended run is, with the same tools and the same
// containment, and its events are the stream's own lines forwarded verbatim,
// so the whole conversation is read by the one decoder as one trace.
// See docs/capabilities/evals.md#a-case-can-have-somebody-on-the-other-side.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/observe/record"
	"github.com/rfizzle/shhh/internal/rpc"
)

// serveGrace is how long a server is given to end its session, and then to
// exit once its input is closed, before it is killed. Ending a session waits
// for a turn being interrupted, which is at worst one provider call giving up;
// a server still there after this is stuck, not finishing.
var serveGrace = 10 * time.Second

// served is what one served conversation produced: the whole of it as one
// trace, how many user turns it took, how many questions the persona had no
// rule for, and, where the conversation broke, the one line saying why.
type served struct {
	trace      trace
	turns      int
	unscripted int
	failed     string
}

// wireMessage is anything the server writes: a response to a call, or a
// notification. Params and Result are held raw so each is decoded by the
// handler that knows its shape.
type wireMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpc.Error      `json:"error"`
}

// serveClient is one conversation's state. Everything below runs on the one
// goroutine that called serveAttempt, so none of it is locked: the reader
// hands each line over a channel and writes nothing.
type serveClient struct {
	w       io.Writer
	inbox   <-chan wireMessage
	p       asker
	out     served
	next    int
	pending map[int]string
	broken  error

	session string
	running bool
	ended   bool
	closes  int
	last    event
}

// errServerGone is a server whose output ended while it was still owed
// something.
var errServerGone = errors.New("the server stopped before the conversation ended")

// serveAttempt holds one conversation with the process cmd describes, which
// has not been started. The process lives under the context cmd was made with;
// runCtx bounds the conversation, and when it ends the turn is interrupted and
// the session ended rather than the process killed, so what the session did
// before the ceiling is still on the trace.
func serveAttempt(runCtx context.Context, cmd *exec.Cmd, prompt string, p asker) served {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return served{failed: err.Error()}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return served{failed: err.Error()}
	}
	var errOut strings.Builder
	cmd.Stderr = &errOut
	if err := cmd.Start(); err != nil {
		return served{failed: err.Error()}
	}

	inbox := make(chan wireMessage)
	stop := make(chan struct{})
	readDone := make(chan struct{})
	go readWire(stdout, inbox, stop, readDone)

	c := &serveClient{w: stdin, inbox: inbox, p: p, pending: map[int]string{}}
	convErr := c.converse(runCtx, prompt)
	c.end()

	// Closing its input is how a stdio client says it is done, and the server
	// exits with it. Its output is still read to the end, so a server with
	// something left to say is never blocked writing it while it is waited on.
	close(stop)
	_ = stdin.Close()
	select {
	case <-readDone:
	case <-time.After(serveGrace):
		_ = cmd.Process.Kill()
		<-readDone
	}
	_ = cmd.Wait()

	switch {
	case convErr == nil, c.out.failed != "", errors.Is(convErr, context.DeadlineExceeded):
		// The ceiling is the attempt's to report, in its own words.
	case errors.Is(convErr, errServerGone):
		// What the server said on its way out is the reason; that it went is
		// only the symptom.
		c.out.failed = firstLine(errOut.String(), convErr.Error())
	default:
		c.out.failed = convErr.Error()
	}
	return c.out
}

// readWire hands each line the server writes to the conversation, until the
// output ends. Once the conversation has stopped listening the lines are read
// and dropped, for the reason above.
func readWire(r io.Reader, inbox chan<- wireMessage, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	defer close(inbox)
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if text := strings.TrimSpace(string(line)); text != "" {
			var m wireMessage
			if json.Unmarshal([]byte(text), &m) == nil {
				select {
				case inbox <- m:
				case <-stop:
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// converse opens the session and runs its turns: the case's prompt first, and
// then whatever the persona says after each close, until it says nothing more,
// the ceiling is reached, a turn breaks, or runCtx ends.
func (c *serveClient) converse(ctx context.Context, prompt string) error {
	c.send(rpc.MethodSessionStart, rpc.StartParams{})
	if err := c.pump(ctx, func() bool { return c.session != "" }); err != nil {
		return err
	}
	for {
		c.out.turns++
		before := c.closes
		c.running = true
		c.send(rpc.MethodTurnStart, rpc.TurnParams{Session: c.session, Prompt: prompt})
		if err := c.pump(ctx, func() bool { return c.closes > before }); err != nil {
			if c.running {
				c.send(rpc.MethodTurnInterrupt, rpc.SessionParams{Session: c.session})
			}
			return err
		}
		c.running = false
		switch c.last.Outcome {
		case record.TurnFailed, record.TurnRejected, record.TurnCapPaused, record.TurnCancelled:
			// The turn broke the way a printed run that exits non-zero breaks:
			// what the check finds afterwards is not about the task. A turn
			// the persona refused a call in closes done, and is not this; and
			// the one interrupt this client sends returns above, so a turn
			// that closes cancelled here was cancelled by something else.
			c.out.failed = firstLine(c.last.Error, "a turn ended "+c.last.Outcome)
			return nil
		}
		final, _ := c.last.finalText()
		var more bool
		if prompt, more = c.p.next(final, c.out.turns); !more {
			return nil
		}
	}
}

// end ends the session, if one was opened, and waits a grace for the answer:
// a session/end interrupts a running turn and waits for it.
func (c *serveClient) end() {
	if c.session == "" || c.broken != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), serveGrace)
	defer cancel()
	c.send(rpc.MethodSessionEnd, rpc.SessionParams{Session: c.session})
	_ = c.pump(ctx, func() bool { return c.ended })
}

// send writes one call. A write that fails is a server that has gone, and the
// next pump says so.
func (c *serveClient) send(method string, params any) {
	if c.broken != nil {
		return
	}
	c.next++
	raw, err := json.Marshal(params)
	if err != nil {
		c.broken = err
		return
	}
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`+"\n", c.next, method, raw)
	// Bounded, because a server that has stopped reading would otherwise hold
	// this goroutine in the write and no ceiling would ever be checked again.
	// The write left behind ends when the pipe is closed.
	wrote := make(chan error, 1)
	go func() { _, err := io.WriteString(c.w, body); wrote <- err }()
	select {
	case err := <-wrote:
		if err != nil {
			c.broken = errServerGone
			return
		}
	case <-time.After(serveGrace):
		c.broken = errors.New("the server stopped reading its input")
		return
	}
	c.pending[c.next] = method
}

// pump handles what the server sends until done says the wait is over, the
// server goes, a call is refused, or ctx ends.
func (c *serveClient) pump(ctx context.Context, done func() bool) error {
	for !done() {
		if c.broken != nil {
			return c.broken
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-c.inbox:
			if !ok {
				return errServerGone
			}
			if err := c.handle(m); err != nil {
				return err
			}
		}
	}
	return nil
}

// handle files one message: an event onto the trace, a request to the persona,
// a response against the call it answers.
func (c *serveClient) handle(m wireMessage) error {
	switch m.Method {
	case rpc.MethodSessionEvent:
		var p rpc.EventParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return fmt.Errorf("a session event is not one: %w", err)
		}
		ev, err := decodeEvent(p.Event)
		if err != nil {
			return fmt.Errorf("a session event is not a stream line: %w", err)
		}
		c.out.trace.events = append(c.out.trace.events, ev)
		if ev.Kind == record.EventClose {
			c.closes++
			c.last = ev
		}
	case rpc.MethodApprovalRequest:
		var p rpc.ApprovalParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return fmt.Errorf("an approval request is not one: %w", err)
		}
		decision, _ := c.p.approve(p.Tool)
		c.send(rpc.MethodApprovalAnswer, rpc.AnswerParams{Session: p.Session, ID: p.ID, Agent: p.Agent, Decision: decision})
	case rpc.MethodQuestionRequest:
		var p rpc.QuestionParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return fmt.Errorf("a question request is not one: %w", err)
		}
		picked, note, scripted := c.p.answer(p.Question, questionLabels(p))
		if !scripted {
			c.out.unscripted++
		}
		c.send(rpc.MethodQuestionAnswer, questionAnswer(p, picked, note))
	case "":
		var id int
		if json.Unmarshal(m.ID, &id) != nil {
			return nil
		}
		method, ok := c.pending[id]
		if !ok {
			return nil
		}
		delete(c.pending, id)
		if m.Error != nil {
			return fmt.Errorf("the server refused %s: %s", method, m.Error.Message)
		}
		switch method {
		case rpc.MethodSessionStart:
			var res rpc.SessionResult
			if err := json.Unmarshal(m.Result, &res); err != nil || res.Session == "" {
				return fmt.Errorf("%s answered with no session", method)
			}
			c.session = res.Session
		case rpc.MethodSessionEnd:
			c.ended = true
		}
	}
	return nil
}

// questionLabels is what the persona is shown of a question's answers: the
// labels it may still pick, or yes and no for a confirm, which offers none
// and is answered with one of the two.
func questionLabels(q rpc.QuestionParams) []string {
	if q.Shape == ask.ShapeConfirm {
		return []string{"yes", "no"}
	}
	var labels []string
	for _, o := range q.Options {
		if o.Unavailable == "" {
			labels = append(labels, o.Label)
		}
	}
	return labels
}

// questionAnswer is the persona's reply as the protocol takes it: a pick on
// the card, or the person's own words. A question asked with a required note
// is given the pick as its note, since a script's pick is all it has to say.
func questionAnswer(q rpc.QuestionParams, picked []string, note string) rpc.QuestionAnswerParams {
	a := rpc.QuestionAnswerParams{Session: q.Session, ID: q.ID}
	if len(picked) > 0 {
		a.Answered, a.Picked = ask.AnsweredOnCard, picked
		if q.Note == ask.NoteRequired {
			a.Note = strings.Join(picked, ", ")
		}
		return a
	}
	a.Answered, a.Note = ask.AnsweredTyped, note
	return a
}
