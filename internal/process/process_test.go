package process

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/sandbox"
)

func newTestSupervisor(t *testing.T, store StoreFunc) *Supervisor {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	s, err := New(t.TempDir(), store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Shrink the immediate-exit probe: these tests poll for exits themselves
	// (the probe's own behavior is covered by TestStart_InstantFailureReportsExit).
	s.probe = 50 * time.Millisecond
	t.Cleanup(s.Close)
	return s
}

func execute(t *testing.T, s *Supervisor, args string) string {
	t.Helper()
	out, err := s.Execute(json.RawMessage(args))
	if err != nil {
		t.Fatalf("Execute(%s): %v", args, err)
	}
	return out
}

func executeErr(t *testing.T, s *Supervisor, args string) error {
	t.Helper()
	_, err := s.Execute(json.RawMessage(args))
	if err == nil {
		t.Fatalf("Execute(%s): expected error", args)
	}
	return err
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartStatusStop_Lifecycle(t *testing.T) {
	s := newTestSupervisor(t, nil)

	out := execute(t, s, `{"action":"start","name":"sleeper","command":"sleep 30"}`)
	if !strings.Contains(out, "sleeper: running (pid ") {
		t.Fatalf("start should report running, got %q", out)
	}

	out = execute(t, s, `{"action":"status","name":"sleeper"}`)
	if !strings.Contains(out, "running") || !strings.Contains(out, "command: sleep 30") {
		t.Fatalf("unexpected status %q", out)
	}

	out = execute(t, s, `{"action":"stop","name":"sleeper"}`)
	if !strings.Contains(out, "exited") {
		t.Fatalf("stop should report the exit, got %q", out)
	}
}

func TestStart_InstantFailureReportsExit(t *testing.T) {
	s := newTestSupervisor(t, nil)
	s.probe = startProbe // the full probe window is what this test exercises
	out := execute(t, s, `{"action":"start","name":"boom","command":"exit 7"}`)
	if !strings.Contains(out, "exited (code 7)") {
		t.Fatalf("an instantly-failing command should report its exit in the start result, got %q", out)
	}
}

func TestStart_Validation(t *testing.T) {
	s := newTestSupervisor(t, nil)

	cases := []struct {
		args string
		want string
	}{
		{`{"action":"start","name":"bad name!","command":"true"}`, "invalid process name"},
		{`{"action":"start","name":"x","command":"  "}`, "command is required"},
		{`{"action":"start","command":"true"}`, "name is required"},
		{`{"action":"start","name":"x","command":"true","cwd":"../.."}`, "outside the workspace"},
		{`{"action":"start","name":"x","command":"true","env":{"PATH":"/evil"}}`, "cannot be overridden"},
		{`{"action":"start","name":"x","command":"true","env":{"HOME":"/evil"}}`, "cannot be overridden"},
		{`{"action":"start","name":"x","command":"true","env":{"BAD-NAME":"v"}}`, "invalid environment variable"},
	}
	for _, c := range cases {
		err := executeErr(t, s, c.args)
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Execute(%s) error %q should contain %q", c.args, err, c.want)
		}
	}
}

func TestStart_DuplicateRunningNameRejected(t *testing.T) {
	s := newTestSupervisor(t, nil)
	execute(t, s, `{"action":"start","name":"dup","command":"sleep 30"}`)
	err := executeErr(t, s, `{"action":"start","name":"dup","command":"true"}`)
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate running name should be rejected, got %v", err)
	}

	// An exited entry is replaceable.
	execute(t, s, `{"action":"stop","name":"dup"}`)
	out := execute(t, s, `{"action":"start","name":"dup","command":"exit 0"}`)
	if !strings.Contains(out, "process dup:") {
		t.Fatalf("restarting an exited name should work, got %q", out)
	}
}

