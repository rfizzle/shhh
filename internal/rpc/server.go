package rpc

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

// Call is one approval-gated tool call as it is put to a client: what the
// model asked for, and where in the session it asked for it.
type Call struct {
	Tool      string
	Arguments string
	Turn      int64
	Round     int64
}

// Seams are what the protocol hands whatever assembles a session: where the
// loop's events go, and who answers a call it may not make unasked. They are
// the only two things the protocol contributes to a run — everything else
// about it is the assembly's, which is what keeps a client from being able to
// widen what a session may do by driving it differently.
type Seams struct {
	// Emit takes one line of the stream an unattended run writes, as that
	// run wrote it. The protocol forwards it to every client attached to the
	// session and reads nothing out of it: a second encoding of the same
	// event is a second vocabulary, and nothing fails when the two drift.
	Emit func(event json.RawMessage)
	// Ask puts one call to the session's clients and blocks until one of
	// them answers it. It answers false where there is nobody attached to
	// ask, and false again if the last client goes away while it is waiting:
	// an unanswered approval is a refusal, because the alternative is a turn
	// that hangs on a decision nobody is left to make.
	Ask func(Call) bool
}

// Loop is one conversation's passive agent as the protocol drives it. The
// protocol builds none of this: whatever knows about providers, tools and
// containment assembles one and hands it over.
// See docs/architecture.md#one-agent-several-front-ends.
type Loop interface {
	// Run carries out turn n and returns when it has ended. The number is
	// the server's, not a second count kept here: the client was told which
	// turn it started, and every event of it has to say the same.
	// It is called from one goroutine at a time; the server holds that.
	Run(turn int64, prompt string) (string, error)
	// Steer joins text to a running turn, which reads it at its next round
	// boundary.
	Steer(text string)
	// Interrupt stops a running turn at its next checkpoint.
	Interrupt()
	// Transcript is the conversation as it stood at the end of the last turn,
	// already encoded. It is a snapshot rather than a live reading because
	// the loop's own goroutine is writing the message list while a turn runs.
	Transcript() json.RawMessage
	// Fork opens a second loop over a copy of this one's conversation, under
	// seams of its own.
	Fork(Seams) (Loop, error)
	// Close releases what the loop was assembled over.
	Close() error
}

// Opener builds the loop behind one session.
type Opener func(ctx context.Context, p StartParams, s Seams) (Loop, error)

// Server holds the sessions this process is serving and the connections
// watching them. A session outlives the connection that opened it, which is
// what lets a client drop and come back to work that has carried on without
// it.
type Server struct {
	open Opener
	// grace is how long a session with nobody watching is left alone before
	// it is reaped, and is the server's rather than a constant so a test can
	// take it down to something it can wait out.
	grace time.Duration

	mu       sync.Mutex
	sessions map[string]*Session
	next     int
	closed   bool
}

// reapGrace is the gap between the last client leaving a session and the
// session being torn down.
//
// A session is not the connection that opened it — that is what lets a client
// drop and come back to work that carried on without it — but a session
// nobody is watching and nothing is running in holds language servers, MCP
// subprocesses, a store slot and whatever containment the assembly built, for
// as long as the process lives. So the two readings are reconciled by a
// clock: long enough that a client swapping connections, or an editor
// restarting its adapter, comes back to the session it left; short enough
// that a client that has genuinely gone does not hold a toolset open for the
// life of the server.
const reapGrace = 30 * time.Second

// NewServer answers a server that opens its sessions with open.
func NewServer(open Opener) *Server {
	return &Server{open: open, grace: reapGrace, sessions: map[string]*Session{}}
}

