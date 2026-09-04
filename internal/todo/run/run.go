// Package run is the backlog runner's state machine: the steps an item goes
// through, what each one asks the model for, how its answer is read, and
// which step comes next. It knows nothing about a terminal or a provider.
// The front-end sends the prompt a step hands it, gives back the text the
// model produced, and is told what to do next — which is what keeps the
// gates in code rather than in the model's judgement.
//
// Which steps a run has is the pipeline's (pipeline.go) and what a step is
// made of is the kind's (step.go). This file is what happens between them.
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

// Stage is where a run is: the name of the pipeline step it is in, or one of
// the two ends no pipeline declares.
type Stage string

// The stages the built-in code pipeline passes through. They are named here
// because both drivers and the surfaces that draw a run reach for them by
// name; a profile with steps of its own names them in its pipeline and the
// machine reads them from there.
const (
	// StageResearch reads the code in plan mode and answers with a plan, a
	// grade and any open questions.
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
	// StageGroom is an item read against the tree. It is not a step of a run
	// — it happens before one and no run ever enters it, which is why no
	// pipeline names it — but it is a turn the same machinery spends, and
	// every turn a backlog takes is named here.
	StageGroom Stage = "groom"
)

// Action is what the front-end does next.
type Action int

const (
	// ActionPrompt: send Prompt to the model in Mode and report the text.
	ActionPrompt Action = iota
	// ActionVerify: run the step's command — the item's tests and the
	// project's checks, or the one Command names — and report the outcome.
	ActionVerify
	// ActionCommit: stage the run's paths and commit with Message; then
	// archive the item with Report.
	ActionCommit
	// ActionPause: show State.Plan, State.Questions and the grade to the
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

// Mode is the permission mode a prompt is sent in, which a step's Access
// decides: a step that changes the tree runs in auto, because there is
// nobody to approve each edit for it.
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
	// Command is the command a command step runs, and empty for the step
	// that runs the item's own checks and the workspace's gate.
	Command string
	// Shown is the one-line label the transcript shows in place of the
	// prompt, so a step reads as a step rather than as a wall of text.
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

// State is a run's checkpoint: everything the machine needs to continue
// from the start of its current step. It is written after every
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

	// GradeBefore is the grade the item carried when the run started and
	// Grade the one it is being worked at, which the reading may raise.
	GradeBefore string `json:"grade_before"`
	Grade       string `json:"grade"`
	// Profile is the vocabulary the item is written in, which is where the
	// grade's rank comes from. It is re-stamped from the item at Start and
	// at Continue, the way the session and the mode are, rather than
	// written into the checkpoint: the words on an item are read from the
	// project every time, and a run that carried its own copy could be
	// working an item by a scale the project has since rewritten.
	Profile todo.Profile `json:"-"`
	// Pipeline is the steps this run takes, re-stamped for the same reason
	// the profile is: it is read from the project at every start. What the
	// checkpoint keeps is PipelineAt, the digest of the shape the run began
	// under, so a run picked up after the profile changed is refused rather
	// than carried on with half its steps from one shape and half from
	// another.
	Pipeline   Pipeline `json:"-"`
	PipelineAt string   `json:"pipeline_at,omitempty"`
	// Plan is the reading's answer as written; Steps its parsed titles.
	Plan      string   `json:"plan"`
	Steps     []string `json:"steps"`
	Questions []string `json:"questions"`

	// Round counts remediation rounds used.
	Round int `json:"round"`
	// Findings is what the run carries forward for a later step to read:
	// what the last review or verify turned up, for the remediation prompt
	// and, at the end, for the evidence — or what a gathering turn found,
	// where the pipeline says its answer is the record.
	Findings string `json:"findings"`
	// Verdict is the word the last review answered with, kept because it is
	// the review's whole answer and because the surfaces that draw a run
	// must not have to infer it from which step came next — an inference
	// that is wrong for a run picked up from a checkpoint, where no surface
	// saw the step change at all.
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
	// Answers are the notes the person gave at a gate, for the re-plan.
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
	// command step runs these and only these.
	Tests []string `json:"tests"`

	// NoCommit is a run that does not end in a commit — one the person asked
	// for without one, or one whose profile never had one. It archives with
	// a report that says where the work is, because an archived item beside
	// an uncommitted tree is the one record nothing later can recover from.
	NoCommit bool `json:"no_commit,omitempty"`
	// Repo reports a git repository at the root. Every step prompt that
	// names a git command reads it: outside a repository those commands
	// fail, and a prompt that asks for them anyway spends a turn teaching
	// the model that its instructions are wrong.
	Repo bool `json:"repo,omitempty"`
	// Sprint is the goal of the set this item is being worked as part of,
	// empty when the session has no sprint. It is in the checkpoint rather
	// than read at each step so a run continued after the sprint file
	// changed still says what the work was started for.
	Sprint string `json:"sprint,omitempty"`
	// CloseGate reports that the workspace names a suite for a turn's
	// close, which for a run means the step that writes checks itself
	// before it hands the tree on.
	CloseGate bool `json:"close_gate,omitempty"`
	// InSprint reports an item the sprint took rather than one a person
	// asked for. Nobody is reading a sprint, which is what the surfaces that
	// only sometimes ask for a reader's attention read it for.
	InSprint bool `json:"in_sprint,omitempty"`
	// Groomed is the reading of this item against the tree that the person
	// accepted, as the run's first step states it, and empty where there is
	// none that still stands. It is in the checkpoint for the reason the
	// sprint's goal is: a run continued a day later must state the reading
	// the run was started with rather than whatever is on disk by then.
	Groomed string `json:"groomed,omitempty"`
	// Checked reports a passing verdict already reached over the tree the
	// writing step left, which the command step takes instead of running
	// the same suite again. It is spent by the verify it was recorded for,
	// and dropped by a run picked up in a later session — a verdict is
	// about the tree it ran over, and a tree left overnight is not that one.
	Checked bool `json:"checked,omitempty"`

	// Wordings are the step instructions this run sends. They are not in
	// the checkpoint: they are files on disk, and a run continued a day
	// later is continued by a session that has read them as they now stand.
	// What the checkpoint keeps is WordingsAt, the digest of the set the run
	// started under, so a run picked up after an edit can say the words
	// moved rather than carry on as though they had not.
	Wordings   Wordings `json:"-"`
	WordingsAt string   `json:"wordings_at,omitempty"`
}

