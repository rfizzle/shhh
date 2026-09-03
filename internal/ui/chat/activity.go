package chat

// Compact activity feed (docs/interface/principles.md#one-grid): tool
// calls and commands render as one-line activity rows — glyph, action, key
// argument, outcome, counts, duration — never raw output blocks by default.
// Focus mode expands a row in place, /ui verbosity changes the default
// density, and a running command shows a live output tail in its row.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/caps"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/web"
)

// verbosity is the activity feed's default density, and the three levels have
// three distinct meanings (docs/interface/surfaces.md#the-step): low
// shows step headers only and draws no think row at all
// (docs/interface/surfaces.md#the-think-row), normal folds a step's
// consecutive read-only calls into one counted row, high expands every row
// with its bounded detail body.
type verbosity int

const (
	verbosityLow verbosity = iota
	verbosityNormal
	verbosityHigh
)

func (v verbosity) String() string {
	switch v {
	case verbosityLow:
		return "low"
	case verbosityHigh:
		return "high"
	}
	return "normal"
}

func parseVerbosity(s string) (verbosity, error) {
	switch s {
	case "low":
		return verbosityLow, nil
	case "normal", "norm", "med", "medium":
		return verbosityNormal, nil
	case "high":
		return verbosityHigh, nil
	}
	return verbosityNormal, fmt.Errorf("unknown verbosity %q (low, normal, high)", s)
}

// TailFunc runs a command like the plain runner while reporting each
// completed output line, so the row can show a live tail. onLine may be
// called from other goroutines.
type TailFunc func(ctx context.Context, command string, onLine func(string)) (string, int)

// WithTailRunner sets the tail-capable runner used for assistant commands and
// /run; without one, commands run with no live tail.
func (m Model) WithTailRunner(fn TailFunc) Model {
	m.tailRunFn = fn
	return m
}

// commandTail is the running command's last output line, shared between the
// runner goroutine and the render loop.
type commandTail struct {
	mu   sync.Mutex
	line string
}

func (t *commandTail) Set(line string) {
	t.mu.Lock()
	t.line = line
	t.mu.Unlock()
}

func (t *commandTail) Line() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.line
}

// pendingToolResult marks a mirrored child tool call that hasn't finished yet
// ; the activity row renders it as running.
const pendingToolResult = "running…"

// cancelledToolResult is the synthetic result left on a tool call abandoned
// when the turn was cancelled.
const cancelledToolResult = "cancelled by user"

// The deciders named on a denied row: your preference, or a rule.
const (
	decidedByYou  = "you"
	decidedByAuto = "auto"
)

// activityVerbs is the one table mapping tool names onto the closed verb
// vocabulary of docs/interface/principles.md#closed-vocabularies — read,
// search, glob, lsp, web, edit, write, patch, run, memory, spawn, fan-out,
// agent, report. A tool that maps onto none of them is a hole in this table,
// not a fifteenth verb: it renders as itself, clipped to the verb column,
// which is the signal that the table is stale.
var activityVerbs = map[string]string{
	"read_file":                 "read",
	"list_directory":            "read",
	evidence.ToolName:           "read",
	"search":                    "search",
	structural.AstGrepToolName:  "search",
	structural.JaqToolName:      "search",
	structural.YqToolName:       "search",
	structural.TokeiToolName:    "search",
	structural.GitToolName:      "read",
	"glob":                      "glob",
	structural.FdToolName:       "glob",
	lsp.DefinitionToolName:      "lsp",
	lsp.ReferencesToolName:      "lsp",
	lsp.WorkspaceSymbolToolName: "lsp",
	lsp.DocumentSymbolToolName:  "lsp",
	lsp.HoverToolName:           "lsp",
	lsp.DiagnosticsToolName:     "lsp",
	web.FetchToolName:           "web",
	web.SearchToolName:          "web",
	"edit_file":                 "edit",
	"write_file":                "write",
	structural.SdToolName:       "patch",
	tools.ExecCommandName:       "run",
	process.ToolName:            "run",
	quality.ToolName:            "run",
	memory.RememberToolName:     "memory",
	skill.ToolName:              "read",
	subagent.SpawnToolName:      "spawn",
	subagent.ReportToolName:     "agent",
	reports.ToolName:            "report",
}

