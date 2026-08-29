package components

// The mono conformance walk (S-095). The first invariant —
// colour never carries meaning alone — is enforced here rather than asserted
// in prose: every surface renders each of its states with the mono palette
// on, the ANSI is stripped off, and the resulting plain texts must all
// differ. Two states that were only ever a hue apart collapse to the same
// string and the surface fails.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
)

// monoOn turns the mono palette on for one test and restores what was there.
func monoOn(t *testing.T) {
	t.Helper()
	was := mono
	SetMono(true)
	t.Cleanup(func() { SetMono(was) })
}

// withColorProfile forces the palette to resolve against a profile that has
// colour to give, which the one detected from a test binary's non-terminal
// stdout does not. The palette assertions need the escape codes to be there
// to check them.
func withColorProfile(t *testing.T, p colorprofile.Profile) {
	t.Helper()
	was := Profile()
	SetProfile(p)
	t.Cleanup(func() { SetProfile(was) })
}

type monoState struct {
	name string
	view string
}

type monoSurface struct {
	name   string
	states []monoState
}

// monoFixtures renders every surface's states with the current palette. The
// fixtures deliberately hold everything else constant — same verb, same
// target, same label — so that the only thing left to tell two states apart
// is what the state itself contributes.
func monoFixtures() []monoSurface {
	const w = 72

	row := func(mut func(*ActivityRow)) string {
		r := ActivityRow{Kind: ActivityTool, Verb: "read", Target: "internal/agent/loop.go"}
		mut(&r)
		return r.View(w)
	}

	hunk := func(kind diff.Kind) []diff.Hunk {
		return []diff.Hunk{{
			OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1,
			Lines: []diff.Line{{Kind: kind, Text: "value := compute()", OldNo: 1, NewNo: 1}},
		}}
	}
	diffLine := func(kind diff.Kind) string {
		return strings.Join(UnifiedLines(hunk(kind), w, UnifiedOpts{LineNumbers: true, Emphasis: true}), "\n")
	}

	// One staged chip. The name and the size are held constant, so the
	// kind's mark is the only thing left to tell the three apart — which is
	// what makes the strip legible on a terminal with no colour at all.
	chipStrip := func(kind ChipKind) string {
		return AttachmentChips([]AttachmentChip{{Kind: kind, Name: "staged.bin", Size: "412 KB"}}, w)
	}

	// The staged image preview. It is the one surface in shhh whose
	// content *is* colour, so it is the strongest thing invariant 1 has to
	// say about mono: the picture is still drawn, still tells its bands
	// apart, and asks the terminal for no colour at all to do it.
	pictureCard := func(mut func(*PictureView)) string {
		p := PictureView{Name: "staged.png", Size: "412 KB", Pixels: "640×400",
			Image: testPicture(64, 40), Height: 9}
		mut(&p)
		return p.View(w)
	}

	// The config screen's two toned fields. Everything but the field
	// under test is held constant, so a value or a source that was only ever
	// a hue apart from another would collapse here.
	configScreen := func(row ConfigRow) string {
		c := &ConfigScreen{Path: "~/.config/shhh/config.toml", Rows: []ConfigRow{row}}
		return c.View(w)
	}
	configValue := func(value string, tone FieldTone) string {
		return configScreen(ConfigRow{
			Group: "SESSION", Key: "behavior.default_mode", Label: "setting",
			Value: value, ValueTone: tone, Source: "user",
		})
	}
	configSourced := func(source string, tone FieldTone) string {
		return configScreen(ConfigRow{
			Group: "SESSION", Key: "behavior.default_mode", Label: "setting",
			Value: "auto", Source: source, SourceTone: tone,
		})
	}

	// The history browser's row states. Everything but the outcome is
	// held constant, so a row that said what happened to it in colour alone
	// would collapse into the row beside it here.
	historyRow := func(state ActivityState, outcome string) string {
		h := &HistoryScreen{Subject: "1 entry · 1 run", Rows: []HistoryRow{{
			ID: "1", Prompt: "delete every log file older than a week",
			Command: "find . -name '*.log' -delete", When: "4m ago",
			Model: "openai/gpt-5.2", Action: "run",
			Outcome: outcome, State: state, Duration: "1.4s",
		}}}
		return h.View(w)
	}

	// The doctor surface's check states. Everything but the state is
	// held constant — one name, one subject, one outcome word — so the only
	// thing left to tell a failure from a warning is its glyph, which is
	// exactly what invariant 1 asks of them.
	doctorState := func(state DoctorState) string {
		d := &DoctorScreen{Checks: []DoctorCheck{{
			Name: "sandbox", Subject: "bwrap", Detail: "workspace profile",
			Outcome: "an outcome", State: state,
		}}}
		return d.View(w)
	}

	// The metrics surface's category meters. The share and the number
	// beside it are held constant, so the only thing left to tell an ordinary
	// share from a cost nobody asked for is the label and its glyph — which
	// is exactly what invariant 1 asks of them.
	metricsBar := func(label string, tone MeterTone) string {
		m := &MetricsScreen{
			Subject: "all time · 1 request · 1 model",
			Blocks: []MetricsBlock{{Title: "where the money went", Bars: []MetricsBar{
				{Label: label, Pct: 40, Text: "$0.96 · 40%", Tone: tone},
			}}},
		}
		return m.View(w)
	}

	pressed := func(pct int, tokens int64) string {
		return PressureCard{
			Pct: pct, Tokens: tokens, Window: 200_000, Warn: 60, Alert: 80,
			Rows:  []PressureRow{{Tokens: tokens, Label: "tool output"}},
			Drops: "the older tool output",
			Keys:  []KeyOffer{{Key: "[enter]", Label: "compact now"}},
		}.View(w)
	}

	card := func(mut func(*ApprovalCard)) string {
		c := ApprovalCard{
			Variant:  ApprovalCommand,
			Title:    "Approve command",
			Headline: "Assistant wants to run: go test ./...",
			Question: "Run this command?",
		}
		mut(&c)
		return c.View(w)
	}

	// The strip's three row states, held to one label and one rating so that
	// only the state itself is left to tell them apart (S-102).
	queued := func(current bool, mut func(*QueueItem)) string {
		it := QueueItem{Number: 2, Label: "go test ./internal/agent/...", Severity: SeverityLow}
		mut(&it)
		return it.render(w, current)
	}

	// A manager row holds its name and task constant so only its state is
	// left to tell two renders apart. A child's row draws through the lane
	// renderer (S-111); the orchestrator has no lane progress and keeps its
	// own status text.
	agents := func(state AgentState, status string) string {
		row := AgentRow{State: state, Name: "writer-1", Task: "docs", Status: status}
		switch state {
		case AgentRunning:
			row.Progress = &AgentProgress{State: FanoutRunning, Tools: 4}
		case AgentBlocked:
			row.Progress = &AgentProgress{State: FanoutBlocked, Tools: 4}
		case AgentDone:
			row.Progress = &AgentProgress{State: FanoutDone, Tools: 4}
		case AgentFailed:
			row.Progress = &AgentProgress{State: FanoutFailed, Tools: 4}
		}
		l := AgentList{Rows: []AgentRow{row}}
		return l.View(w)
	}

	// A fan-out lane holds its name, task and counts constant so that only
	// the lane's state is left to tell two renders apart (S-110).
	lane := func(mut func(*FanoutLane)) string {
		l := FanoutLane{State: FanoutRunning, Name: "writer-1", Task: "docs/loop.md", Tools: 4}
		mut(&l)
		return l.View(w)
	}

	// The palette's row states, held to one command name so that only the
	// state itself is left to tell them apart (S-112).
	// The filter row's three answers (§4a, S-123): a query with matches, a
	// query with none, and a list with no filter open at all. Bold is what
	// tells a matched run from the rest, and bold is what mono keeps.
	filterCard := func(mut func(*Select)) string {
		sel := Select{
			Title: "Switch model", MaxLines: 9, Filterable: true,
			Options: []SelectOption{{Label: "gpt-5.2-mini"}, {Label: "o4-mini"}},
			Total:   24,
		}
		mut(&sel)
		return sel.View(w)
	}

	paletteRow := func(mut func(*SelectOption)) string {
		opt := SelectOption{Label: "/clear"}
		mut(&opt)
		return (&Select{Options: []SelectOption{opt}, Unnumbered: true}).View(w)
	}

	staged := func(checked bool) string {
		s := NewMultiSelect("Stage files", []SelectOption{{Label: "internal/agent/loop.go"}})
		s.Checked[0] = checked
		return s.View(w)
	}

	// The plan card holds its title, files and options constant so that only
	// the step's intent — or the plan's radius — is left to tell the states
	// apart (S-103).
	planned := func(mut func(*PlanCard)) string {
		c := PlanCard{
			Title: "Plan · make the round limit recoverable",
			Chip:  "1 step",
			Steps: []PlanStep{{Number: 1, Title: "Rework the round accounting",
				Detail: "internal/agent/loop.go", Kind: "read only", KindTone: ToneSafe}},
			Summary: []PlanFact{{Text: "1 file touched"}, {Text: "reversible", Tone: ToneSafe}},
			Options: []SelectOption{{Label: "Run the whole plan — accept-edits mode"}},
			Hint:    "enter select · s save · esc keep planning",
		}
		mut(&c)
		return c.View(w)
	}

	// The PLAN checklist holds one step's title and duration constant, so
	// that only its state is left to tell the rows apart (S-104).
	checklist := func(state PlanStepState) string {
		return InspectorRail{Plan: &InspectorPlan{
			Steps: []InspectorPlanStep{{Number: 1, Title: "Return it from runRound",
				State: state, Elapsed: "1.2s"}},
		}}.View(InspectorWidth, 0)
	}

	meter := func(pct int) string {
		return Meter{Pct: pct, Cells: MeterCellsVitals, Tone: MeterPressure, Label: "ctx"}.View()
	}

	// The review surface's staging states, held to one file and one hunk so
	// that only the staging itself is left to tell them apart (S-099).
	review := func(staged []bool, mut func(*ReviewView)) string {
		hunks := diff.Compute(
			"a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n",
			"a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nK\nl\n")
		v := &ReviewView{
			Title:  "turn 7",
			Files:  []ReviewFile{{Path: "internal/agent/loop.go", Hunks: hunks, Staged: staged}},
			Shield: "nothing is committed",
			Height: 12,
		}
		if mut != nil {
			mut(v)
		}
		return v.View(w)
	}

	// The undo confirm holds its counts constant so that only drift is left
	// to tell the states apart (S-100).
	undo := func(drifted []string) string {
		return UndoConfirm{Turn: 7, Restores: 2, Removes: 1, Drifted: drifted}.View(w)
	}

	// The close rows hold their stats constant so that only the state itself
	// is left to tell them apart (S-098).
	closed := func(mut func(*TurnClose)) string {
		c := TurnClose{Steps: 4, Tools: 18, Elapsed: "1m 04s"}
		mut(&c)
		return c.View(w)
	}

	// The recovery row holds its verb, subject and duration constant, so that
	// only the class and its state are left to tell the failures apart
	// (S-106). ⚠ and ✗ are a hue apart in colour; in mono they have to
	// be the glyph and the words.
	recovered := func(mut func(*RecoveryRow)) string {
		r := RecoveryRow{Verb: VerbModel, Subject: "gpt-4o", Duration: "0.3s"}
		mut(&r)
		return r.View(w)
	}

	// The round-limit pause is the same row under a different verb (S-109).
	// Its states differ by what the turn managed and what is still on offer,
	// which in mono is all there is: nothing about them is a colour.
	paused := func(mut func(*RecoveryRow)) string {
		r := RecoveryRow{State: RecoveryStalled, Verb: VerbRounds,
			Subject: "25 of 25 used", Outcome: "stopped", Duration: "4m12s"}
		mut(&r)
		return r.View(w)
	}

	// The retry countdown's states differ by how much is left and what it
	// offers (S-107). In colour the meter drains in accent; in mono the cells
	// and the seconds beside them are the whole message.
	waiting := func(mut func(*RetryWait)) string {
		w := RetryWait{Pct: 60, Text: "retry in 12s", Note: "attempt 1 of 3",
			Keys: []KeyOffer{{Key: "[esc]", Label: "stop the turn"}}}
		mut(&w)
		return w.View(72)
	}

	// The status line holds its numbers constant, so the phase and the
	// outcome are the only things left to tell its states apart.
	status := func(mut func(*TurnStatus)) string {
		s := TurnStatus{Elapsed: "12.4s", Up: "41.2k", Down: "2.1k", Cost: "$0.06",
			Duration: "12.4s", Tools: 18}
		mut(&s)
		return s.View(w)
	}

	start := func(mut func(*StartScreen)) string {
		s := StartScreen{
			Facts: []StartFact{{Text: "~/src/shhh", Lead: true}, {Text: "git main"}},
			Suggestions: []StartSuggestion{
				{Glyph: "▸", Title: "pick up (last session)", Detail: "7 turns"},
				{Glyph: "⚙", Title: "explain what changed", Detail: "reads only, no writes"},
			},
		}
		mut(&s)
		return s.View(w)
	}

	return []monoSurface{
		{"cockpit mode segment", []monoState{
			// The mode word is held constant on purpose: the glyph has to
			// carry the difference on its own.
			{"permissive", Cockpit{Mode: "mode", ModeKind: CockpitPermissive, CtxPct: -1}.View(w)},
			{"gated", Cockpit{Mode: "mode", ModeKind: CockpitGated, CtxPct: -1}.View(w)},
			{"checking", Cockpit{Mode: "mode", ModeKind: CockpitChecking, CtxPct: -1}.View(w)},
		}},
		{"activity row state", []monoState{
			{"done", row(func(r *ActivityRow) { r.Outcome = OutcomeOK; r.Duration = "1.1s" })},
			{"queued", row(func(r *ActivityRow) {
				r.State, r.Outcome, r.Duration = ActivityQueued, OutcomeQueued, NoDuration
			})},
			{"running", row(func(r *ActivityRow) { r.State, r.Outcome = ActivityRunning, OutcomeRunning })},
			{"checking", row(func(r *ActivityRow) { r.State, r.Outcome = ActivityChecking, OutcomeChecking })},
			{"failed", row(func(r *ActivityRow) { r.State, r.Outcome = ActivityFailed, OutcomeExit(1) })},
			// The two denials are the case the invariant is really about: the
			// component colours them differently, so the decider has to be a
			// word as well.
			{"denied by you", row(func(r *ActivityRow) {
				r.State, r.Outcome, r.Duration = ActivityDenied, OutcomeBy(OutcomeDenied, "you"), NoDuration
			})},
			{"denied by rule", row(func(r *ActivityRow) {
				r.State, r.ByRule = ActivityDenied, true
				r.Outcome, r.Duration = OutcomeBy(OutcomeDenied, "auto"), NoDuration
			})},
		}},
		{"activity row kind", []monoState{
			{"tool", row(func(r *ActivityRow) { r.Outcome = OutcomeOK })},
			{"command", row(func(r *ActivityRow) { r.Kind, r.Outcome = ActivityCommand, OutcomeOK })},
			{"edit", row(func(r *ActivityRow) { r.Kind, r.Outcome = ActivityEdit, OutcomeOK })},
			{"sub-agent", row(func(r *ActivityRow) { r.Kind, r.Outcome = ActivitySubagent, OutcomeOK })},
		}},
		{"diff line kind", []monoState{
			{"context", diffLine(diff.Context)},
			{"addition", diffLine(diff.Add)},
			{"deletion", diffLine(diff.Del)},
		}},
		{"approval severity", []monoState{
			{"no warnings", card(func(c *ApprovalCard) {
				c.AllowAlways, c.AlwaysHint = true, "a: always allow commands this session"
			})},
			{"warned", card(func(c *ApprovalCard) {
				c.Warnings = []string{"deletes files recursively (rm -rf)"}
			})},
			{"contained", card(func(c *ApprovalCard) {
				c.Chip = "⛨ bwrap · workspace"
			})},
			{"uncontained", card(func(c *ApprovalCard) {
				c.Uncontained = true
			})},
		}},
		{"approval severity", []monoState{
			{"unrated", card(func(c *ApprovalCard) {})},
			{"low", card(func(c *ApprovalCard) { c.Severity = SeverityLow })},
			{"medium", card(func(c *ApprovalCard) { c.Severity = SeverityMedium })},
			{"high", card(func(c *ApprovalCard) { c.Severity = SeverityHigh })},
		}},
		{"approval radius field", []monoState{
			{"neutral", card(func(c *ApprovalCard) {
				c.Fields = []CardField{{Label: "undo", Value: "partial", Tone: ToneNeutral}}
			})},
			{"safe", card(func(c *ApprovalCard) {
				c.Fields = []CardField{{Label: "undo", Value: "git", Tone: ToneSafe}}
			})},
			{"open", card(func(c *ApprovalCard) {
				c.Fields = []CardField{{Label: "network", Value: "open", Tone: ToneOpen}}
			})},
			{"risk", card(func(c *ApprovalCard) {
				c.Fields = []CardField{{Label: "undo", Value: "none", Tone: ToneRisk}}
			})},
		}},
		{"approval variant", []monoState{
			{"command", card(func(c *ApprovalCard) {})},
			{"edit", card(func(c *ApprovalCard) {
				c.Variant, c.Title, c.Hunks = ApprovalEdit, "Approve edit", hunk(diff.Add)
			})},
			{"generic", card(func(c *ApprovalCard) {
				c.Variant, c.Title, c.Summary = ApprovalGeneric, "Approve tool", "fetch https://example.com"
			})},
		}},
		{"queue strip row", []monoState{
			// Current, waiting, and in the batch: three rows that have to
			// read as three with every hue stripped out.
			{"current", queued(true, func(it *QueueItem) {})},
			{"waiting", queued(false, func(it *QueueItem) {})},
			{"in the batch", queued(false, func(it *QueueItem) { it.Batch = true })},
		}},
		{"list filter state", []monoState{
			{"no filter open", filterCard(func(s *Select) {})},
			{"matches", filterCard(func(s *Select) { s.Filtering, s.Query = true, "mini" })},
			{"no match", filterCard(func(s *Select) {
				s.Filtering, s.Query, s.Options = true, "sonnet-5", nil
				s.Closest = "claude-sonnet-4.6"
			})},
		}},
		{"palette row state", []monoState{
			{"available", paletteRow(func(o *SelectOption) {})},
			{"unavailable", paletteRow(func(o *SelectOption) { o.Dim = true })},
			{"group rail", paletteRow(func(o *SelectOption) { o.Header = true })},
		}},
		{"staged checkbox", []monoState{
			{"unstaged", staged(false)},
			{"staged", staged(true)},
		}},
		// The scroll gutter has two states and one column to say them in, so
		// the stroke is all it has: dim and dimmer are the same grey here.
		{"scroll gutter cell", []monoState{
			// The top row of a gutter scrolled to its end, and of the same
			// gutter at its top.
			{"track", Scrollbar(4, 40, 1, 39)[0]},
			{"thumb", Scrollbar(4, 40, 1, 0)[0]},
		}},
		{"fan-out lane state", []monoState{
			{"queued", lane(func(l *FanoutLane) { l.State = FanoutQueued })},
			{"running", lane(func(l *FanoutLane) {})},
			{"blocked", lane(func(l *FanoutLane) { l.State = FanoutBlocked })},
			{"idle", lane(func(l *FanoutLane) { l.State = FanoutIdle })},
			{"done", lane(func(l *FanoutLane) { l.State = FanoutDone })},
			{"failed", lane(func(l *FanoutLane) { l.State = FanoutFailed })},
		}},
		{"agent lane state", []monoState{
			{"current", agents(AgentCurrent, "working")},
			{"running", agents(AgentRunning, "working")},
			{"blocked", agents(AgentBlocked, "working")},
			{"done", agents(AgentDone, "working")},
			{"failed", agents(AgentFailed, "working")},
		}},
		{"turn close state", []monoState{
			{"done", closed(func(c *TurnClose) {})},
			{"cancelled", closed(func(c *TurnClose) { c.State = TurnCancelled })},
			{"failed", closed(func(c *TurnClose) { c.State = TurnFailed })},
		}},
		{"turn close verdict", []monoState{
			{"nothing changed", closed(func(c *TurnClose) {})},
			{"changed", closed(func(c *TurnClose) {
				c.Changes = &TurnChanges{Files: 3, Added: 30, Removed: 4,
					Keys: []TurnKey{{Key: "[v]", Label: "review"}}}
			})},
			{"checks passing", closed(func(c *TurnClose) {
				c.Checks = &TurnChecks{Label: "go test ./...", Counts: "41 packages"}
			})},
			{"checks failing", closed(func(c *TurnClose) {
				c.Checks = &TurnChecks{Failed: true, Label: "go test ./...", Counts: "41 packages"}
			})},
		}},
		// The status line's phases and outcomes are words before they are
		// colours: the spinner run is one hue for all four phases, and the
		// three resolutions differ by glyph and word as well as by colour.
		{"turn status phase", []monoState{
			{"thinking", status(func(s *TurnStatus) { s.Phase = PhaseThinking })},
			{"deciding", status(func(s *TurnStatus) { s.Phase = PhaseDeciding })},
			{"running", status(func(s *TurnStatus) { s.Phase, s.Tool = PhaseRunning, "go test" })},
			{"streaming", status(func(s *TurnStatus) { s.Phase = PhaseStreaming })},
		}},
		{"turn status resolution", []monoState{
			{"running", status(func(s *TurnStatus) {})},
			{"done", status(func(s *TurnStatus) { s.Done = true })},
			{"cancelled", status(func(s *TurnStatus) { s.Done, s.Outcome = true, TurnCancelled })},
			{"failed", status(func(s *TurnStatus) { s.Done, s.Outcome = true, TurnFailed })},
		}},
		{"review staging", []monoState{
			{"nothing staged", review([]bool{false, false}, nil)},
			{"partly staged", review([]bool{true, false}, nil)},
			{"wholly staged", review([]bool{true, true}, nil)},
		}},
		{"review verdict", []monoState{
			{"no verdict", review([]bool{true, true}, nil)},
			{"checks passing", review([]bool{true, true}, func(v *ReviewView) {
				v.Verdict = &ReviewVerdict{Label: "go test ./..."}
			})},
			{"checks failing", review([]bool{true, true}, func(v *ReviewView) {
				v.Verdict = &ReviewVerdict{Failed: true, Label: "go test ./..."}
			})},
		}},
		{"start screen focus", []monoState{
			// The pointer is what survives the palette: a focus background
			// strips to nothing and the row's own glyph means something else.
			{"first offer", start(func(s *StartScreen) { s.Focus = 0 })},
			{"second offer", start(func(s *StartScreen) { s.Focus = 1 })},
		}},
		{"start screen dirty state", []monoState{
			{"clean tree", start(func(s *StartScreen) {
				s.Facts = append(s.Facts, StartFact{Text: "clean tree", Tone: ToneSafe})
			})},
			{"dirty tree", start(func(s *StartScreen) {
				s.Facts = append(s.Facts, StartFact{Text: "3 files changed", Tone: ToneOpen})
			})},
		}},
		{"recovery row class", []monoState{
			{"unauthorized", recovered(func(r *RecoveryRow) {
				r.Qualifier, r.Outcome = "401 unauthorized", "key ···4f9c rejected"
			})},
			{"rate limited", recovered(func(r *RecoveryRow) {
				r.State, r.Qualifier, r.Outcome = RecoveryStalled, "429 rate limited", "retry in 38s"
			})},
			{"overloaded", recovered(func(r *RecoveryRow) {
				r.State, r.Qualifier, r.Outcome = RecoveryStalled, "529 overloaded", "the provider's side"
			})},
			{"cancelled", recovered(func(r *RecoveryRow) {
				r.State, r.Qualifier, r.Outcome = RecoveryStopped, "cancelled", "stopped"
			})},
			{"unclassified", recovered(func(r *RecoveryRow) {
				r.Qualifier, r.Outcome = "400 unclassified", "message below"
			})},
		}},
		{"round-limit pause", []monoState{
			{"changed files, unchecked", paused(func(r *RecoveryRow) {
				r.Qualifier = "the turn's own bound"
				r.Detail = []string{"3 files changed +30 −4 · the suite has not been re-run since"}
				r.Keys = []KeyOffer{{Key: "[v]", Label: "review what it did"},
					{Key: "[+10]", Label: "ten more rounds"}, {Key: "[u]", Label: "undo the turn"}}
			})},
			{"changed nothing", paused(func(r *RecoveryRow) {
				r.Qualifier = "the turn's own bound"
				r.Detail = []string{"nothing changed"}
				r.Keys = []KeyOffer{{Key: "[+10]", Label: "ten more rounds"}}
			})},
			{"already granted once", paused(func(r *RecoveryRow) {
				r.Subject, r.Qualifier = "35 of 35 used", "10 already granted"
				r.Detail = []string{"3 files changed +30 −4"}
				r.Keys = []KeyOffer{{Key: "[v]", Label: "review what it did"},
					{Key: "[+10]", Label: "ten more rounds"}, {Key: "[u]", Label: "undo the turn"}}
			})},
			{"the grant is spent", paused(func(r *RecoveryRow) {
				r.Qualifier = "the turn's own bound"
				r.Detail = []string{"3 files changed +30 −4 · the suite has not been re-run since"}
				r.Keys = []KeyOffer{{Key: "[v]", Label: "review what it did"},
					{Key: "[u]", Label: "undo the turn"}}
			})},
		}},
		{"retry countdown", []monoState{
			{"just started", waiting(func(w *RetryWait) {})},
			{"nearly out", waiting(func(w *RetryWait) {
				w.Pct, w.Text = 5, "retry in 1s"
			})},
			{"last attempt", waiting(func(w *RetryWait) {
				w.Note = "attempt 3 of 3"
			})},
			{"with a fallback", waiting(func(w *RetryWait) {
				w.Keys = append([]KeyOffer{{Key: "[m]", Label: "finish this turn on gpt-4o-mini"}}, w.Keys...)
			})},
		}},
		{"provider card place", []monoState{
			{"nothing found", ProviderCard{Places: []ProviderPlace{
				{Label: "env", Detail: "OPENAI_API_KEY — unset"}}}.View(w)},
			{"something found", ProviderCard{Places: []ProviderPlace{
				{Label: "env", Emphasis: "OPENAI_API_KEY ···4f9c", Found: true}}}.View(w)},
		}},
		{"context pressure", []monoState{
			{"warning", pressed(70, 140_000)},
			{"alert", pressed(94, 188_000)},
			{"full", pressed(100, 200_000)},
		}},
		{"undo drift", []monoState{
			{"clean", undo(nil)},
			{"drifted", undo([]string{"internal/agent/loop.go"})},
			{"drifted twice", undo([]string{"internal/agent/loop.go", "internal/ui/chat/model.go"})},
		}},
		{"plan checklist state", []monoState{
			{"queued", checklist(PlanStepQueued)},
			{"running", checklist(PlanStepRunning)},
			{"done", checklist(PlanStepDone)},
			{"failed", checklist(PlanStepFailed)},
		}},
		{"plan step intent", []monoState{
			{"read only", planned(func(c *PlanCard) {})},
			{"edits", planned(func(c *PlanCard) {
				c.Steps[0].Kind, c.Steps[0].KindTone = "✎ edits 1 file", ToneNeutral
			})},
			{"creates", planned(func(c *PlanCard) {
				c.Steps[0].Kind, c.Steps[0].KindTone = "✎ creates 1 file", ToneNeutral
			})},
			{"deletes", planned(func(c *PlanCard) {
				c.Steps[0].Kind, c.Steps[0].KindTone = "✎ deletes 1 file", ToneRisk
			})},
			{"runs", planned(func(c *PlanCard) {
				c.Steps[0].Kind, c.Steps[0].KindTone = "$ runs", ToneNeutral
			})},
			{"network", planned(func(c *PlanCard) {
				c.Steps[0].Kind, c.Steps[0].KindTone = "network", ToneOpen
			})},
		}},
		{"plan reversibility", []monoState{
			{"reversible", planned(func(c *PlanCard) {})},
			{"partly reversible", planned(func(c *PlanCard) {
				c.Summary[1] = PlanFact{Text: "partly reversible", Tone: ToneNeutral}
			})},
			{"not reversible", planned(func(c *PlanCard) {
				c.Summary[1] = PlanFact{Text: "not reversible", Tone: ToneRisk}
			})},
			{"nothing to put back", planned(func(c *PlanCard) {
				c.Summary[1] = PlanFact{Text: "nothing to put back", Tone: ToneSafe}
			})},
		}},
		{"context meter pressure", []monoState{
			{"healthy", meter(40)},
			{"pressured", meter(75)},
			{"critical", meter(95)},
		}},
		{"config value", []monoState{
			{"permissive mode", configValue("⏵⏵ auto", ToneSafe)},
			{"gated mode", configValue("⏸ manual", ToneOpen)},
			{"contained", configValue("⛨ workspace-netless", ToneSafe)},
			{"a door left open", configValue("private hosts reachable", ToneOpen)},
		}},
		{"config source", []monoState{
			{"nothing set", configSourced("default", ToneNeutral)},
			{"the file set it", configSourced("user", ToneNeutral)},
			{"staged, not written", configSourced("unwritten", ToneOpen)},
			{"the host cannot honour it", configSourced("unavailable on this host", ToneRisk)},
		}},
		{"metrics category", []monoState{
			{"an ordinary share", metricsBar("$ run", MeterCategory)},
			{"a sub-agent's share", metricsBar("◇ agents", MeterAgent)},
			{"a cost nobody asked for", metricsBar("✗ no answer", MeterUnasked)},
		}},
		{"doctor state", []monoState{
			{"passed", doctorState(DoctorPassed)},
			{"warned", doctorState(DoctorWarned)},
			{"failed", doctorState(DoctorFailed)},
			{"nothing to check", doctorState(DoctorSkipped)},
			{"running", doctorState(DoctorRunning)},
			{"queued", doctorState(DoctorQueued)},
		}},
		{"attachment kind", []monoState{
			{"an image", chipStrip(ChipImage)},
			{"a document", chipStrip(ChipDocument)},
			{"text", chipStrip(ChipText)},
		}},
		{"the staged image preview", []monoState{
			{"a picture", pictureCard(func(*PictureView) {})},
			{"a picture of another shape", pictureCard(func(p *PictureView) {
				p.Image = testPicture(160, 20)
			})},
			{"nothing to draw", pictureCard(func(p *PictureView) {
				p.Image, p.Note = nil, "shhh draws PNG, JPEG and GIF previews"
			})},
		}},
		{"history outcome", []monoState{
			{"ran clean", historyRow(ActivityDone, "exit 0")},
			{"broke", historyRow(ActivityFailed, "exit 128")},
			{"dismissed", historyRow(ActivityDenied, "dismissed")},
			{"never run", historyRow(ActivityQueued, "not run")},
			{"copied instead", historyRow(ActivityDone, "copied")},
		}},
	}
}