// Options are the answers the person gave when they asked for the run, as
// opposed to the ones the machine works out for itself.
type Options struct {
	// NoCommit ends the run with an archive rather than a commit.
	NoCommit bool
	// Repo reports a git repository at the root.
	Repo bool
	// Sprint is the goal paragraph of the open sprint, empty without one.
	Sprint string
	// CloseGate reports that the workspace names an on-close suite, so the
	// writing step's own close runs the checks and the command step after it
	// takes that verdict rather than paying for the same suite twice over a
	// tree that has not moved between them.
	CloseGate bool
	// InSprint reports a run the sprint started rather than one a person
	// asked for.
	InSprint bool
	// Groomed is the accepted reading of the item against the tree, as the
	// run's first step states it. Empty is a run whose item nobody has read
	// that way, or one whose reading the person has since edited past.
	Groomed string
	// Wordings are the step instructions the session read for this
	// checkout, and the empty set is the built-in one.
	Wordings Wordings
	// Pipeline is the steps this run takes, and the empty pipeline is the
	// built-in code one.
	Pipeline Pipeline
	// Notebook reports the session keeping a shared notebook, which is where
	// a write-up is read. A finish that spends a turn asking for one has
	// nowhere to put it without a notebook, so it files what the code can
	// say instead.
	Notebook bool
}

// Steps is the pipeline this run works: the profile's, with a commit finish
// turned into an archive where the person asked for a run without one, and a
// note finish turned into one where there is no notebook for the writing to
// be read in. A run without a commit is not a run with a step missing — it is
// a run that ends another way, and the report it writes says where the work
// is instead.
func (o Options) Steps() Pipeline {
	p := o.Pipeline
	if !p.Stated() {
		p = BuiltinCode()
	}
	if o.NoCommit {
		p = p.Archiving()
	}
	if !o.Notebook {
		p = p.Noteless()
	}
	return p
}

