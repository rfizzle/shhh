package chat

// The `!` prefix (docs/interface/surfaces.md#the-input-frame): the shell
// door every harness shares. A draft that starts with `!` is a command,
// and it goes through the same confirm card `/run` uses — nothing runs
// unseen, whichever door it came in by. `!!` runs the command and keeps
// its output out of the conversation: the reader sees it in the
// transcript, the model never does, and the row's outcome says `local`.
// A `!` anywhere past the start of the draft is a letter.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// bangCommand reads a submitted draft in bang form: the command after the
// bangs, whether the output stays local (`!!`), and whether the draft was
// in bang form at all. A bare `!` or `!!` with nothing after it is not a
// command, and neither is a `!` past the first character — those are text.
func bangCommand(text string) (cmd string, local, ok bool) {
	if !strings.HasPrefix(text, "!") {
		return "", false, false
	}
	rest := text[1:]
	if strings.HasPrefix(rest, "!") {
		rest, local = rest[1:], true
	}
	cmd = strings.TrimSpace(rest)
	if cmd == "" {
		return "", false, false
	}
	return cmd, local, true
}

// bangDraft reports whether the draft as typed is in bang form — what the
// gutter reads to swap its glyph, matched exactly the way submit will read
// the line so the two cannot disagree.
func (m Model) bangDraft() bool {
	_, _, ok := bangCommand(strings.TrimSpace(m.input.Value()))
	return ok
}

// runBang routes a bang draft to the /run confirm card. It shares /run's
// idle-only rule: a command run mid-turn would land its output in a
// conversation the agent is mid-thought in, so it is refused rather than
// queued, in the same words the registry gives /run.
func (m Model) runBang(cmd string, local bool) (tea.Model, tea.Cmd) {
	if m.runFn == nil {
		return m.surfaceNotice("Command execution is not available in this session.")
	}
	if m.working() || m.decisionUngated() {
		reason, _ := idleOnlyReason("/run")
		return m.surfaceNotice("! needs the turn to be finished — " + reason +
			". The agent is still working; nothing was queued. Ctrl+C ends the turn.")
	}
	m.pendingRun = cmd
	m.pendingRunLocal = local
	// The user's own command: never contained, so the working scope has
	// nothing to say about it — exactly as /run.
	m.pendingScope = scopeReach{}
	m.pendingBlast = m.resolveRadius(nil)
	m.clearQueueStrip()
	m.setTurnState(stateConfirmRun)
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
}
