// Package digest builds the content-free account of a session's tool activity
// that the summarizer reads.
//
// It exists because the reading became something more than a status block.
// The summary's verdict interrupts a turn — a steer for a run that has left
// its instruction, an early check-in for one that has what it needs — and the
// surfaces that most need interrupting are the ones with nobody watching:
// a headless run, a sub-agent working in the background. Those have no
// transcript, and the rows the reading is made of used to be assembled inside
// the chat model from one.
//
// So the wording of a row moved here, where every surface can reach it, and
// agent.Recorder collects rows from the tool hooks a headless run already has.
//
// One rule governs everything in this package, and it is the reason the rows
// look the way they do. **A row carries a tool's name, what it was pointed
// at, and an outcome word from a closed set — never the tool's output, and
// never a file's contents.** The digest is read by a model whose answer
// steers the agent, so anything an outside party can write — a fetched page,
// a dependency's README, a test's stdout — would otherwise be able to write
// the instruction the agent is steered with. Result text is read here only to
// choose between "ok" and "error", and never travels.
// See docs/capabilities/coding-agent.md#the-verdict-is-a-steering-signal-so-the-digest-is-a-boundary.
package digest

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/mcp"
)

// The outcome words. A closed set of two, because the question a reading asks
// of an outcome is only whether the call worked.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
)

// Outcome classifies a tool result by the error convention every executor
// follows ("error: ..." prefixes). The result text itself never leaves.
func Outcome(result string) string {
	if strings.HasPrefix(result, "error:") {
		return OutcomeError
	}
	return OutcomeOK
}

// argKeys is the priority order for picking a tool call's key argument.
var argKeys = []string{"path", "pattern", "command", "query", "url", "title", "name", "action", "task", "role"}

// Arg extracts the one argument worth showing beside a tool name — the path
// for read_file (with its line range when paged), the pattern for search —
// falling back to the flat key=value form for shapes it does not know.
//
// Arguments are the model's own words, not an outside party's, which is why
// they may appear where a result may not.
func Arg(tool, rawArgs string) string {
	if strings.TrimSpace(rawArgs) == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return FirstLine(rawArgs)
	}
	if server, ok := mcp.SplitName(tool); ok {
		// The target names the server and the tool, then the arguments
		// compacted: `github get_issue number=42`. The verb column already
		// says `mcp`, so the row reads as where, what, with what.
		target := server + " " + strings.TrimPrefix(tool, server+mcp.Separator)
		if compact := mcp.CompactArgs(json.RawMessage(rawArgs)); compact != "" {
			target += " " + compact
		}
		return target
	}
	if tool == "read_file" {
		if p, _ := args["path"].(string); p != "" {
			start, okS := args["start_line"].(float64)
			end, okE := args["end_line"].(float64)
			switch {
			case okS && okE:
				return p + ":" + strconv.Itoa(int(start)) + "–" + strconv.Itoa(int(end))
			case okS:
				return p + ":" + strconv.Itoa(int(start)) + "–"
			}
			return p
		}
	}
	if tool == "git" {
		// The verb is what the row is about, and the ref or the first path is
		// what it was pointed at: `log internal/agent`. Without this the flat
		// form leads with the alphabetically first key, which is the limit.
		if verb, _ := args["verb"].(string); verb != "" {
			if ref, _ := args["ref"].(string); ref != "" {
				return verb + " " + ref
			}
			if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
				if p, _ := paths[0].(string); p != "" {
					return verb + " " + p
				}
			}
			return verb
		}
	}
	for _, key := range argKeys {
		if v, ok := args[key].(string); ok && v != "" {
			return FirstLine(v)
		}
	}
	return FormatArgs(rawArgs)
}

// FormatArgs is the flat key=value rendering of a call's arguments, for a
// tool whose shape nothing here knows. Keys are sorted so the same call reads
// the same way twice — a row that reshuffles itself looks like a new call.
func FormatArgs(raw string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		switch val := m[k].(type) {
		case string:
			parts = append(parts, k+"="+val)
		default:
			b, _ := json.Marshal(val)
			parts = append(parts, k+"="+string(b))
		}
	}
	return strings.Join(parts, " ")
}

// FirstLine is a value's first line, marked when there was more.
func FirstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
