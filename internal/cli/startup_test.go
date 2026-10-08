package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
)

// startupStore is a fresh store and a session row on it.
func startupStore(t *testing.T) (*storage.DB, *observeRecorder) {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	rec := startObserveRecorder(db, "code", "test", "test-model", nil)
	if rec == nil {
		t.Fatal("expected a recorder")
	}
	return db, rec
}

// A server that cannot start: its command is a path that is not there, so
// the connect fails at once and on any machine.
func missingServer(name, command string) mcp.Definition {
	return mcp.Definition{Name: name, Scope: mcp.ScopeUser, Transport: mcp.TransportStdio, Command: command}
}

func timings(t *testing.T, db *storage.DB, rec *observeRecorder) []storage.AgentTiming {
	t.Helper()
	rows, err := db.AgentTimings(rec.sessionID())
	if err != nil {
		t.Fatalf("timings: %v", err)
	}
	return rows
}

// Each phase a session pays for before its prompt is one row with its
// duration, and each server one row of its own under its name and a closed
// word for how its connect came out. The rows paid before the session's row
// existed are held and written when it opens; the first paint, which comes
// after, goes straight through.
func TestStartup_OneRowPerPhaseAndPerServer(t *testing.T) {
	db, rec := startupStore(t)
	s := &observe.Startup{}
	notePhase(s, observe.PhaseConfig, 3*time.Millisecond)
	notePhase(s, observe.PhaseStore, 5*time.Millisecond)
	ts := mcp.Connect(context.Background(), &mcp.Catalog{Servers: []mcp.Definition{
		missingServer("broken", "/nonexistent/mcp-server"),
		{Name: "off", Scope: mcp.ScopeUser, Transport: mcp.TransportStdio, Command: "true", Disabled: true},
	}}, mcp.Options{Timeout: 5 * time.Second, Observe: serverStartup(s)})
	defer ts.Close()
	notePhase(s, observe.PhaseLSP, 7*time.Millisecond)

	if got := timings(t, db, rec); len(got) != 0 {
		t.Fatalf("rows written before the record was attached: %+v", got)
	}
	s.Attach(rec.startupRow)
	now := func(write func()) { write() }
	firstPaintOn(s, now)()
	firstPaintOn(s, now)() // a second paint of a fresh hook is a second session's
	paint := firstPaintOn(s, now)
	paint()
	paint() // the same hook answers once

	type row struct{ phase, name, outcome string }
	count := map[row]int{}
	for _, r := range timings(t, db, rec) {
		if r.Kind != storage.AgentEventStartup {
			t.Fatalf("a %s row among the startup rows", r.Kind)
		}
		if r.DurationMs == nil {
			t.Fatalf("%s row carries no duration", r.Reason)
		}
		count[row{r.Reason, r.Tool, r.Outcome}]++
	}
	want := map[row]int{
		{observe.PhaseConfig, "", ""}:                      1,
		{observe.PhaseStore, "", ""}:                       1,
		{observe.PhaseMCP, "broken", observe.ServerFailed}: 1,
		{observe.PhaseMCP, "off", observe.ServerDisabled}:  1,
		{observe.PhaseLSP, "", ""}:                         1,
		{observe.PhaseFirstPaint, "", ""}:                  3,
	}
	for k, n := range want {
		if count[k] != n {
			t.Errorf("%+v: %d rows, want %d (all: %v)", k, count[k], n, count)
		}
	}
	if len(count) != len(want) {
		t.Errorf("rows %v, want %v", count, want)
	}
}

// The time a connect measured is the figure the record holds, to the
// millisecond, rather than one taken again somewhere else.
func TestConnect_TheReportsDurationIsRecorded(t *testing.T) {
	db, rec := startupStore(t)
	s := &observe.Startup{}
	s.Attach(rec.startupRow)
	ts := mcp.Connect(context.Background(), &mcp.Catalog{Servers: []mcp.Definition{
		missingServer("broken", "/nonexistent/mcp-server"),
	}}, mcp.Options{Timeout: 5 * time.Second, Observe: serverStartup(s)})
	defer ts.Close()

	rows := timings(t, db, rec)
	if len(rows) != 1 || rows[0].DurationMs == nil {
		t.Fatalf("rows = %+v", rows)
	}
	if want := ts.Reports[0].Took.Milliseconds(); *rows[0].DurationMs != want {
		t.Fatalf("recorded %dms, the report measured %dms", *rows[0].DurationMs, want)
	}
}

