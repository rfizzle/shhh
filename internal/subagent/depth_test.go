package subagent

// Delegation past the first level: how deep it goes, what a descendant may
// be given, which model answers it, and the rule that keeps a tree of agents
// from waiting on itself.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// spawnFromAgent is one agent's own spawn call, dispatched through the chain
// its runtime is given rather than the session's — which is the only way a
// parent is written and a depth is counted.
func spawnFromAgent(sup *Supervisor, caller, args string) (string, error) {
	exec := sup.WrapExecutor(caller, func(name string, _ json.RawMessage) (string, error) {
		return "", fmt.Errorf("unexpected passthrough: %s", name)
	})
	return exec(SpawnToolName, json.RawMessage(args))
}

// TestDepth_TheDefaultIsThreeAndTheThirdLevelIsTheLast walks the default down
// its whole length: the session spawns, that child spawns, and the
// grandchild's own spawn is refused with the depth and the key on it.
func TestDepth_TheDefaultIsThreeAndTheThirdLevelIsTheLast(t *testing.T) {
	env := &scriptedEnv{steps: readRounds(40)}
	sup := newTestSupervisor(t, env)
	if sup.MaxDepth() != DefaultMaxDepth {
		t.Fatalf("an unconfigured supervisor delegates to %d, want %d", sup.MaxDepth(), DefaultMaxDepth)
	}

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the importer","name":"child"}`)
	if _, err := spawnFromAgent(sup, "child", `{"role":"researcher","task":"read the exporter","name":"grandchild"}`); err != nil {
		t.Fatalf("a child of the session must be able to delegate at the default depth: %v", err)
	}
	if parent, ok := sup.Parent("grandchild"); !ok || parent != "child" {
		t.Fatalf("the grandchild's parent is %q, want child", parent)
	}
	if parent, ok := sup.Parent("child"); !ok || parent != "" {
		t.Fatalf("a child of the session has parent %q, want the orchestrator's empty name", parent)
	}

	_, err := spawnFromAgent(sup, "grandchild", `{"role":"researcher","task":"one level too far"}`)
	if err == nil {
		t.Fatal("the third level must not delegate a fourth")
	}
	for _, want := range []string{"depth 3", "depth 4", MaxDepthKey} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal never says %q: %v", want, err)
		}
	}
}

// A configured depth of 2 is a session whose children may not delegate at
// all, which is what a person who wants the old behaviour back sets.
func TestDepth_AConfiguredTwoStopsAtTheFirstLevel(t *testing.T) {
	env := &scriptedEnv{steps: readRounds(20)}
	sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: env.factory(), MaxDepth: 2})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the importer","name":"child"}`)
	_, err := spawnFromAgent(sup, "child", `{"role":"researcher","task":"read the exporter"}`)
	if err == nil || !strings.Contains(err.Error(), "depth 2") {
		t.Fatalf("a child under max_depth 2 must not delegate: %v", err)
	}
}

