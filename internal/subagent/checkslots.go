package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/tools"
)

// DefaultCheckSlots is how many checks may run at once across the session
// when agents.check_slots is unset.
const DefaultCheckSlots = 2

// HeavyCommands are the command prefixes that take a check slot whatever the
// project declares: the builds and test runs that load a machine, which a
// writer runs whether or not the project wrote a quality config naming them.
var HeavyCommands = []string{
	"go test", "go build", "go vet",
	"make test", "make build", "make check", "make lint", "make vet",
	"cargo test", "cargo build", "npm test", "pnpm test", "yarn test", "pytest",
}

// CheckSlots is the session's throttle on checks: every child's quality gate
// run and every child command that is a check takes one of its slots before
// it runs, and so does the session's own gate. A take that finds every slot
// held waits its turn, in the order the takes arrived, and is handed a slot
// directly by the release it waited on — so a waiting child is parked, not
// polling, and nobody behind it can take the slot it was waiting for.
// See docs/capabilities/subagents.md#what-they-share.
type CheckSlots struct {
	mu      sync.Mutex
	size    int
	running int
	queue   []chan struct{}
}

// NewCheckSlots is a throttle of n slots; n <= 0 is DefaultCheckSlots.
func NewCheckSlots(n int) *CheckSlots {
	if n <= 0 {
		n = DefaultCheckSlots
	}
	return &CheckSlots{size: n}
}

// Take takes a slot, waiting while every one is held. waiting, when set, is
// told how many checks are running as the wait begins, and is called only
// when there is a wait. It answers the release that gives the slot back, and
// false where ctx ended before a slot came free. Safe on a nil throttle,
// which throttles nothing.
func (s *CheckSlots) Take(ctx context.Context, waiting func(running int)) (release func(), ok bool) {
	if s == nil {
		return func() {}, true
	}
	s.mu.Lock()
	if s.running < s.size && len(s.queue) == 0 {
		s.running++
		s.mu.Unlock()
		return s.releaser(), true
	}
	turn := make(chan struct{})
	s.queue = append(s.queue, turn)
	running := s.running
	s.mu.Unlock()
	if waiting != nil {
		waiting(running)
	}
	select {
	case <-turn:
		return s.releaser(), true
	case <-ctx.Done():
	}
	s.mu.Lock()
	for i, q := range s.queue {
		if q == turn {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			s.mu.Unlock()
			return nil, false
		}
	}
	s.mu.Unlock()
	// The slot was handed over as the wait was given up: it is passed on
	// rather than dropped, or the throttle would be one slot short for the
	// rest of the session.
	s.releaser()()
	return nil, false
}

// releaser gives one slot back, at most once: to the longest waiter where
// there is one, and to the pool otherwise.
func (s *CheckSlots) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if len(s.queue) > 0 {
				next := s.queue[0]
				s.queue = s.queue[1:]
				close(next)
				return
			}
			s.running--
		})
	}
}

// slotWaitDetail is what a child waiting for a check slot says on its lane.
func slotWaitDetail(running int) string {
	return fmt.Sprintf("waiting for a check slot (%d running)", running)
}

// isCheck reports whether a child's command line is a check: one of the
// commands the project's quality config declares, or one of the heavy ones.
// Any command in the line counts, by the leading-words reading the deny list
// makes of it, so `cd api && go test ./...` is the test run it is.
func (s *Supervisor) isCheck(command string) bool {
	if agent.DenylistMatches(HeavyCommands, command) {
		return true
	}
	if s.opts.CheckCommands == nil {
		return false
	}
	return agent.DenylistMatches(s.opts.CheckCommands(), command)
}

// takeCheckSlot is the one place a child takes a check slot: its lane reads
// that it waits for one while it does, and it is held — parked, drawn beside
// the hold's own park — until the slot is its turn.
func (s *Supervisor) takeCheckSlot(ctx context.Context, c *child) (func(), bool) {
	// A round can run several of a child's checks at once, so the child
	// reads as waiting while any of them does, not only until the first is
	// let through.
	waited := false
	release, ok := s.checks.Take(ctx, func(running int) {
		waited = true
		c.mu.Lock()
		c.slotWaiters++
		c.slotWait = running
		c.mu.Unlock()
		s.emitUpdate(c)
	})
	if waited {
		c.mu.Lock()
		c.slotWaiters--
		if c.slotWaiters == 0 {
			c.slotWait = 0
		}
		c.mu.Unlock()
		s.emitUpdate(c)
	}
	return release, ok
}

// slotStopped is the result a child's check reads when it was stopped while
// it waited for a slot, before anything ran.
const slotStopped = "stopped while waiting for a check slot; nothing ran"

// throttled puts the session's check slots in front of the child's two ways
// of running a check: a command that is one, and a quality gate run. It is
// applied to every environment a child's attempt runs in, where the
// environment is opened; ctx is that attempt's, which ends a wait for a
// slot when the attempt ends.
func (s *Supervisor) throttled(ctx context.Context, c *child, env Env) Env {
	if run := env.RunCommand; run != nil {
		env.RunCommand = func(cctx context.Context, command string) tools.ExecResult {
			if !s.isCheck(command) {
				return run(cctx, command)
			}
			release, ok := s.takeCheckSlot(cctx, c)
			if !ok {
				return tools.ExecResult{Output: slotStopped, ExitCode: -1, Outcome: tools.ExecStopped}
			}
			defer release()
			return run(cctx, command)
		}
	}
	if exec := env.Executor; exec != nil {
		env.Executor = func(name string, args json.RawMessage) (string, error) {
			if name != quality.ToolName || !gateRun(args) {
				return exec(name, args)
			}
			release, ok := s.takeCheckSlot(ctx, c)
			if !ok {
				return "", errors.New(slotStopped)
			}
			defer release()
			return exec(name, args)
		}
	}
	return env
}

// gateRun reports whether a quality gate call runs a suite rather than
// re-reading the last verdict, which takes no slot.
func gateRun(args json.RawMessage) bool {
	var a struct {
		Action string `json:"action"`
	}
	return json.Unmarshal(args, &a) == nil && a.Action == "run"
}
