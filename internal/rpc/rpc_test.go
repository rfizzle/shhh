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
	// held, when set, is what a turn waits on before it finishes, so a test
	// can steer or interrupt a turn that is genuinely still running.
	held    chan struct{}
	release sync.Once

	mu          sync.Mutex
	prompts     []string
	turns       []int64
	steers      []string
	allowed     []bool
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

	mu        sync.Mutex
	next      int
	waiting   map[int]chan response
	events    chan json.RawMessage
	approvals chan ApprovalParams
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

	c := &client{t: t, enc: json.NewEncoder(here),
		waiting:   map[int]chan response{},
		events:    make(chan json.RawMessage, 64),
		approvals: make(chan ApprovalParams, 8)}
	go c.read(here)
	return c
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

	c := &client{t: t, enc: json.NewEncoder(here),
		waiting:   map[int]chan response{},
		events:    make(chan json.RawMessage, 64),
		approvals: make(chan ApprovalParams, 8)}
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

	c := &client{t: t, enc: json.NewEncoder(here),
		waiting:   map[int]chan response{},
		events:    make(chan json.RawMessage, 64),
		approvals: make(chan ApprovalParams, 8)}
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
