package run

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rfizzle/shhh/internal/todo"
)

// A large item is built by writer children, one per lane, rather than by
// the session's own turn. The orchestrator divides the approved plan into
// lanes in a read-only turn; code checks that the lanes are few, named and
// disjoint; then each lane goes to a writer that works in its own copy of
// the tree and hands back a patch the run applies. What makes a fan-out
// safe is the same thing that makes the rest of the run safe: the model
// proposes the split, and the shape it must fit is decided here.
// See docs/capabilities/todo.md#a-large-item-is-built-in-lanes.

// Lane is one writer's share of a large item's plan.
type Lane struct {
	// Name is the short handle the orchestrator gave the lane; the child's
	// name is derived from it.
	Name string `json:"name"`
	// Paths is what the lane may touch, declared to the supervisor so two
	// lanes can never claim one file.
	Paths []string `json:"paths"`
	// Task is the lane's own instructions, as the orchestrator wrote them.
	Task string `json:"task"`
	// Agent is the writer child building the lane in the current round;
	// empty before the fan-out and once the lane's writer has reported.
	Agent string `json:"agent,omitempty"`
	// Done reports the lane's patch landed on the tree. A landed lane is
	// not yet a finished one: the patch reaches the run while the writer
	// that wrote it is still ending its turn, and what the writer says
	// about the lane arrives after.
	Done bool `json:"done"`
	// Report is the writer's final message, bounded, for the integration
	// turn and the record.
	Report string `json:"report,omitempty"`
}

// MaxLanes bounds a fan-out. A lane past the supervisor's concurrency
// queues, which costs nothing; more lanes than this is a plan being
// listed, not divided.
const MaxLanes = 4

// maxLaneName keeps a child's name inside the supervisor's limit with the
// run's prefix in front.
const maxLaneName = 16

// maxLaneReport bounds what one writer's report contributes to the
// integration prompt.
const maxLaneReport = 4000

var (
	lanePattern    = regexp.MustCompile(`(?im)^[ \t]*lane:[ \t]*(.*)$`)
	noLanesPattern = regexp.MustCompile(`(?im)^[ \t]*lanes:[ \t]*none\b`)
)

// ParseLanes reads the split turn's answer. It returns no lanes and no
// error for an answer that says `lanes: none` — the orchestrator judging
// the plan is not divisible — and an error for an answer that is not in
// the asked shape or that describes lanes the run cannot spawn safely.
func ParseLanes(text string) ([]Lane, error) {
	locs := lanePattern.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		// "none" counts only where no lane is offered: an answer that
		// quotes the rule and then lists lanes has listed lanes.
		if noLanesPattern.MatchString(text) {
			return nil, nil
		}
		return nil, fmt.Errorf("no LANE: blocks and no `lanes: none`")
	}
	var lanes []Lane
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		name := laneName(text[loc[2]:loc[3]])
		if name == "" {
			return nil, fmt.Errorf("a LANE: line has no usable name")
		}
		lane := Lane{Name: name}
		var task []string
		inTask := false
		for _, line := range strings.Split(strings.TrimPrefix(text[loc[1]:end], "\n"), "\n") {
			t := strings.TrimSpace(line)
			lower := strings.ToLower(t)
			// The keyword lines come first; once the task has begun a
			// line starting "paths:" is the task's own prose.
			switch {
			case strings.HasPrefix(lower, "paths:") && !inTask:
				for _, p := range strings.Split(t[len("paths:"):], ",") {
					if p = strings.Trim(strings.TrimSpace(p), "`"); p != "" {
						lane.Paths = append(lane.Paths, filepath.ToSlash(p))
					}
				}
			case strings.HasPrefix(lower, "task:"):
				inTask = true
				if rest := strings.TrimSpace(t[len("task:"):]); rest != "" {
					task = append(task, rest)
				}
			case inTask:
				task = append(task, t)
			}
		}
		lane.Task = strings.TrimSpace(strings.Join(task, "\n"))
		if lane.Task == "" {
			return nil, fmt.Errorf("lane %s has no task", name)
		}
		if len(lane.Paths) == 0 {
			return nil, fmt.Errorf("lane %s names no paths", name)
		}
		for _, p := range lane.Paths {
			if err := validLanePath(p); err != nil {
				return nil, fmt.Errorf("lane %s: %w", name, err)
			}
		}
		lanes = append(lanes, lane)
	}
	if len(lanes) > MaxLanes {
		return nil, fmt.Errorf("%d lanes; at most %d", len(lanes), MaxLanes)
	}
	for i := range lanes {
		for j := 0; j < i; j++ {
			if lanes[i].Name == lanes[j].Name {
				return nil, fmt.Errorf("two lanes named %s", lanes[i].Name)
			}
			for _, a := range lanes[i].Paths {
				for _, b := range lanes[j].Paths {
					if pathsOverlap(a, b) {
						return nil, fmt.Errorf("lanes %s and %s both claim %s", lanes[j].Name, lanes[i].Name, a)
					}
				}
			}
		}
	}
	return lanes, nil
}

