// Package run is the backlog runner's state machine: the stages an item
// goes through, what each one asks the model for, how its answer is read,
// and which stage comes next. It knows nothing about a terminal or a
// provider. The front-end sends the prompt a stage hands it, gives back the
// text the model produced, and is told what to do next — which is what
// keeps the gates in code rather than in the model's judgement.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/todo"
)

// Stage is where a run is.
type Stage string

const (
	// StageResearch reads the code in plan mode and answers with a plan, a
	// size and any open questions.
	StageResearch Stage = "research"
	// StageSplit divides a large item's plan into lanes, in plan mode.
	StageSplit Stage = "split"
	// StageFanOut is writer children building the lanes; no session turn.
	StageFanOut Stage = "fan-out"
	// StageImplement carries the plan out — the whole plan for a small or
	// medium item, the integration of the lanes for a large one.
	StageImplement Stage = "implement"
	// StageVerify runs the item's tests and the project's checks. No model.
	StageVerify Stage = "verify"
	// StageReview reads the change against the item and the project's
	// conventions and answers clean or with findings.
	StageReview Stage = "review"
	// StageRemediate fixes what verify or review found, then goes back to
	// verify.
	StageRemediate Stage = "remediate"
	// StageCommit writes the commit message and the report.
	StageCommit Stage = "commit"
	// StageDone is a run that archived its item.
	StageDone Stage = "done"
	// StageBlocked is a run that stopped with evidence.
	StageBlocked Stage = "blocked"
)

// Action is what the front-end does next.
type Action int

const (
	// ActionPrompt: send Prompt to the model in Mode and report the text.
	ActionPrompt Action = iota
	// ActionVerify: run the item's tests and the checks; report the outcome.
	ActionVerify
	// ActionCommit: stage the run's paths and commit with Message; then
	// archive the item with Report.
	ActionCommit
	// ActionPause: show State.Plan, State.Questions and the size to the
	// person and wait — Resume, Replan or Block is theirs.
	ActionPause
	// ActionReview: hand Prompt to a reviewer sub-agent named Reviewer and
	// report its final text through ReviewResult.
	ActionReview
	// ActionFanOut: spawn a writer child per State.Lanes entry whose Agent
	// is set, with LaneTask as its task and Paths as its claim; report
	// each patch through LanePatched and each finish through LaneDone.
	ActionFanOut
	// ActionWait: nothing to do until a child reports.
	ActionWait
	// ActionBlocked: the run is over with State.Blocked as the evidence.
	ActionBlocked
	// ActionDone: the run is over; the item is archived.
	ActionDone
)

// String names the action, as a closed set a record can be keyed on.
func (a Action) String() string {
	switch a {
	case ActionPrompt:
		return "prompt"
	case ActionVerify:
		return "verify"
	case ActionCommit:
		return "commit"
	case ActionPause:
		return "pause"
	case ActionReview:
		return "review"
	case ActionFanOut:
		return "fan-out"
	case ActionWait:
		return "wait"
	case ActionBlocked:
		return "blocked"
	case ActionDone:
		return "done"
	}
	return "unknown"
}

// Strip is the run's stages in the order a run passes through them, and the
// order any surface drawing a run has to draw them in. The stages a run only
// sometimes takes are not in it: the split and the fan-out are how a large
// item implements, and a remediation round is the implement stage happening
// again, so each of them is said under the stage it belongs to. A strip whose
// length depended on what happened could not be read at a glance, which is
// the whole of what a strip is for.
func Strip() []Stage {
	return []Stage{StageResearch, StageImplement, StageVerify, StageReview, StageCommit}
}

// Place is where a stage sits in Strip, and -1 for one that sits nowhere in
// it — an ended run, and the stage a checkpoint could not name. The
// sometimes-stages report the strip stage they belong to.
func Place(s Stage) int {
	switch s {
	case StageResearch:
		return 0
	case StageSplit, StageFanOut, StageImplement, StageRemediate:
		return 1
	case StageVerify:
		return 2
	case StageReview:
		return 3
	case StageCommit:
		return 4
	}
	return -1
}

// Mode is the permission mode a prompt is sent in.
type Mode string

const (
	ModePlan Mode = "plan"
	ModeAuto Mode = "auto"
)

