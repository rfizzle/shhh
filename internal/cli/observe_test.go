package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	rec.toolCall("read_file", 5*time.Millisecond, "ok")
	rec.decision("deny", "plan-mode")
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
	rec.toolCall("read_file", time.Millisecond, "ok")
	rec.decision("allow", "user")
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
