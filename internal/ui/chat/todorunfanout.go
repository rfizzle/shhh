package chat

// The run's fan-out stage: a writer child per lane, and the three things one
// sends back — an approval to route, a patch that landed, a report. It is its
// own file because a lane is a session of its own, so keeping its questions
// in front of the reader is unlike anything the single-turn stages do.

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/subagent"
)

// startTodoFanOut spawns a writer per lane. The lanes share one batch so
// the rail draws them as one fan-out, and each declares its paths so the
// supervisor refuses the overlap the split stage already checked for. A
// session without a supervisor, or a spawn it refuses, does not block the
// item: the session builds it whole and the step label says why.
// See docs/capabilities/todo.md#a-large-item-is-built-in-lanes.
func (m Model) startTodoFanOut() (tea.Model, tea.Cmd) {
	st, it := m.todoRunner.state, m.todoRunner.item
	if m.subagents == nil {
		return m.todoRunStep(st.NoLanes(it, "no agent supervisor; building in this session"))
	}
	// The split turn left the session read-only, and a child's mode is
	// clamped to its parent's: writers spawned now would be writers that
	// cannot write. The fan-out is the working stage.
	m.applyMode(agent.ModeAuto)
	m.subagents.BeginBatch()
	var spawned []string
	for _, lane := range st.Lanes {
		if lane.Done || lane.Agent == "" {
			continue
		}
		args, _ := json.Marshal(map[string]any{
			"role":  string(subagent.RoleWriter),
			"name":  lane.Agent,
			"task":  st.LaneTask(it, lane),
			"paths": lane.Paths,
		})
		if _, err := m.subagents.Spawn(args); err != nil {
			for _, name := range spawned {
				_ = m.subagents.Kill(name)
			}
			for i := range st.Lanes {
				st.Lanes[i].Agent = ""
			}
			model, _ := m.systemNotice(fmt.Sprintf("Lane %s could not be spawned — %s", lane.Name, err.Error()))
			return model.(Model).todoRunStep(st.NoLanes(it, "writers refused; building in this session"))
		}
		spawned = append(spawned, lane.Agent)
	}
	_ = st.Save(m.todos.Root)
	return m.systemNotice(fmt.Sprintf("▸ todo run %s · fan-out: %s", st.Slug, strings.Join(spawned, ", ")))
}

// todoLaneAsk is a routed approval from one of the run's writers. The
// patch is the run's own to take — the lanes were checked disjoint before
// they were spawned, and the tree is verified and reviewed after — so it
// is applied without a card; one the supervisor flags as overlapping an
// earlier patch is refused, and the run blocks on it. Anything else a
// writer asks — a command the classifier could not decide — goes to the
// person the way every child's ask does: that is the steering.
func (m Model) todoLaneAsk(ask *subagent.Ask) (tea.Model, tea.Cmd, bool) {
	st := m.todoRunner.state
	if st == nil || st.Over() || ask == nil || ask.Kind != subagent.AskPatch {
		return m, nil, false
	}
	lane, ok := st.LaneByAgent(ask.Agent)
	if !ok {
		return m, nil, false
	}
	if len(ask.Warnings) > 0 {
		ask.Respond(false)
		m.signal(observe.SignalRun, "lane-refused")
		next, cmd := m.todoRunStep(st.LaneFailed(ask.Agent, "its patch was refused: "+strings.Join(ask.Warnings, "; ")))
		return next, cmd, true
	}
	ask.Respond(true)
	model, _ := m.systemNotice(fmt.Sprintf("▸ todo run %s · lane %s: %s", st.Slug, lane.Name, ask.Title))
	return model, nil, true
}

// todoLanePatched is a writer's patch landing on the tree.
func (m Model) todoLanePatched(p *subagent.PatchApplied) {
	if st := m.todoRunner.state; st != nil && !st.Over() && p != nil {
		st.LanePatched(p.Agent)
		_ = st.Save(m.todos.Root)
	}
}

// todoWriterDone is a lane's writer finishing: its report goes on the lane
// and, when it is the last, the integration turn starts. A writer that
// did not finish blocks the run the way a failed reviewer does.
func (m Model) todoWriterDone(status subagent.Status) (tea.Model, tea.Cmd, bool) {
	st := m.todoRunner.state
	if st == nil || st.Over() {
		return m, nil, false
	}
	if _, ok := st.LaneByAgent(status.Name); !ok {
		return m, nil, false
	}
	report, state, ok := m.subagents.FinalReport(status.Name)
	if !ok || state != subagent.StateDone {
		report = status.Detail
	}
	next, cmd := m.todoRunStep(st.LaneDone(m.todoRunner.item, status.Name, ok && state == subagent.StateDone, report))
	return next, cmd, true
}
