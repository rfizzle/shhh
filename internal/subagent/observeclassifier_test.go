package subagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// downProvider is a classifier backend that fails every request, after a
// pause the failure should be timed by.
type downProvider struct{}

func (downProvider) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	time.Sleep(2 * time.Millisecond)
	return nil, errors.New("backend down")
}

func (downProvider) Name() string { return "down" }

// Every row a child's classifier produced carries the time the judgement took
// and the code of the judgement: an unavailable classifier is filed as that,
// not as the action it was asked about, and a host's standing overruling a yes
// is timed like the yes it overruled.
func TestObserve_EveryClassifierRowIsTimed(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Now().UTC().Format(time.RFC3339)
	for _, name := range web.HostListNames() {
		if name == web.BuiltinHosts {
			continue
		}
		body := "#shhh-hosts 1 " + name + " " + stamp + "\n"
		if name == "urlhaus" {
			body += "bad.test\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name+".hosts"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	web.UseReputation(web.OpenReputation(dir, nil))
	t.Cleanup(func() { web.UseReputation(nil) })

	for _, c := range []struct {
		name  string
		judge provider.Provider
		steps []streamStep
		gated string
		want  string
	}{
		{"failed", downProvider{}, gatedCommandSteps("go install ./cmd/tool"), tools.ExecCommandName, observe.ReasonClassifierFailed},
		{"standing", &verdictProvider{decision: "allow", reason: "reads a page"}, []streamStep{
			{calls: []provider.ToolCall{{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"https://bad.test/x"}`}}},
			{text: "task complete"},
		}, web.FetchToolName, observe.ReasonHostListed},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := &scriptedEnv{steps: c.steps, gated: map[string]bool{c.gated: true}, execOut: "ok"}
			rec := &testRecorder{}
			sup := New(t.Context(), Options{
				Root:       t.TempDir(),
				NewEnv:     env.factory(),
				Classifier: agent.NewClassifier(c.judge, agent.ClassifierConfig{Model: "judge"}),
				Record:     func(Spec, string) Recorder { return rec.recorder() },
			})
			t.Cleanup(sup.Close)
			sup.SetParentMode(agent.ModeAuto)
			execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"do something"}`)
			nextAsk(t, sup).Respond(false)
			execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
			var asks []recordedEvent
			for _, e := range rec.of("decision") {
				if e.outcome == observe.DecisionAsk {
					asks = append(asks, e)
				}
			}
			if len(asks) != 1 || asks[0].reason != c.want || !asks[0].timed {
				t.Fatalf("want one timed ask coded %q, got %+v", c.want, rec.of("decision"))
			}
		})
	}
}
