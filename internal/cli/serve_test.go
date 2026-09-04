package cli

// What a client that is not shhh's own terminal gets when it drives the
// agent, asserted against the built binary for the same reason the unattended
// run's contract is: the protocol is a promise made to another process, and
// one checked a function call away from the command that serves it can be
// broken anywhere along the way with none of these noticing.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/rpc"
)

// rpcClient is a test client: it writes one JSON object per line, reads
// whatever comes back on a goroutine of its own, and files each line as an
// answer to a call it made, an event, or a call waiting for a decision.
type rpcClient struct {
	t *testing.T
	w io.Writer

	mu        sync.Mutex
	next      int
	waiting   map[int]chan rpcReply
	events    chan json.RawMessage
	approvals chan rpc.ApprovalParams
}

// rpcReply is one answer as the client reads it back. The result stays raw so
// a case decodes only the fields it is about.
type rpcReply struct {
	Result json.RawMessage
	Err    *rpc.Error
}

func newRPCClient(t *testing.T, w io.Writer, r io.Reader) *rpcClient {
	c := &rpcClient{t: t, w: w,
		waiting:   map[int]chan rpcReply{},
		events:    make(chan json.RawMessage, 256),
		approvals: make(chan rpc.ApprovalParams, 16)}
	go c.read(r)
	return c
}

func (c *rpcClient) read(r io.Reader) {
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

func (c *rpcClient) file(line []byte) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *rpc.Error      `json:"error"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	switch msg.Method {
	case rpc.MethodSessionEvent:
		var p rpc.EventParams
		if json.Unmarshal(msg.Params, &p) == nil {
			c.events <- p.Event
		}
		return
	case rpc.MethodApprovalRequest:
		var p rpc.ApprovalParams
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
		reply <- rpcReply{Result: msg.Result, Err: msg.Error}
	}
}

func (c *rpcClient) call(method string, params any) rpcReply {
	c.t.Helper()
	c.mu.Lock()
	c.next++
	id := c.next
	reply := make(chan rpcReply, 1)
	c.waiting[id] = reply
	c.mu.Unlock()

	raw, err := json.Marshal(params)
	if err != nil {
		c.t.Fatal(err)
	}
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`+"\n", id, method, raw)
	if _, err := c.w.Write([]byte(body)); err != nil {
		c.t.Fatalf("sending %s: %v", method, err)
	}
	select {
	case res := <-reply:
		return res
	case <-time.After(90 * time.Second):
		c.t.Fatalf("%s was never answered", method)
	}
	return rpcReply{}
}

func (c *rpcClient) mustCall(method string, params, out any) {
	c.t.Helper()
	res := c.call(method, params)
	if res.Err != nil {
		c.t.Fatalf("%s: %v", method, res.Err)
	}
	if out != nil {
		if err := json.Unmarshal(res.Result, out); err != nil {
			c.t.Fatalf("%s result %s: %v", method, res.Result, err)
		}
	}
}

func (c *rpcClient) waitApproval() rpc.ApprovalParams {
	c.t.Helper()
	select {
	case p := <-c.approvals:
		return p
	case <-time.After(90 * time.Second):
		c.t.Fatal("the run never put a call to the client")
	}
	return rpc.ApprovalParams{}
}

// drainToClose reads events until the turn's close line, and hands back every
// event of the turn in the order they arrived.
func (c *rpcClient) drainToClose() []jsonEvent {
	c.t.Helper()
	var got []jsonEvent
	deadline := time.After(90 * time.Second)
	for {
		select {
		case raw := <-c.events:
			var ev jsonEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				c.t.Fatalf("an event that is not one: %s", raw)
			}
			got = append(got, ev)
			if ev.Kind == observe.EventClose {
				return got
			}
		case <-deadline:
			c.t.Fatalf("the turn never closed; events so far: %v", kindsOf(got))
		}
	}
}

// kindsOf is the shape of a run: what happened, in order, without the text
// each line carried. It is what two surfaces of the same run have to agree on.
func kindsOf(events []jsonEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, ev := range events {
		kind := ev.Kind
		if ev.Tool != "" {
			kind += "(" + ev.Tool + ")"
		}
		kinds = append(kinds, kind)
	}
	return kinds
}

// jsonlKinds is the same reading taken off what `-p --output jsonl` printed.
func jsonlKinds(t *testing.T, stdout string) []jsonEvent {
	t.Helper()
	var got []jsonEvent
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("the run printed a line that is not an event: %s", line)
		}
		got = append(got, ev)
	}
	return got
}

