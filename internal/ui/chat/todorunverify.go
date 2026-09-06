package chat

// The run's verify stage: the item's own tests, then the project's quality
// gate, run by the session rather than asked of the model. It is its own
// file because it is one of the two things in a run a model must not do, so
// what counts as a pass is decided in one place and read there.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/runner"
)

// todoVerifyMsg is the verify stage's outcome.
type todoVerifyMsg struct {
	slug   string
	ok     bool
	output string
}

// verifyTimeout bounds the whole verify stage — every test the item lists
// plus the project's checks.
const verifyTimeout = 15 * time.Minute

// todoVerifyCmd runs the item's listed tests, then the project's checks,
// in the background, and reports the tails. The test commands are the
// ones the item held when the run started, before any model turn could
// have edited the file — the model is told to tick boxes in it, and a
// command it wrote itself must not be one shhh runs unasked.
func (m Model) todoVerifyCmd(named string) tea.Cmd {
	root := m.todos.Root
	slug := m.todoRunner.item.Slug
	tests := m.todoRunner.state.Tests
	gate := m.gate.Run
	if named != "" {
		// A step that names its own command runs that and nothing else: the
		// project said what checking this work means, and the item's own
		// tests and the workspace's suite are the answer for the step that
		// did not.
		tests, gate = []string{named}, nil
	}
	// A run whose implement stage closed on a passing gate carries that
	// verdict here rather than paying for the suite twice over a tree that
	// did not move between the two (run.State.Checks).
	checked := m.todoRunner.state.Checked
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
		defer cancel()
		var b strings.Builder
		ok := true
		for _, cmd := range tests {
			out, code := runner.RunCaptureIn(ctx, root, cmd)
			fmt.Fprintf(&b, "$ %s → exit %d\n%s\n", cmd, code, tail(out, 40))
			if code != 0 {
				ok = false
			}
		}
		if gate != nil && !checked {
			res, err := gate(ctx, "")
			switch {
			case err != nil:
				fmt.Fprintf(&b, "quality gate: %v\n", err)
				ok = false
			case res.Verdict != quality.VerdictPass:
				b.WriteString(res.Format(quality.TakeFingerprint(root)) + "\n")
				ok = false
			default:
				fmt.Fprintf(&b, "quality gate %q: pass\n", res.Suite)
			}
		}
		if checked {
			// The implement stage's own close already ran the suite over
			// this tree and it passed; nothing has changed since, so a
			// second run would spend a build to reach the same verdict.
			b.WriteString("quality gate: passed as the implement turn closed\n")
		}
		if len(tests) == 0 && gate == nil {
			b.WriteString("nothing to verify: the item lists no tests and the project has no quality gate\n")
		}
		return todoVerifyMsg{slug: slug, ok: ok, output: strings.TrimRight(b.String(), "\n")}
	}
}

func tail(s string, lines int) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(parts) > lines {
		parts = append([]string{fmt.Sprintf("… %d lines above", len(parts)-lines)}, parts[len(parts)-lines:]...)
	}
	return strings.Join(parts, "\n")
}

// finishTodoVerify applies the verify outcome.
func (m Model) finishTodoVerify(msg todoVerifyMsg) (tea.Model, tea.Cmd) {
	st := m.todoRunner.state
	if st == nil || st.Over() || msg.slug != st.Slug {
		return m, nil
	}
	if !st.AwaitsCommand() {
		return m, nil
	}
	label := "passed"
	if !msg.ok {
		label = "failed"
	}
	model, _ := m.systemNotice(fmt.Sprintf("▸ todo run %s · verify %s\n%s", st.Slug, label, msg.output))
	return model.(Model).todoRunStep(st.VerifyResult(m.todoRunner.item, msg.ok, msg.output))
}
