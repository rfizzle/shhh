package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/ask"
)

// fakeLoop stands in for the agent behind a session. It is the whole of what
// the protocol is allowed to know about one — a turn to run, a steer, an
// interrupt, a transcript — so a server that needed more than this would not
// compile against it.
type fakeLoop struct {
	seams Seams
	// askTool, when set, is a call this loop puts to the clients once per
	// turn, which is the only way an approval can be answered at all.
	askTool string
	// question, when set, is a question this loop puts to the clients once
	// per turn, the same way and for the same reason.
	question *ask.Question
	// held, when set, is what a turn waits on before it finishes, so a test
	// can steer or interrupt a turn that is genuinely still running.
	held    chan struct{}
	release sync.Once

	mu          sync.Mutex
	prompts     []string
	turns       []int64
	steers      []string
	allowed     []bool
	answers     []ask.Answer
	interrupted bool
	forks       int
	closed      bool
	// running says a turn is in flight, and closedMidTurn that the loop was
	// released while one was. What a real turn does as it ends is write its
	// record and save its conversation, so a release that crossed it would
	// be pulling the store out from under exactly that.
	running       bool
	closedMidTurn bool
}

func (f *fakeLoop) Run(turn int64, prompt string) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.turns = append(f.turns, turn)
	f.running = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running = false
		f.mu.Unlock()
	}()
	f.seams.Emit(json.RawMessage(`{"kind":"text","text":"working"}`))
	if f.askTool != "" {
		ok := f.seams.Ask(Call{Tool: f.askTool, Arguments: `{"command":"echo hi"}`, Turn: turn, Round: 1})
		f.mu.Lock()
		f.allowed = append(f.allowed, ok)
		f.mu.Unlock()
	}
	if f.question != nil {
		a := f.seams.Question(Question{Ask: *f.question, Turn: turn, Round: 1})
		f.mu.Lock()
		f.answers = append(f.answers, a)
		f.mu.Unlock()
	}
	if f.held != nil {
		<-f.held
	}
	f.seams.Emit(json.RawMessage(`{"kind":"close","outcome":"done"}`))
	return "the answer", nil
}

func (f *fakeLoop) Steer(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steers = append(f.steers, text)
}

func (f *fakeLoop) Interrupt() {
	f.mu.Lock()
	f.interrupted = true
	f.mu.Unlock()
	if f.held != nil {
		f.release.Do(func() { close(f.held) })
	}
}

func (f *fakeLoop) Transcript() json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := json.Marshal(f.prompts)
	return b
}

func (f *fakeLoop) Fork(s Seams) (Loop, error) {
	f.mu.Lock()
	f.forks++
	prompts := append([]string(nil), f.prompts...)
	f.mu.Unlock()
	return &fakeLoop{seams: s, prompts: prompts}, nil
}

func (f *fakeLoop) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	if f.running {
		f.closedMidTurn = true
	}
	return nil
}

// client is a test client: it writes requests, reads whatever comes back on
// its own goroutine, and files each line as a response to a call it made, an
// event or an approval request.
type client struct {
	t   *testing.T
	enc *json.Encoder
	// hangUp drops this client's connection, which is what a case that is
	// about a session with nobody watching it needs and t.Cleanup is too
	// late for.
	hangUp func()

	mu        sync.Mutex
	next      int
	waiting   map[int]chan response
	events    chan json.RawMessage
	approvals chan ApprovalParams
	questions chan QuestionParams
}

// newClient is the client's own fields, so a case that has to own the
// connection itself still files a question the same way dial's does. A nil
// channel would park the reader on the first question that arrived.
func newClient(t *testing.T, w io.Writer) *client {
	return &client{t: t, enc: json.NewEncoder(w),
		waiting:   map[int]chan response{},
		events:    make(chan json.RawMessage, 64),
		approvals: make(chan ApprovalParams, 8),
		questions: make(chan QuestionParams, 8)}
}

func dial(t *testing.T, srv *Server) *client {
	t.Helper()
	here, there := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = here.Close()
		_ = there.Close()
	})
	go func() { _ = srv.ServeConn(ctx, there, there) }()

	c := newClient(t, here)
	c.hangUp = func() { cancel(); _ = here.Close(); _ = there.Close() }
	go c.read(here)
	return c
}

// waitClosed waits for the session behind loop to have been released, which
// happens on a goroutine of the server's rather than in answer to anything
// this client asked.
func waitClosed(t *testing.T, loop *fakeLoop, why string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		loop.mu.Lock()
		closed := loop.closed
		loop.mu.Unlock()
		if closed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(why)
}

