package chat

// Fan-out lanes in the transcript (S-110, DESIGN-TUI.md §9g). A round that
// spawned one child keeps today's inline `◇ spawn` row; a round that spawned
// two or more turns those rows into a single block with a lane per child, so
// three agents read as three things rather than as one interleaved feed.
//
// The block stores nothing but the batch number. Everything it draws is read
// off the supervisor's live snapshot at render time, which is what lets the
// lanes update in place while the children run and re-render at any width
// after a resize — the same contract every other transcript entry keeps.

import (
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// fanoutBatch is the transcript's handle on one round's fan-out: the batch
// the supervisor stamped on the children that round spawned. It is a pointer
// on the entry so the block keeps its identity as the transcript is
// re-rendered.
type fanoutBatch struct{ batch int }

// beginSpawnBatch opens the supervisor's next spawn batch and forgets the
// last round's first spawn row. The parent front-end is the only thing that
// knows where a tool round begins, which is why the boundary is pushed down
// from here rather than guessed at in the supervisor.
func (m *Model) beginSpawnBatch() {
	m.spawnRow = 0
	if m.subagents != nil {
		m.subagents.BeginBatch()
	}
}

// appendSpawnEntry files the row a successful spawn produced. The first child
// of a round gets the ordinary activity row; the second turns that row into
// the round's fan-out block in place, and every child after it joins the
// block rather than adding a row of its own.
//
// The row is replaced rather than removed: transcript indices are what focus
// mode, the changeset and an approved plan's checklist all address entries
// by, so a fan-out must not shift them.
func (m *Model) appendSpawnEntry(e entry) {
	if m.subagents == nil {
		m.appendEntry(e)
		return
	}
	batch := m.subagents.Batch()
	if m.subagents.BatchSize(batch) < 2 {
		m.spawnRow = len(m.transcript) + 1
		m.appendEntry(e)
		return
	}
	if m.spawnRow > 0 {
		m.transcript[m.spawnRow-1] = entry{kind: entryFanout, fanout: &fanoutBatch{batch: batch}}
		m.spawnRow = 0
		// The replaced row may already be rendered, and the cache never
		// re-renders an entry it has seen.
		m.invalidateRenderCache()
	}
}

// fanoutStatuses is the batch's children, in spawn order.
func (m Model) fanoutStatuses(b *fanoutBatch) []subagent.Status {
	if b == nil || m.subagents == nil {
		return nil
	}
	var out []subagent.Status
	for _, st := range m.subagents.Snapshot() {
		if st.Batch == b.batch {
			out = append(out, st)
		}
	}
	return out
}

// fanoutLive reports whether any child of the entry's batch is still working.
// A block with a live child can never be frozen into the render cache — its
// lanes have to keep moving.
func (m Model) fanoutLive(e entry) bool {
	for _, st := range m.fanoutStatuses(e.fanout) {
		switch st.State {
		case subagent.StateDone, subagent.StateFailed:
		default:
			return true
		}
	}
	return false
}

// fanoutBlockFor builds the block for one entry from the live snapshot.
func (m Model) fanoutBlockFor(e entry) components.FanoutBlock {
	var block components.FanoutBlock
	var longest time.Duration
	for _, st := range m.fanoutStatuses(e.fanout) {
		lane := components.FanoutLane{
			Name:    st.Name,
			Task:    firstLine(st.Task),
			Step:    st.Step,
			Steps:   st.Steps,
			Tools:   st.ToolCalls,
			Spend:   m.childSpendLabel(st),
			Elapsed: turnDuration(st.Elapsed),
			Frame:   m.spinFrame,
		}
		switch st.State {
		case subagent.StateQueued:
			lane.State = components.FanoutQueued
		case subagent.StateBlocked:
			lane.State = components.FanoutBlocked
			lane.Waiting = st.Detail
		case subagent.StateIdle:
			lane.State = components.FanoutIdle
		case subagent.StateDone:
			lane.State = components.FanoutDone
			lane.Summary = firstLine(st.Summary)
		case subagent.StateFailed:
			lane.State = components.FanoutFailed
			// The lane already says "failed"; the note says why.
			lane.Summary = strings.TrimPrefix(st.Detail, "failed · ")
		default:
			lane.State = components.FanoutRunning
		}
		if st.Elapsed > longest {
			longest = st.Elapsed
		}
		block.Lanes = append(block.Lanes, lane)
	}
	// The batch's own span is its longest-lived child: a fan-out is over when
	// the last of them stops.
	block.Elapsed = turnDuration(longest)
	// The manager is where a blocked child is answered today, and it opens
	// mid-turn (S-087). Answering a lane in place is S-111's job.
	block.Keys = []components.TurnKey{{Key: "[ctrl+a]", Label: "agents"}}
	return block
}

// childSpendLabel prices a child's usage against the model it actually ran
// on. A fan-out is the one place where several models are billed at once —
// the orchestrator's price is the wrong one for a child the model sent to a
// cheaper one — so the lane asks the pricing table for the child's.
func (m Model) childSpendLabel(st subagent.Status) string {
	if st.TokensIn == 0 && st.TokensOut == 0 {
		return ""
	}
	if m.prices != nil && st.Model != "" {
		if in, out, found := m.prices.Cost(st.Model, st.TokensIn, st.TokensOut); found {
			return formatCost(in + out)
		}
	}
	return m.spendLabel(st.TokensIn, st.TokensOut)
}

// liveFanoutBlock is the index of the earliest transcript block holding a
// fan-out whose children are still working. renderHistory freezes everything
// before the last block rows can land in; a live fan-out is the one thing
// that keeps changing without any row landing at all, so nothing from there
// on may be frozen.
func (m Model) liveFanoutBlock(blocks []transcriptBlock) int {
	for i, blk := range blocks {
		start, end := blk.start, blk.end
		for j := start; j < end && j < len(m.transcript); j++ {
			if m.transcript[j].kind == entryFanout && m.fanoutLive(m.transcript[j]) {
				return i
			}
		}
	}
	return len(blocks)
}
