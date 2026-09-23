package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// parentTurns is a parent's conversation two turns long, the second of which
// read a file whose contents are large enough to be kept rather than dropped,
// and quoted a secret.
func parentTurns() []provider.Message {
	body := "package loop\n" + strings.Repeat("// the round counter is reset at every turn\n", 40)
	return []provider.Message{
		{Role: provider.RoleSystem, Content: "the parent's system prompt"},
		{Role: provider.RoleUser, Content: "first ask: survey the exporter"},
		{Role: provider.RoleAssistant, Content: "the exporter writes CSV"},
		{Role: provider.RoleUser, Content: "second ask: why does the round cap reset? the token is hunter2"},
		{Role: provider.RoleAssistant, Content: "reading the loop", ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "read_file", Arguments: `{"path":"internal/agent/loop.go"}`}}},
		{Role: provider.RoleTool, ToolCallID: "c1", Content: body},
		{Role: provider.RoleUser, Content: "a gate verdict the session wrote", Machine: true},
		{Role: provider.RoleAssistant, Content: "the counter is zeroed in StartTurn"},
	}
}

// inheritingEnv is a scripted child whose environment carries a scrub and a
// store, as a session's does, and records what each spec said it inherited.
type inheritingEnv struct {
	*scriptedEnv
	mu       sync.Mutex
	inherits []int
	kept     []string
}

func (e *inheritingEnv) factory() EnvFactory {
	inner := e.scriptedEnv.factory()
	return func(ctx context.Context, spec Spec) (Env, error) {
		env, err := inner(ctx, spec)
		e.mu.Lock()
		e.inherits = append(e.inherits, spec.Inherit)
		e.mu.Unlock()
		env.Scrub = func(m provider.Message) provider.Message {
			m.Content = strings.ReplaceAll(m.Content, "hunter2", "[secret:TOKEN]")
			return m
		}
		env.Archive = func(tool, content string) (string, bool) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.kept = append(e.kept, tool+":"+content)
			return "ev-1234567890abcdef", true
		}
		return env, err
	}
}

func (e *inheritingEnv) keptCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.kept)
}

func newInheritingSupervisor(t *testing.T, env *inheritingEnv, profiles Profiles) *Supervisor {
	t.Helper()
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: env.factory(), Profiles: profiles})
	t.Cleanup(sup.Close)
	sup.SetConversation(parentTurns)
	return sup
}

// A child spawned with inherit opens on the parent's last turns, headed as the
// parent's, ahead of its task: the person's and the assistant's words whole,
// the tool result elided to the placeholder that names where it was kept, and
// every word through the scrub.
func TestInheritHandsTheLastTurnsAheadOfTheTask(t *testing.T) {
	env := &inheritingEnv{scriptedEnv: &scriptedEnv{steps: []streamStep{{text: "it resets in StartTurn"}}}}
	sup := newInheritingSupervisor(t, env, nil)
	spawned := execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"check the reset","inherit":1}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	opening := env.openingTurn()
	for _, want := range []string{
		"What follows is the last turn of the conversation that spawned you",
		"your parent's, not yours",
		"user: second ask: why does the round cap reset?",
		"assistant called read_file · internal/agent/loop.go",
		"tool result (read_file): [result elided: ",
		"ev-1234567890abcdef",
		"session note: a gate verdict the session wrote",
		"assistant: the counter is zeroed in StartTurn",
	} {
		if !strings.Contains(opening, want) {
			t.Errorf("the opening turn does not carry %q:\n%s", want, opening)
		}
	}
	for _, gone := range []string{"first ask", "the parent's system prompt", "hunter2", "the round counter is reset at every turn"} {
		if strings.Contains(opening, gone) {
			t.Errorf("the opening turn carries %q, which it must not:\n%s", gone, opening)
		}
	}
	if !strings.HasSuffix(opening, "check the reset") {
		t.Fatalf("the task must follow the turns, last:\n%s", opening)
	}
	if env.inherits[0] != 1 {
		t.Fatalf("the environment was told the child inherits %d turns, want 1", env.inherits[0])
	}
	if env.keptCount() != 1 {
		t.Fatalf("the elided result must be kept once, after admission; kept %d times", env.keptCount())
	}
	if st, _ := sup.Get("researcher-1"); st.Inheritance <= 0 {
		t.Fatalf("the status must carry what the child inherited, got %d", st.Inheritance)
	}
	if !strings.Contains(spawned, "It was handed your last turn") {
		t.Fatalf("the spawn's answer must say what the child was handed:\n%s", spawned)
	}
}

