package plan

import (
	"regexp"
	"strconv"
	"strings"
)

// progressPattern matches the line that marks one step of a plan done:
// `progress: 3`, with the step's number and nothing else required of it. The
// word `step` before the number and anything after it are allowed, because a
// model writes `progress: step 3 done` as readily as the bare form, and the
// number is the whole of what is read.
var progressPattern = regexp.MustCompile(`(?i)^\s*(?:[-*+]\s+)?progress\s*:\s*(?:step\s+)?#?(\d{1,2})\b`)

// Progress reads the step numbers a text marks done, one per `progress:`
// line, in the order they were written. It is the other half of the step
// grammar Parse reads: a plan names its steps once, and the lines after it
// say which of them are finished. A line that names no number marks nothing.
//
// It reads lines and never judges them against a plan: whether a number is
// one of the plan's steps is the caller's question, since only the caller
// holds the plan.
func Progress(text string) []int {
	var out []int
	for _, line := range strings.Split(text, "\n") {
		m := progressPattern.FindStringSubmatch(stripMarks(line))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}