// Session is one conversation and everything attached to it: the loop, the
// clients watching, and the approval requests waiting for one of them.
type Session struct {
	id string
	// srv is the server this session is filed on, so a session that reaps
	// itself is taken off it rather than left behind under its name.
	srv   *Server
	grace time.Duration

	mu      sync.Mutex
	loop    Loop
	conns   map[*conn]struct{}
	pending map[string]chan bool
	asked   int
	turn    int64
	running bool
	// reap is the timer running down the grace an unwatched session is given
	// and reapGen which grace it belongs to; ended says this session is over
	// — set before the teardown rather than after it, so a client attaching
	// in the moment between is told the session has gone instead of being
	// handed a loop that is being closed.
	reap    *time.Timer
	reapGen int
	ended   bool
	// gone is closed when the session is torn down, so an approval waiting
	// for an answer that is never coming stops waiting. The Once is because
	// a session can be torn down by the open that failed to fill it and by
	// the server shutting down, and either may be first.
	gone     chan struct{}
	goneOnce sync.Once
	// done is closed when the teardown has finished, which is what a second
	// caller of end waits on.
	done chan struct{}
	// turns counts the turn goroutine, so a session being torn down can wait
	// for it rather than closing the loop out from under it.
	turns sync.WaitGroup
}

// setLoop fills in the session's agent once the assembly has answered, and
// reports whether the session was still there to take it.
//
// A session can end while it is being assembled — the client that asked for
// it hangs up, the grace runs out, the server stops — and the teardown that
// ran then had no loop to release. So a loop arriving after that is closed
// here instead: it holds language servers and subprocesses like any other,
// and nothing else is ever going to hold it.
func (s *Session) setLoop(l Loop) bool {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		_ = l.Close()
		return false
	}
	s.loop = l
	s.mu.Unlock()
	return true
}

// driver is the session's loop, or the failure a client gets for naming a
// session that is still being assembled. The lock is what makes that a
// refusal rather than a nil dereference: the session is filed under its name
// before the assembly answers, so its seams can address the events the
// assembly is already writing.
func (s *Session) driver() (Loop, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return nil, errorf(CodeUnknownSession, "session %s has ended", s.id)
	}
	if s.loop == nil {
		return nil, errorf(CodeUnknownSession, "session %s is still opening", s.id)
	}
	return s.loop, nil
}

// end tears the session down: the approvals waiting on it stop waiting, the
// turn stops at its next checkpoint, and what the loop was assembled over is
// released.
func (s *Session) end() {
	loop, first, ok := s.markEnded(nil)
	if !ok {
		return
	}
	s.tearDown(loop, first)
}

// markEnded is the step from running to ended, taken under one hold of the
// lock together with whatever condition decided it. That is what the
// condition is a closure for: a reap that read "nobody is watching", released
// the lock and only then marked the session would be tearing it down under a
// client that attached in between.
//
// ok is false where the condition said no. first says this caller is the one
// that has to do the releasing, which tearDown holds it to.
func (s *Session) markEnded(when func() bool) (Loop, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if when != nil && !when() {
		return nil, false, false
	}
	loop, first := s.loop, !s.ended
	s.ended = true
	s.stopReap()
	return loop, first, true
}

// tearDown releases what the loop was assembled over, once.
func (s *Session) tearDown(loop Loop, first bool) {
	// Closing gone first is what stops an approval waiting for a client, so
	// a turn parked on a decision reaches its next checkpoint at all.
	s.goneOnce.Do(func() { close(s.gone) })
	// A session can be ended by a client, by the reap and by the server
	// shutting down, and any two of those can arrive at once. The second
	// caller waits on the first rather than returning ahead of it: a client
	// told its session is over while a teardown is still waiting for the turn
	// would have been told the wrong thing.
	if !first {
		<-s.done
		return
	}
	defer close(s.done)
	if loop == nil {
		return
	}
	loop.Interrupt()
	// The turn is asked to stop and then waited for, rather than only asked.
	// What a turn does as it ends is write its record and save its
	// conversation, and releasing the store and the toolset out from under
	// that would lose exactly the run somebody would want to come back to.
	// The wait is bounded by the loop's own checkpoints, which is what an
	// interrupt reaches.
	s.turns.Wait()
	_ = loop.Close()
}

// ServeConn speaks the protocol over one pair of streams, which is what
// `--stdio` is: the client is the process that started this one and there is
// exactly one of it.
func (s *Server) ServeConn(ctx context.Context, r io.Reader, w io.Writer) error {
	// A cancelled context has to reach a read that is blocked on a pipe, and
	// only closing the reader does that. The stdio caller hands in a file
	// whose close is the way its peer is told the conversation is over.
	shut, _ := r.(io.Closer)
	c := newConn(s, w, shut)
	defer c.close()
	if shut != nil {
		stop := context.AfterFunc(ctx, func() { _ = shut.Close() })
		defer stop()
	}
	return c.read(ctx, r)
}

