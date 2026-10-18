package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/rpc"
)

// fakeServeEnv, when set, makes this test binary a fake `shhh serve --stdio`
// that speaks the protocol from the script the variable names. It is the
// stdlib's helper-process pattern: the attempt starts its binary exactly as it
// starts the real one, and the binary it is given is this one.
const fakeServeEnv = "SHHH_TEST_FAKE_SERVE"

func TestMain(m *testing.M) {
	if script := os.Getenv(fakeServeEnv); script != "" {
		os.Exit(fakeServe(script))
	}
	os.Exit(m.Run())
}

// fakeScript is what the fake server does: for each turn, its steps in order.
// A turn with no steps closes at once with nothing to say.
type fakeScript struct {
	Log string `json:"log"`
	// Die is written to stderr before the fake exits without a word on the
	// protocol, which is a server that failed to open.
	Die   string       `json:"die,omitempty"`
	Turns [][]fakeStep `json:"turns"`
}

// fakeStep is one thing a turn does: write an event, put a question or a call
// to the client and wait for its answer, or hang until it is interrupted.
type fakeStep struct {
	Event    json.RawMessage `json:"event,omitempty"`
	Question string          `json:"question,omitempty"`
	Shape    string          `json:"shape,omitempty"`
	Options  []string        `json:"options,omitempty"`
	Approval string          `json:"approval,omitempty"`
	Hang     bool            `json:"hang,omitempty"`
	// Write is files the turn writes in the workspace, path to contents: the
	// agent's work, done where the check will look for it.
	Write map[string]string `json:"write,omitempty"`
}

// fakeServe is the fake server's main. Every call it is sent is written to the
// log, after a first line saying how it was started.
func fakeServe(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var s fakeScript
	if err := json.Unmarshal(raw, &s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if s.Die != "" {
		fmt.Fprintln(os.Stderr, s.Die)
		return 3
	}
	logFile, err := os.OpenFile(s.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer logFile.Close()

	var mu sync.Mutex
	logLine := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		_, _ = logFile.Write(append(b, '\n'))
		mu.Unlock()
	}
	write := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		_, _ = os.Stdout.Write(append(b, '\n'))
		mu.Unlock()
	}
	notify := func(method string, params any) {
		write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	}
	event := func(raw any) { notify(rpc.MethodSessionEvent, map[string]any{"session": "s1", "event": raw}) }

	wd, _ := os.Getwd()
	logLine(map[string]any{"argv": os.Args[1:], "dir": wd, "data": os.Getenv("XDG_DATA_HOME")})

	answers := map[string]chan json.RawMessage{}
	asked := 0
	interrupt := make(chan struct{}, 1)
	// The answer's channel is registered before the request goes out, so an
	// answer that arrives at once is not dropped.
	register := func(id string) chan json.RawMessage {
		ch := make(chan json.RawMessage, 1)
		mu.Lock()
		answers[id] = ch
		mu.Unlock()
		return ch
	}
	runTurn := func(n int) {
		var steps []fakeStep
		if n <= len(s.Turns) {
			steps = s.Turns[n-1]
		}
		closed := false
		for _, st := range steps {
			switch {
			case st.Event != nil:
				event(st.Event)
				var ev struct {
					Kind string `json:"kind"`
				}
				if json.Unmarshal(st.Event, &ev) == nil && ev.Kind == "close" {
					closed = true
				}
			case st.Question != "":
				mu.Lock()
				asked++
				id := fmt.Sprintf("q%d", asked)
				mu.Unlock()
				ch := register(id)
				opts := make([]map[string]string, 0, len(st.Options))
				for _, o := range st.Options {
					opts = append(opts, map[string]string{"label": o})
				}
				notify(rpc.MethodQuestionRequest, map[string]any{"session": "s1", "id": id,
					"question": st.Question, "shape": st.Shape, "options": opts, "note": "optional", "turn": n, "round": 1})
				<-ch
			case st.Approval != "":
				mu.Lock()
				asked++
				id := fmt.Sprintf("a%d", asked)
				mu.Unlock()
				ch := register(id)
				notify(rpc.MethodApprovalRequest, map[string]any{"session": "s1", "id": id,
					"tool": st.Approval, "arguments": "{}", "turn": n, "round": 1})
				var p struct {
					Decision string `json:"decision"`
				}
				_ = json.Unmarshal(<-ch, &p)
				event(map[string]any{"kind": "decision", "turn": n, "round": 1, "tool": st.Approval, "decision": p.Decision, "reason": "user"})
			case st.Write != nil:
				for name, body := range st.Write {
					_ = os.WriteFile(name, []byte(body), 0o644)
				}
			case st.Hang:
				<-interrupt
				event(map[string]any{"kind": "close", "turn": n, "outcome": "cancelled", "exit": 130, "final": ""})
				return
			}
		}
		if !closed {
			event(map[string]any{"kind": "close", "turn": n, "outcome": "done", "exit": 0, "final": "nothing scripted"})
		}
	}

	turn := 0
	br := bufio.NewReader(os.Stdin)
	for {
		line, err := br.ReadBytes('\n')
		if strings.TrimSpace(string(line)) != "" {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(line, &req) == nil {
				logLine(map[string]any{"method": req.Method, "params": req.Params})
				respond := func(result any) {
					write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
				}
				switch req.Method {
				case rpc.MethodSessionStart:
					respond(map[string]string{"session": "s1"})
				case rpc.MethodTurnStart:
					turn++
					respond(map[string]int{"turn": turn})
					go runTurn(turn)
				case rpc.MethodApprovalAnswer, rpc.MethodQuestionAnswer:
					var p struct {
						ID string `json:"id"`
					}
					_ = json.Unmarshal(req.Params, &p)
					respond(map[string]any{})
					mu.Lock()
					ch := answers[p.ID]
					delete(answers, p.ID)
					mu.Unlock()
					if ch != nil {
						ch <- req.Params
					}
				case rpc.MethodTurnInterrupt, rpc.MethodSessionEnd:
					respond(map[string]any{})
					select {
					case interrupt <- struct{}{}:
					default:
					}
				}
			}
		}
		if err != nil {
			return 0
		}
	}
}

