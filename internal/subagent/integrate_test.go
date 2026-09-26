package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
)

// The integration fixtures: two writers change the same line of mergeBase's
// one function to two different values, which no line merge can settle.
var (
	yIsOne  = strings.Replace(mergeBase, "var y = 0", "var y = 1", 1)
	yIsTwo  = strings.Replace(mergeBase, "var y = 0", "var y = 2", 1)
	yIsBoth = strings.Replace(mergeBase, "var y = 0", "var y = 1 + 2", 1)
)

// integrationScript is mergeFactory with what an integration test reads back:
// the spec each agent was built from and the text its first turn opened on.
type integrationScript struct {
	writers map[string]mergeWriter

	mu     sync.Mutex
	specs  map[string]Spec
	opened map[string]string
}

func (w *integrationScript) factory() EnvFactory {
	inner := mergeFactory(w.writers)
	return func(ctx context.Context, spec Spec) (Env, error) {
		env, err := inner(ctx, spec)
		if err != nil {
			return env, err
		}
		stream := env.Stream
		first := true
		env.Stream = func(msgs []provider.Message, model string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			if first {
				first = false
				var text strings.Builder
				for _, m := range msgs {
					if m.Role == provider.RoleUser {
						text.WriteString(m.Content + "\n")
					}
				}
				w.mu.Lock()
				w.opened[spec.Name] = text.String()
				w.mu.Unlock()
			}
			return stream(msgs, model)
		}
		w.mu.Lock()
		w.specs[spec.Name] = spec
		w.mu.Unlock()
		return env, nil
	}
}

func (w *integrationScript) openedWith(name string) (string, Spec) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.opened[name], w.specs[name]
}

// conflictingWriters is writer-1 setting y to 1 and landing while writer-2,
// which set it to 2 in a copy taken before, is answering — so writer-2's
// patch meets writer-1's on the same line — and the integration writer
// writing what integrate does into its own copy.
func conflictingWriters(repo string, integrate func(root string)) *integrationScript {
	landed := make(chan struct{})
	wrote1, wrote2 := make(chan struct{}), make(chan struct{})
	var once sync.Once
	return &integrationScript{
		specs:  map[string]Spec{},
		opened: map[string]string{},
		writers: map[string]mergeWriter{
			"writer-1": {
				write:     func(root string) { put(root, "main.go", yIsOne); close(wrote1) },
				answering: func() { <-wrote2 },
			},
			"writer-2": {
				write: func(root string) { <-wrote1; put(root, "main.go", yIsTwo) },
				answering: func() {
					close(wrote2)
					// writer-2 answers once writer-1's patch is on the
					// checkout, which is what makes its own conflict.
					for take(repo, "main.go") != yIsOne {
						time.Sleep(10 * time.Millisecond)
					}
					once.Do(func() { close(landed) })
				},
			},
			"integrator-1": {write: integrate},
		},
	}
}

// startConflict spawns the two writers and answers every card but the
// integration writer's, which it hands back; landings are counted by agent.
func startConflict(t *testing.T, repo string, w *integrationScript) (*Supervisor, chan *Ask, func() []string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sup := New(ctx, Options{Root: repo, NewEnv: w.factory()})
	t.Cleanup(sup.Close)
	t.Cleanup(cancel)
	cards := make(chan *Ask, 4)
	var mu sync.Mutex
	var landed []string
	go func() {
		for {
			select {
			case ev, ok := <-sup.Events():
				if !ok {
					return
				}
				switch {
				case ev.Kind == EventAsk && ev.Ask.Agent == "integrator-1":
					cards <- ev.Ask
				case ev.Kind == EventAsk && ev.Ask.Agent == "writer-2":
					t.Errorf("a conflicting patch should not be put on a card: %s", ev.Ask.Title)
					ev.Ask.Respond(false)
				case ev.Kind == EventAsk:
					ev.Ask.Respond(true)
				case ev.Kind == EventPatch:
					mu.Lock()
					landed = append(landed, ev.Patch.Agent)
					mu.Unlock()
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"y is 1","name":"writer-1"}`)
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"y is 2","name":"writer-2"}`)
	return sup, cards, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), landed...)
	}
}

