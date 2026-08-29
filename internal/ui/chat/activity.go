package chat

// Compact activity feed (S-075, DESIGN-TUI.md §6): tool calls and commands
// render as one-line activity rows — glyph, action, key argument, outcome,
// counts, duration — never raw output blocks by default. Focus mode (§7)
// expands a row in place, /ui verbosity changes the default density, and a
// running command shows a live output tail in its row.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/caps"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/web"
)

// verbosity is the activity feed's default density, and the three levels have
// three distinct meanings (S-091, DESIGN-TUI.md §13c): low shows step headers
// only, normal folds a step's consecutive read-only calls into one counted
// row, high expands every row with its bounded detail body.
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

// TailFunc runs a command like the plain runner while reporting each completed
// output line, so the row can show a live tail. onLine may be called from
// other goroutines.
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
// (S-077); the activity row renders it as running.
const pendingToolResult = "running…"

// cancelledToolResult is the synthetic result left on a tool call abandoned
// when the turn was cancelled.
const cancelledToolResult = "cancelled by user"

// The deciders named on a denied row (§6d): your preference, or a rule.
const (
	decidedByYou  = "you"
	decidedByAuto = "auto"
)

// activityVerbs is the one table mapping tool names onto the closed verb
// vocabulary of DESIGN-TUI.md §6c — read, search, glob, lsp, web, edit,
// write, patch, run, memory, spawn, fan-out, agent. A tool that maps onto
// none of them is a hole in this table, not a fourteenth verb: it renders as
// itself, clipped to the verb column, which is the signal that the table is
// stale.
var activityVerbs = map[string]string{
	"read_file":                "read",
	"list_directory":           "read",
	evidence.ToolName:          "read",
	"search":                   "search",
	structural.AstGrepToolName: "search",
	structural.JaqToolName:     "search",
	structural.TokeiToolName:   "search",
	"glob":                     "glob",
	structural.FdToolName:      "glob",
	lsp.DefinitionToolName:     "lsp",
	lsp.ReferencesToolName:     "lsp",
	web.FetchToolName:          "web",
	web.SearchToolName:         "web",
	"edit_file":                "edit",
	"write_file":               "write",
	structural.SdToolName:      "patch",
	tools.ExecCommandName:      "run",
	process.ToolName:           "run",
	quality.ToolName:           "run",
	memory.RememberToolName:    "memory",
	subagent.SpawnToolName:     "spawn",
	subagent.ReportToolName:    "agent",
}

func activityVerb(tool string) string {
	if v, ok := activityVerbs[tool]; ok {
		return v
	}
	return tool
}

// activityKind picks the row's glyph and, with it, whether the row carries
// the mutation rail (§6b, §14): ⚙ reads, $ commands, ✎ anything that
// persists, ◇ sub-agents.
func activityKind(tool string) components.ActivityKind {
	switch {
	case tool == subagent.SpawnToolName || tool == subagent.ReportToolName:
		return components.ActivitySubagent
	case tool == tools.ExecCommandName || tool == process.ToolName || tool == quality.ToolName:
		return components.ActivityCommand
	case tools.IsMutating(tool) || tool == memory.RememberToolName || tool == structural.SdToolName:
		return components.ActivityEdit
	}
	return components.ActivityTool
}

// activityArgKeys is the priority order for picking a tool call's key argument.
var activityArgKeys = []string{"path", "pattern", "command", "query", "url", "name", "action", "task", "role"}

// activityArg extracts the one argument worth showing beside the tool name,
// e.g. the path for read_file (with its line range when paged) or the pattern
// for search; unknown shapes fall back to the flat key=value form.
func activityArg(tool, rawArgs string) string {
	if strings.TrimSpace(rawArgs) == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return firstLine(rawArgs)
	}
	if tool == "read_file" {
		if p, _ := args["path"].(string); p != "" {
			start, okS := args["start_line"].(float64)
			end, okE := args["end_line"].(float64)
			switch {
			case okS && okE:
				return fmt.Sprintf("%s:%d–%d", p, int(start), int(end))
			case okS:
				return fmt.Sprintf("%s:%d–", p, int(start))
			}
			return p
		}
	}
	for _, key := range activityArgKeys {
		if v, ok := args[key].(string); ok && v != "" {
			return firstLine(v)
		}
	}
	return formatToolArgs(rawArgs)
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

// activityDuration renders the row's duration field (§6a). Under 0.5s it is
// blank rather than 0.0s: a column of zeroes down the feed is noise.
func activityDuration(d time.Duration) string {
	if d < 500*time.Millisecond {
		return ""
	}
	return formatDuration(d)
}

// turnDuration renders the duration field of a row whose span is a whole turn
// rather than one call: the three recovery rows of §17a, and the round-limit
// pause above all, since a turn that spent 25 tool rounds is measured in
// minutes. Past a minute `252s` stops reading as a duration, so the field
// takes minutes and seconds — packed, because §6a gives duration six columns
// and FormatElapsed's `4m 12s` would fill them and touch the outcome beside
// it. It is the form DESIGN-TUI.md §17a draws on this row.
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

