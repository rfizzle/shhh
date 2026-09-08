package process

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// livePID is shaped like the pid of a running command. Every offer that uses
// it here is refused for some other reason before the pid is ever signalled.
const livePID = 4242

// adoptReal spawns a command in a group of its own and offers it to the
// supervisor under the given command line, returning the name it got.
//
// The tests below want a process the supervisor is really holding, and a
// made-up pid cannot stand in for one: the supervisor stops what it holds by
// signalling that pid's group, so a fabricated number sends a real signal to
// a group the test does not own — or, for the small values, to every process
// the user has. Adopting something real keeps the cleanup pointed at this
// test's own child.
func adoptReal(t *testing.T, s *Supervisor, command string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	cmd.SysProcAttr = sysProcAttr(false)
	if err := cmd.Start(); err != nil {
		t.Skipf("no shell: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	name, _, err := s.Adopt(Adoption{
		Command: command,
		PID:     cmd.Process.Pid,
		Wait:    func() error { return <-done },
	})
	if err != nil {
		t.Fatalf("Adopt(%q): %v", command, err)
	}
	return name
}

// A running command handed over keeps running, under a name the model can
// use, with everything it prints from then on captured where a read will find
// it — and it stops the way any other process does.
func TestAdopt_TakesARunningCommandUnderAName(t *testing.T) {
	s := newTestSupervisor(t, nil)

	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	cmd.SysProcAttr = sysProcAttr(false)
	if err := cmd.Start(); err != nil {
		t.Skipf("no shell: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	name, sink, err := s.Adopt(Adoption{
		Command: "npm run dev",
		PID:     cmd.Process.Pid,
		Started: time.Now().Add(-time.Minute),
		Wait:    func() error { return <-done },
	})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if name != "npm" {
		t.Errorf("a process should be named after the program it runs, got %q", name)
	}
	if _, err := sink.Write([]byte("listening on 3000\n")); err != nil {
		t.Fatalf("the caller must be able to go on writing output: %v", err)
	}

	out := execute(t, s, `{"action":"read","name":"npm"}`)
	if !strings.Contains(out, "listening on 3000") {
		t.Errorf("output written after the handover should be readable, got %q", out)
	}
	if !strings.Contains(s.List(), "npm run dev") {
		t.Errorf("the process list should show what was asked to run, got %q", s.List())
	}

	out = execute(t, s, `{"action":"stop","name":"npm"}`)
	if !strings.Contains(out, "exited") {
		t.Errorf("stop should end an adopted process like any other, got %q", out)
	}
}

// An adopted command was in the foreground and had no input; saying so by
// name beats a pipe with nothing on the other end of it.
func TestAdopt_HasNoInputToWriteTo(t *testing.T) {
	s := newTestSupervisor(t, nil)
	adoptReal(t, s, "tail -f log")
	err := executeErr(t, s, `{"action":"input","name":"tail","text":"x\n"}`)
	if !strings.Contains(err.Error(), "no input") {
		t.Errorf("the refusal should say why there is no stdin, got %v", err)
	}
}

// A refusal here is answered by the caller stopping the command, so it has to
// be a refusal and never a half-adopted process.
func TestAdopt_RefusesWhatItCannotHold(t *testing.T) {
	s := newTestSupervisor(t, nil)
	if _, _, err := s.Adopt(Adoption{Command: "sleep 30", PID: livePID}); err == nil {
		t.Error("an offer with no wait behind it must be refused")
	}
	if _, _, err := s.Adopt(Adoption{Command: "sleep 30", Wait: func() error { return nil }}); err == nil {
		t.Error("an offer with no process must be refused")
	}
	// A pid this low leads no tree the supervisor could have been given, and
	// letting one in is how a later stop becomes a signal to this process's
	// own group or to every process the user owns.
	for _, pid := range []int{-1, 0, 1} {
		if _, _, err := s.Adopt(Adoption{Command: "sleep 30", PID: pid, Wait: func() error { return nil }}); err == nil {
			t.Errorf("a pid of %d names nothing that can be adopted and must be refused", pid)
		}
	}
	s.Close()
	if _, _, err := s.Adopt(Adoption{Command: "sleep 30", PID: livePID, Wait: func() error { return nil }}); err == nil {
		t.Error("a shut-down supervisor must refuse")
	}
}

func TestProcessName(t *testing.T) {
	for command, want := range map[string]string{
		"npm run dev":            "npm",
		"./scripts/dev.sh --hot": "dev.sh",
		"  go test ./...":        "go",
		"":                       "command",
		"/usr/bin/env python -m": "env",
		"«»":                     "command",
	} {
		if got := processName(command); got != want {
			t.Errorf("processName(%q) = %q, want %q", command, got, want)
		}
	}
}

// Two dev servers are two processes, so the second one gets a name of its own
// rather than colliding with the first.
func TestAdopt_NamesASecondCommandOfTheSameProgram(t *testing.T) {
	s := newTestSupervisor(t, nil)
	first := adoptReal(t, s, "npm run dev")
	second := adoptReal(t, s, "npm run build")
	if first != "npm" || second != "npm-2" {
		t.Fatalf("names collided: %q and %q", first, second)
	}
}