// Two writers change the same function differently. The second's patch is
// kept, not landed with markers and not settled by picking a side, and an
// integration writer is started with both sides and the base in its task; its
// patch comes back on the ordinary card, lands once, and spends the kept one.
func TestAConflictIsHandedToAnIntegrationWriterWhosePatchLandsOnce(t *testing.T) {
	repo := mergeRepo(t)
	w := conflictingWriters(repo, func(root string) { put(root, "main.go", yIsBoth) })
	sup, cards, landed := startConflict(t, repo, w)

	waitState(t, sup, "writer-2", StateDone)
	if !transcriptHas(sup.Transcript("writer-2"), EntrySystem, "integrator-1 was started to reconcile the two") {
		t.Fatalf("writer-2's note should name the integration writer: %v", sup.Transcript("writer-2"))
	}
	var card *Ask
	select {
	case card = <-cards:
	case <-time.After(10 * time.Second):
		t.Fatal("the integration writer's patch never reached a card")
	}
	opened, spec := w.openedWith("integrator-1")
	for _, want := range []string{
		"Reconcile writer-2's patch with the workspace in main.go.",
		"> y is 2",
		"<<<<<<< the workspace", "var y = 1",
		"||||||| base", "var y = 0",
		">>>>>>> writer-2", "var y = 2",
	} {
		if !strings.Contains(opened, want) {
			t.Fatalf("the integration writer's first turn should carry %q:\n%s", want, opened)
		}
	}
	if spec.Integrates != "writer-2" || spec.Role != RoleWriter {
		t.Fatalf("the integration writer should be a writer told whose patch it reconciles, got %+v", spec)
	}
	if st := statusOf(t, sup, "integrator-1"); st.Batch != statusOf(t, sup, "writer-2").Batch {
		t.Fatal("the integration writer should join the round its writer was spawned in")
	}
	if len(card.Files) != 1 || card.Files[0] != "main.go" || !strings.Contains(hunkText(card.Hunks), "+var y = 1 + 2") {
		t.Fatalf("the card should be the reconciliation over the workspace:\n%s", hunkText(card.Hunks))
	}
	// Overwriting writer-1's change is the reconciliation's job, so the card
	// states it as a fact rather than warning of a clash.
	if len(card.Warnings) != 0 || card.Reconciles != "writer-1's change to main.go with writer-2's" {
		t.Fatalf("the card should name what it reconciles and warn of nothing, got %q, %q", card.Reconciles, card.Warnings)
	}
	if got := take(repo, "main.go"); got != yIsOne {
		t.Fatalf("nothing lands before the card is answered:\n%s", got)
	}
	card.Respond(true)
	waitState(t, sup, "integrator-1", StateDone)
	waitFor(t, func() bool { return !statusOf(t, sup, "writer-2").PatchKept })
	if got := take(repo, "main.go"); got != yIsBoth {
		t.Fatalf("the reconciliation should be what landed:\n%s", got)
	}
	if got := strings.Join(landed(), ","); got != "writer-1,integrator-1" {
		t.Fatalf("writer-1 and the reconciliation should each land once, got %s", got)
	}
	if !transcriptHas(sup.Transcript("writer-2"), EntrySystem, "its patch landed through integrator-1") {
		t.Fatal("writer-2's row should say its patch landed through the integration")
	}
}

