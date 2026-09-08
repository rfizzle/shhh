package cli

// What a script gets back from `shhh code -p`, asserted against the built
// binary rather than against the functions behind it. The status is a
// contract a script branches on
// (docs/capabilities/headless.md#the-exit-code-is-the-contract), and one
// checked only a function call away from the switch that decides it can be
// broken anywhere along the return path — the error dressing, the command
// tree, main — with none of those checks noticing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
)

// shhhBinary is the command these tests run, and shhhBuildErr is why there is
// none. The failure is kept rather than raised where it happens: a machine
// with no Go toolchain on PATH cannot build one, and taking the package down
// for that would take every test in it that never wanted a binary.
var (
	shhhBinary   string
	shhhBuildErr error
)

// buildShhhBinary links the command into dir for the whole package. It is a
// TestMain step and not a per-test one because every run below is the same
// binary, and a build inside each of them would put a link on each of them.
//
// It must run before TestMain repoints HOME. The build cache lives under the
// real one, and a build that cannot find it compiles the module from scratch
// — half a minute instead of a second, on every run of this package.
func buildShhhBinary(dir string) {
	tool, err := exec.LookPath("go")
	if err != nil {
		shhhBuildErr = fmt.Errorf("no go toolchain on PATH to build with: %w", err)
		return
	}
	// Under a directory of its own and not directly in dir, which is about
	// to become the package's config and state home: `shhh` there is the
	// name of the config directory, and a file where one is expected fails
	// every test in the package that loads a config with "not a directory".
	out := filepath.Join(dir, "bin", "shhh")
	cmd := exec.Command(tool, "build", "-o", out, "./cmd/shhh")
	// The module root, from this package's own directory. A test never
	// changes its working directory — that is what makes its whole package
	// uncacheable — so what moves is the build's.
	cmd.Dir = filepath.Join("..", "..")
	if b, err := cmd.CombinedOutput(); err != nil {
		shhhBuildErr = fmt.Errorf("go build ./cmd/shhh: %v: %s", err, b)
		return
	}
	shhhBinary = out
}

// reply is one scripted answer from the fake endpoint: text the model wrote,
// a tool call it asked for, or an HTTP status instead of an answer at all.
// The last reply in a script stands for every round after it, so a script of
// one tool call is a model that will never stop calling it.
type reply struct {
	text   string
	tool   string
	args   map[string]string
	status int
	// match, when set, is the request this answer is for: the text appears
	// in one of the request's user messages, and the answer is spent the
	// first time a request carries it.
	//
	// It is how a case with more than one conversation in flight stays
	// deterministic. A run and the child it spawned reach the same endpoint
	// in whatever order the machine gets to them, and so does the permission
	// classifier; a script read by round number would hand each of them
	// whichever answer the race happened to leave. The answers with no match
	// are read in order for every request none of the matched ones is for,
	// so a case can script two conversations and still say what everything
	// else gets.
	match string
	// hold writes the text and then leaves the stream open until the client
	// gives up on it. It is a model that has started answering and not
	// finished, which is the state a run has to be in for anything from
	// outside to interrupt a turn rather than a wait.
	hold bool
	// resume, when set, ends a held stream properly instead of leaving it
	// open: the model started answering, whatever the case had to have happen
	// while it was writing happened, and then it finished. A nil channel
	// never fires, which is the plain hold above.
	resume chan struct{}
}

// fakeProvider is the endpoint the binary is pointed at. It speaks the
// openai-compatible dialect because that is the one a base_url on its own
// redirects: every other built-in provider is its vendor's host, and pointing
// one of those at a local server would be testing the override rather than
// the run.
type fakeProvider struct {
	srv *httptest.Server
	// script is every answer; plain is the ones with no match, which are the
	// ones the round counter walks.
	script []reply
	plain  []reply
	// spent marks a matched answer already given, by its index in script.
	spent map[int]bool
	// holding says a held stream is open and the run is inside it. It is
	// buffered and written to without waiting, so an answer nobody is
	// listening for costs the request nothing.
	holding chan struct{}

	mu    sync.Mutex
	round int
	asked [][]string
	// offered is the tool names each request carried. It is the only place a
	// test can see what the model was given rather than what it did with it,
	// which is what a tool nobody registers has to be asserted against.
	offered [][]string
}