// Step is what the front-end is handed on every transition.
type Step struct {
	Action Action
	Stage  Stage
	Mode   Mode
	Prompt string
	// Shown is the one-line label the transcript shows in place of the
	// prompt, so a stage reads as a stage rather than as a wall of text.
	Shown string
}

// Name is the one word for what a step is, and the only vocabulary either
// the record or a surface may use for it: the stage where the step is a turn
// in one, and the action everywhere else. Both readings used to exist — the
// record keyed every model turn on "prompt", which says which of the actions
// was taken and not which of the stages took it — and a row drawn from one
// while the record was written from the other could disagree about the same
// transition.
func (s Step) Name() string {
	if s.Action == ActionPrompt {
		return string(s.Stage)
	}
	return s.Action.String()
}

// Rounds is how many remediation rounds a size gets before the run blocks.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
func Rounds(size todo.Size) int {
	if size == todo.SizeS {
		return 1
	}
	return 2
}

// State is a run's checkpoint: everything the machine needs to continue
// from the start of its current stage. It is written after every
// transition so a session that dies mid-run can say where it was.
type State struct {
	Slug    string    `json:"slug"`
	Session string    `json:"session"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	Stage   Stage     `json:"stage"`
	// Turn is the session turn the run started at; every changeset turn
	// from here on belongs to it.
	Turn int `json:"turn"`
	// PrevMode is the session's mode before the run, restored after.
	PrevMode string `json:"prev_mode"`

	SizeBefore todo.Size `json:"size_before"`
	Size       todo.Size `json:"size"`
	// Plan is the research answer as written; Steps its parsed titles.
	Plan      string   `json:"plan"`
	Steps     []string `json:"steps"`
	Questions []string `json:"questions"`

	// Round counts remediation rounds used.
	Round int `json:"round"`
	// Findings is what the last review or verify turned up, for the
	// remediation prompt and, at the end, for the evidence.
	Findings string `json:"findings"`
	// Verdict is the word the last review answered with, kept because it is
	// the review's whole answer and because the surfaces that draw a run
	// must not have to infer it from which stage came next — an inference
	// that is wrong for a run picked up from a checkpoint, where no surface
	// saw the stage change at all.
	Verdict string `json:"verdict,omitempty"`
	// Verified reports the last verify passed; a review only runs on a
	// verified tree.
	Verified bool `json:"verified"`

	// Paused is why the run is waiting on the person, empty otherwise.
	Paused string `json:"paused"`
	// Reviewer is the name of the review child in flight, empty when the
	// review is the orchestrator's own turn; Reviews counts the children
	// spawned, so a name is never reused — a killed child keeps its name
	// in the supervisor for the session.
	Reviewer string `json:"reviewer"`
	Reviews  int    `json:"reviews"`
	// Lanes is a large item's division into writer children, and Fanouts
	// how many times they were spawned, so a lane's child name is never
	// one the supervisor already holds.
	Lanes   []Lane `json:"lanes,omitempty"`
	Fanouts int    `json:"fanouts,omitempty"`
	// Answers are the notes the person gave at a pause, for the re-plan.
	Answers []string `json:"answers"`

	Message string `json:"message"`
	Report  string `json:"report"`
	Blocked string `json:"blocked"`
	// Files are the paths the run committed, for the report.
	Files []string `json:"files"`
	// Paths are the repository paths the run has changed so far, kept in
	// the checkpoint because a session's own change records die with it
	// and a run continued in a new session must still know what it may
	// stage.
	Paths []string `json:"paths"`
	// Tests are the item's test commands as they stood when the run
	// started — before any model turn could have edited the file. The
	// verify stage runs these and only these.
	Tests []string `json:"tests"`

	// NoCommit is a run the person asked for without one. It ends after
	// the review instead, and its report says the change was not
	// committed, because an archived item beside an uncommitted tree is
	// the one record nothing later can recover from.
	NoCommit bool `json:"no_commit,omitempty"`
	// Repo reports a git repository at the root. Every stage that tells
	// the model to run a git command reads it: outside a repository those
	// commands fail, and a prompt that asks for them anyway spends a turn
	// teaching the model that its instructions are wrong.
	Repo bool `json:"repo,omitempty"`
	// Sprint is the goal of the set this item is being worked as part of,
	// empty when the session has no sprint. It is in the checkpoint rather
	// than read at each stage so a run continued after the sprint file
	// changed still says what the work was started for.
	Sprint string `json:"sprint,omitempty"`
	// CloseGate reports that the workspace names a suite for a turn's
	// close, which for a run means the implement stage checks itself
	// before it hands the tree on.
	CloseGate bool `json:"close_gate,omitempty"`
	// InSprint reports an item the sprint took rather than one a person
	// asked for by name. Nobody is reading a sprint, which is what the
	// surfaces that only sometimes ask for a reader's attention read it for.
	InSprint bool `json:"in_sprint,omitempty"`
	// Checked reports a passing verdict already reached over the tree the
	// implement stage left, which the verify stage takes instead of running
	// the same suite again. It is spent by the verify it was recorded for,
	// and dropped by a run picked up in a later session — a verdict is
	// about the tree it ran over, and a tree left overnight is not that one.
	Checked bool `json:"checked,omitempty"`
}

// Options are the answers the person gave when they asked for the run, as
// opposed to the ones the machine works out for itself.
type Options struct {
	// NoCommit ends the run after the review with the change in the tree.
	NoCommit bool
	// Repo reports a git repository at the root.
	Repo bool
	// Sprint is the goal paragraph of the open sprint, empty without one.
	Sprint string
	// CloseGate reports that the workspace names an on-close suite, so the
	// implement stage's own close runs the checks and the verify stage
	// takes that verdict rather than paying for the same suite twice over
	// a tree that has not moved between them.
	CloseGate bool
	// InSprint reports a run the sprint started rather than one a person
	// asked for.
	InSprint bool
}

// Start begins a run on an item.
func Start(it todo.Item, session, prevMode string, turn int, opt Options) *State {
	now := time.Now()
	return &State{
		Slug: it.Slug, Session: session, Started: now, Updated: now,
		Stage: StageResearch, Turn: turn, PrevMode: prevMode,
		SizeBefore: it.Size, Size: it.Size,
		Tests:     TestCommands(it.Body),
		NoCommit:  opt.NoCommit,
		Repo:      opt.Repo,
		Sprint:    opt.Sprint,
		CloseGate: opt.CloseGate,
		InSprint:  opt.InSprint,
	}
}

// Dir is where checkpoints live.
func Dir(root string) string { return filepath.Join(todo.Dir(root), todo.RunSubdir) }

func path(root, slug string) string { return filepath.Join(Dir(root), slug+".json") }

// Save writes the checkpoint.
func (s *State) Save(root string) error {
	s.Updated = time.Now()
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(root, s.Slug), data, 0o644)
}

// Load reads a checkpoint.
func Load(root, slug string) (*State, error) {
	data, err := os.ReadFile(path(root, slug))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path(root, slug), err)
	}
	return &s, nil
}

// Discard removes a checkpoint; a run that ended has nothing to continue.
func Discard(root, slug string) { _ = os.Remove(path(root, slug)) }

// Continue re-enters the stage a checkpoint was saved at, for a run picked
// up in a new session: the stage starts over — its prompt is sent again,
// its verify re-run — because the transcript that was mid-stage is gone
// and a stage is the smallest unit that can be judged.
func (s *State) Continue(it todo.Item) Step {
	switch s.Stage {
	case StageResearch:
		// A checkpoint saved at the pause re-shows the card: the plan is
		// there and was never answered, so asking research again would
		// only produce it a second time.
		if s.Paused != "" {
			return s.pause(s.Paused)
		}
		return Step{Action: ActionPrompt, Stage: StageResearch, Mode: ModePlan,
			Prompt: researchPrompt(it, s.Sprint, answersBlock(s.Answers)), Shown: s.label("research (continued)")}
	case StageSplit:
		return s.split(it)
	case StageFanOut:
		// The children died with the session; the lanes that landed are
		// in the tree, and the rest are spawned again under new names.
		return s.fanOut()
	case StageImplement:
		if s.AllLanesDone() {
			return s.integrate(it)
		}
		return Step{Action: ActionPrompt, Stage: StageImplement, Mode: ModeAuto,
			Prompt: implementPrompt(it, s.Plan, answersBlock(s.Answers)), Shown: s.label("implement (continued)")}
	case StageRemediate:
		return Step{Action: ActionPrompt, Stage: StageRemediate, Mode: ModeAuto,
			Prompt: remediatePrompt(it, s.Findings), Shown: s.label("remediate (continued)")}
	case StageVerify:
		// A checkpoint from before Tests existed decodes with none; the
		// gate still runs, and "nothing to verify" is said when there is
		// neither. Any verdict the checkpoint carried is dropped: it was
		// reached in another sitting, and what happened to the tree between
		// the two is not something a checkpoint can know.
		s.Checked = false
		return s.verify()
	case StageReview:
		return s.review(it)
	case StageCommit:
		// A run picked up without a commit has no commit turn left to
		// take. The checkpoint was parked before the answer came back, so
		// re-sending the prompt here would produce exactly the commit the
		// person just asked for the run without.
		if s.NoCommit {
			return s.archive()
		}
		s.Message, s.Report = "", ""
		return Step{Action: ActionPrompt, Stage: StageCommit, Mode: ModePlan,
			Prompt: commitPrompt(it, s.Plan, s.Repo), Shown: s.label("commit (continued)")}
	}
	return s.block("the checkpoint names a stage that cannot be continued: " + string(s.Stage))
}

// ClosesWithGate reports whether the turn the run is in the middle of should
// run the workspace's checks as it closes. It is the implement stage and no
// other: that is the one stage that leaves changed code behind for a later
// stage to judge, and the stages after it read the tree without writing to
// it. Research and the split write nothing; review and commit run read-only
// so that nothing can change between the verdict and the commit.
func (s *State) ClosesWithGate() bool {
	return s != nil && s.CloseGate && s.Stage == StageImplement
}

// Sprinting reports a run the sprint is driving, safely on the nil state the
// surfaces asking hold between runs.
func (s *State) Sprinting() bool { return s != nil && s.InSprint }

// Checks records what such a close reached, so the verify stage can take a
// pass instead of running the same suite over a tree that has not moved
// between them. Only a pass carries: a turn shown a failure was given rounds
// to fix what it found, so the tree it finally left is not the one the
// failing verdict was about.
func (s *State) Checks(passed bool) { s.Checked = passed }

// Over reports whether the run has reached an end state.
func (s *State) Over() bool { return s.Stage == StageDone || s.Stage == StageBlocked }

// First is the run's first step: research, in plan mode.
func (s *State) First(it todo.Item, context string) Step {
	s.Stage = StageResearch
	return Step{Action: ActionPrompt, Stage: StageResearch, Mode: ModePlan,
		Prompt: researchPrompt(it, s.Sprint, context), Shown: s.label("research")}
}

// Observe reads the model's answer to the current stage and returns the
// next step. text is the assistant's final message for the turn; empty
// means the turn ended without one — a failure, a cancel — which blocks
// the run, because a stage with no answer cannot be judged.
func (s *State) Observe(it todo.Item, text string) Step {
	text = strings.TrimSpace(text)
	if text == "" {
		return s.block("the " + string(s.Stage) + " turn ended without an answer")
	}
	// Only a stage that changes things may report itself blocked; a review
	// or a commit turn quoting the word is not a block.
	switch s.Stage {
	case StageResearch, StageSplit, StageImplement, StageRemediate:
		if reason, ok := blockedLine(text); ok {
			return s.block("the model reported it is blocked: " + reason)
		}
	}
	switch s.Stage {
	case StageResearch:
		return s.afterResearch(it, text)
	case StageSplit:
		return s.afterSplit(it, text)
	case StageImplement, StageRemediate:
		return s.verify()
	case StageReview:
		return s.afterReview(it, text)
	case StageCommit:
		return s.afterCommit(text)
	}
	return s.block("no stage to observe")
}

// afterResearch reads the plan, the size and the questions, then applies
// the pause gate. A small item never pauses — an open question on one
// blocks, because guessing an answer is the one thing a deterministic
// runner must not do. A medium one pauses when there is a question or the
// research graded it up; a large one always pauses, because that is the
// moment spend and blast radius are decided.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
func (s *State) afterResearch(it todo.Item, text string) Step {
	p := plan.Parse(text)
	s.Plan = text
	s.Steps = nil
	for _, st := range p.Steps {
		s.Steps = append(s.Steps, st.Title)
	}
	if size, ok := sizeLine(text); ok {
		s.Size = size
	}
	s.Questions = questionLines(text)
	if !p.Structured() {
		return s.block("research produced no numbered plan")
	}
	// An item with no grade yet is not upgraded by getting one.
	upgraded := s.SizeBefore != "" && rank(s.Size) > rank(s.SizeBefore)
	switch {
	case s.Size == todo.SizeS && len(s.Questions) > 0:
		return s.block("open questions after research:\n- " + strings.Join(s.Questions, "\n- "))
	case s.Size == todo.SizeL:
		return s.pause("a large item pauses before anything is built")
	case len(s.Questions) > 0:
		return s.pause("research left questions")
	case upgraded:
		return s.pause(fmt.Sprintf("research graded the item %s, up from %s", s.Size, orDash(string(s.SizeBefore))))
	}
	return s.implement(it)
}

func rank(size todo.Size) int {
	switch size {
	case todo.SizeS:
		return 1
	case todo.SizeM:
		return 2
	case todo.SizeL:
		return 3
	}
	return 0
}

func (s *State) pause(why string) Step {
	s.Paused = why
	return Step{Action: ActionPause, Stage: StageResearch, Shown: s.label("paused — " + why)}
}

// implement is the stage after the gate. A large item is divided first
// and built by writer children; anything smaller is built here.
func (s *State) implement(it todo.Item) Step {
	if s.Size == todo.SizeL {
		return s.split(it)
	}
	s.Paused = ""
	s.Stage = StageImplement
	return Step{Action: ActionPrompt, Stage: StageImplement, Mode: ModeAuto,
		Prompt: implementPrompt(it, s.Plan, answersBlock(s.Answers)), Shown: s.label("implement")}
}

// Resume is the person taking the plan as it stands.
func (s *State) Resume(it todo.Item) Step {
	if s.Paused == "" {
		return s.block("nothing to resume")
	}
	return s.implement(it)
}

// Replan is the person answering the questions or steering the plan: the
// note joins the item's record and research runs again with it in front.
func (s *State) Replan(it todo.Item, note string) Step {
	s.Paused = ""
	s.Answers = append(s.Answers, note)
	s.Stage = StageResearch
	return Step{Action: ActionPrompt, Stage: StageResearch, Mode: ModePlan,
		Prompt: researchPrompt(it, s.Sprint, answersBlock(s.Answers)), Shown: s.label("research again")}
}

func answersBlock(answers []string) string {
	if len(answers) == 0 {
		return ""
	}
	return "ANSWERS AND STEERING FROM THE PERSON (honour these; they settle the questions):\n- " + strings.Join(answers, "\n- ")
}

// verify is the step after any change to the tree.
func (s *State) verify() Step {
	s.Stage = StageVerify
	s.Verified = false
	return Step{Action: ActionVerify, Stage: StageVerify, Shown: s.label("verify")}
}

// VerifyResult is the front-end reporting the verify outcome. Failure spends a
// remediation round; passing goes to review.
func (s *State) VerifyResult(it todo.Item, ok bool, output string) Step {
	// The verdict the implement stage's close reached is spent here. The
	// next verify follows a remediation turn, over a tree that moved.
	s.Checked = false
	if ok {
		s.Verified = true
		return s.review(it)
	}
	return s.remediate(it, "Verification failed:\n"+output)
}

// review is the review step. A small item reviews itself in the
// orchestrator's turn; anything larger is read by a reviewer child that
// did not write it, which is what makes the second opinion one. The
// child's task is built by the front-end (ReviewTask), which has the
// change to hand it; the orchestrator's own turn reads the tree itself.
// Review and the commit turn read only: they run in the read-only mode so
// nothing can change between the verify that passed and the commit — an
// edit made while reviewing would land unverified.
func (s *State) review(it todo.Item) Step {
	s.Stage = StageReview
	if s.Size == todo.SizeS {
		s.Reviewer = ""
		return Step{Action: ActionPrompt, Stage: StageReview, Mode: ModePlan,
			Prompt: reviewPrompt(it, s.Plan, s.Repo), Shown: s.label("review")}
	}
	s.Reviews++
	s.Reviewer = fmt.Sprintf("todo-review-%s-%d", s.Slug, s.Reviews)
	return Step{Action: ActionReview, Stage: StageReview, Mode: ModePlan, Shown: s.label("review by " + s.Reviewer)}
}

// ReviewTask is the reviewer child's task: the item, the plan, and the
// change as the front-end read it, since the child has no commands to
// read the change with itself.
func (s *State) ReviewTask(it todo.Item, diff string) string {
	return reviewTask(it, s.Plan, diff, s.Repo)
}

// SelfReview is the fallback when no reviewer child can be had — the
// orchestrator reviews in its own turn, and the step says so.
func (s *State) SelfReview(it todo.Item) Step {
	s.Reviewer = ""
	return Step{Action: ActionPrompt, Stage: StageReview, Mode: ModePlan,
		Prompt: reviewPrompt(it, s.Plan, s.Repo), Shown: s.label("review (no reviewer agent; reviewing in this session)")}
}

// ReviewResult is the reviewer child's final text.
func (s *State) ReviewResult(it todo.Item, text string) Step {
	s.Reviewer = ""
	if strings.TrimSpace(text) == "" {
		return s.block("the reviewer ended without a report")
	}
	return s.afterReview(it, text)
}

// afterReview reads the verdict line.
func (s *State) afterReview(it todo.Item, text string) Step {
	verdict, findings := verdictLine(text)
	s.Verdict = verdict
	switch verdict {
	case "clean":
		if s.NoCommit {
			return s.archive()
		}
		s.Stage = StageCommit
		return Step{Action: ActionPrompt, Stage: StageCommit, Mode: ModePlan,
			Prompt: commitPrompt(it, s.Plan, s.Repo), Shown: s.label("commit")}
	case "findings":
		return s.remediate(it, "Review findings:\n"+findings)
	}
	return s.block("the review ended without a verdict line")
}

// remediate spends a round, or blocks when they are spent.
func (s *State) remediate(it todo.Item, findings string) Step {
	s.Findings = findings
	if s.Round >= Rounds(s.Size) {
		return s.block(fmt.Sprintf("remediation rounds spent (%d):\n%s", s.Round, findings))
	}
	s.Round++
	s.Stage = StageRemediate
	return Step{Action: ActionPrompt, Stage: StageRemediate, Mode: ModeAuto,
		Prompt: remediatePrompt(it, findings), Shown: s.label(fmt.Sprintf("remediate %d/%d", s.Round, Rounds(s.Size)))}
}

// afterCommit reads the commit message and the report.
func (s *State) afterCommit(text string) Step {
	message, report, ok := commitParts(text)
	if !ok {
		return s.block("the commit turn did not produce a message and a report in the asked shape")
	}
	s.Message, s.Report = message, report
	return Step{Action: ActionCommit, Stage: StageCommit, Shown: s.label("commit")}
}

// archive ends a run that was asked for without a commit: verified,
// reviewed, and finished with the change in the working tree. It is an end
// state and not a stage the run skipped — the item is archived and the
// report is written here rather than by a commit turn that has nothing to
// write a commit message for.
//
// Paths is the run's changed set as of the review step, which is current:
// review reads and does not write, so nothing can have changed the tree
// between the step that snapshotted them and this one.
func (s *State) archive() Step {
	s.Files = append([]string(nil), s.Paths...)
	s.Report = notCommittedReport(s.Files)
	s.Stage = StageDone
	return Step{Action: ActionDone, Stage: StageDone, Shown: s.label("done — not committed")}
}

// notCommittedReport is what goes on the archived item when the run made no
// commit. It names the paths because they are the only place the change can
// be found afterwards: with no commit there is no history to read it out of,
// and a report that said "done" without them would send the next reader
// looking for a commit that was never made.
func notCommittedReport(paths []string) string {
	var b strings.Builder
	b.WriteString("## Report\nSummary: the change was verified and reviewed. It was not committed — the run was asked for without one — so the work is in the working tree.\n")
	if len(paths) == 0 {
		b.WriteString("Not committed: the run changed no files.\n")
		return b.String()
	}
	b.WriteString("Not committed, in the working tree:\n")
	for _, p := range paths {
		b.WriteString("- " + p + "\n")
	}
	return b.String()
}

// Committed is the front-end reporting the commit landed; the run is done.
func (s *State) Committed(files []string) Step {
	s.Files = files
	s.Stage = StageDone
	return Step{Action: ActionDone, Stage: StageDone}
}

// Block ends the run with evidence from the front-end — a commit that
// could not be made, a tree with foreign staged changes.
func (s *State) Block(reason string) Step { return s.block(reason) }

func (s *State) block(reason string) Step {
	s.Blocked = reason
	s.Stage = StageBlocked
	return Step{Action: ActionBlocked, Stage: StageBlocked}
}

func (s *State) label(stage string) string {
	return fmt.Sprintf("▸ todo run %s · %s", s.Slug, stage)
}

// Summary is the one-paragraph state for /todo status and the rail.
func (s *State) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s", s.Slug, s.Stage)
	if s.Size != "" {
		fmt.Fprintf(&b, " · size %s", s.Size)
		if s.SizeBefore != s.Size {
			fmt.Fprintf(&b, " (was %s)", orDash(string(s.SizeBefore)))
		}
	}
	if s.NoCommit {
		b.WriteString(" · not committed")
	}
	if s.Round > 0 {
		fmt.Fprintf(&b, " · remediation %d/%d", s.Round, Rounds(s.Size))
	}
	if n := len(s.Lanes); n > 0 {
		done := 0
		for _, l := range s.Lanes {
			if l.Done {
				done++
			}
		}
		fmt.Fprintf(&b, " · lanes %d/%d landed", done, n)
	}
	if len(s.Steps) > 0 {
		fmt.Fprintf(&b, "\nplan: %d steps", len(s.Steps))
	}
	if s.Blocked != "" {
		fmt.Fprintf(&b, "\nblocked: %s", s.Blocked)
	}
	return b.String()
}

// TestCommands reads an item's Tests section: one command per bullet.
func TestCommands(body string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			in = strings.EqualFold(strings.TrimSpace(t[3:]), "tests")
			continue
		}
		if !in || !strings.HasPrefix(t, "- ") {
			continue
		}
		cmd := strings.Trim(strings.TrimSpace(t[2:]), "`")
		if cmd != "" {
			out = append(out, cmd)
		}
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// The answer shapes. Each is one line the runner reads by prefix, so the
// model's prose around it costs nothing.

var (
	// [ \t]* rather than \s* after the colon: \s crosses a newline, and the
	// value has to be on the marker's own line.
	sizePattern     = regexp.MustCompile(`(?im)^[ \t]*size:[ \t]*([SML])\b`)
	blockedPattern  = regexp.MustCompile(`(?im)^[ \t]*blocked:[ \t]*(.+)$`)
	verdictPattern  = regexp.MustCompile(`(?im)^[ \t]*verdict:[ \t]*(clean|findings)\b`)
	questionPattern = regexp.MustCompile(`(?im)^[ \t]*questions:[ \t]*(.*)$`)
)

func sizeLine(text string) (todo.Size, bool) {
	m := sizePattern.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return todo.Size(strings.ToUpper(m[1])), true
}

func blockedLine(text string) (string, bool) {
	m := blockedPattern.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// questionLines reads the `questions:` block: what is on the line, then
// the bulleted lines under it until a blank line or a heading. "none" is
// no question.
func questionLines(text string) []string {
	loc := questionPattern.FindStringSubmatchIndex(text)
	if loc == nil {
		return nil
	}
	var out []string
	if inline := strings.TrimSpace(text[loc[2]:loc[3]]); inline != "" && !strings.EqualFold(inline, "none") {
		out = append(out, inline)
	}
	// The match ends at the marker line's end; the block starts on the
	// next line.
	for _, line := range strings.Split(strings.TrimPrefix(text[loc[1]:], "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			break
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			q := strings.TrimSpace(t[2:])
			if q != "" && !strings.EqualFold(q, "none") {
				out = append(out, q)
			}
			continue
		}
		break
	}
	return out
}

func verdictLine(text string) (verdict, findings string) {
	loc := verdictPattern.FindStringSubmatchIndex(text)
	if loc == nil {
		return "", ""
	}
	verdict = strings.ToLower(text[loc[2]:loc[3]])
	return verdict, strings.TrimSpace(text[loc[1]:])
}

// commitParts reads the commit turn: a `COMMIT:` block and a `REPORT:`
// block, in that order, each running to the next marker or the end.
func commitParts(text string) (message, report string, ok bool) {
	ci := strings.Index(text, "COMMIT:")
	ri := strings.Index(text, "REPORT:")
	if ci < 0 || ri < 0 || ri < ci {
		return "", "", false
	}
	message = strings.TrimSpace(text[ci+len("COMMIT:") : ri])
	report = strings.TrimSpace(text[ri+len("REPORT:"):])
	message = strings.Trim(message, "`\n ")
	message = strings.TrimPrefix(message, "text\n")
	if message == "" || report == "" {
		return "", "", false
	}
	return message, report, true
}
