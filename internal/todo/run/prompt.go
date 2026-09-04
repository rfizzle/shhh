package run

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/todo"
)

// The stage prompts. Each one names the item, says what the stage is for,
// and ends with the exact shape the runner reads back — because a stage
// whose answer the code cannot parse is a stage the code cannot gate.
//
// A stage prompt is three things and only the first is a wording: the
// instruction, which a project may replace with a file; the blocks the run
// has to hand the model — the item, the plan, the answers, the findings,
// the diff — which the code places; and the answer shape, which the code
// appends whatever the instruction said. What a run tells the model is the
// project's business and how the run reads the answer back is not, which is
// the line the interruption machinery already draws.
// See docs/capabilities/todo.md#the-stage-prompts-are-yours-to-edit.

// The substitutions a stage wording may name, to place a block itself
// rather than take it after the instruction. The same names are what the
// loader checks a file against before a run is built on one
// (internal/agent/steering.go). They are spelled in both places rather than
// shared, because a state machine that builds a string must not have to
// import the agent loop to do it, and a test holds the two sets together.
const (
	PlaceholderItem     = "{{item}}"
	PlaceholderPlan     = "{{plan}}"
	PlaceholderAnswers  = "{{answers}}"
	PlaceholderFindings = "{{findings}}"
	PlaceholderDiff     = "{{diff}}"
)

// Wordings are the stage instructions in force for a run: the text of the
// file that replaced one, and empty for a stage nothing replaced, which is
// what the built-in instruction stands on.
//
// It is carried on the run rather than looked up at each stage because a run
// is worked with the wordings it started under. A file edited mid-run must
// not change what a run three stages in is asked next, and a run picked up
// in another session has to be able to say that the file moved.
type Wordings struct {
	// Standards is the sentence every stage that changes the tree carries:
	// read the project's own instructions, follow how it documents and
	// tests, leave alone what the work did not come for. It is its own
	// wording because it is the line a project most often has to change and
	// because every stage that changes the tree — research, implement,
	// remediate, and a large item's lanes and their integration — sends it.
	Standards string
	// Research, Implement, Review, Remediate and Commit are one stage's
	// instruction each. ReviewTask is the reviewer child's, which is a
	// different instruction to the same end: the child has no commands to
	// read the change with, so it is handed the diff instead of told to
	// produce one.
	Research   string
	Implement  string
	Review     string
	ReviewTask string
	Remediate  string
	Commit     string
}

