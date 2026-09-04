package run

// The kinds of step a run is built out of.
//
// A run used to be five stages named in Go, and what the machine did next was
// a switch over which of them it was in. What a stage *is* — a turn whose
// answer the code reads by marker line, a child that reports a verdict, a
// command whose exit status is the verdict, a pause that asks a person, an
// end that commits or archives — is the part that is not about code at all.
// Which of them a run takes, and in what order, is.
//
// So the kinds live here, closed, and a pipeline composes them. A profile may
// only compose kinds whose answer tail and whose reading of it are the code's:
// a step that could write its own tail could write one the code does not
// parse, and a step the code cannot read is a step it cannot gate.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/todo"
)

// Kind is what one step of a run is made of.
type Kind string

const (
	// KindTurn is a session prompt in the step's own mode. It reads
	// `blocked:` always, and the grade and the questions where the step says
	// so.
	KindTurn Kind = "turn"
	// KindAgent is a sub-agent by role, falling back to a turn of the
	// session's own where there is no supervisor to spawn one from. It reads
	// `verdict: clean|findings`.
	KindAgent Kind = "agent"
	// KindFanOut is writer children over the paths a division declares, with
	// the turn that integrates them after.
	KindFanOut Kind = "fan-out"
	// KindCommand is the item's own checks and the workspace's gate, or a
	// command the step names. No model runs and there is no answer to read:
	// the exit status is the verdict.
	KindCommand Kind = "command"
	// KindGate is the pause card — resume, replan, or stop — which an
	// unattended run has nobody to answer.
	KindGate Kind = "gate"
	// KindFinish ends the run, in one of the ways Finish names.
	KindFinish Kind = "finish"
)

// Kinds is the closed set, in the order this file declares it.
func Kinds() []Kind {
	return []Kind{KindTurn, KindAgent, KindFanOut, KindCommand, KindGate, KindFinish}
}

// Known reports the kind being one the code carries out. A pipeline naming
// anything else is refused at load rather than at the step, because a run
// that reached a step nothing can do would have spent every turn before it.
func (k Kind) Known() bool {
	for _, known := range Kinds() {
		if k == known {
			return true
		}
	}
	return false
}

// Access is what a step may do to the tree: read it, or change it. It is the
// step's mode as a profile states it, and Mode is what a session is put into
// for it — a write step runs in auto, because there is nobody to approve each
// edit for it, and the session's own mode is restored when the run ends.
type Access string

const (
	Read  Access = "read"
	Write Access = "write"
)

// Mode is the permission mode a step of this access is sent in.
func (a Access) Mode() Mode {
	if a == Write {
		return ModeAuto
	}
	return ModePlan
}

// Finish is how a run ends.
type Finish string

const (
	// FinishCommit is a turn that writes the commit message and the report,
	// then the commit the runner makes and the archive after it.
	FinishCommit Finish = "commit"
	// FinishArchive is the item archived with a report the code writes,
	// naming the paths the work is in. It is what a run asked for without a
	// commit ends as, and what a profile whose work does not land in a
	// repository ends as always.
	FinishArchive Finish = "archive"
	// FinishNote is a turn that writes the report and nothing else, then the
	// archive. It is the end for work whose record is the report.
	FinishNote Finish = "note"
	// FinishCommand is a command the step names, run by the runner, and the
	// archive after it passes.
	FinishCommand Finish = "command"
	// FinishHook is the archive with the person's own `stop` hook left to
	// fire at the session's end, for a project whose landing is a script.
	FinishHook Finish = "hook"
)

// Finishes is the closed set, in the order this file declares it.
func Finishes() []Finish {
	return []Finish{FinishCommit, FinishArchive, FinishNote, FinishCommand, FinishHook}
}

// Known reports the finish being one the code carries out.
func (f Finish) Known() bool {
	for _, known := range Finishes() {
		if f == known {
			return true
		}
	}
	return false
}

