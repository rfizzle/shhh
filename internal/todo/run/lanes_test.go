package run

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

const lanesText = `Here is the division.

LANE: rail
paths: internal/ui/components/inspector.go, internal/ui/components/inspector_test.go
task: Build the rail block (plan steps 1 and 2).
Do not touch the chat package.

LANE: command
paths: internal/ui/chat/todo.go
task: Wire the command (step 3).
`

func largeAtSplit(t *testing.T) (*State, todo.Item) {
	t.Helper()
	it := item(todo.SizeL)
	s := Start(it, "sess", "manual", 1)
	s.First(it, "")
	if step := s.Observe(it, strings.Replace(planText, "size: S", "size: L", 1)); step.Action != ActionPause {
		t.Fatalf("L should pause first: %+v", step)
	}
	if step := s.Resume(it); step.Stage != StageSplit || !strings.Contains(step.Prompt, "LANE:") || !strings.Contains(step.Prompt, "lanes: none") {
		t.Fatalf("resume = %+v", step)
	}
	return s, it
}

func TestParseLanes(t *testing.T) {
	lanes, err := ParseLanes(lanesText)
	if err != nil || len(lanes) != 2 {
		t.Fatalf("lanes = %+v, %v", lanes, err)
	}
	if lanes[0].Name != "rail" || len(lanes[0].Paths) != 2 || !strings.Contains(lanes[0].Task, "Do not touch") || lanes[1].Paths[0] != "internal/ui/chat/todo.go" {
		t.Fatalf("lanes = %+v", lanes)
	}
	if lanes, err := ParseLanes("I looked.\nlanes: none\n"); err != nil || lanes != nil {
		t.Fatalf("none = %+v, %v", lanes, err)
	}
	// Quoting the rule and then listing lanes is listing lanes.
	if lanes, err := ParseLanes("Otherwise I would say\nlanes: none\n" + lanesText); err != nil || len(lanes) != 2 {
		t.Fatalf("none then lanes = %+v, %v", lanes, err)
	}
	// A "paths:" line inside a task is prose, not a claim.
	lanes, err = ParseLanes("LANE: a\npaths: a.go\ntask: do a\npaths: leave cmd/ alone\nLANE: b\npaths: b.go\ntask: do b")
	if err != nil || len(lanes[0].Paths) != 1 || !strings.Contains(lanes[0].Task, "leave cmd/ alone") {
		t.Fatalf("paths in task = %+v, %v", lanes, err)
	}
	for name, text := range map[string]string{
		"no shape":     "just prose",
		"no paths":     "LANE: a\ntask: x\nLANE: b\npaths: b.go\ntask: y",
		"no task":      "LANE: a\npaths: a.go\nLANE: b\npaths: b.go\ntask: y",
		"overlap":      "LANE: a\npaths: pkg/a.go\ntask: x\nLANE: b\npaths: pkg/\ntask: y",
		"glob overlap": "LANE: a\npaths: pkg/*.go\ntask: x\nLANE: b\npaths: pkg/b.go\ntask: y",
		"same name":    "LANE: a\npaths: a.go\ntask: x\nLANE: a\npaths: b.go\ntask: y",
		"absolute":     "LANE: a\npaths: /etc/a\ntask: x\nLANE: b\npaths: b.go\ntask: y",
		"escapes":      "LANE: a\npaths: ../a\ntask: x\nLANE: b\npaths: b.go\ntask: y",
		"backlog":      "LANE: a\npaths: .shhh/todo/x.md\ntask: x\nLANE: b\npaths: b.go\ntask: y",
		"too many":     strings.Repeat("LANE: l\npaths: p\ntask: t\n", MaxLanes+1),
	} {
		if _, err := ParseLanes(text); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
	// A lane name is a child-name fragment: slugged and clipped.
	lanes, err = ParseLanes("LANE: The Inspector Rail Block, Really Long\npaths: a.go\ntask: x\nLANE: b\npaths: b.go\ntask: y")
	if err != nil || lanes[0].Name != "the-inspector-ra" || len(lanes[0].Name) > maxLaneName {
		t.Fatalf("name = %q, %v", lanes[0].Name, err)
	}
}

func TestRun_LargeFansOutThenIntegrates(t *testing.T) {
	s, it := largeAtSplit(t)
	step := s.Observe(it, lanesText)
	if step.Action != ActionFanOut || s.Stage != StageFanOut || s.Fanouts != 1 || len(s.Lanes) != 2 {
		t.Fatalf("after split = %+v, %+v", step, s)
	}
	if s.Lanes[0].Agent != "tw1-rail" || s.Lanes[1].Agent != "tw1-command" {
		t.Fatalf("agents = %q %q", s.Lanes[0].Agent, s.Lanes[1].Agent)
	}
	if live := s.LiveAgents(); len(live) != 2 {
		t.Fatalf("live = %v", live)
	}
	task := s.LaneTask(it, s.Lanes[0])
	for _, want := range []string{"BACKLOG ITEM x", "APPROVED PLAN", "YOUR LANE:\nBuild the rail block", "PATHS YOU MAY CREATE OR EDIT: internal/ui/components/inspector.go", "Do not edit the backlog item file"} {
		if !strings.Contains(task, want) {
			t.Errorf("lane task lacks %q", want)
		}
	}
	// A patch landing does not advance the run; the writer's report does.
	s.LanePatched("tw1-rail")
	if !s.Lanes[0].Done || s.AllLanesDone() {
		t.Fatalf("lanes = %+v", s.Lanes)
	}
	step = s.LaneDone(it, "tw1-rail", true, "Built the rail.\nWire X in the chat.")
	if step.Action != ActionWait || s.Stage != StageFanOut || s.Lanes[0].Agent != "" || !strings.Contains(step.Shown, "waiting on command") {
		t.Fatalf("first lane done = %+v", step)
	}
	if got := s.Summary(); !strings.Contains(got, "lanes 1/2 landed") {
		t.Fatalf("summary = %q", got)
	}
	// A child the run does not know is not the run's.
	if step := s.LaneDone(it, "stranger", true, "hi"); step.Action != ActionWait {
		t.Fatalf("stranger = %+v", step)
	}
	s.LanePatched("tw1-command")
	step = s.LaneDone(it, "tw1-command", true, "Wired the command.")
	if step.Action != ActionPrompt || step.Stage != StageImplement || step.Mode != ModeAuto || !s.AllLanesDone() {
		t.Fatalf("last lane done = %+v", step)
	}
	for _, want := range []string{"INTEGRATE stage", "--- lane rail (", "Wire X in the chat.", "--- lane command (", "tick its checkbox"} {
		if !strings.Contains(step.Prompt, want) {
			t.Errorf("integrate prompt lacks %q", want)
		}
	}
	if step := s.Observe(it, "Wired it all."); step.Action != ActionVerify {
		t.Fatalf("after integrate = %+v", step)
	}
	if step := s.VerifyResult(it, true, ""); step.Action != ActionReview {
		t.Fatalf("L still reviews by child: %+v", step)
	}
}

func TestRun_LaneFailuresBlockWithTheLaneNamed(t *testing.T) {
	s, it := largeAtSplit(t)
	s.Observe(it, lanesText)
	if step := s.LaneDone(it, "tw1-rail", false, "cancelled"); step.Action != ActionBlocked || !strings.Contains(s.Blocked, "lane rail") || !strings.Contains(s.Blocked, "did not finish") {
		t.Fatalf("unfinished writer = %+v %q", step, s.Blocked)
	}
	s, it = largeAtSplit(t)
	s.Observe(it, lanesText)
	if step := s.LaneDone(it, "tw1-rail", true, "no file changes were made"); step.Action != ActionBlocked || !strings.Contains(s.Blocked, "patch did not land") {
		t.Fatalf("finished without a patch = %+v %q", step, s.Blocked)
	}
	s, it = largeAtSplit(t)
	s.Observe(it, lanesText)
	if step := s.LaneFailed("tw1-command", "its patch was refused: overwrites a.go"); step.Action != ActionBlocked || !strings.Contains(s.Blocked, "lane command: its patch was refused") {
		t.Fatalf("refused patch = %+v %q", step, s.Blocked)
	}
}

func TestRun_SplitThatDoesNotDivideBuildsWhole(t *testing.T) {
	for name, text := range map[string]string{
		"none":    "lanes: none",
		"one":     "LANE: only\npaths: a.go\ntask: everything",
		"refused": "LANE: a\npaths: a.go\ntask: x\nLANE: b\npaths: a.go\ntask: y",
	} {
		s, it := largeAtSplit(t)
		step := s.Observe(it, text)
		if step.Action != ActionPrompt || step.Stage != StageImplement || step.Mode != ModeAuto || len(s.Lanes) != 0 || !strings.Contains(step.Prompt, "IMPLEMENT stage") {
			t.Errorf("%s: %+v", name, step)
		}
		if name == "refused" && !strings.Contains(step.Shown, "lanes refused") {
			t.Errorf("the label should say why: %q", step.Shown)
		}
	}
	s, it := largeAtSplit(t)
	if step := s.Observe(it, "blocked: the plan is wrong"); step.Action != ActionBlocked {
		t.Fatalf("a split may report itself blocked: %+v", step)
	}
	s, it = largeAtSplit(t)
	s.Observe(it, lanesText)
	if step := s.NoLanes(it, "no supervisor"); step.Stage != StageImplement || len(s.Lanes) != 0 || !strings.Contains(step.Shown, "no supervisor") {
		t.Fatalf("no lanes = %+v", step)
	}
}

func TestContinue_FanOutRespawnsOnlyUnlandedLanes(t *testing.T) {
	s, it := largeAtSplit(t)
	s.Observe(it, lanesText)
	s.LanePatched("tw1-rail")
	s.LaneDone(it, "tw1-rail", true, "done")
	step := s.Continue(it)
	if step.Action != ActionFanOut || s.Fanouts != 2 || s.Lanes[0].Agent != "" || s.Lanes[1].Agent != "tw2-command" {
		t.Fatalf("continue at fan-out = %+v lanes %+v", step, s.Lanes)
	}
	s.LanePatched("tw2-command")
	s.LaneDone(it, "tw2-command", true, "done")
	if step := s.Continue(it); step.Stage != StageImplement || !strings.Contains(step.Prompt, "INTEGRATE stage") {
		t.Fatalf("continue at integrate = %+v", step)
	}
	s.Stage = StageSplit
	if step := s.Continue(it); step.Stage != StageSplit || step.Mode != ModePlan {
		t.Fatalf("continue at split = %+v", step)
	}
}