// laneName is the lane's handle as a child-name fragment: the slug form of
// what was written, clipped.
func laneName(raw string) string {
	s := todo.Slugify(strings.Trim(strings.TrimSpace(raw), "`*_"))
	if s == "item" && !strings.Contains(strings.ToLower(raw), "item") {
		return ""
	}
	if len(s) > maxLaneName {
		s = strings.TrimRight(s[:maxLaneName], "-")
	}
	return s
}

// validLanePath refuses what the supervisor would refuse, and the backlog
// directory besides: a lane that edits item files is editing the record.
func validLanePath(p string) error {
	switch {
	case filepath.IsAbs(p), strings.HasPrefix(p, "/"):
		return fmt.Errorf("path %q is absolute", p)
	case strings.Contains(p, ".."):
		return fmt.Errorf("path %q leaves the workspace", p)
	case strings.HasPrefix(strings.TrimPrefix(p, "./"), todo.StateDir+"/"+todo.Subdir):
		return fmt.Errorf("path %q is the backlog", p)
	}
	return nil
}

// pathsOverlap is the supervisor's rule for two claims naming one file:
// each claim reduced to the literal prefix before its first wildcard, and
// a prefix that contains the other is an overlap.
func pathsOverlap(a, b string) bool {
	pa, pb := literalPrefix(a), literalPrefix(b)
	return strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)
}

func literalPrefix(p string) string {
	p = strings.TrimPrefix(strings.TrimSpace(p), "./")
	if i := strings.IndexAny(p, "*?["); i >= 0 {
		p = p[:i]
		if j := strings.LastIndex(p, "/"); j >= 0 {
			p = p[:j+1]
		} else {
			p = ""
		}
	}
	return p
}

// AllLanesDone reports every lane's patch has landed.
func (s *State) AllLanesDone() bool {
	for _, l := range s.Lanes {
		if !l.Done {
			return false
		}
	}
	return len(s.Lanes) > 0
}

// reported is a lane the run is finished with: its patch is on the tree
// and its writer has handed back what it did.
func (l Lane) reported() bool { return l.Done && l.Agent == "" }

// lanesReported reports every lane is whole. It is not AllLanesDone: a
// patch lands one event before the writer that wrote it finishes, so the
// last lane to land is not always the last lane to report, and a run that
// moved on the landing would take the integration turn with one lane's
// account of itself still in flight — a lane with no findings, in a
// prompt that says the findings are how the lanes get wired together.
func (s *State) lanesReported() bool {
	for _, l := range s.Lanes {
		if !l.reported() {
			return false
		}
	}
	return len(s.Lanes) > 0
}

// LaneByAgent finds the lane a child is building.
func (s *State) LaneByAgent(name string) (*Lane, bool) {
	if name == "" {
		return nil, false
	}
	for i := range s.Lanes {
		if s.Lanes[i].Agent == name {
			return &s.Lanes[i], true
		}
	}
	return nil, false
}

// LiveAgents names the children the run has in flight: the reviewer and
// every lane whose writer has not reported. A lane whose patch already
// landed is one of them — the writer that sent it is still ending its
// turn, and the run is waiting on what it has to say — so ending the run
// kills it rather than leaving it running on a run that is over.
func (s *State) LiveAgents() []string {
	var out []string
	if s.Reviewer != "" {
		out = append(out, s.Reviewer)
	}
	for _, l := range s.Lanes {
		if l.Agent != "" {
			out = append(out, l.Agent)
		}
	}
	return out
}

// split is the turn that divides the plan. It is read-only: the answer is
// a division of work, not work.
func (s *State) split(it todo.Item) Step {
	s.Paused = ""
	s.Stage = StageSplit
	s.Lanes = nil
	return Step{Action: ActionPrompt, Stage: StageSplit, Mode: ModePlan,
		Prompt: splitPrompt(it, s.Plan, answersBlock(s.Answers)), Shown: s.label("split into lanes")}
}