// The default is nothing: a child spawned without inherit opens on its task
// alone, and is told it cannot see the conversation.
func TestAChildInheritsNothingByDefault(t *testing.T) {
	env := &inheritingEnv{scriptedEnv: &scriptedEnv{steps: []streamStep{{text: "done"}}}}
	sup := newInheritingSupervisor(t, env, nil)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"check the reset"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	if opening := env.openingTurn(); opening != "check the reset" {
		t.Fatalf("a child that inherits nothing opens on its task alone, got:\n%s", opening)
	}
	if env.inherits[0] != 0 || env.keptCount() != 0 {
		t.Fatalf("nothing was inherited, yet the spec said %d and %d results were kept", env.inherits[0], env.keptCount())
	}
	if st, _ := sup.Get("researcher-1"); st.Inheritance != 0 {
		t.Fatalf("a child handed its task alone inherited %d", st.Inheritance)
	}
}

// A profile's inherit is a default: a spawn that says nothing takes it, and one
// that says 0 lowers it to nothing.
func TestAProfilesInheritIsADefaultACallMayLower(t *testing.T) {
	profiles := BuiltinProfiles()
	checker := Profile{Name: "checker", Description: "checks a conclusion", Inherit: 2}
	profiles[checker.Name] = checker

	env := &inheritingEnv{scriptedEnv: &scriptedEnv{steps: []streamStep{{text: "one"}}}}
	sup := newInheritingSupervisor(t, env, profiles)
	execTool(t, sup, SpawnToolName, `{"role":"checker","task":"check it"}`)
	execTool(t, sup, ReportToolName, `{"name":"checker-1"}`)
	if opening := env.openingTurn(); !strings.Contains(opening, "user: first ask") || !strings.HasPrefix(opening, "What follows is the last 2 turns") {
		t.Fatalf("the profile's default of two turns was not handed over:\n%s", opening)
	}

	lowered := &inheritingEnv{scriptedEnv: &scriptedEnv{steps: []streamStep{{text: "two"}}}}
	sup = newInheritingSupervisor(t, lowered, profiles)
	execTool(t, sup, SpawnToolName, `{"role":"checker","task":"check it","inherit":0}`)
	execTool(t, sup, ReportToolName, `{"name":"checker-1"}`)
	if opening := lowered.openingTurn(); opening != "check it" {
		t.Fatalf("inherit 0 must lower the profile's default to nothing, got:\n%s", opening)
	}
}

// The inherited turns are part of what a child is admitted for, and a spawn
// they would not leave the working reserve for is refused before anything is
// claimed — no slot, no record, nothing written to the store — naming them
// among what it counted.
func TestInheritedTurnsAreCountedBeforeAnythingIsClaimed(t *testing.T) {
	big := []provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("a long ask ", 60_000)},
		{Role: provider.RoleAssistant, Content: "noted"},
	}
	env := &inheritingEnv{scriptedEnv: &scriptedEnv{steps: []streamStep{{text: "x"}}}}
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: env.factory(),
		Record: func(Spec, string) Recorder { t.Fatal("a refused spawn opened a record"); return Recorder{} }})
	t.Cleanup(sup.Close)
	sup.SetConversation(func() []provider.Message { return big })

	_, err := sup.Spawn(json.RawMessage(`{"role":"researcher","task":"x","inherit":1,"max_tokens":300000}`))
	if err == nil || !strings.Contains(err.Error(), "cannot admit") || !strings.Contains(err.Error(), "your last turn (~") {
		t.Fatalf("the inherited turns must raise the floor and be named in the refusal: %v", err)
	}
	if children := sup.Snapshot(); len(children) != 0 {
		t.Fatalf("a refused spawn claimed a child slot: %+v", children)
	}
	if env.keptCount() != 0 {
		t.Fatalf("a refused spawn wrote %d results to the store", env.keptCount())
	}
}