// An integration writer's patch is not warned about the files it was handed:
// overwriting those is what it was started for, and the card says whose change
// it settles. A file outside that set is a clash like any other writer's.
func TestPatchClashesAnswersAnIntegrationsHandedFilesAsReconciled(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir()})
	t.Cleanup(sup.Close)
	sup.recordApplied("writer-1", []string{"loop.go", "mode.go"})
	integrator := &child{name: "integrator-1", integrates: &integration{source: "writer-2", conflicts: []string{"loop.go"}}}
	clashes, reconciled := sup.patchClashes(integrator, []string{"loop.go", "mode.go"})
	if got := strings.Join(clashes, "; "); got != "writer-1 (mode.go)" {
		t.Fatalf("a file outside the handed set keeps the warning, got %q", got)
	}
	if got := strings.Join(reconciled, "; "); got != "writer-1's change to loop.go" {
		t.Fatalf("the handed file should be reconciled, got %q", got)
	}
	writer := &child{name: "writer-3"}
	clashes, reconciled = sup.patchClashes(writer, []string{"loop.go"})
	if len(clashes) != 1 || len(reconciled) != 0 {
		t.Fatalf("an ordinary writer's patch keeps the warning, got %q, %q", clashes, reconciled)
	}
}

// An integration writer that cannot reconcile the two lands nothing: its
// patch still carries a conflict marker, so no card is raised, nothing is
// written with markers, and both patches stay kept with the file named.
func TestAnIntegrationThatDoesNotReconcileKeepsBothPatches(t *testing.T) {
	repo := mergeRepo(t)
	marked := strings.Replace(mergeBase, "var y = 0", "<<<<<<< the workspace\nvar y = 1\n=======\nvar y = 2\n>>>>>>> writer-2", 1)
	w := conflictingWriters(repo, func(root string) { put(root, "main.go", marked) })
	sup, cards, _ := startConflict(t, repo, w)

	waitState(t, sup, "integrator-1", StateDone)
	select {
	case card := <-cards:
		t.Fatalf("an unreconciled patch should not be put on a card: %s", card.Title)
	default:
	}
	if !statusOf(t, sup, "writer-2").PatchKept || !statusOf(t, sup, "integrator-1").PatchKept {
		t.Fatal("both patches should stay kept for the person")
	}
	if !transcriptHas(sup.Transcript("integrator-1"), EntrySystem, "integrator-1 did not reconcile main.go with the workspace; no files were changed, and both patches are kept") {
		t.Fatalf("the integration writer's note should say so: %v", sup.Transcript("integrator-1"))
	}
	if !transcriptHas(sup.Transcript("writer-2"), EntrySystem, "integrator-1 did not reconcile main.go") {
		t.Fatal("writer-2's row should be told too")
	}
	if got := take(repo, "main.go"); got != yIsOne {
		t.Fatalf("the checkout should be as writer-1 left it:\n%s", got)
	}
	// Neither kept patch starts another integration from its row: the person
	// is asked with the patch in hand.
	ask, err := sup.ReviewKept("writer-2")
	if err != nil || ask == nil {
		t.Fatalf("[p] on the source should put its patch on a card now, got %v", err)
	}
	if _, ok := sup.Get("integrator-2"); ok {
		t.Fatal("a second integration should not be started for a patch one already tried")
	}
}