func TestStart_TooManyProcesses(t *testing.T) {
	s := newTestSupervisor(t, nil)
	for i := 0; i < MaxProcesses; i++ {
		execute(t, s, fmt.Sprintf(`{"action":"start","name":"p%d","command":"sleep 30"}`, i))
	}
	err := executeErr(t, s, `{"action":"start","name":"straw","command":"sleep 30"}`)
	if !strings.Contains(err.Error(), "too many processes") {
		t.Fatalf("expected the process cap, got %v", err)
	}
}

func TestEnv_RestrictedToPathHomePlusExplicit(t *testing.T) {
	s := newTestSupervisor(t, nil)
	t.Setenv("LEAKY_TEST_VAR", "secret")

	execute(t, s, `{"action":"start","name":"env","command":"env","env":{"EXTRA":"val"}}`)
	waitFor(t, "env to exit", func() bool {
		return strings.Contains(execute(t, s, `{"action":"status","name":"env"}`), "exited")
	})
	out := execute(t, s, `{"action":"read","name":"env"}`)
	if strings.Contains(out, "LEAKY_TEST_VAR") {
		t.Errorf("session env must not leak into the process, got %q", out)
	}
	if !strings.Contains(out, "EXTRA=val") {
		t.Errorf("explicit env var missing, got %q", out)
	}
	if !strings.Contains(out, "PATH=") || !strings.Contains(out, "HOME=") {
		t.Errorf("PATH and HOME should be present, got %q", out)
	}
}

// A captured command inherits the whole environment and has the
// credential-shaped names taken out of it; a process starts from nothing and
// is handed the session's secrets back. The two arrive at the same place, and
// this is the half that would go quiet if somebody widened buildEnv — the
// spool of a process that dumped its environment reaches the evidence store
// and outlives the session by a week.
func TestEnv_CredentialShapedVariablesNeverReachAProcess(t *testing.T) {
	s := newTestSupervisor(t, nil)
	t.Setenv("SHHH_TEST_TOKEN", "undeclared")
	t.Setenv("SHHH_TEST_SECRET", "undeclared")
	t.Setenv("SHHH_TEST_KEY", "undeclared")
	s.SetEnv([]string{"DECLARED_KEY=lent-on-purpose"})

	execute(t, s, `{"action":"start","name":"env","command":"env"}`)
	waitFor(t, "env to exit", func() bool {
		return strings.Contains(execute(t, s, `{"action":"status","name":"env"}`), "exited")
	})
	out := execute(t, s, `{"action":"read","name":"env"}`)
	for _, name := range []string{"SHHH_TEST_TOKEN", "SHHH_TEST_SECRET", "SHHH_TEST_KEY"} {
		if strings.Contains(out, name) {
			t.Errorf("%s reached the process: %q", name, out)
		}
	}
	if !strings.Contains(out, "DECLARED_KEY=lent-on-purpose") {
		t.Errorf("a declared secret must reach the process, got %q", out)
	}
}

func TestInput_ReachesStdin(t *testing.T) {
	s := newTestSupervisor(t, nil)
	execute(t, s, `{"action":"start","name":"cat","command":"cat"}`)
	out := execute(t, s, `{"action":"input","name":"cat","text":"hello ring\n"}`)
	if !strings.Contains(out, "wrote 11 bytes") {
		t.Fatalf("unexpected input result %q", out)
	}
	waitFor(t, "echoed stdin", func() bool {
		return strings.Contains(execute(t, s, `{"action":"read","name":"cat"}`), "hello ring")
	})

	execute(t, s, `{"action":"stop","name":"cat"}`)
	if err := executeErr(t, s, `{"action":"input","name":"cat","text":"x"}`); !strings.Contains(err.Error(), "exited") {
		t.Fatalf("input to an exited process should fail cleanly, got %v", err)
	}
}