// A server's error text is the transport's words, and a path or a URL in it
// can carry a credential. The record keeps the server's name and a word, and
// none of what it said.
func TestStartup_NoServerOutputReachesTheRecord(t *testing.T) {
	const secret = "sk-live-0123456789abcdefSECRET"
	db, rec := startupStore(t)
	s := &observe.Startup{}
	s.Attach(rec.startupRow)
	ts := mcp.Connect(context.Background(), &mcp.Catalog{Servers: []mcp.Definition{
		missingServer("leaky", "/nonexistent/"+secret+"/mcp-server"),
	}}, mcp.Options{Timeout: 5 * time.Second, Observe: serverStartup(s)})
	defer ts.Close()
	if r := ts.Reports[0]; r.Status != mcp.StatusFailed || !strings.Contains(r.Error, secret) {
		t.Fatalf("the seeded server's error does not carry the secret, so this proves nothing: %s %q", r.Status, r.Error)
	}

	rows := timings(t, db, rec)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, r := range rows {
		for _, field := range []string{r.Kind, r.Tool, r.Outcome, r.Reason} {
			if strings.Contains(field, secret) || strings.Contains(field, "nonexistent") {
				t.Fatalf("server output reached the record: %+v", r)
			}
		}
	}
	events, err := db.AgentSessionEvents(rec.sessionID())
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, e := range events {
		if strings.Contains(e.Tool+e.Outcome+e.Reason, secret) {
			t.Fatalf("server output reached the record: %+v", e)
		}
	}
}

