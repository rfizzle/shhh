package run

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/rfizzle/shhh/internal/todo"
)

// The step prompts. Each one names the item, says what the step is for, and
// ends with the exact shape the runner reads back — because a step whose
// answer the code cannot parse is a step the code cannot gate.
//
// A step prompt is three things and only the first is a wording: the
// instruction, which a project may replace with a file; the blocks the run
// has to hand the model — the item, the plan, the answers, the findings, the
// diff — which the code places; and the answer shape, which the code builds
// from the step's kind and appends whatever the instruction said. What a run
// tells the model is the project's business and how the run reads the answer
// back is not, which is the line the interruption machinery already draws.
// See docs/capabilities/todo.md#the-stage-prompts-are-yours-to-edit.

// The substitutions a step wording may name, to place a block itself rather
// than take it after the instruction. The same names are what the loader
// checks a file against before a run is built on one
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

// WordingStandards is the key of the one wording no step owns: the sentence
// every step that changes the tree carries. It is not a step's own because
// it is the line a project most often has to change and because more than
// one step sends it.
const WordingStandards = "standards"

// Wordings are the step instructions in force for a run, by wording key: the
// text of the file that replaced one, and nothing at all for a step nothing
// replaced, which is what the built-in instruction stands on.
//
// It is keyed rather than a struct of named stages because the steps are the
// pipeline's, so the set of keys is too: a profile whose run is scope, gather
// and file has three wordings a struct written for a code run has no fields
// for. The keys are the step names, `standards`, and `<step>_task` for the
// reading an agent step hands a child (Pipeline.WordingKeys).
//
// It is carried on the run rather than looked up at each step because a run
// is worked with the wordings it started under. A file edited mid-run must
// not change what a run three steps in is asked next, and a run picked up in
// another session has to be able to say that the file moved.
type Wordings map[string]string