func TestRead_PagingAndClamping(t *testing.T) {
	s := newTestSupervisor(t, nil)
	// 26 letters over stdout; stderr gets its own marker.
	execute(t, s, `{"action":"start","name":"abc","command":"printf abcdefghijklmnopqrstuvwxyz; printf ERR 1>&2"}`)
	waitFor(t, "abc to exit", func() bool {
		return strings.Contains(execute(t, s, `{"action":"status","name":"abc"}`), "exited")
	})

	out := execute(t, s, `{"action":"read","name":"abc","offset":0,"limit":10}`)
	if !strings.Contains(out, "bytes 0-10 of 26") || !strings.Contains(out, "abcdefghij") {
		t.Fatalf("unexpected first page %q", out)
	}
	if !strings.Contains(out, "offset=10") {
		t.Fatalf("paging hint missing from %q", out)
	}

	out = execute(t, s, `{"action":"read","name":"abc","offset":10,"limit":10}`)
	if !strings.Contains(out, "klmnopqrst") {
		t.Fatalf("unexpected second page %q", out)
	}

	// Tail default: no offset returns the end of the stream.
	out = execute(t, s, `{"action":"read","name":"abc","limit":5}`)
	if !strings.Contains(out, "vwxyz") || !strings.Contains(out, "bytes 21-26 of 26") {
		t.Fatalf("unexpected tail read %q", out)
	}

	out = execute(t, s, `{"action":"read","name":"abc","stream":"stderr"}`)
	if !strings.Contains(out, "ERR") {
		t.Fatalf("stderr read missing marker, got %q", out)
	}

	if err := executeErr(t, s, `{"action":"read","name":"abc","stream":"both"}`); !strings.Contains(err.Error(), "unknown stream") {
		t.Fatalf("bad stream should fail, got %v", err)
	}
}

func TestRead_RingEvictionIsHonest(t *testing.T) {
	s := newTestSupervisor(t, nil)
	s.ringBytes = 16

	execute(t, s, `{"action":"start","name":"flood","command":"printf abcdefghijklmnopqrstuvwxyz"}`)
	waitFor(t, "flood to exit", func() bool {
		return strings.Contains(execute(t, s, `{"action":"status","name":"flood"}`), "exited")
	})

	out := execute(t, s, `{"action":"read","name":"flood","offset":0,"limit":100}`)
	if !strings.Contains(out, "evicted") {
		t.Fatalf("reading evicted bytes must say so, got %q", out)
	}
	if !strings.Contains(out, "klmnopqrstuvwxyz") {
		t.Fatalf("the ring window should still be served, got %q", out)
	}
}

func TestReap_StoresFullLogsAsEvidence(t *testing.T) {
	var mu sync.Mutex
	stored := map[string]string{}
	store := func(tool string, content []byte) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		stored[tool] = string(content)
		return "ev-" + tool, nil
	}
	s := newTestSupervisor(t, store)
	s.ringBytes = 4 // evict aggressively; the spool must still hold everything

	execute(t, s, `{"action":"start","name":"logs","command":"printf abcdefghij; printf ERR 1>&2"}`)
	waitFor(t, "evidence stored", func() bool {
		return strings.Contains(execute(t, s, `{"action":"status","name":"logs"}`), "evidence ev-")
	})

	mu.Lock()
	defer mu.Unlock()
	if stored["process:logs:stdout"] != "abcdefghij" {
		t.Errorf("full stdout log should be stored despite ring eviction, got %q", stored["process:logs:stdout"])
	}
	if stored["process:logs:stderr"] != "ERR" {
		t.Errorf("full stderr log should be stored, got %q", stored["process:logs:stderr"])
	}
}

func TestStop_TerminatesProcessTree(t *testing.T) {
	s := newTestSupervisor(t, nil)
	// The shell spawns a grandchild that prints its own pid, then waits.
	execute(t, s, `{"action":"start","name":"tree","command":"(echo GRANDCHILD $$; sleep 30) & echo CHILD; wait"}`)
	waitFor(t, "grandchild pid", func() bool {
		return strings.Contains(execute(t, s, `{"action":"read","name":"tree"}`), "GRANDCHILD")
	})
	out := execute(t, s, `{"action":"read","name":"tree"}`)
	var gcpid int
	for _, line := range strings.Split(out, "\n") {
		if _, err := fmt.Sscanf(line, "GRANDCHILD %d", &gcpid); err == nil {
			break
		}
	}
	if gcpid == 0 {
		t.Fatalf("could not find grandchild pid in %q", out)
	}

	execute(t, s, `{"action":"stop","name":"tree"}`)
	waitFor(t, "grandchild to die", func() bool {
		return !pidAlive(gcpid)
	})
}