func startFakeProvider(t *testing.T, script ...reply) *fakeProvider {
	t.Helper()
	if len(script) == 0 {
		t.Fatal("a fake provider with no script answers nothing")
	}
	f := &fakeProvider{script: script, holding: make(chan struct{}, 1), spent: map[int]bool{}}
	for _, step := range script {
		if step.match == "" {
			f.plain = append(f.plain, step)
		}
	}
	if len(f.plain) == 0 {
		t.Fatal("a script of matched answers alone says nothing about the requests none of them is for")
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := f.next(r)
		if step.status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(step.status)
			fmt.Fprint(w, `{"error":{"message":"the endpoint is not answering"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if step.hold {
			f.holdOpen(w, r, step)
			return
		}
		writeReply(w, step)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// holdOpen writes the opening words of a reply and then keeps the stream open
// until the run drops it. A held request is the one place a turn waits long
// enough to be interrupted on purpose; the alternative — signalling a run at
// whatever point it happens to have reached — is a test that passes on a fast
// machine and reports a killed process on a slow one.
func (f *fakeProvider) holdOpen(w http.ResponseWriter, r *http.Request, step reply) {
	send := sseSender(w)
	send(sseChunk{Choices: []sseChoice{{Delta: sseDelta{Content: step.text}}}})
	select {
	case f.holding <- struct{}{}:
	default:
		// Nobody waiting to hear it, or somebody already told. Either way
		// the request goes on holding the stream, which is what it is for.
	}
	select {
	case <-step.resume:
		// The words are already out; what is left is the ending, so the run
		// reads the same answer it would have read without the wait.
		send(sseChunk{
			Choices: []sseChoice{{FinishReason: "stop"}},
			Usage:   &sseUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	case <-r.Context().Done():
	}
}

// next records what the run asked for and hands back the answer for this
// round. Every user message of the request is kept, in order: the last of
// them is what this round was asked, and the ones in front of it are what the
// conversation carried into it — which is the only place a resumed run can be
// seen from.
func (f *fakeProvider) next(r *http.Request) reply {
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	var asked, offered []string
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
		for _, m := range body.Messages {
			if m.Role != "user" {
				continue
			}
			var text string
			if json.Unmarshal(m.Content, &text) != nil {
				text = string(m.Content)
			}
			asked = append(asked, text)
		}
		for _, tl := range body.Tools {
			offered = append(offered, tl.Function.Name)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, asked)
	f.offered = append(f.offered, offered)
	for i, step := range f.script {
		if step.match == "" || f.spent[i] || !askedFor(asked, step.match) {
			continue
		}
		f.spent[i] = true
		return step
	}
	step := f.plain[min(f.round, len(f.plain)-1)]
	f.round++
	return step
}

// askedFor reports one of the request's user messages carrying the text an
// answer is written for.
func askedFor(asked []string, match string) bool {
	for _, m := range asked {
		if strings.Contains(m, match) {
			return true
		}
	}
	return false
}

// firstPrompt is the user message of the run's opening request: an argument,
// a pipe and both compose one message, and the request is the only place that
// says which one arrived.
func (f *fakeProvider) firstPrompt(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) == 0 {
		t.Fatal("the run never reached the provider")
	}
	first := f.asked[0]
	if len(first) == 0 {
		t.Fatal("the run's opening request asked nothing")
	}
	return first[len(first)-1]
}

// lastRequest is every user message of the most recent request, oldest first.
func (f *fakeProvider) lastRequest(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) == 0 {
		t.Fatal("the run never reached the provider")
	}
	return f.asked[len(f.asked)-1]
}

// toolsOffered is every tool name any request in this run carried. Every
// request and not the last one, because a tool registered once is registered
// for the run and a later round is not where it would go missing.
func (f *fakeProvider) toolsOffered(t *testing.T) map[string]bool {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.offered) == 0 {
		t.Fatal("the run never reached the provider")
	}
	names := map[string]bool{}
	for _, req := range f.offered {
		for _, n := range req {
			names[n] = true
		}
	}
	return names
}

// The chunk shape the dialect streams, cut down to the fields the client
// reads. A chunk is written whole rather than split across frames: what is
// under test is the run's answer to it, not the reassembly, which the
// provider package's own tests cover.
type (
	sseChunk struct {
		ID      string      `json:"id"`
		Object  string      `json:"object"`
		Model   string      `json:"model"`
		Choices []sseChoice `json:"choices"`
		Usage   *sseUsage   `json:"usage,omitempty"`
	}
	sseChoice struct {
		Index        int      `json:"index"`
		Delta        sseDelta `json:"delta"`
		FinishReason string   `json:"finish_reason,omitempty"`
	}
	sseDelta struct {
		Content   string    `json:"content,omitempty"`
		ToolCalls []sseCall `json:"tool_calls,omitempty"`
	}
	sseCall struct {
		Index    int     `json:"index"`
		ID       string  `json:"id"`
		Type     string  `json:"type"`
		Function sseFunc `json:"function"`
	}
	sseFunc struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	sseUsage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
)

// sseSender writes one chunk and flushes it, which is what makes a reply
// arrive in pieces rather than all at once when the handler returns — the
// difference a run that is interrupted mid-stream depends on.
func sseSender(w http.ResponseWriter) func(sseChunk) {
	return func(c sseChunk) {
		c.ID, c.Object, c.Model = "fake", "chat.completion.chunk", "fake-model"
		b, _ := json.Marshal(c)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func writeReply(w http.ResponseWriter, step reply) {
	send := sseSender(w)
	if step.tool != "" {
		args, _ := json.Marshal(step.args)
		send(sseChunk{Choices: []sseChoice{{Delta: sseDelta{ToolCalls: []sseCall{{
			Type: "function", ID: "call-1", Function: sseFunc{Name: step.tool, Arguments: string(args)},
		}}}}}})
		send(sseChunk{Choices: []sseChoice{{FinishReason: "tool_calls"}}})
	} else {
		send(sseChunk{Choices: []sseChoice{{Delta: sseDelta{Content: step.text}}}})
		send(sseChunk{
			Choices: []sseChoice{{FinishReason: "stop"}},
			Usage:   &sseUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// printSession is one machine for one run: a directory to work in and a home
// for the config and the store, so a run reads nothing the developer set for
// themselves and leaves nothing behind when it is over.
type printSession struct {
	dir  string
	home string
	url  string
}

func newPrintSession(t *testing.T, f *fakeProvider) printSession {
	t.Helper()
	if shhhBuildErr != nil {
		t.Fatalf("the binary these tests drive was not built: %v", shhhBuildErr)
	}
	s := printSession{dir: t.TempDir(), home: t.TempDir(), url: f.srv.URL}
	dir := filepath.Join(s.home, "config", "shhh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stall is asked again three times over sixteen seconds, and none of
	// what is asserted below is about the waiting: a run told to wait none
	// reports the failure it already has.
	body := "[behavior]\nprovider_retries = 0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s printSession) env() []string {
	return append(os.Environ(),
		"HOME="+s.home,
		"XDG_CONFIG_HOME="+filepath.Join(s.home, "config"),
		"XDG_DATA_HOME="+filepath.Join(s.home, "data"),
		"SHHH_PROVIDER=openai-compatible",
		"SHHH_BASE_URL="+s.url+"/v1",
		"SHHH_API_KEY=fake-key",
		"SHHH_MODEL=fake-model",
		// The last setting the environment outranks the file for. It is
		// pinned rather than left alone so that a developer who exports one
		// for their own sessions is not quietly running a different test.
		"SHHH_REASONING=medium",
	)
}

// command is the binary as this session runs it: the directory it works in,
// the environment pointing it at the fake endpoint and at a home of its own,
// and the two streams every case here asserts against.
func (s printSession) command(ctx context.Context, stdin string, args ...string) (*exec.Cmd, *strings.Builder, *strings.Builder) {
	cmd := exec.CommandContext(ctx, shhhBinary, args...)
	cmd.Dir = s.dir
	cmd.Env = s.env()
	cmd.Stdin = strings.NewReader(stdin)
	var out, errs strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errs
	return cmd, &out, &errs
}

// run drives the binary and hands back exactly what whatever ran it would
// have: the two streams and the status.
func (s printSession) run(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	// A run that hangs would otherwise take the package's whole timeout and
	// report it against whichever test the panic landed in.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cmd, out, errs := s.command(ctx, stdin, args...)
	return finished(t, cmd, cmd.Run(), out, errs)
}

// signalDuring drives the binary until the endpoint is holding its stream
// open, sends the process a signal, and hands back what it left behind. It is
// the one ending the cases below cannot reach any other way: an interrupt
// comes from outside the process, and nothing inside it stands in for one.
func (s printSession) signalDuring(t *testing.T, f *fakeProvider, sig os.Signal, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cmd, out, errs := s.command(ctx, "", args...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %v: %v", args, err)
	}
	select {
	case <-f.holding:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("the run never opened a stream to interrupt\nstderr: %s", errs.String())
	}
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatalf("signalling the run: %v", err)
	}
	return finished(t, cmd, cmd.Wait(), out, errs)
}

// finished reads the two streams and the status off a process that is over,
// however it ended. A status is a status whether the run chose it or the
// operating system did: exec reports a run killed by a signal as -1, which is
// exactly the answer this whole surface exists to stop a script getting.
func finished(t *testing.T, cmd *exec.Cmd, err error, out, errs *strings.Builder) (stdout, stderr string, code int) {
	t.Helper()
	var exited *exec.ExitError
	if err != nil && !errors.As(err, &exited) {
		t.Fatalf("running %v: %v\n%s", cmd.Args, err, errs.String())
	}
	return out.String(), errs.String(), cmd.ProcessState.ExitCode()
}

// trust marks the checkout the run works in as one whose quality suites may
// load. Nothing else opens the gate: a suite is command text that arrived
// with a clone, and an untrusted checkout gets no runner at all.
func (s printSession) trust(t *testing.T) {
	t.Helper()
	if _, errs, code := s.run(t, "", "doctor", "trust"); code != 0 {
		t.Fatalf("`shhh doctor trust` exited %d: %s", code, errs)
	}
}

// Every status the contract names, produced by the run rather than asserted
// of the projection behind it. Each case also has to say what happened, so a
// code that is right for the wrong reason still fails.
//
// A case with a signal is driven differently and for the same reason the
// others are driven at all: an interrupt is something done to the process
// from outside, so it is sent to the process. The endpoint holds its stream
// open until the signal has landed, which is what makes the case about where
// a signal reaches the turn rather than about how fast the machine is.
func TestPrintRun_EveryStatusTheContractNames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script []reply
		args   []string
		stdin  string
		setUp  func(t *testing.T, s printSession)
		signal os.Signal
		code   int
		says   string
	}{{
		name:   "a turn that finished and nothing objected",
		script: []reply{{text: "the answer"}},
		args:   []string{"code", "-p", "say hi"},
		code:   0,
		says:   "the answer",
	}, {
		name:   "an invocation that could not run at all",
		script: []reply{{text: "never asked"}},
		args:   []string{"code", "-p", "--output", "smoke-signal", "say hi"},
		code:   1,
		says:   "one of text, json or jsonl",
	}, {
		name:   "a turn that used up its tool rounds",
		script: []reply{{tool: "read_file", args: map[string]string{"path": "there.txt"}}},
		args:   []string{"code", "-p", "--max-rounds", "1", "read it"},
		setUp: func(t *testing.T, s printSession) {
			write(t, filepath.Join(s.dir, "there.txt"), "something to read\n")
		},
		code: 2,
		says: "tool round cap reached",
	}, {
		name:   "a provider that stopped answering",
		script: []reply{{status: http.StatusInternalServerError}},
		args:   []string{"code", "-p", "say hi"},
		code:   4,
		says:   "overloaded",
	}, {
		name: "checks that failed after the model stopped",
		script: []reply{
			{tool: "write_file", args: map[string]string{"path": "made.txt", "content": "work\n"}},
			{text: "wrote it"},
		},
		args: []string{"code", "-p", "--yes", "write something"},
		setUp: func(t *testing.T, s printSession) {
			write(t, filepath.Join(s.dir, ".shhh", "quality.json"),
				`{"on_close":"fast","suites":{"fast":{"checks":`+
					`[{"name":"the check","exe":"sh","args":["-c","exit 3"]}]}}}`)
			s.trust(t)
		},
		code: 5,
		says: `quality gate "fast"`,
	}, {
		name: "a call the policy refused as the last word",
		script: []reply{
			{tool: "execute_command", args: map[string]string{"command": "echo hi"}},
			{text: "it would not let me"},
		},
		args: []string{"code", "-p", "run something"},
		code: 6,
		says: "a tool call was refused",
	}, {
		// The other provider ending, and the reason it is not the one above:
		// nothing about waiting fixes a key the endpoint would not take, so
		// a script told to sit this one out would sit it out forever.
		name:   "a request the provider refused as it stands",
		script: []reply{{status: http.StatusUnauthorized}},
		args:   []string{"code", "-p", "say hi"},
		code:   8,
		says:   "unauthorized",
	}, {
		// The statuses are the print path's and not the coding agent's: a
		// conversation behind --print is the same run and leaves the same
		// contract behind.
		name:   "a conversation whose provider stopped answering",
		script: []reply{{status: http.StatusInternalServerError}},
		args:   []string{"chat", "-p", "say hi"},
		code:   4,
		says:   "overloaded",
	}, {
		name:   "a run somebody interrupted while it was working",
		script: []reply{{text: "thinking about it", hold: true}},
		args:   []string{"code", "-p", "take your time"},
		signal: os.Interrupt,
		code:   3,
		says:   "turn interrupted",
	}, {
		name:   "a run something else told to stop",
		script: []reply{{text: "thinking about it", hold: true}},
		args:   []string{"code", "-p", "take your time"},
		signal: syscall.SIGTERM,
		code:   3,
		says:   "turn interrupted",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeProvider(t, tc.script...)
			s := newPrintSession(t, f)
			if tc.setUp != nil {
				tc.setUp(t, s)
			}
			var out, errs string
			var code int
			if tc.signal != nil {
				out, errs, code = s.signalDuring(t, f, tc.signal, tc.args...)
			} else {
				out, errs, code = s.run(t, tc.stdin, tc.args...)
			}
			if code != tc.code {
				t.Fatalf("the run exited %d, want %d\nstdout: %s\nstderr: %s", code, tc.code, out, errs)
			}
			if !strings.Contains(flatten(out+errs), tc.says) {
				t.Errorf("nothing said %q\nstdout: %s\nstderr: %s", tc.says, out, errs)
			}
		})
	}
}

// What an interrupted run leaves behind, which is the whole difference
// between a turn that was stopped and a process that vanished. The status is
// what a script branches on, but the two things that make the status worth
// anything are here: the record says how the turn ended, and the conversation
// is in a slot the next run carries on from.
func TestPrintRun_AnInterruptedRunIsWrittenDownAndContinuable(t *testing.T) {
	f := startFakeProvider(t,
		reply{text: "starting on it", hold: true},
		reply{text: "and here is the rest"})
	s := newPrintSession(t, f)

	_, errs, code := s.signalDuring(t, f, os.Interrupt, "code", "-p", "the interrupted question")
	if code != exitInterrupted {
		t.Fatalf("the interrupted run exited %d, want %d\nstderr: %s", code, exitInterrupted, errs)
	}
	session, turns := s.lastRecord(t)
	if session != observe.SessionInterrupted {
		t.Errorf("the record closed the session as %q, want %q", session, observe.SessionInterrupted)
	}
	if len(turns) != 1 || turns[0] != observe.TurnCancelled {
		t.Errorf("the record's turns are %q, want one %q", turns, observe.TurnCancelled)
	}

	if _, errs, code := s.run(t, "", "code", "-p", "--continue", "and now?"); code != 0 {
		t.Fatalf("continuing the interrupted run exited %d\nstderr: %s", code, errs)
	}
	carried := f.lastRequest(t)
	if len(carried) == 0 || carried[len(carried)-1] != "and now?" {
		t.Fatalf("the continued run asked %q, want the new prompt last", carried)
	}
	if !strings.Contains(strings.Join(carried, "\n"), "the interrupted question") {
		t.Errorf("the continued run carried %q, want the interrupted turn with it", carried)
	}
}

// lastRecord is how the store says the most recent run came out: the
// session's own ending and the outcome of every turn it closed. It is read
// back through the binary's own export, because that is the reading somebody
// tuning against the record actually has.
func (s printSession) lastRecord(t *testing.T) (session string, turns []string) {
	t.Helper()
	out, errs, code := s.run(t, "", "observe", "export")
	if code != 0 {
		t.Fatalf("`shhh observe export` exited %d\nstderr: %s", code, errs)
	}
	var export struct {
		Sessions []struct {
			Outcome string `json:"outcome"`
			Events  []struct {
				Kind    string `json:"kind"`
				Outcome string `json:"outcome"`
			} `json:"events"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &export); err != nil {
		t.Fatalf("reading the export: %v\n%s", err, out)
	}
	if len(export.Sessions) == 0 {
		t.Fatal("the run recorded no session at all")
	}
	last := export.Sessions[len(export.Sessions)-1]
	for _, ev := range last.Events {
		if ev.Kind == "turn" {
			turns = append(turns, ev.Outcome)
		}
	}
	return last.Outcome, turns
}

// A run that finished writes its answer on stdout and nothing else, because
// `$(shhh code -p …)` is the answer. Everything the run has to say about
// working goes to stderr, where a script reading the answer is not looking.
func TestPrintRun_TheAnswerIsAllThatReachesStdout(t *testing.T) {
	f := startFakeProvider(t, reply{tool: "read_file", args: map[string]string{"path": "there.txt"}},
		reply{text: "the answer"})
	s := newPrintSession(t, f)
	write(t, filepath.Join(s.dir, "there.txt"), "something to read\n")

	out, errs, code := s.run(t, "", "code", "-p", "read it")
	if code != 0 {
		t.Fatalf("the run exited %d: %s", code, errs)
	}
	if strings.TrimSpace(out) != "the answer" {
		t.Errorf("stdout is %q, want the answer alone", out)
	}
	if !strings.Contains(errs, "read_file") {
		t.Errorf("the call the run made should be on stderr, got %q", errs)
	}
}

// The three ways a prompt arrives. A pipe on its own is the prompt; a pipe
// beside an argument is context for it, and the argument stays the question —
// which is the ordering `shhh code -p "explain this" < build.log` depends on.
func TestPrintRun_ThePromptIsTheArgumentTheStreamOrBoth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		want  []string
		not   string
	}{{
		name: "an argument on its own",
		args: []string{"code", "-p", "the question"},
		want: []string{"the question"},
		not:  "<context>",
	}, {
		name:  "a pipe on its own",
		args:  []string{"code", "-p"},
		stdin: "the piped question",
		want:  []string{"the piped question"},
		not:   "<context>",
	}, {
		name:  "both, and the argument is still the question",
		args:  []string{"code", "-p", "the question"},
		stdin: "the piped context",
		want:  []string{"<context>", "the piped context", "the question"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeProvider(t, reply{text: "the answer"})
			s := newPrintSession(t, f)
			if _, errs, code := s.run(t, tc.stdin, tc.args...); code != 0 {
				t.Fatalf("the run exited %d: %s", code, errs)
			}
			prompt := f.firstPrompt(t)
			for _, want := range tc.want {
				if !strings.Contains(prompt, want) {
					t.Errorf("the prompt %q does not carry %q", prompt, want)
				}
			}
			if tc.not != "" && strings.Contains(prompt, tc.not) {
				t.Errorf("the prompt %q was wrapped as context", prompt)
			}
		})
	}
	// Neither shape is a run: there is nothing to answer, and starting one
	// would spend a request to be told so.
	f := startFakeProvider(t, reply{text: "never asked"})
	s := newPrintSession(t, f)
	_, errs, code := s.run(t, "", "code", "-p")
	if code != 1 || !strings.Contains(flatten(errs), "needs a prompt") {
		t.Errorf("a run with no prompt at all exited %d saying %q", code, errs)
	}
}

