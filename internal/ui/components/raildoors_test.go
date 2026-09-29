package components

import (
	"strings"
	"testing"
)

// everyBlockRail is a rail with every block on it and every list folding,
// so each block's heading is drawn and the lists draw a marker.
func everyBlockRail() InspectorRail {
	r := fullRail()
	r.Summary = &InspectorSummary{Text: "Reading the loop.", State: SummaryOnTarget, Round: 3}
	r.Plan = &InspectorPlan{Steps: []InspectorPlanStep{{Number: 1, Title: "read the loop"}}}
	r.Steps = &InspectorSteps{Done: 1, Total: 3, Current: "fix the cap"}
	r.Todo = &InspectorTodo{Open: 3, More: 2, Rows: []InspectorTodoRow{{Slug: "cache-ttl", Priority: "H", Grade: "M"}}}
	r.Tools = &InspectorTools{Up: 1, Sources: []InspectorToolSource{{Name: "github", Note: "12 tools"}}}
	for i := range inspectorChangesRows + 3 {
		r.Changes.Files = append(r.Changes.Files, InspectorFile{Path: strings.Repeat("x", i+1) + ".go", Added: 1})
	}
	return r
}

// railBlockNames is every block the rail can draw, by the name its heading
// carries.
var railBlockNames = []string{
	RailSummary, RailTurn, RailAlerts, RailPlan, RailSteps, RailTodo,
	RailChanges, RailAgents, RailTools, RailContext, RailSpend,
}

// A block whose heading has a surface behind it is a door: its heading and
// its fold marker point at the block, so a click on either can open the
// surface that holds the whole of it (docs/interface/surfaces.md#the-inspector-rail).
// Walking the blocks rather than the drawn rows is what catches a block that
// was built without going through the one place a door is attached.
func TestRailTargets_EveryBlockWithASurfaceIsADoor(t *testing.T) {
	r := everyBlockRail()
	r.Doors = map[string]bool{}
	for _, name := range railBlockNames {
		r.Doors[name] = true
	}
	blocks := r.blocks(InspectorWidth)
	if len(blocks) != len(railBlockNames) {
		t.Fatalf("the fixture draws %d blocks, want every one of the %d", len(blocks), len(railBlockNames))
	}
	for i, b := range blocks {
		// The rail folds a block short of its height the same way it does on
		// screen, so a block's own marker is asked for as well.
		b.hidden = append(b.hidden, railLine{text: "x", counted: true})
		rows := b.render(InspectorWidth)
		name := railBlockNames[i]
		want := RailTarget{Kind: RailTargetBlock, Name: name}
		if !strings.Contains(stripANSI(rows[0].Text), name) {
			t.Fatalf("block %d's heading reads %q, want %q", i, stripANSI(rows[0].Text), name)
		}
		if rows[0].Target != want {
			t.Errorf("the %s heading points at %+v, want %+v", name, rows[0].Target, want)
		}
		if last := rows[len(rows)-1]; last.Target != want {
			t.Errorf("the %s fold marker %q points at %+v, want %+v", name, stripANSI(last.Text), last.Target, want)
		}
	}

	// The rows that point at a thing keep pointing at it: a door is the
	// block's heading and marker, never a file or a session inside it.
	for _, row := range r.Rows(InspectorWidth, 0) {
		plain := stripANSI(row.Text)
		if strings.Contains(plain, "agent/loop.go") && row.Target.Kind != RailTargetFile {
			t.Fatalf("a file row became %+v", row.Target)
		}
		if strings.Contains(plain, "writer-1") && row.Target.Kind != RailTargetSession {
			t.Fatalf("a session row became %+v", row.Target)
		}
	}
}

// A block with no surface behind it keeps its heading and its markers inert
// — CHANGES's own marker and TODO's count of what the host left out among
// them — and a door the host named is carried by those rows and no others.
func TestRailTargets_ABlockWithNoSurfaceIsInert(t *testing.T) {
	r := everyBlockRail()
	r.Doors = map[string]bool{RailChanges: true}
	for _, row := range r.Rows(InspectorWidth, 0) {
		plain := stripANSI(row.Text)
		switch {
		case strings.Contains(plain, "CHANGES"), strings.Contains(plain, "… 5 more"):
			if want := (RailTarget{Kind: RailTargetBlock, Name: RailChanges}); row.Target != want {
				t.Fatalf("%q points at %+v, want %+v", plain, row.Target, want)
			}
		case row.Target.Kind == RailTargetBlock:
			t.Fatalf("%q is a door, but only CHANGES was named one", plain)
		}
	}
	// TODO's own count row is a marker in the host's words, and is the door
	// when the block is.
	r.Doors = map[string]bool{RailTodo: true}
	found := false
	for _, row := range r.Rows(InspectorWidth, 0) {
		if strings.Contains(stripANSI(row.Text), "… 2 more") {
			found = true
			if want := (RailTarget{Kind: RailTargetBlock, Name: RailTodo}); row.Target != want {
				t.Fatalf("TODO's count row points at %+v, want %+v", row.Target, want)
			}
		}
	}
	if !found {
		t.Fatalf("TODO drew no count row:\n%s", stripANSI(r.View(InspectorWidth, 0)))
	}
}
