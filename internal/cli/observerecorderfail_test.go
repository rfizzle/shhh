package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
)

// A store that stops answering after the first write is told once and
// counted after: the recorder neither retries nor raises the failure again.
func TestRecorder_FailureIsNoticedOnce(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "cmd", "anthropic", "test-model", nil)
	o := rec.observer()

	o.Signal(observe.Pos{Turn: 1}, observe.SignalGate, "ok")
	if err := o.WriteFailed(); err != nil {
		t.Fatalf("a write that landed was reported as a failure: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	o.Signal(observe.Pos{Turn: 1}, observe.SignalGate, "second")
	o.Turn(1, 1, time.Second, observe.TurnDone)
	if rec.failedWrites() < 2 {
		t.Fatalf("failedWrites = %d, want every refused write counted", rec.failedWrites())
	}
	if o.WriteFailed() == nil {
		t.Fatal("the first failed write was not reported")
	}
	if again := o.WriteFailed(); again != nil {
		t.Fatalf("the failure was reported twice: %v", again)
	}
	o.Signal(observe.Pos{Turn: 2}, observe.SignalGate, "third")
	if again := o.WriteFailed(); again != nil {
		t.Fatalf("a later failure was reported after the notice: %v", again)
	}
}

func TestTodoRecordLine_NamesTheFirstReasonAndTheCount(t *testing.T) {
	line := todoRecordLine(3, errors.New("store gone"))
	for _, want := range []string{"the run's record stopped writing", "store gone", "3 writes failed"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
}