// afterSplit reads the lanes. An answer the run cannot spawn from — no
// shape, overlapping paths, too many lanes — is not a block: the item is
// fine, the division is not, and the orchestrator builds it whole, with the
// record saying why.
func (s *State) afterSplit(it todo.Item, text string) Step {
	lanes, err := ParseLanes(text)
	switch {
	case err != nil:
		return s.implementWhole(it, "lanes refused: "+err.Error())
	case len(lanes) < 2:
		return s.implementWhole(it, "the plan does not divide")
	}
	s.Lanes = lanes
	return s.fanOut()
}

// implementWhole is the large item built by the session's own turn, which
// is what a medium one always is.
func (s *State) implementWhole(it todo.Item, why string) Step {
	s.Lanes = nil
	s.Stage = StageImplement
	return Step{Action: ActionPrompt, Stage: StageImplement, Mode: ModeAuto,
		Prompt: implementPrompt(it, s.Plan, answersBlock(s.Answers), s.Wordings), Shown: s.label("implement (" + why + ")")}
}

// NoLanes is the front-end reporting it cannot fan out — no supervisor, a
// spawn refused — so the session builds the item itself.
func (s *State) NoLanes(it todo.Item, why string) Step { return s.implementWhole(it, why) }

// fanOut names a writer per lane not yet landed and hands the spawning to
// the front-end. Names carry the round so a lane spawned again after a
// dead session never reuses a name the supervisor still holds.
func (s *State) fanOut() Step {
	s.Stage = StageFanOut
	s.Fanouts++
	for i := range s.Lanes {
		if s.Lanes[i].Done {
			s.Lanes[i].Agent = ""
			continue
		}
		s.Lanes[i].Agent = fmt.Sprintf("tw%d-%s", s.Fanouts, s.Lanes[i].Name)
	}
	return Step{Action: ActionFanOut, Stage: StageFanOut, Mode: ModeAuto, Shown: s.label(fmt.Sprintf("fan-out %d: %s", s.Fanouts, strings.Join(s.laneNames(false), ", ")))}
}

func (s *State) laneNames(all bool) []string {
	var out []string
	for _, l := range s.Lanes {
		if all || !l.reported() {
			out = append(out, l.Name)
		}
	}
	return out
}

// LaneTask is a writer's task: the item, the plan, its own lane, and the
// rules a lane works under. The item goes in by content, never by path —
// a worktree of an uncommitted backlog holds no item files.
func (s *State) LaneTask(it todo.Item, lane Lane) string {
	return laneTask(it, s.Plan, lane, answersBlock(s.Answers), s.Wordings)
}

// LanePatched is the front-end reporting a lane's patch landed on the
// tree. It does not advance the run: the child's done event does, once
// its report is in.
func (s *State) LanePatched(agent string) {
	if l, ok := s.LaneByAgent(agent); ok {
		l.Done = true
	}
}

// LaneDone is the front-end reporting a writer finished. A writer that did
// not finish, or finished without its patch landing, blocks the run with
// the lane named: the other lanes' work is in the tree, and the record
// says which part is missing. The last writer to report starts the
// integration turn — not the last patch to land, which is a different
// lane whenever the two events cross.
func (s *State) LaneDone(it todo.Item, agent string, finished bool, report string) Step {
	l, ok := s.LaneByAgent(agent)
	if !ok {
		return Step{Action: ActionWait, Stage: s.Stage}
	}
	l.Report = clampText(report, maxLaneReport)
	switch {
	case !finished:
		return s.block(fmt.Sprintf("writer %s (lane %s) did not finish: %s", agent, l.Name, firstLineOf(report)))
	case !l.Done:
		return s.block(fmt.Sprintf("writer %s (lane %s) finished but its patch did not land: %s", agent, l.Name, firstLineOf(report)))
	}
	l.Agent = ""
	if !s.lanesReported() {
		return Step{Action: ActionWait, Stage: StageFanOut, Shown: s.label("lane " + l.Name + " landed; waiting on " + strings.Join(s.laneNames(false), ", "))}
	}
	return s.integrate(it)
}

// LaneFailed is the front-end ending a lane on its own evidence — a patch
// that overlapped another lane's, one that would not apply.
func (s *State) LaneFailed(agent, why string) Step {
	name := agent
	if l, ok := s.LaneByAgent(agent); ok {
		name = l.Name
	}
	return s.block(fmt.Sprintf("lane %s: %s", name, why))
}

// integrate is the session's own turn after every lane landed: make the
// lanes fit, tick the item, and hand the tree to verify.
func (s *State) integrate(it todo.Item) Step {
	s.Stage = StageImplement
	return Step{Action: ActionPrompt, Stage: StageImplement, Mode: ModeAuto,
		Prompt: integratePrompt(it, s.Plan, s.Lanes, answersBlock(s.Answers), s.Wordings), Shown: s.label("integrate the lanes")}
}

