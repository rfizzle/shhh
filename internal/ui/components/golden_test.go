package components

// Golden-file render tests for the component catalog (S-096). The tests
// beside this one assert substrings, which is why a column that drifted by
// one cell has always been invisible to them; these capture the whole render
// at each of the width breakpoints, in colour and in mono, so drift is a
// failing test instead of something spotted three commits later.
//
// Regenerate after an intended change:
//
//	go test ./internal/ui/components ./internal/ui/chat -update-golden

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/muesli/termenv"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

func TestMain(m *testing.M) { os.Exit(golden.Run(m)) }

// goldenWidths are the width breakpoints from guidelines/layout-breakpoints
// in the shhh Design System project: minimal, folded, one-pane-with-vitals,
// and the two-pane split. Every surface is captured at all four so the drop
// order and the folding are covered rather than assumed.
var goldenWidths = []int{60, 80, 110, 130}

// captureGolden renders one surface at every width, in both palettes, and
// compares each against its checked-in file. panels is called per width
// because most of these surfaces lay themselves out differently as the
// terminal narrows — that is the whole point of capturing them.
func captureGolden(t *testing.T, name, surface string, widths []int, panels func(width int) []golden.Panel) {
	t.Helper()
	withColorProfile(t, termenv.ANSI256)
	for _, mono := range []bool{false, true} {
		label := "color"
		if mono {
			label = "mono"
		}
		t.Run(label, func(t *testing.T) {
			was := Mono()
			SetMono(mono)
			t.Cleanup(func() { SetMono(was) })
			for _, width := range widths {
				golden.Assert(t, widthName(name, width), golden.Case{
					Surface: surface,
					Width:   width,
					Mono:    mono,
					Panels:  panels(width),
				})
			}
		})
	}
}

// widthName suffixes a golden with the width it was taken at, so the four
// captures of a surface sort together in the directory listing.
func widthName(name string, width int) string {
	return name + ".w" + strconv.Itoa(width)
}

// goldenHunks is the fixture edit every diff surface renders: one hunk with a
// deletion, two additions and surrounding context, long enough that the
// full-screen mode has something to scroll and short enough to read in a
// diff.
func goldenHunks() []diff.Hunk {
	return []diff.Hunk{{
		OldStart: 138, OldCount: 6, NewStart: 138, NewCount: 7,
		Lines: []diff.Line{
			{Kind: diff.Context, Text: "\tfor round := 0; ; round++ {", OldNo: 138, NewNo: 138},
			{Kind: diff.Context, Text: "\t\tif round >= a.maxRounds {", OldNo: 139, NewNo: 139},
			{Kind: diff.Del, Text: "\t\t\treturn results, nil", OldNo: 140},
			{Kind: diff.Add, Text: "\t\t\treturn results, ErrRoundLimit", NewNo: 140},
			{Kind: diff.Add, Text: "\t\t}", NewNo: 141},
			{Kind: diff.Context, Text: "\t\t}", OldNo: 141, NewNo: 142},
			{Kind: diff.Context, Text: "\t}", OldNo: 142, NewNo: 143},
		},
	}}
}

