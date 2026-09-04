package chat

// The quality gate in a session: the /gate command, and the run a turn makes
// as it closes over work it changed.
//
// The gate has always been something somebody asked for — a slash command, or
// a tool call the model chose to make. The close run is the other half: where
// nobody is watching, "the model said it was done" is otherwise the only
// signal that the work is finished, and the system prompt asking it to check
// first is a request rather than a check.
// See docs/capabilities/coding-agent.md#it-can-check-itself.

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
)

// Gate wires the quality gate into the chat TUI. Manage backs the
// /gate slash command: "run [suite]" starts a suite in the background,
// "result" reports the latest verdict (marked stale when the tree changed).
type Gate struct {
	Manage func(args []string) string
	// Run runs a suite to completion and returns its result; the backlog
	// runner's verify stage and the on-close run use it. Nil when the
	// project has no gate.
	Run func(ctx context.Context, suite string) (*quality.Result, error)
}

// WithGate enables the /gate command.
func (m Model) WithGate(g Gate) Model {
	m.gate = g
	return m
}

// closeGateRun is where the closing turn's gate run has got to.
type closeGateRun struct {
	// on is this session's own answer to whether it honours the workspace's
	// on_close suite. It is off unless the reader turns it on, and that is
	// the whole difference between the two kinds of session: five small
	// edits with somebody watching become five waits for a suite they would
	// have run themselves, and the breakage they are waiting to hear about
	// is one they would have seen.
	on bool
	// turn is the turn the counters below describe. A close run belongs to
	// one turn, and reading them against another turn's number is how a
	// budget spent on the last question comes to bound the next one.
	turn int64
	// fed is how many failing verdicts this turn has already handed back.
	fed int
	// running marks a run in flight; settled marks a turn whose verdict is
	// in and whose close may now go ahead.
	running, settled bool
	// cancel stops the run in flight. The gate is a subprocess and a suite
	// is minutes of it, so a turn the reader gave up on must not go on
	// spending a build on their behalf.
	cancel context.CancelFunc
}

// closeGateMsg is one on-close run's verdict coming back.
type closeGateMsg struct {
	turn  int64
	suite string
	res   *quality.Result
	err   error
}

// closeGateArmed reports whether this session honours the workspace's
// on_close suite at all. A backlog run's implement stage is armed without
// being asked: nobody is reading it, its next stage commits, and a stage
// that hands a broken tree to a commit turn is the failure this exists to
// stop.
//
// A sprint is armed for every stage of every item in it, and not only for the
// one stage that writes. A sprint is unattended by definition — that is what
// asking for one says — so the toggle a person leaves off for a session they
// are watching is on here without being asked for. In practice it is the
// implement stage that runs the suite either way, because the stages around
// it change nothing for a suite to have an opinion about; what this settles
// is which of the two sessions has to say so.
func (m Model) closeGateArmed() bool {
	return m.closeGate.on || m.todoRun.Sprinting() || m.todoRun.ClosesWithGate()
}

// closeGateSuite is the suite a close should run and how many failing
// verdicts it may hand back, read off the trusted config the way a run reads
// it — fresh, so a file edited mid-session takes effect at the next close.
// An empty suite means nothing runs.
//
// A workspace with no config, or one that will not parse, answers the same
// way as one that names no suite. The gate is optional, a broken file is
// already reported by every run that blocks on it, and a notice at the close
// of every turn would be the session reporting a fault at the moment it is
// least wanted.
func (m Model) closeGateSuite() (string, int) {
	if !m.closeGateArmed() {
		return "", 0
	}
	return m.workspaceCloseSuite()
}

// workspaceCloseSuite is the same reading without the session's answer: what
// the workspace asks for, whoever is or is not honouring it. A backlog run
// reads it when it starts, because what a run does is the run's to decide
// and not something it inherits from a toggle the reader left set.
func (m Model) workspaceCloseSuite() (string, int) {
	if m.gate.Run == nil {
		return "", 0
	}
	cfg, err := quality.LoadConfig(m.workspace)
	if err != nil || cfg.OnClose == "" {
		return "", 0
	}
	return cfg.OnClose, cfg.CloseRetries()
}

// workspaceClosesGate is the question a starting backlog run asks: does the
// workspace name a suite for a turn's close at all.
func (m Model) workspaceClosesGate() bool {
	suite, _ := m.workspaceCloseSuite()
	return suite != ""
}

