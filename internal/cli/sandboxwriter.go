package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/cli/doctor"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// writerBoxes is the containers a sandbox session's writers run their
// commands in, one per writer's copy of the checkout. A writer's copy is made
// under the host's temporary directory, outside the session container's one
// mount, and an engine cannot add a mount to a container that is running; so
// each writer gets a container of its own whose one mount is its copy, made
// from the same prepared image, the same profile and the same ceilings as the
// session's, and removed when the copy is (release) or when the session's own
// container is (close).
// See docs/capabilities/containment.md#a-sandbox-writer-works-in-a-container-of-its-own.
type writerBoxes struct {
	eng       sandbox.Engine
	spec      sandbox.ContainerSpec
	allowlist []string
	store     *sandbox.Store
	// said is what a writer is told its commands run under, and words what
	// the spawn card and /status say of them.
	said  commandEnvironment
	words writerCommands
	// ceiling bounds each command a writer runs, as it bounds a writer's
	// command on the host.
	ceiling time.Duration

	mu     sync.Mutex
	boxes  map[string]sandbox.Container
	closed bool
}

// newWriterBoxes is the writers' half of a sandbox session over the spec its
// own container was made from: the prepared image and the profile's network
// are the session's, and only the mount changes per writer.
func newWriterBoxes(cfg config.Config, eng sandbox.Engine, spec sandbox.ContainerSpec, store *sandbox.Store,
	profile sandbox.Profile, backgrounds bool) *writerBoxes {
	return &writerBoxes{
		eng:       eng,
		spec:      spec,
		allowlist: cfg.Sandbox.ImageAllowlist,
		store:     store,
		said: commandEnvironment{
			Mechanism:   sandboxWriterSaid,
			Profile:     string(profile),
			Network:     spec.Network,
			Ceiling:     cfg.CommandTimeout(),
			Backgrounds: backgrounds,
		},
		words:   sandboxWriterCommands(cfg, profile, spec.Network),
		ceiling: cfg.CommandTimeout(),
		boxes:   map[string]sandbox.Container{},
	}
}

// sandboxWriterSaid is what a writer in a sandbox session is told contains
// its commands.
const sandboxWriterSaid = "a disposable container of its own, whose one mount is your copy of the checkout"

// sandboxWriterCommands is what a writer's commands run under in a sandbox
// session, in the words the session's own indicator uses for its commands —
// the container sandbox and the profile — with what differs said beside
// them: the container is the writer's own, over its copy.
// See docs/capabilities/containment.md#containment-can-be-required.
func sandboxWriterCommands(cfg config.Config, profile sandbox.Profile, network bool) writerCommands {
	required := childRequiresContainment(cfg, true)
	detail := ""
	if required {
		detail = "required"
	}
	reach := "network closed"
	if network {
		reach = "network open"
	}
	detail = doctor.JoinDetail(doctor.JoinDetail(doctor.JoinDetail(detail, "its own container over its worktree"), string(profile)), reach)
	return writerCommands{state: writerContained, mechanism: sandboxMechanism, detail: detail, required: required}
}

// open answers the container a writer working in dir runs its commands in,
// made the first time it is asked for and the same one after that: a
// writer's environment is built again for its last word on a budget, and
// that is the same writer in the same copy. The engine's create is bounded
// by its own lifecycle timeout and by ctx, which is the writer's.
//
// A container that cannot be made is the writer's failure, with the engine's
// own sentence: there is nowhere else its commands may run.
func (w *writerBoxes) open(ctx context.Context, dir string) (sandbox.Container, error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return sandbox.Container{}, errors.New("the session's sandbox is closing")
	}
	if c, ok := w.boxes[dir]; ok {
		w.mu.Unlock()
		return c, nil
	}
	w.mu.Unlock()

	// Made outside the lock, so one writer's create is not a wait for every
	// other writer starting beside it. The image is the one the session's
	// container was made from and whose helper it already asked after, so
	// it is not asked again.
	spec := w.spec
	spec.Workspace = dir
	c, err := sandbox.CreateContainer(ctx, w.eng, spec, w.allowlist, w.store)
	if err != nil {
		return sandbox.Container{}, fmt.Errorf("the writer's sandbox container could not be made: %w", err)
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.destroy([]sandbox.Container{c})
		return sandbox.Container{}, errors.New("the session's sandbox is closing")
	}
	if held, ok := w.boxes[dir]; ok {
		// Another ask for the same copy made one first: keep that one, so
		// no container is made that nothing will remove.
		w.mu.Unlock()
		w.destroy([]sandbox.Container{c})
		return held, nil
	}
	w.boxes[dir] = c
	w.mu.Unlock()
	return c, nil
}