// pidAlive reports whether a pid still exists (signal 0 probe).
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(nil) == nil
}

func TestClose_TerminatesEverything(t *testing.T) {
	s := newTestSupervisor(t, nil)
	execute(t, s, `{"action":"start","name":"a","command":"sleep 30"}`)
	execute(t, s, `{"action":"start","name":"b","command":"sleep 30"}`)

	s.Close()
	for _, name := range []string{"a", "b"} {
		out := execute(t, s, fmt.Sprintf(`{"action":"status","name":"%s"}`, name))
		if !strings.Contains(out, "exited") {
			t.Errorf("process %s should be terminated by Close, got %q", name, out)
		}
	}
	if err := executeErr(t, s, `{"action":"start","name":"late","command":"true"}`); !strings.Contains(err.Error(), "shut down") {
		t.Fatalf("start after Close must be refused, got %v", err)
	}
}

func TestStatus_AllAndList(t *testing.T) {
	s := newTestSupervisor(t, nil)
	if out := execute(t, s, `{"action":"status"}`); !strings.Contains(out, "No processes") {
		t.Fatalf("empty status should say so, got %q", out)
	}
	execute(t, s, `{"action":"start","name":"one","command":"sleep 30"}`)
	out := execute(t, s, `{"action":"status"}`)
	if !strings.Contains(out, "one") || !strings.Contains(out, "running") {
		t.Fatalf("status of all should list the process, got %q", out)
	}
	if list := s.List(); !strings.Contains(list, "one") {
		t.Fatalf("List should show the process, got %q", list)
	}
}

