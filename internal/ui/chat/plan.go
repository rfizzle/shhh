package chat

// Plan mode's in-session flow (S-061): while the session is in plan mode the
// stream request carries planning instructions and the mode policy in
// internal/agent refuses gated calls (waving through read-only inspection
// commands). When the model finishes a planning response, the plan-approval
// card takes over the input area: the user executes the plan in a chosen
// mode, keeps planning, or rejects it — all in the same session.
//
// The card itself is S-103. The plan is parsed into steps and priced once,
// when the prompt is armed, for the same reason the S-101 blast radius is:
// pricing asks git about every file the plan names, and View runs per frame.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// planApprovedMessage is the user-role message that turns an approved plan
// into the execution turn, with the plan already in context.
const planApprovedMessage = "The plan is approved. Leave planning and execute it now, using your tools."

// planApproveOptions are the plan-approval rows, in order; selection is by
// index (see selectPlanOption). Each execution option names the mode the
// session enters, because accepting a plan is never an unstated mode change —
// and its description says what that mode will and will not stop for.
var planApproveOptions = []components.SelectOption{
	{
		Label: "Run the whole plan — accept-edits mode",
		Desc:  "edits apply as they come; commands and other actions still ask",
	},
	{
		Label: "Run it unattended — auto mode",
		Desc:  "edits and allowlisted commands run; the classifier judges the rest",
	},
	{
		Label: "Step through it — manual approvals",
		Desc:  "every edit and every command asks you first",
	},
	{
		Label: "Keep planning — tell me what to change",
		Desc:  "stays in plan mode; the plan keeps its place in the conversation",
	},
	{
		Label: "Reject the plan",
		Desc:  "nothing runs and the session stays in plan mode",
	},
}

// planHint is the key row under the options. [s] is here rather than on a
// card of its own because saving is not a decision — it is something you do
// on the way to one.
const planHint = "↑↓/jk move · enter select · 1–5 jump · s save · esc keep planning"

// armPlan parses and prices the planning response the prompt is about to ask
// about. It runs once, when the prompt opens.
func (m *Model) armPlan() {
	m.planChoice = 0
	m.planDoc = plan.Parse(m.lastAssistantText())
	m.planFacts, m.planDetail = m.planRadius(m.planDoc)
}

// clearPlan drops the armed plan once the prompt is answered, so nothing
// stale can be rendered or saved by a later keystroke.
func (m *Model) clearPlan() {
	m.planDoc = plan.Plan{}
	m.planFacts, m.planDetail = nil, ""
}

// updatePlanApprove handles keys while the plan-approval card is showing.
func (m Model) updatePlanApprove(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch pressed := msg.String(); {
	case pressed == "up", pressed == "k":
		if m.planChoice > 0 {
			m.planChoice--
		}
		return m, nil
	case pressed == "down", pressed == "j":
		if m.planChoice < len(planApproveOptions)-1 {
			m.planChoice++
		}
		return m, nil
	case keys.Is(pressed, keys.Select.Take):
		return m.selectPlanOption(m.planChoice)
	case pressed >= "1" && pressed <= "5" && len(pressed) == 1:
		return m.selectPlanOption(int(pressed[0] - '1'))
	case pressed == "s", pressed == "S":
		return m.savePlanFromCard()
	case keys.Is(pressed, keys.Select.Cancel):
		// Esc never destroys: dismissing the prompt keeps planning.
		return m.keepPlanning()
	case keys.Is(pressed, keys.Draft.Quit):
		m.quitting = true
		return m, m.quitCmd()
	}
	return m, nil
}

func (m Model) selectPlanOption(idx int) (tea.Model, tea.Cmd) {
	switch idx {
	case 0:
		return m.approvePlan(agent.ModeAcceptEdits)
	case 1:
		return m.approvePlan(agent.ModeAuto)
	case 2:
		return m.approvePlan(agent.ModeManual)
	case 3:
		return m.keepPlanning()
	case 4:
		return m.rejectPlan()
	}
	return m, nil
}