// Serve accepts connections on l until the context is cancelled or the
// listener stops answering. Several clients may be connected at once and any
// of them may attach to any session, which is the whole point of a socket.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		nc, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				// The listener was closed because we asked for it to be.
				// That is this server stopping, not this server failing.
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer nc.Close()
			// The connection is closed with the context as well as with the
			// listener. Closing only the listener stops new clients arriving
			// and leaves every read that is already blocked exactly where it
			// was, so the wait below would hold the process open until each
			// client happened to hang up — a server told to stop with
			// somebody connected would never stop.
			stop := context.AfterFunc(ctx, func() { _ = nc.Close() })
			defer stop()
			c := newConn(s, nc, nc)
			defer c.close()
			_ = c.read(ctx, nc)
		}()
	}
}

// Close ends every session this server opened. A loop holds a toolset, a
// store and whatever containment the assembly built, so leaving one open
// outlives the process's reason to have it.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.sessions = map[string]*Session{}
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.end()
	}
}

// newSession files a session under a name of the server's minting, before it
// has a loop. The name is a counter and not a timestamp: two sessions opened
// in the same instant are still two sessions.
func (s *Server) newSession() (*Session, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errorf(CodeInternal, "this server is shutting down")
	}
	s.next++
	sess := &Session{
		id:      fmt.Sprintf("s%d", s.next),
		srv:     s,
		grace:   s.grace,
		conns:   map[*conn]struct{}{},
		pending: map[string]chan bool{},
		gone:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	s.sessions[sess.id] = sess
	return sess, nil
}

func (s *Server) session(id string) (*Session, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, errorf(CodeUnknownSession, "no session %q on this server", id)
	}
	return sess, nil
}

// seams are what a session hands its assembly. They are built before the
// session has a loop — the assembly needs them to build one — so they close
// over the session pointer and are filled in by newSession.
func (s *Session) seams() Seams {
	return Seams{Emit: s.emit, Ask: s.ask}
}

// attach adds a connection to the ones watching this session, and calls off
// the reap if one was running down: a client arriving inside the grace is the
// gap between two connections the grace exists for.
func (s *Session) attach(c *conn) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.stopReap()
	s.mu.Unlock()
}

// armReap starts the grace where this session has nobody watching it and
// nothing running in it, and does nothing otherwise. The caller holds the
// lock.
func (s *Session) armReap() {
	// A grace of none is a server that does not reap at all, which is what a
	// Server built without NewServer gets.
	if s.reap != nil || s.ended || s.srv == nil || s.grace <= 0 {
		return
	}
	if len(s.conns) > 0 || s.running {
		return
	}
	// The generation is what tells a timer that has already fired from the
	// one running now. A fired timer cannot be stopped, so a session that
	// lost a client, got one back and lost it again has two of them in the
	// air, and only the newest may act — the older one would reap at the
	// moment of the second leaving rather than a grace after it.
	s.reapGen++
	gen := s.reapGen
	s.reap = time.AfterFunc(s.grace, func() { s.reapNow(gen) })
}

// stopReap calls off a grace that is running down, including one whose timer
// has fired and is waiting for this lock. The caller holds the lock.
func (s *Session) stopReap() {
	if s.reap != nil {
		s.reap.Stop()
		s.reap = nil
	}
	s.reapGen++
}

// reapNow ends a session the grace ran out on, and does nothing where a
// client came back or a turn started inside it — the timer fires whatever
// happened while it ran, so the condition it was armed on is asked again
// here, in the same hold of the lock that marks the session ended.
func (s *Session) reapNow(gen int) {
	loop, first, ok := s.markEnded(func() bool {
		if s.reapGen != gen {
			return false
		}
		s.reap = nil
		return len(s.conns) == 0 && !s.running
	})
	if !ok {
		return
	}
	// The name comes off the server before the teardown, as it does for a
	// session a client ended.
	s.srv.forget(s)
	s.tearDown(loop, first)
}