// read files everything the server sends: an answer to a call this client
// made, an event, or a call the session is waiting for a decision on.
func (c *client) read(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			c.file(line)
		}
		if err != nil {
			return
		}
	}
}

func (c *client) file(line []byte) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	switch msg.Method {
	case MethodSessionEvent:
		var p EventParams
		if json.Unmarshal(msg.Params, &p) == nil {
			c.events <- p.Event
		}
		return
	case MethodApprovalRequest:
		var p ApprovalParams
		if json.Unmarshal(msg.Params, &p) == nil {
			c.approvals <- p
		}
		return
	case MethodQuestionRequest:
		var p QuestionParams
		if json.Unmarshal(msg.Params, &p) == nil {
			c.questions <- p
		}
		return
	}
	var id int
	if json.Unmarshal(msg.ID, &id) != nil {
		return
	}
	c.mu.Lock()
	reply := c.waiting[id]
	delete(c.waiting, id)
	c.mu.Unlock()
	if reply != nil {
		reply <- response{Result: msg.Result, Error: msg.Error}
	}
}

// call sends one request and waits for its answer.
func (c *client) call(method string, params any) (json.RawMessage, *Error) {
	c.t.Helper()
	c.mu.Lock()
	c.next++
	id := c.next
	reply := make(chan response, 1)
	c.waiting[id] = reply
	c.mu.Unlock()

	raw, err := json.Marshal(params)
	if err != nil {
		c.t.Fatal(err)
	}
	idRaw, _ := json.Marshal(id)
	if err := c.enc.Encode(request{JSONRPC: Version, ID: idRaw, Method: method, Params: raw}); err != nil {
		c.t.Fatalf("sending %s: %v", method, err)
	}
	select {
	case res := <-reply:
		if res.Error != nil {
			return nil, res.Error
		}
		b, _ := json.Marshal(res.Result)
		return b, nil
	case <-time.After(5 * time.Second):
		c.t.Fatalf("%s was never answered", method)
	}
	return nil, nil
}

// mustCall is call for the requests a case is not testing the failure of.
func (c *client) mustCall(method string, params any, out any) {
	c.t.Helper()
	raw, rerr := c.call(method, params)
	if rerr != nil {
		c.t.Fatalf("%s: %v", method, rerr)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s result: %v", method, err)
		}
	}
}

func (c *client) waitApproval() ApprovalParams {
	c.t.Helper()
	select {
	case p := <-c.approvals:
		return p
	case <-time.After(5 * time.Second):
		c.t.Fatal("no approval request arrived")
	}
	return ApprovalParams{}
}

func (c *client) waitQuestion() QuestionParams {
	c.t.Helper()
	select {
	case p := <-c.questions:
		return p
	case <-time.After(5 * time.Second):
		c.t.Fatal("no question arrived")
	}
	return QuestionParams{}
}

func (c *client) waitEvent(kind string) json.RawMessage {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-c.events:
			var probe struct {
				Kind string `json:"kind"`
			}
			_ = json.Unmarshal(ev, &probe)
			if probe.Kind == kind {
				return ev
			}
		case <-deadline:
			c.t.Fatalf("no %q event arrived", kind)
		}
	}
}

func newServerWith(loop *fakeLoop) *Server {
	return NewServer(func(_ context.Context, _ StartParams, s Seams) (Loop, error) {
		loop.seams = s
		return loop, nil
	})
}

// A client opens a session, runs a turn, is shown the call the turn cannot
// make unasked, and answers it. What the loop is told is the client's answer
// and nothing else.
func TestServer_AClientDrivesATurnAndAnswersItsApproval(t *testing.T) {
	loop := &fakeLoop{askTool: "execute_command"}
	srv := newServerWith(loop)
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	if opened.Session == "" {
		t.Fatal("the session was opened without a name to drive it by")
	}

	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "do it"}, &turn)
	if turn.Turn != 1 {
		t.Errorf("the first turn of a session is turn %d", turn.Turn)
	}

	req := c.waitApproval()
	if req.Tool != "execute_command" || req.Session != opened.Session {
		t.Fatalf("the request does not say what is being asked: %+v", req)
	}
	c.mustCall(MethodApprovalAnswer, AnswerParams{Session: opened.Session, ID: req.ID, Decision: DecisionAllow}, nil)
	c.waitEvent("close")

	loop.mu.Lock()
	defer loop.mu.Unlock()
	if len(loop.prompts) != 1 || loop.prompts[0] != "do it" {
		t.Errorf("the turn ran %v", loop.prompts)
	}
	if len(loop.allowed) != 1 || !loop.allowed[0] {
		t.Errorf("the client allowed the call and the loop was told %v", loop.allowed)
	}
}

