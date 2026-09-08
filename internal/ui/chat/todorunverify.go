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
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoVerifyMsg is the verify stage's outcome: what it ran, whether that
// passed, and the reason there was nothing to reach a verdict with at all.
// The last one is separate because a run with nothing to verify is not a run
// that verified, and it is not work a fix round could answer either.
type todoVerifyMsg struct {
	slug    string
	ok      bool
	output  string
	blocked string
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
	// A run whose implement stage closed on a passing gate carries that
	// verdict here rather than paying for the suite twice over a tree that
	// did not move between the two (run.State.Checks).
	checked := m.todoRunner.state.Checked
	if named != "" {
		// A step that names its own command runs that and nothing else: the
		// project said what checking this work means, and the item's own
		// tests and the workspace's suite are the answer for the step that
		// did not — so a verdict about the suite has nothing to say here
		// either.
		tests, gate, checked = []string{named}, nil, false
	}
	keep := m.evidence.Keep
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
		defer cancel()
		var b strings.Builder
		ok := true
		for _, cmd := range tests {
			out, code := runner.RunCaptureIn(ctx, root, cmd)
			// Both ends of the output and a citation for the rest: the
			// stage after this one is asked to fix what this found, and a
			// tail alone hands it the count of the failures and none of
			// the failures.
			fmt.Fprintf(&b, "$ %s → exit %d%s\n%s\n", cmd, code,
				keptAs(keep, "verify:"+cmd, out), quality.Excerpt(out, quality.MaxInlineBytes))
			if code != 0 {
				ok = false
			}
		}
		// silent is why the gate reached no verdict over this tree, and
		// empty where it reached one — pass or fail.
		silent := "this checkout is not trusted, so the project's quality gate does not run here"
		switch {
		case checked:
			// The implement stage's own close already ran the suite over
			// this tree and it passed; nothing has changed since, so a
			// second run would spend a build to reach the same verdict.
			b.WriteString("quality gate: passed as the implement turn closed\n")
			silent = ""
		case gate == nil:
		default:
			res, err := gate(ctx, "")
			switch {
			case err != nil:
				fmt.Fprintf(&b, "quality gate: %v\n", err)
				ok, silent = false, ""
			case res.Unconfigured:
				// A workspace with no quality config is the one blocked
				// verdict that is not about the work: nothing in the tree
				// is wrong, and the file only a person can write is the
				// fix, so a fix round would be spent on findings that do
				// not exist.
				fmt.Fprintf(&b, "quality gate: %s\n", res.Reason)
				silent = "the project has no " + quality.ConfigRelPath
			case res.Verdict != quality.VerdictPass:
				b.WriteString(res.Format(quality.TakeFingerprint(root)) + "\n")
				ok, silent = false, ""
			default:
				fmt.Fprintf(&b, "quality gate %q: pass\n", res.Suite)
				silent = ""
			}
		}
		out := strings.TrimRight(b.String(), "\n")
		// A step that put the work to no check at all stops the run rather
		// than reporting a pass with nothing standing behind it — the same
		// answer the unattended runner gives, because the item must not be
		// able to tell which surface worked it.
		if len(tests) == 0 && silent != "" {
			return todoVerifyMsg{slug: slug, output: out,
				blocked: run.NothingVerifies("it lists no tests and " + silent)}
		}
		return todoVerifyMsg{slug: slug, ok: ok, output: out}
	}
}

// keptAs spools a command's whole output where the session's evidence tool
// can page it back, and answers with the citation to print beside the
// excerpt — in the wording the gate already uses for its own checks
// (quality.Format). A session with no store says nothing: an id nobody can
// resolve is worse than no id.
func keptAs(keep func(tool, content string) (string, bool), tool, content string) string {
	if keep == nil || content == "" {
		return ""
	}
	id, ok := keep(tool, content)
	if !ok {
		return ""
	}
	return " [full output: evidence " + id + "]"
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
	if msg.blocked != "" {
		said := fmt.Sprintf("▸ todo run %s · verify", st.Slug)
		if msg.output != "" {
			said += "\n" + msg.output
		}
		model, _ := m.systemNotice(said)
		return model.(Model).todoRunStep(st.Block(msg.blocked))
	}
	label := "passed"
	if !msg.ok {
		label = "failed"
	}
	model, _ := m.systemNotice(fmt.Sprintf("▸ todo run %s · verify %s\n%s", st.Slug, label, msg.output))
	return model.(Model).todoRunStep(st.VerifyResult(m.todoRunner.item, msg.ok, msg.output))
}
