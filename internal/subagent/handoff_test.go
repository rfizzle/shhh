package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
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
	recommended := recommendedBudget(DefaultMaxTokens, true)
	h := Handoff{
		Child: "failed-reader", Role: RoleResearcher, Task: "survey the parser", Budget: DefaultMaxTokens,
		RecommendedBudget: recommended, Failure: HandoffFailure{Category: "budget", Detail: "token budget exceeded"},
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
	if !ok || st.Task != "survey the parser" || st.Budget != recommended {
		t.Fatalf("replacement status = %+v", st)
	}
}

// keptWriter is a writer whose first round writes kept.go inside its own copy
// of the checkout. After that it either answers, so the patch goes to the
// card, or waits on its context, so a kill is what ends it. Archive stands in
// for the session's evidence store and remembers what it was handed.
type keptWriter struct {
	answer  bool
	writing chan struct{}

	mu       sync.Mutex
	round    int
	archived []string
}

func (w *keptWriter) stored() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.archived...)
}

func (w *keptWriter) factory() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			w.mu.Lock()
			w.round++
			round := w.round
			w.mu.Unlock()
			ch := make(chan provider.StreamEvent, 2)
			switch {
			case round == 1:
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{
					{ID: "w1", Name: "write_file", Arguments: `{"path":"kept.go"}`},
				}}
			case w.answer:
				ch <- provider.StreamEvent{Token: "wrote kept.go"}
				ch <- provider.StreamEvent{Done: true}
			default:
				close(w.writing)
				go func() {
					<-ctx.Done()
					close(ch)
				}()
				return ch, func() {}, nil
			}
			close(ch)
			return ch, func() {}, nil
		}
		return Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor: func(string, json.RawMessage) (string, error) {
				return "written", os.WriteFile(filepath.Join(spec.Root, "kept.go"), []byte("package kept\n"), 0o644)
			},
			Archive: func(tool, content string) (string, bool) {
				w.mu.Lock()
				defer w.mu.Unlock()
				if tool != keptPatchTool {
					return "", false
				}
				w.archived = append(w.archived, content)
				return fmt.Sprintf("ev-%016x", len(w.archived)), true
			},
		}, nil
	}
}

// A declined patch is kept in the evidence store rather than written to a
// file the note names, and the row's review is the same card and the same
// apply a finishing writer's patch goes through: the change lands only on a
// yes, and the session hears about it the way it hears about every patch.
// See docs/capabilities/subagents.md#a-failed-child-leaves-a-handoff.
func TestADeclinedPatchIsKeptAndReviewedFromItsRow(t *testing.T) {
	repo := initTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	w := &keptWriter{answer: true}
	sup := New(ctx, Options{Root: repo, NewEnv: w.factory()})
	t.Cleanup(sup.Close)
	t.Cleanup(cancel)
	landed := make(chan *PatchApplied, 1)
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				switch ev.Kind {
				case EventAsk:
					ev.Ask.Respond(false)
				case EventPatch:
					landed <- ev.Patch
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	if _, err := spawnRaw(sup, `{"role":"writer","task":"add kept.go"}`); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "writer-1", StateDone)
	stored := w.stored()
	if len(stored) != 1 || !strings.Contains(stored[0], "kept.go") {
		t.Fatalf("the declined patch should be in the evidence store, got %q", stored)
	}
	if st := statusOf(t, sup, "writer-1"); !st.PatchKept {
		t.Fatalf("the writer's status should say its patch is kept, got %+v", st)
	}
	report := execTool(t, sup, ReportToolName, `{"name":"writer-1"}`)
	if !strings.Contains(report, "kept as ev-0000000000000001") || strings.Contains(report, "saved to") {
		t.Fatalf("the parent should be told the handle the patch is kept under:\n%s", report)
	}
	if _, err := os.Stat(filepath.Join(repo, "kept.go")); err == nil {
		t.Fatal("a declined patch reached the checkout")
	}

	ask, err := sup.ReviewKept("writer-1")
	if err != nil {
		t.Fatal(err)
	}
	if ask.Kind != AskPatch || !slices.Contains(ask.Files, "kept.go") {
		t.Fatalf("the review should be the patch card over kept.go, got %+v", ask)
	}
	if again, _ := sup.ReviewKept("writer-1"); again != ask {
		t.Fatal("a second review of the same patch put a second card out")
	}
	// Declining the review leaves the patch kept and offered again.
	ask.Respond(false)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if next, err := sup.ReviewKept("writer-1"); err == nil && next != ask {
			ask = next
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a declined review took the kept patch with it")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !statusOf(t, sup, "writer-1").PatchKept {
		t.Fatal("a declined review stopped offering the patch")
	}
	ask.Respond(true)
	select {
	case p := <-landed:
		if p.Agent != "writer-1" || len(p.Files) != 1 {
			t.Fatalf("the applied patch should be recorded as writer-1's, got %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reviewed patch never landed")
	}
	if _, err := os.Stat(filepath.Join(repo, "kept.go")); err != nil {
		t.Fatalf("the approved patch did not reach the checkout: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for statusOf(t, sup, "writer-1").PatchKept {
		if time.Now().After(deadline) {
			t.Fatal("an applied patch is still offered for review")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := sup.ReviewKept("writer-1"); err == nil {
		t.Fatal("a patch that landed was offered again")
	}
}

// A killed writer used to lose what it had written with its copy of the
// checkout. Now the patch is kept before the copy goes, and the handoff names
// the handle rather than carrying the patch a second time.
func TestAKilledWriterKeepsItsPatchAndTheHandoffNamesIt(t *testing.T) {
	repo := initTestRepo(t)
	w := &keptWriter{writing: make(chan struct{})}
	sup := New(context.Background(), Options{Root: repo, NewEnv: w.factory()})
	t.Cleanup(sup.Close)

	if _, err := spawnRaw(sup, `{"role":"writer","task":"add kept.go"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.writing:
	case <-time.After(5 * time.Second):
		t.Fatal("the writer never reached its second round")
	}
	if !sup.PatchToKeep("writer-1") {
		t.Fatal("a writer whose copy holds a change should say a kill keeps it")
	}
	if err := sup.Kill("writer-1"); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "writer-1", StateFailed)
	if st := statusOf(t, sup, "writer-1"); !st.PatchKept {
		t.Fatalf("a killed writer's patch should be kept, got %+v", st)
	}
	if stored := w.stored(); len(stored) != 1 || !strings.Contains(stored[0], "kept.go") {
		t.Fatalf("the killed writer's patch should be in the evidence store, got %q", stored)
	}
	c, err := sup.lookup("writer-1")
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	h := c.handoff
	c.mu.Unlock()
	if h.PatchEvidence != "ev-0000000000000001" {
		t.Fatalf("the handoff should name the kept patch, got %q", h.PatchEvidence)
	}
	if data, _ := MarshalHandoff(h); strings.Contains(string(data), "package kept") {
		t.Fatalf("the handoff carries the patch as well as its handle: %s", data)
	}
	deadline := time.Now().Add(5 * time.Second)
	for linkedWorktrees(t, repo) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the killed writer's copy of the checkout was kept as well as its patch")
		}
		time.Sleep(10 * time.Millisecond)
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