// Start begins a run on an item.
func Start(it todo.Item, session, prevMode string, turn int, opt Options) *State {
	now := time.Now()
	pipeline := opt.Steps()
	return &State{
		Slug: it.Slug, Session: session, Started: now, Updated: now,
		Stage: StageResearch, Turn: turn, PrevMode: prevMode,
		GradeBefore: it.Grade(), Grade: it.Grade(), Profile: it.Profile,
		Tests: TestCommands(it.Body),
		// A run makes no commit where the person asked for none and where
		// the pipeline has none to make; both end the same way and the
		// report says the same thing about where the work is.
		NoCommit:  opt.NoCommit || !pipeline.Commits(),
		Repo:      opt.Repo,
		Sprint:    opt.Sprint,
		CloseGate: opt.CloseGate,
		InSprint:  opt.InSprint,
		Groomed:   opt.Groomed,
		// Each set is carried and its digest recorded, so a run continued
		// after either moved can say so.
		Wordings:   opt.Wordings,
		WordingsAt: opt.Wordings.Digest(),
		Pipeline:   pipeline,
		PipelineAt: pipeline.Digest(),
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

// Shape is the run's pipeline as the surfaces drawing it read it. A state
// that has not been handed one — a checkpoint just decoded, before the
// session re-stamps it — reads as the built-in, because the alternative is a
// row that draws no steps at all for a run that plainly has some.
func (s *State) Shape() Pipeline {
	if s == nil || len(s.Pipeline.Steps) == 0 {
		return BuiltinCode()
	}
	return s.Pipeline
}

// Hold is a run that has an item in flight, as its checkpoint states it.
type Hold struct {
	// Session is the run's session, which is what a refusal names: the
	// person is being told where the other half of the work is, and the
	// session is the only handle they have on it.
	Session string
	// Stage is where that run stopped, empty for an item a sprint has taken
	// and not started.
	Stage Stage
	// Sprint reports the hold coming from the loop's checkpoint rather than
	// the item's own, which is a different thing to end.
	Sprint bool
}

// HeldBy is the run working an item, and false when none is. Whoever changes
// an item under a live run changes what its next step starts from — every
// step prompt states the item as it stands — and the run would go on
// against a file that no longer says what it was started for.
//
// Both checkpoints are read. A sprint records the item it has taken before
// that item's own checkpoint exists, and the gap between the two writes is
// exactly where a second terminal would find the item unheld.
func HeldBy(root, slug string) (Hold, bool) {
	if st, err := Load(root, slug); err == nil && !st.Over() {
		return Hold{Session: st.Session, Stage: st.Stage}, true
	}
	if sp, live := Live(root); live && sp.Current == slug {
		return Hold{Session: sp.Session, Sprint: true}, true
	}
	return Hold{}, false
}

// Continue re-enters the step a checkpoint was saved at, for a run picked up
// in a new session: the step starts over — its prompt is sent again, its
// command re-run — because the transcript that was mid-step is gone and a
// step is the smallest unit that can be judged.
//
// It also answers for the two things that can have moved under a parked run.
// The wordings are files the session re-reads, and a step answered under one
// instruction and its next step sent under another is a run whose halves were
// asked different things: the row is where a reader finds that out. The
// pipeline is worse than that and is refused rather than said, because a run
// whose remaining steps are not the ones its earlier steps were chosen for is
// not one run at all — the plan was made for a shape that no longer exists.
func (s *State) Continue(it todo.Item) Step {
	s.Profile = it.Profile
	if s.PipelineAt != "" && s.Shape().Digest() != s.PipelineAt {
		return s.block("the run's steps have changed since it started, and the work already done was planned for the old ones; `/todo open " + s.Slug + "` puts the item back and a run can start over under the new steps")
	}
	step := s.continueStage(it)
	if now := s.Wordings.Digest(); now != s.WordingsAt {
		s.WordingsAt = now
		step.Shown += " — the stage wordings changed since this run started"
	}
	return step
}

// continueStage is that re-entry, step by step.
func (s *State) continueStage(it todo.Item) Step {
	// A checkpoint saved at a gate re-shows the card: the plan is there and
	// was never answered, so asking the step before it again would only
	// produce it a second time.
	if s.Paused != "" {
		return s.pause(s.Paused)
	}
	ps, ok := s.Shape().At(s.Stage)
	if !ok {
		return s.block("the checkpoint names a stage that cannot be continued: " + string(s.Stage))
	}
	switch ps.Kind {
	case KindTurn:
		// Lanes that landed before the session died are in the tree, and
		// what is left of that step is the turn that makes them fit.
		if ps.Back == "" && ps.Access == Write && s.AllLanesDone() {
			return s.integrate(it, ps)
		}
		return s.resend(it, ps)
	case KindFanOut:
		if ps.Access == Write {
			// The children died with the session; the lanes that landed are
			// in the tree, and the rest are spawned again under new names.
			return s.fanOut(ps)
		}
		return s.split(it, ps)
	case KindCommand:
		// A checkpoint from before Tests existed decodes with none; the
		// gate still runs, and "nothing to verify" is said when there is
		// neither. Any verdict the checkpoint carried is dropped: it was
		// reached in another sitting, and what happened to the tree between
		// the two is not something a checkpoint can know.
		s.Checked = false
		return s.command(ps)
	case KindAgent:
		return s.readBy(it, ps)
	case KindGate:
		return s.gate(it, ps)
	case KindFinish:
		if !ps.Finish.Turns() {
			return s.enter(it, ps)
		}
		// A run picked up without a commit has no commit turn left to take.
		// The checkpoint was parked before the answer came back, so
		// re-sending the prompt here would produce exactly the commit the
		// person just asked for the run without.
		s.Message, s.Report = "", ""
		return s.resend(it, ps)
	}
	return s.block("the checkpoint names a stage that cannot be continued: " + string(s.Stage))
}

// resend is a turn's step sent again, said as the continuation it is.
func (s *State) resend(it todo.Item, ps PipelineStep) Step {
	s.Stage = ps.Stage()
	return Step{Action: ActionPrompt, Stage: ps.Stage(), Mode: ps.Access.Mode(),
		Prompt: s.prompt(it, ps, answersBlock(s.Answers)), Shown: s.label(ps.Name + " (continued)")}
}

// ClosesWithGate reports whether the turn the run is in the middle of should
// run the workspace's checks as it closes. It is the step that writes and no
// other: that is the one step that leaves changed code behind for a later
// step to judge, and the steps after it read the tree without writing to it.
// The reading and the division write nothing; the review and the finish run
// read-only so that nothing can change between the verdict and the commit.
func (s *State) ClosesWithGate() bool {
	if s == nil || !s.CloseGate {
		return false
	}
	ps, ok := s.Shape().At(s.Stage)
	return ok && ps.Kind == KindTurn && ps.Access == Write && ps.Back == ""
}

// StepKind is what the run's current step is made of, and false where the
// checkpoint names a stage the pipeline does not have. It is what a driver
// asks instead of naming stages, so the answers a session gives — whether to
// read a turn that has just ended, which outcome belongs to which step — are
// about the kind of step and not about a word the profile chose.
func (s *State) StepKind() (Kind, bool) {
	ps, ok := s.Shape().At(s.Stage)
	if !ok {
		return "", false
	}
	return ps.Kind, true
}

// AwaitsTurn reports the run standing at a step whose answer is a model
// turn's, which is what a session asks before it reads a turn that has just
// ended as the step's answer.
func (s *State) AwaitsTurn() bool {
	ps, ok := s.Shape().At(s.Stage)
	if !ok {
		return false
	}
	switch ps.Kind {
	case KindTurn, KindAgent:
		return true
	case KindFanOut:
		return ps.Access == Read
	case KindFinish:
		return ps.Finish.Turns()
	}
	return false
}

// AwaitsCommand reports the run standing at a step whose answer is a
// command's exit status: a command step, and the finish that ends the run by
// running one. Both hand back the same outcome, so a surface waiting on one
// asks this rather than asking which kind of step it was.
func (s *State) AwaitsCommand() bool {
	ps, ok := s.Shape().At(s.Stage)
	if !ok {
		return false
	}
	return ps.Kind == KindCommand || (ps.Kind == KindFinish && ps.Finish == FinishCommand)
}

// Remediating reports the run standing at the step that answers a failed
// verdict, which is the one step a round is spent in.
func (s *State) Remediating() bool {
	ps, ok := s.Shape().At(s.Stage)
	return ok && ps.Back != ""
}

// Sprinting reports a run the sprint is driving, safely on the nil state the
// surfaces asking hold between runs.
func (s *State) Sprinting() bool { return s != nil && s.InSprint }

// Checks records what such a close reached, so the command step can take a
// pass instead of running the same suite over a tree that has not moved
// between them. Only a pass carries: a turn shown a failure was given rounds
// to fix what it found, so the tree it finally left is not the one the
// failing verdict was about.
func (s *State) Checks(passed bool) { s.Checked = passed }

// Over reports whether the run has reached an end state.
func (s *State) Over() bool { return s.Stage == StageDone || s.Stage == StageBlocked }

// First is the run's first step.
func (s *State) First(it todo.Item, context string) Step {
	ps, ok := s.Shape().First()
	if !ok {
		return s.block("the profile states no steps for a run")
	}
	s.Stage = ps.Stage()
	if ps.Kind != KindTurn {
		return s.enter(it, ps)
	}
	return Step{Action: ActionPrompt, Stage: ps.Stage(), Mode: ps.Access.Mode(),
		Prompt: s.prompt(it, ps, context), Shown: s.label(ps.Name)}
}

// enter is the run arriving at a step: what the front-end is asked to do
// depends only on the step's kind, which is the whole of what a kind is for.
func (s *State) enter(it todo.Item, ps PipelineStep) Step {
	switch ps.Kind {
	case KindTurn:
		s.Paused = ""
		s.Stage = ps.Stage()
		return Step{Action: ActionPrompt, Stage: ps.Stage(), Mode: ps.Access.Mode(),
			Prompt: s.prompt(it, ps, answersBlock(s.Answers)), Shown: s.label(ps.Name)}
	case KindGate:
		return s.gate(it, ps)
	case KindFanOut:
		if ps.Access == Write {
			return s.fanOut(ps)
		}
		return s.split(it, ps)
	case KindCommand:
		return s.command(ps)
	case KindAgent:
		return s.readBy(it, ps)
	case KindFinish:
		return s.finish(it, ps)
	}
	return s.block("the pipeline names a step this runner has no answer for: " + ps.Name)
}

// advance is the run moving on from a step to whichever one comes next at
// this item's grade.
func (s *State) advance(it todo.Item, from string) Step {
	ps, ok := s.Shape().Next(s.Profile, from, s.rank())
	if !ok {
		return s.block("the run reached the end of its steps with nothing to finish it")
	}
	return s.enter(it, ps)
}

// Observe reads the model's answer to the current step and returns the next
// step. text is the assistant's final message for the turn; empty means the
// turn ended without one — a failure, a cancel — which blocks the run,
// because a step with no answer cannot be judged.
func (s *State) Observe(it todo.Item, text string) Step {
	text = strings.TrimSpace(text)
	if text == "" {
		return s.block("the " + string(s.Stage) + " turn ended without an answer")
	}
	ps, ok := s.Shape().At(s.Stage)
	if !ok {
		return s.block("no stage to observe")
	}
	// Only a turn may report itself blocked; a reading or a finish quoting
	// the word is not a block.
	if ps.Kind == KindTurn || ps.Kind == KindFanOut {
		if reason, ok := blockedLine(text); ok {
			return s.block("the model reported it is blocked: " + reason)
		}
	}
	switch ps.Kind {
	case KindTurn:
		return s.afterTurn(it, ps, text)
	case KindFanOut:
		return s.afterSplit(it, text)
	case KindAgent:
		return s.afterReview(it, text)
	case KindFinish:
		return s.afterFinish(ps, text)
	}
	return s.block("no stage to observe")
}

// afterTurn reads what the step said it takes out of the answer, then moves
// on — back to the step a remediation was entered for, or forward.
func (s *State) afterTurn(it todo.Item, ps PipelineStep, text string) Step {
	if ps.Reads.Has(ReadsGrade) {
		if grade, ok := gradeLine(it.Profile, text); ok {
			s.Grade = grade
		}
	}
	if ps.Reads.Has(ReadsQuestions) {
		s.Questions = questionLines(text)
	}
	if ps.Reads.Has(ReadsFindings) {
		s.Findings = text
	}
	if ps.Reads.Has(ReadsPlan) {
		p := plan.Parse(text)
		s.Plan = text
		s.Steps = nil
		for _, st := range p.Steps {
			s.Steps = append(s.Steps, st.Title)
		}
		if !p.Structured() {
			return s.block(ps.Name + " produced no numbered plan")
		}
	}
	if ps.Back != "" {
		back, ok := s.Shape().Step(ps.Back)
		if !ok {
			return s.block("the " + ps.Name + " step returns to " + ps.Back + ", which is not a step")
		}
		return s.enter(it, back)
	}
	return s.advance(it, ps.Name)
}

// gate is the person's step: whether the run stops here is the rule the
// profile stated at this item's grade, and nothing the model said.
//
// A rule of never is not the same as no gate at all. A step that may not
// pause and left questions ends the run rather than guessing at them, because
// a runner that answers a product decision for the person is writing their
// backlog rather than working it.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
func (s *State) gate(it todo.Item, ps PipelineStep) Step {
	asked := string(s.Stage)
	rank := s.rank()
	// An item with no grade yet is not upgraded by getting one.
	upgraded := s.GradeBefore != "" && rank > s.Profile.GradeRank(s.GradeBefore)
	switch ps.pauseAt(rank, s.Profile.Grades()) {
	case PauseAlways:
		return s.pause(s.alwaysPauses())
	case PauseQuestionsOrUpgraded:
		if len(s.Questions) > 0 {
			return s.pause(asked + " left questions")
		}
		if upgraded {
			return s.pause(fmt.Sprintf("%s graded the item %s, up from %s", asked, s.Grade, orDash(s.GradeBefore)))
		}
	case PauseQuestions:
		if len(s.Questions) > 0 {
			return s.pause(asked + " left questions")
		}
	case PauseNever:
		if len(s.Questions) > 0 {
			return s.block("open questions after " + asked + ":\n- " + strings.Join(s.Questions, "\n- "))
		}
	}
	return s.advance(it, ps.Name)
}

// alwaysPauses is why a gate that always stops at this grade stopped. It
// names the top of the scale as the top of the scale, because that is the
// grade the rule is nearly always written for: the one where spend and blast
// radius are decided.
func (s *State) alwaysPauses() string {
	if s.largest() {
		return "a run pauses at the largest grade before anything is built"
	}
	return fmt.Sprintf("a run pauses at %s %s before anything is built", s.Profile.Grade, orDash(s.Grade))
}

// rank is where this run's grade sits on the profile's scale, and zero for
// an ungraded item.
func (s *State) rank() int { return s.Profile.GradeRank(s.Grade) }

// largest reports the item graded at the top of the scale — the grade that
// buys a division into lanes and a gate before anything is built.
func (s *State) largest() bool {
	rank := s.rank()
	return rank > 0 && rank == s.Profile.Grades()
}

// Rounds is how many remediation rounds this run gets, which is what the
// pipeline's remediation step spends at this item's grade. A pipeline with
// no remediation step spends none: a failed verdict there ends the run.
func (s *State) Rounds() int {
	ps, ok := s.Shape().Remediation()
	if !ok {
		return 0
	}
	return ps.roundsAt(s.rank(), s.Profile.Grades())
}

func (s *State) pause(why string) Step {
	s.Paused = why
	return Step{Action: ActionPause, Stage: s.Stage, Shown: s.label("paused — " + why)}
}

// Resume is the person taking the plan as it stands: the run goes on from
// the gate it stopped at.
func (s *State) Resume(it todo.Item) Step {
	if s.Paused == "" {
		return s.block("nothing to resume")
	}
	s.Paused = ""
	from := string(s.Stage)
	if gate, ok := s.gateAfter(); ok {
		from = gate.Name
	}
	return s.advance(it, from)
}

// gateAfter is the gate the run is stopped at: the step after the one whose
// answer it is about.
func (s *State) gateAfter() (PipelineStep, bool) {
	ps, ok := s.Shape().Next(s.Profile, string(s.Stage), s.rank())
	if !ok || ps.Kind != KindGate {
		return PipelineStep{}, false
	}
	return ps, true
}

// Replan is the person answering the questions or steering the plan: the
// note joins the item's record and the step that asked runs again with it in
// front.
func (s *State) Replan(it todo.Item, note string) Step {
	s.Paused = ""
	s.Answers = append(s.Answers, note)
	ps, ok := s.Shape().At(s.Stage)
	if !ok {
		return s.block("the run is at a stage the pipeline no longer has: " + string(s.Stage))
	}
	return Step{Action: ActionPrompt, Stage: ps.Stage(), Mode: ps.Access.Mode(),
		Prompt: s.prompt(it, ps, answersBlock(s.Answers)), Shown: s.label(ps.Name + " again")}
}

func answersBlock(answers []string) string {
	if len(answers) == 0 {
		return ""
	}
	return "ANSWERS AND STEERING FROM THE PERSON (honour these; they settle the questions):\n- " + strings.Join(answers, "\n- ")
}

// prompt is one step's prompt as this run stands. answers is the steering
// block, which the run's first step takes as whatever context it was started
// with instead.
func (s *State) prompt(it todo.Item, ps PipelineStep, answers string) string {
	a := promptArgs{step: ps, item: it, repo: s.Repo,
		plan: s.Plan, answers: answers, findings: s.Findings}
	// The set's goal and the reading the person accepted ride with the item
	// on the step that plans the work, and nowhere else: what the set is for
	// changes how the work is scoped, and repeating it at every step would
	// only spend tokens restating it.
	if first, ok := s.Shape().First(); ok && first.Name == ps.Name {
		a.with = []string{sprintBlock(s.Sprint), s.Groomed}
	}
	return s.Shape().prompt(a, s.Wordings, s.Profile)
}

// command is a step with no model in it: the item's own checks and the
// project's, or the command the step names. The exit status is the verdict.
func (s *State) command(ps PipelineStep) Step {
	s.Stage = ps.Stage()
	s.Verified = false
	return Step{Action: ActionVerify, Stage: ps.Stage(), Command: ps.Command, Shown: s.label(ps.Name)}
}

// VerifyResult is the front-end reporting a command step's outcome. Failure
// spends a remediation round; passing moves on.
func (s *State) VerifyResult(it todo.Item, ok bool, output string) Step {
	// The verdict the writing step's close reached is spent here. The next
	// run of the checks follows a remediation turn, over a tree that moved.
	s.Checked = false
	if !ok {
		return s.remediate(it, "Verification failed:\n"+output)
	}
	s.Verified = true
	ps, known := s.Shape().At(s.Stage)
	if !known {
		return s.block("the run is at a stage the pipeline no longer has: " + string(s.Stage))
	}
	if ps.Kind == KindFinish {
		// A command that ends the run has passed, and there is nothing after
		// it to spend a turn on.
		return s.archive()
	}
	return s.advance(it, ps.Name)
}

// readBy is the reading step. The grade the profile names reads its own work
// in the orchestrator's turn; anything else is read by a child that did not
// write it, which is what makes the second opinion one. The child's task is
// built by the front-end (ReviewTask), which has the change to hand it; the
// orchestrator's own turn reads the tree itself.
//
// A reading runs read-only so nothing can change between the verify that
// passed and the finish — an edit made while reviewing would land unverified.
func (s *State) readBy(it todo.Item, ps PipelineStep) Step {
	s.Stage = ps.Stage()
	if ps.Solo > 0 && s.rank() == ps.Solo {
		s.Reviewer = ""
		return Step{Action: ActionPrompt, Stage: ps.Stage(), Mode: ps.Access.Mode(),
			Prompt: s.prompt(it, ps, ""), Shown: s.label(ps.Name)}
	}
	s.Reviews++
	s.Reviewer = fmt.Sprintf("todo-review-%s-%d", s.Slug, s.Reviews)
	return Step{Action: ActionReview, Stage: ps.Stage(), Mode: ps.Access.Mode(),
		Shown: s.label(ps.Name + " by " + s.Reviewer)}
}

// readingStep is the step a reading is taken at.
func (s *State) readingStep() (PipelineStep, bool) {
	if ps, ok := s.Shape().At(s.Stage); ok && ps.Kind == KindAgent {
		return ps, true
	}
	for _, ps := range s.Shape().Steps {
		if ps.Kind == KindAgent {
			return ps, true
		}
	}
	return PipelineStep{}, false
}

// ReviewTask is the reader child's task: the item, the plan, and the change
// as the front-end read it, since the child has no commands to read the
// change with itself — and what the run gathered, for a pipeline whose
// reading is of a turn's findings rather than of a change to the tree.
func (s *State) ReviewTask(it todo.Item, diff string) string {
	ps, ok := s.readingStep()
	if !ok {
		return ""
	}
	return s.Shape().prompt(promptArgs{step: ps, item: it, repo: s.Repo,
		plan: s.Plan, diff: diff, findings: s.Findings, task: true}, s.Wordings, s.Profile)
}

// SelfReview is the fallback when no child can be had — the orchestrator
// reads in its own turn, and the step says so.
func (s *State) SelfReview(it todo.Item) Step {
	ps, ok := s.readingStep()
	if !ok {
		return s.block("the pipeline has no reading step")
	}
	s.Stage = ps.Stage()
	s.Reviewer = ""
	return Step{Action: ActionPrompt, Stage: ps.Stage(), Mode: ps.Access.Mode(),
		Prompt: s.prompt(it, ps, ""), Shown: s.label(ps.Name + " (no reviewer agent; reviewing in this session)")}
}

// ReviewResult is the reader child's final text.
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
		return s.advance(it, string(s.Stage))
	case "findings":
		return s.remediate(it, "Review findings:\n"+findings)
	}
	return s.block("the review ended without a verdict line")
}