// approvePlan continues the same session straight into execution: the mode
// switches to the chosen one and the approval message becomes the next user
// turn, with the plan already in context.
func (m Model) approvePlan(execMode agent.Mode) (tea.Model, tea.Cmd) {
	m.applyMode(execMode)
	doc := m.planDoc
	m.clearPlan()
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("Plan approved — executing in %s mode.", execMode)})
	m.recordCheckpoint(planApprovedMessage)
	m.agent.StartTurn(planApprovedMessage)
	m.appendEntry(entry{kind: entryUser, text: planApprovedMessage})
	// From here the approved plan is the transcript's step list, the rail's
	// PLAN block and what /plan answers with (S-104). A plan that never
	// adopted the step shape has no list to keep, and newPlanRun says so.
	m.planRun = newPlanRun(doc, len(m.transcript))
	m.invalidateRenderCache()
	m.trimForRequest()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, tea.Batch(m.requestStream(), m.autosaveCmd())
}

// keepPlanning dismisses the prompt so the user can send feedback; the
// session stays in plan mode.
func (m Model) keepPlanning() (tea.Model, tea.Cmd) {
	m.clearPlan()
	m.setTurnState(stateInput)
	m.syncViewport()
	m.appendEntry(entry{kind: entrySystem, text: "Keep planning — describe what to change and the agent will revise the plan."})
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
}

// rejectPlan discards the prompt; the session stays in plan mode and the plan
// remains in the transcript for reference.
func (m Model) rejectPlan() (tea.Model, tea.Cmd) {
	m.clearPlan()
	m.setTurnState(stateInput)
	m.syncViewport()
	m.appendEntry(entry{kind: entrySystem, text: "Plan rejected. Still in plan mode — give new directions, or switch modes with Shift+Tab or /mode."})
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
}

// savePlanFromCard is [s]: it writes the plan exactly as the model wrote it
// through the same savePlan that /plan save uses, and leaves the card up.
// Saving is not a decision, so it does not answer one.
func (m Model) savePlanFromCard() (tea.Model, tea.Cmd) {
	text := m.planDoc.Text
	if strings.TrimSpace(text) == "" {
		text = m.lastAssistantText()
	}
	if strings.TrimSpace(text) == "" {
		m.appendEntry(entry{kind: entrySystem, text: "No plan to save yet."})
	} else if path, err := savePlan(text, ""); err != nil {
		m.appendEntry(entry{kind: entrySystem, text: "Error saving plan: " + err.Error()})
	} else {
		m.appendEntry(entry{kind: entrySystem, text: "Plan saved to " + path})
	}
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
}

// planPanelBound is how tall the plan card may grow. An approval card keeps
// the 40% bound (docs/interface/principles.md#the-grammar) because the
// context for its decision is the transcript behind it — the file it is
// about, the output that led here. A plan card carries its own context: the
// steps *are* the decision, and at 40% of a 30-row terminal the fixed rows
// leave room for none of them, which is a prompt about nothing. It gets 60%,
// and the transcript keeps the plan in full either way.
func (m Model) planPanelBound() int {
	return max(m.height*3/5, inputHeight)
}

// planCard builds the card from the plan armed when the prompt opened. It
// reads nothing from disk — everything it needs was resolved in armPlan.
func (m Model) planCard() *components.PlanCard {
	doc := m.planDoc
	card := &components.PlanCard{
		Title:         "Plan ready",
		Options:       planApproveOptions,
		Focus:         m.planChoice,
		Hint:          planHint,
		Summary:       m.planFacts,
		SummaryDetail: m.planDetail,
		MaxLines:      m.planPanelBound(),
	}
	if m.decisionUngated() {
		// The plan landed while a sentence was half-typed: its keys are not
		// live until ctrl+g hands the keyboard over (S-117).
		card.NotYetLive, card.Handover = true, keys.Shown(keys.Draft.Answer)
	}
	if doc.Title != "" {
		card.Title = "Plan · " + doc.Title
	}
	if n := len(doc.Steps); n > 0 {
		card.Chip = fmt.Sprintf("%d steps", n)
		if n == 1 {
			card.Chip = "1 step"
		}
	}
	for _, s := range doc.Steps {
		card.Steps = append(card.Steps, planStepRow(s))
	}
	if prose := strings.TrimSpace(m.wordWrap(doc.Text, m.contentWidth()-4)); !doc.Structured() && prose != "" {
		// A plan with no structure is still a plan: it renders as the prose
		// the model wrote, with the same options below it.
		card.Prose = strings.Split(prose, "\n")
	}
	return card
}