func clampText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…"
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no report"
	}
	return s
}

func splitPrompt(it todo.Item, plan, answers string) string {
	var b strings.Builder
	b.WriteString("SPLIT stage. This is a large item. Divide the approved plan into lanes that writer sub-agents can build at the same time, each in its own isolated copy of the repository. Read, do not change anything.\n\n")
	b.WriteString(itemBlock(it))
	b.WriteString("\nAPPROVED PLAN:\n" + strings.TrimSpace(plan) + "\n\n")
	if answers != "" {
		b.WriteString(answers + "\n\n")
	}
	fmt.Fprintf(&b, `The rules a lane works under, because the writers cannot see each other's work until every patch has landed:

- Each lane must build and pass its own tests against the tree AS IT IS NOW. A lane may use only what exists today, never a symbol another lane is adding.
- Lanes must not share a file. Name every path a lane will create or edit; two lanes naming one path is refused.
- Between 2 and %d lanes. If the plan needs a foundation the other steps build on, or divides into fewer than two independent parts, answer with one line `+"`lanes: none`"+` and the whole plan will be built in this session instead; a wrong split costs more than no split.
- The item file is not in a lane's copy. Do not put ticking the item's boxes in a lane; the integration turn after the lanes does that.

Answer with one LANE block per lane and nothing else that starts a line with LANE:, in this shape:

LANE: <short name, one or two words>
paths: <path>, <path>
task: <what this lane builds, which plan steps it covers, and what it must not touch — several lines are fine; the paths line comes before the task, and no line of the task may start with LANE:>
`, MaxLanes)
	return b.String()
}

func laneTask(it todo.Item, plan string, lane Lane, answers string, w Wordings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are building lane %q of a backlog item that other writers are building the rest of, in parallel, each in an isolated copy of the repository.\n\n", lane.Name)
	b.WriteString(itemBlock(it))
	b.WriteString("\nAPPROVED PLAN (for context; your lane is only part of it):\n" + strings.TrimSpace(plan) + "\n\n")
	if answers != "" {
		b.WriteString(answers + "\n\n")
	}
	b.WriteString("YOUR LANE:\n" + strings.TrimSpace(lane.Task) + "\n\n")
	b.WriteString("PATHS YOU MAY CREATE OR EDIT: " + strings.Join(lane.Paths, ", ") + "\n\n")
	b.WriteString(w.standards() + "\n\n")
	b.WriteString(`Touch only the paths listed; another lane owns everything else, and a change outside your paths will collide with theirs. Use only what exists in the tree now — the other lanes' work is not in your copy. Build and run the tests for what you changed. Do not edit the backlog item file (it is not in your copy) and do not commit.

End with a short report of what you changed and anything the integration turn must wire up. If you cannot finish inside your paths, say so plainly in the report and change nothing you cannot finish.`)
	return b.String()
}

func integratePrompt(it todo.Item, plan string, lanes []Lane, answers string, w Wordings) string {
	var b strings.Builder
	b.WriteString("INTEGRATE stage. Writer sub-agents built this item in lanes, each in an isolated copy, and every lane's patch is now applied to the tree. Make the lanes fit together and finish the item.\n\n")
	b.WriteString(itemBlock(it))
	b.WriteString("\nAPPROVED PLAN:\n" + strings.TrimSpace(plan) + "\n\n")
	if answers != "" {
		b.WriteString(answers + "\n\n")
	}
	b.WriteString("THE LANES AND THEIR REPORTS:\n")
	for _, l := range lanes {
		report := strings.TrimSpace(l.Report)
		if report == "" {
			// A lane whose patch landed before its session died has no
			// report to give; its paths are the report.
			report = "(no report survived; the lane's changes are in the paths above — read them)"
		}
		fmt.Fprintf(&b, "\n--- lane %s (%s) ---\n%s\n", l.Name, strings.Join(l.Paths, ", "), report)
	}
	b.WriteString("\n" + w.standards() + "\n\n")
	b.WriteString(`Read the tree as it now is — the lanes were written blind to each other. Wire what the reports say needs wiring, resolve anything that no longer builds, and satisfy every acceptance criterion the lanes left. As you confirm each criterion and task, tick its checkbox in the item file named above. Do not commit; the runner commits. Do not run the whole verification suite yourself; the runner runs it next.

When you are done, answer with a short summary of what you changed beyond the lanes and anything departed from in the plan and why. If the lanes cannot be made to fit, answer with one line ` + "`blocked: <why>`" + `.`)
	return b.String()
}