// Writes reports a finish that puts the work somewhere a later reader can
// find it by itself, rather than leaving it in the working tree. It is what
// decides whether a run needs a repository before it spends its first turn.
func (f Finish) Writes() bool { return f == FinishCommit }

// Turns reports a finish that spends a model turn before it ends the run.
// An archive does not: there is no message to write and no report to ask
// for, so a turn there would be a turn spent producing what the code already
// knows.
func (f Finish) Turns() bool { return f == FinishCommit || f == FinishNote }

// Reads is what a step takes out of its answer beyond what its kind always
// takes. It is the step's because a plan, a grade and a question are things
// a project may or may not ask a stage for, while the marker lines they
// arrive on are not.
type Reads uint8

const (
	// ReadsPlan takes the numbered plan the plan shape asks for; a step that
	// reads one and gets none blocks, because the stages after it are built
	// on it.
	ReadsPlan Reads = 1 << iota
	// ReadsGrade takes the profile's grading field, which is what the rounds,
	// the review and the division are spent against.
	ReadsGrade
	// ReadsQuestions takes the questions the step could not settle, which is
	// what the gate after it is about.
	ReadsQuestions
	// ReadsFindings keeps the whole answer as what the run carries forward
	// for a later step to read. It has no marker line, because there is no
	// part of the answer to pick out: a step that gathers rather than
	// changes anything has produced nothing but what it wrote, and a step
	// after it that read a summary of that would be reading a summary of a
	// summary.
	ReadsFindings
)

// Has reports the step taking that part of the answer.
func (r Reads) Has(what Reads) bool { return r&what != 0 }

// PauseRule is when a gate stops for the person. The rules are a closed set
// because a gate is the one place a run hands the work back, and a project
// that could write its own condition could write one that never fires.
type PauseRule string

const (
	// PauseNever passes straight through — and blocks where the step before
	// it left questions, since a runner that answers a product question for
	// the person is writing their backlog rather than working it.
	PauseNever PauseRule = "never"
	// PauseQuestions stops where there are questions.
	PauseQuestions PauseRule = "questions"
	// PauseQuestionsOrUpgraded stops for those, and for work the step before
	// it graded higher than the file did.
	PauseQuestionsOrUpgraded PauseRule = "questions-or-upgraded"
	// PauseAlways stops whatever the answer said. A profile writes this as
	// `always-when <grade>`: the rule sits at that grade's place on the
	// scale, which is the grade at which spend and blast radius stop being
	// the runner's to decide.
	PauseAlways PauseRule = "always"
)

// PauseRules is the closed set, in the order this file declares it.
func PauseRules() []PauseRule {
	return []PauseRule{PauseNever, PauseQuestions, PauseQuestionsOrUpgraded, PauseAlways}
}

// Known reports the rule being one the gate carries out.
func (r PauseRule) Known() bool {
	for _, known := range PauseRules() {
		if r == known {
			return true
		}
	}
	return false
}

// When is the grade a step applies at, and empty for one that always does.
type When string

const (
	// WhenAlways is every run.
	WhenAlways When = ""
	// WhenLargest is the top of the profile's grading scale — the grade that
	// buys a division into lanes, and the only one that does.
	WhenLargest When = "largest"
)

// The answer shapes. Each is built from the kind and what the step says it
// reads, so a profile gets the shape its steps are gated by without writing
// a word of it. The shape is appended after whatever the instruction said,
// for the reason the whole arrangement exists: a wording that stopped asking
// for the grade would not fail the gate, it would quietly stop being one.
// See docs/capabilities/todo.md#the-stage-prompts-are-yours-to-edit.

// blockedLineShape is the line every turn ends with. A step that changes the
// tree and cannot is the one answer no gate can infer from prose.
const blockedLineShape = "If you cannot do it as the item asks, answer with one line `blocked: <why>` instead."

// summaryShape is what a turn that reads nothing in particular is asked for:
// a short account of what it did, and the block line.
const summaryShape = "When you are done, answer with a short summary of what you did and anything you departed from and why. " + blockedLineShape