// planStepRow turns one parsed step into the card's row pair.
func planStepRow(s plan.Step) components.PlanStep {
	row := components.PlanStep{Number: s.Number, Title: s.Title}
	detail := append([]string(nil), s.Paths...)
	if s.Note != "" {
		detail = append(detail, s.Note)
	}
	row.Detail = strings.Join(detail, " · ")
	row.Kind, row.KindTone = planStepKind(s)
	return row
}

// planStepKind is the step's right-hand label. It states the intent in words
// and counts only paths the model actually named — a step that said what it
// would do but not where says so, and claims no count. The glyphs are the
// four the design system already owns (⚙ ✎ $ ◇); a step that persists nothing
// carries none, which is the difference the reader is scanning for.
func planStepKind(s plan.Step) (string, components.FieldTone) {
	verb, tone := "", components.ToneNeutral
	switch s.Action {
	case plan.Edit:
		verb = "✎ edits"
	case plan.Create:
		verb = "✎ creates"
	case plan.Delete:
		verb, tone = "✎ deletes", components.ToneRisk
	case plan.Run:
		return "$ runs", components.ToneNeutral
	case plan.Network:
		return "network", components.ToneOpen
	default:
		return "read only", components.ToneSafe
	}
	if n := len(s.Paths); n > 0 {
		return verb + " " + filesWord(n), tone
	}
	return verb, tone
}

// planRadius prices the whole plan in one line: what it touches, whether
// anything is deleted, whether the network is needed, and whether it can be
// put back. The reversibility answer comes from the same git-tracked check
// the approval cards use (S-101) — shhh will not claim more about a plan than
// it claims about one edit.
func (m Model) planRadius(doc plan.Plan) ([]components.PlanFact, string) {
	if !doc.Structured() {
		return nil, ""
	}
	writes := doc.WritePaths()
	facts := []components.PlanFact{{Text: touchedWord(len(writes))}}
	if n := len(doc.DeletePaths()); n > 0 {
		facts = append(facts, components.PlanFact{
			Text: fmt.Sprintf("deletes %s", filesWord(n)), Tone: components.ToneRisk,
		})
	} else {
		facts = append(facts, components.PlanFact{Text: "no deletes", Tone: components.ToneSafe})
	}
	if doc.NeedsNetwork() {
		facts = append(facts, components.PlanFact{Text: "network needed", Tone: components.ToneOpen})
	} else {
		facts = append(facts, components.PlanFact{Text: "no network", Tone: components.ToneSafe})
	}
	fact, detail := m.planReversibility(writes)
	return append(facts, fact), detail
}

// planReversibility answers what could be done about the plan's files
// afterwards. It counts the paths git already knows, which is the only claim
// shhh can make before a single edit has been recorded.
func (m Model) planReversibility(writes []string) (components.PlanFact, string) {
	if len(writes) == 0 {
		return components.PlanFact{Text: "nothing to put back", Tone: components.ToneSafe},
			"no step names a file it would change"
	}
	if !m.tracker.Repo() {
		return components.PlanFact{Text: "not reversible", Tone: components.ToneRisk},
			"this is not a git work tree"
	}
	tracked := 0
	for _, path := range writes {
		if m.tracker.Track(path) == changeset.TrackTracked {
			tracked++
		}
	}
	switch {
	case tracked == len(writes):
		return components.PlanFact{Text: "reversible", Tone: components.ToneSafe},
			"every file is tracked in git"
	case tracked == 0:
		return components.PlanFact{Text: "not reversible", Tone: components.ToneRisk},
			"no file it names is tracked in git yet"
	}
	return components.PlanFact{Text: "partly reversible", Tone: components.ToneNeutral},
		fmt.Sprintf("%d of %d files tracked in git", tracked, len(writes))
}

