package subagent

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/skill"
)

// bulkyEnv is the scripted environment with an executor that returns more
// than the child's window can hold, which is the whole shape of the failure:
// a child reading a large file used to run out of window and end there.
func bulkyEnv(s *scriptedEnv, bytes int) EnvFactory {
	base := s.factory()
	return func(ctx context.Context, spec Spec) (Env, error) {
		env, err := base(ctx, spec)
		if err != nil {
			return env, err
		}
		env.Executor = func(string, json.RawMessage) (string, error) {
			return strings.Repeat("word ", bytes/5), nil
		}
		return env, nil
	}
}

// A child that would have met its window finishes under it instead, and says
// so where its parent is looking. The model is one with a small published
// window, because a child works out what it has to fit inside from the only
// thing it is told about the model it runs on: its name.
func TestChildCompactsAndReportsOnItsLane(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{text: "the child read a large file and found the thing it was after"},
		{text: "found it"},
	}}
	rec := &testRecorder{}
	sup := New(t.Context(), Options{
		Root:   t.TempDir(),
		NewEnv: bulkyEnv(env, 20000),
		Record: func(Spec, string) Recorder { return rec.recorder() },
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read the file","model":"phi"}`)
	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	if !strings.Contains(report, "found it") {
		t.Fatalf("the child did not finish under its window: %s", report)
	}

	var said string
	for _, e := range sup.Transcript("researcher-1") {
		if e.Kind == EntrySystem && strings.Contains(e.Text, "compacted") {
			said = e.Text
		}
	}
	if said == "" {
		t.Fatalf("a child recycled its conversation and said nothing on its lane: %+v",
			sup.Transcript("researcher-1"))
	}

	var compacted []recordedEvent
	for _, e := range rec.of("signal") {
		if e.outcome == observe.SignalCompact {
			compacted = append(compacted, e)
		}
	}
	if len(compacted) != 1 || compacted[0].reason != observe.CompactPressure {
		t.Fatalf("expected one compaction recorded under pressure, got %+v", compacted)
	}
}

// A model no table and no family answers for leaves a child exactly as it
// was: recovering against a guessed window would throw away the work of a
// child that had most of its room left.
func TestChildCompactorNeedsAWindowItCanName(t *testing.T) {
	if c := childCompactor("a-private-build-of-our-own", Env{}); c != nil {
		t.Fatalf("a step was built against a window nothing could name: %+v", c)
	}
	c := childCompactor("phi", Env{})
	if c == nil {
		t.Fatal("no step for a model whose window is published")
	}
	if c.Window <= 0 || c.Model != "phi" {
		t.Fatalf("step built with %+v", c)
	}
	// The child's own stream and no other: one stream, bound to one model and
	// one role-scoped toolset, is the only door a child has out.
	if c.Stream != nil {
		t.Fatal("a child was given a stream of its own to summarize on")
	}
}

// The table is asked before the family floor, and the definitions the child
// was actually given are part of what fills its window. A child is routinely
// routed to a model the session is not on — a cheap one for a wide search —
// and the floor is a reading of a model's name, so a name only the table can
// place used to leave that child running with no recovery at all.
func TestChildCompactorTakesTheTablesWindowAndTheToolsetsCost(t *testing.T) {
	env := Env{Window: 400_000, ToolTokens: 3_500}
	c := childCompactor("a-private-build-of-our-own", env)
	if c == nil {
		t.Fatal("no step for a model the price table can place")
	}
	if c.Window != 400_000 || c.ToolTokens != 3_500 {
		t.Fatalf("step built with %+v", c)
	}
	// And the table wins where both can answer: it is the model's published
	// window, where the family floor is what every model of that shape has.
	if c := childCompactor("phi", Env{Window: 999_000}); c == nil || c.Window != 999_000 {
		t.Fatalf("the family floor was preferred to the table: %+v", c)
	}
}

// The whole of what a child loses when a trim runs and nothing was installed
// to catch it: the contents of its early reads, and the instructions it was
// told to follow. A child recovers its window at every round boundary, so it
// meets this far more often than a person's session does — and there is
// nobody watching it to notice either one go.
func TestNewChildAgent_ATrimIsRecoverableAndSparesSkills(t *testing.T) {
	store, err := evidence.Open(t.TempDir(), "sess-child-trim")
	if err != nil {
		t.Fatal(err)
	}
	red := evidence.NewReducer(store)

	instructions := "<skill_content name=\"documentation\">\n" + strings.Repeat("follow this. ", 200) + "\n</skill_content>"
	if !skill.IsContent(instructions) {
		t.Fatal("the fixture is not what the session's keep predicate looks for")
	}
	original := strings.Repeat("a finding the child read once. ", 200)

	a := newChildAgent(Env{
		SystemPrompt: "sys",
		KeepResult:   skill.IsContent,
		Archive:      red.Keep,
	}, 10)
	a.SetMessages([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "the task"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "read_file"}, {ID: "c2", Name: "skill"}}},
		{Role: provider.RoleTool, Content: original, ToolCallID: "c1"},
		{Role: provider.RoleTool, Content: instructions, ToolCallID: "c2"},
		// The turn the trim runs in front of: everything before it is what
		// a round boundary may take.
		{Role: provider.RoleUser, Content: "carry on"},
	})

	if elided, _ := a.TrimOldToolResults(70000, 60000, 40000, agent.Calibration{}); elided != 1 {
		t.Fatalf("want the read elided and the instructions left, got %d elided", elided)
	}
	if got := a.Messages()[4].Content; got != instructions {
		t.Fatalf("a skill's instructions were elided out from under the child: %q", got)
	}
	placeholder := a.Messages()[3].Content
	m := regexp.MustCompile(`evidence (ev-[0-9a-f]{16})`).FindStringSubmatch(placeholder)
	if m == nil {
		t.Fatalf("the placeholder must name an id the child's evidence tool can read: %q", placeholder)
	}
	data, meta, err := store.Read(m[1], 0, len(original)+1)
	if err != nil {
		t.Fatalf("the elided original must be readable: %v", err)
	}
	if string(data) != original || meta.Tool != "read_file" {
		t.Fatalf("the store holds %d bytes filed under %q", len(data), meta.Tool)
	}
}

// A child whose session has no store and no skills is left exactly as it was:
// the trim still runs, because the request that provoked it still has to fit.
func TestNewChildAgent_NothingInstalledStillTrims(t *testing.T) {
	a := newChildAgent(Env{SystemPrompt: "sys"}, 10)
	a.SetMessages([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "the task"},
		{Role: provider.RoleTool, Content: strings.Repeat("x", 4000), ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "carry on"},
	})
	if elided, _ := a.TrimOldToolResults(70000, 60000, 40000, agent.Calibration{}); elided != 1 {
		t.Fatalf("want the result elided, got %d", elided)
	}
	if got := a.Messages()[2].Content; got != agent.ElidedResult {
		t.Fatalf("want the bare placeholder, got %q", got)
	}
}