// detach removes one, and answers every approval still waiting if it was the
// last: a request with nobody left to see it is a request nobody is going to
// answer, and the turn behind it would otherwise wait for a client that has
// gone.
func (s *Session) detach(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	var orphaned []chan bool
	if len(s.conns) == 0 {
		for id, ch := range s.pending {
			orphaned = append(orphaned, ch)
			delete(s.pending, id)
		}
	}
	// And a session nobody is left watching is one nobody is coming back to,
	// unless somebody does inside the grace.
	s.armReap()
	s.mu.Unlock()
	for _, ch := range orphaned {
		ch <- false
	}
}

// emit forwards one event to every client watching.
func (s *Session) emit(event json.RawMessage) {
	s.mu.Lock()
	conns := s.watchers()
	s.mu.Unlock()
	params := EventParams{Session: s.id, Event: event}
	for _, c := range conns {
		c.notify(MethodSessionEvent, params)
	}
}

// watchers is the connections attached right now, copied so the notification
// write below does not hold the session's lock — a client that has stopped
// reading would otherwise stall every other client and the turn itself.
// The caller holds the lock.
func (s *Session) watchers() []*conn {
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	return conns
}

// ask puts one call to the clients and waits for an answer.
//
// The id is minted here and handed out only in the request, which is what
// makes an answer to a call nobody was shown impossible to spell: there is no
// name for a request that has not happened, so a client cannot approve a tier
// in advance, only the call in front of it.
func (s *Session) ask(call Call) bool {
	s.mu.Lock()
	if len(s.conns) == 0 {
		s.mu.Unlock()
		return false
	}
	s.asked++
	id := fmt.Sprintf("a%d", s.asked)
	answer := make(chan bool, 1)
	s.pending[id] = answer
	conns := s.watchers()
	s.mu.Unlock()

	params := ApprovalParams{
		Session: s.id, ID: id,
		Tool: call.Tool, Arguments: call.Arguments,
		Turn: call.Turn, Round: call.Round,
	}
	for _, c := range conns {
		c.notify(MethodApprovalRequest, params)
	}
	select {
	case allowed := <-answer:
		return allowed
	case <-s.gone:
		return false
	}
}

// answer resolves one waiting request. An id nothing is waiting under is
// refused rather than ignored: a client that thinks it approved something is
// worse off than one that was told it did not.
func (s *Session) answer(id, decision string) *Error {
	switch decision {
	case DecisionAllow, DecisionDeny:
	default:
		return errorf(CodeInvalidParams, "decision %q: one of %s or %s", decision, DecisionAllow, DecisionDeny)
	}
	s.mu.Lock()
	ch, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if !ok {
		return errorf(CodeUnknownApproval,
			"no approval request %q is waiting on session %s: a client answers a request it was shown", id, s.id)
	}
	ch <- decision == DecisionAllow
	return nil
}

// start puts a prompt to the session and returns once the turn is under way.
// The turn runs on a goroutine of its own so the connection stays free to
// steer it, interrupt it and answer its approvals — a client that had to
// return from this call first could do none of those to the turn it started.
func (s *Session) start(prompt string) (int64, *Error) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return 0, errorf(CodeUnknownSession, "session %s has ended", s.id)
	}
	if s.loop == nil {
		s.mu.Unlock()
		return 0, errorf(CodeUnknownSession, "session %s is still opening", s.id)
	}
	if s.running {
		s.mu.Unlock()
		return 0, errorf(CodeTurnRunning, "session %s already has a turn running", s.id)
	}
	s.running = true
	s.turn++
	turn, loop := s.turn, s.loop
	// A turn is work nobody may pull the store and the toolset out from
	// under, so the grace stops running down for as long as one is.
	s.stopReap()
	s.turns.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.turns.Done()
		defer func() {
			s.mu.Lock()
			s.running = false
			// A client that walked away mid-turn left a session that could
			// not be reaped at the time: the turn was still running and its
			// work is exactly what an interrupt would have cost. It can be
			// now, so the grace starts where the turn ends.
			s.armReap()
			s.mu.Unlock()
		}()
		// What the turn answered and how it ended both reached the clients
		// on the stream as they happened, and the close line states them
		// again. There is nothing here left to report.
		_, _ = loop.Run(turn, prompt)
	}()
	return turn, nil
}