// Digest identifies the set, for a record that has to say the wordings moved
// under a run. It is empty for the built-in set, so a project that replaced
// nothing writes what it always wrote.
func (w Wordings) Digest() string {
	if len(w) == 0 {
		return ""
	}
	keys := make([]string, 0, len(w))
	for key := range w {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		fmt.Fprintf(h, "%d\x00%s\x00%d\x00%s", len(key), key, len(w[key]), w[key])
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// standards is the shared sentence as this run sends it: the file that
// replaced it, or the one the pipeline carries.
func (w Wordings) standards(p Pipeline) string { return or(w[WordingStandards], p.standards()) }

// or is the wording in force for one step: the file's where there is one, and
// the built-in text otherwise. A file of whitespace is not a wording —
// nothing downstream could tell it from an unset key — so it reads as unset
// here too, behind the loader that should already have refused it.
func or(configured, builtin string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	return builtin
}

// itemBlock is the item as the model sees it in every step. The header
// fields are named as well as stated — `size: M` rather than `size M` —
// because which fields an item carries is the project's to say, and a step
// reading `deep` on its own could not tell what it was the answer to.
func itemBlock(it todo.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "BACKLOG ITEM %s", it.Slug)
	if fields := itemFields(it); fields != "" {
		fmt.Fprintf(&b, " (%s)", fields)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "File: %s\n", it.Path)
	fmt.Fprintf(&b, "Title: %s\n", it.Title)
	if body := strings.TrimSpace(it.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return b.String()
}

// itemFields is the item's header fields in the profile's order, each as
// `name: value`, leaving out the ones the file did not set.
func itemFields(it todo.Item) string {
	var parts []string
	for _, f := range it.Profile.Fields {
		value := it.Fields[f.Name]
		if f.Orders() {
			value = string(it.Priority)
		}
		if value != "" {
			parts = append(parts, f.Name+": "+value)
		}
	}
	return strings.Join(parts, ", ")
}

const builtinStandards = `Read AGENTS.md (or the project's equivalent) and any skill that applies before you touch anything. Follow the project's standards for how work is documented and tested. Preserve unrelated changes already in the working tree and never edit generated files directly.`

// block is one part a step hands the model: the substitution a wording may
// name to place it, and the text itself.
type block struct {
	name string
	text string
}

// stepPrompt assembles one step: the instruction, the blocks it carries, the
// pieces the code puts after them, and the answer shape.
//
// A wording that names a block's substitution places that block itself,
// exactly where it wrote it and with the spacing it wrote around it — a
// substitution mid-sentence is a substitution mid-sentence, and a builder
// that reflowed it into paragraphs would be editing the file. Every block the
// wording did not name is taken after the instruction, in the order the step
// declares them, and `after` — the standards sentence, the commit style, the
// step's tail, the answer shape — comes last, in the order given.
//
// The shape comes from here whatever the wording said: Observe reads a step's
// answer by its marker lines, so a wording that stopped asking for the grade
// would make every research turn look like a block, and a project cannot be
// allowed to edit out the thing that makes a gate a gate.
func stepPrompt(wording string, blocks []block, after ...string) string {
	var rest []string
	for _, b := range blocks {
		text := strings.TrimSpace(b.text)
		if !strings.Contains(wording, b.name) {
			rest = append(rest, text)
			continue
		}
		// A block this run has nothing for takes the blank line it sat on
		// with it, so a step with no answers does not send the hole where
		// they would have been.
		if text == "" {
			wording = strings.ReplaceAll(wording, "\n\n"+b.name+"\n\n", "\n\n")
		}
		wording = strings.ReplaceAll(wording, b.name, text)
	}
	return join(append(append([]string{wording}, rest...), after...)...)
}

// join runs the pieces of a prompt together with a blank line between them
// and leaves out the ones this run has nothing for, so a step with no answers
// and no findings does not send the space where they would be.
func join(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// sprintBlock is the set's goal, for an item being worked as part of one. It
// rides in the run's first step and nowhere else: what the set is for changes
// how the work is scoped, which is a question for the reading, and repeating
// it at every step would only spend tokens restating it.
func sprintBlock(goal string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ""
	}
	return "SPRINT — what the set of items this one belongs to is for:\n" + goal
}

// planBlock is the approved plan under the label the runner gives it. The
// label is the code's and not the wording's: it is how the model is told
// which of the blocks is the plan, and a step that renamed it would be
// describing a block by a name nothing else in the run uses.
func planBlock(plan string) string {
	if strings.TrimSpace(plan) == "" {
		return ""
	}
	return "APPROVED PLAN:\n" + strings.TrimSpace(plan)
}

// readTheChange is how a reading step is told to find what changed. It is a
// block rather than part of the wording because whether there is a history to
// read is a fact about the machine and not a sentence anyone is tuning:
// outside a repository the two git commands return nothing and the model
// spends its turn discovering that, and a project that replaced the review
// wording must not be able to lose the sentence that says so.
func readTheChange(repo bool) string {
	if repo {
		return "Run `git diff` and `git status` and read every changed file in full."
	}
	return "This is not a git repository, so there is no diff to read: read in full every file the approved plan names, and any file it led you to."
}

// diffBlock is the change itself, for the child that has no commands to go
// and read it with. Outside a repository there is no history behind the diff
// either, and a reader that believes there is will report a file as unchanged
// when what it found was no repository to ask.
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

// commitStyle is how a commit finish is told to find the repository's own
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

// promptArgs is one step's prompt as this run has it: the step, the item, and
// whatever of the blocks the run holds.
type promptArgs struct {
	step PipelineStep
	item todo.Item
	// with is what rides in the item block rather than in a block of its own:
	// the set's goal and the reading the person accepted, both of which are
	// what this run states the item to be, so a wording that placed the item
	// gets them where it put it.
	with     []string
	plan     string
	answers  string
	findings string
	diff     string
	repo     bool
	// task builds the reading an agent step hands a child rather than the
	// turn the step takes itself.
	task bool
}

// prompt is one step of this pipeline, assembled.
func (p Pipeline) prompt(a promptArgs, w Wordings, profile todo.Profile) string {
	ps := a.step
	instruction, key := ps.Builtin, ps.Key()
	if a.task {
		instruction, key = ps.TaskBuiltin, ps.TaskKey()
	}
	blocks := make([]block, 0, len(ps.Blocks))
	for _, name := range ps.Blocks {
		blocks = append(blocks, block{name, a.blockText(name)})
	}
	var after []string
	if ps.Standards {
		after = append(after, w.standards(p))
	}
	if ps.Kind == KindFinish && ps.Finish == FinishCommit {
		// The style sentence is not a substitution a wording may place: it is
		// not the change, and a step's own name for it would be a second
		// meaning for `{{diff}}` in a run that already has one.
		after = append(after, commitStyle(a.repo))
	}
	after = append(after, ps.Tail, ps.Shape(profile, a.task))
	return stepPrompt(or(w[key], instruction), blocks, after...)
}

// blockText is what this run has for one substitution.
func (a promptArgs) blockText(name string) string {
	switch name {
	case PlaceholderItem:
		return join(append([]string{itemBlock(a.item)}, a.with...)...)
	case PlaceholderPlan:
		return planBlock(a.plan)
	case PlaceholderAnswers:
		return a.answers
	case PlaceholderFindings:
		return a.findings
	case PlaceholderDiff:
		if a.task {
			return diffBlock(a.diff, a.repo)
		}
		return readTheChange(a.repo)
	}
	return ""
}

// GroomPrompt is the reading of one item against the tree as it stands: every
// claim the item makes graded from a closed set, with one line of evidence
// under each. It is the reading step's own moved to before the run — which is
// where a stale item is worth finding, rather than three steps in on a plan
// built against the wrong file.
//
// The answer is markers rather than prose because what comes of it is a diff
// of single lines the person accepts one by one, and a diff needs a fact per
// line. The verdict words are the backlog's own closed set, so the prompt
// that asks for them and the reader that parses them cannot drift apart.
//
// It carries the built-in standards sentence and takes no wording of its
// own: a grooming pass is not a step of a run — no run ever enters it — and
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
