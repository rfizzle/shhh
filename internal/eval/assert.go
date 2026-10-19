package eval

// What a case may say about how the session went.
//
// The check decides whether the work was done; an assertion decides whether
// the route to it was one the case author would sign. Both are mechanical: a
// word from a closed set, decided from the tool calls and decisions the
// session's own stream carries, never from its prose. A free-form expression
// language would be a way to grade a transcript by the back door, and any
// threshold the harness learned from a run would move with the thing it
// measures.
//
// A finding names a tool, an assertion, a reason code and a count, and never
// an argument, a path, a command or an answer: it is drawn in a report and
// kept in a baseline, and the stream it was read from is dropped.
// See docs/capabilities/evals.md#a-case-can-say-how-the-session-should-have-gone.

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rfizzle/agentcore/repeat"
	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/observe/record"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// The assertion words. wordBudget is the one that takes a tool and a limit.
const (
	wordBudget       = "budget"
	wordNoRepeat     = "no-repeat"
	wordReadBefore   = "read-before-edit"
	wordNoMutation   = "no-mutation"
	wordNoShell      = "no-shell"
	wordNoDenial     = "no-denial"
	wordAsks         = "asks"
	budgetAllCalls   = "calls"
	gitWriteToolName = "git_write"
)

// assertWords is the closed set a case's assert key may name.
var assertWords = []string{wordNoRepeat, wordReadBefore, wordNoMutation, wordNoShell, wordNoDenial, wordAsks}

// Assertion is one declared expectation. Tool and Limit belong to a budget.
type Assertion struct {
	Word  string
	Tool  string
	Limit int
}

// Label is the assertion as the case wrote it, which is how a report and a
// baseline name it.
func (a Assertion) Label() string {
	if a.Word == wordBudget {
		return wordBudget + " " + a.Tool + " " + strconv.Itoa(a.Limit)
	}
	return a.Word
}

// Finding is an assertion's answer: held or broken, with a count and a detail
// made only of tool names, reason codes and counts.
type Finding struct {
	Assertion
	Held   bool
	Count  int
	Detail string
}

// parseAssertions reads a case's budget and assert keys. Every refusal names
// the key, because the reader is editing the file.
//
// asks is held by a served conversation, where the question tool is
// registered; hasPersona says the case runs as one, and without it the word
// could never hold and would read as a model that never asked.
func parseAssertions(budget, assert []string, path string, hasPersona bool) ([]Assertion, error) {
	var out []Assertion
	seen := map[string]bool{}
	for _, line := range budget {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf(`%s: budget %q needs a tool and a count — "<tool> N", or "calls N" for every call`, path, line)
		}
		limit, err := strconv.Atoi(fields[1])
		if err != nil || limit < 0 {
			return nil, fmt.Errorf(`%s: budget %q needs a count of zero or more after the tool — "<tool> N"`, path, line)
		}
		a := Assertion{Word: wordBudget, Tool: fields[0], Limit: limit}
		if a.Tool != budgetAllCalls && !sessionTools()[a.Tool] {
			return nil, fmt.Errorf(`%s: budget %q names %s, which no session offers — a tool the registry defines, or "calls" for every call`, path, line, a.Tool)
		}
		if seen[a.Tool+"\x00budget"] {
			return nil, fmt.Errorf("%s: budget names %s twice — one limit per tool", path, a.Tool)
		}
		seen[a.Tool+"\x00budget"] = true
		out = append(out, a)
	}
	for _, word := range assert {
		word = strings.TrimSpace(word)
		if !slices.Contains(assertWords, word) {
			return nil, fmt.Errorf("%s: assert %q is not one this suite knows — %s", path, word, strings.Join(assertWords, ", "))
		}
		if seen[word] {
			return nil, fmt.Errorf("%s: assert names %s twice", path, word)
		}
		seen[word] = true
		if word == wordAsks && !hasPersona {
			return nil, fmt.Errorf("%s: assert asks needs a persona — the question tool is only offered to a session somebody can answer, so without one it could never hold", path)
		}
		out = append(out, Assertion{Word: word})
	}
	return out, nil
}