// A spawn past the cap is refused where the token admission is refused:
// before anything has been built, opened or claimed for it.
func TestDepth_ARefusedSpawnClaimsNothing(t *testing.T) {
	var mu sync.Mutex
	specs := 0
	env := &scriptedEnv{steps: readRounds(20)}
	factory := env.factory()
	sup := New(t.Context(), Options{
		Root:     t.TempDir(),
		MaxDepth: 2,
		NewEnv: func(ctx context.Context, spec Spec) (Env, error) {
			mu.Lock()
			specs++
			mu.Unlock()
			return factory(ctx, spec)
		},
		Record: func(spec Spec, _ string) Recorder {
			if spec.Depth > 2 {
				t.Errorf("a refused spawn opened a record row at depth %d", spec.Depth)
			}
			return Recorder{}
		},
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the importer","name":"child"}`)
	mu.Lock()
	before := specs
	mu.Unlock()

	if _, err := spawnFromAgent(sup, "child", `{"role":"writer","task":"too deep","name":"grandchild"}`); err == nil {
		t.Fatal("the spawn past the cap must be refused")
	}
	mu.Lock()
	after := specs
	mu.Unlock()
	if after != before {
		t.Errorf("a refused spawn built %d environments; the depth is checked before any of that", after-before)
	}
	if _, ok := sup.Get("grandchild"); ok {
		t.Error("a refused spawn left a live child behind")
	}
	for _, st := range sup.Snapshot() {
		if st.Name == "grandchild" {
			t.Error("a refused spawn is on the roster")
		}
	}
}

// The model each depth answers on, with the role's own entry outranking it
// and the spawn call outranking both.
func TestDepth_TheModelPrecedenceRunsCallThenRoleThenDepth(t *testing.T) {
	byDepth := map[int]string{2: "depth-two-model", 3: "depth-three-model"}
	withProfile := map[Role]string{RoleReviewer: "reviewer-model"}

	env := &scriptedEnv{steps: readRounds(60)}
	sup := New(t.Context(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		ModelFor: func(role Role, depth int, requested string) string {
			// The layering the CLI builds, stated here as the contract the
			// supervisor asks for: the call, then the role, then the depth.
			if requested != "" {
				return requested
			}
			if m, ok := withProfile[role]; ok {
				return m
			}
			return byDepth[depth]
		},
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"two"}`)
	execTool(t, sup, SpawnToolName, `{"role":"reviewer","task":"b","name":"two-with-profile","paths":["a.go"]}`)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"c","name":"two-asked","model":"asked-for"}`)
	if _, err := spawnFromAgent(sup, "two", `{"role":"researcher","task":"d","name":"three"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := spawnFromAgent(sup, "two", `{"role":"reviewer","task":"e","name":"three-with-profile","paths":["b.go"]}`); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, want string }{
		{"two", "depth-two-model"},
		{"three", "depth-three-model"},
		// A profile with a model takes it at every depth: the role is above
		// the depth, so the depth's default never reaches it.
		{"two-with-profile", "reviewer-model"},
		{"three-with-profile", "reviewer-model"},
		{"two-asked", "asked-for"},
	} {
		if got := statusOf(t, sup, tc.name).Model; got != tc.want {
			t.Errorf("%s runs on %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A depth with no entry of its own inherits, which is what keeps a config
// that has never heard of depth behaving exactly as it did.
func TestDepth_ADepthWithNoEntryInherits(t *testing.T) {
	env := &scriptedEnv{steps: readRounds(20)}
	sup := New(t.Context(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		ModelFor: func(_ Role, depth int, requested string) string {
			if requested != "" {
				return requested
			}
			if depth == 2 {
				return "children-run-here"
			}
			return "the-session-model"
		},
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"two"}`)
	if _, err := spawnFromAgent(sup, "two", `{"role":"researcher","task":"b","name":"three"}`); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, sup, "three").Model; got != "the-session-model" {
		t.Errorf("a depth nobody configured runs on %q, want the session's", got)
	}
}

// An agent that changes nothing may delegate an agent that changes nothing,
// and nothing more. The refusal is at the spawn rather than at the
// descendant's first write, so no slot is spent on an agent that could never
// have done the job.
func TestDepth_AReadOnlyAgentsDescendantIsReadOnly(t *testing.T) {
	env := &scriptedEnv{steps: readRounds(20)}
	sup := newTestSupervisor(t, env)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","name":"reader"}`)
	_, err := spawnFromAgent(sup, "reader", `{"role":"writer","task":"change the loop","name":"nope"}`)
	if err == nil {
		t.Fatal("a read-only agent must not delegate a writer")
	}
	if !strings.Contains(err.Error(), "reader") || !strings.Contains(err.Error(), "writes") {
		t.Errorf("the refusal names neither the agent nor what it cannot pass on: %v", err)
	}
	if _, ok := sup.Get("nope"); ok {
		t.Error("the refused writer was started anyway")
	}
	// The reading half of the same rule: what it may delegate, it does.
	if _, err := spawnFromAgent(sup, "reader", `{"role":"researcher","task":"read the exporter","name":"fine"}`); err != nil {
		t.Fatalf("a read-only agent must be able to delegate a read-only one: %v", err)
	}
}

// A descendant starts no looser than the agent that spawned it: a child put
// in plan mode cannot delegate its way out of plan mode.
func TestDepth_ADescendantStartsNoLooserThanItsSpawner(t *testing.T) {
	env := &scriptedEnv{steps: readRounds(20)}
	sup := newTestSupervisor(t, env)
	sup.SetParentMode(agent.ModeAuto)

	execTool(t, sup, SpawnToolName, `{"role":"reviewer","task":"judge it","name":"planner","paths":["a.go"]}`)
	planned, ok := sup.AgentMode("planner")
	if !ok || planned != agent.ModePlan {
		t.Fatalf("the reviewer profile starts in %v, want plan", planned)
	}
	if _, err := spawnFromAgent(sup, "planner", `{"role":"researcher","task":"look","name":"under"}`); err != nil {
		t.Fatal(err)
	}
	under, ok := sup.AgentMode("under")
	if !ok || under != agent.ModePlan {
		t.Fatalf("a descendant of a plan-mode agent starts in %v, want plan", under)
	}
}

// An agent's orchestration tools reach what it spawned and nothing else.
// This is the half of the deadlock rule that makes the wait graph a tree: two
// agents that could wait on each other could never both finish.
func TestDepth_AnAgentReachesOnlyWhatItSpawned(t *testing.T) {
	env := &scriptedEnv{steps: readRounds(60)}
	sup := newTestSupervisor(t, env)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"one"}`)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"b","name":"two"}`)
	if _, err := spawnFromAgent(sup, "one", `{"role":"researcher","task":"c","name":"one-a"}`); err != nil {
		t.Fatal(err)
	}

	exec := func(caller, tool, args string) error {
		e := sup.WrapExecutor(caller, func(string, json.RawMessage) (string, error) {
			return "", errors.New("unexpected passthrough")
		})
		_, err := e(tool, json.RawMessage(args))
		return err
	}
	if err := exec("one", ReportToolName, `{"name":"two","wait":false}`); err == nil {
		t.Error("an agent must not report on its sibling")
	}
	if err := exec("one", SteerToolName, `{"name":"two","message":"stop"}`); err == nil {
		t.Error("an agent must not steer its sibling")
	}
	if err := exec("one-a", ReportToolName, `{"name":"one","wait":false}`); err == nil {
		t.Error("an agent must not report on its own parent")
	}
	if err := exec("one", ReportToolName, `{"name":"one-a","wait":false}`); err != nil {
		t.Errorf("an agent must reach what it spawned: %v", err)
	}
	// The session is above everything and reaches all of it.
	if err := exec("", ReportToolName, `{"name":"one-a","wait":false}`); err != nil {
		t.Errorf("the session must reach a grandchild: %v", err)
	}
	// And the roster an agent is given is its own subtree.
	roster := execTool(t, sup, ReportToolName, `{}`)
	if !strings.Contains(roster, "two") {
		t.Error("the session's roster is missing one of its children")
	}
	child := sup.WrapExecutor("one", func(string, json.RawMessage) (string, error) {
		return "", errors.New("unexpected passthrough")
	})
	own, err := child(ReportToolName, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(own, "one-a") {
		t.Error("an agent's roster is missing the agent it spawned")
	}
	if strings.Contains(own, "\ntwo (") {
		t.Errorf("an agent's roster lists its sibling:\n%s", own)
	}
}

