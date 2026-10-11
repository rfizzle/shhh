package prompt

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/shell"
)

// The rule for a server or a watcher is stated once in each prompt that holds
// commands, and says "where you hold a tool" because the base prompt names no
// tool: a session without the process tool must not be promised one.
func TestPrompt_SaysWhenToStartAProcess(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}
	const rule = "A server, a watcher or a log tail does not end on its own"
	for name, got := range map[string]string{
		"agent":  BuildAgent(info),
		"writer": BuildWriter(info),
	} {
		if n := strings.Count(got, rule); n != 1 {
			t.Errorf("%s prompt states the rule %d times, want once", name, n)
		}
		if strings.Contains(got, "process tool") {
			t.Errorf("%s prompt names the process tool, which the session may not hold", name)
		}
	}
	if got := Build(info); strings.Contains(got, rule) {
		t.Error("the one-shot prompt has no commands to run and must not carry the rule")
	}
}