// run is a writer's command runner over its container: every command execs
// there through the helper, attached, so a cancel and the ceiling reach it
// through the stream and the second exec (containedExec), and the session's
// secrets cross by name as they do for the session's own commands.
func (w *writerBoxes) run(c sandbox.Container, dir string) func(context.Context, string) tools.ExecResult {
	return boundedRunner(func(ctx context.Context, command string) tools.ExecResult {
		argv, stop, err := containedExec(c, sandbox.HelperExec{Secrets: runner.SessionEnvNames()}, []string{"/bin/sh", "-c", command})
		if err != nil {
			return runner.WrapFailure(dir, err)
		}
		return runner.RunCaptureArgvInAttached(ctx, "", command, argv, stop)
	}, w.ceiling)
}

// release removes the containers made over a writer's copy once the copy is
// gone (subagent.Options.Removed).
func (w *writerBoxes) release(worktree string) {
	w.mu.Lock()
	var gone []sandbox.Container
	for dir, c := range w.boxes {
		if dir == worktree || strings.HasPrefix(dir, worktree+string(filepath.Separator)) {
			gone = append(gone, c)
			delete(w.boxes, dir)
		}
	}
	w.mu.Unlock()
	w.destroy(gone)
}

// close removes every writer's container still standing and refuses any
// more: the session's container is going, and the writers' go with it, on a
// quit and on a hang-up or termination alike (closeContainer).
func (w *writerBoxes) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.closed = true
	gone := make([]sandbox.Container, 0, len(w.boxes))
	for dir, c := range w.boxes {
		gone = append(gone, c)
		delete(w.boxes, dir)
	}
	w.mu.Unlock()
	w.destroy(gone)
}

// destroy removes containers side by side under one bound, so a session with
// several writers is not one engine timeout per writer on its way out. A
// removal that fails is written down rather than printed, since a writer can
// end with the screen up, and the reaper takes the container by its record.
func (w *writerBoxes) destroy(cs []sandbox.Container) {
	if len(cs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Add(1)
		go func(c sandbox.Container) {
			defer wg.Done()
			if err := sandbox.DestroyContainer(ctx, w.eng.Path, w.store, c.Record); err != nil {
				logs.Logger().Warn("a writer's sandbox container was not removed; TTL reaping will retry", "id", c.Record.ID, "err", err.Error())
			}
		}(c)
	}
	wg.Wait()
}

// releaser is release as the supervisor's hook, nil where there are no
// writers' containers to remove.
func (w *writerBoxes) releaser() func(string) {
	if w == nil {
		return nil
	}
	return w.release
}

// sandboxWriterCommandsIn is a sandbox session's writer's runner and refusal.
// The container is made only over a writer's own copy: the environment the
// supervisor builds to measure a spawn before it is admitted names the
// session's checkout instead, and is never run, so it gets the refusal and
// no container. A writer is never handed a runner on the host.
func sandboxWriterCommandsIn(ctx context.Context, w *writerBoxes, spec subagent.Spec, sessionRoot string) (func(context.Context, string) tools.ExecResult, string, error) {
	if !spec.Worktree || spec.Root == sessionRoot {
		return nil, sandboxChildRefusal, nil
	}
	c, err := w.open(ctx, spec.Root)
	if err != nil {
		return nil, "", err
	}
	return w.run(c, spec.Root), "", nil
}
