package run

// The pipeline: which steps a run takes, in what order, and what each of them
// is made of.
//
// The state machine used to hold the order as a switch and the strip as a
// literal, which made "research, implement, verify, review, commit" a fact
// about the program rather than about the work. A backlog of readings has no
// verify and no commit; a checklist has no run at all. The mechanism the
// machine protects — the tail is appended by code, the answer is read by
// code, the person's gate is a step — does not depend on the steps being
// those five, so the order comes out and the mechanism stays in.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/todo"
)

// Pipeline is a run, as the profile states it: an ordered list of steps.
type Pipeline struct {
	// Name is the pipeline's own, which is what a refusal made in its terms
	// says it came from.
	Name  string
	Steps []PipelineStep
	// Standards is the shared sentence every step that changes the tree
	// carries, and empty for a pipeline that keeps the built-in one. It is
	// the pipeline's rather than a constant because a profile ships its own
	// wordings and this is one of them: a backlog of readings has different
	// standards to a checkout of code, and a run that sent the code one
	// anyway would be telling the model to read AGENTS.md about work that
	// has no tree.
	Standards string
}

// standards is the shared sentence this pipeline sends where no file
// replaced it.
func (p Pipeline) standards() string { return or(p.Standards, builtinStandards) }

// PipelineStep is one step of a run.
type PipelineStep struct {
	// Name is the step's word, which is the vocabulary the record keys a
	// transition on and the word a surface draws it with (Step.Name).
	Name string
	// Kind is what the step is made of, from the closed set in step.go.
	Kind Kind
	// Access is the step's mode: whether its turn may change the tree.
	Access Access
	// Wording is the key the step's instruction is read from, and empty for
	// a step whose key is its own name. The keys a session loads are these
	// with `todo_` in front (internal/cli/prompts.go).
	Wording string
	// Builtin is the instruction this step carries where nothing replaced
	// it, TaskBuiltin the one an agent step's child is given, and Tail the
	// prose the code appends after either whatever the wording said — the
	// sentences that are about how the run works rather than about what the
	// step is for. The answer shape comes after all of it and is built from
	// the kind (PipelineStep.Shape).
	Builtin     string
	TaskBuiltin string
	Tail        string
	// Blocks are the substitutions this step's wording may place, in the
	// order the built-in instruction has them. A block a wording does not
	// name is taken after the instruction; one the step does not declare is
	// refused when the file is read.
	Blocks []string
	// Standards reports the step carrying the shared standards sentence —
	// the stages that change the tree, and the one that plans the change.
	Standards bool
	// Reads is what the step takes out of its answer beyond its kind's own.
	Reads Reads
	// Pause is the gate's rule at each grade on the profile's scale, in that
	// order. A scale shorter than the list is read from its start; a grade
	// past its end, and an item with no grade at all, take the last rule.
	Pause []PauseRule
	// Rounds is how many remediation rounds this step spends at each grade,
	// read the same way. It is set on the step a failed verdict is answered
	// by, which is the one step Back names something from.
	Rounds []int
	// Back is the earlier step a failed verdict returns to once this step
	// has answered it. It is what makes a step the pipeline's remediation:
	// exactly one step may name it, and a pipeline with none blocks on a
	// failed verdict rather than looping.
	Back string
	// Under is the step this one is said under on the strip, and empty for a
	// step with a place of its own. The strip is the steps a run always
	// passes through; a division into lanes and a remediation round are how
	// a step happens, not steps beside it, and a strip whose length depended
	// on what happened could not be read at a glance.
	Under string
	// When is the grade this step applies at, and empty for one that always
	// does.
	When When
	// Solo is the grade whose rank reads its own work rather than handing it
	// to a child, for an agent step. Zero hands every grade to a child.
	Solo int
	// Persona is the agent profile an agent step's child reads as, and
	// empty for a step whose reader is whichever role the surface would
	// spawn anyway. A persona is an agent profile like any other, so naming
	// one where a role would go is the whole of it: a session that has a
	// profile by that name spawns that one, and a session that does not
	// falls back to the role, which is what a coding session does.
	// See docs/capabilities/chat.md#colleagues-not-workers.
	Persona string
	// Finish is how a finish step ends the run.
	Finish Finish
	// Command is the command a command step runs, and empty for the step
	// that runs the item's own checks and the workspace's gate.
	Command string
}

