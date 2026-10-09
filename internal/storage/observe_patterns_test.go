package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// patternCall is one call a seeded round asked for: the tool and its
// arguments as the model sent them.
type patternCall struct{ tool, args string }

// patternRound is one round of a seeded session: where it was, the calls the
// conversation holds for it, and the events the record holds for it.
type patternRound struct {
	turn, round int64
	calls       []patternCall
	events      []AgentEvent
}

// seedPatternSession opens a session in a checkout and writes its rounds to
// both sides: the record's events and, where slot is not empty, a saved
// conversation linked to the session.
func seedPatternSession(t *testing.T, db *DB, project, slot string, rounds ...patternRound) int64 {
	t.Helper()
	id, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.StampAgentSession(id, AgentProvenance{Project: project}); err != nil {
		t.Fatalf("stamp session: %v", err)
	}
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "go", Turn: 1}}
	for i, r := range rounds {
		var tcs []provider.ToolCall
		for j, c := range r.calls {
			tcs = append(tcs, provider.ToolCall{ID: fmt.Sprintf("c%d-%d", i, j), Name: c.tool, Arguments: c.args})
		}
		if len(tcs) > 0 {
			msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Turn: r.turn, Round: r.round, ToolCalls: tcs})
		}
		for _, e := range r.events {
			e.Turn, e.Round = r.turn, r.round
			if err := db.RecordAgentEvent(id, e); err != nil {
				t.Fatalf("record event: %v", err)
			}
		}
	}
	if slot != "" {
		if err := db.SaveChat(slot, msgs); err != nil {
			t.Fatalf("save chat: %v", err)
		}
		if _, err := db.LinkAgentSession(id, slot); err != nil {
			t.Fatalf("link session: %v", err)
		}
	}
	return id
}

func toolEvent(tool string) AgentEvent {
	return AgentEvent{Kind: AgentEventTool, Tool: tool, Outcome: "ok"}
}

func decisionEvent(outcome, reason string) AgentEvent {
	return AgentEvent{Kind: AgentEventDecision, Outcome: outcome, Reason: reason}
}

func gateEvent(suite, verdict string) AgentEvent {
	return AgentEvent{Kind: AgentEventSignal, Outcome: "gate", Tool: suite, Reason: verdict}
}

func readRound(turn, round int64, paths ...string) patternRound {
	r := patternRound{turn: turn, round: round}
	for _, p := range paths {
		r.calls = append(r.calls, patternCall{"read_file", `{"path":"` + p + `"}`})
		r.events = append(r.events, toolEvent("read_file"))
	}
	return r
}

var patternWindow = time.Now().AddDate(0, 0, -30)

// A path is counted by the distinct sessions that read it, each read paired
// with the call it was by session, turn, round, tool and position; a session
// whose conversation is gone, and a read at turn 0, are counted as reads no
// conversation could name rather than dropped; another checkout's sessions
// are not read at all.
func TestAgentFilePatterns_CountsDistinctSessionsAndSaysWhatCouldNotBeNamed(t *testing.T) {
	db := openTestDB(t)
	// Twice in one round, then once more later: one session, three calls.
	seedPatternSession(t, db, "p", "s1", readRound(1, 1, "go.mod", "go.mod"), readRound(2, 1, "go.mod"))
	seedPatternSession(t, db, "p", "s2", readRound(1, 3, "go.mod", "Makefile"))
	seedPatternSession(t, db, "p", "s3", readRound(1, 2, "go.mod"), patternRound{turn: 1, round: 4,
		calls:  []patternCall{{"search", `{"pattern":"TODO"}`}, {"glob", `{"pattern":"*.go","path":"internal"}`}},
		events: []AgentEvent{toolEvent("search"), toolEvent("glob")}})
	// Another checkout reads the same file, and is not this one's habit.
	seedPatternSession(t, db, "q", "s4", readRound(1, 1, "go.mod"))
	// A pruned conversation: the reads happened and cannot be named.
	seedPatternSession(t, db, "p", "s5", readRound(1, 1, "go.mod", "go.mod"))
	if err := db.DeleteChat("s5"); err != nil {
		t.Fatalf("delete chat: %v", err)
	}
	// A read recorded before positions were kept.
	seedPatternSession(t, db, "p", "s6", readRound(0, 0, "go.mod"))

	files, unjoined, err := db.AgentFilePatterns(patternWindow, "p", 1)
	if err != nil {
		t.Fatalf("read file patterns: %v", err)
	}
	want := []AgentFilePattern{
		{Path: "go.mod", Sessions: 3, Calls: 5},
		{Path: ".", Sessions: 1, Calls: 1},
		{Path: "Makefile", Sessions: 1, Calls: 1},
		{Path: "internal", Sessions: 1, Calls: 1},
	}
	if fmt.Sprint(files) != fmt.Sprint(want) {
		t.Fatalf("file patterns = %+v, want %+v", files, want)
	}
	if unjoined != (AgentUnjoined{Sessions: 2, Events: 3}) {
		t.Fatalf("unjoined = %+v, want 2 sessions and 3 reads", unjoined)
	}

	// The threshold is on sessions, not calls: go.mod's three sessions pass
	// it, and no other path does.
	files, _, err = db.AgentFilePatterns(patternWindow, "p", 3)
	if err != nil || len(files) != 1 || files[0].Path != "go.mod" {
		t.Fatalf("at three sessions: %+v (%v)", files, err)
	}

	// The window is the events': a cutoff after them reads nothing.
	files, unjoined, err = db.AgentFilePatterns(time.Now().Add(time.Hour), "p", 1)
	if err != nil || len(files) != 0 || unjoined != (AgentUnjoined{}) {
		t.Fatalf("after the window: %+v %+v (%v)", files, unjoined, err)
	}
}

