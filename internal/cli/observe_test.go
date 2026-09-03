package cli

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
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
	if err := db.EndAgentSession(id, ""); err != nil {
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
	rec.stamp("prompt", 1, "/repo", storage.AgentSettings{})
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
	// Stamped in the mode it started in and never left: no mode signal is
	// recorded below, and the page must still say what mode it ran in.
	rec.stamp("the prompt", 2, "/repo", sessionSettings(config.Config{}, runSettings{
		mode: agent.ModeAuto.String(), effort: provider.EffortHigh, rounds: 60,
		sandbox: "workspace", model: "test-model", summary: true, classifier: true,
	}))
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
		"mode:", "auto", "reasoning:", "high", "rounds:", "60",
		"summary:", "test-model · every 10 rounds", "classifier:", "sandbox:", "workspace", "config:",
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
	sess.stamp("the agent prompt", 3, "/repo", storage.AgentSettings{})
	sess.link("2026-09-02 09:00:00")
	emptyOutcome, emptyClass := observe.ToolOutcome("No matches found.")
	sess.toolCallAt(observe.Pos{Turn: 1, Round: 1}, "search", 4*time.Millisecond, emptyOutcome, emptyClass)
	sess.decisionAt(observe.Pos{Turn: 1, Round: 2}, observe.DecisionAsk,
		observe.AskReason(agent.Action{Kind: agent.ActionEdit, OutOfScope: []string{"/etc/passwd"}}))
	sess.signal(observe.Pos{Turn: 1, Round: 3}, observe.SignalSummary, observe.SummaryCode(agent.SummaryOnTarget))
	// Both gate runs go through the real boundary rather than straight at
	// the recorder: the second is a suite name that resolved against
	// nothing, which is the one string on any recording path the model
	// chooses, and the closed-set walk below is what has to see it.
	gate := observe.GateHook(sess.observer())
	gate("default", quality.VerdictPass)
	gate("", quality.VerdictBlocked)
	sess.turn(1, 3, time.Second, observe.TurnDone)
	sess.end()
	ids["code"] = sess.sessionID()

	// A headless run: the same events through its own adapter.
	head := startObserveRecorder(db, "print", "anthropic", "test-model", nil)
	head.stamp("the agent prompt", 3, "/repo", storage.AgentSettings{})
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
	child.stamp("the researcher prompt", 3, "/repo", storage.AgentSettings{})
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
	one.stamp("the one-shot prompt", 0, "/repo", storage.AgentSettings{})
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

// The session's outcome is the last turn's, and the exit leaves it alone.
func TestObserveRecorder_OutcomeFollowsTheLastTurn(t *testing.T) {
	for turn, want := range map[string]string{
		observe.TurnDone:      "completed",
		observe.TurnCancelled: "interrupted",
		observe.TurnFailed:    "error",
		// A cap is a pause and not a close, so nothing stands when the
		// session exits there and the exit calls it what it was.
		observe.TurnCapPaused: "abandoned",
	} {
		t.Run(turn, func(t *testing.T) {
			db := fixtureStore(t)
			rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
			rec.turn(1, 2, time.Second, turn)
			rec.end()
			if got := sessionOutcomeOf(t, db, rec.sessionID()); got != want {
				t.Fatalf("a %s turn left the session %q, want %q", turn, got, want)
			}
		})
	}
}

// A pause at the round cap writes no outcome, so a sub-agent that pauses to
// take stock and is granted more rounds by its own supervisor is never
// marked as having given up — not even for the moment between the two turns,
// which is the moment a kill would freeze.
func TestObserveRecorder_APauseIsNotAnAbandonment(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "researcher", "anthropic", "test-model", nil)
	rec.turn(1, 8, time.Second, observe.TurnDone)
	rec.turn(2, 40, time.Second, observe.TurnCapPaused)
	if got := sessionOutcomeOf(t, db, rec.sessionID()); got != "completed" {
		t.Fatalf("the pause overwrote the standing reading with %q", got)
	}
	rec.turn(2, 55, 2*time.Second, observe.TurnDone)
	rec.end()
	if got := sessionOutcomeOf(t, db, rec.sessionID()); got != "completed" {
		t.Fatalf("the resumed turn left the session %q, want completed", got)
	}
}