// running reports whether there is a turn to steer or interrupt.
func (s *Session) turnRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// outboundQueue is how far behind a client may fall.
//
// Everything the server has to say goes through one queue per connection —
// responses from the read loop, events and approval requests from whichever
// goroutine a turn is running on — because they are one ordered stream to
// that client and two writers would interleave halves of two objects.
//
// It is a queue and not a lock around the encoder because of who else is
// waiting. A write to a client that has stopped reading blocks until it
// starts again, and with a lock that block is the turn's goroutine: one
// wedged client would stop the agent, and every other client watching the
// same session with it. The number is large enough that no client falls
// behind by keeping up slowly, and reaching it means something is wrong with
// the client rather than with its timing.
const outboundQueue = 512

// conn is one client.
type conn struct {
	srv *Server
	out chan any
	// done is closed when this connection is finished with, which is what
	// stops the writer and unblocks anything trying to send to it.
	done      chan struct{}
	closeOnce sync.Once
	// shut is what ends the read side: the socket, or the pipe a stdio
	// client is speaking on. Closing it is how a connection the server gave
	// up on stops being read from as well as written to.
	shut io.Closer

	sessMu   sync.Mutex
	attached map[*Session]struct{}
}

func newConn(s *Server, w io.Writer, shut io.Closer) *conn {
	c := &conn{srv: s, out: make(chan any, outboundQueue), done: make(chan struct{}),
		shut: shut, attached: map[*Session]struct{}{}}
	go c.writeLoop(w)
	return c
}

// writeLoop is the one writer.
func (c *conn) writeLoop(w io.Writer) {
	enc := json.NewEncoder(w)
	for {
		select {
		case v := <-c.out:
			if err := enc.Encode(v); err != nil {
				// The other end is gone or broken. Nothing more can be said
				// to it, and the read side has to stop too or the connection
				// sits there attached to sessions it can no longer watch.
				c.shutdown()
				return
			}
		case <-c.done:
			return
		}
	}
}

// shutdown gives up on this connection, from either side.
func (c *conn) shutdown() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.shut != nil {
			_ = c.shut.Close()
		}
	})
}

// close detaches this connection from everything it was watching and stops
// its writer.
func (c *conn) close() {
	c.shutdown()
	c.sessMu.Lock()
	sessions := make([]*Session, 0, len(c.attached))
	for s := range c.attached {
		sessions = append(sessions, s)
	}
	c.attached = map[*Session]struct{}{}
	c.sessMu.Unlock()
	for _, s := range sessions {
		s.detach(c)
	}
}

func (c *conn) join(s *Session) {
	c.sessMu.Lock()
	_, already := c.attached[s]
	c.attached[s] = struct{}{}
	c.sessMu.Unlock()
	if !already {
		s.attach(c)
	}
}

func (c *conn) notify(method string, params any) {
	c.write(notification{JSONRPC: Version, Method: method, Params: params})
}

// write queues one message. It never blocks: a caller here is either the read
// loop, which owes another client's request an answer, or a turn, which owes
// the record and the tree the work it is in the middle of.
func (c *conn) write(v any) {
	select {
	case c.out <- v:
	case <-c.done:
	default:
		// The queue is full, so this client has not read a message in a long
		// time. Nothing can be told to it reliably any more — least of all
		// that it has fallen behind — so it is given up on rather than
		// silently served an incomplete stream.
		c.shutdown()
	}
}