// A patch kept for its conflict and reviewed from the row before any
// integration writer has tried it starts one instead of a card for a patch
// that cannot land; a kept patch that merges cleanly over the moved checkout
// is put on the card merged.
func TestAKeptPatchReviewedFromItsRowIsMergedOrIntegrated(t *testing.T) {
	repo := mergeRepo(t)
	h, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	put(h.root, "main.go", ours(mergeBase))
	patch, err := worktreePatch(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	w := &integrationScript{specs: map[string]Spec{}, opened: map[string]string{}, writers: map[string]mergeWriter{
		"integrator-1": {write: func(root string) {}},
	}}
	sup := New(context.Background(), Options{Root: repo, NewEnv: w.factory()})
	t.Cleanup(sup.Close)
	drainEvents(t, sup)
	c := &child{name: "writer-9", role: RoleWriter, profile: BuiltinProfiles()[RoleWriter], worktree: h.dir, repoTop: repo,
		done: make(chan struct{}), state: StateDone, spend: meter.New(nil)}
	c.keepWriterPatch(patch, nil)
	removeWorktree(h.repoTop, h.dir)
	sup.mu.Lock()
	sup.children = append(sup.children, c)
	sup.byName[c.name] = c
	sup.mu.Unlock()

	// The checkout moved beside the line: the kept patch merges cleanly.
	put(repo, "main.go", moved(mergeBase))
	ask, err := sup.ReviewKept("writer-9")
	if err != nil {
		t.Fatal(err)
	}
	if len(ask.Merged) != 1 || !strings.Contains(hunkText(ask.Hunks), " // C") {
		t.Fatalf("the card should be the kept patch merged over the moved checkout:\n%s", hunkText(ask.Hunks))
	}
	ask.Respond(false)
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.kept != nil && c.kept.review == nil
	})

	// The checkout moved on the same line: an integration writer is started.
	put(repo, "main.go", yIsOne)
	_, err = sup.ReviewKept("writer-9")
	var started *IntegrationStarted
	if !errors.As(err, &started) || started.Agent != "integrator-1" || strings.Join(started.Files, ",") != "main.go" {
		t.Fatalf("[p] on a conflicting kept patch should start an integration writer, got %v", err)
	}
	if _, ok := sup.Get("integrator-1"); !ok {
		t.Fatal("the integration writer should be on the roster")
	}
}

// Two writers may claim one file on purpose when both say so: the second is
// spawned beside the first rather than refused, its answer and its card say
// whose claim it shares, and one that allows it beside a writer that did not
// is refused as before, saying why.
func TestOverlapAllowedLetsTwoWritersClaimOneFile(t *testing.T) {
	repo := reseedRepo(t)
	w := newClaimWriters(repo)
	sup, _ := startClaimWriters(t, repo, w)
	t.Cleanup(func() { close(w.release) })

	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"edit main","name":"writer-1","paths":["main.go"],"overlap":"allowed"}`)
	second := `{"role":"writer","task":"edit main","name":"writer-2","paths":["main.go"],"overlap":"allowed"}`
	if holder, claim := sup.SharedClaim(json.RawMessage(second)); holder != "writer-1" || claim != "main.go" {
		t.Fatalf("the card should say whose claim it shares, got %q %q", holder, claim)
	}
	out := execTool(t, sup, SpawnToolName, second)
	if !strings.Contains(out, "It shares main.go with writer-1.") {
		t.Fatalf("the spawn's answer should say whose claim it shares:\n%s", out)
	}
	plan, err := SpawnPlan(nil, json.RawMessage(second))
	if err != nil || !strings.HasSuffix(plan.Scope, "claims main.go · overlap allowed") {
		t.Fatalf("the card's scope should say the claim may be shared, got %q (%v)", plan.Scope, err)
	}

	exec := sup.WrapExecutor("", func(string, json.RawMessage) (string, error) { return "", nil })
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"edit other","name":"writer-4","paths":["other.go"]}`)
	_, err = exec(SpawnToolName, json.RawMessage(`{"role":"writer","task":"edit other","name":"writer-5","paths":["other.go"],"overlap":"allowed"}`))
	if err == nil || !strings.Contains(err.Error(), "was not spawned with overlap: allowed") {
		t.Fatalf("overlap allowed on one side only should be refused, got %v", err)
	}
	_, err = exec(SpawnToolName, json.RawMessage(`{"role":"researcher","task":"look","overlap":"allowed"}`))
	if err == nil || !strings.Contains(err.Error(), "overlap applies to agents that change files") {
		t.Fatalf("overlap on a reader should be refused, got %v", err)
	}
	_, err = exec(SpawnToolName, json.RawMessage(`{"role":"writer","task":"look","overlap":"sometimes"}`))
	if err == nil || !strings.Contains(err.Error(), `overlap "sometimes" is not one of`) {
		t.Fatalf("an unknown overlap word should be refused, got %v", err)
	}
}