// The turn row carries its time split four ways, in whole milliseconds that
// add up to its own duration even where every part has a fraction of one.
func TestTurnTime_TheRowsColumnsAddUpToItsDuration(t *testing.T) {
	db, rec := startupStore(t)
	split := agent.TurnSplit{
		ModelFirst: 1200*time.Millisecond + 700*time.Microsecond, ModelStream: 300*time.Millisecond + 600*time.Microsecond,
		Tool: 4*time.Second + 900*time.Microsecond, Person: 2*time.Second + 800*time.Microsecond,
	}
	rec.turnTimed(1, 3, split.Total(), observe.TurnDone, split)

	rows := timings(t, db, rec)
	if len(rows) != 1 || rows[0].Kind != storage.AgentEventTurn {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	sum := *r.ModelFirstMs + *r.ModelStreamMs + *r.ToolMs + *r.PersonMs
	if sum != *r.DurationMs || *r.DurationMs != split.Total().Milliseconds() {
		t.Fatalf("parts %d+%d+%d+%d = %d, duration %d", *r.ModelFirstMs, *r.ModelStreamMs, *r.ToolMs, *r.PersonMs, sum, *r.DurationMs)
	}
}

// The longest stretch with nothing on the screen is one row per turn, and a
// stream that delivered something unseen in it is a different row from one
// that delivered nothing — the count says which, and the events themselves
// are never kept.
func TestQuietStretch_TellsAQuietStreamFromASilentOne(t *testing.T) {
	db, rec := startupStore(t)
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

	// A model thinking where nobody can see it: two events in forty seconds,
	// neither drawn.
	var quiet agent.TurnClock
	quiet.Begin(t0)
	quiet.Event(at(1), true)
	quiet.Event(at(10), false)
	quiet.Event(at(30), false)
	quiet.Event(at(41), true)
	q := quiet.Split(at(42))
	rec.turnTimed(1, 1, q.Total(), observe.TurnDone, q)

	// A connection that went silent: nothing at all for forty seconds.
	var silent agent.TurnClock
	silent.Begin(at(100))
	silent.Event(at(101), true)
	silent.Event(at(141), true)
	s := silent.Split(at(142))
	rec.turnTimed(2, 1, s.Total(), observe.TurnDone, s)

	var stretches []storage.AgentTiming
	for _, r := range timings(t, db, rec) {
		if r.Kind == storage.AgentEventQuiet {
			stretches = append(stretches, r)
		}
	}
	if len(stretches) != 2 {
		t.Fatalf("stretches = %+v, want one per turn", stretches)
	}
	a, b := stretches[0], stretches[1]
	if a.Turn != 1 || a.Outcome != observe.StretchQuiet || *a.Delivered != 2 || *a.DurationMs != 40000 || a.Reason != observe.WaitModelStream {
		t.Errorf("quiet stream's row = %+v (delivered %d, %dms)", a, *a.Delivered, *a.DurationMs)
	}
	if b.Turn != 2 || b.Outcome != observe.StretchSilent || *b.Delivered != 0 || *b.DurationMs != 40000 {
		t.Errorf("silent stream's row = %+v (delivered %d, %dms)", b, *b.Delivered, *b.DurationMs)
	}

	// A long command is neither: no stream was open to say anything, so
	// the stretch is filed as waiting and never among the dead connections.
	var build agent.TurnClock
	build.Begin(at(200))
	build.Event(at(201), true)
	build.Tool(at(202))
	build.Drew(at(202))
	build.Drew(at(500))
	w := build.Split(at(501))
	rec.turnTimed(3, 1, w.Total(), observe.TurnDone, w)

	// A turn paused at its cap may never be taken up again, so the pause
	// writes its stretch; granted more rounds and closing with the same
	// longest stretch, it writes no second one, and only a longer one
	// after the grant adds a row.
	rec.turnTimed(4, 5, s.Total(), observe.TurnCapPaused, s)
	rec.turnTimed(4, 7, s.Total()+time.Second, observe.TurnDone, s)
	longer := s
	longer.Quiet.Took = time.Minute
	rec.turnTimed(5, 5, s.Total(), observe.TurnCapPaused, s)
	rec.turnTimed(5, 7, 2*time.Minute, observe.TurnDone, longer)

	perTurn := map[int64][]storage.AgentTiming{}
	for _, r := range timings(t, db, rec) {
		if r.Kind == storage.AgentEventQuiet {
			perTurn[r.Turn] = append(perTurn[r.Turn], r)
		}
	}
	if got := perTurn[3]; len(got) != 1 || got[0].Outcome != observe.StretchWaiting || got[0].Reason != observe.WaitTool {
		t.Errorf("a long command's stretch = %+v", got)
	}
	if got := perTurn[4]; len(got) != 1 {
		t.Errorf("a paused turn that closed with the same stretch wrote %d rows", len(got))
	}
	if got := perTurn[5]; len(got) != 2 || *got[1].DurationMs != 60000 {
		t.Errorf("a paused turn whose stretch grew wrote %+v", got)
	}
}

// The first-paint hook runs inside the frame being drawn, so it writes
// nothing itself: the row is handed to the queue, and it is in the record
// once the queue has run it, once however many frames call the hook.
func TestFirstPaint_IsNotWrittenFromView(t *testing.T) {
	db, rec := startupStore(t)
	s := &observe.Startup{}
	s.Attach(rec.startupRow)
	var queued []func()
	paint := firstPaintOn(s, func(write func()) { queued = append(queued, write) })
	paint()
	paint()
	paint()
	if got := timings(t, db, rec); len(got) != 0 {
		t.Fatalf("%d rows written from the frame, want none", len(got))
	}
	if len(queued) != 1 {
		t.Fatalf("%d writes queued, want one", len(queued))
	}
	queued[0]()
	got := timings(t, db, rec)
	if len(got) != 1 || got[0].Reason != observe.PhaseFirstPaint {
		t.Fatalf("rows after the queue ran = %+v, want the first-paint row", got)
	}
}