// TestGolden_ActivityRows captures every activity row kind and state on one
// sheet (§6b, §6d). Everything but the state is held constant, so the file
// reads as a table of what each state contributes.
func TestGolden_ActivityRows(t *testing.T) {
	captureGolden(t, "activity-rows", "activity row grammar", goldenWidths, func(width int) []golden.Panel {
		row := func(mut func(*ActivityRow)) string {
			r := ActivityRow{Kind: ActivityTool, Verb: "read", Target: "internal/agent/loop.go"}
			mut(&r)
			return r.View(width)
		}
		return []golden.Panel{
			{Label: "kind · tool (read-only, no rail)", View: row(func(r *ActivityRow) {
				r.Counts, r.Duration = "218 lines", "0.6s"
			})},
			{Label: "kind · command", View: row(func(r *ActivityRow) {
				r.Kind, r.Verb, r.Target = ActivityCommand, "run", "go test ./internal/agent/..."
				r.Outcome, r.Duration = OutcomeExit(0), "12.4s"
			})},
			{Label: "kind · edit", View: row(func(r *ActivityRow) {
				r.Kind, r.Verb = ActivityEdit, "edit"
				r.Counts, r.Outcome, r.Duration = "+12 −4 · 2 hunks", OutcomeBy(OutcomeApproved, "you"), "1.1s"
			})},
			{Label: "kind · sub-agent", View: row(func(r *ActivityRow) {
				r.Kind, r.Verb, r.Target = ActivitySubagent, "agent", "writer-1 · docs/loop.md"
				r.Outcome, r.Duration = OutcomeOK, "48.0s"
			})},
			{Label: "state · queued", View: row(func(r *ActivityRow) {
				r.State, r.Outcome, r.Duration = ActivityQueued, OutcomeQueued, NoDuration
			})},
			{Label: "state · running with live tail", View: row(func(r *ActivityRow) {
				r.Kind, r.Verb, r.Target = ActivityCommand, "run", "go build ./cmd/shhh"
				r.State, r.Outcome = ActivityRunning, OutcomeRunning
				r.Tail = "internal/ui/chat/model.go:1660:1: too many arguments"
			})},
			{Label: "state · classifier checking", View: row(func(r *ActivityRow) {
				r.State, r.Outcome = ActivityChecking, OutcomeChecking
			})},
			{Label: "state · failed, expanded", View: row(func(r *ActivityRow) {
				r.Kind, r.Verb, r.Target = ActivityCommand, "run", "go test ./internal/agent/..."
				r.State, r.Outcome, r.Duration = ActivityFailed, OutcomeExit(1), "21.4s"
				r.Expanded = true
				r.Detail = []string{"--- FAIL: TestRoundLimit (0.00s)", "    loop_test.go:88: want ErrRoundLimit, got nil"}
			})},
			{Label: "state · denied by you", View: row(func(r *ActivityRow) {
				r.Kind, r.Verb, r.Target = ActivityCommand, "run", "rm -rf ./build"
				r.State, r.Outcome, r.Duration = ActivityDenied, OutcomeBy(OutcomeDenied, "you"), NoDuration
			})},
			{Label: "state · denied by a rule", View: row(func(r *ActivityRow) {
				r.Kind, r.Verb, r.Target = ActivityCommand, "run", "rm -rf ./build"
				r.State, r.ByRule = ActivityDenied, true
				r.Outcome, r.Duration = OutcomeBy(OutcomeDenied, "auto · plan mode"), NoDuration
				r.Keys = "/mode why"
			})},
			{Label: "state · auto-allowed", View: row(func(r *ActivityRow) {
				r.Outcome, r.Counts, r.Duration = OutcomeBy(OutcomeAutoAllowed, "read-only"), "218 lines", "0.6s"
			})},
			{Label: "focus · selected", View: row(func(r *ActivityRow) {
				r.Selected, r.Counts, r.Duration = true, "218 lines", "0.6s"
			})},
			{Label: "overflow · target clips, outcome does not", View: row(func(r *ActivityRow) {
				r.Kind, r.Verb = ActivityEdit, "edit"
				r.Target = "internal/ui/components/a/very/deeply/nested/path/activityrow.go"
				r.Counts, r.Duration = "+128 −64 · 9 hunks", "3.2s"
			})},
		}
	})
}

