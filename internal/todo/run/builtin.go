package run

// The built-in pipeline and the stage instructions it carries, named so a
// surface outside this package can write one to a file or compare one
// against a file that was written.
//
// They are accessors rather than exported constants because what a stage is
// told is this package's to shape: the blocks it places, the answer shape it
// appends, the substitutions it reads back. A caller gets the wording as it
// stands and nothing to assign to.
//
// A scaffold writes these verbatim, which is what makes editing a stage
// prompt an edit rather than a search through Go for the text and a key to
// point at it. The same texts are what a wording is compared against to
// decide whether it replaced anything at all: a file holding exactly the
// built-in asks the model exactly what the built-in asks, and a record that
// split the runs either side of it would report a change nobody made.
// See docs/capabilities/todo.md#the-stage-prompts-are-yours-to-edit.

// The built-in wordings name their own substitutions, so the blocks sit
// where the stages have always had them: what the model is being shown
// before the instructions that talk about it. A file that names none gets
// them after its instruction instead, which is the only order a builder can
// choose for prose it did not write.
const builtinResearch = `You are working one backlog item through to a commit, in stages. This is the RESEARCH stage: read, do not change anything.

` + PlaceholderItem + `

` + PlaceholderAnswers

// The built-in instructions say nothing about where a block sits, beyond
// what the code places for itself. A wording is put before the blocks it
// carries and a file may move any of them anyway, so an instruction that
// said "the item above" would be wrong the first time somebody wrote one.
const builtinImplement = `IMPLEMENT stage. Carry out the approved plan and satisfy the item's acceptance criteria.

` + PlaceholderItem + `

` + PlaceholderPlan + `

` + PlaceholderAnswers + `

Touch only what the plan names plus what it must to work. Write the tests the item lists where they do not exist. As you satisfy each acceptance criterion and finish each task, tick its checkbox in the item's own file — the one the item block names, which is the record.`

const builtinReview = `REVIEW stage. Verification passed. Now read the change as a reviewer who did not write it.

` + PlaceholderItem + `

` + PlaceholderPlan + `

` + PlaceholderDiff + ` Check, in this order: bugs — concrete inputs that produce a wrong result; acceptance criteria not actually met; behaviour the item did not ask for; the project's conventions from AGENTS.md broken; tests missing for a case the criteria name. Do not change anything in this stage.`

const builtinReviewTask = `Review this change against the backlog item it implements. You did not write it.

` + PlaceholderItem + `

` + PlaceholderPlan + `

` + PlaceholderDiff + `

Read every file the diff touches in full, then the tests that cover them. Check, in this order: bugs — concrete inputs that produce a wrong result; acceptance criteria not actually met; behaviour the item did not ask for; the project's conventions from AGENTS.md broken; tests missing for a case the criteria name.`

const builtinRemediate = `REMEDIATE stage. Fix exactly what the findings list, and nothing else.

` + PlaceholderItem + `

` + PlaceholderFindings

const builtinCommit = `COMMIT stage. The change is verified and reviewed. Do not change any file now.

` + PlaceholderItem

// The tails: the sentences that are about how the run works rather than
// about what a step is for. They are appended whatever the wording said,
// because a project that replaced the implement instruction must not be able
// to leave the model believing it should make the commit itself.
const (
	implementTail = "Do not commit; the runner commits. Do not run the whole verification suite yourself; the runner runs it next."
	remediateTail = "Do not commit. If a finding is wrong, say so and why rather than changing working code."
)

// BuiltinCode is today's machine as a pipeline: read the item and plan the
// change, gate on what the reading found, build it — in lanes where the work
// is graded largest — verify, review, remediate what either turned up, and
// commit.
//
// It is the pipeline a checkout of code has always run and the only one this
// release ships. Everything about it that used to be a branch in the state
// machine is a field here, which is what lets a backlog of readings state a
// run with no verify and no commit without any of the mechanism moving.
func BuiltinCode() Pipeline {
	return Pipeline{
		Name:      "code",
		Standards: builtinStandards,
		Steps: []PipelineStep{{
			Name: "research", Kind: KindTurn, Access: Read,
			Builtin:   builtinResearch,
			Blocks:    []string{PlaceholderItem, PlaceholderAnswers},
			Standards: true,
			Reads:     ReadsPlan | ReadsGrade | ReadsQuestions,
		}, {
			// A small item never pauses, and an open question on one ends the
			// run rather than being guessed at: a runner that answers a
			// product decision for the person is writing their backlog rather
			// than working it. A medium one pauses on a question or on a
			// grade the reading raised. The largest always pauses, because
			// that is the moment spend and blast radius are decided.
			Name: "pause", Kind: KindGate, Under: "research",
			Pause: []PauseRule{PauseNever, PauseQuestionsOrUpgraded, PauseAlways},
		}, {
			Name: "split", Kind: KindFanOut, Access: Read,
			Under: "implement", When: WhenLargest,
		}, {
			Name: "fan-out", Kind: KindFanOut, Access: Write,
			Under: "implement", When: WhenLargest,
		}, {
			Name: "implement", Kind: KindTurn, Access: Write,
			Builtin:   builtinImplement,
			Tail:      implementTail,
			Blocks:    []string{PlaceholderItem, PlaceholderPlan, PlaceholderAnswers},
			Standards: true,
		}, {
			Name: "verify", Kind: KindCommand,
		}, {
			// The smallest grade reads its own work in the session's own
			// turn; anything larger is read by a child that did not write it,
			// because a second opinion is only one if it comes from somewhere
			// else.
			Name: "review", Kind: KindAgent, Access: Read,
			Builtin:     builtinReview,
			TaskBuiltin: builtinReviewTask,
			Blocks:      []string{PlaceholderItem, PlaceholderPlan, PlaceholderDiff},
			Solo:        1,
		}, {
			Name: "remediate", Kind: KindTurn, Access: Write,
			Builtin:   builtinRemediate,
			Tail:      remediateTail,
			Blocks:    []string{PlaceholderItem, PlaceholderFindings},
			Standards: true,
			Under:     "implement",
			Back:      "verify",
			Rounds:    []int{1, 2, 2},
		}, {
			Name: "commit", Kind: KindFinish, Access: Read, Finish: FinishCommit,
			Builtin: builtinCommit,
			Blocks:  []string{PlaceholderItem},
		}},
	}
}

// BuiltinWordings is the built-in set, keyed the way a run reads it. It is
// built from the pipeline rather than written out beside it so that a step
// added to a profile is a step whose built-in words come from one place —
// and so that a caller walking the set cannot pair a step with another
// step's text.
func BuiltinWordings() Wordings { return BuiltinCode().Builtins() }

// Builtins is this pipeline's own words, by key.
func (p Pipeline) Builtins() Wordings {
	if len(p.Steps) == 0 {
		return Wordings{}
	}
	w := Wordings{WordingStandards: p.standards()}
	for _, ps := range p.Steps {
		if ps.Builtin != "" {
			w[ps.Key()] = ps.Builtin
		}
		if ps.Kind == KindAgent && ps.TaskBuiltin != "" {
			w[ps.TaskKey()] = ps.TaskBuiltin
		}
	}
	return w
}
