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
	"slices"
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

// argKeys is the priority order for picking a tool call's key argument. Every
// one of them names something the call was pointed at, which is what the
// field is for: the target says what the act was about and never what carried
// it (docs/interface/principles.md#one-grid).
//
// `action` is the key that is deliberately not here. It is the operation, not
// the subject — the same word the verb column already carries — so a call that
// fell through to it rendered `read  read` and `run  run`, a row naming the
// tool twice and the file, suite or process never. The three tools that take
// an action answer for their own subject in actionToolTarget instead.
var argKeys = []string{"path", "pattern", "command", "query", "question", "url", "title", "name", "task", "role"}

// searchTools are the calls whose subject is the pattern and whose path is
// only the scope it was asked in, so their rows lead with what was asked.
// Membership is decided per tool by that role and never by reordering
// argKeys: read_file and the write tools are about their path, and a global
// swap would put a search's pattern where their filename belongs.
var searchTools = map[string]bool{
	"search":   true,
	"glob":     true,
	"ast_grep": true,
	"fd":       true,
}

// Arg extracts the one argument worth showing beside a tool name — the path
// for read_file (with its line range when paged), the pattern and then the
// path for search — falling back to the flat key=value form for shapes it
// does not know.
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
	if searchTools[tool] {
		// A search's pattern is the question and its path is only where the
		// question was put, so the row is `steeringItem ./internal/ui/chat`
		// and not the directory alone. Led by the path, twenty different
		// questions about one package are twenty identical rows — and the
		// reading asked whether a run is going in circles is handed rows
		// that were made indistinguishable before it saw them.
		// See docs/interface/principles.md#one-grid.
		if target := searchTarget(args); target != "" {
			return target
		}
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
		// The command a person would have typed, which is the subject here:
		// `git status`, `git diff HEAD`, `git log internal/agent`. The verb
		// alone read as a bare enum word in a column of file paths — `status`
		// beside `internal/agent/loop.go` — and `git` alone would be the tool
		// naming itself, which the target never is
		// (docs/interface/principles.md#one-grid). Without either the flat
		// form leads with the alphabetically first key, which is the limit.
		if verb, _ := args["verb"].(string); verb != "" {
			subject := "git " + verb
			if ref, _ := args["ref"].(string); ref != "" {
				return subject + " " + ref
			}
			if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
				if p, _ := paths[0].(string); p != "" {
					return subject + " " + p
				}
			}
			return subject
		}
	}
	if tool == "git_write" {
		// The row's verb column already carries the write verb (GitVerb), so
		// the target is what the verb was pointed at and never repeats it: a
		// commit's subject, a branch's name, the first file a staging named.
		return gitWriteTarget(args)
	}
	if tool == "agent_steer" {
		// The row is who was redirected and what they were told, in that
		// order: the name alone would make every steer of a fan-out look
		// alike, and the message is the point of the call. Bounded to its
		// first line, marked when there was more; the row clips the rest.
		if name, _ := args["name"].(string); name != "" {
			if msg, _ := args["message"].(string); msg != "" {
				return name + " " + FirstLine(msg)
			}
			return name
		}
	}
	if tool == "agent_report" {
		// A wait on a set is about every agent it names, so the row lists
		// them joined — `writer-1, writer-2, reviewer-1` — and the row clips
		// a long set as it clips any long target.
		var names []string
		if name, _ := args["name"].(string); name != "" {
			names = append(names, name)
		}
		if set, ok := args["names"].([]any); ok {
			for _, v := range set {
				if name, _ := v.(string); name != "" && !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
		}
		if len(names) > 0 {
			return strings.Join(names, ", ")
		}
	}
	if target := actionToolTarget(tool, args); target != "" {
		return target
	}
	for _, key := range argKeys {
		if v, ok := args[key].(string); ok && v != "" {
			return FirstLine(v)
		}
	}
	return FormatArgs(rawArgs)
}

// GateSubject is what a quality-gate row is about before the suite's own name
// is added to it. It is spelled as the thing rather than as the tool
// (`quality_gate`) because the target column is read as a list of subjects
// (docs/interface/principles.md#one-grid).
const GateSubject = "quality gate"

