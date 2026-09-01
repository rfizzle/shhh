package storage

import (
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
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
		if err := db.RecordAgentEvent(id, AgentEvent{Kind: e.kind, Tool: e.tool, DurationMs: e.duration, Outcome: e.outcome, Reason: e.reason, Turn: 1, Round: 2}); err != nil {
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

	export, err := db.ExportAgentObservability(since, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(export) != 1 || len(export[0].Events) != len(events) {
		t.Fatalf("unexpected export: %+v", export)
	}
	if export[0].Events[0].Tool != "read_file" || export[0].Events[0].Outcome != "ok" {
		t.Fatalf("unexpected first exported event: %+v", export[0].Events[0])
	}
	if export[0].Events[0].Turn != 1 || export[0].Events[0].Round != 2 {
		t.Fatalf("expected the event's position to export, got %+v", export[0].Events[0])
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

func TestStartChildAgentSession_LinksParent(t *testing.T) {
	db := openTestDB(t)

	parentID, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start parent: %v", err)
	}
	childID, err := db.StartChildAgentSession(parentID, "writer", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	if childID == parentID {
		t.Fatal("child must be its own session row")
	}
	if err := db.UpdateAgentSession(childID, 1, 500, 100, 0.01); err != nil {
		t.Fatalf("update child: %v", err)
	}

	sessions, err := db.ExportAgentObservability(time.Now().Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var foundChild, foundParent bool
	for _, s := range sessions {
		switch s.ID {
		case childID:
			foundChild = true
			if s.ParentID == nil || *s.ParentID != parentID {
				t.Fatalf("child parent_id = %v, want %d", s.ParentID, parentID)
			}
			if s.Kind != "writer" {
				t.Fatalf("child kind = %q", s.Kind)
			}
		case parentID:
			foundParent = true
			if s.ParentID != nil {
				t.Fatal("parent must have no parent_id")
			}
		}
	}
	if !foundChild || !foundParent {
		t.Fatalf("export missing sessions: child=%v parent=%v", foundChild, foundParent)
	}

	// A non-positive parent records an unlinked session.
	looseID, err := db.StartChildAgentSession(0, "researcher", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start unlinked: %v", err)
	}
	if looseID <= 0 {
		t.Fatal("unlinked session not created")
	}
}

func TestAgentObservability_TurnsSignalsAndProvenance(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	settings := AgentSettings{
		Mode: "auto", Reasoning: "high", MaxRounds: 60,
		SummaryModel: "small-model", SummaryInterval: 10, SummaryEnabled: true,
		ClassifierModel: "small-model", SandboxProfile: "workspace", ConfigHash: "cfg0",
	}
	if err := db.StampAgentSession(id, AgentProvenance{Version: "1.2.3", PromptHash: "abc123", Skills: 2, Project: "p0", Settings: settings}); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if err := db.LinkAgentSession(id, "2026-01-01 10:00:00"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := db.SaveChat("2026-01-01 10:00:00", []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "fix the build"},
	}); err != nil {
		t.Fatalf("save chat: %v", err)
	}

	ms := int64(900)
	events := []AgentEvent{
		{Kind: AgentEventTurn, Outcome: "done", Turn: 1, Round: 4, DurationMs: &ms},
		{Kind: AgentEventTurn, Outcome: "done", Turn: 2, Round: 8, DurationMs: &ms},
		{Kind: AgentEventTurn, Outcome: "cap-paused", Turn: 3, Round: 150},
		{Kind: AgentEventSignal, Outcome: "summary", Reason: "off-target", Turn: 3, Round: 40},
		{Kind: AgentEventSignal, Outcome: "repeat-notice", Reason: "search", Turn: 3, Round: 41},
		{Kind: AgentEventSignal, Outcome: "repeat-notice", Reason: "search", Turn: 3, Round: 42},
		{Kind: AgentEventTool, Tool: "read_file", Outcome: "error", Reason: "not-found", Turn: 1, Round: 1},
		{Kind: AgentEventTool, Tool: "read_file", Outcome: "ok", Turn: 1, Round: 2},
	}
	for _, e := range events {
		if err := db.RecordAgentEvent(id, e); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	since := time.Now().Add(-time.Hour)
	turns, err := db.AgentTurns(since)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != 2 || turns[0].Outcome != "done" || turns[0].Count != 2 || turns[0].AvgRounds != 6 || turns[0].MaxRounds != 8 {
		t.Fatalf("unexpected turns: %+v", turns)
	}
	if turns[1].Outcome != "cap-paused" || turns[1].AvgDurationMs != nil {
		t.Fatalf("unexpected paused turn row: %+v", turns[1])
	}

	signals, err := db.AgentSignals(since)
	if err != nil {
		t.Fatalf("signals: %v", err)
	}
	if len(signals) != 2 || signals[0].Signal != "repeat-notice" || signals[0].Reason != "search" || signals[0].Count != 2 {
		t.Fatalf("unexpected signals: %+v", signals)
	}

	errs, err := db.AgentToolErrors(since)
	if err != nil {
		t.Fatalf("tool errors: %v", err)
	}
	if len(errs) != 1 || errs[0].Tool != "read_file" || errs[0].Class != "not-found" || errs[0].Count != 1 {
		t.Fatalf("unexpected tool errors: %+v", errs)
	}

	s, ok, err := db.AgentSession(id)
	if err != nil || !ok {
		t.Fatalf("session: ok=%v err=%v", ok, err)
	}
	if s.Version != "1.2.3" || s.PromptHash != "abc123" || s.Skills != 2 || s.Project != "p0" || s.ChatSession != "2026-01-01 10:00:00" {
		t.Fatalf("unexpected provenance: %+v", s)
	}
	if s.Settings == nil || *s.Settings != settings {
		t.Fatalf("unexpected settings: %+v", s.Settings)
	}
	if _, ok, err := db.AgentSession(id + 100); err != nil || ok {
		t.Fatalf("expected no session, got ok=%v err=%v", ok, err)
	}

	plain, err := db.ExportAgentObservability(since, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(plain) != 1 || plain[0].Transcript != nil || plain[0].PromptHash != "abc123" {
		t.Fatalf("expected a content-free export with provenance, got %+v", plain)
	}
	if plain[0].Settings == nil || plain[0].Settings.ConfigHash != "cfg0" {
		t.Fatalf("expected the settings exported, got %+v", plain[0].Settings)
	}
	joined, err := db.ExportAgentObservability(since, true)
	if err != nil {
		t.Fatalf("export with transcript: %v", err)
	}
	if len(joined) != 1 || len(joined[0].Transcript) != 2 || joined[0].Transcript[1].Content != "fix the build" {
		t.Fatalf("expected the transcript joined, got %+v", joined)
	}
}

// A session recorded before settings were stamped has no settings, not a
// row of zero values: NULL columns are what the migration leaves on an old
// row, and what a stamp that carries no hash leaves too, and both read back
// as nil rather than as "manual, off, uncapped". The second stands in for
// the first here because the two are the same bytes in the table — the
// migration adds the columns with no default, so an old row's are NULL —
// and there is no harness that opens a store at the previous schema.
func TestAgentSession_SettingsAbsentReadAsEmpty(t *testing.T) {
	db := openTestDB(t)

	old, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.StampAgentSession(old, AgentProvenance{Version: "1.2.3", PromptHash: "abc123"}); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	s, ok, err := db.AgentSession(old)
	if err != nil || !ok {
		t.Fatalf("session: ok=%v err=%v", ok, err)
	}
	if s.Settings != nil {
		t.Fatalf("a stamp without a config hash must leave the settings empty, got %+v", s.Settings)
	}
	if s.Version != "1.2.3" {
		t.Fatalf("the provenance half must still land, got %+v", s)
	}

	listed, err := db.AgentSessions(time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(listed) != 1 || listed[0].Settings != nil {
		t.Fatalf("expected one session with no settings, got %+v", listed)
	}
	exported, err := db.ExportAgentObservability(time.Now().Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(exported) != 1 || exported[0].Settings != nil {
		t.Fatalf("expected the export to omit absent settings, got %+v", exported)
	}
}
