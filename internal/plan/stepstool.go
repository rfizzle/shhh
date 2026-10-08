package plan

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/receipt/describe"
)

// StepsToolName is the tool an agent keeps its working checklist with.
const StepsToolName = "steps"

// StepsToolDefinition is the steps tool. It takes the whole list on every
// call rather than an edit to it, so marking a step done, taking a mark back,
// rewording, adding, dropping and reordering are one call and one shape, and
// the list the call leaves is exactly the list it names. The result is that
// list with the numbers it is drawn by, which is what lets the agent carry on
// from it rather than from what it remembers writing.
// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.
func StepsToolDefinition() provider.Tool {
	return provider.Tool{
		Name: StepsToolName,
		Description: "Keep your working list for a task of several steps: the steps you mean to take, in order, and which are done. " +
			"Pass the whole list on every call — it replaces the one before — so marking a step done, taking a mark back, rewording, adding, dropping or reordering a step is the same call. " +
			"The person watching sees it as how far along you are. It is your own checklist, not a plan anyone approved, and marking every step done does not make the task finished. " +
			fmt.Sprintf("At most %d steps; an empty list clears it. The result is the list as it now stands, numbered as it is shown.", MaxWorkingSteps),
		Parameters: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"steps": {
					"type": "array",
					"maxItems": %d,
					"description": "Every step of the task in order, finished ones included; the list replaces the previous one whole",
					"items": {
						"type": "object",
						"properties": {
							"title": {"type": "string", "description": "What the step does, in one short line the person watching can follow"},
							"done": {"type": "boolean", "description": "True once the step is finished; leave it false, or send false to take a mark back"},
							"paths": {"type": "array", "items": {"type": "string"}, "description": "The files the step will touch, where you know them; they are shown under the step"}
						},
						"required": ["title"]
					}
				}
			},
			"required": ["steps"]
		}`, MaxWorkingSteps)),
	}
}

// Describers is how a call to the steps tool reads, by tool name. It has no
// word in the closed vocabulary yet, so it reads as its own name.
func Describers() map[string]describe.Describer {
	return map[string]describe.Describer{StepsToolName: describe.Unworded}
}

// stepsCall is the tool's arguments as the model sends them.
type stepsCall struct {
	Steps []struct {
		Title string   `json:"title"`
		Done  bool     `json:"done"`
		Paths []string `json:"paths"`
	} `json:"steps"`
}

// ParseStepsCall reads a steps call into the checklist it names, numbered
// from 1 in the order given. A call it refuses changes nothing, and the error
// says why in words the model can act on.
func ParseStepsCall(args json.RawMessage) (Checklist, error) {
	var c stepsCall
	if err := json.Unmarshal(args, &c); err != nil {
		return Checklist{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(c.Steps) > MaxWorkingSteps {
		return Checklist{}, fmt.Errorf("the list has %d steps and holds at most %d: group the work into fewer, larger steps", len(c.Steps), MaxWorkingSteps)
	}
	var l Checklist
	for i, s := range c.Steps {
		title := strings.Join(strings.Fields(s.Title), " ")
		if title == "" {
			return Checklist{}, fmt.Errorf("step %d has no title", i+1)
		}
		var paths []string
		for _, p := range s.Paths {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
		n := i + 1
		l.Steps = append(l.Steps, Step{Number: n, Title: title, Paths: paths})
		if s.Done {
			if l.Done == nil {
				l.Done = map[int]bool{}
			}
			l.Done[n] = true
		}
	}
	return l, nil
}

// Report is the checklist as the steps tool answers with it: the count, then
// every step with its number and its mark, the one being worked on pointed
// at.
func (l Checklist) Report() string {
	if len(l.Steps) == 0 {
		return "The working list is cleared."
	}
	done, total, current := l.Tally()
	var b strings.Builder
	fmt.Fprintf(&b, "Working list, %d of %d done", done, total)
	if current != "" {
		fmt.Fprintf(&b, " — on: %s", current)
	}
	b.WriteString("\n")
	onCurrent := false
	for _, s := range l.Steps {
		mark := "[ ]"
		if l.Done[s.Number] {
			mark = "[x]"
		}
		lead := "  "
		if !l.Done[s.Number] && !onCurrent {
			lead, onCurrent = "→ ", true
		}
		fmt.Fprintf(&b, "%s%s %d. %s\n", lead, mark, s.Number, s.Title)
	}
	return strings.TrimRight(b.String(), "\n")
}

// CarriedSteps is the list as it is put back in front of the model where the
// conversation that held its last steps call is gone — a compaction's
// summary, a resumed conversation — and "" for no list.
func CarriedSteps(l Checklist) string {
	if len(l.Steps) == 0 {
		return ""
	}
	return CarriedStepsPrefix + "\n" + l.Report()
}

// CarriedStepsPrefix opens the carried list, so a surface that has to take a
// carried copy back out of a conversation can recognise it.
const CarriedStepsPrefix = agent.CarriedStepsPrefix

// WrapStepsExecutor answers the steps tool: it checks the call and replies
// with the list the call names, and passes every other tool on. It keeps no
// state of its own — a surface applies the same call to the list it draws
// (ParseStepsCall), where that list lives, so a checked call and a drawn one
// cannot disagree.
func WrapStepsExecutor(next agent.ToolExecutor) agent.ToolExecutor {
	return func(name string, args json.RawMessage) (string, error) {
		if name != StepsToolName {
			return next(name, args)
		}
		l, err := ParseStepsCall(args)
		if err != nil {
			return "", err
		}
		return l.Report(), nil
	}
}