// The queue is the protocol's: an id is minted when a call is put to the
// clients and handed out only in that request, so there is no name for a
// request that has not happened and nothing to approve a tier with in
// advance. An answer that names one is refused rather than ignored.
func TestServer_AnAnswerToARequestNobodyWasShownIsRefused(t *testing.T) {
	loop := &fakeLoop{askTool: "execute_command"}
	srv := newServerWith(loop)
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)

	// Before any call has been made, which is what a pre-approval would be.
	_, rerr := c.call(MethodApprovalAnswer, AnswerParams{Session: opened.Session, ID: "a1", Decision: DecisionAllow})
	if rerr == nil || rerr.Code != CodeUnknownApproval {
		t.Fatalf("approving a call nobody had asked for was answered %v", rerr)
	}

	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "do it"}, &turn)
	req := c.waitApproval()

	// And an id beside the one that is waiting.
	_, rerr = c.call(MethodApprovalAnswer, AnswerParams{Session: opened.Session, ID: req.ID + "x", Decision: DecisionAllow})
	if rerr == nil || rerr.Code != CodeUnknownApproval {
		t.Fatalf("an id nothing is waiting under was answered %v", rerr)
	}
	// A word the protocol has no meaning for is not a decision either.
	_, rerr = c.call(MethodApprovalAnswer, AnswerParams{Session: opened.Session, ID: req.ID, Decision: "maybe"})
	if rerr == nil || rerr.Code != CodeInvalidParams {
		t.Fatalf("a decision that is neither answer was answered %v", rerr)
	}

	c.mustCall(MethodApprovalAnswer, AnswerParams{Session: opened.Session, ID: req.ID, Decision: DecisionDeny}, nil)
	c.waitEvent("close")

	// And the same id again, now that it has been spent.
	_, rerr = c.call(MethodApprovalAnswer, AnswerParams{Session: opened.Session, ID: req.ID, Decision: DecisionAllow})
	if rerr == nil || rerr.Code != CodeUnknownApproval {
		t.Fatalf("an answered request was answerable again: %v", rerr)
	}

	loop.mu.Lock()
	defer loop.mu.Unlock()
	if len(loop.allowed) != 1 || loop.allowed[0] {
		t.Errorf("the client declined and the loop was told %v", loop.allowed)
	}
}

// Two clients on one session are two views of one conversation: the second
// is handed the transcript on the way in and both are told everything that
// happens after.
func TestServer_ASecondClientSeesTheFirstsTranscript(t *testing.T) {
	loop := &fakeLoop{}
	srv := newServerWith(loop)
	defer srv.Close()
	first := dial(t, srv)

	var opened SessionResult
	first.mustCall(MethodSessionStart, StartParams{}, &opened)
	var turn TurnResult
	first.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "the first question"}, &turn)
	first.waitEvent("close")

	second := dial(t, srv)
	var joined SessionResult
	second.mustCall(MethodSessionResume, SessionParams{Session: opened.Session}, &joined)
	if joined.Session != opened.Session {
		t.Fatalf("the second client joined %q instead of %q", joined.Session, opened.Session)
	}
	if !strings.Contains(string(joined.Transcript), "the first question") {
		t.Fatalf("the second client was not shown what the first had said: %s", joined.Transcript)
	}

	// And from here they are one audience.
	second.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "the second question"}, &turn)
	if turn.Turn != 2 {
		t.Errorf("the session's second turn is turn %d", turn.Turn)
	}
	first.waitEvent("close")
	second.waitEvent("close")
}

// A steer and an interrupt reach the turn that is running, and are refused
// where there is no turn to reach: a client that was told its steer landed
// when nothing read it would be waiting for an answer to a question the
// session never heard.
func TestServer_SteerAndInterruptReachARunningTurn(t *testing.T) {
	loop := &fakeLoop{held: make(chan struct{})}
	srv := newServerWith(loop)
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)

	_, rerr := c.call(MethodTurnSteer, SteerParams{Session: opened.Session, Text: "over here"})
	if rerr == nil || rerr.Code != CodeNoTurn {
		t.Fatalf("steering a session with no turn was answered %v", rerr)
	}

	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "take your time"}, &turn)
	c.waitEvent("text")
	c.mustCall(MethodTurnSteer, SteerParams{Session: opened.Session, Text: "over here"}, nil)
	c.mustCall(MethodTurnInterrupt, SessionParams{Session: opened.Session}, nil)
	c.waitEvent("close")

	loop.mu.Lock()
	defer loop.mu.Unlock()
	if len(loop.steers) != 1 || loop.steers[0] != "over here" {
		t.Errorf("the turn was steered with %v", loop.steers)
	}
	if !loop.interrupted {
		t.Error("the turn was never told to stop")
	}
}

