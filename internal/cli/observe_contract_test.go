//go:build contract

package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/observe"
)

// A collector nobody is listening on changes nothing about the run. The
// record's local half is written exactly as it would be with no endpoint
// configured at all, and the session comes out the way its last turn did —
// which is the whole of what "the record is a by-product" has to mean.
func TestObserveRecorder_ADeadCollectorLeavesTheRunAlone(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := dead.URL
	dead.Close()

	logs.To(filepath.Join(t.TempDir(), "shhh.log"))
	t.Cleanup(func() { logs.To("") })
	setObserveExport(endpoint)
	t.Cleanup(func() { setObserveExport("") })
	if observeExport == nil {
		t.Fatal("a URL that parses should have built an exporter")
	}

	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "openai", "gpt-test", nil)
	if rec == nil {
		t.Fatal("expected a recorder")
	}
	rec.toolCallAt(observe.Pos{Turn: 1, Round: 1}, "read_file", 5*time.Millisecond, observe.OutcomeOK, "")
	rec.turn(1, 1, time.Second, observe.TurnDone)
	rec.end()

	since := time.Now().Add(-time.Hour)
	sessions, err := db.AgentSessions(since, 10)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Outcome != observe.SessionCompleted {
		t.Fatalf("the run did not come out the way its turn did: %+v", sessions)
	}
	mix, err := db.AgentToolMix(since)
	if err != nil {
		t.Fatalf("tool mix: %v", err)
	}
	if len(mix) != 1 || mix[0].Tool != "read_file" {
		t.Fatalf("the call did not reach the store: %+v", mix)
	}
}

// A session boundary sends the closing conversation's span from a goroutine,
// because `/new` is answered inside the update loop and a slow collector
// would otherwise hold the screen. The span still goes: this asserts the
// hand-off delivers rather than dropping the row on the floor.
func TestObserveRecorder_ARestartStillSendsTheSpanItClosed(t *testing.T) {
	arrived := make(chan struct{}, 4)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		arrived <- struct{}{}
	}))
	t.Cleanup(collector.Close)

	setObserveExport(collector.URL)
	t.Cleanup(func() { setObserveExport("") })

	db := fixtureStore(t)
	rec := startObserveRecorder(db, "chat", "openai", "gpt-test", nil)
	if rec == nil {
		t.Fatal("expected a recorder")
	}
	rec.turn(1, 1, time.Second, observe.TurnDone)
	if !rec.restart() {
		t.Fatal("the boundary did not open a second row")
	}
	rec.end()

	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of the two spans reached the collector", i)
		}
	}
}