// Digest identifies the set, for a record that has to say the wordings moved
// under a run. It is empty for the built-in set, so a project that replaced
// nothing writes what it always wrote.
func (w Wordings) Digest() string {
	if w == (Wordings{}) {
		return ""
	}
	h := sha256.New()
	for _, part := range []string{w.Standards, w.Research, w.Implement, w.Review, w.ReviewTask, w.Remediate, w.Commit} {
		fmt.Fprintf(h, "%d\x00%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// standards is the shared sentence as this run sends it.
func (w Wordings) standards() string { return or(w.Standards, builtinStandards) }

// or is the wording in force for one stage: the file's where there is one,
// and the built-in text otherwise. A file of whitespace is not a wording —
// nothing downstream could tell it from an unset key — so it reads as unset
// here too, behind the loader that should already have refused it.
func or(configured, builtin string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	return builtin
}

// itemBlock is the item as the model sees it in every stage.
func itemBlock(it todo.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "BACKLOG ITEM %s (%s, priority %s", it.Slug, orDash(string(it.Kind)), it.Priority)
	if it.Size != "" {
		fmt.Fprintf(&b, ", size %s", it.Size)
	}
	b.WriteString(")\n")
	fmt.Fprintf(&b, "File: %s\n", it.Path)
	fmt.Fprintf(&b, "Title: %s\n", it.Title)
	if body := strings.TrimSpace(it.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return b.String()
}

const builtinStandards = `Read AGENTS.md (or the project's equivalent) and any skill that applies before you touch anything. Follow the project's standards for how work is documented and tested. Preserve unrelated changes already in the working tree and never edit generated files directly.`

// block is one part a stage hands the model: the substitution a wording may
// name to place it, and the text itself.
type block struct {
	name string
	text string
}

// stagePrompt assembles one stage: the instruction, the blocks it carries,
// the pieces the code puts after them, and the answer shape.
//
// A wording that names a block's substitution places that block itself,
// exactly where it wrote it and with the spacing it wrote around it — a
// substitution mid-sentence is a substitution mid-sentence, and a builder
// that reflowed it into paragraphs would be editing the file. Every block
// the wording did not name is taken after the instruction, in the order the
// stage declares them, and `after` — the standards sentence, the commit
// style, the answer shape — comes last, in the order given.
//
// The shape comes from here whatever the wording said: Observe reads a
// stage's answer by its marker lines, so a wording that stopped asking for
// `size:` would make every research turn look like a block, and a project
// cannot be allowed to edit out the thing that makes a gate a gate.
func stagePrompt(wording string, blocks []block, after ...string) string {
	var rest []string
	for _, b := range blocks {
		text := strings.TrimSpace(b.text)
		if !strings.Contains(wording, b.name) {
			rest = append(rest, text)
			continue
		}
		// A block this run has nothing for takes the blank line it sat on
		// with it, so a stage with no answers does not send the hole where
		// they would have been.
		if text == "" {
			wording = strings.ReplaceAll(wording, "\n\n"+b.name+"\n\n", "\n\n")
		}
		wording = strings.ReplaceAll(wording, b.name, text)
	}
	return join(append(append([]string{wording}, rest...), after...)...)
}

// join runs the pieces of a prompt together with a blank line between them
// and leaves out the ones this run has nothing for, so a stage with no
// answers and no findings does not send the space where they would be.
func join(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// sprintBlock is the set's goal, for an item being worked as part of one.
// It rides in the research stage and nowhere else: what the set is for
// changes how the work is scoped, which is a research question, and
// repeating it at every stage would only spend tokens restating it.
func sprintBlock(goal string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ""
	}
	return "SPRINT — what the set of items this one belongs to is for:\n" + goal
}

// planBlock is the approved plan under the label the runner gives it. The
// label is the code's and not the wording's: it is how the model is told
// which of the blocks is the plan, and a stage that renamed it would be
// describing a block by a name nothing else in the run uses.
func planBlock(plan string) string {
	return "APPROVED PLAN:\n" + strings.TrimSpace(plan)
}

// The built-in wordings name their own substitutions, so the blocks sit
// where the stages have always had them: what the model is being shown
// before the instructions that talk about it. A file that names none gets
// them after its instruction instead, which is the only order a builder can
// choose for prose it did not write.
const builtinResearch = `You are working one backlog item through to a commit, in stages. This is the RESEARCH stage: read, do not change anything.

` + PlaceholderItem + `

` + PlaceholderAnswers

const researchTail = `Work out exactly how to satisfy the acceptance criteria. Then answer with:

1. The plan, in the plan shape (a "## Plan:" heading, then numbered steps with files:/action:/note: lines).
2. One line ` + "`size: S|M|L`" + ` — your grade of the work, whatever the item says: S is an hour in one or two files with no design decisions, M an afternoon over a few files with some judgement, L days, many files, or a design decision still open.
3. One line ` + "`questions: none`" + `, or ` + "`questions:`" + ` followed by one bulleted line per question you cannot answer from the code and the item and that would change what you build. Do not ask what you can decide yourself; do ask before guessing at a product decision.

If the item cannot be done as written, answer with one line ` + "`blocked: <why>`" + ` instead.`

func researchPrompt(it todo.Item, sprint, groomed, answers string, w Wordings) string {
	// The sprint's goal and the reading the person accepted ride with the
	// item rather than as blocks of their own: they are what this run states
	// the item to be, and a wording that placed the item would want them
	// where it put it.
	item := join(itemBlock(it), sprintBlock(sprint), groomed)
	return stagePrompt(or(w.Research, builtinResearch), []block{
		{PlaceholderItem, item},
		{PlaceholderAnswers, answers},
	}, w.standards(), researchTail)
}

// The built-in instructions say nothing about where a block sits, beyond
// what the code places for itself. A wording is put before the blocks it
// carries and a file may move any of them anyway, so an instruction that
// said "the item above" would be wrong the first time somebody wrote one.
const builtinImplement = `IMPLEMENT stage. Carry out the approved plan and satisfy the item's acceptance criteria.

` + PlaceholderItem + `

` + PlaceholderPlan + `

` + PlaceholderAnswers + `

Touch only what the plan names plus what it must to work. Write the tests the item lists where they do not exist. As you satisfy each acceptance criterion and finish each task, tick its checkbox in the item's own file — the one the item block names, which is the record.`

const implementTail = "Do not commit; the runner commits. Do not run the whole verification suite yourself; the runner runs it next.\n\n" +
	"When you are done, answer with a short summary of what you changed and anything you departed from in the plan and why. If you cannot finish, answer with one line `blocked: <why>`."

func implementPrompt(it todo.Item, plan, answers string, w Wordings) string {
	return stagePrompt(or(w.Implement, builtinImplement), []block{
		{PlaceholderItem, itemBlock(it)},
		{PlaceholderPlan, planBlock(plan)},
		{PlaceholderAnswers, answers},
	}, w.standards(), implementTail)
}

// readTheChange is how a review stage is told to find what changed. It is a
// block rather than part of the wording because whether there is a history
// to read is a fact about the machine and not a sentence anyone is tuning:
// outside a repository the two git commands return nothing and the model
// spends its turn discovering that, and a project that replaced the review
// wording must not be able to lose the sentence that says so.
func readTheChange(repo bool) string {
	if repo {
		return "Run `git diff` and `git status` and read every changed file in full."
	}
	return "This is not a git repository, so there is no diff to read: read in full every file the approved plan names, and any file it led you to."
}

const builtinReview = `REVIEW stage. Verification passed. Now read the change as a reviewer who did not write it.

` + PlaceholderItem + `

` + PlaceholderPlan + `

` + PlaceholderDiff + ` Check, in this order: bugs — concrete inputs that produce a wrong result; acceptance criteria not actually met; behaviour the item did not ask for; the project's conventions from AGENTS.md broken; tests missing for a case the criteria name. Do not change anything in this stage.`

const reviewTail = "Answer with one line `verdict: clean` if there is nothing that must change before this commits, or `verdict: findings` followed by the findings ranked by severity with file:line. Style that hides no bug is not a finding."

func reviewPrompt(it todo.Item, plan string, repo bool, w Wordings) string {
	return stagePrompt(or(w.Review, builtinReview), []block{
		{PlaceholderItem, itemBlock(it)},
		{PlaceholderPlan, planBlock(plan)},
		{PlaceholderDiff, readTheChange(repo)},
	}, reviewTail)
}

// diffBlock is the change itself, for the reviewer child that has no
// commands to go and read it with. Outside a repository there is no history
// behind the diff either, and a reviewer that believes there is will report
// a file as unchanged when what it found was no repository to ask.
func diffBlock(diff string, repo bool) string {
	var parts []string
	if diff = strings.TrimSpace(diff); diff != "" {
		parts = append(parts, "THE CHANGE (git diff, bounded):\n```diff\n"+diff+"\n```")
	}
	if !repo {
		parts = append(parts, "This is not a git repository. The change above is shhh's own record of every edit the run made, and it is the whole of what changed; there is no history to compare it against.")
	}
	return join(parts...)
}

const builtinReviewTask = `Review this change against the backlog item it implements. You did not write it.

` + PlaceholderItem + `

` + PlaceholderPlan + `

` + PlaceholderDiff + `

Read every file the diff touches in full, then the tests that cover them. Check, in this order: bugs — concrete inputs that produce a wrong result; acceptance criteria not actually met; behaviour the item did not ask for; the project's conventions from AGENTS.md broken; tests missing for a case the criteria name.`

const reviewTaskTail = "End your report with one line `verdict: clean` if nothing must change before this commits, or `verdict: findings` followed by the findings ranked by severity with file:line. Style that hides no bug is not a finding."

// reviewTask is the reviewer child's task: the item, the plan and the
// diff, since the child has no commands to read the change with itself.
func reviewTask(it todo.Item, plan, diff string, repo bool, w Wordings) string {
	return stagePrompt(or(w.ReviewTask, builtinReviewTask), []block{
		{PlaceholderItem, itemBlock(it)},
		{PlaceholderPlan, planBlock(plan)},
		{PlaceholderDiff, diffBlock(diff, repo)},
	}, reviewTaskTail)
}

const builtinRemediate = `REMEDIATE stage. Fix exactly what the findings list, and nothing else.

` + PlaceholderItem + `

` + PlaceholderFindings

const remediateTail = "Do not commit. When you are done, answer with a short summary of the fixes. If a finding is wrong, say so and why rather than changing working code; if you cannot fix one, answer with one line `blocked: <why>`."

func remediatePrompt(it todo.Item, findings string, w Wordings) string {
	return stagePrompt(or(w.Remediate, builtinRemediate), []block{
		{PlaceholderItem, itemBlock(it)},
		{PlaceholderFindings, findings},
	}, w.standards(), remediateTail)
}

// commitStyle is how the commit stage is told to find the repository's own
// wording. It is a block for the reason readTheChange is: whether there is a
// history to read a house style out of is a fact about the machine, and a
// prompt builder that took the repository for granted is how the run came to
// tell the model to read a history that does not exist.
func commitStyle(repo bool) string {
	if repo {
		return "Write the commit message in this repository's own style: read `git log -10 --format='%s%n%n%b'` first and match the shape of its subjects and bodies exactly — its case, its length, whether it uses a type prefix, how it argues."
	}
	return "Write the commit message as a conventional commit — a `type(scope): summary` subject in the imperative under 72 characters, then a blank line and a body in prose saying why — since there is no history here to read a house style out of."
}

const builtinCommit = `COMMIT stage. The change is verified and reviewed. Do not change any file now.

` + PlaceholderItem

const commitTail = `Then write the report for the item's archive.

Answer in exactly this shape, and nothing else:

COMMIT:
<subject line>

<body>

REPORT:
## Report
Summary: <one paragraph of what was done>
Decisions: <bullets — what was decided, the alternative, why>
| File | Change |
|---|---|
<one row per file changed>
Deviations from plan: <none, or bullets>
Follow-ups: <none, or bullets of work this item leaves open>`

func commitPrompt(it todo.Item, repo bool, w Wordings) string {
	// The style sentence is not a substitution a wording may place: it is
	// not the change, and a stage's own name for it would be a second
	// meaning for `{{diff}}` in a run that already has one.
	return stagePrompt(or(w.Commit, builtinCommit), []block{
		{PlaceholderItem, itemBlock(it)},
	}, commitStyle(repo), commitTail)
}

// GroomPrompt is the reading of one item against the tree as it stands: every
// claim the item makes graded from a closed set, with one line of evidence
// under each. It is the research stage's own reading moved to before the run
// — which is where a stale item is worth finding, rather than three stages
// in on a plan built against the wrong file.
//
// The answer is markers rather than prose because what comes of it is a diff
// of single lines the person accepts one by one, and a diff needs a fact per
// line. The verdict words are the backlog's own closed set, so the prompt
// that asks for them and the reader that parses them cannot drift apart.
//
// It carries the built-in standards sentence and takes no wording of its
// own: a grooming pass is not a stage of a run — no run ever enters it — and
// the wordings a project replaces are the run's.
// See docs/capabilities/todo.md#an-item-is-checked-before-it-is-worked.
func GroomPrompt(it todo.Item) string {
	var b strings.Builder
	b.WriteString("This is a GROOMING pass over one backlog item: read the code, change nothing, and say whether the item is still true.\n\n")
	b.WriteString(itemBlock(it))
	b.WriteString("\n" + builtinStandards + "\n\n")
	b.WriteString(`Take every claim the item makes and check it against the tree as it stands: every ` + "`path:line`" + `, every function, flag, config key or command it names, every sentence about what happens today, every entry in ` + "`depends_on`" + `, every acceptance criterion, and the size it is graded at. Read the files; do not answer from the item alone.

Answer with one block per claim and nothing else between the blocks:

claim: <the item's line, copied exactly as the file has it>
verdict: <exactly one of: ` + verdictWords() + `>
now: <the line as it should read — omit it for holds and unknown, and leave it empty to say the line should go>
evidence: <one line: the path, the symbol or the commit that shows it>

`)
	b.WriteString(verdictKey())
	b.WriteString("\n\nThe evidence line is the only free text you may write. Do not add a summary, and do not raise a claim the item does not make: a reading that can say \"this may need updating\" will say it about everything, which is why the verdicts are a closed set.")
	return b.String()
}

// verdictWords is the closed set on one line, for the prompt's verdict line.
// It is built from the set rather than written out, so a verdict added to
// the vocabulary is one the prompt offers without anybody remembering to.
func verdictWords() string {
	words := make([]string, 0, len(todo.Verdicts()))
	for _, v := range todo.Verdicts() {
		words = append(words, string(v))
	}
	return strings.Join(words, ", ")
}

// verdictKey is what each verdict means, in the order the set is declared.
// The wording is here rather than beside the constants because it is an
// instruction to a model, and the constants are a vocabulary the code shares
// with a header line and a card.
func verdictKey() string {
	return `What the verdicts mean:
- holds: the tree still bears the claim out.
- moved: what it names is still there, under another name or in another file. ` + "`now:`" + ` is the line pointing at where it is.
- changed: what it says happens today is not what happens today. ` + "`now:`" + ` restates it.
- gone: what it names is not in the tree at all.
- already done: an acceptance criterion the tree already satisfies. ` + "`now:`" + ` is the criterion ticked, naming the commit that did it.
- unknown: you could not settle it from the code. Say so rather than guessing; a guess written into the item is worse than the line it replaced.`
}
