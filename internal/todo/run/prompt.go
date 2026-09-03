package run

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/todo"
)

// The stage prompts. Each one names the item, says what the stage is for,
// and ends with the exact shape the runner reads back — because a stage
// whose answer the code cannot parse is a stage the code cannot gate.

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

const standards = `Read AGENTS.md (or the project's equivalent) and any skill that applies before you touch anything. Follow the project's standards for how work is documented and tested. Preserve unrelated changes already in the working tree and never edit generated files directly.`

// sprintBlock is the set's goal, for an item being worked as part of one.
// It rides in the research stage and nowhere else: what the set is for
// changes how the work is scoped, which is a research question, and
// repeating it at every stage would only spend tokens restating it.
func sprintBlock(goal string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ""
	}
	return "SPRINT — what the set of items this one belongs to is for:\n" + goal + "\n"
}

func researchPrompt(it todo.Item, sprint, context string) string {
	var b strings.Builder
	b.WriteString("You are working one backlog item through to a commit, in stages. This is the RESEARCH stage: read, do not change anything.\n\n")
	b.WriteString(itemBlock(it))
	if block := sprintBlock(sprint); block != "" {
		b.WriteString("\n" + block)
	}
	if context != "" {
		b.WriteString("\n" + context + "\n")
	}
	b.WriteString("\n" + standards + "\n\n")
	b.WriteString(`Work out exactly how to satisfy the acceptance criteria. Then answer with:

1. The plan, in the plan shape (a "## Plan:" heading, then numbered steps with files:/action:/note: lines).
2. One line ` + "`size: S|M|L`" + ` — your grade of the work, whatever the item says: S is an hour in one or two files with no design decisions, M an afternoon over a few files with some judgement, L days, many files, or a design decision still open.
3. One line ` + "`questions: none`" + `, or ` + "`questions:`" + ` followed by one bulleted line per question you cannot answer from the code and the item and that would change what you build. Do not ask what you can decide yourself; do ask before guessing at a product decision.

If the item cannot be done as written, answer with one line ` + "`blocked: <why>`" + ` instead.`)
	return b.String()
}

func implementPrompt(it todo.Item, plan, answers string) string {
	var b strings.Builder
	b.WriteString("IMPLEMENT stage. Carry out the plan below and satisfy the item's acceptance criteria.\n\n")
	b.WriteString(itemBlock(it))
	b.WriteString("\nAPPROVED PLAN:\n" + strings.TrimSpace(plan) + "\n\n")
	if answers != "" {
		b.WriteString(answers + "\n\n")
	}
	b.WriteString(standards + "\n\n")
	b.WriteString(`Touch only what the plan names plus what it must to work. Write the tests the item lists where they do not exist. As you satisfy each acceptance criterion and finish each task, tick its checkbox in the item file named above — that file is the record. Do not commit; the runner commits. Do not run the whole verification suite yourself; the runner runs it next.

When you are done, answer with a short summary of what you changed and anything you departed from in the plan and why. If you cannot finish, answer with one line ` + "`blocked: <why>`" + `.`)
	return b.String()
}

// readTheChange is how a review stage is told to find what changed. Outside
// a repository the two git commands return nothing and the model spends its
// turn discovering that; the plan names the files, so it is what stands in.
func readTheChange(repo bool) string {
	if repo {
		return "Run `git diff` and `git status` and read every changed file in full."
	}
	return "This is not a git repository, so there is no diff to read: read in full every file the plan below names, and any file it led you to."
}

func reviewPrompt(it todo.Item, plan string, repo bool) string {
	var b strings.Builder
	b.WriteString("REVIEW stage. Verification passed. Now read the change as a reviewer who did not write it.\n\n")
	b.WriteString(itemBlock(it))
	b.WriteString("\nAPPROVED PLAN:\n" + strings.TrimSpace(plan) + "\n\n")
	b.WriteString(readTheChange(repo) + ` Check, in this order: bugs — concrete inputs that produce a wrong result; acceptance criteria not actually met; behaviour the item did not ask for; the project's conventions from AGENTS.md broken; tests missing for a case the criteria name. Do not change anything in this stage.

Answer with one line ` + "`verdict: clean`" + ` if there is nothing that must change before this commits, or ` + "`verdict: findings`" + ` followed by the findings ranked by severity with file:line. Style that hides no bug is not a finding.`)
	return b.String()
}

// reviewTask is the reviewer child's task: the item, the plan and the
// diff, since the child has no commands to read the change with itself.
func reviewTask(it todo.Item, plan, diff string, repo bool) string {
	var b strings.Builder
	b.WriteString("Review this change against the backlog item it implements. You did not write it.\n\n")
	b.WriteString(itemBlock(it))
	b.WriteString("\nAPPROVED PLAN:\n" + strings.TrimSpace(plan) + "\n\n")
	if strings.TrimSpace(diff) != "" {
		b.WriteString("THE CHANGE (git diff, bounded):\n```diff\n" + strings.TrimSpace(diff) + "\n```\n\n")
	}
	// The child has no commands, so it cannot go and check. Outside a
	// repository there is no history behind the diff either, and a
	// reviewer that believes there is will report a file as unchanged
	// when what it actually found was no repository to ask.
	if !repo {
		b.WriteString("This is not a git repository. The change above is shhh's own record of every edit the run made, and it is the whole of what changed; there is no history to compare it against.\n\n")
	}
	b.WriteString(`Read every file the diff touches in full, then the tests that cover them. Check, in this order: bugs — concrete inputs that produce a wrong result; acceptance criteria not actually met; behaviour the item did not ask for; the project's conventions from AGENTS.md broken; tests missing for a case the criteria name.

End your report with one line ` + "`verdict: clean`" + ` if nothing must change before this commits, or ` + "`verdict: findings`" + ` followed by the findings ranked by severity with file:line. Style that hides no bug is not a finding.`)
	return b.String()
}

func remediatePrompt(it todo.Item, findings string) string {
	var b strings.Builder
	b.WriteString("REMEDIATE stage. Fix exactly what is listed below, and nothing else.\n\n")
	b.WriteString(itemBlock(it))
	b.WriteString("\n" + strings.TrimSpace(findings) + "\n\n")
	b.WriteString(standards + "\n\n")
	b.WriteString("Do not commit. When you are done, answer with a short summary of the fixes. If a finding is wrong, say so and why rather than changing working code; if you cannot fix one, answer with one line `blocked: <why>`.")
	return b.String()
}

// commitStyle is how the commit stage is told to find the repository's own
// wording. repo is read here rather than assumed because every git
// instruction in a stage prompt reads the same fact, and a prompt builder
// that took the repository for granted is how the run came to tell the
// model to read a history that does not exist.
func commitStyle(repo bool) string {
	if repo {
		return "Write the commit message in this repository's own style: read `git log -10 --format='%s%n%n%b'` first and match the shape of its subjects and bodies exactly — its case, its length, whether it uses a type prefix, how it argues."
	}
	return "Write the commit message as a conventional commit — a `type(scope): summary` subject in the imperative under 72 characters, then a blank line and a body in prose saying why — since there is no history here to read a house style out of."
}

func commitPrompt(it todo.Item, plan string, repo bool) string {
	var b strings.Builder
	b.WriteString("COMMIT stage. The change is verified and reviewed. Do not change any file now.\n\n")
	b.WriteString(itemBlock(it))
	b.WriteString("\n" + commitStyle(repo) + ` Then write the report for the item's archive.

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
Follow-ups: <none, or bullets of work this item leaves open>`)
	return b.String()
}