// A fork is the same history and a separate future: the new session opens on
// a copy of the conversation and is a session of its own from there.
func TestServer_AForkOpensOnACopyOfTheConversation(t *testing.T) {
	loop := &fakeLoop{}
	srv := newServerWith(loop)
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "the shared question"}, &turn)
	c.waitEvent("close")

	var forked SessionResult
	c.mustCall(MethodSessionFork, SessionParams{Session: opened.Session}, &forked)
	if forked.Session == opened.Session {
		t.Fatal("the fork is the session it forked from")
	}
	if !strings.Contains(string(forked.Transcript), "the shared question") {
		t.Fatalf("the fork did not carry the conversation: %s", forked.Transcript)
	}
}

// Nothing on this server answers to a name it never minted, and a method it
// does not have is a failure the client can act on rather than silence.
func TestServer_RefusesWhatItDoesNotHave(t *testing.T) {
	srv := newServerWith(&fakeLoop{})
	defer srv.Close()
	c := dial(t, srv)

	if _, rerr := c.call(MethodTurnStart, TurnParams{Session: "s9", Prompt: "hello"}); rerr == nil || rerr.Code != CodeUnknownSession {
		t.Errorf("a session nobody opened was answered %v", rerr)
	}
	if _, rerr := c.call("turn/rewind", SessionParams{Session: "s1"}); rerr == nil || rerr.Code != CodeMethodNotFound {
		t.Errorf("a method this server does not have was answered %v", rerr)
	}
}

// An approval nobody is left to answer is a refusal. A turn that waited for
// a client that has gone would hold a session open on a decision that is
// never coming.
func TestServer_AnApprovalWithNobodyLeftToAnswerIsRefused(t *testing.T) {
	loop := &fakeLoop{askTool: "execute_command"}
	srv := NewServer(func(_ context.Context, _ StartParams, s Seams) (Loop, error) {
		loop.seams = s
		return loop, nil
	})
	defer srv.Close()

	here, there := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan struct{})
	go func() { defer close(served); _ = srv.ServeConn(ctx, there, there) }()

	c := newClient(t, here)
	go c.read(here)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "do it"}, &turn)
	c.waitApproval()

	// The client goes away with the request unanswered.
	_ = here.Close()
	<-served

	deadline := time.After(5 * time.Second)
	for {
		loop.mu.Lock()
		answered := len(loop.allowed)
		var got bool
		if answered > 0 {
			got = loop.allowed[0]
		}
		loop.mu.Unlock()
		if answered > 0 {
			if got {
				t.Fatal("a request nobody was left to answer was allowed")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("the turn is still waiting for an answer from a client that has gone")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A line that is not a message is answered rather than dropped, and the
// connection carries on: one malformed write from a client is not a reason to
// stop serving the session it has open.
func TestServer_AMalformedLineIsAnsweredAndTheConnectionCarriesOn(t *testing.T) {
	srv := newServerWith(&fakeLoop{})
	defer srv.Close()

	here, there := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeConn(ctx, there, there) }()

	c := newClient(t, here)
	go c.read(here)

	if _, err := here.Write([]byte("{not json\n")); err != nil {
		t.Fatal(err)
	}
	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	if opened.Session == "" {
		t.Fatal("the connection stopped serving after a line it could not read")
	}
}

// A session being torn down asks its turn to stop and then waits for it. What
// a turn does as it ends is write its record and save its conversation, and
// releasing the store and the toolset out from under that loses exactly the
// run somebody would want to come back to.
func TestServer_TearingDownASessionWaitsForItsTurn(t *testing.T) {
	loop := &fakeLoop{held: make(chan struct{})}
	srv := newServerWith(loop)
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "take your time"}, &turn)
	// The turn has reached the middle of itself, which is the only place a
	// teardown can cross it.
	c.waitEvent("text")

	srv.Close()

	loop.mu.Lock()
	defer loop.mu.Unlock()
	if !loop.closed {
		t.Fatal("the session was torn down without releasing its loop")
	}
	if loop.closedMidTurn {
		t.Error("the loop was released while its turn was still running")
	}
}

