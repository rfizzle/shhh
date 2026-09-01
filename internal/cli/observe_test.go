package cli

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

func TestParseObserveWindow(t *testing.T) {
	for _, valid := range []string{"7d", "30d", "1d"} {
		if _, err := parseObserveWindow(valid); err != nil {
			t.Errorf("parseObserveWindow(%q) unexpected error: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "d", "0d", "-3d", "7h", "week"} {
		if _, err := parseObserveWindow(invalid); err == nil {
			t.Errorf("parseObserveWindow(%q) should fail", invalid)
		}
	}

	since, err := parseObserveWindow("7d")
	if err != nil {
		t.Fatalf("parse 7d: %v", err)
	}
	expected := time.Now().AddDate(0, 0, -7)
	if diff := since.Sub(expected); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("7d cutoff off by %v", diff)
	}
}

func TestObserveRecorder_RoundTrip(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	prices := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-test": {InputCostPerToken: 0.001, OutputCostPerToken: 0.002},
	})
	rec := startObserveRecorder(db, "code", "openai", "gpt-test", prices)
	if rec == nil {
		t.Fatal("expected a recorder")
	}

	rec.usage(2, 100, 50)
	rec.toolCallAt(observe.Pos{Turn: 1, Round: 1}, "read_file", 5*time.Millisecond, "ok", "")
	rec.decisionAt(observe.Pos{Turn: 1, Round: 1}, "deny", "plan-mode")
	rec.end()

	since := time.Now().Add(-time.Hour)
	sessions, err := db.AgentSessions(since, 10)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.Kind != "code" || s.Turns != 2 || s.TokensIn != 100 || s.TokensOut != 50 {
		t.Fatalf("unexpected session: %+v", s)
	}
	wantCost := 100*0.001 + 50*0.002
	if s.Cost != wantCost {
		t.Fatalf("expected cost %v, got %v", wantCost, s.Cost)
	}
	if s.EndedAt == nil {
		t.Fatal("expected session to be ended")
	}

	toolMix, err := db.AgentToolMix(since)
	if err != nil {
		t.Fatalf("tool mix: %v", err)
	}
	if len(toolMix) != 1 || toolMix[0].Tool != "read_file" || toolMix[0].Count != 1 {
		t.Fatalf("unexpected tool mix: %+v", toolMix)
	}

	decisions, err := db.AgentDecisions(since)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Decision != "deny" || decisions[0].Reason != "plan-mode" {
		t.Fatalf("unexpected decisions: %+v", decisions)
	}
}

func TestRenderObserveDashboard_Sections(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	id, err := db.StartAgentSession("code", "anthropic", "test-model")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.UpdateAgentSession(id, 4, 152000, 8300, 0.58); err != nil {
		t.Fatalf("update session: %v", err)
	}
	ms := int64(42)
	if err := db.RecordAgentEvent(id, storage.AgentEvent{Kind: storage.AgentEventTool, Tool: "read_file", DurationMs: &ms, Outcome: "ok", Turn: 1, Round: 1}); err != nil {
		t.Fatalf("record tool event: %v", err)
	}
	if err := db.RecordAgentEvent(id, storage.AgentEvent{Kind: storage.AgentEventDecision, Outcome: "deny", Reason: "classifier", Turn: 1, Round: 1}); err != nil {
		t.Fatalf("record decision event: %v", err)
	}
	if err := db.EndAgentSession(id); err != nil {
		t.Fatalf("end session: %v", err)
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := renderObserveDashboard(cmd, db, "30d", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"BY DAY", "BY MODEL", "TOOLS", "DECISIONS", "SESSIONS",
		"anthropic", "test-model", "read_file", "denied", "auto · classifier",
		"↑152k ↓8.3k", "$0.58", "code",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, out)
		}
	}
}

func TestObserveRecorder_NilSafe(t *testing.T) {
	var rec *observeRecorder
	rec.usage(1, 1, 1)
	rec.toolCallAt(observe.Pos{}, "read_file", time.Millisecond, "ok", "")
	rec.decisionAt(observe.Pos{}, "allow", "user")
	rec.stamp("prompt", 1, "/repo")
	rec.turn(1, 3, time.Second, "done")
	rec.signal(observe.Pos{}, "summary", "on-target")
	rec.link("name")
	rec.end()
	if obs := rec.observer(); obs.Usage != nil || obs.ToolCall != nil || obs.Decision != nil || obs.Turn != nil || obs.Signal != nil || obs.Session != nil {
		t.Fatal("nil recorder should produce a zero observer")
	}
	if r := startObserveRecorder(nil, "chat", "p", "m", nil); r != nil {
		t.Fatal("nil db should produce a nil recorder")
	}
}

