package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
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
	if c := childCompactor("a-private-build-of-our-own"); c != nil {
		t.Fatalf("a step was built against a window nothing could name: %+v", c)
	}
	c := childCompactor("phi")
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
