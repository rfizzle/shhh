package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
)

// A shell read the model ran is answered with its output and, under it, the
// built-in tool that would have answered it — once in the turn, after the
// repeat detector has had its say, and never on the row the person reads.
func TestAShellReadNamesTheToolThatAnswersItOncePerTurn(t *testing.T) {
	m := gatedModel(t, nil, nil).
		WithRunner(legacyRunner(func(context.Context, string) (string, int) { return "a.go:3: TODO", 0 })).
		WithRepeats(agent.NewRepeatDetector())

	m, first := runOnce(t, m, "grep -rn TODO . | head -5")
	if !strings.Contains(first, "a.go:3: TODO") || !strings.HasSuffix(first, "\n[built-in: search answers this without an approval: a pattern across the tree or in one file, each match with the lines around it; files_only names only the files, include narrows to one kind of file.]") {
		t.Fatalf("a search through the shell should name search under its output:\n%s", first)
	}
	for _, e := range m.transcript {
		if strings.Contains(e.toolResult, "[built-in:") {
			t.Fatalf("the line is the model's, not the row's: %q", e.toolResult)
		}
	}

	m.state = stateStreaming
	m, second := runOnce(t, m, "grep -rn TODO . | head -5")
	if !agent.IsRepeatNotice(second) {
		t.Fatalf("the line on the first run must not hide the repeat from the detector:\n%s", second)
	}
	if strings.Contains(second, "[built-in:") {
		t.Fatalf("search was named once this turn already:\n%s", second)
	}

	m.state = stateStreaming
	_, build := runOnce(t, m, "go test ./...")
	if strings.Contains(build, "[built-in:") {
		t.Fatalf("a build is no read:\n%s", build)
	}
}