func TestExecute_UnknownActionAndProcess(t *testing.T) {
	s := newTestSupervisor(t, nil)
	if err := executeErr(t, s, `{"action":"restart","name":"x"}`); !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("unexpected error %v", err)
	}
	if err := executeErr(t, s, `{"action":"status","name":"ghost"}`); !strings.Contains(err.Error(), "no process named") {
		t.Fatalf("unexpected error %v", err)
	}
	if err := executeErr(t, s, `{"action":"read"}`); !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestNeedsApproval_OnlyStartGates(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{`{"action":"start","name":"x","command":"true"}`, true},
		{`{"action":"status"}`, false},
		{`{"action":"read","name":"x"}`, false},
		{`{"action":"input","name":"x","text":"y"}`, false},
		{`{"action":"stop","name":"x"}`, false},
		{`not json`, true}, // fail closed
	}
	for _, c := range cases {
		if got := NeedsApproval(json.RawMessage(c.args)); got != c.want {
			t.Errorf("NeedsApproval(%s) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestStartSummary(t *testing.T) {
	name, command, err := StartSummary(json.RawMessage(`{"action":"start","name":"web","command":"npm run dev"}`))
	if err != nil || name != "web" || command != "npm run dev" {
		t.Fatalf("unexpected summary %q %q %v", name, command, err)
	}
	if _, _, err := StartSummary(json.RawMessage(`{"action":"start","name":"web"}`)); err == nil {
		t.Fatal("missing command must error")
	}
	if _, _, err := StartSummary(json.RawMessage(`{"action":"stop","name":"web"}`)); err == nil {
		t.Fatal("non-start action must error")
	}
}

func TestWrapExecutor_DispatchesAndFallsThrough(t *testing.T) {
	s := newTestSupervisor(t, nil)
	exec := s.WrapExecutor(func(name string, _ json.RawMessage) (string, error) {
		return "next:" + name, nil
	})
	if out, err := exec("other_tool", nil); err != nil || out != "next:other_tool" {
		t.Fatalf("fall-through broken: %q %v", out, err)
	}
	if out, err := exec(ToolName, json.RawMessage(`{"action":"status"}`)); err != nil || !strings.Contains(out, "No processes") {
		t.Fatalf("dispatch broken: %q %v", out, err)
	}
}

func TestSetEnv_ProcessesCarrySessionPairsOverExtras(t *testing.T) {
	s := newTestSupervisor(t, nil)
	s.SetEnv([]string{"SHHH_TEST_SECRET=vault"})
	extra := map[string]string{"SHHH_TEST_SECRET": "model", "OTHER": "kept"}
	if _, err := s.start("env", `printf '%s %s' "$SHHH_TEST_SECRET" "$OTHER"; sleep 0.2`, "", extra); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, err := s.read("env", "stdout", 0, 0)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.Contains(out, "vault kept") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session pair must win over the model's extra: %q", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A process is handed the session's secrets as environment variables, so its
// output is where one is most likely to be printed — and the spool it goes
// into is a copy that reaches the evidence store and outlives it.
func TestSetScrub_SpoolAndRingHoldNoValue(t *testing.T) {
	var mu sync.Mutex
	stored := map[string]string{}
	store := func(tool string, content []byte) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		stored[tool] = string(content)
		return "ev-" + tool, nil
	}
	s := newTestSupervisor(t, store)
	s.SetScrub(func(out string) string { return strings.ReplaceAll(out, "hunter2", "[secret:PW]") })

	execute(t, s, `{"action":"start","name":"leaky","command":"printf 'token hunter2 here'; printf 'err hunter2' 1>&2"}`)
	waitFor(t, "evidence stored", func() bool {
		return strings.Contains(execute(t, s, `{"action":"status","name":"leaky"}`), "evidence ev-")
	})

	// The ring the model pages and the spool on its way to the store are the
	// same bytes: a value in one and not the other would mean the read and
	// the stored log disagree about what the process printed.
	page := execute(t, s, `{"action":"read","name":"leaky","stream":"stdout","offset":0}`)
	if strings.Contains(page, "hunter2") || !strings.Contains(page, "token [secret:PW] here") {
		t.Fatalf("the ring must be scrubbed: %q", page)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := stored["process:leaky:stdout"]; got != "token [secret:PW] here" {
		t.Errorf("stdout spool = %q", got)
	}
	if got := stored["process:leaky:stderr"]; got != "err [secret:PW]" {
		t.Errorf("stderr spool = %q", got)
	}
}

// os/exec reads a short write as a failed one and tears the pipe down, so a
// scrub that changes the length must still report the caller's own count —
// scrubbing can never be why a process stops being captured.
func TestStreamBuf_WriteReportsTheCallersCount(t *testing.T) {
	b := newStreamBuf(64, 64, func(string) string { return "" })
	n, err := b.Write([]byte("hunter2"))
	if n != 7 || err != nil {
		t.Fatalf("Write = %d, %v; want 7, nil", n, err)
	}
	if b.size() != 0 {
		t.Fatalf("the buffers hold what the scrub returned, got %d bytes", b.size())
	}
}

// A pipe delivers whatever the OS hands over, so a value can arrive in two
// writes. The ring is scrubbed per write and keeps the offsets a read was
// told; the spool is the copy that becomes a file, and gets the whole-text
// pass that catches the split.
func TestStreamBuf_SpoolCatchesAValueSplitAcrossWrites(t *testing.T) {
	b := newStreamBuf(1024, 1024, func(s string) string {
		return strings.ReplaceAll(s, "supersecret", "[secret:S]")
	})
	for _, chunk := range []string{"head super", "secret tail"} {
		if _, err := b.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write(%q): %v", chunk, err)
		}
	}

	page, _, _, _ := b.readAt(0, 1024)
	if !strings.Contains(string(page), "supersecret") {
		t.Fatalf("the per-write scrub cannot see across the split: %q", page)
	}
	spool, _ := b.spoolCopy()
	if string(spool) != "head [secret:S] tail" {
		t.Fatalf("the stored copy must be scrubbed whole, got %q", spool)
	}
}

// A start spawns exactly what execute_command would have spawned, so it goes
// through the same wrap — and the directory the supervisor resolved travels
// with the argv, because a mechanism that chdirs is the one deciding where
// the process lands.
func TestSetContainment_StartRunsUnderTheWrap(t *testing.T) {
	s := newTestSupervisor(t, nil)
	var (
		mu       sync.Mutex
		sawDir   string
		sawArgv  []string
		wrapRuns int
	)
	s.SetContainment(Containment{
		Mechanism: "testwrap",
		Wrap: func(dir string, argv []string) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			sawDir, sawArgv, wrapRuns = dir, argv, wrapRuns+1
			return []string{"/bin/sh", "-c", "echo wrapped-by-the-mechanism"}, nil
		},
	})

	execute(t, s, `{"action":"start","name":"srv","command":"echo bare"}`)
	waitFor(t, "the wrapped argv to run", func() bool {
		return strings.Contains(
			execute(t, s, `{"action":"read","name":"srv","stream":"stdout","offset":0}`),
			"wrapped-by-the-mechanism")
	})

	mu.Lock()
	defer mu.Unlock()
	if wrapRuns != 1 {
		t.Fatalf("the wrap should run once per start, ran %d times", wrapRuns)
	}
	if sawDir != s.root {
		t.Errorf("the wrap must be handed the start's directory, got %q want %q", sawDir, s.root)
	}
	if len(sawArgv) == 0 || !strings.Contains(strings.Join(sawArgv, " "), "echo bare") {
		t.Errorf("the wrap must be handed the command's own argv, got %v", sawArgv)
	}
	if got := s.Contained(); got != "testwrap" {
		t.Errorf("Contained() = %q, want the mechanism in force", got)
	}
}

// The card, the report and the doctor all say what is containing this
// session's processes, and a supervisor nothing wraps has to say so rather
// than let a caller assume the ordinary command path's answer.
func TestContained_EmptyUntilAMechanismIsInForce(t *testing.T) {
	s := newTestSupervisor(t, nil)
	if got := s.Contained(); got != "" {
		t.Fatalf("a supervisor with no wrap is uncontained, got %q", got)
	}
	// A mechanism named with nothing behind it would be a supervisor that
	// reports containment and spawns bare; the pair decides together.
	s.SetContainment(Containment{Mechanism: "testwrap"})
	if got := s.Contained(); got != "" {
		t.Fatalf("a mechanism with no wrap is not in force, got %q", got)
	}
}

// Refused, never started bare: a process outlives the call that made it, so
// one running outside the mechanism would make every surface that reports
// this session wrong for as long as it lived.
func TestSetContainment_WrapFailureRefusesTheStart(t *testing.T) {
	s := newTestSupervisor(t, nil)
	s.SetContainment(Containment{
		Mechanism: "testwrap",
		Wrap: func(string, []string) ([]string, error) {
			return nil, fmt.Errorf("writable path /w is inside masked path /w/m")
		},
	})

	err := executeErr(t, s, `{"action":"start","name":"srv","command":"sleep 30"}`)
	for _, want := range []string{"testwrap", "writable path", "not started"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q should name %q", err, want)
		}
	}
	if list := s.List(); strings.Contains(list, "srv") {
		t.Fatalf("a refused start must leave nothing behind:\n%s", list)
	}
	if s.Running() != 0 {
		t.Fatalf("a refused start must not count as running")
	}

	// A refusal must not consume the record of a process that already ran
	// under that name: restarting it is exactly when the wrap is most
	// likely to be broken, and losing the last run's evidence with it would
	// take the reader's only account of what happened.
	s.SetContainment(Containment{})
	execute(t, s, `{"action":"start","name":"srv","command":"true"}`)
	waitFor(t, "the first run to exit", func() bool {
		return strings.Contains(execute(t, s, `{"action":"status","name":"srv"}`), "exited")
	})
	s.SetContainment(Containment{
		Mechanism: "testwrap",
		Wrap:      func(string, []string) ([]string, error) { return nil, fmt.Errorf("no") },
	})
	_ = executeErr(t, s, `{"action":"start","name":"srv","command":"sleep 30"}`)
	if !strings.Contains(execute(t, s, `{"action":"status","name":"srv"}`), "exited") {
		t.Fatal("a refused restart must leave the earlier run's record in place")
	}
}