func commandRound(round int64, line string, decisions ...AgentEvent) patternRound {
	return patternRound{turn: 1, round: round,
		calls:  []patternCall{{"execute_command", fmt.Sprintf("{\"command\":%q}", line)}},
		events: append(decisions, toolEvent("execute_command"))}
}

// A command is keyed on its first two words, counted by the distinct
// sessions a person was asked about it in, and answered by the round's last
// decision; a round that cannot be put to one command is counted apart; a
// command nobody was asked about is not a pattern.
func TestAgentCommandPatterns_KeysOnTwoWordsAndCountsTheAnswer(t *testing.T) {
	db := openTestDB(t)
	ask := decisionEvent("ask", "safety")
	seedPatternSession(t, db, "p", "s1",
		commandRound(1, "go test ./...", ask, decisionEvent("allow", "user")),
		commandRound(2, "go test -run X", ask, decisionEvent("allow", "user")),
		// Allowed by the list: nobody was asked.
		commandRound(3, "go vet ./...", decisionEvent("allow", "allowlist")))
	seedPatternSession(t, db, "p", "s2",
		commandRound(1, "  go\ttest  ./internal", ask, decisionEvent("deny", "user")))
	seedPatternSession(t, db, "p", "s3",
		// The person's grant from the card's list is an asked command too.
		commandRound(1, "go test", ask, decisionEvent("allow", "user-turn")),
		// A command beside a read: either could have been the one asked.
		patternRound{turn: 1, round: 2,
			calls:  []patternCall{{"execute_command", `{"command":"make lint"}`}, {"read_file", `{"path":"/etc/hosts"}`}},
			events: []AgentEvent{ask, decisionEvent("allow", "user")}},
		// An ask about a write is not a command's.
		patternRound{turn: 1, round: 3,
			calls:  []patternCall{{"write_file", `{"path":"a.go"}`}},
			events: []AgentEvent{ask, decisionEvent("allow", "user")}})
	seedPatternSession(t, db, "q", "s4", commandRound(1, "go test ./...", ask, decisionEvent("allow", "user")))
	// No conversation to read the round from.
	seedPatternSession(t, db, "p", "", commandRound(1, "go test ./...", ask))

	commands, unjoined, err := db.AgentCommandPatterns(patternWindow, "p", 1)
	if err != nil {
		t.Fatalf("read command patterns: %v", err)
	}
	want := []AgentCommandPattern{{Command: "go test", Sessions: 3, Asked: 4, Allowed: 3}}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("command patterns = %+v, want %+v", commands, want)
	}
	if unjoined != (AgentUnjoined{Sessions: 2, Events: 2}) {
		t.Fatalf("unjoined = %+v, want 2 sessions and 2 asks", unjoined)
	}
	if commands, _, err := db.AgentCommandPatterns(patternWindow, "p", 4); err != nil || len(commands) != 0 {
		t.Fatalf("at four sessions: %+v (%v)", commands, err)
	}
}

// A suite failed first where a failing run came before any pass in that
// session; a later failure, a blocked run and another checkout do not count.
func TestAgentSuitePatterns_CountsSessionsWhereAFailureCameFirst(t *testing.T) {
	db := openTestDB(t)
	gates := func(project string, events ...AgentEvent) {
		seedPatternSession(t, db, project, "", patternRound{events: events})
	}
	gates("p", gateEvent("default", "fail"), gateEvent("default", "pass"))
	gates("p", gateEvent("default", "pass"), gateEvent("default", "fail"))
	gates("p", gateEvent("default", "blocked"), gateEvent("default", "fail"), gateEvent("lint", "fail"))
	gates("p", gateEvent("default", "fail"), gateEvent("default", "fail"))
	gates("q", gateEvent("default", "fail"))

	suites, err := db.AgentSuitePatterns(patternWindow, "p", 1)
	if err != nil {
		t.Fatalf("read suite patterns: %v", err)
	}
	want := []AgentSuitePattern{{Suite: "default", Sessions: 3, Ran: 4}, {Suite: "lint", Sessions: 1, Ran: 1}}
	if fmt.Sprint(suites) != fmt.Sprint(want) {
		t.Fatalf("suite patterns = %+v, want %+v", suites, want)
	}
	if suites, err := db.AgentSuitePatterns(patternWindow, "p", 2); err != nil || len(suites) != 1 {
		t.Fatalf("at two sessions: %+v (%v)", suites, err)
	}
}
