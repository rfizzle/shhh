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
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// TestGolden_TurnStatus captures the running turn's status line (§8d, S-118):
// the four phases, the fields ticking, the collapse ladder as the slot
// narrows, and the three ways it resolves. The captures are taken at the
// §8c widths, which is where the ladder is visible — the slot is what is
// left of the frame's top rail after the identity, so the narrow captures
// are the drop order rather than a copy of the wide ones.
func TestGolden_TurnStatus(t *testing.T) {
	captureGolden(t, "turn-status", "running turn status", goldenWidths, func(width int) []golden.Panel {
		live := func(mut func(*TurnStatus)) string {
			s := TurnStatus{
				Phase: PhaseRunning, Tool: "go test ./internal/agent/...",
				Elapsed: "12.4s", Up: "41.2k", Down: "2.1k", Cost: "$0.06",
			}
			mut(&s)
			return s.View(width)
		}
		done := func(mut func(*TurnStatus)) string {
			s := TurnStatus{Done: true, Duration: "1m 04s", Tools: 18, Cost: "$0.14"}
			mut(&s)
			return s.View(width)
		}
		return []golden.Panel{
			{Label: "phase · thinking", View: live(func(s *TurnStatus) {
				s.Phase, s.Tool, s.Elapsed = PhaseThinking, "", "4.2s"
			})},
			{Label: "phase · deciding", View: live(func(s *TurnStatus) {
				s.Phase, s.Tool, s.Elapsed = PhaseDeciding, "", "0.8s"
			})},
			{Label: "phase · running, named", View: live(func(s *TurnStatus) {})},
			{Label: "phase · streaming", View: live(func(s *TurnStatus) {
				s.Phase, s.Tool = PhaseStreaming, ""
			})},
			{Label: "unpriced · tokens, never a made-up zero", View: live(func(s *TurnStatus) {
				s.Cost = "~43.3k tok"
			})},
			{Label: "resolved · done", View: done(func(s *TurnStatus) {})},
			{Label: "resolved · cancelled", View: done(func(s *TurnStatus) {
				s.Outcome, s.Tools, s.Duration = TurnCancelled, 5, "8.1s"
			})},
			{Label: "resolved · failed", View: done(func(s *TurnStatus) {
				s.Outcome, s.Tools, s.Duration, s.Cost = TurnFailed, 2, "3.4s", "~1.2k tok"
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
			{Label: "variant · command, a batch waiting behind it", View: card(func(c *ApprovalCard) {
				c.QueuePos = "1 of 5"
				c.AllowAlways, c.AlwaysHint = true, "a: always allow commands this session"
				c.Batch, c.BatchHint = true, "A: approve 3 like this"
				c.Severity = SeverityLow
			})},
			{Label: "variant · command, flagged, contained, blast radius", View: card(func(c *ApprovalCard) {
				c.QueuePos = "2 of 5"
				c.Headline = "Assistant wants to run: rm -rf ./build && npm run build"
				c.Severity = SeverityHigh
				c.Warnings = []string{"deletes files recursively (rm -rf)"}
				c.Chip = "⛨ bwrap · workspace"
				c.Fields = []CardField{
					{Label: "touches", Value: "./build", Detail: "412 files, 84.0 MB; shhh cannot tell what npm writes"},
					{Label: "undo", Value: "none", Detail: "nothing it writes is tracked in git", Tone: ToneRisk},
					{Label: "network", Value: "open", Detail: "the workspace profile allows network access", Tone: ToneOpen},
				}
				c.SafeDefault = "[n] deny — the safe answer"
				c.Footnote = "[a] always — not offered: a safety-flagged command is never pre-approved"
				c.Return = "[esc] back to your draft — the decision stays waiting, nothing is denied"
			})},
			{Label: "variant · command, uncontained", View: card(func(c *ApprovalCard) {
				c.Headline = "Assistant wants to run: curl -fsSL https://get.pnpm.io/install.sh | sh"
				c.Severity, c.Uncontained = SeverityMedium, true
				c.Fields = []CardField{
					{Label: "touches", Value: "unknown", Detail: "piped into sh; what it runs is not inspected first", Tone: ToneRisk},
					{Label: "undo", Value: "unknown", Detail: "shhh could not resolve what this writes", Tone: ToneRisk},
					{Label: "network", Value: "open", Detail: "nothing contains this command, so nothing limits what it reaches", Tone: ToneOpen},
					{Label: "⛨", Value: "no sandbox", Detail: "bubblewrap (bwrap) not found on PATH; the command runs as you", Tone: ToneRisk},
				}
				c.SafeDefault = "[n] deny — the safe answer"
				c.Footnote = "containment is off for this session · /sandbox doctor explains why"
				c.Return = "[esc] back to your draft — the decision stays waiting, nothing is denied"
			})},
			{Label: "variant · edit, diff body", View: card(func(c *ApprovalCard) {
				c.Variant, c.Title = ApprovalEdit, "Approve edit"
				c.Headline = "Assistant wants to edit: internal/agent/loop.go"
				c.Question = "Apply this edit?"
				c.Severity = SeverityMedium
				c.Hunks, c.FullDiff = goldenHunks(), true
				c.Reversibility = "undo yes — recorded, and git has this file"
			})},
			// The state every card is in the moment it appears beside a live
			// draft (§7b, S-117): the decision keys not yet live, and the one
			// key that hands the keyboard over offered under them.
			{Label: "state · not yet live, beside a draft that has the keyboard", View: card(func(c *ApprovalCard) {
				c.Variant, c.Title = ApprovalEdit, "Approve edit"
				c.Headline = "Assistant wants to edit: internal/agent/loop.go"
				c.Question = "Apply this edit?"
				c.Severity = SeverityMedium
				c.Hunks, c.FullDiff = goldenHunks(), true
				c.AllowAlways, c.AlwaysHint = true, "a: always allow edits"
				c.NotYetLive, c.Handover = true, "ctrl+g"
			})},
			{Label: "variant · generic", View: card(func(c *ApprovalCard) {
				c.Variant, c.Title = ApprovalGeneric, "Approve tool"
				c.Headline = "Assistant wants to use: web_fetch"
				c.Summary = "GET https://pkg.go.dev/context#WithCancel"
				c.Question = "Allow this call?"
				c.Severity = SeverityLow
				c.Fields = []CardField{
					{Label: "domain", Value: "pkg.go.dev", Detail: "the request leaves this machine", Tone: ToneOpen},
					{Label: "sends", Value: "the URL and a shhh-web/1.0 user-agent", Detail: "no file contents, no credentials"},
					{Label: "receives", Value: "page text into the conversation, bounded to 2 MB", Detail: "it counts against the context window"},
				}
			})},
		}
	})
}

// TestGolden_QueueStrip captures the stack above the card (§2e): the full
// list, the bounded list with its overflow count, and a queue with no batch
// in it at all.
func TestGolden_QueueStrip(t *testing.T) {
	captureGolden(t, "queue-strip", "approval queue strip", goldenWidths, func(width int) []golden.Panel {
		items := []QueueItem{
			{Number: 1, Label: "go test ./internal/agent/...", Severity: SeverityLow, Batch: true},
			{Number: 2, Label: "npm run build", Severity: SeverityLow, Batch: true},
			{Number: 3, Label: "edit internal/ui/chat/model.go", Detail: "+9 −1", Severity: SeverityMedium},
			{Number: 4, Label: "rm -rf ./dist", Severity: SeverityHigh},
			{Number: 5, Label: "write docs/loop.md", Detail: "+12 −0", Severity: SeverityMedium},
		}
		strip := func(mut func(*QueueStrip)) string {
			q := QueueStrip{Items: append([]QueueItem(nil), items...), Note: "[A] answers the 3 marked"}
			mut(&q)
			return strings.Join(q.View(width), "\n")
		}
		return []golden.Panel{
			{Label: "five pending · two behind the current one join the batch", View: strip(func(q *QueueStrip) {})},
			{Label: "bounded · the rest are counted, not dropped", View: strip(func(q *QueueStrip) {
				q.MaxRows = 3
				q.Items[4].Batch = true
			})},
			{Label: "no batch · nothing behind it is the same kind", View: strip(func(q *QueueStrip) {
				q.Items[0].Batch, q.Items[1].Batch, q.Note = false, false, ""
			})},
		}
	})
}

// listWidths are the two the Lists artboard is drawn at: the card at its
// working width, and the same list on a terminal narrow enough that the rows
// have to give something up.
var listWidths = []int{60, 110}

// goldenCatalog is the 24-entry model list the Lists artboard uses, long
// enough that every card below it has to window.
func goldenCatalog() []SelectOption {
	names := []string{
		"gpt-5.2", "gpt-5.2-mini", "gpt-5.1", "gpt-5.1-mini", "o4-mini",
		"claude-opus-4.6", "claude-sonnet-4.6", "claude-sonnet-4.5",
		"claude-haiku-4.5", "gemini-3-pro", "gemini-3-flash", "grok-4.1",
		"deepseek-r2", "deepseek-v4", "qwen3-coder-72b", "llama-4-maverick",
		"llama-3.3-70b", "mistral-large-3", "phi-5-mini", "command-r-plus",
		"nova-pro-2", "jamba-2", "yi-2-34b", "solar-pro-3",
	}
	opts := make([]SelectOption, 0, len(names))
	for _, n := range names {
		opts = append(opts, SelectOption{Label: n})
	}
	return opts
}

// TestGolden_Lists captures the window and the filter row over it (§4a,
// ui_kits/cockpit/Lists.html): the window at the head of a list, mid-list
// where both markers count, and at its tail, then the same card with a query
// on it and the card a query matched nothing on.
//
// The three window panels are walked to rather than positioned, because the
// window is path-dependent on purpose: an option reached from above sits at
// the foot of the window and the same option reached from below sits at its
// head, and that is exactly what a fixed Focus would not capture.
func TestGolden_Lists(t *testing.T) {
	captureGolden(t, "lists", "list windowing and the filter row", listWidths, func(width int) []golden.Panel {
		walked := func(steps int, k string) string {
			s := &Select{Title: "Switch model", Options: goldenCatalog(), MaxLines: 11, Filterable: true}
			if k == "up" {
				s.Focus = len(s.Options) - 1
			}
			for i := 0; i < steps; i++ {
				s.View(width)
				s.Update(key(k))
			}
			return s.View(width)
		}
		filtered := func(query string, mut func(*Select)) string {
			var matches []SelectOption
			for _, o := range goldenCatalog() {
				if strings.Contains(o.Label, query) {
					matches = append(matches, o)
				}
			}
			s := &Select{
				Title: "Switch model", Options: matches, MaxLines: 11,
				Filterable: true, Filtering: true, Query: query, Total: 24,
			}
			if mut != nil {
				mut(s)
			}
			return s.View(width)
		}
		return []golden.Panel{
			{Label: "the head · nothing is hidden above it", View: walked(0, "down")},
			{Label: "mid-list from above · the option sits at the foot of the window", View: walked(12, "down")},
			{Label: "the same option from below · it sits at the head instead", View: walked(11, "up")},
			{Label: "the tail · nothing is hidden below it", View: walked(23, "down")},
			{Label: "filtered · the run is bold, the row states both counts", View: filtered("mini", nil)},
			{Label: "no match · a row, not an empty pane", View: filtered("sonnet-5", func(s *Select) {
				s.Closest = "claude-sonnet-4.6"
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

// TestGolden_ReviewMode captures the review surface (§16a, S-099): the two
// panes at the widths that reach them, the stacked layout below 60 columns,
// and the paired hunks the wide widths switch to automatically.
func TestGolden_ReviewMode(t *testing.T) {
	// 56 is the 60-column breakpoint less the surface's horizontal padding —
	// what a minimal terminal actually hands the component, and the only one
	// of these widths that reaches the stacked layout.
	reviewWidths := append([]int{56}, goldenWidths...)
	captureGolden(t, "review-mode", "review mode", reviewWidths, func(width int) []golden.Panel {
		view := func(mut func(*ReviewView)) string {
			v := &ReviewView{
				Title: "turn 7",
				Files: []ReviewFile{
					{Path: "internal/agent/loop.go", Hunks: goldenHunks(), Staged: []bool{true}},
					{Path: "internal/ui/chat/model.go", Hunks: goldenHunks(), Staged: []bool{false}},
					{Path: "internal/agent/errors.go", Hunks: goldenHunks(), Staged: []bool{false}, Agent: "writer-1"},
				},
				Verdict: &ReviewVerdict{
					Failed: true, Label: "go test ./internal/agent/... · exit 1",
					Detail: []string{"--- FAIL: TestRoundLimit (0.03s)", "loop_test.go:142"},
				},
				Shield:       "nothing is committed",
				ShieldDetail: "undo restores the 3 files this turn wrote",
				ApplyVerb:    "undo",
				Height:       18,
			}
			if mut != nil {
				mut(v)
			}
			return v.View(width)
		}
		return []golden.Panel{
			{Label: "staging · one of three files staged", View: view(nil)},
			{Label: "staging · everything staged, second file focused", View: view(func(v *ReviewView) {
				v.Update(key("A"))
				v.Update(key("j"))
			})},
			{Label: "layout · side-by-side forced", View: view(func(v *ReviewView) { v.SideBySide = true })},
			{Label: "read-only (a cumulative diff has nothing to stage)", View: view(func(v *ReviewView) {
				v.ReadOnly = true
			})},
		}
	})
}

// TestGolden_AgentList captures the sub-agent manager (§9a) with every row
// state present, the blocked child sorted to the top below the orchestrator,
// and the focus moved across the three rows whose offers differ: the
// orchestrator (no child progress at all), the blocked child ([a] answers it
// here) and the failed one ([r] runs it again). The progress is the fan-out
// lane's renderer, so the capture is also where the two surfaces are held to
// the same columns.
func TestGolden_AgentList(t *testing.T) {
	captureGolden(t, "agent-list", "agent list", goldenWidths, func(width int) []golden.Panel {
		progress := func(p AgentProgress) *AgentProgress { return &p }
		rows := []AgentRow{
			{State: AgentCurrent, Name: "orchestrator", Task: "this session", Status: "round 7 · streaming…", Spend: "$0.12"},
			{State: AgentBlocked, Name: "runner-2", Task: "go test ./...", Answerable: true,
				Progress: progress(AgentProgress{State: FanoutBlocked, Tools: 3, Spend: "$0.01"}),
				Note:     "waiting approval: run go test ./internal/agent/..."},
			{State: AgentRunning, Name: "writer-1", Task: "docs/loop.md",
				Progress: progress(AgentProgress{State: FanoutRunning, Step: 2, Steps: 5, Tools: 6, Spend: "$0.02"})},
			{State: AgentDone, Name: "reader-3", Task: "survey internal/ui",
				Progress: progress(AgentProgress{State: FanoutDone, Tools: 12, Spend: "$0.04"}),
				Note:     "the rails and the frame are one component"},
			{State: AgentFailed, Name: "patcher-4", Task: "apply patch", Retryable: true,
				Progress: progress(AgentProgress{State: FanoutFailed, Tools: 1, Spend: "$0.01"}),
				Note:     "round limit (25) reached"},
		}
		return []golden.Panel{
			{Label: "focus · the orchestrator", View: (&AgentList{Rows: rows}).View(width)},
			{Label: "focus · the blocked child, [a] answers it here", View: (&AgentList{Rows: rows, Focus: 1}).View(width)},
			{Label: "focus · the failed child, [r] runs it again", View: (&AgentList{Rows: rows, Focus: 4}).View(width)},
		}
	})
}

// TestGolden_FanoutBlock captures the fan-out block (§9g, S-110) in the three
// readings that matter: a batch mid-flight with one child waiting on an
// answer, a batch where every child has stopped, and a batch nobody declared
// a step count for, where every lane spins instead of drawing a ratio. The
// spinner frame is fixed so the capture is about layout rather than about
// when the test ran.
func TestGolden_FanoutBlock(t *testing.T) {
	captureGolden(t, "fanout-block", "fan-out block", goldenWidths, func(width int) []golden.Panel {
		flight := FanoutBlock{
			Elapsed: "1m12s",
			Keys:    []TurnKey{{Key: "[ctrl+a]", Label: "agents"}},
			Lanes: []FanoutLane{
				{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md",
					Step: 2, Steps: 5, Tools: 6, Spend: "$0.02", Elapsed: "12s"},
				{State: FanoutDone, Name: "tester-2", Task: "internal/agent tests",
					Tools: 9, Spend: "$0.03", Elapsed: "41s", Summary: "all four packages pass"},
				{State: FanoutBlocked, Name: "scout-3", Task: "other ErrRoundLimit callers",
					Tools: 3, Spend: "$0.01", Elapsed: "18s",
					Waiting: "waiting approval: read ../shhh-plugins/registry.go"},
				{State: FanoutQueued, Name: "reader-4", Task: "survey internal/ui", Elapsed: "1.0s"},
			},
		}
		settled := FanoutBlock{
			Elapsed: "2m04s",
			Lanes: []FanoutLane{
				{State: FanoutDone, Name: "writer-1", Task: "docs/loop.md",
					Tools: 11, Spend: "$0.04", Elapsed: "1m38s", Summary: "documented the sentinel and linked the test"},
				{State: FanoutDone, Name: "tester-2", Task: "internal/agent tests",
					Tools: 9, Spend: "$0.03", Elapsed: "41s", Summary: "all four packages pass"},
				{State: FanoutFailed, Name: "patcher-3", Task: "apply the patch",
					Tools: 12, Spend: "$0.05", Elapsed: "2m04s", Summary: "round limit (25) reached"},
			},
		}
		spinning := FanoutBlock{
			Elapsed: "22s",
			Lanes: []FanoutLane{
				{State: FanoutRunning, Name: "researcher-1", Task: "survey the round accounting",
					Tools: 4, Spend: "$0.01", Elapsed: "22s", Frame: 2},
				{State: FanoutRunning, Name: "researcher-2", Task: "survey the fold state",
					Tools: 2, Spend: "$0.01", Elapsed: "19s", Frame: 2},
			},
		}
		return []golden.Panel{
			{Label: "mid-flight · one child is waiting on you", View: flight.View(width)},
			{Label: "settled · every child has stopped", View: settled.View(width)},
			{Label: "no declared step count · every lane spins", View: spinning.View(width)},
		}
	})
}

// TestGolden_InspectorRail captures the rail (§15). Its width is fixed at
// InspectorWidth — it exists only in the two-pane layout and never renders at
// another size — so the axis worth capturing is which blocks are present and
// what a height too short to hold them all drops. The full rail carries an
// approved plan's PLAN checklist (S-104), which is also what gives THIS TURN's
// meter a denominator.
func TestGolden_InspectorRail(t *testing.T) {
	captureGolden(t, "inspector-rail", "inspector rail", []int{InspectorWidth}, func(width int) []golden.Panel {
		full := InspectorRail{
			Turn: &InspectorTurn{Step: 3, Steps: 4, Tools: 18, Elapsed: 64 * time.Second, Running: true,
				Files: 3, Added: 30, Removed: 4},
			Plan: &InspectorPlan{
				Steps: []InspectorPlanStep{
					{Number: 1, Title: "Locate the round accounting", State: PlanStepDone, Elapsed: "6.2s"},
					{Number: 2, Title: "Add a RoundsExhausted sentinel", State: PlanStepFailed, Elapsed: "38.1s"},
					{Number: 3, Title: "Return it from runRound", State: PlanStepRunning},
					{Number: 4, Title: "Offer more rounds in the chat model", State: PlanStepQueued},
				},
				Done: 2, Drift: "1 off plan", Hint: "/plan for the whole list",
			},
			Changes: &InspectorChanges{
				Files: []InspectorFile{
					{Path: "internal/agent/loop.go", Added: 18, Removed: 3, Turns: 3, ThisTurn: true},
					{Path: "internal/ui/chat/model.go", Added: 9, Removed: 1, ThisTurn: true},
				},
				Added: 27, Removed: 4,
				Alerts: []InspectorAlert{
					{Label: "go test ./internal/agent/...", Note: OutcomeExit(1), Turn: 7},
				},
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
		// Four turns deep, with the rail shorter than the list it has to show
		// (S-120, §15a): repeat edits collapse to one row carrying their turn
		// count, the rows this turn wrote survive the fold, the fold carries
		// the counts it took, and an alert from turn 7 is still standing in
		// turn 9 because the workspace is still broken.
		session := InspectorRail{
			Turn: &InspectorTurn{Step: 1, Steps: 3, Running: true, Elapsed: 8 * time.Second},
			Changes: &InspectorChanges{
				Files: []InspectorFile{
					{Path: "internal/agent/loop.go", Added: 21, Removed: 4, Turns: 3, ThisTurn: true},
					{Path: "internal/agent/loop_test.go", Added: 18, Turns: 2},
					{Path: "internal/ui/chat/model.go", Added: 9, Removed: 1, ThisTurn: true},
					{Path: "internal/agent/round.go", Added: 6, Removed: 2},
					{Path: "internal/tool/exec.go", Added: 4, Removed: 4},
					{Path: "internal/agent/errors.go", Added: 3, ThisTurn: true},
					{Path: "docs/loop.md", Added: 34},
					{Path: "go.mod", Added: 1},
				},
				Added: 96, Removed: 11,
				Alerts: []InspectorAlert{
					{Label: "go test ./internal/agent/...", Note: OutcomeExit(1), Turn: 7},
					{Label: "go build ./...", Note: OutcomeExit(2), Turn: 9},
				},
			},
		}
		return []golden.Panel{
			{Label: "every block, unbounded height", View: full.View(width, 0)},
			{Label: "every block, height 16 (truncating)", View: full.View(width, 16)},
			{Label: "blocks with nothing to say are omitted", View: quiet.View(width, 0)},
			{Label: "eight files, four turns deep", View: session.View(width, 0)},
			{Label: "the rail is shorter than the list (height 14)", View: session.View(width, 14)},
		}
	})
}

// TestGolden_PlanCard captures the plan card (§4d): the priced step list with
// its computed summary, a plan whose radius is the one worth reading twice,
// the height bound that counts the steps it drops, and the prose fallback for
// a plan that never adopted the shape.
func TestGolden_PlanCard(t *testing.T) {
	captureGolden(t, "plan-card", "plan card", goldenWidths, func(width int) []golden.Panel {
		card := func(mut func(*PlanCard)) string {
			c := PlanCard{
				Title: "Plan · make the round limit recoverable",
				Chip:  "4 steps",
				Steps: []PlanStep{
					{Number: 1, Title: "Locate the round accounting",
						Detail: "internal/agent/loop.go · internal/agent/round.go",
						Kind:   "read only", KindTone: ToneSafe},
					{Number: 2, Title: "Add a RoundsExhausted sentinel",
						Detail: "internal/agent/errors.go · new type, no signature changes",
						Kind:   "✎ creates 1 file"},
					{Number: 3, Title: "Return it from runRound and handle it in Run",
						Detail: "internal/agent/loop.go · 2 hunks", Kind: "✎ edits 1 file"},
					{Number: 4, Title: "Offer more rounds in the chat model",
						Detail: "internal/ui/chat/model.go · one case in the update switch",
						Kind:   "✎ edits 1 file"},
				},
				Summary: []PlanFact{
					{Text: "3 files touched"},
					{Text: "no deletes", Tone: ToneSafe},
					{Text: "no network", Tone: ToneSafe},
					{Text: "reversible", Tone: ToneSafe},
				},
				SummaryDetail: "every file is tracked in git",
				Options: []SelectOption{
					{Label: "Run the whole plan — accept-edits mode",
						Desc: "edits apply as they come; commands and other actions still ask"},
					{Label: "Run it unattended — auto mode",
						Desc: "edits and allowlisted commands run; the classifier judges the rest"},
					{Label: "Step through it — manual approvals",
						Desc: "every edit and every command asks you first"},
					{Label: "Keep planning — tell me what to change",
						Desc: "stays in plan mode; the plan keeps its place in the conversation"},
					{Label: "Reject the plan", Desc: "nothing runs and the session stays in plan mode"},
				},
				Hint: "↑↓/jk move · enter select · 1–5 jump · s save · esc keep planning",
			}
			mut(&c)
			return c.View(width)
		}
		return []golden.Panel{
			{Label: "priced steps · the first option focused", View: card(func(c *PlanCard) {})},
			{Label: "focus moved · only the focused option explains itself", View: card(func(c *PlanCard) {
				c.Focus = 2
			})},
			{Label: "a radius worth reading twice", View: card(func(c *PlanCard) {
				c.Steps[3] = PlanStep{Number: 4, Title: "Drop the old round shim",
					Detail: "internal/agent/shim.go", Kind: "✎ deletes 1 file", KindTone: ToneRisk}
				c.Summary = []PlanFact{
					{Text: "4 files touched"},
					{Text: "deletes 1 file", Tone: ToneRisk},
					{Text: "network needed", Tone: ToneOpen},
					{Text: "partly reversible", Tone: ToneNeutral},
				}
				c.SummaryDetail = "3 of 4 files tracked in git"
			})},
			{Label: "bounded · the steps it cannot fit are counted", View: card(func(c *PlanCard) {
				c.MaxLines = 18
			})},
			{Label: "too short for a single step · says so rather than counting more than none", View: card(func(c *PlanCard) {
				c.MaxLines = 14
			})},
			{Label: "no structure · prose with the same options below it", View: card(func(c *PlanCard) {
				c.Title, c.Chip = "Plan ready", ""
				c.Steps, c.Summary, c.SummaryDetail = nil, nil, ""
				c.Prose = []string{
					"I'd add a sentinel error to the agent package and return it from",
					"the round loop, then handle it in the chat model so the user is",
					"offered more rounds instead of the turn simply stopping.",
				}
			})},
		}
	})
}

// TestGolden_StartScreen captures the first-contact screen (§17c): the facts
// a session already knows, the two notes under them, the three offers with
// the pointer on one, and the screen the reader is left with once typing has
// dismissed the list.
func TestGolden_StartScreen(t *testing.T) {
	captureGolden(t, "start-screen", "first-contact screen", goldenWidths, func(width int) []golden.Panel {
		screen := func(mut func(*StartScreen)) string {
			s := StartScreen{
				Facts: []StartFact{
					{Text: "~/src/shhh", Lead: true},
					{Text: "go 1.24"},
					{Text: "git main"},
					{Text: "3 files changed", Tone: ToneOpen},
					{Text: "41 packages"},
				},
				Notes: []StartNote{
					{Label: "context", Value: "AGENTS.md", Detail: "in the system prompt"},
					{Label: "gate", Value: "default", Detail: "vet, test · runs without asking"},
				},
				Lead: "Some things worth doing first:",
				Suggestions: []StartSuggestion{
					{Glyph: "▸", Title: "pick up (last session)", Detail: "7 turns · $0.42 · 4m ago"},
					{Glyph: "⚙", Title: "explain what changed in the working tree",
						Detail: "reads only, no writes"},
					{Glyph: "⚙", Title: "run the default quality gate and triage what fails",
						Detail: "one approval, then it reports back"},
				},
				Hint: "[↑↓] choose · [enter] start · or just type what you want",
			}
			mut(&s)
			return s.View(width)
		}
		return []golden.Panel{
			{Label: "first contact · the pointer on the resume offer", View: screen(func(s *StartScreen) {})},
			{Label: "pointer moved to the offer that costs an approval", View: screen(func(s *StartScreen) {
				s.Focus = 2
			})},
			{Label: "nothing to pick up · a clean tree in a project with no gate", View: screen(func(s *StartScreen) {
				s.Facts[3] = StartFact{Text: "clean tree", Tone: ToneSafe}
				s.Notes = []StartNote{
					{Label: "context", Value: "nothing read", Detail: "no .shhh or AGENTS.md above this directory"},
					{Label: "gate", Value: "not configured", Detail: ".shhh/quality.json"},
				}
				s.Suggestions = []StartSuggestion{
					{Glyph: "⚙", Title: "walk me through what this project does", Detail: "reads only, no writes"},
					{Glyph: "⚙", Title: "summarise the last ten commits", Detail: "reads only, no writes"},
					{Glyph: "⚙", Title: "run go test ./... and triage the failures",
						Detail: "one approval, then it reports back"},
				}
			})},
			{Label: "typing dismissed the list · the facts stay", View: screen(func(s *StartScreen) {
				s.Suggestions, s.Lead, s.Hint = nil, "", ""
			})},
		}
	})
}

// TestGolden_RecoveryRows captures every provider failure class on one sheet
// (§17a). The verb, subject and duration are held constant, so the file reads
// as a table of what each class contributes: its glyph, the words in its
// outcome, the keys it offers and what it says survived.
func TestGolden_RecoveryRows(t *testing.T) {
	captureGolden(t, "recovery-rows", "provider failure rows", goldenWidths, func(width int) []golden.Panel {
		row := func(mut func(*RecoveryRow)) string {
			r := RecoveryRow{Verb: VerbModel, Subject: "gpt-4o", Duration: "0.3s",
				Note: "nothing in the turn was lost"}
			mut(&r)
			return r.View(width)
		}
		return []golden.Panel{
			{Label: "auth · the key it sent, named by its last four", View: row(func(r *RecoveryRow) {
				r.Qualifier, r.Outcome = "401 unauthorized", "key ···4f9c rejected"
				r.Detail = []string{"Incorrect API key provided"}
				r.Keys = []KeyOffer{{Key: "[e]", Label: "enter a new key"}, {Key: "[p]", Label: "switch provider"}}
			})},
			{Label: "rate limit · a stall, with the wait the provider asked for", View: row(func(r *RecoveryRow) {
				r.State, r.Qualifier, r.Outcome = RecoveryStalled, "429 rate limited", "retry in 38s"
				r.Detail = []string{"Rate limit reached for gpt-4o. Please try again in 38s."}
				r.Keys = []KeyOffer{{Key: "[r]", Label: "try again"}, {Key: "[p]", Label: "switch provider"}}
			})},
			{Label: "quota · not a stall, because waiting does not clear it", View: row(func(r *RecoveryRow) {
				r.Qualifier, r.Outcome = "429 quota exhausted", "the account, not the rate"
				r.Detail = []string{"You exceeded your current quota, please check your plan and billing details"}
				r.Keys = []KeyOffer{{Key: "[p]", Label: "switch provider"}}
				r.Note = "waiting will not clear this one"
			})},
			{Label: "overloaded · the provider's own side", View: row(func(r *RecoveryRow) {
				r.State, r.Qualifier, r.Outcome = RecoveryStalled, "529 overloaded", "the provider's side"
				r.Detail = []string{"Overloaded"}
				r.Keys = []KeyOffer{{Key: "[r]", Label: "try again"}, {Key: "[p]", Label: "switch provider"}}
			})},
			{Label: "context length · the one class with a remedy of its own", View: row(func(r *RecoveryRow) {
				r.Qualifier, r.Outcome = "400 context too long", "over the window"
				r.Detail = []string{"This model's maximum context length is 128000 tokens"}
				r.Keys = []KeyOffer{{Key: "[c]", Label: "compact now"}, {Key: "[r]", Label: "then try again"}}
				r.Note = "compacting keeps the plan and the recent turns"
			})},
			{Label: "network · it never reached the provider", View: row(func(r *RecoveryRow) {
				r.State, r.Qualifier, r.Outcome = RecoveryStalled, "network", "never reached it"
				r.Detail = []string{`Post "https://api.openai.com/v1/chat/completions": dial tcp: connection refused`}
				r.Keys = []KeyOffer{{Key: "[r]", Label: "try again"}}
			})},
			{Label: "cancelled · you did it on purpose, so no key is offered", View: row(func(r *RecoveryRow) {
				r.State, r.Qualifier, r.Outcome = RecoveryStopped, "cancelled", "stopped"
				r.Duration = NoDuration
			})},
			{Label: "unclassified · the message is the whole point of the row", View: row(func(r *RecoveryRow) {
				r.Qualifier, r.Outcome = "400 unclassified", "message below"
				r.Detail = []string{"Unknown parameter: 'reasoning.effort'"}
				r.Keys = []KeyOffer{{Key: "[r]", Label: "try again"}, {Key: "[p]", Label: "switch provider"}}
			})},
		}
	})
}

// TestGolden_ProviderCard captures the missing-provider card (§17b) — the one
// card a failure earns, because it is the one failure the session cannot
// continue past.
func TestGolden_ProviderCard(t *testing.T) {
	captureGolden(t, "provider-card", "no provider configured", goldenWidths, func(width int) []golden.Panel {
		card := func(mut func(*ProviderCard)) string {
			c := ProviderCard{
				Places: []ProviderPlace{
					{Label: "env", Detail: "SHHH_API_KEY, OPENAI_API_KEY — unset"},
					{Label: "config", Detail: "~/.config/shhh/config.toml — no provider api_key"},
					{Label: "profiles", Detail: "no .toml in ~/.config/shhh/providers"},
					{Label: "local", Emphasis: "localhost:11434", Detail: "llama3.3, qwen2.5-coder", Found: true},
				},
				Likely: "the local runtime is already answering — that is the quickest way in",
				Keys: []KeyOffer{
					{Key: "[enter]", Label: "setup wizard"},
					{Key: "[p]", Label: "paste a key"},
					{Key: "[o]", Label: "use llama3.3 locally"},
				},
			}
			mut(&c)
			return c.View(width)
		}
		return []golden.Panel{
			{Label: "a local runtime is answering · the likely fix is named", View: card(func(c *ProviderCard) {})},
			{Label: "nothing anywhere · no fix to point at, and no local offer", View: card(func(c *ProviderCard) {
				c.Places[3] = ProviderPlace{Label: "local", Detail: "localhost:11434 — nothing listening"}
				c.Likely = ""
				c.Keys = c.Keys[:2]
			})},
			{Label: "a gateway profile is ready but its variable is not exported", View: card(func(c *ProviderCard) {
				c.Places[2] = ProviderPlace{Label: "profiles", Detail: "litellm (LITELLM_KEY unset)"}
				c.Places[3] = ProviderPlace{Label: "local", Detail: "localhost:11434 — nothing listening"}
				c.Likely = "a gateway profile loaded but its key is not exported — export LITELLM_KEY"
				c.Keys = c.Keys[:2]
			})},
		}
	})
}

// TestGolden_PressureCard captures the context-pressure card (§17b) — the
// second of the two cards, and the only place in the product that itemises
// token spend, because it is the only place where you can act on it.
func TestGolden_PressureCard(t *testing.T) {
	captureGolden(t, "pressure-card", "context is nearly full", goldenWidths, func(width int) []golden.Panel {
		card := func(mut func(*PressureCard)) string {
			c := PressureCard{
				Pct: 94, Tokens: 188_000, Window: 200_000,
				Warn: 60, Alert: 80,
				Rows: []PressureRow{
					{Tokens: 88_000, Label: "tool output", Detail: "6 results"},
					{Tokens: 54_000, Label: "the conversation", Detail: "14 messages"},
					{Tokens: 31_000, Label: "system prompt"},
					{Tokens: 15_000, Label: "project context"},
				},
				Keeps:       "the plan, 3 changed files and the last 2 turns",
				Drops:       "the older tool output",
				Recovers:    96_000,
				RecoversPct: 48,
				Continuing:  "keeping going asks nothing further — the oldest tool output is elided before each request from here, and what falls out does not come back",
				Keys: []KeyOffer{
					{Key: "[enter]", Label: "compact now"},
					{Key: "[n]", Label: "new session"},
					{Key: "[esc]", Label: "keep going"},
				},
			}
			mut(&c)
			return c.View(width)
		}
		return []golden.Panel{
			{Label: "94% · a plan and a changeset to keep", View: card(func(c *PressureCard) {})},
			{Label: "an estimated total · nothing to keep but the turns", View: card(func(c *PressureCard) {
				c.Estimated = true
				c.Keeps = "the last turn"
				c.Recovers, c.RecoversPct = 41_000, 20
			})},
		}
	})
}

// TestGolden_SecretPrompt captures the masked key entry an auth failure's [k]
// opens (S-106). What is typed is never rendered; the mask is.
func TestGolden_SecretPrompt(t *testing.T) {
	captureGolden(t, "secret-prompt", "masked key entry", goldenWidths, func(width int) []golden.Panel {
		typed := func(n int, mut func(*SecretPrompt)) string {
			s := SecretPrompt{Prompt: "Paste a key for openai", Hint: "SHHH_API_KEY or OPENAI_API_KEY"}
			mut(&s)
			for i := 0; i < n; i++ {
				s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			}
			return s.View(width)
		}
		return []golden.Panel{
			{Label: "nothing typed yet", View: typed(0, func(s *SecretPrompt) {})},
			{Label: "a key pasted · masked, never echoed", View: typed(51, func(s *SecretPrompt) {})},
			{Label: "replacing a key the provider rejected", View: typed(12, func(s *SecretPrompt) {
				s.Replace = "4f9c"
			})},
		}
	})
}