// closeGateOwed reports whether the turn now ending still owes a gate run.
// It is what holds the close row back: the verdict belongs on that row, so
// the row waits for it rather than being drawn twice.
func (m Model) closeGateOwed() bool {
	if m.closeGate.running {
		return false
	}
	if m.closeGate.settled && m.closeGate.turn == m.turnCount {
		return false
	}
	if suite, _ := m.closeGateSuite(); suite == "" {
		return false
	}
	// A turn that changed nothing, or changed only shhh's own state
	// directory, has nothing a suite could have an opinion about.
	t, ok := m.changes.Turn(m.turnCount)
	return ok && t.Checkable()
}

// closeGateCmd starts the run the closing turn owes. It is derived on the
// Update tail the way the turn's summary is: "the turn reached its close" is
// a fact about the model before against the model after, and setTurnState —
// where the wait is decided — hands back no command to start it with.
func (m *Model) closeGateCmd() tea.Cmd {
	if m.turnState() != stateCloseGate || m.closeGate.running {
		return nil
	}
	suite, _ := m.closeGateSuite()
	if suite == "" {
		// The config was readable when the turn was sent here and is not
		// now. A turn must never sit waiting on a run nothing is going to
		// start, so the close it was holding back goes ahead.
		m.closeGate.settled, m.closeGate.turn = true, m.turnCount
		m.setTurnState(stateInput)
		return nil
	}
	if m.closeGate.turn != m.turnCount {
		m.closeGate.turn, m.closeGate.fed, m.closeGate.settled = m.turnCount, 0, false
	}
	run, turn := m.gate.Run, m.turnCount
	ctx, cancel := context.WithCancel(context.Background())
	m.closeGate.running, m.closeGate.cancel = true, cancel
	return func() tea.Msg {
		defer cancel()
		res, err := run(ctx, suite)
		return closeGateMsg{turn: turn, suite: suite, res: res, err: err}
	}
}

// finishCloseGate applies the verdict: the row first, then either another
// round with the verdict in front of the model or the close the turn has
// been waiting to draw.
func (m Model) finishCloseGate(msg closeGateMsg) (tea.Model, tea.Cmd) {
	if !m.closeGate.running || msg.turn != m.turnCount {
		return m, nil
	}
	m.closeGate.running, m.closeGate.cancel = false, nil
	if msg.err != nil {
		// The one error Run reports is a suite already in flight. Where the
		// turn already has a gate row it is the model's own run, whose
		// verdict the close is about to read anyway, and a second row would
		// be the second gate run this mechanism exists to avoid. Where it
		// has none — a background /gate run still going — the turn would
		// otherwise close saying nothing at all about checks it owed, so
		// what it says is that they did not run. Blocked is never a pass.
		if !hasGateRow(m.turnEntries()) {
			res := &quality.Result{
				Suite: msg.suite, Verdict: quality.VerdictBlocked, Reason: msg.err.Error(),
				Fingerprint: quality.TakeFingerprint(m.workspace),
			}
			m.appendCloseGateRow(msg.suite, res.Format(res.Fingerprint))
		}
		return m.settleCloseGate()
	}
	_, retries := m.closeGateSuite()
	text := msg.res.Format(quality.TakeFingerprint(m.workspace))
	m.appendCloseGateRow(msg.suite, text)
	// A backlog run's verify stage is about to ask the same question of the
	// same tree. It is told the answer here rather than paying for it again
	// (run.State.Checks). The reading is the formatted result's own, so
	// what counts as a pass is the one definition every surface uses — a
	// stale pass among them, which is not one.
	if m.todoRun.ClosesWithGate() {
		sum, ok := quality.Summarize(text)
		m.todoRun.Checks(ok && sum.OK())
	}
	failed := msg.res.Verdict == quality.VerdictFail || msg.res.Verdict == quality.VerdictBlocked
	if failed && m.closeGate.fed < retries {
		m.closeGate.fed++
		// The same text the tool returns, and nothing around it: a session
		// that phrased this failure its own way would be teaching the model
		// a second vocabulary for an event it already knows one for.
		m.agent.Append(provider.Message{Role: provider.RoleUser, Content: text})
		return m.resumeForCloseGate()
	}
	return m.settleCloseGate()
}

// hasGateRow reports whether the turn already holds a gate verdict, whoever
// asked for it.
func hasGateRow(es []entry) bool {
	for _, e := range es {
		if e.kind == entryTool && e.toolName == quality.ToolName {
			return true
		}
	}
	return false
}