func activityVerb(tool string) string {
	if v, ok := activityVerbs[tool]; ok {
		return v
	}
	if _, ok := mcp.SplitName(tool); ok {
		return "mcp"
	}
	return tool
}

// activityKind picks the row's glyph and, with it, whether the row carries
// the mutation rail: ⚙ reads, $ commands, ✎ anything that
// persists, ◇ sub-agents, ⇄ a server call the user did not mark read-only.
func (m Model) activityKind(tool string) components.ActivityKind {
	switch {
	case m.mcp.Has != nil && m.mcp.Has(tool):
		// A read-only server's call is a read and draws as one; every
		// other server's call is an act shhh cannot see the far side of
		// (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).
		if m.mcp.ReadOnly(tool) {
			return components.ActivityTool
		}
		return components.ActivityRemote
	case tool == subagent.SpawnToolName || tool == subagent.ReportToolName:
		return components.ActivitySubagent
	case tool == reports.ToolName:
		return components.ActivityReport
	case tool == tools.ExecCommandName || tool == process.ToolName || tool == quality.ToolName:
		return components.ActivityCommand
	case tools.IsMutating(tool) || tool == memory.RememberToolName || tool == structural.SdToolName:
		return components.ActivityEdit
	}
	return components.ActivityTool
}

// activityCounts summarizes a result's size with a tool-appropriate noun
// (matches found, items listed, lines read).
func activityCounts(tool, result string) string {
	if strings.TrimSpace(result) == "" {
		return ""
	}
	n := len(strings.Split(strings.TrimRight(result, "\n"), "\n"))
	singular, plural := "line", "lines"
	switch tool {
	case "search", "ast_grep":
		singular, plural = "match", "matches"
	case "glob", "list_directory", "fd":
		singular, plural = "item", "items"
	}
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func formatDuration(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// activityDuration renders the row's duration field. Under 0.5s it is
// blank rather than 0.0s: a column of zeroes down the feed is noise.
func activityDuration(d time.Duration) string {
	if d < 500*time.Millisecond {
		return ""
	}
	return formatDuration(d)
}

// turnDuration renders the duration field of a row whose span is a whole turn
// rather than one call: the three recovery rows, and the round-limit pause
// above all, since a turn that spent 25 tool rounds is measured in minutes.
// Past a minute `252s` stops reading as a duration, so the field takes
// minutes and seconds — packed, because the grid gives duration six columns
// and FormatElapsed's `4m 12s` would fill them and touch the outcome beside
// it. It is the form docs/interface/surfaces.md#the-recovery-row draws on
// this row.
func turnDuration(d time.Duration) string {
	if d < time.Minute {
		return activityDuration(d)
	}
	if mins := int(d.Minutes()); mins < 100 {
		return fmt.Sprintf("%dm%02ds", mins, int(d.Seconds())%60)
	}
	// Longer than that and the seconds are not the interesting part anyway.
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// activityRowFor builds the compact row for a tool or command entry, as
// it renders outside any step that has been opened. Everything that only
// wants to read a row's state — what it is, whether it ran, whether it broke
// — asks through here, because none of those answers depend on how much of
// the output is showing.
func (m Model) activityRowFor(e entry) components.ActivityRow {
	return m.activityRowDetail(e, false)
}

// activityRowDetail is the same row told whether the step around it has its
// detail open. Collapsed rows never show output; focus-mode expansion opens
// the wider in-place window, with the whole result one more press away on
// the full screen (docs/interface/surfaces.md#the-activity-row); failed
// rows, an opened step and high verbosity show the bounded detail view; and
// low verbosity hides counts. Every bounded body counts what it swallowed.
//
// A row you opened yourself keeps its wider body inside an opened step: the
// step's answer is the default for its rows, never a ceiling on one you
// asked about by name.
func (m Model) activityRowDetail(e entry, stepDetail bool) components.ActivityRow {
	row := components.ActivityRow{
		Expanded:  e.expanded || stepDetail || m.verbosity == verbosityHigh,
		MaxDetail: maxToolResultLines,
		Duration:  activityDuration(e.duration),
		Frame:     m.spinFrame,
	}
	if e.expanded {
		row.MaxDetail = maxExpandedResultLines
	}
	result := e.toolResult
	if e.kind == entryCommand {
		row.Kind = components.ActivityCommand
		row.Verb = "run"
		row.Target = firstLine(e.text)
		if e.exitCode != 0 {
			row.State = components.ActivityFailed
			row.Outcome = components.OutcomeExit(e.exitCode)
		} else {
			row.Outcome = components.OutcomeOK
		}
		// A `!!` run's output never joined the conversation, and the
		// outcome is where the row says so (bang.go).
		if e.localRun {
			row.Outcome += " · " + components.OutcomeLocal
		}
	} else {
		row.Kind = m.activityKind(e.toolName)
		row.Verb = activityVerb(e.toolName)
		row.Target = digest.Arg(e.toolName, e.toolArgs)
		switch {
		case e.deniedBy != "":
			// A refusal is not a failure: ⊘ and the decider's name say the
			// call never ran, and the duration field says so too.
			row.State = components.ActivityDenied
			row.ByRule = e.deniedBy != decidedByYou
			row.Outcome = components.OutcomeBy(components.OutcomeDenied, e.deniedBy)
			if e.denyRule != "" {
				row.Outcome += " · " + e.denyRule
			}
			if row.ByRule {
				row.Keys = "/permissions why"
			}
			if row.Duration == "" {
				row.Duration = components.NoDuration
			}
			result = ""
		case result == pendingToolResult:
			row.State = components.ActivityRunning
			row.Outcome = components.OutcomeRunning
			// The row animates from the session's one frame, and only
			// while the loop that advances it is running: a call left pending
			// by a cancelled turn keeps the still `▸` rather than standing on
			// one braille frame, which would read as a hang.
			row.Spin = m.spinnerWanted()
			result = ""
		case result == cancelledToolResult:
			// Ctrl+C during a turn: you stopped it, so it reads as your
			// refusal rather than as a break.
			row.State = components.ActivityDenied
			row.Outcome = components.OutcomeBy(components.OutcomeDenied, decidedByYou)
			row.Duration = components.NoDuration
			result = ""
		case strings.HasPrefix(result, "error:"):
			row.State = components.ActivityFailed
			row.Outcome = "error"
		case e.toolName == reports.ToolName:
			// The link is the outcome — the one field that never clips —
			// and the page is the body, so the row keeps nothing else. The
			// result's first line is the URL by the tool's own contract.
			row.Outcome = "→ " + firstLine(result)
			result = ""
		}
	}
	if strings.TrimSpace(result) != "" {
		row.Detail = strings.Split(strings.TrimRight(result, "\n"), "\n")
		if !row.Failed() && m.verbosity != verbosityLow {
			row.Counts = activityCounts(e.toolName, result)
		}
	}
	return row
}

// runningCommandRow renders the in-flight command as a live activity row with
// its output tail; spinner ticks keep it re-rendering while the command runs.
func (m Model) runningCommandRow(width int) string {
	row := components.ActivityRow{
		Kind:    components.ActivityCommand,
		State:   components.ActivityRunning,
		Verb:    "run",
		Target:  firstLine(m.runningCommand),
		Outcome: components.OutcomeRunning,
		Spin:    m.spinnerWanted(),
		Frame:   m.spinFrame,
	}
	if !m.runStart.IsZero() {
		row.Duration = activityDuration(time.Since(m.runStart))
	}
	if m.runTail != nil {
		row.Tail = m.runTail.Line()
	}
	return row.View(width)
}

// uiCommand handles /ui: the activity feed's verbosity, mono conformance,
// terminal mouse reporting, desktop notifications, what the terminal's own
// window is called, and what the terminal itself can do.
func (m *Model) uiCommand(parts []string) string {
	if len(parts) == 1 {
		return fmt.Sprintf("Activity feed verbosity: %s.\nMonochrome: %s.\nMouse reporting: %s.\nDesktop notifications: %s.\nSession titles: %s.\nWindow title: %s.\nLayout: %s.\nTerminal: %s.\n"+uiUsage, m.verbosity, monoStatus(), m.mouseStatus(), m.notifyStatus(), m.titleStatus(), m.windowStatus(), m.inspectorStatus(), terminalName(m.caps))
	}
	switch parts[1] {
	case "verbosity":
		if len(parts) == 2 {
			return fmt.Sprintf("Activity feed verbosity: %s.\nUsage: /ui verbosity <low|normal|high> — low shows step headers only and drops think rows, normal folds read-only groups, high expands every row.\nFor one step rather than all of them, /step opens the detail of the step in flight.", m.verbosity)
		}
		if len(parts) != 3 {
			return "Usage: /ui verbosity <low|normal|high>"
		}
		v, err := parseVerbosity(parts[2])
		if err != nil {
			return "Error: " + err.Error()
		}
		m.verbosity = v
		m.invalidateRenderCache()
		return fmt.Sprintf("Activity feed verbosity set to %s.", v)
	case "mono":
		return m.monoCommand(parts)
	case "mouse":
		return m.mouseCommand(parts)
	case "notify":
		return m.notifyCommand(parts)
	case "title":
		return m.titleCommand(parts)
	case "window":
		return m.windowCommand(parts)
	case "rail":
		return m.railCommand(parts)
	case "terminal":
		return m.terminalReport()
	}
	return uiUsage
}

// uiUsage is the one line naming everything /ui answers for. It is a constant
// because the bare readout and the unknown-subcommand reply are the same
// list, and a list written twice is a list that drifts.
const uiUsage = "Usage: /ui verbosity <low|normal|high> · /ui mono <on|off> · /ui mouse <on|off> · /ui notify <on|off> · /ui title <on|off> · /ui window <on|off> · /ui rail <auto|columns> · /ui terminal"

// terminalName is the one-line answer the bare /ui gives: what the terminal
// called itself when shhh asked. A terminal that was asked
// and did not name itself is not the same as one shhh never asked, and a
// readout that could not tell them apart would be the reason someone
// distrusts the rest of it.
func terminalName(t caps.Terminal) string {
	switch {
	case t.Name != "":
		return t.Name
	case !t.Asked:
		return "not asked"
	case t.Held != "":
		return "unnamed — " + t.Held
	}
	return "unnamed"
}

// terminalReport handles /ui terminal: what this terminal answered when shhh
// asked what it can do. It is a diagnostic, and the question
// it exists to answer is "why did that not happen here" — so a capability
// nobody asked about says so rather than reading as a no.
func (m Model) terminalReport() string {
	t := m.caps
	if !t.Asked {
		return "Terminal: not asked — " + t.Held + ".\nNothing was queried, so nothing here would be an answer."
	}
	if t.Dumb {
		return "Terminal: dumb — TERM says so.\nNothing was asked of it and nothing is sent to it: no title on the tab, no progress state beside it, no notification."
	}
	lines := []string{
		"Terminal: " + terminalName(t) + ".",
		"Inline images: " + imageSupport(t) + ".",
		"Desktop notifications: " + pick(t.Notifications, "OSC 99", "no OSC 99 answer — OSC 777 is the blind fallback") + ".",
		"Focus events: " + pick(t.FocusEvents, "reported", "not reported") + ".",
		// The progress state has no query to answer, the way OSC 777 has
		// none: it is written and either understood or ignored, and a
		// readout that listed it beside the answered capabilities would be
		// claiming an answer nobody gave (terminal.go).
		"Progress indicator: sent blind — the sequence has no query, so silence here is not a no.",
	}
	// The cell size is the terminal's pixels over the session's own columns
	// and rows, which is why it is measured here rather than kept there.
	if w, h := t.CellSize(m.width, m.height); w > 0 && h > 0 {
		lines = append(lines, fmt.Sprintf("Cell size: %d×%d px.", w, h))
	}
	if t.Held != "" {
		lines = append(lines, "Graphics and name were not asked for: "+t.Held+".")
	}
	return strings.Join(lines, "\n")
}

// imageSupport names how a staged image is drawn here, or says
// why there is no name to give.
//
// It answers for what shhh spends rather than for what the terminal offered,
// which is the difference between a diagnostic and a list. Sixel is detected
// and deliberately not drawn (internal/ui/caps/graphics.go), so a terminal
// that offered only sixel is a terminal whose pictures are half-blocks — and
// the reader looking at this line is looking at it to find out which of those
// they are about to see.
func imageSupport(t caps.Terminal) string {
	switch {
	case t.Kitty:
		return "kitty graphics"
	case t.Sixel:
		return "sixel, which shhh does not draw — pictures here are half-blocks"
	case t.Held != "":
		return "not asked"
	}
	return "neither kitty graphics nor sixel — pictures here are half-blocks"
}

// pick is the two words a capability comes back as. It exists so the three
// rows above read as a table rather than as three if statements.
func pick(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

// monoStatus describes the current monochrome state, naming the environment
// when it is what turned mono on.
func monoStatus() string {
	switch {
	case components.MonoForced():
		return "on (NO_COLOR)"
	case components.Mono():
		return "on"
	}
	return "off"
}

// monoCommand handles /ui mono: strip every surface to the two greys of the
// first invariant
// (docs/interface/principles.md#colour-never-carries-meaning-alone), so that
// a state distinguished only by colour becomes visibly wrong.
// NO_COLOR and TERM=dumb turn it on for the whole session and it cannot be
// turned back off from inside — the environment asked, not the user.
func (m *Model) monoCommand(parts []string) string {
	if len(parts) == 2 {
		return fmt.Sprintf("Monochrome: %s.\nUsage: /ui mono <on|off> — strips every surface to two greys; glyphs, words and layout carry the states.", monoStatus())
	}
	if len(parts) != 3 {
		return "Usage: /ui mono <on|off>"
	}
	var on bool
	switch parts[2] {
	case "on", "true", "yes":
		on = true
	case "off", "false", "no":
		on = false
	default:
		return fmt.Sprintf("Error: unknown mono setting %q (on, off)", parts[2])
	}
	if !on && components.MonoForced() {
		return "Monochrome is on because NO_COLOR is set in this environment; it cannot be turned off from here."
	}
	if on == components.Mono() {
		return fmt.Sprintf("Monochrome already %s.", monoStatus())
	}
	components.SetMono(on)
	m.invalidateRenderCache()
	if on {
		return "Monochrome on — every surface renders in two greys."
	}
	return "Monochrome off — the full palette is back."
}

// The compose row
// (docs/capabilities/coding-agent.md#a-long-call-is-counted-while-it-is-written):
// how much of a tool call the model has written, while it is writing it.
//
// A round that ends in a large edit spends most of itself inside one JSON
// blob, and nothing about the call is true until the blob closes — no target,
// no outcome, no duration — so the transcript's last act stays whatever the
// model said before it started, for as long as the write takes. The fragments
// the stream reports (internal/provider) are counted here and stated as a
// size on one row.
//
// It is a reading of the round in flight and not an entry in the history: it
// is drawn under the answer as it arrives, the way the answer itself is drawn
// before it becomes an entry, and it goes when the round's calls land. What
// replaces it is those calls' own rows, which say what was written and what
// came of it — a compose row left behind them would be a second row about one
// act, and the grid gives an act one row
// (docs/interface/principles.md#one-grid).
//
// It states a size and never a target, because a fragment carries the call's
// id and its bytes: the tool's name arrives with the finished call, which is
// the first moment it is true.

// composeVerb is the row's verb, closed like every other
// (docs/interface/principles.md#closed-vocabularies).
const composeVerb = "compose"

// composeFloor is how much a round has to have written before the row is
// drawn at all. It is measured against the calls not worth watching: a read,
// a search or a glob is a path and a pattern, a couple of hundred bytes at
// the outside, while a file being written or a batch of edits runs to
// kilobytes. A kilobyte sits clear of the first group and well under the
// second, so the row appears for the calls a reader is waiting on and for no
// others.
const composeFloor = 1 << 10

// toolDeltaMsg is one fragment of a tool call's arguments, off the stream.
type toolDeltaMsg struct{ delta provider.ToolCallDelta }

// repaintsOnTick reports whether a token batch that ended on this message can
// leave its repaint to the spinner's tick. Nothing terminal can — a round
// that has ended has to draw what it ended with — but a fragment is not the
// end of anything, and the row it feeds restates a size that only moves every
// kilobyte. A stream that sends its arguments in twenty-byte chunks would
// otherwise re-render the transcript twenty bytes at a time, which is the
// cost this row exists to make visible rather than to pay.
func repaintsOnTick(final tea.Msg) bool {
	if final == nil {
		return true
	}
	_, ok := final.(toolDeltaMsg)
	return ok
}

// appendCompose counts an arriving fragment into the round's total. The
// reading is the round's and not the call's, the way the think row is
// (think.go): a round writing three calls at once is one wait to the person
// watching it, and three numbers counting interleaved fragments are three
// numbers nobody can add up.
func (m *Model) appendCompose(d provider.ToolCallDelta) {
	if m.compacting {
		// A compaction is housekeeping, not a turn, and it calls no tools; a
		// reading of one would be a reading of something that never happened
		// (context.go).
		return
	}
	m.composed += len(d.Arguments)
}

// showCompose reports whether the compose row is drawn at all. Low verbosity
// is step headers only, and this row reports no act either — the act it was
// counting is the row that lands in its place a moment later.
func (m Model) showCompose() bool { return m.verbosity != verbosityLow }

// composeRowLine is the round's compose row as its rendered line, and nothing
// where the row has not earned its place: no round in flight, nothing written
// worth waiting for, or a verbosity that draws no such row. prev is whatever
// the transcript rendered last, which is what decides the spacing above it.
//
// The row does not spin. What moves on it is the count, which is the reading
// itself; the frame's own status line is where the turn says it is running.
func (m Model) composeRowLine(width int, prev entry, havePrev bool) string {
	if m.events == nil || m.composed < composeFloor || !m.showCompose() {
		return ""
	}
	row := components.ActivityRow{
		Kind:   components.ActivityThink,
		Verb:   composeVerb,
		Counts: attachment.HumanSize(m.composed),
	}
	// The spacing above the row is the transcript's own, so it is asked for
	// rather than chosen here. The answer arriving is the one thing above it
	// whose last line is still open — every finished entry ends its own — so
	// that case closes the line first and the separator follows.
	lead := ""
	switch {
	case m.answerIsArriving():
		lead = "\n" + separatorBefore(entry{kind: entryAssistant}, entry{kind: entryTool})
	case havePrev:
		lead = separatorBefore(prev, entry{kind: entryTool})
	}
	return lead + row.View(width) + "\n"
}