// A server told to stop stops, with a client still connected and saying
// nothing. Closing only the listener leaves every read already blocked
// exactly where it was, so the process would hold open until each client
// happened to hang up.
func TestServer_StopsOnASocketWithAClientStillConnected(t *testing.T) {
	srv := newServerWith(&fakeLoop{})
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "s")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- srv.Serve(ctx, l) }()

	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dialling the server: %v", err)
	}
	defer client.Close()
	// Connected and idle, which is the state the hang needs. Nothing is
	// asked of the server: what is under test is a read that is blocked.

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("the server stopped with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop while a client was connected")
	}
}

// A client ends the session it opened, and what the session was assembled
// over is released there rather than at the end of the process. An adapter
// that opens a session per file is the reason: sixteen files open is sixteen
// sets of language servers, subprocesses and claimed slots, and nothing else
// gives one back.
func TestServer_AClientEndsTheSessionItOpened(t *testing.T) {
	loop := &fakeLoop{}
	srv := newServerWith(loop)
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	c.mustCall(MethodSessionEnd, SessionParams{Session: opened.Session}, nil)

	loop.mu.Lock()
	closed := loop.closed
	loop.mu.Unlock()
	if !closed {
		t.Error("the session ended without releasing what it was assembled over")
	}
	// And the name stops answering, so a client cannot go on driving a
	// session whose toolset has been taken away.
	if _, rerr := c.call(MethodSessionResume, SessionParams{Session: opened.Session}); rerr == nil || rerr.Code != CodeUnknownSession {
		t.Errorf("resuming an ended session was answered %v", rerr)
	}
}

// Ending a session with a turn in it interrupts the turn and waits for it,
// for the reason a teardown does: what a turn does as it ends is write its
// record and save its conversation.
func TestServer_EndingASessionWaitsForTheTurnItInterrupts(t *testing.T) {
	loop := &fakeLoop{held: make(chan struct{})}
	srv := newServerWith(loop)
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "take your time"}, &turn)
	// The turn has reached the middle of itself, which is the only place the
	// end can cross it.
	c.waitEvent("text")

	c.mustCall(MethodSessionEnd, SessionParams{Session: opened.Session}, nil)

	loop.mu.Lock()
	defer loop.mu.Unlock()
	if !loop.interrupted {
		t.Error("the running turn was never told to stop")
	}
	if !loop.closed {
		t.Error("the session ended without releasing its loop")
	}
	if loop.closedMidTurn {
		t.Error("the loop was released while its turn was still running")
	}
}

// A session nobody is left watching is reaped once the grace has run out.
// A session outliving the connection that opened it is what lets a client
// drop and come back; a session outliving every client there will ever be is
// a toolset held open for the life of the process.
func TestServer_AnUnwatchedSessionIsReapedAfterTheGrace(t *testing.T) {
	loop := &fakeLoop{}
	srv := newServerWith(loop)
	srv.grace = 10 * time.Millisecond
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	c.hangUp()

	waitClosed(t, loop, "the session nobody was watching was never reaped")
	if _, rerr := srv.session(opened.Session); rerr == nil {
		t.Error("the reaped session still answers to its name")
	}
}

// And a gap between two clients is not a session nobody is coming back to.
// The grace is the whole difference between the two, so a client that
// reconnects inside it finds the session it left.
func TestServer_AClientBackInsideTheGraceFindsItsSession(t *testing.T) {
	loop := &fakeLoop{}
	srv := newServerWith(loop)
	srv.grace = time.Second
	defer srv.Close()

	first := dial(t, srv)
	var opened SessionResult
	first.mustCall(MethodSessionStart, StartParams{}, &opened)
	first.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "the first question"}, &TurnResult{})
	first.waitEvent("close")
	first.hangUp()

	second := dial(t, srv)
	var joined SessionResult
	second.mustCall(MethodSessionResume, SessionParams{Session: opened.Session}, &joined)
	if joined.Session != opened.Session {
		t.Fatalf("the second client joined %q instead of %q", joined.Session, opened.Session)
	}
	// Past the grace the first client's leaving started: the reap was called
	// off rather than left to fire behind the client that arrived.
	time.Sleep(srv.grace + srv.grace/2)
	loop.mu.Lock()
	closed := loop.closed
	loop.mu.Unlock()
	if closed {
		t.Error("the session was reaped out from under the client that came back to it")
	}
	second.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "the second question"}, &TurnResult{})
	second.waitEvent("close")
}