// TestMonoConformance is the invariant check: with the palette down to two
// greys, no two states of a surface may render to the same plain text.
func TestMonoConformance(t *testing.T) {
	monoOn(t)
	for _, s := range monoFixtures() {
		seen := map[string]string{}
		for _, st := range s.states {
			plain := strings.TrimRight(ansi.Strip(st.view), " \n")
			if plain == "" {
				t.Errorf("%s/%s: rendered nothing to tell it apart by", s.name, st.name)
				continue
			}
			if prev, dup := seen[plain]; dup {
				t.Errorf("%s: %q and %q are identical once colour is stripped — the state needs a glyph or a word:\n%s",
					s.name, prev, st.name, plain)
				continue
			}
			seen[plain] = st.name
		}
	}
}

// sgrParams pulls the parameter list out of every SGR escape in s.
var sgrPattern = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// index256 is the 256-colour index a token stands for, written the way an SGR
// escape writes it. A token holds a colour value per profile rather than the
// digits (S-155), so the digits are read back off the ANSI256 rung. The three
// mono shades are all above sixteen, so all three are indexed colours.
func index256(t Token) string {
	i, ok := t.ANSI256.(lipgloss.ANSIColor)
	if !ok {
		return ""
	}
	return strconv.Itoa(int(i))
}