// Key is the wording key this step's instruction is read from.
func (ps PipelineStep) Key() string {
	if ps.Wording != "" {
		return ps.Wording
	}
	return ps.Name
}

// TaskKey is the key for an agent step's child task, which is a different
// instruction to the same end: the child has no commands to read the change
// with, so it is handed the change instead of told to produce one.
func (ps PipelineStep) TaskKey() string { return ps.Key() + "_task" }

// Stage is the step's name as the machine's own stage word.
func (ps PipelineStep) Stage() Stage { return Stage(ps.Name) }

// Applies reports the step being taken at this grade's rank on the scale.
func (ps PipelineStep) Applies(p todo.Profile, rank int) bool {
	switch ps.When {
	case WhenLargest:
		return rank > 0 && rank == p.Grades()
	}
	return true
}

// pauseAt is the gate's rule at a rank, and PauseNever for a step that
// declares none.
func (ps PipelineStep) pauseAt(rank, grades int) PauseRule {
	if len(ps.Pause) == 0 {
		return PauseNever
	}
	return ps.Pause[scaleIndex(rank, grades, len(ps.Pause))]
}

// roundsAt is how many remediation rounds a rank buys.
func (ps PipelineStep) roundsAt(rank, grades int) int {
	if len(ps.Rounds) == 0 {
		return 0
	}
	return ps.Rounds[scaleIndex(rank, grades, len(ps.Rounds))]
}

// scaleIndex is where a grade reads from a list stated by grade. The list is
// written smallest first for a scale of its own length, and a profile's scale
// may be shorter or longer, so it is read from both ends: the smallest grade
// takes the first entry, the largest takes the last, and everything between
// takes the entries between. That is what lets one pipeline gate a backlog
// graded quick · deep exactly as it gates one graded S · M · L, with the
// runner knowing none of those words.
//
// An item nobody graded reads from the middle: it is not the smallest work,
// since nobody said it was, and it is not the largest either.
func scaleIndex(rank, grades, n int) int {
	switch {
	case n <= 1:
		return 0
	case rank == 1:
		return 0
	case grades > 0 && rank >= grades:
		return n - 1
	}
	lo, hi := 1, n-2
	if hi < lo {
		return n - 1
	}
	i := rank - 1
	if i < lo {
		i = lo
	}
	if i > hi {
		i = hi
	}
	return i
}

// Step is the step with that name.
func (p Pipeline) Step(name string) (PipelineStep, bool) {
	for _, ps := range p.Steps {
		if ps.Name == name {
			return ps, true
		}
	}
	return PipelineStep{}, false
}

// At is the step a stage is in, which is how the machine reads its own
// checkpoint back.
func (p Pipeline) At(stage Stage) (PipelineStep, bool) { return p.Step(string(stage)) }

// First is the step a run starts at.
func (p Pipeline) First() (PipelineStep, bool) {
	if len(p.Steps) == 0 {
		return PipelineStep{}, false
	}
	return p.Steps[0], true
}

// Next is the step after this one that applies at the rank, and false at the
// end of the pipeline. Steps that do not apply are passed over rather than
// entered and skipped, so nothing records a stage the run did not take.
//
// The remediation step is passed over too, always: it is entered from a
// failed verdict and from nowhere else, so a run whose review came back clean
// walks straight past it to the finish.
func (p Pipeline) Next(profile todo.Profile, from string, rank int) (PipelineStep, bool) {
	at := -1
	for i, ps := range p.Steps {
		if ps.Name == from {
			at = i
			break
		}
	}
	for i := at + 1; i < len(p.Steps); i++ {
		if p.Steps[i].Back == "" && p.Steps[i].Applies(profile, rank) {
			return p.Steps[i], true
		}
	}
	return PipelineStep{}, false
}