// logEntry is one line of the fake's log: how it was started, or a call.
type logEntry struct {
	Argv   []string        `json:"argv"`
	Dir    string          `json:"dir"`
	Data   string          `json:"data"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// fakeServed points this process's next attempt at the fake with the script,
// and hands back the binary to run and where the fake's log will be.
func fakeServed(t *testing.T, s fakeScript) (bin, logPath string) {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	s.Log = filepath.Join(dir, "log")
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "script.json")
	if err := os.WriteFile(script, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeServeEnv, script)
	bin, err = os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return bin, s.Log
}

func readLog(t *testing.T, path string) []logEntry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []logEntry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e logEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

// sent is the log's calls of one method, in the order they were sent.
func sent(log []logEntry, method string) []json.RawMessage {
	var out []json.RawMessage
	for _, e := range log {
		if e.Method == method {
			out = append(out, e.Params)
		}
	}
	return out
}

// servedSuite is one workspace case with a persona, as a suite author writes
// it; extra is any more case keys.
func servedSuite(t *testing.T, turns int, extra string, lines ...string) []Case {
	t.Helper()
	root := t.TempDir()
	quoted := make([]string, len(lines))
	for i, l := range lines {
		quoted[i] = "'" + l + "'"
	}
	body := fmt.Sprintf("prompt = \"fix the failing Sum\"\ncheck = [\"true\"]\npersona = [%s]\nturns = %d\n%s",
		strings.Join(quoted, ", "), turns, extra)
	writeCase(t, root, "served", body)
	if err := os.WriteFile(filepath.Join(root, "served", workspaceDir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func ev(s string) fakeStep { return fakeStep{Event: json.RawMessage(s)} }

// The persona answers what the session puts to it — a question from its
// script, one it has no rule for with "no preference", a call it refuses and
// one it lets through — and the server is the binary started as a printed
// attempt is: in the copied workspace, with its own data directory and the
// forwarded flags.
func TestAServedAttemptAnswersAQuestionAndAnApproval(t *testing.T) {
	bin, logPath := fakeServed(t, fakeScript{Turns: [][]fakeStep{{
		ev(`{"kind":"tool-call","turn":1,"round":1,"id":"c1","tool":"ask","arguments":"{}"}`),
		{Question: "Which package holds the Sum to fix?", Shape: "choose", Options: []string{"stats", "calc"}},
		{Question: "Should the test name change?", Shape: "text"},
		ev(`{"kind":"tool-result","turn":1,"round":1,"id":"c1","tool":"ask","result":"{}"}`),
		{Approval: "git_write"},
		{Approval: "edit_file"},
		ev(`{"kind":"close","turn":1,"round":2,"outcome":"done","exit":0,"final":"Done.","usage":{"prompt_tokens":40,"completion_tokens":4}}`),
	}}})
	cases := servedSuite(t, 2, "assert = [\"asks\", \"no-denial\"]\n",
		`question (?i)which package => calc`,
		`approval git_write => deny`,
		`close .* => stop`)

	sum, err := Run(context.Background(), cases, Options{Binary: bin, Args: []string{"--model", "m"}})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err != nil {
		t.Fatalf("the attempt broke: %v", a.Err)
	}
	if a.Turns != 1 || a.Unscripted != 1 {
		t.Errorf("turns %d, unscripted %d, want 1 and 1", a.Turns, a.Unscripted)
	}
	// asks held; no-denial broke on the call the script refused, which is
	// how a case sees that it was refused.
	if len(a.Findings) != 2 || !a.Findings[0].Held || a.Findings[1].Held {
		t.Errorf("findings = %+v", a.Findings)
	}
	if !a.CheckPassed || a.Passed {
		t.Errorf("check %v, passed %v: the check held and an assertion broke", a.CheckPassed, a.Passed)
	}

	log := readLog(t, logPath)
	start := log[0]
	if !slices.Equal(start.Argv, []string{"serve", "--stdio", "--model", "m"}) {
		t.Errorf("argv = %v", start.Argv)
	}
	if !strings.Contains(start.Dir, "shhh-eval-") || strings.Contains(start.Dir, cases[0].Dir) {
		t.Errorf("the server ran in %q, want the attempt's copy", start.Dir)
	}
	if !strings.Contains(start.Data, "shhh-eval-data-") {
		t.Errorf("data home = %q, want the attempt's own", start.Data)
	}

	var answers []rpc.QuestionAnswerParams
	for _, raw := range sent(log, rpc.MethodQuestionAnswer) {
		var p rpc.QuestionAnswerParams
		_ = json.Unmarshal(raw, &p)
		answers = append(answers, p)
	}
	if len(answers) != 2 {
		t.Fatalf("answers = %+v", answers)
	}
	if answers[0].Answered != "on the card" || !slices.Equal(answers[0].Picked, []string{"calc"}) {
		t.Errorf("scripted answer = %+v", answers[0])
	}
	if answers[1].Answered != "typed" || answers[1].Note != noPreference {
		t.Errorf("unscripted answer = %+v", answers[1])
	}
	var decisions []string
	for _, raw := range sent(log, rpc.MethodApprovalAnswer) {
		var p rpc.AnswerParams
		_ = json.Unmarshal(raw, &p)
		decisions = append(decisions, p.Decision)
	}
	if !slices.Equal(decisions, []string{"deny", "allow"}) {
		t.Errorf("decisions = %v, want the script's deny and the default allow", decisions)
	}
	if len(sent(log, rpc.MethodSessionEnd)) != 1 {
		t.Error("the session was not ended")
	}
}

// The ceiling counts the first prompt: a script that would go on for ever is
// stopped at turns, and each later turn is the script's words.
func TestAServedAttemptStopsAtTheCeiling(t *testing.T) {
	bin, logPath := fakeServed(t, fakeScript{})
	cases := servedSuite(t, 2, "", `close .* => keep going`)

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err != nil || a.Turns != 2 {
		t.Fatalf("err %v, turns %d, want 2", a.Err, a.Turns)
	}
	var prompts []string
	for _, raw := range sent(readLog(t, logPath), rpc.MethodTurnStart) {
		var p rpc.TurnParams
		_ = json.Unmarshal(raw, &p)
		prompts = append(prompts, p.Prompt)
	}
	if !slices.Equal(prompts, []string{"fix the failing Sum", "keep going"}) {
		t.Errorf("prompts = %q", prompts)
	}
}

// Every turn's lines are one trace: rounds are positions across the turns,
// calls add up, the usage is the last close line's — a served session's close
// states the session's total so far — and an assertion spans them all.
func TestAServedAttemptIsOneTrace(t *testing.T) {
	bin, _ := fakeServed(t, fakeScript{Turns: [][]fakeStep{
		{
			ev(`{"kind":"tool-call","turn":1,"round":1,"id":"1","tool":"read_file","arguments":"{\"path\":\"a.txt\"}"}`),
			ev(`{"kind":"tool-result","turn":1,"round":1,"id":"1","tool":"read_file","result":"x"}`),
			ev(`{"kind":"close","turn":1,"round":2,"outcome":"done","exit":0,"final":"done","usage":{"prompt_tokens":100,"completion_tokens":10}}`),
		},
		{
			ev(`{"kind":"tool-call","turn":2,"round":1,"id":"2","tool":"read_file","arguments":"{\"path\":\"a.txt\"}"}`),
			ev(`{"kind":"tool-result","turn":2,"round":1,"id":"2","tool":"read_file","result":"x"}`),
			ev(`{"kind":"close","turn":2,"round":2,"outcome":"done","exit":0,"final":"done","usage":{"prompt_tokens":250,"completion_tokens":30}}`),
		},
	}})
	cases := servedSuite(t, 3, "budget = [\"read_file 1\"]\n", `close (?i)done => and again`)
	priced := 0
	sum, err := Run(context.Background(), cases, Options{Binary: bin, Model: "m",
		Price: func(_ string, in, out int) (float64, bool) { priced = in + out; return 0.5, true }})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err != nil {
		t.Fatal(a.Err)
	}
	if a.Turns != 3 || a.Rounds != 2 || a.Calls != 2 {
		t.Errorf("turns %d, rounds %d, calls %d, want 3, 2 and 2", a.Turns, a.Rounds, a.Calls)
	}
	if a.TokensIn != 250 || a.TokensOut != 30 || priced != 280 || !a.Priced || a.Cost != 0.5 {
		t.Errorf("usage %d/%d priced on %d (%v, %v)", a.TokensIn, a.TokensOut, priced, a.Priced, a.Cost)
	}
	if len(a.Findings) != 1 || a.Findings[0].Held || a.Findings[0].Count != 2 {
		t.Errorf("the budget spans both turns: %+v", a.Findings)
	}
}

// The run's timeout still bounds a served attempt: the turn is interrupted and
// the session ended, and the attempt is a run that did not finish.
func TestAServedAttemptIsBoundedByTheRunsTimeout(t *testing.T) {
	bin, logPath := fakeServed(t, fakeScript{Turns: [][]fakeStep{{{Hang: true}}}})
	cases := servedSuite(t, 1, "", `close .* => stop`)

	sum, err := Run(context.Background(), cases, Options{Binary: bin, Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err == nil || !strings.Contains(a.Err.Error(), "did not finish inside") {
		t.Fatalf("err = %v", a.Err)
	}
	var methods []string
	for _, e := range readLog(t, logPath) {
		if e.Method == rpc.MethodTurnInterrupt || e.Method == rpc.MethodSessionEnd {
			methods = append(methods, e.Method)
		}
	}
	if !slices.Equal(methods, []string{rpc.MethodTurnInterrupt, rpc.MethodSessionEnd}) {
		t.Errorf("at the ceiling the client sent %v, want an interrupt and then the end", methods)
	}
}

// A server that dies is a run that broke, with the server's own words.
func TestAServerThatDiesIsARunThatBroke(t *testing.T) {
	bin, _ := fakeServed(t, fakeScript{Die: "no provider configured"})
	cases := servedSuite(t, 1, "", `close .* => stop`)

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Results[0].Verdict(); got != Errored {
		t.Fatalf("verdict = %v, want Errored", got)
	}
	if a := sum.Results[0].Attempts[0]; a.Err == nil || !strings.Contains(a.Err.Error(), "no provider configured") {
		t.Errorf("err = %v", a.Err)
	}
}

// A turn that failed is a run that broke, the way a printed run's non-zero
// exit is, and the conversation goes no further.
func TestAServedTurnThatFailedIsARunThatBroke(t *testing.T) {
	bin, logPath := fakeServed(t, fakeScript{Turns: [][]fakeStep{{
		ev(`{"kind":"close","turn":1,"outcome":"failed","exit":1,"final":"","error":"provider: 503"}`),
	}}})
	cases := servedSuite(t, 3, "", `close .* => again`)

	sum, err := Run(context.Background(), cases, Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	if a := sum.Results[0].Attempts[0]; a.Err == nil || !strings.Contains(a.Err.Error(), "provider: 503") {
		t.Errorf("err = %v", a.Err)
	}
	if n := len(sent(readLog(t, logPath), rpc.MethodTurnStart)); n != 1 {
		t.Errorf("%d turns started after a failed one, want 1", n)
	}
}

// The shipped case that proves the persona: it fails when the agent never
// asks, and its check fails when the answer was not used. The fake does the
// agent's part; the check is the case's own.
func TestTheAskWhichFileCaseFailsWithoutAQuestionAndWithoutTheAnswer(t *testing.T) {
	if !defaultLookPath("go") || !defaultLookPath("sh") {
		t.Skip("the case's check needs go and sh")
	}
	c, err := loadCase(filepath.Join("..", "..", "evals", "ask-which-file"))
	if err != nil {
		t.Fatal(err)
	}
	if c.persona == nil || len(c.Assertions) != 1 || c.Assertions[0].Word != wordAsks {
		t.Fatalf("the case declares persona %+v and assertions %+v", c.persona, c.Assertions)
	}
	asked := []fakeStep{
		ev(`{"kind":"tool-call","turn":1,"round":1,"id":"q","tool":"ask","arguments":"{}"}`),
		{Question: "Which Sum is failing: calc or stats?", Shape: "choose", Options: []string{"calc", "stats"}},
		ev(`{"kind":"tool-result","turn":1,"round":1,"id":"q","tool":"ask","result":"{}"}`),
	}
	fixCalc := fakeStep{Write: map[string]string{"calc/sum.go": "package calc\n\nfunc Sum(amounts []int) int {\n\ttotal := 0\n\tfor _, a := range amounts {\n\t\ttotal += a\n\t}\n\treturn total\n}\n"}}
	fixStats := fakeStep{Write: map[string]string{"stats/sum.go": "package stats\n\nfunc Sum(r []float64) float64 {\n\tt := 0.0\n\tfor _, x := range r {\n\t\tt += x\n\t}\n\treturn t\n}\n"}}
	closed := ev(`{"kind":"close","turn":1,"round":2,"outcome":"done","exit":0,"final":"Fixed."}`)

	for _, tc := range []struct {
		name              string
		steps             []fakeStep
		passed, checkHeld bool
	}{
		{"asked and used the answer", append(slices.Clone(asked), fixCalc, closed), true, true},
		{"never asked", []fakeStep{fixCalc, closed}, false, true},
		{"asked and fixed the other one", append(slices.Clone(asked), fixStats, closed), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, _ := fakeServed(t, fakeScript{Turns: [][]fakeStep{tc.steps}})
			sum, err := Run(context.Background(), []Case{c}, Options{Binary: bin})
			if err != nil {
				t.Fatal(err)
			}
			a := sum.Results[0].Attempts[0]
			if a.Err != nil {
				t.Fatalf("the attempt broke: %v", a.Err)
			}
			if a.Passed != tc.passed || a.CheckPassed != tc.checkHeld {
				t.Errorf("passed %v, check %v, want %v and %v (%s)", a.Passed, a.CheckPassed, tc.passed, tc.checkHeld, a.CheckOutput)
			}
		})
	}
}