// A retry is handed the same turns the first attempt was, ahead of how that
// attempt ended — not a fresh read of a conversation that has moved on.
func TestARetryReissuesTheSameInheritanceAheadOfItsHandoff(t *testing.T) {
	env := &inheritingEnv{scriptedEnv: &scriptedEnv{steps: []streamStep{
		{text: "ran out of room", usage: &provider.Usage{PromptTokens: 300100}},
	}}}
	sup := newInheritingSupervisor(t, env, nil)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"check the reset","inherit":1,"max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)
	first, _ := sup.Get("researcher-1")

	// The parent's conversation moves on; the retry must not read it again.
	sup.SetConversation(func() []provider.Message {
		return []provider.Message{{Role: provider.RoleUser, Content: "a later ask nobody handed over"}}
	})
	env.scriptedEnv.mu.Lock()
	env.steps = []streamStep{{text: "it resets in StartTurn"}}
	env.requests = nil
	env.scriptedEnv.mu.Unlock()
	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitState(t, sup, "researcher-1", StateDone)

	opening := env.openingTurn()
	turns := strings.Index(opening, "user: second ask")
	handoff := strings.Index(opening, "A previous attempt at this task ended")
	switch {
	case turns < 0:
		t.Fatalf("the retry lost the inherited turns:\n%s", opening)
	case handoff < 0:
		t.Fatalf("the retry lost its handoff:\n%s", opening)
	case turns > handoff:
		t.Fatalf("the inherited turns must come ahead of the handoff:\n%s", opening)
	case strings.Contains(opening, "a later ask"):
		t.Fatalf("the retry read the parent's conversation again:\n%s", opening)
	}
	if last := env.inherits[len(env.inherits)-1]; last != 1 {
		t.Fatalf("the retry's environment was told it inherits %d turns, want 1", last)
	}
	if st, _ := sup.Get("researcher-1"); st.Inheritance != first.Inheritance {
		t.Fatalf("the retry's inheritance moved: %d, was %d", st.Inheritance, first.Inheritance)
	}
}

// The spawn card says what of the conversation the child will be shown, since
// that is part of what is being approved.
func TestTheSpawnCardSaysTheTurnsAreHandedOver(t *testing.T) {
	plan, err := SpawnPlan(nil, json.RawMessage(`{"role":"reviewer","task":"judge it","inherit":2}`))
	if err != nil || !strings.HasSuffix(plan.Budget, ", handed your last 2 turns") {
		t.Fatalf("the card's budget line does not say the turns are handed over: %q (%v)", plan.Budget, err)
	}
	if plan, _ := SpawnPlan(nil, json.RawMessage(`{"role":"reviewer","task":"judge it"}`)); strings.Contains(plan.Budget, "handed") {
		t.Fatalf("a spawn handing nothing over says it does: %q", plan.Budget)
	}
}

// A negative count is refused: it is a number of turns.
func TestANegativeInheritIsRefused(t *testing.T) {
	if _, err := parseSpawnArgs(nil, json.RawMessage(`{"role":"researcher","task":"x","inherit":-1}`)); err == nil {
		t.Fatal("a negative inherit must be refused")
	}
}

// Turns are counted at the messages somebody wrote: a message the session
// wrote itself belongs to the turn it arrived in, and a conversation shorter
// than the ask hands over what there is.
func TestLastTurnsCountsTheTurnsSomebodyStarted(t *testing.T) {
	msgs := parentTurns()
	cases := []struct {
		n, got int
		first  string
	}{
		{0, 0, ""},
		{1, 1, "second ask: why does the round cap reset? the token is hunter2"},
		{2, 2, "first ask: survey the exporter"},
		{5, 2, "first ask: survey the exporter"},
	}
	for _, c := range cases {
		turns, got := lastTurns(msgs, c.n)
		if got != c.got {
			t.Errorf("lastTurns(%d) counted %d turns, want %d", c.n, got, c.got)
			continue
		}
		if c.first == "" {
			if len(turns) != 0 {
				t.Errorf("lastTurns(%d) handed over %d messages", c.n, len(turns))
			}
			continue
		}
		if turns[0].Content != c.first {
			t.Errorf("lastTurns(%d) starts at %q, want %q", c.n, turns[0].Content, c.first)
		}
		for _, m := range turns {
			if m.Role == provider.RoleSystem {
				t.Errorf("lastTurns(%d) handed over the parent's system prompt", c.n)
			}
		}
	}
}