// sessionTools is every tool name a session can be offered, read off the
// definitions each package registers rather than listed a second time here.
// A budget on a name outside it could never be spent, so it would hold for
// the wrong reason. A tool an MCP server brings is the reader's own
// configuration, and a case measures what ships.
func sessionTools() map[string]bool {
	defs := slices.Concat(
		tools.DefinitionsFull(),
		structural.Registrable(),
		subagent.Definitions(nil, subagent.Offer{}),
		notebook.Definitions(),
		[]llm.Tool{ask.ToolDefinition(), process.Definition(), quality.ToolDefinition(), evidence.ToolDefinition()},
	)
	names := map[string]bool{
		web.FetchToolName: true, web.SearchToolName: true,
		lsp.DefinitionToolName: true, lsp.ReferencesToolName: true, lsp.WorkspaceSymbolToolName: true,
		lsp.HoverToolName: true, lsp.DiagnosticsToolName: true,
	}
	for _, d := range defs {
		names[d.Name] = true
	}
	return names
}

// failed says the call's answer was an error, by the class the stream
// recorded for it. A search that found nothing is a successful one, and a
// call with no answer is not known to have failed.
func (c call) failed() bool {
	return c.answered && c.Class != "" && c.Class != record.ClassEmpty
}

// fixture is the files a workspace held when the session started, which is
// what separates an edit from the creation of a file.
type fixture struct {
	roots []string
	files map[string]bool
}

// rel is a path a call named, as the slash-separated path inside the
// workspace, or the cleaned path itself when it points outside it.
func (f fixture) rel(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	for _, root := range f.roots {
		root = filepath.ToSlash(root)
		if p == root {
			return "."
		}
		if strings.HasPrefix(p, root+"/") {
			p = p[len(root)+1:]
			break
		}
	}
	return path.Clean(p)
}

// assert decides every declared assertion from the trace, in declaration order.
func (t trace) assert(as []Assertion, fx fixture) []Finding {
	if len(as) == 0 {
		return nil
	}
	calls := t.calls()
	out := make([]Finding, 0, len(as))
	for _, a := range as {
		f := Finding{Assertion: a}
		switch a.Word {
		case wordBudget:
			f.Count = countCalls(calls, a.Tool)
			f.Held = f.Count <= a.Limit
			if !f.Held {
				f.Detail = plural(f.Count, "call")
			}
		case wordNoRepeat:
			f.Count, f.Detail = repeats(calls)
			f.Held = f.Count == 0
		case wordReadBefore:
			f.Count, f.Detail = unreadEdits(calls, fx)
			f.Held = f.Count == 0
		case wordNoMutation:
			f.Count, f.Detail = mutations(calls)
			f.Held = f.Count == 0
		case wordNoShell:
			f.Count = countCalls(calls, tools.ExecCommandName)
			f.Held = f.Count == 0
			if !f.Held {
				f.Detail = tools.ExecCommandName + ": " + plural(f.Count, "call")
			}
		case wordNoDenial:
			f.Count, f.Detail = t.denials()
			f.Held = f.Count == 0
		case wordAsks:
			f.Count = countCalls(calls, ask.ToolName)
			f.Held = f.Count > 0
			if !f.Held {
				f.Detail = "no " + ask.ToolName + " call"
			}
		}
		out = append(out, f)
	}
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// countCalls counts the tool-call lines for one tool, or every one for the
// budget's "calls".
func countCalls(calls []call, tool string) int {
	if tool == budgetAllCalls {
		return len(calls)
	}
	n := 0
	for _, c := range calls {
		if c.Tool == tool {
			n++
		}
	}
	return n
}

// byTool is "tool: n calls" for each tool with a count, in name order, so a
// detail reads the same twice.
func byTool(counts map[string]int, what string) string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+": "+plural(counts[name], what))
	}
	return strings.Join(parts, ", ")
}

// drawnTool is the name a call may be drawn under. The name on a call line is
// whatever the model wrote, and only a call the session answered as a tool it
// has is known to be a real name; any other is not repeated in a report.
func drawnTool(c call) string {
	switch {
	case !c.answered:
		return "an unanswered call"
	case c.Class == record.ClassUnknown:
		return "a tool never offered"
	}
	return c.Tool
}