// allowedMonoSGR is what a mono render may emit: the attribute codes that
// carry weight and shape, and the two greys (plus the selection background)
// of tokens/colors.css. Anything else is a colour that survived the swap.
func allowedMonoSGR(params string) bool {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "", "0", "1", "2", "3", "4", "7", "22", "23", "24", "27", "39", "49":
			continue
		case "38", "48":
			// 256-colour foreground/background: 38;5;N.
			if i+2 >= len(fields) || fields[i+1] != "5" {
				return false
			}
			switch fields[i+2] {
			case index256(MonoFg), index256(MonoDim), index256(MonoBg):
				i += 2
				continue
			}
			return false
		default:
			// A bare 30–37/90–97 colour is still a colour.
			if n, err := strconv.Atoi(fields[i]); err == nil && n >= 30 {
				return false
			}
			return false
		}
	}
	return true
}

// TestMonoRendersTwoGreys checks the other half of the claim: mono does not
// merely keep states distinguishable, it actually strips the palette. Every
// escape a surface emits must be an attribute or one of the mono shades.
func TestMonoRendersTwoGreys(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	monoOn(t)
	for _, s := range monoFixtures() {
		for _, st := range s.states {
			for _, m := range sgrPattern.FindAllStringSubmatch(st.view, -1) {
				if !allowedMonoSGR(m[1]) {
					t.Errorf("%s/%s emits SGR %q, which is not one of the two greys", s.name, st.name, m[1])
				}
			}
		}
	}
}

