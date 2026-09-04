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
	"strings"
	"sync"
	"testing"
	"time"
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
}

// fakeProvider is the endpoint the binary is pointed at. It speaks the
// openai-compatible dialect because that is the one a base_url on its own
// redirects: every other built-in provider is its vendor's host, and pointing
// one of those at a local server would be testing the override rather than
// the run.
type fakeProvider struct {
	srv    *httptest.Server
	script []reply

	mu    sync.Mutex
	round int
	asked []string
}

func startFakeProvider(t *testing.T, script ...reply) *fakeProvider {
	t.Helper()
	if len(script) == 0 {
		t.Fatal("a fake provider with no script answers nothing")
	}
	f := &fakeProvider{script: script}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := f.next(r)
		if step.status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(step.status)
			fmt.Fprint(w, `{"error":{"message":"the endpoint is not answering"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeReply(w, step)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// next records what the run asked for and hands back the answer for this
// round. The prompt is the assertion the stdin shapes are made against: an
// argument, a pipe and both compose one message, and the request is the only
// place that says which one arrived.
func (f *fakeProvider) next(r *http.Request) reply {
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	asked := ""
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
		for _, m := range body.Messages {
			if m.Role != "user" {
				continue
			}
			var text string
			if json.Unmarshal(m.Content, &text) != nil {
				text = string(m.Content)
			}
			asked = text
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, asked)
	step := f.script[min(f.round, len(f.script)-1)]
	f.round++
	return step
}

// firstPrompt is the user message of the run's opening request.
func (f *fakeProvider) firstPrompt(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) == 0 {
		t.Fatal("the run never reached the provider")
	}
	return f.asked[0]
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

func writeReply(w http.ResponseWriter, step reply) {
	send := func(c sseChunk) {
		c.ID, c.Object, c.Model = "fake", "chat.completion.chunk", "fake-model"
		b, _ := json.Marshal(c)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
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

// run drives the binary and hands back exactly what whatever ran it would
// have: the two streams and the status.
func (s printSession) run(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	// A run that hangs would otherwise take the package's whole timeout and
	// report it against whichever test the panic landed in.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, shhhBinary, args...)
	cmd.Dir = s.dir
	cmd.Env = s.env()
	cmd.Stdin = strings.NewReader(stdin)
	var out, errs strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	var exited *exec.ExitError
	if err != nil && !errors.As(err, &exited) {
		t.Fatalf("running %v: %v\n%s", args, err, errs.String())
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
// 3 — the run was interrupted — is not among them, because no signal reaches
// the turn: the headless path installs no handler and nothing calls the
// loop's interrupt, so a SIGINT kills the process and what a script reads is
// the signal rather than a status. Asserting that answer here would settle
// the gap in place instead of leaving it visible.
func TestPrintRun_EveryStatusTheContractNames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script []reply
		args   []string
		stdin  string
		setUp  func(t *testing.T, s printSession)
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
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeProvider(t, tc.script...)
			s := newPrintSession(t, f)
			if tc.setUp != nil {
				tc.setUp(t, s)
			}
			out, errs, code := s.run(t, tc.stdin, tc.args...)
			if code != tc.code {
				t.Fatalf("the run exited %d, want %d\nstdout: %s\nstderr: %s", code, tc.code, out, errs)
			}
			if !strings.Contains(flatten(out+errs), tc.says) {
				t.Errorf("nothing said %q\nstdout: %s\nstderr: %s", tc.says, out, errs)
			}
		})
	}
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