// read is the connection's loop. The framing is one JSON object per line, in
// both directions, which is the framing the run's own event stream already
// uses — a client that can read that stream can read this one with the same
// three lines of code.
//
// Every request is handled here rather than on a goroutine each, so the
// answers a client gets are in the order it asked. Nothing handled here
// blocks, with one exception: a turn runs elsewhere and an approval is
// answered by a call that only hands a channel a value, while ending a
// session waits for the turn it interrupted (sessionEnd). That wait is
// bounded by the loop's own checkpoints and cannot be held up by an approval
// nobody is left to answer, but it is this connection's other sessions that
// are waiting with it — which is the price of an answer that means the
// session is over rather than that it soon will be.
func (c *conn) read(ctx context.Context, r io.Reader) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if trimmed := strings.TrimSpace(string(line)); trimmed != "" {
			c.handle(ctx, []byte(trimmed))
		}
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// handle answers one line.
func (c *conn) handle(ctx context.Context, line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		c.write(response{JSONRPC: Version, ID: json.RawMessage("null"),
			Error: errorf(CodeParse, "not a JSON-RPC message: %v", err)})
		return
	}
	id := req.ID
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	if req.JSONRPC != Version {
		c.write(response{JSONRPC: Version, ID: id,
			Error: errorf(CodeInvalidRequest, "jsonrpc must be %q", Version)})
		return
	}
	result, rerr := c.dispatch(ctx, req)
	if rerr != nil {
		c.write(response{JSONRPC: Version, ID: id, Error: rerr})
		return
	}
	c.write(response{JSONRPC: Version, ID: id, Result: result})
}

func (c *conn) dispatch(ctx context.Context, req request) (any, *Error) {
	switch req.Method {
	case MethodSessionStart:
		return c.sessionStart(ctx, req.Params)
	case MethodSessionResume:
		return c.sessionResume(req.Params)
	case MethodSessionFork:
		return c.sessionFork(req.Params)
	case MethodSessionEnd:
		return c.sessionEnd(req.Params)
	case MethodTurnStart:
		return c.turnStart(req.Params)
	case MethodTurnSteer:
		return c.turnSteer(req.Params)
	case MethodTurnInterrupt:
		return c.turnInterrupt(req.Params)
	case MethodApprovalAnswer:
		return c.approvalAnswer(req.Params)
	}
	return nil, errorf(CodeMethodNotFound, "no method %q", req.Method)
}

// decode reads a method's parameters, or says which method could not read
// them. Absent parameters decode as the zero value, which is what a method
// whose fields are all optional should get.
func decode[T any](raw json.RawMessage) (T, *Error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, errorf(CodeInvalidParams, "params: %v", err)
	}
	return v, nil
}

