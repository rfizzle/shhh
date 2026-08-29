// Package plan turns a planning response into the ordered step list the plan
// card renders.
//
// A plan that arrives as a paragraph asks the reader to accept a leap of
// faith: they approve a mode change and a run of tool calls on the strength
// of prose they skimmed. Naming the files each step intends to touch turns
// that into a check. This package is the parser for the shape plan mode asks
// the model to emit (internal/prompt.PlanModeInstructions) — a numbered step
// list with optional `files:`, `action:` and `note:` continuation lines.
//
// It is deliberately forgiving in one direction and strict in the other. A
// response that never adopted the shape parses to zero steps, and the card
// falls back to prose rather than failing; but a field the model did not
// supply is never invented. Where there are no paths there is no path row,
// and the summary counts only what the plan actually named.
package plan

import (
	"regexp"
	"strconv"
	"strings"
)

// Action is what one step intends to do. It decides the step's right-hand
// label on the card and whether its paths count toward the plan's radius.
type Action int

const (
	// Read is inspection only: nothing in the workspace changes.
	Read Action = iota
	// Edit changes files that already exist.
	Edit
	// Create adds files that do not exist yet.
	Create
	// Delete removes files.
	Delete
	// Run executes something — a build, a test suite, a script.
	Run
	// Network reaches off the machine.
	Network
)

// Writes reports whether the action changes files in the workspace, which is
// what the plan's `files touched` count is counting.
func (a Action) Writes() bool {
	return a == Edit || a == Create || a == Delete
}

// String is the action as the parser names it, for tests and diagnostics.
func (a Action) String() string {
	switch a {
	case Edit:
		return "edit"
	case Create:
		return "create"
	case Delete:
		return "delete"
	case Run:
		return "run"
	case Network:
		return "network"
	}
	return "read"
}

// Step is one numbered item of the plan.
type Step struct {
	// Number is the number the model gave it, not the index: a plan that
	// skips 3 keeps the numbering the user read in the transcript.
	Number int
	Title  string
	// Paths are the files the step said it would touch, in the order given.
	// Empty means the model did not say — never that the step touches
	// nothing.
	Paths  []string
	Note   string
	Action Action
}

// Plan is a parsed planning response.
type Plan struct {
	Title string
	Steps []Step
	// Text is the response exactly as the model wrote it, which is what [s]
	// saves and what the prose fallback renders.
	Text string
}

// Structured reports whether the response adopted the step shape. A false
// answer is the card's cue to render prose instead of failing.
func (p Plan) Structured() bool { return len(p.Steps) > 0 }

// WritePaths are the distinct files the plan says it would change, in first
// mention order.
func (p Plan) WritePaths() []string { return p.pathsWhere(func(a Action) bool { return a.Writes() }) }

// DeletePaths are the distinct files the plan says it would remove.
func (p Plan) DeletePaths() []string { return p.pathsWhere(func(a Action) bool { return a == Delete }) }

// NeedsNetwork reports whether any step said it reaches off the machine.
func (p Plan) NeedsNetwork() bool { return p.anyAction(Network) }

// Runs reports whether any step said it executes something.
func (p Plan) Runs() bool { return p.anyAction(Run) }

func (p Plan) anyAction(want Action) bool {
	for _, s := range p.Steps {
		if s.Action == want {
			return true
		}
	}
	return false
}

func (p Plan) pathsWhere(match func(Action) bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if !match(s.Action) {
			continue
		}
		for _, path := range s.Paths {
			if seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

// stepPattern matches a numbered item, capturing its indent: every step of
// one list is written at the same indent, which is what tells a step apart
// from a numbered list nested inside one.
var stepPattern = regexp.MustCompile(`^( {0,3})(\d{1,2})[.)]\s+(\S.*)$`)

// keyPattern matches a step's continuation line — `files: …`, optionally
// bulleted and at any indent.
var keyPattern = regexp.MustCompile(`^\s*(?:[-*+]\s+)?([A-Za-z]+)\s*:\s*(.*)$`)

// Parse reads a planning response into a plan. It never errors: a response
// with no step list parses to a Plan carrying only Text, which is what the
// card's prose fallback renders.
func Parse(text string) Plan {
	p := Plan{Text: text}
	var cur *Step
	indent := ""
	// stated records the steps whose action the model spelled out, so the
	// default below never overrides one it was given.
	stated := map[int]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := stepPattern.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[2])
			// The list has to start at 1 and climb, or it is prose that
			// happens to be numbered rather than the plan's steps; and every
			// step sits at the indent the first one set, so a sub-list under
			// a step is a detail of that step, not the next one.
			if cur == nil && n != 1 {
				continue
			}
			if cur != nil && (n <= cur.Number || m[1] != indent) {
				continue
			}
			indent = m[1]
			p.Steps = append(p.Steps, Step{Number: n, Title: cleanTitle(m[3])})
			cur = &p.Steps[len(p.Steps)-1]
			continue
		}
		if cur != nil {
			if m := keyPattern.FindStringSubmatch(line); m != nil {
				if applyKey(cur, strings.ToLower(m[1]), m[2]) {
					stated[len(p.Steps)-1] = true
				}
			}
			continue
		}
		if p.Title == "" {
			p.Title = headingTitle(line)
		}
	}
	// A step that named files and did not say what for is an edit; one that
	// named neither is inspection. Neither is a guess about paths — only
	// about what the paths the model already gave are for.
	for i := range p.Steps {
		if !stated[i] && len(p.Steps[i].Paths) > 0 {
			p.Steps[i].Action = Edit
		}
	}
	return p
}