// The deadlock the rule exists for: every concurrent slot at the first level
// held by an agent that is blocked in agent_report waiting for a descendant
// which has not started yet. With one pool of slots for the whole session
// this never unwinds; with a pool per depth the grandchildren run, the
// waiters collect them, and the turn ends.
//
// The children are wired the way the CLI wires one — their own executor
// carries the delegation wrap under their own name — because the wait that
// deadlocks is a tool call made from inside the child's loop, while the child
// is holding its slot. A wait made from beside the supervisor holds no slot
// and would let this test pass with the bug still in.
func TestDepth_ThreeWaitingAgentsAndAQueuedGrandchildAllFinish(t *testing.T) {
	const width = DefaultMaxConcurrent

	scripts := map[string][]streamStep{}
	var kids []string
	for i := range width {
		parent := fmt.Sprintf("part-%d", i)
		kid := parent + "-a"
		kids = append(kids, parent, kid)
		scripts[parent] = []streamStep{
			{calls: []provider.ToolCall{{ID: "s", Name: SpawnToolName,
				Arguments: fmt.Sprintf(`{"role":"researcher","task":"piece of %s","name":%q}`, parent, kid)}}},
			{calls: []provider.ToolCall{{ID: "w", Name: ReportToolName,
				Arguments: fmt.Sprintf(`{"name":%q}`, kid)}}},
			{text: "collected " + kid},
		}
		scripts[kid] = []streamStep{{text: "the piece is done"}}
	}

	var sup *Supervisor
	sup = New(t.Context(), Options{
		Root: t.TempDir(),
		NewEnv: perAgentEnv(scripts, func(name string, base agent.ToolExecutor) agent.ToolExecutor {
			return sup.WrapExecutor(name, base)
		}),
	})
	t.Cleanup(sup.Close)

	for i := range width {
		execTool(t, sup, SpawnToolName,
			fmt.Sprintf(`{"role":"researcher","task":"part %d","name":"part-%d"}`, i, i))
	}
	// Each one's first round is its spawn and its second is the wait, so by
	// the time any grandchild is looking for a slot every first-level slot is
	// held by an agent that will not release it until that grandchild has
	// answered. That is the arrangement, and it needs no synchronising here:
	// the scripts put every agent in it.
	deadline := time.Now().Add(20 * time.Second)
	for _, name := range kids {
		for {
			st, ok := sup.Get(name)
			if ok && st.State == StateDone {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never finished (last: %v) — three agents each waiting on a queued "+
					"descendant is the deadlock the per-depth slots prevent", name, st.State)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// A second attempt is the same agent in the same place: it keeps its parent
// and its depth, so it draws from the pool its first attempt drew from. A
// retry that lost its depth would queue at the session's level, behind the
// very agents that are waiting for it.
func TestDepth_ARetriedAgentKeepsItsPlaceInTheTree(t *testing.T) {
	var mu sync.Mutex
	var specs []Spec
	var sup *Supervisor
	base := perAgentEnv(map[string][]streamStep{
		"two": {{text: "the parent has answered"}},
		"three": {
			{fail: &provider.Failure{Message: "the provider refused"}},
			{text: "the second attempt answered"},
		},
	}, func(name string, next agent.ToolExecutor) agent.ToolExecutor {
		return sup.WrapExecutor(name, next)
	})
	sup = New(t.Context(), Options{
		Root: t.TempDir(),
		NewEnv: func(ctx context.Context, spec Spec) (Env, error) {
			mu.Lock()
			specs = append(specs, spec)
			mu.Unlock()
			return base(ctx, spec)
		},
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"two"}`)
	if _, err := spawnFromAgent(sup, "two", `{"role":"researcher","task":"b","name":"three"}`); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "three", StateFailed)
	if err := sup.Retry("three"); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "three", StateDone)

	mu.Lock()
	defer mu.Unlock()
	seen := 0
	for _, spec := range specs {
		if spec.Name != "three" {
			continue
		}
		seen++
		if spec.Depth != 3 || spec.Parent != "two" {
			t.Errorf("an attempt of the grandchild was built at depth %d under %q, want 3 under two",
				spec.Depth, spec.Parent)
		}
	}
	if seen < 2 {
		t.Fatalf("the retry built %d environments for the grandchild, want the first attempt's and the second's", seen)
	}
	if got := sup.depthOf("three"); got != 3 {
		t.Errorf("after the retry the grandchild sits at depth %d, want 3", got)
	}
}

// A killed agent comes out of the wait it is in. An agent blocked in
// agent_report waits on the agent below it, and the kill cancels the waiter's
// own context and nothing else — so a wait that watched only the agent below
// would leave a killed agent inside its tool call, holding its slot and its
// goroutine, until a descendant that may itself be waiting on a person
// happened to finish.
func TestDepth_AKilledAgentComesOutOfItsWaitOnADescendant(t *testing.T) {
	// The grandchild never answers: its stream blocks on the child context
	// until something cancels it, which is exactly the descendant a killed
	// waiter must not be held by.
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	scripts := map[string][]streamStep{
		"parent": {
			{calls: []provider.ToolCall{{ID: "s", Name: SpawnToolName,
				Arguments: `{"role":"researcher","task":"a piece","name":"kid"}`}}},
			{calls: []provider.ToolCall{{ID: "w", Name: ReportToolName, Arguments: `{"name":"kid"}`}}},
			{text: "collected"},
		},
	}
	var sup *Supervisor
	base := perAgentEnv(scripts, func(name string, next agent.ToolExecutor) agent.ToolExecutor {
		return sup.WrapExecutor(name, next)
	})
	sup = New(t.Context(), Options{
		Root: t.TempDir(),
		NewEnv: func(ctx context.Context, spec Spec) (Env, error) {
			if spec.Name != "kid" {
				return base(ctx, spec)
			}
			env, err := base(ctx, spec)
			if err != nil {
				return env, err
			}
			env.Stream = func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				select {
				case <-ctx.Done():
				case <-blocked:
				}
				return nil, nil, ctx.Err()
			}
			return env, nil
		},
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"the whole job","name":"parent"}`)
	// Wait until the parent is actually inside the wait: it has spawned the
	// grandchild and has nothing else to do.
	waitFor(t, func() bool {
		_, ok := sup.Get("kid")
		return ok
	})

	if err := sup.Kill("parent"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, ok := sup.Get("parent")
		if ok && (st.State == StateFailed || st.State == StateDone) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a killed agent is still %v — it is held inside agent_report by a descendant "+
				"that is not going to answer", st.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// perAgentEnv is a factory whose children each replay their own script, chosen
// by name, with their own executor wrapped by wrap. The shared scriptedEnv pops
// one queue for every child, which is right for a test about a race and wrong
// for one about who is waiting for whom.
func perAgentEnv(scripts map[string][]streamStep, wrap func(string, agent.ToolExecutor) agent.ToolExecutor) EnvFactory {
	var mu sync.Mutex
	left := map[string][]streamStep{}
	for name, steps := range scripts {
		left[name] = append([]streamStep(nil), steps...)
	}
	return func(ctx context.Context, spec Spec) (Env, error) {
		name := spec.Name
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			mu.Lock()
			steps := left[name]
			if len(steps) == 0 {
				mu.Unlock()
				return nil, nil, fmt.Errorf("no script left for %s", name)
			}
			step := steps[0]
			left[name] = steps[1:]
			mu.Unlock()
			if step.fail != nil {
				return nil, nil, step.fail
			}
			ch := make(chan provider.StreamEvent, 2)
			if step.text != "" {
				ch <- provider.StreamEvent{Token: step.text}
			}
			if len(step.calls) > 0 {
				ch <- provider.StreamEvent{ToolCalls: step.calls}
			} else {
				ch <- provider.StreamEvent{Done: true}
			}
			close(ch)
			_, cancel := context.WithCancel(context.Background())
			return ch, cancel, nil
		}
		base := agent.ToolExecutor(func(tool string, _ json.RawMessage) (string, error) {
			return "auto:" + tool, nil
		})
		return Env{
			SystemPrompt: "test system prompt",
			Stream:       stream,
			Executor:     wrap(name, base),
			ExecuteGated: wrap(name, base),
		}, nil
	}
}

// The slots themselves: one pool per depth, each the configured width.
func TestDepth_SlotsAreHeldPerDepth(t *testing.T) {
	sup := New(t.Context(), Options{Root: t.TempDir(),
		NewEnv: (&scriptedEnv{}).factory(), MaxConcurrent: 2})
	t.Cleanup(sup.Close)

	two, three := sup.slots(2), sup.slots(3)
	if two == three {
		t.Fatal("two depths must not share one set of slots")
	}
	if cap(two) != 2 || cap(three) != 2 {
		t.Fatalf("each depth's pool is the configured width: %d and %d", cap(two), cap(three))
	}
	if again := sup.slots(2); again != two {
		t.Fatal("a depth's pool must be the same one every time it is asked for")
	}
}

// The spec a child's runtime is built from carries where it sits, because
// what the runtime hands it turns on that: the delegation tools go on an
// agent with a level below it and not on one without.
func TestDepth_TheSpecCarriesTheParentAndTheDepth(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]Spec{}
	env := &scriptedEnv{steps: readRounds(20)}
	factory := env.factory()
	sup := New(t.Context(), Options{
		Root: t.TempDir(),
		NewEnv: func(ctx context.Context, spec Spec) (Env, error) {
			mu.Lock()
			if _, ok := seen[spec.Name]; !ok {
				seen[spec.Name] = spec
			}
			mu.Unlock()
			return factory(ctx, spec)
		},
	})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"two"}`)
	if _, err := spawnFromAgent(sup, "two", `{"role":"researcher","task":"b","name":"three"}`); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := seen["two"]; got.Depth != 2 || got.Parent != "" {
		t.Errorf("a child of the session is spec{parent:%q depth:%d}, want {\"\" 2}", got.Parent, got.Depth)
	}
	if got := seen["three"]; got.Depth != 3 || got.Parent != "two" {
		t.Errorf("a grandchild is spec{parent:%q depth:%d}, want {\"two\" 3}", got.Parent, got.Depth)
	}
}

// A supervisor that never sees a delegated spawn behaves as it always did:
// one level, one pool, no parent on anything.
func TestDepth_ASessionThatNeverDelegatesIsUnchanged(t *testing.T) {
	env := &scriptedEnv{steps: readRounds(20)}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"only"}`)
	if parent, ok := sup.Parent("only"); !ok || parent != "" {
		t.Fatalf("parent of a session's own child = %q", parent)
	}
	if got := sup.depthOf(""); got != SessionDepth {
		t.Fatalf("the session sits at depth %d, want %d", got, SessionDepth)
	}
	if got := sup.depthOf("only"); got != 2 {
		t.Fatalf("a child of the session sits at depth %d, want 2", got)
	}
}

// A kill takes the subtree, and the agent named goes last. The grandchild here
// is blocked on an approval nobody is going to answer, which is the state a
// cascade that reached only the agent named would leave running forever: its
// parent is gone, the copy of the checkout it was reading is about to be
// discarded, and the request on the person's card is for work that no longer
// has anywhere to land.
//
// What the assertions are for: the approval comes out of its wait (nothing is
// left blocked), both agents' rows close, the slots at both depths come back,
// and the writer's worktree outlives the agent that was reading it.
// See docs/capabilities/subagents.md#what-nesting-does-to-the-rest-of-it.
func TestDepth_AKillTakesTheSubtreeAndTheWorktreeGoesLast(t *testing.T) {
	repo := initTestRepo(t)
	scripts := map[string][]streamStep{
		"writer-1": {
			{calls: []provider.ToolCall{{ID: "s", Name: SpawnToolName,
				Arguments: `{"role":"researcher","task":"read the change","name":"reader"}`}}},
			{calls: []provider.ToolCall{{ID: "w", Name: ReportToolName, Arguments: `{"name":"reader"}`}}},
			{text: "collected"},
		},
		"reader": {
			{calls: []provider.ToolCall{{ID: "c", Name: tools.ExecCommandName,
				Arguments: `{"command":"go test ./..."}`}}},
			{text: "read it"},
		},
	}
	var sup *Supervisor
	base := perAgentEnv(scripts, func(name string, next agent.ToolExecutor) agent.ToolExecutor {
		return sup.WrapExecutor(name, next)
	})

	// The reader is held inside its own teardown, between the row it closes
	// and the done channel it closes last, so the test can look at the
	// writer's worktree at the one moment the ordering is about.
	ending, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var ends []observe.ChildEnd
	sup = New(t.Context(), Options{
		Root: repo,
		NewEnv: func(ctx context.Context, spec Spec) (Env, error) {
			env, err := base(ctx, spec)
			if err != nil {
				return env, err
			}
			env.Gated = map[string]bool{tools.ExecCommandName: true}
			env.RunCommand = func(context.Context, string) (string, int) { return "", 0 }
			return env, nil
		},
		Record: func(spec Spec, _ string) Recorder {
			name := spec.Name
			return Recorder{End: func(e observe.ChildEnd) {
				mu.Lock()
				ends = append(ends, e)
				mu.Unlock()
				if name == "reader" {
					close(ending)
					<-release
				}
			}}
		},
	})
	released := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() { released(); sup.Close() })

	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"add the exporter","name":"writer-1"}`)
	// The reader has to be on the card before the kill: a grandchild that had
	// not asked yet would be cancelled out of a wait it was never in.
	waitFor(t, func() bool {
		st, ok := sup.Get("reader")
		return ok && st.State == StateBlocked
	})
	worktree := worktreeOf(sup, "writer-1")
	if worktree == "" {
		t.Fatal("the writer never opened a worktree, so there is nothing to order the removal of")
	}
	writerDone := doneOf(sup, "writer-1")

	if err := sup.Kill("writer-1"); err != nil {
		t.Fatal(err)
	}

	<-ending
	// The writer has said how it ended, so everything left of it is teardown —
	// and that teardown is inside the wait for the reader, which is why the
	// copy of the checkout the reader was given is still there.
	waitState(t, sup, "writer-1", StateFailed)
	select {
	case <-writerDone:
		t.Fatal("the killed writer finished tearing down while the agent under it was still ending")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("the killed writer's worktree went before its subtree had ended: %v", err)
	}
	released()

	waitState(t, sup, "reader", StateFailed)
	st, _ := sup.Get("reader")
	if st.End != observe.ChildCancelled {
		t.Errorf("the agent under the killed one ended as %q, want %q — nobody ended it, its parent did",
			st.End, observe.ChildCancelled)
	}
	if !strings.Contains(st.Detail, "writer-1 was killed") {
		t.Errorf("the lane says %q, and never says whose kill this agent went with", st.Detail)
	}
	if killed, _ := sup.Get("writer-1"); killed.End != observe.ChildKilled {
		t.Errorf("the agent the person named ended as %q, want %q", killed.End, observe.ChildKilled)
	}

	// Nothing is left holding an approval, a slot or a copy of the checkout.
	waitFor(t, func() bool {
		active, blocked := sup.ActiveCounts()
		return active == 0 && blocked == 0
	})
	waitFor(t, func() bool { return len(sup.slots(2)) == 0 && len(sup.slots(3)) == 0 })
	waitFor(t, func() bool { _, err := os.Stat(worktree); return os.IsNotExist(err) })
	mu.Lock()
	defer mu.Unlock()
	if len(ends) != 2 {
		t.Fatalf("the kill closed %d rows, want one for each agent in the subtree", len(ends))
	}
}

// What the confirm counts before it asks: the live agents under the one being
// killed, deepest first, and none for a leaf — whose confirm is the one it
// always was.
func TestDepth_UnderNamesTheLiveAgentsAKillWouldTake(t *testing.T) {
	sup := New(t.Context(), Options{Root: t.TempDir(), MaxDepth: 4,
		NewEnv: (&scriptedEnv{steps: toolRounds(200), delay: 5 * time.Millisecond}).factory()})
	t.Cleanup(sup.Close)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"the whole job","name":"top"}`)
	if _, err := spawnFromAgent(sup, "top", `{"role":"researcher","task":"a piece","name":"mid"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := spawnFromAgent(sup, "mid", `{"role":"researcher","task":"a detail","name":"low"}`); err != nil {
		t.Fatal(err)
	}

	if got := sup.Under("low"); len(got) != 0 {
		t.Errorf("a leaf has %v under it, want none", got)
	}
	// Deepest first, which is the order the kill ends them in.
	if got := sup.Under("top"); !slices.Equal(got, []string{"low", "mid"}) {
		t.Errorf("the agents under the root child are %v, want [low mid]", got)
	}
	if got := sup.Under("mid"); !slices.Equal(got, []string{"low"}) {
		t.Errorf("the agents under the middle one are %v, want [low]", got)
	}

	// A finished agent is not one a kill would take.
	if err := sup.Kill("low"); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "low", StateFailed)
	if got := sup.Under("top"); !slices.Equal(got, []string{"mid"}) {
		t.Errorf("the agents under the root child are %v once one has ended, want [mid]", got)
	}
}

// worktreeOf and doneOf reach past the public surface for the two things an
// ordering test has to watch and no caller outside the package ever needs: the
// directory an attempt is working in, and the channel its goroutine closes
// last of all.
func worktreeOf(s *Supervisor, name string) string {
	s.mu.Lock()
	c := s.byName[name]
	s.mu.Unlock()
	if c == nil {
		return ""
	}
	worktree, _ := c.workspace()
	return worktree
}

func doneOf(s *Supervisor, name string) <-chan struct{} {
	s.mu.Lock()
	c := s.byName[name]
	s.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done
}
