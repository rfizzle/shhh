package storage

import (
	"testing"
	"time"
)

func TestAgentObservability_Lifecycle(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.UpdateAgentSession(id, 3, 1000, 200, 0.05); err != nil {
		t.Fatalf("update session: %v", err)
	}

	ms := int64(12)
	events := []struct {
		kind, tool, outcome, reason string
		duration                    *int64
	}{
		{AgentEventTool, "read_file", "ok", "", &ms},
		{AgentEventTool, "read_file", "error", "", &ms},
		{AgentEventTool, "execute_command", "ok", "", &ms},
		{AgentEventDecision, "", "deny", "plan-mode", nil},
		{AgentEventDecision, "", "allow", "mode-auto", nil},
	}
	for _, e := range events {
		if err := db.RecordAgentEvent(id, e.kind, e.tool, e.duration, e.outcome, e.reason); err != nil {
			t.Fatalf("record event: %v", err)
		}
	}
	if err := db.EndAgentSession(id); err != nil {
		t.Fatalf("end session: %v", err)
	}

	since := time.Now().Add(-time.Hour)

	byDay, err := db.AgentUsageByDay(since)
	if err != nil {
		t.Fatalf("usage by day: %v", err)
	}
	if len(byDay) != 1 || byDay[0].Sessions != 1 || byDay[0].TokensIn != 1000 || byDay[0].TokensOut != 200 {
		t.Fatalf("unexpected day usage: %+v", byDay)
	}

	byModel, err := db.AgentUsageByModel(since)
	if err != nil {
		t.Fatalf("usage by model: %v", err)
	}
	if len(byModel) != 1 || byModel[0].Provider != "openai" || byModel[0].Model != "gpt-test" {
		t.Fatalf("unexpected model usage: %+v", byModel)
	}
	if byModel[0].Cost != 0.05 {
		t.Fatalf("expected cost 0.05, got %v", byModel[0].Cost)
	}

	toolMix, err := db.AgentToolMix(since)
	if err != nil {
		t.Fatalf("tool mix: %v", err)
	}
	if len(toolMix) != 2 {
		t.Fatalf("expected 2 tools, got %+v", toolMix)
	}
	if toolMix[0].Tool != "read_file" || toolMix[0].Count != 2 || toolMix[0].ErrorRate != 0.5 {
		t.Fatalf("unexpected read_file mix: %+v", toolMix[0])
	}
	if toolMix[0].AvgDurationMs == nil || *toolMix[0].AvgDurationMs != 12 {
		t.Fatalf("unexpected avg duration: %+v", toolMix[0].AvgDurationMs)
	}

	decisions, err := db.AgentDecisions(since)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	if len(decisions) != 2 {
		t.Fatalf("expected 2 decision groups, got %+v", decisions)
	}
	found := map[string]int{}
	for _, d := range decisions {
		found[d.Decision+"/"+d.Reason] = d.Count
	}
	if found["deny/plan-mode"] != 1 || found["allow/mode-auto"] != 1 {
		t.Fatalf("unexpected decisions: %+v", found)
	}

	sessions, err := db.AgentSessions(since, 10)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.ID != id || s.Kind != "code" || s.Turns != 3 || s.TokensIn != 1000 || s.TokensOut != 200 {
		t.Fatalf("unexpected session: %+v", s)
	}
	if s.EndedAt == nil {
		t.Fatal("expected ended session to have an end time")
	}

	export, err := db.ExportAgentObservability(since)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(export) != 1 || len(export[0].Events) != len(events) {
		t.Fatalf("unexpected export: %+v", export)
	}
	if export[0].Events[0].Tool != "read_file" || export[0].Events[0].Outcome != "ok" {
		t.Fatalf("unexpected first exported event: %+v", export[0].Events[0])
	}

	purged, err := db.PurgeAgentObservability()
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged session, got %d", purged)
	}
	sessions, err = db.AgentSessions(since, 10)
	if err != nil {
		t.Fatalf("sessions after purge: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions after purge, got %d", len(sessions))
	}
}

func TestAgentObservability_WindowCutoff(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.StartAgentSession("chat", "openai", "gpt-test"); err != nil {
		t.Fatalf("start session: %v", err)
	}

	future := time.Now().Add(time.Hour)
	sessions, err := db.AgentSessions(future, 10)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("future cutoff should exclude the session, got %d", len(sessions))
	}

	past := time.Now().Add(-time.Hour)
	sessions, err = db.AgentSessions(past, 10)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("past cutoff should include the session, got %d", len(sessions))
	}
}
