package cli

// A checkpoint is the sprint looking at the checkout between landings. Each
// lane's patch passed its own gate in its own copy, and a screen no gate
// draws can still drift over several of them, so after every Nth landing the
// sprint runs a suite the project names on the checkout itself, while it
// holds the land lock: no lane can land, carry or be seeded from the
// checkout until the suite has answered.
//
// The sprint takes no item while that is happening, and the lanes in flight
// are not stopped: each goes on until its next boundary, which asks the lock
// and so waits there. A suite that passes lets everything go on. A check that
// fails is run again, alone, by the quality runner (the batch's rule for a
// load race), and a second failure halts the sprint: the lanes in flight
// finish, nothing new is taken, and it ends blocked naming the checkpoint,
// the check and the evidence the failing run was kept as.
// See docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoCheckpointSlug names the run directory a checkpoint keeps its
// evidence in, beside the lanes' own.
const todoCheckpointSlug = "checkpoint"

// todoCheckpointCount reads a suite's own count out of a check's output
// (`94/94 scenes passed`); a check that prints none is counted as a check.
var todoCheckpointCount = regexp.MustCompile(`(\d+)/(\d+) [a-z]+ passed`)

// todoCheckpoint is a sprint's checkpoint: how often, which suite, the
// runner over the checkout, and the landings since the last one. landed is
// guarded by the land lock, running by the lanes' mutex.
type todoCheckpoint struct {
	every  int
	suite  string
	runner *quality.Runner
	// untrusted is why there is no runner: the suites are command text out
	// of a file that arrived with the clone.
	untrusted bool
	landed    int
	// running is set while the suite is on the checkout and keeps the
	// sprint from taking an item.
	running bool
}

// checkpointPlan is the checkpoint this driver was configured with, or nil
// for none.
func (d *todoDriver) checkpointPlan() *todoCheckpoint {
	if d.checkpointEvery <= 0 {
		return nil
	}
	cp := &todoCheckpoint{every: d.checkpointEvery, suite: d.checkpointSuite, untrusted: d.gate == nil}
	if d.gate != nil {
		cp.runner = &quality.Runner{Workspace: d.root, Wrap: d.gate.Wrap, WrapIn: d.gate.WrapIn, Slot: d.takeCheckSlot}
		recordGateFlakes(cp.runner, d.root, func() string { return recordedSession(d.rec) })
	}
	return cp
}

// landedOne counts a landing toward the next checkpoint. The caller holds
// the land lock.
func (s *todoLanes) landedOne() {
	if s.cp != nil {
		s.cp.landed++
	}
}

// checkpointing reports a checkpoint on the checkout, which is when the
// sprint takes no item. The caller holds the lanes' mutex.
func (s *todoLanes) checkpointing() bool {
	return s.cp != nil && s.cp.running
}

// checkpoint runs the suite when the Nth landing since the last one has
// landed. The caller holds the land lock, which is what makes the checkout
// the tree the suite is about; it is let go as the caller lets it go.
func (s *todoLanes) checkpoint(ctx context.Context) {
	cp := s.cp
	if cp == nil || cp.landed < cp.every || ctx.Err() != nil {
		return
	}
	cp.landed = 0
	s.mu.Lock()
	cp.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		cp.running = false
		s.mu.Unlock()
	}()
	name := cp.suite
	switch {
	case name == "":
		s.halted("todo.checkpoint_every is set and todo.checkpoint_suite names no suite", "checkpoint · none · not run")
		return
	case cp.untrusted:
		s.halted("the checkpoint suite "+name+" was not run: this checkout is not trusted, so the project's quality suites do not run here", "checkpoint · "+name+" · not run")
		return
	}
	s.said("checkpoint · " + name + " · running")
	cp.runner.Evidence = s.checkpointEvidence()
	res, err := cp.runner.Run(ctx, name)
	switch {
	case ctx.Err() != nil:
		s.said("")
		return
	case err != nil:
		s.halted(fmt.Sprintf("checkpoint · %s blocked — the suite could not run: %v", name, err), "checkpoint · "+name+" · not run")
		return
	case res.Verdict == quality.VerdictBlocked || res.Verdict == quality.VerdictCancelled:
		s.halted(fmt.Sprintf("checkpoint · %s blocked — the suite could not run: %s", name, res.Reason), "checkpoint · "+name+" · not run")
		return
	case res.Verdict == quality.VerdictPass:
		s.said(checkpointWords(name, res))
		return
	}
	for _, c := range res.Checks {
		if c.OK() {
			continue
		}
		why := fmt.Sprintf("checkpoint · %s blocked — check %s failed", name, c.Name)
		if c.EvidenceID != "" {
			why += " (evidence " + c.EvidenceID + ")"
		}
		s.halted(why, "checkpoint · "+name+" · "+c.Name+" failed")
		return
	}
	s.halted(fmt.Sprintf("checkpoint · %s blocked — the suite failed", name), "checkpoint · "+name+" · failed")
}

// checkpointEvidence is where a checkpoint's checks keep their whole output,
// so the id the sprint ends on can be read. A store that will not open costs
// the id and nothing else.
func (s *todoLanes) checkpointEvidence() quality.EvidenceFunc {
	dir, err := run.MakeSpool(s.root, todoCheckpointSlug)
	if err != nil {
		return nil
	}
	store, err := evidence.OpenAt(dir, todoCheckpointSlug)
	if err != nil {
		return nil
	}
	return store.Put
}

// checkpointWords is a passed checkpoint as the board and the log read it:
// `checkpoint · tui · 94/94`, with the suite's own count where a check
// printed one and the checks passed otherwise.
func checkpointWords(name string, res *quality.Result) string {
	passed, total := 0, 0
	for _, c := range res.Checks {
		if m := todoCheckpointCount.FindAllStringSubmatch(c.Output, -1); len(m) > 0 {
			last := m[len(m)-1]
			p, _ := strconv.Atoi(last[1])
			t, _ := strconv.Atoi(last[2])
			passed, total = passed+p, total+t
			continue
		}
		total++
		if c.OK() {
			passed++
		}
	}
	return fmt.Sprintf("checkpoint · %s · %d/%d", name, passed, total)
}

// said writes the checkpoint's words on the board's row and, except while it
// runs, in the log.
func (s *todoLanes) said(words string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sp.Checkpoint = words
	s.saveLocked()
	if words != "" && !strings.HasSuffix(words, " · running") {
		fmt.Fprintln(s.out, words)
	}
}

// halted stops the taking of items for why, and says it on the board and in
// the log.
func (s *todoLanes) halted(why, words string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sp.Checkpoint = words
	s.sp.Halt(why)
	s.saveLocked()
	fmt.Fprintln(s.out, "✗ "+why)
}