// But a session that only ever paused, and then quit, is abandoned: the exit
// is where a pause turns out to have been the end.
func TestObserveRecorder_QuittingAtThePauseIsAnAbandonment(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	rec.turn(1, 40, time.Second, observe.TurnCapPaused)
	if got := sessionOutcomeOf(t, db, rec.sessionID()); got != "" {
		t.Fatalf("a pause wrote %q, and a pause is not a close", got)
	}
	rec.end()
	if got := sessionOutcomeOf(t, db, rec.sessionID()); got != "abandoned" {
		t.Fatalf("the exit left the session %q, want abandoned", got)
	}
}

// A surface that knows better than its turns do says so: a session whose
// program failed did not come out the way its last turn did, and the exit
// that reports the failure runs no deferred close.
func TestObserveRecorder_EndWithOverridesTheStandingReading(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	rec.turn(1, 3, time.Second, observe.TurnDone)
	rec.endWith(observe.SessionError)
	if got := sessionOutcomeOf(t, db, rec.sessionID()); got != "error" {
		t.Fatalf("a failed program left the session %q, want error", got)
	}
}

// The session the record most needs an outcome for is the one whose exit
// never runs. A killed process leaves the last turn's reading behind rather
// than a blank, which is the whole reason the write is optimistic.
func TestObserveRecorder_KilledSessionKeepsTheLastTurnsOutcome(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	rec.turn(1, 3, time.Second, observe.TurnDone)
	// No end(): the process died here.
	s, ok, err := db.AgentSession(rec.sessionID())
	if err != nil || !ok {
		t.Fatalf("session: ok=%v err=%v", ok, err)
	}
	if s.Outcome != "completed" {
		t.Fatalf("outcome = %q, want completed", s.Outcome)
	}
	if s.EndedAt != nil {
		t.Fatal("a killed session must not look ended")
	}
}

// The two ways a session can finish nothing are different facts. One reached
// its own exit and is abandoned; the other never got to say anything and
// reads as unknown.
func TestObserveRecorder_UnknownIsTheSessionThatNeverSpoke(t *testing.T) {
	db := fixtureStore(t)

	quit := startObserveRecorder(db, "chat", "anthropic", "test-model", nil)
	quit.end()
	if got := sessionOutcomeOf(t, db, quit.sessionID()); got != "abandoned" {
		t.Fatalf("a session that exited having closed no turn = %q, want abandoned", got)
	}

	killed := startObserveRecorder(db, "chat", "anthropic", "test-model", nil)
	if got := sessionOutcomeOf(t, db, killed.sessionID()); got != "" {
		t.Fatalf("a session that never spoke = %q, want no outcome at all", got)
	}
	mix, err := db.AgentSessionOutcomes(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("outcomes: %v", err)
	}
	counts := map[string]int{}
	for _, o := range mix {
		counts[o.Outcome] = o.Count
	}
	if counts["unknown"] != 1 || counts["abandoned"] != 1 {
		t.Fatalf("the mix folded the two together: %+v", mix)
	}
}