// Integrate is the turn that makes a division's lanes fit: the first step
// after it that writes. The validator refuses a pipeline without one, so a
// division always has somewhere to be put back together.
func (p Pipeline) Integrate(after string) (PipelineStep, bool) {
	at := -1
	for i, ps := range p.Steps {
		if ps.Name == after {
			at = i
			break
		}
	}
	for i := at + 1; i < len(p.Steps); i++ {
		if p.Steps[i].Kind == KindTurn && p.Steps[i].Access == Write && p.Steps[i].Back == "" {
			return p.Steps[i], true
		}
	}
	return PipelineStep{}, false
}

// Writes reports the pipeline changing the tree at all. It is what decides
// whether there is a change for a reading to be about and whether a commit
// would have anything to carry: a run of readings leaves the tree where it
// found it, and a surface that asked what it changed would get the empty
// answer and read it as a run that did nothing.
func (p Pipeline) Writes() bool {
	for _, ps := range p.Steps {
		if ps.Access == Write || ps.Kind == KindFanOut {
			return true
		}
	}
	return false
}

// Ending is how this pipeline ends: the first finish step, which is the one
// a run walking the steps in order reaches. It is false for a pipeline with
// no finish at all, which the validator refuses, so only one nobody
// validated has none.
func (p Pipeline) Ending() (Finish, bool) {
	for _, ps := range p.Steps {
		if ps.Kind == KindFinish {
			return ps.Finish, true
		}
	}
	return "", false
}

// Commits reports the pipeline ending in a commit, which is what decides
// whether a run needs a repository before it spends a turn.
func (p Pipeline) Commits() bool {
	for _, ps := range p.Steps {
		if ps.Kind == KindFinish && ps.Finish.Writes() {
			return true
		}
	}
	return false
}

// Remediation is the step a failed verdict is answered by — the one step
// that names something to go Back to — and false for a pipeline that has
// none, where a failure ends the run.
func (p Pipeline) Remediation() (PipelineStep, bool) {
	for _, ps := range p.Steps {
		if ps.Back != "" {
			return ps, true
		}
	}
	return PipelineStep{}, false
}

// Stated reports a pipeline somebody wrote down, which is not the same as
// one with steps in it: a profile may state a run of no steps, and a caller
// that named no pipeline at all has simply not said and takes the built-in
// one (Options.Steps).
func (p Pipeline) Stated() bool { return p.Name != "" || len(p.Steps) > 0 }

// Runs reports the pipeline having any steps at all. A profile may state no
// run — a checklist is a list of things to do rather than a thing to work,
// and a model turn spent on one would describe the work instead of doing it
// — and a surface asked for a run says so rather than sending the item into
// a run its own backlog never stated.
// See docs/capabilities/todo.md#a-profile-says-what-the-work-is.
func (p Pipeline) Runs() bool { return len(p.Steps) > 0 }

// Finishes reports the pipeline ending in something.
func (p Pipeline) Finishes() bool {
	for _, ps := range p.Steps {
		if ps.Kind == KindFinish {
			return true
		}
	}
	return false
}

// Strip is the steps in the order a run passes through them, and the order
// any surface drawing a run has to draw them in. The steps a run only
// sometimes takes are not in it: a division into lanes is how a large item
// implements and a remediation round is a step happening again, so each of
// them is said under the step it belongs to.
func (p Pipeline) Strip() []Stage {
	var out []Stage
	for _, ps := range p.Steps {
		if ps.Under == "" {
			out = append(out, ps.Stage())
		}
	}
	return out
}