// TestMonoLeavesTheFullPaletteIntact guards the swap itself: turning mono off
// restores the colours, so the check above is testing a real change.
func TestMonoLeavesTheFullPaletteIntact(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	was := mono
	t.Cleanup(func() { SetMono(was) })

	SetMono(false)
	colored := ActivityRow{Kind: ActivityEdit, Verb: "edit", Target: "loop.go", Outcome: OutcomeOK}.View(60)
	if Mono() {
		t.Fatal("mono should be off")
	}
	if Palette != FullPalette {
		t.Fatal("the full palette should be back")
	}
	var offPalette bool
	for _, m := range sgrPattern.FindAllStringSubmatch(colored, -1) {
		if !allowedMonoSGR(m[1]) {
			offPalette = true
		}
	}
	if !offPalette {
		t.Fatal("with mono off the row should render in colours the mono check would reject")
	}

	SetMono(true)
	if !Mono() || Palette != MonoPalette {
		t.Fatal("mono should be on with the mono palette")
	}
}

// TestMonoDeclinesSyntaxHighlighting covers the syntax register: even
// though it is drawn from the palette now, mono declines it outright rather
// than collapsing it onto the two greys, because the +/- styling under it is
// already carrying the distinction that matters.
func TestMonoDeclinesSyntaxHighlighting(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	monoOn(t)
	syntax := func(line string) []Segment {
		return []Segment{{Text: line, Color: Palette.Info}}
	}
	hunks := []diff.Hunk{{
		OldStart: 1, OldCount: 0, NewStart: 1, NewCount: 1,
		Lines: []diff.Line{{Kind: diff.Add, Text: "x := 1", NewNo: 1}},
	}}
	out := strings.Join(UnifiedLines(hunks, 60, UnifiedOpts{Syntax: syntax}), "\n")
	for _, m := range sgrPattern.FindAllStringSubmatch(out, -1) {
		if !allowedMonoSGR(m[1]) {
			t.Fatalf("mono diff kept a syntax colour: SGR %q", m[1])
		}
	}
	if !strings.Contains(ansi.Strip(out), "+x := 1") {
		t.Fatalf("the line should still read as an addition, got %q", ansi.Strip(out))
	}
}

// TestMonoFromEnv covers the environment half of the switch: NO_COLOR turns
// mono on for the session regardless of its value, and so does a terminal
// with no attributes at all.
func TestMonoFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", map[string]string{}, false},
		{"NO_COLOR empty is not set", map[string]string{"NO_COLOR": ""}, false},
		{"NO_COLOR any value", map[string]string{"NO_COLOR": "0"}, true},
		{"dumb terminal", map[string]string{"TERM": "dumb"}, true},
		{"ordinary terminal", map[string]string{"TERM": "xterm-256color"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := monoFromEnv(func(k string) string { return tc.env[k] })
			if got != tc.want {
				t.Fatalf("monoFromEnv(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
