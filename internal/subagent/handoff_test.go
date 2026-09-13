package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
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

// A resume opens on the handoff's bounded context before it opens on its
// task, so that context is part of what the budget has to carry. The floor a
// resume is admitted against states it.
func TestResumeAdmissionCountsTheHandoffPrologue(t *testing.T) {
	read := handoffReadPaths(400)
	data := marshalTestHandoff(t, Handoff{
		Child: "failed-reader", Role: RoleResearcher, Task: "survey the parser", Budget: DefaultMaxTokens,
		Failure:   HandoffFailure{Category: "budget", Detail: "token budget exceeded"},
		LastRound: 2, ReadPaths: read,
	})
	env := &scriptedEnv{steps: []streamStep{{text: "surveyed"}, {text: "carried on"}}}
	sup := New(context.Background(), Options{
		Root: t.TempDir(), NewEnv: env.factory(),
		LoadHandoff: func(string) ([]byte, error) { return data, nil },
	})
	t.Cleanup(sup.Close)

	// The same task and budget without a handoff, so the difference between
	// the two floors is the handoff and nothing else.
	if _, err := sup.Spawn(json.RawMessage(`{"role":"researcher","task":"survey the parser","max_tokens":300000}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := sup.Spawn(json.RawMessage(`{"role":"researcher","task":"ignored","max_tokens":300000,"resume_handoff":"handoff-1"}`)); err != nil {
		t.Fatalf("a budget that carries the prologue must admit the resume: %v", err)
	}
	plain, ok := sup.Get("researcher-1")
	resumed, resumedOK := sup.Get("researcher-2")
	if !ok || !resumedOK {
		t.Fatalf("both children should exist: %v %v", ok, resumedOK)
	}
	grew := resumed.AdmissionFloor - plain.AdmissionFloor
	if want := agent.EstimateTokens(strings.Join(read, ", ")); grew < want {
		t.Fatalf("the resume's floor grew by %d over the plain spawn's, want at least the %d tokens of handoff context it opens on", grew, want)
	}
}

// And a budget the prologue plus the working reserve cannot fit is refused
// where every other doomed budget is: before the replacement holds anything.
func TestResumeTooSmallForItsPrologueIsRefused(t *testing.T) {
	repo := initTestRepo(t)
	read := handoffReadPaths(2_000)
	data := marshalTestHandoff(t, Handoff{
		Child: "failed-writer", Role: RoleWriter, Task: "rewrite the parser", Budget: DefaultMaxTokens,
		Failure:   HandoffFailure{Category: "provider", Detail: "provider unavailable"},
		LastRound: 3, ReadPaths: read,
	})
	env := &scriptedEnv{steps: []streamStep{{text: "unreachable"}}}
	sup := New(context.Background(), Options{
		Root: repo, NewEnv: env.factory(),
		LoadHandoff: func(string) ([]byte, error) { return data, nil },
		Record:      func(Spec, string) Recorder { t.Fatal("a refused resume opened a record"); return Recorder{} },
	})
	t.Cleanup(sup.Close)

	// A budget that clears the inherited prompt, the task and the reserve, and
	// is short only by what the handoff adds.
	_, err := sup.Spawn(json.RawMessage(`{"role":"writer","task":"ignored","max_tokens":210000,"resume_handoff":"handoff-1"}`))
	if err == nil || !strings.Contains(err.Error(), "cannot admit") {
		t.Fatalf("undersized resume error = %v, want a refusal", err)
	}
	required := requiredMinimum(t, err)
	if floor := MinChildMaxTokens + agent.EstimateTokens(strings.Join(read, ", ")); required < floor {
		t.Fatalf("the refusal requires %d, want at least the %d the prologue and the reserve need", required, floor)
	}
	if children := sup.Snapshot(); len(children) != 0 {
		t.Fatalf("a refused resume claimed a child slot: %+v", children)
	}
	out, listErr := runGit(repo, "worktree", "list")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n") + 1; lines != 1 {
		t.Fatalf("a refused resume left a worktree behind:\n%s", out)
	}
}

// handoffReadPaths is the survey a failed child hands over: the bulk of a
// resume's bounded context, and what makes a prologue worth admitting for.
func handoffReadPaths(n int) []string {
	read := make([]string, n)
	for i := range read {
		read[i] = fmt.Sprintf("internal/parser/section%04d/parse.go", i)
	}
	return read
}

func marshalTestHandoff(t *testing.T, h Handoff) []byte {
	t.Helper()
	data, err := MarshalHandoff(h)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

var admissionMinimum = regexp.MustCompile(`at least (\d+)`)

// requiredMinimum is the floor a refusal states, so a test can say the
// refusal was about the size of the budget rather than about anything else.
func requiredMinimum(t *testing.T, err error) int64 {
	t.Helper()
	m := admissionMinimum.FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("refusal did not state the budget it required: %v", err)
	}
	n, convErr := strconv.ParseInt(m[1], 10, 64)
	if convErr != nil {
		t.Fatal(convErr)
	}
	return n
}