func TestObserveSessionTimeline(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	rec.stamp("the prompt", 2, "/repo")
	rec.link("2026-01-01 10:00:00")
	rec.link("2026-01-01 10:00:00")
	rec.toolCallAt(observe.Pos{Turn: 1, Round: 1}, "search", 5*time.Millisecond, "ok", "empty")
	rec.decisionAt(observe.Pos{Turn: 1, Round: 2}, "ask", "safety")
	rec.signal(observe.Pos{Turn: 1, Round: 40}, "summary", "off-target")
	rec.turn(1, 41, 90*time.Second, "cap-paused")
	rec.end()

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := renderObserveSession(cmd, db, rec.sessionID()); err != nil {
		t.Fatalf("render session: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"shhh observe session 1", "prompt:", fingerprint("the prompt"), "skills:", "2",
		"project:", fingerprint("/repo"), "conversation:", "2026-01-01 10:00:00",
		"TURN 1", "search", "empty", "asked", "auto · safety",
		"summary", "off-target", "cap-paused", "41 rounds",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("timeline missing %q:\n%s", want, out)
		}
	}
	if err := renderObserveSession(cmd, db, 99); err == nil {
		t.Fatal("expected an error for a session that does not exist")
	}

	buf.Reset()
	if err := renderObserveDashboard(cmd, db, "30d", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	out = buf.String()
	for _, want := range []string{"TURNS", "cap-paused", "SIGNALS", "off-target", "observe session"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, out)
		}
	}
}

func TestFingerprint(t *testing.T) {
	if fingerprint("") != "" {
		t.Fatal("empty input must fingerprint to empty")
	}
	if a, b := fingerprint("x"), fingerprint("y"); a == b || len(a) != 12 {
		t.Fatalf("fingerprints %q %q", a, b)
	}
}

func fixtureStore(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// eventShape is one recorded event as a test states it.
type eventShape struct {
	kind    string
	tool    string
	outcome string
	reason  string
	turn    int64
	round   int64
	timed   bool
}

func shapesOf(t *testing.T, db *storage.DB, id int64) []eventShape {
	t.Helper()
	events, err := db.AgentSessionEvents(id)
	if err != nil {
		t.Fatalf("session events: %v", err)
	}
	out := make([]eventShape, 0, len(events))
	for _, e := range events {
		out = append(out, eventShape{
			kind: e.Kind, tool: e.Tool, outcome: e.Outcome, reason: e.Reason,
			turn: e.Turn, round: e.Round, timed: e.DurationMs != nil,
		})
	}
	return out
}

func assertShapes(t *testing.T, got, want []eventShape) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("recorded %d events, want %d:\ngot  %+v\nwant %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d:\ngot  %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

// recordEverySurface writes one session of each kind shhh ships, with the
// events that kind produces, and returns them by kind.
func recordEverySurface(t *testing.T, db *storage.DB) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}

	// A session: turns, positioned tool calls, a policy decision, signals.
	sess := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	sess.stamp("the agent prompt", 3, "/repo")
	sess.link("2026-09-02 09:00:00")
	emptyOutcome, emptyClass := observe.ToolOutcome("No matches found.")
	sess.toolCallAt(observe.Pos{Turn: 1, Round: 1}, "search", 4*time.Millisecond, emptyOutcome, emptyClass)
	sess.decisionAt(observe.Pos{Turn: 1, Round: 2}, observe.DecisionAsk,
		observe.AskReason(agent.Action{Kind: agent.ActionEdit, OutOfScope: []string{"/etc/passwd"}}))
	sess.signal(observe.Pos{Turn: 1, Round: 3}, observe.SignalSummary, observe.SummaryCode(agent.SummaryOnTarget))
	sess.turn(1, 3, time.Second, observe.TurnDone)
	sess.end()
	ids["code"] = sess.sessionID()

	// A headless run: the same events through its own adapter.
	head := startObserveRecorder(db, "print", "anthropic", "test-model", nil)
	head.stamp("the agent prompt", 3, "/repo")
	obs := headlessObserver{rec: head, rounds: func() int { return 2 }}
	obs.toolResult("execute_command", time.Millisecond,
		"error: command not approved: headless mode denies commands by default")
	obs.decision(observe.DecisionDeny, "headless-default")
	obs.intervene(agent.Intervention{Kind: agent.InterveneCheckIn})
	head.turn(1, 2, time.Second, observe.TurnFailed)
	head.end()
	ids["print"] = head.sessionID()

	// A sub-agent: its own provenance, linked to the session that spawned it.
	child := startChildObserveRecorder(db, "researcher", "anthropic", "cheap-model", nil, ids["code"])
	child.stamp("the researcher prompt", 3, "/repo")
	declined, declinedClass := observe.ToolOutcome("error: the user declined this tool call")
	child.decisionAt(observe.Pos{Turn: 1, Round: 1}, observe.DecisionAsk, observe.ReasonPolicy)
	child.decisionAt(observe.Pos{Turn: 1, Round: 1}, observe.DecisionDeny, observe.ReasonUser)
	child.toolCallAt(observe.Pos{Turn: 1, Round: 1}, "read_file", 2*time.Millisecond, declined, declinedClass)
	child.signal(observe.Pos{Turn: 1, Round: 1}, observe.SignalRepeat, "read_file")
	child.turn(1, 1, time.Second, observe.TurnCapPaused)
	child.end()
	ids["researcher"] = child.sessionID()

	// The one-shot: one request, so one turn.
	one := startObserveRecorder(db, "cmd", "openai", "gpt-test", nil)
	one.stamp("the one-shot prompt", 0, "/repo")
	one.usagePriced(1, 900, 120, 0.004, true)
	// A one-shot raises neither of these; they ride here so the closed-set
	// walk below covers the two signals whose reason is a count rather than
	// a word, which is the shape most likely to smuggle something through.
	sess.signal(observe.Pos{Turn: 1, Round: 4}, observe.SignalTrim, "12")
	sess.signal(observe.Pos{Turn: 1, Round: 5}, observe.SignalSteer, "2")
	one.turn(1, 0, 2*time.Second, observe.TurnCancelled)
	one.end()
	ids["cmd"] = one.sessionID()

	return ids
}

