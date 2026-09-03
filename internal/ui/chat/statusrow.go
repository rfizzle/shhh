package chat

// The status row (docs/interface/surfaces.md#the-inspector-rail). Below the
// width the surface splits at, the inspector rail is dropped rather than
// compressed — and dropping it took the reading of the session with it, on
// exactly the terminals that have the least room to go looking for one. This
// is the one row that stands in: what the session's last reading said, and
// what the turn or the session has changed.
//
// It is a row of the vitals kind, not a compressed rail. It carries two
// clauses, it degrades the way every vitals rail does — the rightmost clause
// of the highest drop rank goes first — and it is absent when there is
// nothing to say. The whole reading is still one command away, and asking
// for it is what forces a fresh one (summary.go).

import (
	"fmt"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// statusRow is the row itself, or "" when the surface has a rail to draw the
// same facts in, when the orchestrator is not the session on screen, or when
// neither half has anything to say.
func (m Model) statusRow() string {
	if m.attachedTo != "" || m.twoPane() {
		// Attached, the rail is still up wherever there is room for one and
		// it marks the session the keyboard is in (inspector.go). This row
		// is what a terminal with no room for the rail gets, and it has none
		// to spare for that mark either: an unmarked session-wide reading
		// beside an agent's own status bar would read as that agent's.
		return ""
	}
	var segs []components.RailSegment
	if verdict := m.statusVerdict(); verdict != "" {
		segs = append(segs, components.RailSegment{Text: verdict, Drop: components.RailVital})
	}
	if changed := m.statusChanges(); changed != "" {
		segs = append(segs, components.RailSegment{Text: changed, Drop: components.RailNormal})
	}
	if len(segs) == 0 {
		return ""
	}
	return components.FitRail(segs, sty.StatusBar.Render(" · "), m.contentWidth())
}

// statusVerdict is the SUMMARY block's two facts on one clause: the reading's
// judgement and the round it was taken at. A reading the session has outrun
// is marked stale here exactly as the block's heading marks it — an old
// reading is still the best there is, and this is where that is said.
func (m Model) statusVerdict() string {
	// The rail's own block is the one that decides whether there is a reading
	// to draw and how old it is; reading it here rather than the state behind
	// it is what keeps the row and the block from ever disagreeing.
	s := m.inspectorSummary()
	if s == nil {
		return ""
	}
	row := components.SummaryLabel(s.State)
	if s.Round > 0 {
		row += sty.StatusBar.Render(fmt.Sprintf(" · as of round %d", s.Round))
	}
	if s.Stale {
		row += sty.UpdateNotice.Render(" · stale")
	}
	return row
}

// statusChanges is the changed-file clause, and which question it answers
// depends on whether a turn is running. A live turn is asking "what is this
// one doing to my machine", so the clause is the turn's file count in the
// rail's own words; an idle session is asking what the whole session came to,
// so it is the session's net change. Both name their scope, because the two
// numbers would otherwise read as contradicting each other.
//
// A conversation has no changeset at all (conversation.go), so it gets no
// clause rather than a zero.
func (m Model) statusChanges() string {
	if !m.codingSurfaces() {
		return ""
	}
	if m.working() {
		turn, ok := m.changes.Turn(m.turnCount)
		if !ok {
			return ""
		}
		return sty.StatusBar.Render(plural(turn.Files(), "file") + " this turn")
	}
	files, added, removed := m.changes.Totals()
	if files == 0 {
		return ""
	}
	return sty.StatusBar.Render("session · ") + components.DiffStat(added, removed)
}