// flatten is what an assertion about the streams is made against. Failures
// leave through the error dressing, which wraps to the terminal's width and
// capitalises the first word, so a phrase asserted verbatim breaks the day
// the sentence around it gets a word longer.
func flatten(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A conversation runs without a screen too, and what it leaves behind is what
// the coding agent's print run leaves behind: the same transcript, the same
// fields in it, the same status. A script that reads one reads the other.
func TestPrintRun_AConversationLeavesTheSameTranscript(t *testing.T) {
	f := startFakeProvider(t,
		reply{tool: "read_file", args: map[string]string{"path": "there.txt"}},
		reply{text: "the answer"})
	s := newPrintSession(t, f)
	write(t, filepath.Join(s.dir, "there.txt"), "something to read\n")

	out, errs, code := s.run(t, "", "chat", "--print", "--output", "json", "read it")
	if code != 0 {
		t.Fatalf("the run exited %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	var transcript struct {
		Final     string `json:"final"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &transcript); err != nil {
		t.Fatalf("the transcript did not parse: %v\n%s", err, out)
	}
	if transcript.Final != "the answer" || transcript.Truncated {
		t.Fatalf("the transcript says %+v", transcript)
	}
	if !strings.Contains(errs, "read_file") {
		t.Errorf("the read the run made should be on stderr, got %q", errs)
	}
}

// The class the run ended on, in both JSON shapes. The status says which
// branch to take and the class says why, so a consumer that logs what
// happened, or that wants to tell one provider ending from another, does not
// have to wait for a code to be minted per class or parse it out of a
// sentence.
//
// Both endings are driven rather than asserted of the projection, because the
// point is that the word the classification produced is the word that comes
// out the far end of the run.
func TestPrintRun_TheProviderFailureClassIsStated(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   int
		class  string
	}{
		{"a key the endpoint would not take", http.StatusUnauthorized, exitRejected, "unauthorized"},
		{"a provider failing on its own side", http.StatusInternalServerError, exitProvider, "overloaded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("json", func(t *testing.T) {
				f := startFakeProvider(t, reply{status: tc.status})
				s := newPrintSession(t, f)
				out, errs, code := s.run(t, "", "code", "-p", "--output", "json", "say hi")
				if code != tc.code {
					t.Fatalf("the run exited %d, want %d\nstderr: %s", code, tc.code, errs)
				}
				var transcript struct {
					Success    bool   `json:"success"`
					Error      string `json:"error"`
					ErrorClass string `json:"error_class"`
				}
				if err := json.Unmarshal([]byte(out), &transcript); err != nil {
					t.Fatalf("the transcript did not parse: %v\n%s", err, out)
				}
				if transcript.Success || transcript.ErrorClass != tc.class {
					t.Fatalf("the transcript says %+v, want a failure classed %q", transcript, tc.class)
				}
			})
			t.Run("jsonl", func(t *testing.T) {
				f := startFakeProvider(t, reply{status: tc.status})
				s := newPrintSession(t, f)
				out, errs, code := s.run(t, "", "code", "-p", "--output", "jsonl", "say hi")
				if code != tc.code {
					t.Fatalf("the run exited %d, want %d\nstderr: %s", code, tc.code, errs)
				}
				closing := closeLine(t, out)
				if closing.ErrorClass != tc.class {
					t.Fatalf("the close line says %+v, want a class of %q", closing, tc.class)
				}
				if closing.Exit == nil || *closing.Exit != tc.code {
					t.Fatalf("the close line's exit is %v, want %d", closing.Exit, tc.code)
				}
			})
		})
	}
}

// A run that ended on nothing the provider said carries no class at all,
// which is how a consumer tells a provider ending from every other kind
// without reading the sentence beside it.
func TestPrintRun_AnEndingThatWasNotAProviderCallHasNoClass(t *testing.T) {
	f := startFakeProvider(t,
		reply{tool: "execute_command", args: map[string]string{"command": "echo hi"}},
		reply{text: "it would not let me"})
	s := newPrintSession(t, f)

	out, errs, code := s.run(t, "", "code", "-p", "--output", "jsonl", "run something")
	if code != exitRefused {
		t.Fatalf("the run exited %d, want %d\nstderr: %s", code, exitRefused, errs)
	}
	closing := closeLine(t, out)
	if closing.Error == "" {
		t.Fatalf("a refused run's close line said nothing went wrong: %+v", closing)
	}
	if closing.ErrorClass != "" {
		t.Fatalf("the close line classed a refusal as %q", closing.ErrorClass)
	}
}

// closeLine is the last line of a jsonl run: the one a consumer that reads
// nothing else still reads.
func closeLine(t *testing.T, out string) (ev struct {
	Kind       string `json:"kind"`
	Outcome    string `json:"outcome"`
	Exit       *int   `json:"exit"`
	Error      string `json:"error"`
	ErrorClass string `json:"error_class"`
	Chat       string `json:"chat"`
	Session    string `json:"session"`
	Resume     string `json:"resume"`
}) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
			t.Fatalf("line %d of the stream did not parse: %v\n%s", i+1, err, lines[i])
		}
		if ev.Kind == observe.EventClose {
			return ev
		}
	}
	t.Fatalf("the stream has no close line:\n%s", out)
	return ev
}

// Where a run left off, in both shapes. A script that reads a status and
// wants to carry that run on had `--continue` and nothing else, which on a
// machine running two of these is whichever finished last — so the slot is
// named, the record row it cost is named beside it, and the command that
// opens the conversation again is spelled out rather than left to be
// assembled.
func TestPrintRun_TheRunSaysWhichSlotItLeft(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		f := startFakeProvider(t, reply{text: "the answer"})
		s := newPrintSession(t, f)
		out, errs, code := s.run(t, "", "code", "-p", "--output", "json", "say hi")
		if code != 0 {
			t.Fatalf("the run exited %d\nstderr: %s", code, errs)
		}
		var transcript struct {
			Chat    string `json:"chat"`
			Session string `json:"session"`
			Resume  string `json:"resume"`
		}
		if err := json.Unmarshal([]byte(out), &transcript); err != nil {
			t.Fatalf("the transcript did not parse: %v\n%s", err, out)
		}
		if transcript.Chat == "" || transcript.Session == "" {
			t.Fatalf("the transcript names no handle: %+v", transcript)
		}
		// The command names the slot rather than saying --continue, which is
		// the whole reason it is stated.
		want := "shhh code --resume='" + transcript.Chat + "'"
		if transcript.Resume != want {
			t.Fatalf("the transcript resumes with %q, want %q", transcript.Resume, want)
		}
		// And it is a handle: the conversation really is under that name.
		if listing, _, code := s.run(t, "", "chats", "list", "--json"); code != 0 ||
			!strings.Contains(listing, transcript.Chat) {
			t.Fatalf("the slot the run named is not in the saved conversations (exit %d):\n%s", code, listing)
		}
	})
	// A reader of the stream reads lines and never the transcript, so the
	// same three have to be on the close line or they do not reach it.
	t.Run("jsonl", func(t *testing.T) {
		f := startFakeProvider(t, reply{text: "the answer"})
		s := newPrintSession(t, f)
		out, errs, code := s.run(t, "", "chat", "--print", "--output", "jsonl", "say hi")
		if code != 0 {
			t.Fatalf("the run exited %d\nstderr: %s", code, errs)
		}
		closing := closeLine(t, out)
		if closing.Chat == "" || closing.Session == "" {
			t.Fatalf("the close line names no handle: %+v", closing)
		}
		// The conversation a reading run left is reopened with the reading
		// surface, not with the coding one.
		if want := "shhh chat --resume='" + closing.Chat + "'"; closing.Resume != want {
			t.Fatalf("the close line resumes with %q, want %q", closing.Resume, want)
		}
	})
}

// A run told which record row it is a stage of opens its own row under that
// one, and says which item and which stage it was. It is the driver whose
// children are processes that needs this: without it a sprint is dozens of
// runs the record cannot tell from dozens of unrelated ones.
func TestPrintRun_ARunToldItsParentFilesItsRowUnderIt(t *testing.T) {
	f := startFakeProvider(t, reply{text: "done"})
	s := newPrintSession(t, f)

	// The first run stands in for the driver's own row, which is a row like
	// any other; the second is the stage started under it.
	first, errs, code := s.run(t, "", "code", "-p", "--output", "json", "one")
	if code != 0 {
		t.Fatalf("the first run exited %d\nstderr: %s", code, errs)
	}
	var parent struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(first), &parent); err != nil {
		t.Fatalf("the transcript did not parse: %v\n%s", err, first)
	}

	stage := func(env ...string) int64 {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd, out, errs := s.command(ctx, "", "code", "-p", "--output", "json", "two")
		cmd.Env = append(cmd.Env, env...)
		body, stderr, code := finished(t, cmd, cmd.Run(), out, errs)
		if code != 0 {
			t.Fatalf("the stage exited %d\nstderr: %s", code, stderr)
		}
		var child struct {
			Session string `json:"session"`
		}
		if err := json.Unmarshal([]byte(body), &child); err != nil {
			t.Fatalf("the transcript did not parse: %v\n%s", err, body)
		}
		id, err := strconv.ParseInt(child.Session, 10, 64)
		if err != nil {
			t.Fatalf("the stage named its record row as %q: %v", child.Session, err)
		}
		return id
	}

	child := stage(
		"SHHH_PARENT_SESSION="+parent.Session,
		"SHHH_TODO_ITEM=s-290",
		"SHHH_TODO_STAGE=implement",
	)
	db, err := storage.OpenPath(filepath.Join(s.home, "data", "shhh", "shhh.db"))
	if err != nil {
		t.Fatalf("opening the store the runs wrote to: %v", err)
	}
	defer db.Close()
	rows, err := db.AgentSessions(time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	var got storage.AgentSessionSummary
	for _, r := range rows {
		if r.ID == child {
			got = r
		}
	}
	if got.ParentID == nil || strconv.FormatInt(*got.ParentID, 10) != parent.Session {
		t.Fatalf("the stage's row hangs under %v, want %s", got.ParentID, parent.Session)
	}
	if got.Settings == nil || got.Settings.Item != "s-290" || got.Settings.Stage != "implement" {
		t.Fatalf("the stage's row says it was %+v", got.Settings)
	}

	// An id naming no row is a link that cannot be made, and the record is
	// worth more than the link: the row is opened without it rather than not
	// opened at all.
	unknown, err := strconv.ParseInt(parent.Session, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	orphan := stage("SHHH_PARENT_SESSION=" + strconv.FormatInt(unknown+9999, 10))
	if orphan <= 0 {
		t.Fatal("a run given an id that names no row recorded nothing at all")
	}
}

// What a conversation cannot do, it cannot be told to do. A tool that writes
// is not registered, so a call naming one is answered as the unknown name it
// is rather than resolved by a policy that judges by tier — and the opt-in
// that approves what leaves the machine buys nothing here, because there was
// never a call to approve.
func TestPrintRun_AConversationHasNothingThatWrites(t *testing.T) {
	f := startFakeProvider(t,
		reply{tool: "write_file", args: map[string]string{"path": "made.txt", "content": "work\n"}},
		reply{text: "there is nothing here to write with"})
	s := newPrintSession(t, f)

	out, errs, code := s.run(t, "", "chat", "--print", "--yes", "write something")
	if code != 0 {
		t.Fatalf("the run exited %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "made.txt")); !os.IsNotExist(err) {
		t.Fatalf("a conversation wrote a file: %v", err)
	}
	if !strings.Contains(flatten(errs), "unknown tool: write_file") {
		t.Errorf("the call should have been answered as an unknown name, stderr: %s", errs)
	}
}

// A run behind --yes can delegate: the spawn is a gated call the flag
// answers, the child works and reports, and what it found reaches the run's
// answer. The child and the run reach the endpoint in whatever order the
// machine gets to them, which is why each answer says which request it is
// for rather than which round.
func TestPrintRun_WithYesTheRunCanDelegate(t *testing.T) {
	const prompt = "Delegate the search, then say what came back."
	const task = "Find the needle in the haystack"
	f := startFakeProvider(t,
		reply{match: prompt, tool: "spawn_agent", args: map[string]string{
			"role": "researcher", "name": "scout", "task": task,
		}},
		reply{match: prompt, tool: "agent_report", args: map[string]string{"name": "scout"}},
		reply{match: prompt, text: "The scout says: NEEDLE FOUND."},
		reply{match: task, text: "NEEDLE FOUND in haystack.go."},
		// Everything the run asks on its own — a reading, a title — gets
		// this, and none of it is what the case is about.
		reply{text: "noted"},
	)
	s := newPrintSession(t, f)
	stdout, stderr, code := s.run(t, "", "code", "--print", "--yes", prompt)
	if code != exitDone {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, exitDone, stdout, stderr)
	}
	if !strings.Contains(stdout, "The scout says: NEEDLE FOUND.") {
		t.Fatalf("the run's answer should carry what the child reported:\n%s", stdout)
	}
	if !f.wasAsked(task) {
		t.Fatalf("the child never ran; the endpoint was asked:\n%s", f.everyPrompt())
	}
}

// And a run that was given no answer to the spawn card is not offered the
// roles at all: a tool it could only be refused is worse than one it never
// saw, so the model is told the name is not one of its tools and no child
// is started.
func TestPrintRun_WithoutYesNothingIsDelegated(t *testing.T) {
	const prompt = "Delegate the search, then say what came back."
	const task = "Find the needle in the haystack"
	f := startFakeProvider(t,
		reply{match: prompt, tool: "spawn_agent", args: map[string]string{
			"role": "researcher", "name": "scout", "task": task,
		}},
		reply{match: prompt, text: "I have no way to delegate that."},
		reply{text: "noted"},
	)
	s := newPrintSession(t, f)
	stdout, stderr, code := s.run(t, "", "code", "--print", prompt)
	if code != exitDone {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, exitDone, stdout, stderr)
	}
	if f.wasAsked(task) {
		t.Fatalf("no child should have been started:\n%s", f.everyPrompt())
	}
	if !strings.Contains(stderr, "spawn_agent") {
		t.Fatalf("the refused call should be on the activity stream:\n%s", stderr)
	}
}

// --mode auto puts a call the flags did not answer to the permission
// classifier, and a classifier that cannot answer is a refusal rather than a
// prompt nobody would see. The status says the run was refused, which is the
// whole of what a script reads off a run that did nothing.
func TestPrintRun_AutoModeRefusesWhatTheClassifierCannotAnswer(t *testing.T) {
	f := startFakeProvider(t,
		// The classifier's own request is the one carrying the evidence
		// header, and it is answered with a failure both times it is tried.
		reply{match: "UNTRUSTED EVIDENCE", status: 500},
		reply{match: "UNTRUSTED EVIDENCE", status: 500},
		reply{tool: "execute_command", args: map[string]string{"command": "echo hello"}},
		reply{text: "I could not run it."},
	)
	s := newPrintSession(t, f)
	stdout, stderr, code := s.run(t, "", "code", "--print", "--mode", "auto", "run echo")
	if code != exitRefused {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, exitRefused, stdout, stderr)
	}
	if !f.wasAsked("UNTRUSTED EVIDENCE") {
		t.Fatalf("the classifier should have been asked:\n%s", f.everyPrompt())
	}
	if !strings.Contains(stderr, "not approved") {
		t.Fatalf("the refusal should say the command was not approved:\n%s", stderr)
	}
}

// A mode that needs somebody to prompt is refused rather than accepted and
// quietly turned into one of the two that do not.
func TestPrintRun_OnlyAutoIsAModeARunWithNoTerminalTakes(t *testing.T) {
	f := startFakeProvider(t, reply{text: "unreached"})
	s := newPrintSession(t, f)
	_, stderr, code := s.run(t, "", "code", "--print", "--mode", "manual", "do it")
	if code == exitDone {
		t.Fatalf("the run should have been refused\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "auto is the only mode") {
		t.Fatalf("the refusal should say which mode is taken:\n%s", stderr)
	}
}

// wasAsked reports the endpoint having been sent a request carrying this
// text in one of its user messages — which is how a case says a second
// conversation happened at all.
func (f *fakeProvider) wasAsked(text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, asked := range f.asked {
		if askedFor(asked, text) {
			return true
		}
	}
	return false
}

// everyPrompt is what the endpoint was asked, for a failure that has to say
// what happened instead.
func (f *fakeProvider) everyPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	for i, asked := range f.asked {
		fmt.Fprintf(&b, "request %d: %s\n", i+1, clipActivityLine(strings.Join(asked, " | ")))
	}
	return b.String()
}