// TestGolden_ApprovalCard captures the decision surface's variants (§4): the
// command card plain and flagged, the edit card carrying its diff, and the
// generic card.
// TestGolden_TurnClose captures the rows a turn ends with (§16, S-098): the
// three-row close, the one-row close of a turn that changed nothing, and the
// two ways a turn can stop early.
func TestGolden_TurnClose(t *testing.T) {
	captureGolden(t, "turn-close", "turn close rows", goldenWidths, func(width int) []golden.Panel {
		closed := func(mut func(*TurnClose)) string {
			c := TurnClose{
				Steps: 4, Tools: 18, Elapsed: "1m 04s", Spend: "$0.14", Note: "round 7/25",
				Changes: &TurnChanges{
					Files: 3, Added: 30, Removed: 4,
					Keys: []TurnKey{{Key: "[v]", Label: "review"}, {Key: "[u]", Label: "undo turn"}},
					Note: "all tracked in git",
				},
				Checks: &TurnChecks{Label: "go test ./internal/agent/...", Counts: "41 packages · 12.8s"},
			}
			mut(&c)
			return c.View(width)
		}
		return []golden.Panel{
			{Label: "done · changed · checked", View: closed(func(c *TurnClose) {})},
			{Label: "done · checks failing", View: closed(func(c *TurnClose) { c.Checks.Failed = true })},
			{Label: "done · nothing changed", View: closed(func(c *TurnClose) {
				c.Changes, c.Checks = nil, nil
			})},
			{Label: "cancelled · reports what it changed before stopping", View: closed(func(c *TurnClose) {
				c.State, c.Checks = TurnCancelled, nil
				c.Steps, c.Tools, c.Elapsed = 2, 5, "8.1s"
			})},
			{Label: "failed · the stream broke", View: closed(func(c *TurnClose) {
				c.State, c.Changes, c.Checks = TurnFailed, nil, nil
				c.Steps, c.Tools, c.Elapsed, c.Spend = 1, 2, "3.4s", "~1.2k tok"
			})},
			{Label: "unpriced · tokens, never a made-up zero", View: closed(func(c *TurnClose) {
				c.Spend, c.Changes, c.Checks = "~48.1k tok", nil, nil
			})},
		}
	})
}

func TestGolden_ApprovalCard(t *testing.T) {
	captureGolden(t, "approval-card", "approval card", goldenWidths, func(width int) []golden.Panel {
		card := func(mut func(*ApprovalCard)) string {
			c := ApprovalCard{
				Variant:  ApprovalCommand,
				Title:    "Approve command",
				Headline: "Assistant wants to run: go test ./internal/agent/...",
				Question: "Run this command?",
			}
			mut(&c)
			return c.View(width)
		}
		return []golden.Panel{
			{Label: "variant · command, always-allow offered", View: card(func(c *ApprovalCard) {
				c.AllowAlways, c.AlwaysHint = true, "a: always allow commands this session"
			})},
			{Label: "variant · command, flagged and contained", View: card(func(c *ApprovalCard) {
				c.QueuePos = "2 of 5"
				c.Headline = "Assistant wants to run: rm -rf ./build"
				c.Warnings = []string{"deletes files recursively (rm -rf)"}
				c.Containment = "contained · workspace profile · network on"
			})},
			{Label: "variant · edit, diff body", View: card(func(c *ApprovalCard) {
				c.Variant, c.Title = ApprovalEdit, "Approve edit"
				c.Headline = "Assistant wants to edit: internal/agent/loop.go"
				c.Question = "Apply this edit?"
				c.Hunks, c.FullDiff = goldenHunks(), true
			})},
			{Label: "variant · generic", View: card(func(c *ApprovalCard) {
				c.Variant, c.Title = ApprovalGeneric, "Approve tool"
				c.Headline = "Assistant wants to use: web_fetch"
				c.Summary = "fetch https://example.com/spec"
				c.Question = "Allow this call?"
			})},
		}
	})
}

// TestGolden_DiffView captures the viewer's three modes (§3): the transcript
// row, the bounded in-transcript body, and the full-screen view.
func TestGolden_DiffView(t *testing.T) {
	captureGolden(t, "diff-view", "diff viewer", goldenWidths, func(width int) []golden.Panel {
		view := func(mode DiffMode, mut func(*DiffView)) string {
			d := &DiffView{
				Path: "internal/agent/loop.go", Verb: "edit",
				Hunks: goldenHunks(), Mode: mode, Height: 14,
			}
			if mut != nil {
				mut(d)
			}
			return d.View(width)
		}
		return []golden.Panel{
			{Label: "mode · collapsed (transcript row)", View: view(DiffCollapsed, nil)},
			{Label: "mode · expanded (bounded body)", View: view(DiffExpanded, func(d *DiffView) { d.MaxLines = 12 })},
			{Label: "mode · full screen", View: view(DiffFull, nil)},
		}
	})
}

