package storage

import (
	"os"
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
	if err := db.EndAgentSession(id, ""); err != nil {
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

// The outcome written at a turn close stands on the row, and a session that
// ends with one already standing keeps it: the exit corrects an outcome
// only where there is none to correct.
func TestAgentSessionOutcome_StandsUntilCorrected(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.SetAgentSessionOutcome(id, "abandoned"); err != nil {
		t.Fatalf("set outcome: %v", err)
	}
	if err := db.SetAgentSessionOutcome(id, "completed"); err != nil {
		t.Fatalf("set outcome: %v", err)
	}
	// The killed session: nothing ends the row, and the last turn's reading
	// is what a reader finds.
	s, ok, err := db.AgentSession(id)
	if err != nil || !ok {
		t.Fatalf("session: ok=%v err=%v", ok, err)
	}
	if s.Outcome != "completed" || s.EndedAt != nil {
		t.Fatalf("expected a standing outcome on an unended session, got %+v", s)
	}

	if err := db.EndAgentSession(id, ""); err != nil {
		t.Fatalf("end session: %v", err)
	}
	if s, _, _ = db.AgentSession(id); s.Outcome != "completed" || s.EndedAt == nil {
		t.Fatalf("an empty outcome must leave the standing one alone, got %+v", s)
	}

	other, err := db.StartAgentSession("chat", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.EndAgentSession(other, "abandoned"); err != nil {
		t.Fatalf("end session: %v", err)
	}
	if s, _, _ = db.AgentSession(other); s.Outcome != "abandoned" {
		t.Fatalf("the exit must be able to write an outcome, got %+v", s)
	}
}

// A session with no outcome recorded counts as unknown rather than being
// dropped or folded into another bucket, and the outcome joins the export.
func TestAgentSessionOutcomes_UnknownIsItsOwnCategory(t *testing.T) {
	db := openTestDB(t)

	for _, outcome := range []string{"completed", "completed", "error", ""} {
		id, err := db.StartAgentSession("code", "openai", "gpt-test")
		if err != nil {
			t.Fatalf("start session: %v", err)
		}
		if outcome != "" {
			if err := db.SetAgentSessionOutcome(id, outcome); err != nil {
				t.Fatalf("set outcome: %v", err)
			}
		}
	}

	got, err := db.AgentSessionOutcomes(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("outcomes: %v", err)
	}
	counts := map[string]int{}
	for _, o := range got {
		counts[o.Outcome] = o.Count
	}
	want := map[string]int{"completed": 2, "error": 1, "unknown": 1}
	for outcome, n := range want {
		if counts[outcome] != n {
			t.Errorf("%s = %d sessions, want %d (got %+v)", outcome, counts[outcome], n, got)
		}
	}
	if len(counts) != len(want) {
		t.Errorf("outcome mix has %d buckets, want %d: %+v", len(counts), len(want), got)
	}

	exported, err := db.ExportAgentObservability(time.Now().Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var withOutcome int
	for _, s := range exported {
		if s.Outcome != "" {
			withOutcome++
		}
	}
	if withOutcome != 3 {
		t.Fatalf("the export carried %d outcomes, want 3", withOutcome)
	}
}

// Gate verdicts aggregate by suite and verdict, and a blocked run is counted
// as blocked: an infrastructure problem read as a failing check would move
// the one rate in the record that judges the work.
func TestAgentGateVerdicts(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	for _, e := range []struct{ suite, verdict string }{
		{"default", "pass"}, {"default", "pass"}, {"default", "fail"},
		{"lint", "blocked"},
	} {
		if err := db.RecordAgentEvent(id, AgentEvent{
			Kind: AgentEventSignal, Tool: e.suite, Outcome: "gate", Reason: e.verdict,
		}); err != nil {
			t.Fatalf("record gate event: %v", err)
		}
	}
	// A signal that is not a gate never reaches the gate aggregate.
	if err := db.RecordAgentEvent(id, AgentEvent{
		Kind: AgentEventSignal, Outcome: "summary", Reason: "on-target",
	}); err != nil {
		t.Fatalf("record signal: %v", err)
	}

	got, err := db.AgentGateVerdicts(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("gate verdicts: %v", err)
	}
	want := []AgentGateVerdict{
		{Suite: "default", Verdict: "pass", Count: 2},
		{Suite: "default", Verdict: "fail", Count: 1},
		{Suite: "lint", Verdict: "blocked", Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The walk's second query: a session is offered once it has a conversation to
// remind the reader what it was, and never again once it has been answered.
// The reminder prefers the title a model wrote and falls back to the first
// thing the person said.
func TestListUnratedSessions_OffersWhatCanBeRemembered(t *testing.T) {
	db := openTestDB(t)

	titled := unratedFixture(t, db, "titled", "make the dashboard show the gate rate")
	if err := db.SetChatTitle("titled", "the gate pass rate"); err != nil {
		t.Fatalf("set title: %v", err)
	}
	untitled := unratedFixture(t, db, "untitled", "rename the observer's callbacks")

	// A session whose conversation was never saved, and one whose saved
	// conversation holds nothing the reader could be reminded by. Neither can
	// be judged, so neither is asked about.
	unlinked, err := db.StartAgentSession("chat", "anthropic", "m")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.EndAgentSession(unlinked, "completed"); err != nil {
		t.Fatalf("end session: %v", err)
	}
	for chat, msg := range map[string]provider.Message{
		// Nothing the reader said at all…
		"silent": {Role: provider.RoleAssistant, Content: "hello"},
		// …and something they said that is not a reminder of anything.
		"blank": {Role: provider.RoleUser, Content: "   \n  "},
	} {
		if err := db.SaveChat(chat, []provider.Message{msg}); err != nil {
			t.Fatalf("save chat %q: %v", chat, err)
		}
		id, err := db.StartAgentSession("chat", "anthropic", "m")
		if err != nil {
			t.Fatalf("start session: %v", err)
		}
		if err := db.LinkAgentSession(id, chat); err != nil {
			t.Fatalf("link session: %v", err)
		}
	}

	got, err := db.ListUnratedSessions(10)
	if err != nil {
		t.Fatalf("list unrated sessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("the walk offered %d sessions, want the two with a conversation: %+v", len(got), got)
	}
	if got[0].ID != untitled || got[1].ID != titled {
		t.Errorf("the walk offered %d then %d, want the newest first", got[0].ID, got[1].ID)
	}
	if got[0].Title != "" || got[0].Opening != "rename the observer's callbacks" {
		t.Errorf("an untitled conversation reads %+v, want its opening line", got[0])
	}
	if got[1].Title != "the gate pass rate" || got[1].Chat != "titled" {
		t.Errorf("a titled conversation reads %+v", got[1])
	}
	if got[1].Kind != "chat" || got[1].Outcome != "completed" {
		t.Errorf("the session's own fields did not come with it: %+v", got[1])
	}

	if err := db.RateAgentSession(titled, true); err != nil {
		t.Fatalf("rate session: %v", err)
	}
	got, err = db.ListUnratedSessions(10)
	if err != nil {
		t.Fatalf("list unrated sessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != untitled {
		t.Errorf("an answered session was offered again: %+v", got)
	}
}

// The answer lands on the session's own row and joins the export, and an
// unanswered session is nil rather than false — "nobody has said" and "it
// went badly" are different facts and only one of them is a judgement.
func TestRateAgentSession_JoinsTheRowAndTheExport(t *testing.T) {
	db := openTestDB(t)

	liked := unratedFixture(t, db, "liked", "add the retention window")
	disliked := unratedFixture(t, db, "disliked", "rewrite the trimmer")
	unrated := unratedFixture(t, db, "unrated", "read the provider layer")

	if err := db.RateAgentSession(liked, true); err != nil {
		t.Fatalf("rate session: %v", err)
	}
	if err := db.RateAgentSession(disliked, false); err != nil {
		t.Fatalf("rate session: %v", err)
	}

	want := map[int64]*bool{liked: boolPtr(true), disliked: boolPtr(false), unrated: nil}
	for id, w := range want {
		s, ok, err := db.AgentSession(id)
		if err != nil || !ok {
			t.Fatalf("read session %d: %v", id, err)
		}
		if !sameRating(s.Rating, w) {
			t.Errorf("session %d reads %v, want %v", id, ratingWord(s.Rating), ratingWord(w))
		}
	}

	sessions, err := db.ExportAgentObservability(time.Time{}, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(sessions) != len(want) {
		t.Fatalf("the export carries %d sessions, want %d", len(sessions), len(want))
	}
	for _, s := range sessions {
		if !sameRating(s.Rating, want[s.ID]) {
			t.Errorf("the export gives session %d %v, want %v",
				s.ID, ratingWord(s.Rating), ratingWord(want[s.ID]))
		}
	}
}

// The two ratings are separate columns on separate tables, so the accuracy
// figure `shhh metrics` reports keeps meaning what it has always meant: what
// people made of the commands, and nothing about the sessions.
func TestRateAgentSession_LeavesTheCommandFiguresAlone(t *testing.T) {
	db := openTestDB(t)

	id, err := db.RecordRequest(RequestRecord{
		Provider: "anthropic", Model: "m", Prompt: "p", Command: "ls", Action: "run", Success: true})
	if err != nil {
		t.Fatalf("record request: %v", err)
	}
	session := unratedFixture(t, db, "session", "read the provider layer")
	if err := db.RateAgentSession(session, false); err != nil {
		t.Fatalf("rate session: %v", err)
	}

	before, err := db.MetricsSummary(time.Time{})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if len(before) != 1 || before[0].RatedCount != 0 {
		t.Fatalf("a rated session moved the command figures: %+v", before)
	}
	if err := db.RateRequest(id, true); err != nil {
		t.Fatalf("rate request: %v", err)
	}
	after, err := db.MetricsSummary(time.Time{})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if len(after) != 1 || after[0].RatedCount != 1 {
		t.Fatalf("the command rating did not reach the figures: %+v", after)
	}
}

// unratedFixture is one ended session with a saved conversation behind it,
// which is what the walk requires before it will ask about one.
func unratedFixture(t *testing.T, db *DB, chat, opening string) int64 {
	t.Helper()
	if err := db.SaveChat(chat, []provider.Message{{Role: provider.RoleUser, Content: opening}}); err != nil {
		t.Fatalf("save chat %q: %v", chat, err)
	}
	id, err := db.StartAgentSession("chat", "anthropic", "m")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.LinkAgentSession(id, chat); err != nil {
		t.Fatalf("link session: %v", err)
	}
	if err := db.EndAgentSession(id, "completed"); err != nil {
		t.Fatalf("end session: %v", err)
	}
	return id
}

func boolPtr(b bool) *bool { return &b }

func sameRating(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func ratingWord(r *bool) string {
	switch {
	case r == nil:
		return "unrated"
	case *r:
		return "up"
	}
	return "down"
}

// liveOnly makes the liveness check answer for a fixed set of ids, which is
// how a test writes down "this process is gone" without needing an id that
// is free on every machine.
func liveOnly(t *testing.T, pids ...int) {
	t.Helper()
	set := map[int]bool{}
	for _, pid := range pids {
		set[pid] = true
	}
	prev := pidRunning
	pidRunning = func(pid int) bool { return set[pid] }
	t.Cleanup(func() { pidRunning = prev })
}

// openSessionIn writes a row as another process would have left it: running
// in the checkout project fingerprints, under pid, last heard from at beat.
func openSessionIn(t *testing.T, db *DB, project string, pid int, beat time.Time) int64 {
	t.Helper()
	id, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.StampAgentSession(id, AgentProvenance{Project: project}); err != nil {
		t.Fatalf("stamp session: %v", err)
	}
	if _, err := db.SQL().Exec(`UPDATE agent_sessions SET pid = ?, heartbeat = ? WHERE id = ?`,
		pid, beat.UTC().Format(observeTimeFormat), id); err != nil {
		t.Fatalf("place session: %v", err)
	}
	return id
}

func TestLiveSibling_SeesTheOtherSessionInThisCheckoutAndNotOneElsewhere(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	liveOnly(t, 4242, 4343)

	here := now.Add(-20 * time.Minute)
	openSessionIn(t, db, "checkout-a", 4242, now)
	openSessionIn(t, db, "checkout-b", 4343, now)
	// The row is placed after the stamp, so the start time is this instant;
	// what the assertion needs is only that the right row was chosen.
	if _, err := db.SQL().Exec(`UPDATE agent_sessions SET started_at = ? WHERE pid = 4242`,
		here.UTC().Format(observeTimeFormat)); err != nil {
		t.Fatalf("age the sibling: %v", err)
	}

	sib, ok, err := db.LiveSibling("checkout-a", now)
	if err != nil || !ok {
		t.Fatalf("LiveSibling in checkout-a = %v, %v, want a sibling", ok, err)
	}
	if got := sib.Since.UTC().Truncate(time.Second); !got.Equal(here.UTC().Truncate(time.Second)) {
		t.Fatalf("sibling started at %s, want %s", got, here.UTC().Truncate(time.Second))
	}
	if _, ok, err := db.LiveSibling("checkout-c", now); ok || err != nil {
		t.Fatalf("LiveSibling in a checkout nobody is in = %v, %v, want none", ok, err)
	}
}

func TestLiveSibling_IgnoresThisProcessItsChildrenAndWhatIsGone(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	liveOnly(t, os.Getpid(), 4242, 4343)

	// This process's own row, and the row a new conversation in it left
	// behind: neither is another session.
	own, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start own session: %v", err)
	}
	if err := db.StampAgentSession(own, AgentProvenance{Project: "checkout-a"}); err != nil {
		t.Fatalf("stamp own session: %v", err)
	}
	if _, ok, _ := db.LiveSibling("checkout-a", now); ok {
		t.Fatal("this process's own row reported as a sibling")
	}

	// A sub-agent runs inside somebody's process and is never a session of
	// its own to be alone with.
	child, err := db.StartChildAgentSession(own, "writer", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	if err := db.StampAgentSession(child, AgentProvenance{Project: "checkout-a"}); err != nil {
		t.Fatalf("stamp child: %v", err)
	}
	if _, err := db.SQL().Exec(`UPDATE agent_sessions SET pid = 4242 WHERE id = ?`, child); err != nil {
		t.Fatalf("place child: %v", err)
	}
	if _, ok, _ := db.LiveSibling("checkout-a", now); ok {
		t.Fatal("a sub-agent's row reported as a sibling")
	}

	// A row whose heartbeat is older than the window: the id may be a
	// stranger's by now, so it vouches for nothing.
	openSessionIn(t, db, "checkout-a", 4343, now.Add(-2*agentHeartbeatWindow))
	if _, ok, _ := db.LiveSibling("checkout-a", now); ok {
		t.Fatal("a row nobody has heard from since the window reported as a sibling")
	}

	// And one whose process is simply gone.
	openSessionIn(t, db, "checkout-a", 9999, now)
	if _, ok, _ := db.LiveSibling("checkout-a", now); ok {
		t.Fatal("a row whose process is gone reported as a sibling")
	}
}

func TestLiveSibling_AnUnknownCheckoutMatchesNothing(t *testing.T) {
	db := openTestDB(t)
	liveOnly(t, 4242)
	openSessionIn(t, db, "", 4242, time.Now())
	if _, ok, err := db.LiveSibling("", time.Now()); ok || err != nil {
		t.Fatalf("LiveSibling with no fingerprint = %v, %v, want none", ok, err)
	}
}

func TestCloseCrashedAgentSessions_ClosesTheGoneAndLeavesTheRest(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	liveOnly(t, 4242)

	live := openSessionIn(t, db, "checkout-a", 4242, now)
	crashed := openSessionIn(t, db, "checkout-a", 9999, now)
	// A row from a build that recorded no id cannot vouch for itself, and
	// closing it would be rewriting a history the reader can still see.
	unknown := openSessionIn(t, db, "checkout-a", 0, now)

	closed, err := db.CloseCrashedAgentSessions()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed %d rows, want 1", closed)
	}
	for _, tc := range []struct {
		id   int64
		name string
		want bool
	}{
		{live, "the live session", false},
		{crashed, "the crashed session", true},
		{unknown, "the session with no recorded id", false},
	} {
		var ended *string
		if err := db.SQL().QueryRow(`SELECT ended_at FROM agent_sessions WHERE id = ?`, tc.id).Scan(&ended); err != nil {
			t.Fatalf("read %s: %v", tc.name, err)
		}
		if got := ended != nil; got != tc.want {
			t.Fatalf("%s closed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBeatAgentSession_KeepsTheRowInsideTheWindow(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	liveOnly(t, 4242)

	id := openSessionIn(t, db, "checkout-a", 4242, now.Add(-2*agentHeartbeatWindow))
	if _, ok, _ := db.LiveSibling("checkout-a", now); ok {
		t.Fatal("a stale row reported as a sibling before the beat")
	}
	if err := db.BeatAgentSession(id); err != nil {
		t.Fatalf("beat: %v", err)
	}
	if _, ok, _ := db.LiveSibling("checkout-a", now); !ok {
		t.Fatal("the row is still stale after a beat")
	}
}