// A session boundary closes this session's row and opens the next one. One
// record never spans two conversations: the row left behind is ended and
// keeps what its last turn said about it, the row that follows starts clean,
// and each is linked to the slot its own conversation was written to.
func TestObserveRecorder_RestartEndsOneRowAndOpensAnother(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	first := rec.sessionID()
	rec.turn(1, 2, time.Second, observe.TurnDone)
	rec.link("first slot")

	if !rec.restart() {
		t.Fatal("the boundary should have opened a row")
	}
	second := rec.sessionID()
	if second == first {
		t.Fatal("the second conversation needs a row of its own")
	}
	rec.link("second slot")
	rec.end()

	left, ok, err := db.AgentSession(first)
	if err != nil || !ok {
		t.Fatalf("session %d: ok=%v err=%v", first, ok, err)
	}
	if left.EndedAt == nil {
		t.Fatal("the row left behind should be ended")
	}
	if left.Outcome != "completed" || left.ChatSession != "first slot" {
		t.Fatalf("the row should keep its own turn and slot, got %+v", left)
	}
	next, ok, err := db.AgentSession(second)
	if err != nil || !ok {
		t.Fatalf("session %d: ok=%v err=%v", second, ok, err)
	}
	if next.ChatSession != "second slot" {
		t.Fatalf("the new row belongs to the new slot, got %q", next.ChatSession)
	}
	if next.Kind != left.Kind || next.Provider != left.Provider || next.Model != left.Model {
		t.Fatalf("the new row is the same session assembled the same way: %+v vs %+v", next, left)
	}
	// The last turn of the first conversation is not the outcome of the
	// second: nothing was finished in it, and the exit says so.
	if next.Outcome != "abandoned" {
		t.Fatalf("the new row wrote its own outcome, got %q", next.Outcome)
	}
}

func sessionOutcomeOf(t *testing.T, db *storage.DB, id int64) string {
	t.Helper()
	s, ok, err := db.AgentSession(id)
	if err != nil || !ok {
		t.Fatalf("session %d: ok=%v err=%v", id, ok, err)
	}
	return s.Outcome
}

// A gate verdict reaches the record with the suite it is a verdict of, and
// it carries no turn or round: /gate run starts a run between turns, so a
// position would be invented for some runs and real for others.
func TestObserveRecorder_GateVerdictCarriesTheSuite(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	hook := observe.GateHook(rec.observer())
	if hook == nil {
		t.Fatal("a recording session must take gate verdicts")
	}
	hook("default", quality.VerdictBlocked)
	rec.end()

	assertShapes(t, shapesOf(t, db, rec.sessionID()), []eventShape{
		{kind: storage.AgentEventSignal, tool: "default", outcome: "gate", reason: "blocked"},
	})
}