// appendCloseGateRow puts the verdict in the transcript as the gate tool row
// it would have been had the model asked for it. One shape for both, so the
// close row's tally, the staleness reading and the observer all see one kind
// of gate run rather than two.
func (m *Model) appendCloseGateRow(suite, text string) {
	m.appendEntry(entry{
		kind:       entryTool,
		toolName:   quality.ToolName,
		toolArgs:   fmt.Sprintf(`{"action":"run","suite":%q}`, suite),
		toolResult: text,
	})
}

// settleCloseGate lets the close the turn was holding back go ahead. The
// transition it makes is the ordinary one back to the input, so everything a
// turn's end owes — the close row with the verdict on it, the record, the
// vitals ring — happens where it always does.
func (m Model) settleCloseGate() (tea.Model, tea.Cmd) {
	m.closeGate.settled, m.closeGate.turn = true, m.turnCount
	m.setTurnState(stateInput)
	m.invalidateRenderCache()
	// And what was typed while the suite ran becomes the next turn now,
	// which is what would have happened at this turn's end had there been no
	// checks to wait for. It was held rather than sent because sending it
	// would have taken the turn away from the run it was waiting on.
	if cmd := m.dispatchSteering(); cmd != nil {
		return m, cmd
	}
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.autosaveCmd()
}

// resumeForCloseGate continues the turn with the failing verdict in front of
// the model. The turn never closed — its accounting, its changeset and its
// counters are the ones it has had all along — so this is the round-limit
// grant's shape without the grant: the counter is untouched, because a turn
// that could not finish inside its budget must not be handed more of one for
// having failed a check.
func (m Model) resumeForCloseGate() (tea.Model, tea.Cmd) {
	m.atBottom = true
	m.invalidateRenderCache()
	// Through the tool loop rather than straight to a request, because
	// everything a round boundary owes the next round is owed here too: the
	// steering typed while the suite ran, a tree that moved under it, a hold
	// asked on the way, and the ceiling the turn is still under — a turn at
	// its cap parks on the checkpoint instead of taking one more round.
	next, cmd := m.resumeToolLoop()
	resumed, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	resumed.viewport.SetLines(resumed.renderHistoryLines())
	resumed.viewport.GotoBottom()
	return resumed, tea.Batch(cmd, resumed.autosaveCmd())
}

// cancelCloseGate stops a run in flight and leaves the turn a verdict saying
// so. It is called from the one path that abandons a turn, because the close
// row is drawn from what the transcript holds: without this the row would
// report only the checks the turn ran itself, and a turn stopped mid-suite
// would close looking like one whose checks were never in doubt.
func (m *Model) cancelCloseGate() {
	if !m.closeGate.running {
		return
	}
	if m.closeGate.cancel != nil {
		m.closeGate.cancel()
	}
	m.closeGate.running, m.closeGate.cancel = false, nil
	m.closeGate.settled, m.closeGate.turn = true, m.turnCount
	suite, _ := m.closeGateSuite()
	res := &quality.Result{
		Suite:       suite,
		Verdict:     quality.VerdictCancelled,
		Reason:      "the run was cancelled before completing",
		Fingerprint: quality.TakeFingerprint(m.workspace),
	}
	m.appendCloseGateRow(suite, res.Format(res.Fingerprint))
}

// closeGateBlock is the live tail while the run is going. The gate is the one
// thing a turn does after the model has stopped talking, so without a line of
// its own the session would look finished for as long as the suite takes.
func (m Model) closeGateBlock() string {
	suite, _ := m.closeGateSuite()
	if suite == "" {
		return m.spinner.View() + " Running the quality gate…"
	}
	return fmt.Sprintf("%s Running the %q quality-gate suite…", m.spinner.View(), suite)
}

// gateToggle answers /gate on and /gate off, and reports whether it did. The
// toggle is the session's own state and the runner behind Manage has none,
// so it is answered here rather than there.
//
// The bottom rail's mode segment does not change for it: the gate is not a
// permission mode, and a session that checks its work is under exactly the
// same rules about what it may do as one that does not.
func (m *Model) gateToggle(args []string) (bool, string) {
	if len(args) != 1 {
		return false, ""
	}
	switch args[0] {
	case "on":
		m.closeGate.on = true
		suite, _ := m.closeGateSuite()
		if suite == "" {
			return true, "A closing turn will run the gate once " + quality.ConfigRelPath + " names an on_close suite. It names none."
		}
		return true, fmt.Sprintf("A turn that changed files will run the %q suite as it closes.", suite)
	case "off":
		m.closeGate.on = false
		return true, "A closing turn will not run the gate. /gate run still does."
	}
	return false, ""
}