// findingsShape is what a turn whose answer is the record asks for. It says
// the answer is the whole of it, because a turn that left the work in files
// nobody named — or in a summary of itself — leaves the steps after it
// reading nothing.
const findingsShape = "Answer with what you found, in full and in order, naming the source of every claim. This answer is the whole of what the steps after this one read, so anything left out of it is lost. " + blockedLineShape

// planShape is the numbered plan the steps after a planning turn are built on.
const planShape = `The plan, in the plan shape (a "## Plan:" heading, then numbered steps with files:/action:/note: lines).`

// questionsShape is the question block a gate reads.
const questionsShape = "One line `questions: none`, or `questions:` followed by one bulleted line per question you cannot answer from the code and the item and that would change what you build. Do not ask what you can decide yourself; do ask before guessing at a product decision."

// verdictShape is what a reading answers with, whether the reader is a child
// or the session's own turn.
const verdictShape = "one line `verdict: clean` if there is nothing that must change before this is finished, or `verdict: findings` followed by the findings ranked by severity with file:line. Style that hides no bug is not a finding."

// reportShape is the archive's own record, which every finish that spends a
// turn asks for.
const reportShape = `## Report
Summary: <one paragraph of what was done>
Decisions: <bullets — what was decided, the alternative, why>
| File | Change |
|---|---|
<one row per file changed>
Deviations from plan: <none, or bullets>
Follow-ups: <none, or bullets of work this item leaves open>`

// Shape is the answer this step asks for, in the words the code that reads it
// back understands. task is the reading handed to a child rather than taken
// in the session's own turn: the child writes a report and the verdict is the
// last line of it, where a turn answers with the verdict and nothing else.
func (ps PipelineStep) Shape(p todo.Profile, task bool) string {
	switch ps.Kind {
	case KindTurn:
		return ps.turnShape(p)
	case KindAgent:
		if task {
			return "End your report with " + verdictShape
		}
		return "Answer with " + verdictShape
	case KindFanOut:
		// The division's own shape is the lane grammar, which lanes.go
		// states beside the parser that reads it back.
		return ""
	case KindFinish:
		return finishShape(ps.Finish)
	}
	return ""
}

// turnShape is a turn's answer: the numbered asks it declared it reads, or a
// summary where it declared none, and the block line either way.
func (ps PipelineStep) turnShape(p todo.Profile) string {
	if ps.Reads.Has(ReadsFindings) {
		return findingsShape
	}
	var asks []string
	if ps.Reads.Has(ReadsPlan) {
		asks = append(asks, planShape)
	}
	if ps.Reads.Has(ReadsGrade) {
		if grade, ok := p.GradeField(); ok {
			asks = append(asks, fmt.Sprintf("One line `%s: %s` — your grade of the work, whatever the item says: %s.",
				grade.Name, strings.Join(grade.Words(), "|"), grade.Sentence()))
		}
	}
	if ps.Reads.Has(ReadsQuestions) {
		asks = append(asks, questionsShape)
	}
	if len(asks) == 0 {
		return summaryShape
	}
	var b strings.Builder
	b.WriteString("Work out exactly how to satisfy the acceptance criteria. Then answer with:\n\n")
	for i, ask := range asks {
		fmt.Fprintf(&b, "%d. %s\n", i+1, ask)
	}
	b.WriteString("\n" + blockedLineShape)
	return b.String()
}

// finishShape is what a finish that spends a turn asks that turn for.
func finishShape(f Finish) string {
	switch f {
	case FinishCommit:
		return "Then write the report for the item's archive.\n\nAnswer in exactly this shape, and nothing else:\n\nCOMMIT:\n<subject line>\n\n<body>\n\nREPORT:\n" + reportShape
	case FinishNote:
		return "Answer with the report for the item's archive, in exactly this shape and nothing else:\n\nREPORT:\n" + reportShape
	}
	return ""
}
