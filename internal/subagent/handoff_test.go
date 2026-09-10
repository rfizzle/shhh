package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHandoff_UsesOnlyPublicProgressAndOpaqueEvidence(t *testing.T) {
	c := &child{
		name: "researcher-1", role: RoleResearcher, task: "survey the parser", model: "test",
		maxTokens: 300000, wrote: map[string]bool{"parser.go": true}, progress: []string{"mapped the parser entry points"},
		transcript: []TranscriptEntry{
			{Kind: EntryAssistant, Text: "private model prose must not be copied"},
			{Kind: EntryTool, Tool: "read_file", Args: `{"path":"parser.go"}`, Result: "read 40 lines; evidence ev-1234567890abcdef"},
			{Kind: EntryTool, Tool: "search", Args: `{"path":"internal"}`, Result: "secret raw output"},
		},
	}
	h := c.makeHandoff("provider", "failed · provider unavailable", 3)
	if len(h.Progress) != 1 || h.Progress[0] != "mapped the parser entry points" {
		t.Fatalf("progress = %#v", h.Progress)
	}
	if len(h.ReadPaths) != 2 || h.ReadPaths[0] != "internal" || h.ReadPaths[1] != "parser.go" {
		t.Fatalf("read paths = %#v", h.ReadPaths)
	}
	if len(h.Evidence) != 1 || h.Evidence[0] != "ev-1234567890abcdef" {
		t.Fatalf("evidence = %#v", h.Evidence)
	}
	data, err := MarshalHandoff(h)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private model prose") || strings.Contains(string(data), "secret raw output") {
		t.Fatalf("handoff retained untrusted or private text: %s", data)
	}
}

func TestSpawnResumesASanitizedFailureHandoff(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "finished the unresolved action"}}}
	h := Handoff{
		Child: "failed-reader", Role: RoleResearcher, Task: "survey the parser", Budget: DefaultMaxTokens,
		RecommendedBudget: 600000, Failure: HandoffFailure{Category: "budget", Detail: "token budget exceeded"},
		LastRound: 2, ReadPaths: []string{"parser.go"}, Progress: []string{"mapped the parser entry points"},
		Evidence: []string{"ev-1234567890abcdef"},
	}
	data, err := MarshalHandoff(h)
	if err != nil {
		t.Fatal(err)
	}
	sup := New(context.Background(), Options{
		Root: t.TempDir(), NewEnv: env.factory(),
		LoadHandoff: func(handle string) ([]byte, error) {
			if handle != "handoff-1" {
				t.Fatalf("requested %q", handle)
			}
			return data, nil
		},
		EvidenceExists: func(handle string) bool { return handle == "ev-1234567890abcdef" },
	})
	t.Cleanup(sup.Close)
	out, err := sup.Spawn(json.RawMessage(`{"role":"writer","task":"ignored","resume_handoff":"handoff-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "resumes the verified handoff handoff-1") {
		t.Fatalf("spawn result = %q", out)
	}
	waitState(t, sup, "researcher-1", StateDone)
	opening := env.openingTurn()
	for _, want := range []string{"Do not repeat its repository survey", "parser.go", "mapped the parser entry points", "ev-1234567890abcdef", "survey the parser"} {
		if !strings.Contains(opening, want) {
			t.Fatalf("replacement did not receive %q:\n%s", want, opening)
		}
	}
	if strings.Contains(opening, "ignored") {
		t.Fatalf("replacement accepted a task outside the handoff: %s", opening)
	}
	st, ok := sup.Get("researcher-1")
	if !ok || st.Task != "survey the parser" || st.Budget != 600000 {
		t.Fatalf("replacement status = %+v", st)
	}
}

func TestFailedWriterKeepsItsPatchUntilSuperseded(t *testing.T) {
	c := &child{
		name: "writer-1", profile: Profile{Writes: true}, worktree: "/worktree", repoTop: "/repo",
		handoffID: "handoff-1", handoff: Handoff{Patch: "diff --git a/a b/a"}, retainWorktree: true,
	}
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: (&scriptedEnv{}).factory()})
	sup.children = []*child{c}
	if !c.keepsWorktree("/worktree") {
		t.Fatal("failed writer patch should keep its worktree")
	}
	sup.supersedeHandoff("handoff-1")
	if c.keepsWorktree("/worktree") {
		t.Fatal("replacement should release the superseded worktree")
	}
}

func TestResumePrologueDropsMissingEvidence(t *testing.T) {
	h := Handoff{Failure: HandoffFailure{Category: "provider"}, Evidence: []string{"ev-1234567890abcdef"}}
	got := resumePrologue(h, func(string) bool { return false })
	if strings.Contains(got, "ev-1234567890abcdef") {
		t.Fatalf("invalid evidence reached replacement: %s", got)
	}
}