func (c *conn) sessionStart(ctx context.Context, raw json.RawMessage) (any, *Error) {
	p, rerr := decode[StartParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	// The session is filed before it is opened so the assembly's seams can
	// name it: a loop reports events while it is being built, and an event
	// addressed to a session nobody has heard of is an event nobody can file.
	sess, rerr := c.srv.newSession()
	if rerr != nil {
		return nil, rerr
	}
	// The connection joins before the assembly runs, so a session-start hook
	// or a project notice written while the loop is being built reaches the
	// client that asked for it rather than nobody.
	c.join(sess)
	loop, err := c.srv.open(ctx, p, sess.seams())
	if err != nil {
		c.srv.drop(sess)
		return nil, errorf(CodeInternal, "%v", err)
	}
	if !sess.setLoop(loop) {
		return nil, errorf(CodeUnknownSession, "session %s ended while it was opening", sess.id)
	}
	return SessionResult{Session: sess.id, Transcript: loop.Transcript()}, nil
}

func (c *conn) sessionResume(raw json.RawMessage) (any, *Error) {
	p, rerr := decode[SessionParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	sess, rerr := c.srv.session(p.Session)
	if rerr != nil {
		return nil, rerr
	}
	loop, rerr := sess.driver()
	if rerr != nil {
		return nil, rerr
	}
	c.join(sess)
	return SessionResult{Session: sess.id, Transcript: loop.Transcript()}, nil
}

func (c *conn) sessionFork(raw json.RawMessage) (any, *Error) {
	p, rerr := decode[SessionParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	parent, rerr := c.srv.session(p.Session)
	if rerr != nil {
		return nil, rerr
	}
	from, rerr := parent.driver()
	if rerr != nil {
		return nil, rerr
	}
	sess, rerr := c.srv.newSession()
	if rerr != nil {
		return nil, rerr
	}
	c.join(sess)
	loop, err := from.Fork(sess.seams())
	if err != nil {
		c.srv.drop(sess)
		return nil, errorf(CodeInternal, "%v", err)
	}
	if !sess.setLoop(loop) {
		return nil, errorf(CodeUnknownSession, "session %s ended while it was opening", sess.id)
	}
	return SessionResult{Session: sess.id, Transcript: loop.Transcript()}, nil
}

// sessionEnd ends a session on a client's say-so, which is how an adapter
// that opens one per file gives back the language servers, subprocesses and
// claimed slots of the one it has finished with.
//
// It is the one request here that waits: a turn is interrupted and then
// waited for, because what a turn does as it ends is write its record and
// save its conversation, and answering before that has happened would tell a
// client the session was over while it was still writing. The wait is bounded
// by the loop's own checkpoints and cannot be held up by an approval, since
// the teardown stops those waiting before it asks the turn to stop.
func (c *conn) sessionEnd(raw json.RawMessage) (any, *Error) {
	p, rerr := decode[SessionParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	sess, rerr := c.srv.session(p.Session)
	if rerr != nil {
		return nil, rerr
	}
	c.srv.endSession(sess)
	return struct{}{}, nil
}

func (c *conn) turnStart(raw json.RawMessage) (any, *Error) {
	p, rerr := decode[TurnParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return nil, errorf(CodeInvalidParams, "a turn needs a prompt")
	}
	sess, rerr := c.srv.session(p.Session)
	if rerr != nil {
		return nil, rerr
	}
	// Starting a turn is also how a client that did not open the session
	// begins watching it: the events it asked for are about to arrive.
	c.join(sess)
	turn, rerr := sess.start(p.Prompt)
	if rerr != nil {
		return nil, rerr
	}
	return TurnResult{Turn: turn}, nil
}

func (c *conn) turnSteer(raw json.RawMessage) (any, *Error) {
	p, rerr := decode[SteerParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, errorf(CodeInvalidParams, "a steer needs something to say")
	}
	sess, rerr := c.srv.session(p.Session)
	if rerr != nil {
		return nil, rerr
	}
	loop, rerr := sess.driver()
	if rerr != nil {
		return nil, rerr
	}
	if !sess.turnRunning() {
		return nil, errorf(CodeNoTurn, "session %s has no turn to steer", sess.id)
	}
	loop.Steer(p.Text)
	return struct{}{}, nil
}

func (c *conn) turnInterrupt(raw json.RawMessage) (any, *Error) {
	p, rerr := decode[SessionParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	sess, rerr := c.srv.session(p.Session)
	if rerr != nil {
		return nil, rerr
	}
	loop, rerr := sess.driver()
	if rerr != nil {
		return nil, rerr
	}
	if !sess.turnRunning() {
		return nil, errorf(CodeNoTurn, "session %s has no turn to interrupt", sess.id)
	}
	loop.Interrupt()
	return struct{}{}, nil
}

func (c *conn) approvalAnswer(raw json.RawMessage) (any, *Error) {
	p, rerr := decode[AnswerParams](raw)
	if rerr != nil {
		return nil, rerr
	}
	sess, rerr := c.srv.session(p.Session)
	if rerr != nil {
		return nil, rerr
	}
	if rerr := sess.answer(p.ID, p.Decision); rerr != nil {
		return nil, rerr
	}
	return struct{}{}, nil
}

// drop forgets a session that never got a loop, so a failed open leaves no
// name behind for a client to attach to.
func (s *Server) drop(sess *Session) {
	s.forget(sess)
	sess.goneOnce.Do(func() { close(sess.gone) })
}

// endSession takes a session off the server and tears it down. The order is
// what stops a client attaching to a session that is going away: the name
// stops answering first, and what the session was assembled over is released
// after.
func (s *Server) endSession(sess *Session) {
	s.forget(sess)
	sess.end()
}

func (s *Server) forget(sess *Session) {
	s.mu.Lock()
	delete(s.sessions, sess.id)
	s.mu.Unlock()
}
