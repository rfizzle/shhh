package changeset

import (
	"os/exec"
	"strings"
	"sync"
)

// Tracker answers whether git knew about a file when it was edited. It is the
// only part of the changeset that talks to git, and it is optional: outside a
// repository every answer is TrackUnknown and the store still records
// everything else.
//
// A nil *Tracker answers TrackUnknown, so a session without one calls it
// unconditionally.
type Tracker struct {
	dir  string
	repo bool

	mu sync.Mutex
}

// NewTracker returns a tracker for the repository containing dir, or one that
// only ever answers TrackUnknown when dir is not inside a work tree.
func NewTracker(dir string) *Tracker {
	if dir == "" {
		dir = "."
	}
	t := &Tracker{dir: dir}
	out, err := t.git("rev-parse", "--is-inside-work-tree")
	t.repo = err == nil && strings.TrimSpace(out) == "true"
	return t
}

// Track reports whether path is tracked right now. It is called just before
// the edit is applied, so the answer is the file's state at the time of the
// edit — a file the turn creates is untracked even after the user adds it.
func (t *Tracker) Track(path string) Tracking {
	if t == nil || !t.repo || path == "" {
		return TrackUnknown
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.git("ls-files", "--error-unmatch", "--", path); err != nil {
		return TrackUntracked
	}
	return TrackTracked
}

// Repo reports whether the tracker found a git work tree; the surfaces that
// speak about reversibility need to know the difference between "untracked"
// and "there is no repository to be tracked by".
func (t *Tracker) Repo() bool { return t != nil && t.repo }

func (t *Tracker) git(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", t.dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}