// TestGolden_AgentList captures the sub-agent manager (§9a) with every lane
// state present, and with the focus on the child that is waiting on an
// answer.
func TestGolden_AgentList(t *testing.T) {
	captureGolden(t, "agent-list", "agent list", goldenWidths, func(width int) []golden.Panel {
		rows := []AgentRow{
			{State: AgentCurrent, Name: "orchestrator", Task: "this session", Status: "working", Spend: "$0.12"},
			{State: AgentRunning, Name: "writer-1", Task: "docs/loop.md", Status: "4 tools · 48s", Spend: "$0.02"},
			{State: AgentBlocked, Name: "runner-2", Task: "go test ./...", Status: "waiting on you", Spend: "$0.01"},
			{State: AgentDone, Name: "reader-3", Task: "survey internal/ui", Status: "12 tools · 1m 20s", Spend: "$0.04"},
			{State: AgentFailed, Name: "patcher-4", Task: "apply patch", Status: "exit 1", Spend: "$0.00"},
		}
		return []golden.Panel{
			{Label: "focus · the current agent", View: (&AgentList{Rows: rows}).View(width)},
			{Label: "focus · the blocked child", View: (&AgentList{Rows: rows, Focus: 2}).View(width)},
		}
	})
}

// TestGolden_InspectorRail captures the rail (§15). Its width is fixed at
// InspectorWidth — it exists only in the two-pane layout and never renders at
// another size — so the axis worth capturing is which blocks are present and
// what a height too short to hold them all drops.
func TestGolden_InspectorRail(t *testing.T) {
	captureGolden(t, "inspector-rail", "inspector rail", []int{InspectorWidth}, func(width int) []golden.Panel {
		full := InspectorRail{
			Turn: &InspectorTurn{Step: 3, Steps: 4, Tools: 18, Elapsed: 64 * time.Second, Running: true},
			Changes: &InspectorChanges{
				Files: []InspectorFile{
					{Path: "internal/agent/loop.go", Added: 18, Removed: 3},
					{Path: "internal/ui/chat/model.go", Added: 9, Removed: 1},
				},
				Added: 27, Removed: 4,
				Failure: "go test ./internal/agent/...", FailureNote: OutcomeExit(1),
			},
			Agents: []InspectorAgent{
				{Name: "writer-1", Detail: "docs/loop.md", Spend: "$0.02", Tools: 4},
				{Name: "runner-2", Detail: "go test ./...", Spend: "$0.01", Step: 2, Steps: 3, Blocked: true},
			},
			Context: &InspectorContext{
				Pct: 62, Tokens: 124000, Window: 200000,
				Tokens1: "↑41.2k", Tokens2: "↓9.8k",
				Burn: []float64{1, 2, 3, 3, 4, 5, 5, 6},
			},
			Spend: &InspectorSpend{Turn: "$0.14", Main: "$0.12", Children: "$0.02",
				Session: "$1.86", Model: "gpt-5.2"},
		}
		quiet := InspectorRail{
			Turn:    &InspectorTurn{Tools: 2, Elapsed: 3 * time.Second, Running: true},
			Context: &InspectorContext{Pct: 41, Tokens: 82000, Window: 200000, Estimated: true},
		}
		return []golden.Panel{
			{Label: "every block, unbounded height", View: full.View(width, 0)},
			{Label: "every block, height 16 (truncating)", View: full.View(width, 16)},
			{Label: "blocks with nothing to say are omitted", View: quiet.View(width, 0)},
		}
	})
}