// serveOverStdio starts the binary speaking the protocol on its own stdin and
// stdout, which is the transport a client that spawned shhh itself uses.
func serveOverStdio(t *testing.T, s printSession, args ...string) *rpcClient {
	t.Helper()
	if shhhBuildErr != nil {
		t.Fatalf("the binary these tests drive was not built: %v", shhhBuildErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	cmd := exec.CommandContext(ctx, shhhBinary, append([]string{"serve", "--stdio"}, args...)...)
	cmd.Dir = s.dir
	cmd.Env = s.env()
	var errs strings.Builder
	cmd.Stderr = &errs
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the server: %v", err)
	}
	t.Cleanup(func() {
		// Closing stdin is how a client says it is done; the process ends
		// with it. The kill is only for a server that did not.
		_ = in.Close()
		done := make(chan struct{})
		go func() { defer close(done); _ = cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		cancel()
		if t.Failed() {
			t.Logf("server stderr: %s", errs.String())
		}
	})
	return newRPCClient(t, in, out)
}

// serveOnUnixSocket starts a server listening on a socket and hands back the
// path, so a case can open as many clients on it as it needs.
func serveOnUnixSocket(t *testing.T, s printSession) string {
	t.Helper()
	if shhhBuildErr != nil {
		t.Fatalf("the binary these tests drive was not built: %v", shhhBuildErr)
	}
	// Under the shortest directory available: a unix socket path is bounded
	// at about a hundred bytes by the operating system, and a test temporary
	// directory is most of that on its own.
	path := filepath.Join(t.TempDir(), "s")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	cmd := exec.CommandContext(ctx, shhhBinary, "serve", "--socket", path)
	cmd.Dir = s.dir
	cmd.Env = s.env()
	var errs strings.Builder
	cmd.Stderr = &errs
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		if t.Failed() {
			t.Logf("server stderr: %s", errs.String())
		}
	})
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the server never opened %s\nstderr: %s", path, errs.String())
	return ""
}

func dialServe(t *testing.T, path string) *rpcClient {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dialling %s: %v", path, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return newRPCClient(t, conn, conn)
}