// remediate spends a round, or blocks when they are spent — and blocks at
// once in a pipeline with no step that answers a failed verdict, since there
// is nowhere for the failure to go.
func (s *State) remediate(it todo.Item, findings string) Step {
	s.Findings = findings
	ps, ok := s.Shape().Remediation()
	if !ok {
		return s.block("nothing in this run answers a failed verdict:\n" + findings)
	}
	if s.Round >= s.Rounds() {
		return s.block(fmt.Sprintf("remediation rounds spent (%d):\n%s", s.Round, findings))
	}
	s.Round++
	s.Stage = ps.Stage()
	return Step{Action: ActionPrompt, Stage: ps.Stage(), Mode: ps.Access.Mode(),
		Prompt: s.prompt(it, ps, answersBlock(s.Answers)),
		Shown:  s.label(fmt.Sprintf("%s %d/%d", ps.Name, s.Round, s.Rounds()))}
}

// finish is the run's end, in whichever of the ways the step names.
func (s *State) finish(it todo.Item, ps PipelineStep) Step {
	s.Stage = ps.Stage()
	switch ps.Finish {
	case FinishCommit, FinishNote:
		s.Message, s.Report = "", ""
		return Step{Action: ActionPrompt, Stage: ps.Stage(), Mode: ps.Access.Mode(),
			Prompt: s.prompt(it, ps, ""), Shown: s.label(ps.Name)}
	case FinishCommand:
		return s.command(ps)
	}
	return s.archive()
}

