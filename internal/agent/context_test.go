package agent

import (
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/provider"
)

// placeholderIDRe reads the evidence id back out of a trim's placeholder. It
// is the same shape the reduction notice carries, which is the point: one
// wording, so the model has one thing to learn.
var placeholderIDRe = regexp.MustCompile(`evidence (ev-[0-9a-f]{16})`)

// trimmable is a tool result big enough to be worth an evidence entry and
// big enough to move a trim's arithmetic.
func trimmable() string { return strings.Repeat("finding worth keeping\n", 2000) }

func withOldResult(content string) []provider.Message {
	return []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "execute_command"}}},
		{Role: provider.RoleTool, Content: content, ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}
}

// TestTrimOldToolResults_ElisionIsRecoverable is the whole first half of the
// bug: before this, a session that trimmed and then needed the result had no
// way back to it and ran the tool again.
func TestTrimOldToolResults_ElisionIsRecoverable(t *testing.T) {
	store, err := evidence.Open(t.TempDir(), "sess-trim")
	if err != nil {
		t.Fatal(err)
	}
	red := evidence.NewReducer(store)
	original := trimmable()
	a := New(withOldResult(original), noStream)
	a.StoreElided(red.Keep)

	if elided, _ := a.TrimOldToolResults(70000, 60000, 40000, Calibration{}); elided != 1 {
		t.Fatalf("want the old result elided, got %d", elided)
	}
	placeholder := a.Messages()[3].Content
	m := placeholderIDRe.FindStringSubmatch(placeholder)
	if m == nil {
		t.Fatalf("the placeholder must name the entry that holds the original: %q", placeholder)
	}
	// Short enough that eliding still recovers the window it elided for: the
	// placeholder is a rounding error beside what it replaced.
	if EstimateTokens(placeholder)*10 > EstimateTokens(original) {
		t.Fatalf("placeholder costs %d tokens against the result's %d",
			EstimateTokens(placeholder), EstimateTokens(original))
	}

	data, meta, err := store.Read(m[1], 0, len(original)+1)
	if err != nil {
		t.Fatalf("the elided original must be readable: %v", err)
	}
	if string(data) != original {
		t.Fatal("the store must hold the result verbatim")
	}
	// The tool is read off the assistant message that made the call, so the
	// evidence tool can say what the entry is when the model asks.
	if meta.Tool != "execute_command" {
		t.Fatalf("entry filed under %q, want the tool that produced it", meta.Tool)
	}

	// And a second trim over the same conversation leaves the placeholder
	// alone rather than spending another entry on it.
	before := a.Messages()[3].Content
	a.TrimOldToolResults(70000, 60000, 40000, Calibration{})
	if a.Messages()[3].Content != before {
		t.Fatal("a placeholder must not be elided again")
	}
	if st := store.Stats(); st.Entries != 1 {
		t.Fatalf("want one entry for one elision, got %d", st.Entries)
	}
}

// TestTrimOldToolResults_StoreFailureStillElides: the trim's job is to make
// the next request fit, and a session whose store refuses is exactly the
// session that most needs the window back.
func TestTrimOldToolResults_StoreFailureStillElides(t *testing.T) {
	a := New(withOldResult(trimmable()), noStream)
	a.StoreElided(func(string, string) (string, bool) { return "", false })

	elided, newEst := a.TrimOldToolResults(70000, 60000, 40000, Calibration{})
	if elided != 1 {
		t.Fatalf("a refused store must not stop the trim, got %d elided", elided)
	}
	if got := a.Messages()[3].Content; got != ElidedResult {
		t.Fatalf("want the bare placeholder, got %q", got)
	}
	if newEst >= 70000 {
		t.Fatalf("the estimate should still fall, got %d", newEst)
	}
}

// TestTrimOldToolResults_ShortResultIsLeftAlone: a result no longer than the
// placeholder that would replace it recovers nothing, and rewriting it would
// spend the provider's cached prefix from that message on for no gain. It is
// also never handed to the store — an entry nothing in the conversation
// points at is a file kept for a week that nobody can ask for.
func TestTrimOldToolResults_ShortResultIsLeftAlone(t *testing.T) {
	a := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleTool, Content: "ok", ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, noStream)
	offered := 0
	a.StoreElided(func(string, string) (string, bool) {
		offered++
		return "ev-0123456789abcdef", true
	})

	elided, _ := a.TrimOldToolResults(70000, 60000, 40000, Calibration{})
	if elided != 0 {
		t.Fatalf("want nothing elided, got %d", elided)
	}
	if a.Messages()[2].Content != "ok" {
		t.Fatal("a result shorter than its placeholder must be left as it is")
	}
	if offered != 0 {
		t.Fatalf("the store was asked to keep %d results the trim then left alone", offered)
	}
}