// A client that is not this program's terminal runs a turn, answers the one
// call the turn may not make unasked, steers it, and reads the same events
// the unattended run prints. The two runs are the same script through the
// same loop, so what they say about it has to be the same too — an event that
// reaches one surface and not the other is how the two vocabularies come
// apart, and nothing fails when they do.
func TestServe_AClientReadsTheSameRunTheStreamPrints(t *testing.T) {
	script := []reply{
		{tool: "execute_command", args: map[string]string{"command": "echo hi"}},
		{text: "done"},
	}

	printed := startFakeProvider(t, script...)
	ps := newPrintSession(t, printed)
	stdout, stderr, code := ps.run(t, "", "code", "-p", "--output", "jsonl", "--yes", "do it")
	if code != exitDone {
		t.Fatalf("the unattended run exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	want := kindsOf(jsonlKinds(t, stdout))

	served := startFakeProvider(t, script...)
	ss := newPrintSession(t, served)
	c := serveOverStdio(t, ss)

	var opened rpc.SessionResult
	c.mustCall(rpc.MethodSessionStart, rpc.StartParams{}, &opened)
	var turn rpc.TurnResult
	c.mustCall(rpc.MethodTurnStart, rpc.TurnParams{Session: opened.Session, Prompt: "do it"}, &turn)

	req := c.waitApproval()
	if req.Tool != "execute_command" {
		t.Fatalf("the client was asked about %q", req.Tool)
	}
	// An id nobody was shown is refused, whichever way it is spelled: there
	// is no name for a request that has not happened, which is what stops a
	// client approving a tier rather than a call.
	if res := c.call(rpc.MethodApprovalAnswer, rpc.AnswerParams{
		Session: opened.Session, ID: req.ID + "-not-mine", Decision: rpc.DecisionAllow,
	}); res.Err == nil || res.Err.Code != rpc.CodeUnknownApproval {
		t.Fatalf("an answer to a request nobody was shown was accepted: %v", res.Err)
	}
	// The turn is demonstrably running — it is waiting on this decision — so
	// this is the one moment a steer can be sent and known to have been read.
	c.mustCall(rpc.MethodTurnSteer, rpc.SteerParams{Session: opened.Session, Text: "and keep it short"}, nil)
	c.mustCall(rpc.MethodApprovalAnswer, rpc.AnswerParams{
		Session: opened.Session, ID: req.ID, Decision: rpc.DecisionAllow,
	}, nil)

	events := c.drainToClose()
	if got := kindsOf(events); !equalStrings(got, want) {
		t.Errorf("the protocol's events are\n  %v\nand the stream's are\n  %v", got, want)
	}
	last := events[len(events)-1]
	if last.Outcome != observe.TurnDone || last.Exit == nil || *last.Exit != exitDone {
		t.Errorf("the close line says %+v", last)
	}
	if last.Final != "done" {
		t.Errorf("the turn answered %q", last.Final)
	}
	if steered := served.lastRequest(t); !containsString(steered, "and keep it short") {
		t.Errorf("the steer never reached the turn: %v", steered)
	}
}

// The deny list is not the client's to overrule. A call it allows still meets
// every standing refusal on the way to running, because the answer is a
// decision and a decision cannot outrank a rule — which is the whole of what
// keeps driving the agent from somewhere else from being a way around it.
func TestServe_AClientsAnswerDoesNotOutrankTheDenyList(t *testing.T) {
	f := startFakeProvider(t,
		reply{tool: "execute_command", args: map[string]string{"command": "echo hi"}},
		reply{text: "it would not let me"})
	s := newPrintSession(t, f)
	// Under the [behavior] table the session's config already opens with.
	appendConfig(t, s, "command_denylist = [\"echo\"]\n")

	c := serveOverStdio(t, s)
	var opened rpc.SessionResult
	c.mustCall(rpc.MethodSessionStart, rpc.StartParams{}, &opened)
	var turn rpc.TurnResult
	c.mustCall(rpc.MethodTurnStart, rpc.TurnParams{Session: opened.Session, Prompt: "run it"}, &turn)

	req := c.waitApproval()
	c.mustCall(rpc.MethodApprovalAnswer, rpc.AnswerParams{
		Session: opened.Session, ID: req.ID, Decision: rpc.DecisionAllow,
	}, nil)

	events := c.drainToClose()
	var refused bool
	for _, ev := range events {
		if ev.Kind == observe.EventDecision && ev.Decision == observe.DecisionDeny && ev.Reason == observe.ReasonDenylist {
			refused = true
		}
		if ev.Kind == observe.EventToolResult && strings.Contains(ev.Result, "echo hi") {
			t.Errorf("the command the deny list refuses ran anyway: %s", ev.Result)
		}
	}
	if !refused {
		t.Errorf("nothing said the deny list answered the call: %v", kindsOf(events))
	}
}

// Two clients on one session are two views of one conversation: the second is
// handed what has already been said on the way in, and is one of the audience
// for everything after.
func TestServe_ASecondClientSeesTheFirstsTranscript(t *testing.T) {
	f := startFakeProvider(t, reply{text: "the first answer"}, reply{text: "the second answer"})
	s := newPrintSession(t, f)
	path := serveOnUnixSocket(t, s)

	first := dialServe(t, path)
	var opened rpc.SessionResult
	first.mustCall(rpc.MethodSessionStart, rpc.StartParams{}, &opened)
	var turn rpc.TurnResult
	first.mustCall(rpc.MethodTurnStart, rpc.TurnParams{Session: opened.Session, Prompt: "the first question"}, &turn)
	first.drainToClose()

	second := dialServe(t, path)
	var joined rpc.SessionResult
	second.mustCall(rpc.MethodSessionResume, rpc.SessionParams{Session: opened.Session}, &joined)
	if joined.Session != opened.Session {
		t.Fatalf("the second client joined %q instead of %q", joined.Session, opened.Session)
	}
	for _, said := range []string{"the first question", "the first answer"} {
		if !strings.Contains(string(joined.Transcript), said) {
			t.Fatalf("the second client was not shown %q: %s", said, joined.Transcript)
		}
	}

	// And from here they are one audience for one turn.
	second.mustCall(rpc.MethodTurnStart, rpc.TurnParams{Session: opened.Session, Prompt: "the second question"}, &turn)
	if turn.Turn != 2 {
		t.Errorf("the session's second turn is turn %d", turn.Turn)
	}
	firstSaw := first.drainToClose()
	secondSaw := second.drainToClose()
	if !equalStrings(kindsOf(firstSaw), kindsOf(secondSaw)) {
		t.Errorf("the two clients saw different turns:\n  %v\n  %v", kindsOf(firstSaw), kindsOf(secondSaw))
	}
	if last := firstSaw[len(firstSaw)-1]; last.Turn != 2 {
		t.Errorf("the second turn's events say turn %d", last.Turn)
	}
}

// appendConfig adds to the config this session's runs read, so a case can say
// what the machine it drives is set up like. It appends to the file as it
// stands, which is one [behavior] table: a second header for a table that is
// already open is a config nothing loads.
func appendConfig(t *testing.T, s printSession, body string) {
	t.Helper()
	path := filepath.Join(s.home, "config", "shhh", "config.toml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + body); err != nil {
		t.Fatal(err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// A socket that is already there is refused rather than replaced: it is
// either a server that is still running, whose clients would silently stop
// being served, or the remains of one that died, and unlinking on the
// person's behalf is how the first case becomes the second.
func TestServeOnSocket_RefusesAPathThatIsAlreadyThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taken")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := serveOnSocket(context.Background(), rpc.NewServer(nil), path)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("listening on a path that is taken answered %v", err)
	}
}
