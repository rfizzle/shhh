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

	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// verbosity is the activity feed's default rendering density: low hides
// counts, med collapses rows, high renders every row expanded.
type verbosity int

const (
	verbosityLow verbosity = iota
	verbosityMed
	verbosityHigh
)

func (v verbosity) String() string {
	switch v {
	case verbosityLow:
		return "low"
	case verbosityHigh:
		return "high"
	}
	return "med"
}

func parseVerbosity(s string) (verbosity, error) {
	switch s {
	case "low":
		return verbosityLow, nil
	case "med", "medium":
		return verbosityMed, nil
	case "high":
		return verbosityHigh, nil
	}
	return verbosityMed, fmt.Errorf("unknown verbosity %q (low, med, high)", s)
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

// activityVerbs maps tool names onto the short action shown in the row.
var activityVerbs = map[string]string{
	"read_file":            "read",
	"list_directory":       "list",
	"write_file":           "write",
	"edit_file":            "edit",
	"web_fetch":            "fetch",
	"web_search":           "web",
	"quality_gate":         "gate",
	subagent.SpawnToolName: "agent",
}

func activityVerb(tool string) string {
	if v, ok := activityVerbs[tool]; ok {
		return v
	}
	return tool
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

// activityRowFor builds the compact row for a tool or command entry (§6).
// Collapsed rows never show output; focus-mode expansion shows the full
// stored result (already bounded upstream by S-051/S-064), failed rows and
// high verbosity show the bounded detail view, and low verbosity hides counts.
func (m Model) activityRowFor(e entry) components.ActivityRow {
	row := components.ActivityRow{
		Expanded:  e.expanded || m.verbosity == verbosityHigh,
		MaxDetail: maxToolResultLines,
	}
	if e.expanded {
		row.MaxDetail = 0
	}
	if e.duration > 0 {
		row.Duration = formatDuration(e.duration)
	}
	result := e.toolResult
	if e.kind == entryCommand {
		row.Kind = components.ActivityCommand
		row.Name = firstLine(e.text)
		if e.exitCode != 0 {
			row.Failed = true
			row.Outcome = fmt.Sprintf("exit %d", e.exitCode)
		} else {
			row.Outcome = "ok"
		}
	} else {
		switch {
		case e.toolName == subagent.SpawnToolName:
			row.Kind = components.ActivitySubagent
		case tools.IsMutating(e.toolName):
			row.Kind = components.ActivityEdit
		default:
			row.Kind = components.ActivityTool
		}
		row.Name = activityVerb(e.toolName)
		row.Arg = activityArg(e.toolName, e.toolArgs)
		switch {
		case result == pendingToolResult:
			row.Running = true
			row.Outcome = pendingToolResult
			result = ""
		case strings.HasPrefix(result, "error:"):
			row.Failed = true
			row.Outcome = "error"
		}
	}
	if strings.TrimSpace(result) != "" {
		row.Detail = strings.Split(strings.TrimRight(result, "\n"), "\n")
		if !row.Failed && m.verbosity != verbosityLow {
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
		Name:    firstLine(m.runningCommand),
		Running: true,
		Outcome: "running…",
	}
	if !m.runStart.IsZero() {
		row.Duration = formatDuration(time.Since(m.runStart))
	}
	if m.runTail != nil {
		row.Tail = m.runTail.Line()
	}
	return row.View(width)
}

// uiCommand handles /ui: showing and setting the activity feed's verbosity.
func (m *Model) uiCommand(parts []string) string {
	if len(parts) == 1 || (len(parts) == 2 && parts[1] == "verbosity") {
		return fmt.Sprintf("Activity feed verbosity: %s.\nUsage: /ui verbosity <low|med|high> — low hides counts, med collapses rows, high expands rows.", m.verbosity)
	}
	if len(parts) != 3 || parts[1] != "verbosity" {
		return "Usage: /ui verbosity <low|med|high>"
	}
	v, err := parseVerbosity(parts[2])
	if err != nil {
		return "Error: " + err.Error()
	}
	m.verbosity = v
	m.invalidateRenderCache()
	return fmt.Sprintf("Activity feed verbosity set to %s.", v)
}