// actionToolTarget is the subject of a call to one of the three tools that
// take an `action` — the gate, the evidence store and the process supervisor.
// Their operation is in the verb column already, so what is left for the
// target is the thing the operation is being done to: the suite, the stored
// entry, the process. It reports "" for a call that named none of those,
// which falls back to the flat form like any other unknown shape.
func actionToolTarget(tool string, args map[string]any) string {
	action, _ := args["action"].(string)
	switch tool {
	case "quality_gate":
		// A named suite is the subject of a run; the last run is the subject
		// of a re-report, which has no suite of its own to name. The check
		// count belongs beside the suite, but it is a fact about the verdict
		// rather than about the call, so whoever holds the result adds it.
		if action == "result" {
			return GateSubject + " · last result"
		}
		if suite, _ := args["suite"].(string); suite != "" {
			return GateSubject + " · " + suite
		}
		return GateSubject
	case "evidence":
		// The entry is the subject, and a search of one reads like any other
		// search: the pattern asked, then where it was asked
		// (docs/interface/principles.md#one-grid).
		id, _ := args["id"].(string)
		if id == "" {
			return ""
		}
		if q, _ := args["query"].(string); q != "" {
			return FirstLine(q) + " " + id
		}
		return id
	case "process":
		// The named process, or the command that is about to become one. A
		// status that named nothing is asking about all of them, which is a
		// subject too; every other action needs a name, so a call without one
		// is a malformed call and has nothing to be about.
		if cmd, _ := args["command"].(string); cmd != "" {
			return FirstLine(cmd)
		}
		if name, _ := args["name"].(string); name != "" {
			return name
		}
		if action == "status" {
			return "all processes"
		}
	}
	return ""
}

// Path is the file a call's arguments named, for a caller that needs the
// subject of a call that never ran and so has no row of its own to read it
// off. Arguments that are not JSON at all name nothing: the text is then the
// parse error's business, not a subject.
func Path(rawArgs string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	if p, _ := args["path"].(string); p != "" {
		return p
	}
	if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
		if p, _ := paths[0].(string); p != "" {
			return p
		}
	}
	return ""
}

// GitVerb is the write verb a call to the writing half of git names. It is
// the row's verb rather than the tool's name because the four verbs are four
// different acts, and a row that called all of them by the tool's name would
// put the one word the reader is scanning for into the target column.
func GitVerb(rawArgs string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	verb, _ := args["verb"].(string)
	return verb
}

// searchTarget is what one search was pointed at: the pattern, then the scope
// when the call named one. A pattern the call left out — fd lists a directory
// when it is given none — leaves the path to stand alone, because then the
// directory is the whole question.
func searchTarget(args map[string]any) string {
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)
	pattern, path = FirstLine(pattern), strings.TrimSpace(path)
	if pattern == "" {
		return path
	}
	if scope := searchScope(path); scope != "" {
		return pattern + " " + scope
	}
	return pattern
}

// SearchScope is the place one pattern-subject call was put, marked as a
// place the way a row marks it, and "." for a call that named none — which is
// a scope like any other, since a run can as easily ask the whole tree twenty
// questions as one directory. It reports false for every tool whose subject
// is its path rather than its pattern, so a caller grouping calls by where
// they were pointed does not have to know which tools those are.
//
// It is here rather than beside its caller because it is the same reading
// Arg makes to build a search's target: a second copy would agree on the day
// it was written and stop agreeing the day a fifth pattern-subject tool is
// registered.
func SearchScope(tool, rawArgs string) (scope string, ok bool) {
	if !searchTools[tool] {
		return "", false
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "", false
	}
	path, _ := args["path"].(string)
	if s := searchScope(strings.TrimSpace(path)); s != "" {
		return s, true
	}
	return ".", true
}

// searchScope marks a search's path as a place, so the second half of
// `steeringItem ./internal/ui/chat` cannot be read as more pattern. A path
// that already anchors itself keeps its own form, and the default scope — the
// whole tree, which every search shares — is left off rather than spent.
func searchScope(path string) string {
	switch {
	case path == "" || path == ".":
		return ""
	case strings.HasPrefix(path, "./"), strings.HasPrefix(path, "../"),
		strings.HasPrefix(path, "/"), strings.HasPrefix(path, "~"):
		return path
	}
	return "./" + path
}

// gitWriteTarget is what one write was pointed at.
func gitWriteTarget(args map[string]any) string {
	if m, _ := args["message"].(string); m != "" {
		return FirstLine(m)
	}
	if b, _ := args["branch"].(string); b != "" {
		return b
	}
	if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
		first, _ := paths[0].(string)
		if len(paths) == 1 {
			return first
		}
		return first + " +" + strconv.Itoa(len(paths)-1)
	}
	return ""
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
