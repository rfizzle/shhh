package process

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
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
