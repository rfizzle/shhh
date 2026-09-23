package chat

// Routes where the session asks something other than the reader: the
// classifier's verdict in auto mode, and the public status a silent run is
// asked for (program_routes_test.go says what these are for).

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// judgeProvider answers the classifier with the next verdict it was given,
// each as the decision tool call the classifier reads.
type judgeProvider struct {
	mu       sync.Mutex
	verdicts [][2]string
}

func (p *judgeProvider) Name() string { return "judge" }

func (p *judgeProvider) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	v := p.verdicts[0]
	if len(p.verdicts) > 1 {
		p.verdicts = p.verdicts[1:]
	}
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{
		ID: "d1", Name: agent.DecisionToolName,
		Arguments: `{"decision":"` + v[0] + `","reason":"` + v[1] + `"}`,
	}}, Done: true}
	close(ch)
	return ch, nil
}

// In auto mode the classifier's refusal is a row in the feed and not a card,
// the turn carries on past it, and reading mode opens the row onto the
// reason the judge wrote.
func TestProgram_AJudgedRefusalIsARowTheReaderCanOpen(t *testing.T) {
	var ran []string
	m, _ := scriptedSession(
		commandTurn(nil, "npm run deploy -- --tag latest"),
		programTurn{text: "Leaving the release alone."},
	)
	m = m.WithRunner(legacyRunner(func(_ context.Context, cmd string) (string, int) {
		ran = append(ran, cmd)
		return "", 0
	})).WithClassifier(agent.NewClassifier(&judgeProvider{verdicts: [][2]string{
		{"deny", "the task asked for a release check and this publishes one"},
	}}, agent.ClassifierConfig{Model: "judge"}))
	tm := runProgram(t, m)

	programPress(t, tm, "shift+tab", "shift+tab")
	waitForText(t, tm, "⏵⏵ auto")
	send(tm, "publish the release")
	waitForText(t, tm, "Leaving the release alone")
	programPress(t, tm, "ctrl+o", "k", "k", "enter")
	waitForText(t, tm, "this publishes one")

	frame := finalFrame(t, tm)
	if len(ran) != 0 {
		t.Fatalf("a command the classifier refused ran: %v\n%s", ran, frame)
	}
	frameHas(t, frame, "blocked", "this publishes one")
}

// A run that has read for a while without a word is asked for a public
// status at a round boundary, and the prose that answers it is drawn on the
// grid over the calls it leads. The request is read off the provider, since
// what the frame draws for a status and for any other paragraph is the
// same text in a different colour — the golden's question.
func TestProgram_ASilentRunIsAskedForItsStatus(t *testing.T) {
	const status = "The objective is where the round limit is counted and who is allowed to raise it. " +
		"The evidence is that the counter moves in the agent while the ceiling is read by the session. " +
		"Next I will read the pause row."
	dir := fixtureDir(t, map[string]string{"one.go": "package fixture\n", "two.go": "package fixture\n", "three.go": "package fixture\n", "stream.go": "package fixture\n"})
	m, p := scriptedSession(
		programTurn{calls: reads("one.go")},
		programTurn{calls: reads("two.go")},
		programTurn{calls: reads("three.go")},
		programTurn{text: status + "\n", calls: reads("stream.go")},
		programTurn{text: "The suite is green and the run is quiet again."},
	)
	m = m.WithWorkspace(dir).WithToolExecutor(subagent.RootedExecutor(dir, tools.Execute)).
		WithProgressIntervals(2, time.Hour)
	tm := runProgramAt(t, m, 110, 40)

	send(tm, "trace the checkpoint")
	waitForText(t, tm, "quiet again")

	frameHas(t, finalFrame(t, tm), "The objective is where the round limit is counted")
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.ContainsFunc(p.asked, func(msg provider.Message) bool { return msg.Content == agent.ProgressPrompt }) {
		t.Fatal("no round boundary asked the silent run for its status")
	}
}
