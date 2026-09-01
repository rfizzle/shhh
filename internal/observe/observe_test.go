package observe

import (
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/quality"
)

func TestReasonCode_Mapping(t *testing.T) {
	cases := map[string]string{
		"accept-edits mode":    "mode-accept-edits",
		"auto mode":            "mode-auto",
		"session policy":       "session-grant",
		"session grant":        "session-scope",
		"allowlist":            "allowlist",
		"plan mode":            "plan-mode",
		"plan mode inspection": "plan-inspection",
		// The refusal carries the directory it was refused for, so it is
		// matched by shape — and the path does not reach the code.
		"outside the working scope: /home/someone/secrets": "out-of-scope",
		"rm -rf / looked really safe!":                     "other",
		"":                                                 "other",
	}
	for raw, want := range cases {
		if got := ReasonCode(raw); got != want {
			t.Errorf("ReasonCode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestAskReason(t *testing.T) {
	for _, c := range []struct {
		name string
		act  agent.Action
		want string
	}{
		{"safety wins", agent.Action{Kind: agent.ActionCommand, SafetyFlagged: true}, "safety"},
		{"scope-sensitive", agent.Action{Kind: agent.ActionEdit, ScopeSensitive: true}, "scope-sensitive"},
		// The paths are what put the call out of scope and none of them
		// reaches the code.
		{"out of scope", agent.Action{Kind: agent.ActionEdit, OutOfScope: []string{"/etc"}}, "out-of-scope"},
		{"nothing flagged", agent.Action{Kind: agent.ActionEdit}, "policy"},
	} {
		if got := AskReason(c.act); got != c.want {
			t.Errorf("%s: AskReason = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestClassFromResult(t *testing.T) {
	cases := map[string]string{
		"data":              "",
		"No matches found.": ClassEmpty,
		"error: open x: no such file or directory": ClassNotFound,
		"error: the user declined this tool call":  ClassDeclined,
		"error: this session is in plan mode":      ClassPlanMode,
		"error: this path is outside the session":  ClassOutOfScope,
		"error: cancelled by user":                 ClassCancelled,
		"error: open x: permission denied":         ClassDeclined,
		"error: context deadline exceeded":         ClassTimeout,
		"error: unknown tool frobnicate":           ClassUnknown,
		"error: invalid arguments: missing 'path'": ClassBadArgs,
		"error: boom": ClassOther,
	}
	for in, want := range cases {
		if got := ClassFromResult(in); got != want {
			t.Errorf("ClassFromResult(%q) = %q, want %q", in, got, want)
		}
	}
}

// ToolOutcome is the one call a surface makes to report a result, so it has
// to agree with the two functions it is made of on every shape of result.
func TestToolOutcome(t *testing.T) {
	for _, c := range []struct {
		result  string
		outcome string
		class   string
	}{
		{"data", OutcomeOK, ""},
		{"No matches found.", OutcomeOK, ClassEmpty},
		{"error: open x: no such file or directory", OutcomeError, ClassNotFound},
	} {
		outcome, class := ToolOutcome(c.result)
		if outcome != c.outcome || class != c.class {
			t.Errorf("ToolOutcome(%q) = (%q, %q), want (%q, %q)",
				c.result, outcome, class, c.outcome, c.class)
		}
	}
}

// Every unattended surface reports a reading with this, so the four states
// have to keep the four spellings a stored rate is grouped by.
func TestSummaryCode(t *testing.T) {
	for _, c := range []struct {
		state agent.SummaryState
		want  string
	}{
		{agent.SummaryOnTarget, "on-target"},
		{agent.SummaryOffTarget, "off-target"},
		{agent.SummarySufficient, "sufficient"},
		{agent.SummaryUncertain, "unclear"},
		// A state nothing declared reads as unclear rather than as a fifth
		// code: the reading is what is uncertain, not the vocabulary.
		{agent.SummaryState(99), "unclear"},
	} {
		if got := SummaryCode(c.state); got != c.want {
			t.Errorf("SummaryCode(%v) = %q, want %q", c.state, got, c.want)
		}
	}
}

func TestGateVerdict(t *testing.T) {
	cases := map[quality.Verdict]string{
		quality.VerdictPass:      GatePass,
		quality.VerdictFail:      GateFail,
		quality.VerdictBlocked:   GateBlocked,
		quality.VerdictCancelled: GateCancelled,
		// A verdict this build has no word for is unknown, never a pass:
		// the record must not learn to read a fifth verdict as success.
		quality.Verdict("flaky"): GateUnknown,
		quality.Verdict(""):      GateUnknown,
	}
	for v, want := range cases {
		if got := GateVerdict(v); got != want {
			t.Errorf("GateVerdict(%q) = %q, want %q", v, got, want)
		}
	}
}

// An observer that takes no gate verdicts hands the runner nothing to call,
// so a surface can wire the hook without asking whether it is recording.
func TestGateHook_NilObserverTakesNoHook(t *testing.T) {
	if GateHook(Observer{}) != nil {
		t.Fatal("an observer with no Gate must produce no hook")
	}
}

func TestGateHook_CarriesSuiteAndVerdict(t *testing.T) {
	var suite, verdict string
	hook := GateHook(Observer{Gate: func(s, v string) { suite, verdict = s, v }})
	if hook == nil {
		t.Fatal("expected a hook")
	}
	hook("lint", quality.VerdictFail)
	if suite != "lint" || verdict != GateFail {
		t.Fatalf("hook recorded %q/%q, want lint/%s", suite, verdict, GateFail)
	}
	// The runner sends no name for a suite that resolved against nothing,
	// and what reaches the record is a code rather than a blank.
	hook("", quality.VerdictBlocked)
	if suite != GateSuiteUnknown || verdict != GateBlocked {
		t.Fatalf("hook recorded %q/%q, want %s/%s", suite, verdict, GateSuiteUnknown, GateBlocked)
	}
}

// The session's outcome is the last turn's, read into the vocabulary the row
// is kept in.
func TestSessionOutcome(t *testing.T) {
	cases := map[string]string{
		TurnDone:      SessionCompleted,
		TurnCancelled: SessionInterrupted,
		TurnFailed:    SessionError,
		// A cap is a pause, not a close: a sub-agent's supervisor grants
		// itself more rounds and runs on, so reading one as an abandonment
		// would libel every child that stopped to take stock.
		TurnCapPaused: "",
		// Nothing to read leaves the standing outcome alone rather than
		// overwriting it with a guess.
		"":         "",
		"whatever": "",
	}
	for turn, want := range cases {
		if got := SessionOutcome(turn); got != want {
			t.Errorf("SessionOutcome(%q) = %q, want %q", turn, got, want)
		}
	}
}

// unknown is never written by anything. It is how an absent outcome reads,
// which is a fact about the record rather than about the work, and a mapping
// that produced it would make the two indistinguishable.
func TestSessionOutcome_NeverWritesUnknown(t *testing.T) {
	for _, turn := range []string{TurnDone, TurnCancelled, TurnFailed, TurnCapPaused, "", "surprise"} {
		if SessionOutcome(turn) == SessionUnknown {
			t.Errorf("SessionOutcome(%q) produced %q", turn, SessionUnknown)
		}
		// Abandonment is a reading of how the session left, which no turn
		// is in a position to take.
		if SessionOutcome(turn) == SessionAbandoned {
			t.Errorf("SessionOutcome(%q) produced %q", turn, SessionAbandoned)
		}
	}
}
