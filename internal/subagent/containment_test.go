package subagent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/tools"
)

// A child whose commands must be contained on a host with nothing to contain
// them has its command answered with the refusal before anything decides:
// no card is put to the person, the classifier is not asked, nothing runs,
// and the record files no decision — the refusal is the call's result, as a
// session that requires containment answers its own commands. A child with
// no refusal has its command carded as before.
// See docs/capabilities/containment.md#containment-can-be-required.
func TestARefusedCommandIsAnsweredBeforeTheCard(t *testing.T) {
	const refusal = "error: a writer's commands require containment and no mechanism is in force: none here\n  install one"
	for _, tc := range []struct {
		name    string
		refusal string
		mode    agent.Mode
	}{
		{"manual", refusal, agent.ModeManual},
		{"auto", refusal, agent.ModeAuto},
		{"no refusal", "", agent.ModeManual},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := &scriptedEnv{
				steps:          gatedCommandSteps("go build ./..."),
				gated:          map[string]bool{tools.ExecCommandName: true},
				execOut:        "built",
				commandRefusal: tc.refusal,
			}
			judge := &verdictProvider{decision: "allow", reason: "the task asked for it"}
			var mu sync.Mutex
			var decisions []string
			sup := New(context.Background(), Options{
				Root:       t.TempDir(),
				NewEnv:     env.factory(),
				Classifier: agent.NewClassifier(judge, agent.ClassifierConfig{Model: "judge"}),
				Record: func(Spec, string) Recorder {
					return Recorder{Observer: observe.Observer{
						Decision: func(_ observe.Pos, decision, code string) {
							mu.Lock()
							decisions = append(decisions, decision+"/"+code)
							mu.Unlock()
						},
					}}
				},
			})
			t.Cleanup(sup.Close)
			sup.SetParentMode(tc.mode)
			execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"build it"}`)

			if tc.refusal == "" {
				ask := nextAsk(t, sup)
				if ask.Kind != AskCommand {
					t.Fatalf("a command with no refusal was not carded: %+v", ask)
				}
				ask.Respond(true)
				execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
				if !env.ranCommand.Load() {
					t.Fatal("an approved command did not run")
				}
				return
			}

			// Any card that arrives is answered, so a regression fails the
			// test rather than hanging it on the report.
			asks := make(chan int, 1)
			done := make(chan struct{})
			go func() {
				n := 0
				defer func() { asks <- n }()
				for {
					select {
					case ev := <-sup.Events():
						if ev.Kind == EventAsk {
							n++
							ev.Ask.Respond(true)
						}
					case <-done:
						return
					}
				}
			}()
			execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
			close(done)
			if n := <-asks; n != 0 {
				t.Fatalf("a refused command was put to the person on %d card(s)", n)
			}
			if env.ranCommand.Load() {
				t.Fatal("the runner was reached for a refused command")
			}
			if judge.calls.Load() != 0 {
				t.Fatalf("the classifier was asked about a refused command (%d calls)", judge.calls.Load())
			}
			if got := env.lastToolResult(); got != tc.refusal {
				t.Fatalf("the child read %q, want the refusal %q", got, tc.refusal)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(decisions) != 0 {
				t.Fatalf("a refusal was filed as a decision: %v", decisions)
			}
			var row bool
			for _, e := range sup.Transcript("researcher-1") {
				if e.Kind == EntrySystem && strings.HasPrefix(e.Text, "Refused: ") && e.Result == tc.refusal {
					row = true
				}
			}
			if !row {
				t.Error("the child's transcript has no row saying the command was refused")
			}
		})
	}
}