// touchedWord counts the plan's write targets. A plan that names none is not
// described as touching nothing — it is described as having said nothing,
// which is a different claim.
func touchedWord(n int) string {
	if n == 0 {
		return "no files named"
	}
	return filesWord(n) + " touched"
}

// filesWord renders "1 file" / "3 files".
func filesWord(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// planApproveLines renders the plan-approval card, one row per element.
func (m Model) planApproveLines() []string {
	return strings.Split(m.planCard().View(m.contentWidth()), "\n")
}

// planPanelLines is the card plus the rail that names the keyboard's owner
// and the draft it is holding while it does (S-117).
func (m Model) planPanelLines() []string {
	return m.dressDecision(m.planApproveLines(), m.contentWidth())
}

// renderPlanApprove renders the plan-approval card padded to the bottom
// panel height.
func (m Model) renderPlanApprove() string {
	lines := m.planPanelLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// savePlan writes the plan text to .shhh/plans/<name>.md (an empty name gets
// a timestamp) and returns the written path. Saving is optional — the
// approval flow never requires it.
func savePlan(text, name string) (string, error) {
	name = sanitizePlanName(strings.TrimSuffix(name, ".md"))
	dir := filepath.Join(".shhh", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".md")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// sanitizePlanName keeps plan names filesystem-safe: letters, digits, dot,
// dash, underscore; anything else becomes a dash. Empty (or all-unsafe) names
// fall back to a timestamp.
func sanitizePlanName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.TrimLeft(b.String(), ".-")
	if out == "" {
		out = "plan-" + time.Now().Format("20060102-150405")
	}
	return out
}

// planUsage is what /plan answers to when it is given something it does not
// know; the checklist itself is the bare form.
const planUsage = "Usage: /plan · /plan save [name] · /plan drop"

// planHintRail is the PLAN block's last row. It names the command rather than
// a bracketed key because the rail's keys are the host's, and the input
// textarea owns every unmodified letter — a `[p]` printed there would be an
// offer nothing accepts (the reason S-101 kept [y/n/a]).
const planHintRail = "/plan for the whole list"

// declaredSteps is the approved plan's step list, or nil when no plan is
// running — or when the transcript on screen belongs to a child, which the
// orchestrator's plan says nothing about.
func (m Model) declaredSteps() []plan.Step {
	if m.planRun == nil || m.attachedTo != "" {
		return nil
	}
	return m.planRun.doc.Steps
}

// blocksOf tiles entries into transcript blocks with the approved plan's
// declared steps overlaid, which is the one call site every reader of the
// outline should use.
func (m Model) blocksOf(es []entry) []transcriptBlock {
	return stepBlocks(es, m.declaredSteps())
}

// lastLiveBlock is the index of the last block rows can still land in — the
// last one with entries. Declared steps that have not started trail it and
// change as the run reaches them, so nothing from there on can be frozen.
func lastLiveBlock(blocks []transcriptBlock) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].end > blocks[i].start {
			return i
		}
	}
	return 0
}

// planEntries is the slice of the transcript the approved plan has been
// carried out over. It starts where the execution turn started, so a plan
// that outlives one turn keeps its checklist and one that has been rewound
// past reads nothing.
func (m Model) planEntries() []entry {
	if m.planRun == nil || m.planRun.start > len(m.transcript) {
		return nil
	}
	return m.transcript[m.planRun.start:]
}

// stampStep records which declared step an assistant announcement carries
// out, so the outline, the rail and /plan all read one assignment made in the
// order the transcript was written (S-104). An entry that is not a step
// title, or a session with no plan running, is returned untouched.
func (m *Model) stampStep(e entry) entry {
	if m.planRun == nil {
		return e
	}
	title, ok := stepTitle(e)
	if !ok {
		return e
	}
	e.planStep = m.planRun.claim(title)
	return e
}