// applyKey folds one `key: value` continuation line into the open step and
// reports whether it was an `action:` line — the one key whose absence the
// caller has to make a decision about.
func applyKey(s *Step, key, value string) (statedAction bool) {
	value = strings.TrimSpace(value)
	switch key {
	case "files", "file", "paths", "path":
		s.Paths = append(s.Paths, parsePaths(value)...)
	case "action", "kind", "intent":
		if a, ok := parseAction(value); ok {
			s.Action = a
			return true
		}
	case "note", "notes", "detail", "details":
		if s.Note == "" {
			s.Note = strings.TrimSpace(stripMarks(value))
		}
	}
	return false
}

// pathSplit are the separators a model reaches for when listing paths: the
// comma it was asked for, and the middle dot the card itself uses.
var pathSplit = regexp.MustCompile(`\s*[,·;]\s*`)

// parsePaths reads a `files:` value. Everything the model can plausibly wrap
// a path in — backticks, quotes, bold — comes off; a value that says there
// are none yields none rather than a path called "none".
func parsePaths(value string) []string {
	var out []string
	for _, field := range pathSplit.Split(value, -1) {
		path := strings.TrimSpace(stripMarks(field))
		path = strings.Trim(path, `"'`)
		switch strings.ToLower(path) {
		case "", "none", "n/a", "na", "-", "—":
			continue
		}
		out = append(out, path)
	}
	return out
}

// actionWords maps what a model is likely to write to the action it means.
// An unrecognised word leaves the step's action alone rather than forcing it
// into the nearest match.
var actionWords = map[string]Action{
	"read": Read, "inspect": Read, "review": Read, "research": Read, "none": Read,
	"edit": Edit, "modify": Edit, "update": Edit, "change": Edit, "rewrite": Edit,
	"create": Create, "add": Create, "new": Create, "write": Create,
	"delete": Delete, "remove": Delete, "rm": Delete,
	"run": Run, "command": Run, "test": Run, "build": Run, "verify": Run,
	"network": Network, "fetch": Network, "download": Network, "web": Network,
}

func parseAction(value string) (Action, bool) {
	word := strings.ToLower(strings.TrimSpace(stripMarks(value)))
	if i := strings.IndexAny(word, " \t-—"); i > 0 {
		word = word[:i]
	}
	a, ok := actionWords[strings.TrimSuffix(word, "s")]
	return a, ok
}

// headingTitle pulls the plan's title out of a heading above the steps. Only
// a heading that names itself a plan qualifies, so a response that opens with
// a section heading of its own does not end up titled by it.
func headingTitle(line string) string {
	rest := strings.TrimSpace(line)
	if !strings.HasPrefix(rest, "#") {
		return ""
	}
	rest = strings.TrimSpace(strings.TrimLeft(rest, "#"))
	rest = stripMarks(rest)
	lower := strings.ToLower(rest)
	if !strings.HasPrefix(lower, "plan") {
		return ""
	}
	rest = strings.TrimSpace(rest[len("plan"):])
	return strings.TrimSpace(strings.TrimLeft(rest, ":-–—·"))
}

// cleanTitle strips markdown emphasis and a trailing colon from a step title,
// which is the whole of what models decorate them with.
func cleanTitle(s string) string {
	return strings.TrimRight(strings.TrimSpace(stripMarks(s)), ":")
}

// stripMarks removes backticks and the ** / __ emphasis pairs. It is not a
// markdown parser: it removes the characters, which is all these one-line
// fields ever need.
func stripMarks(s string) string {
	return strings.NewReplacer("`", "", "**", "", "__", "").Replace(s)
}