// loopShapes is the one thing of shhh's tools the loop's key reads: a question
// is its text alone, so this agrees with the key the session's detector uses
// (internal/cli/repeatshapes.go).
var loopShapes = repeat.Shapes{
	Question: func(tool string, args json.RawMessage) (string, bool) {
		if tool != ask.ToolName {
			return "", false
		}
		return ask.QuestionText(args), true
	},
}

// repeats counts the calls that share the loop's own key — the tool, its
// canonical arguments and a byte-identical result — with an earlier one, and
// names the tools they were calls of.
func repeats(calls []call) (int, string) {
	seen := map[string]bool{}
	counts := map[string]int{}
	n := 0
	for _, c := range calls {
		key := loopShapes.InteractionKey(c.Tool, json.RawMessage(c.Arguments), c.Result)
		if seen[key] {
			n++
			counts[drawnTool(c)]++
		}
		seen[key] = true
	}
	if n == 0 {
		return 0, ""
	}
	return n, byTool(counts, "repeated call")
}

// unreadEdits counts the edits and writes to a file that existed when the
// session began, made without a read_file of that path first. A file the
// fixture did not hold is new, an edit_file that only appends quotes nothing
// of the file, and a file the session wrote itself is one whose content it
// already knows: none of the three needs a read.
func unreadEdits(calls []call, fx fixture) (int, string) {
	known := map[string]bool{}
	counts := map[string]int{}
	n := 0
	for _, c := range calls {
		// A read or write that came back as an error taught the session
		// nothing of the file, so it makes no path known.
		switch c.Tool {
		case tools.ReadFileName:
			if c.failed() {
				continue
			}
			var args struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(c.Arguments), &args) == nil && strings.TrimSpace(args.Path) != "" {
				known[fx.rel(args.Path)] = true
			}
		case tools.WriteFileName, tools.EditFileName:
			paths := tools.WrittenPaths(c.Tool, c.Arguments)
			var args struct {
				Append string `json:"append"`
			}
			appends := c.Tool == tools.EditFileName && json.Unmarshal([]byte(c.Arguments), &args) == nil && args.Append != ""
			blind := false
			for _, p := range paths {
				p = fx.rel(p)
				if fx.files[p] && !appends && !known[p] {
					blind = true
				}
				known[p] = true
			}
			if blind {
				n++
				counts[c.Tool]++
			}
		}
	}
	if n == 0 {
		return 0, ""
	}
	return n, byTool(counts, "call") + " on a path never read"
}

// mutations counts the calls that could change something — a file tool, the
// shell, the process tool, a git write — and the results that say the session
// asked for a tool it was never offered, which is how a read-only
// conversation is seen to attempt an edit. The name of such a tool is the
// model's own text and is not repeated.
func mutations(calls []call) (int, string) {
	counts := map[string]int{}
	unknown := 0
	n := 0
	for _, c := range calls {
		switch {
		case c.answered && c.Class == record.ClassUnknown:
			unknown++
			n++
		case tools.IsMutating(c.Tool), c.Tool == tools.ExecCommandName, c.Tool == process.ToolName, c.Tool == gitWriteToolName:
			counts[c.Tool]++
			n++
		}
	}
	if n == 0 {
		return 0, ""
	}
	detail := byTool(counts, "call")
	if unknown > 0 {
		if detail != "" {
			detail += ", "
		}
		detail += plural(unknown, "call") + " to a tool never offered"
	}
	return n, detail
}

// denials counts the decision lines that refused a call and names each reason
// code. A reason is a closed code from the record's vocabulary; it is what
// says which rule the session provoked.
func (t trace) denials() (int, string) {
	reasons := map[string]int{}
	n := 0
	for _, ev := range t.events {
		if ev.Kind == record.EventDecision && ev.Decision == record.DecisionDeny {
			n++
			reasons[ev.Reason]++
		}
	}
	if n == 0 {
		return 0, ""
	}
	names := make([]string, 0, len(reasons))
	for r := range reasons {
		names = append(names, r)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, r := range names {
		label := r
		if label == "" {
			label = "no reason"
		}
		parts = append(parts, label+" "+strconv.Itoa(reasons[r]))
	}
	return n, "denied: " + strings.Join(parts, ", ")
}

// Broken is how many of the findings did not hold.
func Broken(fs []Finding) int {
	n := 0
	for _, f := range fs {
		if !f.Held {
			n++
		}
	}
	return n
}