// A turn is work nobody may pull the store and the toolset out from under, so
// a client that walks away mid-turn leaves a session that is reaped when the
// turn ends and not while it runs.
func TestServer_ASessionIsNotReapedWhileItsTurnRuns(t *testing.T) {
	loop := &fakeLoop{held: make(chan struct{})}
	srv := newServerWith(loop)
	srv.grace = 10 * time.Millisecond
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "take your time"}, &TurnResult{})
	c.waitEvent("text")
	c.hangUp()

	time.Sleep(20 * srv.grace)
	loop.mu.Lock()
	closedEarly := loop.closed
	loop.mu.Unlock()
	if closedEarly {
		t.Fatal("the session was reaped with a turn still running in it")
	}
	// The turn ends, and the grace starts where it ends.
	loop.Interrupt()
	waitClosed(t, loop, "the session was never reaped after its turn ended")
	loop.mu.Lock()
	defer loop.mu.Unlock()
	if loop.closedMidTurn {
		t.Error("the reap released the loop while its turn was still running")
	}
}

// A session can end while it is still being assembled — the server stops, or
// the grace runs out on the client that asked for it — and the teardown that
// ran then had no loop to release. The loop that arrives afterwards holds
// language servers and subprocesses like any other, so it is released where
// it lands rather than left with nobody holding it.
func TestServer_ALoopThatArrivesAfterItsSessionEndedIsReleased(t *testing.T) {
	loop := &fakeLoop{}
	assembling, finish := make(chan struct{}), make(chan struct{})
	srv := NewServer(func(_ context.Context, _ StartParams, s Seams) (Loop, error) {
		loop.seams = s
		close(assembling)
		<-finish
		return loop, nil
	})
	c := dial(t, srv)

	// The request is sent from a goroutine because the session is being
	// opened for as long as this case says so, and nothing is answered until
	// it is.
	go func() {
		id, _ := json.Marshal(1)
		params, _ := json.Marshal(StartParams{})
		_ = c.enc.Encode(request{JSONRPC: Version, ID: id, Method: MethodSessionStart, Params: params})
	}()
	<-assembling

	srv.Close()
	close(finish)

	waitClosed(t, loop, "a loop assembled after its session ended was left running")
}

// The grace's timer fires whatever happened while it ran, so what it finds is
// asked again — and asked in the same hold of the lock that marks the session
// ended, or a client attaching between the question and the answer would have
// its session torn down under it.
func TestSession_AReapThatFindsAClientBackDoesNothing(t *testing.T) {
	loop := &fakeLoop{}
	srv := newServerWith(loop)
	defer srv.Close()
	sess, rerr := srv.newSession()
	if rerr != nil {
		t.Fatal(rerr)
	}
	sess.setLoop(loop)
	sess.attach(&conn{})

	sess.reapNow(sess.reapGen)

	loop.mu.Lock()
	defer loop.mu.Unlock()
	if loop.closed {
		t.Error("the session was reaped with a client watching it")
	}
}

// And a timer from a grace that was called off has nothing to say about the
// grace running now: a fired timer cannot be stopped, so a session that lost
// a client, got one back and lost it again has two of them in the air.
func TestSession_AReapFromAGraceThatWasCalledOffDoesNothing(t *testing.T) {
	loop := &fakeLoop{}
	srv := newServerWith(loop)
	srv.grace = time.Hour
	defer srv.Close()
	sess, rerr := srv.newSession()
	if rerr != nil {
		t.Fatal(rerr)
	}
	sess.setLoop(loop)

	sess.mu.Lock()
	sess.armReap()
	first := sess.reapGen
	sess.mu.Unlock()
	c := &conn{}
	sess.attach(c) // the client is back, so that grace is off
	sess.detach(c) // and gone again, which is a grace of its own

	sess.reapNow(first)

	loop.mu.Lock()
	defer loop.mu.Unlock()
	if loop.closed {
		t.Error("a grace that was called off reaped the session anyway")
	}
}