// afterFinish reads what a finish turn answered: a commit message and a
// report, or a report alone.
func (s *State) afterFinish(ps PipelineStep, text string) Step {
	if ps.Finish == FinishCommit {
		message, report, ok := commitParts(text)
		if !ok {
			return s.block("the commit turn did not produce a message and a report in the asked shape")
		}
		s.Message, s.Report = message, report
		return Step{Action: ActionCommit, Stage: ps.Stage(), Shown: s.label(ps.Name)}
	}
	report, ok := reportPart(text)
	if !ok {
		return s.block("the " + ps.Name + " turn did not produce a report in the asked shape")
	}
	s.Report = report
	return s.archive()
}

// archive ends a run that makes no commit: verified, read, and finished with
// the change in the working tree. It is an end state and not a step the run
// skipped — the item is archived and the report is written here rather than
// by a turn that has nothing to write a commit message for.
//
// Paths is the run's changed set as of the reading step, which is current:
// a reading reads and does not write, so nothing can have changed the tree
// between the step that snapshotted them and this one.
func (s *State) archive() Step {
	s.Files = append([]string(nil), s.Paths...)
	if strings.TrimSpace(s.Report) == "" {
		s.Report = notCommittedReport(s.Files)
	}
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

// CutAtCeiling is the evidence a step blocks with when its answer stopped at
// the model's output ceiling and the one continuation a step gets had already
// been spent. A step gets one because a step free to ask for one more
// paragraph every time it filled a budget would be under no ceiling at all;
// it gets no more than one because grading half a review or half an
// implementation is the mistake the gates exist to prevent.
//
// It is a function rather than a method because both drivers reach the same
// reading by different routes — the session watches its own turn end, the
// unattended runner reads what a step's process reported — and the item
// should not be able to tell which of them stopped it.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
func CutAtCeiling(stage Stage) string {
	return fmt.Sprintf("the %s answer was cut at the model's output ceiling twice; it was continued once and stopped short again, and half an answer is not the stage's answer", stage)
}

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
	if s.Grade != "" {
		fmt.Fprintf(&b, " · %s %s", s.Profile.Grade, s.Grade)
		if s.GradeBefore != s.Grade {
			fmt.Fprintf(&b, " (was %s)", orDash(s.GradeBefore))
		}
	}
	if s.NoCommit {
		b.WriteString(" · not committed")
	}
	if s.Round > 0 {
		fmt.Fprintf(&b, " · remediation %d/%d", s.Round, s.Rounds())
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
	blockedPattern  = regexp.MustCompile(`(?im)^[ \t]*blocked:[ \t]*(.+)$`)
	verdictPattern  = regexp.MustCompile(`(?im)^[ \t]*verdict:[ \t]*(clean|findings)\b`)
	questionPattern = regexp.MustCompile(`(?im)^[ \t]*questions:[ \t]*(.*)$`)
)

// gradeLine is the grade a step answered with: the first line whose key is
// the profile's grading field and whose first word is one of that field's
// own. A line naming a word off the scale is passed over rather than taken,
// because a grade the profile cannot rank is one the run cannot spend
// against — and the line below it may well be the right one.
func gradeLine(p todo.Profile, text string) (string, bool) {
	f, ok := p.GradeField()
	if !ok {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(strings.TrimSpace(key), f.Name) {
			continue
		}
		word, _, _ := strings.Cut(strings.TrimSpace(value), " ")
		if grade, ok := f.Canonical(word); ok {
			return grade, true
		}
	}
	return "", false
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

// commitParts reads a commit finish's turn: a `COMMIT:` block and a
// `REPORT:` block, in that order, each running to the next marker or the end.
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

// reportPart reads a finish that asks for the report alone.
func reportPart(text string) (string, bool) {
	ri := strings.Index(text, "REPORT:")
	if ri < 0 {
		return "", false
	}
	report := strings.TrimSpace(text[ri+len("REPORT:"):])
	return report, report != ""
}