// Place is where a stage sits on the strip, and -1 for one that sits nowhere
// in it — an ended run, and the stage a checkpoint could not name. The
// sometimes-steps report the strip step they belong to.
func (p Pipeline) Place(s Stage) int {
	ps, ok := p.At(s)
	if !ok {
		return -1
	}
	name := ps.Name
	if ps.Under != "" {
		name = ps.Under
	}
	for i, stage := range p.Strip() {
		if string(stage) == name {
			return i
		}
	}
	return -1
}

// WordingKeys is every key this pipeline's steps are instructed by, the
// shared standards sentence first. A session reads exactly these rather than
// a table of its own, so a profile with a step nobody thought of still has a
// file it can be tuned with.
func (p Pipeline) WordingKeys() []string {
	// A profile with no run instructs nothing, so it names no wording and a
	// scaffold of its directory writes no files: a `standards.md` beside a
	// pipeline that never sends it is a file whose edits do nothing.
	if len(p.Steps) == 0 {
		return nil
	}
	out := []string{WordingStandards}
	for _, ps := range p.Steps {
		switch ps.Kind {
		case KindTurn:
			out = append(out, ps.Key())
		case KindAgent:
			out = append(out, ps.Key(), ps.TaskKey())
		case KindFinish:
			if ps.Finish.Turns() {
				out = append(out, ps.Key())
			}
		}
	}
	return out
}

// BlocksFor is the substitutions a key's wording may name, which is what a
// file is checked against before a run is built on one.
func (p Pipeline) BlocksFor(key string) []string {
	for _, ps := range p.Steps {
		switch key {
		case ps.Key():
			return ps.Blocks
		case ps.TaskKey():
			if ps.Kind == KindAgent {
				return ps.Blocks
			}
		}
	}
	return nil
}