// A question crosses the protocol in each of the four shapes: what the model
// asked goes out with its options and the reasons a row cannot be taken, and
// what comes back is the labels the reader took and the words they wrote —
// never an index, which is a fact about a list the model wrote and the client
// may have drawn in an order of its own.
func TestServer_AQuestionCrossesTheProtocolInEveryShape(t *testing.T) {
	listed := []ask.Option{
		{Label: "one package", Detail: "the narrow change", Field: "3 files", Recommended: true},
		{Label: "both packages", Detail: "the wider one", Field: "11 files"},
		{Label: "rewrite the caller", Unavailable: "the caller is generated"},
	}
	cases := []struct {
		name   string
		q      ask.Question
		answer QuestionAnswerParams
		want   ask.Answer
	}{
		{
			name: "choose",
			q:    ask.Question{Question: "How far should this reach?", Shape: ask.ShapeChoose, Options: listed, Note: ask.NoteOptional},
			answer: QuestionAnswerParams{Answered: ask.AnsweredOnCard,
				Picked: []string{"both packages"}, Note: "the second one is where the bug is"},
			want: ask.Answer{Answered: ask.AnsweredOnCard,
				Picked: []string{"both packages"}, Note: "the second one is where the bug is"},
		},
		{
			name: "choose_many",
			q:    ask.Question{Question: "Which of these should I fix?", Shape: ask.ShapeChooseMany, Options: listed, Note: ask.NoteOptional},
			answer: QuestionAnswerParams{Answered: ask.AnsweredOnCard,
				Picked: []string{"one package", "both packages"}},
			want: ask.Answer{Answered: ask.AnsweredOnCard, Picked: []string{"one package", "both packages"}},
		},
		{
			name:   "confirm",
			q:      ask.Question{Question: "Should I delete the old path?", Shape: ask.ShapeConfirm, Note: ask.NoteRequired},
			answer: QuestionAnswerParams{Answered: ask.AnsweredOnCard, Picked: []string{"no"}, Note: "keep it for a release"},
			want:   ask.Answer{Answered: ask.AnsweredOnCard, Picked: []string{"no"}, Note: "keep it for a release"},
		},
		{
			name:   "text",
			q:      ask.Question{Question: "What should it be called?", Shape: ask.ShapeText, Note: ask.NoteRequired},
			answer: QuestionAnswerParams{Answered: ask.AnsweredTyped, Note: "call it the register"},
			want:   ask.Answer{Answered: ask.AnsweredTyped, Note: "call it the register"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.q
			loop := &fakeLoop{question: &q}
			srv := newServerWith(loop)
			defer srv.Close()
			c := dial(t, srv)

			var opened SessionResult
			c.mustCall(MethodSessionStart, StartParams{}, &opened)
			var turn TurnResult
			c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "do it"}, &turn)

			put := c.waitQuestion()
			if put.Session != opened.Session || put.Question != q.Question {
				t.Fatalf("the request does not say what is being asked: %+v", put)
			}
			if put.Shape != q.Shape || put.Note != q.Note {
				t.Errorf("the question crossed as shape %q note %q", put.Shape, put.Note)
			}
			if put.Turn != turn.Turn || put.Round != 1 {
				t.Errorf("the question does not say where it was asked: turn %d round %d", put.Turn, put.Round)
			}
			if len(put.Options) != len(q.Options) {
				t.Fatalf("the client was offered %d of %d options", len(put.Options), len(q.Options))
			}
			for i, o := range put.Options {
				want := q.Options[i]
				if o.Label != want.Label || o.Detail != want.Detail || o.Field != want.Field ||
					o.Recommended != want.Recommended || o.Unavailable != want.Unavailable {
					t.Errorf("option %d crossed as %+v, not %+v", i, o, want)
				}
			}

			answer := tc.answer
			answer.Session, answer.ID = opened.Session, put.ID
			c.mustCall(MethodQuestionAnswer, answer, nil)
			c.waitEvent("close")

			loop.mu.Lock()
			defer loop.mu.Unlock()
			if len(loop.answers) != 1 {
				t.Fatalf("the loop was told %v", loop.answers)
			}
			got := loop.answers[0]
			if got.Answered != tc.want.Answered || got.Note != tc.want.Note ||
				strings.Join(got.Picked, "|") != strings.Join(tc.want.Picked, "|") {
				t.Errorf("the answer reached the loop as %+v, not %+v", got, tc.want)
			}
		})
	}
}