// The gate and the outcome mix are sections of the dashboard, and the gate
// is drawn once: a verdict counted under SIGNALS as well would read as two
// facts. A window with no gate runs draws no gate section at all.
func TestObserveDashboard_GateAndOutcomeSections(t *testing.T) {
	db := fixtureStore(t)
	recordEverySurface(t, db)

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := renderObserveDashboard(cmd, db, "30d", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"GATE", "default", "100% passed", "OUTCOMES", "completed", "abandoned"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard missing %q:\n%s", want, out)
		}
	}
	if signals := sectionOf(out, "SIGNALS"); strings.Contains(signals, "gate") {
		t.Errorf("the gate signal is drawn under SIGNALS as well:\n%s", signals)
	}

	// A window with sessions but no gate runs: the section is omitted
	// rather than drawn empty.
	bare := fixtureStore(t)
	rec := startObserveRecorder(bare, "code", "anthropic", "test-model", nil)
	rec.turn(1, 1, time.Second, observe.TurnDone)
	rec.end()
	buf.Reset()
	if err := renderObserveDashboard(cmd, bare, "30d", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	if strings.Contains(buf.String(), "GATE") {
		t.Errorf("a window with no gate runs still drew the section:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "OUTCOMES") {
		t.Errorf("the outcome mix is missing:\n%s", buf.String())
	}
}

// A blocked run is drawn as neither a pass nor a failure, on the timeline
// and in the tally: the run learned nothing about the code, and reading it
// as a failing check is the confusion the gate keeps the two apart to avoid.
func TestObserveGate_BlockedIsNotAFailure(t *testing.T) {
	if got := observeGateState("blocked"); got == report.Fail || got == report.Pass {
		t.Fatalf("a blocked verdict draws as %v", got)
	}
	if got := observeGateState("cancelled"); got == report.Fail || got == report.Pass {
		t.Fatalf("a cancelled verdict draws as %v", got)
	}
	if observeGateState("fail") != report.Fail || observeGateState("pass") != report.Pass {
		t.Fatal("a pass and a fail must keep their own weight")
	}

	// A blocked run is named and left out of the rate on both sides: three
	// passes and one blocked run is a suite that passed everything it
	// actually judged.
	rows := observeGateRows([]storage.AgentGateVerdict{
		{Suite: "default", Verdict: "pass", Count: 3},
		{Suite: "default", Verdict: "blocked", Count: 1},
		{Suite: "lint", Verdict: "pass", Count: 1},
		{Suite: "lint", Verdict: "fail", Count: 1},
		{Suite: "vet", Verdict: "blocked", Count: 2},
	})
	if len(rows) != 3 {
		t.Fatalf("expected one row per suite, got %+v", rows)
	}
	if rows[0].Outcome != "100% passed" || !strings.Contains(rows[0].Detail, "blocked 1") {
		t.Fatalf("a blocked run must be named and left out of the rate: %+v", rows[0])
	}
	if rows[0].Subject != "4 runs" {
		t.Fatalf("the run count still counts every run: %+v", rows[0])
	}
	// A failure is what the rate is against.
	if rows[1].Outcome != "50% passed" {
		t.Fatalf("a failing run must move the rate: %+v", rows[1])
	}
	// A suite that never produced a verdict says so rather than printing a
	// rate over nothing — a checkout with no gate config yet must not read
	// as a project failing every check.
	if rows[2].Outcome != "no verdict" {
		t.Fatalf("a suite with no verdict either way printed %q", rows[2].Outcome)
	}
}

// sectionOf is one section's rows, from its heading to the next blank-line
// gap that starts another heading, so an assertion about one part of the
// dashboard cannot be satisfied by another part of it.
func sectionOf(report, header string) string {
	lines := strings.Split(report, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != header {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			body := strings.TrimSpace(lines[j])
			if body != "" && body == strings.ToUpper(body) && !strings.HasPrefix(body, "\u00b7") {
				return strings.Join(lines[i:j], "\n")
			}
		}
		return strings.Join(lines[i:], "\n")
	}
	return ""
}

// The wiring itself: a gate handed a recording session reports to it, one
// handed a session that is not recording reports nowhere, and no gate at
// all is not a crash. This is the seam every gate test above stops at.
func TestRecordGateVerdicts_Wiring(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)

	gate := &quality.Runner{Workspace: t.TempDir()}
	recordGateVerdicts(gate, rec)
	if gate.Observe == nil {
		t.Fatal("a recording session must leave the gate reporting to it")
	}
	gate.Observe("default", quality.VerdictPass)
	assertShapes(t, shapesOf(t, db, rec.sessionID()), []eventShape{
		{kind: storage.AgentEventSignal, tool: "default", outcome: "gate", reason: "pass"},
	})

	quiet := &quality.Runner{Workspace: t.TempDir()}
	recordGateVerdicts(quiet, nil)
	if quiet.Observe != nil {
		t.Fatal("a session that is not recording must leave the hook nil")
	}
	recordGateVerdicts(nil, rec)
}

// The rating is drawn beside the outcome it audits, in the words the walk
// asked the question in, and a session nobody has answered for draws no row
// at all — an empty rating and a thumbs-down are different facts, and the
// page would be inventing one of them by filling the field in.
func TestObserveSessionReport_RatingSitsBesideTheOutcome(t *testing.T) {
	row := goldenObserveSessionRow()
	if got := observeSessionReport(row, nil).Render(80); strings.Contains(got, "rated:") {
		t.Errorf("an unrated session was given a rating:\n%s", got)
	}
	for _, tc := range []struct {
		rating bool
		want   string
	}{{true, "rated:         worked"}, {false, "rated:         did not work"}} {
		row.Rating = &tc.rating
		got := observeSessionReport(row, nil).Render(80)
		if !strings.Contains(got, tc.want) {
			t.Errorf("the page does not say %q:\n%s", tc.want, got)
		}
		// It is next to what it is a check on, not filed among the provenance.
		if strings.Index(got, "outcome:") > strings.Index(got, tc.want) {
			t.Errorf("the rating is drawn before the outcome it checks:\n%s", got)
		}
	}
}