// Running is what the containment report counts: the part of the session
// still going when the report is asked for.
func TestRunning_CountsWhatIsStillAlive(t *testing.T) {
	s := newTestSupervisor(t, nil)
	if s.Running() != 0 {
		t.Fatalf("a fresh supervisor runs nothing")
	}
	execute(t, s, `{"action":"start","name":"up","command":"sleep 30"}`)
	execute(t, s, `{"action":"start","name":"gone","command":"true"}`)
	waitFor(t, "the short process to exit", func() bool { return s.Running() == 1 })
	execute(t, s, `{"action":"stop","name":"up"}`)
	if got := s.Running(); got != 0 {
		t.Fatalf("Running() = %d after stopping the last one", got)
	}
}

// The claim this whole seam makes, against the real mechanism: the deny mask
// that keeps a command out of the user's keys keeps a started process out of
// them too. It skips where bubblewrap is unavailable — most machines are not
// Linux with unprivileged user namespaces — and the uncontained supervisor
// beside it is the control that would catch a wrap that quietly did nothing.
func TestStart_ContainedProcessCannotReadTheDenyMask(t *testing.T) {
	avail := sandbox.Detect()
	if !avail.OK || avail.Mechanism != "bwrap" {
		t.Skipf("no bubblewrap containment here: %s", avail.Detail)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("PRIVATE-KEY-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	const peek = `{"action":"start","name":"peek","command":"cat \"$HOME/.ssh/id_ed25519\""}`

	bare := newTestSupervisor(t, nil)
	execute(t, bare, peek)
	if out := streams(t, bare, "peek"); !strings.Contains(out, "PRIVATE-KEY-BYTES") {
		t.Fatalf("the control must read the key, or this test proves nothing:\n%s", out)
	}

	s := newTestSupervisor(t, nil)
	s.SetContainment(Containment{
		Mechanism: avail.Mechanism,
		Wrap: func(dir string, argv []string) ([]string, error) {
			return sandbox.WrapArgv(avail, sandbox.Policy{Workspace: s.root, Cwd: dir}, argv)
		},
	})
	execute(t, s, peek)
	out := streams(t, s, "peek")
	if strings.Contains(out, "PRIVATE-KEY-BYTES") {
		t.Fatalf("a contained process read the deny mask:\n%s", out)
	}
	// It has to have failed on the mask rather than come back empty for some
	// other reason — an empty capture would pass the check above whatever
	// went wrong.
	if st := execute(t, s, `{"action":"status","name":"peek"}`); strings.Contains(st, "exited (code 0)") {
		t.Fatalf("the contained read should have failed on the masked path:\n%s\n%s", st, out)
	}
}

// streams is both of a finished process's captured streams, for the tests
// that care what it printed rather than where.
func streams(t *testing.T, s *Supervisor, name string) string {
	t.Helper()
	waitFor(t, "process "+name+" to exit", func() bool {
		return strings.Contains(execute(t, s, `{"action":"status","name":"`+name+`"}`), "exited")
	})
	return execute(t, s, `{"action":"read","name":"`+name+`","stream":"stdout","offset":0}`) +
		execute(t, s, `{"action":"read","name":"`+name+`","stream":"stderr","offset":0}`)
}