// A question with nobody left to answer it is not a refusal and not a guess:
// the model is told the reader has gone and to state the assumption it would
// have asked about, so the turn carries on rather than parking on a decision
// that is never coming.
func TestServer_AQuestionWithNobodyLeftToAnswerIsNobodyToAsk(t *testing.T) {
	q := ask.Question{Question: "Should I delete the old path?", Shape: ask.ShapeConfirm}
	loop := &fakeLoop{question: &q}
	srv := newServerWith(loop)
	defer srv.Close()

	here, there := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan struct{})
	go func() { defer close(served); _ = srv.ServeConn(ctx, there, there) }()

	c := newClient(t, here)
	go c.read(here)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)
	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "do it"}, &turn)
	c.waitQuestion()

	// The client goes away with the question outstanding.
	_ = here.Close()
	<-served

	deadline := time.After(5 * time.Second)
	for {
		loop.mu.Lock()
		answers := append([]ask.Answer(nil), loop.answers...)
		loop.mu.Unlock()
		if len(answers) > 0 {
			if answers[0].Answered != ask.AnsweredNobody {
				t.Fatalf("a question nobody was left to answer came back %+v", answers[0])
			}
			if !strings.Contains(answers[0].Result(), "carry on") {
				t.Errorf("the model was not told what to do about it: %s", answers[0].Result())
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("the turn is still waiting for an answer from a client that has gone")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// The question queue is the protocol's, in a series of its own: an approval's
// id cannot answer a question, an answer the card would have refused as it
// collected it is refused here, and a refusal does not spend the question —
// a client told its answer was malformed can send another.
func TestServer_AnAnswerToAQuestionNobodyWasShownIsRefused(t *testing.T) {
	q := ask.Question{
		Question: "How far should this reach?",
		Shape:    ask.ShapeChoose,
		Note:     ask.NoteRequired,
		Options:  []ask.Option{{Label: "one package"}, {Label: "both packages"}},
	}
	loop := &fakeLoop{askTool: "execute_command", question: &q}
	srv := newServerWith(loop)
	defer srv.Close()
	c := dial(t, srv)

	var opened SessionResult
	c.mustCall(MethodSessionStart, StartParams{}, &opened)

	// Before any question has been asked, which is what answering one in
	// advance would be.
	_, rerr := c.call(MethodQuestionAnswer, QuestionAnswerParams{
		Session: opened.Session, ID: "q1", Answered: ask.AnsweredSkipped})
	if rerr == nil || rerr.Code != CodeUnknownQuestion {
		t.Fatalf("answering a question nobody had asked was answered %v", rerr)
	}

	var turn TurnResult
	c.mustCall(MethodTurnStart, TurnParams{Session: opened.Session, Prompt: "do it"}, &turn)
	approval := c.waitApproval()
	c.mustCall(MethodApprovalAnswer, AnswerParams{Session: opened.Session, ID: approval.ID, Decision: DecisionAllow}, nil)
	put := c.waitQuestion()

	// The approval answered a moment ago is in another series, so its id
	// names no question.
	_, rerr = c.call(MethodQuestionAnswer, QuestionAnswerParams{
		Session: opened.Session, ID: approval.ID, Answered: ask.AnsweredSkipped})
	if rerr == nil || rerr.Code != CodeUnknownQuestion {
		t.Fatalf("an approval's id answered a question: %v", rerr)
	}
	// `nobody to ask` is the surface's own answer and not one a client may
	// give: a client cannot report its own absence.
	_, rerr = c.call(MethodQuestionAnswer, QuestionAnswerParams{
		Session: opened.Session, ID: put.ID, Answered: ask.AnsweredNobody})
	if rerr == nil || rerr.Code != CodeInvalidParams {
		t.Fatalf("a client claimed the reader had gone: %v", rerr)
	}
	// And the two rules the card enforces as it collects an answer: a pick
	// is a pick, and a note the model said it needs is not optional.
	_, rerr = c.call(MethodQuestionAnswer, QuestionAnswerParams{
		Session: opened.Session, ID: put.ID, Answered: ask.AnsweredOnCard, Note: "neither"})
	if rerr == nil || rerr.Code != CodeInvalidParams {
		t.Fatalf("an on-the-card answer that picked nothing was answered %v", rerr)
	}
	_, rerr = c.call(MethodQuestionAnswer, QuestionAnswerParams{
		Session: opened.Session, ID: put.ID, Answered: ask.AnsweredOnCard, Picked: []string{"one package"}})
	if rerr == nil || rerr.Code != CodeInvalidParams {
		t.Fatalf("a required note was left off and the answer stood: %v", rerr)
	}

	// None of that spent the question, so the reader can still answer it.
	c.mustCall(MethodQuestionAnswer, QuestionAnswerParams{
		Session: opened.Session, ID: put.ID, Answered: ask.AnsweredOnCard,
		Picked: []string{"one package"}, Note: "start narrow"}, nil)
	c.waitEvent("close")

	// And the same id again, now that it has been.
	_, rerr = c.call(MethodQuestionAnswer, QuestionAnswerParams{
		Session: opened.Session, ID: put.ID, Answered: ask.AnsweredSkipped})
	if rerr == nil || rerr.Code != CodeUnknownQuestion {
		t.Fatalf("an answered question was answerable again: %v", rerr)
	}

	loop.mu.Lock()
	defer loop.mu.Unlock()
	if len(loop.answers) != 1 || loop.answers[0].Note != "start narrow" {
		t.Errorf("the loop was told %v", loop.answers)
	}
}