// activityRowFor builds the compact row for a tool or command entry (§6), as
// it renders outside any step that has been opened. Everything that only
// wants to read a row's state — what it is, whether it ran, whether it broke
// — asks through here, because none of those answers depend on how much of
// the output is showing.
func (m Model) activityRowFor(e entry) components.ActivityRow {
	return m.activityRowDetail(e, false)
}

// activityRowDetail is the same row told whether the step around it has its
// detail open (S-137, §13d). Collapsed rows never show output; focus-mode
// expansion shows the full stored result (already bounded upstream by
// S-051/S-064); failed rows, an opened step and high verbosity show the
// bounded detail view; and low verbosity hides counts.
//
// A row you opened yourself keeps its unbounded body inside an opened step:
// the step's answer is the default for its rows, never a ceiling on one you
// asked about by name.
func (m Model) activityRowDetail(e entry, stepDetail bool) components.ActivityRow {
	row := components.ActivityRow{
		Expanded:  e.expanded || stepDetail || m.verbosity == verbosityHigh,
		MaxDetail: maxToolResultLines,
		Duration:  activityDuration(e.duration),
		Frame:     m.spinFrame,
	}
	if e.expanded {
		row.MaxDetail = 0
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
	} else {
		row.Kind = activityKind(e.toolName)
		row.Verb = activityVerb(e.toolName)
		row.Target = activityArg(e.toolName, e.toolArgs)
		switch {
		case e.deniedBy != "":
			// A refusal is not a failure: ⊘ and the decider's name say the
			// call never ran, and the duration field says so too (§6d).
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
			// The row animates from the session's one frame (§10c), and only
			// while the loop that advances it is running: a call left pending
			// by a cancelled turn keeps the still `▸` rather than standing on
			// one braille frame, which would read as a hang (S-119).
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
// terminal mouse reporting, and what the terminal itself can do.
func (m *Model) uiCommand(parts []string) string {
	if len(parts) == 1 {
		return fmt.Sprintf("Activity feed verbosity: %s.\nMonochrome: %s.\nMouse reporting: %s.\nLayout: %s.\nTerminal: %s.\nUsage: /ui verbosity <low|normal|high> · /ui mono <on|off> · /ui mouse <on|off> · /ui terminal", m.verbosity, monoStatus(), m.mouseStatus(), m.inspectorStatus(), terminalName(m.caps))
	}
	switch parts[1] {
	case "verbosity":
		if len(parts) == 2 {
			return fmt.Sprintf("Activity feed verbosity: %s.\nUsage: /ui verbosity <low|normal|high> — low shows step headers only, normal folds read-only groups, high expands every row.\nFor one step rather than all of them, ctrl+o opens the detail of the step in flight.", m.verbosity)
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
	case "terminal":
		return m.terminalReport()
	}
	return "Usage: /ui verbosity <low|normal|high> · /ui mono <on|off> · /ui mouse <on|off> · /ui terminal"
}

// terminalName is the one-line answer the bare /ui gives: what the terminal
// called itself when shhh asked (S-156, §10k). A terminal that was asked
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
// asked what it can do (S-156, §10k). It is a diagnostic, and the question
// it exists to answer is "why did that not happen here" — so a capability
// nobody asked about says so rather than reading as a no.
func (m Model) terminalReport() string {
	t := m.caps
	if !t.Asked {
		return "Terminal: not asked — " + t.Held + ".\nNothing was queried, so nothing here would be an answer."
	}
	lines := []string{
		"Terminal: " + terminalName(t) + ".",
		"Inline images: " + imageSupport(t) + ".",
		"Desktop notifications: " + pick(t.Notifications, "OSC 99", "no OSC 99 answer — OSC 777 is the blind fallback") + ".",
		"Focus events: " + pick(t.FocusEvents, "reported", "not reported") + ".",
	}
	// The cell size is the terminal's pixels over the session's own columns
	// and rows, which is why it is measured here rather than kept there
	// (S-156).
	if w, h := t.CellSize(m.width, m.height); w > 0 && h > 0 {
		lines = append(lines, fmt.Sprintf("Cell size: %d×%d px.", w, h))
	}
	if t.Held != "" {
		lines = append(lines, "Graphics and name were not asked for: "+t.Held+".")
	}
	return strings.Join(lines, "\n")
}

// imageSupport names the picture protocols the terminal offered, or says why
// there is no name to give.
func imageSupport(t caps.Terminal) string {
	var have []string
	if t.Kitty {
		have = append(have, "kitty graphics")
	}
	if t.Sixel {
		have = append(have, "sixel")
	}
	if len(have) > 0 {
		return strings.Join(have, " · ")
	}
	if t.Held != "" {
		return "not asked"
	}
	return "neither kitty graphics nor sixel"
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

// monoCommand handles /ui mono: strip every surface to the two greys of
// DESIGN-TUI.md's first invariant, so that a state distinguished only by
// colour becomes visibly wrong (S-095). NO_COLOR and TERM=dumb turn it on for
// the whole session and it cannot be turned back off from inside — the
// environment asked, not the user.
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