// planChecklist reads the state of every declared step off the transcript. It
// stores nothing: a step's state is its group's state, so the checklist and
// the outline cannot disagree about what happened.
func (m Model) planChecklist() []components.InspectorPlanStep {
	run := m.planRun
	if run == nil {
		return nil
	}
	es := m.planEntries()
	type observed struct {
		state stepState
		label string
	}
	at := map[int]observed{}
	for _, blk := range m.blocksOf(es) {
		g := blk.step
		if g == nil || g.offPlan || g.queued() {
			continue
		}
		h := m.headerFor(blk, es)
		o := observed{state: h.State}
		if h.State != stepRunning {
			o.label = activityDuration(h.Duration)
		}
		at[g.ordinal] = o
	}
	out := make([]components.InspectorPlanStep, 0, len(run.doc.Steps))
	for _, s := range run.doc.Steps {
		row := components.InspectorPlanStep{Number: s.Number, Title: s.Title}
		// A run carried across a compaction starts from what it observed
		// before the transcript went; anything the new transcript records
		// outranks it (S-108).
		if c, ok := run.carried[s.Number]; ok {
			row.State, row.Elapsed = c.State, c.Elapsed
		}
		if o, ok := at[s.Number]; ok {
			row.State, row.Elapsed = planStepState(o.state), o.label
		}
		out = append(out, row)
	}
	return out
}

// planStepState maps an outline step's state onto the rail's, which is the
// same four states under another name — the two surfaces draw one step.
func planStepState(s stepState) components.PlanStepState {
	switch s {
	case stepRunning:
		return components.PlanStepRunning
	case stepFailed:
		return components.PlanStepFailed
	case stepDone:
		return components.PlanStepDone
	}
	return components.PlanStepQueued
}

// planStepsDone counts the checklist's finished steps. A failed step
// finished.
func planStepsDone(steps []components.InspectorPlanStep) int {
	done := 0
	for _, s := range steps {
		if s.State == components.PlanStepDone || s.State == components.PlanStepFailed {
			done++
		}
	}
	return done
}

// planProgress is how far through its steps the run is, for the THIS TURN
// meter: the finished steps, plus the one in flight, because the step being
// worked on is the step you are on.
func planProgress(steps []components.InspectorPlanStep) int {
	n := planStepsDone(steps)
	for _, s := range steps {
		if s.State == components.PlanStepRunning {
			n++
			break
		}
	}
	return min(n, len(steps))
}

// planStatus is /plan: the approved plan as a checklist, and what the run has
// done that the plan did not say. It is the same list the rail draws, so a
// terminal too narrow for the rail loses nothing.
func (m Model) planStatus() string {
	run := m.planRun
	if run == nil {
		if m.mode == agent.ModePlan {
			return "No plan approved yet — the card offers the checklist once one is.\n" + planUsage
		}
		return "No approved plan is running.\n" + planUsage
	}
	steps := m.planChecklist()
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %d of %d done\n", run.title(), planStepsDone(steps), len(steps))
	for _, s := range steps {
		fmt.Fprintf(&b, "  %s %d  %s", planStatusGlyph(s.State), s.Number, s.Title)
		if note := planStatusNote(s); note != "" {
			b.WriteString(" · " + note)
		}
		b.WriteString("\n")
	}
	if d := run.drift(); len(d) > 0 {
		b.WriteString("Drift: " + strings.Join(d, "; ") + ".")
	} else {
		b.WriteString("No drift — every step so far is one the plan named, in the order it named them.")
	}
	return b.String()
}

// planStatusGlyph is the checklist glyph /plan prints, the same four the
// outline and the rail use.
func planStatusGlyph(s components.PlanStepState) string {
	switch s {
	case components.PlanStepRunning:
		return "▸"
	case components.PlanStepDone:
		return "✓"
	case components.PlanStepFailed:
		return "✗"
	}
	return "·"
}

// planStatusNote is the step's right-hand word: what it cost, or what it is
// waiting on. A step that finished in under half a second reports nothing,
// like every other duration in the product.
func planStatusNote(s components.InspectorPlanStep) string {
	switch s.State {
	case components.PlanStepQueued:
		return components.OutcomeQueued
	case components.PlanStepRunning:
		return "running"
	}
	return s.Elapsed
}