// Digest identifies the pipeline, for a checkpoint that has to say the run's
// shape moved under it. It is empty for no steps at all, so nothing that has
// never had a pipeline reads as one that lost it.
func (p Pipeline) Digest() string {
	if len(p.Steps) == 0 {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00", p.Name)
	for _, ps := range p.Steps {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%d\x00%s\x00%v\x00%v\x00",
			ps.Name, ps.Kind, ps.Access, ps.Finish, ps.Command, ps.Reads,
			ps.Back, ps.Under, ps.Solo, ps.Persona, ps.Pause, ps.Rounds)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// Archiving is this pipeline with every commit finish turned into an archive
// — the run a person asked for without a commit, and the standing answer a
// project sets. It is the same pipeline otherwise: a run without a commit is
// not a run with a stage missing, it is a run that ends another way, and the
// report it writes says where the work is instead.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
func (p Pipeline) Archiving() Pipeline { return p.archiving(FinishCommit) }

// Noteless is this pipeline with every note finish turned into an archive —
// the run in a session that has no shared notebook. A note finish spends a
// turn asking for the write-up rather than letting the code state what was
// done, which is worth a turn when there is somewhere for the writing to be
// read; where there is not, the code's own report says the same thing for
// nothing.
// See docs/capabilities/todo.md#a-profile-says-what-the-work-is.
func (p Pipeline) Noteless() Pipeline { return p.archiving(FinishNote) }

// archiving is this pipeline with one kind of finish replaced by an archive.
func (p Pipeline) archiving(from Finish) Pipeline {
	out := p
	out.Steps = make([]PipelineStep, len(p.Steps))
	copy(out.Steps, p.Steps)
	for i := range out.Steps {
		if out.Steps[i].Kind == KindFinish && out.Steps[i].Finish == from {
			out.Steps[i].Finish = FinishArchive
		}
	}
	return out
}

// Can is what the session asking for a run is able to do. Every field is a
// fact about the session and none of them is about the item.
type Can struct {
	// Changeset reports the session tracking what it changed, which is the
	// only way a run can know what its own work is.
	Changeset bool
	// Supervisor reports an agent supervisor to spawn children from.
	Supervisor bool
	// Runner reports commands being runnable here at all.
	Runner bool
	// Repo reports a git repository at the root.
	Repo bool
}

// Need is what a step wanted from the session and did not get.
type Need string

const (
	// NeedChangeset is a step that changes the tree in a session with no
	// record of what it changed.
	NeedChangeset Need = "changeset"
	// NeedSupervisor is a division into lanes with nothing to spawn writers
	// from.
	NeedSupervisor Need = "supervisor"
	// NeedRunner is a command step where no command can be run.
	NeedRunner Need = "runner"
	// NeedRepo is a commit finish outside a repository.
	NeedRepo Need = "repository"
)

// Refusal is a step the session cannot take: which step, what it wanted, and
// the clause a surface says it in. The need is named as well as said because
// each surface has its own way through — a flag here, a setting there — and
// only the surface knows what to offer.
type Refusal struct {
	Step string
	Need Need
	Why  string
}

// Refuse is the step this session cannot take, and false for a pipeline it
// can run whole. It is asked before the first step rather than at the step
// that needs the thing, because every step before that one spends a turn: a
// run that did all of them and then found it had nowhere to put the result
// has spent them for an item it leaves in progress.
//
// It is per step and not per session. "This session does not track changes"
// is the right sentence for a step that writes and the wrong one for a
// pipeline that never writes, and a read-only session can run the latter.
//
// A step that only sometimes runs asks for nothing: a division into lanes
// happens at one grade and falls back to the session's own turn where it
// cannot happen at all, so refusing every run for want of a supervisor would
// refuse the runs that never wanted one.
func (p Pipeline) Refuse(can Can) (Refusal, bool) {
	for _, ps := range p.Steps {
		if ps.When != WhenAlways {
			continue
		}
		writes := (ps.Kind == KindTurn && ps.Access == Write) || ps.Kind == KindFanOut
		switch {
		case writes && !can.Changeset:
			return Refusal{ps.Name, NeedChangeset,
				fmt.Sprintf("the %s step changes the tree and this session does not track changes, so a run could not know what it did", ps.Name)}, true
		case ps.Kind == KindFanOut && !can.Supervisor:
			return Refusal{ps.Name, NeedSupervisor,
				fmt.Sprintf("the %s step builds in lanes and this session has no agent supervisor to spawn writers from", ps.Name)}, true
		case ps.Kind == KindCommand && !can.Runner:
			return Refusal{ps.Name, NeedRunner,
				fmt.Sprintf("the %s step runs the project's checks and this session cannot run a command", ps.Name)}, true
		case ps.Kind == KindFinish && ps.Finish.Writes() && !can.Repo:
			return Refusal{ps.Name, NeedRepo,
				fmt.Sprintf("the %s step ends in a commit and there is no git repository here", ps.Name)}, true
		}
	}
	return Refusal{}, false
}

// Validate refuses a pipeline the run cannot carry out, naming the step and
// the rule. A free list makes shapes a fixed skeleton would have prevented,
// and a validator is what a free list owes in return.
//
// The pipeline shipped here is a Go literal and a test holds it to this, so
// nothing calls it at runtime yet. Whatever first reads a pipeline out of a
// file owes the person this refusal at load, with the line the step is on:
// a run that reached a step nothing can do would have spent every turn
// before it, which is the failure this note exists to stop somebody
// shipping.
func (p Pipeline) Validate() error {
	if len(p.Steps) == 0 {
		return fmt.Errorf("pipeline %q: no steps", p.Name)
	}
	if !p.Finishes() {
		return fmt.Errorf("pipeline %q: no finish step, so a run in it could never end", p.Name)
	}
	seen := map[string]bool{}
	remediation := ""
	for _, ps := range p.Steps {
		if ps.Name == "" {
			return fmt.Errorf("pipeline %q: a step has no name", p.Name)
		}
		if seen[ps.Name] {
			return fmt.Errorf("pipeline %q: two steps named %q", p.Name, ps.Name)
		}
		seen[ps.Name] = true
		if !ps.Kind.Known() {
			return fmt.Errorf("pipeline %q: step %q is of no kind this runner has (%s)", p.Name, ps.Name, strings.Join(kindWords(), ", "))
		}
		for _, rule := range ps.Pause {
			if !rule.Known() {
				return fmt.Errorf("pipeline %q: step %q pauses on %q, which is not a rule this runner has", p.Name, ps.Name, rule)
			}
		}
		if ps.Back != "" {
			if remediation != "" {
				return fmt.Errorf("pipeline %q: steps %q and %q both answer a failed verdict", p.Name, remediation, ps.Name)
			}
			remediation = ps.Name
			if _, ok := p.Step(ps.Back); !ok {
				return fmt.Errorf("pipeline %q: step %q returns to %q, which is not a step", p.Name, ps.Name, ps.Back)
			}
		}
		if ps.Under != "" {
			under, ok := p.Step(ps.Under)
			if !ok {
				return fmt.Errorf("pipeline %q: step %q is said under %q, which is not a step", p.Name, ps.Name, ps.Under)
			}
			if under.Under != "" {
				return fmt.Errorf("pipeline %q: step %q is said under %q, which is itself said under another step", p.Name, ps.Name, ps.Under)
			}
		}
		if ps.Persona != "" && ps.Kind != KindAgent {
			return fmt.Errorf("pipeline %q: step %q names the persona %q and is not a sub-agent step, so there is nobody for the persona to be", p.Name, ps.Name, ps.Persona)
		}
		if ps.Kind == KindFinish && !ps.Finish.Known() {
			return fmt.Errorf("pipeline %q: step %q ends the run in a way this runner has no answer for (%q)", p.Name, ps.Name, ps.Finish)
		}
	}
	if err := p.validateGates(); err != nil {
		return err
	}
	if err := p.validateFanOut(); err != nil {
		return err
	}
	return p.validateFinish()
}

// validateGates refuses a gate after a step that writes. A gate is where the
// person decides what gets built; one that opens after it was built is
// asking them about a tree that has already moved, and the answer they would
// have given is not an answer any more.
func (p Pipeline) validateGates() error {
	wrote := ""
	for _, ps := range p.Steps {
		if ps.Kind == KindGate && wrote != "" {
			return fmt.Errorf("pipeline %q: the %s gate comes after %s, which changes the tree; a gate decides what is built and cannot come after it was", p.Name, ps.Name, wrote)
		}
		if ps.Access == Write || ps.Kind == KindFanOut {
			wrote = ps.Name
		}
	}
	return nil
}

// validateFanOut refuses a division with nothing to integrate it. The lanes
// are written blind to each other and land as patches on one tree; without a
// turn after them to make them fit, a run would hand a tree nobody has read
// as one piece to whatever comes next.
func (p Pipeline) validateFanOut() error {
	for i, ps := range p.Steps {
		if ps.Kind != KindFanOut {
			continue
		}
		integrated := false
		for _, after := range p.Steps[i+1:] {
			if after.Kind == KindTurn && after.Access == Write {
				integrated = true
				break
			}
		}
		if !integrated {
			return fmt.Errorf("pipeline %q: the %s step divides the work into lanes and no turn after it integrates them", p.Name, ps.Name)
		}
	}
	return nil
}

// validateFinish refuses a commit in a pipeline whose turns never write.
// Nothing would have changed, so the commit would either carry somebody
// else's work or refuse at the end of every run.
func (p Pipeline) validateFinish() error {
	if p.Writes() {
		return nil
	}
	for _, ps := range p.Steps {
		if ps.Kind == KindFinish && ps.Finish == FinishCommit {
			return fmt.Errorf("pipeline %q: the %s step ends in a commit and no step in it changes the tree", p.Name, ps.Name)
		}
	}
	return nil
}

func kindWords() []string {
	out := make([]string, 0, len(Kinds()))
	for _, k := range Kinds() {
		out = append(out, string(k))
	}
	return out
}
