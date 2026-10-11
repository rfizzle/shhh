package process

import (
	"strings"
	"testing"
	"time"
)

// The manager reads every process as Info, oldest first, running or not, and
// a process's end — its code and when — once it has one.
func TestSnapshot_ListsEveryProcessOldestFirst(t *testing.T) {
	s := newTestSupervisor(t, nil)
	if got := s.Snapshot(); len(got) != 0 {
		t.Fatalf("a supervisor with nothing started lists %v", got)
	}
	execute(t, s, `{"action":"start","name":"first","command":"exit 3"}`)
	execute(t, s, `{"action":"start","name":"second","command":"sleep 30"}`)
	waitFor(t, "the first process to exit", func() bool {
		got := s.Snapshot()
		return len(got) == 2 && !got[0].Running
	})
	got := s.Snapshot()
	if got[0].Name != "first" || got[1].Name != "second" {
		t.Fatalf("snapshot order = %s, %s; want first, second", got[0].Name, got[1].Name)
	}
	if got[0].ExitCode != 3 || got[0].Ended.IsZero() {
		t.Errorf("the exited process reads code %d ended %v, want 3 and a time", got[0].ExitCode, got[0].Ended)
	}
	if !got[1].Running || got[1].Command != "sleep 30" || got[1].Container || got[0].Stopped {
		t.Errorf("the running process reads %+v", got[1])
	}
}

// Opening a process shows what the process tool's read returns, for each
// stream.
func TestTail_IsTheReadOfEachStream(t *testing.T) {
	s := newTestSupervisor(t, nil)
	execute(t, s, `{"action":"start","name":"talker","command":"echo out-line; echo err-line >&2; sleep 30"}`)
	waitFor(t, "both streams to arrive", func() bool {
		out, errs, err := s.Tail("talker")
		return err == nil && strings.Contains(out, "out-line") && strings.Contains(errs, "err-line")
	})
	out, errs, _ := s.Tail("talker")
	read := execute(t, s, `{"action":"read","name":"talker"}`)
	if out != read {
		t.Errorf("the tail of stdout is %q, the tool's read %q", out, read)
	}
	if !strings.HasPrefix(errs, "process talker (stderr)") {
		t.Errorf("the tail of stderr reads %q", errs)
	}
	if _, _, err := s.Tail("nobody"); err == nil {
		t.Error("a name nothing was started under has a tail")
	}
}

// A kill takes no grace: a process that ignores the gentle signal is gone in
// well under the time a stop gives it.
func TestKill_TakesNoGrace(t *testing.T) {
	s := newTestSupervisor(t, nil)
	execute(t, s, `{"action":"start","name":"stubborn","command":"trap '' TERM HUP; sleep 30"}`)
	start := time.Now()
	out, err := s.Kill("stubborn")
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if took := time.Since(start); took >= stopGrace {
		t.Errorf("kill took %s, as long as a stop's grace", took)
	}
	if !strings.Contains(out, "exited") {
		t.Errorf("kill reports %q, want the exit", out)
	}
	if out, err := s.Kill("stubborn"); err != nil || !strings.Contains(out, "exited") {
		t.Errorf("a second kill of a gone process reads %q, %v", out, err)
	}
}

// The manager's stop is the tool's: it ends a willing process and reports the
// exit.
func TestStop_FromTheManagerIsTheToolsStop(t *testing.T) {
	s := newTestSupervisor(t, nil)
	execute(t, s, `{"action":"start","name":"willing","command":"sleep 30"}`)
	out, err := s.Stop("willing")
	if err != nil || !strings.Contains(out, "exited") {
		t.Fatalf("stop reads %q, %v", out, err)
	}
	if got := s.Snapshot(); got[0].Running || !got[0].Stopped {
		t.Errorf("a stopped process reads %+v, want stopped", got[0])
	}
}
