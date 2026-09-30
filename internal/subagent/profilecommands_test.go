package subagent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// commandProfiles is the built-in roles plus one whose profile states a
// Commands section, read from wherever checkout says.
func commandProfiles(checkout bool) Profiles {
	p := BuiltinProfiles()
	p["tester"] = Profile{Name: "tester", Intent: "running the package's tests",
		Deny: []string{"git push"}, Checkout: checkout}
	return p
}

// A profile's deny entries are refused the way the person's own are: through
// the same matcher, so a chained or wrapped spelling is caught too, with the
// same result, which names neither the profile nor the key — and the
// person's own list still stands beside them.
func TestAProfilesDenyIsRefusedLikeThePersonsOwn(t *testing.T) {
	for _, command := range []string{"git push origin main", "git status && git push", "sudo git push", "/usr/bin/git push", "rm -rf build"} {
		t.Run(command, func(t *testing.T) {
			env := &scriptedEnv{
				steps: gatedCommandSteps(command),
				gated: map[string]bool{tools.ExecCommandName: true},
			}
			sup := New(context.Background(), Options{
				Root: t.TempDir(), NewEnv: env.factory(), Profiles: commandProfiles(false),
				CommandDenylist: []string{"rm"},
			})
			t.Cleanup(sup.Close)
			sup.SetParentMode(agent.ModeAuto)
			execTool(t, sup, SpawnToolName, `{"role":"tester","task":"push it"}`)
			execTool(t, sup, ReportToolName, `{"name":"tester-1"}`)
			if env.ranCommand.Load() {
				t.Fatal("a command the profile denies ran")
			}
			result := env.lastToolResult()
			if result != agent.DenylistResult {
				t.Fatalf("the child read %q, want the deny list's own result", result)
			}
			for _, leak := range []string{"tester", "profile", "deny ="} {
				if strings.Contains(result, leak) {
					t.Fatalf("the refusal names %q: %q", leak, result)
				}
			}
		})
	}
}

// One role's deny entries are that role's: a child of another role is asked
// about the same command rather than refused, and the entries never reach
// the list every child shares.
func TestAProfilesDenyStaysWithItsRole(t *testing.T) {
	env := &scriptedEnv{
		steps: gatedCommandSteps("git push"),
		gated: map[string]bool{tools.ExecCommandName: true},
	}
	sup := New(context.Background(), Options{
		Root: t.TempDir(), NewEnv: env.factory(), Profiles: commandProfiles(false),
		CommandDenylist: []string{"rm"},
	})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"push it"}`)
	ask := nextAsk(t, sup)
	if ask.Agent != "researcher-1" || ask.Kind != AskCommand {
		t.Fatalf("the other role's command should be asked about, got %+v", ask)
	}
	ask.Respond(false)
	tester := &child{profile: commandProfiles(false)["tester"]}
	if got := strings.Join(sup.childPolicy(tester).CommandDenylist, ","); got != "rm,git push" {
		t.Fatalf("the tester's policy should add its entries to the person's: %q", got)
	}
	if got := strings.Join(sup.opts.CommandDenylist, ","); got != "rm" {
		t.Fatalf("a profile's entries reached the shared list: %q", got)
	}
}

// evidenceProvider answers every judgement with a yes and keeps the
// evidence each was asked on.
type evidenceProvider struct {
	mu       sync.Mutex
	evidence []string
}

func (p *evidenceProvider) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.evidence = append(p.evidence, msgs[len(msgs)-1].Content)
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: "d1", Name: agent.DecisionToolName,
		Arguments: `{"decision":"allow","reason":"ok"}`}}, Done: true}
	close(ch)
	return ch, nil
}

func (p *evidenceProvider) Name() string { return "evidence" }

// What a profile says its commands are for rides that child's command calls
// to the classifier as profile_scope — the person's own profile's, and never
// a checkout's, whose words are shown on the spawn card instead.
func TestAProfilesIntentReachesTheClassifierOnlyFromThePersonsOwn(t *testing.T) {
	for _, c := range []struct {
		name     string
		checkout bool
		want     bool
	}{
		{"the person's profile", false, true},
		{"the checkout's profile", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := &scriptedEnv{
				steps:   gatedCommandSteps("go test ./..."),
				gated:   map[string]bool{tools.ExecCommandName: true},
				execOut: "ok",
			}
			judge := &evidenceProvider{}
			sup := New(context.Background(), Options{
				Root: t.TempDir(), NewEnv: env.factory(), Profiles: commandProfiles(c.checkout),
				Classifier: agent.NewClassifier(judge, agent.ClassifierConfig{Model: "judge"}),
			})
			t.Cleanup(sup.Close)
			sup.SetParentMode(agent.ModeAuto)
			execTool(t, sup, SpawnToolName, `{"role":"tester","task":"run the tests"}`)
			execTool(t, sup, ReportToolName, `{"name":"tester-1"}`)
			judge.mu.Lock()
			defer judge.mu.Unlock()
			if len(judge.evidence) != 1 {
				t.Fatalf("the classifier was asked %d times", len(judge.evidence))
			}
			got := strings.Contains(judge.evidence[0], `"profile_scope":"running the package's tests"`)
			if got != c.want {
				t.Fatalf("profile_scope sent = %v, want %v:\n%s", got, c.want, judge.evidence[0])
			}
			if !env.ranCommand.Load() {
				t.Fatal("the classifier's yes should have run the command")
			}
		})
	}
}

// The scope the classifier may be handed is the person's own profile's and
// never a checkout's.
func TestAProfilesClassifierScopeIsThePersonsOwn(t *testing.T) {
	p := Profile{Intent: "running the package's tests"}
	if p.ClassifierScope() == "" {
		t.Fatal("the person's profile should state its scope")
	}
	p.Checkout = true
	if p.ClassifierScope() != "" {
		t.Fatal("a checkout's profile should state no scope to the classifier")
	}
}