// The export shows the same event shapes for every session kind: a reader
// gets a tool event, a decision, a turn and a signal from each of them and
// never a shape that belongs to one surface alone.
func TestEverySessionKindExportsTheSameShapes(t *testing.T) {
	db := fixtureStore(t)
	recordEverySurface(t, db)

	sessions, err := db.ExportAgentObservability(time.Now().Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(sessions) != 4 {
		t.Fatalf("exported %d sessions, want one per surface", len(sessions))
	}
	for _, s := range sessions {
		if s.Version == "" || s.PromptHash == "" || s.Project == "" {
			t.Errorf("%s session is unstamped: %+v", s.Kind, s)
		}
		if s.EndedAt == nil {
			t.Errorf("%s session never ended", s.Kind)
		}
		var turns, decisions int
		for _, e := range s.Events {
			switch e.Kind {
			case storage.AgentEventTurn:
				turns++
			case storage.AgentEventDecision:
				decisions++
			case storage.AgentEventTool:
				if e.Turn == 0 {
					t.Errorf("%s session has an unplaced tool event: %+v", s.Kind, e)
				}
				if e.DurationMs == nil {
					t.Errorf("%s session has an untimed tool event: %+v", s.Kind, e)
				}
			}
		}
		if turns != 1 {
			t.Errorf("%s session closed with %d turn events, want 1", s.Kind, turns)
		}
		// The one-shot gates nothing, so it has nothing to decide. Every
		// surface that runs tools reports what the policy said about them.
		if s.Kind != "cmd" && decisions == 0 {
			t.Errorf("%s session recorded no permission decisions", s.Kind)
		}
	}
}

// The dashboard renders every kind through the same sections: no surface has
// a special case, so nothing has to be added when the next one is recorded.
func TestObserveDashboardNeedsNoPerKindCase(t *testing.T) {
	db := fixtureStore(t)
	ids := recordEverySurface(t, db)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := renderObserveDashboard(cmd, db, "30d", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"code", "print", "researcher", "cmd"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard does not name the %s surface:\n%s", want, out)
		}
	}

	// And each of them renders as a timeline through the same code.
	for kind, id := range ids {
		buf.Reset()
		if err := renderObserveSession(cmd, db, id); err != nil {
			t.Fatalf("render %s session: %v", kind, err)
		}
		if !strings.Contains(buf.String(), "TURN 1") {
			t.Errorf("%s session timeline has no turn:\n%s", kind, buf.String())
		}
	}
}

// storedWord is what a stored string is allowed to look like: a fixed
// identifier or a code from a closed set. A path, a command or a prompt
// fragment fails it, which is the whole guarantee the record rests on.
var storedWord = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

// Every string every surface stores is a fixed identifier or a closed-set
// code. This walks every session kind rather than living in each surface's
// own file, because the guarantee is about the table and not about any one
// writer of it.
// See docs/capabilities/sessions-and-memory.md#every-composition-is-one-population.
func TestNoSurfaceStoresAnythingOutsideTheClosedSets(t *testing.T) {
	db := fixtureStore(t)
	recordEverySurface(t, db)

	sessions, err := db.ExportAgentObservability(time.Now().Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, s := range sessions {
		for _, e := range s.Events {
			for name, value := range map[string]string{
				"kind": e.Kind, "tool": e.Tool, "outcome": e.Outcome, "reason": e.Reason,
			} {
				if value == "" {
					continue
				}
				// A trim signal's qualifier is a count, which the same shape
				// admits; nothing else numeric reaches the table.
				if !storedWord.MatchString(value) {
					t.Errorf("%s session stored a free string in %s: %q", s.Kind, name, value)
				}
			}
		}
	}
}