// TestTrimOldToolResults_ShrinksByTheCallersUnits: the caller trims against a
// corrected figure, so the amount taken off it has to be corrected too, or
// the loop watches the number fall slower than the conversation does and
// elides past the mark it asked for.
func TestTrimOldToolResults_ShrinksByTheCallersUnits(t *testing.T) {
	big := strings.Repeat("x", 40000) // ~10k estimated tokens each
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
	}
	for range 6 {
		msgs = append(msgs, provider.Message{Role: provider.RoleTool, Content: big, ToolCallID: "c"})
	}
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: "q2"})

	var cal Calibration
	for range 8 {
		cal.Observe("m", 3, 2) // a session that tokenizes at three bytes
	}
	if got := cal.Factor(); got < 1.4 || got > 1.5 {
		t.Fatalf("factor %v is not near the ratio it was fed", got)
	}

	a := New(msgs, noStream)
	elided, newEst := a.TrimOldToolResults(70000, 60000, 40000, cal)
	if newEst > 40000 {
		t.Fatalf("the trim stopped at %d, above the mark of 40000", newEst)
	}
	// Each result is worth ~10k raw and ~15k corrected, so three clear the
	// 30k the mark asks for where four would be needed in raw units.
	if elided != 3 {
		t.Fatalf("want 3 elided under a 1.5 correction, got %d", elided)
	}
}

// TestCalibration_ConvergesAndIsBounded: the factor is a running measurement
// of the estimator, so it has to move toward what the reports say and stop
// well short of a figure no text could produce.
func TestCalibration_ConvergesAndIsBounded(t *testing.T) {
	var c Calibration
	if c.Factor() != 1 || c.Corrected() {
		t.Fatalf("an unmeasured session must correct nothing, got %v", c.Factor())
	}
	if got := c.Apply(1000); got != 1000 {
		t.Fatalf("an unmeasured factor must leave an estimate alone, got %d", got)
	}

	// Three responses that each cost a third more than the estimate said.
	var prev float64 = 1
	for range 3 {
		c.Observe("gpt-5.2", 4000, 3000)
		f := c.Factor()
		if f <= prev || f > 4.0/3.0 {
			t.Fatalf("factor %v should climb toward 1.333 from %v", f, prev)
		}
		prev = f
	}
	// Three responses close seven eighths of the distance, which is what
	// "the factor follows the reports" has to mean to be worth having: a
	// turn that trims two rounds after a model started answering is trimming
	// against a figure the session has already measured.
	const ratio = 4.0 / 3.0
	if gap := (ratio - prev) / (ratio - 1); gap > 0.15 {
		t.Fatalf("three responses left %.0f%% of the gap, at factor %v", gap*100, prev)
	}
	if !c.Corrected() {
		t.Fatal("a moved factor is a corrected figure and must say so")
	}
	if got := c.Apply(3000); got < 3800 || got > 4000 {
		t.Fatalf("Apply should scale an estimate toward the report, got %d", got)
	}

	// A report ten times the estimate is not the estimator being wrong about
	// the messages; it is describing something else, and the factor stops.
	for range 20 {
		c.Observe("gpt-5.2", 40000, 3000)
	}
	if got := c.Factor(); got != calibrationCeiling {
		t.Fatalf("factor ran to %v, want the ceiling %v", got, calibrationCeiling)
	}
	for range 20 {
		c.Observe("gpt-5.2", 300, 3000)
	}
	if got := c.Factor(); got != calibrationFloor {
		t.Fatalf("factor ran to %v, want the floor %v", got, calibrationFloor)
	}
}

// TestCalibration_ResetsOnModelSwitch: the factor describes a tokenizer, and
// a stale one is worse than none because it is confident.
func TestCalibration_ResetsOnModelSwitch(t *testing.T) {
	var c Calibration
	for range 5 {
		c.Observe("gpt-5.2", 4000, 3000)
	}
	if !c.Corrected() {
		t.Fatal("the first model should have moved the factor")
	}
	c.Observe("claude-opus-5", 0, 0)
	if c.Corrected() || c.Factor() != 1 {
		t.Fatalf("a model switch must start the measurement over, got %v", c.Factor())
	}
}

// TestCalibration_NothingReportedChangesNothing: a provider that reports no
// usage leaves the session on the estimate it has always used.
func TestCalibration_NothingReportedChangesNothing(t *testing.T) {
	var c Calibration
	for range 5 {
		c.Observe("local-model", 0, 3000)
	}
	// And a conversation with nothing in it yet is no comparison either.
	c.Observe("local-model", 4000, 0)
	if c.Corrected() || c.Apply(1234) != 1234 {
		t.Fatalf("an unreported session must estimate exactly as before, got %v", c.Factor())
	}
}
